package clinical

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Paediatric growth percentiles (CP47, [R-06], D-21).
//
// # Why this one lives only on the server
//
// ADR-0025 says every clinical formula has two implementations held together by fixtures. This
// deliberately does not, and the distinction is worth stating: what is duplicated there is an
// *equation*, a dozen lines that two people can independently get right. What would be
// duplicated here is a **table** — 1,452 rows of published parameters — and two copies of a
// table do not drift the way two copies of an equation do. They drift silently, one row at a
// time, and no fixture catches the row nobody happened to test.
//
// So the table lives in one place, the arithmetic that reads it lives beside it, and the
// client asks. CP48's card and chart need the patient's own history plotted against the
// reference curves anyway, which is a join only the server can do.
//
// # The arithmetic
//
// The LMS method, exactly as both publishers define it:
//
//	z = ((X/M)^L − 1) / (L·S)   for L ≠ 0
//	z = ln(X/M) / S             for L = 0
//
// and the percentile is Φ(z). Nothing else. The care is in what surrounds it: the age, the
// choice of table, the edges, and the honesty about what is not computable.
//
// # Exact age, and why in days
//
// Criterion 3. A percentile at "four years old" is not a number: a child's height-for-age
// moves visibly across a single year, and the difference between 4y 0m and 4y 11m is the
// difference between the 40th and the 25th percentile for the same height. So the age is
// computed in whole days from the validated date of birth and converted to months as
// days ÷ 30.4375 — the conversion both standards specify, not an approximation chosen here.
//
// # Between published points
//
// L, M and S are interpolated linearly between the two nearest published ages, which is what
// CDC's own documentation instructs. At a published age the interpolation is a lookup, which
// is why the validation set — every published point — proves the parameters exactly.
//
// # What it refuses
//
// Criterion 4: an age outside the reference returns "not applicable", never an extrapolated
// number. A percentile for a 25-year-old computed off the end of a paediatric chart is a
// number that looks like every other number on the screen and means nothing at all.

// Indicator is what is being scored against age.
type Indicator string

const (
	HeightForAge Indicator = "HFA"
	WeightForAge Indicator = "WFA"
	BMIForAge    Indicator = "BFA"
)

// Indicators is the closed list, in the order a growth card shows them.
var Indicators = []Indicator{HeightForAge, WeightForAge, BMIForAge}

// The code each indicator scores.
var indicatorCode = map[Indicator]string{
	HeightForAge: "BODY_HEIGHT",
	WeightForAge: "BODY_WEIGHT",
	BMIForAge:    "BMI",
}

// ErrNotApplicable is an age or a measurement the reference does not cover. Deliberately a
// distinct answer rather than an error: "this child is too old for a growth chart" is a
// clinical fact a screen should say plainly, not a failure.
var ErrNotApplicable = errors.New("clinical: growth percentiles do not apply at this age")

// Percentile is one scored measurement.
type Percentile struct {
	Indicator Indicator `json:"indicator"`
	Code      string    `json:"code"`
	// Value is the measurement, canonical.
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
	// AgeDays is exact, from the validated date of birth. AgeMonths is what the tables are
	// keyed by; both are reported because a clinician reads one and the chart plots the other.
	AgeDays   int     `json:"age_days"`
	AgeMonths float64 `json:"age_months"`

	// Z is the z-score and P the percentile. Both, because they answer different questions:
	// a percentile is what a parent understands and a z-score is what a change over time is
	// measured in — and beyond about the 99th percentile the percentile stops discriminating
	// while the z-score keeps going.
	Z float64 `json:"z"`
	P float64 `json:"percentile"`

	// Standard and StandardVersion are stored with every computed value (criterion 2), so a
	// number from 2026 stays interpretable if the protocol moves.
	Standard        string `json:"standard"`
	StandardVersion string `json:"standard_version"`
	// L, M and S are reported so a clinician can reproduce the number by hand.
	L float64 `json:"l"`
	M float64 `json:"m"`
	S float64 `json:"s"`

	// EffectiveAt is when the measurement was taken.
	EffectiveAt time.Time `json:"effective_at"`
}

