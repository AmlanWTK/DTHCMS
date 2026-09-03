package auth_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/totp"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// CP17's acceptance criteria, against the real router and the real schema:
//
//   (1) enrolment works with what a standard authenticator app would compute;
//   (2) a privileged endpoint is unreachable without a step-up, proven here;
//   (3) recovery codes work once;
//   (4) the seed is encrypted at rest — nothing in the table decrypts without the key,
//       and the application role cannot delete or rewrite any of it.

// enrolOverHTTP takes a signed-in user through enrolment and returns the seed and the
// recovery codes, exactly as an authenticator app and a screen would have them.
func (s *authServer) enrolOverHTTP(t *testing.T, token string) (secret string, recovery []string) {
	t.Helper()
	begin := s.call(t, "POST", "/v1/auth/second-factor/enrol", nil, token, "")
	if begin.Status != http.StatusOK {
		t.Fatalf("enrol: %d %s", begin.Status, begin.Raw)
	}
	secret, _ = begin.Body["secret"].(string)
	uri, _ := begin.Body["otpauth_uri"].(string)
	if !strings.HasPrefix(uri, "otpauth://totp/DTHCMS:") || !strings.Contains(uri, secret) {
		t.Fatalf("otpauth_uri = %q", uri)
	}

	confirm := s.call(t, "POST", "/v1/auth/second-factor/confirm",
		map[string]string{"code": codeNow(t, secret, s.clock.Now())}, token, "")
	if confirm.Status != http.StatusOK {
		t.Fatalf("confirm: %d %s", confirm.Status, confirm.Raw)
	}
	for _, c := range confirm.Body["recovery_codes"].([]any) {
		recovery = append(recovery, c.(string))
	}
	return secret, recovery
}

