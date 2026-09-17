package qa

import (
	"fmt"
	"sort"
	"strings"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinicalterm"
)

// The engine: a rule table plus a file, in and a list of findings out.
//
// # Why an empty rule table is a clean result and not a skipped check
//
// [Ruleset.Evaluate] on an empty table returns a [Review] with no findings and `RulesLive: 0`.
// The officer sees "no rules are configured — nothing to check", clicks clear, and the clearance
// is recorded. Nothing about the empty table reaches the gate: `core.qa_clearance_stands()` reads
// `read.qa_review` and has no path to `core.qa_rule` at all.
//
// This is `docs/qa-rules.md` §5's distinction, and the reason it is stated here as well as in the
// migration is that the two halves live in two places and either one alone would be wrong. A Go
// engine that refused to clear an unchecked file would be a check one refactor removes; a
// database gate with no engine would be a station with nothing on its screen.

// Ruleset is the live rules of one facility, in the order they will be reported.
type Ruleset struct {
	Rules []Rule
}

// NewRuleset orders and validates rows read from `core.qa_rule`.
//
// **A rule that does not describe something askable is refused here, loudly, rather than
// skipped.** A skipped rule is a rule that is present on the configuration screen, enabled, and
// silent — which `docs/qa-rules.md` §1 identifies as worse than having no station at all, because
// it creates a record saying somebody checked.
func NewRuleset(rows []Rule) (Ruleset, error) {
	live := make([]Rule, 0, len(rows))
	problems := []string{}
	for _, rule := range rows {
		if !rule.Live() {
			continue
		}
		if err := rule.Validate(); err != nil {
			problems = append(problems, rule.Code+": "+err.Error())
			continue
		}
		live = append(live, rule)
	}
	if len(problems) > 0 {
		return Ruleset{}, fmt.Errorf("%w: %s", ErrRuleInvalid, strings.Join(problems, "; "))
	}

	sort.SliceStable(live, func(i, j int) bool {
		// Blocks before warnings, then the table's own ordering. A screen that listed them in
		// primary-key order would put the thing that stops the patient below the thing that
		// does not, and the officer reads top-down with somebody waiting.
		if live[i].Sev != live[j].Sev {
			return live[i].Sev == SeverityBlock
		}
		if live[i].Ordering != live[j].Ordering {
			return live[i].Ordering < live[j].Ordering
		}
		return live[i].Code < live[j].Code
	})
	return Ruleset{Rules: live}, nil
}

// Validate refuses a row that cannot ask its own question.
//
// Each check is here because the corresponding row is writable: severity, window, parameters and
// the station are all things somebody can change through `qa.rule.write`, and every one of them
// has a value that would make the rule silently never fire. The database catches the two it can
// see from a constraint — a BLOCK with no station, a window on a shape with none — and this
// catches the ones that need to know what the shape reads.
func (r Rule) Validate() error {
	known := false
	for _, k := range AllKinds {
		if k == r.Kind {
			known = true
		}
	}
	if !known {
		return ErrUnknownKind
	}
	if r.Sev != SeverityBlock && r.Sev != SeverityWarn {
		return fmt.Errorf("severity %q is neither BLOCK nor WARN", r.Sev)
	}
	if r.Sev == SeverityBlock && r.BounceStation == "" {
		return fmt.Errorf("a BLOCK names the station the patient walks back to")
	}
	if strings.TrimSpace(r.TitleEN) == "" || strings.TrimSpace(r.TitleBN) == "" {
		return fmt.Errorf("a finding needs its title in both languages")
	}

	switch r.Kind {
	case KindObservationRecent:
		if len(r.Params.Codes) == 0 {
			return fmt.Errorf("names no observation code, so it can never find one missing")
		}
		if r.WindowDays == nil || *r.WindowDays <= 0 {
			return fmt.Errorf("needs a recency window: how recent is recent enough is the clinic's number")
		}
	case KindObservationThisVisit, KindWeightForWeightBasedDose:
		if len(r.Params.Codes) == 0 {
			return fmt.Errorf("names no observation code, so it can never find one missing")
		}
	case KindCounselingItemCovered:
		if len(r.Params.ItemCodes) == 0 {
			return fmt.Errorf("names no counselling item, so it asks about nothing")
		}
	case KindSafetyEngineFinding:
		if r.Params.MinSeverity != "" && !contains([]string{"INFO", "WARN", "BLOCK"},
			strings.ToUpper(r.Params.MinSeverity)) {
			return fmt.Errorf("min_severity %q is not one of INFO, WARN, BLOCK", r.Params.MinSeverity)
		}
	case KindTeratogenPregnancyStatus:
		if r.Params.AgeMin != nil && r.Params.AgeMax != nil && *r.Params.AgeMin > *r.Params.AgeMax {
			return fmt.Errorf("the age band runs backwards")
		}
	}
	return nil
}