// GrowthPoint is one indicator on one visit, for a trajectory.
type GrowthPoint struct {
	Percentile
	// Velocity is the change in the measurement per year since the previous point of the
	// same indicator, when there is one and the two are far enough apart to mean something.
	Velocity     *float64 `json:"velocity_per_year,omitempty"`
	VelocityUnit string   `json:"velocity_unit,omitempty"`
	// StandardChanged marks the point where the reference switched — the 5.0-year boundary
	// D-21 insists must be visible rather than silent.
	StandardChanged bool `json:"standard_changed,omitempty"`
}

// Growth is everything the percentile card and the chart need.
type Growth struct {
	PatientID uuid.UUID `json:"patient_id"`
	Sex       string    `json:"sex"`
	AgeDays   int       `json:"age_days"`
	// Applicable is false for a patient outside every band. The rest is then empty, and the
	// screen says so rather than drawing an empty chart.
	Applicable bool `json:"applicable"`
	// Current is the newest scored measurement per indicator.
	Current map[Indicator]Percentile `json:"current,omitempty"`
	// History is every scored measurement, oldest first, per indicator.
	History map[Indicator][]GrowthPoint `json:"history,omitempty"`
	// Note names why nothing was computed, when nothing was.
	Note string `json:"note,omitempty"`
}

// --- the arithmetic ---

// zScore is the LMS transform. The whole of it.
func zScore(value, l, m, s float64) (float64, error) {
	if value <= 0 || m <= 0 || s <= 0 {
		return 0, ErrNotApplicable
	}
	if math.Abs(l) < 1e-12 {
		return math.Log(value/m) / s, nil
	}
	return (math.Pow(value/m, l) - 1) / (l * s), nil
}

// percentileOf is Φ(z), as a percentage.
//
// math.Erfc rather than a series of my own: the standard library's is correct to the last
// bit, and a hand-rolled normal CDF is exactly the kind of thing that is right to four
// decimal places and wrong in the tail — which is where a paediatric percentile matters most.
func percentileOf(z float64) float64 {
	return 100 * 0.5 * math.Erfc(-z/math.Sqrt2)
}

// ageMonthsOf converts exact days to the month scale both standards are keyed by.
//
// 30.4375 is 365.25 ÷ 12, which is the conversion CDC's own program uses and WHO's tables
// assume. Not 30, and not "months since birth by calendar", either of which would shift a
// child by up to half a percentile band.
func ageMonthsOf(days int) float64 { return float64(days) / 30.4375 }

// ageDaysBetween is whole days, which is what criterion 3 asks for.
func ageDaysBetween(birth, at time.Time) int {
	b := time.Date(birth.Year(), birth.Month(), birth.Day(), 0, 0, 0, 0, time.UTC)
	a := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	return int(a.Sub(b).Hours() / 24)
}

// --- the table ---

// lmsRow is one published point.
type lmsRow struct {
	ageMonths float64
	l, m, s   float64
}

// edgeToleranceMonths is how far past a table's own first or last published row an age may
// fall and still be scored, using that row's parameters unchanged.
//
// It exists for exactly one gap, and naming it is better than pretending the gap is not
// there. D-21 puts the handover at 5.0 years; WHO's last published row is at 60.0 months and
// CDC's first is at 60.5, because CDC publishes on half-months. A child between those two
// ages is inside the CDC band and outside the CDC table by fifteen days.
//
// The alternatives were worse. Moving the handover to 60.5 would contradict a recorded
// clinical decision to save a fortnight; returning "not applicable" would tell a parent
// standing in the room that their five-year-old cannot be plotted. So the child is scored
// against CDC's first published row, half a month early — an error far smaller than the
// interpolation this function does everywhere else, and confined to one boundary.
//
// One month, not more. A tolerance wide enough to cover a real gap in the data is a tolerance
// that would hide one.
const edgeToleranceMonths = 1.0

