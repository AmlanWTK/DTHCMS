package education

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Store reads the station's reference data and what earlier visits recorded.
//
// It writes nothing. Every value this station produces goes through `clinical.Service`, which
// is the only write path for an observation in this system — so there is no method here that
// could put a row in `read.observation`, and `dthcms_app` holds no grant that would let one.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: dbgen.New(pool)} }

// ---------------------------------------------------------------------------
// Reference data
// ---------------------------------------------------------------------------

// Reference is everything a station app needs before the patient sits down.
//
// Assembled here rather than in the handler because the assembly is the interesting part: the
// checklists and their items arrive as two flat result sets and have to be stitched, and a
// handler that did the stitching would be a second place that knows an item belongs to a
// checklist.
func (s *Store) Reference(ctx context.Context) (Reference, error) {
	out := Reference{
		ComplianceEN: ComplianceQuestionEN,
		ComplianceBN: ComplianceQuestionBN,
	}

	deviceRows, err := s.q.EducationDeviceTypes(ctx)
	if err != nil {
		return Reference{}, err
	}
	for _, row := range deviceRows {
		out.DeviceTypes = append(out.DeviceTypes, DeviceType{
			Code: row.Code, NameEN: row.NameEn, NameBN: row.NameBn, Ordering: int(row.Ordering),
		})
	}

	itemRows, err := s.q.EducationChecklistItems(ctx)
	if err != nil {
		return Reference{}, err
	}
	byChecklist := map[string][]Item{}
	for _, row := range itemRows {
		byChecklist[row.ChecklistCode] = append(byChecklist[row.ChecklistCode], Item{
			Ordinal: int(row.Ordinal), Code: row.ObservationCode,
			TextEN: row.TextEn, TextBN: row.TextBn, Critical: row.IsCritical,
		})
	}

	checklistRows, err := s.q.EducationChecklists(ctx)
	if err != nil {
		return Reference{}, err
	}
	for _, row := range checklistRows {
		out.Checklists = append(out.Checklists, Checklist{
			Code: row.Code, DeviceType: row.DeviceType,
			TitleEN: row.TitleEn, TitleBN: row.TitleBn,
			// An empty item list here would be a checklist that records nothing while looking
			// like a working one. Invariant 133 refuses that state at the database, so an
			// empty slice below means the invariant is not running, not that the content is
			// optional.
			Items: byChecklist[row.Code],
		})
	}

	stateRows, err := s.q.EducationItemStates(ctx)
	if err != nil {
		return Reference{}, err
	}
	for _, row := range stateRows {
		out.States = append(out.States, AnswerOption{
			State: State(row.ValueCode), DisplayEN: row.DisplayEn,
			DisplayBN: row.DisplayBn, Ordering: int(row.Ordering),
		})
	}

	scale, err := s.Scale(ctx)
	if err != nil {
		return Reference{}, err
	}
	out.Scale = scale

	naRows, err := s.q.ProNotApplicableReasons(ctx)
	if err != nil {
		return Reference{}, err
	}
	for _, row := range naRows {
		out.NotAskedWhy = append(out.NotAskedWhy, CodedOption{
			Code: row.Code, DisplayEN: row.DisplayEn,
			DisplayBN: row.DisplayBn, Ordering: int(row.Ordering),
		})
	}

	missRows, err := s.q.MedicationMissReasons(ctx)
	if err != nil {
		return Reference{}, err
	}
	for _, row := range missRows {
		out.MissReasons = append(out.MissReasons, CodedOption{
			Code: row.Code, DisplayEN: row.DisplayEn,
			DisplayBN: row.DisplayBn, Ordering: int(row.Ordering),
		})
	}

	policy, err := s.Policy(ctx)
	if err != nil {
		return Reference{}, err
	}
	out.Policy = policy
	return out, nil
}

// Scale is the live scale for the improvement score, with its bands.
func (s *Store) Scale(ctx context.Context) (Scale, error) {
	row, err := s.q.ProScale(ctx, ScoreCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Not a nil scale silently returned. A station with no live scale has no question
			// to read aloud, which from the floor looks like the station simply not asking —
			// and invariant 132 already refuses this state, so reaching it means the invariant
			// is not being run.
			return Scale{}, errors.New("education: there is no live improvement scale; see invariant 132")
		}
		return Scale{}, err
	}
	scale := Scale{
		Code: row.Code, QuestionEN: row.QuestionEn, QuestionBN: row.QuestionBn,
		MinValue: int(row.MinValue), MaxValue: int(row.MaxValue),
		NeutralValue: int(row.NeutralValue),
	}
	anchors, err := s.q.ProScaleAnchors(ctx, row.Code)
	if err != nil {
		return Scale{}, err
	}
	for _, anchor := range anchors {
		scale.Anchors = append(scale.Anchors, Anchor{
			FromValue: int(anchor.FromValue), ToValue: int(anchor.ToValue),
			LabelEN: anchor.LabelEn, LabelBN: anchor.LabelBn,
			FaceRank: int(anchor.FaceRank), Ordering: int(anchor.Ordering),
		})
	}
	return scale, nil
}

