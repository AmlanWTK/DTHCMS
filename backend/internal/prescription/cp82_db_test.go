package prescription_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinicalterm"
	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/config"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
)

// AI prescribing suggestions and the physician's decision (CP82, D-28).
//
// # What these tests are trying to be
//
// The checkpoint is judged on one sentence — *no AI-suggested item can reach a SIGNED prescription
// without an explicit accept or edit event* — and the brief is explicit that proving it by failing
// to find a path is weaker than a design where the path does not exist.
//
// So the tests below are in three layers, and it is worth saying which layer each claim lives in
// because they are not equally strong:
//
//	structural   TestThereIsNoWayToMarkALineAsAIWithoutADecision walks `prescription.Addition` by
//	             reflection and asserts it has no field that could carry a suggestion id. That is a
//	             claim about the *type*, and it fails the moment somebody adds one — before any
//	             runtime behaviour changes.
//	enforced     TestTheDatabaseRefusesAnAIOriginLineWithNoDecision writes the line directly as
//	             `dthcms_projector`, the role that actually holds the grant, bypassing every line of
//	             Go in this repository. The transaction cannot commit. That is a claim about the
//	             *database*, and it survives a refactor that deletes this whole package.
//	behavioural  the rest: accept, edit, reject, unactioned, the refusals, the safety engine. These
//	             are claims about what the code does today.
//
// The first two are what the report calls the structural guarantee; the third is the ordinary kind
// of confidence. `TestTheMutationIsCaught` is the check on the checks: it inserts exactly the
// forbidden row and asserts that the boundary refuses it, so a boundary that had quietly stopped
// working would show up as a *passing* insert rather than as a silently green suite.

// ---------------------------------------------------------------------------
// The harness
// ---------------------------------------------------------------------------

// aiRig is the CP80 rig with CP82's agent attached.
type aiRig struct {
	*rig
	answer  func(ai.ProviderRequest) (ai.ProviderResponse, error)
	allergy string
	facts   stubFacts
}

// stubBriefing stands in for the composition root's synthesis bridge.
//
// It returns a small, real-shaped CP71 context: a `facts` array with references the grounding
// check can resolve, and the numbers those facts carry. Small on purpose — a fixture that copied a
// whole assembled context would make every failure here a failure to read a hundred lines of JSON.
type stubBriefing struct{ patient uuid.UUID }

func (b stubBriefing) PrescribingBriefing(_ context.Context,
	_, patient, _ uuid.UUID) (ai.Subject, map[string]any, error) {

	return ai.Subject{
			PatientID: patient, AgeMonths: 738, Sex: "female",
			Identifiers: map[string]string{"name_en": "Rahima Begum", "phone": "+8801711111801"},
		}, map[string]any{
			"demographics": map[string]any{"age_text": "sixty-one years", "sex": "female"},
			"facts": []any{
				map[string]any{"ref": "dx.type_2_diabetes_mellitus", "label": "Type 2 diabetes mellitus"},
				map[string]any{"ref": "obs.hba1c:2026-09-01", "label": "HbA1c", "value": "9.1"},
			},
			"gaps": []any{},
		}, nil
}

// stubAllergyGate answers CP54's question with whatever the test set.
type stubAllergyGate struct{ status *string }

func (g stubAllergyGate) AllergyStatus(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return *g.status, nil
}

// newAIRig builds the CP80 rig with a gateway, a briefing and an allergy gate attached.
//
// The gateway is on the **mock tier**, which is what the free-tier guard is not about: CP70 proves
// the guard, and a test that had to register a synthetic subject to exercise CP82's sifting would
// be testing CP70 again through a longer path. The provider is `ai.Mock` with `Respond` set, so
// each test writes the exact answer whose handling it is asserting.
func newAIRig(t *testing.T) *aiRig {
	t.Helper()
	base := newRig(t)
	// The clinical picture CP80's tests already use: an eGFR of 25, which is the plan's own
	// metformin scenario and therefore gives the origin-blindness test a real finding to compare
	// rather than two empty results.
	out := &aiRig{rig: base, allergy: "NO_KNOWN_ALLERGY",
		facts: stubFacts{at: base.clock.Now()}}
	// The vocabulary route declares `reference.read` (migration 00067), which CP80's rig has no
	// reason to hold. Added here rather than in `newRig` so that CP80's tests keep asserting
	// against the permission set CP80 actually needs.
	base.held = append(base.held, prescription.PermSuggestReasons)

	registry, err := ai.LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	minimiser, err := ai.NewMinimiser("cp82-test-pepper")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	provider := ai.NewMock()
	provider.Respond = func(req ai.ProviderRequest) (ai.ProviderResponse, error) {
		if out.answer == nil {
			return ai.ProviderResponse{Text: `{"suggestions":[]}`,
				ModelVersion: ai.MockModelVersion, FinishReason: "STOP"}, nil
		}
		return out.answer(req)
	}
	gateway := ai.NewGateway(ai.GatewayConfig{
		Store: ai.NewStore(base.pool), Registry: registry, Provider: provider,
		Minimiser: minimiser, Tier: config.TierMock, Env: config.EnvTest,
		Facility: base.facility, Clock: base.clock, Logger: logger,
	})
	if err := registry.Deploy(base.ctx(), ai.NewStore(base.pool), base.clock.Now()); err != nil {
		t.Fatal(err)
	}

	base.service = base.service.WithTerms(clinicalterm.NewCache(base.pool)).
		WithSuggestions(prescription.SuggestConfig{
			Gateway:  gateway,
			Briefing: stubBriefing{patient: base.patient},
			Allergy:  stubAllergyGate{status: &out.allergy},
		}, logger)

	// Rebuild the handlers so the HTTP routes see the service the agent is attached to, and wire
	// the safety engine, which the origin-blindness test drives through its real route.
	base.rebuildHandlers(t, out.facts)
	return out
}

// suggestion is the answer a well-behaved model gives: one metformin, grounded in the two facts
// the briefing carries.
//
// The dose is "500 mg" and not "1 tablet", and that is not arbitrary. The gateway's grounding
// check refuses any number in the model's prose that is not in the payload it was shown, and "500"
// is in the payload because the candidate's strength is. This is a real property of the design and
// it is worth a test reader knowing it: a model that invents a dose has its whole answer refused.
func (r *aiRig) proposeMetformin(t *testing.T) {
	t.Helper()
	r.answer = func(req ai.ProviderRequest) (ai.ProviderResponse, error) {
		ref, strength := refFromPrompt(t, req.User, "Metformin")
		body := map[string]any{"suggestions": []any{map[string]any{
			"product_ref": ref, "dose": strength, "frequency": "twice daily",
			"duration_days": 30, "route": "oral",
			"rationale_en": "Glycaemic control is above target on the current regimen and metformin is the usual first step here.",
			"rationale_bn": "বর্তমান ব্যবস্থাপত্রে রক্তে শর্করার নিয়ন্ত্রণ লক্ষ্যমাত্রার বাইরে; এখানে মেটফরমিনই সাধারণত প্রথম পছন্দ।",
			"basis":        []any{"obs.hba1c:2026-09-01", "dx.type_2_diabetes_mellitus"},
		}}}
		encoded, _ := json.Marshal(body)
		return ai.ProviderResponse{Text: string(encoded),
			ModelVersion: ai.MockModelVersion, FinishReason: "STOP"}, nil
	}
}

// refFromPrompt pulls a handle out of the shortlist the server actually put in the prompt.
//
// Out of the prompt and not out of the database, because the model is supposed to choose from that
// list and nothing else: a test whose "model" read an id from `core.medication_product` would be
// testing a model that cheated, and would pass even if the server had sent the wrong shortlist or
// no shortlist at all.
func refFromPrompt(t *testing.T, prompt, generic string) (string, string) {
	t.Helper()
	var payload struct {
		Candidates []struct {
			Ref         string `json:"ref"`
			GenericName string `json:"generic"`
			Strength    string `json:"strength"`
		} `json:"formulary_candidates"`
	}
	start := strings.Index(prompt, "{")
	end := strings.LastIndex(prompt, "}")
	if start < 0 || end < start {
		t.Fatalf("the prompt carries no payload")
	}
	if err := json.Unmarshal([]byte(prompt[start:end+1]), &payload); err != nil {
		t.Fatalf("the prompt payload does not decode: %v", err)
	}
	for _, c := range payload.Candidates {
		if strings.Contains(strings.ToLower(c.GenericName), strings.ToLower(generic)) {
			return c.Ref, c.Strength
		}
	}
	t.Fatalf("the shortlist the server sent has no %s in it", generic)
	return "", ""
}