// interpolate returns the parameters at an exact age, and whether the age is covered.
//
// Linear in L, M and S between the two nearest published points, which is what CDC's
// documentation instructs. Beyond the published range by more than edgeToleranceMonths it
// returns false rather than clamping: a clamped percentile far outside the table is an
// extrapolation wearing a lookup's clothes.
func interpolate(rows []lmsRow, ageMonths float64) (lmsRow, bool) {
	if len(rows) == 0 {
		return lmsRow{}, false
	}
	first, last := rows[0], rows[len(rows)-1]
	if ageMonths < first.ageMonths {
		if first.ageMonths-ageMonths > edgeToleranceMonths {
			return lmsRow{}, false
		}
		return first, true
	}
	if ageMonths > last.ageMonths {
		if ageMonths-last.ageMonths > edgeToleranceMonths {
			return lmsRow{}, false
		}
		return last, true
	}
	index := sort.Search(len(rows), func(i int) bool { return rows[i].ageMonths >= ageMonths })
	if rows[index].ageMonths == ageMonths {
		return rows[index], true
	}
	lower, upper := rows[index-1], rows[index]
	span := upper.ageMonths - lower.ageMonths
	if span <= 0 {
		return lower, true
	}
	t := (ageMonths - lower.ageMonths) / span
	return lmsRow{
		ageMonths: ageMonths,
		l:         lower.l + t*(upper.l-lower.l),
		m:         lower.m + t*(upper.m-lower.m),
		s:         lower.s + t*(upper.s-lower.s),
	}, true
}

// --- the store's half ---

// growthTable is one indicator, one sex, one standard: the rows and the standard's identity.
type growthTable struct {
	standard string
	version  string
	rows     []lmsRow
}

// standardFor is D-21 applied: which standard covers this age for this indicator.
func (s *Store) standardFor(ctx context.Context, indicator Indicator, ageMonths float64) (string, bool, error) {
	code, err := s.q.GrowthStandardForAge(ctx, dbgen.GrowthStandardForAgeParams{
		Indicator: string(indicator), AgeMonths: numericOf(ageMonths),
	})
	if err != nil {
		return "", false, nil //nolint:nilerr // no band is "not applicable", not a failure
	}
	return code, true, nil
}

// growthTableFor loads one table.
func (s *Store) growthTableFor(ctx context.Context, standard string, indicator Indicator,
	sex string) (growthTable, error) {

	rows, err := s.q.GrowthLMS(ctx, dbgen.GrowthLMSParams{
		StandardCode: standard, Indicator: string(indicator), Sex: sex,
	})
	if err != nil {
		return growthTable{}, err
	}
	version, err := s.q.GrowthStandardVersion(ctx, standard)
	if err != nil {
		return growthTable{}, err
	}
	out := growthTable{standard: standard, version: version}
	for _, row := range rows {
		age, _ := numericValue(row.AgeMonths)
		l, _ := numericValue(row.L)
		m, _ := numericValue(row.M)
		sigma, _ := numericValue(row.S)
		out.rows = append(out.rows, lmsRow{ageMonths: age, l: l, m: m, s: sigma})
	}
	return out, nil
}

// --- the service ---

// Score computes one percentile: the arithmetic, with the table chosen by D-21's bands.
func (s *Service) Score(ctx context.Context, indicator Indicator, sex string,
	ageDays int, value float64, unit string, at time.Time) (Percentile, error) {

	ageMonths := ageMonthsOf(ageDays)
	standard, covered, err := s.store.standardFor(ctx, indicator, ageMonths)
	if err != nil {
		return Percentile{}, err
	}
	if !covered {
		return Percentile{}, ErrNotApplicable
	}
	table, err := s.store.growthTableFor(ctx, standard, indicator, sex)
	if err != nil {
		return Percentile{}, err
	}
	row, ok := interpolate(table.rows, ageMonths)
	if !ok {
		return Percentile{}, ErrNotApplicable
	}
	z, err := zScore(value, row.l, row.m, row.s)
	if err != nil {
		return Percentile{}, err
	}
	return Percentile{
		Indicator: indicator, Code: indicatorCode[indicator],
		Value: value, Unit: unit,
		AgeDays: ageDays, AgeMonths: ageMonths,
		Z: z, P: percentileOf(z),
		Standard: table.standard, StandardVersion: table.version,
		L: row.l, M: row.m, S: row.s,
		EffectiveAt: at,
	}, nil
}

