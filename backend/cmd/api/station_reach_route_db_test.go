package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The escalation, through the real thing (CP84, ADR-0036).
//
// internal/rbac's reach_db_test.go holds the rule against the real tables and the real
// query, one function call from the engine. This holds the same rule one layer further out:
// a real sign-in, a real session, the real middleware chain, the real route guard, the real
// scope debt and the real handler. The difference matters because every layer between those
// two is a layer that has, at some point in this codebase's history, quietly decided
// something on its own — the guard invented a resource, the read path demanded a device, the
// scope table swept a desk in with a prefix. A rule that holds in the engine and not on the
// wire is a rule the clinic does not have.
//
// The shape is deliberately the one an attacker would use: an ordinary station officer, with
// a valid session at their own workstation, wearing a hat they really hold, asking for a
// patient by id. No forged header, no stolen token — just a uuid they should not be able to
// open. That is what a nosy colleague looks like, and §4.4 exists for them and not for a
// cryptographer.

// reachFixture puts a patient in the building at one station and hands back their id.
func (s *registrationStack) reachFixture(t *testing.T, station, status string) uuid.UUID {
	t.Helper()
	var patient uuid.UUID
	if err := s.db.SQL.QueryRow(`
		INSERT INTO core.patient
		  (facility_id, clinical_id, name_en, sex, birth_date, dob_verified_by,
		   phone_primary, registered_by)
		VALUES ($1, $2, 'Reach Subject', 'female', '1988-02-02', 'patient_stated',
		        '+8801712345670', $3)
		RETURNING id`, s.facility, fmt.Sprintf("DTH-REACH-%s", uuid.NewString()[:8]), s.user).Scan(&patient); err != nil {
		t.Fatalf("creating the patient: %v", err)
	}
	// The allergy gate (CP54) refuses a queue entry past station 4 without a status. Not
	// what this test is about.
	if _, err := s.db.SQL.Exec(`
		INSERT INTO read.allergy_assertion
		  (id, event_id, facility_id, patient_id, kind, asserted_at, asserted_by, global_seq)
		VALUES (gen_random_uuid(), gen_random_uuid(), $1, $2, 'NO_KNOWN_ALLERGY', now(), $3,
		        (SELECT coalesce(max(global_seq), 0) + 1 FROM read.allergy_assertion))`,
		s.facility, patient, s.user); err != nil {
		t.Fatalf("clearing the allergy gate: %v", err)
	}
	if station == "" {
		return patient
	}
	var visit uuid.UUID
	if err := s.db.SQL.QueryRow(`
		INSERT INTO core.visit (facility_id, patient_id, visit_code, visit_type, clinic_day, opened_by)
		VALUES ($1, $2, $3, 'new', current_date, $4) RETURNING id`,
		s.facility, patient, "V-REACH-"+uuid.NewString()[:8], s.user).Scan(&visit); err != nil {
		t.Fatalf("opening the visit: %v", err)
	}
	if _, err := s.db.SQL.Exec(`
		INSERT INTO core.queue_entry
		  (facility_id, visit_id, patient_id, station_code, status, clinic_day, called_at, called_by)
		VALUES ($1, $2, $3, $4, $5, current_date, now(), $6)`,
		s.facility, visit, patient, station, status, s.user); err != nil {
		t.Fatalf("queueing at %s: %v", station, err)
	}
	return patient
}

// get is a read through the real chain, returning the status and the body.
func (s *registrationStack) get(t *testing.T, token, role, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("Authorization", "Bearer "+token)
	if role != "" {
		req.Header.Set(httpx.ActiveRoleHeader, role)
	}
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	payload, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(payload)
}

