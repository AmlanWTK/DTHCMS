package auth_test

import (
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
)

// In-session role switching (CP41, [R-02]).
//
// Four acceptance criteria. Two of them are properties of a screen — "≤2 taps" and "the
// active role is unmistakably visible" — and are verified on the tablet and in the mobile
// suite. The two the server owes are here, and the second is the one that matters:
// **switching to an ungranted role is impossible.**
//
// That is not enforced by this endpoint. It is enforced by the authorisation engine on
// every subsequent request, because the active role travels as a header and a client can
// simply not call this endpoint. What the endpoint owes is that it never *confirms* a role
// the person does not hold — a switcher that said yes and then had every write refused
// would be worse than one that said no.

func switchTo(t *testing.T, s *authServer, bearer, role, from string) response {
	t.Helper()
	body := map[string]string{"role": role}
	if from != "" {
		body["from"] = from
	}
	return s.call(t, "POST", "/v1/auth/active-role", body, bearer, "")
}

func TestSwitchingRoleNeedsNoReauthentication(t *testing.T) {
	// Criterion 1's server half. One login, two hats, no password in between: the clinic
	// this is for has nine staff and twelve stations, and a login per hat is a clinic that
	// stops using the software by Wednesday.
	s := newAuthServer(t)
	s.seedUser(t, "R014", auth.RoleAnthropometry, auth.RoleClinicalAssistant)

	signedIn := s.login(t, "R014", testPassword)
	if signedIn.Status != 200 {
		t.Fatalf("signing in: %d %s", signedIn.Status, signedIn.Raw)
	}
	bearer := signedIn.Body["access_token"].(string)

	for _, role := range []string{"CLINICAL_ASSISTANT", "ANTHROPOMETRY"} {
		got := switchTo(t, s, bearer, role, "")
		if got.Status != 200 {
			t.Fatalf("switching to %s: %d %s", role, got.Status, got.Raw)
		}
		if got.Body["role"] != role {
			t.Errorf("switched to %v, asked for %s", got.Body["role"], role)
		}
		// Criterion 1's other half: the interface must be able to redraw itself to one
		// hat's worth of forms, which needs that role's permissions — not the union.
		grant := got.Body["grant"].(map[string]any)
		if grant["role"] != role {
			t.Errorf("the grant returned is for %v", grant["role"])
		}
		if len(grant["permissions"].([]any)) == 0 {
			t.Errorf("%s came back with no permissions", role)
		}
	}
}

func TestSwitchingToAnUngrantedRoleIsRefused(t *testing.T) {
	// Criterion 4. An anthropometry officer cannot become a physician by asking.
	s := newAuthServer(t)
	s.seedUser(t, "R015", auth.RoleAnthropometry)

	bearer := s.login(t, "R015", testPassword).Body["access_token"].(string)

	got := switchTo(t, s, bearer, "PHYSICIAN", "ANTHROPOMETRY")
	if got.Status != 403 {
		t.Fatalf("switching to an ungranted role answered %d: %s", got.Status, got.Raw)
	}
	// A 403 and not a 404, deliberately: the roles a person holds are not a secret from
	// them, and "you do not have that role" is the sentence that makes a greyed-out entry
	// in the switcher make sense.
	if got.Body["code"] == "NOT_FOUND" {
		t.Error("the refusal pretends the role does not exist")
	}
}

func TestSwitchingToANonsenseRoleIsRefusedAsValidation(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "R016", auth.RoleAnthropometry)
	bearer := s.login(t, "R016", testPassword).Body["access_token"].(string)

	if got := switchTo(t, s, bearer, "", ""); got.Status != 422 {
		t.Errorf("an empty role answered %d, wanted 422", got.Status)
	}
	if got := switchTo(t, s, bearer, "NOT_A_ROLE", ""); got.Status != 403 {
		t.Errorf("an invented role answered %d, wanted 403", got.Status)
	}
}