// GrowthFor is the whole picture for one patient: current percentiles and the trajectory.
func (s *Service) GrowthFor(ctx context.Context, patientID, facility uuid.UUID) (Growth, error) {
	var sex string
	var birth time.Time
	err := s.store.pool.QueryRow(ctx,
		`SELECT sex, birth_date FROM core.patient WHERE id = $1 AND facility_id = $2`,
		patientID, facility).Scan(&sex, &birth)
	if err != nil {
		return Growth{}, ErrNotFound
	}

	now := s.clock.Now().UTC()
	out := Growth{PatientID: patientID, Sex: sex, AgeDays: ageDaysBetween(birth, now)}

	// The equations have coefficients for two sexes and the tables have rows for two. A
	// third is not a coefficient somebody forgot to add; refusing is the honest answer, and
	// it is a sentence rather than a silent zero.
	if sex != "male" && sex != "female" {
		out.Note = "no_reference_for_sex"
		return out, nil
	}
	if ageMonthsOf(out.AgeDays) > 240.5 {
		out.Note = "too_old_for_a_growth_reference"
		return out, nil
	}

	out.Current = map[Indicator]Percentile{}
	out.History = map[Indicator][]GrowthPoint{}

	for _, indicator := range Indicators {
		rows, err := s.store.History(ctx, patientID, facility, indicatorCode[indicator], 200)
		if err != nil {
			return Growth{}, err
		}
		// History is newest first; a trajectory reads oldest first.
		points := make([]GrowthPoint, 0, len(rows))
		for i := len(rows) - 1; i >= 0; i-- {
			row := rows[i]
			if row.Value == nil || row.Status != Active {
				continue
			}
			ageDays := ageDaysBetween(birth, row.EffectiveAt)
			if ageDays < 0 {
				continue
			}
			scored, err := s.Score(ctx, indicator, sex, ageDays, *row.Value, row.Unit, row.EffectiveAt)
			if errors.Is(err, ErrNotApplicable) {
				continue
			}
			if err != nil {
				return Growth{}, err
			}
			point := GrowthPoint{Percentile: scored}
			if len(points) > 0 {
				earlier := points[len(points)-1]
				// The switch at 5.0 years is a real discontinuity, not a rounding detail
				// (D-21). Marked so the chart can draw it and nobody compares across it.
				point.StandardChanged = earlier.Standard != scored.Standard
				years := scored.EffectiveAt.Sub(earlier.EffectiveAt).Hours() / 24 / 365.2425
				// Under a month, a velocity is measurement noise multiplied by twelve.
				if years > 1.0/12 {
					velocity := (scored.Value - earlier.Value) / years
					point.Velocity = &velocity
					point.VelocityUnit = scored.Unit + "/year"
				}
			}
			points = append(points, point)
		}
		if len(points) == 0 {
			continue
		}
		out.History[indicator] = points
		out.Current[indicator] = points[len(points)-1].Percentile
		out.Applicable = true
	}

	if !out.Applicable {
		out.Note = "nothing_measured_yet"
	}
	return out, nil
}

// Curves is the reference percentile curves for a chart (CP48).
//
// Computed from the same seeded parameters as the patient's own point, so a plotted child and
// the lines behind them cannot come from different tables — which would be a chart that is
// wrong in a way nobody could see.
type Curve struct {
	Percentile float64 `json:"percentile"`
	// Points are (age in months, value), oldest first.
	Points [][2]float64 `json:"points"`
}

type CurveSet struct {
	Indicator Indicator `json:"indicator"`
	Sex       string    `json:"sex"`
	Unit      string    `json:"unit"`
	// Standards names each standard's age span, so the chart can draw where the reference
	// changes rather than pretending one curve runs the whole way.
	Standards []CurveStandard `json:"standards"`
	Curves    []Curve         `json:"curves"`
}

type CurveStandard struct {
	Code          string  `json:"code"`
	Version       string  `json:"version"`
	MinAgeMonths  float64 `json:"min_age_months"`
	MaxAgeMonths  float64 `json:"max_age_months"`
	NameEN        string  `json:"name_en"`
	NameBN        string  `json:"name_bn"`
	AppliesToThis bool    `json:"applies_to_this_patient,omitempty"`
}

// The lines a growth chart draws. The 3rd and 97th are the outer pair every paediatric chart
// carries; the 95th is here because [R-06] flags childhood obesity at it, and a threshold
// with no line on the chart is a threshold nobody can see a child approaching.
var curvePercentiles = []float64{3, 15, 50, 85, 95, 97}

