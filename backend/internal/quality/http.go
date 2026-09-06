package quality

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"log/slog"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The quality record over HTTP (CP63, criteria 3 and 4).
//
// # Who may read whose record
//
// **Your own: a session, and nothing else.** `GET /v1/quality/me` reads the caller's own id from
// the session, so there is no version of it that returns somebody else's work, and there is no
// patient in the response. Requiring a permission would repeat the mistake CP62 made and had to
// undo — and an operator who has to be granted something before they may see their own error
// count is an operator who will assume the count is being kept from them.
//
// **Somebody else's: `quality.read.team`**, held by the physician, QA and the administrator.
// Deliberately not `hr.performance.read`, which HR holds — see ADR-0029. The plan puts
// performance-linked pay and discipline out of scope, and a permission that hands an operator's
// error history to the department that sets pay puts it back in whatever anybody intends.
//
// **Answering a flag: `quality.flag.resolve`.**

const (
	// PermReadTeam lets a supervisor read the record of the operators they supervise.
	PermReadTeam = "quality.read.team"
	// PermResolveFlag lets a supervisor acknowledge or dismiss a retraining flag.
	PermResolveFlag = "quality.flag.resolve"
)

// Handlers serve the quality record.
type Handlers struct {
	store  *Store
	clock  interface{ Now() time.Time }
	audit  AuditSink
	notify Notifier
	logger *slog.Logger
}

// HandlersConfig builds Handlers.
type HandlersConfig struct {
	Store *Store
	Clock interface{ Now() time.Time }
	// Audit and Notify are the same two interfaces the detector holds, and for the same reason:
	// answering a flag is as much a thing that happened as raising one, and the person it is
	// about has to learn what was decided.
	Audit  AuditSink
	Notify Notifier
	Logger *slog.Logger
}

// NewHandlers builds the handlers.
func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{
		store: cfg.Store, clock: cfg.Clock,
		audit: cfg.Audit, notify: cfg.Notify, logger: cfg.Logger,
	}
}

// Mount attaches /v1/quality.
func (h *Handlers) Mount(r chi.Router) {
	team := httpx.Permission(PermReadTeam)

	r.Route("/quality", func(q chi.Router) {
		// My own record. A session and nothing more — see the note above.
		q.Method("GET", "/me", httpx.Declare(httpx.Session(), h.myRecord))
		// The vocabulary of patterns, and what each looks for. Readable by anybody with a
		// session for the same reason: an operator who can see a flag on their own record
		// should be able to read what raised it, without asking a supervisor to explain.
		q.Method("GET", "/thresholds", httpx.Declare(httpx.Session(), h.thresholds))

		// The supervisor's surface.
		q.Method("GET", "/operators", httpx.Declare(team, h.operators))
		q.Method("GET", "/operators/{id}", httpx.Declare(team, h.operatorRecord))
		q.Method("GET", "/flags", httpx.Declare(team, h.flags))
		q.Method("GET", "/flags/{id}", httpx.Declare(team, h.flag))
		q.Method("POST", "/flags/{id}/resolve",
			httpx.Declare(httpx.Permission(PermResolveFlag), h.resolveFlag))
	})
}

// defaultWindowDays is what a record covers when nobody says. Thirty, because that is the window
// the plan's own manual verification uses — "three transcription errors within 30 days".
const defaultWindowDays = 30

// maxWindowDays is a year. Beyond that the counting stops being cheap and the question stops
// being about retraining.
const maxWindowDays = 365

// who parses the caller's own ids once.
//
// `Caller` carries them as strings, because the middleware that builds it reads them from a
// session record rather than from a body. Every handler here needs them as uuids, and doing the
// parse in one place is what stops a handler quietly treating an unparseable id as the zero
// uuid — which, for a facility, would be a query across no clinic at all rather than an error.
func who(r *http.Request) (user, facility uuid.UUID, ok bool) {
	caller, present := httpx.CallerFrom(r.Context())
	if !present {
		return uuid.Nil, uuid.Nil, false
	}
	user, err := uuid.Parse(caller.UserID)
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	facility, err = uuid.Parse(caller.FacilityID)
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	return user, facility, true
}

func (h *Handlers) myRecord(w http.ResponseWriter, r *http.Request) {
	user, facility, ok := who(r)
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	from, to, err := h.window(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	record, err := h.store.Record(r.Context(), facility, user, from, to)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"record": record, "mine": true})
}

