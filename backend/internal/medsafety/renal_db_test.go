package medsafety_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
)

// Renal dosing (CP79).
//
// Four acceptance criteria, and each is written here against the property rather than against the
// presence of a field:
//
//  1. the eGFR used is displayed with its date;
//  2. a stale eGFR triggers an explicit warning;
//  3. missing creatinine for a drug that needs one fails closed;
//  4. banding matches the authored rules exactly.
//
// The one that decides whether this checkpoint works is (2), and it is a boundary. A staleness
// window is the kind of thing that is off by one unit for two years before anybody notices,
// because nothing about a wrong answer at 183 days looks wrong. So it is tested at the day
// before, the instant of, and the instant after — and at five, six and seven calendar months,
// which is the sentence a physician would say.

// ---------------------------------------------------------------------------
// Criterion 2 — the boundary
// ---------------------------------------------------------------------------

func TestTheStalenessBoundaryIsExact(t *testing.T) {
	// **Delete the `at.After(expires)` comparison, or change months to days, and this goes
	// red.** The five-month and seven-month cases alone would not catch either: the interesting
	// cases are the two that sit either side of the exact instant, and the calendar-month case
	// that a 180-day implementation gets wrong while looking right.
	run := newGoldenRun(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name      string
		asOf      time.Time
		wantStale bool
		why       string
	}{
		{"five months ago", now.AddDate(0, -5, 0), false,
			"five months is inside a six-month window by a month"},
		{"six months ago exactly", now.AddDate(0, -6, 0), false,
			"exactly at the window is the last instant a result counts. " +
				"A 180-day implementation calls this stale, because six calendar months " +
				"before 12 September is 184 days"},
		{"six months ago plus one second", now.AddDate(0, -6, 0).Add(-time.Second), true,
			"one second past the window is the first instant a result does not count"},
		{"seven months ago", now.AddDate(0, -7, 0), true,
			"seven months is outside a six-month window by a month"},
		{"one day short of six months", now.AddDate(0, -6, 1), false, "inside, by a day"},
		{"one day past six months", now.AddDate(0, -6, -1), true, "outside, by a day"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asOf := c.asOf
			result, err := run.engine.Check(ctx, medsafety.Request{
				FacilityID: run.facility, At: now,
				Picture: medsafety.Picture{
					EGFR: f(52), EGFRAsOf: &asOf,
					Diagnoses: []string{}, AllergenGroups: []string{},
				},
				Proposed: []medsafety.Item{
					{Ref: "1", Generic: "Metformin hydrochloride", Label: "Comet 500"},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Renal.Stale != c.wantStale {
				t.Fatalf("an eGFR from %s is stale=%v, want %v — %s",
					asOf.Format("2 January 2006 15:04:05"), result.Renal.Stale, c.wantStale, c.why)
			}
			// The warning is criterion 2, and it must be a *finding* rather than only a flag
			// on a struct: a physician reads the finding list, and a boolean on a response
			// nobody rendered is not a warning.
			found, finding := findingFor(result, medsafety.CodeRenalStale, "")
			if found != c.wantStale {
				t.Fatalf("stale finding present=%v while stale=%v; the flag and the "+
					"finding must not disagree", found, c.wantStale)
			}
			if c.wantStale {
				if finding.Outcome != medsafety.OutcomeFires {
					t.Errorf("the stale finding is %s, want FIRES: a stale eGFR is a known "+
						"fact, not an absence", finding.Outcome)
				}
				if finding.MessageBN == "" {
					t.Error("the stale warning has no Bengali")
				}
			}
		})
	}
}

func TestTheWindowIsTheFacilitysAndNotAConstant(t *testing.T) {
	// The checkpoint's decision was "configurable per facility, default six months". A test
	// that only checked the default would pass against a hardcoded 6 — so this changes the row
	// and asserts the answer moves with it.
	run := newGoldenRun(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	asOf := now.AddDate(0, -4, 0) // four months old

	check := func() medsafety.RenalStatus {
		t.Helper()
		when := asOf
		result, err := run.engine.Check(ctx, medsafety.Request{
			FacilityID: run.facility, At: now,
			Picture: medsafety.Picture{EGFR: f(52), EGFRAsOf: &when,
				Diagnoses: []string{}, AllergenGroups: []string{}},
			Proposed: []medsafety.Item{{Ref: "1", Generic: "Metformin hydrochloride"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result.Renal
	}

	if before := check(); before.Stale {
		t.Fatalf("four months old is stale under the default six-month window")
	} else if before.Policy.RecencyMonths != 6 {
		t.Fatalf("the default window is %d months, want 6", before.Policy.RecencyMonths)
	} else if before.Policy.Approved {
		t.Error("the seeded window reports itself approved. Nobody has approved it, and the " +
			"indicator's honesty depends on this being false until somebody does.")
	}

	if _, err := run.SQL.Exec(
		`UPDATE core.facility_renal_policy SET egfr_recency_months = 3 WHERE facility_id = $1`,
		run.facility); err != nil {
		t.Fatal(err)
	}

	after := check()
	if !after.Stale {
		t.Error("four months old is still current after the window was narrowed to three " +
			"months. The window is being read from somewhere other than the facility's row.")
	}
	if after.Policy.RecencyMonths != 3 {
		t.Errorf("the window reports %d months after being set to 3", after.Policy.RecencyMonths)
	}
}

// ---------------------------------------------------------------------------
// Criterion 3 — fail closed, proved by removal
// ---------------------------------------------------------------------------

func TestNoEGFRForADrugThatNeedsOneFailsClosed(t *testing.T) {
	// Proved by removal: the same prescription is checked twice, once with a kidney function
	// and once with the row deleted, and the difference is the whole assertion. A test that
	// only ran the absent case could pass against an engine that emitted the finding always.
	run := newGoldenRun(t)
	ctx := context.Background()
	at := time.Now().UTC()
	run.approve(t, []string{"MET-RENAL-30", "MET-RENAL-45"}, at)
	taken := at.AddDate(0, -1, 0)

	item := medsafety.Item{Ref: "1", Generic: "Metformin hydrochloride", Label: "Comet 500"}

	with, err := run.engine.Check(ctx, medsafety.Request{
		FacilityID: run.facility, At: at,
		Picture: medsafety.Picture{EGFR: f(88), EGFRAsOf: &taken,
			Diagnoses: []string{}, AllergenGroups: []string{}},
		Proposed: []medsafety.Item{item},
	})
	if err != nil {
		t.Fatal(err)
	}
	if found, _ := findingFor(with, medsafety.CodeRenalAbsent, "1"); found {
		t.Fatal("the missing-eGFR finding fired while an eGFR of 88 was on file")
	}

	without, err := run.engine.Check(ctx, medsafety.Request{
		FacilityID: run.facility, At: at,
		Picture: medsafety.Picture{
			// Nil, not zero. A zero eGFR is anuric renal failure.
			EGFR: nil, EGFRAsOf: nil, Diagnoses: []string{}, AllergenGroups: []string{},
		},
		Proposed: []medsafety.Item{item},
	})
	if err != nil {
		t.Fatal(err)
	}
	found, finding := findingFor(without, medsafety.CodeRenalAbsent, "1")
	if !found {
		t.Fatal("metformin with no eGFR on file produced no finding. This is the exact " +
			"silence §7.2 says must never happen.")
	}
	if finding.Outcome != medsafety.OutcomeCannotVerify {
		t.Errorf("the finding is %s, want CANNOT_VERIFY: nothing is known to be wrong, what "+
			"is known is that nobody can tell", finding.Outcome)
	}
	if finding.Severity != medsafety.SeverityBlock {
		t.Errorf("the finding is %s, want BLOCK. Fail-closed means the prescription does not "+
			"proceed as though it had been checked, and a warning among warnings is not that.",
			finding.Severity)
	}
	if without.Verdict != medsafety.VerdictCannotVerify {
		t.Errorf("the verdict is %s, want CANNOT_VERIFY", without.Verdict)
	}
	if finding.MessageBN == "" || finding.AdviceBN == "" {
		t.Error("the fail-closed finding does not read in Bengali")
	}
	assertNeverReadsAsSafe(t, without, 1)
}

func TestADrugThatDoesNotNeedAnEGFRDoesNotFailClosed(t *testing.T) {
	// The other half, and the one that keeps criterion 3 from being satisfied by a finding on
	// every line. Linagliptin needs no eGFR — that is why every seeded renal rule offers it as
	// the alternative — and a fail-closed finding on it would teach the physician to click past
	// the ones that matter.
	run := newGoldenRun(t)
	result, err := run.engine.Check(context.Background(), medsafety.Request{
		FacilityID: run.facility, At: time.Now().UTC(),
		Picture: medsafety.Picture{Diagnoses: []string{}, AllergenGroups: []string{}},
		Proposed: []medsafety.Item{
			{Ref: "1", Generic: "Linagliptin", Label: "Trajenta 5"},
			{Ref: "2", Generic: "Levothyroxine sodium", Label: "Thyrox 50"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"1", "2"} {
		if found, _ := findingFor(result, medsafety.CodeRenalAbsent, ref); found {
			t.Errorf("a drug classified EGFR_NOT_REQUIRED raised the fail-closed finding on "+
				"ref %s", ref)
		}
	}
}

func TestAnUnclassifiedMoleculeIsReportedAndNeverReadAsSafe(t *testing.T) {
	// Voglibose is deliberately left unclassified by migration 00061, because the published
	// renal data is thinner than acarbose's and a confident answer would be invented. The
	// engine has to say so — "nobody has classified this" and "this does not need an eGFR"
	// must never render the same, which is CP78's coverage argument applied to renal dosing.
	run := newGoldenRun(t)
	result, err := run.engine.Check(context.Background(), medsafety.Request{
		FacilityID: run.facility, At: time.Now().UTC(),
		Picture:  medsafety.Picture{Diagnoses: []string{}, AllergenGroups: []string{}},
		Proposed: []medsafety.Item{{Ref: "1", Generic: "Voglibose", Label: "Volix 0.2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	found, finding := findingFor(result, medsafety.CodeRenalUnclassified, "1")
	if !found {
		t.Fatal("an unclassified molecule with no eGFR on file produced no finding")
	}
	if finding.Outcome != medsafety.OutcomeCannotVerify {
		t.Errorf("the finding is %s, want CANNOT_VERIFY", finding.Outcome)
	}
	if result.Verdict == medsafety.VerdictClearWithinCoverage {
		t.Error("the verdict is CLEAR_WITHIN_COVERAGE for a drug nobody has classified and a " +
			"patient with no kidney function on file")
	}
}

// ---------------------------------------------------------------------------
// Criterion 4 — banding matches the authored rules exactly
// ---------------------------------------------------------------------------

func TestTheBandingMatchesTheAuthoredRulesExactly(t *testing.T) {
	// Every seeded renal rule is `EGFR LT value`, so the value itself must *not* fire and one
	// below it must. Off-by-one here is a metformin prescription blocked at exactly 30, or —
	// far worse — allowed at 29.9.
	run := newGoldenRun(t)
	ctx := context.Background()
	at := time.Now().UTC()
	run.approve(t, []string{"MET-RENAL-30", "MET-RENAL-45"}, at)
	taken := at.AddDate(0, -1, 0)

	cases := []struct {
		egfr       float64
		wantBlock  bool
		wantWarn45 bool
	}{
		{88, false, false},
		{45.1, false, false},
		{45, false, false},  // LT 45 — exactly 45 does not fire
		{44.9, false, true}, // just below 45
		{30.1, false, true},
		{30, false, true},  // LT 30 — exactly 30 does not block
		{29.9, true, true}, // just below 30
		{0, true, true},    // anuric: a real value, not "unknown"
	}
	for _, c := range cases {
		when := taken
		value := c.egfr
		result, err := run.engine.Check(ctx, medsafety.Request{
			FacilityID: run.facility, At: at,
			Picture: medsafety.Picture{EGFR: &value, EGFRAsOf: &when,
				Diagnoses: []string{}, AllergenGroups: []string{}},
			Proposed: []medsafety.Item{
				{Ref: "1", Generic: "Metformin hydrochloride", Label: "Comet 500"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		blocked, _ := findingFor(result, "MET-RENAL-30", "1")
		warned, _ := findingFor(result, "MET-RENAL-45", "1")
		if blocked != c.wantBlock {
			t.Errorf("eGFR %v: MET-RENAL-30 fired=%v, want %v", c.egfr, blocked, c.wantBlock)
		}
		if warned != c.wantWarn45 {
			t.Errorf("eGFR %v: MET-RENAL-45 fired=%v, want %v", c.egfr, warned, c.wantWarn45)
		}
		if c.wantBlock && result.Verdict != medsafety.VerdictBlocked {
			t.Errorf("eGFR %v: verdict is %s, want BLOCKED", c.egfr, result.Verdict)
		}
	}
}

func TestTheCKDStageBoundariesAreKDIGOsAndNotRounded(t *testing.T) {
	// KDIGO 2024 table 1, at every edge. The G3a/G3b split at 45 is the one that matters most
	// here, because it is where metformin's dose guidance changes.
	cases := []struct {
		egfr float64
		want medsafety.Stage
	}{
		{120, medsafety.StageG1}, {90, medsafety.StageG1}, {89.9, medsafety.StageG2},
		{60, medsafety.StageG2}, {59.9, medsafety.StageG3a},
		{45, medsafety.StageG3a}, {44.9, medsafety.StageG3b},
		{30, medsafety.StageG3b}, {29.9, medsafety.StageG4},
		{15, medsafety.StageG4}, {14.9, medsafety.StageG5}, {0, medsafety.StageG5},
	}
	for _, c := range cases {
		if got := medsafety.StageOf(c.egfr); got != c.want {
			t.Errorf("eGFR %v is stage %s, want %s", c.egfr, got, c.want)
		}
	}
	for _, s := range []medsafety.Stage{
		medsafety.StageG1, medsafety.StageG2, medsafety.StageG3a,
		medsafety.StageG3b, medsafety.StageG4, medsafety.StageG5,
	} {
		en, bn := medsafety.StageLabel(s)
		if en == "" || bn == "" {
			t.Errorf("stage %s has no label in one of the two languages", s)
		}
	}
}

// ---------------------------------------------------------------------------
// Criterion 1 — the value and its date, together, always
// ---------------------------------------------------------------------------

func TestTheRenalStatusAlwaysCarriesTheDateAndASentence(t *testing.T) {
	run := newGoldenRun(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	taken := at.AddDate(0, 0, -43)

	result, err := run.engine.Check(ctx, medsafety.Request{
		FacilityID: run.facility, At: at,
		Picture: medsafety.Picture{EGFR: f(41.379310344827587), EGFRAsOf: &taken,
			Diagnoses: []string{}, AllergenGroups: []string{}},
		Proposed: []medsafety.Item{{Ref: "1", Generic: "Metformin hydrochloride"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	renal := result.Renal
	if !renal.Known || renal.EGFR == nil || renal.AsOf == nil {
		t.Fatal("the renal status does not carry both the value and its date")
	}
	if !renal.AsOf.Equal(taken) {
		t.Errorf("the date reported is %s, want %s", renal.AsOf, taken)
	}
	if renal.AgeDays == nil || *renal.AgeDays != 43 {
		t.Errorf("the age reported is %v, want 43 days", renal.AgeDays)
	}
	if renal.Stage != medsafety.StageG3b {
		t.Errorf("eGFR 41.4 is staged %s, want G3b", renal.Stage)
	}
	if renal.SummaryEN == "" || renal.SummaryBN == "" {
		t.Fatal("the renal status has no sentence in one of the two languages")
	}
	// A derived eGFR is a float with a long tail. A screen must not be handed
	// 41.379310344827587.
	if want := "41.4"; !strings.Contains(renal.SummaryEN, want) {
		t.Errorf("the summary reads %q and does not contain %q", renal.SummaryEN, want)
	}
	if strings.Contains(renal.SummaryEN, "41.379") {
		t.Errorf("the summary renders the raw derived float: %q", renal.SummaryEN)
	}

	// And with nothing on file, the sentence says so rather than being blank.
	absent, err := run.engine.Check(ctx, medsafety.Request{
		FacilityID: run.facility, At: at,
		Picture:  medsafety.Picture{Diagnoses: []string{}, AllergenGroups: []string{}},
		Proposed: []medsafety.Item{{Ref: "1", Generic: "Metformin hydrochloride"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if absent.Renal.Known {
		t.Error("a patient with no eGFR reports Known=true")
	}
	if absent.Renal.SummaryEN == "" || absent.Renal.SummaryBN == "" {
		t.Error("the absent case has an empty sentence. An empty indicator reads as " +
			"'checked, nothing found', which is the opposite of what is true.")
	}
}

func TestAnUndatedEGFRIsNeverTreatedAsCurrent(t *testing.T) {
	// The bridge cannot produce one — `Renal` returns the value and the instant together or
	// neither — but a caller assembling a picture by hand can, and a value whose age is unknown
	// must not be quietly treated as fresh.
	run := newGoldenRun(t)
	result, err := run.engine.Check(context.Background(), medsafety.Request{
		FacilityID: run.facility, At: time.Now().UTC(),
		Picture: medsafety.Picture{EGFR: f(52), EGFRAsOf: nil,
			Diagnoses: []string{}, AllergenGroups: []string{}},
		Proposed: []medsafety.Item{{Ref: "1", Generic: "Metformin hydrochloride"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Renal.Stale {
		t.Error("an eGFR with no date against it is reported as current")
	}
}