func TestEveryRoleSwitchIsRecorded(t *testing.T) {
	// Criterion 2's other half. Every event already carries the role active at write time,
	// so "which hat were they wearing" is answerable one event at a time. What the events
	// cannot answer is "when did they change, and to what" — the question somebody asks
	// when a whole run of entries looks wrong.
	s := newAuthServer(t)
	s.seedUser(t, "R017", auth.RoleAnthropometry, auth.RoleClinicalAssistant)
	bearer := s.login(t, "R017", testPassword).Body["access_token"].(string)

	if got := switchTo(t, s, bearer, "CLINICAL_ASSISTANT", "ANTHROPOMETRY"); got.Status != 200 {
		t.Fatalf("switching: %d %s", got.Status, got.Raw)
	}

	var recorded *auth.AuditEntry
	for i := range s.audit.entries {
		if s.audit.entries[i].Kind == "role.switched" {
			recorded = &s.audit.entries[i]
		}
	}
	if recorded == nil {
		t.Fatalf("no role.switched entry; the trail holds %v", s.audit.kinds())
	}
	if recorded.Before["role"] != "ANTHROPOMETRY" || recorded.After["role"] != "CLINICAL_ASSISTANT" {
		t.Errorf("the entry says %v → %v", recorded.Before["role"], recorded.After["role"])
	}
	if recorded.ActorCode != "R017" {
		t.Errorf("the switch is attributed to %q", recorded.ActorCode)
	}
	// The trail is a record of a *person's* own act on themselves, which is why the target
	// is the same user. An entry with an empty target would render as "switched — to —".
	if recorded.TargetUserID == nil || *recorded.TargetUserID != recorded.ActorID {
		t.Error("the switch does not name whose role changed")
	}
}

func TestARevokedRoleDisappearsFromTheSwitcherImmediately(t *testing.T) {
	// The reason this endpoint reads grants live rather than issuing a token. A role
	// revoked while the operator is holding the phone must stop being switchable at once —
	// not when a token expires.
	s := newAuthServer(t)
	id := s.seedUser(t, "R018", auth.RoleAnthropometry, auth.RoleClinicalAssistant)
	bearer := s.login(t, "R018", testPassword).Body["access_token"].(string)

	if got := switchTo(t, s, bearer, "CLINICAL_ASSISTANT", ""); got.Status != 200 {
		t.Fatalf("the first switch: %d %s", got.Status, got.Raw)
	}

	// Revoked the way the console revokes: the grant is ended, never deleted.
	if _, err := s.db.SQL.Exec(`
		UPDATE core.user_role SET revoked_at = now(), revoke_reason = 'moved stations'
		 WHERE user_id = $1
		   AND role_id = (SELECT id FROM core.role WHERE code = 'CLINICAL_ASSISTANT')`,
		id); err != nil {
		t.Fatal(err)
	}

	if got := switchTo(t, s, bearer, "CLINICAL_ASSISTANT", ""); got.Status != 403 {
		t.Errorf("a revoked role is still switchable: %d %s", got.Status, got.Raw)
	}
}

func TestTheSwitcherTellsAStationAppWhichStationItIsNowWorking(t *testing.T) {
	// The station app's own need. An operator's queue is their station's queue, and a
	// screen that asks them to choose is a screen where somebody calls a patient to the
	// wrong room (CP39). The station comes back with the grant so the app never guesses.
	s := newAuthServer(t)
	s.seedUser(t, "R019", auth.RoleAnthropometry)
	bearer := s.login(t, "R019", testPassword).Body["access_token"].(string)

	got := switchTo(t, s, bearer, "ANTHROPOMETRY", "")
	if got.Status != 200 {
		t.Fatalf("switching: %d %s", got.Status, got.Raw)
	}
	grant := got.Body["grant"].(map[string]any)
	if grant["station"] != "STN_ANTHROPOMETRY" {
		t.Errorf("the anthropometry hat reports station %v", grant["station"])
	}
}
