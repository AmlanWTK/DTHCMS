package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/pwhash"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/secretbox"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
)

// The six endpoints, end to end: a real router, a real database, real cookies.
//
// sessions_test.go proves the service against an in-memory store. These prove the part it
// cannot — that the HTTP layer, the Postgres store and the migration agree about what a
// login is. The in-memory tests could all pass while a login against the real schema
// returned 500 on the first NULL timestamp, which is not hypothetical: that is what the
// **time.Time bug would have done.
//
// They skip without DTHCMS_TEST_POSTGRES_URL, like every database test here.

// testPassword is deliberately not a strong password. The hasher's parameters are turned
// down below, and the strength of the secret is not what any of these tests are about.
const testPassword = "correct horse battery"

// authServer is one test's private stack.
type authServer struct {
	*httptest.Server
	db           *testsupport.DB
	store        *auth.PostgresStore
	sessions     *auth.Sessions
	secondFactor *auth.SecondFactor
	devices      *auth.Devices
	admin        *auth.Admin
	audit        *memAudit
	router       chi.Routes
	clock        *clock.Fixed
	facility     uuid.UUID

	// slept records every throttle delay the service asked for, without waiting for it.
	mu    sync.Mutex
	slept []time.Duration
}

func newAuthServer(t *testing.T) *authServer {
	t.Helper()

	db := testsupport.Postgres(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, db.DSN)
	if err != nil {
		t.Fatalf("opening a pool on %s: %v", db.Name, err)
	}
	t.Cleanup(pool.Close)

	var facility uuid.UUID
	if err := db.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&facility); err != nil {
		t.Fatalf("reading the default facility: %v", err)
	}

	s := &authServer{db: db, facility: facility, clock: clock.NewFixed(time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC))}
	s.store = auth.NewPostgresStore(pool)

	ring, err := secretbox.NewRing(secretbox.Key{ID: "test-1", Material: bytes.Repeat([]byte{7}, secretbox.KeySize)})
	if err != nil {
		t.Fatal(err)
	}
	s.secondFactor = auth.NewSecondFactor(auth.SecondFactorConfig{
		Store: s.store, Users: s.store, Ring: ring, Clock: s.clock,
	})

	s.sessions = auth.NewSessions(auth.SessionsConfig{
		Store:        s.store,
		Hasher:       testHasher(),
		Clock:        s.clock,
		SecondFactor: s.secondFactor,
		Sleep: func(_ context.Context, d time.Duration) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.slept = append(s.slept, d)
		},
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := s.sessions
	// Built before the handlers so that the auth endpoints can record too: CP41's role
	// switch is the first one that does.
	s.audit = &memAudit{}
	handlers := auth.NewHandlers(auth.HandlersConfig{
		Sessions: sessions, Store: s.store, SecondFactor: s.secondFactor, Logger: logger,
		FacilityID:    facility,
		SecureCookies: true,
		Audit:         s.audit,
	})

	// Devices (CP18), with an in-memory nonce store standing in for Redis.
	s.devices = auth.NewDevices(auth.DevicesConfig{Store: s.store, Nonces: newMemoryNonces(s.clock), Clock: s.clock})
	deviceHandlers := auth.NewDeviceHandlers(auth.DeviceHandlersConfig{Devices: s.devices, Store: s.store, Logger: logger})

	// The console (CP21), recording into the same memory.
	s.admin = auth.NewAdmin(auth.AdminConfig{
		Store: s.store, Identity: auth.NewService(s.store), Sessions: sessions, SecondFactor: s.secondFactor,
		Hasher: testHasher(), Audit: s.audit, Clock: s.clock,
	})
	adminHandlers := auth.NewAdminHandlers(auth.AdminHandlersConfig{Admin: s.admin, SecondFactor: s.secondFactor, Logger: logger})

	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger:         logger,
		IDs:            &ids.Sequential{Prefix: "req"},
		AllowedOrigins: []string{"http://localhost:3000"},
		MaxBodyBytes:   64 * 1024,
		RequestTimeout: 10 * time.Second,
		Authenticator:  &auth.Identifier{Sessions: sessions, Store: s.store},
		DeviceVerifier: &auth.DeviceVerifierAdapter{Devices: s.devices},
		AuthRoutes: func(r chi.Router) {
			handlers.Mount(r)
			deviceHandlers.MountAuth(r)
		},
		Routes: func(r chi.Router) {
			deviceHandlers.Mount(r)
			adminHandlers.Mount(r)
			// A stand-in for a clinical write, so the device rule can be proven before any
			// clinical module exists. The real ones get the same middleware.
			r.With(httpx.RequireDevice(logger)).Method("POST", "/test/clinical-write",
				httpx.Declare(httpx.Permission(auth.PermObservationWriteAnthro), func(w http.ResponseWriter, r *http.Request) {
					device, _ := httpx.DeviceFrom(r.Context())
					httpx.WriteJSON(w, http.StatusOK, map[string]string{"device_id": device.DeviceID})
				}))
		},
		// The route guard decides on the caller's own permission set here; the engine
		// proper is wired and proven in internal/rbac, which may import this package
		// while this package may not import it.
		Authorizer: permissionAuthorizer{},
	})
	if err != nil {
		t.Fatalf("building the router: %v", err)
	}

	s.router = router
	s.Server = httptest.NewServer(router)
	t.Cleanup(s.Close)
	return s
}

