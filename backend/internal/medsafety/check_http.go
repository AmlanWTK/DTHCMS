package medsafety

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The safety check endpoint (CP78).
//
// # The route, and the seam left for CP80
//
// §7.2 names `POST /prescriptions/{id}/safety-check`. **CP80 — the prescription aggregate — does
// not exist**, so that route has no id to take. The choice was between inventing a prescription
// table now and taking what the engine actually reads, and the second is the one that does not
// leave CP80 a second prescription model to migrate away from.
//
// So the route is `POST /v1/patients/{id}/safety-check`, and its body is **a list of proposed
// items**. The patient id is real today — it is what the clinical picture is read from — and the
// item list is exactly what a prescription's items will be. When CP80 lands, its route is nine
// lines: load the prescription, map `prescription.Item` to [Item], call the same
// [Engine.Check]. No evaluation logic moves, because none of it was ever keyed to a prescription.
//
// # Why the patient is in the path and the picture is not in the body
//
// The alternative shape — post the whole clinical picture — is what the CP77 sandbox does, and it
// is right there because the sandbox is testing a rule rather than checking a patient. Here it
// would be wrong twice: a client assembling the picture is a client that can assemble it *wrong*
// and get a clean check, and a picture in a request body is a patient's clinical record
// travelling through a client that did not need to hold it.
//
// # This is a read, and it uses the read door
//
// It changes nothing except an audit entry. It is a POST because it carries a body, not because
// it writes — so it must be reachable from a browser with no enrolled device, which is the CP74
// defect `dthclint readpath` exists to keep out. Nothing in this file touches
// `eventstore.ActorFrom`; the caller comes from `httpx.PrincipalFrom`, like every other handler
// in this module.
//
// # Nothing here is logged
//
// Not the picture, not the findings, not a count of them. The audit entry carries the rule
// versions and the counts, and names no drug, no diagnosis, no allergen and no eGFR.

// PermCheck is the permission a safety check needs.
//
// Its own permission rather than `medication.rule.read`, because the two are different acts on
// different objects: reading the library is reading a drug label, and running a check is reading
// a patient's kidney function, diagnoses and allergies. Granted to the prescribers and to QA,
// which is CP83 running the interaction and duplicate checks as part of clearance.
const PermCheck = "medication.safety.check"

// SafetyCheckRun is what the audit trail records about one check. **Criterion 5 depends on it.**
//
// The question this answers, asked months later about a prescription somebody is questioning, is
// *what was it checked against*. The rule table can answer that only until somebody publishes a
// v3 or withdraws the rule, and both are ordinary things to have happened — so the versions
// travel into the hash-chained trail where they cannot be quietly edited.
//
// **It carries no clinical content.** Not a drug name, not a diagnosis, not an allergen, not the
// eGFR. The counts and the verdict say that a check happened and what it concluded; what it
// concluded *about* is in the prescription, which is where a patient's record belongs.
type SafetyCheckRun struct {
	FacilityID uuid.UUID
	ActorID    uuid.UUID
	ActorCode  string
	ActorRole  string

	// PatientID is the subject of the check. In the audit entry's subject fields, never in its
	// details beside a clinical fact.
	PatientID uuid.UUID

	At      time.Time
	Verdict Verdict

	ItemCount       int
	FindingCount    int
	BlockCount      int
	UnverifiedCount int
	UncoveredCount  int
	RulesLive       int

	// Evaluated is the exact list of rule versions that ran. Criterion 5.
	Evaluated []EvaluatedVersion
}

// CheckAuditor records a run. Implemented by a bridge in `cmd/api`, for the reason every bridge
// in that binary exists: a module able to write audit entries directly grows a second,
// differently-shaped way of describing what happened.
type CheckAuditor interface {
	SafetyCheckRun(ctx context.Context, run SafetyCheckRun) error
}

// CheckHandlers serve the safety check.
type CheckHandlers struct {
	engine *Engine
	facts  PatientFacts
	audit  CheckAuditor
	clock  interface{ Now() time.Time }
	logger interface {
		ErrorContext(context.Context, string, ...any)
	}
}

// CheckHandlersConfig builds them.
type CheckHandlersConfig struct {
	Engine *Engine
	Facts  PatientFacts
	Audit  CheckAuditor
	Clock  interface{ Now() time.Time }
	Logger interface {
		ErrorContext(context.Context, string, ...any)
	}
}

// NewCheckHandlers builds them.
func NewCheckHandlers(cfg CheckHandlersConfig) *CheckHandlers {
	return &CheckHandlers{
		engine: cfg.Engine, facts: cfg.Facts, audit: cfg.Audit,
		clock: cfg.Clock, logger: cfg.Logger,
	}
}

// MountPatient attaches the check under a patient.
func (h *CheckHandlers) MountPatient(r chi.Router) {
	check := httpx.Permission(PermCheck)
	r.Method("POST", "/{id}/safety-check", httpx.Declare(check, h.check))
	r.Method("GET", "/{id}/renal-status", httpx.Declare(check, h.renal))
}

