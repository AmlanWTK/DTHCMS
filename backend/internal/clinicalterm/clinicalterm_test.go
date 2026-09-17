package clinicalterm_test

import (
	"strings"
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinicalterm"
)

func lexicon() clinicalterm.Lexicon {
	return clinicalterm.New(
		clinicalterm.Term{Code: "HBA1C", EN: "HbA1c", BN: "এইচবিএ১সি"},
		clinicalterm.Term{Code: "CHOL_LDL", EN: "LDL cholesterol", BN: "এলডিএল"},
		clinicalterm.Term{Code: "EGFR", EN: "eGFR (CKD-EPI 2021)", BN: "ইজিএফআর"},
	)
}

// The whole point of the package, asserted once: a code goes in and a name comes out.
func TestACodeIsRenderedAsItsName(t *testing.T) {
	lex := lexicon()
	if got := lex.Observation("CHOL_LDL").EN; got != "LDL cholesterol" {
		t.Fatalf("EN: %q", got)
	}
	if got := lex.Observation("CHOL_LDL").BN; got != "এলডিএল" {
		t.Fatalf("BN: %q", got)
	}
	// The code is not thrown away: somebody debugging a rule needs it, and a client offers it
	// as a detail rather than as the sentence.
	if got := lex.Observation("CHOL_LDL").Code; got != "CHOL_LDL" {
		t.Fatalf("the code was dropped: %q", got)
	}
}

// A code the catalogue does not define must still not reach a screen as a database column. It
// reads badly, which is correct — it is a row that needs fixing, and invariant 138 reports it.
func TestAnUnknownCodeIsSpelledRatherThanPrinted(t *testing.T) {
	got := lexicon().Observation("FOOT_RISK_LEFT").EN
	if strings.Contains(got, "_") || got != "Foot risk left" {
		t.Fatalf("an unknown code rendered as %q; it must not read like a column name", got)
	}
}

// The phrasing defect, in both directions: a singular window must not be counted, and a small
// number is written rather than digitised.
func TestAWindowIsPhrasedTheWayItIsSaid(t *testing.T) {
	cases := map[int]string{
		365: "the last year",
		730: "the last two years",
		180: "the last six months",
		30:  "the last month",
		7:   "the last seven days",
		1:   "the last day",
	}
	for days, want := range cases {
		if got := clinicalterm.WindowEN(days); got != want {
			t.Errorf("%d days: %q, want %q", days, got, want)
		}
	}
	// Bangla takes Bengali digits and drops the numeral on one, for the same reason.
	if got := clinicalterm.WindowBN(365); got != "গত বছরে" {
		t.Errorf("BN one year: %q", got)
	}
	if got := clinicalterm.WindowBN(180); got != "গত ৬ মাসে" {
		t.Errorf("BN six months: %q", got)
	}
	if strings.ContainsAny(clinicalterm.WindowBN(180), "0123456789") {
		t.Error("a Bangla clinical sentence carrying Latin digits reads half-translated")
	}
}

// CP82's rationale line. `obs.hba1c:2026-09-01` is a storage format on a screen a physician
// scans in five seconds.
func TestAFactReferenceReadsAsAClinicianWouldSayIt(t *testing.T) {
	lex := lexicon()
	got := clinicalterm.Refer(lex, "obs.hba1c:2026-09-01")
	if got.EN != "HbA1c, 1 Sep 2026" {
		t.Fatalf("EN: %q", got.EN)
	}
	if got.BN != "এইচবিএ১সি, ১ সেপ্ট ২০২৬" {
		t.Fatalf("BN: %q", got.BN)
	}
	// The raw reference survives. It is what grounding validated and what an engineer greps for.
	if got.Raw != "obs.hba1c:2026-09-01" {
		t.Fatalf("the reference was lost: %q", got.Raw)
	}

	// A repeat within one context carries a letter suffix, which is not part of the test's name.
	if second := clinicalterm.Refer(lex, "obs.hba1c:2026-09-01.b"); second.EN != "HbA1c, 1 Sep 2026" {
		t.Fatalf("a duplicate reference invented a test called %q", second.EN)
	}

	// A reference this package cannot resolve still must not render as a token.
	other := clinicalterm.Refer(lex, "dx.type_2_diabetes_mellitus")
	if strings.Contains(other.EN, "_") || strings.Contains(other.EN, "dx.") {
		t.Fatalf("an unresolvable reference reached the screen raw: %q", other.EN)
	}
}

func TestAListReadsAsAList(t *testing.T) {
	if got := clinicalterm.ListEN([]string{"HbA1c", "LDL cholesterol", "TSH"}); got !=
		"HbA1c, LDL cholesterol or TSH" {
		t.Fatalf("%q", got)
	}
	if got := clinicalterm.ListEN([]string{"HbA1c"}); got != "HbA1c" {
		t.Fatalf("a list of one acquired a separator: %q", got)
	}
	if got := clinicalterm.ListEN(nil); got != "" {
		t.Fatalf("an empty list rendered %q", got)
	}
}

func TestCountingIsProseAndNotAField(t *testing.T) {
	if got := clinicalterm.PluralEN(1, "check", "checks"); got != "one check" {
		t.Fatalf("%q", got)
	}
	if got := clinicalterm.PluralEN(3, "warning", "warnings"); got != "three warnings" {
		t.Fatalf("%q", got)
	}
	// Above ten the digit is easier to take in at a glance than the word.
	if got := clinicalterm.PluralEN(17, "finding", "findings"); got != "17 findings" {
		t.Fatalf("%q", got)
	}
	if got := clinicalterm.Sentence("one check passed"); got != "One check passed" {
		t.Fatalf("%q", got)
	}
	// Bengali has no case, so Sentence is a no-op rather than a corruption.
	if got := clinicalterm.Sentence("গত বছরে"); got != "গত বছরে" {
		t.Fatalf("%q", got)
	}
}

// The zero value is a working lexicon. A process whose catalogue query failed renders spelled
// codes; it does not panic and it does not render nothing.
func TestTheZeroLexiconStillAnswers(t *testing.T) {
	var lex clinicalterm.Lexicon
	if got := lex.Observation("HBA1C").EN; got != "Hba1c" {
		t.Fatalf("%q", got)
	}
	if lex.Known("HBA1C") {
		t.Fatal("the empty lexicon claimed to know a code")
	}
}
