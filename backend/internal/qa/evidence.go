package qa

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinicalterm"
	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
	"github.com/AmlanWTK/DTHCMS/backend/internal/education"
	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// Reading one file into [Evidence] (CP83).
//
// # Why this reads modules and never their tables
//
// Every fact below comes from an exported method of the module that owns it. Rule 6 asks CP79's
// engine what it thinks of the eGFR rather than reading `read.observation` and comparing against a
// window of its own; rule 16 asks CP57's gate function rather than re-deriving which checklist
// items are mandatory. The alternative — one more SQL query per rule, here — would be a second
// implementation of each of those judgements, and the failure mode of two implementations is that
// they agree until one of them changes. A QA screen showing a green tick beside a gate that is
// refusing the patient standing in front of it is precisely the defect this station exists to be.
//
// Where a module did not expose what this one needs, the method was added there:
// [clinical.Store.OrdersFor], [counseling.Store.CoveredItems],
// [education.Store.RecordedInVisit] and [medsafety.Store.TeratogenicGenerics]. Four narrow
// methods, each the natural question to ask its own module, and none of them reaching across.
//
// # The one fact this package must not read for itself
//
// The patient register. Age and sex arrive through [Demographics], implemented in `cmd/api` the
// way `medsafety.PatientFacts` is, so that the name and the identifiers stop at the boundary. QA
// legitimately works on one named patient's file — this is not CP78's stricter isolation — but
// the register has thirty columns and this station needs two of them.

// Demographics is the two facts about a person that rule 14 reads.
//
// Sex and age, and nothing else. Nil age is "not recorded", which rule 14 treats as inside the
// band: a woman whose date of birth nobody wrote down might be thirty.
type Demographics interface {
	// AgeAndSex returns fractional years (nil when the birth date is unusable) and the
	// register's sex value, which is empty when it is not recorded.
	AgeAndSex(ctx context.Context, facility, patient uuid.UUID) (*float64, string, error)
}

// Sources are the modules this station consumes.
//
// A struct of concrete stores rather than an interface per module, because every one of them is
// already a narrow read surface and an interface per module here would be twelve interfaces whose
// only implementation is the store they wrap. [Demographics] is the exception and its comment
// says why.
type Sources struct {
	Sheets    *prescription.Store
	Catalogue *formulary.Store
	Values    *clinical.Store
	Visits    *visit.Store
	Histories *history.Store
	Allergies *allergy.Store
	Counsel   *counseling.Store
	Educate   *education.Store
	Safety    *medsafety.Engine
	Rules     *medsafety.Store
	Facts     medsafety.PatientFacts
	Who       Demographics

	// Terms is the observation catalogue, cached for the life of the process. Reference data:
	// see [clinicalterm.Cache] for what a restart is needed for and what it is not.
	Terms *clinicalterm.Cache
}

// firstSentence trims a counselling item to what fits on a finding.
//
// Most items are already a short label — "Glucometer use", "Insulin injection sites and
// technique" — and one is not: `AGRANULOCYTOSIS_WARNING` is the four-line script the counsellor
// reads aloud. A finding listing three outstanding items must not turn into a paragraph, and the
// first sentence of a counselling item is what the item is about.
func firstSentence(text string) string {
	text = strings.TrimSpace(text)
	for _, stop := range []string{". ", "। ", ".\n", "।\n"} {
		if at := strings.Index(text, stop); at >= 0 {
			return strings.TrimSpace(text[:at+1])
		}
	}
	return text
}

