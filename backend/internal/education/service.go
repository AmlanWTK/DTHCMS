package education

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Service assembles the station's screen and records what happened at it.
//
// # Why it writes through `clinical` rather than to a table
//
// Both checkpoints say it in the plan, in the same words: *"Uses the CP42 observation tables; no
// new schema."* An education module with its own results table would have its own event type,
// its own projection, its own correction path and its own absence from the timeline and the
// research extract — four things to build and four things to keep in step, for values that are
// already exactly what an observation is. So the station's output goes through the one write
// path, and everything downstream gets it for free.
type Service struct {
	store  *Store
	values *clinical.Service
	sheets Sheets
	clock  interface{ Now() time.Time }
}

// Sheets is the prescription a patient was given, reduced to the only thing this station needs
// from it: which products are on it.
//
// An interface rather than an import. `education` has no business knowing what a prescription
// status machine is, and `prescription` has no business knowing this station exists — a clinic
// that removed the education station would not change a line of that module. The composition
// root holds the one adapter.
type Sheets interface {
	// PrescribedDevices is the live lines of this visit's prescription, in line order. A visit
	// with no prescription yet returns an empty slice and no error: a patient who reaches the
	// education station before the sheet is signed is early, not broken.
	PrescribedDevices(ctx context.Context, patient, visit, facility uuid.UUID) ([]PrescribedLine, error)
}

// PrescribedLine is one line of the sheet, as this station reads it.
type PrescribedLine struct {
	// ProductID is nil for a line typed as free text rather than picked from the formulary.
	// Such a line selects no checklist, and that is the honest answer rather than a guess: a
	// product this clinic does not stock is a product nobody has classified, and inferring
	// "pen" from the words on it is exactly the name-matching this module refuses to do.
	ProductID    *uuid.UUID
	Label        string
	GenericName  string
	DurationDays *int
}

func NewService(store *Store, values *clinical.Service, sheets Sheets,
	clk interface{ Now() time.Time }) *Service {

	return &Service{store: store, values: values, sheets: sheets, clock: clk}
}

// ---------------------------------------------------------------------------
// The screen
// ---------------------------------------------------------------------------

// Session is what the officer sees when the patient sits down, and what the physician sees at
// the next visit.
//
// One method for both readers, because they want the same facts: which devices this patient is
// on, what they could do last time, and whether anybody has flagged them. What differs is the
// permission that reaches it, and that is the route's decision rather than this one's — a method
// that returned different fields to different roles would be a redaction rule hidden in a
// service, and CP20 put redaction in the serialiser for exactly that reason.
func (s *Service) Session(ctx context.Context, patient, visit, facility uuid.UUID) (Session, error) {
	out := Session{PatientID: patient, VisitID: visit}

	lines, err := s.sheets.PrescribedDevices(ctx, patient, visit, facility)
	if err != nil {
		return Session{}, err
	}
	products := make([]uuid.UUID, 0, len(lines))
	for _, line := range lines {
		if line.ProductID != nil {
			products = append(products, *line.ProductID)
		}
	}
	matches, err := s.store.ChecklistsForProducts(ctx, facility, products)
	if err != nil {
		return Session{}, err
	}
	// The gap, read in the same breath as the selection. A line this station ought to recognise
	// and does not is not an empty result — it is the one an officer has to be told about.
	if out.Unclassified, err = s.store.UnclassifiedFor(ctx, facility, products); err != nil {
		return Session{}, err
	}

	// Which checklists, and which line brought each one up. The officer is owed the second
	// half: criterion 1 is that the checklist appears with no manual selection, and a checklist
	// somebody did not choose and cannot explain is one they will work around.
	selected := map[string]bool{}
	for _, line := range lines {
		if line.ProductID == nil {
			continue
		}
		for _, match := range matches[*line.ProductID] {
			selected[match.Checklist] = true
			out.Devices = append(out.Devices, SelectedDevice{
				ChecklistCode: match.Checklist, DeviceType: match.DeviceType,
				ProductID: *line.ProductID, ProductLabel: line.Label,
				GenericName: line.GenericName,
			})
		}
	}

	reference, err := s.store.Reference(ctx)
	if err != nil {
		return Session{}, err
	}
	for _, checklist := range reference.Checklists {
		if selected[checklist.Code] {
			out.Checklists = append(out.Checklists, checklist)
		}
	}
	// Device order, which is the order the reference data declares and therefore the order the
	// physician authored. A patient on a pen and a meter works through the pen first because
	// that is the one that costs them their dose if it is wrong.
	sort.SliceStable(out.Devices, func(i, j int) bool {
		return deviceOrder(reference, out.Devices[i].DeviceType) <
			deviceOrder(reference, out.Devices[j].DeviceType)
	})

	if out.Improvement, err = s.store.ImprovementFor(ctx, patient, facility, visit); err != nil {
		return Session{}, err
	}
	if out.ImprovementRecord, err = s.store.ImprovementRecordFor(ctx, patient, facility, visit); err != nil {
		return Session{}, err
	}
	if out.ComplianceRecord, err = s.store.ComplianceFor(ctx, patient, facility, visit); err != nil {
		return Session{}, err
	}
	earlier, err := s.store.HadAnEarlierVisit(ctx, patient, facility, visit)
	if err != nil {
		return Session{}, err
	}
	out.FirstVisit = !earlier

	if out.PriorCompetency, err = s.store.LatestCompetency(ctx, patient, facility); err != nil {
		return Session{}, err
	}
	if out.ReeducationFlagged, err = s.store.FlagStanding(ctx, patient, facility); err != nil {
		return Session{}, err
	}
	return out, nil
}

