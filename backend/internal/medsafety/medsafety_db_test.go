package medsafety_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
)

// The rule library against the real database and the real forty seeded rules (CP77).
//
// Four acceptance criteria:
//
//	1. a physician can author, test and publish a rule without a developer;
//	2. every rule has a bilingual message and a severity;
//	3. rule versions are retained and historical checks are reproducible;
//	4. the sandbox shows the exact effect before publishing.
//
// And one decision from the brief that is stronger than any of them: **a rule I drafted must not
// behave like a rule Dr. Nahid wrote.** `TestNoneOfTheFortySeededRulesCanFire` and
// `TestApprovingASeededRuleIsWhatMakesItFire` are that property, and the second exists so the
// first cannot pass by the fixture being unable to fire for some other reason.

// ---------------------------------------------------------------------------
// The harness
// ---------------------------------------------------------------------------

type api struct {
	*testsupport.DB
	store    *medsafety.Store
	server   *httptest.Server
	facility uuid.UUID
	doctor   uuid.UUID
	held     []string
	// stepUp decides whether the fake verifier accepts. Flipped by the step-up test.
	stepUp *fakeStepUp
	logs   *bytes.Buffer
}

// fakeStepUp stands in for the second-factor service. It records what it was asked for, so a
// test can assert the purpose as well as the fact.
type fakeStepUp struct {
	accept  bool
	purpose string
	seen    int
}

func (f *fakeStepUp) ConsumeStepUp(_ context.Context, token, _ string, purpose string) error {
	f.seen++
	f.purpose = purpose
	if !f.accept || strings.TrimSpace(token) == "" {
		return errStepUp
	}
	return nil
}

var errStepUp = &stepUpError{}

type stepUpError struct{}

func (*stepUpError) Error() string { return "the step-up token is not valid" }

type staff struct {
	facility, user uuid.UUID
	code           string
	permissions    *[]string
}

func (s staff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.user.String(), FacilityID: s.facility.String(),
		SessionID:   uuid.NewSHA1(s.user, []byte("session")).String(),
		Code:        s.code,
		Permissions: *s.permissions, Roles: []string{"PHYSICIAN"},
		// No device. A browser has none, and the physician authors these at a browser — the
		// CP74 defect `dthclint readpath` exists to keep out.
	}, nil
}

func (s staff) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID,
					SessionID: caller.SessionID, Code: caller.Code, Role: "PHYSICIAN",
				}), httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
			}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

type testAuditor struct{ recorder *audit.Recorder }

func (a *testAuditor) RulePublished(ctx context.Context, p medsafety.Publication) error {
	actor := p.ActorID
	_, err := a.recorder.Record(ctx, audit.Entry{
		Kind: "medication_rule.published", FacilityID: p.FacilityID, ActorID: &actor,
		ActorCode: p.ActorCode, ActorRole: p.ActorRole,
		Details: map[string]any{
			"rule": p.Rule.Code, "type": string(p.Rule.Type), "version": p.Version.Version,
			"severity": string(p.Version.Severity), "plain": p.Plain.EN, "plain_bn": p.Plain.BN,
			"name_en": p.Version.NameEN, "name_bn": p.Version.NameBN,
			"message_en": p.Version.MessageEN, "message_bn": p.Version.MessageBN,
			"advice_en": p.Version.AdviceEN, "advice_bn": p.Version.AdviceBN,
			"source": p.Version.Source, "origin": string(p.Version.Origin),
			"condition": p.Version.Condition,
		},
	})
	return err
}

func (a *testAuditor) RuleWithdrawn(ctx context.Context, wd medsafety.Withdrawal) error {
	actor := wd.ActorID
	_, err := a.recorder.Record(ctx, audit.Entry{
		Kind: "medication_rule.withdrawn", FacilityID: wd.FacilityID, ActorID: &actor,
		ActorCode: wd.ActorCode, ActorRole: wd.ActorRole, Reason: wd.Reason,
		Details: map[string]any{
			"rule": wd.Rule.Code, "type": string(wd.Rule.Type), "version": wd.Version.Version,
		},
	})
	return err
}

func newAPI(t *testing.T) *api {
	t.Helper()
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h := &api{
		DB:     base,
		held:   []string{medsafety.PermRead, medsafety.PermWrite, medsafety.PermPublish},
		stepUp: &fakeStepUp{accept: true},
		logs:   &bytes.Buffer{},
	}
	h.store = medsafety.NewStore(pool, formulary.NewStore(pool))
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}
	if err := base.SQL.QueryRow(`
		INSERT INTO core.app_user (facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, 'DOC77', 'Nahid', 'নাহিদ', 'active') RETURNING id`,
		h.facility).Scan(&h.doctor); err != nil {
		t.Fatal(err)
	}

	// Everything the handlers log goes into a buffer, so the sandbox test can assert that a
	// patient's clinical picture is not in it.
	logger := slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	handlers := medsafety.NewHandlers(medsafety.HandlersConfig{
		Store:  h.store,
		Audit:  &testAuditor{recorder: audit.NewRecorder(audit.NewPostgresStore(pool), clock.Real{}, logger)},
		StepUp: h.stepUp,
		Clock:  clock.Real{}, Logger: logger,
	})
	who := staff{facility: h.facility, user: h.doctor, code: "DOC77", permissions: &h.held}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 21, RequestTimeout: 30 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(r chi.Router) { handlers.Mount(r) },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.server = httptest.NewServer(router)
	t.Cleanup(h.server.Close)
	return h
}