// ask runs the agent and returns the run.
func (r *aiRig) ask(t *testing.T, sheet uuid.UUID) prescription.Run {
	t.Helper()
	run, err := r.service.Suggest(r.ctx(), sheet)
	if err != nil {
		t.Fatalf("asking for suggestions: %v", err)
	}
	return run
}

// ---------------------------------------------------------------------------
// 1. The structural claim
// ---------------------------------------------------------------------------

// TestThereIsNoWayToMarkALineAsAIWithoutADecision is the structural half of criterion 1.
//
// `Service.AddItem` is the only exported way to put a line on a prescription, and it takes exactly
// one argument: an [prescription.Addition]. If that struct has no field naming a suggestion, then
// no caller anywhere — in this repository or in one written later against this package — can add a
// line that claims to have come from the AI. The origin is set by an unexported function whose
// only caller is `Decide`.
//
// This is a test about a *type*, so it fails at the moment somebody adds the field, in the same
// commit, rather than when some later behaviour changes. That is the difference between a test
// that fails to find a path and a design where the path does not exist.
func TestThereIsNoWayToMarkALineAsAIWithoutADecision(t *testing.T) {
	addition := reflect.TypeOf(prescription.Addition{})
	for i := 0; i < addition.NumField(); i++ {
		name := strings.ToLower(addition.Field(i).Name)
		if strings.Contains(name, "suggestion") || strings.Contains(name, "aiorigin") ||
			strings.Contains(name, "origin") {
			t.Fatalf("prescription.Addition has a field %q. The whole of CP82's criterion 1 on "+
				"the Go side is that there is no exported way to mark a prescription line as "+
				"having come from an AI suggestion: the origin is set by Service.Decide, inside "+
				"the transaction that also writes the decision. A field here is a way for any "+
				"caller to claim a provenance it did not earn. If this field is deliberate, the "+
				"database's deferred constraint still refuses a line with no decision behind it — "+
				"but the compiler no longer helps, and this test is where that trade is argued.",
				addition.Field(i).Name)
		}
	}

}

// TestTheDatabaseRefusesAnAIOriginLineWithNoDecision is the enforced half.
//
// It writes the row as `dthcms_projector` — the role that actually holds INSERT on
// `read.prescription_item` — so it bypasses every line of Go in this repository. A guarantee that
// rests on a Go function is a guarantee that ends with the refactor that removes it; this one is a
// deferred constraint trigger and it refuses the **commit**.
func TestTheDatabaseRefusesAnAIOriginLineWithNoDecision(t *testing.T) {
	r := newAIRig(t)
	r.proposeMetformin(t)
	sheet := r.draftEmpty(t)
	run := r.ask(t, sheet.ID)
	if len(run.Suggestions) != 1 {
		t.Fatalf("the agent offered %d suggestions, want 1 (state %s/%s detail %q dropped %v)", len(run.Suggestions), run.State, run.Refusal, run.FailureDetail, run.DroppedReasons)
	}
	offered := run.Suggestions[0]

	tx, err := r.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SET LOCAL ROLE dthcms_projector`); err != nil {
		t.Fatal(err)
	}
	// Everything a real accepted line has, except the decision.
	if _, err := tx.Exec(`
		INSERT INTO read.prescription_item (
		  id, prescription_id, facility_id, line_no, product_id, product_label, generic_name,
		  dose, frequency, ai_suggestion_id, recorded_at, recorded_by, event_id, global_seq)
		VALUES (gen_random_uuid(), $1, $2, 99, $3, 'Comet', 'Metformin hydrochloride',
		        '500 mg', 'twice daily', $4, now(), $5, gen_random_uuid(), 999999999)`,
		sheet.ID, r.facility, offered.ProductID, offered.ID, r.user); err != nil {
		t.Fatalf("the INSERT itself was refused, which is not what this test is about: %v", err)
	}
	err = tx.Commit()
	if err == nil {
		t.Fatal("a prescription line claiming to come from an AI suggestion committed with no " +
			"physician decision behind it. CP82's acceptance criterion 1 is that this cannot " +
			"happen, and the deferred constraint trigger in migration 00069 is what is supposed " +
			"to stop it.")
	}
	if !strings.Contains(err.Error(), "no physician decision") {
		t.Fatalf("the commit was refused, but not by the check that is supposed to refuse it: %v", err)
	}
	t.Logf("refused at COMMIT, as designed: %v", err)
}

// TestNoAISuggestedItemReachesASignedPrescriptionWithoutADecision drives the whole path and then
// asks the database the same question invariant 130 asks.
func TestNoAISuggestedItemReachesASignedPrescriptionWithoutADecision(t *testing.T) {
	r := newAIRig(t)
	r.proposeMetformin(t)
	sheet := r.draftEmpty(t)
	run := r.ask(t, sheet.ID)
	offered := run.Suggestions[0]

	if _, err := r.service.Decide(r.ctx(), prescription.Deciding{
		PrescriptionID: sheet.ID, SuggestionID: offered.ID,
		Kind: prescription.DecisionAccepted,
	}); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	r.advance(t, sheet.ID, prescription.StatusSigned)

	// The invariant, run against the real database as the verifier runs it. This is the check that
	// would catch a line written by a path nobody has thought of yet.
	if _, err := r.SQL.Exec(`SELECT core.assert_ai_lines_were_decided()`); err != nil {
		t.Fatalf("invariant 130 fails after a signed prescription with an accepted AI line: %v", err)
	}

	// And the same question asked directly, because an invariant that silently stopped selecting
	// anything would also pass.
	var orphans int
	if err := r.SQL.QueryRow(`
		SELECT count(*) FROM read.prescription_item i
		  JOIN read.prescription p ON p.id = i.prescription_id
		 WHERE p.status = 'SIGNED' AND i.ai_suggestion_id IS NOT NULL
		   AND NOT EXISTS (SELECT 1 FROM core.ai_prescribing_decision d
		                    WHERE d.suggestion_id = i.ai_suggestion_id
		                      AND d.prescription_item_id = i.id
		                      AND d.decision IN ('ACCEPTED','EDITED'))`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Fatalf("%d AI-origin lines are on a signed prescription with no decision", orphans)
	}

	// The counterpart: the line that *is* there says where it came from, and the decision names it
	// back. A test that only counted zero orphans would also pass against a system that had
	// recorded no AI line at all.
	var marked int
	if err := r.SQL.QueryRow(`
		SELECT count(*) FROM read.prescription_item i
		  JOIN read.prescription p ON p.id = i.prescription_id
		 WHERE p.id = $1 AND i.ai_suggestion_id = $2`, sheet.ID, offered.ID).Scan(&marked); err != nil {
		t.Fatal(err)
	}
	if marked != 1 {
		t.Fatalf("the signed prescription carries %d lines from the suggestion, want 1", marked)
	}
}

// TestTheMutationIsCaught is the check on the check.
//
// The brief's instruction, taken literally: introduce the forbidden path and confirm a test fails.
// The path is "copy a suggestion into an item without a decision event", and the most direct form
// of it is the INSERT below — which is what a future `Service.acceptSuggestion` that forgot the
// decision row would produce, byte for byte, at the moment of commit.
//
// This test **passes when the boundary works**, because what it asserts is the refusal. Its value
// is that it fails the day the boundary stops working, rather than the suite going quietly green
// with the guarantee gone — which is what would happen to a suite whose only evidence was that no
// accepted line was ever missing its decision.
func TestTheMutationIsCaught(t *testing.T) {
	r := newAIRig(t)
	r.proposeMetformin(t)
	sheet := r.draftEmpty(t)
	offered := r.ask(t, sheet.ID).Suggestions[0]

	// The mutation: an item carrying the suggestion's id, written as the application would write
	// it, with the decision insert deliberately left out.
	tx, err := r.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SET LOCAL ROLE dthcms_projector`); err != nil {
		t.Fatal(err)
	}
	itemID := uuid.New()
	if _, err := tx.Exec(`
		INSERT INTO read.prescription_item (
		  id, prescription_id, facility_id, line_no, product_id, product_label, generic_name,
		  dose, frequency, ai_suggestion_id, recorded_at, recorded_by, event_id, global_seq)
		VALUES ($6, $1, $2, 98, $3, 'Comet', 'Metformin hydrochloride',
		        '500 mg', 'twice daily', $4, now(), $5, gen_random_uuid(), 999999998)`,
		sheet.ID, r.facility, offered.ProductID, offered.ID, r.user, itemID); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("MUTATION SURVIVED: a suggestion was copied onto a prescription with no decision " +
			"behind it and the transaction committed. Everything else in this file is now " +
			"testing a boundary that is not there.")
	} else {
		t.Logf("mutation refused: %v", err)
	}

	// And the invariant would have found it had the trigger been dropped. Asserted by inserting
	// the row with the trigger disabled — which is the only way to reach the state invariant 130
	// exists for — and then checking that the invariant says so.
	if _, err := r.SQL.Exec(`ALTER TABLE read.prescription_item DISABLE TRIGGER prescription_item_ai_origin_is_decided`); err != nil {
		t.Fatalf("the constraint trigger is not present under the name the migration gives it: %v", err)
	}
	if _, err := r.SQL.Exec(`SET ROLE dthcms_projector`); err != nil {
		t.Fatal(err)
	}
	_, insertErr := r.SQL.Exec(`
		INSERT INTO read.prescription_item (
		  id, prescription_id, facility_id, line_no, product_id, product_label, generic_name,
		  dose, frequency, ai_suggestion_id, recorded_at, recorded_by, event_id, global_seq)
		VALUES ($6, $1, $2, 97, $3, 'Comet', 'Metformin hydrochloride',
		        '500 mg', 'twice daily', $4, now(), $5, gen_random_uuid(), 999999997)`,
		sheet.ID, r.facility, offered.ProductID, offered.ID, r.user, uuid.New())
	if _, err := r.SQL.Exec(`RESET ROLE`); err != nil {
		t.Fatal(err)
	}
	if insertErr != nil {
		t.Fatalf("insert with the trigger disabled: %v", insertErr)
	}
	if _, err := r.SQL.Exec(`SELECT core.assert_ai_lines_were_decided()`); err == nil {
		t.Fatal("MUTATION SURVIVED: with the constraint trigger disabled, invariant 130 did not " +
			"notice an AI-origin line with no decision behind it. The invariant is the second " +
			"layer and it is the one that is supposed to survive a dropped trigger.")
	} else {
		t.Logf("invariant 130 caught it: %v", err)
	}
	if _, err := r.SQL.Exec(`ALTER TABLE read.prescription_item ENABLE TRIGGER prescription_item_ai_origin_is_decided`); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// 2. Ignoring the panel
