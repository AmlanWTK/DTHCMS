package counseling

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Counselling sessions over HTTP (CP56, §5.3).
//
// # Why there is no endpoint that ticks a list
//
// Criterion 1 — every tick individually attributed — is only as strong as the smallest thing a
// client can send. One request per item is more traffic and it is the point: a `PUT /ticks`
// taking seven codes would attribute seven acts to one press, and §5.4's spot-questioning
// ("who told you about injection sites?") would have no answer.
//
// The cost is paid where it is cheapest. Each tick is a small request a phone can retry, each
// carries a client-generated event id so a lost reply does not double-tick, and the offline
// queue (CP64–CP67) already has exactly this shape to replay.
//
// # Why reading a session is a different permission from ticking one
//
// The physician's panel (CP57) and the traffic board read sessions and never tick. A panel that
// needed `counseling.tick` would be a physician's screen carrying the right to write on
// somebody else's checklist, which is the sort of grant nobody notices until it is used.

const (
	// PermTick is a counsellor's. It exists in the catalogue from CP15.
	PermTick = "counseling.tick"
	// PermSessionRead is the physician's panel and the board (CP56).
	PermSessionRead = "counseling.session.read"
)

// MountSessions attaches the floor surface under /v1/counseling.
//
// Separate from Mount because the two halves are different products: authoring is a desk
// activity done by one physician now and then, and ticking is what twelve stations do all day.
func (h *Handlers) MountSessions(c chi.Router) {
	read := httpx.Permission(PermSessionRead)
	tick := httpx.Permission(PermTick)

	c.Route("/sessions", func(s chi.Router) {
		// Starting is a tick permission, not a read one: opening a checklist for a patient is
		// the first act of counselling them.
		s.Method("POST", "/", httpx.Declare(tick, h.startSession))

		s.Route("/{sessionId}", func(one chi.Router) {
			one.Method("GET", "/", httpx.Declare(read, h.session))
			// One item, one request. See the note above.
			one.Method("POST", "/ticks", httpx.Declare(tick, h.tick))
			one.Method("POST", "/unticks", httpx.Declare(tick, h.untick))
			one.Method("POST", "/complete", httpx.Declare(tick, h.completeSession))
		})
	})

	// What a visit has been walked through, for the physician's panel and the board.
	c.Method("GET", "/visits/{visitId}/sessions", httpx.Declare(read, h.sessionsForVisit))

	// Which checklists this visit calls for. Either permission: a counsellor asks it before
	// starting, and the physician's panel (CP57) has to ask the same question to explain a gate
	// refusal — a panel that could see what a visit *was* walked through but not what it
	// *should have been* could not say why the patient was held.
	c.Method("GET", "/visits/{visitId}/checklists",
		httpx.Declare(httpx.Permission(PermTick, PermSessionRead), h.checklistsForVisit))
}

func (h *Handlers) checklistsForVisit(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "visitId")
	if !ok {
		return
	}
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	facility, err := uuid.Parse(principal.FacilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated.WithDetail(err))
		return
	}

	if h.visits == nil {
		// A deployment that mounted this surface without a way to resolve a visit is
		// misconfigured, and answering "no checklists" would hide that behind an empty screen.
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(
			errors.New("counseling: no visit lookup is configured")))
		return
	}

	// The visit says who the patient is. Read through `visit` rather than trusting a patient id
	// in the query string: a counsellor's phone asking about a visit must not be able to ask
	// about somebody else's conditions by changing one parameter.
	patient, err := h.visits.PatientOf(r.Context(), id, facility)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSession(err))
		return
	}

	checklists, err := h.store.Suggest(r.Context(), id, patient, h.conditions)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"checklists": checklists})
}

type startSessionRequest struct {
	EventID    string `json:"event_id,omitempty"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id"`
	TemplateID string `json:"template_id"`
}