func (h *api) do(t *testing.T, method, path string, body any, stepUpToken string) (*http.Response, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "DTHCMS")
		req.Header.Set("Idempotency-Key", uuid.New().String())
		req.Header.Set("Content-Type", "application/json")
	}
	if stepUpToken != "" {
		req.Header.Set(httpx.StepUpHeader, stepUpToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp, decoded
}

// seededRule finds one of the forty by code.
func (h *api) seededRule(t *testing.T, code string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	var ruleID, versionID uuid.UUID
	if err := h.SQL.QueryRow(`
		SELECT r.id, v.id FROM core.medication_rule r
		  JOIN core.medication_rule_version v ON v.rule_id = r.id AND v.version = 1
		 WHERE r.facility_id = $1 AND r.code = $2`, h.facility, code).Scan(&ruleID, &versionID); err != nil {
		t.Fatalf("finding the seeded rule %s: %v", code, err)
	}
	return ruleID, versionID
}

// aRenalFailurePicture is a clinical picture in which MET-RENAL-30 would certainly fire.
func aRenalFailurePicture() medsafety.Context {
	egfr := 18.0
	return medsafety.Context{
		EGFR: &egfr,
		Proposed: []medsafety.Drug{{
			Label: "Comet 500 mg", Generic: "Metformin hydrochloride", Class: "BIGUANIDE",
		}},
	}
}

// ---------------------------------------------------------------------------
// The guarantee: an unapproved rule cannot fire
// ---------------------------------------------------------------------------

func TestNoneOfTheFortySeededRulesCanFire(t *testing.T) {
	// **The brief's own requirement: a rule I wrote must not behave like a rule he wrote.**
	//
	// Not "is filtered out" — *cannot be returned*. A seeded version has no effective period,
	// so there is no instant at which it was live and `RulesetAt` has nothing to return.
	h := newAPI(t)
	ctx := context.Background()

	var seeded int
	if err := h.SQL.QueryRow(`
		SELECT count(*) FROM core.medication_rule_version WHERE origin = 'SEED'`).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if seeded < 40 {
		t.Fatalf("the migration seeded %d rules; this test is about all of them", seeded)
	}

	set, err := h.store.RulesetAt(ctx, h.facility, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Rules) != 0 {
		t.Fatalf("%d seeded rules are live without anybody approving them: %v",
			len(set.Rules), set.Rules)
	}
	if findings := set.Findings(aRenalFailurePicture()); len(findings) != 0 {
		t.Fatalf("metformin at eGFR 18 produced %d findings from a library nobody has approved",
			len(findings))
	}

	// And the invariant that re-checks the whole table agrees.
	if _, err := h.SQL.Exec(`SELECT core.assert_no_unapproved_rule_is_live()`); err != nil {
		t.Fatalf("the invariant that guards this is not satisfied: %v", err)
	}
}

func TestApprovingASeededRuleIsWhatMakesItFire(t *testing.T) {
	// The other half, and the reason the test above is not vacuous: the same rule, the same
	// patient, approved — and now it blocks.
	h := newAPI(t)
	ctx := context.Background()
	_, versionID := h.seededRule(t, "MET-RENAL-30")

	resp, body := h.do(t, http.MethodPost, rulePath(h, t, versionID)+"/publish", nil, "a-fresh-token")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publishing answered %d: %v", resp.StatusCode, body)
	}

	set, err := h.store.RulesetAt(ctx, h.facility, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Rules) != 1 || set.Rules[0].Code != "MET-RENAL-30" {
		t.Fatalf("the approved rule is not in the ruleset: %+v", set.Rules)
	}
	findings := set.Findings(aRenalFailurePicture())
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Outcome != medsafety.OutcomeFires || findings[0].Severity != medsafety.SeverityBlock {
		t.Errorf("the finding is %s/%s, want FIRES/BLOCK", findings[0].Outcome, findings[0].Severity)
	}
	if findings[0].MessageBN == "" {
		t.Error("the finding has no Bengali message")
	}
	if !strings.Contains(findings[0].Source, "ADA") {
		t.Errorf("the finding does not carry its citation: %q", findings[0].Source)
	}
}

// rulePath returns /v1/medication-rules/{ruleId}/versions/{versionId} for a version.
func rulePath(h *api, t *testing.T, versionID uuid.UUID) string {
	t.Helper()
	var ruleID uuid.UUID
	if err := h.SQL.QueryRow(`SELECT rule_id FROM core.medication_rule_version WHERE id = $1`,
		versionID).Scan(&ruleID); err != nil {
		t.Fatal(err)
	}
	return "/v1/medication-rules/" + ruleID.String() + "/versions/" + versionID.String()
}

func TestEverySeededRuleIsUnapprovedCitesItsSourceAndReadsInBothLanguages(t *testing.T) {
	// Acceptance criterion 2, over the whole seeded set, through the API a screen reads.
	h := newAPI(t)
	resp, body := h.do(t, http.MethodGet, "/v1/medication-rules?limit=200", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listing answered %d", resp.StatusCode)
	}
	items, _ := body["items"].([]any)
	if len(items) < 40 {
		t.Fatalf("the library has %d rules", len(items))
	}

	types := map[string]int{}
	for _, raw := range items {
		item := raw.(map[string]any)
		code := item["code"].(string)
		types[item["type"].(string)]++

		if item["approved"].(bool) {
			t.Errorf("%s is approved; nobody at this clinic has read it", code)
		}
		if item["origin"].(string) != "SEED" {
			t.Errorf("%s says its origin is %v", code, item["origin"])
		}
		for _, field := range []string{"name_en", "name_bn", "severity"} {
			if s, _ := item[field].(string); strings.TrimSpace(s) == "" {
				t.Errorf("%s has no %s", code, field)
			}
		}

		// And the whole version, where the message, the source and the preview live.
		_, one := h.do(t, http.MethodGet, "/v1/medication-rules/"+item["id"].(string), nil, "")
		versions, _ := one["versions"].([]any)
		if len(versions) != 1 {
			t.Errorf("%s has %d versions", code, len(versions))
			continue
		}
		v := versions[0].(map[string]any)
		for _, field := range []string{"message_en", "message_bn", "source"} {
			if s, _ := v[field].(string); strings.TrimSpace(s) == "" {
				t.Errorf("%s has no %s", code, field)
			}
		}
		if v["approved_at"] != nil {
			t.Errorf("%s names an approver", code)
		}
		if v["effective_from"] != nil {
			t.Errorf("%s has an effective period and could therefore fire", code)
		}
		plain, _ := v["plain"].(map[string]any)
		for _, lang := range []string{"en", "bn"} {
			if s, _ := plain[lang].(string); strings.TrimSpace(s) == "" {
				t.Errorf("%s has no %s preview", code, lang)
			}
		}
	}

	// All eight kinds of rule are covered by the seed. A seed that had only renal rules would
	// leave seven authoring paths that nobody had ever produced a real example of.
	for _, want := range []string{"INTERACTION", "CONTRAINDICATION", "RENAL", "HEPATIC",
		"PREGNANCY", "PAEDIATRIC", "DUPLICATE_THERAPY", "MAX_DOSE"} {
		if types[want] == 0 {
			t.Errorf("nothing in the seed is a %s rule", want)
		}
	}
	t.Logf("seeded rules by type: %v", types)
}

