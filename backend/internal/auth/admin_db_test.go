package auth_test

import (
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// CP21's acceptance criteria, against the real router and schema:
//
//	(1) a new staff member can be created and productive without developer involvement;
//	(2) the effective-permission preview matches actual behaviour;
//	(3) every administrative action is audited with actor and timestamp;
//	(4) admin routes require step-up authentication — and non-admins cannot reach any.

// adminSession signs an administrator in and enrols their authenticator, returning the
// access token and the TOTP secret with which step-up tokens are minted.
func (s *authServer) adminSession(t *testing.T) (token, secret string) {
	t.Helper()
	s.seedUser(t, "A001", auth.RoleAdmin)
	res := s.login(t, "A001", testPassword)
	if res.Status != http.StatusOK {
		t.Fatalf("admin login: %d %s", res.Status, res.Raw)
	}
	token = accessToken(t, res)
	secret, _ = s.enrolOverHTTP(t, token)
	return token, secret
}

// stepUp mints a step-up token for a purpose. The clock is advanced a step so the code is
// not the one enrolment just spent (replay guard).
func (s *authServer) stepUp(t *testing.T, token, secret, purpose string) string {
	t.Helper()
	s.clock.Advance(30 * time.Second)
	res := s.call(t, "POST", "/v1/auth/step-up",
		map[string]string{"purpose": purpose, "code": codeNow(t, secret, s.clock.Now())}, token, "")
	if res.Status != http.StatusOK {
		t.Fatalf("step-up for %s: %d %s", purpose, res.Status, res.Raw)
	}
	return res.Body["step_up_token"].(string)
}

// adminCall is call with a step-up token attached.
func (s *authServer) adminCall(t *testing.T, method, path string, body any, token, stepUp string) response {
	t.Helper()
	req := s.buildRequest(t, method, path, body, token)
	if stepUp != "" {
		req.Header.Set(httpx.StepUpHeader, stepUp)
	}
	return s.do(t, req)
}

func TestNonAdminsCannotReachAnyAdminRoute(t *testing.T) {
	// Criterion 4, the authorisation half: walk the declarations and hit every /v1/admin
	// route as a physician — who holds audit.read and a great deal else, but nothing the
	// console asks for.
	s := newAuthServer(t)
	s.seedUser(t, "P001", auth.RolePhysician)
	physician := accessToken(t, s.login(t, "P001", testPassword))

	decls, err := httpx.Declarations(s.router)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for route := range decls {
		if !strings.Contains(route, "/v1/admin") {
			continue
		}
		n++
		method, path, _ := strings.Cut(route, " ")
		path = strings.ReplaceAll(path, "{id}", uuid.New().String())
		path = strings.ReplaceAll(path, "{role}", "QA")
		var body any
		if method == "POST" {
			body = map[string]string{"reason": "because", "status": "suspended", "role": "QA", "password": "twelve characters long"}
		}
		res := s.call(t, method, path, body, physician, "")
		if res.Status != http.StatusForbidden {
			t.Errorf("%s: physician got %d, want 403: %s", route, res.Status, res.Raw)
		}
		// And nothing about the resource, or the missing step-up, leaks: it is the plain
		// FORBIDDEN, because permission is decided before step-up is even asked for.
		if code := res.Body["error"].(map[string]any)["code"]; code != "FORBIDDEN" {
			t.Errorf("%s: code %v", route, code)
		}
	}
	if n < 10 {
		t.Fatalf("only %d admin routes walked", n)
	}
}

func TestAdminWritesRequireAStepUp(t *testing.T) {
	s := newAuthServer(t)
	token, secret := s.adminSession(t)
	s.seedUser(t, "N001", auth.RoleAnthropometry)
	var nurseID string
	if err := s.db.SQL.QueryRow(`SELECT id FROM core.app_user WHERE employee_code = 'N001'`).Scan(&nurseID); err != nil {
		t.Fatal(err)
	}

	// No token: STEP_UP_REQUIRED, distinct from FORBIDDEN — the client opens the prompt.
	res := s.adminCall(t, "POST", "/v1/admin/users/"+nurseID+"/status", map[string]string{"status": "suspended", "reason": "review"}, token, "")
	if res.Status != http.StatusForbidden || res.Body["error"].(map[string]any)["code"] != "STEP_UP_REQUIRED" {
		t.Fatalf("without step-up: %d %s", res.Status, res.Raw)
	}
	// A token for the other purpose: refused. A credential reset is not an account change.
	wrong := s.stepUp(t, token, secret, auth.PurposeResetCredential)
	res = s.adminCall(t, "POST", "/v1/admin/users/"+nurseID+"/status", map[string]string{"status": "suspended", "reason": "review"}, token, wrong)
	if res.Status != http.StatusForbidden {
		t.Fatalf("wrong-purpose step-up: %d %s", res.Status, res.Raw)
	}
	// The right one: works, once.
	right := s.stepUp(t, token, secret, auth.PurposeManageUsers)
	res = s.adminCall(t, "POST", "/v1/admin/users/"+nurseID+"/status", map[string]string{"status": "suspended", "reason": "under review"}, token, right)
	if res.Status != http.StatusOK || res.Body["status"] != "suspended" {
		t.Fatalf("suspend: %d %s", res.Status, res.Raw)
	}
	res = s.adminCall(t, "POST", "/v1/admin/users/"+nurseID+"/status", map[string]string{"status": "active", "reason": "cleared"}, token, right)
	if res.Status != http.StatusForbidden {
		t.Fatalf("a spent step-up must not work twice: %d %s", res.Status, res.Raw)
	}
	// Reads need no step-up.
	if res := s.call(t, "GET", "/v1/admin/users", nil, token, ""); res.Status != http.StatusOK {
		t.Fatalf("list: %d %s", res.Status, res.Raw)
	}
}

func TestANewStaffMemberIsProductiveWithoutADeveloper(t *testing.T) {
	// Criteria 1 and 2: invite with two roles and a password; sign in as them; what /me
	// reports equals the console's preview; revoke one role and the change is felt.
	s := newAuthServer(t)
	token, secret := s.adminSession(t)

	invite := s.adminCall(t, "POST", "/v1/admin/users", map[string]any{
		"employee_code": "anth_02", "name_en": "Shirin Akter", "name_bn": "শিরিন আক্তার",
		"phone": "+8801700000000", "roles": []string{"ANTHROPOMETRY", "COUNSELOR"},
		"password": "three bangla words here",
	}, token, s.stepUp(t, token, secret, auth.PurposeManageUsers))
	if invite.Status != http.StatusCreated {
		t.Fatalf("invite: %d %s", invite.Status, invite.Raw)
	}
	if invite.Body["status"] != "active" || invite.Body["employee_code"] != "ANTH_02" {
		t.Fatalf("invited account: %s", invite.Raw)
	}
	userID := invite.Body["id"].(string)
	preview := stringsOf(invite.Body["permissions"])

	// The person signs in with what the administrator typed, and holds exactly the preview.
	login := s.login(t, "ANTH_02", "three bangla words here")
	if login.Status != http.StatusOK {
		t.Fatalf("new staff login: %d %s", login.Status, login.Raw)
	}
	actual := stringsOf(login.Body["user"].(map[string]any)["permissions"])
	if !reflect.DeepEqual(preview, actual) {
		t.Fatalf("criterion 2: preview %v, actual %v", preview, actual)
	}
	if !contains(actual, auth.PermCounselingTick) || !contains(actual, auth.PermObservationWriteAnthro) {
		t.Fatalf("both roles' permissions expected: %v", actual)
	}

	// The catalogue endpoint says what each role would add — the preview before granting.
	roles := s.call(t, "GET", "/v1/admin/roles", nil, token, "")
	if roles.Status != http.StatusOK || len(roles.Body["roles"].([]any)) != len(auth.AllRoles) {
		t.Fatalf("roles: %d %s", roles.Status, roles.Raw)
	}

	// Revoke the counselor role: the next request no longer has it.
	revoke := s.adminCall(t, "POST", "/v1/admin/users/"+userID+"/roles/COUNSELOR/revoke",
		map[string]string{"reason": "moved to anthropometry only"}, token, s.stepUp(t, token, secret, auth.PurposeManageUsers))
	if revoke.Status != http.StatusOK {
		t.Fatalf("revoke: %d %s", revoke.Status, revoke.Raw)
	}
	me := s.call(t, "GET", "/v1/auth/me", nil, accessToken(t, login), "")
	if me.Status != http.StatusOK || contains(stringsOf(me.Body["permissions"]), auth.PermCounselingTick) {
		t.Fatalf("after revocation: %d %s", me.Status, me.Raw)
	}
	if !reflect.DeepEqual(stringsOf(revoke.Body["permissions"]), stringsOf(me.Body["permissions"])) {
		t.Fatalf("criterion 2 after revocation: console %v, actual %v", stringsOf(revoke.Body["permissions"]), stringsOf(me.Body["permissions"]))
	}

	// A duplicate code, a weak password, an unknown role: validation, not 500.
	for name, body := range map[string]map[string]any{
		"duplicate": {"employee_code": "ANTH_02", "name_en": "x", "name_bn": "y", "roles": []string{}},
		"weak":      {"employee_code": "ANTH_03", "name_en": "x", "name_bn": "y", "roles": []string{}, "password": "short"},
		"role":      {"employee_code": "ANTH_04", "name_en": "x", "name_bn": "y", "roles": []string{"WIZARD"}},
	} {
		res := s.adminCall(t, "POST", "/v1/admin/users", body, token, s.stepUp(t, token, secret, auth.PurposeManageUsers))
		if res.Status != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, res.Status, res.Raw)
		}
	}
}

