package auth_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/devicesig"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// CP18's acceptance criteria, against the real router and the real schema:
//
//	(1) clinical writes from unenrolled or revoked devices are rejected;
//	(2) a forged device id fails signature verification;
//	(3) every device appears in the admin list with last-seen and app version;
//	(4) revocation is effective on the next request.
//
// Plus the properties the criteria rest on: a replayed request is refused, a session opened
// from a device cannot be used without it, re-enrolment retires the old key, and the
// database invariants hold after every transition.

// memoryNonces is the in-memory NonceStore: what Redis does in production.
type memoryNonces struct {
	clock clock.Clock
	mu    sync.Mutex
	seen  map[string]time.Time
}

func newMemoryNonces(c clock.Clock) *memoryNonces {
	return &memoryNonces{clock: c, seen: map[string]time.Time{}}
}

func (m *memoryNonces) Remember(_ context.Context, key string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	if until, ok := m.seen[key]; ok && now.Before(until) {
		return false, nil
	}
	m.seen[key] = now.Add(ttl)
	return true, nil
}

// tablet is an enrolled device as the test drives it: a keypair and an id.
type tablet struct {
	id   string
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// signed makes a device-signed request: the same as call, plus the four headers.
func (s *authServer) signed(t *testing.T, d tablet, method, path string, body any, bearer string) response {
	t.Helper()

	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	nonce, err := devicesig.NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	proof := devicesig.Proof{
		DeviceID: d.id, Timestamp: s.clock.Now().Unix(), Nonce: nonce,
		Method: method, Path: path, BodyDigest: devicesig.DigestBody(encoded),
	}
	proof.Signature = devicesig.Sign(d.priv, proof)
	return s.rawSigned(t, proof, method, path, encoded, bearer)
}

func (s *authServer) rawSigned(t *testing.T, proof devicesig.Proof, method, path string, encoded []byte, bearer string) response {
	t.Helper()
	var payload io.Reader
	if encoded != nil {
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, s.URL+path, payload)
	if err != nil {
		t.Fatal(err)
	}
	if encoded != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("User-Agent", "dthcms-station/1.2.0")
	req.Header.Set(httpx.RequestedWithHeader, httpx.RequestedWithValue)
	req.Header.Set(devicesig.HeaderID, proof.DeviceID)
	req.Header.Set(devicesig.HeaderTimestamp, strconv.FormatInt(proof.Timestamp, 10))
	req.Header.Set(devicesig.HeaderNonce, proof.Nonce)
	req.Header.Set(devicesig.HeaderSignature, devicesig.EncodeSignature(proof.Signature))
	req.Header.Set(devicesig.HeaderAppVersion, "1.2.0")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
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

// enrolTablet takes a device from an administrator's code to an enrolled keypair.
func (s *authServer) enrolTablet(t *testing.T, adminToken, name string) tablet {
	t.Helper()
	issued := s.call(t, "POST", "/v1/devices", map[string]string{"name": name, "kind": "tablet"}, adminToken, "")
	if issued.Status != http.StatusCreated {
		t.Fatalf("issue: %d %s", issued.Status, issued.Raw)
	}
	code, _ := issued.Body["code"].(string)
	device := issued.Body["device"].(map[string]any)
	if device["status"] != "pending" || len(code) != 11 {
		t.Fatalf("issued = %s", issued.Raw)
	}

	pub, priv := newKeypair(t)
	enrolled := s.call(t, "POST", "/v1/auth/device/enrol", map[string]string{
		"code": code, "public_key": devicesig.EncodePublicKey(pub),
		"model": "Samsung SM-X200", "os_version": "Android 13", "app_version": "1.2.0",
	}, "", "")
	if enrolled.Status != http.StatusOK {
		t.Fatalf("enrol: %d %s", enrolled.Status, enrolled.Raw)
	}
	device = enrolled.Body["device"].(map[string]any)
	if device["status"] != "active" {
		t.Fatalf("after enrolment: %s", enrolled.Raw)
	}
	return tablet{id: device["id"].(string), pub: pub, priv: priv}
}

func (s *authServer) adminToken(t *testing.T) string {
	t.Helper()
	s.seedUser(t, "A001", auth.RoleAdmin)
	// An administrator must have a second factor; the test does not want to exercise it here.
	res := s.login(t, "A001", testPassword)
	if res.Status != http.StatusOK {
		t.Fatalf("admin login: %d %s", res.Status, res.Raw)
	}
	return accessToken(t, res)
}

func (s *authServer) assertInvariants(t *testing.T) {
	t.Helper()
	if _, err := s.db.SQL.Exec(`SELECT core.assert_invariants()`); err != nil {
		t.Fatalf("database invariants violated: %v", err)
	}
}

// --- criterion 1: clinical writes need an enrolled, active device ---

func TestClinicalWritesNeedAnEnrolledDevice(t *testing.T) {
	s := newAuthServer(t)
	admin := s.adminToken(t)
	s.seedUser(t, "N001", auth.RoleAnthropometry)

	// A browser session: signed in, no device. The write is refused with its own code.
	browser := s.login(t, "N001", testPassword)
	res := s.call(t, "POST", "/v1/test/clinical-write", map[string]int{"value": 72}, accessToken(t, browser), "")
	if res.Status != http.StatusForbidden || res.Body["error"].(map[string]any)["code"] != "DEVICE_REQUIRED" {
		t.Fatalf("browser write: %d %s", res.Status, res.Raw)
	}

	// A tablet: enrolled, and the nurse signs in from it. The write carries the device.
	tab := s.enrolTablet(t, admin, "Anthropometry tablet 1")
	login := s.signed(t, tab, "POST", "/v1/auth/login",
		map[string]string{"employee_code": "N001", "password": testPassword, "transport": "bearer"}, "")
	if login.Status != http.StatusOK {
		t.Fatalf("tablet login: %d %s", login.Status, login.Raw)
	}
	token := accessToken(t, login)
	res = s.signed(t, tab, "POST", "/v1/test/clinical-write", map[string]int{"value": 72}, token)
	if res.Status != http.StatusOK || res.Body["device_id"] != tab.id {
		t.Fatalf("tablet write: %d %s", res.Status, res.Raw)
	}

	// An unenrolled device — a key nobody issued a code for — is refused at the door.
	pub, priv := newKeypair(t)
	rogue := tablet{id: "9b2c4e6f-1a3b-4c5d-8e7f-0a1b2c3d4e5f", pub: pub, priv: priv}
	res = s.signed(t, rogue, "POST", "/v1/test/clinical-write", map[string]int{"value": 72}, accessToken(t, browser))
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("unenrolled device write: %d %s", res.Status, res.Raw)
	}

	// Revoked: the next write from the tablet fails, and the session with it.
	revoked := s.call(t, "POST", "/v1/devices/"+tab.id+"/revoke", map[string]string{"reason": "screen cracked, returned to supplier"}, admin, "")
	if revoked.Status != http.StatusOK || revoked.Body["status"] != "revoked" {
		t.Fatalf("revoke: %d %s", revoked.Status, revoked.Raw)
	}
	res = s.signed(t, tab, "POST", "/v1/test/clinical-write", map[string]int{"value": 72}, token)
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("write after revocation: %d %s", res.Status, res.Raw)
	}
	s.assertInvariants(t)
}

