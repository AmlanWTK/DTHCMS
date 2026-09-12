package medsafety

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
)

// The deterministic medication safety engine (CP78, §7.2).
//
//	"proposed drugs × renal function × allergies × contraindications × current medications.
//	 **Not generative.**"
//
// This engine, and not the AI, is the authority on prescribing safety. Nothing in this file asks
// a model anything; nothing in it is probabilistic; the same picture and the same ruleset give
// the same answer in three years, which is what criterion 5's reproducibility rests on.
//
// # What it adds to CP77
//
// CP77 ships one rule against one picture ([Version.Evaluate]) because the sandbox cannot exist
// without it. This file is everything above that line:
//
//   - **coverage** — which proposed drugs no live rule mentioned, reported per drug in three
//     states that never collapse into "safe";
//   - **allergy expansion** through the seeded cross-reactivity map, and a refusal to expand
//     through a mapping nobody has approved;
//   - **duplicate therapy across items**, at molecule level, through the components migration
//     00058 added — the fix that makes Siglimet beside Comet visible;
//   - **per-line evaluation**, so that two contraindicated drugs on one sheet are two findings;
//   - **a verdict** the interface can lead with, whose best value is *clear within what was
//     checked* rather than *safe*;
//   - **the exact rule versions evaluated**, returned and audited, which is criterion 5.
//
// # The three states, and the sentence that is never rendered
//
// §7.2's risk note says an incomplete rule set presented as complete safety checking would be
// worse than no engine at all. So every proposed drug comes back as exactly one of:
//
//	CoveredClear   — a live rule was about this drug and it did not fire
//	CoveredFiring  — a live rule was about this drug and it fired, or could not be verified
//	NotCovered     — no live rule was about this drug at all
//
// An empty finding list is therefore never the whole answer, and there is no field anywhere in
// [Result] whose value is the word "safe". The closest the engine gets is
// [VerdictClearWithinCoverage], which is spelled that way in both languages on purpose.
//
// # Fail-closed, in four places
//
// A rule that met absent data answers CANNOT_VERIFY (CP77's three-valued logic). This file adds
// three more absences that CP77 could not see:
//
//  1. a proposed drug this formulary does not hold — reported as uncovered *and* as a finding,
//     because "no rules matched" and "I do not know what this drug is" are different;
//  2. a drug whose molecules nobody has written out — duplicate detection answers cannot verify;
//  3. an allergy that needs the cross-reactivity map, when the mapping is unapproved — a
//     cannot-verify finding, never a pass. That is the brief's own decision and it is right:
//     the map is CP77 seed content, and an unapproved map suppressing a penicillin warning
//     would be the exact failure the approval mechanism exists to prevent.
//
// And a fourth that is the honest state of this clinic today: **no rule is approved at all.** All
// forty seeded rules are inert until Dr. Nahid reads them, so a check run right now finds nothing
// — and [VerdictNoRulesApproved] says so in a sentence rather than returning an empty list that
// reads like a clean bill.
//
// # No PHI leaves this file
//
// Nothing here logs, traces or counts. A [Picture] is a patient's age, pregnancy state, kidney
// function, diagnoses, allergies and medication list, and a span attribute is exported to the
// same backend as a log line. The audit entry the handler writes carries the rule versions and
// the counts, and names no drug, no diagnosis and no allergen.

// ---------------------------------------------------------------------------
// What the caller asks with
// ---------------------------------------------------------------------------

// Item is one line of a draft prescription, as the caller has it.
//
// # Why a list of items rather than a prescription id
//
// §7.2's route is `POST /prescriptions/{id}/safety-check`, and CP80 — the prescription aggregate
// — does not exist. Two ways to handle that, and only one of them is honest.
//
// The engine could have been built around a prescription id with a table underneath it. That
// table would be a guess at CP80's aggregate, written by the checkpoint that does not own it,
// and CP80 would arrive to find a second prescription model already in the database with rows
// in it. The migration to unify them is the kind nobody schedules.
//
// So the engine takes what it actually reads: **a list of proposed items**. CP80's seam is one
// function — load the prescription, map its items to these, call [Engine.Check]. Nothing about
// the evaluation changes when the id exists, because the id was never an input to it.
type Item struct {
	// Ref is the caller's handle for the line, echoed on every finding and coverage entry so
	// the editor can point at the row. CP80 will put the prescription item id here.
	Ref string `json:"ref"`

	// ProductID is a `core.medication_product`, which is what CP76's autocomplete returns and
	// what CP81 will hold. The engine resolves it to a molecule and a class.
	ProductID *uuid.UUID `json:"product_id,omitempty"`
	// Generic names the molecule directly, for a caller that has one — the golden suite, and a
	// carry-forward from a record written before the product existed.
	Generic string `json:"generic,omitempty"`
	// Label is what to call it in a message. Falls back to the resolved product or generic
	// name when the caller does not supply one.
	Label string `json:"label,omitempty"`

	// DailyDose and DoseUnit feed the max-dose rules. Absent means a max-dose rule about this
	// drug answers "cannot verify" rather than passing — CP77's behaviour, unchanged.
	DailyDose *float64 `json:"daily_dose,omitempty"`
	DoseUnit  string   `json:"dose_unit,omitempty"`
}