// permissionAuthorizer allows a route when the caller holds any of its permissions — the
// union across live roles, as the Identifier resolved it. No hats, no rules: those are
// the engine's, tested where the engine lives.
type permissionAuthorizer struct{}

func (permissionAuthorizer) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	held := auth.NewPermissionSet(caller.Permissions...)
	for _, p := range anyOf {
		if held.Has(p) {
			return ctx, httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

// memAudit collects what the console recorded.
type memAudit struct {
	mu      sync.Mutex
	entries []auth.AuditEntry
}

func (m *memAudit) RecordAudit(_ context.Context, e auth.AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
	return nil
}

func (m *memAudit) kinds() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e.Kind)
	}
	return out
}

// testHasher is argon2id at a cost that makes a login take milliseconds rather than a
// quarter of a second. The parameters travel with the hash, so this changes nothing about
// what is being tested — only how long the suite takes.
func testHasher() *pwhash.Hasher {
	return pwhash.New(pwhash.Params{
		Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	})
}

// seedUser creates an active user with a password and, optionally, a role.
func (s *authServer) seedUser(t *testing.T, code string, roles ...auth.RoleCode) uuid.UUID {
	t.Helper()

	hash, err := testHasher().Hash(testPassword)
	if err != nil {
		t.Fatalf("hashing the test password: %v", err)
	}

	var id uuid.UUID
	err = s.db.SQL.QueryRow(`
		INSERT INTO core.app_user (facility_id, employee_code, name_en, name_bn, password_hash, password_set_at)
		VALUES ($1, $2, 'Test User', 'পরীক্ষামূলক ব্যবহারকারী', $3, now())
		RETURNING id`, s.facility, code, hash).Scan(&id)
	if err != nil {
		t.Fatalf("inserting %s: %v", code, err)
	}
	if _, err := s.db.SQL.Exec(
		`UPDATE core.app_user SET status = 'active' WHERE id = $1`, id); err != nil {
		t.Fatalf("activating %s: %v", code, err)
	}

	for _, role := range roles {
		if _, err := s.db.SQL.Exec(`
			INSERT INTO core.user_role (user_id, role_id, facility_id)
			SELECT $1, id, $2 FROM core.role WHERE code = $3`, id, s.facility, string(role)); err != nil {
			t.Fatalf("granting %s to %s: %v", role, code, err)
		}
	}
	return id
}

// --- a small client ---

type response struct {
	Status  int
	Body    map[string]any
	Raw     string
	Cookies []*http.Cookie
}

// call makes a request. bearer and refresh are optional; an empty string omits them.
func (s *authServer) call(t *testing.T, method, path string, body any, bearer, refresh string) response {
	t.Helper()
	req := s.buildRequest(t, method, path, body, bearer)
	if refresh != "" {
		req.AddCookie(&http.Cookie{Name: "dthcms.refresh", Value: refresh})
	}
	return s.do(t, req)
}