// --- criterion 2: a forged device id fails ---

func TestForgedDeviceIDFailsVerification(t *testing.T) {
	s := newAuthServer(t)
	admin := s.adminToken(t)
	victim := s.enrolTablet(t, admin, "Victim tablet")

	// Somebody with their own key and the victim's id: verified under the victim's key,
	// so refused — and recorded against the victim's device, where an administrator looks.
	_, attackerPriv := newKeypair(t)
	forged := tablet{id: victim.id, priv: attackerPriv}
	res := s.signed(t, forged, "POST", "/v1/auth/login",
		map[string]string{"employee_code": "A001", "password": testPassword}, "")
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("forged login: %d %s", res.Status, res.Raw)
	}
	events := s.call(t, "GET", "/v1/devices/"+victim.id+"/events", nil, admin, "")
	if events.Status != http.StatusOK {
		t.Fatalf("events: %d %s", events.Status, events.Raw)
	}
	var refused bool
	for _, e := range events.Body["events"].([]any) {
		if e.(map[string]any)["kind"] == "signature_refused" {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("no signature_refused event recorded: %s", events.Raw)
	}

	// Tampering with the signed body after signing: the digest no longer matches.
	nonce, _ := devicesig.NewNonce()
	proof := devicesig.Proof{
		DeviceID: victim.id, Timestamp: s.clock.Now().Unix(), Nonce: nonce,
		Method: "POST", Path: "/v1/auth/login", BodyDigest: devicesig.DigestBody([]byte(`{"employee_code":"A001","password":"x"}`)),
	}
	proof.Signature = devicesig.Sign(victim.priv, proof)
	res = s.rawSigned(t, proof, "POST", "/v1/auth/login", []byte(`{"employee_code":"A001","password":"`+testPassword+`"}`), "")
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("tampered body: %d %s", res.Status, res.Raw)
	}

	// A malformed proof is the client's bug, not a refusal: 400.
	proof.Nonce = "short"
	res = s.rawSigned(t, proof, "POST", "/v1/auth/login", []byte(`{}`), "")
	if res.Status != http.StatusBadRequest {
		t.Fatalf("malformed proof: %d %s", res.Status, res.Raw)
	}
}

