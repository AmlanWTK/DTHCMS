package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The dashboard over HTTP (CP73, §8).
//
// # Two routes, and the first one is the acceptance criterion
//
// `GET /v1/patients/{id}/dashboard` is the whole screen. Not twelve requests, not one per
// panel: the checkpoint says "one request, not twelve", and it says so because a clinic's
// shared connection turns twelve round trips into a second and a half of spinner in front of
// a patient who is already sitting down.
//
// `POST /v1/patients/{id}/dashboard/suggestions/{ref}/decision` is what the physician does
// with one of the drafts. It is a write and it is small, and it is deliberately *not* folded
// into the read: a screen that had to re-read everything to record a rejection would make the
// cheapest interaction on the page the most expensive request.
//
// # The permission is `patient.read.clinical`, and it is sensitive
//
// The dashboard is the patient's whole clinical picture on one screen. §4.4 blinds
// registration and the pharmacist from exactly that, and the route's own guard is what makes
// it true before a single query runs. The panels *within* the response are filtered again,
// against the same subject, by the service and then by the serialiser — three layers, which
// is what the access model asks for and is not redundancy: the route knows nothing about the
// patient, the service knows nothing about the wire format, and the serialiser is the only
// one that fails closed on a field somebody adds next year.
//
// # Why the response is serialised through `rbac.Marshal`
//
// This is the first production use of CP20's serialiser, and it earns its place here more
// than anywhere else in the system: this is the widest clinical payload in the application,
// and it is the one a future checkpoint will add a field to. The serialiser is
// default-restrictive — a field whose name looks clinical and carries no `visible` tag makes
// the whole type unserialisable — so the failure mode for a forgotten tag is a 500 in a test
// rather than a diagnosis on a pharmacist's screen.

const (
	// PermRead is the whole screen. Sensitive, and already in the catalogue since CP06.
	PermRead = "patient.read.clinical"
	// PermDecide is answering one of the AI's drafts. Its own permission — narrower than
	// reading — because agreeing with a machine's proposal about a patient is an act and not
	// a look, and §7.2's roster has nine more agents coming that will want the same gate.
	PermDecide = "ai.suggestion.approve"
)

// AuditRecorder is how a dashboard view reaches the security trail.
//
// The same interface shape `patient` declares, for the same reason: this module may not
// import `audit`, and `cmd/api` owns the bridge. It is deliberately the *same kind of entry*
// — `patient.viewed` — rather than a new one. A review asking "who opened this patient's
// record" must get one answer, and a second kind would mean a query that missed half the
// openings until somebody remembered to add it. What distinguishes a dashboard view from a
// timeline view is the `by` field, which already carries "timeline" for CP37's read.
type AuditRecorder interface {
	RecordDashboardView(ctx context.Context, entry AccessEntry) error
}

// AccessEntry is one look at a patient's dashboard.
//
// No clinical content, ever. The trail is read by administrators who may hold no clinical
// permission at all, and a line saying which diagnoses were on screen would put the record
// into a table that exists to police access to it.
type AccessEntry struct {
	ActorID    uuid.UUID
	ActorCode  string
	ActorRole  string
	FacilityID uuid.UUID
	PatientID  uuid.UUID
	// Basis is NORMAL or BREAK_GLASS. On the entry because "who read this record" and "on
	// what authority" are the same question asked twice, and CP22's break-glass rows are
	// about the door rather than about what was read through it.
	Basis string
	At    time.Time
}

// Handlers serve the dashboard.
type Handlers struct {
	service *Service
	logger  *slog.Logger
	audit   AuditRecorder
}

// HandlersConfig builds them.
type HandlersConfig struct {
	Service *Service
	Logger  *slog.Logger
	Audit   AuditRecorder
}

// NewHandlers builds them.
func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{service: cfg.Service, logger: cfg.Logger, audit: cfg.Audit}
}

// MountPatient attaches the routes under /v1/patients/{id}.
func (h *Handlers) MountPatient(p chi.Router) {
	p.Method("GET", "/{id}/dashboard", httpx.Declare(httpx.Permission(PermRead), h.dashboard))
	p.Method("POST", "/{id}/dashboard/suggestions/{ref}/decision",
		httpx.Declare(httpx.Permission(PermDecide), h.decide))
}