// CurvesFor builds the reference lines for one indicator and sex over an age span.
func (s *Service) CurvesFor(ctx context.Context, indicator Indicator, sex string,
	fromMonths, toMonths float64) (CurveSet, error) {

	if sex != "male" && sex != "female" {
		return CurveSet{}, ErrNotApplicable
	}
	bands, err := s.store.q.GrowthBands(ctx, string(indicator))
	if err != nil {
		return CurveSet{}, err
	}

	out := CurveSet{Indicator: indicator, Sex: sex, Unit: unitOfIndicator(indicator)}
	byPercentile := map[float64][][2]float64{}

	for _, band := range bands {
		lo, _ := numericValue(band.MinAgeMonths)
		hi, _ := numericValue(band.MaxAgeMonths)
		table, err := s.store.growthTableFor(ctx, band.StandardCode, indicator, sex)
		if err != nil {
			return CurveSet{}, err
		}
		out.Standards = append(out.Standards, CurveStandard{
			Code: band.StandardCode, Version: table.version,
			MinAgeMonths: lo, MaxAgeMonths: hi,
			NameEN: band.NameEn, NameBN: band.NameBn,
		})

		for _, row := range table.rows {
			if row.ageMonths < math.Max(lo, fromMonths) || row.ageMonths > math.Min(hi, toMonths) {
				continue
			}
			for _, p := range curvePercentiles {
				z := probit(p / 100)
				value := row.m * math.Pow(1+row.l*row.s*z, 1/row.l)
				if math.Abs(row.l) < 1e-12 {
					value = row.m * math.Exp(row.s*z)
				}
				byPercentile[p] = append(byPercentile[p], [2]float64{row.ageMonths, value})
			}
		}
	}

	for _, p := range curvePercentiles {
		points := byPercentile[p]
		sort.Slice(points, func(i, j int) bool { return points[i][0] < points[j][0] })
		out.Curves = append(out.Curves, Curve{Percentile: p, Points: points})
	}
	return out, nil
}

// valueAtPercentile is the measurement sitting on one reference line at one age.
//
// Used for [R-06]'s obesity threshold and for nothing else. It exists because "is this child
// above the 95th percentile" and "how far above it" are different questions, and the second
// one needs the line's own value.
func (s *Service) valueAtPercentile(ctx context.Context, indicator Indicator, sex string,
	ageMonths, percentile float64) (float64, error) {

	standard, covered, err := s.store.standardFor(ctx, indicator, ageMonths)
	if err != nil || !covered {
		return 0, ErrNotApplicable
	}
	table, err := s.store.growthTableFor(ctx, standard, indicator, sex)
	if err != nil {
		return 0, err
	}
	row, ok := interpolate(table.rows, ageMonths)
	if !ok {
		return 0, ErrNotApplicable
	}
	z := probit(percentile / 100)
	if math.Abs(row.l) < 1e-12 {
		return row.m * math.Exp(row.s*z), nil
	}
	return row.m * math.Pow(1+row.l*row.s*z, 1/row.l), nil
}

func unitOfIndicator(indicator Indicator) string {
	switch indicator {
	case HeightForAge:
		return "cm"
	case WeightForAge:
		return "kg"
	default:
		return "kg/m2"
	}
}

// probit is the inverse normal CDF, by bisection on math.Erfc.
//
// Slower than a rational approximation and exact to the limit of float64, which matters here:
// it is used to draw the reference curves, and a curve that is a thousandth off the published
// one is a curve a clinician can catch by holding a printed chart against the screen. It runs
// six times per age point when a chart is built, which is nothing.
func probit(p float64) float64 {
	if p <= 0 || p >= 1 {
		return math.NaN()
	}
	lo, hi := -10.0, 10.0
	for i := 0; i < 200; i++ {
		mid := (lo + hi) / 2
		if 0.5*math.Erfc(-mid/math.Sqrt2) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// obesityFlag is [R-06]'s threshold, with CDC's severity extension (D-21).
//
// ≥95th percentile is obesity. The class-2 and class-3 extensions — 120% and 140% of the 95th
// percentile — are CDC's own convention and matter in a caseload where obesity is the largest
// single presenting problem: above the 99th percentile the percentile scale stops
// discriminating, and "99.7th" covers children who are very differently unwell.
func obesityFlag(p Percentile, ninetyFifth float64) (string, float64) {
	if p.Indicator != BMIForAge || ninetyFifth <= 0 {
		return "", 0
	}
	ratio := 100 * p.Value / ninetyFifth
	switch {
	case ratio >= 140:
		return "obese_class_3", ratio
	case ratio >= 120:
		return "obese_class_2", ratio
	case p.P >= 95:
		return "obese", ratio
	case p.P >= 85:
		return "overweight", ratio
	case p.P < 5:
		return "underweight", ratio
	default:
		return "healthy", ratio
	}
}