func TestReplayedRequestIsRefused(t *testing.T) {
	s := newAuthServer(t)
	admin := s.adminToken(t)
	tab := s.enrolTablet(t, admin, "Replay tablet")

	nonce, _ := devicesig.NewNonce()
	proof := devicesig.Proof{
		DeviceID: tab.id, Timestamp: s.clock.Now().Unix(), Nonce: nonce,
		Method: "GET", Path: "/v1/devices/self", BodyDigest: devicesig.DigestBody(nil),
	}
	proof.Signature = devicesig.Sign(tab.priv, proof)

	first := s.rawSigned(t, proof, "GET", "/v1/devices/self", nil, admin)
	if first.Status != http.StatusOK {
		t.Fatalf("first: %d %s", first.Status, first.Raw)
	}
	again := s.rawSigned(t, proof, "GET", "/v1/devices/self", nil, admin)
	if again.Status != http.StatusUnauthorized {
		t.Fatalf("replay must be refused: %d %s", again.Status, again.Raw)
	}

	// And a request from outside the skew, however fresh its nonce.
	s.clock.Advance(devicesig.MaxSkew + time.Minute)
	stale := s.signed(t, tablet{id: tab.id, priv: tab.priv}, "GET", "/v1/devices/self", nil, admin)
	if stale.Status != http.StatusOK {
		t.Fatalf("a request signed now must pass: %d %s", stale.Status, stale.Raw)
	}
	old := devicesig.Proof{
		DeviceID: tab.id, Timestamp: s.clock.Now().Add(-devicesig.MaxSkew - time.Minute).Unix(), Nonce: nonce + "x",
		Method: "GET", Path: "/v1/devices/self", BodyDigest: devicesig.DigestBody(nil),
	}
	old.Signature = devicesig.Sign(tab.priv, old)
	if res := s.rawSigned(t, old, "GET", "/v1/devices/self", nil, admin); res.Status != http.StatusUnauthorized {
		t.Fatalf("stale timestamp must be refused: %d %s", res.Status, res.Raw)
	}
}

// --- criterion 3: the admin list ---

