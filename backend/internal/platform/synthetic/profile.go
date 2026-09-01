package synthetic

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"strings"
)

// Profile is the clinician-authored case-mix the generator samples from.
//
// It mirrors backend/internal/testdata/profile.v1.json field for field. The JSON carries
// "$comment" keys that have no counterpart here and are ignored on decode, which is
// deliberate: the reasoning belongs beside the numbers where a clinician will read it, not
// only in Go where they will not.
type Profile struct {
	Version    int    `json:"version"`
	AuthoredOn string `json:"authoredOn"`
	AuthoredBy string `json:"authoredBy"`
	Provenance string `json:"provenance"`

	Population struct {
		AdultShare       float64 `json:"adultShare"`
		PaediatricShare  float64 `json:"paediatricShare"`
		AdultFemaleShare float64 `json:"adultFemaleShare"`
		AdultAge         Range   `json:"adultAge"`
		PaediatricAge    Range   `json:"paediatricAge"`
		UrbanShare       float64 `json:"urbanShare"`
		NewPatientShare  float64 `json:"newPatientShareOfClinicDay"`
	} `json:"population"`

	PresentingProblem Shares `json:"presentingProblem"`

	Diabetes struct {
		Type2          float64 `json:"type2"`
		Type1          float64 `json:"type1"`
		Gestational    float64 `json:"gestational"`
		OtherSecondary float64 `json:"otherSecondary"`

		AgeAtDiagnosisType2 Range `json:"ageAtDiagnosisType2"`
		AgeAtDiagnosisType1 Range `json:"ageAtDiagnosisType1"`

		DurationAtFirstVisit struct {
			DiagnosedHere    float64 `json:"diagnosedHere"`
			WithinOneYear    float64 `json:"withinOneYear"`
			Established      float64 `json:"established"`
			EstablishedYears Range   `json:"establishedYears"`
		} `json:"durationAtFirstVisit"`

		HbA1cAtPresentation Range   `json:"hba1cAtPresentation"`
		AtTargetFirstVisit  float64 `json:"atTargetFirstVisit"`

		AtTargetAfterOneYear struct {
			Value       float64 `json:"value"`
			Denominator string  `json:"denominator"`
		} `json:"atTargetAfterOneYear"`

		HbA1cFallFirstSixMonths Range `json:"hba1cFallFirstSixMonths"`

		ComplicationAtFirstVisit struct {
			Value float64 `json:"value"`
		} `json:"complicationAtFirstVisit"`
	} `json:"diabetes"`

	Thyroid struct {
		OvertPrimaryHypothyroid float64 `json:"overtPrimaryHypothyroid"`
		SubclinicalHypothyroid  float64 `json:"subclinicalHypothyroid"`
		HyperthyroidGraves      float64 `json:"hyperthyroidGraves"`
		EuthyroidGoitre         float64 `json:"euthyroidGoitre"`
		NoduleSurveillance      float64 `json:"noduleSurveillance"`
		PostSurgicalOrRAI       float64 `json:"postSurgicalOrRAI"`
		CancerFollowUp          float64 `json:"cancerFollowUp"`
		FemaleShare             float64 `json:"femaleShare"`
		TSHHypothyroid          Range   `json:"tshHypothyroid"`
		TSHHyperthyroid         Range   `json:"tshHyperthyroid"`
		LevothyroxineStart      struct {
			Low           int `json:"low"`
			High          int `json:"high"`
			CautiousStart int `json:"cautiousStart"`
		} `json:"levothyroxineStart"`
	} `json:"thyroid"`

	OtherConditions struct {
		Obesity struct {
			Share float64 `json:"share"`
			// AsMainProblem is the slice of Share that walks in *for* obesity. The remainder
			// is obesity found alongside something else, and must be sampled conditionally.
			AsMainProblem float64 `json:"asMainProblem"`
			BMI           struct {
				Low     float64 `json:"low"`
				High    float64 `json:"high"`
				Cluster Range   `json:"cluster"`
			} `json:"bmi"`
		} `json:"obesity"`
	} `json:"otherConditions"`

	Comorbidity Denominators `json:"comorbidity"`

	Prescribing struct {
		InsulinShare      float64 `json:"insulinShare"`
		SGLT2Share        float64 `json:"sglt2Share"`
		GLP1Share         float64 `json:"glp1Share"`
		DPP4Share         float64 `json:"dpp4Share"`
		SulfonylureaShare float64 `json:"sulfonylureaShare"`
	} `json:"prescribing"`

	FollowUp struct {
		StableInterval struct {
			ThreeMonthly float64 `json:"threeMonthly"`
			SixMonthly   float64 `json:"sixMonthly"`
			Annual       float64 `json:"annual"`
		} `json:"stableInterval"`
		LostToFollowUpWithinYear float64 `json:"lostToFollowUpWithinYear"`
		VisitsInFirstYear        int     `json:"visitsInFirstYear"`
	} `json:"followUp"`

	NamesAndLanguage struct {
		MuslimShare        float64 `json:"muslimShare"`
		HinduShare         float64 `json:"hinduShare"`
		OtherShare         float64 `json:"otherShare"`
		BanglaForNarrative float64 `json:"banglaForNarrative"`
	} `json:"namesAndLanguage"`
}