// Policy is the threshold at which technique is called poor.
func (s *Store) Policy(ctx context.Context) (Policy, error) {
	row, err := s.q.EducationReeducationPolicy(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Refused rather than defaulted. A default here would be a threshold nobody chose,
			// applied to a clinical judgement, and it would be invisible — the flag would fire
			// or not fire and nothing on any screen would say which rule it used.
			return Policy{}, errors.New("education: there is no re-education policy; see invariant 133")
		}
		return Policy{}, err
	}
	return Policy{
		UnableRaisesFlag:        row.UnableRaisesFlag,
		CorrectedTodayThreshold: int(row.CorrectedTodayThreshold),
	}, nil
}

// ItemIndex is every live checklist item by its observation code, for validating an assessment.
func (s *Store) ItemIndex(ctx context.Context) (map[string]Item, map[string]string, error) {
	rows, err := s.q.EducationChecklistItems(ctx)
	if err != nil {
		return nil, nil, err
	}
	items := make(map[string]Item, len(rows))
	checklistOf := make(map[string]string, len(rows))
	for _, row := range rows {
		items[row.ObservationCode] = Item{
			Ordinal: int(row.Ordinal), Code: row.ObservationCode,
			TextEN: row.TextEn, TextBN: row.TextBn, Critical: row.IsCritical,
		}
		checklistOf[row.ObservationCode] = row.ChecklistCode
	}
	return items, checklistOf, nil
}

// MissReasonCodes is the vocabulary as a set, for validating an answer.
func (s *Store) MissReasonCodes(ctx context.Context) (map[string]bool, error) {
	rows, err := s.q.MedicationMissReasons(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		out[row.Code] = true
	}
	return out, nil
}

// NotApplicableCodes is the vocabulary of reasons the question did not apply.
func (s *Store) NotApplicableCodes(ctx context.Context) (map[string]bool, error) {
	rows, err := s.q.ProNotApplicableReasons(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		out[row.Code] = true
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// The selection
// ---------------------------------------------------------------------------

// ChecklistsForProducts is CP92's first acceptance criterion: which checklists these prescribed
// products bring up.
//
// Takes product ids rather than a prescription id, so that the selection rule is a pure function
// of the formulary and the rule table and can be exercised without a prescription — which is
// what lets the mutation test break the mapping and see a failure, rather than break the mapping
// and watch a whole fixture fall over for unrelated reasons.
func (s *Store) ChecklistsForProducts(ctx context.Context,
	facility uuid.UUID, products []uuid.UUID) (map[uuid.UUID][]checklistMatch, error) {

	out := map[uuid.UUID][]checklistMatch{}
	if len(products) == 0 {
		return out, nil
	}
	rows, err := s.q.EducationChecklistsForProducts(ctx, dbgen.EducationChecklistsForProductsParams{
		ProductIds: products, FacilityID: facility,
	})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ProductID] = append(out[row.ProductID],
			checklistMatch{Checklist: row.ChecklistCode, DeviceType: row.DeviceType})
	}
	return out, nil
}

// checklistMatch is one rule's verdict on one product.
type checklistMatch struct {
	Checklist  string
	DeviceType string
}

