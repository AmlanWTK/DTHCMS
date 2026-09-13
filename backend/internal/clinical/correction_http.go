package clinical

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The correction workflow over HTTP (CP62, §4.3, [R-04]).
//
// # Who may do what, and why it is two permissions rather than one
//
// **Flagging** needs `observation.correct.request`, which the plan gives to the physician, QA and
// senior operators: saying "that number looks wrong" is a clinical judgement and it must not
// require the authority to change anybody's work.
//
// **Answering** needs neither, and that is deliberate: the request was routed to the person who
// typed the value, and asking them to hold a permission to fix their own mistake would mean the
// mistakes stay. What needs a permission is answering *somebody else's* request —
// `observation.correct.approve` — and that path writes a different event, because an operator's
// quality record must not read a supervisor's fix as though they had put it right themselves.
//
// The route therefore admits either permission and the handler decides which act it was, from
// the caller's own permissions rather than from anything in the body.

const (
	// PermCorrectionRequest lets somebody say a value is wrong.
	PermCorrectionRequest = "observation.correct.request"
	// PermCorrectionApprove lets somebody correct a value they did not record.
	PermCorrectionApprove = "observation.correct.approve"
)

// answerPermissions is what the answering routes admit.
//
// It is deliberately not `observation.read.values`, which was the first thing tried. A request is
// routed to *whoever typed the value*, and the field worker records values without holding the
// read permission at all (CP15's seed; a field worker fills a form and does not browse the
// record). A route guarded on the read permission therefore refused exactly the operator the
// request was addressed to, and their mistakes would have stayed on the record forever with a
// 403 in front of the only person who could fix them.
//
// So the guard is "anybody who records values at all, or a supervisor" — enough to keep HR, the
// pharmacist and registration out of the workflow entirely, and no more. The decision that
// actually matters is made in the service, against the name on the request.
var answerPermissions = append([]string{PermObservationRead, PermCorrectionApprove}, writePermissions...)

// MountCorrections attaches /v1/corrections and the flag on one observation.
func (h *Handlers) MountCorrections(r chi.Router) {
	answer := httpx.Permission(answerPermissions...)

	r.Route("/corrections", func(c chi.Router) {
		// The vocabulary. Reference data — a list of codes and their two displays, with no
		// patient in it — so it declares `reference.read` (CP85) and nothing else.
		//
		// The union it used to declare was already an attempt to say this, and it is worth
		// recording why the attempt failed twice. First: registration holds
		// `observation.correct.request` and none of the answering permissions, so a clerk
		// could raise a flag and could not read the list of reasons the flag form requires —
		// which meant they could not flag at all, and the screen said only that the reasons
		// could not be read. Widening the union fixed that and created the second failure:
		// every permission in the union is a permission about a patient, so every one of
		// them reaches only the station being worked for the station roles, and the guard
		// refused the route rather than enter a handler that could judge nothing. A list of
		// reason codes is not a patient, and now it does not claim to be.
		c.Method("GET", "/reasons", httpx.Declare(httpx.Permission(PermReferenceRead), h.correctionReasons))
		// What am I being asked to fix. The operator's own queue, and nobody else's — the
		// handler reads the caller's own id rather than taking one from the query string.
		//
		// Scoped (CP85), and the reach it settles is **ownership**, not station. ADR-0036 §1
		// asks "is this patient at your station in this visit", which is the wrong question
		// here in both halves: a correction is raised about a value recorded earlier, so the
		// patient has walked on by the time anybody answers, and the route deliberately admits
		// a FIELD_WORKER who stands at no station at all. The ADR has no §1(c) for ownership;
		// this is that rule in code, and it needs no station and no change to the ADR.
		//
		// `rbac.AuthorizeOwnList` returns the id the query filters on, and restricting to the
		// caller's own rows is narrower than every reach in the scope table — so it satisfies
		// whichever of this route's permissions let the caller in, without the handler having
		// to know which one did.
		c.Method("GET", "/mine", httpx.Declare(httpx.PermissionScoped(answerPermissions...), h.myCorrections))
		// NOT scoped, and this is the one route in the module where that is a considered
		// refusal rather than an omission (CP84's report names it).
		//
		// Its permission union admits a FIELD_WORKER, whose reach is `own` — the records
		// they made — and ownership of a correction *request* is not something the station
		// reach can express: the query answers "is this patient at your station", and the
		// field worker is at no station and the patient is nobody's. The handler below
		// already refuses a request that does not name the caller, which is the right rule
		// and the wrong shape to hand the engine: the owner is a fact about the row, and the
		// row has not been loaded when the guard decides. Until this handler asks
		// rbac.Authorize with the owner it read off the request, the route stays refused for
		// the narrow roles (CP85's report names it).
		c.Method("GET", "/{id}", httpx.Declare(answer, h.correction))
		// The rest of the correction workflow stays unscoped, and it is worth saying why in
		// one place rather than three times (CP85's report names the three that remain:
		// GET /{id}, POST /{id}/apply, POST /{id}/reject, and the flag below).
		//
		// ADR-0036 §1 measures reach as "is this patient at your station, in this visit".
		// That question is the wrong one here in two independent ways. First, a correction
		// is raised precisely about a value recorded earlier: by the time somebody answers
		// it the patient has walked on, so the *write* reach — which ADR-0036 justifies by
		// pointing at this very workflow as the path for finished patients — would make the
		// workflow unusable in exactly the case it exists for. Second, these routes
		// deliberately admit a FIELD_WORKER who holds no read permission at all, because the
		// author is the person a request is addressed to; their reach is `own`, which is
		// ownership of a *request*, and the station query cannot express ownership.
		//
		// The handlers already enforce the rule that matters — a request is answerable by
		// the person it names, or by somebody holding `observation.correct.approve`, decided
		// in the service against the name on the request. That is the right rule in the
		// wrong shape to hand the engine, so these routes are refused for the narrow roles
		// until the engine can be given an owner here.
		//
		// `/mine` above is now the exception, and the difference is worth naming: a *list*
		// of the caller's own rows can be restricted to them, which is what
		// rbac.AuthorizeOwnList does. These three name a request by id, so the owner is a
		// fact about a row the handler must first load, and settling the scope on the
		// caller's own id before loading it would be a check that cannot fail.
		//
		// ADR-0021's report argues apply and reject should additionally demand
		// actor.Proven(). That is a separate policy decision and is not made here.
		c.Method("POST", "/{id}/apply", httpx.Declare(answer, h.applyCorrection))
		c.Method("POST", "/{id}/reject", httpx.Declare(answer, h.rejectCorrection))
	})

	// Unscoped, for the reason above: the flag is the first step of the same workflow.
	r.Method("POST", "/observations/{id}/flag",
		httpx.Declare(httpx.Permission(PermCorrectionRequest), h.flagObservation))
}

