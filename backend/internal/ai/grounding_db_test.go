package ai_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/config"
)

// The grounding check against a real database (CP72).
//
// The Go check is one of three enforcements and it is the one a future code path could be written
// around. The other two are in migration 00054 — two check constraints and three invariants — and
// they are only real if something asserts against PostgreSQL rather than against a stub. That is
// the same argument CP70's database tests make, applied to the rule that matters more.

// groundedPayload is a payload with a fact index, as `internal/synthesis` produces.
func groundedPayload() map[string]any {
	return map[string]any{
		"visit": map[string]any{"clinic_day": "2026-09-14"},
		"facts": []any{
			map[string]any{"ref": "obs.hba1c:2026-09-14", "kind": "obs", "label": "HbA1c",
				"value": "8.2", "unit": "%", "on": "2026-09-14"},
		},
	}
}

// answering makes the mock reply with one exact object, so a test can put a claim in front of the
// check and say what it expects to happen to it.
func (h *harness) answering(t *testing.T, object map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	h.provider.Respond = func(ai.ProviderRequest) (ai.ProviderResponse, error) {
		return ai.ProviderResponse{
			Text: string(encoded), ModelVersion: ai.MockModelVersion,
			InputTokens: 100, OutputTokens: 40, FinishReason: "STOP",
		}, nil
	}
}

// groundingRequest invokes the synthesis agent, which is the one this checkpoint grounds. The echo
// fixture is exempt (see migration 00054), and a test of the check that used it would be testing
// the exemption.
func (h *harness) groundingRequest(payload map[string]any) ai.Request {
	return ai.Request{
		AgentCode: "clinical.synthesis",
		Subject: ai.Subject{
			PatientID: h.realPatient, AgeMonths: 511, Sex: "female",
			Identifiers: map[string]string{"name_en": "Ayesha Rahman"},
		},
		Payload: payload,
	}
}

