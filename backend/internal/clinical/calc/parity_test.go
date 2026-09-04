package calc_test

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical/calc"
)

// The parity harness (CP43, criteria 1 and 2).
//
// Every case in `packages/clinical-calc/fixtures/reference.json` is run here and, by the
// identical harness in `packages/clinical-calc/test/parity.test.ts`, in TypeScript. The two
// suites read the same file, so "Go and TS agree on 100% of fixtures" is not a claim
// somebody has to check by hand — it is what green means.
//
// The fixture values were computed independently from the published equations. A fixture
// read off either implementation would prove only that the implementation agrees with
// itself, which is exactly the failure mode this exists to catch.

const fixturePath = "../../../../packages/clinical-calc/fixtures/reference.json"

type fixtures struct {
	Tolerance float64                      `json:"tolerance"`
	Cases     map[string][]json.RawMessage `json:"cases"`
	Refusals  []refusal                    `json:"refusals"`
}

type numericCase struct {
	Name     string         `json:"name"`
	Inputs   map[string]any `json:"inputs"`
	Expected float64        `json:"expected"`
}

type classCase struct {
	Name     string            `json:"name"`
	Inputs   map[string]any    `json:"inputs"`
	Expected map[string]string `json:"expected"`
}

type refusal struct {
	Formula string         `json:"formula"`
	Inputs  map[string]any `json:"inputs"`
	Reason  string         `json:"reason"`
}

func load(t *testing.T) fixtures {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(fixturePath))
	if err != nil {
		t.Fatalf("the shared fixtures are the contract between the two implementations "+
			"and could not be read: %v", err)
	}
	var out fixtures
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Tolerance <= 0 {
		t.Fatal("the fixture file declares no tolerance")
	}
	return out
}

func num(t *testing.T, in map[string]any, key string) float64 {
	t.Helper()
	v, ok := in[key]
	if !ok {
		t.Fatalf("the fixture has no %q", key)
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("%q is %T, not a number", key, v)
	}
	return f
}

func sex(t *testing.T, in map[string]any) calc.Sex {
	t.Helper()
	v, _ := in["sex"].(string)
	return calc.Sex(v)
}

// run dispatches one formula by name. The switch is the only place a fixture name meets a Go
// function, and its TypeScript twin is the only place the same name meets a TS function —
// which is what makes a formula added to one language and not the other a failing test
// rather than a silent gap.
func run(t *testing.T, formula string, in map[string]any) (calc.Result, error) {
	t.Helper()
	switch formula {
	case "bmi":
		return calc.BMI(num(t, in, "weight_kg"), num(t, in, "height_cm"))
	case "whr":
		return calc.WHR(num(t, in, "waist_cm"), num(t, in, "hip_cm"))
	case "bmr_mifflin_st_jeor":
		return calc.BMRMifflin(num(t, in, "weight_kg"), num(t, in, "height_cm"),
			num(t, in, "age_years"), sex(t, in))
	case "bmr_harris_benedict_revised":
		return calc.BMRHarrisBenedict(num(t, in, "weight_kg"), num(t, in, "height_cm"),
			num(t, in, "age_years"), sex(t, in))
	case "ibw_devine":
		return calc.IdealBodyWeight(num(t, in, "height_cm"), sex(t, in))
	case "bsa_du_bois":
		return calc.BSADuBois(num(t, in, "weight_kg"), num(t, in, "height_cm"))
	case "bsa_mosteller":
		return calc.BSAMosteller(num(t, in, "weight_kg"), num(t, in, "height_cm"))
	case "egfr_ckd_epi_2021":
		return calc.EGFRCKDEPI2021(num(t, in, "creatinine_mg_dl"), num(t, in, "age_years"), sex(t, in))
	case "egfr_bedside_schwartz":
		return calc.EGFRBedsideSchwartz(num(t, in, "creatinine_mg_dl"),
			num(t, in, "height_cm"), num(t, in, "age_years"))
	case "pack_years":
		return calc.PackYears(num(t, in, "cigarettes_per_day"), num(t, in, "years"))
	default:
		t.Fatalf("the fixture names a formula this library does not implement: %s", formula)
		return calc.Result{}, nil
	}
}

