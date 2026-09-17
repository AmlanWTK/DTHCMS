package qa

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinicalterm"
)

// The twelve predicates, and the facts they are asked about (CP83, docs/qa-rules.md §3).
//
// # Why the facts are gathered once and the predicates are pure
//
// Eighteen rules against one file. A predicate that fetched its own evidence would read the
// observation table five times for the five recency rules and the formulary once per line, and
// the officer would wait for it with a patient in front of them. More importantly it would make
// the rules untestable in isolation: every rule test would need a database.
//
// So `evidence.go` does all the reading, once, and everything below is a function from
// [Evidence] and a [Rule] to a finding or nothing. Each of the tests in `rules_test.go` builds an
// [Evidence] literal, which is how each rule of §3 gets its "fires when it should and not when it
// shouldn't" pair without a fixture.
//
// # The shape of a predicate, and the one thing all of them share
//
// Every rule may be conditioned on the *file* — a diagnosis the patient carries, a molecule or a
// class on the sheet. That triggering test is [Rule.triggers], shared by every shape, because it
// is the half that turns "no TSH in six months" into "no TSH in six months **for somebody on
// levothyroxine**". A rule whose trigger does not match produces nothing, and producing nothing
// is different from passing: a rule that does not apply is not a rule that was satisfied, and
// neither is a finding.

// ---------------------------------------------------------------------------
// The facts
// ---------------------------------------------------------------------------

// Line is one drug on the sheet, as this station needs to see it.
//
// Deliberately not `prescription.Item`: this package needs the molecule and its class, which the
// prescription line does not carry (it carries what was printed), and it does not need the price,
// the instructions or the line number. The mapping is in `evidence.go`.
type Line struct {
	ItemID uuid.UUID
	// Label is what the sheet says — the trade name the patient will look for on the box.
	Label string
	// Generic is the molecule, lowercased for matching and kept in its own case for display.
	Generic        string
	GenericDisplay string
	// ClassCode is the formulary's therapeutic class, empty when the product is not classified.
	ClassCode string

	// DoseUnit and Dose are carried for rule 18: a dose written "per kg" is a weight-based dose
	// whatever the molecule is, and the sheet is the only place that says so.
	Dose     string
	DoseUnit string
}

// WeightBased reports whether this line's dose is expressed per unit of body weight.
//
// A string test, and it is the honest one: `read.prescription_item` has no "is this weight-based"
// column and inventing one would mean asking every prescriber to set a flag they would forget.
// What a prescriber actually writes is "15 mg/kg" or "0.5 units/kg/day", and both of those say
// it. The cost of the string test is a false positive on a dose that mentions kilograms for some
// other reason, and rule 18's finding is a request for a weight — which is thirty seconds at a
// station the patient walked past.
func (l Line) WeightBased() bool {
	hay := strings.ToLower(l.Dose + " " + l.DoseUnit)
	return strings.Contains(hay, "/kg") || strings.Contains(hay, "per kg") ||
		strings.Contains(hay, "/ kg")
}

// SafetyFinding is one thing CP78's engine said about this sheet, reduced to what this station
// judges on.
//
// Not `medsafety.Finding` itself, for the reason [Line] is not `prescription.Item`: what matters
// here is the severity, the kind of rule and the sentence, and importing the whole finding would
// tie this package's tests to CP77's rule model.
type SafetyFinding struct {
	RuleCode  string
	RuleType  string
	Severity  string
	MessageEN string
	MessageBN string
	// Subject is the drug the finding is about, when it is about one.
	Subject string
}

// ObservationFact is the newest live value of one observation code, and when it was true.
type ObservationFact struct {
	Code        string
	EffectiveAt time.Time
	// InThisVisit is true when the newest value was recorded during the visit being reviewed.
	// Carried separately from the timestamp because "today" and "within six months" are two
	// different questions and a same-day comparison against a clock is a bug waiting for a
	// clinic that runs past midnight.
	InThisVisit bool
}

