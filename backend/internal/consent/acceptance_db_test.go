package consent_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/consent"
)

// CP36's four acceptance criteria, each proved against a real database.

func TestAConsentIsCapturedWithItsVersionLanguageEvidenceAndWitness(t *testing.T) {
	// Acceptance criterion 1.
	h := newAPI(t)

	resp, body := h.grant(t, "research", nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}
	record := body["consent"].(map[string]any)
	if record["status"] != "granted" {
		t.Errorf("status = %v", record["status"])
	}
	if record["template_version"] != float64(1) {
		t.Errorf("template_version = %v; the consent does not say which words were shown", record["template_version"])
	}
	if record["language"] != "bn" {
		t.Errorf("language = %v", record["language"])
	}
	if record["witnessed_by_code"] != "R001" {
		t.Errorf("witnessed_by_code = %v", record["witnessed_by_code"])
	}

	// And the digest of the exact text is in the ledger, so a template row altered later by
	// somebody with database access is detectable rather than merely unlikely.
	var digest, stored string
	if err := h.SQL.QueryRow(
		`SELECT payload ->> 'template_digest' FROM ledger.event WHERE event_type = 'CONSENT_GRANTED'`,
	).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if err := h.SQL.QueryRow(
		`SELECT body_digest FROM core.consent_template
		  WHERE consent_type = 'research' AND language = 'bn' AND version = 1`,
	).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if digest != stored || len(digest) != 64 {
		t.Errorf("event digest %q does not match the template's %q", digest, stored)
	}
}

func TestRevocationBlocksTheBehaviourImmediately(t *testing.T) {
	// Acceptance criterion 2, and the §15.1 budget is one minute. It is met by construction:
	// the row that says "do not send" is written by the same COMMIT as the event saying so.
	h := newAPI(t)
	sent := &recorder{}
	sender := consent.NewGatedSender(h.gate, sent)
	message := consent.Message{
		PatientID: h.patient, FacilityID: h.facility, Kind: consent.Remind, Body: "Your results are ready.",
	}

	// Before any consent: refused, and not sent.
	if err := sender.Send(h.ctx(), message); !errors.Is(err, consent.ErrDenied) {
		t.Fatalf("an unconsented message was not refused: %v", err)
	}
	if sent.count != 0 {
		t.Fatal("the message reached the sender anyway")
	}

	if resp, body := h.grant(t, "communication", nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("granting: %d %v", resp.StatusCode, body)
	}
	if err := sender.Send(h.ctx(), message); err != nil {
		t.Fatalf("a consented message was refused: %v", err)
	}
	if sent.count != 1 {
		t.Fatalf("sent %d messages", sent.count)
	}

	// The revocation. No sleep, no polling: the next call is refused.
	if resp, body := h.revoke(t, "communication", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("revoking: %d %v", resp.StatusCode, body)
	}
	err := sender.Send(h.ctx(), message)
	if !errors.Is(err, consent.ErrDenied) {
		t.Fatalf("a message went out after consent was withdrawn: %v", err)
	}
	var denied consent.Denied
	if errors.As(err, &denied) && denied.Status != consent.Revoked {
		t.Errorf("the refusal says %q rather than that consent was withdrawn", denied.Status)
	}
	if sent.count != 1 {
		t.Errorf("the sender was called %d times; the second must never have reached it", sent.count)
	}
}