/*
 * Shares and Denominators exist so the profile can carry the clinician's reasoning.
 *
 * The JSON is meant to be read by a doctor, not only by Go, and the reasoning belongs beside
 * the numbers — "Share of caseload by the MAIN problem attended for. Sums to 1.00." is worth
 * more where the shares are than in a package comment nobody clinical will open.
 *
 * Go's decoder does not agree: a "$comment" string inside a map[string]float64 is a type
 * error, and the message it gives ("cannot unmarshal string into Go struct field
 * Profile.presentingProblem of type float64") names neither the key nor the file. That
 * happened, cost a session, and was worked around by moving the comments out to sibling keys
 * — which is the wrong fix, because it makes the file worse to serve the parser.
 *
 * So the parser gives way instead. Any key beginning with "$" is annotation and is skipped
 * wherever these types are used; everything else must be a number, and says which key it was
 * if it is not.
 */

// Shares is a set of named proportions.
type Shares map[string]float64

// UnmarshalJSON skips annotation keys and names the offender on a real type error.
func (s *Shares) UnmarshalJSON(data []byte) error {
	raw, err := annotatedObject(data)
	if err != nil {
		return err
	}

	out := make(Shares, len(raw))
	for key, value := range raw {
		var share float64
		if err := json.Unmarshal(value, &share); err != nil {
			return fmt.Errorf("%q is not a number: %w", key, err)
		}
		out[key] = share
	}
	*s = out
	return nil
}

// Denominator is a figure together with the population it was given against.
//
// The denominator is not decoration. The clinician answered each comorbidity row against a
// different population — "of adults with type 2 diabetes", "of the diabetes caseload", "of
// the total clinic caseload" — and flattening them into one produces a cohort that is wrong
// in a way no reviewer catches by eye.
type Denominator struct {
	Value       float64 `json:"value"`
	Denominator string  `json:"denominator"`
}

// Denominators is a set of those, tolerating annotation keys in the same way as Shares.
type Denominators map[string]Denominator

func (d *Denominators) UnmarshalJSON(data []byte) error {
	raw, err := annotatedObject(data)
	if err != nil {
		return err
	}

	out := make(Denominators, len(raw))
	for key, value := range raw {
		var entry Denominator
		if err := json.Unmarshal(value, &entry); err != nil {
			return fmt.Errorf("%q: %w", key, err)
		}
		out[key] = entry
	}
	*d = out
	return nil
}

// annotatedObject decodes a JSON object and drops every "$"-prefixed annotation key.
func annotatedObject(data []byte) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	for key := range raw {
		if strings.HasPrefix(key, "$") {
			delete(raw, key)
		}
	}
	return raw, nil
}

// Range is "median about X, usually between low and high", as a clinician gives it.
type Range struct {
	Median float64 `json:"median"`
	Low    float64 `json:"low"`
	High   float64 `json:"high"`
}

// LoadProfile reads a profile and refuses one that does not add up.
func LoadProfile(path string) (*Profile, error) {
	file, err := os.Open(path) //nolint:gosec // a repository-local test fixture
	if err != nil {
		return nil, fmt.Errorf("opening profile: %w", err)
	}
	defer func() { _ = file.Close() }()

	return ParseProfile(file)
}