func (h *Handlers) startSession(w http.ResponseWriter, r *http.Request) {
	var body startSessionRequest
	if !h.decode(w, r, &body) {
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}
	patient, ok := h.bodyUUID(w, r, body.PatientID, "patient_id")
	if !ok {
		return
	}
	visit, ok := h.bodyUUID(w, r, body.VisitID, "visit_id")
	if !ok {
		return
	}
	template, ok := h.bodyUUID(w, r, body.TemplateID, "template_id")
	if !ok {
		return
	}

	session, err := h.sessions.Start(r.Context(), StartSession{
		EventID: eventID, PatientID: patient, VisitID: visit, TemplateID: template,
		LedgerSource: sessionSourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSession(err))
		return
	}
	// 200 rather than 201 when it was already open, because it was: a phone that lost the
	// reply and pressed start again is looking at the session it already has.
	status := http.StatusCreated
	if len(session.Ticks) > 0 || session.Complete() {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, map[string]any{"session": session})
}

func (h *Handlers) session(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "sessionId")
	if !ok {
		return
	}
	session, err := h.store.Session(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSession(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (h *Handlers) sessionsForVisit(w http.ResponseWriter, r *http.Request) {
	visit, ok := h.uuidParam(w, r, "visitId")
	if !ok {
		return
	}
	sessions, err := h.store.SessionsForVisit(r.Context(), visit)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSession(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

type tickRequest struct {
	EventID  string `json:"event_id,omitempty"`
	ItemCode string `json:"item_code"`
	Note     string `json:"note,omitempty"`
}

func (h *Handlers) tick(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "sessionId")
	if !ok {
		return
	}
	var body tickRequest
	if !h.decode(w, r, &body) {
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}

	session, err := h.sessions.Tick(r.Context(), TickItem{
		EventID: eventID, SessionID: id, ItemCode: body.ItemCode, Note: body.Note,
		LedgerSource: sessionSourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSession(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"session": session})
}

type untickRequest struct {
	EventID  string `json:"event_id,omitempty"`
	ItemCode string `json:"item_code"`
	Reason   string `json:"reason"`
}

func (h *Handlers) untick(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "sessionId")
	if !ok {
		return
	}
	var body untickRequest
	if !h.decode(w, r, &body) {
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}

	session, err := h.sessions.Untick(r.Context(), UntickItem{
		EventID: eventID, SessionID: id, ItemCode: body.ItemCode, Reason: body.Reason,
		LedgerSource: sessionSourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSession(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"session": session})
}

type completeRequest struct {
	EventID string `json:"event_id,omitempty"`
}

func (h *Handlers) completeSession(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "sessionId")
	if !ok {
		return
	}
	var body completeRequest
	if !h.decode(w, r, &body) {
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}

	session, err := h.sessions.Complete(r.Context(), eventID, id, sessionSourceOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSession(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"session": session})
}

// --- the small shared pieces ---

func (h *Handlers) eventID(w http.ResponseWriter, r *http.Request, raw string) (uuid.UUID, bool) {
	if strings.TrimSpace(raw) == "" {
		return uuid.New(), true
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
			"That is not an event id.", "এটি বৈধ ইভেন্ট আইডি নয়।"))
		return uuid.Nil, false
	}
	return parsed, true
}

func (h *Handlers) bodyUUID(w http.ResponseWriter, r *http.Request,
	raw, field string) (uuid.UUID, bool) {

	parsed, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn(field,
			"That is not an identifier.", "এটি বৈধ আইডি নয়।"))
		return uuid.Nil, false
	}
	return parsed, true
}

func sessionSourceOf(r *http.Request) eventstore.Source {
	if strings.TrimSpace(r.Header.Get("X-DTHCMS-Device")) != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}

