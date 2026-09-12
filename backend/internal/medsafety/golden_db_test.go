package medsafety_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
)

// The golden suite (CP78, acceptance criterion 1).
//
// # There is no second copy of any scenario in this file
//
// Every clinical scenario lives in `docs/medication-safety-golden-suite.json`, and this file is
// the machinery that runs it. That is deliberate and it is the point of the whole arrangement:
// the plan says the suite is authored with Dr. Nahid, and a suite he cannot read or edit without
// a Go compiler is a suite he will not author. He edits the JSON; the tests change; nothing is
// recompiled. A scenario he deletes is a scenario the engine stops being held to, and a severity
// he changes changes what the engine must do.
//
// # Zero false negatives, and how that is actually proved
//
// The verification bar asks for more than "the expected finding appeared", because a suite that
// passes against a broken engine is the worst artefact this project could produce. So every
// scenario is run **four times**, and two of the four are there to make the other two mean
// something:
//
//  1. **Unapproved.** The rules the scenario names are left as the drafts they ship as, and the
//     expected finding must *not* appear. This is the per-scenario false-negative proof: it makes
//     the engine blind to exactly the thing that should catch the scenario, and confirms the
//     scenario then fails. If an assertion passes here, it was never measuring the rule.
//  2. **Approved.** The named rules are approved inside the test, and the finding must appear
//     with the expected severity, on the expected prescription line.
//  3. **Blinded.** The one clinical fact the scenario names in `blind.remove` — the eGFR, the
//     allergy list, the diagnoses, the current medications, the drug's components — is taken
//     away, and the result must degrade to `CANNOT_VERIFY`. Never to a quiet pass.
//  4. **Reproduced.** The check is re-run against the instant of the first, and must produce the
//     identical verdict and the identical rule versions. Criterion 5.
//
// # Nothing here touches the development database
//
// `testsupport.Postgres` clones a template into a throwaway database per test, dropped at the
// end. The approvals this suite writes exist for the length of one subtest. **No seeded rule in
// the development database is ever approved by running these tests**, which is the brief's own
// decision and is asserted at the end of the run.

// ---------------------------------------------------------------------------
// The file
// ---------------------------------------------------------------------------

const goldenSuitePath = "../../../docs/medication-safety-golden-suite.json"

type goldenSuite struct {
	Format    int              `json:"format"`
	Gaps      []map[string]any `json:"gaps_that_cannot_be_expressed_yet"`
	Scenarios []goldenScenario `json:"scenarios"`
}

type goldenScenario struct {
	ID       string `json:"id"`
	Named    bool   `json:"named_in_the_plan"`
	TitleEN  string `json:"title_en"`
	TitleBN  string `json:"title_bn"`
	WhyEN    string `json:"why_en"`
	WhyBN    string `json:"why_bn"`
	Citation string `json:"citation"`

	RulesNeeded     []string `json:"rules_needed"`
	AllergenGroups  []string `json:"approve_allergen_groups"`
	CrossReactions  []string `json:"approve_cross_reactions"`
	UndeterminedFor string   `json:"undetermined_generic"`

	Patient  goldenPatient `json:"patient"`
	Proposed []goldenItem  `json:"proposed"`

	Expect goldenExpect `json:"expect"`
	Blind  goldenBlind  `json:"blind"`
}

type goldenPatient struct {
	AgeYears  *float64 `json:"age_years"`
	Pregnancy string   `json:"pregnancy"`
	EGFR      *float64 `json:"egfr"`
	// EGFRDaysAgo is how old that result is at the instant of the check (CP79). Absent means
	// **taken today**, which is what every scenario written before CP79 meant by "egfr: 88" —
	// so adding the field changed no existing scenario's meaning while making the one thing
	// CP79 turns on, the age of the result, something a scenario can state.
	EGFRDaysAgo *int   `json:"egfr_days_ago"`
	Hepatic     string `json:"hepatic"`
	// The three that carry the nil-versus-empty contract. A JSON `null` is "nobody asked";
	// `[]` is "asked, and there were none". Pointers to slices so the distinction survives
	// decoding, which a bare slice would lose the moment anybody wrote `[]string{}` by habit.
	Diagnoses *[]string     `json:"diagnoses"`
	Allergies *[]string     `json:"allergies"`
	Current   *[]goldenItem `json:"current_medicines"`
}