// The test this checkpoint exists for.
func TestAStationOfficerCannotOpenAPatientWhoIsNotOnTheirQueue(t *testing.T) {
	s := newRegistrationStack(t)
	s.seedClerk(t, "NUT-CP84", auth.RoleNutritionist)
	workstation := s.enrol(t, "Nutrition desk 1", "STN_NUTRITION")
	token := s.signIn(t, "NUT-CP84", workstation)
	hat := string(auth.RoleNutritionist)

	mine := s.reachFixture(t, string(auth.StationNutrition), "in_service")
	theirs := s.reachFixture(t, string(auth.StationAnthropometry), "in_service")
	unrouted := s.reachFixture(t, "", "")

	// Their own patient opens.
	if status, body := s.get(t, token, hat, "/v1/patients/"+mine.String()); status != http.StatusOK {
		t.Fatalf("the nutritionist cannot open the patient at their own station: %d %s\n"+
			"If this is a 403 the clinic does not work; if it is a 500 the reach store is not wired.",
			status, body)
	}

	// The patient at the next desk does not.
	status, body := s.get(t, token, hat, "/v1/patients/"+theirs.String())
	if status != http.StatusForbidden {
		t.Fatalf("the nutritionist opened a patient queued to anthropometry: %d %s\n\n"+
			"This is the escalation ADR-0036 exists to prevent, and it is the one thing in "+
			"CP84 that must not regress.", status, body)
	}

	// Nor does one nobody has routed anywhere.
	unroutedStatus, unroutedBody := s.get(t, token, hat, "/v1/patients/"+unrouted.String())
	if unroutedStatus != http.StatusForbidden {
		t.Fatalf("the nutritionist opened a patient with no visit: %d %s", unroutedStatus, unroutedBody)
	}

	// A patient who does not exist at all answers exactly the same, which is what makes the
	// two refusals above safe to make: a caller cannot sort ids into real and imaginary.
	absentStatus, absentBody := s.get(t, token, hat, "/v1/patients/"+uuid.NewString())
	if absentStatus != http.StatusForbidden {
		t.Fatalf("an id that names nobody answered %d: %s\n"+
			"A different answer here is an existence oracle over the whole register.",
			absentStatus, absentBody)
	}
	for _, pair := range [][2]string{{body, unroutedBody}, {body, absentBody}} {
		if messageOf(t, pair[0]) != messageOf(t, pair[1]) {
			t.Errorf("two refusals read differently:\n  %s\n  %s", pair[0], pair[1])
		}
	}

	// And nothing in any refusal names a patient.
	for _, refusal := range []string{body, unroutedBody, absentBody} {
		for _, id := range []uuid.UUID{mine, theirs, unrouted} {
			if contains(refusal, id.String()) {
				t.Errorf("a refusal names a patient id: %s", refusal)
			}
		}
	}
}

// The write reach is narrower than the read reach, on the wire.
//
// The counsellor has finished with this patient: `done` on the queue. They may still open
// the record — the counsellor re-reading what they just wrote is the ordinary case — and
// they may not amend it, because that is the correction workflow's job and it is a
// different, flagged path on purpose.
func TestAFinishedPatientIsReadableAndNotWritableOnTheWire(t *testing.T) {
	s := newRegistrationStack(t)
	s.seedClerk(t, "REG-CP84W", auth.RoleRegistration)
	workstation := s.enrol(t, "Registration desk 9", "STN_REGISTRATION")
	regToken := s.signIn(t, "REG-CP84W", workstation)
	_ = regToken

	// A second person, at the counselling desk.
	s.seedClerk(t, "CNS-CP84W", auth.RoleCounselor)
	counselDesk := s.enrol(t, "Counselling desk 9", "STN_COUNSELING")
	token := s.signIn(t, "CNS-CP84W", counselDesk)
	hat := string(auth.RoleCounselor)

	patient := s.reachFixture(t, string(auth.StationCounseling), "done")

	if status, body := s.get(t, token, hat, "/v1/patients/"+patient.String()); status != http.StatusOK {
		t.Fatalf("the counsellor cannot re-open the record they just wrote: %d %s\n"+
			"A read reach that stops at `done` makes the software slower than the paper it replaces.",
			status, body)
	}

	// The write. PATCH /v1/patients/{id} is `patient.write.demographics`, which the
	// counsellor does not hold at all — so the interesting write here is the one they do
	// hold, and it is checked in internal/rbac against the engine. What this asserts on the
	// wire is the half that can be asserted here: the read opened and the correction path is
	// what a finished record goes through.
	status, body := s.patch(t, token, hat, "/v1/patients/"+patient.String(),
		map[string]any{"event_id": uuid.Must(uuid.NewV7()).String(), "reason": "typo", "name_en": "Changed"})
	if status != http.StatusForbidden {
		t.Fatalf("a counsellor amended a patient record: %d %s", status, body)
	}
}

func (s *registrationStack) patch(t *testing.T, token, role, path string, body any) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPatch, s.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	req.Header.Set("Authorization", "Bearer "+token)
	if role != "" {
		req.Header.Set(httpx.ActiveRoleHeader, role)
	}
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	payload, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(payload)
}

func messageOf(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			MessageBN string `json:"message_bn"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return body
	}
	return envelope.Error.Code + "|" + envelope.Error.Message + "|" + envelope.Error.MessageBN
}

func contains(haystack, needle string) bool {
	return needle != "" && strings.Contains(haystack, needle)
}