// Gather reads one prescription's whole file.
//
// # Why it reads the rules first and the facts second
//
// Only the codes some live rule actually asks about are read: a clinic whose checklist mentions
// four observation codes does not pay for a scan of seventy-five. On an empty rule table this
// function does almost no work, which is the honest cost of a station with nothing to check —
// and it still returns a [Review], because the clearance is still required.
func (s Sources) Gather(ctx context.Context, set Ruleset, facility, id uuid.UUID,
	now time.Time) (Evidence, error) {

	sheet, err := s.Sheets.ByID(ctx, id, facility)
	if err != nil {
		return Evidence{}, ErrNotFound
	}

	e := Evidence{
		Now:                 now.UTC(),
		PrescriptionID:      sheet.ID,
		PatientID:           sheet.PatientID,
		VisitID:             sheet.VisitID,
		Latest:              map[string]ObservationFact{},
		Orders:              map[string]OrderFact{},
		CounselingCovered:   map[string]bool{},
		TeratogenicGenerics: map[string]bool{},
		StationNames:        map[string][2]string{},
		Terms:               s.Terms.Get(ctx),
	}

	if err := s.lines(ctx, &e, sheet); err != nil {
		return Evidence{}, err
	}
	if err := s.stations(ctx, &e, facility); err != nil {
		return Evidence{}, err
	}

	// Which facts are worth reading is decided by the rule table, so a clinic that retires the
	// thyroid rules stops paying for a TSH lookup.
	need := wanted(set)
	if err := s.observations(ctx, &e, facility, need); err != nil {
		return Evidence{}, err
	}
	if err := s.record(ctx, &e, facility, set); err != nil {
		return Evidence{}, err
	}
	if err := s.engines(ctx, &e, facility, set); err != nil {
		return Evidence{}, err
	}
	return e, nil
}

// wanted is every observation code some live rule names, plus the codes needed to answer a rule
// about this visit. A set rather than a slice because five rules naming HBA1C should read it once.
func wanted(set Ruleset) map[string]bool {
	need := map[string]bool{}
	for _, rule := range set.Rules {
		for _, code := range rule.Params.Codes {
			need[code] = true
		}
		if rule.Kind == KindTeratogenPregnancyStatus {
			// The answer to rule 14 is a recorded pregnancy status, which is the observation
			// code migration 00071 had to add: the rule as written had no remediation before
			// it, because there was nowhere in the record for the answer to go.
			need[pregnancyStatusCode] = true
		}
	}
	return need
}

// pregnancyStatusCode is the code migration 00071 adds. Named here rather than made a rule
// parameter because it is not a choice: rule 14's question is "was pregnancy status recorded",
// and a rule pointing that at some other code would be asking something else.
const pregnancyStatusCode = "PREGNANCY_STATUS"

// ---------------------------------------------------------------------------
// The sheet
// ---------------------------------------------------------------------------

// lines maps the prescription's live items onto [Line], resolving each product to its molecule
// and class.
//
// The class is resolved from the formulary rather than copied onto the prescription line, which
// is the one place this package deliberately joins: `read.prescription_item` copies the label,
// generic name and strength at the moment of prescribing precisely so that a historical sheet
// stays readable, and the therapeutic class is not on it. A rule that says "insulin or a GLP-1
// pen" is a rule about the class, and the class is the formulary's.
//
// A product that has since been withdrawn resolves to nothing, and the line keeps the generic
// name the prescription itself recorded. Rules that match on a molecule still work; rules that
// match on a class do not fire for that line, which is the honest answer — nothing in the system
// knows what class a withdrawn product was in.
func (s Sources) lines(ctx context.Context, e *Evidence, sheet prescription.Prescription) error {
	for _, item := range sheet.Items {
		if !item.Live() {
			continue
		}
		line := Line{
			ItemID: item.ID, Label: item.Label,
			Generic: item.GenericName, GenericDisplay: item.GenericName,
			Dose: item.Dose, DoseUnit: item.DoseUnit,
		}
		if item.ProductID != nil && s.Catalogue != nil {
			product, err := s.Catalogue.Product(ctx, sheet.FacilityID, *item.ProductID, e.Now)
			if err == nil {
				line.ClassCode = product.ClassCode
				if product.GenericName != "" {
					line.Generic = product.GenericName
				}
			}
		}
		e.Lines = append(e.Lines, line)
	}
	return nil
}

// ---------------------------------------------------------------------------
// The route
// ---------------------------------------------------------------------------