func TestEveryReferenceVectorMatches(t *testing.T) {
	// Criterion 1: every formula matches published reference values exactly, within the
	// declared tolerance.
	f := load(t)

	checked := 0
	for formula, cases := range f.Cases {
		if formula == "obesity_class" {
			continue // its expectation is two strings, not a number; below.
		}
		for _, raw := range cases {
			var c numericCase
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatal(err)
			}
			t.Run(formula+"/"+c.Name, func(t *testing.T) {
				got, err := run(t, formula, c.Inputs)
				if err != nil {
					t.Fatalf("refused a valid case: %v", err)
				}
				if math.Abs(got.Value-c.Expected) > f.Tolerance {
					t.Errorf("got %.12g, the published equation gives %.12g (tolerance %g)",
						got.Value, c.Expected, f.Tolerance)
				}
				if got.Formula != formula {
					t.Errorf("the result names formula %q", got.Formula)
				}
				if got.Version == "" {
					t.Error("the result carries no version; a stored value would be unidentifiable")
				}
			})
			checked++
		}
	}
	if checked < 20 {
		t.Fatalf("only %d numeric vectors ran; the fixture file may not have loaded", checked)
	}
}

func TestTheObesityScalesDisagreeWhereTheyShould(t *testing.T) {
	// The classification is the one place in this library where getting it wrong changes a
	// patient's pathway rather than a number on a screen: a BMI of 24 is "normal"
	// internationally and "overweight" in a Bangladeshi patient, and the whole screening
	// pathway hangs on which side of that line they fall.
	f := load(t)

	for _, raw := range f.Cases["obesity_class"] {
		var c classCase
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		t.Run(c.Name, func(t *testing.T) {
			bmi := num(t, c.Inputs, "bmi")
			for scale, want := range c.Expected {
				got, version, err := calc.Classify(bmi, scale == "asian")
				if err != nil {
					t.Fatalf("%s: %v", scale, err)
				}
				if string(got) != want {
					t.Errorf("%s scale: BMI %.1f is %q, wanted %q", scale, bmi, got, want)
				}
				if version == "" {
					t.Error("the classification carries no version")
				}
			}
		})
	}
}

func TestAnInvalidInputSaysSoRatherThanReturningAWrongNumber(t *testing.T) {
	// Criterion 4. A BMI computed from a height of zero is +Inf, which serialises to `null`
	// and renders as an empty cell — a wrong answer that looks like a missing one.
	f := load(t)

	reasons := map[string]error{
		"not_positive":    calc.ErrNotPositive,
		"out_of_range":    calc.ErrOutOfRange,
		"sex_unsupported": calc.ErrSexUnsupported,
		"missing_input":   calc.ErrMissingInput,
	}

	for _, r := range f.Refusals {
		t.Run(r.Formula+"/"+r.Reason, func(t *testing.T) {
			want, ok := reasons[r.Reason]
			if !ok {
				t.Fatalf("the fixture names a refusal reason this library has no error for: %s", r.Reason)
			}
			got, err := run(t, r.Formula, r.Inputs)
			if err == nil {
				t.Fatalf("%v returned %v instead of refusing", r.Inputs, got.Value)
			}
			if !errors.Is(err, want) {
				t.Errorf("refused with %v, wanted %v", err, want)
			}
		})
	}
}

func TestEveryFormulaIsVersioned(t *testing.T) {
	// Criterion 3's precondition. A derived value stored without its formula version cannot
	// be identified later, and CKD-EPI has already changed once — the 2021 revision removed
	// a race coefficient, and a value computed under the old equation must stay
	// identifiable as such.
	formulas := calc.Formulas()
	if len(formulas) < 10 {
		t.Fatalf("the library reports only %d formulas", len(formulas))
	}
	for name, version := range formulas {
		if version == "" {
			t.Errorf("%s has no version", name)
		}
	}

	// And every formula the fixtures exercise is in that map, so a formula can neither be
	// implemented without a version nor versioned without being reachable.
	f := load(t)
	for formula := range f.Cases {
		if _, ok := formulas[formula]; !ok {
			t.Errorf("the fixtures exercise %q, which Formulas() does not list", formula)
		}
	}
}

func TestRoundingIsTheSameOnBothSides(t *testing.T) {
	// "Round to one decimal" is ambiguous at a half, and the two runtimes resolve it
	// differently. Every derived value here is positive, so today they agree; this is what
	// keeps them agreeing when somebody adds a formula that is not.
	for _, c := range []struct {
		value    float64
		decimals int
		want     float64
	}{
		{24.691358, 1, 24.7},
		{24.65, 1, 24.7},
		{24.64999, 1, 24.6},
		{77.88343795839066, 0, 78},
		{1.996421022275045, 2, 2.0},
		{0.5, 0, 1},
	} {
		if got := calc.Round(c.value, c.decimals); got != c.want {
			t.Errorf("Round(%v, %d) = %v, wanted %v", c.value, c.decimals, got, c.want)
		}
	}
}