func TestAdminListShowsLastSeenAndAppVersion(t *testing.T) {
	s := newAuthServer(t)
	admin := s.adminToken(t)
	tab := s.enrolTablet(t, admin, "Registration tablet")
	s.call(t, "POST", "/v1/devices", map[string]string{"name": "Spare phone", "kind": "phone"}, admin, "")

	// The tablet makes a request five minutes later; the list reflects it.
	s.clock.Advance(5 * time.Minute)
	if res := s.signed(t, tab, "GET", "/v1/devices/self", nil, admin); res.Status != http.StatusOK {
		t.Fatalf("self: %d %s", res.Status, res.Raw)
	}

	list := s.call(t, "GET", "/v1/devices", nil, admin, "")
	if list.Status != http.StatusOK {
		t.Fatalf("list: %d %s", list.Status, list.Raw)
	}
	devices := list.Body["devices"].([]any)
	if len(devices) != 2 {
		t.Fatalf("%d devices listed: %s", len(devices), list.Raw)
	}
	var seen bool
	for _, d := range devices {
		dev := d.(map[string]any)
		switch dev["name"] {
		case "Registration tablet":
			seen = true
			if dev["status"] != "active" || dev["app_version"] != "1.2.0" || dev["model"] != "Samsung SM-X200" {
				t.Errorf("tablet row = %v", dev)
			}
			lastSeen, _ := time.Parse(time.RFC3339, dev["last_seen_at"].(string))
			if !lastSeen.Equal(s.clock.Now()) {
				t.Errorf("last_seen_at = %v, want %v", lastSeen, s.clock.Now())
			}
		case "Spare phone":
			if dev["status"] != "pending" || dev["last_seen_at"] != nil {
				t.Errorf("phone row = %v", dev)
			}
		}
	}
	if !seen {
		t.Fatal("the tablet is not in the list")
	}

	// Somebody without the permission sees nothing — not an empty list, a refusal.
	s.seedUser(t, "N002", auth.RoleNutritionist)
	nurse := s.login(t, "N002", testPassword)
	if res := s.call(t, "GET", "/v1/devices", nil, accessToken(t, nurse), ""); res.Status != http.StatusForbidden {
		t.Fatalf("nutritionist listing devices: %d %s", res.Status, res.Raw)
	}
}

// --- criterion 4: revocation on the next request ---

func TestRevocationIsEffectiveOnTheNextRequest(t *testing.T) {
	s := newAuthServer(t)
	admin := s.adminToken(t)
	s.seedUser(t, "N003", auth.RoleAnthropometry)
	tab := s.enrolTablet(t, admin, "Ward tablet")

	login := s.signed(t, tab, "POST", "/v1/auth/login",
		map[string]string{"employee_code": "N003", "password": testPassword, "transport": "bearer"}, "")
	token := accessToken(t, login)
	if res := s.signed(t, tab, "GET", "/v1/auth/me", nil, token); res.Status != http.StatusOK {
		t.Fatalf("before: %d %s", res.Status, res.Raw)
	}

	// Suspend: refused, reversible.
	if res := s.call(t, "POST", "/v1/devices/"+tab.id+"/suspend", map[string]string{"reason": "left in a taxi"}, admin, ""); res.Status != http.StatusOK {
		t.Fatalf("suspend: %d %s", res.Status, res.Raw)
	}
	if res := s.signed(t, tab, "GET", "/v1/auth/me", nil, token); res.Status != http.StatusUnauthorized {
		t.Fatalf("suspended device must be refused: %d %s", res.Status, res.Raw)
	}
	if res := s.call(t, "POST", "/v1/devices/"+tab.id+"/reinstate", map[string]string{"reason": "taxi driver brought it back"}, admin, ""); res.Status != http.StatusOK {
		t.Fatalf("reinstate: %d %s", res.Status, res.Raw)
	}
	if res := s.signed(t, tab, "GET", "/v1/auth/me", nil, token); res.Status != http.StatusOK {
		t.Fatalf("reinstated device must work again: %d %s", res.Status, res.Raw)
	}

	// Lost: terminal. The next request fails; so does the session, even presented from
	// somewhere else; so does re-enrolment.
	if res := s.call(t, "POST", "/v1/devices/"+tab.id+"/lost", map[string]string{"reason": "not seen since Tuesday"}, admin, ""); res.Status != http.StatusOK {
		t.Fatalf("lost: %d %s", res.Status, res.Raw)
	}
	if res := s.signed(t, tab, "GET", "/v1/auth/me", nil, token); res.Status != http.StatusUnauthorized {
		t.Fatalf("lost device must be refused: %d %s", res.Status, res.Raw)
	}
	if res := s.call(t, "GET", "/v1/auth/me", nil, token, ""); res.Status != http.StatusUnauthorized {
		t.Fatalf("the session must have been ended: %d %s", res.Status, res.Raw)
	}
	if res := s.call(t, "POST", "/v1/devices/"+tab.id+"/enrolments", nil, admin, ""); res.Status != http.StatusConflict {
		t.Fatalf("re-enrolling a lost device: %d %s", res.Status, res.Raw)
	}
	if res := s.call(t, "POST", "/v1/devices/"+tab.id+"/reinstate", map[string]string{"reason": "found it"}, admin, ""); res.Status != http.StatusConflict {
		t.Fatalf("reinstating a lost device: %d %s", res.Status, res.Raw)
	}
	s.assertInvariants(t)
}