// OrderFact is a test somebody asked for and nobody has resulted yet.
type OrderFact struct {
	Code      string
	OrderedAt time.Time
}

// StationFact is one station on this visit's planned route.
type StationFact struct {
	Code      string
	NameEN    string
	NameBN    string
	Mandatory bool
	// Recorded is whether anything at all reached the record from that station on this visit —
	// an observation, a counselling tick, an education record. Rule 17 is about the station
	// that was skipped, and a station the patient never reached records nothing.
	Recorded bool
}

// Evidence is every fact the eighteen rules read, gathered once.
//
// # nil versus empty, again
//
// The same contract `medsafety.PatientFacts` carries, and for the same reason. `AllergyAsserted`
// is a tri-state and not a bool: "no known allergy" is an answer and silence is not, and a field
// that could not tell them apart would turn rule 2 — a BLOCK — into a silent pass. Where a fact
// could not be read, the rule that reads it fires: this station's whole job is to notice absence.
type Evidence struct {
	Now time.Time

	PrescriptionID uuid.UUID
	PatientID      uuid.UUID
	VisitID        uuid.UUID

	// Lines is the live sheet. Removed lines are not here: a drug taken off the prescription is
	// not prescribed.
	Lines []Line

	// AgeYears is nil when the date of birth is not recorded, which makes rule 14's age band
	// unanswerable — and the rule then fires, because a woman whose age nobody knows is a woman
	// who might be 30.
	AgeYears *float64
	// Sex is the register's value: 'female', 'male', or empty for not recorded.
	Sex string
	// PregnancyThisVisit is whether a pregnancy status was recorded during this visit.
	PregnancyThisVisit bool

	// AllergyAsserted is nil when nobody has said anything about this patient's allergies,
	// and a pointer to true or false when somebody has.
	AllergyAsserted *bool

	// Safety is CP78's answer about this sheet.
	Safety []SafetyFinding
	// RenalBlocking is CP79's answer: a renally-dosed drug on this sheet with no eGFR, or one
	// outside the facility's window. Each entry names the drug.
	RenalBlocking []string
	// RenalWindowDays is the facility's window, carried so the finding can say what it is.
	RenalWindowDays int

	// Latest is the newest live value for every observation code any rule asks about.
	Latest map[string]ObservationFact
	// Orders is the newest live order for every code any rule asks about.
	Orders map[string]OrderFact

	// DiagnosisCodes is every coded condition this patient carries, active, from the history.
	// Used by the triggering test — "is this patient diabetic".
	DiagnosisCodes []string
	// CodedThisVisit is whether a coded diagnosis was recorded or confirmed at this visit.
	// Rule 15, and it is a different question from the one above: a patient known to be
	// diabetic for six years still needs today's visit coded, or today's visit is invisible.
	CodedThisVisit bool

	// CounselingOutstanding is CP57's answer: mandatory items nobody has covered, carrying the
	// words the checklist itself uses rather than the item codes. CP57 already reads `text_en`
	// and `text_bn` for its own screen, so this is the same sentence the counsellor saw.
	CounselingOutstanding []Term
	// CounselingCovered is every item code ticked on this visit.
	CounselingCovered map[string]bool

	// EducationThisVisit is whether station 11 recorded anything for this patient this visit.
	EducationThisVisit bool

	// TeratogenicGenerics is the set of molecules CP77 flags teratogenic, lowercased. **Empty
	// in production today**, which is `docs/qa-rules.md` §3.4's documented state and the reason
	// invariant 137 reports it on every verify rather than leaving it silent.
	TeratogenicGenerics map[string]bool

	// Stations is this visit's planned route.
	Stations []StationFact

	// StationNames is the facility's station catalogue, for putting a room's own words on a
	// bounce rather than STN_CONSULTATION.
	StationNames map[string][2]string

	// Terms is the observation catalogue, so a rule naming one code can say what that code is
	// called. See `internal/clinicalterm`: the zero value is a working lexicon that spells
	// codes rather than naming them, which is what a unit test gets and what a process that
	// could not read the catalogue gets instead of a nil dereference.
	Terms clinicalterm.Lexicon
}

