// Package exercise is station 8's assessment and plan (CP60, §3 step 8, §12.1).
//
// # The one decision this package exists to make unmistakable
//
// Acceptance criterion 1 is *"contraindicated exercises are excluded, not warned"*, and the
// checkpoint's manual verification says it again in bold: a patient with severe neuropathy must
// find the contraindicated options **absent**.
//
// A screen that received the whole library and hid part of it would be a warning wearing a
// different colour. The data would be on the device, one bug or one "show all" affordance away
// from being offered, and an operator's tap would be the only thing between a patient with an
// insensate foot and a jumping routine. So there is no code path in this package — no store
// method, no handler, no query — that returns the library for a patient. `Options` computes the
// permitted set on the server from the contraindications actually recorded, and the excluded
// exercises never leave the process.
//
// What does leave is a count and the reasons: *"three options are not shown because of severe
// neuropathy"*. That is transparency about the filter rather than an offer, and a physician
// looking at a short list needs it — otherwise a list that is short on purpose is
// indistinguishable from a library that is missing rows.
//
// # Why the plan freezes the assessment
//
// A plan names the assessment it was filtered against, and that reference never moves. A patient
// whose foot ulcer heals gets a new assessment, not an edited one, and last month's plan stays
// readable against last month's findings — which is the only way "why was she given stair
// climbing in June" has an answer at all.
package exercise

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Errors this package returns. Translated to HTTP in http.go, so that the sentence a clinician
// reads is written once and in both languages.
var (
	// ErrNotFound is a patient this facility does not have, or a plan that is not there.
	ErrNotFound = errors.New("exercise: not found")
	// ErrNoAssessment is a plan asked for before anybody answered the questions. Refused rather
	// than defaulted: "no contraindications recorded" and "no contraindications" are different
	// facts, and treating the first as the second is how a neuropathic patient is offered
	// jogging by a station that skipped a screen.
	ErrNoAssessment = errors.New("exercise: no assessment has been recorded for this patient")
	// ErrUnknownContraindication is a condition code that is not in the catalogue.
	ErrUnknownContraindication = errors.New("exercise: unknown contraindication")
	// ErrUnknownExercise is an exercise code that is not in the library.
	ErrUnknownExercise = errors.New("exercise: unknown exercise")
	// ErrContraindicated is the refusal criterion 1 is about: a plan that named something this
	// patient's own assessment forbids.
	ErrContraindicated = errors.New("exercise: contraindicated for this patient")
	// ErrEmptyPlan is a plan with nothing in it.
	ErrEmptyPlan = errors.New("exercise: a plan with no exercises is not a plan")
	// ErrTargetNotCountable is a target §12.1 could not compare across visits.
	ErrTargetNotCountable = errors.New("exercise: a target is a number of times a week and a number of minutes")
	// ErrStaleAssessment is a plan built against an assessment that has since been superseded.
	// Refused, because the list the operator was looking at is no longer the permitted one.
	ErrStaleAssessment = errors.New("exercise: the assessment this plan was built against is no longer the current one")
	// ErrIncompleteAssessment is an assessment that left a live condition unanswered. Refused,
	// because the filter cannot tell a skipped question from a negative answer.
	ErrIncompleteAssessment = errors.New("exercise: some conditions were not asked about")
	// ErrFindingWasNotAsked is a condition recorded as applying that the assessment did not ask
	// about — invariant 89, at the door.
	ErrFindingWasNotAsked = errors.New("exercise: a condition is recorded as applying that was not asked about")
	// ErrDuplicateTarget is one exercise targeted twice, with two different weekly totals.
	ErrDuplicateTarget = errors.New("exercise: an exercise is targeted twice")
)

// DuplicateTargetError names the exercise that is on the plan twice.
type DuplicateTargetError struct {
	ExerciseCode string
	ExerciseEN   string
	ExerciseBN   string
}

func (e *DuplicateTargetError) Error() string {
	return "exercise: " + e.ExerciseCode + " is targeted twice"
}

