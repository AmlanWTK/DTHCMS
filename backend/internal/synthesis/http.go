package synthesis

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The synthesis over HTTP (CP71).
//
// # Four routes, and the first one is the checkpoint's fourth acceptance criterion
//
// `GET /v1/visits/{id}/synthesis` **never answers 404 for a visit that exists**. A summary that has
// not been asked for, one that is queued, one that failed and one that is ready are four different
// 200s with four different state names and four different sentences — because D-15 is *"fail
// visible, never fail silent"* and a 404 is how a client ends up rendering an empty panel that says
// nothing about whether the system tried.
//
// # There is still no invoke endpoint
//
// The POST here asks for a *summary of a visit*, not for a model call: it names a visit and nothing
// else, the payload is assembled by this module from the record, and the only thing a caller
// controls is which patient. CP70's rule — that a caller must never be in charge of assembling a
// payload — is intact, and this route could not be turned into a way around it without changing
// this file.

const (
	// PermRead is reading a summary. Sensitive, and already in the catalogue since CP06: the
	// narrative is the patient's whole clinical picture in prose, which is exactly what §4.4
	// blinds registration and the pharmacist from.
	PermRead = "ai.synthesis.read"
	// PermRequest is pressing the button. Its own permission, and narrower: the exercise
	// specialist who finishes the last station before the consultation should be able to ask for
	// the summary and has no business reading it.
	PermRequest = "ai.synthesis.request"
	// PermSLA is the operational report. The queue's own read permission rather than a new one:
	// this is a report about a job kind's deadline, it contains no patient, and the person who
	// looks at it is the person already looking at the queue-health page.
	PermSLA = "ops.jobs.read"
)

// defaultSLAWindow is how far back the SLA report looks when nobody says. A day, which is one
// clinic: §7.1's promise is about a morning, and a week's average would hide the morning the
// provider was down.
const defaultSLAWindow = 24 * time.Hour

// Handlers serve the synthesis.
type Handlers struct {
	service *Service
	store   *Store
	clock   Clock
	logger  *slog.Logger
	// budget is the queue's SLA budget for this kind, in seconds, resolved at start-up from
	// `ops.job_kind`. Reported alongside the measurement so that a reader is not comparing a
	// percentile against a number they have to remember. Zero when the catalogue could not be
	// read, which the report says rather than substituting §7.1's five minutes as though it had
	// been configured.
	budget int
	// facility resolves the caller's facility. Supplied by the composition root, because this
	// module may not import the one that knows how.
	facility func(*http.Request) uuid.UUID
}

// HandlersConfig builds Handlers.
type HandlersConfig struct {
	Service  *Service
	Store    *Store
	Clock    Clock
	Logger   *slog.Logger
	Budget   int
	Facility func(*http.Request) uuid.UUID
}

// NewHandlers builds them.
func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{
		service: cfg.Service, store: cfg.Store, clock: cfg.Clock, logger: cfg.Logger,
		budget: cfg.Budget, facility: cfg.Facility,
	}
}

// MountVisit attaches the per-visit routes under /v1/visits/{id}.
func (h *Handlers) MountVisit(r chi.Router) {
	read := httpx.Permission(PermRead)
	request := httpx.Permission(PermRequest)

	r.Route("/visits/{id}/synthesis", func(v chi.Router) {
		v.Method("GET", "/", httpx.Declare(read, h.current))
		v.Method("POST", "/", httpx.Declare(request, h.request))
		v.Method("GET", "/history", httpx.Declare(read, h.history))
	})
}

// MountOps attaches the SLA report under /v1/ops.
func (h *Handlers) MountOps(r chi.Router) {
	r.Method("GET", "/ops/ai/synthesis-sla", httpx.Declare(httpx.Permission(PermSLA), h.sla))
}

func (h *Handlers) current(w http.ResponseWriter, r *http.Request) {
	visitID, ok := h.visitID(w, r)
	if !ok {
		return
	}
	view, err := h.service.Current(r.Context(), visitID, h.facilityOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *Handlers) request(w http.ResponseWriter, r *http.Request) {
	visitID, ok := h.visitID(w, r)
	if !ok {
		return
	}
	run, err := h.service.Request(r.Context(), Requesting{
		VisitID: visitID, Trigger: Manual,
		EventID: uuid.New(), Source: sourceOf(r),
	})
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	case errors.Is(err, ErrVisitNotOpen):
		// 409 and not 422: the request is well formed and the caller is entitled to make it; the
		// resource is in a state that refuses it, and the remedy is to reopen the visit rather
		// than to send different fields.
		httpx.WriteError(w, r, h.logger, errVisitClosed.WithDetail(err))
		return
	case err != nil:
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	// 202 rather than 201: nothing is ready, and a 201 with a Location a client would immediately
	// fetch and find empty is a worse lie than the honest "accepted, ask again".
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"synthesis": run})
}

func (h *Handlers) history(w http.ResponseWriter, r *http.Request) {
	visitID, ok := h.visitID(w, r)
	if !ok {
		return
	}
	runs, err := h.service.History(r.Context(), visitID, h.facilityOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (h *Handlers) sla(w http.ResponseWriter, r *http.Request) {
	window := defaultSLAWindow
	if raw := strings.TrimSpace(r.URL.Query().Get("window_seconds")); raw != "" {
		// Refused rather than clamped, for the same reason the queue's health window is: a window
		// that quietly became a day would show a reader a day's arithmetic while they believed
		// they were looking at the last hour, and every number on the page would then mean
		// something other than what they asked for.
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 300 || parsed > 2592000 {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("window_seconds",
				"The window is between five minutes and thirty days, in seconds.",
				"সময়সীমা পাঁচ মিনিট থেকে ত্রিশ দিনের মধ্যে, সেকেন্ডে দিতে হবে।"))
			return
		}
		window = time.Duration(parsed) * time.Second
	}
	now := h.clock.Now().UTC()
	measured, err := h.service.SLA(r.Context(), h.facilityOf(r), now.Add(-window), now, h.budget)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"sla": measured,
		// On the payload rather than left to the client to assume, for the same reason the queue's
		// health page carries its window: "95% met" without "in the last day" is a number that
		// means something different at midnight.
		"window_seconds": int(window.Seconds()),
		"as_of":          now,
	})
}

func (h *Handlers) visitID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("id",
			"That is not a visit id.", "এটি কোনো ভিজিট আইডি নয়।"))
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handlers) facilityOf(r *http.Request) uuid.UUID {
	if h.facility == nil {
		return uuid.Nil
	}
	return h.facility(r)
}

// errVisitClosed is this module's one addition to the error catalogue.
//
// Its own code rather than the platform's bare CONFLICT, because a client has to be able to tell
// this apart from the other conflicts it may meet on a visit: the remedy is to reopen the visit,
// and a screen that said only "this conflicts with a change someone else made" would send an
// operator looking for a change nobody made.
var errVisitClosed = errs.New("SYNTHESIS_VISIT_CLOSED", errs.KindConflict, http.StatusConflict,
	"This visit is closed, so no pre-consultation summary can be prepared for it.",
	"এই ভিজিটটি বন্ধ, তাই এর জন্য পরামর্শ-পূর্ব সারসংক্ষেপ তৈরি করা যাবে না।")

// sourceOf says how the request reached the server (§7.2). The same shape as `visit`'s: a session
// carrying a device is the station app, and anything else is the web.
func sourceOf(r *http.Request) eventstore.Source {
	principal, ok := httpx.PrincipalFrom(r.Context())
	if ok && principal.DeviceID != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}
