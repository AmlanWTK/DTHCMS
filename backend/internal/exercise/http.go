package exercise

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

// Station 8 over HTTP (CP60).
//
// # The route that does not exist
//
// There is no `GET /v1/exercises`. The library is only ever reachable through
// `GET /v1/patients/{id}/exercise/options`, which applies the filter — because a route that
// returned the whole library would be the thing criterion 1 forbids, whatever the screen did with
// it afterwards. The catalogue of *conditions* is a separate route and is unfiltered, since it
// contains no exercises and station 8 needs it to ask the questions.

const (
	// PermWriteExercise is station 8's own.
	PermWriteExercise = "observation.write.exercise"
	// PermReadValues is what every clinical reader holds. A physician reading a plan at the
	// consultation needs it; a plan they could not read is a plan they cannot discuss.
	PermReadValues = "observation.read.values"
)

// Handlers serve station 8.
type Handlers struct {
	service *Service
	store   *Store
	logger  *slog.Logger
}

// HandlersConfig builds Handlers.
type HandlersConfig struct {
	Service *Service
	Store   *Store
	Clock   interface{ Now() time.Time }
	Logger  *slog.Logger
}

// NewHandlers builds them.
func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{service: cfg.Service, store: cfg.Store, logger: cfg.Logger}
}

// Mount attaches /v1/exercise.
func (h *Handlers) Mount(r chi.Router) {
	read := httpx.Permission(PermReadValues, PermWriteExercise)
	write := httpx.Permission(PermWriteExercise)

	r.Route("/exercise", func(e chi.Router) {
		// The questions, not the exercises. Readable by anyone who may read a value, because
		// the physician's view renders a recorded contraindication by its name.
		e.Method("GET", "/contraindications", httpx.Declare(read, h.contraindications))
		e.Method("POST", "/assessments", httpx.Declare(write, h.record))
		e.Method("POST", "/plans", httpx.Declare(write, h.issue))
	})
}

// MountPatient hangs the assessment, the options and the plan off a patient.
func (h *Handlers) MountPatient(p chi.Router) {
	read := httpx.Permission(PermReadValues, PermWriteExercise)
	p.Method("GET", "/{id}/exercise", httpx.Declare(read, h.standing))
	p.Method("GET", "/{id}/exercise/options", httpx.Declare(read, h.options))
	p.Method("GET", "/{id}/exercise/history", httpx.Declare(read, h.history))
}

func (h *Handlers) contraindications(w http.ResponseWriter, r *http.Request) {
	conditions, err := h.store.Contraindications(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"contraindications": conditions})
}

type recordRequest struct {
	EventID   string `json:"event_id,omitempty"`
	PatientID string `json:"patient_id"`
	VisitID   string `json:"visit_id,omitempty"`

	WalksUnaided *bool  `json:"walks_unaided,omitempty"`
	WalkMinutes  *int   `json:"walk_minutes,omitempty"`
	JointPain    string `json:"joint_pain,omitempty"`

	Contraindications []string `json:"contraindications"`
	// Asked is which questions were put to the patient. Required, and it has to cover every live
	// condition: without it the server cannot tell a skipped question from a negative answer.
	Asked []string `json:"asked"`
	Note  string   `json:"note,omitempty"`
}

func (h *Handlers) record(w http.ResponseWriter, r *http.Request) {
	var body recordRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	patient, err := uuid.Parse(strings.TrimSpace(body.PatientID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("patient_id",
			"That is not a patient identifier.", "এটি কোনও রোগীর পরিচিতি নয়।"))
		return
	}
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	// Scope before anything else, and a 404 rather than a 403: whether this facility has that
	// patient is itself something a 403 would disclose.
	known, err := h.store.KnowsPatient(r.Context(), patient, actor.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	if !known {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}

	in := Recording{
		PatientID:         patient,
		WalksUnaided:      body.WalksUnaided,
		WalkMinutes:       body.WalkMinutes,
		JointPain:         strings.TrimSpace(body.JointPain),
		Contraindications: body.Contraindications,
		Asked:             body.Asked,
		Note:              strings.TrimSpace(body.Note),
		LedgerSource:      sourceOf(r),
	}
	if raw := strings.TrimSpace(body.EventID); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
				"That is not an event identifier.", "এটি কোনও ইভেন্টের পরিচিতি নয়।"))
			return
		}
		in.EventID = id
	}
	if raw := strings.TrimSpace(body.VisitID); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("visit_id",
				"That is not a visit identifier.", "এটি কোনও ভিজিটের পরিচিতি নয়।"))
			return
		}
		in.VisitID = &id
	}

	assessment, options, err := h.service.Record(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateExercise(err))
		return
	}
	// The options come back with the assessment so that the screen never holds a list it was not
	// given by the server — see Service.Record.
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"assessment": assessment, "options": options,
	})
}