// --- the properties underneath ---

func TestSessionOpenedFromADeviceIsBoundToIt(t *testing.T) {
	s := newAuthServer(t)
	admin := s.adminToken(t)
	s.seedUser(t, "N004", auth.RoleAnthropometry)
	tab := s.enrolTablet(t, admin, "Bound tablet")
	other := s.enrolTablet(t, admin, "Other tablet")

	login := s.signed(t, tab, "POST", "/v1/auth/login",
		map[string]string{"employee_code": "N004", "password": testPassword, "transport": "bearer"}, "")
	token := accessToken(t, login)

	// The token alone — lifted off the tablet and used from a laptop — is refused.
	if res := s.call(t, "GET", "/v1/auth/me", nil, token, ""); res.Status != http.StatusUnauthorized {
		t.Fatalf("device-bound token without the device: %d %s", res.Status, res.Raw)
	}
	// From a different enrolled device: also refused.
	if res := s.signed(t, other, "GET", "/v1/auth/me", nil, token); res.Status != http.StatusUnauthorized {
		t.Fatalf("device-bound token from another device: %d %s", res.Status, res.Raw)
	}
	// From its own device: fine.
	if res := s.signed(t, tab, "GET", "/v1/auth/me", nil, token); res.Status != http.StatusOK {
		t.Fatalf("from its own device: %d %s", res.Status, res.Raw)
	}

	// The refresh token, likewise: refused off the device — and refused before rotation,
	// so the tablet's own copy still works afterwards.
	refresh, _ := login.Body["refresh_token"].(string)
	if res := s.call(t, "POST", "/v1/auth/refresh", map[string]string{"refresh_token": refresh}, "", ""); res.Status != http.StatusUnauthorized {
		t.Fatalf("refresh off the device: %d %s", res.Status, res.Raw)
	}
	if res := s.signed(t, other, "POST", "/v1/auth/refresh", map[string]string{"refresh_token": refresh}, ""); res.Status != http.StatusUnauthorized {
		t.Fatalf("refresh from another device: %d %s", res.Status, res.Raw)
	}
	rotated := s.signed(t, tab, "POST", "/v1/auth/refresh", map[string]string{"refresh_token": refresh}, "")
	if rotated.Status != http.StatusOK {
		t.Fatalf("refresh from its own device: %d %s", rotated.Status, rotated.Raw)
	}
	if res := s.signed(t, tab, "GET", "/v1/auth/me", nil, accessToken(t, rotated)); res.Status != http.StatusOK {
		t.Fatalf("after refresh: %d %s", res.Status, res.Raw)
	}
}