func (h *Handlers) dashboard(w http.ResponseWriter, r *http.Request) {
	patientID, ok := h.uuidParam(w, r, "id", "That is not a patient id.", "এটি কোনো রোগী আইডি নয়।")
	if !ok {
		return
	}
	// The subject the route guard resolved, and **not** `eventstore.ActorFrom`.
	//
	// That distinction is worth stating because several per-patient read endpoints in this
	// system get it the other way round, and the consequence is not small. `ActorFrom` builds
	// a *write* envelope, and D-46 requires one to name the device it came from — so a handler
	// that reaches for it on a read refuses every browser with `DEVICE_REQUIRED`, because a
	// browser has no device identity and will not have one until D-71 is decided (ADR-0021).
	//
	// This screen is a web screen. A dashboard that only worked from a tablet would be the
	// physician's own surface refusing the physician. Nothing here appends an event, so
	// nothing here needs an actor: the caller's facility comes off the subject the guard
	// already verified.
	subject, ok := rbac.SubjectFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrForbidden)
		return
	}
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}

	req := Request{
		PatientID: patientID, FacilityID: subject.FacilityID, Subject: subject,
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("visit_id")); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("visit_id",
				"That is not a visit id.", "এটি কোনো ভিজিট আইডি নয়।"))
			return
		}
		req.VisitID = parsed
	}

	view, err := h.service.Assemble(r.Context(), req)
	if err != nil {
		if errors.Is(err, ErrNoSuchPatient) {
			httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
			return
		}
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}

	// Audited as a record opening (§4.5). A dashboard *is* the record in one response, which
	// is what a bulk read looks like from the outside — and access to a clinical record is an
	// auditable event whether or not anything was changed.
	//
	// Recorded **after** the assembly and before the response, so that the entry names the
	// basis the read actually happened on. A break-glass banner the physician saw and a trail
	// line saying the same thing are the two halves of one guarantee.
	h.record(r, AccessEntry{
		ActorID: subject.UserID, ActorCode: caller.Code, ActorRole: string(subject.ActiveRole),
		FacilityID: subject.FacilityID, PatientID: patientID,
		Basis: string(view.Access.Basis), At: view.AsOf,
	})

	// The serialiser, and its refusal is not swallowed. A type that fails `planFor` is a
	// programming error — a clinical field somebody added without declaring who may see it —
	// and answering 500 is the correct outcome: the alternative is shipping the untagged
	// field to whoever asked.
	body, err := rbac.Marshal(subject, view)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, json.RawMessage(body))
}

// decisionRequest is what the right panel's three buttons send.
type decisionRequest struct {
	EventID  string `json:"event_id"`
	VisitID  string `json:"visit_id"`
	Decision string `json:"decision"`
	Edited   string `json:"edited,omitempty"`
	Note     string `json:"note,omitempty"`
}

func (h *Handlers) decide(w http.ResponseWriter, r *http.Request) {
	patientID, ok := h.uuidParam(w, r, "id", "That is not a patient id.", "এটি কোনো রোগী আইডি নয়।")
	if !ok {
		return
	}
	// The write path, where an actor *is* required: a decision is an event in the clinical
	// ledger, and D-46 says a clinical write names the device it was made on. A browser has no
	// device identity today, so this refuses with `DEVICE_REQUIRED` from the web — the same
	// wall CP32's registration desk met, unblocked by the same decision (D-71, ADR-0021).
	// Inheriting it is deliberate: inventing a way round it here would be deciding D-71 in a
	// handler.
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateActor(err))
		return
	}

	var body decisionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("decision",
			"Send the decision as JSON.", "সিদ্ধান্তটি JSON হিসেবে পাঠান।"))
		return
	}

	visitID, err := uuid.Parse(strings.TrimSpace(body.VisitID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("visit_id",
			"Say which visit this decision is about.", "এই সিদ্ধান্ত কোন ভিজিট সম্পর্কে তা বলুন।"))
		return
	}

	req := Deciding{
		VisitID: visitID,
		// The patient from the path, checked against the visit's own inside the service. Two
		// identifiers naming one thing is usually a smell; here it is the check that stops a
		// decision being written against a visit belonging to somebody else, which a caller
		// holding one valid visit id could otherwise do by changing the path.
		PatientID: patientID,
		// The reference is a path segment rather than a body field, so that the thing being
		// decided is in the URL a log line shows. A decision whose subject is only visible by
		// re-reading a body is a decision an incident review cannot follow.
		Ref:        strings.TrimSpace(chi.URLParam(r, "ref")),
		Kind:       DecisionKind(strings.ToUpper(strings.TrimSpace(body.Decision))),
		Edited:     body.Edited,
		Note:       body.Note,
		FacilityID: actor.FacilityID(),
		Source:     sourceOf(r),
	}
	if raw := strings.TrimSpace(body.EventID); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
				"That is not an event id.", "এটি কোনো ইভেন্ট আইডি নয়।"))
			return
		}
		req.EventID = parsed
	}

	decision, err := h.service.Decide(r.Context(), req)
	switch {
	case errors.Is(err, ErrUnknownDecision):
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("decision",
			"A decision is accept, edit or reject.",
			"সিদ্ধান্ত হবে গ্রহণ, সম্পাদনা বা প্রত্যাখ্যান।"))
		return
	case errors.Is(err, ErrEditNeedsText):
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("edited",
			"An edited suggestion carries the wording you changed it to.",
			"সম্পাদিত পরামর্শের সঙ্গে আপনার পরিবর্তিত লেখাটিও পাঠাতে হবে।"))
		return
	case errors.Is(err, ErrNoSuchPatient):
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	case errors.Is(err, ErrNoSuchSuggestion):
		httpx.WriteError(w, r, h.logger, errStaleSuggestion.WithDetail(err))
		return
	case errors.Is(err, ErrVisitClosed):
		httpx.WriteError(w, r, h.logger, errVisitClosed.WithDetail(err))
		return
	case err != nil:
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"decision": decision})
}