func deviceOrder(reference Reference, code string) int {
	for _, device := range reference.DeviceTypes {
		if device.Code == code {
			return device.Ordering
		}
	}
	// An unknown device sorts last rather than first. It should be unreachable — the checklist
	// join names a live device type — and if it ever is reached, the wrong answer that puts an
	// unclassified thing at the *top* of an officer's list is worse than the one that puts it at
	// the bottom.
	return 1 << 30
}

// ---------------------------------------------------------------------------
// The write
// ---------------------------------------------------------------------------

// Result is what the station recorded, and what it concluded.
type Result struct {
	Observations []clinical.Observation `json:"observations"`
	// Reeducation is the flag as it was computed and stored. Returned so the screen shows the
	// record's answer rather than its own — the arithmetic is [Policy.Raises] and it runs here,
	// once, so that a flag an officer saw is a flag the physician will see.
	Reeducation bool `json:"reeducation_flagged"`
	// Unable and CorrectedToday are the tally the flag was computed from, so an officer can see
	// why it fired — or why it did not, which is the question they will actually ask.
	Unable         int `json:"unable"`
	CorrectedToday int `json:"corrected_today"`
}

// Record writes one assessment: the technique items, the compliance answer and the improvement
// score, in one transaction.
//
// # What is refused, and what is not
//
// A partial assessment is fine. An officer who ran out of time has recorded what they saw, and a
// service that insisted on all ten items would make the record less true rather than more —
// there is no state here that means "I did not get to this", so the absence of a row is it.
//
// What is refused is an answer against a checklist the prescription did not select. A pen
// checklist filled in for a patient on tablets alone is either a mis-tap or a record about
// somebody else, and both are worth a sentence in front of the officer while the patient is
// still there.
func (s *Service) Record(ctx context.Context, in Assessment) (Result, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Result{}, err
	}
	if in.EventID == uuid.Nil {
		return Result{}, fmt.Errorf("%w: an assessment needs its own event id", ErrAnswerShape)
	}

	session, err := s.Session(ctx, in.PatientID, in.VisitID, actor.FacilityID())
	if err != nil {
		return Result{}, err
	}
	selected := map[string]bool{}
	for _, checklist := range session.Checklists {
		selected[checklist.Code] = true
	}
	items, checklistOf, err := s.store.ItemIndex(ctx)
	if err != nil {
		return Result{}, err
	}

	records := make([]clinical.Recording, 0, len(in.Items)+4)
	var unable, correctedToday int
	for _, answer := range in.Items {
		if _, known := items[answer.Code]; !known {
			return Result{}, fmt.Errorf("%w: %s", ErrUnknownItem, answer.Code)
		}
		if !answer.State.Valid() {
			return Result{}, fmt.Errorf("%w: %q is not one of demonstrated, corrected_today, unable",
				ErrAnswerShape, answer.State)
		}
		if !selected[checklistOf[answer.Code]] {
			return Result{}, fmt.Errorf("%w: %s belongs to %s",
				ErrNotSelected, answer.Code, checklistOf[answer.Code])
		}
		switch answer.State {
		case Unable:
			unable++
		case CorrectedToday:
			correctedToday++
		case Demonstrated:
		}
		records = append(records, s.coded(in, answer.Code, string(answer.State)))
	}

	if in.Compliance.MissedDoses != nil {
		// A count with no reasons behind it is accepted rather than refused. §7 asks for the
		// reason *if any were missed*, and a patient who will not say why has told the officer
		// something real — refusing the write would turn that into a record saying they were
		// never asked.
		missed := float64(*in.Compliance.MissedDoses)
		records = append(records, clinical.Recording{
			EventID: itemEventID(in.EventID, MissedDosesCode), PatientID: in.PatientID,
			VisitID: &in.VisitID, Code: MissedDosesCode,
			Value: &missed, Unit: "1",
			EffectiveAt: s.clock.Now(),
			// PATIENT rather than STATION. What the operator measured is the technique; what
			// the patient said about last week is their report, and a physician deciding
			// whether an HbA1c is a dose problem or an adherence problem needs to know which of
			// those they are reading.
			Source: clinical.Patient, LedgerSource: in.LedgerSource,
		})
	}
	known, err := s.store.MissReasonCodes(ctx)
	if err != nil {
		return Result{}, err
	}
	for _, reason := range in.Compliance.Reasons {
		if !known[reason] {
			return Result{}, fmt.Errorf("%w: %s", ErrUnknownReason, reason)
		}
		records = append(records, s.coded(in, MissReasonCode, reason))
	}

	improvement, err := s.improvementRecords(ctx, in, session)
	if err != nil {
		return Result{}, err
	}
	records = append(records, improvement...)

	policy, err := s.store.Policy(ctx)
	if err != nil {
		return Result{}, err
	}
	raised := policy.Raises(unable, correctedToday)
	if len(in.Items) > 0 {
		// Recorded even when it is false, and that is the point. A flag written only when it
		// fires makes "this patient has never been flagged" and "nobody has assessed this
		// patient" the same row count, and the physician reading the dashboard cannot tell a
		// clean technique from an unasked one. The same argument as the not-applicable score,
		// one station over.
		flag := raised
		records = append(records, clinical.Recording{
			EventID: itemEventID(in.EventID, ReeducationFlagCode), PatientID: in.PatientID,
			VisitID: &in.VisitID, Code: ReeducationFlagCode, ValueBool: &flag,
			EffectiveAt: s.clock.Now(),
			Source:      clinical.Station, LedgerSource: in.LedgerSource,
		})
	}

	if len(records) == 0 {
		return Result{}, ErrNothingToRecord
	}

	observations, _, err := s.values.RecordBatch(ctx, clinical.Batch{
		EventID: in.EventID, Records: records,
		PatientID: in.PatientID, VisitID: &in.VisitID,
		LedgerSource: in.LedgerSource,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{
		Observations: observations, Reeducation: raised,
		Unable: unable, CorrectedToday: correctedToday,
	}, nil
}

// improvementRecords turns §2's answer into at most one observation.
//
// The two shapes write two different codes, and there is no shape that writes both — which is
// what makes "not applicable" and "a score" distinguishable by a query that knows nothing about
// this package. Nobody having asked writes neither, and is therefore distinguishable from both.
func (s *Service) improvementRecords(ctx context.Context,
	in Assessment, session Session) ([]clinical.Recording, error) {

	if !in.Improvement.Answered() {
		return nil, nil
	}
	if score, ok := in.Improvement.Score(); ok {
		if session.FirstVisit {
			// §2: a first-visit score would be a number answering a different question. Refused
			// rather than stored, because the damage is downstream and silent — it would be
			// averaged with the others by every analysis that ever touches this column.
			return nil, ErrScoreOnFirstVisit
		}
		value := float64(score)
		return []clinical.Recording{{
			EventID: itemEventID(in.EventID, ScoreCode), PatientID: in.PatientID,
			VisitID: &in.VisitID, Code: ScoreCode,
			Value: &value, Unit: "1",
			EffectiveAt: s.clock.Now(),
			Source:      clinical.Patient, LedgerSource: in.LedgerSource,
		}}, nil
	}

	reason, _ := in.Improvement.NotApplicable()
	allowed, err := s.store.NotApplicableCodes(ctx)
	if err != nil {
		return nil, err
	}
	if !allowed[reason] {
		return nil, fmt.Errorf("%w: %s", ErrUnknownReason, reason)
	}
	return []clinical.Recording{s.coded(in, NotApplicableCode, reason)}, nil
}

// coded builds one coded observation for this assessment.
func (s *Service) coded(in Assessment, code, value string) clinical.Recording {
	source := clinical.Station
	if code == NotApplicableCode || code == MissReasonCode {
		// Both are the patient's own account rather than something anybody watched.
		source = clinical.Patient
	}
	return clinical.Recording{
		EventID: itemEventID(in.EventID, code+":"+value), PatientID: in.PatientID,
		VisitID: &in.VisitID, Code: code, ValueCode: value,
		EffectiveAt: s.clock.Now(),
		Source:      source, LedgerSource: in.LedgerSource,
	}
}

// itemEventID gives each value in an assessment a ledger id derived from the assessment's own
// id.
//
// The same trick CP45's batch uses on its derivations, and for the same reason: a tablet that
// lost the reply and pressed save again writes the same ids, and the ledger's primary key
// absorbs the retry. A random id per value would turn every retry into a second assessment, and
// a second assessment of the same demonstration is a patient who appears to have been watched
// twice — which, for a station whose whole output is "this happened in front of somebody",
// is a lie rather than a duplicate.
func itemEventID(assessment uuid.UUID, key string) uuid.UUID {
	return uuid.NewSHA1(assessmentNamespace, []byte(assessment.String()+":"+key))
}

var assessmentNamespace = uuid.MustParse("4b1d6a32-0c57-4f8e-9d21-7a6e3f2c81b4")