// Picture is the patient half of the check: everything §7.2 multiplies the proposed drugs by.
//
// It is [Context] with the clinical facts and without the drugs, plus the two things CP77's
// Context could not carry — the patient's reported allergen groups before expansion, and whether
// anybody asked. The engine turns one of these plus a list of items into a Context.
//
// **Nil versus empty is load-bearing, exactly as in CP77.** `Diagnoses == nil` means nobody read
// the diagnosis list and every diagnosis rule fails closed; `Diagnoses == []string{}` means
// somebody read it and there were none. Same for allergies and current medications. Same for the
// `*float64`s, because a zero eGFR is anuric renal failure and not "unknown".
//
// **There is no patient identifier in it and there must not be.** The handler resolves a patient
// into one of these and throws the id away at the boundary.
type Picture struct {
	AgeYears *float64 `json:"age_years"`
	// Pregnancy is PREGNANT, BREASTFEEDING, PLANNING, NOT_PREGNANT, or empty for unknown.
	Pregnancy string `json:"pregnancy"`

	EGFR     *float64   `json:"egfr"`
	EGFRAsOf *time.Time `json:"egfr_as_of,omitempty"`

	// Hepatic is NONE, MILD, MODERATE, SEVERE, or empty for unknown.
	Hepatic string `json:"hepatic"`

	// Diagnoses are coded diagnoses. Nil means nobody asked.
	Diagnoses []string `json:"diagnoses"`

	// AllergenGroups are the groups the patient reacts to, **before** cross-reactivity
	// expansion. Nil means allergy status has not been established, which is a different fact
	// from "no known allergy" and fails closed.
	AllergenGroups []string `json:"allergen_groups"`

	// Current is what the patient is already taking. Nil means the medication list has not
	// been read.
	Current []Item `json:"current"`
}

// Request is one whole safety check.
type Request struct {
	FacilityID uuid.UUID
	// Proposed is the draft prescription.
	Proposed []Item
	Picture  Picture
	// At is the instant to evaluate against. `time.Now()` for a live check; the instant of a
	// past check when reproducing one. **Criterion 5 is this parameter and nothing else** —
	// nothing about the evaluation changes between the two calls.
	At time.Time
}

// ---------------------------------------------------------------------------
// What comes back
// ---------------------------------------------------------------------------

// Verdict is the one word the interface leads with.
type Verdict string

const (
	// VerdictBlocked — at least one BLOCK rule fired. Do not print this prescription.
	VerdictBlocked Verdict = "BLOCKED"
	// VerdictCannotVerify — no block fired, but something could not be checked at all. Ranked
	// above a warning because an unknown is not a smaller version of a known risk.
	VerdictCannotVerify Verdict = "CANNOT_VERIFY"
	// VerdictWarnings — rules fired, none of them blocking.
	VerdictWarnings Verdict = "WARNINGS"
	// VerdictClearWithinCoverage — every live rule that was about these drugs was satisfied.
	//
	// **Not "SAFE", and the name is the point.** The rule library covers what Dr. Nahid has
	// approved and nothing else, so the strongest true statement the engine can make is about
	// its own coverage. `UncoveredCount` sits beside this and is what the screen has to show
	// next to it.
	VerdictClearWithinCoverage Verdict = "CLEAR_WITHIN_COVERAGE"
	// VerdictNoRulesApproved — the library has no live rule, so nothing was checked.
	//
	// The state of this clinic today: all forty seeded rules are drafts nobody has approved.
	// Its own verdict rather than a clear one with a zero beside it, because the difference
	// between "everything was checked and passed" and "nothing was checked" is the whole
	// checkpoint.
	VerdictNoRulesApproved Verdict = "NO_RULES_APPROVED"
	// VerdictNothingProposed — an empty item list.
	VerdictNothingProposed Verdict = "NOTHING_PROPOSED"
)

// CoverageState is what the engine can say about one proposed drug.
type CoverageState string

const (
	// CoverageClear — a live rule was about this drug, and it did not fire.
	CoverageClear CoverageState = "COVERED_CLEAR"
	// CoverageFiring — a live rule was about this drug and it fired, or could not be verified.
	CoverageFiring CoverageState = "COVERED_FIRING"
	// CoverageNone — **no live rule was about this drug at all.**
	//
	// The state criterion 3 exists for. A drug in this state has not been checked, and a
	// screen that draws it the same as CoverageClear is the "incomplete rule set presented as
	// complete safety checking" §7.2 calls worse than no engine.
	CoverageNone CoverageState = "NOT_COVERED"
)

// Coverage is what was checked about one proposed drug, and what was not.
type Coverage struct {
	Ref     string `json:"ref"`
	Label   string `json:"label"`
	Generic string `json:"generic,omitempty"`
	Class   string `json:"class,omitempty"`

	State CoverageState `json:"state"`
	// RulesConsidered is how many live rules were about this drug. Zero on CoverageNone, by
	// definition, and returned so a screen can say "checked against 4 rules" rather than
	// asking the reader to trust a green tick.
	RulesConsidered int `json:"rules_considered"`
	// Findings is how many of those had something to say.
	Findings int `json:"findings"`

	// InFormulary is false for a drug this clinic does not hold. Such a drug is uncovered by
	// definition — no rule can name a molecule the rule validator would have refused — and the
	// reason matters to the reader, so it is reported rather than inferred.
	InFormulary bool `json:"in_formulary"`
	// ComponentsKnown is false for a medicine whose molecules nobody has written out. Duplicate
	// detection about it answers "cannot verify"; see migration 00058.
	ComponentsKnown bool `json:"components_known"`

	NoteEN string `json:"note_en"`
	NoteBN string `json:"note_bn"`
}