// buildRequest is the request half of call, for tests that add a header of their own.
func (s *authServer) buildRequest(t *testing.T, method, path string, body any, bearer string) *http.Request {
	t.Helper()

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encoding the request body: %v", err)
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, s.URL+path, payload)
	if err != nil {
		t.Fatalf("building %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("User-Agent", "dthcms-test/1.0")
	req.Header.Set(httpx.RequestedWithHeader, httpx.RequestedWithValue)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return req
}

// do sends a request and reads the reply.
func (s *authServer) do(t *testing.T, req *http.Request) response {
	t.Helper()
	method, path := req.Method, req.URL.Path
	// Cookies are asserted on directly, so the client must not swallow them into a jar.
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()

	raw, _ := io.ReadAll(res.Body)
	out := response{Status: res.StatusCode, Raw: string(raw), Cookies: res.Cookies()}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.Body); err != nil {
			t.Fatalf("%s %s returned non-JSON: %s", method, path, raw)
		}
	}
	return out
}

func (s *authServer) login(t *testing.T, code, password string) response {
	t.Helper()
	return s.call(t, "POST", "/v1/auth/login",
		map[string]string{"employee_code": code, "password": password}, "", "")
}

func refreshCookie(t *testing.T, r response) *http.Cookie {
	t.Helper()
	for _, c := range r.Cookies {
		if c.Name == "dthcms.refresh" {
			return c
		}
	}
	t.Fatalf("no dthcms.refresh cookie in the response (status %d): %s", r.Status, r.Raw)
	return nil
}

func sessionCookie(t *testing.T, r response) *http.Cookie {
	t.Helper()
	for _, c := range r.Cookies {
		if c.Name == httpx.SessionCookie {
			return c
		}
	}
	t.Fatalf("no %s cookie in the response (status %d): %s", httpx.SessionCookie, r.Status, r.Raw)
	return nil
}

func accessToken(t *testing.T, r response) string {
	t.Helper()
	token, _ := r.Body["access_token"].(string)
	if token == "" {
		t.Fatalf("no access_token in the response (status %d): %s", r.Status, r.Raw)
	}
	return token
}

// --- login ---

func TestLoginIssuesAnAccessTokenAndARefreshCookie(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001", auth.RolePhysician)

	res := s.login(t, "E001", testPassword)
	if res.Status != http.StatusOK {
		t.Fatalf("login: %d %s", res.Status, res.Raw)
	}

	token := accessToken(t, res)
	if len(token) < 40 {
		t.Errorf("access token is %d characters; 32 random bytes encode to 43", len(token))
	}

	// The expiry is the fixed clock plus the default access lifetime, not wall time.
	expires, err := time.Parse(time.RFC3339, res.Body["expires_at"].(string))
	if err != nil {
		t.Fatalf("expires_at is not RFC 3339: %v", err)
	}
	if want := s.clock.Now().Add(auth.DefaultLifetimes().Access); !expires.Equal(want) {
		t.Errorf("expires_at = %v, want %v", expires, want)
	}

	user := res.Body["user"].(map[string]any)
	if user["employee_code"] != "E001" || user["status"] != "active" {
		t.Errorf("user in the response = %v", user)
	}
	if roles := user["roles"].([]any); len(roles) != 1 || roles[0] != "PHYSICIAN" {
		t.Errorf("roles = %v, want [PHYSICIAN]", roles)
	}
	if perms := user["permissions"].([]any); len(perms) == 0 {
		t.Error("a physician has no permissions in the response")
	}

	// The refresh cookie: script-proof, path-scoped, Secure, Lax, expiring with the token.
	c := refreshCookie(t, res)
	if !c.HttpOnly {
		t.Error("refresh cookie is readable by script (ADR-0010)")
	}
	if !c.Secure {
		t.Error("refresh cookie is not Secure with SecureCookies: true")
	}
	if c.Path != "/v1/auth" {
		t.Errorf("refresh cookie path = %q, want /v1/auth — it must not ride on every request", c.Path)
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("refresh cookie SameSite = %v, want Lax", c.SameSite)
	}
	if c.Value == token {
		t.Error("the refresh token is the access token")
	}
}