type issueRequest struct {
	EventID   string `json:"event_id,omitempty"`
	PatientID string `json:"patient_id"`
	VisitID   string `json:"visit_id,omitempty"`
	// AssessmentID is the findings the operator was looking at. Required: see Service.Issue.
	AssessmentID string   `json:"assessment_id"`
	Targets      []Target `json:"targets"`
	Note         string   `json:"note,omitempty"`
}

func (h *Handlers) issue(w http.ResponseWriter, r *http.Request) {
	var body issueRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	patient, err := uuid.Parse(strings.TrimSpace(body.PatientID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("patient_id",
			"That is not a patient identifier.", "এটি কোনও রোগীর পরিচিতি নয়।"))
		return
	}
	assessment, err := uuid.Parse(strings.TrimSpace(body.AssessmentID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("assessment_id",
			"A plan has to say which assessment it was built from.",
			"পরিকল্পনাটি কোন মূল্যায়নের ভিত্তিতে তৈরি, তা বলতে হবে।"))
		return
	}
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	known, err := h.store.KnowsPatient(r.Context(), patient, actor.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	if !known {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}

	in := Issuing{
		PatientID:    patient,
		AssessmentID: assessment,
		Targets:      body.Targets,
		Note:         strings.TrimSpace(body.Note),
		LedgerSource: sourceOf(r),
	}
	if raw := strings.TrimSpace(body.EventID); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
				"That is not an event identifier.", "এটি কোনও ইভেন্টের পরিচিতি নয়।"))
			return
		}
		in.EventID = id
	}
	if raw := strings.TrimSpace(body.VisitID); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("visit_id",
				"That is not a visit identifier.", "এটি কোনও ভিজিটের পরিচিতি নয়।"))
			return
		}
		in.VisitID = &id
	}

	plan, err := h.service.Issue(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateExercise(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"plan": plan})
}

// standing is what this patient's exercise record says right now: the findings and the plan.
func (h *Handlers) standing(w http.ResponseWriter, r *http.Request) {
	patient, reader, ok := h.patientOf(w, r)
	if !ok {
		return
	}

	body := map[string]any{"assessment": nil, "plan": nil}

	assessment, err := h.store.LiveAssessment(r.Context(), patient, reader.FacilityID())
	switch {
	case errors.Is(err, ErrNoAssessment):
		// Not an error. A patient who has not been to station 8 has no findings, and a 404 here
		// would make an ordinary first visit look like a broken route.
	case err != nil:
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	default:
		body["assessment"] = assessment
	}

	plan, err := h.store.LivePlan(r.Context(), patient, reader.FacilityID())
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	default:
		body["plan"] = plan
	}

	httpx.WriteJSON(w, http.StatusOK, body)
}

// options is the permitted set, the count and the reasons.
func (h *Handlers) options(w http.ResponseWriter, r *http.Request) {
	patient, reader, ok := h.patientOf(w, r)
	if !ok {
		return
	}
	options, err := h.store.Options(r.Context(), patient, reader.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateExercise(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, options)
}

// history is the assessments and the plans, so a follow-up can be compared with the last one.
func (h *Handlers) history(w http.ResponseWriter, r *http.Request) {
	patient, reader, ok := h.patientOf(w, r)
	if !ok {
		return
	}
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	assessments, err := h.store.AssessmentsForPatient(r.Context(), patient, reader.FacilityID(), limit)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	plans, err := h.store.PlansForPatient(r.Context(), patient, reader.FacilityID(), limit)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"assessments": assessments, "plans": plans,
	})
}

// patientOf resolves the patient in the path and checks this facility has them, answering 404
// either way so that a wrong identifier and somebody else's patient are indistinguishable.
func (h *Handlers) patientOf(w http.ResponseWriter, r *http.Request) (uuid.UUID, eventstore.Reader, bool) {
	var zero eventstore.Reader
	patient, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, zero, false
	}
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return uuid.Nil, zero, false
	}
	known, err := h.store.KnowsPatient(r.Context(), patient, reader.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return uuid.Nil, zero, false
	}
	if !known {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, zero, false
	}
	return patient, reader, true
}