// ---------------------------------------------------------------------------

// TestIgnoringThePanelProducesAPrescriptionWithNoAIInvolvement is §1's first consequence.
func TestIgnoringThePanelProducesAPrescriptionWithNoAIInvolvement(t *testing.T) {
	r := newAIRig(t)
	r.proposeMetformin(t)
	sheet := r.draftEmpty(t)
	run := r.ask(t, sheet.ID)
	if len(run.Suggestions) == 0 {
		t.Fatal("this test needs a suggestion to ignore")
	}

	// The physician ignores the panel entirely and signs.
	r.advance(t, sheet.ID, prescription.StatusSigned)

	signed, err := r.store.ByID(r.ctx(), sheet.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range signed.Items {
		var origin *uuid.UUID
		if err := r.SQL.QueryRow(`SELECT ai_suggestion_id FROM read.prescription_item WHERE id = $1`,
			item.ID).Scan(&origin); err != nil {
			t.Fatal(err)
		}
		if origin != nil {
			t.Errorf("line %d on an ignored-panel prescription came from a suggestion", item.LineNo)
		}
	}

	// And nothing anywhere says a physician decided anything.
	var decisions int
	if err := r.SQL.QueryRow(`
		SELECT count(*) FROM core.ai_prescribing_decision d
		  JOIN core.ai_prescribing_suggestion s ON s.id = d.suggestion_id
		 WHERE s.prescription_id = $1`, sheet.ID).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if decisions != 0 {
		t.Errorf("%d decisions were recorded against a panel nobody touched", decisions)
	}

	// Nor any decision event in the ledger. The three CP82 event names, asked for by name: a
	// prescription that involved no AI has no AI event on its stream.
	var events int
	if err := r.SQL.QueryRow(`
		SELECT count(*) FROM ledger.event
		 WHERE aggregate_id = $1
		   AND event_type IN ('AI_SUGGESTION_ACCEPTED','AI_SUGGESTION_EDITED','AI_SUGGESTION_REJECTED')`,
		sheet.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Errorf("%d AI decision events are on the stream of a prescription nobody used the AI for", events)
	}
}

// ---------------------------------------------------------------------------
// 3. The edit preserves the offer (§4)
// ---------------------------------------------------------------------------

// TestAnEditPreservesTheSuggestionAsOffered is §4's load-bearing sentence, asserted the way §4
// states it: the model said 500 mg, the physician wrote 850 mg, and **500 is still recoverable**.
func TestAnEditPreservesTheSuggestionAsOffered(t *testing.T) {
	r := newAIRig(t)
	r.proposeMetformin(t)
	sheet := r.draftEmpty(t)
	offered := r.ask(t, sheet.ID).Suggestions[0]
	if offered.Dose == "" {
		t.Fatal("the fixture offered no dose")
	}
	offeredDose := offered.Dose

	edited := "850 mg"
	decision, err := r.service.Decide(r.ctx(), prescription.Deciding{
		PrescriptionID: sheet.ID, SuggestionID: offered.ID,
		Kind: prescription.DecisionEdited,
		Edit: prescription.EditedLine{Dose: &edited},
	})
	if err != nil {
		t.Fatalf("editing: %v", err)
	}

	// The line says 850.
	after, err := r.store.ByID(r.ctx(), sheet.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	var line prescription.Item
	for _, item := range after.Items {
		if decision.ItemID != nil && item.ID == *decision.ItemID {
			line = item
		}
	}
	if line.Dose != "850 mg" {
		t.Fatalf("the issued line reads %q, want 850 mg", line.Dose)
	}

	// The suggestion still says 500 — read from the table, not from the value the test is holding.
	var storedDose string
	if err := r.SQL.QueryRow(`SELECT dose FROM core.ai_prescribing_suggestion WHERE id = $1`,
		offered.ID).Scan(&storedDose); err != nil {
		t.Fatal(err)
	}
	if storedDose != offeredDose {
		t.Fatalf("the suggestion as offered now reads %q. §4: the difference between what the "+
			"model said and what the physician wrote is the entire training signal, and it is "+
			"gone.", storedDose)
	}

	// And the ledger carries both halves, so the fact survives without the table.
	var payload []byte
	if err := r.SQL.QueryRow(`
		SELECT payload FROM ledger.event
		 WHERE aggregate_id = $1 AND event_type = 'AI_SUGGESTION_EDITED'`, sheet.ID).Scan(&payload); err != nil {
		t.Fatalf("no AI_SUGGESTION_EDITED event on the prescription's stream: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["offered_dose"] != offeredDose || decoded["issued_dose"] != "850 mg" {
		t.Fatalf("the ledger event carries offered=%v issued=%v; both halves have to travel",
			decoded["offered_dose"], decoded["issued_dose"])
	}

	// The database refuses to rewrite the offer, for every role including this one.
	if _, err := r.SQL.Exec(`UPDATE core.ai_prescribing_suggestion SET dose = '850 mg' WHERE id = $1`,
		offered.ID); err == nil {
		t.Fatal("a suggestion was rewritten. It is stored as offered and must never be mutated.")
	}
}

// ---------------------------------------------------------------------------
// 4. Unactioned is not rejected (§1)
// ---------------------------------------------------------------------------

// TestUnactionedIsNotRejected asserts the distinction directly, in the three places it could be
// lost: the API shape, the database, and the count a screen reads.
func TestUnactionedIsNotRejected(t *testing.T) {
	r := newAIRig(t)
	// Two suggestions: one the physician rejects, one he never looks at.
	r.answer = func(req ai.ProviderRequest) (ai.ProviderResponse, error) {
		metformin, metStrength := refFromPrompt(t, req.User, "Metformin")
		// The second one is taken from the shortlist by position rather than by name, because
		// which molecules are within the candidate cap is a property of the formulary and of the
		// cap, not something this test should assert.
		other, otherStrength := secondRef(t, req.User, metformin)
		item := func(id, strength string) map[string]any {
			return map[string]any{
				"product_ref": id, "dose": strength, "frequency": "twice daily",
				"duration_days": 30, "route": "oral",
				"rationale_en": "Worth considering on the picture the record shows.",
				"rationale_bn": "নথিতে যা আছে তার ভিত্তিতে বিবেচনার যোগ্য।",
				"basis":        []any{"obs.hba1c:2026-09-01"},
			}
		}
		encoded, _ := json.Marshal(map[string]any{"suggestions": []any{item(metformin, metStrength), item(other, otherStrength)}})
		return ai.ProviderResponse{Text: string(encoded),
			ModelVersion: ai.MockModelVersion, FinishReason: "STOP"}, nil
	}
	sheet := r.draftEmpty(t)
	run := r.ask(t, sheet.ID)
	if len(run.Suggestions) != 2 {
		t.Fatalf("the agent offered %d suggestions, want 2 (dropped: %v)", len(run.Suggestions), run.DroppedReasons)
	}
	rejected, untouched := run.Suggestions[0], run.Suggestions[1]

	if _, err := r.service.Decide(r.ctx(), prescription.Deciding{
		PrescriptionID: sheet.ID, SuggestionID: rejected.ID,
		Kind: prescription.DecisionRejected, ReasonCode: "PREFER_ALTERNATIVE",
	}); err != nil {
		t.Fatalf("rejecting: %v", err)
	}

	after, err := r.service.SuggestionsFor(r.ctx(), sheet.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range after.Suggestions {
		switch s.ID {
		case rejected.ID:
			if s.Decision == nil {
				t.Fatal("the rejected suggestion carries no decision")
			}
			if s.Decision.Kind != prescription.DecisionRejected {
				t.Errorf("the rejected suggestion reads %s", s.Decision.Kind)
			}
			if s.State() != "REJECTED" {
				t.Errorf("its state reads %q", s.State())
			}
		case untouched.ID:
			// The whole point. A pointer, and it is nil.
			if s.Decision != nil {
				t.Fatalf("a suggestion nobody answered carries a decision of %s. §1: conflating "+
					"'he said no' with 'he did not look' poisons the learning signal and lets "+
					"silence be read as a decision.", s.Decision.Kind)
			}
			if s.State() != "UNACTIONED" {
				t.Errorf("its state reads %q", s.State())
			}
			if s.Actioned() {
				t.Error("it reports itself as actioned")
			}
		}
	}
	if after.Unactioned() != 1 {
		t.Errorf("the run counts %d unactioned suggestions, want 1", after.Unactioned())
	}

	// In the database: one decision row, not two. There is no row that means "unactioned", and no
	// value of `decision` that could be mistaken for one.
	var rows int
	if err := r.SQL.QueryRow(`
		SELECT count(*) FROM core.ai_prescribing_decision d
		  JOIN core.ai_prescribing_suggestion s ON s.id = d.suggestion_id
		 WHERE s.prescription_id = $1`, sheet.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("%d decision rows for one rejection and one untouched suggestion, want 1", rows)
	}

	// And the column cannot hold such a value even if somebody tried.
	if _, err := r.SQL.Exec(`
		INSERT INTO core.ai_prescribing_decision
		  (id, suggestion_id, facility_id, decision, decided_by, decided_at, event_id)
		VALUES (gen_random_uuid(), $1, $2, 'UNACTIONED', $3, now(), gen_random_uuid())`,
		untouched.ID, r.facility, r.user); err == nil {
		t.Fatal("the decision column accepted 'UNACTIONED'. Unactioned is the absence of a row, " +
			"and a value that says otherwise is a value a query will one day count as a rejection.")
	}

	// On the wire: the JSON for an unactioned suggestion has no `decision` key at all, so a client
	// testing `decision === 'REJECTED'` cannot be fooled by a default.
	resp, body := r.do(t, "GET", "/v1/prescriptions/"+sheet.ID.String()+"/ai-suggestions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET ai-suggestions: %d", resp.StatusCode)
	}
	for _, raw := range body["suggestions"].([]any) {
		object := raw.(map[string]any)
		if object["id"] != untouched.ID.String() {
			continue
		}
		if _, present := object["decision"]; present {
			t.Errorf("the unactioned suggestion serialises a decision key: %v", object["decision"])
		}
	}
}

// ---------------------------------------------------------------------------
// 5. §2's refusals
// ---------------------------------------------------------------------------

// TestAPatientWithNoAllergyStatusGetsZeroSuggestions is §2's hard stop, and the assertion is
// **zero suggestions and no model call**, not a filtered answer.
func TestAPatientWithNoAllergyStatusGetsZeroSuggestions(t *testing.T) {
	r := newAIRig(t)
	r.allergy = prescription.AllergyStatusNoneRecorded
	called := false
	r.answer = func(ai.ProviderRequest) (ai.ProviderResponse, error) {
		called = true
		return ai.ProviderResponse{Text: `{"suggestions":[]}`,
			ModelVersion: ai.MockModelVersion, FinishReason: "STOP"}, nil
	}
	sheet := r.draftEmpty(t)
	run := r.ask(t, sheet.ID)

	if len(run.Suggestions) != 0 {
		t.Fatalf("a patient with no recorded allergy status got %d suggestions", len(run.Suggestions))
	}
	if called {
		t.Fatal("the model was contacted about a patient with no recorded allergy status. §2: " +
			"not 'propose cautiously' — propose nothing, and do not form a proposal to discard. " +
			"A payload was still assembled and sent, which is the cost this stop exists to avoid.")
	}
	if run.State != prescription.RunRefused || run.Refusal != prescription.RefusalNoAllergyStatus {
		t.Fatalf("the run reads %s/%s, want REFUSED/NO_ALLERGY_STATUS", run.State, run.Refusal)
	}
	// The physician is told which stop this was, in both languages, because he can clear it in
	// thirty seconds by sending the patient back to the history station.
	if !strings.Contains(run.MessageEN, "allergy") || run.MessageBN == "" {
		t.Errorf("the refusal does not say what to do about it: %q / %q", run.MessageEN, run.MessageBN)
	}

	// And no interaction was recorded, because none happened.
	var calls int
	if err := r.SQL.QueryRow(`SELECT count(*) FROM core.ai_interaction WHERE agent_code = $1`,
		prescription.AgentCode).Scan(&calls); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("%d AI interactions were recorded for a run that should never have reached the gateway", calls)
	}
}

// TestTheServerDropsWhatSection2Forbids covers the three refusals that are about the answer.
//
// Each is asserted by having the "model" return exactly the forbidden item and then checking that
// **nothing was stored** and that the drop was counted under its own name — because a system that
// dropped everything for one reason would pass a test that only counted zero suggestions.
func TestTheServerDropsWhatSection2Forbids(t *testing.T) {
	type probe struct {
		name string
		item func(t *testing.T, r *aiRig, prompt string) map[string]any
		want prescription.Drop
	}
	probes := []probe{
		{
			name: "a product that is not in the formulary",
			item: func(t *testing.T, r *aiRig, prompt string) map[string]any {
				return map[string]any{
					"product_ref": "FXXX", "dose": mustStrength(t, prompt, "Metformin"), "frequency": "twice daily",
					"duration_days": 30, "route": "oral",
					"rationale_en": "Worth considering on the picture the record shows.",
					"rationale_bn": "নথিতে যা আছে তার ভিত্তিতে বিবেচনার যোগ্য।",
					"basis":        []any{"obs.hba1c:2026-09-01"},
				}
			},
			want: prescription.DropNotInFormulary,
		},
		{
			name: "a controlled drug",
			item: func(t *testing.T, r *aiRig, prompt string) map[string]any {
				// The interesting shape, and the one that makes this check more than decoration.
				//
				// A controlled product is not on the shortlist, so a well-formed answer cannot
				// name one — the first lock holds. What this probe does is move the register
				// *after* the shortlist was built and before the answer is sifted, which is
				// exactly the race the second lock exists for and exactly what will happen
				// routinely the day the shortlist comes from CP76's in-memory cache.
				ref, strength := refFromPrompt(t, prompt, "Metformin")
				if _, err := r.SQL.Exec(`
					INSERT INTO core.controlled_molecule (molecule, schedule, note_en, note_bn)
					VALUES ('Metformin', 'Scheduled — test', 'Added mid-consultation.',
					        'পরামর্শ চলাকালীন যোগ করা হয়েছে।')
					ON CONFLICT (molecule) DO NOTHING`); err != nil {
					t.Fatal(err)
				}
				return map[string]any{
					"product_ref": ref, "dose": strength, "frequency": "twice daily",
					"duration_days": 30, "route": "oral",
					"rationale_en": "Worth considering on the picture the record shows.",
					"rationale_bn": "নথিতে যা আছে তার ভিত্তিতে বিবেচনার যোগ্য।",
					"basis":        []any{"obs.hba1c:2026-09-01"},
				}
			},
			want: prescription.DropControlled,
		},
		{
			name: "a suggestion with no reasoning behind it",
			item: func(t *testing.T, r *aiRig, prompt string) map[string]any {
				return map[string]any{
					"product_ref": mustRef(t, prompt, "Metformin"),
					"dose":        mustStrength(t, prompt, "Metformin"), "frequency": "twice daily",
					"duration_days": 30, "route": "oral",
					"rationale_en": "Worth considering on the picture the record shows.",
					"rationale_bn": "নথিতে যা আছে তার ভিত্তিতে বিবেচনার যোগ্য।",
					// A basis of whitespace. The output schema's `min_length: 2` counts runes
					// and cannot see that they are all spaces, which is precisely why this check
					// is not a duplicate of it: a suggestion resting on "  " rests on nothing.
					"basis": []any{"  "},
				}
			},
			want: prescription.DropNoRationale,
		},
	}

	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			r := newAIRig(t)
			r.answer = func(req ai.ProviderRequest) (ai.ProviderResponse, error) {
				encoded, _ := json.Marshal(map[string]any{
					"suggestions": []any{p.item(t, r, req.User)}})
				return ai.ProviderResponse{Text: string(encoded),
					ModelVersion: ai.MockModelVersion, FinishReason: "STOP"}, nil
			}
			sheet := r.draftEmpty(t)
			run := r.ask(t, sheet.ID)

			if len(run.Suggestions) != 0 {
				t.Fatalf("%s was stored as a suggestion", p.name)
			}
			if run.DroppedReasons[string(p.want)] != 1 {
				t.Fatalf("dropped as %v, want one %s. A rule that drops everything for the wrong "+
					"reason is a rule nobody can audit from the row.", run.DroppedReasons, p.want)
			}
			// Nothing repaired: no row with a substituted product, no row with a filled-in basis.
			var stored int
			if err := r.SQL.QueryRow(`
				SELECT count(*) FROM core.ai_prescribing_suggestion WHERE prescription_id = $1`,
				sheet.ID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != 0 {
				t.Fatalf("%d suggestions were stored. §2: dropped, not repaired.", stored)
			}
		})
	}
}