func TestWrongPasswordAndUnknownUserAreIndistinguishable(t *testing.T) {
	// Blueprint: the response must not say which. Same status, same body, same cookie
	// behaviour — the only thing allowed to differ is the correlation id.
	s := newAuthServer(t)
	s.seedUser(t, "E001")

	wrong := s.login(t, "E001", "not the password")
	unknown := s.login(t, "E999", testPassword)

	if wrong.Status != http.StatusUnauthorized || unknown.Status != http.StatusUnauthorized {
		t.Fatalf("statuses: wrong password %d, unknown user %d; want 401 for both",
			wrong.Status, unknown.Status)
	}

	strip := func(r response) map[string]any {
		body := r.Body["error"].(map[string]any)
		delete(body, "correlation_id")
		return body
	}
	if a, b := strip(wrong), strip(unknown); !equalJSON(a, b) {
		t.Errorf("bodies differ:\n  wrong password: %v\n  unknown user:   %v", a, b)
	}
	if len(wrong.Cookies) != len(unknown.Cookies) {
		t.Errorf("cookie counts differ: %d vs %d", len(wrong.Cookies), len(unknown.Cookies))
	}
	for _, r := range []response{wrong, unknown} {
		if _, has := r.Body["access_token"]; has {
			t.Error("a failed login returned an access token")
		}
	}
}

func TestFailedLoginsAreRecordedWithTheirReason(t *testing.T) {
	// The client is told nothing; the table is told everything. Suspicious activity is
	// investigated from core.login_attempt, not from the responses.
	s := newAuthServer(t)
	s.seedUser(t, "E001")

	s.login(t, "E001", "wrong")
	s.login(t, "E999", "whatever")
	s.login(t, "E001", testPassword)

	rows, err := s.db.SQL.Query(`
		SELECT employee_code, succeeded, failure_kind
		FROM core.login_attempt ORDER BY id`)
	if err != nil {
		t.Fatalf("reading login attempts: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var got []string
	for rows.Next() {
		var code, kind string
		var ok bool
		if err := rows.Scan(&code, &ok, &kind); err != nil {
			t.Fatal(err)
		}
		got = append(got, code+":"+map[bool]string{true: "ok", false: kind}[ok])
	}
	want := []string{"E001:bad_password", "E999:no_such_user", "E001:ok"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("login_attempt rows = %v, want %v", got, want)
	}
}

func TestLoginRejectsAMalformedBodyWithoutTouchingTheThrottle(t *testing.T) {
	s := newAuthServer(t)

	req, _ := http.NewRequest("POST", s.URL+"/v1/auth/login", strings.NewReader(`{"employee_code": `))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpx.RequestedWithHeader, httpx.RequestedWithValue)
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed JSON: %d, want 400", res.StatusCode)
	}

	var n int
	if err := s.db.SQL.QueryRow(`SELECT count(*) FROM core.login_attempt`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("a request that never reached the service recorded %d attempts", n)
	}
}