// sourceOf is which surface wrote the event, decided the same way `clinical` decides it.
func sourceOf(r *http.Request) eventstore.Source {
	if strings.TrimSpace(r.Header.Get("X-DTHCMS-Device")) != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}

func translateExercise(err error) error {
	// The refusals that know something specific are rendered first, because the sentinel cases
	// below would swallow them into a generic message — which is how "not allowed" reaches an
	// operator who then tries the next high-impact option.
	var contraindicated *ContraindicatedError
	if errors.As(err, &contraindicated) {
		return contraindicatedError(contraindicated)
	}
	var unknown *UnknownExerciseError
	if errors.As(err, &unknown) {
		if unknown.Retired {
			// Its own code, because it means something a client must act on differently: the
			// list moved, so fetch the options again. A shared VALIDATION_FAILED would leave a
			// screen guessing, and a screen that guesses refetches on a typo or fails to refetch
			// on a retirement.
			//
			// Named rather than coded, because the operator chose a row that said "ভারী ওজন
			// তোলা" and a sentence answering them with HEAVY_LIFT is answering a question they
			// did not ask.
			return errs.New("EXERCISE_RETIRED", errs.KindConflict, http.StatusConflict,
				unknown.ExerciseEN+" is no longer in the library. Review the options and choose again.",
				unknown.ExerciseBN+" আর তালিকায় নেই। বিকল্পগুলি দেখে আবার বেছে নিন।").
				WithFieldIn("exercise_code", unknown.ExerciseCode, unknown.ExerciseCode)
		}
		return errs.ErrValidation.
			WithFieldIn("targets",
				unknown.ExerciseCode+" is not an exercise in the library.",
				unknown.ExerciseCode+" এই তালিকার কোনও ব্যায়াম নয়।").
			WithFieldIn("exercise_code", unknown.ExerciseCode, unknown.ExerciseCode)
	}
	// A note on the Bangla below, and on every other sentence here that carries a name.
	//
	// Bengali puts a case ending on the noun — সাঁতারের জন্য, not সাঁতার-এর জন্য — and which
	// ending depends on the name's final character. A sentence assembled as name-plus-suffix is
	// therefore either wrong or hyphenated, and hyphenated reads as a graft. So a name goes in
	// **apposition**: the name, a dash, then a complete sentence that needs no ending from it.
	// Where the name is already the subject in the nominative (`X দুইবার আছে`, `X আর তালিকায়
	// নেই`) no ending is needed and it reads naturally as it stands.
	var uncountable *NotCountableError
	if errors.As(err, &uncountable) {
		return errs.ErrValidation.
			WithFieldIn("targets",
				uncountable.ExerciseEN+" needs how many times a week and how many minutes each time.",
				uncountable.ExerciseBN+" — সপ্তাহে কতবার এবং প্রতিবার কত মিনিট, তা দিতে হবে।").
			WithFieldIn("exercise_code", uncountable.ExerciseCode, uncountable.ExerciseCode)
	}
	var duplicated *DuplicateTargetError
	if errors.As(err, &duplicated) {
		return errs.ErrValidation.
			WithFieldIn("targets",
				duplicated.ExerciseEN+" is on the plan twice, with two different targets. Keep one.",
				duplicated.ExerciseBN+" দুইবার আছে, দুই রকম লক্ষ্য নিয়ে। একটি রাখুন।").
			WithFieldIn("exercise_code", duplicated.ExerciseCode, duplicated.ExerciseCode)
	}

	switch {
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrNoAssessment):
		// 409 rather than 404 or a default. The patient exists; the questions have not been
		// asked, and there is no honest list to give until they are. Answering "no
		// contraindications recorded" with the whole library is exactly the failure criterion 1
		// is about, so the refusal is the answer, and the message says what to do about it.
		return errs.New("EXERCISE_NO_ASSESSMENT", errs.KindConflict, http.StatusConflict,
			"No exercise assessment has been recorded for this patient yet, so no options can be offered.",
			"এই রোগীর ব্যায়াম-মূল্যায়ন এখনও নেওয়া হয়নি, তাই কোনও বিকল্প দেখানো যাচ্ছে না।").WithDetail(err)
	case errors.Is(err, ErrStaleAssessment):
		return errs.New("EXERCISE_ASSESSMENT_SUPERSEDED", errs.KindConflict, http.StatusConflict,
			"A newer assessment has been recorded for this patient. Review the options again before issuing a plan.",
			"এই রোগীর নতুন একটি মূল্যায়ন নেওয়া হয়েছে। পরিকল্পনা দেওয়ার আগে বিকল্পগুলি আবার দেখুন।").WithDetail(err)
	case errors.Is(err, ErrUnknownContraindication):
		return errs.ErrValidation.WithFieldIn("contraindications",
			"One of those conditions is not in the list this clinic asks about.",
			"ওই অবস্থাগুলির একটি এই ক্লিনিকের প্রশ্নতালিকায় নেই।")
	case errors.Is(err, ErrEmptyPlan):
		return errs.ErrValidation.WithFieldIn("targets",
			"A plan needs at least one exercise.",
			"পরিকল্পনায় অন্তত একটি ব্যায়াম থাকতে হবে।")
	// There is deliberately no `ErrTargetNotCountable` case here. Every path that produces it
	// returns the typed `*NotCountableError`, which is handled above and names the exercise; a
	// sentinel case underneath would be unreachable today and would silently start producing a
	// second, worse screen for the same refusal the day somebody returned the bare error. The
	// same goes for `ErrUnknownExercise` and `ErrContraindicated`.
	case errors.Is(err, ErrFindingWasNotAsked):
		return errs.ErrValidation.WithFieldIn("contraindications",
			"A condition is recorded as applying that the assessment did not ask about.",
			"এমন একটি অবস্থা প্রযোজ্য বলে লেখা হয়েছে, যেটি সম্পর্কে জিজ্ঞাসাই করা হয়নি।")
	case errors.Is(err, ErrIncompleteAssessment):
		// Named rather than "incomplete", because a client working from a stale catalogue — an
		// offline tablet, a rolling deploy — has to know which questions to fetch and put before
		// it can record. The codes go in the field message so a screen can highlight them.
		missing := strings.TrimPrefix(err.Error(), ErrIncompleteAssessment.Error()+": ")
		return errs.ErrValidation.
			WithFieldIn("asked",
				"These questions have not been asked, and the plan cannot be filtered without them: "+missing,
				"এই প্রশ্নগুলি করা হয়নি, আর এগুলি ছাড়া পরিকল্পনা যাচাই করা যাবে না: "+missing).
			// The same codes, out of the prose. A client that wants to highlight the two new
			// questions should not have to parse a sentence that gets translated, shortened and
			// improved — which is the thing the rest of this design refuses to make anybody do.
			WithFieldIn("missing_conditions", missing, missing)
	}
	return errs.ErrInternal.WithDetail(err)
}