func codeNow(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.Code(secret, totp.Step(at))
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// --- criterion 1: enrolment, and sign-in afterwards ---

func TestEnrolmentTurnsSignInIntoTwoSteps(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001", auth.RolePhysician)

	// Before: a physician signs in with a password alone, and /me says enrolment is owed.
	first := s.login(t, "E001", testPassword)
	if first.Status != http.StatusOK {
		t.Fatalf("login before enrolment: %d %s", first.Status, first.Raw)
	}
	sf := first.Body["user"].(map[string]any)["second_factor"].(map[string]any)
	if sf["required"] != true || sf["enrolled"] != false {
		t.Errorf("second_factor before enrolment = %v", sf)
	}

	secret, recovery := s.enrolOverHTTP(t, accessToken(t, first))
	if len(recovery) != auth.RecoveryCodeCount {
		t.Fatalf("%d recovery codes", len(recovery))
	}

	// After: the password earns a challenge, not a session.
	s.clock.Advance(time.Minute)
	challenged := s.login(t, "E001", testPassword)
	if challenged.Status != http.StatusAccepted {
		t.Fatalf("login after enrolment: %d %s, want 202", challenged.Status, challenged.Raw)
	}
	if _, leaked := challenged.Body["access_token"]; leaked {
		t.Fatal("a challenge response carried an access token")
	}
	if len(challenged.Cookies) != 0 {
		t.Errorf("a challenge response set %d cookies", len(challenged.Cookies))
	}
	challenge, _ := challenged.Body["challenge"].(string)

	// A wrong code is one refusal, recorded against the account.
	wrong := s.call(t, "POST", "/v1/auth/login/second-factor",
		map[string]string{"challenge": challenge, "code": "000000"}, "", "")
	if wrong.Status != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d %s", wrong.Status, wrong.Raw)
	}
	var kinds []string
	rows, err := s.db.SQL.Query(`SELECT failure_kind FROM core.login_attempt ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		kinds = append(kinds, k)
	}
	_ = rows.Close()
	if want := "second_factor_pending,bad_second_factor"; !strings.HasSuffix(strings.Join(kinds, ","), want) {
		t.Errorf("attempt kinds = %v, want …%s", kinds, want)
	}

	// The right code, and a session with cookies, like any other sign-in.
	done := s.call(t, "POST", "/v1/auth/login/second-factor",
		map[string]string{"challenge": challenge, "code": codeNow(t, secret, s.clock.Now())}, "", "")
	if done.Status != http.StatusOK {
		t.Fatalf("right code: %d %s", done.Status, done.Raw)
	}
	sessionCookie(t, done)
	me := s.call(t, "GET", "/v1/auth/me", nil, accessToken(t, done), "")
	sf = me.Body["second_factor"].(map[string]any)
	if sf["enrolled"] != true || sf["recovery_codes_left"] != float64(auth.RecoveryCodeCount) {
		t.Errorf("second_factor after enrolment = %v", sf)
	}

	// The challenge is spent.
	s.clock.Advance(time.Minute)
	again := s.call(t, "POST", "/v1/auth/login/second-factor",
		map[string]string{"challenge": challenge, "code": codeNow(t, secret, s.clock.Now())}, "", "")
	if again.Status != http.StatusUnauthorized {
		t.Errorf("a spent challenge completed again: %d", again.Status)
	}
}

func TestFiveWrongCodesKillTheChallenge(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001", auth.RoleAdmin)
	secret, _ := s.enrolOverHTTP(t, accessToken(t, s.login(t, "E001", testPassword)))

	s.clock.Advance(time.Minute)
	challenge, _ := s.login(t, "E001", testPassword).Body["challenge"].(string)
	for i := 0; i < auth.ChallengeMaxFailures; i++ {
		s.call(t, "POST", "/v1/auth/login/second-factor",
			map[string]string{"challenge": challenge, "code": "000000"}, "", "")
	}
	res := s.call(t, "POST", "/v1/auth/login/second-factor",
		map[string]string{"challenge": challenge, "code": codeNow(t, secret, s.clock.Now())}, "", "")
	if res.Status != http.StatusUnauthorized {
		t.Errorf("the right code revived an exhausted challenge: %d", res.Status)
	}
}

// --- criterion 2: privileged endpoints need a step-up ---

func TestAPrivilegedEndpointIsUnreachableWithoutAStepUp(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001", auth.RolePhysician)
	token := accessToken(t, s.login(t, "E001", testPassword))
	secret, _ := s.enrolOverHTTP(t, token)
	s.clock.Advance(time.Minute)

	withStepUp := func(stepUp string) response {
		req, _ := http.NewRequest("POST", s.URL+"/v1/auth/second-factor/disable", strings.NewReader(`{"reason":"test"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(httpx.RequestedWithHeader, httpx.RequestedWithValue)
		if stepUp != "" {
			req.Header.Set(httpx.StepUpHeader, stepUp)
		}
		res, err := s.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		out := response{Status: res.StatusCode}
		return out
	}

	// No token: refused, with the code the client branches on.
	bare := s.call(t, "POST", "/v1/auth/second-factor/disable", map[string]string{"reason": "test"}, token, "")
	if bare.Status != http.StatusForbidden {
		t.Fatalf("without a step-up: %d %s, want 403", bare.Status, bare.Raw)
	}
	if code := bare.Body["error"].(map[string]any)["code"]; code != "STEP_UP_REQUIRED" {
		t.Errorf("error code = %v, want STEP_UP_REQUIRED", code)
	}

	// A step-up for a different purpose does not open this door.
	other := s.call(t, "POST", "/v1/auth/step-up",
		map[string]string{"purpose": auth.PurposeRecoveryCodes, "code": codeNow(t, secret, s.clock.Now())}, token, "")
	if other.Status != http.StatusOK {
		t.Fatalf("step-up: %d %s", other.Status, other.Raw)
	}
	if res := withStepUp(other.Body["step_up_token"].(string)); res.Status != http.StatusForbidden {
		t.Errorf("a step-up for another purpose was accepted: %d", res.Status)
	}

	// A wrong code mints nothing. An undeclared purpose is refused before the code is read.
	if res := s.call(t, "POST", "/v1/auth/step-up",
		map[string]string{"purpose": auth.PurposeDisableSecondFactor, "code": "000000"}, token, ""); res.Status != http.StatusUnauthorized {
		t.Errorf("a wrong code at step-up: %d", res.Status)
	}
	if res := s.call(t, "POST", "/v1/auth/step-up",
		map[string]string{"purpose": "make.tea", "code": "000000"}, token, ""); res.Status != http.StatusBadRequest {
		t.Errorf("an undeclared purpose: %d", res.Status)
	}

	// The right step-up, for this purpose: the door opens once.
	s.clock.Advance(totp.Period)
	right := s.call(t, "POST", "/v1/auth/step-up",
		map[string]string{"purpose": auth.PurposeDisableSecondFactor, "code": codeNow(t, secret, s.clock.Now())}, token, "")
	if right.Status != http.StatusOK {
		t.Fatalf("step-up: %d %s", right.Status, right.Raw)
	}
	stepUp := right.Body["step_up_token"].(string)
	if res := withStepUp(stepUp); res.Status != http.StatusNoContent {
		t.Fatalf("disable with a valid step-up: %d, want 204", res.Status)
	}
	// Spent — and the factor is gone, so /me says so.
	if res := withStepUp(stepUp); res.Status != http.StatusForbidden {
		t.Errorf("a consumed step-up worked again: %d", res.Status)
	}
	me := s.call(t, "GET", "/v1/auth/me", nil, token, "")
	if me.Body["second_factor"].(map[string]any)["enrolled"] != false {
		t.Error("the factor is still enrolled after disable")
	}

	// And the events say what happened, without saying anything secret.
	var kinds []string
	rows, err := s.db.SQL.Query(`SELECT kind FROM core.security_event ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		kinds = append(kinds, k)
	}
	_ = rows.Close()
	for _, want := range []string{"totp_enrolment_started", "totp_enrolment_confirmed", "step_up_failed", "step_up_passed", "step_up_used", "totp_disabled"} {
		found := false
		for _, k := range kinds {
			if k == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no %s event; have %v", want, kinds)
		}
	}
	var leaked int
	if err := s.db.SQL.QueryRow(`SELECT count(*) FROM core.security_event WHERE detail::text LIKE '%'||$1||'%'`, secret).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Error("the seed appears in a security event")
	}
}

// --- criterion 3: recovery codes work once ---

func TestARecoveryCodeSignsInOnceAndOnlyOnce(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001", auth.RolePharmacist)
	_, recovery := s.enrolOverHTTP(t, accessToken(t, s.login(t, "E001", testPassword)))

	s.clock.Advance(time.Minute)
	challenge, _ := s.login(t, "E001", testPassword).Body["challenge"].(string)
	// Typed the way a person types it.
	sloppy := strings.ToLower(strings.ReplaceAll(recovery[0], "-", " "))
	first := s.call(t, "POST", "/v1/auth/login/second-factor",
		map[string]string{"challenge": challenge, "recovery_code": sloppy}, "", "")
	if first.Status != http.StatusOK {
		t.Fatalf("recovery code: %d %s", first.Status, first.Raw)
	}
	me := s.call(t, "GET", "/v1/auth/me", nil, accessToken(t, first), "")
	if left := me.Body["second_factor"].(map[string]any)["recovery_codes_left"]; left != float64(auth.RecoveryCodeCount-1) {
		t.Errorf("recovery_codes_left = %v", left)
	}

	s.clock.Advance(time.Minute)
	challenge, _ = s.login(t, "E001", testPassword).Body["challenge"].(string)
	second := s.call(t, "POST", "/v1/auth/login/second-factor",
		map[string]string{"challenge": challenge, "recovery_code": recovery[0]}, "", "")
	if second.Status != http.StatusUnauthorized {
		t.Errorf("a used recovery code signed in again: %d", second.Status)
	}
	// The row is still there, marked used — the evidence, not the absence of it.
	var used int
	if err := s.db.SQL.QueryRow(`SELECT count(*) FROM core.recovery_code WHERE used_at IS NOT NULL`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 1 {
		t.Errorf("%d used rows, want 1", used)
	}
}

// --- criterion 4: encrypted at rest, and untouchable ---

func TestTheSeedIsSealedAndNothingCanBeDeleted(t *testing.T) {
	s := newAuthServer(t)
	userID := s.seedUser(t, "E001", auth.RoleResearcher)
	token := accessToken(t, s.login(t, "E001", testPassword))
	secret, recovery := s.enrolOverHTTP(t, token)

	var sealed []byte
	var keyID string
	if err := s.db.SQL.QueryRow(`SELECT secret_sealed, key_id FROM core.user_totp WHERE user_id = $1`, userID).Scan(&sealed, &keyID); err != nil {
		t.Fatal(err)
	}
	if keyID != "test-1" {
		t.Errorf("key_id = %q", keyID)
	}
	if bytes.Contains(sealed, []byte(secret)) || bytes.Contains(sealed, []byte(strings.ToLower(secret))) {
		t.Fatal("the seed is readable in core.user_totp")
	}
	// Not anywhere else in the table either, nor a recovery code anywhere.
	var hits int
	if err := s.db.SQL.QueryRow(`
		SELECT count(*) FROM core.recovery_code
		 WHERE encode(code_digest, 'hex') LIKE '%'||lower($1)||'%'`, strings.ReplaceAll(recovery[0], "-", "")).Scan(&hits); err != nil {
		t.Fatal(err)
	}
	if hits != 0 {
		t.Error("a recovery code is stored in the clear")
	}

	// The application role can delete none of it and rewrite no event.
	tx, err := s.db.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SET ROLE dthcms_app`); err != nil {
		t.Fatalf("assuming the application role: %v", err)
	}
	refused := func(what, statement string) {
		t.Helper()
		if _, err := tx.Exec(`SAVEPOINT probe`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(statement); err == nil {
			t.Errorf("the application role %s", what)
		}
		if _, err := tx.Exec(`ROLLBACK TO SAVEPOINT probe`); err != nil {
			t.Fatal(err)
		}
	}
	refused("deleted a TOTP seed", `DELETE FROM core.user_totp`)
	refused("deleted a recovery code", `DELETE FROM core.recovery_code`)
	refused("deleted a short token", `DELETE FROM core.short_token`)
	refused("deleted a security event", `DELETE FROM core.security_event`)
	refused("rewrote a security event", `UPDATE core.security_event SET outcome = 'ok'`)
}

func TestRegeneratingRecoveryCodesNeedsAStepUp(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001", auth.RolePhysician)
	token := accessToken(t, s.login(t, "E001", testPassword))
	secret, old := s.enrolOverHTTP(t, token)
	s.clock.Advance(time.Minute)

	if res := s.call(t, "POST", "/v1/auth/second-factor/recovery-codes", nil, token, ""); res.Status != http.StatusForbidden {
		t.Fatalf("without a step-up: %d", res.Status)
	}
	stepUp := s.call(t, "POST", "/v1/auth/step-up",
		map[string]string{"purpose": auth.PurposeRecoveryCodes, "code": codeNow(t, secret, s.clock.Now())}, token, "")
	req, _ := http.NewRequest("POST", s.URL+"/v1/auth/second-factor/recovery-codes", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(httpx.RequestedWithHeader, httpx.RequestedWithValue)
	req.Header.Set(httpx.StepUpHeader, stepUp.Body["step_up_token"].(string))
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("regenerate with a step-up: %d", res.StatusCode)
	}

	// The old sheet is dead.
	s.clock.Advance(time.Minute)
	challenge, _ := s.login(t, "E001", testPassword).Body["challenge"].(string)
	if r := s.call(t, "POST", "/v1/auth/login/second-factor",
		map[string]string{"challenge": challenge, "recovery_code": old[0]}, "", ""); r.Status != http.StatusUnauthorized {
		t.Errorf("an old recovery code still works: %d", r.Status)
	}
}