func (e *DuplicateTargetError) Is(target error) bool { return target == ErrDuplicateTarget }

// named fills in the bilingual names of an exercise from the list the operator was shown, falling
// back to the code when it is not on that list — which happens only when the code was never an
// exercise, and then the code is the only true thing there is to say.
func named(offered map[string]Exercise, code string) (en, bn string) {
	if e, ok := offered[code]; ok {
		return e.NameEN, e.NameBN
	}
	return code, code
}

// ContraindicatedError is the refusal this whole checkpoint exists for.
//
// It carries what the operator needs rather than only what the developer needs: the exercise, the
// condition, and the **sentence from the mapping table** saying why the pair is unsafe — in both
// languages. An operator told only "not allowed" tries the next high-impact option; one told that
// a foot without protective sensation cannot feel an injury from repeated impact does not.
//
// `Applies` separates a finding about this patient from a question nobody has put yet. The
// operator's next act differs: one is a conversation with a physician, the other is asking the
// patient something.
type ContraindicatedError struct {
	ExerciseCode string
	ExerciseEN   string
	ExerciseBN   string

	ConditionCode string
	ConditionEN   string
	ConditionBN   string

	ReasonEN string
	ReasonBN string

	Applies bool
}

func (e *ContraindicatedError) Error() string {
	return "exercise: " + e.ExerciseCode + " is contraindicated by " + e.ConditionCode
}

// Is lets callers keep matching on the sentinel while the value carries the detail.
func (e *ContraindicatedError) Is(target error) bool { return target == ErrContraindicated }

// UnknownExerciseError is a code that is not in the library, or one retired between the operator
// seeing the list and choosing from it.
//
// Two sentences and **two error codes**, because they mean different things to a client: a
// retirement means the list moved and the right act is to fetch the options again, while an
// unknown code means the request is wrong and refetching would loop. A client cannot tell them
// apart from prose, and a screen that guessed would refetch on a typo or fail to refetch on a
// retirement.
type UnknownExerciseError struct {
	ExerciseCode string
	// ExerciseEN and ExerciseBN are the names, when the row is still there to read them from —
	// which it is in the retired case, and is not when the code was never an exercise. A refusal
	// that put a database identifier in the middle of a Bengali sentence would be the only one
	// here that did.
	ExerciseEN string
	ExerciseBN string
	Retired    bool
}

func (e *UnknownExerciseError) Error() string {
	if e.Retired {
		return "exercise: " + e.ExerciseCode + " has been retired"
	}
	return "exercise: " + e.ExerciseCode + " is not in the library"
}

func (e *UnknownExerciseError) Is(target error) bool { return target == ErrUnknownExercise }

// NotCountableError names the target §12.1 could not compare.
//
// Carries the names as well as the code for the same reason `UnknownExerciseError` does: the
// operator chose a row that said "সাঁতার", and a sentence answering them with SWIM answers a
// question they did not ask. The names are available here because a target only reaches this
// check after passing the permitted one, so it is on the list the operator was just shown.
type NotCountableError struct {
	ExerciseCode string
	ExerciseEN   string
	ExerciseBN   string
}

func (e *NotCountableError) Error() string {
	return "exercise: the target for " + e.ExerciseCode + " is not two numbers"
}

func (e *NotCountableError) Is(target error) bool { return target == ErrTargetNotCountable }

// Contraindication is one thing that can stop somebody exercising, with the question the station
// actually asks. The question rather than the label, because a checkbox saying "neuropathy" gets
// ticked for tingling toes and one naming the monofilament test does not.
type Contraindication struct {
	Code       string `json:"code"`
	NameEN     string `json:"name_en"`
	NameBN     string `json:"name_bn"`
	QuestionEN string `json:"question_en"`
	QuestionBN string `json:"question_bn"`
	// FromObservation is where the record may already know the answer, so a station can pre-fill
	// rather than ask again. Empty when only a person can say.
	FromObservation string `json:"from_observation,omitempty"`
	Ordering        int    `json:"ordering"`
}