// renal is CP79's indicator: which kidney function this patient has, when it was taken, and
// whether that is still current.
//
// # Why it is its own route rather than a field on the patient header
//
// Because the answer depends on a facility policy and a window, and a header that carried the
// raw number would leave every screen to decide for itself whether it was stale. One route, one
// resolution, one sentence — so the indicator on the prescription editor and the number the
// rules ran against cannot drift apart.
//
// # Why it holds the safety-check permission
//
// The object is the patient's kidney function, which is the same clinical object
// `POST /safety-check` reads. Giving it a permission of its own would have meant a grant that
// says "may read a patient's renal function but may not check a prescription against it", which
// describes nobody in this clinic.
func (h *CheckHandlers) renal(w http.ResponseWriter, r *http.Request) {
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, nil, errs.ErrUnauthenticated)
		return
	}
	facility, err := uuid.Parse(principal.FacilityID)
	if err != nil {
		httpx.WriteError(w, r, nil, errs.ErrUnauthenticated.WithDetail(err))
		return
	}
	patient, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, nil, errs.ErrNotFound)
		return
	}
	status, err := h.engine.Renal(r.Context(), h.facts, facility, patient, h.now())
	if err != nil {
		httpx.WriteError(w, r, nil, errs.ErrInternal.WithDetail(err))
		return
	}
	// Nothing is logged and nothing is audited. A GET that read one number is a read like any
	// other clinical read, and the audit trail for reading a patient's record is CP22's, not a
	// second one written here.
	httpx.WriteJSON(w, http.StatusOK, status)
}

// checkBody is a draft prescription, as the editor holds it.
type checkBody struct {
	// Items is the proposed prescription. May be empty, and an empty check is answered rather
	// than refused: an editor with nothing typed in it still wants to know that nothing has
	// been checked.
	Items []Item `json:"items"`

	// At reproduces a past check against the rule versions that were live at that instant.
	// **Criterion 5, as a parameter.** Absent means now.
	At *time.Time `json:"at,omitempty"`
}

func (h *CheckHandlers) check(w http.ResponseWriter, r *http.Request) {
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, nil, errs.ErrUnauthenticated)
		return
	}
	facility, err := uuid.Parse(principal.FacilityID)
	if err != nil {
		httpx.WriteError(w, r, nil, errs.ErrUnauthenticated.WithDetail(err))
		return
	}
	actor, err := uuid.Parse(principal.UserID)
	if err != nil {
		httpx.WriteError(w, r, nil, errs.ErrUnauthenticated.WithDetail(err))
		return
	}
	patient, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		// 404 rather than 422, like every other patient-scoped route: a malformed id and an id
		// this facility does not have are the same answer.
		httpx.WriteError(w, r, nil, errs.ErrNotFound)
		return
	}

	var body checkBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, nil, err)
		return
	}

	at := h.now()
	if body.At != nil {
		at = body.At.UTC()
	}

	picture, unclassified, err := h.engine.Assemble(r.Context(), h.facts, facility, patient)
	if err != nil {
		httpx.WriteError(w, r, nil, errs.ErrInternal.WithDetail(err))
		return
	}

	result, err := h.engine.Check(r.Context(), Request{
		FacilityID: facility, Proposed: body.Items, Picture: picture, At: at,
	})
	if err != nil {
		httpx.WriteError(w, r, nil, errs.ErrInternal.WithDetail(err))
		return
	}

	// An allergy nobody could classify is folded in here rather than inside the engine,
	// because the engine takes a picture and this is a fact about how the picture was read.
	// Re-sorted and the verdict recomputed, so a block raised by an unclassifiable anaphylaxis
	// actually blocks rather than sitting at the bottom of the list.
	// Through [Fold] rather than inline, since CP80 needs the identical three lines for the
	// prescription-scoped route and two copies of "does an unclassifiable anaphylaxis block
	// this" is one copy too many.
	result = Fold(result, UnclassifiedAllergyFindings(unclassified), len(body.Items))

	h.record(r.Context(), SafetyCheckRun{
		FacilityID: facility, ActorID: actor, ActorCode: principal.Code,
		ActorRole: principal.Role, PatientID: patient,
		At: result.At, Verdict: result.Verdict,
		ItemCount:       len(body.Items),
		FindingCount:    len(result.Findings),
		BlockCount:      countBlocks(result.Findings),
		UnverifiedCount: countUnverified(result.Findings),
		UncoveredCount:  result.UncoveredCount,
		RulesLive:       result.RulesLive,
		Evaluated:       result.Evaluated,
	})

	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *CheckHandlers) now() time.Time {
	if h.clock == nil {
		return time.Now().UTC()
	}
	return h.clock.Now().UTC()
}

// record writes the audit entry and logs a failure without failing the request.
//
// The check has happened and the physician is looking at its result; an audit write that failed
// afterwards is a gap in the trail, which is worth an error in the log and is not worth telling
// him his prescription was not checked when it was.
func (h *CheckHandlers) record(ctx context.Context, run SafetyCheckRun) {
	if h.audit == nil {
		return
	}
	if err := h.audit.SafetyCheckRun(ctx, run); err != nil && h.logger != nil {
		h.logger.ErrorContext(ctx, "safety check audit entry not recorded",
			"error", err.Error())
	}
}