func TestReEnrolmentRetiresTheOldKey(t *testing.T) {
	s := newAuthServer(t)
	admin := s.adminToken(t)
	tab := s.enrolTablet(t, admin, "Reinstalled tablet")

	reissued := s.call(t, "POST", "/v1/devices/"+tab.id+"/enrolments", nil, admin, "")
	if reissued.Status != http.StatusCreated {
		t.Fatalf("reissue: %d %s", reissued.Status, reissued.Raw)
	}
	// The old key works until the new one arrives.
	if res := s.signed(t, tab, "GET", "/v1/devices/self", nil, admin); res.Status != http.StatusOK {
		t.Fatalf("old key before re-enrolment: %d %s", res.Status, res.Raw)
	}

	pub, priv := newKeypair(t)
	code := reissued.Body["code"].(string)
	// Typed by hand: lower case, no dash, a zero for an O.
	typed := lowerAndConfuse(code)
	enrolled := s.call(t, "POST", "/v1/auth/device/enrol", map[string]string{
		"code": typed, "public_key": devicesig.EncodePublicKey(pub), "app_version": "1.3.0",
	}, "", "")
	if enrolled.Status != http.StatusOK {
		t.Fatalf("re-enrol with %q (from %q): %d %s", typed, code, enrolled.Status, enrolled.Raw)
	}

	// Old key: refused. New key: works. Code: spent.
	if res := s.signed(t, tab, "GET", "/v1/devices/self", nil, admin); res.Status != http.StatusUnauthorized {
		t.Fatalf("old key after re-enrolment: %d %s", res.Status, res.Raw)
	}
	fresh := tablet{id: tab.id, pub: pub, priv: priv}
	if res := s.signed(t, fresh, "GET", "/v1/devices/self", nil, admin); res.Status != http.StatusOK {
		t.Fatalf("new key: %d %s", res.Status, res.Raw)
	}
	again := s.call(t, "POST", "/v1/auth/device/enrol", map[string]string{
		"code": code, "public_key": devicesig.EncodePublicKey(pub),
	}, "", "")
	if again.Status != http.StatusUnauthorized {
		t.Fatalf("a spent code: %d %s", again.Status, again.Raw)
	}

	// Rotation by the device itself, signed with the current key.
	pub2, priv2 := newKeypair(t)
	rotated := s.signed(t, fresh, "POST", "/v1/devices/self/rotate-key", map[string]string{"public_key": devicesig.EncodePublicKey(pub2)}, admin)
	if rotated.Status != http.StatusOK {
		t.Fatalf("rotate: %d %s", rotated.Status, rotated.Raw)
	}
	if res := s.signed(t, fresh, "GET", "/v1/devices/self", nil, admin); res.Status != http.StatusUnauthorized {
		t.Fatalf("rotated-away key: %d %s", res.Status, res.Raw)
	}
	if res := s.signed(t, tablet{id: tab.id, pub: pub2, priv: priv2}, "GET", "/v1/devices/self", nil, admin); res.Status != http.StatusOK {
		t.Fatalf("rotated-in key: %d %s", res.Status, res.Raw)
	}
	s.assertInvariants(t)
}

func TestEnrolmentCodeExpiresAndIsSingleUse(t *testing.T) {
	s := newAuthServer(t)
	admin := s.adminToken(t)
	issued := s.call(t, "POST", "/v1/devices", map[string]string{"name": "Late tablet", "kind": "tablet"}, admin, "")
	code := issued.Body["code"].(string)
	pub, _ := newKeypair(t)

	// A duplicate name is a validation error, not a 500.
	dup := s.call(t, "POST", "/v1/devices", map[string]string{"name": "late TABLET", "kind": "tablet"}, admin, "")
	if dup.Status != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate name: %d %s", dup.Status, dup.Raw)
	}

	s.clock.Advance(auth.EnrolmentCodeLifetime + time.Second)
	res := s.call(t, "POST", "/v1/auth/device/enrol", map[string]string{"code": code, "public_key": devicesig.EncodePublicKey(pub)}, "", "")
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("expired code: %d %s", res.Status, res.Raw)
	}
	res = s.call(t, "POST", "/v1/auth/device/enrol", map[string]string{"code": "AAAAA-AAAAA", "public_key": devicesig.EncodePublicKey(pub)}, "", "")
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("unknown code: %d %s", res.Status, res.Raw)
	}
}

// lowerAndConfuse makes a code look typed: lower case, dash dropped, O written as 0.
func lowerAndConfuse(code string) string {
	out := make([]byte, 0, len(code))
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case c == '-':
			continue
		case c == 'O':
			out = append(out, '0')
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		default:
			out = append(out, c)
		}
	}
	return string(out)
}
