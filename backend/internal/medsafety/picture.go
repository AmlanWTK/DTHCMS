package medsafety

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Building a clinical picture from a real patient (CP78).
//
// # Why this is an interface rather than a set of imports
//
// The facts §7.2 multiplies the proposed drugs by live in four modules this one may not import:
// allergies in `allergy`, current medicines in `history`, kidney function in `clinical`, age and
// pregnancy on the patient record. `architecture.json` allows `medsafety` only `platform`,
// `formulary` and `clinical`, and the restriction is worth keeping rather than widening.
//
// Not because layering is tidy. Because of what the restriction buys: a [Picture] cannot acquire
// a patient identifier on the way in. The engine reads a patient's allergies, diagnoses and
// kidney function, and a module that could also read the register is a module one refactor away
// from logging which patient the eGFR of 24 belonged to. The bridge in `cmd/api` does the
// reading; the id stops at the boundary; what crosses is six clinical facts and no name.
//
// # The contract this interface really carries is nil versus empty
//
// Every list method may return nil, and nil is not "none". It is **"nobody asked"**, and it is
// what makes a rule about that fact answer *cannot verify* instead of *does not apply*. An
// implementation that returned an empty slice where it meant "not established" would turn every
// fail-closed case in this checkpoint into a silent pass, and nothing downstream could tell.
// [FactsContract] is a test any implementation can be run through, and the bridge is.

// ReportedAllergy is one thing a patient says they react to, as the record holds it.
//
// Deliberately not an allergen group. The record holds what the patient said and what the
// terminology matched; **which group that is** is this module's question, because the groups and
// their membership are CP77's tables and the mapping is where an unclassifiable allergy has to
// be noticed rather than dropped.
type ReportedAllergy struct {
	// Code is the terminology code, when the substance was codeable.
	Code string
	// Display is the catalogue's words for it.
	Display string
	// Said is the patient's own words, and is the only field on an uncoded allergy.
	Said string
	// Emergency marks a reaction the record considers life-threatening. Carried so that an
	// allergy the engine cannot classify is reported more loudly when the reaction was
	// anaphylaxis than when it was a rash.
	Emergency bool
}

// PatientFacts is everything the engine needs about a patient, and nothing else.
//
// Six methods, each returning one fact and its absence. Implemented by a bridge in `cmd/api`
// over `allergy`, `history`, `clinical` and `patient`.
type PatientFacts interface {
	// Age returns the patient's age in years. Nil when the date of birth is not recorded.
	Age(ctx context.Context, facility, patient uuid.UUID) (*float64, error)

	// Pregnancy returns PREGNANT, BREASTFEEDING, PLANNING, NOT_PREGNANT, or **empty for not
	// recorded**. Empty and NOT_PREGNANT are different: the first fails closed.
	Pregnancy(ctx context.Context, facility, patient uuid.UUID) (string, error)

	// Renal returns the most recent eGFR and the instant it was effective. Nil when there is
	// none. A zero eGFR is anuric renal failure and is not nil.
	Renal(ctx context.Context, facility, patient uuid.UUID) (*float64, *time.Time, error)

	// Hepatic returns NONE, MILD, MODERATE, SEVERE, or empty for not assessed.
	Hepatic(ctx context.Context, facility, patient uuid.UUID) (string, error)

	// Diagnoses returns the patient's coded diagnoses. **Nil means the diagnosis list was not
	// read**; an empty non-nil slice means it was read and there are none.
	Diagnoses(ctx context.Context, facility, patient uuid.UUID) ([]string, error)

	// Allergies returns what the patient reports. **Nil means allergy status has not been
	// established** — which is a different fact from "no known allergy" and is the one that
	// fails closed.
	Allergies(ctx context.Context, facility, patient uuid.UUID) ([]ReportedAllergy, error)

	// CurrentMedications returns what the patient is already taking. **Nil means the list was
	// not read.**
	CurrentMedications(ctx context.Context, facility, patient uuid.UUID) ([]Item, error)
}

// Assemble reads one patient into a [Picture], and reports the allergies it could not classify.
//
// Returns the picture, the reported allergies no allergen group matched, and an error. The second
// return is not a nicety: an allergy the engine cannot place in a group is an allergy no rule can
// fire on, and it has to become a visible cannot-verify rather than a substance that quietly
// influenced nothing.
func (e *Engine) Assemble(ctx context.Context, facts PatientFacts,
	facility, patient uuid.UUID) (Picture, []ReportedAllergy, error) {

	var picture Picture
	var err error

	if picture.AgeYears, err = facts.Age(ctx, facility, patient); err != nil {
		return Picture{}, nil, err
	}
	if picture.Pregnancy, err = facts.Pregnancy(ctx, facility, patient); err != nil {
		return Picture{}, nil, err
	}
	if picture.EGFR, picture.EGFRAsOf, err = facts.Renal(ctx, facility, patient); err != nil {
		return Picture{}, nil, err
	}
	if picture.Hepatic, err = facts.Hepatic(ctx, facility, patient); err != nil {
		return Picture{}, nil, err
	}
	if picture.Diagnoses, err = facts.Diagnoses(ctx, facility, patient); err != nil {
		return Picture{}, nil, err
	}
	if picture.Current, err = facts.CurrentMedications(ctx, facility, patient); err != nil {
		return Picture{}, nil, err
	}

	reported, err := facts.Allergies(ctx, facility, patient)
	if err != nil {
		return Picture{}, nil, err
	}
	if reported == nil {
		// Status not established. Stays nil all the way into Context.Allergies, which is what
		// makes every allergy rule answer "cannot verify".
		return picture, nil, nil
	}

	groups, _, err := e.rules.Allergens(ctx)
	if err != nil {
		return Picture{}, nil, err
	}
	matched, unmatched := classify(reported, groups)
	// Non-nil even when empty: status was established and there were none, which is the fact
	// that legitimately lets an allergy rule answer "does not apply".
	picture.AllergenGroups = matched
	return picture, unmatched, nil
}