// MountPatientCorrections hangs the per-patient list off a patient.
func (h *Handlers) MountPatientCorrections(p chi.Router) {
	read := httpx.PermissionScoped(PermObservationRead)
	p.Method("GET", "/{id}/corrections", httpx.Declare(read, h.correctionsForPatient))
}

func (h *Handlers) correctionReasons(w http.ResponseWriter, r *http.Request) {
	reasons, err := h.store.Reasons(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"reasons": reasons})
}

type flagRequest struct {
	EventID    string `json:"event_id,omitempty"`
	ReasonCode string `json:"reason_code"`
	Note       string `json:"note,omitempty"`
}

func (h *Handlers) flagObservation(w http.ResponseWriter, r *http.Request) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return
	}
	var body flagRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	eventID, ok := h.correctionEventID(w, r, body.EventID)
	if !ok {
		return
	}

	request, err := h.service.Flag(r.Context(), Flagging{
		EventID: eventID, ObservationID: id,
		ReasonCode: body.ReasonCode, Note: body.Note,
		LedgerSource: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateCorrection(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"request": request})
}

type applyRequest struct {
	EventID   string   `json:"event_id,omitempty"`
	Value     *float64 `json:"value,omitempty"`
	Unit      string   `json:"unit,omitempty"`
	ValueText string   `json:"value_text,omitempty"`
	ValueBool *bool    `json:"value_bool,omitempty"`
	ValueCode string   `json:"value_code,omitempty"`
	Note      string   `json:"note,omitempty"`
}

func (h *Handlers) applyCorrection(w http.ResponseWriter, r *http.Request) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return
	}
	var body applyRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	eventID, ok := h.correctionEventID(w, r, body.EventID)
	if !ok {
		return
	}

	request, err := h.service.Apply(r.Context(), Correcting{
		EventID: eventID, RequestID: id,
		Value: body.Value, Unit: body.Unit,
		ValueText: body.ValueText, ValueBool: body.ValueBool, ValueCode: body.ValueCode,
		Note:         body.Note,
		AsSupervisor: holds(r, PermCorrectionApprove),
		LedgerSource: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateCorrection(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"request": request})
}

type rejectRequest struct {
	EventID string `json:"event_id,omitempty"`
	Reason  string `json:"reason"`
}

func (h *Handlers) rejectCorrection(w http.ResponseWriter, r *http.Request) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return
	}
	var body rejectRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	eventID, ok := h.correctionEventID(w, r, body.EventID)
	if !ok {
		return
	}

	request, err := h.service.Reject(r.Context(), eventID, id, body.Reason,
		holds(r, PermCorrectionApprove), sourceOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateCorrection(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"request": request})
}