func TestEachConsentIsIndependentlyGrantableAndRevocable(t *testing.T) {
	// Acceptance criterion 3, and the whole argument for layered consent (D-02 option ii).
	h := newAPI(t)

	for _, kind := range []string{"care", "communication", "research"} {
		if resp, body := h.grant(t, kind, nil); resp.StatusCode != http.StatusCreated {
			t.Fatalf("granting %s: %d %v", kind, resp.StatusCode, body)
		}
	}
	if resp, body := h.revoke(t, "communication", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("revoking communication: %d %v", resp.StatusCode, body)
	}

	// Withdrawing the SMS did not withdraw the treatment.
	if err := h.gate.Check(h.ctx(), h.patient, h.facility, consent.Care); err != nil {
		t.Errorf("care consent was affected by a communication revocation: %v", err)
	}
	if err := h.gate.Check(h.ctx(), h.patient, h.facility, consent.Research); err != nil {
		t.Errorf("research consent was affected by a communication revocation: %v", err)
	}
	if err := h.gate.Check(h.ctx(), h.patient, h.facility, consent.Communication); !errors.Is(err, consent.ErrDenied) {
		t.Errorf("communication consent survived its own revocation: %v", err)
	}
	// And the two nobody asked about are absent, which is not the same as refused.
	var denied consent.Denied
	err := h.gate.Check(h.ctx(), h.patient, h.facility, consent.Outreach)
	if !errors.As(err, &denied) || denied.Status != consent.Absent {
		t.Errorf("an unasked consent reads as %v rather than absent", err)
	}
}

func TestConsentStatusIsVisibleWhereverItAffectsAnAction(t *testing.T) {
	// Acceptance criterion 4. Every type comes back, including the ones never asked about:
	// a list of only what exists cannot show a desk what it has not done.
	h := newAPI(t)
	if resp, body := h.grant(t, "care", nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("granting: %d %v", resp.StatusCode, body)
	}

	resp, body := h.call(t, http.MethodGet, "/v1/patients/"+h.patient.String()+"/consents", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}
	rows := body["consents"].([]any)
	if len(rows) != 5 {
		t.Fatalf("got %d consents; all five types must be listed", len(rows))
	}
	states := map[string]string{}
	for _, row := range rows {
		entry := row.(map[string]any)
		states[entry["consent_type"].(string)] = entry["status"].(string)
	}
	if states["care"] != "granted" {
		t.Errorf("care = %q", states["care"])
	}
	if states["outreach"] != "absent" {
		t.Errorf("outreach = %q; never asked must not read as refused", states["outreach"])
	}
}

// --- the research boundary ---

func TestResearchCannotSeeSomebodyWhoDidNotConsent(t *testing.T) {
	// The strongest half of "enforcement at the point of use": research holds no privilege
	// on the subject table at all, so this is not a filter anybody can forget to apply.
	h := newAPI(t)

	if h.cohortSize(t) != 0 {
		t.Fatal("a subject is in the cohort before consenting")
	}
	if resp, body := h.grant(t, "research", nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("granting: %d %v", resp.StatusCode, body)
	}
	if h.cohortSize(t) != 1 {
		t.Fatal("a consenting subject is not in the cohort")
	}
	if resp, body := h.revoke(t, "research", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("revoking: %d %v", resp.StatusCode, body)
	}
	if h.cohortSize(t) != 0 {
		t.Error("a subject who withdrew is still in the cohort")
	}

	// And the base table is out of reach entirely, so writing the query by hand does not help.
	var can bool
	if err := h.SQL.QueryRow(
		`SELECT has_table_privilege('dthcms_research', 'research.research_subject', 'SELECT')`,
	).Scan(&can); err != nil {
		t.Fatal(err)
	}
	if can {
		t.Error("dthcms_research can read the subject table directly")
	}
}