// ParseProfile decodes and validates a profile.
func ParseProfile(r io.Reader) (*Profile, error) {
	var p Profile
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return nil, fmt.Errorf("decoding profile: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate checks the arithmetic.
//
// A profile whose shares do not sum to one produces a population silently skewed toward
// whichever category the sampler reaches first — a bug that looks like a clinical opinion
// and is very hard to see in the output. Cheaper to refuse the file.
func (p *Profile) Validate() error {
	if p.Version == 0 {
		return fmt.Errorf("profile has no version — a dataset must be reproducible from a named profile")
	}

	sums := []struct {
		name  string
		total float64
	}{
		{"presentingProblem", sumMap(p.PresentingProblem)},
		{"diabetes types", p.Diabetes.Type2 + p.Diabetes.Type1 + p.Diabetes.Gestational + p.Diabetes.OtherSecondary},
		{"diabetes duration", p.Diabetes.DurationAtFirstVisit.DiagnosedHere +
			p.Diabetes.DurationAtFirstVisit.WithinOneYear + p.Diabetes.DurationAtFirstVisit.Established},
		{"thyroid categories", p.Thyroid.OvertPrimaryHypothyroid + p.Thyroid.SubclinicalHypothyroid +
			p.Thyroid.HyperthyroidGraves + p.Thyroid.EuthyroidGoitre + p.Thyroid.NoduleSurveillance +
			p.Thyroid.PostSurgicalOrRAI + p.Thyroid.CancerFollowUp},
		{"adult/paediatric", p.Population.AdultShare + p.Population.PaediatricShare},
		{"name traditions", p.NamesAndLanguage.MuslimShare + p.NamesAndLanguage.HinduShare + p.NamesAndLanguage.OtherShare},
		{"follow-up intervals", p.FollowUp.StableInterval.ThreeMonthly +
			p.FollowUp.StableInterval.SixMonthly + p.FollowUp.StableInterval.Annual},
	}

	for _, s := range sums {
		if math.Abs(s.total-1.0) > 0.001 {
			return fmt.Errorf("profile v%d: %s sums to %.4f, not 1.0", p.Version, s.name, s.total)
		}
	}

	// The case mix (mix.go) is this package's own interpolation of who presents with what.
	// Checking it here means a profile can never be loaded against a mix that contradicts it:
	// the failure surfaces at load, naming both numbers, rather than as a skewed population
	// nobody notices.
	if err := reconcile(p, 0.01); err != nil {
		return fmt.Errorf("profile v%d: %w", p.Version, err)
	}
	return nil
}

func sumMap(m map[string]float64) float64 {
	total := 0.0
	for _, v := range m {
		total += v
	}
	return total
}

// --- sampling primitives ---

// triangular draws from the shape a clinician's "median about X, usually A to B" describes.
//
// Chosen over a normal distribution because it has no tails beyond the stated range, and a
// clinician saying "usually 7.5 to 14" does not mean "and occasionally 22". Where a genuine
// tail was described — TSH above 100 — it is generated explicitly rather than by widening
// this.
func triangular(rng *rand.Rand, r Range) float64 {
	low, mode, high := r.Low, r.Median, r.High
	if high <= low {
		return low
	}
	if mode < low || mode > high {
		mode = (low + high) / 2
	}

	u := rng.Float64()
	split := (mode - low) / (high - low)
	if u < split {
		return low + math.Sqrt(u*(high-low)*(mode-low))
	}
	return high - math.Sqrt((1-u)*(high-low)*(high-mode))
}

// pick returns true with probability p.
func pick(rng *rand.Rand, p float64) bool { return rng.Float64() < p }

// choose selects a key from weighted shares, deterministically ordered so that the same
// seed gives the same population on every machine.
func choose(rng *rand.Rand, weights map[string]float64, order []string) string {
	u := rng.Float64()
	cumulative := 0.0
	for _, key := range order {
		cumulative += weights[key]
		if u < cumulative {
			return key
		}
	}
	if len(order) == 0 {
		return ""
	}
	return order[len(order)-1]
}