// stations reads the visit's planned route and asks whether each mandatory station recorded
// anything.
//
// "Recorded anything" is deliberately generous. The alternative — a per-station list of the codes
// that station is supposed to produce — is a second copy of the route's meaning that would go
// stale the first time a station's job changed, and rule 17 is a WARN whose remedy is a
// conversation. A station that finished an encounter and wrote nothing is what it catches.
func (s Sources) stations(ctx context.Context, e *Evidence, facility uuid.UUID) error {
	if s.Visits == nil {
		return nil
	}
	one, err := s.Visits.ByID(ctx, e.VisitID, facility)
	if err != nil {
		return err
	}
	planned, err := s.Visits.Planned(ctx, facility, one.VisitType)
	if err != nil {
		return err
	}

	observations, err := s.Values.ForVisit(ctx, e.VisitID, facility)
	if err != nil {
		return err
	}
	touched := map[string]bool{}
	for _, o := range observations {
		if o.StationCode != "" {
			touched[o.StationCode] = true
		}
	}

	names, err := s.Visits.StationNames(ctx, facility)
	if err != nil {
		return err
	}
	for code, pair := range names {
		e.StationNames[code] = pair
	}

	for _, p := range planned {
		// The QA station itself is not asked whether it recorded anything: it is the station
		// doing the asking, and a rule that bounced a patient to the room they are standing in
		// would be a loop.
		if p.StationCode == "STN_QA" {
			continue
		}
		fact := StationFact{Code: p.StationCode, Mandatory: p.Required, Recorded: touched[p.StationCode]}
		if pair, ok := e.StationNames[p.StationCode]; ok {
			fact.NameEN, fact.NameBN = pair[0], pair[1]
		} else {
			fact.NameEN, fact.NameBN = p.StationCode, p.StationCode
		}
		e.Stations = append(e.Stations, fact)
	}
	return nil
}

// ---------------------------------------------------------------------------
// The measurements
// ---------------------------------------------------------------------------

// observations reads the newest live value of every code some rule names, and the orders.
func (s Sources) observations(ctx context.Context, e *Evidence, facility uuid.UUID,
	need map[string]bool) error {

	if len(need) == 0 {
		return nil
	}
	current, err := s.Values.Current(ctx, e.PatientID, facility, "")
	if err != nil {
		return err
	}
	for _, o := range current {
		if !need[o.Code] {
			continue
		}
		fact := ObservationFact{Code: o.Code, EffectiveAt: o.EffectiveAt}
		fact.InThisVisit = o.VisitID != nil && *o.VisitID == e.VisitID
		e.Latest[o.Code] = fact
	}
	if fact, ok := e.Latest[pregnancyStatusCode]; ok {
		e.PregnancyThisVisit = fact.InThisVisit
	}

	codes := make([]string, 0, len(need))
	for code := range need {
		codes = append(codes, code)
	}
	orders, err := s.Values.OrdersFor(ctx, e.PatientID, facility, codes)
	if err != nil {
		return err
	}
	for _, o := range orders {
		e.Orders[o.Code] = OrderFact{Code: o.Code, OrderedAt: o.OrderedAt}
	}
	return nil
}

// ---------------------------------------------------------------------------
// The record
// ---------------------------------------------------------------------------

// record reads the facts that live in `history`, `allergy`, `counseling`, `education` and the
// register.
func (s Sources) record(ctx context.Context, e *Evidence, facility uuid.UUID, set Ruleset) error {
	items, err := s.Histories.ForPatient(ctx, e.PatientID)
	if err != nil {
		return err
	}
	visitID := e.VisitID.String()
	for _, item := range items {
		if item.Status != "ACTIVE" || item.Code == "" {
			continue
		}
		if item.Kind != "COMORBIDITY" && item.Kind != "COMPLAINT" {
			continue
		}
		e.DiagnosisCodes = append(e.DiagnosisCodes, item.Code)
		// Rule 15: recorded **or confirmed** at this visit. Confirming last year's diagnosis is
		// coding today's visit — the officer looked at it and said it still holds — and a rule
		// that only counted new items would bounce every follow-up in the clinic.
		if item.RecordedVisit == visitID || item.ConfirmedVisit == visitID {
			e.CodedThisVisit = true
		}
	}

	// `core.allergy_status` answers NONE_RECORDED when nobody has said anything, and one of
	// ALLERGIES_RECORDED or an assertion kind when somebody has. The tri-state is that
	// distinction and nothing else: a patient asserted NKA has an answer, and rule 2 is
	// satisfied by the answer rather than by its content.
	status, err := s.Allergies.Status(ctx, e.PatientID)
	if err != nil {
		return err
	}
	if status != "" && status != "NONE_RECORDED" {
		asserted := status == "ALLERGIES_RECORDED"
		e.AllergyAsserted = &asserted
	}

	missing, err := s.Counsel.Missing(ctx, e.VisitID)
	if err != nil {
		return err
	}
	for _, m := range missing {
		// The checklist's own words, which CP57 already returns beside the code because its own
		// screen needs them. The code is carried too, as the detail a person debugging a rule
		// needs and never as the sentence an officer reads.
		e.CounselingOutstanding = append(e.CounselingOutstanding,
			Term{Code: m.ItemCode, EN: firstSentence(m.TextEN), BN: firstSentence(m.TextBN)})
	}
	covered, err := s.Counsel.CoveredItems(ctx, e.VisitID)
	if err != nil {
		return err
	}
	for _, code := range covered {
		e.CounselingCovered[code] = true
	}

	taught, err := s.Educate.RecordedInVisit(ctx, e.PatientID, e.VisitID, facility)
	if err != nil {
		return err
	}
	e.EducationThisVisit = taught

	if s.Who != nil {
		age, sex, err := s.Who.AgeAndSex(ctx, facility, e.PatientID)
		if err != nil {
			return err
		}
		e.AgeYears, e.Sex = age, sex
	}

	// Only if some live rule asks. A clinic that has retired rule 14 does not pay for the
	// coverage query, and — more to the point — a build that read it unconditionally would hide
	// the fact that the answer is empty behind a call that always succeeds.
	for _, rule := range set.Rules {
		if rule.Kind != KindTeratogenPregnancyStatus {
			continue
		}
		flagged, err := s.Rules.TeratogenicGenerics(ctx, facility, e.Now)
		if err != nil {
			return err
		}
		for name := range flagged {
			e.TeratogenicGenerics[name] = true
		}
		break
	}
	return nil
}