func TestSuspendedUserCannotLogInEvenWithTheRightPassword(t *testing.T) {
	s := newAuthServer(t)
	id := s.seedUser(t, "E001")
	if _, err := s.db.SQL.Exec(
		`UPDATE core.app_user SET status = 'suspended', status_reason = 'test' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}

	res := s.login(t, "E001", testPassword)
	if res.Status != http.StatusUnauthorized {
		t.Errorf("suspended login: %d, want 401", res.Status)
	}

	var kind string
	if err := s.db.SQL.QueryRow(`SELECT failure_kind FROM core.login_attempt`).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "not_active" {
		t.Errorf("recorded failure = %q, want not_active", kind)
	}
}

// --- the browser transport ---

func TestLoginSetsAnHttpOnlySessionCookieTheBrowserCanAuthenticateWith(t *testing.T) {
	// ADR-0010: the web application never holds the token. It arrives in a cookie script
	// cannot read, on every path, and the middleware accepts it in place of the header.
	s := newAuthServer(t)
	s.seedUser(t, "E001")

	login := s.login(t, "E001", testPassword)
	c := sessionCookie(t, login)
	if !c.HttpOnly || !c.Secure || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie attributes: httpOnly=%v secure=%v path=%q sameSite=%v",
			c.HttpOnly, c.Secure, c.Path, c.SameSite)
	}
	if c.Value != accessToken(t, login) {
		t.Error("the cookie does not carry the same token the body does")
	}
	if !c.Expires.Equal(s.clock.Now().Add(auth.DefaultLifetimes().Access)) {
		t.Errorf("session cookie expires %v, want with the access token", c.Expires)
	}

	// Cookie only — no Authorization header at all.
	req, _ := http.NewRequest("GET", s.URL+"/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: httpx.SessionCookie, Value: c.Value})
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Errorf("/me with the session cookie alone: %d, want 200", res.StatusCode)
	}
}

func TestLogoutClearsBothCookies(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001")
	token := accessToken(t, s.login(t, "E001", testPassword))

	res := s.call(t, "POST", "/v1/auth/logout", nil, token, "")
	if res.Status != http.StatusNoContent {
		t.Fatalf("logout: %d %s", res.Status, res.Raw)
	}
	for _, name := range []string{httpx.SessionCookie, "dthcms.refresh"} {
		var found *http.Cookie
		for _, c := range res.Cookies {
			if c.Name == name {
				found = c
			}
		}
		if found == nil {
			t.Errorf("logout did not touch the %s cookie", name)
			continue
		}
		if found.MaxAge >= 0 || found.Value != "" {
			t.Errorf("logout did not clear %s: %+v", name, found)
		}
	}
}

func TestStateChangingRequestsRequireTheForgeryGuard(t *testing.T) {
	// A form on another site can post to /v1/auth/login with the victim's cookies. It
	// cannot set X-Requested-With. So a request without it is refused before anything is
	// looked at — including login itself, since logging somebody into an attacker's
	// account is also an attack.
	s := newAuthServer(t)
	s.seedUser(t, "E001")
	token := accessToken(t, s.login(t, "E001", testPassword))

	bare := func(method, path string, bearer string, body string) int {
		req, _ := http.NewRequest(method, s.URL+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, err := s.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		return res.StatusCode
	}

	if got := bare("POST", "/v1/auth/login", "", `{"employee_code":"E001","password":"x"}`); got != http.StatusForbidden {
		t.Errorf("login without the guard: %d, want 403", got)
	}
	if got := bare("POST", "/v1/auth/logout", token, ""); got != http.StatusForbidden {
		t.Errorf("logout without the guard: %d, want 403", got)
	}
	// Reads are not state changes and browsers cannot add headers to navigations.
	if got := bare("GET", "/v1/auth/me", token, ""); got != http.StatusOK {
		t.Errorf("GET without the guard: %d, want 200", got)
	}

	var attempts int
	if err := s.db.SQL.QueryRow(`SELECT count(*) FROM core.login_attempt`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Errorf("%d login attempts recorded; the refused one must not count", attempts)
	}
}

// --- the bearer transport ---

func TestBearerTransportReturnsBothTokensAndSetsNoCookies(t *testing.T) {
	// The station app holds its own credentials. It must get the refresh token in the
	// body, and it must not get cookies: a jar that re-sent a rotated-away token would be
	// read as theft.
	s := newAuthServer(t)
	s.seedUser(t, "E001")

	res := s.call(t, "POST", "/v1/auth/login",
		map[string]string{"employee_code": "E001", "password": testPassword, "transport": "bearer"}, "", "")
	if res.Status != http.StatusOK {
		t.Fatalf("bearer login: %d %s", res.Status, res.Raw)
	}
	refresh, _ := res.Body["refresh_token"].(string)
	if refresh == "" {
		t.Fatal("no refresh_token in the body for the bearer transport")
	}
	if _, ok := res.Body["refresh_expires_at"]; !ok {
		t.Error("no refresh_expires_at in the body")
	}
	if len(res.Cookies) != 0 {
		t.Errorf("bearer login set %d cookies; want none", len(res.Cookies))
	}

	// Refresh by body, not cookie; rotated token comes back in the body; still no cookies.
	s.clock.Advance(time.Minute)
	next := s.call(t, "POST", "/v1/auth/refresh", map[string]string{"refresh_token": refresh}, "", "")
	if next.Status != http.StatusOK {
		t.Fatalf("bearer refresh: %d %s", next.Status, next.Raw)
	}
	rotated, _ := next.Body["refresh_token"].(string)
	if rotated == "" || rotated == refresh {
		t.Errorf("bearer refresh returned refresh_token %q; want a new one", rotated)
	}
	if len(next.Cookies) != 0 {
		t.Errorf("bearer refresh set %d cookies; want none", len(next.Cookies))
	}
	if r := s.call(t, "GET", "/v1/auth/me", nil, accessToken(t, next), ""); r.Status != http.StatusOK {
		t.Errorf("the refreshed access token does not authenticate: %d", r.Status)
	}
}

func TestCookieTransportNeverRevealsTheRefreshToken(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001")

	login := s.login(t, "E001", testPassword)
	if _, leaked := login.Body["refresh_token"]; leaked {
		t.Error("the cookie transport put the refresh token in the body, where script can read it")
	}
	refreshed := s.call(t, "POST", "/v1/auth/refresh", nil, "", refreshCookie(t, login).Value)
	if _, leaked := refreshed.Body["refresh_token"]; leaked {
		t.Error("a cookie refresh put the refresh token in the body")
	}
}

func TestTheBodyRefreshTokenWinsOverAStaleCookie(t *testing.T) {
	// A native client's HTTP library may keep a cookie jar the app does not know about. If
	// the jar held a token the app had since rotated, reading the cookie first would
	// present a spent token and revoke the family — the app's own fault, as far as the
	// server could tell. The body is what the client meant.
	s := newAuthServer(t)
	s.seedUser(t, "E001")

	first := s.call(t, "POST", "/v1/auth/login",
		map[string]string{"employee_code": "E001", "password": testPassword, "transport": "bearer"}, "", "")
	stale, _ := first.Body["refresh_token"].(string)
	second := s.call(t, "POST", "/v1/auth/refresh", map[string]string{"refresh_token": stale}, "", "")
	current, _ := second.Body["refresh_token"].(string)

	// Body current, cookie stale.
	res := s.call(t, "POST", "/v1/auth/refresh", map[string]string{"refresh_token": current}, "", stale)
	if res.Status != http.StatusOK {
		t.Fatalf("refresh with a current body token and a stale cookie: %d %s", res.Status, res.Raw)
	}
}

func TestAnUnknownTransportIsRefused(t *testing.T) {
	s := newAuthServer(t)
	res := s.call(t, "POST", "/v1/auth/login",
		map[string]string{"employee_code": "E001", "password": testPassword, "transport": "carrier-pigeon"}, "", "")
	if res.Status != http.StatusBadRequest {
		t.Errorf("unknown transport: %d, want 400", res.Status)
	}
}

// --- throttling ---

func TestRepeatedFailuresAreDelayedProgressively(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001")

	for i := 0; i < 6; i++ {
		s.login(t, "E001", "wrong")
	}

	// The delay is applied *before* the attempt is judged, from the failures already on
	// record. Attempts one to three see zero, one and two prior failures — all within the
	// two that are free. The fourth sees three and waits 1s; the fifth 2s; the sixth 4s.
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	s.mu.Lock()
	got := append([]time.Duration(nil), s.slept...)
	s.mu.Unlock()

	if len(got) != len(want) {
		t.Fatalf("delays applied = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("delay %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestTheThrottleCountsAttemptsAgainstCodesThatDoNotExist(t *testing.T) {
	// A throttle that only counted real accounts would answer "does this person work
	// here" by how fast it refuses.
	s := newAuthServer(t)

	for i := 0; i < 4; i++ {
		s.login(t, "NOBODY", "wrong")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.slept) == 0 {
		t.Fatal("four failures against an unknown code applied no delay")
	}
}

// --- authenticated endpoints ---

func TestMeRequiresABearerTokenAndReflectsGrants(t *testing.T) {
	s := newAuthServer(t)
	id := s.seedUser(t, "E001", auth.RoleRegistration)
	token := accessToken(t, s.login(t, "E001", testPassword))

	if res := s.call(t, "GET", "/v1/auth/me", nil, "", ""); res.Status != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", res.Status)
	}
	if res := s.call(t, "GET", "/v1/auth/me", nil, "not-a-real-token", ""); res.Status != http.StatusUnauthorized {
		t.Errorf("bad token: %d, want 401", res.Status)
	}

	res := s.call(t, "GET", "/v1/auth/me", nil, token, "")
	if res.Status != http.StatusOK {
		t.Fatalf("me: %d %s", res.Status, res.Raw)
	}
	if res.Body["id"] != id.String() {
		t.Errorf("me.id = %v, want %v", res.Body["id"], id)
	}
	if roles := res.Body["roles"].([]any); len(roles) != 1 || roles[0] != "REGISTRATION" {
		t.Errorf("roles = %v", roles)
	}

	// Revoke the grant. The very next /me must not show it — nothing is cached in the
	// token (ADR-0011).
	if _, err := s.db.SQL.Exec(
		`UPDATE core.user_role SET revoked_at = now(), revoke_reason = 'test' WHERE user_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	res = s.call(t, "GET", "/v1/auth/me", nil, token, "")
	if roles := res.Body["roles"].([]any); len(roles) != 0 {
		t.Errorf("a revoked grant is still reported: %v", roles)
	}
	if perms := res.Body["permissions"].([]any); len(perms) != 0 {
		t.Errorf("a revoked grant still confers permissions: %v", perms)
	}
}

func TestAnExpiredAccessTokenIsRefused(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001")
	token := accessToken(t, s.login(t, "E001", testPassword))

	s.clock.Advance(auth.DefaultLifetimes().Access - time.Second)
	if res := s.call(t, "GET", "/v1/auth/me", nil, token, ""); res.Status != http.StatusOK {
		t.Errorf("one second before expiry: %d, want 200", res.Status)
	}

	s.clock.Advance(2 * time.Second)
	if res := s.call(t, "GET", "/v1/auth/me", nil, token, ""); res.Status != http.StatusUnauthorized {
		t.Errorf("one second after expiry: %d, want 401", res.Status)
	}
}

// --- refresh ---

func TestRefreshRotatesTheTokenAndReuseRevokesTheFamily(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001")

	first := s.login(t, "E001", testPassword)
	firstRefresh := refreshCookie(t, first).Value

	// Legitimate rotation: a new access token and a new refresh cookie.
	s.clock.Advance(time.Minute)
	second := s.call(t, "POST", "/v1/auth/refresh", nil, "", firstRefresh)
	if second.Status != http.StatusOK {
		t.Fatalf("refresh: %d %s", second.Status, second.Raw)
	}
	secondRefresh := refreshCookie(t, second).Value
	if secondRefresh == firstRefresh {
		t.Fatal("refresh returned the same refresh token")
	}
	if accessToken(t, second) == accessToken(t, first) {
		t.Fatal("refresh returned the same access token")
	}

	// The stolen-and-replayed case: the spent token is presented again.
	replay := s.call(t, "POST", "/v1/auth/refresh", nil, "", firstRefresh)
	if replay.Status != http.StatusUnauthorized {
		t.Fatalf("replaying a spent token: %d, want 401", replay.Status)
	}
	if c := refreshCookie(t, replay); c.MaxAge >= 0 && c.Value != "" {
		t.Errorf("a refused refresh did not clear the cookie: %+v", c)
	}

	// Which revokes the whole lineage: the legitimate holder's *current* token is dead
	// too, because at this point nobody knows which of the two is the attacker.
	if res := s.call(t, "POST", "/v1/auth/refresh", nil, "", secondRefresh); res.Status != http.StatusUnauthorized {
		t.Errorf("the successor of a replayed token still refreshes: %d", res.Status)
	}
	if res := s.call(t, "GET", "/v1/auth/me", nil, accessToken(t, second), ""); res.Status != http.StatusUnauthorized {
		t.Errorf("the session survived its family being revoked: %d", res.Status)
	}

	var revoked int
	if err := s.db.SQL.QueryRow(
		`SELECT count(*) FROM core.refresh_token WHERE revoked_at IS NOT NULL`).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if revoked != 2 {
		t.Errorf("%d refresh tokens revoked, want both members of the family", revoked)
	}
}

func TestRefreshWithoutACookieIsRefused(t *testing.T) {
	s := newAuthServer(t)
	if res := s.call(t, "POST", "/v1/auth/refresh", nil, "", ""); res.Status != http.StatusUnauthorized {
		t.Errorf("no cookie: %d, want 401", res.Status)
	}
	if res := s.call(t, "POST", "/v1/auth/refresh", nil, "", "made-up"); res.Status != http.StatusUnauthorized {
		t.Errorf("unknown cookie: %d, want 401", res.Status)
	}
}

// --- sessions and logout ---

func TestSessionsListsLiveSessionsAndMarksTheCurrentOne(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001")

	tokenA := accessToken(t, s.login(t, "E001", testPassword))
	s.clock.Advance(time.Minute)
	tokenB := accessToken(t, s.login(t, "E001", testPassword))

	res := s.call(t, "GET", "/v1/auth/sessions", nil, tokenB, "")
	if res.Status != http.StatusOK {
		t.Fatalf("sessions: %d %s", res.Status, res.Raw)
	}
	list := res.Body["sessions"].([]any)
	if len(list) != 2 {
		t.Fatalf("%d sessions listed, want 2", len(list))
	}

	current := 0
	for _, item := range list {
		if item.(map[string]any)["current"] == true {
			current++
		}
	}
	if current != 1 {
		t.Errorf("%d sessions marked current, want exactly 1", current)
	}

	// Seen from the other session, the other one is current.
	res = s.call(t, "GET", "/v1/auth/sessions", nil, tokenA, "")
	for _, item := range res.Body["sessions"].([]any) {
		view := item.(map[string]any)
		if view["user_agent"] != "dthcms-test/1.0" {
			t.Errorf("user_agent = %v", view["user_agent"])
		}
	}
}

func TestLogoutEndsTheSessionAndClearsTheCookie(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001")

	login := s.login(t, "E001", testPassword)
	token := accessToken(t, login)
	refresh := refreshCookie(t, login).Value

	res := s.call(t, "POST", "/v1/auth/logout", nil, token, "")
	if res.Status != http.StatusNoContent {
		t.Fatalf("logout: %d %s", res.Status, res.Raw)
	}
	if c := refreshCookie(t, res); c.MaxAge >= 0 || c.Value != "" {
		t.Errorf("logout did not clear the refresh cookie: %+v", c)
	}

	if res := s.call(t, "GET", "/v1/auth/me", nil, token, ""); res.Status != http.StatusUnauthorized {
		t.Errorf("the access token works after logout: %d", res.Status)
	}
	if res := s.call(t, "POST", "/v1/auth/refresh", nil, "", refresh); res.Status != http.StatusUnauthorized {
		t.Errorf("the refresh token works after logout: %d", res.Status)
	}

	// Ended, not erased. The row is the audit trail.
	var reason string
	if err := s.db.SQL.QueryRow(`SELECT revoke_reason FROM core.session`).Scan(&reason); err != nil {
		t.Fatalf("the session row is gone: %v", err)
	}
	if reason != "signed out" {
		t.Errorf("revoke_reason = %q", reason)
	}
}

func TestLogoutAllEndsEverySessionOfTheUser(t *testing.T) {
	s := newAuthServer(t)
	s.seedUser(t, "E001")
	s.seedUser(t, "E002")

	tokenA := accessToken(t, s.login(t, "E001", testPassword))
	tokenB := accessToken(t, s.login(t, "E001", testPassword))
	other := accessToken(t, s.login(t, "E002", testPassword))

	res := s.call(t, "POST", "/v1/auth/logout-all", nil, tokenA, "")
	if res.Status != http.StatusOK {
		t.Fatalf("logout-all: %d %s", res.Status, res.Raw)
	}
	if ended := res.Body["ended"]; ended != float64(2) {
		t.Errorf("ended = %v, want 2", ended)
	}

	for name, token := range map[string]string{"A": tokenA, "B": tokenB} {
		if r := s.call(t, "GET", "/v1/auth/me", nil, token, ""); r.Status != http.StatusUnauthorized {
			t.Errorf("session %s survived logout-all: %d", name, r.Status)
		}
	}
	// Somebody else's session is not touched.
	if r := s.call(t, "GET", "/v1/auth/me", nil, other, ""); r.Status != http.StatusOK {
		t.Errorf("logout-all for E001 ended E002's session: %d", r.Status)
	}
}

// --- helpers ---

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
