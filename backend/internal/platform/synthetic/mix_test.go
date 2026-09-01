package synthetic

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProfileToleratesAnnotationKeys.
//
// The profile is meant to be read by a doctor, so the reasoning sits beside the numbers it
// explains. Go's decoder refused that — a "$comment" string inside a map of shares is a type
// error — and the message named neither the key nor the file:
//
//	json: cannot unmarshal string into Go struct field Profile.presentingProblem of type float64
//
// It was first worked around by moving the comments out to sibling keys, which is the wrong
// way round: it degrades the file to suit the parser. The parser gives way instead, and this
// is the test that keeps it giving way.
func TestProfileToleratesAnnotationKeys(t *testing.T) {
	const annotated = `{
	  "version": 1,
	  "presentingProblem": {
	    "$comment": "Share of caseload by the MAIN problem attended for. Sums to 1.00.",
	    "diabetes": 0.6, "thyroid": 0.4
	  },
	  "comorbidity": {
	    "$comment": "Denominators differ per row and are stated.",
	    "hypertension": { "value": 0.5, "denominator": "adults with type 2 diabetes" }
	  }
	}`

	var p Profile
	if err := jsonUnmarshalProfile(annotated, &p); err != nil {
		t.Fatalf("a profile with $comment keys beside its numbers was rejected: %v", err)
	}

	if len(p.PresentingProblem) != 2 {
		t.Errorf("expected 2 presenting shares, got %d: %v", len(p.PresentingProblem), p.PresentingProblem)
	}
	if _, leaked := p.PresentingProblem["$comment"]; leaked {
		t.Error("the annotation key was decoded as a share")
	}
	if got := p.Comorbidity["hypertension"].Denominator; got != "adults with type 2 diabetes" {
		t.Errorf("denominator lost: %q", got)
	}

	// A real type error must still fail, and must say which key.
	const broken = `{"version":1,"presentingProblem":{"diabetes":"most of them"}}`
	err := jsonUnmarshalProfile(broken, &p)
	if err == nil {
		t.Fatal("a non-numeric share was accepted")
	}
	if !strings.Contains(err.Error(), "diabetes") {
		t.Errorf("the error does not name the offending key: %v", err)
	}
}

// TestProfileRefusesArithmeticThatDoesNotAddUp.
//
// A profile whose shares do not sum to one produces a population silently skewed toward
// whichever category the sampler reaches first — a bug that looks like a clinical opinion.
// Refusing the file is much cheaper than finding that later.
func TestProfileRefusesArithmeticThatDoesNotAddUp(t *testing.T) {
	p := loadProfile(t)
	p.PresentingProblem["diabetes"] += 0.10

	err := p.Validate()
	if err == nil {
		t.Fatal("a profile whose presenting shares sum to 1.10 was accepted")
	}
	if !strings.Contains(err.Error(), "presentingProblem") {
		t.Errorf("the error does not name the offending section: %v", err)
	}
}

// TestReconcileCatchesACaseMixThatContradictsTheProfile.
//
// mix.go holds this package's own interpolation of who presents with what — numbers the
// clinician did not give. The guarantee that makes that safe is that the interpolation cannot
// silently disagree with the numbers he did give. This is that guarantee.
func TestReconcileCatchesACaseMixThatContradictsTheProfile(t *testing.T) {
	p := loadProfile(t)

	original := caseMix["obesityMetabolic"]
	t.Cleanup(func() { caseMix["obesityMetabolic"] = original })

	broken := original
	broken.paediatric = 0.05 // obesity is where most of the paediatric caseload lives
	caseMix["obesityMetabolic"] = broken

	err := reconcile(p, 0.01)
	if err == nil {
		t.Fatal("a case mix implying far fewer children than the profile states was accepted")
	}
	if !strings.Contains(err.Error(), "paediatric") {
		t.Errorf("the error does not say what disagrees: %v", err)
	}
}

// TestReconcileCatchesCrossOverRatesThatMissTheStatedOverlap.
func TestReconcileCatchesCrossOverRatesThatMissTheStatedOverlap(t *testing.T) {
	p := loadProfile(t)

	stated := p.Comorbidity["diabetesAndThyroid"]
	stated.Value = 0.40 // twice what the rates in mix.go produce
	p.Comorbidity["diabetesAndThyroid"] = stated

	err := reconcile(p, 0.01)
	if err == nil {
		t.Fatal("cross-over rates producing half the stated overlap were accepted")
	}
	if !strings.Contains(err.Error(), "thyroid") {
		t.Errorf("the error does not name the disagreement: %v", err)
	}
}

// TestAdultType1RateIsSolvedNotAssumed.
//
// The derived rate must be below the profile's stated share, because the children — who are
// type 1 by clinical necessity — already account for part of it. Equality would mean the
// derivation is not running, which is the shape of the bug it was written to fix.
func TestAdultType1RateIsSolvedNotAssumed(t *testing.T) {
	p := loadProfile(t)
	m := computeMasses(p)

	rate, err := adultType1Rate(p, m)
	if err != nil {
		t.Fatalf("solving for the adult type 1 rate: %v", err)
	}
	if rate <= 0 {
		t.Fatalf("adult type 1 rate came out at %.4f — no adult would ever have type 1", rate)
	}
	if rate >= p.Diabetes.Type1 {
		t.Errorf("adult type 1 rate %.4f is not below the caseload share %.4f — the paediatric "+
			"mass is not being subtracted", rate, p.Diabetes.Type1)
	}

	// And the arithmetic must close: children plus adults must land on the stated share.
	total := paediatricType1Share*m.childDiabetes + rate*m.adultDiabetes
	near(t, "implied type 1 share of the diabetes caseload", total/m.diabetes, p.Diabetes.Type1, 0.001)
}

// TestConditionalShare covers the arithmetic behind "half the caseload is obese, a third
// present for it" — where sampling the remainder at one half again lands the total at 67%.
func TestConditionalShare(t *testing.T) {
	cases := []struct{ total, covered, want float64 }{
		{0.50, 0.34, 0.242424},
		{0.30, 0.02, 0.285714},
		{0.20, 0.00, 0.200000},
		{0.20, 0.50, 0.000000}, // already over-covered: never add more
		{0.20, 1.00, 0.000000},
	}
	for _, c := range cases {
		got := conditionalShare(c.total, c.covered)
		near(t, "conditionalShare", got, c.want, 0.0001)
	}
}

func jsonUnmarshalProfile(text string, p *Profile) error {
	return json.Unmarshal([]byte(text), p)
}