func TestCredentialResets(t *testing.T) {
	s := newAuthServer(t)
	token, secret := s.adminSession(t)
	s.seedUser(t, "P001", auth.RolePhysician)
	var physicianID, adminID string
	_ = s.db.SQL.QueryRow(`SELECT id FROM core.app_user WHERE employee_code = 'P001'`).Scan(&physicianID)
	_ = s.db.SQL.QueryRow(`SELECT id FROM core.app_user WHERE employee_code = 'A001'`).Scan(&adminID)

	// The physician signs in on two devices and enrols an authenticator.
	one := accessToken(t, s.login(t, "P001", testPassword))
	two := accessToken(t, s.login(t, "P001", testPassword))
	s.enrolOverHTTP(t, one)

	// Forced logout: both sessions end.
	ended := s.adminCall(t, "POST", "/v1/admin/users/"+physicianID+"/sessions/end",
		map[string]string{"reason": "tablet left on the bus"}, token, s.stepUp(t, token, secret, auth.PurposeResetCredential))
	if ended.Status != http.StatusOK || ended.Body["sessions_ended"].(float64) != 2 {
		t.Fatalf("end sessions: %d %s", ended.Status, ended.Raw)
	}
	for _, tok := range []string{one, two} {
		if res := s.call(t, "GET", "/v1/auth/me", nil, tok, ""); res.Status != http.StatusUnauthorized {
			t.Fatalf("a session survived the forced logout: %d", res.Status)
		}
	}

	// Password set in person: the old one stops, the new one works.
	set := s.adminCall(t, "POST", "/v1/admin/users/"+physicianID+"/password",
		map[string]string{"password": "a fresh passphrase for P001", "reason": "forgotten, reset at the desk"},
		token, s.stepUp(t, token, secret, auth.PurposeResetCredential))
	if set.Status != http.StatusNoContent {
		t.Fatalf("set password: %d %s", set.Status, set.Raw)
	}
	if res := s.login(t, "P001", testPassword); res.Status != http.StatusUnauthorized {
		t.Fatalf("old password: %d", res.Status)
	}
	// Enrolled: the new password earns a challenge, not a session — the factor is intact.
	if res := s.login(t, "P001", "a fresh passphrase for P001"); res.Status != http.StatusAccepted {
		t.Fatalf("new password, enrolled: %d %s", res.Status, res.Raw)
	}

	// Second-factor reset for a lost phone and lost codes: the factor is gone, the next
	// sign-in is a plain one, and /me says enrolment is owed again.
	reset := s.adminCall(t, "POST", "/v1/admin/users/"+physicianID+"/second-factor/reset",
		map[string]string{"reason": "phone and codes lost, identity checked in person"},
		token, s.stepUp(t, token, secret, auth.PurposeResetCredential))
	if reset.Status != http.StatusNoContent {
		t.Fatalf("reset factor: %d %s", reset.Status, reset.Raw)
	}
	after := s.login(t, "P001", "a fresh passphrase for P001")
	if after.Status != http.StatusOK {
		t.Fatalf("after the reset: %d %s", after.Status, after.Raw)
	}
	sf := after.Body["user"].(map[string]any)["second_factor"].(map[string]any)
	if sf["enrolled"] != false || sf["required"] != true {
		t.Fatalf("second factor after reset: %v", sf)
	}

	// The administrator cannot reset their own factor from the session it protects.
	self := s.adminCall(t, "POST", "/v1/admin/users/"+adminID+"/second-factor/reset",
		map[string]string{"reason": "trying"}, token, s.stepUp(t, token, secret, auth.PurposeResetCredential))
	if self.Status != http.StatusConflict {
		t.Fatalf("self reset: %d %s", self.Status, self.Raw)
	}

	// Criterion 3: every act was recorded with the actor and a timestamp.
	kinds := s.audit.kinds()
	sort.Strings(kinds)
	want := []string{"password.set", "second_factor.reset", "sessions.ended"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("audit kinds = %v, want %v", kinds, want)
	}
	for _, e := range s.audit.entries {
		if e.ActorID.String() != adminID || e.At.IsZero() || e.TargetUserID == nil || e.Reason == "" {
			t.Errorf("audit entry incomplete: %+v", e)
		}
	}
}

func stringsOf(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, i := range items {
		out = append(out, i.(string))
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