// ---------------------------------------------------------------------------
// The engines
// ---------------------------------------------------------------------------

// engines runs CP78's safety check over this sheet and takes CP79's renal answer out of it.
//
// One call, two rules' worth of evidence, and **no second implementation of either**. The renal
// findings are the engine's own — `renalFindings` in `medsafety/renal.go` — so the window this
// station quotes is the facility's row and not a number compiled in here.
func (s Sources) engines(ctx context.Context, e *Evidence, facility uuid.UUID, set Ruleset) error {
	asked := false
	for _, rule := range set.Rules {
		if rule.Kind == KindSafetyEngineFinding || rule.Kind == KindRenalWindow {
			asked = true
		}
	}
	if !asked || s.Safety == nil {
		return nil
	}

	picture, _, err := s.Safety.Assemble(ctx, s.Facts, facility, e.PatientID)
	if err != nil {
		return err
	}
	proposed := make([]medsafety.Item, 0, len(e.Lines))
	for _, line := range e.Lines {
		proposed = append(proposed, medsafety.Item{
			Ref: line.ItemID.String(), Generic: line.Generic, Label: line.Label,
		})
	}
	result, err := s.Safety.Check(ctx, medsafety.Request{
		FacilityID: facility, At: e.Now, Picture: picture, Proposed: proposed,
	})
	if err != nil {
		return err
	}

	policy, err := s.Rules.RenalPolicy(ctx, facility)
	if err != nil {
		return err
	}
	// CP79 states its window in months, which is how a clinician says it. Days is what the
	// finding's sentence needs, and thirty is the conversion `days()` reverses exactly.
	e.RenalWindowDays = policy.RecencyMonths * 30

	labels := map[string]string{}
	for _, line := range e.Lines {
		labels[line.ItemID.String()] = line.Label
	}
	for _, f := range result.Findings {
		subject := f.Subject
		if named, ok := labels[strings.TrimPrefix(f.SubjectRef, "current:")]; ok && named != "" {
			subject = named
		}
		if f.Type == medsafety.TypeRenal || strings.HasPrefix(f.RuleCode, "RENAL_") {
			// CP79's three: no eGFR for a drug that needs one, an eGFR too old to be current,
			// and a molecule nobody has classified. Each is a statement about what the check
			// could not do, which is exactly what rule 6 is about.
			if f.Severity == medsafety.SeverityBlock || f.Outcome == medsafety.OutcomeCannotVerify {
				e.RenalBlocking = append(e.RenalBlocking, subject)
				continue
			}
		}
		e.Safety = append(e.Safety, SafetyFinding{
			RuleCode: f.RuleCode, RuleType: string(f.Type), Severity: string(f.Severity),
			MessageEN: f.MessageEN, MessageBN: f.MessageBN, Subject: subject,
		})
	}
	return nil
}
