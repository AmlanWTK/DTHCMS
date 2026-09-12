package medsafety_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
)

// The guarantees the golden suite cannot state as a scenario (CP78).
//
// A scenario says "this picture produces this finding". These say things about the *shape* of
// every answer — that a drug with no rules can never be read as safe, that the components model
// fails closed, that a check names the versions it ran, that nothing here reaches a log. Each is
// written so that deleting the code it guards turns it red, which is the property this project's
// recurring failure is about: five times a check has passed while the thing it checked was
// broken, and three times the check itself had the hole it existed to catch.

// ---------------------------------------------------------------------------
// Coverage honesty — criterion 3
// ---------------------------------------------------------------------------

func TestADrugWithNoRulesIsReportedUncoveredAndNeverAsSafe(t *testing.T) {
	// **Delete the coverage logic and this test goes red**, which is the whole reason it is
	// written against three separate properties rather than one: the state, the count, and the
	// verdict. A change that fixed one to hide a break in another would still fail here.
	run := newGoldenRun(t)
	ctx := context.Background()
	at := time.Now().UTC()
	run.approve(t, []string{"MET-RENAL-30"}, at)

	result, err := run.engine.Check(ctx, medsafety.Request{
		FacilityID: run.facility, At: at,
		Picture: medsafety.Picture{EGFR: f(88), Diagnoses: []string{}, AllergenGroups: []string{}},
		Proposed: []medsafety.Item{
			{Ref: "1", Generic: "Metformin hydrochloride", Label: "Comet 500"},
			{Ref: "2", Generic: "Levothyroxine sodium", Label: "Thyrox 50"},
			{Ref: "3", Generic: "Ciprofloxacin", Label: "Ciprocin 500"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	states := map[string]medsafety.CoverageState{}
	for _, entry := range result.Coverage {
		states[entry.Ref] = entry.State
	}
	// Metformin: a live rule was about it and did not fire.
	if states["1"] != medsafety.CoverageClear {
		t.Errorf("metformin at eGFR 88 is %s, want COVERED_CLEAR", states["1"])
	}
	// Levothyroxine: in the formulary, but no approved rule is about it. **Not the same thing
	// as clear**, and this is the assertion that most easily rots into one.
	if states["2"] != medsafety.CoverageNone {
		t.Errorf("levothyroxine with no approved rule about it is %s, want NOT_COVERED.\n"+
			"A drug nothing checked and a drug everything passed must never render the same.",
			states["2"])
	}
	// Ciprofloxacin: not in the formulary at all.
	if states["3"] != medsafety.CoverageNone {
		t.Errorf("a drug this formulary does not hold is %s, want NOT_COVERED", states["3"])
	}
	if result.UncoveredCount != 2 {
		t.Errorf("uncovered_count is %d, want 2", result.UncoveredCount)
	}
	if result.Verdict == medsafety.VerdictClearWithinCoverage {
		t.Errorf("the verdict is CLEAR_WITHIN_COVERAGE while two of three drugs were checked " +
			"against nothing")
	}
	// The unknown drug must also be a finding, not only a coverage row: the coverage list is
	// what a careful reader consults and the finding list is what a busy one reads.
	if found, got := findingFor(result, medsafety.CodeUnknownDrug, "3"); !found {
		t.Errorf("no finding about the unidentified drug; coverage alone is not enough")
	} else if got.Outcome != medsafety.OutcomeCannotVerify {
		t.Errorf("the unidentified drug produced %s, want CANNOT_VERIFY", got.Outcome)
	}
	// And no field anywhere says the word.
	assertNeverReadsAsSafe(t, result, 3)
}

func TestACoveredDrugSaysHowManyRulesLookedAtIt(t *testing.T) {
	// The number is what lets a screen say "checked against 4 rules" instead of drawing a green
	// tick the reader has to trust. Without it, CoverageClear is indistinguishable from a
	// guess — which is exactly how an incomplete rule set comes to be presented as complete.
	run := newGoldenRun(t)
	at := time.Now().UTC()
	run.approve(t, []string{"ALL"}, at)

	result, err := run.engine.Check(context.Background(), medsafety.Request{
		FacilityID: run.facility, At: at,
		Picture: medsafety.Picture{
			AgeYears: f(55), Pregnancy: "NOT_PREGNANT", EGFR: f(90), Hepatic: "NONE",
			Diagnoses: []string{}, AllergenGroups: []string{}, Current: []medsafety.Item{},
		},
		Proposed: []medsafety.Item{{
			Ref: "1", Generic: "Metformin hydrochloride", Label: "Comet",
			DailyDose: f(1000), DoseUnit: "mg",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := coverageFor(result, "1")
	if !ok {
		t.Fatal("no coverage entry")
	}
	if entry.RulesConsidered < 4 {
		t.Errorf("metformin was considered by %d rules; the seeded library has renal, "+
			"hepatic, contraindication, duplicate and max-dose rules about it",
			entry.RulesConsidered)
	}
	if !strings.Contains(entry.NoteEN, "Only those rules were checked") {
		t.Errorf("the clear note does not say what was *not* checked: %s", entry.NoteEN)
	}
}

// ---------------------------------------------------------------------------
// Fail-closed, proved by removal
// ---------------------------------------------------------------------------

func TestEveryMissingDatumProducesCannotVerifyRatherThanSilence(t *testing.T) {
	// Criterion 2, over every datum at once rather than one scenario at a time. A table so that
	// adding a datum to the model means adding a row here — and so that a regression in one of
	// them cannot hide behind the others passing.
	run := newGoldenRun(t)
	at := time.Now().UTC()
	run.approve(t, []string{"ALL"}, at)

	full := func() medsafety.Picture {
		return medsafety.Picture{
			AgeYears: f(60), Pregnancy: "NOT_PREGNANT", EGFR: f(80), Hepatic: "NONE",
			Diagnoses: []string{}, AllergenGroups: []string{}, Current: []medsafety.Item{},
		}
	}
	items := []medsafety.Item{
		{Ref: "1", Generic: "Metformin hydrochloride", Label: "Comet", DailyDose: f(1000), DoseUnit: "mg"},
		{Ref: "2", Generic: "Pioglitazone", Label: "Pioglit", DailyDose: f(15), DoseUnit: "mg"},
		{Ref: "3", Generic: "Ramipril", Label: "Ramipril", DailyDose: f(5), DoseUnit: "mg"},
	}

	for _, tc := range []struct {
		datum  string
		remove func(*medsafety.Picture)
		expect medsafety.Datum
	}{
		{"eGFR", func(p *medsafety.Picture) { p.EGFR = nil }, medsafety.DatumEGFR},
		{"age", func(p *medsafety.Picture) { p.AgeYears = nil }, medsafety.DatumAge},
		{"pregnancy", func(p *medsafety.Picture) { p.Pregnancy = "" }, medsafety.DatumPregnancy},
		{"liver function", func(p *medsafety.Picture) { p.Hepatic = "" }, medsafety.DatumHepatic},
		{"the diagnosis list", func(p *medsafety.Picture) { p.Diagnoses = nil }, medsafety.DatumDiagnoses},
		{"the allergy list", func(p *medsafety.Picture) { p.AllergenGroups = nil }, medsafety.DatumAllergies},
		{"the medication list", func(p *medsafety.Picture) { p.Current = nil }, medsafety.DatumCurrentMeds},
	} {
		t.Run(tc.datum, func(t *testing.T) {
			picture := full()
			tc.remove(&picture)
			result, err := run.engine.Check(context.Background(), medsafety.Request{
				FacilityID: run.facility, At: at, Picture: picture, Proposed: items,
			})
			if err != nil {
				t.Fatal(err)
			}
			named := false
			for _, finding := range result.Findings {
				if finding.Outcome != medsafety.OutcomeCannotVerify {
					continue
				}
				for _, missing := range finding.Missing {
					if missing == tc.expect {
						named = true
					}
				}
			}
			if !named {
				t.Errorf("with %s taken away, nothing said it could not verify anything for "+
					"want of %s. Missing data must produce an explicit finding, never "+
					"silence.\nwhat came back: %s", tc.datum, tc.expect, describe(result))
			}
			if result.Verdict == medsafety.VerdictClearWithinCoverage {
				t.Errorf("with %s taken away the verdict is CLEAR_WITHIN_COVERAGE", tc.datum)
			}
		})
	}
}

func TestADrugWhoseMoleculesNobodyWroteCannotBeClearedOfDuplication(t *testing.T) {
	// The components half of fail-closed, and the one the whole of migration 00058 exists for.
	// Before it, this pair of drugs produced "no duplicate" — which is the same pixel as "safe"
	// and is wrong.
	run := newGoldenRun(t)
	at := time.Now().UTC()
	run.approve(t, []string{"DUP-GENERIC"}, at)

	items := []medsafety.Item{
		{Ref: "1", Generic: "Sitagliptin + Metformin hydrochloride", Label: "Siglimet"},
		{Ref: "2", Generic: "Metformin hydrochloride", Label: "Comet"},
	}
	picture := medsafety.Picture{Diagnoses: []string{}, AllergenGroups: []string{},
		Current: []medsafety.Item{}}

	// With the molecules recorded: a duplicate, named by molecule.
	before, err := run.engine.Check(context.Background(), medsafety.Request{
		FacilityID: run.facility, At: at, Picture: picture, Proposed: items,
	})
	if err != nil {
		t.Fatal(err)
	}
	found, finding := findingFor(before, "DUP-GENERIC", "1")
	if !found || finding.Outcome != medsafety.OutcomeFires {
		t.Fatalf("Siglimet beside Comet is metformin twice and did not fire: %s", describe(before))
	}
	if !strings.Contains(strings.ToLower(describeSteps(before)), "metformin") {
		t.Errorf("the finding does not name the molecule, so the physician cannot check it "+
			"against the box: %s", describeSteps(before))
	}

	// With them taken away: cannot verify, never "no duplicate".
	run.undetermine(t, "Sitagliptin + Metformin hydrochloride")
	after, err := run.engine.Check(context.Background(), medsafety.Request{
		FacilityID: run.facility, At: at, Picture: picture, Proposed: items,
	})
	if err != nil {
		t.Fatal(err)
	}
	found, finding = findingFor(after, "DUP-GENERIC", "2")
	if !found {
		t.Fatalf("with Siglimet's molecules unrecorded, the duplicate check said nothing at "+
			"all. An unwritten molecule set intersects nothing, and 'intersects nothing' must "+
			"not be reported as 'is not a duplicate': %s", describe(after))
	}
	if finding.Outcome != medsafety.OutcomeCannotVerify {
		t.Errorf("the duplicate check produced %s, want CANNOT_VERIFY", finding.Outcome)
	}
	entry, _ := coverageFor(after, "1")
	if entry.ComponentsKnown {
		t.Errorf("the coverage entry still claims the molecules are known")
	}
}

func TestAnUnapprovedCrossReactivityMapIsACannotVerifyAndNotAPass(t *testing.T) {
	// The brief's decision, and it is the right one: the map is CP77 seed content nobody has
	// read, and silently not expanding an allergy looks exactly like a patient with no
	// cross-reactivity.
	run := newGoldenRun(t)
	at := time.Now().UTC()
	run.approve(t, []string{"ALL"}, at)

	request := medsafety.Request{
		FacilityID: run.facility, At: at,
		Picture: medsafety.Picture{
			AllergenGroups: []string{"PENICILLIN"}, Diagnoses: []string{},
			Current: []medsafety.Item{}, EGFR: f(90),
		},
		Proposed: []medsafety.Item{{Ref: "1", Generic: "Metformin hydrochloride", Label: "Comet"}},
	}

	before, err := run.engine.Check(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if found, got := findingFor(before, medsafety.CodeCrossReactivityUnapproved, ""); !found {
		t.Fatalf("an unapproved cross-reactivity mapping produced no finding: %s", describe(before))
	} else if got.Outcome != medsafety.OutcomeCannotVerify {
		t.Errorf("it produced %s, want CANNOT_VERIFY", got.Outcome)
	}
	if len(before.CrossReactivity.Unapproved) == 0 {
		t.Errorf("the report does not name the mappings it refused to use")
	}
	if len(before.CrossReactivity.Added) != 0 {
		t.Errorf("an unapproved mapping expanded the allergy anyway: %v",
			before.CrossReactivity.Added)
	}

	// Approve the groups and the mapping, and the expansion happens and the refusal stops.
	run.approveAllergens(t, []string{"PENICILLIN", "CEPHALOSPORIN", "CARBAPENEM"},
		[]string{"PENICILLIN→CEPHALOSPORIN"}, at)
	after, err := run.engine.Check(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	added := strings.Join(after.CrossReactivity.Added, ",")
	if !strings.Contains(added, "CEPHALOSPORIN") {
		t.Errorf("an approved mapping did not expand the allergy; added %q", added)
	}
	// This is what makes the first half of this test mean something: approving is what changes
	// the behaviour, and nothing else.
	if len(after.CrossReactivity.Unapproved) >= len(before.CrossReactivity.Unapproved) {
		t.Errorf("approving a mapping did not reduce the refusals: %v → %v",
			before.CrossReactivity.Unapproved, after.CrossReactivity.Unapproved)
	}
}

// ---------------------------------------------------------------------------
// Reproducibility — criterion 5
// ---------------------------------------------------------------------------

func TestACheckReproducesAgainstTheVersionsThatWereLiveAtTheTime(t *testing.T) {
	// The claim the audit entry makes. A check run in March must be re-runnable in December and
	// give March's answer, not December's — and a schema in which the live version is
	// `WHERE is_current` cannot do it however carefully the query is written.
	run := newGoldenRun(t)
	ctx := context.Background()
	first := time.Now().UTC().Add(-2 * time.Hour)
	run.approve(t, []string{"MET-RENAL-30"}, first)

	request := func(at time.Time) medsafety.Request {
		return medsafety.Request{
			FacilityID: run.facility, At: at,
			Picture:  medsafety.Picture{EGFR: f(25), Diagnoses: []string{}, AllergenGroups: []string{}},
			Proposed: []medsafety.Item{{Ref: "1", Generic: "Metformin hydrochloride", Label: "Comet"}},
		}
	}

	original, err := run.engine.Check(ctx, request(first.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if original.Verdict != medsafety.VerdictBlocked {
		t.Fatalf("metformin at eGFR 25 gave %s", original.Verdict)
	}
	if len(original.Evaluated) != 1 || original.Evaluated[0].Rule != "MET-RENAL-30" {
		t.Fatalf("the check does not name the version it ran: %+v", original.Evaluated)
	}

	// A second version of the same rule is published now, with a different threshold.
	ruleID, _ := run.seededRuleID(t, "MET-RENAL-30")
	second := time.Now().UTC()
	v2 := run.draftAndPublish(t, ruleID, second)

	// Today's check uses v2 ...
	today, err := run.engine.Check(ctx, request(second.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if len(today.Evaluated) != 1 || today.Evaluated[0].VersionID != v2 {
		t.Fatalf("a check today did not use the version published today: %+v", today.Evaluated)
	}

	// ... and the original instant still reproduces against v1, byte for byte.
	again, err := run.engine.Check(ctx, request(first.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if versionList(again) != versionList(original) {
		t.Errorf("the March check no longer reproduces:\n  then: %s\n  now:  %s",
			versionList(original), versionList(again))
	}
	if again.Verdict != original.Verdict {
		t.Errorf("the March check now gives %s, gave %s", again.Verdict, original.Verdict)
	}
	if again.Evaluated[0].VersionID == v2 {
		t.Errorf("reproducing a past check used a version published after it")
	}
}

func (r *goldenRun) seededRuleID(t *testing.T, code string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	var ruleID, versionID uuid.UUID
	if err := r.SQL.QueryRow(`
		SELECT r.id, v.id FROM core.medication_rule r
		  JOIN core.medication_rule_version v ON v.rule_id = r.id AND v.version = 1
		 WHERE r.facility_id = $1 AND r.code = $2`, r.facility, code).Scan(&ruleID, &versionID); err != nil {
		t.Fatalf("finding %s: %v", code, err)
	}
	return ruleID, versionID
}

// draftAndPublish writes a second version of a rule and makes it live, through the real store.
func (r *goldenRun) draftAndPublish(t *testing.T, ruleID uuid.UUID, at time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	draft, err := r.store.DraftVersion(ctx, r.facility, ruleID, r.doctor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.Publish(ctx, r.facility, draft.ID, r.doctor, at); err != nil {
		t.Fatal(err)
	}
	return draft.ID
}

// ---------------------------------------------------------------------------
// The route, the audit entry, and the logs
// ---------------------------------------------------------------------------

// stubFacts is a clinical picture a test can pose, standing in for the cmd/api bridge.
type stubFacts struct {
	age       *float64
	pregnancy string
	egfr      *float64
	diagnoses []string
	allergies []medsafety.ReportedAllergy
	current   []medsafety.Item
}

func (s stubFacts) Age(context.Context, uuid.UUID, uuid.UUID) (*float64, error) {
	return s.age, nil
}
func (s stubFacts) Pregnancy(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return s.pregnancy, nil
}
func (s stubFacts) Renal(context.Context, uuid.UUID, uuid.UUID) (*float64, *time.Time, error) {
	if s.egfr == nil {
		return nil, nil, nil
	}
	when := time.Now().UTC()
	return s.egfr, &when, nil
}
func (s stubFacts) Hepatic(context.Context, uuid.UUID, uuid.UUID) (string, error) { return "", nil }
func (s stubFacts) Diagnoses(context.Context, uuid.UUID, uuid.UUID) ([]string, error) {
	return s.diagnoses, nil
}
func (s stubFacts) Allergies(context.Context, uuid.UUID, uuid.UUID) ([]medsafety.ReportedAllergy, error) {
	return s.allergies, nil
}
func (s stubFacts) CurrentMedications(context.Context, uuid.UUID, uuid.UUID) ([]medsafety.Item, error) {
	return s.current, nil
}

type recordedRun struct{ runs []medsafety.SafetyCheckRun }

func (r *recordedRun) SafetyCheckRun(_ context.Context, run medsafety.SafetyCheckRun) error {
	r.runs = append(r.runs, run)
	return nil
}

func (r *goldenRun) serve(t *testing.T, facts medsafety.PatientFacts,
	auditor medsafety.CheckAuditor, logs *bytes.Buffer) *httptest.Server {

	t.Helper()
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	handlers := medsafety.NewCheckHandlers(medsafety.CheckHandlersConfig{
		Engine: r.engine, Facts: facts, Audit: auditor,
		Clock: clock.Real{}, Logger: logger,
	})
	held := []string{medsafety.PermCheck}
	who := staff{facility: r.facility, user: r.doctor, code: "DOC01", permissions: &held}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 20, RequestTimeout: 30 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(rt chi.Router) {
			rt.Route("/patients", func(p chi.Router) { handlers.MountPatient(p) })
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server
}

func TestTheSafetyCheckRouteAnswersAndRecordsTheVersionsItRan(t *testing.T) {
	run := newGoldenRun(t)
	at := time.Now().UTC()
	run.approve(t, []string{"MET-RENAL-30"}, at)

	auditor := &recordedRun{}
	logs := &bytes.Buffer{}
	server := run.serve(t, stubFacts{
		age: f(66), pregnancy: "NOT_PREGNANT", egfr: f(25),
		diagnoses: []string{"E11.9"}, allergies: []medsafety.ReportedAllergy{},
		current: []medsafety.Item{},
	}, auditor, logs)

	patient := uuid.New()
	body, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"ref": "1", "generic": "Metformin hydrochloride", "label": "Comet 500 mg"},
		},
	})
	req, _ := http.NewRequest(http.MethodPost,
		server.URL+"/v1/patients/"+patient.String()+"/safety-check", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("Idempotency-Key", uuid.New().String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := readAll(resp)
		t.Fatalf("the route answered %d: %s", resp.StatusCode, raw)
	}
	var decoded medsafety.Result
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Verdict != medsafety.VerdictBlocked {
		t.Errorf("the route gave %s, want BLOCKED: %s", decoded.Verdict, decoded.SummaryEN)
	}
	if strings.TrimSpace(decoded.SummaryBN) == "" {
		t.Errorf("the response has no Bengali summary")
	}

	// The audit entry, and criterion 5's content in it.
	if len(auditor.runs) != 1 {
		t.Fatalf("%d audit entries, want 1", len(auditor.runs))
	}
	entry := auditor.runs[0]
	if entry.PatientID != patient {
		t.Errorf("the audit entry names the wrong patient")
	}
	if len(entry.Evaluated) != 1 || entry.Evaluated[0].Rule != "MET-RENAL-30" {
		t.Errorf("the audit entry does not name the rule versions that ran: %+v", entry.Evaluated)
	}
	if entry.Evaluated[0].VersionID == uuid.Nil {
		t.Errorf("the audit entry names a rule but not which version of it — which is the " +
			"only part that survives somebody publishing a v3")
	}
	if entry.BlockCount != 1 || entry.Verdict != medsafety.VerdictBlocked {
		t.Errorf("the audit entry does not record what the check concluded: %+v", entry)
	}
}

func TestTheSafetyCheckLogsNothingAboutThePatient(t *testing.T) {
	// The same test CP77 wrote for its sandbox, one layer up and with more at stake: this
	// handler sees a real patient's eGFR, diagnoses and allergies. A log line with any of it in
	// it is a clinical record in a file with different access controls.
	run := newGoldenRun(t)
	at := time.Now().UTC()
	run.approve(t, []string{"ALL"}, at)

	logs := &bytes.Buffer{}
	server := run.serve(t, stubFacts{
		age: f(66), pregnancy: "PREGNANT", egfr: f(23),
		diagnoses: []string{"I50.9", "C73"},
		allergies: []medsafety.ReportedAllergy{{Display: "Penicillin", Said: "penicillin injection"}},
		current:   []medsafety.Item{{Ref: "c1", Generic: "Ramipril", Label: "Ramipril 5 mg"}},
	}, &recordedRun{}, logs)

	body, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"ref": "1", "generic": "Metformin hydrochloride", "label": "Comet 500 mg"},
			{"ref": "2", "generic": "Semaglutide", "label": "Ozempic 0.5 mg"},
		},
	})
	req, _ := http.NewRequest(http.MethodPost,
		server.URL+"/v1/patients/"+uuid.New().String()+"/safety-check", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("Idempotency-Key", uuid.New().String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	written := logs.String()
	for _, secret := range []string{
		"I50.9", "C73", "Penicillin", "penicillin", "Comet", "Ozempic", "Semaglutide",
		"Metformin", "Ramipril", "PREGNANT",
	} {
		if strings.Contains(written, secret) {
			t.Errorf("the logs carry %q, which is this patient's clinical picture:\n%s",
				secret, written)
		}
	}
	// The two numbers are matched on word boundaries, and the reason is a defect this check
	// had: a bare `strings.Contains(written, "66")` matched the "466b" inside a randomly
	// generated patient UUID and the "20016" of a byte count, so the test failed on roughly one
	// run in twenty for a reason that had nothing to do with logging. A check that fails at
	// random is a check people re-run until it is green, which is the same as not having it.
	for _, number := range []string{"23", "66"} {
		if regexp.MustCompile(`\b` + number + `\b`).MatchString(written) {
			t.Errorf("the logs carry the number %s, which is this patient's clinical picture:\n%s",
				number, written)
		}
	}
}

func readAll(resp *http.Response) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			return sb.String(), nil
		}
	}
}

func f(v float64) *float64 { return &v }