// EvaluatedVersion is one rule version this check ran. **Criterion 5 is this list.**
//
// Returned in the response and written into the `SAFETY_CHECK_RUN` audit entry, so that the
// question asked months later — "what was this prescription checked against" — has an answer
// that survives somebody publishing a v3 and somebody withdrawing the rule.
type EvaluatedVersion struct {
	Rule      string    `json:"rule"`
	RuleID    uuid.UUID `json:"rule_id"`
	VersionID uuid.UUID `json:"version_id"`
	Version   int       `json:"version"`
	Severity  Severity  `json:"severity"`
}

// Result is the whole answer.
type Result struct {
	// At is the instant the ruleset was taken at. Re-sending it reproduces this check exactly.
	At time.Time `json:"at"`

	Verdict Verdict `json:"verdict"`
	// SummaryEN and SummaryBN are the sentence the screen leads with. Always present, always
	// in both languages, and written so that the empty case says what was *not* done.
	SummaryEN string `json:"summary_en"`
	SummaryBN string `json:"summary_bn"`

	// Findings are ordered by severity, blocks first, with cannot-verifies after the rules
	// that definitely fired at the same severity. Each carries its rule code, version and
	// citation.
	Findings []Finding `json:"findings"`

	// Coverage is one entry per proposed drug, in the order they were sent.
	Coverage []Coverage `json:"coverage"`
	// UncoveredCount is how many proposed drugs no rule was about. Lifted out of the coverage
	// list because it is the number a screen has to show beside the verdict, and a number a
	// client has to compute is a number a client will forget to compute.
	UncoveredCount int `json:"uncovered_count"`

	// RulesLive is how many rules were live at `At`, whether or not they were about these
	// drugs. Zero is [VerdictNoRulesApproved].
	RulesLive int `json:"rules_live"`
	// Evaluated is every version that ran. Criterion 5.
	Evaluated []EvaluatedVersion `json:"evaluated_versions"`

	// CrossReactivity says what the allergy expansion did, and what it refused to do.
	CrossReactivity CrossReactivityReport `json:"cross_reactivity"`

	// Renal is the kidney function this check ran against, the date it was taken, and whether
	// that date is still inside the facility's window (CP79).
	//
	// **Criterion 1 of CP79 is this field.** It is returned on every check, including the ones
	// where there is no eGFR at all, because "which kidney function did you use" must have an
	// answer on the screen rather than be inferred from whether a renal rule happened to fire.
	Renal RenalStatus `json:"renal"`
}

// CrossReactivityReport is what an allergy was expanded into, and what it could not be.
type CrossReactivityReport struct {
	// Reported are the groups the patient's record names. Nil when allergy status has not been
	// established.
	Reported []string `json:"reported"`
	// Added are the groups an approved mapping added.
	Added []string `json:"added,omitempty"`
	// Unapproved are mappings that would have expanded one of the reported groups and were not
	// used because nobody has approved them. **Each of these produces a cannot-verify finding**
	// — the brief's decision, and the right one.
	Unapproved []string `json:"unapproved,omitempty"`
}

// Blocked reports whether anything in this result stops a prescription.
func (r Result) Blocked() bool { return r.Verdict == VerdictBlocked }

// ---------------------------------------------------------------------------
// The engine
// ---------------------------------------------------------------------------

// Engine runs a whole safety check.
//
// It holds the rule store and the formulary, and nothing else. No patient reader, no event
// store, no logger: assembling a [Picture] from a record is the handler's job, because the
// modules that hold allergies, history and observations are ones `architecture.json` does not
// let this one import — and that restriction is worth keeping rather than working around, since
// it is what guarantees a Picture cannot acquire a patient id on the way in.
type Engine struct {
	rules     *Store
	catalogue *formulary.Store
}

// NewEngine builds one.
func NewEngine(rules *Store, catalogue *formulary.Store) *Engine {
	return &Engine{rules: rules, catalogue: catalogue}
}

