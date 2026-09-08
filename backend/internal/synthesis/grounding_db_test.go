package synthesis_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/config"
	"github.com/AmlanWTK/DTHCMS/backend/internal/synthesis"
)

// The block, at the one table a physician's screen reads (CP72).
//
// `internal/ai` proves the check catches a fabricated value and refuses to hand it back. This file
// proves the other half, which is the half the checkpoint is actually about: **that a summary
// carrying such a value cannot reach the state the screen renders as a summary.**
//
// Three enforcements, tested separately because "each independently sufficient" is a claim about
// each of them on its own:
//
//	the gateway     TestAFabricatedLabValueNeverReachesThePhysiciansScreen
//	the constraint  TestTheDatabaseRefusesAReadySummaryThatWasNotGrounded
//	this module     TestASummaryAssembledWithoutAVerdictIsWithheldRatherThanShown

// corrupted makes the model return a well-formed answer with a fabricated laboratory value in it.
//
// Deliberately a *plausible* value in a well-formed answer that cites a real fact reference: this
// is the failure mode §10.2 is about, and it is the one a schema validator cannot see.
func corrupted(narrative string) func(ai.ProviderRequest) (ai.ProviderResponse, error) {
	return func(ai.ProviderRequest) (ai.ProviderResponse, error) {
		encoded, err := json.Marshal(map[string]any{
			"narrative_en": narrative,
			"key_points":   []any{"Glycaemic control has deteriorated since the last visit."},
			"confidence":   0.7,
			// The optional arrays are omitted rather than filled, exactly as the mock's own
			// example does: an answer that drafted a medication would be refused by the drug arm
			// and this test would pass for the wrong reason.
			"suggested_diagnoses":    []any{},
			"missing_investigations": []any{},
			"draft_medications":      []any{},
			"red_flags":              []any{},
			"citations":              []any{},
		})
		if err != nil {
			return ai.ProviderResponse{}, err
		}
		return ai.ProviderResponse{
			Text: string(encoded), ModelVersion: ai.MockModelVersion,
			InputTokens: 900, OutputTokens: 120, FinishReason: "STOP",
		}, nil
	}
}