// Exercise is one row of the library, as it is offered and as it is printed.
type Exercise struct {
	Code   string `json:"code"`
	NameEN string `json:"name_en"`
	NameBN string `json:"name_bn"`
	// HowEN and HowBN are what the patient is told to do, in their own language, because this is
	// what gets printed and handed to them (criterion 3).
	HowEN string `json:"how_en"`
	HowBN string `json:"how_bn"`

	Kind      string `json:"kind"`
	Intensity string `json:"intensity"`
	Impact    string `json:"impact"`

	NeedsEquipment bool `json:"needs_equipment"`
	CanDoAtHome    bool `json:"can_do_at_home"`

	// Approved is false on everything seeded. The plan names the library content as an open
	// clinical decision, and this is what stops a starter list being mistaken for the clinic's
	// agreed one.
	Approved   bool       `json:"approved"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	Ordering   int        `json:"ordering"`
}

// Exclusion is one reason the offered list is shorter than the library.
//
// Named by condition, never by exercise. The count is how many the condition accounts for, and
// the counts overlap on purpose — jogging is excluded by neuropathy and by an open ulcer, and
// reporting it under both is what makes each reason true on its own.
type Exclusion struct {
	Code   string `json:"code"`
	NameEN string `json:"name_en"`
	NameBN string `json:"name_bn"`
	// Status is APPLIES — a finding about this patient — or NOT_ASKED, a question somebody still
	// has to put. Reported apart because the operator's next act is different for each, and
	// because folding them together would tell an operator that a patient has a condition nobody
	// has asked them about.
	Status   string `json:"status"`
	Excluded int    `json:"excluded"`
}

// The two kinds of exclusion.
const (
	// ExclusionApplies is a condition recorded as true of this patient.
	ExclusionApplies = "APPLIES"
	// ExclusionNotAsked is a condition added to the catalogue since this assessment was taken.
	// It excludes, because absence of evidence is not evidence of absence — and the screen says
	// so, so that the narrowing is visible rather than silent.
	ExclusionNotAsked = "NOT_ASKED"
)

// Options is what a station may offer this patient, and why it is not more.
type Options struct {
	AssessmentID uuid.UUID `json:"assessment_id"`
	// Exercises is the permitted set. Never `omitempty`: an empty list is a real answer, and a
	// client that saw the key vanish would not know whether the filter excluded everything or the
	// request failed.
	Exercises []Exercise `json:"exercises"`
	// LibrarySize is how many exercises exist, so a short list can say it is short on purpose.
	LibrarySize int `json:"library_size"`
	// Excluded is LibrarySize minus what is offered — the honest total, which is never the sum of
	// the per-condition counts because those overlap.
	Excluded int `json:"excluded"`
	// Reasons is why, by condition.
	Reasons []Exclusion `json:"reasons"`
}

// Assessment is what station 8 found.
type Assessment struct {
	ID        uuid.UUID  `json:"id"`
	PatientID uuid.UUID  `json:"patient_id"`
	VisitID   *uuid.UUID `json:"visit_id,omitempty"`

	WalksUnaided *bool  `json:"walks_unaided,omitempty"`
	WalkMinutes  *int   `json:"walk_minutes,omitempty"`
	JointPain    string `json:"joint_pain,omitempty"`

	// Contraindications is the set that applies, and Asked is the set that was put to the
	// patient. Neither is ever `omitempty`: "none apply" is the fact the whole filter turns on,
	// and a missing key reads as "not asked" — which is precisely the distinction these two
	// fields exist to draw.
	Contraindications []string `json:"contraindications"`
	Asked             []string `json:"asked"`

	Note   string `json:"note,omitempty"`
	Status string `json:"status"`

	RecordedAt   time.Time  `json:"recorded_at"`
	RecordedBy   uuid.UUID  `json:"recorded_by"`
	RecordedRole string     `json:"recorded_role,omitempty"`
	StationCode  string     `json:"station_code,omitempty"`
	DeviceID     *uuid.UUID `json:"device_id,omitempty"`
	Source       string     `json:"source,omitempty"`

	// [R-03]: who entered this, without digging.
	RecordedByCode   string `json:"recorded_by_code,omitempty"`
	RecordedByNameEN string `json:"recorded_by_name_en,omitempty"`
	RecordedByNameBN string `json:"recorded_by_name_bn,omitempty"`
}

// Live reports whether this is the assessment a plan would be filtered against.
func (a Assessment) Live() bool { return a.Status == "ACTIVE" }

// Target is one exercise and how much of it, in the two numbers §12.1 compares across visits.
// "Walk more" is not a target; three times a week for twenty minutes is.
type Target struct {
	ExerciseCode      string `json:"exercise_code"`
	TimesPerWeek      int    `json:"times_per_week"`
	MinutesPerSession int    `json:"minutes_per_session"`
	Ordering          int    `json:"ordering,omitempty"`
	Note              string `json:"note,omitempty"`
}

// Item is a target as it is read back, carrying the wording that gets printed.
//
// The instruction is joined from the library rather than copied onto the row, so that a correction
// to the Bangla reaches a sheet reprinted tomorrow.
type Item struct {
	Target
	NameEN string `json:"name_en"`
	NameBN string `json:"name_bn"`
	// Never omitempty: the contract marks these required, because this is the sheet the patient
	// is handed. An empty string would vanish from the body and violate the schema the generated
	// client trusts — unreachable today (invariant 88 refuses empty wording, and the plan item's
	// foreign key stops the library row being deleted) but a latent mismatch either way.
	HowEN string `json:"how_en"`
	HowBN string `json:"how_bn"`

	Kind      string `json:"kind,omitempty"`
	Intensity string `json:"intensity,omitempty"`
	Impact    string `json:"impact,omitempty"`

	NeedsEquipment bool `json:"needs_equipment"`
	CanDoAtHome    bool `json:"can_do_at_home"`

	Approved bool `json:"approved"`

	// MinutesPerWeek is the derived number, computed once on the server so that a screen, a
	// printed sheet and §12.1's extract cannot each round it differently.
	MinutesPerWeek int `json:"minutes_per_week"`
}

// Plan is what the patient was given.
type Plan struct {
	ID        uuid.UUID  `json:"id"`
	PatientID uuid.UUID  `json:"patient_id"`
	VisitID   *uuid.UUID `json:"visit_id,omitempty"`
	// AssessmentID is frozen: the findings this plan was filtered against, not the findings that
	// are true now.
	AssessmentID uuid.UUID `json:"assessment_id"`

	Status string `json:"status"`
	Note   string `json:"note,omitempty"`

	Items []Item `json:"items"`

	IssuedAt    time.Time  `json:"issued_at"`
	IssuedBy    uuid.UUID  `json:"issued_by"`
	IssuedRole  string     `json:"issued_role,omitempty"`
	StationCode string     `json:"station_code,omitempty"`
	DeviceID    *uuid.UUID `json:"device_id,omitempty"`
	Source      string     `json:"source,omitempty"`

	IssuedByCode   string `json:"issued_by_code,omitempty"`
	IssuedByNameEN string `json:"issued_by_name_en,omitempty"`
	IssuedByNameBN string `json:"issued_by_name_bn,omitempty"`

	// MinutesPerWeek is the plan's total, which is the number a follow-up visit compares against
	// the week before.
	MinutesPerWeek int `json:"minutes_per_week"`
}

// Live reports whether this is the plan the patient is currently on.
func (p Plan) Live() bool { return p.Status == "ACTIVE" }

// Store reads the library and the read model.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewStore builds one.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: dbgen.New(pool)} }

// Contraindications is the catalogue station 8 works through.
func (s *Store) Contraindications(ctx context.Context) ([]Contraindication, error) {
	rows, err := s.q.Contraindications(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Contraindication, 0, len(rows))
	for _, row := range rows {
		c := Contraindication{
			Code: row.Code, NameEN: row.NameEn, NameBN: row.NameBn,
			QuestionEN: row.QuestionEn, QuestionBN: row.QuestionBn,
			Ordering: int(row.Ordering),
		}
		if row.FromObservation != nil {
			c.FromObservation = *row.FromObservation
		}
		out = append(out, c)
	}
	return out, nil
}

// Options is the permitted set for one patient, with the count and the reasons.
//
// This is the only method that returns exercises for a named patient, and it is the reason the
// filter lives in `core.exercises_permitted` rather than in Go: the physician dashboard and the
// research extract read the same function, so "contraindicated exercises are excluded" cannot
// quietly become "excluded on the station app".
func (s *Store) Options(ctx context.Context, patient, facility uuid.UUID) (Options, error) {
	assessment, err := s.LiveAssessment(ctx, patient, facility)
	if err != nil {
		return Options{}, err
	}

	permitted, err := s.q.PermittedExercises(ctx, dbgen.PermittedExercisesParams{
		PatientID: patient, FacilityID: facility,
	})
	if err != nil {
		return Options{}, err
	}
	size, err := s.q.LibrarySize(ctx)
	if err != nil {
		return Options{}, err
	}
	reasons, err := s.q.ExclusionReasons(ctx, assessment.ID)
	if err != nil {
		return Options{}, err
	}

	out := Options{
		AssessmentID: assessment.ID,
		Exercises:    make([]Exercise, 0, len(permitted)),
		LibrarySize:  int(size),
		Reasons:      make([]Exclusion, 0, len(reasons)),
	}
	for _, row := range permitted {
		out.Exercises = append(out.Exercises, Exercise{
			Code: row.Code, NameEN: row.NameEn, NameBN: row.NameBn,
			HowEN: row.HowEn, HowBN: row.HowBn,
			Kind: row.Kind, Intensity: row.Intensity, Impact: row.Impact,
			NeedsEquipment: row.NeedsEquipment, CanDoAtHome: row.CanDoAtHome,
			Approved: row.ApprovedAt != nil, ApprovedAt: row.ApprovedAt,
			Ordering: int(row.Ordering),
		})
	}
	for _, row := range reasons {
		status := ExclusionNotAsked
		if row.Applies {
			status = ExclusionApplies
		}
		out.Reasons = append(out.Reasons, Exclusion{
			Code: row.Code, NameEN: row.NameEn, NameBN: row.NameBn,
			Status: status, Excluded: int(row.Excluded),
		})
	}
	out.Excluded = out.LibrarySize - len(out.Exercises)
	if out.Excluded < 0 {
		out.Excluded = 0
	}
	return out, nil
}

// LiveAssessment is the one a plan would be filtered against.
func (s *Store) LiveAssessment(ctx context.Context, patient, facility uuid.UUID) (Assessment, error) {
	row, err := s.q.LiveAssessment(ctx, dbgen.LiveAssessmentParams{
		PatientID: patient, FacilityID: facility,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Assessment{}, ErrNoAssessment
	}
	if err != nil {
		return Assessment{}, err
	}
	return assessmentOf(assessmentRow(row)), nil
}

// AssessmentByID reads one, scoped to the facility asking.
func (s *Store) AssessmentByID(ctx context.Context, id, facility uuid.UUID) (Assessment, error) {
	row, err := s.q.AssessmentByID(ctx, dbgen.AssessmentByIDParams{ID: id, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return Assessment{}, ErrNotFound
	}
	if err != nil {
		return Assessment{}, err
	}
	return assessmentOf(assessmentRow(row)), nil
}

// AssessmentsForPatient is the history. §12.1 compares a patient against themselves across
// visits, and a contraindication that resolved is as interesting as one that appeared.
func (s *Store) AssessmentsForPatient(ctx context.Context, patient, facility uuid.UUID,
	limit int) ([]Assessment, error) {

	rows, err := s.q.AssessmentsForPatient(ctx, dbgen.AssessmentsForPatientParams{
		PatientID: patient, FacilityID: facility, Limit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Assessment, 0, len(rows))
	for _, row := range rows {
		out = append(out, assessmentOf(assessmentRow(row)))
	}
	return out, nil
}

// LivePlan is the plan the patient is currently on, with its targets.
func (s *Store) LivePlan(ctx context.Context, patient, facility uuid.UUID) (Plan, error) {
	row, err := s.q.LivePlan(ctx, dbgen.LivePlanParams{PatientID: patient, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return Plan{}, ErrNotFound
	}
	if err != nil {
		return Plan{}, err
	}
	plan := planOf(planRow(row))
	items, err := s.itemsFor(ctx, []uuid.UUID{plan.ID})
	if err != nil {
		return Plan{}, err
	}
	plan.Items = items[plan.ID]
	plan.MinutesPerWeek = weeklyMinutes(plan.Items)
	return plan, nil
}

// PlansForPatient is the history, with the targets on every one, because comparing this visit's
// plan against the last is the whole point of structuring the targets.
func (s *Store) PlansForPatient(ctx context.Context, patient, facility uuid.UUID,
	limit int) ([]Plan, error) {

	rows, err := s.q.PlansForPatient(ctx, dbgen.PlansForPatientParams{
		PatientID: patient, FacilityID: facility, Limit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	plans := make([]Plan, 0, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		plan := planOf(planRow(row))
		plans = append(plans, plan)
		ids = append(ids, plan.ID)
	}
	if len(ids) == 0 {
		return plans, nil
	}
	// One query for every plan's items rather than one per plan: a patient with a year of visits
	// would otherwise cost a round trip each, on a screen a physician opens while somebody waits.
	items, err := s.itemsFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range plans {
		plans[i].Items = items[plans[i].ID]
		plans[i].MinutesPerWeek = weeklyMinutes(plans[i].Items)
	}
	return plans, nil
}

func (s *Store) itemsFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]Item, error) {
	rows, err := s.q.PlanItems(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID][]Item, len(ids))
	for _, id := range ids {
		out[id] = []Item{}
	}
	for _, row := range rows {
		out[row.PlanID] = append(out[row.PlanID], Item{
			Target: Target{
				ExerciseCode:      row.ExerciseCode,
				TimesPerWeek:      int(row.TimesPerWeek),
				MinutesPerSession: int(row.MinutesPerSession),
				Ordering:          int(row.Ordering),
				Note:              row.Note,
			},
			NameEN: row.NameEn, NameBN: row.NameBn, HowEN: row.HowEn, HowBN: row.HowBn,
			Kind: row.Kind, Intensity: row.Intensity, Impact: row.Impact,
			NeedsEquipment: row.NeedsEquipment, CanDoAtHome: row.CanDoAtHome,
			Approved:       row.ApprovedAt != nil,
			MinutesPerWeek: int(row.TimesPerWeek) * int(row.MinutesPerSession),
		})
	}
	return out, nil
}

// KnowsPatient is the facility scope check every read does before it says "not found".
func (s *Store) KnowsPatient(ctx context.Context, patient, facility uuid.UUID) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM core.patient WHERE id = $1 AND facility_id = $2)`,
		patient, facility).Scan(&exists)
	return exists, err
}