// Check evaluates a draft prescription.
//
// Deterministic and clock-free: `req.At` is the only instant that matters, and it is the
// caller's. Sub-second by construction — one ruleset read, one composition read, one allergen
// read, then O(items × rules × predicates) in memory with no allocation inside the inner loop
// that is not a finding somebody asked for.
func (e *Engine) Check(ctx context.Context, req Request) (Result, error) {
	at := req.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	out := Result{At: at, Findings: []Finding{}, Coverage: []Coverage{},
		Evaluated: []EvaluatedVersion{}}

	set, err := e.rules.RulesetAt(ctx, req.FacilityID, at)
	if err != nil {
		return Result{}, err
	}
	out.RulesLive = len(set.Rules)

	compositions, err := e.catalogue.Compositions(ctx)
	if err != nil {
		return Result{}, err
	}

	// CP79. The window is the facility's, read rather than assumed: there is no constant in
	// this package that says six months, because the whole point of the table is that the
	// number is Dr. Nahid's to change.
	policy, err := e.rules.RenalPolicy(ctx, req.FacilityID)
	if err != nil {
		return Result{}, err
	}
	out.Renal = resolveRenal(req.Picture.EGFR, req.Picture.EGFRAsOf, policy, at)

	renalDependence, err := e.rules.RenalDependence(ctx)
	if err != nil {
		return Result{}, err
	}

	// Allergy expansion first: it changes the Context every allergy rule then reads.
	groups, reactions, err := e.rules.Allergens(ctx)
	if err != nil {
		return Result{}, err
	}
	expanded, report := expandAllergies(req.Picture.AllergenGroups, groups, reactions)
	out.CrossReactivity = report

	// Every item that named a product rather than a molecule, resolved in one pass before the
	// loop: a lookup inside it would be one round trip per line, and "sub-second" is a promise
	// about a twelve-line prescription on a clinic LAN rather than about a benchmark.
	byProduct, err := e.products(ctx, req)
	if err != nil {
		return Result{}, err
	}

	proposed := make([]Drug, 0, len(req.Proposed))
	inFormulary := make([]bool, 0, len(req.Proposed))
	for i, item := range req.Proposed {
		drug, known := resolve(item, compositions, byProduct, i)
		proposed = append(proposed, drug)
		inFormulary = append(inFormulary, known)
	}
	var current []Drug
	if req.Picture.Current != nil {
		current = make([]Drug, 0, len(req.Picture.Current))
		for i, item := range req.Picture.Current {
			drug, _ := resolve(item, compositions, byProduct, i)
			drug.Ref = "current:" + drug.Ref
			current = append(current, drug)
		}
	}

	c := Context{
		AgeYears: req.Picture.AgeYears, Pregnancy: req.Picture.Pregnancy,
		EGFR: req.Picture.EGFR, EGFRAsOf: req.Picture.EGFRAsOf,
		Hepatic:   req.Picture.Hepatic,
		Diagnoses: req.Picture.Diagnoses, Allergies: expanded,
		Proposed: proposed, Current: current,
	}

	// ---- the evaluation, per line rather than per rule -------------------
	//
	// Per line because a prescription with metformin and Siglimet at eGFR 25 has two
	// contraindicated lines, and a check reporting one of them leaves the physician deleting
	// the wrong drug. `Version.Evaluate` reports the strongest thing a rule has to say about a
	// whole prescription, which is right for the sandbox and wrong here.
	considered := make([]int, len(proposed))
	firing := make([]int, len(proposed))

	for ruleIndex, rule := range set.Rules {
		version := set.Versions[ruleIndex]
		if !version.Approved() {
			// Unreachable through RulesetAt, and kept for the same reason CP77 kept it in
			// Ruleset.Findings: a rule a developer drafted must not behave like a rule the
			// physician wrote, and that is worth two locks.
			continue
		}
		out.Evaluated = append(out.Evaluated, EvaluatedVersion{
			Rule: rule.Code, RuleID: rule.ID, VersionID: version.ID,
			Version: version.Version, Severity: version.Severity,
		})

		for i := range proposed {
			if !version.Covers(proposed[i]) {
				continue
			}
			considered[i]++
			f := version.EvaluateFor(rule, c, proposed[i])
			switch f.Outcome {
			case OutcomeFires, OutcomeCannotVerify:
				firing[i]++
				out.Findings = append(out.Findings, f)
			}
		}
	}

	// ---- the engine's own findings ---------------------------------------
	out.Findings = append(out.Findings, unknownDrugFindings(proposed, inFormulary)...)
	// CP79's three: no eGFR for a drug that needs one, an eGFR too old to be current, and a
	// molecule nobody has classified. They are the engine's own for the same reason the
	// coverage findings are — each is a statement about what the check could not do.
	out.Findings = append(out.Findings, renalFindings(out.Renal, proposed, renalDependence)...)
	out.Findings = append(out.Findings, crossReactivityFindings(report)...)
	if req.Picture.AllergenGroups == nil && len(proposed) > 0 {
		out.Findings = append(out.Findings, allergyStatusUnknownFinding())
	}

	// ---- coverage, per proposed drug -------------------------------------
	for i, drug := range proposed {
		out.Coverage = append(out.Coverage, coverageFor(drug, inFormulary[i],
			considered[i], firing[i]))
	}
	for _, entry := range out.Coverage {
		if entry.State == CoverageNone {
			out.UncoveredCount++
		}
	}

	sortFindings(out.Findings)
	out.Verdict = verdictOf(out, len(req.Proposed))
	out.SummaryEN, out.SummaryBN = summarise(out, len(req.Proposed))
	return out, nil
}

// products resolves every item that named a formulary product into its molecule name.
//
// A product id is what CP76's autocomplete returns and what CP81 will hold, so it is the shape
// the editor will actually send. A product this facility does not have resolves to nothing and
// the item goes on to be reported uncovered — which is right: an id nobody can resolve is a drug
// the engine cannot identify, and that is a fact about the check rather than an error in it.
func (e *Engine) products(ctx context.Context, req Request) (map[uuid.UUID]string, error) {
	wanted := map[uuid.UUID]bool{}
	collect := func(items []Item) {
		for _, item := range items {
			if item.ProductID != nil && strings.TrimSpace(item.Generic) == "" {
				wanted[*item.ProductID] = true
			}
		}
	}
	collect(req.Proposed)
	collect(req.Picture.Current)
	if len(wanted) == 0 {
		return nil, nil
	}

	out := make(map[uuid.UUID]string, len(wanted))
	for id := range wanted {
		product, err := e.catalogue.Product(ctx, req.FacilityID, id, req.At)
		if err != nil {
			// Not found, withdrawn, or belonging to another facility: all of them mean this
			// check cannot say what the drug is, which is the uncovered path rather than a
			// failed request. A 500 here would turn a stale id in an editor into an outage.
			continue
		}
		out[id] = product.GenericName
	}
	return out, nil
}