// contraindicatedError is the refusal an operator reads, built from the mapping row.
//
// Both languages carry the same three things: what was chosen, what forbids it, and why. The
// "why" is the sentence from `core.exercise_contraindication`, which is the sentence a clinician
// who disagrees with the exclusion argues with — a constant here would have made the mapping
// unarguable at the one place somebody meets it.
func contraindicatedError(e *ContraindicatedError) error {
	en := e.ExerciseEN + " is not safe with " + strings.ToLower(e.ConditionEN) + ". " + e.ReasonEN
	bn := e.ConditionBN + " থাকলে " + e.ExerciseBN + " নিরাপদ নয়। " + e.ReasonBN
	if !e.Applies {
		// Not a finding about this patient: a question added to the catalogue since the
		// assessment was taken. Saying "the patient has X" here would be a claim nobody made.
		en = e.ExerciseEN + " cannot be offered until the question about " +
			strings.ToLower(e.ConditionEN) + " has been asked."
		bn = e.ConditionBN + " সম্পর্কে জিজ্ঞাসা না করা পর্যন্ত " + e.ExerciseBN + " দেওয়া যাবে না।"
	}
	return errs.New("EXERCISE_CONTRAINDICATED", errs.KindValidation,
		http.StatusUnprocessableEntity, en, bn).
		WithFieldIn("targets", en, bn).
		WithFieldIn("exercise_code", e.ExerciseCode, e.ExerciseCode).
		WithFieldIn("contraindication_code", e.ConditionCode, e.ConditionCode).
		WithDetail(e)
}