// TestAControlledDrugIsNotEvenOffered is the first half of §2's controlled rule: the model is never
// shown one.
//
// Asserted against the prompt the server actually sent, not against the shortlist function, because
// what matters is what left the building.
func TestAControlledDrugIsNotEvenOffered(t *testing.T) {
	r := newAIRig(t)
	var sentPrompt string
	r.answer = func(req ai.ProviderRequest) (ai.ProviderResponse, error) {
		sentPrompt = req.User
		return ai.ProviderResponse{Text: `{"suggestions":[]}`,
			ModelVersion: ai.MockModelVersion, FinishReason: "STOP"}, nil
	}
	sheet := r.draftEmpty(t)
	r.ask(t, sheet.ID)

	var controlled []string
	// The registered molecules that this clinic actually stocks. Asked through the same predicate
	// the shortlist subtraction reads, so a register entry that resolves to nothing here is a
	// molecule the formulary does not hold — which is now an ordinary state and not a defect.
	rows, err := r.SQL.Query(`
		SELECT DISTINCT g.name FROM core.generic g WHERE core.generic_is_controlled(g.id)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		controlled = append(controlled, name)
	}
	if len(controlled) == 0 {
		t.Fatal("the controlled register is empty; this rule has nothing to be tested against. " +
			"Migration 00069 seeds pregabalin, which is in CP75's formulary.")
	}
	for _, name := range controlled {
		if strings.Contains(strings.ToLower(sentPrompt), strings.ToLower(name)) {
			t.Fatalf("%q was put in front of the model. The shortlist is §2's first line of "+
				"defence and it is the one that costs nothing.", name)
		}
	}
	// And the prompt is not empty of everything, which a test asserting only an absence would not
	// notice: a shortlist the server failed to build would pass the loop above.
	if !strings.Contains(sentPrompt, "formulary_candidates") || !strings.Contains(sentPrompt, "F001") {
		t.Fatalf("the prompt carries no shortlist at all, so the absence above proves nothing")
	}
}

// ---------------------------------------------------------------------------
// 6. The safety engine is not told which lines came from the AI (§1)
// ---------------------------------------------------------------------------

// TestTheSafetyEngineGivesTheSameVerdictWhateverTheOrigin builds two prescriptions with identical
// content — one accepted from the AI, one typed — and drives the real safety-check route against
// both.
//
// It compares the whole result rather than the verdict alone. A test that only compared "BLOCK vs
// BLOCK" would pass against an engine that had quietly reordered or re-worded findings for AI
// lines, and the findings are what a physician reads.
func TestTheSafetyEngineGivesTheSameVerdictWhateverTheOrigin(t *testing.T) {
	r := newAIRig(t)
	r.proposeMetformin(t)

	// The AI one.
	fromAI := r.draftEmpty(t)
	offered := r.ask(t, fromAI.ID).Suggestions[0]
	if _, err := r.service.Decide(r.ctx(), prescription.Deciding{
		PrescriptionID: fromAI.ID, SuggestionID: offered.ID,
		Kind: prescription.DecisionAccepted,
	}); err != nil {
		t.Fatalf("accepting: %v", err)
	}

	// The typed one, written by hand with the same product and the same directions.
	typed := r.draftEmpty(t)
	product := offered.ProductID
	if _, err := r.service.AddItem(r.ctx(), prescription.Addition{
		PrescriptionID: typed.ID, ProductID: &product,
		Dose: offered.Dose, Frequency: offered.Frequency,
		DurationDays: offered.DurationDays, Route: offered.Route,
	}); err != nil {
		t.Fatalf("typing the same line: %v", err)
	}

	check := func(id uuid.UUID) map[string]any {
		resp, body := r.do(t, "POST", "/v1/prescriptions/"+id.String()+"/safety-check",
			map[string]any{"at": "2026-09-14T09:30:00Z"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("safety-check on %s: %d — %v", id, resp.StatusCode, body)
		}
		result, _ := body["result"].(map[string]any)
		if result == nil {
			t.Fatalf("no result in the safety check response: %v", body)
		}
		// The coverage list keys on the prescription item id, which is different by construction.
		// Blanked rather than dropped, so a coverage list that changed *length* still fails.
		for _, raw := range coverageOf(result) {
			raw["ref"] = "(item)"
			raw["label"] = "(medicine)"
		}
		for _, raw := range findingsOf(result) {
			raw["refs"] = "(items)"
		}
		return result
	}

	machine := check(fromAI.ID)
	hand := check(typed.ID)
	if !reflect.DeepEqual(machine, hand) {
		t.Fatalf("the safety engine answered differently for the same medicine depending on where "+
			"the line came from.\n  from the AI: %v\n  typed:       %v\n"+
			"§1: the engine runs on the final content of the prescription and is not told which is "+
			"which.", machine, hand)
	}

	// And the direct evidence that it is not told: nothing the handler sends the engine carries an
	// origin. `medsafety.Item` is the whole of what crosses.
	item := reflect.TypeOf(medsafety.Item{})
	for i := 0; i < item.NumField(); i++ {
		name := strings.ToLower(item.Field(i).Name)
		if strings.HasPrefix(name, "ai") || strings.Contains(name, "suggestion") ||
			strings.Contains(name, "origin") {
			t.Fatalf("medsafety.Item carries a field %q. The engine is not supposed to be able to "+
				"know where a line came from.", item.Field(i).Name)
		}
	}
}

func coverageOf(result map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, raw := range asList(result["coverage"]) {
		if object, ok := raw.(map[string]any); ok {
			out = append(out, object)
		}
	}
	return out
}

func findingsOf(result map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, raw := range asList(result["findings"]) {
		if object, ok := raw.(map[string]any); ok {
			out = append(out, object)
		}
	}
	return out
}

func asList(v any) []any {
	out, _ := v.([]any)
	return out
}

// ---------------------------------------------------------------------------
// 7. The vocabulary (§5)
// ---------------------------------------------------------------------------

// TestTheRejectionVocabularyIsReferenceDataAndBilingual asserts what §5 asks of the list, and one
// thing it asks of the *storage*: that a reason can be added without a code release.
func TestTheRejectionVocabularyIsReferenceDataAndBilingual(t *testing.T) {
	r := newAIRig(t)

	reasons, err := r.store.RejectReasons(r.ctx())
	if err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 12 {
		t.Fatalf("the vocabulary has %d reasons; §5 authors twelve", len(reasons))
	}
	seen := map[string]bool{}
	for _, reason := range reasons {
		if strings.TrimSpace(reason.LabelEN) == "" || strings.TrimSpace(reason.LabelBN) == "" {
			t.Errorf("%s is not bilingual", reason.Code)
		}
		if reason.LabelEN == reason.LabelBN {
			t.Errorf("%s has the same text in both languages", reason.Code)
		}
		seen[reason.Code] = true
	}
	// The two §5 singles out for Dr Nahid's review, asserted by name so that collapsing them is a
	// failing test rather than a quiet loss of the distinction the data is meant to measure.
	for _, code := range []string{"COST", "AVAILABILITY", "INSUFFICIENT_DATA"} {
		if !seen[code] {
			t.Errorf("%s is missing. §5 argues for it specifically.", code)
		}
	}

	// Reference data: a thirteenth reason is an INSERT, not a release. Asserted by adding one as
	// the application role and then serving it.
	if _, err := r.SQL.Exec(`
		INSERT INTO core.ai_suggestion_reject_reason (code, label_en, label_bn, ordering)
		VALUES ('SEEN_ELSEWHERE', 'Already started by another clinic',
		        'অন্য ক্লিনিকে ইতিমধ্যেই শুরু হয়েছে', 130)`); err != nil {
		t.Fatalf("a new reason could not be added without a code release: %v", err)
	}
	after, err := r.store.RejectReasons(r.ctx())
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 13 {
		t.Errorf("the new reason is not served: %d reasons", len(after))
	}

	// And it is served under `reference.read`, which the handler's declaration says and the
	// contract test in cmd/api enforces. What is asserted here is the route works at all.
	resp, body := r.do(t, "GET", "/v1/ai-suggestion-reject-reasons", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET the vocabulary: %d", resp.StatusCode)
	}
	if int(body["total"].(float64)) != 13 {
		t.Errorf("the route serves %v reasons", body["total"])
	}
}

// ---------------------------------------------------------------------------
// 8. The decision rules
// ---------------------------------------------------------------------------

func TestOneDecisionPerSuggestionAndTheReasonBelongsToTheRejection(t *testing.T) {
	r := newAIRig(t)
	r.proposeMetformin(t)
	sheet := r.draftEmpty(t)
	offered := r.ask(t, sheet.ID).Suggestions[0]

	// A reason on an acceptance is refused rather than absorbed.
	if _, err := r.service.Decide(r.ctx(), prescription.Deciding{
		PrescriptionID: sheet.ID, SuggestionID: offered.ID,
		Kind: prescription.DecisionAccepted, ReasonCode: "COST",
	}); err == nil {
		t.Error("an acceptance carrying a rejection reason was accepted; the reason would be " +
			"counted in the one signal §5 exists to collect")
	}

	// A reason this clinic does not use is refused.
	if _, err := r.service.Decide(r.ctx(), prescription.Deciding{
		PrescriptionID: sheet.ID, SuggestionID: offered.ID,
		Kind: prescription.DecisionRejected, ReasonCode: "BECAUSE_I_SAID_SO",
	}); err == nil {
		t.Error("an unknown rejection reason was accepted")
	}

	// An edit that changes nothing is an acceptance.
	same := offered.Dose
	if _, err := r.service.Decide(r.ctx(), prescription.Deciding{
		PrescriptionID: sheet.ID, SuggestionID: offered.ID,
		Kind: prescription.DecisionEdited, Edit: prescription.EditedLine{Dose: &same},
	}); err == nil {
		t.Error("an edit identical to the offer was recorded as an edit")
	}

	// A rejection with no reason at all: one action, which §5 requires.
	if _, err := r.service.Decide(r.ctx(), prescription.Deciding{
		PrescriptionID: sheet.ID, SuggestionID: offered.ID,
		Kind: prescription.DecisionRejected,
	}); err != nil {
		t.Fatalf("a rejection with no reason was refused: %v. §5: a physician mid-clinic with a "+
			"patient in front of him should be able to dismiss a suggestion in one action.", err)
	}

	// And a second decision is refused.
	if _, err := r.service.Decide(r.ctx(), prescription.Deciding{
		PrescriptionID: sheet.ID, SuggestionID: offered.ID,
		Kind: prescription.DecisionAccepted,
	}); err == nil {
		t.Error("a suggestion was decided twice; the first answer would be gone from the trail")
	}
}

// TestASuggestionInAnotherPrescriptionIs404 is the house rule about what a refusal may reveal.
func TestASuggestionInAnotherPrescriptionIs404(t *testing.T) {
	r := newAIRig(t)
	r.proposeMetformin(t)
	mine := r.draftEmpty(t)
	offered := r.ask(t, mine.ID).Suggestions[0]
	other := r.draftEmpty(t)

	resp, _ := r.do(t, "POST",
		"/v1/prescriptions/"+other.ID.String()+"/ai-suggestions/"+offered.ID.String()+"/decision",
		map[string]any{"decision": "ACCEPTED"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deciding another prescription's suggestion answered %d, want 404. A 403 or a "+
			"message naming the right prescription would confirm a row the caller is not "+
			"entitled to know about.", resp.StatusCode)
	}
}

// TestTheEmptyStateSaysTheAIProposedNothing is D-15 on this screen: every state is a sentence.
func TestTheEmptyStateSaysTheAIProposedNothing(t *testing.T) {
	r := newAIRig(t)
	sheet := r.draftEmpty(t)

	// Never asked.
	before, err := r.service.SuggestionsFor(r.ctx(), sheet.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if before.MessageEN == "" || before.MessageBN == "" {
		t.Error("the never-asked state has no sentence")
	}
	if before.State != "" {
		t.Errorf("the never-asked state reads %q", before.State)
	}

	// Asked, and the model proposed nothing. The default mock answer is an empty list.
	run := r.ask(t, sheet.ID)
	if run.State != prescription.RunReady {
		t.Fatalf("an answered run reads %s", run.State)
	}
	if len(run.Suggestions) != 0 {
		t.Fatalf("the model proposed nothing and %d suggestions were stored", len(run.Suggestions))
	}
	if run.MessageEN == before.MessageEN {
		t.Error("'nobody asked' and 'the AI proposed nothing' show the same sentence. They are " +
			"different facts and one of them is an answer.")
	}
	if !strings.Contains(run.MessageEN, "nothing") || run.MessageBN == "" {
		t.Errorf("the empty answer does not say so: %q / %q", run.MessageEN, run.MessageBN)
	}
}

// ---------------------------------------------------------------------------
// 9. No PHI in what leaves the process
// ---------------------------------------------------------------------------

// TestNothingThatNamesThePatientReachesTheModel is the gateway's guarantee, asserted at this
// agent's own boundary rather than taken on trust from CP70.
//
// The briefing stub deliberately carries the patient's real name and phone number as subject
// identifiers, exactly as the composition root's bridge does. What is asserted is that neither
// appears in the prompt the provider was handed.
func TestNothingThatNamesThePatientReachesTheModel(t *testing.T) {
	r := newAIRig(t)
	var sent string
	r.answer = func(req ai.ProviderRequest) (ai.ProviderResponse, error) {
		sent = req.System + "\n" + req.User
		return ai.ProviderResponse{Text: `{"suggestions":[]}`,
			ModelVersion: ai.MockModelVersion, FinishReason: "STOP"}, nil
	}
	sheet := r.draftEmpty(t)
	r.ask(t, sheet.ID)

	for _, identifier := range []string{"Rahima Begum", "+8801711111801", "8801711111801"} {
		if strings.Contains(sent, identifier) {
			t.Fatalf("%q reached the model. This is the highest-risk surface in the system for "+
				"that, and the gateway's minimiser is what is supposed to stop it.", identifier)
		}
	}
	if sent == "" {
		t.Fatal("nothing was sent, so this test proved nothing")
	}
}

// ---------------------------------------------------------------------------
// Rig helpers CP82 needs and CP80's did not
// ---------------------------------------------------------------------------

// draftEmpty opens a draft with nothing on it.
//
// `rig.draft` puts a metformin line on the sheet, which is right for CP80's tests and wrong for
// most of these: a suggestion for a medicine already on the prescription is dropped as a
// duplicate, which is correct behaviour and would make every test here assert the wrong thing.
func (r *rig) draftEmpty(t *testing.T) prescription.Prescription {
	t.Helper()
	sheet, err := r.service.Create(r.ctx(), prescription.Creation{
		PatientID: r.patient, VisitID: r.visit,
	})
	if err != nil {
		t.Fatalf("creating a draft: %v", err)
	}
	return sheet
}

// rebuildHandlers replaces the rig's HTTP surface with one built over the current service.
//
// Needed because CP82 attaches the agent to the service *after* `newRig` has already built the
// handlers, and because these tests drive the safety-check route — which CP80's rig mounts without
// an engine. Building a second server rather than mutating the first keeps the cleanup honest.
func (r *rig) rebuildHandlers(t *testing.T, facts medsafety.PatientFacts) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := prescription.NewHandlers(prescription.HandlersConfig{
		Service: r.service, Store: r.store, Clock: r.clock, Logger: logger,
		Header: stubHeader{},
		Engine: medsafety.NewEngine(medsafety.NewStore(r.pool, formulary.NewStore(r.pool)),
			formulary.NewStore(r.pool)),
		Facts: facts,
	})
	who := staff{facility: r.facility, user: r.user, device: r.device,
		permissions: &r.held, role: &r.role}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 18, RequestTimeout: 30 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(rt chi.Router) {
			handlers.Mount(rt)
			handlers.MountContent(rt)
			handlers.MountSuggestions(rt)
			rt.Route("/patients", func(p chi.Router) { handlers.MountPatient(p) })
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.server.Close()
	r.server = httptest.NewServer(router)
	t.Cleanup(r.server.Close)
}

// mustRef and mustStrength are refFromPrompt's two halves, for the places a single value reads
// better than a pair.
func mustRef(t *testing.T, prompt, generic string) string {
	t.Helper()
	ref, _ := refFromPrompt(t, prompt, generic)
	return ref
}

func mustStrength(t *testing.T, prompt, generic string) string {
	t.Helper()
	_, strength := refFromPrompt(t, prompt, generic)
	return strength
}

// secondRef is a handle from the shortlist that is not the one already used.
func secondRef(t *testing.T, prompt, notThis string) (string, string) {
	t.Helper()
	var payload struct {
		Candidates []struct {
			Ref      string `json:"ref"`
			Strength string `json:"strength"`
		} `json:"formulary_candidates"`
	}
	start := strings.Index(prompt, "{")
	end := strings.LastIndex(prompt, "}")
	if err := json.Unmarshal([]byte(prompt[start:end+1]), &payload); err != nil {
		t.Fatal(err)
	}
	for _, c := range payload.Candidates {
		if c.Ref != notThis && strings.TrimSpace(c.Strength) != "" {
			return c.Ref, c.Strength
		}
	}
	t.Fatal("the shortlist has fewer than two products in it")
	return "", ""
}

// TestTheAllergyStopCompareAgainstWhatTheDatabaseActuallySays closes the one gap the rest of this
// file leaves open.
//
// `prescription.AllergyStatusNoneRecorded` is a string literal duplicated from `allergy.StatusNone`,
// because `prescription` may not import `allergy`. Every other test here drives that comparison
// through a stub, so a typo in the constant would leave all of them green while the gate never
// matched — and a gate that never matches refuses nothing, which is the failure direction that
// matters.
//
// So this asks the database. `core.allergy_status()` is the same function the trigger on the queue
// calls, and a patient with nothing recorded is what CP54's hard stop is about.
func TestTheAllergyStopComparesAgainstWhatTheDatabaseActuallySays(t *testing.T) {
	r := newAIRig(t)

	var status string
	if err := r.SQL.QueryRow(`SELECT core.allergy_status($1)::text`, r.patient).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != prescription.AllergyStatusNoneRecorded {
		t.Fatalf("core.allergy_status() answers %q for a patient with nothing recorded, and "+
			"prescription.AllergyStatusNoneRecorded is %q. The two have to be the same string: "+
			"the comparison that stops the agent is between them, and a mismatch makes it never "+
			"fire while every stubbed test here stays green.",
			status, prescription.AllergyStatusNoneRecorded)
	}
}

// TestTheRegisterProtectsAMoleculeTheFormularyDoesNotHoldYet is the property the first version of
// the register did not have.
//
// It was keyed on `core.generic.id`, so "the AI may not propose testosterone" came into existence
// at the same instant as the testosterone product it was supposed to forbid. A seed for a molecule
// this clinic does not stock matched nothing and inserted nothing, and the register looked like
// protection.
//
// So this test does the thing that used to break it: it registers nothing, adds the molecule to
// the formulary **after** the register was written, and asserts the agent still cannot propose it.
// The register is counted before and after, so a test that accidentally fixed itself by writing a
// row would fail rather than pass.
func TestTheRegisterProtectsAMoleculeTheFormularyDoesNotHoldYet(t *testing.T) {
	r := newAIRig(t)

	// 1. Testosterone is registered, and the formulary does not hold it. Both halves asserted,
	// because the interesting state is the pair: a register entry with nothing behind it.
	var registered bool
	if err := r.SQL.QueryRow(`
		SELECT EXISTS (SELECT 1 FROM core.controlled_molecule
		                WHERE lower(molecule) = 'testosterone')`).Scan(&registered); err != nil {
		t.Fatal(err)
	}
	if !registered {
		t.Fatal("testosterone is not on the do-not-propose register. Migration 00069 seeds it by " +
			"name precisely so that it is there before the clinic stocks it.")
	}
	var stocked int
	if err := r.SQL.QueryRow(`
		SELECT count(*) FROM core.medication_product p
		  JOIN core.generic g ON g.id = p.generic_id
		 WHERE lower(g.name) LIKE '%testosterone%'`).Scan(&stocked); err != nil {
		t.Fatal(err)
	}
	if stocked != 0 {
		t.Fatalf("the formulary already holds %d testosterone product(s), so this test is no "+
			"longer about a molecule the clinic does not stock. Pick one it does not.", stocked)
	}

	var before int
	if err := r.SQL.QueryRow(`SELECT count(*) FROM core.controlled_molecule`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	// 2. A pharmacist adds it, the way CP75 adds one: a generic and a product, with no component
	// decomposition — which is the default state and the one that would slip past a check that
	// only read `core.generic_component`.
	var genericID, productID uuid.UUID
	if err := r.SQL.QueryRow(`
		INSERT INTO core.generic (name, class_code)
		SELECT 'Testosterone undecanoate', g.class_code FROM core.generic g
		 WHERE lower(g.name) = 'pregabalin'
		RETURNING id`).Scan(&genericID); err != nil {
		t.Fatalf("adding the generic: %v", err)
	}
	if err := r.SQL.QueryRow(`
		INSERT INTO core.medication_product
		  (facility_id, generic_id, trade_name, strength, form_code, manufacturer, dispense_unit)
		SELECT $1, $2, 'Androlone', '40 mg', p.form_code, 'Test Pharma', p.dispense_unit
		  FROM core.medication_product p WHERE p.id = $3
		RETURNING id`, r.facility, genericID, r.product).Scan(&productID); err != nil {
		t.Fatalf("adding the product: %v", err)
	}

	// 3. Nothing was written to the register in between. This is the assertion that makes the rest
	// of the test mean what it says.
	var after int
	if err := r.SQL.QueryRow(`SELECT count(*) FROM core.controlled_molecule`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("the register changed from %d rows to %d while the product was being added; the "+
			"whole point is that it does not have to", before, after)
	}

	// 4. The shortlist subtraction excludes it — asserted against the prompt the server actually
	// sent, because that is what left the building.
	var sentPrompt string
	r.answer = func(req ai.ProviderRequest) (ai.ProviderResponse, error) {
		sentPrompt = req.User
		return ai.ProviderResponse{Text: `{"suggestions":[]}`,
			ModelVersion: ai.MockModelVersion, FinishReason: "STOP"}, nil
	}
	sheet := r.draftEmpty(t)
	r.ask(t, sheet.ID)

	if strings.Contains(strings.ToLower(sentPrompt), "androlone") ||
		strings.Contains(strings.ToLower(sentPrompt), "testosterone") {
		t.Fatal("a testosterone product added to the formulary after the register was written was " +
			"put in front of the model. The register is supposed to hold before the clinic " +
			"stocks the drug as well as after.")
	}
	if !strings.Contains(sentPrompt, "formulary_candidates") {
		t.Fatal("the prompt carries no shortlist, so the absence above proves nothing")
	}

	// 5. And the answer check would refuse it too, through the same predicate. Asked of the
	// database rather than of the Go map, because the predicate is the thing both sides read and a
	// test of one of the two callers would not notice the other drifting.
	var controlled bool
	if err := r.SQL.QueryRow(`SELECT core.generic_is_controlled($1)`, genericID).Scan(&controlled); err != nil {
		t.Fatal(err)
	}
	if !controlled {
		t.Fatal("core.generic_is_controlled says a testosterone generic is not controlled. Both " +
			"the shortlist subtraction and the answer check read that function, so this is both " +
			"of §2's locks open at once.")
	}

	// 6. The word-boundary comparison does not over-reach: the molecule is 'Testosterone' and the
	// generic is 'Testosterone undecanoate', which must match — but a generic that merely contains
	// the letters must not. Asserted because a containment check that matched substrings would
	// quietly refuse unrelated medicines and nobody would notice until a physician did.
	var overreach bool
	if err := r.SQL.QueryRow(`
		SELECT core.generic_is_controlled(g.id) FROM core.generic g
		 WHERE lower(g.name) = 'gabapentin'`).Scan(&overreach); err != nil {
		t.Fatal(err)
	}
	if overreach {
		t.Fatal("gabapentin reads as controlled. It is deliberately not on the register — the " +
			"distinction between it and pregabalin is what the register exists to be able to make.")
	}
}

// TestTheInvariantNoticesAnEmptyRegister is the check on the check.
//
// Invariant 131 exists because the register silently emptied itself once. A test that only ran it
// against a correctly seeded database would pass whether or not it could ever fail, so this empties
// the register and asserts the invariant says so.
func TestTheInvariantNoticesAnEmptyRegister(t *testing.T) {
	r := newAIRig(t)

	if _, err := r.SQL.Exec(`SELECT core.assert_controlled_molecules_are_registered()`); err != nil {
		t.Fatalf("invariant 131 fails against a correctly seeded register: %v", err)
	}

	// Emptied as the owner, because the application role is deliberately refused DELETE.
	if _, err := r.SQL.Exec(`DELETE FROM core.controlled_molecule`); err != nil {
		t.Fatal(err)
	}
	_, err := r.SQL.Exec(`SELECT core.assert_controlled_molecules_are_registered()`)
	if err == nil {
		t.Fatal("the register was emptied and invariant 131 passed. A safety register that can be " +
			"empty without complaint is worse than no register, because it looks like protection.")
	}
	t.Logf("empty register caught: %v", err)

	// And one seeded molecule missing, which is the shape the original defect actually had: the
	// table was not empty, it just did not contain what the migration intended.
	if _, err := r.SQL.Exec(`
		INSERT INTO core.controlled_molecule (molecule, schedule, note_en, note_bn)
		VALUES ('Pregabalin', 'Scheduled — psychotropic', 'note', 'নোট')`); err != nil {
		t.Fatal(err)
	}
	_, err = r.SQL.Exec(`SELECT core.assert_controlled_molecules_are_registered()`)
	if err == nil {
		t.Fatal("testosterone is missing from the register and invariant 131 passed. That is " +
			"exactly the state the seed produced when it matched zero rows.")
	}
	t.Logf("missing seeded molecule caught: %v", err)
}

// ---------------------------------------------------------------------------
// The rationale line reads as a clinician would say it
// ---------------------------------------------------------------------------

// §2 asks for a suggestion a physician can audit in five seconds, and the fact references are
// what make that possible. They were reaching the panel as `obs.hba1c:2026-09-01 ·
// dx.type_2_diabetes_mellitus` — a storage format, shown to somebody scanning three cards.
//
// The raw reference stays on the row, because it is what CP72's grounding arm validated and what
// an engineer greps for. What changes is which of the two the screen leads with.
func TestTheFactsASuggestionRestsOnAreReadable(t *testing.T) {
	r := newAIRig(t)
	r.proposeMetformin(t)
	sheet := r.draftEmpty(t)
	run := r.ask(t, sheet.ID)
	if len(run.Suggestions) != 1 {
		t.Fatalf("expected one suggestion: %+v", run.DroppedReasons)
	}

	sg := run.Suggestions[0]
	if len(sg.BasisEN) != len(sg.Basis) || len(sg.BasisBN) != len(sg.Basis) {
		t.Fatalf("the rendered basis is not index-aligned with the raw one: %v / %v / %v",
			sg.Basis, sg.BasisEN, sg.BasisBN)
	}
	for i, raw := range sg.Basis {
		if strings.Contains(sg.BasisEN[i], "_") || strings.Contains(sg.BasisEN[i], ":") {
			t.Errorf("basis %q rendered as %q, which is still a token", raw, sg.BasisEN[i])
		}
		if strings.TrimSpace(sg.BasisBN[i]) == "" {
			t.Errorf("basis %q has no Bangla rendering", raw)
		}
	}
	// `basis` is sorted, so the pair is found by its raw reference rather than by position.
	shown := map[string]string{}
	for i, raw := range sg.Basis {
		shown[raw] = sg.BasisEN[i]
	}
	if got := shown["obs.hba1c:2026-09-01"]; got != "HbA1c, 1 Sep 2026" {
		t.Errorf("the HbA1c reference reads %q; a date on a clinical screen is not ISO", got)
	}
	// A reference this package cannot resolve to a catalogue entry still reads as words.
	if got := shown["dx.type_2_diabetes_mellitus"]; got != "Type 2 diabetes mellitus" {
		t.Errorf("the diagnosis reference reads %q", got)
	}

	// And it is derived on read, so re-reading the same run renders it again rather than
	// returning a row that happens to have been decorated once on the way out of the agent.
	again, err := r.service.SuggestionsFor(r.ctx(), sheet.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Suggestions[0].BasisEN) == 0 {
		t.Fatal("a re-read suggestion lost its rendered basis")
	}
}