// resolve turns a caller's item into a drug the rules can match on.
//
// The second return says whether this clinic's formulary holds the molecule. False is not an
// error: a patient's current medication list legitimately names drugs bought elsewhere, and a
// physician may type a drug the formulary has not caught up with. What it must not be is
// invisible — an unresolved drug is uncovered, says so, and carries no components, so any
// duplicate question about it answers cannot verify.
func resolve(item Item, compositions map[string]formulary.Composition,
	byProduct map[uuid.UUID]string, index int) (Drug, bool) {

	name := strings.TrimSpace(item.Generic)
	if name == "" && item.ProductID != nil {
		name = byProduct[*item.ProductID]
	}
	drug := Drug{
		Ref:       strings.TrimSpace(item.Ref),
		Label:     strings.TrimSpace(item.Label),
		Generic:   name,
		DailyDose: item.DailyDose,
		DoseUnit:  strings.TrimSpace(item.DoseUnit),
	}
	if drug.Ref == "" {
		// A stable handle so that two lines of the identical medicine are two items. Derived
		// from the position, which is the only thing that distinguishes them.
		drug.Ref = "item:" + itoa(index)
	}
	if drug.Label == "" {
		drug.Label = name
	}

	composition, ok := compositions[strings.ToLower(name)]
	if !ok {
		return drug, false
	}
	// The formulary's spelling wins over the caller's, so that a rule written against
	// `core.generic.name` matches whatever case the caller sent.
	drug.Generic = composition.Generic
	drug.Class = composition.Class
	drug.Components = composition.Components
	drug.ComponentsKnown = composition.Determined
	if drug.Label == "" {
		drug.Label = composition.Generic
	}
	return drug, true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// ---------------------------------------------------------------------------
// Coverage
// ---------------------------------------------------------------------------

// coverageFor says what was checked about one drug, in words.
//
// The note is written for the physician rather than for the log. "No rule in this library is
// about this medicine" is a sentence he can act on — by writing one, or by knowing to check the
// label himself. "Not covered" on its own is a status somebody learns to ignore.
func coverageFor(drug Drug, inFormulary bool, considered, firing int) Coverage {
	entry := Coverage{
		Ref: drug.Ref, Label: drug.Label, Generic: drug.Generic, Class: drug.Class,
		RulesConsidered: considered, Findings: firing,
		InFormulary: inFormulary, ComponentsKnown: drug.ComponentsKnown,
	}
	switch {
	case considered == 0 && !inFormulary:
		entry.State = CoverageNone
		entry.NoteEN = "This clinic's formulary does not hold " + drug.Label +
			", so no rule could be about it. Nothing has been checked."
		entry.NoteBN = "এই ক্লিনিকের ওষুধ-তালিকায় " + drug.Label +
			" নেই, তাই এটি নিয়ে কোনো নিয়ম থাকতে পারে না। কিছুই যাচাই করা হয়নি।"
	case considered == 0:
		entry.State = CoverageNone
		entry.NoteEN = "No approved rule is about " + drug.Label + ". Nothing has been checked."
		entry.NoteBN = drug.Label + " নিয়ে অনুমোদিত কোনো নিয়ম নেই। কিছুই যাচাই করা হয়নি।"
	case firing > 0:
		entry.State = CoverageFiring
		entry.NoteEN = "Checked against " + itoa(considered) + " rule(s); " +
			itoa(firing) + " had something to say."
		entry.NoteBN = itoa(considered) + "টি নিয়মের সঙ্গে মিলিয়ে দেখা হয়েছে; " +
			itoa(firing) + "টিতে বলার মতো কিছু আছে।"
	default:
		entry.State = CoverageClear
		entry.NoteEN = "Checked against " + itoa(considered) +
			" rule(s), and none of them applied. Only those rules were checked."
		entry.NoteBN = itoa(considered) +
			"টি নিয়মের সঙ্গে মিলিয়ে দেখা হয়েছে, কোনোটিই প্রযোজ্য হয়নি। কেবল ওই নিয়মগুলোই দেখা হয়েছে।"
	}
	return entry
}

// unknownDrugFindings raises a cannot-verify for every proposed drug the formulary does not hold.
//
// A finding as well as a coverage entry, and the duplication is deliberate. The coverage list is
// what a careful reader consults; the finding list is what a busy one reads. A drug the engine
// could not identify belongs in both, because "I do not know what this is" is a different and
// more serious statement than "no rule mentioned it".
func unknownDrugFindings(proposed []Drug, inFormulary []bool) []Finding {
	var out []Finding
	for i, drug := range proposed {
		if inFormulary[i] {
			continue
		}
		out = append(out, Finding{
			RuleCode: CodeUnknownDrug, Type: TypeContraindication, Severity: SeverityWarn,
			Outcome: OutcomeCannotVerify, Subject: drug.Label, SubjectRef: drug.Ref,
			MessageEN: "Safety cannot be verified for " + drug.Label +
				": this clinic's formulary does not hold it, so no rule, no interaction check " +
				"and no duplicate check could be applied to it.",
			MessageBN: drug.Label + "-এর নিরাপত্তা যাচাই করা যায়নি: এই ক্লিনিকের ওষুধ-তালিকায় এটি নেই, " +
				"তাই এর উপর কোনো নিয়ম, কোনো পারস্পরিক প্রতিক্রিয়া বা পুনরাবৃত্তির পরীক্ষা চালানো যায়নি।",
			AdviceEN: "Add it to the formulary, or check the label yourself before signing.",
			AdviceBN: "ওষুধটি তালিকায় যোগ করুন, নয়তো সই করার আগে নিজে লেবেল দেখে নিন।",
			Source:   "CP78 coverage reporting; §7.2's requirement that a drug without rules is never reported as safe.",
			Missing:  []Datum{DatumFormulary},
		})
	}
	return out
}

// crossReactivityFindings raises a cannot-verify for every mapping that was refused.
//
// **The brief's decision, and it is the right one.** The cross-reactivity map is CP77 seed
// content: six groups and four directed reactions, none of them read by a physician. Expanding
// an allergy through an unapproved mapping would let a developer's reading of the literature
// suppress or raise a warning on a real prescription. Silently *not* expanding is worse still,
// because it looks exactly like a patient with no cross-reactivity — so the refusal is said out
// loud, once per mapping, at WARN.
func crossReactivityFindings(report CrossReactivityReport) []Finding {
	var out []Finding
	for _, pair := range report.Unapproved {
		out = append(out, Finding{
			RuleCode: CodeCrossReactivityUnapproved, Type: TypeContraindication,
			Severity: SeverityWarn, Outcome: OutcomeCannotVerify,
			MessageEN: "Cross-reactivity cannot be checked for " + pair +
				": nobody has approved that mapping, so the allergy was not expanded through it.",
			MessageBN: pair + "-এর ক্রস-রিঅ্যাকশন যাচাই করা যায়নি: ওই সম্পর্কটি কেউ অনুমোদন করেননি, " +
				"তাই অ্যালার্জিটি ওই পথে বিস্তৃত করা হয়নি।",
			AdviceEN: "Approve the mapping in the rule library, or decide this one by hand.",
			AdviceBN: "নিয়ম-তালিকায় সম্পর্কটি অনুমোদন করুন, অথবা এটি নিজে বিবেচনা করুন।",
			Source:   "CP77 seeded cross-reactivity map; approval is required before it may affect a prescription.",
			Missing:  []Datum{DatumCrossReactivity},
		})
	}
	return out
}

// allergyStatusUnknownFinding is raised whenever nobody has established allergy status.
//
// # Why this is not left to the rules
//
// A rule with an ALLERGY predicate answers "cannot verify" when the list is nil — but only if its
// subject is one of the prescribed drugs. A prescription of metformin, pioglitazone and ramipril
// meets no allergy rule at all, so nothing is said, and the physician sees a clean check on a
// patient nobody has ever asked about allergies.
//
// That is the shape of silence this checkpoint exists to remove. Allergy status is one of the
// five things §7.2 multiplies the proposed drugs by, and its absence is a fact about the whole
// check rather than about any one line — so the engine says it once, whatever was prescribed.
//
// WARN rather than BLOCK, and the reason is that CP54 already gates the queue on allergy status
// in the database: a patient reaching a prescription with no allergy status recorded is an
// unusual path rather than the normal one, and blocking here would be a second gate arguing with
// the first. What this does is make sure the physician on that unusual path is told.
func allergyStatusUnknownFinding() Finding {
	return Finding{
		RuleCode: CodeAllergyStatusUnknown, Type: TypeContraindication, Severity: SeverityWarn,
		Outcome: OutcomeCannotVerify,
		MessageEN: "Nobody has established this patient's allergy status, so nothing on this " +
			"prescription has been checked against an allergy.",
		MessageBN: "এই রোগীর অ্যালার্জির তথ্য কেউ নেননি, তাই এই ব্যবস্থাপত্রের কিছুই কোনো " +
			"অ্যালার্জির সঙ্গে মিলিয়ে দেখা হয়নি।",
		AdviceEN: "Ask, and record the answer — including \"no known allergy\", which is an " +
			"answer and not a blank.",
		AdviceBN: "জিজ্ঞাসা করুন এবং উত্তরটি লিখুন — \"জানা কোনো অ্যালার্জি নেই\"ও একটি উত্তর, " +
			"ফাঁকা নয়।",
		Source:  "CP54: NONE_RECORDED is \"nobody has asked\" and is a different fact from NO_KNOWN_ALLERGY. CP78 fail-closed behaviour.",
		Missing: []Datum{DatumAllergies},
	}
}

// The codes the engine cites for findings that are its own rather than a rule's.
//
// Named constants rather than string literals because a client branches on them, and because
// they appear in the golden suite as the expected citation of two of its scenarios.
const (
	// CodeUnknownDrug — a proposed drug this formulary does not hold.
	CodeUnknownDrug = "COVERAGE-UNKNOWN-DRUG"
	// CodeCrossReactivityUnapproved — an allergy that could not be expanded.
	CodeCrossReactivityUnapproved = "XREACT-UNAPPROVED"
	// CodeAllergyStatusUnknown — nobody has asked this patient about allergies.
	CodeAllergyStatusUnknown = "ALLERGY-STATUS-UNKNOWN"
)

// Two more data a check can lack, beyond CP77's eight.
const (
	// DatumFormulary — the engine could not say what a proposed drug is.
	DatumFormulary Datum = "FORMULARY"
	// DatumCrossReactivity — an allergen mapping nobody has approved.
	DatumCrossReactivity Datum = "CROSS_REACTIVITY"
	// DatumComponents — a medicine whose molecules nobody has written out.
	DatumComponents Datum = "COMPONENTS"
)

// ---------------------------------------------------------------------------
// Allergy expansion
// ---------------------------------------------------------------------------

// expandAllergies widens a patient's reported allergen groups through the cross-reactivity map.
//
// # Nil in, nil out
//
// An unestablished allergy status is nil and stays nil, so that every allergy predicate answers
// *cannot tell* rather than *no reaction recorded*. Expanding nil into an empty slice here would
// turn "nobody asked" into "asked, and there were none" at the one boundary where the difference
// decides whether a penicillin warning appears.
//
// # Only approved mappings widen anything
//
// A mapping nobody has approved is not used, and is reported so the caller can raise the
// cannot-verify finding. A mapping at risk NONE is approved-or-not like any other, and when it
// is approved it adds nothing — which is its whole purpose: the sulfonamide-antibiotic row exists
// to record that the cross-reaction most prescribers assume is not supported, and a map that
// omitted the row would leave the belief in place.
func expandAllergies(reported []string, groups []AllergenGroup,
	reactions []CrossReaction) ([]string, CrossReactivityReport) {

	report := CrossReactivityReport{Reported: reported}
	if reported == nil {
		return nil, report
	}

	approvedGroup := map[string]bool{}
	for _, g := range groups {
		if g.Approved && g.IsActive {
			approvedGroup[g.Code] = true
		}
	}

	have := map[string]bool{}
	out := make([]string, 0, len(reported))
	for _, code := range reported {
		code = strings.ToUpper(strings.TrimSpace(code))
		if code == "" || have[code] {
			continue
		}
		have[code] = true
		out = append(out, code)
	}

	// One pass, not a transitive closure. Penicillin → cephalosporin → penicillin is a cycle,
	// and a second hop is a clinical claim nobody made: the map says what a reaction to one
	// group implies about another, not what it implies about the others' others.
	for _, reaction := range reactions {
		if !have[strings.ToUpper(reaction.From)] {
			continue
		}
		pair := reaction.From + " → " + reaction.To
		if !reaction.IsActive {
			continue
		}
		if !reaction.Approved || !approvedGroup[reaction.From] || !approvedGroup[reaction.To] {
			report.Unapproved = append(report.Unapproved, pair)
			continue
		}
		if reaction.Risk == "NONE" {
			// Approved, and it says there is nothing to add. Not an expansion and not a
			// refusal — the map answering the question.
			continue
		}
		to := strings.ToUpper(strings.TrimSpace(reaction.To))
		if to == "" || have[to] {
			continue
		}
		have[to] = true
		out = append(out, to)
		report.Added = append(report.Added, to)
	}
	sort.Strings(report.Unapproved)
	return out, report
}

// ---------------------------------------------------------------------------
// Ordering, the verdict and the sentence
// ---------------------------------------------------------------------------

// sortFindings puts the list in the order a physician reads it.
//
// Blocks first; within a severity, rules that definitely fired before rules that could not be
// verified; then by code so the order is total and a diff between two runs shows what changed
// rather than what was reordered. The same comparison CP77's `Ruleset.Findings` uses, kept
// identical on purpose — a sandbox and an engine that ordered findings differently would show a
// physician two different top-of-list findings for one picture.
func sortFindings(out []Finding) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity.Rank() != out[j].Severity.Rank() {
			return out[i].Severity.Rank() < out[j].Severity.Rank()
		}
		if (out[i].Outcome == OutcomeFires) != (out[j].Outcome == OutcomeFires) {
			return out[i].Outcome == OutcomeFires
		}
		if out[i].RuleCode != out[j].RuleCode {
			return out[i].RuleCode < out[j].RuleCode
		}
		return out[i].SubjectRef < out[j].SubjectRef
	})
}