type goldenItem struct {
	Ref       string   `json:"ref"`
	Generic   string   `json:"generic"`
	Label     string   `json:"label"`
	DailyDose *float64 `json:"daily_dose"`
	DoseUnit  string   `json:"dose_unit"`
}

type goldenExpect struct {
	Verdict          string            `json:"verdict"`
	VerdictMustNotBe []string          `json:"verdict_must_not_be"`
	MustFire         []goldenFiring    `json:"must_fire"`
	MustNotFire      []string          `json:"must_not_fire"`
	Coverage         map[string]string `json:"coverage"`
	UncoveredCount   *int              `json:"uncovered_count"`
	MentionMust      string            `json:"message_must_mention"`
	UnderMS          int               `json:"under_milliseconds"`
}

type goldenFiring struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	On       string `json:"on"`
	// Outcome defaults to FIRES. A scenario whose expected finding is itself a cannot-verify —
	// a unit mismatch, an unapproved cross-reactivity map — says so.
	Outcome string `json:"outcome"`
}

type goldenBlind struct {
	Remove        string   `json:"remove"`
	ExpectVerdict string   `json:"expect_verdict"`
	CannotVerify  []string `json:"expect_cannot_verify"`
	WhyEN         string   `json:"why_en"`
}

func loadGoldenSuite(t *testing.T) goldenSuite {
	t.Helper()
	raw, err := os.ReadFile(goldenSuitePath)
	if err != nil {
		t.Fatalf("the golden suite is the deliverable; it must be readable: %v", err)
	}
	var suite goldenSuite
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatalf("%s does not parse: %v", goldenSuitePath, err)
	}
	if suite.Format != 1 {
		t.Fatalf("%s is format %d; this build reads format 1", goldenSuitePath, suite.Format)
	}
	return suite
}

// ---------------------------------------------------------------------------
// The harness
// ---------------------------------------------------------------------------

type goldenRun struct {
	*testsupport.DB
	store    *medsafety.Store
	engine   *medsafety.Engine
	facility uuid.UUID
	doctor   uuid.UUID
}

func newGoldenRun(t *testing.T) *goldenRun {
	t.Helper()
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	catalogue := formulary.NewStore(pool)
	store := medsafety.NewStore(pool, catalogue)
	run := &goldenRun{DB: base, store: store, engine: medsafety.NewEngine(store, catalogue)}
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&run.facility); err != nil {
		t.Fatal(err)
	}
	if err := base.SQL.QueryRow(`
		INSERT INTO core.app_user (facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, 'DOC01', 'Nahid', 'নাহিদ', 'active') RETURNING id`,
		run.facility).Scan(&run.doctor); err != nil {
		t.Fatal(err)
	}
	return run
}