// errStaleSuggestion is a decision about a summary that has been replaced.
//
// Its own code rather than the platform's bare conflict, because the remedy is specific and a
// screen has to be able to say it: reload the panel. A generic "this conflicts with a change
// someone else made" would send a physician looking for a colleague who changed nothing — the
// summary re-ran, which is the system working.
var errStaleSuggestion = errs.New("DASHBOARD_SUGGESTION_STALE", errs.KindConflict, http.StatusConflict,
	"The summary has been prepared again since this panel was drawn. Reload it and decide on the current suggestions.",
	"এই প্যানেল দেখানোর পর সারসংক্ষেপটি আবার তৈরি হয়েছে। পুনরায় লোড করে বর্তমান পরামর্শগুলোর ওপর সিদ্ধান্ত নিন।")

// errVisitClosed refuses a decision on a consultation that is over.
var errVisitClosed = errs.New("DASHBOARD_VISIT_CLOSED", errs.KindConflict, http.StatusConflict,
	"This visit is closed, so its draft suggestions can no longer be answered.",
	"এই ভিজিটটি বন্ধ, তাই এর খসড়া পরামর্শগুলোতে আর সিদ্ধান্ত দেওয়া যাবে না।")

// translateActor turns the write path's identity failures into the sentences a client can act
// on. `DEVICE_REQUIRED` is the one that matters: "you must be signed in again" would send a
// physician to re-authenticate against a wall that has nothing to do with their session.
func translateActor(err error) error {
	switch {
	case errors.Is(err, eventstore.ErrNoDevice):
		return errs.ErrDeviceRequired
	case errors.Is(err, eventstore.ErrNoRole):
		return errs.ErrForbidden.WithDetail(err)
	}
	return errs.ErrUnauthenticated
}

func (h *Handlers) uuidParam(w http.ResponseWriter, r *http.Request, name, en, bn string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn(name, en, bn))
		return uuid.Nil, false
	}
	return id, true
}

// record writes the audit entry, and never fails the request for it.
//
// A physician who cannot open a patient because the audit table is busy is a worse outcome
// than an audit line that is late — but a *missing* line is a hole in the trail, so it is
// logged loudly rather than swallowed. The same trade `patient.recordAccess` makes, and the
// same reasoning.
func (h *Handlers) record(r *http.Request, entry AccessEntry) {
	if h.audit == nil {
		return
	}
	if err := h.audit.RecordDashboardView(r.Context(), entry); err != nil {
		h.logger.ErrorContext(r.Context(), "a dashboard view was not audited",
			"actor", entry.ActorCode, "basis", entry.Basis, "error", err)
	}
}

// sourceOf says how the request reached the server (§7.2). The same shape as `visit`'s: a
// session carrying a device is the station app, and anything else is the web.
func sourceOf(r *http.Request) eventstore.Source {
	principal, ok := httpx.PrincipalFrom(r.Context())
	if ok && principal.DeviceID != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}