// verdictOf reduces the whole result to the one word the screen leads with.
//
// Order matters and is argued: a block outranks everything; an unverifiable outranks a warning,
// because an unknown is not a smaller version of a known risk; and the clear case is last and is
// spelled "clear within coverage".
func verdictOf(r Result, proposed int) Verdict {
	if proposed == 0 {
		return VerdictNothingProposed
	}
	if r.RulesLive == 0 {
		return VerdictNoRulesApproved
	}
	warn := false
	unverified := false
	for _, f := range r.Findings {
		switch {
		case f.Outcome == OutcomeFires && f.Severity == SeverityBlock:
			return VerdictBlocked
		case f.Outcome == OutcomeCannotVerify:
			unverified = true
		case f.Outcome == OutcomeFires && f.Severity != SeverityInfo:
			// INFO deliberately does not raise the verdict. CP77's scale says INFO is "worth
			// knowing, nothing to decide", and the SULFA-SU-ALLERGY rule is the example that
			// settles it: its whole purpose is to stop a physician needlessly withholding a
			// sulphonylurea, and a verdict of WARNINGS on it would do the opposite. The
			// summary sentence names the notes instead.
			warn = true
		}
	}
	switch {
	case unverified:
		return VerdictCannotVerify
	case warn:
		return VerdictWarnings
	default:
		return VerdictClearWithinCoverage
	}
}