// approve puts the physician's name on the rules a scenario needs, **inside this test's own
// throwaway database**.
//
// Through `Store.Publish`, which is the path a physician's Publish button takes, rather than an
// UPDATE: a suite that approved rules by writing the columns directly would pass while the real
// approval path was broken, and would not notice.
func (r *goldenRun) approve(t *testing.T, codes []string, at time.Time) {
	t.Helper()
	ctx := context.Background()

	wanted := map[string]bool{}
	all := false
	for _, code := range codes {
		if code == "ALL" {
			all = true
		}
		wanted[code] = true
	}
	if len(wanted) == 0 {
		return
	}

	rows, err := r.SQL.Query(`
		SELECT r.code, v.id FROM core.medication_rule r
		  JOIN core.medication_rule_version v ON v.rule_id = r.id
		 WHERE r.facility_id = $1 AND v.status = 'DRAFT'
		 ORDER BY r.code`, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	type pending struct {
		code string
		id   uuid.UUID
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.code, &p.id); err != nil {
			t.Fatal(err)
		}
		if all || wanted[p.code] {
			todo = append(todo, p)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	found := map[string]bool{}
	for _, p := range todo {
		if _, err := r.store.Publish(ctx, r.facility, p.id, r.doctor, at); err != nil {
			t.Fatalf("approving %s: %v", p.code, err)
		}
		found[p.code] = true
	}
	if !all {
		for code := range wanted {
			if !found[code] {
				// A scenario naming a rule that does not exist would otherwise pass its
				// unapproved run, pass its approved run by firing nothing, and assert
				// nothing at all.
				t.Fatalf("this scenario needs rule %s and the library has no draft of it; "+
					"either the rule was renamed or the scenario names one that was never written",
					code)
			}
		}
	}
}

// approveAllergens approves the groups and cross-reactions a scenario names.
//
// Separate from the rules because the cross-reactivity map is separately unapproved, and because
// the scenario that proves an unapproved map produces a cannot-verify needs the *group* approved
// and the *reaction* not.
func (r *goldenRun) approveAllergens(t *testing.T, groups, reactions []string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	for _, code := range groups {
		if err := r.store.ApproveAllergenGroup(ctx, code, r.doctor, at); err != nil {
			t.Fatalf("approving allergen group %s: %v", code, err)
		}
	}
	for _, pair := range reactions {
		parts := strings.SplitN(pair, "→", 2)
		if len(parts) != 2 {
			t.Fatalf("%q is not a cross-reaction; write it as FROM→TO", pair)
		}
		var id uuid.UUID
		if err := r.SQL.QueryRow(`
			SELECT id FROM core.allergen_cross_reaction
			 WHERE from_group = $1 AND to_group = $2`,
			strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])).Scan(&id); err != nil {
			t.Fatalf("finding cross-reaction %s: %v", pair, err)
		}
		if err := r.store.ApproveCrossReaction(ctx, id, r.doctor, at); err != nil {
			t.Fatalf("approving cross-reaction %s: %v", pair, err)
		}
	}
}

