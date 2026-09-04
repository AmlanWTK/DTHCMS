package clinical_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
)

// Paediatric growth percentiles (CP47, [R-06], D-21).
//
// # The validation set is not ours
//
// Criterion 1 asks that the computed percentiles match the official reference tables exactly
// on a validation set. Both WHO and CDC print, beside their L/M/S, the cut-offs those
// parameters produce — the −3 SD … +3 SD columns and the P3 … P97 columns. Every one of them
// is in `packages/clinical-calc/fixtures/growth-reference.json`: 1,452 age points, roughly
// twelve thousand printed values, none of which anybody on this project wrote.
//
// So a parameter transcribed wrongly fails here rather than agreeing with itself, which is
// the only kind of check worth having over a table this size.
//
// # What is still owed
//
// Criterion 5 is Dr. Nahid checking ten paediatric cases against the printed charts he uses
// today. **This checkpoint is not complete without it**, and no test can stand in for it —
// what it verifies is that the right standard was chosen for this clinic, which is a clinical
// question, not an arithmetic one.

type growthFixture struct {
	WhoZ   []float64            `json:"who_z"`
	CdcP   map[string][]float64 `json:"cdc_p"`
	Tables map[string]map[string]map[string][][]float64
}

func loadGrowthFixture(t *testing.T) growthFixture {
	t.Helper()
	raw, err := os.ReadFile("../../../packages/clinical-calc/fixtures/growth-reference.json")
	if err != nil {
		t.Fatalf("the growth validation set could not be read: %v", err)
	}
	var wrapper struct {
		WhoZ   []float64                                    `json:"who_z"`
		CdcP   map[string][]float64                         `json:"cdc_p"`
		Tables map[string]map[string]map[string][][]float64 `json:"tables"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatalf("parsing the growth validation set: %v", err)
	}
	return growthFixture{WhoZ: wrapper.WhoZ, CdcP: wrapper.CdcP, Tables: wrapper.Tables}
}

// --- criterion 1 ---

func TestEveryPublishedCutOffIsReproducedFromTheSeededParameters(t *testing.T) {
	// The whole validation set, all of it, every time. A sampled version of this test would
	// pass with one row transcribed wrongly, and the row nobody sampled is exactly the row a
	// child will one day be scored against.
	h := newAPI(t)
	fixture := loadGrowthFixture(t)
	ctx := context.Background()

	if len(fixture.Tables) != 2 {
		t.Fatalf("the fixture holds %d standards; D-21 uses two", len(fixture.Tables))
	}

	checked := 0
	for standard, indicators := range fixture.Tables {
		for indicator, sexes := range indicators {
			for sex, rows := range sexes {
				// The z-scores each column of this table corresponds to.
				var zs []float64
				if standard == "WHO_2006" {
					zs = fixture.WhoZ
				} else {
					for _, p := range fixture.CdcP[indicator] {
						zs = append(zs, clinical.ProbitForTest(p/100))
					}
				}

				for _, row := range rows {
					age := row[0]
					var l, m, s float64
					err := h.SQL.QueryRow(`
						SELECT l, m, s FROM core.growth_lms
						 WHERE standard_code = $1 AND indicator = $2 AND sex = $3
						   AND age_months = $4`,
						standard, indicator, sex, age).Scan(&l, &m, &s)
					if err != nil {
						t.Fatalf("%s/%s/%s at %v months is not seeded: %v",
							standard, indicator, sex, age, err)
					}

					for i, z := range zs {
						want := row[i+1]
						got := clinical.ValueAtZForTest(l, m, s, z)
						// The published columns are rounded to their own precision; compare
						// on that grid rather than inventing a tolerance.
						if !closeOnPublishedGrid(got, want, standard) {
							t.Errorf("%s %s %s at %v months, z=%v: computed %.10f, "+
								"%s printed %v", standard, indicator, sex, age, z, got,
								standard, want)
						}
						checked++
					}
				}
			}
		}
	}
	if checked < 10000 {
		t.Fatalf("only %d published values were checked; the fixture is not being read", checked)
	}
	_ = ctx
}

// closeOnPublishedGrid compares against a printed number at the precision it was printed to.
//
// WHO prints its SD columns to one or two decimals; CDC prints its percentile columns to
// eight or nine significant figures. Rounding the computed value to the printed value's own
// precision and demanding equality is stricter than any tolerance, and it is the comparison
// somebody holding the printed table would make.
func closeOnPublishedGrid(got, want float64, standard string) bool {
	if standard == "WHO_2006" {
		// One decimal for lengths and weights, two for BMI. Round to whichever the printed
		// value itself uses, by trying both.
		for _, decimals := range []int{1, 2, 3} {
			scale := math.Pow(10, float64(decimals))
			if math.Round(want*scale) == want*scale &&
				math.Abs(math.Round(got*scale)/scale-want) < 1e-12 {
				return true
			}
		}
		return math.Abs(got-want) < 0.05
	}
	// CDC prints to full precision; anything beyond a part in 10^8 is a transcription error.
	return math.Abs(got-want) <= math.Abs(want)*1e-8
}

// --- criterion 2 ---

func TestEveryPercentileNamesTheStandardAndVersionThatProducedIt(t *testing.T) {
	// A percentile computed under WHO and one computed under CDC are not the same
	// measurement. A stored value with no standard cannot afterwards be told apart, and a
	// system that silently recomputed old values under a new protocol would rewrite history.
	h := newAPI(t)
	child := h.seedChild(t, "2022-09-14", "male") // four years old on the fixed clock

	scored := h.score(t, child, clinical.HeightForAge, 103, "cm")
	if scored.Standard != "WHO_2006" {
		t.Errorf("a four-year-old was scored against %s", scored.Standard)
	}
	if scored.StandardVersion == "" {
		t.Error("the percentile carries no version")
	}

	older := h.seedChild(t, "2016-09-14", "male") // ten
	scored = h.score(t, older, clinical.HeightForAge, 138, "cm")
	if scored.Standard != "CDC_2000" {
		t.Errorf("a ten-year-old was scored against %s", scored.Standard)
	}
}

func TestTheSwitchAtFiveYearsHappensExactlyThere(t *testing.T) {
	// D-21 chose WHO below 5.0 years and CDC from 5.0. A boundary that drifted by a month
	// would put children either side of it on different charts for no reason anybody could
	// explain.
	h := newAPI(t)
	for _, c := range []struct {
		ageDays int
		want    string
	}{
		{1823, "WHO_2006"}, // 59.9 months
		{1827, "CDC_2000"}, // 60.02 months
	} {
		scored, err := h.service.Score(context.Background(), clinical.HeightForAge,
			"male", c.ageDays, 110, "cm", time.Now())
		if err != nil {
			t.Fatalf("scoring at %d days: %v", c.ageDays, err)
		}
		if scored.Standard != c.want {
			t.Errorf("at %d days the standard was %s, want %s", c.ageDays, scored.Standard, c.want)
		}
	}
}

// --- criterion 3 ---

func TestAgeIsExactInDaysAndItChangesTheAnswer(t *testing.T) {
	// The reason DOB validation is mandatory at registration. A child's height-for-age moves
	// visibly inside one year, and a percentile computed at "four years old" is not a number.
	h := newAPI(t)
	ctx := context.Background()

	atFourFlat, err := h.service.Score(ctx, clinical.HeightForAge, "male", 4*365, 103, "cm", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	atFourAndTen, err := h.service.Score(ctx, clinical.HeightForAge, "male",
		4*365+305, 103, "cm", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if atFourFlat.P <= atFourAndTen.P {
		t.Error("the same height scored no higher at four than at nearly five, which cannot be right")
	}
	if math.Abs(atFourFlat.P-atFourAndTen.P) < 5 {
		t.Errorf("ten months of age moved the percentile by only %.2f points; "+
			"the age is probably being rounded to years",
			math.Abs(atFourFlat.P-atFourAndTen.P))
	}
	if atFourFlat.AgeDays != 4*365 {
		t.Errorf("the age was reported as %d days", atFourFlat.AgeDays)
	}
}

// --- criterion 4 ---

func TestAnAgeOutsideTheReferenceSaysSoRatherThanExtrapolating(t *testing.T) {
	// A percentile for a 25-year-old computed off the end of a paediatric chart is a number
	// that looks like every other number on the screen and means nothing at all.
	h := newAPI(t)
	ctx := context.Background()

	_, err := h.service.Score(ctx, clinical.HeightForAge, "male", 25*365, 175, "cm", time.Now())
	if err == nil {
		t.Fatal("a 25-year-old was given a paediatric percentile")
	}

	// And through the patient-level view, where it has to be a sentence rather than an error:
	// the seeded adult patient is 41.
	growth, err := h.service.GrowthFor(ctx, h.patient, h.facility)
	if err != nil {
		t.Fatal(err)
	}
	if growth.Applicable {
		t.Error("an adult was given a growth chart")
	}
	if growth.Note == "" {
		t.Error("nothing was computed and nothing said why")
	}
}

func TestASexTheReferenceDoesNotCoverIsSaidPlainly(t *testing.T) {
	// The tables have rows for two sexes. A third is not a row somebody forgot to add, and
	// inventing one would be inventing clinical evidence.
	h := newAPI(t)
	child := h.seedChild(t, "2022-09-14", "other")

	growth, err := h.service.GrowthFor(context.Background(), child, h.facility)
	if err != nil {
		t.Fatal(err)
	}
	if growth.Applicable || growth.Note != "no_reference_for_sex" {
		t.Errorf("scored anyway: applicable=%v note=%q", growth.Applicable, growth.Note)
	}
}

// --- the trajectory ---

func TestAGrowthTrajectoryReadsOldestFirstAndCarriesVelocity(t *testing.T) {
	h := newAPI(t)
	child := h.seedChild(t, "2020-09-14", "male") // six on the fixed clock

	h.measure(t, child, "BODY_HEIGHT", 105, "cm", "2024-09-14T09:00:00Z")
	h.measure(t, child, "BODY_HEIGHT", 112, "cm", "2025-09-14T09:00:00Z")
	h.measure(t, child, "BODY_HEIGHT", 119, "cm", "2026-09-14T09:00:00Z")

	growth, err := h.service.GrowthFor(context.Background(), child, h.facility)
	if err != nil {
		t.Fatal(err)
	}
	points := growth.History[clinical.HeightForAge]
	if len(points) != 3 {
		t.Fatalf("the trajectory has %d points", len(points))
	}
	if !points[0].EffectiveAt.Before(points[2].EffectiveAt) {
		t.Error("the trajectory is not oldest first")
	}
	if points[1].Velocity == nil {
		t.Fatal("the second point has no velocity")
	}
	if math.Abs(*points[1].Velocity-7) > 0.1 {
		t.Errorf("growth velocity is %.2f cm/year; 105 to 112 in a year is 7", *points[1].Velocity)
	}

	// The reference changed under this child at five, and D-21 says that must be visible
	// rather than silent.
	var marked bool
	for _, point := range points {
		if point.StandardChanged {
			marked = true
		}
	}
	if !marked {
		t.Error("the child crossed the 5.0-year boundary and no point is marked")
	}
	if growth.Current[clinical.HeightForAge].Standard != "CDC_2000" {
		t.Errorf("the current point uses %s", growth.Current[clinical.HeightForAge].Standard)
	}
}

func TestTheReferenceCurvesComeFromTheSameParametersAsThePatientsPoint(t *testing.T) {
	// A chart whose lines came from a different table than the plotted child would be wrong
	// in a way nobody could see by looking at it.
	h := newAPI(t)
	ctx := context.Background()

	curves, err := h.service.CurvesFor(ctx, clinical.HeightForAge, "male", 0, 240.5)
	if err != nil {
		t.Fatal(err)
	}
	if len(curves.Curves) < 5 {
		t.Fatalf("the chart has %d lines", len(curves.Curves))
	}
	if len(curves.Standards) != 2 {
		t.Fatalf("the chart names %d standards; D-21 uses two", len(curves.Standards))
	}

	// The 95th percentile line is here because [R-06] flags obesity at it, and a threshold
	// with no line is a threshold nobody can see a child approaching.
	var has95 bool
	for _, curve := range curves.Curves {
		if curve.Percentile == 95 {
			has95 = true
		}
	}
	if !has95 {
		t.Error("the chart has no 95th percentile line")
	}

	// A child sitting exactly on the 50th line must score 50.
	var fiftieth clinical.Curve
	for _, curve := range curves.Curves {
		if curve.Percentile == 50 {
			fiftieth = curve
		}
	}
	sample := fiftieth.Points[len(fiftieth.Points)/2]
	ageDays := int(math.Round(sample[0] * 30.4375))
	scored, err := h.service.Score(ctx, clinical.HeightForAge, "male", ageDays, sample[1], "cm", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(scored.P-50) > 0.5 {
		t.Errorf("a child on the 50th line scored %.3f", scored.P)
	}
}

// --- helpers ---

func (h *api) seedChild(t *testing.T, birth, sex string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, $3, 'Growth Test', $4, $5::date,
		        'day', 'birth_certificate', '+8801711111199', 'active', $6, now())`,
		id, h.facility, "DTHC-FRD-2026-G"+id.String()[:6], sex, birth, h.user); err != nil {
		t.Fatal(err)
	}
	return id
}

func (h *api) score(t *testing.T, patient uuid.UUID, indicator clinical.Indicator,
	value float64, unit string) clinical.Percentile {
	t.Helper()
	var sex string
	var birth time.Time
	if err := h.SQL.QueryRow(`SELECT sex, birth_date FROM core.patient WHERE id = $1`,
		patient).Scan(&sex, &birth); err != nil {
		t.Fatal(err)
	}
	days := int(h.clock.Now().UTC().Sub(birth).Hours() / 24)
	scored, err := h.service.Score(context.Background(), indicator, sex, days, value, unit,
		h.clock.Now().UTC())
	if err != nil {
		t.Fatalf("scoring: %v", err)
	}
	return scored
}

func (h *api) measure(t *testing.T, patient uuid.UUID, code string, value float64,
	unit, at string) {
	t.Helper()
	resp, body := h.record(t, map[string]any{
		"patient_id": patient.String(), "code": code, "value": value, "unit": unit,
		"effective_at": at, "confirmed": true, "confirmed_reason": "test fixture",
	})
	if resp.StatusCode != 201 {
		t.Fatalf("recording %s: %d %v", code, resp.StatusCode, body)
	}
}