// The three conflicts this surface can answer with, each with its own code.
//
// They arrive at a phone as the same 409 otherwise, and a client cannot branch on a message —
// only on a code. The difference matters on the floor: "somebody already covered that" is a
// colleague to talk to, "this session is finished" is a session to reopen or a new one to
// start, and "that item is not ticked" is a screen that has drifted from the record. A screen
// that had to guess which of the three it met would guess wrong in front of a patient.
var (
	errAlreadyCovered = errs.New("COUNSELING_ITEM_ALREADY_COVERED", errs.KindConflict,
		http.StatusConflict,
		"Somebody has already covered that item. Open the session again to see who.",
		"এই বিষয়টি আগেই কেউ কভার করেছেন। কে করেছেন দেখতে সেশনটি আবার খুলুন।")

	errSessionFinished = errs.New("COUNSELING_SESSION_FINISHED", errs.KindConflict,
		http.StatusConflict,
		"This counselling session is finished.",
		"এই কাউন্সেলিং সেশনটি শেষ হয়ে গেছে।")

	errItemNotTicked = errs.New("COUNSELING_ITEM_NOT_TICKED", errs.KindConflict,
		http.StatusConflict,
		"That item is not ticked.",
		"এই বিষয়টিতে টিক দেওয়া নেই।")

	errAlreadyOverridden = errs.New("COUNSELING_GATE_ALREADY_OVERRIDDEN", errs.KindConflict,
		http.StatusConflict,
		"Somebody has already let this patient through, with a reason.",
		"একজন কারণ লিখে এই রোগীকে আগেই পার করিয়ে দিয়েছেন।")
)

// translateSession turns the session refusals into answers a counsellor can act on.
func translateSession(err error) error {
	switch {
	case errors.Is(err, ErrNoSession):
		return errs.ErrNotFound
	case errors.Is(err, ErrNoVisit):
		// A visit that does not exist is a 404, not a 500. Same answer as an id the caller
		// may not see, which is deliberate: distinguishing them is a way to learn which
		// visits exist.
		return errs.ErrNotFound
	case errors.Is(err, ErrSessionComplete):
		// 409: the request is well-formed and allowed; the session moved on. A counsellor who
		// meant to add something to a closed session is told it is closed rather than told
		// they are wrong.
		return errSessionFinished.WithDetail(err)
	case errors.Is(err, ErrNothingPublished):
		return errs.ErrValidation.WithFieldIn("template_id",
			"That checklist has no published version yet.",
			"ওই তালিকার কোনো প্রকাশিত সংস্করণ এখনো নেই।")
	case errors.Is(err, ErrUnknownItem):
		return errs.ErrValidation.WithFieldIn("item_code",
			"That item is not on this checklist. Reload it.",
			"এই তালিকায় ওই বিষয়টি নেই। তালিকাটি আবার লোড করুন।")
	case errors.Is(err, ErrNotTicked):
		return errItemNotTicked.WithDetail(err)
	case errors.Is(err, ErrAlreadyTicked):
		// 409, and deliberately not a silent success. A phone that retried a tick it already
		// made — same event id — was answered with the session; this is a *different* press on
		// an item somebody has covered, and answering 200 would quietly rewrite who covered it.
		// The message says to open the session again, because that is where the name is.
		return errAlreadyCovered.WithDetail(err)
	case errors.Is(err, ErrAlreadyOverridden):
		return errAlreadyOverridden.WithDetail(err)
	case errors.Is(err, ErrNothingToOverride):
		// 422 rather than a quiet success. An override recorded on a visit the gate is not
		// holding is a row that makes the rate view lie about how often this happens.
		return errs.ErrValidation.WithFieldIn("reason",
			"Nothing is outstanding on this visit; there is nothing to override.",
			"এই ভিজিটে বাকি কিছু নেই; কিছু উপেক্ষা করার নেই।")
	case errors.Is(err, ErrOverrideReasonRequired):
		return errs.ErrValidation.WithFieldIn("reason",
			"Say why this patient is going through without the counselling.",
			"কাউন্সেলিং ছাড়া রোগীকে কেন পাঠানো হচ্ছে তা লিখুন।")
	case errors.Is(err, ErrReasonRequired):
		return errs.ErrValidation.WithFieldIn("reason",
			"Say why the tick is being taken back.",
			"টিক কেন তুলে নেওয়া হচ্ছে তা লিখুন।")
	case errors.Is(err, eventstore.ErrNoDevice):
		return errs.ErrDeviceRequired
	default:
		return translate(err)
	}
}