// Term is a bilingual name for something a finding has to mention. A local alias so that the
// evidence struct does not make every reader of this file open another package.
type Term = clinicalterm.Term

// ---------------------------------------------------------------------------
// The triggering test
// ---------------------------------------------------------------------------

// triggers reports whether this rule applies to this file at all, and what on the file made it.
//
// Three conditions, ORed, because that is what the rule rows mean: a rule may be about a patient
// with a diagnosis, or about a sheet carrying a molecule, or about a sheet carrying a class. A
// rule with none of the three applies to every file, which is what rules 2, 15, 16 and 17 are.
//
// The returned lines are what matched, so a finding can name the drug rather than say "a drug".
func (r Rule) triggers(e Evidence) ([]Line, bool) {
	p := r.Params
	if len(p.WhenDiagnosis) == 0 && len(p.WhenGenerics) == 0 && len(p.WhenClasses) == 0 {
		return nil, true
	}

	if len(p.WhenDiagnosis) > 0 && hasDiagnosis(e.DiagnosisCodes, p.WhenDiagnosis) {
		return nil, true
	}

	matched := []Line{}
	for _, line := range e.Lines {
		if containsFold(p.WhenGenerics, line.Generic) || contains(p.WhenClasses, line.ClassCode) {
			matched = append(matched, line)
		}
	}
	return matched, len(matched) > 0
}