func (h *Handlers) correction(w http.ResponseWriter, r *http.Request) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return
	}
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	request, err := h.store.Request(r.Context(), id, reader.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateCorrection(err))
		return
	}

	// The route now admits operators who hold no read permission, so that a request can reach
	// the person it names. That must not turn this into a facility-wide browse: somebody
	// without `observation.read.values` sees the requests addressed to them and nothing else.
	//
	// The refusal is the *not-found* one, not a 403. A 403 here would answer "does a correction
	// request exist against that patient's values" for anybody who could guess an id, which is
	// the question the standing rule about 403s is there to keep unanswered.
	if request.AssignedTo != reader.UserID() && !holds(r, PermObservationRead) {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"request": request})
}

func (h *Handlers) myCorrections(w http.ResponseWriter, r *http.Request) {
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	// Whose rows these are, from the engine rather than from the reader. The id comes back
	// from the authorisation call and is the only id this list is allowed to be about — which
	// is what makes the ownership reach a thing the handler cannot skip rather than a thing it
	// is asked to remember. A caller who names somebody else in the query string is not
	// consulted, because nothing here reads the query string for a person.
	operator, err := rbac.AuthorizeOwnList(r.Context(), answerPermissions...)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	// Open only, unless the caller asks for the lot. An operator's queue is what is waiting;
	// their history is a different question and a different screen.
	openOnly := strings.TrimSpace(r.URL.Query().Get("all")) == ""
	requests, err := h.store.RequestsFor(r.Context(), reader.FacilityID(), operator,
		openOnly, correctionLimit(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"requests": requests})
}

func (h *Handlers) correctionsForPatient(w http.ResponseWriter, r *http.Request) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return
	}
	if !h.mayReadPatient(w, r, id) {
		return
	}
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	requests, err := h.store.RequestsForPatient(r.Context(), id, reader.FacilityID(),
		correctionLimit(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"requests": requests})
}

// --- the small shared pieces ---

// holds says whether the caller holds a permission.
//
// Read from the caller rather than from the principal, because the principal is what the *route's*
// decision left behind and this is a second question: the route admitted them, and the handler is
// asking which of two acts this is.
func holds(r *http.Request, permission string) bool {
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		return false
	}
	for _, held := range caller.Permissions {
		if held == permission {
			return true
		}
	}
	return false
}

func correctionLimit(r *http.Request) int {
	const fallback, most = 50, 200
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 {
		return fallback
	}
	if parsed > most {
		return most
	}
	return parsed
}

func (h *Handlers) correctionEventID(w http.ResponseWriter, r *http.Request,
	raw string) (uuid.UUID, bool) {

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

// translateCorrection turns the workflow's refusals into answers a person can act on.
func translateCorrection(err error) error {
	switch {
	case errors.Is(err, ErrNoRequest):
		return errs.ErrNotFound
	case errors.Is(err, ErrAlreadyFlagged):
		return errs.New("CORRECTION_ALREADY_OPEN", errs.KindConflict, http.StatusConflict,
			"Somebody has already asked for this value to be looked at.",
			"এই মানটি দেখার জন্য আগেই কেউ অনুরোধ করেছেন।").WithDetail(err)
	case errors.Is(err, ErrRequestClosed):
		return errs.New("CORRECTION_ALREADY_ANSWERED", errs.KindConflict, http.StatusConflict,
			"That request has already been answered.",
			"ওই অনুরোধের উত্তর আগেই দেওয়া হয়েছে।").WithDetail(err)
	case errors.Is(err, ErrNotYours):
		// 403 rather than 422: the request is well-formed and it is somebody else's to answer.
		// The message says whose, because an operator staring at a refused button needs to know
		// who to go and find rather than what to retype.
		return errs.ErrForbidden.WithDetail(err)
	case errors.Is(err, ErrCannotFlagDerived):
		return errs.ErrValidation.WithFieldIn("observation_id",
			"This value was computed, not typed. Ask for the measurement it came from to be corrected.",
			"এই মানটি হিসাব করে বের করা, কেউ লেখেননি। যে মাপ থেকে এটি এসেছে সেটি ঠিক করার অনুরোধ করুন।")
	case errors.Is(err, ErrCannotFlagOwn):
		return errs.ErrValidation.WithFieldIn("observation_id",
			"This is your own value — correct it rather than flagging it.",
			"এটি আপনারই দেওয়া মান — অনুরোধ না করে নিজেই ঠিক করুন।")
	case errors.Is(err, ErrUnknownReason):
		return errs.ErrValidation.WithFieldIn("reason_code",
			"Choose a reason from the list.",
			"তালিকা থেকে একটি কারণ বেছে নিন।")
	case errors.Is(err, ErrReasonRequired):
		return errs.ErrValidation.WithFieldIn("reason",
			"Say why the value stands. A refusal with no reason teaches nobody anything.",
			"মানটি কেন ঠিক আছে তা লিখুন। কারণহীন অস্বীকৃতি কাউকে কিছু শেখায় না।")
	default:
		return translate(err)
	}
}
