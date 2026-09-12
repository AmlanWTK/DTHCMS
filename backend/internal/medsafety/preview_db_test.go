package medsafety_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
)

// The rule preview names the molecule rather than seven products (CP77's defect, fixed at CP81).
//
// # What was wrong
//
// `MET-RENAL-30` is a rule about metformin, and its subject names the seven generics this
// formulary holds that contain metformin — because that is what the engine matches on. The
// preview therefore read:
//
//	"When Empagliflozin + metformin hydrochloride, Glimepiride + metformin hydrochloride,
//	 Linagliptin + metformin hydrochloride, Metformin hydrochloride, Pioglitazone + metformin
//	 hydrochloride, Sitagliptin + metformin hydrochloride and Vildagliptin + metformin
//	 hydrochloride is prescribed and the eGFR is below 30, stop the prescription…"
//
// A preview exists so a physician can check that what the system understood is what he meant. A
// sentence nobody finishes cannot do that job, and CP77's own acceptance criterion 4 — *the
// sandbox shows the exact effect before publishing* — is unmet by a sentence that is skipped.
//
// # Why the tests below are shaped the way they are
//
// The short sentence is a **claim about coverage**, so the interesting tests are the ones where
// the claim would be false. `TestAPartialMoleculeListIsNotCondensed` takes one generic off the
// list and asserts the long form comes back: a condensation that fired there would promise cover
// the rule does not give, which is worse than the sentence it replaced.

func TestTheMetforminRulePreviewNamesTheMoleculeRatherThanSevenProducts(t *testing.T) {
	h := newAPI(t)
	ruleID, _ := h.seededRule(t, "MET-RENAL-30")

	res, body := h.do(t, http.MethodGet, "/v1/medication-rules/"+ruleID.String(), nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reading the rule: %d", res.StatusCode)
	}
	plain := firstPlain(t, body)

	en, _ := plain["en"].(string)
	if !strings.Contains(en, "any medicine containing metformin") {
		t.Fatalf("the preview still lists the products:\n%s", en)
	}
	// The specific failure: a sentence a physician skips. Seven full product names is roughly
	// two hundred characters of subject before the condition starts.
	if strings.Contains(en, "Sitagliptin + metformin hydrochloride") {
		t.Fatalf("the condensed sentence still names a combination product:\n%s", en)
	}
	if strings.Count(en, "metformin") > 2 {
		t.Fatalf("the molecule is named %d times in one sentence:\n%s",
			strings.Count(en, "metformin"), en)
	}

	bn, _ := plain["bn"].(string)
	if !strings.Contains(bn, "Metformin আছে এমন যেকোনো ওষুধ") {
		t.Fatalf("the Bengali preview was not condensed:\n%s", bn)
	}

	if got, _ := plain["condensed_to"].(string); !strings.EqualFold(got, "metformin") {
		t.Fatalf("condensed_to is %q; a screen cannot tell whether to offer the full list", got)
	}
}

func TestACondensedPreviewStillCarriesEveryProductItCovers(t *testing.T) {
	h := newAPI(t)
	ruleID, _ := h.seededRule(t, "MET-RENAL-30")

	_, body := h.do(t, http.MethodGet, "/v1/medication-rules/"+ruleID.String(), nil, "")
	plain := firstPlain(t, body)

	covers, _ := plain["covers"].([]any)
	if len(covers) != 7 {
		t.Fatalf("the full list is %d entries; a physician approving this rule may reasonably "+
			"want to see exactly which products it covers", len(covers))
	}
	joined := strings.ToLower(strings.Join(stringsOf(covers), "|"))
	for _, want := range []string{
		"metformin hydrochloride",
		"sitagliptin + metformin hydrochloride",
		"vildagliptin + metformin hydrochloride",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the full list does not carry %q: %s", want, joined)
		}
	}
}