// --- the gate (CP57) ---

// PermOverride lets somebody send a patient past the counselling gate, in their own name.
const PermOverride = "counseling.gate.override"

// MountGate attaches the gate surface under /v1/counseling.
//
// Reading the gate is `counseling.session.read`: the physician's panel, the counsellor's phone
// and the board all need to know whether a patient is held and why, and none of them writes.
// Overriding is its own permission, its own event and its own audit entry.
func (h *Handlers) MountGate(c chi.Router) {
	read := httpx.Permission(PermSessionRead)
	override := httpx.Permission(PermOverride)

	c.Method("GET", "/visits/{visitId}/gate", httpx.Declare(read, h.gate))
	c.Method("POST", "/visits/{visitId}/gate/override", httpx.Declare(override, h.overrideGate))

	// The rate view. The plan's mitigation for clinic-floor friction is the valve *plus*
	// override-rate monitoring, and monitoring nobody can read is a plan on paper.
	c.Method("GET", "/gate/overrides", httpx.Declare(httpx.Permission(PermQAReview), h.overrides))
}

// PermQAReview is quality's own permission. Named here rather than imported because the QA
// console is a later checkpoint and this package should not depend on it existing.
const PermQAReview = "qa.review"

func (h *Handlers) gate(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "visitId")
	if !ok {
		return
	}
	status, err := h.store.Gate(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSession(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"gate": status})
}

type overrideRequest struct {
	EventID string `json:"event_id,omitempty"`
	Reason  string `json:"reason"`
}

func (h *Handlers) overrideGate(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "visitId")
	if !ok {
		return
	}
	var body overrideRequest
	if !h.decode(w, r, &body) {
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	facility, err := uuid.Parse(principal.FacilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated.WithDetail(err))
		return
	}
	if h.visits == nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(
			errors.New("counseling: no visit lookup is configured")))
		return
	}
	patient, err := h.visits.PatientOf(r.Context(), id, facility)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSession(err))
		return
	}

	status, err := h.sessions.GrantOverride(r.Context(), eventID, id, patient,
		body.Reason, sessionSourceOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSession(err))
		return
	}

	// Audited, and after the write rather than before: an audit entry for an override that then
	// failed is a log saying somebody did something they did not do. The entry is what the
	// person reviewing override rates reads beside the numbers.
	if h.audit != nil && status.Override != nil {
		user, _ := uuid.Parse(principal.UserID)
		if err := h.audit.GateOverridden(r.Context(), GateOverride{
			FacilityID: facility, ActorID: user,
			ActorCode: principal.Code, ActorRole: principal.Role,
			VisitID: id, PatientID: patient,
			Reason: status.Override.Reason, Missing: len(status.Override.MissingAtGrant),
		}); err != nil {
			h.logger.ErrorContext(r.Context(), "recording a counselling gate override",
				"error", err, "visit", id)
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"gate": status})
}

func (h *Handlers) overrides(w http.ResponseWriter, r *http.Request) {
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	facility, err := uuid.Parse(principal.FacilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated.WithDetail(err))
		return
	}

	// Whole days, half-open. A window that ended "now" would exclude the override granted a
	// minute ago — which is the one somebody is asking about.
	to := h.clock.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
	from := to.AddDate(0, 0, -7)
	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		parsed, perr := time.Parse("2006-01-02", raw)
		if perr != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("from",
				"That is not a date.", "এটি বৈধ তারিখ নয়।"))
			return
		}
		from = parsed.UTC()
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		parsed, perr := time.Parse("2006-01-02", raw)
		if perr != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("to",
				"That is not a date.", "এটি বৈধ তারিখ নয়।"))
			return
		}
		to = parsed.UTC().AddDate(0, 0, 1)
	}

	list, err := h.store.Overrides(r.Context(), facility, from, to)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"from": from.Format("2006-01-02"), "to": to.AddDate(0, 0, -1).Format("2006-01-02"),
		"overrides": list,
	})
}