// summarise writes the verdict as a sentence, in both languages.
//
// **Every one of these says what was not checked.** That is criterion 3 as a sentence rather than
// as a field: a screen that renders only this line must still be honest, because a screen that
// renders only this line is the screen that will exist.
func summarise(r Result, proposed int) (string, string) {
	uncovered := r.UncoveredCount
	switch r.Verdict {
	case VerdictNothingProposed:
		return "Nothing has been proposed, so nothing has been checked.",
			"কিছু প্রস্তাব করা হয়নি, তাই কিছু যাচাইও করা হয়নি।"

	case VerdictNoRulesApproved:
		return "No medication safety rule has been approved yet, so none of these " +
				itoa(proposed) + " medicine(s) has been checked against anything. " +
				"This is not a clean result — it is no result.",
			"এখনও কোনো ওষুধ-নিরাপত্তার নিয়ম অনুমোদিত হয়নি, তাই এই " + itoa(proposed) +
				"টি ওষুধের কোনোটিই কিছুর সঙ্গে মিলিয়ে দেখা হয়নি। এটি নিরাপদ বলে ফলাফল নয় — এটি কোনো ফলাফলই নয়।"

	case VerdictBlocked:
		return "Do not prescribe as written: " + itoa(countBlocks(r.Findings)) +
				" rule(s) block this prescription. " + uncoveredEN(uncovered),
			"এই অবস্থায় ব্যবস্থাপত্র দেবেন না: " + itoa(countBlocks(r.Findings)) +
				"টি নিয়ম এটি আটকাচ্ছে। " + uncoveredBN(uncovered)

	case VerdictCannotVerify:
		return "Safety could not be verified for part of this prescription — " +
				itoa(countUnverified(r.Findings)) + " check(s) lacked the information they needed. " +
				uncoveredEN(uncovered),
			"এই ব্যবস্থাপত্রের কিছু অংশের নিরাপত্তা যাচাই করা যায়নি — " +
				itoa(countUnverified(r.Findings)) + "টি পরীক্ষার প্রয়োজনীয় তথ্য ছিল না। " +
				uncoveredBN(uncovered)

	case VerdictWarnings:
		return itoa(len(r.Findings)) + " rule(s) have something to say about this prescription. " +
				uncoveredEN(uncovered),
			itoa(len(r.Findings)) + "টি নিয়মের এই ব্যবস্থাপত্র নিয়ে কিছু বলার আছে। " +
				uncoveredBN(uncovered)

	default:
		// An INFO rule that fired does not raise the verdict — CP77's severities say INFO is
		// "worth knowing, nothing to decide", and a screen that went amber for a note is a
		// screen that goes amber every day. But the sentence must not then claim every rule
		// was satisfied, because one was not: it fired, and it had something to say.
		notes := ""
		notesBN := ""
		if n := len(r.Findings); n > 0 {
			notes = " " + itoa(n) + " note(s) below are worth reading."
			notesBN = " নিচের " + itoa(n) + "টি নোট পড়ার মতো।"
		}
		return "No approved rule raised a warning about these medicines." + notes +
				" That is not the same as safe: " + uncoveredEN(uncovered),
			"এই ওষুধগুলো নিয়ে কোনো অনুমোদিত নিয়ম সতর্ক করেনি।" + notesBN +
				" এর অর্থ নিরাপদ নয়: " + uncoveredBN(uncovered)
	}
}