// Evaluate asks every live rule about one file.
//
// Deterministic and clock-free: `e.Now` is the only instant that matters, and it is the caller's,
// so a review re-run against the same evidence produces the same findings — which is what makes
// the decision recorded against it reproducible.
func (s Ruleset) Evaluate(e Evidence) Review {
	review := Review{
		PrescriptionID: e.PrescriptionID,
		PatientID:      e.PatientID,
		VisitID:        e.VisitID,
		At:             e.Now,
		Findings:       []Finding{},
		RulesLive:      len(s.Rules),
		Stations:       e.StationNames,
	}
	for _, rule := range s.Rules {
		review.Findings = append(review.Findings, rule.Evaluate(e)...)
	}

	// Blocks first inside the result too, not only inside the rule order: `mandatoryStations`
	// can emit several findings from one rule and a later rule may be a block.
	sort.SliceStable(review.Findings, func(i, j int) bool {
		return review.Findings[i].Sev == SeverityBlock && review.Findings[j].Sev != SeverityBlock
	})
	return review
}

// SummaryEN and SummaryBN are the sentence the screen leads with.
//
// Written so that **the empty case says what was not done**, which is the same discipline CP78's
// summary follows and the same reason: "nothing found" and "nothing looked" render identically
// unless one of them says so, and at this station the difference is whether a clinic has a
// quality check or a button.
func (r Review) SummaryEN() string {
	blocking, warnings := len(r.Blocking()), len(r.Warnings())
	switch {
	case r.RulesLive == 0:
		return "No QA rules are configured for this clinic, so nothing was checked. " +
			"Clearance is still required before this prescription can be signed."
	case blocking == 0 && warnings == 0:
		return clinicalterm.Sentence(
			clinicalterm.PluralEN(r.RulesLive, "check", "checks")) + " passed. This file is complete."
	case blocking == 0:
		return clinicalterm.Sentence(
			clinicalterm.PluralEN(warnings, "warning", "warnings")) +
			" to acknowledge. Nothing blocks this file."
	case warnings == 0:
		return clinicalterm.Sentence(
			clinicalterm.PluralEN(blocking, "blocking finding", "blocking findings")) +
			". This file cannot be cleared."
	default:
		return clinicalterm.Sentence(
			clinicalterm.PluralEN(blocking, "blocking finding", "blocking findings")) +
			" and " + clinicalterm.PluralEN(warnings, "warning", "warnings") +
			". This file cannot be cleared."
	}
}

// SummaryBN is the same sentence in clinical Bengali.
func (r Review) SummaryBN() string {
	blocking, warnings := len(r.Blocking()), len(r.Warnings())
	switch {
	case r.RulesLive == 0:
		return "এই ক্লিনিকের জন্য কোনো কিউএ নিয়ম নির্ধারণ করা নেই, তাই কিছুই যাচাই করা হয়নি। " +
			"তবু স্বাক্ষরের আগে ছাড়পত্র দিতেই হবে।"
	case blocking == 0 && warnings == 0:
		return clinicalterm.Digits(r.RulesLive) + "টি যাচাইয়ের সবগুলিই উত্তীর্ণ। ফাইলটি সম্পূর্ণ।"
	case blocking == 0:
		return clinicalterm.Digits(warnings) + "টি সতর্কবার্তা স্বীকার করতে হবে। কিছুই আটকাচ্ছে না।"
	case warnings == 0:
		return clinicalterm.Digits(blocking) + "টি বাধা। এই ফাইলে ছাড়পত্র দেওয়া যাবে না।"
	default:
		return clinicalterm.Digits(blocking) + "টি বাধা ও " + clinicalterm.Digits(warnings) +
			"টি সতর্কবার্তা। এই ফাইলে ছাড়পত্র দেওয়া যাবে না।"
	}
}
