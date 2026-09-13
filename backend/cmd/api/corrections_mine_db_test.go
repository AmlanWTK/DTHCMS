package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
)

// `GET /v1/corrections/mine`, and the reach ADR-0036 has no section for (CP85).
//
// # Why it was refused
//
// The route's permission union admits a FIELD_WORKER, whose reach is `own` — the records
// they made — and eight station roles whose reach is their own station. ADR-0036 §1 asks
// "is this patient at your station in the current visit", which is the wrong question here
// in both halves: a correction is raised about a value recorded earlier, so the patient has
// walked on by the time anybody answers, and the field worker stands at no station at all.
// So the route declared no resource check, and the guard refused it for every narrow role —
// which is every role the workflow is *for*.
//
// # What it is instead
//
// Ownership. The rows are the caller's own, and restricting to them is narrower than every
// reach in the scope table, so it satisfies whichever of the route's permissions let the
// caller in. The ADR is missing a §1(c) and `rbac.AuthorizeOwnList` is it in code.
//
// # What this test holds
//
// Two operators, one request each, and neither sees the other's. Two rather than one
// because a handler that ignored the caller entirely and returned the whole table would
// pass a one-operator test perfectly.

// flagFor writes one open correction request assigned to an operator.
func (s *registrationStack) flagFor(t *testing.T, assignee uuid.UUID, note string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := s.db.SQL.Exec(`
		INSERT INTO read.correction_request
		  (id, facility_id, patient_id, observation_id, code, requested_at, requested_by,
		   requested_role, reason_code, note, assigned_to, status, global_seq, event_id)
		VALUES ($1, $2, gen_random_uuid(), gen_random_uuid(), 'BP_SYS', now(), $3,
		        'PHYSICIAN', (SELECT code FROM core.correction_reason ORDER BY code LIMIT 1),
		        $4, $3, 'OPEN',
		        (SELECT coalesce(max(global_seq), 0) + 1 FROM read.correction_request),
		        gen_random_uuid())`,
		id, s.facility, assignee, note); err != nil {
		t.Fatalf("writing a correction request: %v", err)
	}
	return id
}

func correctionIDsIn(t *testing.T, body string) map[string]bool {
	t.Helper()
	var decoded struct {
		Requests []struct {
			ID string `json:"id"`
		} `json:"requests"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("the queue did not decode: %v\n%s", err, body)
	}
	out := map[string]bool{}
	for _, r := range decoded.Requests {
		out[r.ID] = true
	}
	return out
}

func TestMyCorrectionsReturnsOnlyTheCallersOwnQueue(t *testing.T) {
	s := newRegistrationStack(t, mountReferenceModules)

	// Two operators at two stations, each with one request on their queue.
	s.seedClerk(t, "ANT-CP85C", auth.RoleAnthropometry)
	anthroUser := s.user
	anthroDesk := s.enrol(t, "Anthropometry desk 85", "STN_ANTHROPOMETRY")
	anthroToken := s.signIn(t, "ANT-CP85C", anthroDesk)

	s.seedClerk(t, "CNS-CP85C", auth.RoleCounselor)
	counselUser := s.user
	counselDesk := s.enrol(t, "Counselling desk 85", "STN_COUNSELING")
	counselToken := s.signIn(t, "CNS-CP85C", counselDesk)

	anthroFlag := s.flagFor(t, anthroUser, "re-measure the height")
	counselFlag := s.flagFor(t, counselUser, "the smoking answer is wrong")

	for _, who := range []struct {
		label string
		token string
		role  auth.RoleCode
		own   uuid.UUID
		other uuid.UUID
	}{
		{"anthropometry", anthroToken, auth.RoleAnthropometry, anthroFlag, counselFlag},
		{"counsellor", counselToken, auth.RoleCounselor, counselFlag, anthroFlag},
	} {
		status, body := s.get(t, who.token, string(who.role), "/v1/corrections/mine")
		if status != http.StatusOK {
			t.Fatalf("%s: GET /v1/corrections/mine answered %d: %s\n\n"+
				"This route is the operator's own queue. A 403 here means it is back on a "+
				"station reach, which cannot express ownership and refuses every role the "+
				"workflow is for.", who.label, status, body)
		}
		shown := correctionIDsIn(t, body)
		if !shown[who.own.String()] {
			t.Errorf("%s cannot see their own request: %s", who.label, body)
		}
		if shown[who.other.String()] {
			t.Errorf("%s can see somebody else's request: %s\n\n"+
				"The ownership reach is the whole of this route's authorisation. A queue that "+
				"shows another operator's corrections is a list of colleagues' mistakes.",
				who.label, body)
		}
	}
}