func (h *Handlers) thresholds(w http.ResponseWriter, r *http.Request) {
	thresholds, err := h.store.Thresholds(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	// The description is rendered here rather than stored, so a threshold whose numbers change
	// cannot end up with a sentence describing the old ones — and it is a pair, because this is
	// the screen an operator reads to understand the rule that measures them.
	described := make([]map[string]any, 0, len(thresholds))
	for _, threshold := range thresholds {
		en, bn := Describe(threshold)
		described = append(described, map[string]any{
			"threshold": threshold, "looks_for_en": en, "looks_for_bn": bn,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"thresholds": described})
}

func (h *Handlers) operators(w http.ResponseWriter, r *http.Request) {
	_, facility, ok := who(r)
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	from, to, err := h.window(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	// A roster by default. The list of people-with-corrections is available on request, and the
	// difference is not cosmetic: a list every row of which has at least one correction is
	// structurally an accusation, whatever it is titled.
	includeAll := strings.TrimSpace(r.URL.Query().Get("only_corrected")) == ""
	operators, err := h.store.Operators(r.Context(), facility, from, to, includeAll, 200)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"operators": operators,
		"window":    Window{From: from, To: to, Days: int(to.Sub(from).Hours() / 24)},
	})
}

func (h *Handlers) operatorRecord(w http.ResponseWriter, r *http.Request) {
	user, facility, ok := who(r)
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("id",
			"That is not a staff identifier.", "এটি কোনও কর্মীর পরিচিতি নয়।"))
		return
	}
	from, to, err := h.window(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	record, err := h.store.Record(r.Context(), facility, id, from, to)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"record": record, "mine": id == user,
	})
}

func (h *Handlers) flags(w http.ResponseWriter, r *http.Request) {
	_, facility, ok := who(r)
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	// The same window control governs this list as governs the record beside it. It used to
	// govern neither: the queue was "everything ever raised, newest hundred", so one control on
	// one screen silently scoped two of its three sections and left the third unbounded.
	from, _, err := h.window(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	openOnly := strings.TrimSpace(r.URL.Query().Get("all")) == ""
	var operator *uuid.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("operator_id")); raw != "" {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("operator_id",
				"That is not a staff identifier.", "এটি কোনও কর্মীর পরিচিতি নয়।"))
			return
		}
		operator = &id
	}
	flags, err := h.store.Flags(r.Context(), facility, operator, openOnly, 100, &from)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"flags": flags})
}

func (h *Handlers) flag(w http.ResponseWriter, r *http.Request) {
	_, facility, ok := who(r)
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	flag, err := h.store.Flag(r.Context(), id, facility)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateQuality(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"flag": flag})
}

type resolveRequest struct {
	Status     string `json:"status"`
	Resolution string `json:"resolution,omitempty"`
}

func (h *Handlers) resolveFlag(w http.ResponseWriter, r *http.Request) {
	user, facility, ok := who(r)
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	var body resolveRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	flag, err := h.store.Resolve(r.Context(), id, facility, user,
		body.Status, body.Resolution, h.clock.Now().UTC())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateQuality(err))
		return
	}
	// Recorded, and told. Both matter and for different reasons: "nobody thought this was a
	// problem" is a fact a later review needs, and the operator it is about must learn what was
	// decided from the system rather than from a colleague — a note that appeared on their device
	// and then silently vanished when somebody closed it is the same ambush arriving from the
	// other end. Neither failure loses the answer, which is already committed.
	if h.audit != nil {
		if _, err := h.audit.QualityFlagResolved(r.Context(), flag); err != nil {
			h.logger.WarnContext(r.Context(), "a quality flag was answered but not recorded on the audit trail",
				"flag_id", flag.ID.String(), "error", err.Error())
		}
	}
	if h.notify != nil {
		h.notify.PublishQualityFlag(r.Context(), flag)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"flag": flag})
}

// window reads ?days=. A window is always a whole number of days ending now, because "the last
// thirty days" is the question people actually ask and an arbitrary date range is a report rather
// than a record.
//
// **An unusable `days` is refused, not silently replaced.** The first version fell back to thirty
// on anything it could not parse and clamped a larger number without saying so, which meant a
// client asking for fourteen days and being given thirty had two windows pretending to be one —
// and every count on the screen was then a numerator over somebody else's denominator.
func (h *Handlers) window(r *http.Request) (time.Time, time.Time, error) {
	days := defaultWindowDays
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxWindowDays {
			return time.Time{}, time.Time{}, errs.ErrValidation.WithFieldIn("days",
				"Ask for between 1 and 365 days.",
				"১ থেকে ৩৬৫ দিনের মধ্যে চান।")
		}
		days = parsed
	}
	to := h.clock.Now().UTC()
	return to.AddDate(0, 0, -days), to, nil
}

func translateQuality(err error) error {
	switch {
	case errors.Is(err, ErrNoFlag):
		return errs.ErrNotFound
	case errors.Is(err, ErrFlagClosed):
		return errs.New("QUALITY_FLAG_ANSWERED", errs.KindConflict, http.StatusConflict,
			"Somebody has already answered this.",
			"এটির উত্তর আগেই কেউ দিয়েছেন।").WithDetail(err)
	case errors.Is(err, ErrReasonRequired):
		return errs.ErrValidation.WithFieldIn("resolution",
			"Say why this is not a problem. A dismissal with no reason teaches nobody anything.",
			"কেন এটি সমস্যা নয় তা লিখুন। কারণহীন খারিজ কাউকে কিছু শেখায় না।")
	case errors.Is(err, ErrUnknownStatus):
		return errs.ErrValidation.WithFieldIn("status",
			"A flag is either acknowledged or dismissed.",
			"একটি ফ্ল্যাগ হয় গ্রহণ করা হয়, নয়তো খারিজ করা হয়।")
	default:
		return errs.ErrInternal.WithDetail(err)
	}
}