// classify maps reported allergies onto allergen groups.
//
// # What it matches on, and what it refuses to
//
// A group's members name molecules and classes. A reported allergy names a coded substance, a
// catalogue display, or whatever the patient said. Matching is on the **whole word**: the
// member's value must appear in the reported text bounded by something that is not a letter, so
// that "penicillin" matches "Penicillin V" and "allergic to penicillin" and does not match
// nothing at all, while "ampicillin" does not match a member called "penicillin".
//
// That last clause is the one worth being careful about, and it is why this is not a substring
// test: `strings.Contains("ampicillin", "penicillin")` is true, and a substring match would file
// an ampicillin allergy under penicillin — which happens to be clinically reasonable and is
// arrived at by accident, which means the next such coincidence will not be.
//
// # An unmatched allergy is returned, not dropped
//
// "The yellow tablet from the pharmacy near the bridge" matches no group, and the honest answer
// is that this patient has an allergy the engine cannot check anything against. Dropping it would
// produce a check that ran cleanly while ignoring the most dangerous thing on the record.
func classify(reported []ReportedAllergy, groups []AllergenGroup) ([]string, []ReportedAllergy) {
	matched := []string{}
	seen := map[string]bool{}
	var unmatched []ReportedAllergy

	for _, allergy := range reported {
		hit := false
		haystacks := []string{allergy.Code, allergy.Display, allergy.Said}
		for _, group := range groups {
			if !group.IsActive {
				continue
			}
			for _, member := range group.Members {
				if member.Value == "" {
					continue
				}
				for _, haystack := range haystacks {
					if containsWord(haystack, member.Value) {
						hit = true
						if !seen[group.Code] {
							seen[group.Code] = true
							matched = append(matched, group.Code)
						}
					}
				}
			}
		}
		if !hit {
			unmatched = append(unmatched, allergy)
		}
	}
	return matched, unmatched
}

// containsWord reports whether needle appears in haystack bounded by non-letters, ignoring case.
func containsWord(haystack, needle string) bool {
	h := strings.ToLower(strings.TrimSpace(haystack))
	n := strings.ToLower(strings.TrimSpace(needle))
	if h == "" || n == "" {
		return false
	}
	from := 0
	for {
		at := strings.Index(h[from:], n)
		if at < 0 {
			return false
		}
		at += from
		before := at == 0 || !isLetter(rune(h[at-1]))
		endsAt := at + len(n)
		after := endsAt >= len(h) || !isLetter(rune(h[endsAt]))
		if before && after {
			return true
		}
		from = at + 1
	}
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// UnclassifiedAllergyFindings turns allergies no group matched into cannot-verify findings.
//
// One per substance, at BLOCK when the recorded reaction was life-threatening and WARN otherwise.
// The severity split is the one judgement in this function and it is defensible in one sentence:
// a patient whose record says *anaphylaxis to something the engine cannot identify* is a patient
// whose prescription a human has to look at before it is printed, and a warning among warnings is
// not that.
func UnclassifiedAllergyFindings(unmatched []ReportedAllergy) []Finding {
	var out []Finding
	for _, allergy := range unmatched {
		what := firstNonEmpty(allergy.Display, allergy.Said, allergy.Code)
		severity := SeverityWarn
		if allergy.Emergency {
			severity = SeverityBlock
		}
		out = append(out, Finding{
			RuleCode: CodeUnclassifiedAllergy, Type: TypeContraindication, Severity: severity,
			Outcome: OutcomeCannotVerify,
			MessageEN: "This patient reports a reaction to " + what +
				", which matches no allergen group. Nothing on this prescription has been " +
				"checked against it.",
			MessageBN: "রোগী " + what + "-এ প্রতিক্রিয়ার কথা বলেছেন, যা কোনো অ্যালার্জি-শ্রেণির " +
				"সঙ্গে মেলেনি। এই ব্যবস্থাপত্রের কিছুই তার সঙ্গে মিলিয়ে দেখা হয়নি।",
			AdviceEN: "Identify the substance and add it to an allergen group, or check this " +
				"prescription against it by hand.",
			AdviceBN: "পদার্থটি চিহ্নিত করে কোনো অ্যালার্জি-শ্রেণিতে যোগ করুন, নয়তো ব্যবস্থাপত্রটি " +
				"নিজে মিলিয়ে দেখুন।",
			Source:  "CP78 fail-closed behaviour; CP54's uncoded allergies are countable precisely because the safety engine cannot match them.",
			Missing: []Datum{DatumAllergies},
		})
	}
	return out
}

// CodeUnclassifiedAllergy is cited by a finding about an allergy no group matched.
const CodeUnclassifiedAllergy = "ALLERGY-UNCLASSIFIED"

func firstNonEmpty(in ...string) string {
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return "an unnamed substance"
}