func uncoveredEN(n int) string {
	if n == 0 {
		return "Every medicine on it was covered by at least one rule."
	}
	return itoa(n) + " medicine(s) on it are covered by no rule at all and have not been checked."
}

func uncoveredBN(n int) string {
	if n == 0 {
		return "এর প্রতিটি ওষুধ অন্তত একটি নিয়মের আওতায় ছিল।"
	}
	return "এর " + itoa(n) + "টি ওষুধ কোনো নিয়মের আওতাতেই পড়ে না এবং যাচাই করা হয়নি।"
}

func countBlocks(findings []Finding) int {
	n := 0
	for _, f := range findings {
		if f.Outcome == OutcomeFires && f.Severity == SeverityBlock {
			n++
		}
	}
	return n
}

func countUnverified(findings []Finding) int {
	n := 0
	for _, f := range findings {
		if f.Outcome == OutcomeCannotVerify {
			n++
		}
	}
	return n
}

// Fold adds findings the engine could not have produced itself and recomputes the answer.
//
// # Why this exists, and why it is exported
//
// [Engine.Check] takes a [Picture] and knows nothing about how that picture was read. Some
// findings are facts about the *reading* rather than about the drugs — an allergy no allergen
// group matched is the one CP78 shipped — so the handler that assembled the picture folds them
// in afterwards.
//
// CP78 did that inline, in its own handler, with the unexported `sortFindings` and `verdictOf`.
// CP80 needed the identical three lines for the prescription-scoped route, and the choice was
// between copying them and exporting them. Copying would have meant two places that decide
// whether an unclassifiable anaphylaxis blocks a prescription, and the second copy would be the
// one somebody forgets to update — so it is one function, and both handlers call it.
//
// **The verdict and the summary are recomputed, not patched.** A block raised here has to
// actually block, rather than sit at the bottom of a list under a verdict that was decided
// before it arrived.
func Fold(result Result, extra []Finding, proposed int) Result {
	if len(extra) == 0 {
		return result
	}
	result.Findings = append(result.Findings, extra...)
	sortFindings(result.Findings)
	result.Verdict = verdictOf(result, proposed)
	result.SummaryEN, result.SummaryBN = summarise(result, proposed)
	return result
}