// UnclassifiedFor is the prescribed products this station ought to recognise and does not.
//
// Separate from [Store.ChecklistsForProducts] rather than folded into it, because the two answer
// different questions and only one of them is an alarm: "which checklists" is the ordinary path,
// and "which of these should have brought one up and did not" is a gap somebody has to close.
// A single query returning both would make the gap a value a caller has to remember to look at.
func (s *Store) UnclassifiedFor(ctx context.Context,
	facility uuid.UUID, products []uuid.UUID) ([]UnclassifiedDevice, error) {

	if len(products) == 0 {
		return nil, nil
	}
	rows, err := s.q.EducationUnclassifiedDevices(ctx, dbgen.EducationUnclassifiedDevicesParams{
		ProductIds: products, FacilityID: facility,
	})
	if err != nil {
		return nil, err
	}
	out := make([]UnclassifiedDevice, 0, len(rows))
	for _, row := range rows {
		out = append(out, UnclassifiedDevice{
			ProductID: row.ProductID, GenericName: row.GenericName,
			ClassCode: row.ClassCode, ClassNameEN: row.ClassNameEn,
			ClassNameBN: row.ClassNameBn,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// What earlier visits recorded
// ---------------------------------------------------------------------------

// LatestCompetency is the most recent state of every checklist item for one patient.
func (s *Store) LatestCompetency(ctx context.Context,
	patient, facility uuid.UUID) ([]Competency, error) {

	rows, err := s.q.EducationLatestCompetency(ctx, dbgen.EducationLatestCompetencyParams{
		PatientID: patient, FacilityID: facility,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Competency, 0, len(rows))
	for _, row := range rows {
		entry := Competency{
			Code: row.Code, State: State(row.ValueCode),
			ChecklistCode: row.ChecklistCode, Ordinal: int(row.Ordinal),
			TextEN: row.TextEn, TextBN: row.TextBn, Critical: row.IsCritical,
			ObservedAt:   row.EffectiveAt.Format(time.RFC3339),
			RecordedBy:   row.RecordedBy,
			RecordedRole: row.RecordedRole,
			RecordedAt:   row.RecordedAt.Format(time.RFC3339),
			StationCode:  row.StationCode,
			Source:       row.Source,
		}
		if row.VisitID.Valid {
			entry.VisitID = row.VisitID.UUID
		}
		out = append(out, entry)
	}
	return out, nil
}

// FlagStanding is whether a re-education flag is raised from the most recent assessment.
//
// The most recent, not "any ever": a patient who was flagged two years ago and has demonstrated
// correctly since is not a patient who needs re-educating, and a flag that never came down would
// be one the physician learned to ignore.
func (s *Store) FlagStanding(ctx context.Context, patient, facility uuid.UUID) (bool, error) {
	row, err := s.q.EducationLatestFlag(ctx, dbgen.EducationLatestFlagParams{
		PatientID: patient, FacilityID: facility,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return row.ValueBool != nil && *row.ValueBool, nil
}

// ImprovementFor is this visit's answer to §2's question.
//
// Three outcomes and they are genuinely three: a score, a recorded not-applicable, and nobody
// has asked. The last is the zero Answer, and the caller can tell it apart because `Answered`
// reports false — which is the whole reason `Answer`'s fields are unexported.
func (s *Store) ImprovementFor(ctx context.Context,
	patient, facility, visit uuid.UUID) (Answer, error) {

	rows, err := s.q.ImprovementAnswerForVisit(ctx, dbgen.ImprovementAnswerForVisitParams{
		PatientID: patient, FacilityID: facility, VisitID: visit,
	})
	if err != nil {
		return Answer{}, err
	}
	for _, row := range rows {
		switch row.Code {
		case ScoreCode:
			if value, ok := wholeNumber(row.ValueNum); ok {
				return Scored(value), nil
			}
		case NotApplicableCode:
			if row.ValueCode != "" {
				return NotAsked(row.ValueCode), nil
			}
		}
	}
	return Answer{}, nil
}

// ImprovementRecordFor is this visit's answer with the person who asked it against it.
//
// A second method rather than a second return value from [Store.ImprovementFor], because the two
// have different readers and only one of them needs the asker: the officer's screen seeds a
// control from the value, and the physician's screen is reading a claim about who made it.
func (s *Store) ImprovementRecordFor(ctx context.Context,
	patient, facility, visit uuid.UUID) (*RecordedImprovement, error) {

	rows, err := s.q.ImprovementAnswerForVisit(ctx, dbgen.ImprovementAnswerForVisitParams{
		PatientID: patient, FacilityID: facility, VisitID: visit,
	})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var answer Answer
		switch row.Code {
		case ScoreCode:
			value, ok := wholeNumber(row.ValueNum)
			if !ok {
				continue
			}
			answer = Scored(value)
		case NotApplicableCode:
			if row.ValueCode == "" {
				continue
			}
			answer = NotAsked(row.ValueCode)
		default:
			continue
		}
		return &RecordedImprovement{
			Answer: answer,
			RecordedAnswer: RecordedAnswer{
				RecordedBy: row.RecordedBy, RecordedRole: row.RecordedRole,
				RecordedAt:  row.RecordedAt.Format(time.RFC3339),
				EffectiveAt: row.EffectiveAt.Format(time.RFC3339),
				StationCode: row.StationCode, Source: row.Source,
			},
		}, nil
	}
	return nil, nil
}

// ComplianceFor is §7's answer at this visit, or nil when nobody asked.
//
// Nil and not a zeroed struct. "Nobody asked", "they said none" and "they said three" are three
// facts, and the one a screen must never invent is the first drawn as the second — a patient
// nobody asked rendered as a patient with perfect adherence.
func (s *Store) ComplianceFor(ctx context.Context,
	patient, facility, visit uuid.UUID) (*RecordedCompliance, error) {

	rows, err := s.q.EducationComplianceForVisit(ctx, dbgen.EducationComplianceForVisitParams{
		PatientID: patient, FacilityID: facility, VisitID: visit,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := &RecordedCompliance{Reasons: []string{}}
	for _, row := range rows {
		switch row.Code {
		case MissedDosesCode:
			if value, ok := wholeNumber(row.ValueNum); ok {
				out.MissedDoses = &value
			}
		case MissReasonCode:
			if row.ValueCode != "" {
				out.Reasons = append(out.Reasons, row.ValueCode)
			}
		}
		// The attribution of whichever row came last. They are written in one transaction by one
		// officer, so there is one answer here and taking it from any row is taking it from all
		// of them — a per-row attribution would suggest the count and the reason could have come
		// from two people, which the write path makes impossible.
		out.RecordedAnswer = RecordedAnswer{
			RecordedBy: row.RecordedBy, RecordedRole: row.RecordedRole,
			RecordedAt:  row.RecordedAt.Format(time.RFC3339),
			EffectiveAt: row.EffectiveAt.Format(time.RFC3339),
			StationCode: row.StationCode, Source: row.Source,
		}
	}
	return out, nil
}

// HadAnEarlierVisit reports whether this patient has been here before this visit.
//
// §2: for a first visit there is no last visit, so the score is not asked. The server decides
// this rather than the screen, because a screen inferring it from an empty observation history
// would call every patient with nothing recorded a first visit — including the follow-up whose
// first visit predates this system.
func (s *Store) HadAnEarlierVisit(ctx context.Context,
	patient, facility, visit uuid.UUID) (bool, error) {

	return s.q.EducationEarlierVisitExists(ctx, dbgen.EducationEarlierVisitExistsParams{
		PatientID: patient, FacilityID: facility, VisitID: visit,
	})
}

// wholeNumber reads a stored numeric as the small integer the scale guarantees it is.
//
// The score's plausibility band is 1-10 and the database enforces it, so a value that reaches
// here is an integer or the record is already wrong in a way this function cannot fix. Rounding
// rather than truncating, because a stored 7.0000001 from some future unit conversion should
// read as 7 rather than as 6 — a score that drifted down by one on the way out would be the
// quietest possible corruption of the one number this checkpoint exists to make trustworthy.
func wholeNumber(n pgtype.Numeric) (int, bool) {
	if !n.Valid {
		return 0, false
	}
	value, err := n.Float64Value()
	if err != nil || !value.Valid {
		return 0, false
	}
	return int(math.Round(value.Float64)), true
}

// RecordedInVisit reports whether station 11 wrote anything for this patient on this visit.
//
// # Why this is a boolean and not a list of what was taught
//
// CP83's rule 10 asks one question — *"was a first insulin pen handed over with nobody watching
// the patient use it"* — and the answer is whether the station happened at all. A list of items
// would invite a QA rule about *which* items, which would be this station's checklist duplicated
// in the QA rule table, and `docs/qa-rules.md` §4 is explicit that QA checks the file is complete
// rather than second-guessing what was done.
//
// "Wrote anything" means any coded value this module records under `education.record`. A session
// opened and abandoned leaves nothing, which is the right answer: the officer met the patient and
// did not get through it.
func (s *Store) RecordedInVisit(ctx context.Context, patient, visit, facility uuid.UUID) (bool, error) {
	var found bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM read.observation o
		   WHERE o.patient_id = $1 AND o.visit_id = $2 AND o.facility_id = $3
		     AND o.status = 'ACTIVE'
		     AND EXISTS (SELECT 1 FROM core.education_checklist_item i
		                  WHERE i.observation_code = o.code))`,
		patient, visit, facility).Scan(&found)
	return found, err
}