// LiveConditionCodes is the catalogue's codes, in catalogue order.
//
// Used for two things at once, and they are the same list on purpose: validating what a station
// sends, and deciding whether it asked everything. A code that is not in the catalogue is refused
// rather than stored — an assessment holding a typo'd condition would filter nothing and look
// exactly like one that filtered correctly — and an assessment that leaves one of these
// unanswered is refused too, because the filter cannot tell a skipped question from a negative
// answer unless the station says which it asked.
func (s *Store) LiveConditionCodes(ctx context.Context) ([]string, error) {
	rows, err := s.q.Contraindications(ctx)
	if err != nil {
		return nil, err
	}
	codes := make([]string, 0, len(rows))
	for _, row := range rows {
		codes = append(codes, row.Code)
	}
	return codes, nil
}

// InTransaction runs fn against a transaction on this store's pool.
func (s *Store) InTransaction(ctx context.Context,
	fn func(context.Context, pgx.Tx, *dbgen.Queries) error) error {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx, s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// weeklyMinutes is the plan's total, computed once here so that a screen, a printed sheet and
// §12.1's extract cannot each arrive at a different number.
func weeklyMinutes(items []Item) int {
	total := 0
	for _, item := range items {
		total += item.MinutesPerWeek
	}
	return total
}

// assessmentRow and planRow flatten the three sqlc row types that differ only in name, so that
// the mapping below is written once. sqlc generates a distinct struct per query even when the
// columns are identical, and three copies of this conversion is three places for a field to be
// forgotten.
type assessmentRow struct {
	ID                uuid.UUID
	PatientID         uuid.UUID
	VisitID           uuid.NullUUID
	WalksUnaided      *bool
	WalkMinutes       *int32
	JointPain         string
	Contraindications []string
	Asked             []string
	Note              string
	Status            string
	RecordedAt        time.Time
	RecordedBy        uuid.UUID
	RecordedRole      string
	StationCode       string
	DeviceID          uuid.NullUUID
	Source            string
	RecordedByCode    string
	RecordedByNameEn  string
	RecordedByNameBn  string
}

type planRow struct {
	ID             uuid.UUID
	PatientID      uuid.UUID
	VisitID        uuid.NullUUID
	AssessmentID   uuid.UUID
	Status         string
	Note           string
	IssuedAt       time.Time
	IssuedBy       uuid.UUID
	IssuedRole     string
	StationCode    string
	DeviceID       uuid.NullUUID
	Source         string
	IssuedByCode   string
	IssuedByNameEn string
	IssuedByNameBn string
}

func assessmentOf(row assessmentRow) Assessment {
	a := Assessment{
		ID: row.ID, PatientID: row.PatientID,
		JointPain: row.JointPain, Note: row.Note, Status: row.Status,
		WalksUnaided: row.WalksUnaided,
		// Never nil in JSON: "none recorded" is the fact the filter turns on.
		Contraindications: append([]string{}, row.Contraindications...),
		Asked:             append([]string{}, row.Asked...),
		RecordedAt:        row.RecordedAt, RecordedBy: row.RecordedBy,
		RecordedRole: row.RecordedRole, StationCode: row.StationCode, Source: row.Source,
		RecordedByCode:   row.RecordedByCode,
		RecordedByNameEN: row.RecordedByNameEn, RecordedByNameBN: row.RecordedByNameBn,
	}
	if row.VisitID.Valid {
		id := row.VisitID.UUID
		a.VisitID = &id
	}
	if row.DeviceID.Valid {
		id := row.DeviceID.UUID
		a.DeviceID = &id
	}
	if row.WalkMinutes != nil {
		minutes := int(*row.WalkMinutes)
		a.WalkMinutes = &minutes
	}
	return a
}

func planOf(row planRow) Plan {
	p := Plan{
		ID: row.ID, PatientID: row.PatientID, AssessmentID: row.AssessmentID,
		Status: row.Status, Note: row.Note,
		IssuedAt: row.IssuedAt, IssuedBy: row.IssuedBy, IssuedRole: row.IssuedRole,
		StationCode: row.StationCode, Source: row.Source,
		IssuedByCode:   row.IssuedByCode,
		IssuedByNameEN: row.IssuedByNameEn, IssuedByNameBN: row.IssuedByNameBn,
		Items: []Item{},
	}
	if row.VisitID.Valid {
		id := row.VisitID.UUID
		p.VisitID = &id
	}
	if row.DeviceID.Valid {
		id := row.DeviceID.UUID
		p.DeviceID = &id
	}
	return p
}