// undetermine takes a medicine's molecules away, for the scenario that proves an undetermined
// composition fails closed.
//
// Component rows are not deletable by the application role (invariant 118), so this flips the
// status — which is the state a generic arrives in from an import or the admin screen, and
// therefore the state the fail-closed path actually has to handle.
func (r *goldenRun) undetermine(t *testing.T, generic string) {
	t.Helper()
	res, err := r.SQL.Exec(`
		UPDATE core.generic
		   SET components_status = 'UNDETERMINED', components_source = '',
		       components_determined_at = NULL
		 WHERE lower(name) = lower($1)`, generic)
	if err != nil {
		t.Fatalf("making %s undetermined: %v", generic, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("no generic called %q to make undetermined", generic)
	}
}

// picture turns a scenario's patient into what the engine takes, preserving nil-versus-empty.
//
// Takes the instant of the check because CP79 made the *age* of an eGFR load-bearing: a result
// with no date against it is never treated as current, so a suite that supplied the value alone
// would put every scenario permanently into the stale branch.
func (p goldenPatient) picture(at time.Time) medsafety.Picture {
	out := medsafety.Picture{
		AgeYears: p.AgeYears, Pregnancy: p.Pregnancy,
		EGFR: p.EGFR, Hepatic: p.Hepatic,
	}
	if p.EGFR != nil {
		days := 0
		if p.EGFRDaysAgo != nil {
			days = *p.EGFRDaysAgo
		}
		when := at.AddDate(0, 0, -days)
		out.EGFRAsOf = &when
	}
	if p.Diagnoses != nil {
		out.Diagnoses = append([]string{}, *p.Diagnoses...)
	}
	if p.Allergies != nil {
		out.AllergenGroups = append([]string{}, *p.Allergies...)
	}
	if p.Current != nil {
		out.Current = itemsOf(*p.Current)
	}
	return out
}

func itemsOf(in []goldenItem) []medsafety.Item {
	out := make([]medsafety.Item, 0, len(in))
	for i, item := range in {
		ref := item.Ref
		if ref == "" {
			ref = fmt.Sprintf("c%d", i+1)
		}
		out = append(out, medsafety.Item{
			Ref: ref, Generic: item.Generic, Label: item.Label,
			DailyDose: item.DailyDose, DoseUnit: item.DoseUnit,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// The suite
// ---------------------------------------------------------------------------

func TestTheGoldenSuite(t *testing.T) {
	suite := loadGoldenSuite(t)
	if len(suite.Scenarios) < 24 {
		t.Fatalf("the suite has %d scenarios; the brief asks for the four named ones and at "+
			"least twenty more", len(suite.Scenarios))
	}

	for _, scenario := range suite.Scenarios {
		t.Run(scenario.ID, func(t *testing.T) {
			t.Parallel()
			runGoldenScenario(t, scenario)
		})
	}
}

func runGoldenScenario(t *testing.T, s goldenScenario) {
	t.Helper()
	run := newGoldenRun(t)
	ctx := context.Background()
	at := time.Now().UTC()

	proposed := itemsOf(s.Proposed)
	request := func(p medsafety.Picture, items []medsafety.Item, when time.Time) medsafety.Request {
		return medsafety.Request{
			FacilityID: run.facility, Proposed: items, Picture: p, At: when,
		}
	}

	// ---- 1. UNAPPROVED -------------------------------------------------
	//
	// The false-negative proof, per scenario. Nothing is approved, so the engine is blind to
	// exactly the rule that should catch this scenario, and the expected finding must be absent.
	// An assertion that still passes here was never measuring the rule.
	if len(s.RulesNeeded) > 0 {
		before, err := run.engine.Check(ctx, request(s.Patient.picture(at), proposed, at))
		if err != nil {
			t.Fatalf("the unapproved run failed: %v", err)
		}
		for _, want := range s.Expect.MustFire {
			if isEngineFinding(want.Rule) {
				// COVERAGE-UNKNOWN-DRUG and its siblings are the engine's own and do not
				// depend on an approval, so their absence here would prove nothing.
				continue
			}
			if found, _ := findingFor(before, want.Rule, want.On); found {
				t.Errorf("BLINDING FAILED: %s fired on line %q with nothing approved.\n"+
					"This scenario cannot prove anything: whatever produced the finding, it "+
					"was not the physician approving the rule.", want.Rule, want.On)
			}
		}
		if before.Verdict != medsafety.VerdictNoRulesApproved &&
			before.Verdict != medsafety.VerdictNothingProposed &&
			before.Verdict != medsafety.VerdictCannotVerify {
			t.Errorf("with nothing approved the verdict was %s; an unapproved library must "+
				"never produce a verdict that reads like a result", before.Verdict)
		}
	}

	// ---- approve what this scenario needs, in this test's own database ----
	run.approve(t, s.RulesNeeded, at)
	run.approveAllergens(t, s.AllergenGroups, s.CrossReactions, at)
	if s.UndeterminedFor != "" {
		run.undetermine(t, s.UndeterminedFor)
	}

	// ---- 2. APPROVED ---------------------------------------------------
	started := time.Now()
	result, err := run.engine.Check(ctx, request(s.Patient.picture(at), proposed, at))
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("the check failed: %v", err)
	}

	if s.Expect.Verdict != "" && string(result.Verdict) != s.Expect.Verdict {
		t.Errorf("verdict is %s, want %s\nwhy this scenario exists: %s\nsummary: %s",
			result.Verdict, s.Expect.Verdict, s.WhyEN, result.SummaryEN)
	}
	for _, forbidden := range s.Expect.VerdictMustNotBe {
		if string(result.Verdict) == forbidden {
			t.Errorf("verdict is %s, which this scenario says it must never be.\n%s",
				forbidden, s.WhyEN)
		}
	}
	for _, want := range s.Expect.MustFire {
		found, got := findingFor(result, want.Rule, want.On)
		if !found {
			t.Errorf("%s did not fire on line %q.\nwhy it should have: %s\ncitation: %s\n"+
				"what did fire: %s", want.Rule, want.On, s.WhyEN, s.Citation, describe(result))
			continue
		}
		if want.Severity != "" && string(got.Severity) != want.Severity {
			t.Errorf("%s fired at %s, want %s", want.Rule, got.Severity, want.Severity)
		}
		outcome := want.Outcome
		if outcome == "" {
			outcome = string(medsafety.OutcomeFires)
		}
		if string(got.Outcome) != outcome {
			t.Errorf("%s produced outcome %s, want %s", want.Rule, got.Outcome, outcome)
		}
		if strings.TrimSpace(got.MessageBN) == "" {
			t.Errorf("%s has no Bengali message; every finding is bilingual", want.Rule)
		}
		if strings.TrimSpace(got.Source) == "" {
			t.Errorf("%s cites nothing; a finding without a citation cannot be argued with",
				want.Rule)
		}
	}
	for _, forbidden := range s.Expect.MustNotFire {
		if found, _ := findingFor(result, forbidden, ""); found {
			t.Errorf("%s fired and this scenario says it must not.\n%s", forbidden, s.WhyEN)
		}
	}
	for ref, wantState := range s.Expect.Coverage {
		entry, ok := coverageFor(result, ref)
		if !ok {
			t.Fatalf("no coverage entry for line %q; every proposed drug gets one", ref)
		}
		if string(entry.State) != wantState {
			t.Errorf("line %q is %s, want %s (%s)", ref, entry.State, wantState, entry.NoteEN)
		}
		if strings.TrimSpace(entry.NoteBN) == "" {
			t.Errorf("line %q has no Bengali coverage note", ref)
		}
	}
	if s.Expect.UncoveredCount != nil && result.UncoveredCount != *s.Expect.UncoveredCount {
		t.Errorf("uncovered_count is %d, want %d", result.UncoveredCount, *s.Expect.UncoveredCount)
	}
	if s.Expect.MentionMust != "" {
		joined := strings.ToLower(describeSteps(result))
		if !strings.Contains(joined, strings.ToLower(s.Expect.MentionMust)) {
			t.Errorf("no finding's working names %q, so the physician cannot check the claim "+
				"against the box.\nworking: %s", s.Expect.MentionMust, describeSteps(result))
		}
	}
	if s.Expect.UnderMS > 0 {
		t.Logf("evaluation took %s over %d proposed items and %d live rules",
			elapsed, len(proposed), result.RulesLive)
		if elapsed > time.Duration(s.Expect.UnderMS)*time.Millisecond {
			t.Errorf("evaluation took %s, which is over the %dms this scenario allows",
				elapsed, s.Expect.UnderMS)
		}
	}

	// Every scenario, not only the ones that ask: the result must never be readable as safe.
	assertNeverReadsAsSafe(t, result, len(proposed))

	// ---- 3. REPRODUCED -------------------------------------------------
	//
	// Criterion 5. Same instant, same answer, same versions — including the version ids, which
	// is the claim the audit entry makes about a check somebody questions months later.
	again, err := run.engine.Check(ctx, request(s.Patient.picture(at), proposed, at))
	if err != nil {
		t.Fatalf("the reproduction failed: %v", err)
	}
	if again.Verdict != result.Verdict {
		t.Errorf("re-running the same check against the same instant gave %s, first gave %s",
			again.Verdict, result.Verdict)
	}
	if a, b := versionList(result), versionList(again); a != b {
		t.Errorf("the same check evaluated different rule versions:\n  first: %s\n  again: %s", a, b)
	}

	// ---- 4. BLINDED ----------------------------------------------------
	//
	// Last, because blinding a scenario's components is done in the database rather than in
	// the request — that is the honest version, since UNDETERMINED is the state a generic
	// actually arrives in from an import — and a database the blinding has changed is not one
	// the reproduction can be run against afterwards.
	if s.Blind.Remove != "" && s.Blind.Remove != "nothing" {
		blinded := blind(t, run, s, proposed, at)
		after, err := run.engine.Check(ctx, request(blinded.picture, blinded.items, at))
		if err != nil {
			t.Fatalf("the blinded run failed: %v", err)
		}
		if s.Blind.ExpectVerdict != "" && s.Blind.ExpectVerdict != "ANY" &&
			string(after.Verdict) != s.Blind.ExpectVerdict {
			t.Errorf("BLINDING: with %s taken away the verdict is %s, want %s.\n%s\nsummary: %s",
				s.Blind.Remove, after.Verdict, s.Blind.ExpectVerdict, s.Blind.WhyEN,
				after.SummaryEN)
		}
		for _, code := range s.Blind.CannotVerify {
			found, got := findingFor(after, code, "")
			if !found {
				t.Errorf("BLINDING: with %s taken away, %s said nothing at all. Missing data "+
					"must produce an explicit 'cannot verify', never silence.\n%s",
					s.Blind.Remove, code, describe(after))
				continue
			}
			if got.Outcome != medsafety.OutcomeCannotVerify {
				t.Errorf("BLINDING: with %s taken away, %s produced %s rather than "+
					"CANNOT_VERIFY", s.Blind.Remove, code, got.Outcome)
			}
			if len(got.Missing) == 0 {
				t.Errorf("BLINDING: %s cannot verify but names no missing datum. "+
					"\"Safety cannot be verified\" with no noun in it is a message people "+
					"learn to click past.", code)
			}
		}
		assertNeverReadsAsSafe(t, after, len(blinded.items))
	}

}

// blinded is a scenario with one fact taken away.
type blinded struct {
	picture medsafety.Picture
	items   []medsafety.Item
}

// blind removes exactly one thing, and is where "prove it by removal" is implemented.
func blind(t *testing.T, run *goldenRun, s goldenScenario, items []medsafety.Item, at time.Time) blinded {
	t.Helper()
	p := s.Patient.picture(at)
	out := blinded{picture: p, items: items}
	switch s.Blind.Remove {
	case "egfr":
		// The date goes with the value. An eGFR date with no eGFR under it would be a shape
		// the bridge cannot produce and a picture no patient has.
		out.picture.EGFR = nil
		out.picture.EGFRAsOf = nil
	case "age":
		out.picture.AgeYears = nil
	case "pregnancy":
		out.picture.Pregnancy = ""
	case "hepatic":
		out.picture.Hepatic = ""
	case "diagnoses":
		out.picture.Diagnoses = nil
	case "allergies":
		out.picture.AllergenGroups = nil
	case "current_medicines":
		out.picture.Current = nil
	case "daily_dose":
		stripped := make([]medsafety.Item, len(items))
		copy(stripped, items)
		for i := range stripped {
			stripped[i].DailyDose = nil
		}
		out.items = stripped
	case "components":
		// The molecules of every medicine this scenario proposes. The engine reads them from
		// the formulary, so the blinding is in the database rather than in the request — which
		// is the honest version: this is the state a generic is in after an import.
		for _, item := range items {
			if item.Generic != "" {
				run.undetermine(t, item.Generic)
			}
		}
	default:
		t.Fatalf("%q is not something a scenario can blind. Write one of: egfr, age, "+
			"pregnancy, hepatic, diagnoses, allergies, current_medicines, daily_dose, "+
			"components, nothing.", s.Blind.Remove)
	}
	return out
}

// assertNeverReadsAsSafe is criterion 3, applied to every result the suite produces.
//
// Not "the coverage field is correct" — that is asserted per scenario — but the stronger
// property the whole checkpoint rests on: **there is no state in which this response can be read
// as a clean bill of health while something was not checked.** Delete the coverage logic and
// this goes red across the whole suite.
func assertNeverReadsAsSafe(t *testing.T, r medsafety.Result, proposed int) {
	t.Helper()

	if len(r.Coverage) != proposed {
		t.Errorf("%d drugs were proposed and %d coverage entries came back; a drug with no "+
			"coverage entry is a drug the screen has nothing to say about",
			proposed, len(r.Coverage))
	}
	uncovered := 0
	for _, entry := range r.Coverage {
		if entry.State == medsafety.CoverageNone {
			uncovered++
		}
		if entry.State == medsafety.CoverageClear && entry.RulesConsidered == 0 {
			t.Errorf("line %q is reported clear having been checked against no rules at all; "+
				"that is the exact confusion §7.2 calls worse than no engine", entry.Ref)
		}
	}
	if uncovered != r.UncoveredCount {
		t.Errorf("uncovered_count says %d and the coverage list has %d; a client reading one "+
			"and a client reading the other would draw different screens",
			r.UncoveredCount, uncovered)
	}
	if r.Verdict == medsafety.VerdictClearWithinCoverage && uncovered > 0 {
		t.Errorf("the verdict is CLEAR_WITHIN_COVERAGE while %d drug(s) were covered by no "+
			"rule at all", uncovered)
	}
	if r.RulesLive == 0 && proposed > 0 && r.Verdict != medsafety.VerdictNoRulesApproved {
		t.Errorf("no rule was live and the verdict is %s; nothing was checked and the verdict "+
			"has to say so", r.Verdict)
	}
	// The word itself. A future refactor that renamed the clear verdict to something
	// reassuring would pass every other assertion in this file.
	for _, word := range []string{"\"safe\"", " is safe", "SAFE"} {
		if strings.Contains(r.SummaryEN, word) {
			t.Errorf("the summary says %q: %s", word, r.SummaryEN)
		}
	}
	if strings.TrimSpace(r.SummaryEN) == "" || strings.TrimSpace(r.SummaryBN) == "" {
		t.Errorf("a result with no summary in one of the two languages: %q / %q",
			r.SummaryEN, r.SummaryBN)
	}
}

// ---------------------------------------------------------------------------
// Reading a result
// ---------------------------------------------------------------------------

func isEngineFinding(code string) bool {
	switch code {
	case medsafety.CodeUnknownDrug, medsafety.CodeCrossReactivityUnapproved,
		medsafety.CodeUnclassifiedAllergy, medsafety.CodeAllergyStatusUnknown,
		// CP79's three. Like the coverage findings, each is a statement about what the check
		// could not do rather than a claim a physician authored, so none of them can be
		// approved and their presence in the unapproved pass proves nothing either way.
		medsafety.CodeRenalStale, medsafety.CodeRenalAbsent, medsafety.CodeRenalUnclassified:
		return true
	}
	return false
}

func findingFor(r medsafety.Result, rule, on string) (bool, medsafety.Finding) {
	for _, f := range r.Findings {
		if f.RuleCode != rule {
			continue
		}
		if on != "" && f.SubjectRef != on {
			continue
		}
		return true, f
	}
	return false, medsafety.Finding{}
}

func coverageFor(r medsafety.Result, ref string) (medsafety.Coverage, bool) {
	for _, entry := range r.Coverage {
		if entry.Ref == ref {
			return entry, true
		}
	}
	return medsafety.Coverage{}, false
}

func describe(r medsafety.Result) string {
	if len(r.Findings) == 0 {
		return "(nothing; verdict " + string(r.Verdict) + ")"
	}
	var parts []string
	for _, f := range r.Findings {
		parts = append(parts, fmt.Sprintf("%s/%s/%s on %q",
			f.RuleCode, f.Severity, f.Outcome, f.SubjectRef))
	}
	return strings.Join(parts, ", ")
}

func describeSteps(r medsafety.Result) string {
	var parts []string
	for _, f := range r.Findings {
		for _, step := range f.Steps {
			parts = append(parts, step.BecauseEN)
		}
	}
	return strings.Join(parts, " | ")
}

func versionList(r medsafety.Result) string {
	out := make([]string, 0, len(r.Evaluated))
	for _, v := range r.Evaluated {
		out = append(out, fmt.Sprintf("%s@%d/%s", v.Rule, v.Version, v.VersionID))
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}