func TestEverySeededConditionRoundTripsThroughTheValidator(t *testing.T) {
	// The migration writes the conditions as literal JSON. Nothing in SQL can check that they
	// are conditions this Go model can read, evaluate and re-serialise unchanged — so this does,
	// against the real rows, for all forty.
	h := newAPI(t)
	ctx := context.Background()
	vocab, err := h.store.Vocabulary(ctx, h.facility)
	if err != nil {
		t.Fatal(err)
	}

	page, err := h.store.Rules(ctx, h.facility, medsafety.RuleFilter{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range page.Items {
		_, versions, err := h.store.Rule(ctx, h.facility, summary.ID)
		if err != nil {
			t.Fatalf("%s: %v", summary.Code, err)
		}
		for _, v := range versions {
			if err := v.Validate(vocab, summary.Type); err != nil {
				t.Errorf("%s v%d does not validate: %v", summary.Code, v.Version, err)
			}
			// Canonical in the database as well as in Go, so a diff between two versions
			// shows what changed rather than what was reordered.
			stored, err := medsafety.MarshalCondition(v.Condition)
			if err != nil {
				t.Fatal(err)
			}
			again, err := medsafety.MarshalCondition(v.Condition.Canonical())
			if err != nil {
				t.Fatal(err)
			}
			if string(stored) != string(again) {
				t.Errorf("%s v%d is not stored canonically:\n  %s\n  %s",
					summary.Code, v.Version, stored, again)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Criterion 3: versions are retained and historical checks reproduce
// ---------------------------------------------------------------------------

func TestACheckAgainstVersionOneStillReproducesAfterVersionTwoIsPublished(t *testing.T) {
	// **Criterion 3, and the reason a version has a period rather than a flag.**
	//
	// v1 blocks below eGFR 30. It is published, a check is run, and the instant is noted. v2
	// changes the rule to a warning below eGFR 45 and is published. The same check, re-run
	// against the instant it was originally run at, must still say BLOCK — because that is what
	// the rule said at the time, and that is the question somebody asks months later.
	h := newAPI(t)
	ctx := context.Background()
	ruleID, v1 := h.seededRule(t, "MET-RENAL-30")

	if resp, body := h.do(t, http.MethodPost, rulePath(h, t, v1)+"/publish", nil, "tok"); resp.StatusCode != http.StatusOK {
		t.Fatalf("publishing v1 answered %d: %v", resp.StatusCode, body)
	}

	// A patient at eGFR 40: above v1's threshold, below v2's.
	egfr := 40.0
	patient := medsafety.Context{
		EGFR: &egfr,
		Proposed: []medsafety.Drug{{Label: "Comet 500 mg",
			Generic: "Metformin hydrochloride", Class: "BIGUANIDE"}},
	}

	under1, err := h.store.RulesetAt(ctx, h.facility, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := under1.Findings(patient); len(got) != 0 {
		t.Fatalf("v1 fired at eGFR 40: %+v", got)
	}
	// A patient who *is* below 30 under v1.
	sick := aRenalFailurePicture()
	if got := under1.Findings(sick); len(got) != 1 || got[0].Severity != medsafety.SeverityBlock {
		t.Fatalf("v1 did not block at eGFR 18: %+v", got)
	}
	asOf := time.Now()

	// Wait a moment so the two periods are genuinely distinguishable — a period is a
	// timestamptz and two publications in the same microsecond would make `at` ambiguous in a
	// way no clinic could produce but a test can.
	time.Sleep(10 * time.Millisecond)

	// v2: a warning, and at a higher threshold.
	_, drafted := h.do(t, http.MethodPost, "/v1/medication-rules/"+ruleID.String()+"/versions", nil, "")
	v2raw := drafted["version"].(map[string]any)["id"].(string)
	v2 := uuid.MustParse(v2raw)

	edit := map[string]any{
		"severity": "WARN",
		"name_en":  "Metformin below eGFR 45", "name_bn": "eGFR 45-এর নিচে মেটফরমিন",
		"message_en": "Reassess metformin below an eGFR of 45.",
		"message_bn": "eGFR 45-এর নিচে মেটফরমিন আবার বিবেচনা করুন।",
		"source":     "KDIGO 2022.",
		"condition": map[string]any{
			"subject": map[string]any{"match": "GENERIC",
				"generics": []string{"metformin hydrochloride"}},
			"when": []any{map[string]any{"kind": "EGFR", "operator": "LT",
				"value": 45, "unit": "mL/min/1.73m2"}},
		},
	}
	if resp, body := h.do(t, http.MethodPut, rulePath(h, t, v2), edit, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("saving v2 answered %d: %v", resp.StatusCode, body)
	}
	if resp, body := h.do(t, http.MethodPost, rulePath(h, t, v2)+"/publish", nil, "tok"); resp.StatusCode != http.StatusOK {
		t.Fatalf("publishing v2 answered %d: %v", resp.StatusCode, body)
	}

	// Today: v2 is live, so eGFR 40 now warns.
	today, err := h.store.RulesetAt(ctx, h.facility, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got := today.Findings(patient)
	if len(got) != 1 || got[0].Severity != medsafety.SeverityWarn || got[0].Version != 2 {
		t.Fatalf("under v2, eGFR 40 produced %+v", got)
	}

	// **The reproduction.** The same check, at the instant it was originally run.
	then, err := h.store.RulesetAt(ctx, h.facility, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(then.Versions) != 1 || then.Versions[0].Version != 1 {
		t.Fatalf("the ruleset as of the original instant is v%d", then.Versions[0].Version)
	}
	if reproduced := then.Findings(patient); len(reproduced) != 0 {
		t.Errorf("re-running the original check now produces %+v; at the time it produced nothing",
			reproduced)
	}
	if reproduced := then.Findings(sick); len(reproduced) != 1 ||
		reproduced[0].Severity != medsafety.SeverityBlock || reproduced[0].Version != 1 {
		t.Errorf("the original BLOCK does not reproduce: %+v", reproduced)
	}

	// And every version is still there, with its period.
	_, versions, err := h.store.Rule(ctx, h.facility, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("the rule has %d versions; nothing is ever removed", len(versions))
	}
	for _, v := range versions {
		if v.Version == 1 && (v.Status != medsafety.StatusSuperseded || v.EffectiveTo == nil) {
			t.Errorf("v1 is %s with effective_to %v; it should be closed", v.Status, v.EffectiveTo)
		}
	}
}

func TestAPublishedVersionCannotBeEdited(t *testing.T) {
	// Enforced by a trigger as well as by the handler, because the thing being protected is the
	// reproducibility above: a published version whose message changed makes every historical
	// check a lie.
	h := newAPI(t)
	_, v1 := h.seededRule(t, "PIO-HEART-FAILURE")
	if resp, _ := h.do(t, http.MethodPost, rulePath(h, t, v1)+"/publish", nil, "tok"); resp.StatusCode != http.StatusOK {
		t.Fatal("could not publish")
	}

	edit := map[string]any{
		"severity": "INFO", "name_en": "x", "name_bn": "x",
		"message_en": "changed", "message_bn": "বদলানো", "source": "x",
		"condition": map[string]any{
			"subject": map[string]any{"match": "CLASS", "classes": []string{"THIAZOLIDINEDIONE"}},
			"when":    []any{map[string]any{"kind": "DIAGNOSIS", "diagnosis_codes": []string{"I50.9"}}},
		},
	}
	resp, body := h.do(t, http.MethodPut, rulePath(h, t, v1), edit, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("editing a published version answered %d: %v", resp.StatusCode, body)
	}

	// And directly, with SQL, past the handler: the trigger refuses it too.
	if _, err := h.SQL.Exec(
		`UPDATE core.medication_rule_version SET message_en = 'rewritten' WHERE id = $1`, v1); err == nil {
		t.Error("a published rule's message was rewritten in the database")
	}
}

// ---------------------------------------------------------------------------
// Publishing: the physician, and the step-up
// ---------------------------------------------------------------------------

func TestPublishingNeedsAStepUpAndTheRightPermission(t *testing.T) {
	h := newAPI(t)
	_, v := h.seededRule(t, "ACE-PREG")
	path := rulePath(h, t, v) + "/publish"

	// No token.
	resp, body := h.do(t, http.MethodPost, path, nil, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("publishing with no step-up answered %d: %v", resp.StatusCode, body)
	}
	if code := errorCode(body); code != "STEP_UP_REQUIRED" {
		t.Errorf("the error code is %q; it has to be distinguishable from FORBIDDEN, because "+
			"the person is allowed to do this and merely has to prove it is still them", code)
	}

	// A token the verifier refuses.
	h.stepUp.accept = false
	if resp, _ := h.do(t, http.MethodPost, path, nil, "stale"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a refused step-up token answered %d", resp.StatusCode)
	}
	h.stepUp.accept = true

	// The right purpose was asked for. A token minted for ending somebody's sessions must not
	// publish a clinical rule.
	if h.stepUp.purpose != medsafety.StepUpPurpose {
		t.Errorf("the step-up was consumed for purpose %q, want %q",
			h.stepUp.purpose, medsafety.StepUpPurpose)
	}

	// And without the publish permission, even with a good token.
	h.held = []string{medsafety.PermRead, medsafety.PermWrite}
	if resp, _ := h.do(t, http.MethodPost, path, nil, "tok"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a caller without medication.rule.publish published a rule: %d", resp.StatusCode)
	}

	h.held = []string{medsafety.PermRead, medsafety.PermWrite, medsafety.PermPublish}
	if resp, body := h.do(t, http.MethodPost, path, nil, "tok"); resp.StatusCode != http.StatusOK {
		t.Fatalf("the physician with a token got %d: %v", resp.StatusCode, body)
	}
}

func TestAPublicationIsAuditedWithTheWholeRule(t *testing.T) {
	// The checkpoint's audit requirement, and the defect it names: a rule that changed with no
	// record of what it said. A version number in the trail is not enough, because the version
	// table can be superseded and the rule withdrawn, and then the trail is the only copy.
	h := newAPI(t)
	_, v := h.seededRule(t, "GLP1-MTC")
	if resp, body := h.do(t, http.MethodPost, rulePath(h, t, v)+"/publish", nil, "tok"); resp.StatusCode != http.StatusOK {
		t.Fatalf("publishing answered %d: %v", resp.StatusCode, body)
	}

	var kind, actorCode string
	var details []byte
	if err := h.SQL.QueryRow(`
		SELECT kind, actor_code, details FROM ledger.audit_event
		 WHERE kind = 'medication_rule.published' ORDER BY recorded_at DESC LIMIT 1`).
		Scan(&kind, &actorCode, &details); err != nil {
		t.Fatalf("no audit entry: %v", err)
	}
	if actorCode != "DOC77" {
		t.Errorf("the entry names %q", actorCode)
	}
	var parsed map[string]any
	if err := json.Unmarshal(details, &parsed); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"rule", "version", "severity", "message_en", "message_bn",
		"source", "condition", "plain", "plain_bn"} {
		if parsed[key] == nil {
			t.Errorf("the audit entry has no %s; the whole rule content was asked for", key)
		}
	}
	// The condition is the document, not a summary of it: it has to be enough to re-evaluate.
	condition, _ := json.Marshal(parsed["condition"])
	if !strings.Contains(string(condition), "GLP_1_RECEPTOR_AGONIST") ||
		!strings.Contains(string(condition), "C73") {
		t.Errorf("the recorded condition is not the rule's own: %s", condition)
	}
	if plain, _ := parsed["plain"].(string); !strings.Contains(plain, "stop the prescription") {
		t.Errorf("the entry does not say in words what the rule does: %q", plain)
	}
}

func TestWithdrawingARuleKeepsEveryVersionAndItsPeriod(t *testing.T) {
	h := newAPI(t)
	ctx := context.Background()
	ruleID, v := h.seededRule(t, "STATIN-PREG")
	if resp, _ := h.do(t, http.MethodPost, rulePath(h, t, v)+"/publish", nil, "tok"); resp.StatusCode != http.StatusOK {
		t.Fatal("could not publish")
	}
	liveAt := time.Now()
	time.Sleep(10 * time.Millisecond)

	resp, body := h.do(t, http.MethodPost, "/v1/medication-rules/"+ruleID.String()+"/withdraw",
		map[string]any{"reason": "Superseded by the 2026 guidance."}, "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("withdrawing answered %d: %v", resp.StatusCode, body)
	}

	// Gone from today's ruleset...
	now, err := h.store.RulesetAt(ctx, h.facility, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range now.Rules {
		if r.Code == "STATIN-PREG" {
			t.Error("a withdrawn rule is still live")
		}
	}
	// ...and still there for the period it covered, which is the question somebody asks.
	before, err := h.store.RulesetAt(ctx, h.facility, liveAt)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range before.Rules {
		if r.Code == "STATIN-PREG" {
			found = true
		}
	}
	if !found {
		t.Error("a check run while the rule was live can no longer be reproduced")
	}

	// A reason is required.
	_, v2 := h.seededRule(t, "PGB-PAED")
	_ = v2
	ruleID2, _ := h.seededRule(t, "PGB-PAED")
	if resp, _ := h.do(t, http.MethodPost, "/v1/medication-rules/"+ruleID2.String()+"/withdraw",
		map[string]any{"reason": "  "}, "tok"); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("withdrawing with no reason answered %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Criterion 4: the sandbox
// ---------------------------------------------------------------------------

func TestTheSandboxShowsTheExactEffectBeforePublishing(t *testing.T) {
	h := newAPI(t)
	_, v := h.seededRule(t, "MET-RENAL-30")

	drug := map[string]any{"label": "Comet 500 mg",
		"generic": "Metformin hydrochloride", "class": "BIGUANIDE"}

	for _, tc := range []struct {
		name    string
		patient map[string]any
		outcome string
		missing string
	}{
		{"eGFR 25", map[string]any{"egfr": 25, "proposed": []any{drug}}, "FIRES", ""},
		{"eGFR 45", map[string]any{"egfr": 45, "proposed": []any{drug}}, "DOES_NOT_FIRE", ""},
		{"no eGFR at all", map[string]any{"proposed": []any{drug}}, "CANNOT_VERIFY", "EGFR"},
		{"a different medicine", map[string]any{"egfr": 12, "proposed": []any{
			map[string]any{"label": "Thyrox 50", "generic": "Levothyroxine sodium",
				"class": "THYROID_HORMONE"}}}, "NOT_APPLICABLE", ""},
	} {
		resp, body := h.do(t, http.MethodPost, "/v1/medication-rules/sandbox",
			map[string]any{"version_id": v.String(), "patient": tc.patient}, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: the sandbox answered %d: %v", tc.name, resp.StatusCode, body)
		}
		finding := body["finding"].(map[string]any)
		if finding["outcome"] != tc.outcome {
			t.Errorf("%s produced %v, want %s", tc.name, finding["outcome"], tc.outcome)
		}
		if tc.missing != "" {
			missing, _ := finding["missing"].([]any)
			if len(missing) != 1 || missing[0] != tc.missing {
				t.Errorf("%s did not name %s as missing: %v", tc.name, tc.missing, missing)
			}
		}
		// **The sandbox says out loud that nothing it shows is happening yet.**
		if body["live"] != false {
			t.Errorf("%s: the sandbox says the draft is live", tc.name)
		}
		// The working, in both languages, so the physician can see why.
		steps, _ := finding["steps"].([]any)
		if tc.outcome != "NOT_APPLICABLE" {
			if len(steps) == 0 {
				t.Errorf("%s: there is no working to read", tc.name)
			} else {
				step := steps[0].(map[string]any)
				if s, _ := step["because_bn"].(string); strings.TrimSpace(s) == "" {
					t.Errorf("%s: the working has no Bengali", tc.name)
				}
			}
		}
		// The picture is echoed back, so the screen can say what was tested.
		if body["patient"] == nil {
			t.Errorf("%s: the sandbox did not say what it tested", tc.name)
		}
	}
}

func TestTheSandboxRunsAnUnsavedRuleToo(t *testing.T) {
	// Criterion 1: author, test, publish. Testing before saving is the order a physician works
	// in, and a sandbox that only ran saved rules would make him save a rule he is not sure of.
	h := newAPI(t)
	body := map[string]any{
		"type": "MAX_DOSE",
		"rule": map[string]any{
			"severity": "WARN",
			"name_en":  "Glimepiride above 6 mg", "name_bn": "6 mg-এর বেশি গ্লিমেপিরাইড",
			"message_en": "More than 6 mg adds hypoglycaemia and little else.",
			"message_bn": "6 mg-এর বেশি দিলে হাইপোগ্লাইসেমিয়া বাড়ে, আর কিছু নয়।",
			"source":     "Dr. Nahid's own practice.",
			"condition": map[string]any{
				"subject": map[string]any{"match": "GENERIC", "generics": []string{"Glimepiride"}},
				"when": []any{map[string]any{"kind": "DAILY_DOSE", "operator": "GT",
					"value": 6, "unit": "mg"}},
			},
		},
		"patient": map[string]any{"proposed": []any{map[string]any{
			"label": "Secrin 4 mg", "generic": "Glimepiride", "class": "SULPHONYLUREA",
			"daily_dose": 8, "dose_unit": "mg"}}},
	}
	resp, out := h.do(t, http.MethodPost, "/v1/medication-rules/sandbox", body, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the sandbox answered %d: %v", resp.StatusCode, out)
	}
	finding := out["finding"].(map[string]any)
	if finding["outcome"] != "FIRES" {
		t.Errorf("8 mg against a 6 mg rule produced %v", finding["outcome"])
	}
	plain := out["plain"].(map[string]any)
	if !strings.Contains(plain["en"].(string), "6 mg") {
		t.Errorf("the preview does not state the threshold: %v", plain["en"])
	}
}

func TestTheSandboxLogsNothingAboutThePatient(t *testing.T) {
	// **No PHI in logs.** The sandbox is where somebody would most reasonably have added a log
	// line, and the picture it runs on is a patient's age, kidney function, diagnoses and
	// medication list.
	h := newAPI(t)
	_, v := h.seededRule(t, "GLP1-MTC")
	h.logs.Reset()

	resp, _ := h.do(t, http.MethodPost, "/v1/medication-rules/sandbox", map[string]any{
		"version_id": v.String(),
		"patient": map[string]any{
			"age_years": 61.5, "egfr": 23.7, "pregnancy": "NOT_PREGNANT",
			"diagnoses": []string{"C73", "E11.9"},
			"allergies": []string{"PENICILLIN"},
			"proposed": []any{map[string]any{"label": "Ozempic",
				"generic": "Semaglutide", "class": "GLP_1_RECEPTOR_AGONIST"}},
		},
	}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the sandbox answered %d", resp.StatusCode)
	}

	logged := h.logs.String()
	for _, secret := range []string{"61.5", "23.7", "C73", "E11.9", "PENICILLIN", "Ozempic"} {
		if strings.Contains(logged, secret) {
			t.Errorf("the log carries %q from the tested picture:\n%s", secret, logged)
		}
	}
}

// ---------------------------------------------------------------------------
// Import and export
// ---------------------------------------------------------------------------

func TestImportedRulesArriveUnapprovedHoweverTheFileIsMarked(t *testing.T) {
	// A file is a thing anybody can edit. An import that could set an approval would be a way
	// to publish a clinical rule without a physician's second factor.
	h := newAPI(t)
	doc := map[string]any{
		"format": 1,
		"rules": []any{map[string]any{
			"code": "NEW-FROM-FILE", "type": "PAEDIATRIC",
			// Every one of these is a lie the file is telling, and every one is ignored.
			"status": "PUBLISHED", "approved": true, "origin": "AUTHORED", "version": 9,
			"severity": "BLOCK",
			"name_en":  "Pioglitazone under 18", "name_bn": "18 বছরের কম বয়সে পায়োগ্লিটাজোন",
			"message_en": "Not established under 18.", "message_bn": "18 বছরের কম বয়সে প্রতিষ্ঠিত নয়।",
			"source": "Imported for review.",
			"condition": map[string]any{
				"subject": map[string]any{"match": "GENERIC", "generics": []string{"Pioglitazone"}},
				"when": []any{map[string]any{"kind": "AGE", "operator": "LT",
					"value": 18, "unit": "years"}},
			},
		}},
	}

	// A dry run touches nothing.
	resp, report := h.do(t, http.MethodPost, "/v1/medication-rules/import", doc, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the dry run answered %d: %v", resp.StatusCode, report)
	}
	if report["dry_run"] != true {
		t.Error("the default mode is not a dry run")
	}
	var exists int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM core.medication_rule WHERE code = 'NEW-FROM-FILE'`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != 0 {
		t.Fatal("the dry run created a rule")
	}

	// Applied.
	resp, report = h.do(t, http.MethodPost, "/v1/medication-rules/import?mode=APPLY", doc, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the import answered %d: %v", resp.StatusCode, report)
	}
	var status, origin string
	var approvedAt, effectiveFrom *time.Time
	if err := h.SQL.QueryRow(`
		SELECT v.status, v.origin, v.approved_at, v.effective_from
		  FROM core.medication_rule r JOIN core.medication_rule_version v ON v.rule_id = r.id
		 WHERE r.code = 'NEW-FROM-FILE'`).Scan(&status, &origin, &approvedAt, &effectiveFrom); err != nil {
		t.Fatalf("the rule was not created: %v", err)
	}
	if status != "DRAFT" || origin != "IMPORTED" {
		t.Errorf("the imported rule is %s/%s, want DRAFT/IMPORTED", status, origin)
	}
	if approvedAt != nil || effectiveFrom != nil {
		t.Errorf("the imported rule arrived approved: approved_at=%v effective_from=%v",
			approvedAt, effectiveFrom)
	}
	// And the report says what it ignored, rather than leaving the reviewer to discover it.
	ignored, _ := report["ignored_fields_en"].([]any)
	if len(ignored) == 0 {
		t.Error("the report does not say which fields were ignored")
	}
}

func TestImportRejectsPerRuleAndKeepsTheRest(t *testing.T) {
	h := newAPI(t)
	good := map[string]any{
		"code": "GOOD-ONE", "type": "PAEDIATRIC", "severity": "WARN",
		"name_en": "a", "name_bn": "ক", "message_en": "a", "message_bn": "ক",
		"source": "x",
		"condition": map[string]any{
			"subject": map[string]any{"match": "GENERIC", "generics": []string{"Pioglitazone"}},
			"when": []any{map[string]any{"kind": "AGE", "operator": "LT", "value": 18,
				"unit": "years"}},
		},
	}
	bad := map[string]any{
		"code": "BAD-ONE", "type": "PAEDIATRIC", "severity": "WARN",
		"name_en": "a", "name_bn": "ক", "message_en": "a", "message_bn": "",
		"source": "x",
		"condition": map[string]any{
			"subject": map[string]any{"match": "GENERIC", "generics": []string{"Not a molecule"}},
			"when":    []any{map[string]any{"kind": "AGE", "operator": "LT", "value": 18, "unit": "years"}},
		},
	}
	resp, report := h.do(t, http.MethodPost, "/v1/medication-rules/import?mode=APPLY",
		map[string]any{"format": 1, "rules": []any{bad, good}}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the import answered %d: %v", resp.StatusCode, report)
	}
	if report["accepted"].(float64) != 1 || report["rejected"].(float64) != 1 {
		t.Errorf("accepted %v, rejected %v", report["accepted"], report["rejected"])
	}
	outcomes := report["outcomes"].([]any)
	first := outcomes[0].(map[string]any)
	if first["result"] != "REJECTED" {
		t.Errorf("the broken rule was %v", first["result"])
	}
	for _, lang := range []string{"reason_en", "reason_bn"} {
		if s, _ := first[lang].(string); strings.TrimSpace(s) == "" {
			t.Errorf("the rejection has no %s", lang)
		}
	}
	if first["line"].(float64) != 1 {
		t.Errorf("the rejection does not say which line: %v", first["line"])
	}
	var created int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM core.medication_rule WHERE code = 'GOOD-ONE'`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != 1 {
		t.Error("one bad rule blocked the good one")
	}
}

func TestExportingAndReimportingTheLibraryAddsNothing(t *testing.T) {
	// The failure otherwise: a physician exports, reads, changes nothing, imports — and has
	// forty new drafts to work through.
	h := newAPI(t)
	resp, doc := h.do(t, http.MethodGet, "/v1/medication-rules/export", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export answered %d", resp.StatusCode)
	}
	rules, _ := doc["rules"].([]any)
	if len(rules) < 40 {
		t.Fatalf("the export has %d rules", len(rules))
	}
	// The file is readable by somebody who will not read JSON.
	first := rules[0].(map[string]any)
	plain, _ := first["plain"].(map[string]any)
	if s, _ := plain["bn"].(string); strings.TrimSpace(s) == "" {
		t.Error("the exported rule has no Bengali plain-language form")
	}

	resp, report := h.do(t, http.MethodPost, "/v1/medication-rules/import?mode=APPLY", doc, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-import answered %d: %v", resp.StatusCode, report)
	}
	if report["accepted"].(float64) != 0 {
		t.Errorf("re-importing an unchanged export drafted %v new versions", report["accepted"])
	}
	if report["skipped"].(float64) < 40 {
		t.Errorf("only %v rules were recognised as unchanged", report["skipped"])
	}
}

// ---------------------------------------------------------------------------
// Authoring, end to end
// ---------------------------------------------------------------------------

func TestAPhysicianCanAuthorTestAndPublishWithoutADeveloper(t *testing.T) {
	// **Acceptance criterion 1, walked through the API in the order the screen walks it.** Not
	// a proof that the UI is usable — only Dr. Nahid can say that — but a proof that every step
	// he has to take exists and is reachable with the permissions he holds.
	h := newAPI(t)

	// 1. What may I say? The form is built from this.
	resp, vocab := h.do(t, http.MethodGet, "/v1/medication-rules/vocabulary", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the vocabulary answered %d", resp.StatusCode)
	}
	types, _ := vocab["types"].([]any)
	if len(types) != 8 {
		t.Fatalf("the form offers %d rule types", len(types))
	}
	generics, _ := vocab["generics"].([]any)
	if len(generics) < 50 {
		t.Fatalf("the form offers %d molecules", len(generics))
	}

	draft := map[string]any{
		"code": "NAHID-SU-ELDERLY", "type": "PAEDIATRIC",
		"severity": "WARN",
		"name_en":  "Sulphonylurea over 75", "name_bn": "75 বছরের বেশি বয়সে সালফোনাইলইউরিয়া",
		"message_en": "A sulphonylurea over 75 causes hypoglycaemia that presents as a fall.",
		"message_bn": "75 বছরের বেশি বয়সে সালফোনাইলইউরিয়ায় যে হাইপোগ্লাইসেমিয়া হয়, তা পড়ে যাওয়া হিসেবে দেখা দেয়।",
		"advice_en":  "Use linagliptin, or halve the dose and check.",
		"advice_bn":  "লিনাগ্লিপটিন দিন, নয়তো মাত্রা অর্ধেক করে দেখুন।",
		"source":     "Dr. Nahid's own practice, to be cited properly before publication.",
		"condition": map[string]any{
			"subject": map[string]any{"match": "CLASS", "classes": []string{"SULPHONYLUREA"}},
			"when": []any{map[string]any{"kind": "AGE", "operator": "GT", "value": 75,
				"unit": "years"}},
		},
	}

	// 2. Say it back to me.
	resp, preview := h.do(t, http.MethodPost, "/v1/medication-rules/preview", draft, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the preview answered %d: %v", resp.StatusCode, preview)
	}
	if preview["valid"] != true {
		t.Fatalf("the preview says the rule is not checkable: %v", preview["problem"])
	}
	plain := preview["plain"].(map[string]any)
	if !strings.Contains(plain["en"].(string), "above 75 years") {
		t.Errorf("the preview does not state the threshold: %v", plain["en"])
	}

	// 3. Save it.
	resp, created := h.do(t, http.MethodPost, "/v1/medication-rules", draft, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating answered %d: %v", resp.StatusCode, created)
	}
	ruleID := created["rule"].(map[string]any)["id"].(string)
	versionID := created["version"].(map[string]any)["id"].(string)

	// 4. Test it before publishing.
	resp, tested := h.do(t, http.MethodPost, "/v1/medication-rules/sandbox", map[string]any{
		"version_id": versionID,
		"patient": map[string]any{"age_years": 81, "proposed": []any{map[string]any{
			"label": "Secrin 2 mg", "generic": "Glimepiride", "class": "SULPHONYLUREA"}}},
	}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the sandbox answered %d: %v", resp.StatusCode, tested)
	}
	if tested["finding"].(map[string]any)["outcome"] != "FIRES" {
		t.Fatalf("the rule did not fire on an 81-year-old: %v", tested["finding"])
	}
	if tested["live"] != false {
		t.Error("the sandbox implied the unpublished rule was already in force")
	}

	// 5. Publish it.
	resp, published := h.do(t, http.MethodPost,
		"/v1/medication-rules/"+ruleID+"/versions/"+versionID+"/publish", nil, "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publishing answered %d: %v", resp.StatusCode, published)
	}

	// 6. It is live, it is his, and the library says so.
	set, err := h.store.RulesetAt(context.Background(), h.facility, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Versions) != 1 || set.Versions[0].ApprovedBy == nil ||
		*set.Versions[0].ApprovedBy != h.doctor {
		t.Fatalf("the published rule is %+v", set.Versions)
	}
	if set.Versions[0].Origin != medsafety.OriginAuthored {
		t.Errorf("a rule he wrote is recorded as %s", set.Versions[0].Origin)
	}

	// And the library screen shows his employee code against it. `RulesetAt` deliberately does
	// not join the user table — the evaluation path has no use for a name and should not pay a
	// join for one — so the read that a person looks at is the one that carries it.
	_, versions, err := h.store.Rule(context.Background(), h.facility, uuid.MustParse(ruleID))
	if err != nil {
		t.Fatal(err)
	}
	if versions[0].ApprovedCode != "DOC77" || versions[0].AuthoredCode != "DOC77" {
		t.Errorf("the version names %q as approver and %q as author",
			versions[0].ApprovedCode, versions[0].AuthoredCode)
	}
}

func TestTheLibraryIsReachableFromABrowserSessionWithNoDevice(t *testing.T) {
	// The CP74 defect. The physician authors these at a browser, and a browser has no enrolled
	// device by design.
	h := newAPI(t)
	for _, path := range []string{
		"/v1/medication-rules", "/v1/medication-rules/vocabulary",
		"/v1/medication-rules/allergens", "/v1/medication-rules/export",
	} {
		if resp, body := h.do(t, http.MethodGet, path, nil, ""); resp.StatusCode != http.StatusOK {
			t.Errorf("%s answered %d from a browser session: %v", path, resp.StatusCode, body)
		}
	}
}

// ---------------------------------------------------------------------------
// Cross-reactivity
// ---------------------------------------------------------------------------

func TestEveryCrossReactivityMappingIsSeededUnapprovedAndCited(t *testing.T) {
	h := newAPI(t)
	resp, body := h.do(t, http.MethodGet, "/v1/medication-rules/allergens", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the allergen map answered %d", resp.StatusCode)
	}
	groups, _ := body["groups"].([]any)
	cross, _ := body["cross_reactions"].([]any)
	if len(groups) < 6 || len(cross) < 4 {
		t.Fatalf("%d groups and %d cross-reactions", len(groups), len(cross))
	}
	for _, raw := range append(groups, cross...) {
		item := raw.(map[string]any)
		if item["approved"] != false {
			t.Errorf("%v is approved; nobody at this clinic has read it", item["code"])
		}
		if s, _ := item["source"].(string); strings.TrimSpace(s) == "" {
			t.Errorf("%v cites nothing", item["code"])
		}
	}
	// The one that matters clinically: the cross-reaction most prescribers assume, recorded as
	// not supported rather than left out.
	found := false
	for _, raw := range cross {
		item := raw.(map[string]any)
		if item["from_group"] == "SULFONAMIDE_ANTIBIOTIC" && item["to_group"] == "SULFONAMIDE_NON_ANTIBIOTIC" {
			found = true
			if item["risk"] != "NONE" {
				t.Errorf("the sulfonamide mapping says risk %v", item["risk"])
			}
			if s, _ := item["note_bn"].(string); strings.TrimSpace(s) == "" {
				t.Error("the sulfonamide mapping has no Bengali note")
			}
		}
	}
	if !found {
		t.Error("the sulfonamide-to-sulphonylurea mapping is not in the seed")
	}
}

func TestApprovingACrossReactionNeedsAStepUp(t *testing.T) {
	h := newAPI(t)
	var id uuid.UUID
	if err := h.SQL.QueryRow(`
		SELECT id FROM core.allergen_cross_reaction
		 WHERE from_group = 'PENICILLIN' AND to_group = 'CEPHALOSPORIN'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	path := "/v1/medication-rules/allergens/cross-reactions/" + id.String() + "/approve"
	if resp, _ := h.do(t, http.MethodPost, path, nil, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("approving with no step-up answered %d", resp.StatusCode)
	}
	resp, body := h.do(t, http.MethodPost, path, nil, "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approving answered %d: %v", resp.StatusCode, body)
	}
	var approvedBy *uuid.UUID
	if err := h.SQL.QueryRow(
		`SELECT approved_by FROM core.allergen_cross_reaction WHERE id = $1`, id).Scan(&approvedBy); err != nil {
		t.Fatal(err)
	}
	if approvedBy == nil || *approvedBy != h.doctor {
		t.Errorf("the approval names %v", approvedBy)
	}
}

func errorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}