func (h *harness) defects(t *testing.T, interaction uuid.UUID) []ai.Defect {
	t.Helper()
	all, err := h.store.Defects(context.Background(), h.facility, ai.DefectFilter{
		Since: h.clock.Now().Add(-time.Hour), Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	var mine []ai.Defect
	for _, one := range all {
		if one.InteractionID == interaction {
			mine = append(mine, one)
		}
	}
	return mine
}

// TestAnUngroundedAnswerNeverReachesTheCallerAndBecomesADefect.
//
// The whole checkpoint in one test. The model returns a narrative quoting an HbA1c that is not in
// the payload; the caller gets an error and no output; the interaction says UNGROUNDED; and the
// finding is a row somebody can act on a week later.
func TestAnUngroundedAnswerNeverReachesTheCallerAndBecomesADefect(t *testing.T) {
	h := newHarness(t)
	h.answering(t, synthesisAnswer("HbA1c is 12.4 % and rising [obs.hba1c:2026-09-14]."))

	result, err := h.gateway(t, config.TierPaid, config.EnvTest).
		Invoke(context.Background(), h.groundingRequest(groundedPayload()))

	if err == nil {
		t.Fatal("an answer quoting a value the model was never shown was returned to the caller")
	}
	if !errors.Is(err, ai.ErrUngrounded) {
		t.Fatalf("the refusal was %v, not a grounding refusal", err)
	}
	if result.Output != nil {
		t.Errorf("the refused result still carries output: %v", result.Output)
	}
	// The typed error carries the verdict out, which is what the operator screens and the
	// evaluation harness read rather than parsing a sentence.
	var ungrounded *ai.UngroundedError
	if !errors.As(err, &ungrounded) {
		t.Fatalf("the refusal is not an *ai.UngroundedError: %T", err)
	}
	if ungrounded.Report.State != ai.GroundingFailed || len(ungrounded.Report.Findings) == 0 {
		t.Fatalf("the refusal carries no findings: %+v", ungrounded.Report)
	}

	interaction := h.latest(t)
	if interaction.Status != "UNGROUNDED" {
		t.Errorf("the call is recorded as %s", interaction.Status)
	}
	// The answer is kept. A defect whose evidence was discarded is the "silently retried" failure
	// wearing a different hat.
	if len(interaction.Response) == 0 {
		t.Error("the model's answer was not kept, so nobody can see what it said")
	}

	defects := h.defects(t, interaction.ID)
	if len(defects) != len(ungrounded.Report.Findings) {
		t.Fatalf("%d defects recorded for %d findings", len(defects), len(ungrounded.Report.Findings))
	}
	one := defects[0]
	// What a defect has to carry for somebody to act on it a week later, asserted field by field
	// rather than by counting rows.
	switch {
	case one.Arm != ai.ArmNumber:
		t.Errorf("the defect names the %s arm", one.Arm)
	case one.Token != "12.4":
		t.Errorf("the defect's token is %q", one.Token)
	case one.Path != "narrative_en":
		t.Errorf("the defect points at %q", one.Path)
	case one.PromptVersion == "" || one.ModelVersion == "":
		t.Errorf("the defect does not name the prompt and model that produced it: %q / %q",
			one.PromptVersion, one.ModelVersion)
	case one.InteractionID != interaction.ID:
		t.Error("the defect does not name the call it came from, so the payload cannot be found")
	case one.Status != "OPEN":
		t.Errorf("a fresh defect is %s", one.Status)
	case !strings.Contains(one.Excerpt, "12.4"):
		t.Errorf("the defect's excerpt %q does not show the sentence", one.Excerpt)
	}

	h.verifyGroundingInvariants(t)
}

// TestAnUngroundedAnswerIsNotRetried is the contrast with CP70's schema failure.
//
// A malformed answer is retried up to `max_attempts`, because JSON that does not parse is a
// transport problem the second attempt usually fixes. An invented HbA1c is a quality problem: the
// second attempt would probably "work", the evidence would be gone, and the rate of the failure
// this checkpoint exists to measure would read as zero.
func TestAnUngroundedAnswerIsNotRetried(t *testing.T) {
	h := newHarness(t)
	calls := 0
	answer, err := json.Marshal(synthesisAnswer("Creatinine is 3.9 mg/dL [obs.hba1c:2026-09-14]."))
	if err != nil {
		t.Fatal(err)
	}
	h.provider.Respond = func(ai.ProviderRequest) (ai.ProviderResponse, error) {
		calls++
		return ai.ProviderResponse{
			Text: string(answer), ModelVersion: ai.MockModelVersion, FinishReason: "STOP",
		}, nil
	}

	_, err = h.gateway(t, config.TierPaid, config.EnvTest).
		Invoke(context.Background(), h.groundingRequest(groundedPayload()))
	if !errors.Is(err, ai.ErrUngrounded) {
		t.Fatalf("the fabricated creatinine was not refused: %v", err)
	}
	if calls != 1 {
		t.Errorf("the model was called %d times for an ungrounded answer; retrying one destroys "+
			"the evidence and makes the violation rate read as zero", calls)
	}
	if interactions := h.rows(t, `agent_code = 'clinical.synthesis'`); interactions != 1 {
		t.Errorf("%d interactions were recorded; a refusal is one call, not a series", interactions)
	}
}

// TestTheDatabaseRefusesToRecordAFailedGroundingAsASuccess.
//
// The second enforcement, on its own. Every line of Go in this package could be wrong and this
// would still hold, which is the property "each independently sufficient" is asking for.
func TestTheDatabaseRefusesToRecordAFailedGroundingAsASuccess(t *testing.T) {
	h := newHarness(t)
	h.answering(t, synthesisAnswer("HbA1c is 8.2 % [obs.hba1c:2026-09-14]."))
	if _, err := h.gateway(t, config.TierPaid, config.EnvTest).
		Invoke(context.Background(), h.groundingRequest(groundedPayload())); err != nil {
		t.Fatalf("a grounded answer was refused: %v", err)
	}
	id := h.latest(t).ID

	_, err := h.SQL.Exec(
		`UPDATE core.ai_interaction SET grounding_state = 'FAILED', grounding_findings = 1 WHERE id = $1`, id)
	if err == nil {
		t.Fatal("the database accepted a SUCCEEDED call whose grounding failed")
	}
	if !strings.Contains(err.Error(), "ai_interaction_a_failed_grounding_is_not_a_success") {
		t.Errorf("refused by %v, not by the constraint that exists for this", err)
	}

	// And the other way: a status claiming a refusal without the evidence for it.
	_, err = h.SQL.Exec(`UPDATE core.ai_interaction SET status = 'UNGROUNDED' WHERE id = $1`, id)
	if err == nil {
		t.Fatal("the database accepted an UNGROUNDED row with no findings behind it")
	}
	if !strings.Contains(err.Error(), "ai_interaction_ungrounded_says_what_was_wrong") {
		t.Errorf("refused by %v, not by the constraint that exists for this", err)
	}
}

// TestAnExemptAgentIsNotGroundedAndSaysSo.
//
// `gateway.echo` answers with a count of the payload's fields — a number that is correct, derived
// from what it was shown, and in the payload nowhere. It is exempt, in a row somebody wrote a
// reason into, and the verdict on its calls is NOT_REQUIRED rather than PASSED: "we did not have to
// look" and "we looked and it was fine" are different facts.
func TestAnExemptAgentIsNotGroundedAndSaysSo(t *testing.T) {
	h := newHarness(t)
	h.registerFabricated(t, h.fabricated)

	result, err := h.gateway(t, config.TierPaid, config.EnvTest).
		Invoke(context.Background(), h.request(h.fabricated, map[string]any{"note": "a payload"}))
	if err != nil {
		t.Fatalf("the exempt fixture was refused: %v", err)
	}
	if result.Grounding.State != ai.GroundingNotRequired {
		t.Errorf("the exempt agent's verdict is %s", result.Grounding.State)
	}

	var state string
	if err := h.SQL.QueryRow(
		`SELECT grounding_state FROM core.ai_interaction WHERE id = $1`, result.InteractionID).
		Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "NOT_REQUIRED" {
		t.Errorf("the recorded verdict is %s, not NOT_REQUIRED", state)
	}

	// The exemption is argued for, in the register, in a sentence. A boolean nobody had to justify
	// is the shape this whole design refuses.
	var reason string
	if err := h.SQL.QueryRow(
		`SELECT grounding_exempt_reason FROM core.ai_agent WHERE agent_code = 'gateway.echo'`).
		Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(reason)) < 20 {
		t.Errorf("the exemption's reason is %q", reason)
	}
}

// TestGroundingIsRequiredWhenTheRegisterCannotBeRead.
//
// Fail closed, the same rule the tier guard follows: a lookup that failed must never become
// permission. Simulated by asking about an agent that is not in the register at all, which is what
// a failed read looks like to this function.
func TestGroundingIsRequiredWhenTheRegisterCannotBeRead(t *testing.T) {
	h := newHarness(t)
	required, _, err := h.store.GroundingRequired(context.Background(), "no.such.agent")
	if err == nil {
		t.Fatal("reading an agent that does not exist did not fail")
	}
	if !required {
		t.Error("a failed lookup answered 'grounding is not required', which is a database outage " +
			"becoming permission to show a physician an unchecked summary")
	}
}

// TestACacheHitIsGroundedToo.
//
// The cache is keyed on the hash of the same payload, so the verdict cannot differ — and that is
// exactly why this test exists rather than being skipped as unnecessary. The guarantee is not "the
// verdict is right on the cached path", it is "there is no exit from this package that does not run
// the check", and an exit that happens to be safe today is one somebody changes tomorrow.
func TestACacheHitIsGroundedToo(t *testing.T) {
	h := newHarness(t)
	h.registerFabricated(t, h.fabricated)

	// The echo agent is the one with a cache TTL, and it is exempt — so the assertion here is that
	// the cached path produced a *verdict* at all, which is what proves it went through deliver.
	gateway := h.gateway(t, config.TierPaid, config.EnvTest)
	payload := map[string]any{"note": "identical both times"}
	if _, err := gateway.Invoke(context.Background(), h.request(h.fabricated, payload)); err != nil {
		t.Fatal(err)
	}
	second, err := gateway.Invoke(context.Background(), h.request(h.fabricated, payload))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Cached {
		t.Fatal("the second identical call was not served from the cache; this test is not testing what it says")
	}
	if second.Grounding.State == "" {
		t.Error("a cache hit came back with no grounding verdict at all, which means it did not go " +
			"through the one function that could have checked it")
	}

	var state string
	if err := h.SQL.QueryRow(
		`SELECT grounding_state FROM core.ai_interaction WHERE id = $1`, second.InteractionID).
		Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == "NOT_CHECKED" {
		t.Error("the cached call is recorded as never having been checked")
	}
}

// TestAReviewedDefectCannotBeReviewedTwice.
//
// The 409 the API documents. Two reviewers reaching the same defect is ordinary; the second
// silently overwriting the first's judgement about whether a model invented something is not.
func TestAReviewedDefectCannotBeReviewedTwice(t *testing.T) {
	h := newHarness(t)
	h.answering(t, synthesisAnswer("HbA1c is 12.4 % [obs.hba1c:2026-09-14]."))
	if _, err := h.gateway(t, config.TierPaid, config.EnvTest).
		Invoke(context.Background(), h.groundingRequest(groundedPayload())); !errors.Is(err, ai.ErrUngrounded) {
		t.Fatalf("the fabricated value was not refused: %v", err)
	}
	defects := h.defects(t, h.latest(t).ID)
	if len(defects) == 0 {
		t.Fatal("no defect to review")
	}

	reviewed, err := h.store.ReviewDefect(context.Background(), ai.Reviewing{
		DefectID: defects[0].ID, Facility: h.facility, Classification: "TRUE_POSITIVE",
		At: h.clock.Now(),
	})
	if err != nil {
		t.Fatalf("recording a verdict: %v", err)
	}
	if reviewed.Status != "REVIEWED" || reviewed.Classification != "TRUE_POSITIVE" {
		t.Errorf("the defect is %s / %q", reviewed.Status, reviewed.Classification)
	}

	_, err = h.store.ReviewDefect(context.Background(), ai.Reviewing{
		DefectID: defects[0].ID, Facility: h.facility, Classification: "FALSE_POSITIVE",
		Note: "a second reviewer disagreeing, twenty characters of it", At: h.clock.Now(),
	})
	if !errors.Is(err, ai.ErrDefectClosed) {
		t.Errorf("the second verdict was accepted: %v", err)
	}
}

// TestSayingTheCheckWasWrongRequiresAReason.
//
// The false-positive rate is the number any future argument for loosening the check will be made
// with, and one assembled from unexplained clicks is a number that loosens it. Both the handler and
// the column refuse; this is the column.
func TestSayingTheCheckWasWrongRequiresAReason(t *testing.T) {
	h := newHarness(t)
	h.answering(t, synthesisAnswer("HbA1c is 12.4 % [obs.hba1c:2026-09-14]."))
	if _, err := h.gateway(t, config.TierPaid, config.EnvTest).
		Invoke(context.Background(), h.groundingRequest(groundedPayload())); !errors.Is(err, ai.ErrUngrounded) {
		t.Fatalf("the fabricated value was not refused: %v", err)
	}
	defects := h.defects(t, h.latest(t).ID)

	_, err := h.store.ReviewDefect(context.Background(), ai.Reviewing{
		DefectID: defects[0].ID, Facility: h.facility, Classification: "FALSE_POSITIVE",
		Note: "wrong", At: h.clock.Now(),
	})
	if err == nil {
		t.Fatal("a five-character disagreement with the check was accepted")
	}
	if !strings.Contains(err.Error(), "ai_grounding_defect_disagreement_is_argued") {
		t.Errorf("refused by %v, not by the constraint that exists for this", err)
	}
}

// TestTheFalsePositiveRateIsAbsentUntilSomebodyHasReviewedSomething.
//
// Criterion 2, in production rather than on the frozen set. A validator nobody has checked has an
// *unknown* false-positive rate, and a system that reported 0% for it would be publishing the most
// misleading number available to it.
func TestTheFalsePositiveRateIsAbsentUntilSomebodyHasReviewedSomething(t *testing.T) {
	h := newHarness(t)
	h.answering(t, synthesisAnswer("HbA1c is 12.4 % [obs.hba1c:2026-09-14]."))
	if _, err := h.gateway(t, config.TierPaid, config.EnvTest).
		Invoke(context.Background(), h.groundingRequest(groundedPayload())); !errors.Is(err, ai.ErrUngrounded) {
		t.Fatalf("the fabricated value was not refused: %v", err)
	}

	health, err := h.store.Health(context.Background(), h.facility, h.clock.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var synthesis *ai.GroundingHealth
	for i, row := range health {
		if row.AgentCode == "clinical.synthesis" {
			synthesis = &health[i]
		}
	}
	if synthesis == nil {
		t.Fatal("the health query does not report the agent that was just called")
	}
	if synthesis.Failed != 1 || synthesis.DefectsOpen == 0 {
		t.Errorf("failed=%d open=%d", synthesis.Failed, synthesis.DefectsOpen)
	}
	if rate := synthesis.FalsePositiveRate(); rate != nil {
		t.Errorf("a false-positive rate of %.2f was reported before anybody reviewed anything", *rate)
	}

	defects := h.defects(t, h.latest(t).ID)
	if _, err := h.store.ReviewDefect(context.Background(), ai.Reviewing{
		DefectID: defects[0].ID, Facility: h.facility, Classification: "TRUE_POSITIVE",
		At: h.clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	health, err = h.store.Health(context.Background(), h.facility, h.clock.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range health {
		if row.AgentCode != "clinical.synthesis" {
			continue
		}
		rate := row.FalsePositiveRate()
		if rate == nil {
			t.Fatal("no rate is reported after a review")
		}
		if *rate != 0 {
			t.Errorf("one true positive gives a false-positive rate of %.2f", *rate)
		}
	}
}

// verifyGroundingInvariants runs the database's own checks over the whole table, which is the third
// enforcement: the one that catches a row that arrived some other way.
func (h *harness) verifyGroundingInvariants(t *testing.T) {
	t.Helper()
	rows, err := h.SQL.Query(
		`SELECT schema_name, function_name FROM ops.invariant WHERE sequence BETWEEN 106 AND 109 ORDER BY sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names [][2]string
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			t.Fatal(err)
		}
		names = append(names, [2]string{schema, name})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// A loop over an empty list passes, which is the shape of a check that has stopped checking.
	if len(names) != 4 {
		t.Fatalf("%d grounding invariants are registered, want 4", len(names))
	}
	for _, one := range names {
		if _, err := h.SQL.Exec(`SELECT ` + one[0] + `.` + one[1] + `()`); err != nil {
			t.Errorf("%s: %v", one[1], err)
		}
	}
}

// synthesisAnswer is a minimal answer in the synthesis agent's shape, with one sentence in it.
func synthesisAnswer(narrative string) map[string]any {
	return map[string]any{
		"narrative_en":           narrative + " The record is otherwise unremarkable at this visit.",
		"key_points":             []any{"One point"},
		"suggested_diagnoses":    []any{},
		"missing_investigations": []any{},
		"draft_medications":      []any{},
		"red_flags":              []any{},
		"confidence":             0.5,
		"citations":              []any{},
	}
}