// hasDiagnosis matches by prefix, because ICD-10 is a tree: "E11" is type 2 diabetes and the
// codes that actually appear on a record are E11.9, E11.22, E11.65. A rule listing every leaf
// would be a rule that stopped matching the day somebody coded a complication.
func hasDiagnosis(carried, wanted []string) bool {
	for _, code := range carried {
		up := strings.ToUpper(strings.TrimSpace(code))
		for _, want := range wanted {
			if up != "" && strings.HasPrefix(up, strings.ToUpper(want)) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Evaluation
// ---------------------------------------------------------------------------

// Evaluate asks one rule of one file.
//
// Returns zero or more findings: zero when the rule does not apply or is satisfied, and more than
// one when the same rule is broken by more than one line — a sheet with metformin and
// dapagliflozin at an unknown eGFR has two lines to fix, and a finding naming one of them leaves
// the physician deleting the wrong drug. That is CP78's own per-line decision, taken again here
// for the same reason.
func (r Rule) Evaluate(e Evidence) []Finding {
	matched, applies := r.triggers(e)
	if !applies {
		return nil
	}

	switch r.Kind {
	case KindSafetyEngineFinding:
		return r.safetyEngine(e)
	case KindAllergyStatusAsserted:
		return r.allergyStatus(e)
	case KindObservationRecent:
		return r.observationRecent(e)
	case KindObservationThisVisit:
		return r.observationThisVisit(e)
	case KindRenalWindow:
		return r.renalWindow(e)
	case KindEducationThisVisit:
		return r.educationThisVisit(e, matched)
	case KindCounselingItemCovered:
		return r.counselingItem(e)
	case KindCounselingComplete:
		return r.counselingComplete(e)
	case KindTeratogenPregnancyStatus:
		return r.teratogen(e)
	case KindDiagnosisCodedThisVisit:
		return r.diagnosisCoded(e)
	case KindMandatoryStationData:
		return r.mandatoryStations(e)
	case KindWeightForWeightBasedDose:
		return r.weightBasedDose(e)
	}
	// Unreachable: `core.qa_rule.kind` references `core.qa_rule_kind` and [Ruleset] refuses a
	// kind this build does not implement before any of this runs. Returning nothing here would
	// be the silent failure the whole design is arranged against, so it does not.
	return []Finding{r.finding(e, "this rule names a question this build cannot ask",
		"এই নিয়মটি এমন প্রশ্ন করে যা এই সংস্করণ জিজ্ঞেস করতে পারে না")}
}

// finding builds one, with the rule's own words and the station's own name.
func (r Rule) finding(e Evidence, subjectEN, subjectBN string) Finding {
	f := Finding{
		RuleCode: r.Code, Sev: r.Sev,
		TitleEN: r.TitleEN, TitleBN: r.TitleBN,
		DetailEN: r.DetailEN, DetailBN: r.DetailBN,
		SubjectEN: subjectEN, SubjectBN: subjectBN,
		BounceStation: r.BounceStation,
	}
	if names, ok := e.StationNames[r.BounceStation]; ok {
		f.StationEN, f.StationBN = names[0], names[1]
	}
	return f
}

// ---------------------------------------------------------------------------
// Rules 1 and 3 — CP78, consumed rather than reimplemented
// ---------------------------------------------------------------------------

// safetyEngine turns CP78's unresolved findings into QA findings.
//
// It re-states the engine's own sentence rather than composing a new one. The engine's message is
// physician-authored, cited and versioned (CP77), and paraphrasing it at this station would put a
// developer's words in a clinical warning.
//
// **Severity here is the QA rule's, not the engine's.** They are different judgements: the engine
// says how dangerous the interaction is, and this rule says whether the file may close with it
// unresolved. `min_severity` is what selects which engine findings this rule is about.
func (r Rule) safetyEngine(e Evidence) []Finding {
	out := []Finding{}
	for _, s := range e.Safety {
		if !severityAtLeast(s.Severity, r.Params.MinSeverity) {
			continue
		}
		if len(r.Params.RuleTypes) > 0 && !contains(r.Params.RuleTypes, s.RuleType) {
			continue
		}
		subject := s.MessageEN
		if s.Subject != "" {
			subject = s.Subject + ": " + s.MessageEN
		}
		subjectBN := s.MessageBN
		if s.Subject != "" {
			subjectBN = s.Subject + ": " + s.MessageBN
		}
		out = append(out, r.finding(e, subject, subjectBN))
	}
	return out
}

// severityAtLeast orders CP77's three severities. An empty `min_severity` means every finding,
// which is a rule that has not narrowed itself rather than a rule that matches nothing.
func severityAtLeast(have, min string) bool {
	rank := map[string]int{"INFO": 1, "WARN": 2, "BLOCK": 3}
	if min == "" {
		return true
	}
	return rank[strings.ToUpper(have)] >= rank[strings.ToUpper(min)]
}

// ---------------------------------------------------------------------------
// Rule 2 — allergy status
// ---------------------------------------------------------------------------

// allergyStatus is the tri-state made a finding.
//
// Fires on nil and on nothing else. `AllergyAsserted` pointing at false is *"asked, and the
// patient has none"*, which is the answer CP54 wanted; nil is nobody having asked.
func (r Rule) allergyStatus(e Evidence) []Finding {
	if e.AllergyAsserted != nil {
		return nil
	}
	return []Finding{r.finding(e,
		"nobody has recorded whether this patient has allergies",
		"এই রোগীর অ্যালার্জি আছে কিনা কেউ লেখেননি")}
}

// ---------------------------------------------------------------------------
// Rules 4, 8, 9, 11, 13 — recency
// ---------------------------------------------------------------------------

// observationRecent is the workhorse: is there a value for any of these codes inside the window?
//
// **Any one of them satisfies it.** A lipid profile is present if LDL is present, because a lab
// reports what it reports and a rule requiring all four would block on a panel that came back
// without HDL.
//
// `accept_ordered` is rule 4's second half, and it is checked against the same window: an HbA1c
// ordered eight months ago and never resulted is not an HbA1c ordered. That is a judgement, and
// it is the one that makes the rule mean what it says — the consultant who ordered it today has
// done the right thing, and the one whose order was lost in March has not been protected by it.
func (r Rule) observationRecent(e Evidence) []Finding {
	window := 0
	if r.WindowDays != nil {
		window = *r.WindowDays
	}
	cutoff := e.Now.AddDate(0, 0, -window)

	for _, code := range r.Params.Codes {
		if fact, ok := e.Latest[code]; ok && !fact.EffectiveAt.Before(cutoff) {
			return nil
		}
		if r.Params.AcceptOrdered {
			if order, ok := e.Orders[code]; ok && !order.OrderedAt.Before(cutoff) {
				return nil
			}
		}
	}

	thing := r.looksFor(e)
	en := fmt.Sprintf("no %s in %s", thing.EN, clinicalterm.WindowEN(window))
	bn := fmt.Sprintf("%s কোনো %s নেই", clinicalterm.WindowBN(window), thing.BN)
	if r.Params.AcceptOrdered {
		en += ", and none ordered"
		bn += ", এবং কোনোটি দেওয়াও হয়নি"
	}
	f := r.finding(e, en, bn)
	f.SubjectCodes = r.Params.Codes
	return []Finding{f}
}

// looksFor is what this rule is hunting for, in the words the officer reads.
//
// # Why the rule answers this and not a lookup table
//
// Two of the three sources below could have been one dictionary somewhere, and the third could
// not. `CHOL_LDL, CHOL_TOTAL, CHOL_HDL, TRIGLYCERIDE` is a **lipid profile** — but that is a
// statement about what *this rule* means by listing them, not a fact about the codes: another
// rule could list `CHOL_LDL` alone and mean an LDL. So the phrase is a column on the rule, edited
// by the same person and the same permission that edits the codes, and `core.qa_rule`'s
// `qa_rule_says_what_it_looks_for` refuses a multi-code rule that does not carry one.
//
// The single-code case needs no phrase, because `core.observation_code` already says "HbA1c",
// and a second copy of that name on the rule row is a second copy to drift out of date.
//
// The last fallback spells the code rather than printing it. A finding that reads "no chol ldl"
// is a rule row that needs fixing, and invariant 138 reports it; a finding that reads
// "no CHOL_LDL" is the defect this whole function exists to remove, and it would look like
// content rather than like a missing row.
func (r Rule) looksFor(e Evidence) Term {
	if strings.TrimSpace(r.LooksForEN) != "" {
		return Term{EN: r.LooksForEN, BN: r.LooksForBN}
	}
	codes := r.Params.Codes
	if len(codes) == 0 {
		// A counselling rule with no phrase. The database refuses that row — see
		// `qa_rule_says_what_it_looks_for` — so this is reachable only from a hand-built rule in
		// a test, and spelling the code is better than composing a sentence with a hole in it.
		codes = r.Params.ItemCodes
		if len(codes) == 1 {
			spelled := clinicalterm.Spell(codes[0])
			return Term{EN: spelled, BN: spelled}
		}
	}
	if len(codes) == 1 {
		return e.Terms.Observation(codes[0])
	}
	names := make([]string, 0, len(codes))
	bnNames := make([]string, 0, len(codes))
	for _, code := range codes {
		t := e.Terms.Observation(code)
		names, bnNames = append(names, t.EN), append(bnNames, t.BN)
	}
	return Term{EN: clinicalterm.ListEN(names), BN: clinicalterm.ListBN(bnNames)}
}

// ---------------------------------------------------------------------------
// Rule 5 — this visit, not lately
// ---------------------------------------------------------------------------

// observationThisVisit fires unless one of the codes was recorded during this visit.
//
// Not "today": this visit. A patient who came yesterday, was sent home and came back is on a new
// visit, and yesterday's blood pressure belongs to yesterday's file.
func (r Rule) observationThisVisit(e Evidence) []Finding {
	for _, code := range r.Params.Codes {
		if fact, ok := e.Latest[code]; ok && fact.InThisVisit {
			return nil
		}
	}
	thing := r.looksFor(e)
	f := r.finding(e,
		"no "+thing.EN+" recorded during this visit",
		"এই পরিদর্শনে "+thing.BN+" নেওয়া হয়নি")
	f.SubjectCodes = r.Params.Codes
	return []Finding{f}
}

// ---------------------------------------------------------------------------
// Rule 6 — CP79's renal window, consumed
// ---------------------------------------------------------------------------

// renalWindow reports each drug CP79 says was prescribed against an eGFR that is missing or
// stale.
//
// **The window is not this package's number.** It is `core.facility_renal_policy`, read by the
// safety engine, and the finding quotes it so that an officer asking "how old is too old" gets
// the clinic's own answer rather than a constant somebody compiled in.
func (r Rule) renalWindow(e Evidence) []Finding {
	out := []Finding{}
	for _, drug := range e.RenalBlocking {
		out = append(out, r.finding(e,
			fmt.Sprintf("%s needs an eGFR from %s", drug, clinicalterm.WindowEN(e.RenalWindowDays)),
			fmt.Sprintf("%s-এর জন্য %s ইজিএফআর দরকার", drug, clinicalterm.WindowBN(e.RenalWindowDays))))
	}
	return out
}

// ---------------------------------------------------------------------------
// Rule 10 — CP92's education record, consumed
// ---------------------------------------------------------------------------

// educationThisVisit is the link between stations 11 and 10.
//
// The rule `docs/qa-rules.md` expects to be argued about, and the spec's own relaxation is
// recorded here rather than implemented: if it proves too heavy, restrict it to a **first**
// prescription of that device rather than downgrading it to a WARN. That is a change to the
// trigger, which is a column, once "first prescription of this device" is a fact somebody
// records — CP92 holds the competency history that would answer it.
func (r Rule) educationThisVisit(e Evidence, matched []Line) []Finding {
	if e.EducationThisVisit {
		return nil
	}
	labels := make([]string, 0, len(matched))
	for _, line := range matched {
		labels = append(labels, line.Label)
	}
	subject := strings.Join(labels, ", ")
	return []Finding{r.finding(e,
		subject+" prescribed, and station 11 recorded nothing for this patient this visit",
		subject+" লেখা হয়েছে, অথচ এই পরিদর্শনে ১১ নম্বর কেন্দ্র কিছুই লেখেনি")}
}

// ---------------------------------------------------------------------------
// Rules 12 and 16 — CP55-57's ticks, consumed
// ---------------------------------------------------------------------------

// counselingItem asks about one named item rather than the whole checklist.
//
// Rule 12 is the one `docs/qa-rules.md` argues hardest for: agranulocytosis is survivable only if
// the patient knows that a sore throat and fever means stop the drug and get a blood count today.
// A patient who was never told has no way to act on a symptom they will otherwise treat as flu.
//
// **It is inert in this clinic today**, because no counselling template defines
// `AGRANULOCYTOSIS_WARNING`. Invariant 137 reports that on every verify. The rule is built, tested
// against a seeded item, and will start firing the day the item exists — which is the right
// shape, because the item is clinical content and this is engineering.
// # One finding for the rule, not one per item code
//
// The rule carries the phrase — `core.qa_rule.looks_for_en`, which the database requires of any
// rule naming a counselling item, because `core.counseling_item` has no short name to offer: its
// `text_en` is the four-line script the counsellor reads aloud. So a rule listing three items has
// one phrase for all three and would otherwise repeat it three times on the screen. What the
// officer needs to know is that the thing this rule is about was not covered; which of the codes
// behind it was missed is a detail, and it is carried as one.
func (r Rule) counselingItem(e Evidence) []Finding {
	missed := []string{}
	for _, item := range r.Params.ItemCodes {
		if !e.CounselingCovered[item] {
			missed = append(missed, item)
		}
	}
	if len(missed) == 0 {
		return nil
	}
	thing := r.looksFor(e)
	f := r.finding(e,
		"the "+thing.EN+" has not been covered for this visit",
		"এই পরিদর্শনে "+thing.BN+" বিষয়টি বোঝানো হয়নি")
	f.SubjectCodes = missed
	return []Finding{f}
}

// counselingComplete is CP57's gate asked once more, at the last station that can catch it.
func (r Rule) counselingComplete(e Evidence) []Finding {
	if len(e.CounselingOutstanding) == 0 {
		return nil
	}
	en := make([]string, 0, len(e.CounselingOutstanding))
	bn := make([]string, 0, len(e.CounselingOutstanding))
	codes := make([]string, 0, len(e.CounselingOutstanding))
	for _, item := range e.CounselingOutstanding {
		// The checklist's own words and not its item codes. CP57 already reads `text_en` and
		// `text_bn` to draw the counsellor's screen, so the officer at station 10 and the
		// counsellor at station 3 are looking at the same sentence, which is what makes a bounce
		// actionable — "still to cover: Glucometer use" tells the counsellor what to do and
		// "GLUCOMETER" makes them guess whether it is the same item they already ticked.
		en, bn = append(en, item.EN), append(bn, item.BN)
		codes = append(codes, item.Code)
	}
	f := r.finding(e,
		// Comma-joined rather than [clinicalterm.ListEN], whose last separator is "or": these are
		// all outstanding, not alternatives, and "Diet or Glucometer use" would read as a choice.
		"still to cover: "+strings.Join(en, ", "),
		"এখনও বাকি: "+strings.Join(bn, ", "))
	f.SubjectCodes = codes
	return []Finding{f}
}

// ---------------------------------------------------------------------------
// Rule 14 — the one that fires for nothing
// ---------------------------------------------------------------------------

// teratogen is `docs/qa-rules.md` §3.4, built and wired to a flag no molecule carries.
//
// # Why it is here at all when it can never fire today
//
// Because the day somebody flags carbimazole is the day this has to work, and a rule written that
// day is a rule written in a hurry. It is covered by tests that seed the flag, so the predicate is
// exercised even while the reference data is empty — and the emptiness is reported by invariant
// 137 rather than being a silence somebody discovers in a year.
//
// # The age band, and why an unknown age fires
//
// The band is a proxy and a crude one; the spec says so and says that occasionally asking an
// irrelevant question is the correct direction to be wrong in. An unrecorded date of birth is
// treated as inside the band for the same reason: a woman whose age nobody wrote down might be
// thirty, and the cost of asking is a question.
//
// Sex is read the same way. A patient whose sex is not recorded is not excluded, because
// excluding on an absent fact is how a fail-closed rule becomes fail-open.
func (r Rule) teratogen(e Evidence) []Finding {
	if e.PregnancyThisVisit {
		return nil
	}
	if strings.EqualFold(e.Sex, "male") {
		return nil
	}
	if e.AgeYears != nil {
		age := int(*e.AgeYears)
		if r.Params.AgeMin != nil && age < *r.Params.AgeMin {
			return nil
		}
		if r.Params.AgeMax != nil && age > *r.Params.AgeMax {
			return nil
		}
	}

	out := []Finding{}
	for _, line := range e.Lines {
		// The molecule, and the label when the line has no molecule.
		//
		// A line with no formulary product carries no generic name — CP80's `Addition.Label` is
		// "a medicine this formulary does not hold" — and **spironolactone is exactly that here**:
		// `docs/qa-rules.md` A2 says this clinic prescribes it for PCOS, to the very women rule 14
		// exists for, and migration 00072 had to add the molecule to the dictionary because the
		// pharmacy stocks none. So a label-only line is matched on its label.
		//
		// Exact, lowercased, whole-string. "Spironolactone" matches; "Aldactone 25" does not, and
		// that limit is real and is reported: a trade name typed free-hand reaches no molecule,
		// and the fix for it is a product on the formulary rather than fuzzy matching here, which
		// would eventually ask the question about the wrong drug.
		if !e.TeratogenicGenerics[strings.ToLower(line.Generic)] &&
			!e.TeratogenicGenerics[strings.ToLower(strings.TrimSpace(line.Label))] {
			continue
		}
		out = append(out, r.finding(e,
			line.Label+" is flagged teratogenic and no pregnancy status was recorded this visit",
			line.Label+" ভ্রূণ-ক্ষতিকর হিসেবে চিহ্নিত, আর এই পরিদর্শনে গর্ভাবস্থার তথ্য নেওয়া হয়নি"))
	}
	return out
}

// ---------------------------------------------------------------------------
// Rule 15 — the visit as a record
// ---------------------------------------------------------------------------

// diagnosisCoded fires when nothing on this visit carries a code.
//
// **A patient known to be diabetic for six years still fails this if today's visit is uncoded.**
// That is the point and it is the part people find surprising: `DiagnosisCodes` is what the
// patient carries and `CodedThisVisit` is what today's visit says, and §12's research reads the
// second. A year of uncoded visits is a year that cannot be analysed, and nobody notices until
// somebody asks a question of the data.
func (r Rule) diagnosisCoded(e Evidence) []Finding {
	if e.CodedThisVisit {
		return nil
	}
	return []Finding{r.finding(e,
		"nothing on this visit carries a diagnosis code",
		"এই পরিদর্শনের কিছুতেই রোগনির্ণয়ের সংকেত নেই")}
}

// ---------------------------------------------------------------------------
// Rule 17 — the route's own mandatory stations
// ---------------------------------------------------------------------------

// mandatoryStations is the one rule whose bounce station is not the rule's: it is the station
// that recorded nothing, which is why `core.qa_rule` lets a WARN carry no station of its own.
func (r Rule) mandatoryStations(e Evidence) []Finding {
	out := []Finding{}
	for _, station := range e.Stations {
		if !station.Mandatory || station.Recorded {
			continue
		}
		f := r.finding(e,
			station.NameEN+" recorded nothing on this visit",
			station.NameBN+" এই পরিদর্শনে কিছুই লেখেনি")
		// The rule names no station; the finding does.
		f.BounceStation, f.StationEN, f.StationBN = station.Code, station.NameEN, station.NameBN
		out = append(out, f)
	}
	return out
}

// ---------------------------------------------------------------------------
// Rule 18 — a dose per kilogram, and no kilograms
// ---------------------------------------------------------------------------

func (r Rule) weightBasedDose(e Evidence) []Finding {
	weighed := false
	for _, code := range r.Params.Codes {
		if fact, ok := e.Latest[code]; ok && fact.InThisVisit {
			weighed = true
		}
	}
	if weighed {
		return nil
	}

	out := []Finding{}
	for _, line := range e.Lines {
		if !line.WeightBased() {
			continue
		}
		out = append(out, r.finding(e,
			line.Label+" is dosed by weight ("+strings.TrimSpace(line.Dose+" "+line.DoseUnit)+
				") and no weight was recorded this visit",
			line.Label+" ওজন অনুযায়ী দেওয়া হয়েছে ("+strings.TrimSpace(line.Dose+" "+line.DoseUnit)+
				"), অথচ এই পরিদর্শনে ওজন নেওয়া হয়নি"))
	}
	return out
}

// ---------------------------------------------------------------------------
// Small shared helpers
// ---------------------------------------------------------------------------

func contains(haystack []string, needle string) bool {
	if needle == "" {
		return false
	}
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func containsFold(haystack []string, needle string) bool {
	if needle == "" {
		return false
	}
	for _, s := range haystack {
		if strings.EqualFold(s, needle) {
			return true
		}
	}
	return false
}