// TestAFabricatedLabValueNeverReachesThePhysiciansScreen is the checkpoint's manual verification,
// as a test.
//
// A model answer is deliberately corrupted with an HbA1c the record does not contain. The summary
// must not become READY, the narrative must not be stored where the screen reads it, and the screen
// must say what happened rather than showing an empty panel.
func TestAFabricatedLabValueNeverReachesThePhysiciansScreen(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")

	r.provider.Respond = corrupted(
		"The patient attends for review. HbA1c is 12.4 % and has risen sharply since the last " +
			"visit, which is poor control and warrants escalation of therapy today.")

	run := r.pending(t)
	// The queue is told nothing went wrong, because nothing the queue can fix went wrong. Retrying
	// would spend the SLA on the same answer and destroy the evidence.
	if err := r.service.Perform(context.Background(), r.actor(), synthesis.JobArgs{
		SynthesisID: run.ID, VisitID: r.visit, Generation: run.Generation,
	}); err != nil {
		t.Fatalf("an ungrounded answer was returned to the queue as retryable: %v", err)
	}

	view, err := r.service.Current(context.Background(), r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == synthesis.Ready {
		t.Fatalf("a summary quoting an HbA1c the record does not contain is READY and on the screen")
	}
	if view.State != synthesis.Failed {
		t.Fatalf("the run is %s, want FAILED", view.State)
	}
	if view.Run.FailureKind != synthesis.FailureUngrounded {
		t.Errorf("the failure is classified %s, so the screen would say the wrong thing",
			view.Run.FailureKind)
	}
	if view.Run.Grounding != synthesis.GroundingFailed {
		t.Errorf("the run records its grounding verdict as %q", view.Run.Grounding)
	}
	if view.Run.GroundingFindings != 1 {
		t.Errorf("the run records %d findings; the screen cannot say how many rows are waiting "+
			"in the defect queue", view.Run.GroundingFindings)
	}
	// The narrative is not on the row the screen reads. This is the assertion the checkpoint's
	// manual verification is really about: not "an error was logged" but "the sentence is not
	// there".
	if len(view.Run.Output) > 0 && strings.Contains(string(view.Run.Output), "12.4") {
		t.Errorf("the fabricated value is in the stored output: %s", view.Run.Output)
	}
	if !view.Degraded {
		t.Error("the screen is not told to show the structured record instead")
	}
	if !strings.Contains(view.MessageEN, "withheld") {
		t.Errorf("the screen says %q, which does not tell the physician a summary is being withheld",
			view.MessageEN)
	}
	if view.MessageBN == "" || view.MessageBN == view.MessageEN {
		t.Error("the withheld message is not bilingual")
	}
	// D-15: the physician still reads the whole assembled record.
	if len(view.Run.Context.Facts) == 0 {
		t.Error("the failed run kept no context, so the degraded screen has nothing to show")
	}

	// And the evidence is where somebody can act on it.
	var status, groundingState string
	var findings int
	if err := r.SQL.QueryRow(
		`SELECT status, grounding_state, grounding_findings FROM core.ai_interaction
		  WHERE agent_code = $1 ORDER BY started_at DESC LIMIT 1`, synthesis.AgentCode).
		Scan(&status, &groundingState, &findings); err != nil {
		t.Fatal(err)
	}
	if status != "UNGROUNDED" || groundingState != "FAILED" || findings == 0 {
		t.Errorf("the call is recorded as %s / %s with %d findings", status, groundingState, findings)
	}
	var defects int
	if err := r.SQL.QueryRow(
		`SELECT count(*) FROM core.ai_grounding_defect WHERE token = '12.4'`).Scan(&defects); err != nil {
		t.Fatal(err)
	}
	if defects == 0 {
		t.Error("no defect names the fabricated value; the evidence was discarded")
	}
	// The ledger, and this assertion is here because the manual verification found it missing.
	// The row was right, the screen was right, and `AI_SYNTHESIS_FAILED` could not be appended
	// because the event schema's list of failure kinds had never heard of UNGROUNDED — a hole
	// nothing else in the suite was looking at, in the one record a medico-legal review reads.
	if got := r.eventTypes(t); !contains(got, "AI_SYNTHESIS_FAILED") {
		t.Errorf("the ledger carries %v, with no AI_SYNTHESIS_FAILED; a withheld summary is not "+
			"in the record anybody would review afterwards", got)
	}
	r.verifyGroundingInvariants(t)
}

// TestAGroundedSummaryIsReadyAndSaysSo is the control for the test above.
//
// Without it, everything in this file would be satisfied by a pipeline that withheld every summary,
// which is the failure a block-everything check has and which no test of the block can see.
func TestAGroundedSummaryIsReadyAndSaysSo(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")

	run := r.pending(t)
	if err := r.service.Perform(context.Background(), r.actor(), synthesis.JobArgs{
		SynthesisID: run.ID, VisitID: r.visit, Generation: run.Generation,
	}); err != nil {
		t.Fatalf("performing the run: %v", err)
	}
	view, err := r.service.Current(context.Background(), r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != synthesis.Ready {
		t.Fatalf("a grounded summary is %s: %s", view.State, view.Run.FailureDetail)
	}
	if view.Run.Grounding != synthesis.GroundingPassed {
		t.Errorf("a READY summary records its verdict as %q; the check constraint should have "+
			"refused the row", view.Run.Grounding)
	}
	if view.Run.GroundingFindings != 0 {
		t.Errorf("a passing verdict carries %d findings", view.Run.GroundingFindings)
	}
	r.verifyGroundingInvariants(t)
}

// TestTheDatabaseRefusesAReadySummaryThatWasNotGrounded.
//
// The second enforcement on its own. Every line of Go in `internal/synthesis` and `internal/ai`
// could be wrong and this would still hold — which is what makes it a second guarantee rather than
// a second copy of the first.
func TestTheDatabaseRefusesAReadySummaryThatWasNotGrounded(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")
	run := r.pending(t)
	if err := r.service.Perform(context.Background(), r.actor(), synthesis.JobArgs{
		SynthesisID: run.ID, VisitID: r.visit, Generation: run.Generation,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := r.SQL.Exec(
		`UPDATE core.ai_synthesis SET grounding_state = 'NOT_CHECKED' WHERE id = $1`, run.ID)
	if err == nil {
		t.Fatal("the database let a READY summary drop its grounding verdict")
	}
	if !strings.Contains(err.Error(), "ai_synthesis_ready_is_grounded") {
		t.Errorf("refused by %v, not by the constraint that exists for this", err)
	}

	// The same, for the state that would let an exemption on the agent quietly disable the block.
	// `core.ai_synthesis` does not accept NOT_REQUIRED at all: flipping `core.ai_agent` to exempt
	// the synthesis agent would stop summaries being stored rather than stop them being checked,
	// which is loud in the right direction.
	_, err = r.SQL.Exec(
		`UPDATE core.ai_synthesis SET grounding_state = 'NOT_REQUIRED' WHERE id = $1`, run.ID)
	if err == nil {
		t.Fatal("the database accepted a READY summary whose agent had been exempted")
	}
}

// TestASummaryAssembledWithoutAVerdictIsWithheldRatherThanShown is the third enforcement.
//
// The one inside this module, which catches a future caller here building a terminal write by hand
// and forgetting to carry the gateway's verdict across. Reached by exempting the synthesis agent in
// the register — the one way to make the gateway return a passing `Result` whose verdict is not
// PASSED — and asserting that the run is withheld rather than stored.
func TestASummaryAssembledWithoutAVerdictIsWithheldRatherThanShown(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	if _, err := r.SQL.Exec(
		`UPDATE core.ai_agent
		    SET grounding_required = false,
		        grounding_exempt_reason = 'a deliberate misconfiguration, to prove the module refuses it'
		  WHERE agent_code = $1`, synthesis.AgentCode); err != nil {
		t.Fatal(err)
	}
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")

	run := r.pending(t)
	if err := r.service.Perform(context.Background(), r.actor(), synthesis.JobArgs{
		SynthesisID: run.ID, VisitID: r.visit, Generation: run.Generation,
	}); err != nil {
		t.Fatalf("performing the run: %v", err)
	}
	view, err := r.service.Current(context.Background(), r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == synthesis.Ready {
		t.Fatal("exempting the synthesis agent in the register put an unchecked summary on the screen")
	}
	if view.Run.FailureKind != synthesis.FailureUngrounded {
		t.Errorf("the withheld run is classified %s", view.Run.FailureKind)
	}
	r.verifyGroundingInvariants(t)
}

// verifyGroundingInvariants runs the four CP72 invariants over the whole database.
func (r *rig) verifyGroundingInvariants(t *testing.T) {
	t.Helper()
	rows, err := r.SQL.Query(
		`SELECT schema_name, function_name FROM ops.invariant WHERE sequence BETWEEN 106 AND 109`)
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
	if len(names) != 4 {
		t.Fatalf("%d grounding invariants are registered, want 4", len(names))
	}
	for _, one := range names {
		if _, err := r.SQL.Exec(`SELECT ` + one[0] + `.` + one[1] + `()`); err != nil {
			t.Errorf("%s: %v", one[1], err)
		}
	}
}