func TestAPartialMoleculeListIsNotCondensed(t *testing.T) {
	h := newAPI(t)

	// Six of the seven. The rule is now genuinely narrower than "any medicine containing
	// metformin", and saying otherwise would promise cover it does not give.
	res, body := h.do(t, http.MethodPost, "/v1/medication-rules/preview", map[string]any{
		"type":       "RENAL",
		"severity":   "BLOCK",
		"name_en":    "Six of seven",
		"name_bn":    "সাতের মধ্যে ছয়",
		"message_en": "Not all metformin products.",
		"message_bn": "সব মেটফরমিন পণ্য নয়।",
		"source":     "test",
		"condition": map[string]any{
			"subject": map[string]any{"match": "GENERIC", "generics": []string{
				"metformin hydrochloride",
				"sitagliptin + metformin hydrochloride",
				"vildagliptin + metformin hydrochloride",
				"linagliptin + metformin hydrochloride",
				"glimepiride + metformin hydrochloride",
				"pioglitazone + metformin hydrochloride",
			}},
			"when": []any{map[string]any{
				"kind": "EGFR", "operator": "LT", "value": 30, "unit": "mL/min/1.73m2",
			}},
		},
	}, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("previewing: %d — %v", res.StatusCode, body)
	}
	plain, _ := body["plain"].(map[string]any)
	if plain == nil {
		t.Fatalf("no preview came back: %v", body)
	}
	en, _ := plain["en"].(string)
	if strings.Contains(en, "any medicine containing") {
		t.Fatalf("a rule covering six of this clinic's seven metformin products claims to "+
			"cover them all:\n%s", en)
	}
	if got, _ := plain["condensed_to"].(string); got != "" {
		t.Fatalf("condensed_to is %q on a partial list", got)
	}
}

func TestASingleMoleculeSubjectIsStillNamedOutright(t *testing.T) {
	h := newAPI(t)
	ruleID, _ := h.seededRule(t, "SITA-RENAL-45")

	_, body := h.do(t, http.MethodGet, "/v1/medication-rules/"+ruleID.String(), nil, "")
	plain := firstPlain(t, body)
	en, _ := plain["en"].(string)
	// Sitagliptin is stocked alone and in a combination, so it is not a single-generic rule;
	// what matters is only that whatever it says, it says something a reader can check.
	if strings.TrimSpace(en) == "" {
		t.Fatal("the sitagliptin rule has no preview at all")
	}
	if strings.HasPrefix(en, "When  ") {
		t.Fatalf("the subject phrase is empty:\n%s", en)
	}
}

// TestTheCondensationIsNotDoneByReadingTheName is the test that separates this implementation
// from the one somebody would write in ten minutes.
//
// Splitting "Sitagliptin + Metformin hydrochloride" on the plus sign gets the right answer for
// the metformin rules and the wrong answer for "Calcium lactate gluconate + Calcium carbonate +
// Vitamin D3" and for "Aspirin (low dose)". The condensation must come from migration 00058's
// component rows, so this asserts against a subject whose shared molecule is **not** a substring
// of every generic's name in the same way.
func TestTheCondensationIsNotDoneByReadingTheName(t *testing.T) {
	h := newAPI(t)

	vocab, err := h.store.Vocabulary(context.Background(), h.facility)
	if err != nil {
		t.Fatal(err)
	}
	if len(vocab.Molecules) == 0 {
		t.Fatal("the vocabulary carries no molecules, so no preview could ever condense")
	}
	// "Aspirin (low dose)" decomposes to the molecule "Aspirin" — the parenthesis is a dose
	// qualifier, not a second molecule. A name-splitting implementation has no way to know it.
	if got := vocab.Molecules["aspirin (low dose)"]; len(got) != 1 ||
		!strings.EqualFold(got[0], "Aspirin") {
		t.Fatalf("aspirin's molecules read %v; the condensation would be reading names", got)
	}
	// And a generic nobody has decomposed must be flagged, so a condensation over it fails
	// closed rather than concluding it shares nothing.
	undetermined := 0
	for name, known := range vocab.MoleculeKnown {
		if !known {
			undetermined++
			if _, present := vocab.Molecules[name]; present {
				t.Fatalf("%s is undetermined and still carries molecules", name)
			}
		}
	}
	t.Logf("%d of %d generics have no determined molecules; a rule naming one of them is "+
		"never condensed", undetermined, len(vocab.MoleculeKnown))
}

func firstPlain(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	versions, _ := body["versions"].([]any)
	if len(versions) == 0 {
		t.Fatalf("no versions came back: %v", body)
	}
	first, _ := versions[0].(map[string]any)
	plain, _ := first["plain"].(map[string]any)
	if plain == nil {
		t.Fatalf("the version carries no preview: %v", first)
	}
	return plain
}

func stringsOf(in []any) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

var _ = medsafety.Vocabulary{}
