package calc_test

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical/calc"
)

// The panel's half of the parity harness (CP45 criterion 2, ADR-0025).
//
// `packages/clinical-calc/test/panel.test.ts` reads the same file and asserts the same
// things. Neither implementation generated it: every expected number was computed from the
// published equations directly, so a shared mistake in the two libraries fails here rather
// than agreeing with itself.

type panelFixture struct {
	Tolerance float64 `json:"tolerance"`
	Cases     []struct {
		Name  string `json:"name"`
		Input struct {
			WeightKg *float64 `json:"weight_kg"`
			HeightCm *float64 `json:"height_cm"`
			WaistCm  *float64 `json:"waist_cm"`
			HipCm    *float64 `json:"hip_cm"`
			AgeYears float64  `json:"age_years"`
			Sex      string   `json:"sex"`
			Asian    bool     `json:"asian"`
		} `json:"input"`
		Expect struct {
			BMI                 *calc.Result        `json:"bmi"`
			ObesityClass        string              `json:"obesity_class"`
			ObesityClassVersion string              `json:"obesity_class_version"`
			BMR                 *calc.Result        `json:"bmr"`
			IBW                 *calc.Result        `json:"ideal_body_weight"`
			WHR                 *calc.Result        `json:"whr"`
			Needs               map[string][]string `json:"needs"`
			Refused             map[string]string   `json:"refused"`
		} `json:"expect"`
	} `json:"cases"`
}

func TestThePanelMatchesTheReferenceFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../../packages/clinical-calc/fixtures/panel.json")
	if err != nil {
		t.Fatalf("reading the panel fixture: %v", err)
	}
	var fixture panelFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parsing the panel fixture: %v", err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("the fixture is empty, which would make this test pass by doing nothing")
	}

	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := calc.AnthroPanel(calc.PanelInput{
				WeightKg: c.Input.WeightKg, HeightCm: c.Input.HeightCm,
				WaistCm: c.Input.WaistCm, HipCm: c.Input.HipCm,
				AgeYears: c.Input.AgeYears, Sex: calc.Sex(c.Input.Sex), Asian: c.Input.Asian,
			})

			same := func(name string, want, have *calc.Result) {
				t.Helper()
				switch {
				case want == nil && have == nil:
					return
				case want == nil:
					t.Fatalf("%s: computed %v, and the fixture says it should not exist", name, have.Value)
				case have == nil:
					t.Fatalf("%s: nothing computed, and the fixture expects %v", name, want.Value)
				}
				if math.Abs(want.Value-have.Value) > fixture.Tolerance {
					t.Errorf("%s = %.12f, want %.12f", name, have.Value, want.Value)
				}
				if want.Unit != have.Unit {
					t.Errorf("%s unit = %q, want %q", name, have.Unit, want.Unit)
				}
				if want.Formula != have.Formula || want.Version != have.Version {
					t.Errorf("%s formula = %s@%s, want %s@%s",
						name, have.Formula, have.Version, want.Formula, want.Version)
				}
			}

			same("bmi", c.Expect.BMI, got.BMI)
			same("bmr", c.Expect.BMR, got.BMR)
			same("ideal_body_weight", c.Expect.IBW, got.IBW)
			same("whr", c.Expect.WHR, got.WHR)

			if string(got.Class) != c.Expect.ObesityClass {
				t.Errorf("obesity class = %q, want %q", got.Class, c.Expect.ObesityClass)
			}
			if got.ClassVersion != c.Expect.ObesityClassVersion {
				t.Errorf("class version = %q, want %q", got.ClassVersion, c.Expect.ObesityClassVersion)
			}

			if len(got.Needs) != len(c.Expect.Needs) {
				t.Errorf("needs = %v, want %v", got.Needs, c.Expect.Needs)
			}
			for key, want := range c.Expect.Needs {
				have, ok := got.Needs[key]
				if !ok {
					t.Errorf("needs[%s] missing; want %v", key, want)
					continue
				}
				if len(have) != len(want) {
					t.Errorf("needs[%s] = %v, want %v", key, have, want)
					continue
				}
				for i := range want {
					if have[i] != want[i] {
						t.Errorf("needs[%s] = %v, want %v", key, have, want)
						break
					}
				}
			}

			if len(got.Refused) != len(c.Expect.Refused) {
				t.Errorf("refused = %v, want %v", got.Refused, c.Expect.Refused)
			}
			for key, want := range c.Expect.Refused {
				if got.Refused[key] != want {
					t.Errorf("refused[%s] = %q, want %q", key, got.Refused[key], want)
				}
			}
		})
	}
}

// A panel is only useful on a phone if it is instant. Criterion (1) says the derived values
// appear within 200ms of the last keystroke; this asserts the computation itself is far
// enough below that budget that the whole of it belongs to rendering.
func TestThePanelIsFastEnoughToRunOnEveryKeystroke(t *testing.T) {
	weight, height, waist, hip := 72.5, 170.0, 92.0, 98.0
	in := calc.PanelInput{
		WeightKg: &weight, HeightCm: &height, WaistCm: &waist, HipCm: &hip,
		AgeYears: 45, Sex: calc.Male, Asian: true,
	}
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = calc.AnthroPanel(in)
		}
	})
	if perOp := result.NsPerOp(); perOp > 100_000 {
		t.Errorf("a panel takes %dns; the whole keystroke budget is 200ms", perOp)
	}
}