func (h *api) cohortSize(t *testing.T) int {
	t.Helper()
	var n int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM research.cohort`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// --- the rules that would otherwise fail silently ---

func TestAConsentCannotBeTakenAgainstWordsThatDoNotExist(t *testing.T) {
	// Until D-02 is answered this is the *normal* state, and the honest answer is that the
	// deployment is not finished — not that the request was wrong.
	h := newAPI(t)
	if _, err := h.SQL.Exec(
		`UPDATE core.consent_template SET status = 'retired', retired_at = now() WHERE consent_type = 'outreach'`,
	); err != nil {
		t.Fatal(err)
	}
	resp, body := h.grant(t, "outreach", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}
}

func TestATemplateThatHasBeenConsentedAgainstCannotBeEdited(t *testing.T) {
	h := newAPI(t)
	if resp, body := h.grant(t, "care", nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("granting: %d %v", resp.StatusCode, body)
	}
	_, err := h.SQL.Exec(
		`UPDATE core.consent_template SET body = 'Something else entirely.' WHERE consent_type = 'care'`)
	if err == nil {
		t.Fatal("an active consent template was edited; a patient agreed to those exact words")
	}
	// Nor deleted: a version that vanishes turns a recorded consent into a consent to nothing.
	if _, err := h.SQL.Exec(`DELETE FROM core.consent_template WHERE consent_type = 'care'`); err == nil {
		t.Error("a consent template was deleted")
	}
}

func TestAThumbprintNeedsAWitnessAndASignatureNeedsItsImage(t *testing.T) {
	h := newAPI(t)

	resp, body := h.grant(t, "care", func(b map[string]any) {
		b["capture_method"] = "thumbprint"
		delete(b, "witnessed_by")
		b["evidence_key"] = "patients/x/consent-1.png"
		b["evidence_sha256"] = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("an unwitnessed thumbprint returned %d: %v", resp.StatusCode, body)
	}

	resp, body = h.grant(t, "care", func(b map[string]any) { b["capture_method"] = "signature" })
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a signature with no image returned %d: %v", resp.StatusCode, body)
	}
}

func TestThereIsNothingToRevokeUntilSomethingWasGranted(t *testing.T) {
	// A revocation of a consent nobody took records a withdrawal that never happened, and a
	// patient asking "did you stop" deserves a truthful answer rather than a reassuring row.
	h := newAPI(t)
	resp, body := h.revoke(t, "outreach", nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}
}

func TestARetriedGrantRecordsOneConsentAndOneEvent(t *testing.T) {
	h := newAPI(t)
	eventID := uuid.Must(uuid.NewV7()).String()
	same := func(b map[string]any) { b["event_id"] = eventID }

	if resp, body := h.grant(t, "care", same); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first: %d %v", resp.StatusCode, body)
	}
	if resp, body := h.grant(t, "care", same); resp.StatusCode != http.StatusCreated {
		t.Fatalf("retry: %d %v", resp.StatusCode, body)
	}

	var events, entries int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ledger.event WHERE event_type = 'CONSENT_GRANTED'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM read.patient_consent_event`).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if events != 1 || entries != 1 {
		t.Errorf("a retried consent produced %d events and %d history rows", events, entries)
	}
}

func TestTheHistoryAnswersWhetherASendWasLawfulAtTheTime(t *testing.T) {
	h := newAPI(t)
	if resp, _ := h.grant(t, "communication", nil); resp.StatusCode != http.StatusCreated {
		t.Fatal("granting failed")
	}
	if resp, _ := h.revoke(t, "communication", func(b map[string]any) {
		b["reason"] = "The patient asked us to stop texting."
	}); resp.StatusCode != http.StatusOK {
		t.Fatal("revoking failed")
	}

	resp, body := h.call(t, http.MethodGet,
		"/v1/patients/"+h.patient.String()+"/consents/history", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}
	entries := body["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %v", entries)
	}
	// Newest first: the revocation, then the grant it withdrew. Both are needed, which is
	// the whole reason a revocation is not an UPDATE of the grant.
	if entries[0].(map[string]any)["action"] != "revoked" {
		t.Errorf("first entry = %v", entries[0])
	}
	if entries[1].(map[string]any)["action"] != "granted" {
		t.Errorf("second entry = %v", entries[1])
	}
}

func TestAnUnknownPurposeIsRefusedRatherThanAllowed(t *testing.T) {
	// A new kind of message added without deciding which consent covers it is exactly the
	// mistake this catches, and failing open would make it invisible.
	h := newAPI(t)
	sender := consent.NewGatedSender(h.gate, &recorder{})
	err := sender.Send(h.ctx(), consent.Message{
		PatientID: h.patient, FacilityID: h.facility, Kind: consent.Purpose("survey"),
	})
	if err == nil {
		t.Fatal("a message with no consent behind its purpose was sent")
	}
}

// recorder is the sender the gate wraps. Counting rather than asserting on content: what is
// being tested is whether the call reached it at all.
type recorder struct{ count int }

func (r *recorder) Send(context.Context, consent.Message) error {
	r.count++
	return nil
}
