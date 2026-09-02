package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
)

// --- tokens ---

func TestTokensAreUnmemorableAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		token, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[token.Plaintext] {
			t.Fatalf("the same token was minted twice: %s", token.Plaintext)
		}
		seen[token.Plaintext] = true

		if len(token.Digest) != 32 {
			t.Fatalf("digest is %d bytes, want 32", len(token.Digest))
		}
		if strings.Contains(token.Plaintext, "=") {
			t.Errorf("the token carries base64 padding, which does not survive every URL: %q", token.Plaintext)
		}
		// The plaintext must not be recoverable from what the database holds.
		if strings.Contains(token.Plaintext, string(token.Digest)) {
			t.Error("the digest contains the plaintext")
		}
	}
}

func TestDigestOfMatchesWhatWasMinted(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if got := DigestOf(token.Plaintext); string(got) != string(token.Digest) {
		t.Error("a token does not hash to the digest it was minted with")
	}
	if got := DigestOf(token.Plaintext + "x"); string(got) == string(token.Digest) {
		t.Error("a different token hashed to the same digest")
	}
}

// --- throttle ---

func TestProgressiveDelayDoublesAndCaps(t *testing.T) {
	p := ThrottlePolicy{Free: 2, Base: time.Second, Max: 30 * time.Second, Window: time.Minute}

	for _, c := range []struct {
		failures int
		want     time.Duration
	}{
		{0, 0}, {1, 0}, {2, 0}, // mistyping twice is a Tuesday
		{3, time.Second},
		{4, 2 * time.Second},
		{5, 4 * time.Second},
		{6, 8 * time.Second},
		{7, 16 * time.Second},
		{8, 30 * time.Second},  // capped
		{40, 30 * time.Second}, // still capped, not a lockout
	} {
		if got := p.Delay(c.failures); got != c.want {
			t.Errorf("Delay(%d) = %s, want %s", c.failures, got, c.want)
		}
	}
}

// TestTheDelayIsNeverAnEffectiveLockout.
//
// An unbounded delay is a lockout wearing a different hat: anybody who knows an employee
// code — and they are printed on rosters and called across a clinic floor — could otherwise
// keep a doctor out of the system on the morning they need it.
func TestTheDelayIsNeverAnEffectiveLockout(t *testing.T) {
	p := DefaultThrottle()
	if got := p.Delay(1_000_000); got != p.Max {
		t.Errorf("a million failures produced %s; the cap is %s", got, p.Max)
	}
	if p.Max > time.Minute {
		t.Errorf("the cap is %s, which is long enough to be a denial of service in itself", p.Max)
	}
}

// --- login ---

func TestFailedLoginsAreIndistinguishable(t *testing.T) {
	s, store := newSessions(t)
	store.addUser("R01", "correct horse", StatusActive)
	store.addUser("R02", "correct horse", StatusSuspended)

	var messages []string
	for _, req := range []LoginRequest{
		{FacilityID: store.facility, Code: "NOBODY", Password: "correct horse"},
		{FacilityID: store.facility, Code: "R01", Password: "wrong"},
		{FacilityID: store.facility, Code: "R02", Password: "correct horse"},
	} {
		_, err := s.Login(context.Background(), req)
		if !errors.Is(err, ErrAuthentication) {
			t.Fatalf("%s: expected ErrAuthentication, got %v", req.Code, err)
		}
		messages = append(messages, err.Error())
	}

	for i := 1; i < len(messages); i++ {
		if messages[i] != messages[0] {
			t.Errorf("failures are distinguishable: %q vs %q", messages[0], messages[i])
		}
	}

	// And the reason is recorded, where an administrator can read it and an attacker cannot.
	want := []FailureKind{FailureNoSuchUser, FailureBadPassword, FailureNotActive}
	if len(store.attempts) != 3 {
		t.Fatalf("expected 3 recorded attempts, got %d", len(store.attempts))
	}
	for i, kind := range want {
		if store.attempts[i].Failure != kind {
			t.Errorf("attempt %d recorded %q, want %q", i, store.attempts[i].Failure, kind)
		}
	}
}

// TestAnUnknownAccountStillCostsAHashVerification.
//
// Returning the same message either way is undone by a stopwatch if the unknown-account path
// skips the expensive part. The dummy verify is what closes that.
func TestAnUnknownAccountStillCostsAHashVerification(t *testing.T) {
	s, store := newSessions(t)
	store.addUser("R01", "correct horse", StatusActive)

	before := store.hasher.verifications
	if _, err := s.Login(context.Background(), LoginRequest{
		FacilityID: store.facility, Code: "NOBODY", Password: "anything"}); err == nil {
		t.Fatal("an unknown account logged in")
	}
	if store.hasher.verifications == before {
		t.Error("no hash was verified for an unknown account, so the refusal is measurably faster")
	}
	if !store.hasher.sawDummy {
		t.Error("the verification was not against the dummy hash")
	}
}

// TestASuspendedAccountIsRefusedOnlyAfterThePasswordIsChecked.
//
// Order matters. Refusing on status first would let an attacker learn that an account is
// suspended — and therefore that it exists — without knowing its password.
func TestASuspendedAccountIsRefusedOnlyAfterThePasswordIsChecked(t *testing.T) {
	s, store := newSessions(t)
	store.addUser("R02", "correct horse", StatusSuspended)

	before := store.hasher.verifications
	_, _ = s.Login(context.Background(), LoginRequest{
		FacilityID: store.facility, Code: "R02", Password: "wrong"})
	if store.hasher.verifications == before {
		t.Fatal("the password was not verified before the status was consulted")
	}
	if store.attempts[0].Failure != FailureBadPassword {
		t.Errorf("a wrong password against a suspended account recorded %q; it should record "+
			"the password failure, because that is what happened first", store.attempts[0].Failure)
	}
}

func TestTheDelayIsAppliedBeforeTheAnswer(t *testing.T) {
	s, store := newSessions(t)
	store.addUser("R01", "correct horse", StatusActive)
	store.failuresByCode["R01"] = 6

	_, _ = s.Login(context.Background(), LoginRequest{
		FacilityID: store.facility, Code: "R01", Password: "correct horse"})

	if len(store.slept) != 1 {
		t.Fatalf("expected one delay, got %d", len(store.slept))
	}
	if store.slept[0] != 8*time.Second {
		t.Errorf("delayed %s, want 8s for six recent failures", store.slept[0])
	}
	// Applied even though this login succeeds: a delay that only happens on failure tells
	// the attacker which guess was right.
	if store.sleptAfterLookup {
		t.Error("the delay was applied after the user lookup, so response time still leaks")
	}
}

func TestTheWorseOfTheTwoThrottlesWins(t *testing.T) {
	s, store := newSessions(t)
	store.addUser("R01", "correct horse", StatusActive)
	store.failuresByCode["R01"] = 3        // 1s
	store.failuresByClient["10.0.0.1"] = 6 // 8s — someone working through the roster
	digest := DigestOfRaw([]byte("10.0.0.1"))

	_, _ = s.Login(context.Background(), LoginRequest{
		FacilityID: store.facility, Code: "R01", Password: "correct horse", ClientDigest: digest})

	if store.slept[0] != 8*time.Second {
		t.Errorf("delayed %s; the client throttle was worse and should have won", store.slept[0])
	}
}

func TestASuccessfulLoginUpgradesAWeakHash(t *testing.T) {
	s, store := newSessions(t)
	store.addUser("R01", "correct horse", StatusActive)
	store.hasher.needsRehash = true

	if _, err := s.Login(context.Background(), LoginRequest{
		FacilityID: store.facility, Code: "R01", Password: "correct horse"}); err != nil {
		t.Fatal(err)
	}
	if !store.rehashed {
		t.Error("a password verified under weaker parameters was not upgraded, and a correct " +
			"login is the only moment the plaintext is available to do it")
	}
}

// --- refresh and reuse ---

func TestRefreshRotatesBothTokens(t *testing.T) {
	s, store := newSessions(t)
	store.addUser("R01", "correct horse", StatusActive)

	first := mustLogin(t, s, store, "R01", "correct horse")
	second, err := s.Refresh(context.Background(), first.RefreshToken)
	if err != nil {
		t.Fatalf("refreshing: %v", err)
	}

	if second.AccessToken == first.AccessToken {
		t.Error("the access token was not rotated")
	}
	if second.RefreshToken == first.RefreshToken {
		t.Error("the refresh token was not rotated")
	}
	if second.Session.ID != first.Session.ID {
		t.Error("refreshing created a new session; it should re-key the existing one, or " +
			"the session list fills with ghosts and step-up state is lost every fifteen minutes")
	}

	// The old access token must stop working the moment the new one exists.
	if _, _, err := s.Authenticate(context.Background(), first.AccessToken); !errors.Is(err, ErrSessionInvalid) {
		t.Error("the previous access token still authenticates after a refresh")
	}
}

// TestRefreshDoesNotExtendTheRefreshWindow.
//
// If a rotated token got a fresh fourteen days, a session used every day would never expire
// and "log in again every fortnight" would quietly become "never log in again".
func TestRefreshDoesNotExtendTheRefreshWindow(t *testing.T) {
	s, store := newSessions(t)
	store.addUser("R01", "correct horse", StatusActive)

	first := mustLogin(t, s, store, "R01", "correct horse")
	store.clock.Advance(24 * time.Hour)

	second, err := s.Refresh(context.Background(), first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if !second.RefreshExpiry.Equal(first.RefreshExpiry) {
		t.Errorf("the refresh window moved from %s to %s", first.RefreshExpiry, second.RefreshExpiry)
	}
}

// TestAReusedRefreshTokenRevokesTheWholeFamily is acceptance criterion 2.
//
// A spent token arriving again means either a client retried after a dropped response, or
// somebody else has a copy. The server cannot tell which, and only one of those readings is
// safe to act on.
func TestAReusedRefreshTokenRevokesTheWholeFamily(t *testing.T) {
	s, store := newSessions(t)
	store.addUser("R01", "correct horse", StatusActive)

	first := mustLogin(t, s, store, "R01", "correct horse")
	second, err := s.Refresh(context.Background(), first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}

	// The attacker replays the token they stole.
	_, err = s.Refresh(context.Background(), first.RefreshToken)
	if !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("expected ErrRefreshReused, got %v", err)
	}

	// And the legitimate holder's current token is dead too. That is the point: the server
	// cannot tell them apart, so both are ended and both log in again.
	if _, err := s.Refresh(context.Background(), second.RefreshToken); err == nil {
		t.Error("the successor token still works after its family was revoked")
	}
	if _, _, err := s.Authenticate(context.Background(), second.AccessToken); !errors.Is(err, ErrSessionInvalid) {
		t.Error("the session survived the revocation of its refresh family")
	}
}

func TestRefreshRefusesAUserWhoIsNoLongerActive(t *testing.T) {
	s, store := newSessions(t)
	user := store.addUser("R01", "correct horse", StatusActive)

	first := mustLogin(t, s, store, "R01", "correct horse")
	store.setStatus(user, StatusSuspended)

	if _, err := s.Refresh(context.Background(), first.RefreshToken); !errors.Is(err, ErrSessionInvalid) {
		t.Errorf("a suspended user refreshed their session: %v", err)
	}
}

// --- authenticating ---

// TestRevocationTakesEffectWithinOneRequest is acceptance criterion 3.
func TestRevocationTakesEffectWithinOneRequest(t *testing.T) {
	s, store := newSessions(t)
	store.addUser("R01", "correct horse", StatusActive)
	creds := mustLogin(t, s, store, "R01", "correct horse")

	if _, _, err := s.Authenticate(context.Background(), creds.AccessToken); err != nil {
		t.Fatalf("a fresh session did not authenticate: %v", err)
	}

	if err := s.Logout(context.Background(), creds.Session.ID, nil, "signed out"); err != nil {
		t.Fatal(err)
	}

	// The very next request, with no cache to wait for.
	if _, _, err := s.Authenticate(context.Background(), creds.AccessToken); !errors.Is(err, ErrSessionInvalid) {
		t.Error("a revoked session still authenticated on the next request")
	}
}

func TestSuspensionEndsEverySessionAtOnce(t *testing.T) {
	s, store := newSessions(t)
	user := store.addUser("R01", "correct horse", StatusActive)

	phone := mustLogin(t, s, store, "R01", "correct horse")
	desk := mustLogin(t, s, store, "R01", "correct horse")

	store.setStatus(user, StatusSuspended)

	for name, token := range map[string]string{"phone": phone.AccessToken, "desk": desk.AccessToken} {
		if _, _, err := s.Authenticate(context.Background(), token); !errors.Is(err, ErrSessionInvalid) {
			t.Errorf("the %s session survived suspension", name)
		}
	}
	// Without a single grant being touched.
	if store.revocations != 0 {
		t.Errorf("suspension revoked %d sessions individually; one status check should do it", store.revocations)
	}
}

func TestAnExpiredSessionDoesNotAuthenticate(t *testing.T) {
	s, store := newSessions(t)
	store.addUser("R01", "correct horse", StatusActive)
	creds := mustLogin(t, s, store, "R01", "correct horse")

	store.clock.Advance(16 * time.Minute)
	if _, _, err := s.Authenticate(context.Background(), creds.AccessToken); !errors.Is(err, ErrSessionInvalid) {
		t.Error("an expired access token still authenticated")
	}
}

func TestAnUnknownTokenIsRefusedLikeAnyOther(t *testing.T) {
	s, _ := newSessions(t)
	for _, token := range []string{"", "not-a-token", strings.Repeat("A", 43)} {
		if _, _, err := s.Authenticate(context.Background(), token); !errors.Is(err, ErrSessionInvalid) {
			t.Errorf("token %q produced %v, want ErrSessionInvalid", token, err)
		}
	}
}

func TestLastSeenIsNotWrittenOnEveryRequest(t *testing.T) {
	s, store := newSessions(t)
	store.addUser("R01", "correct horse", StatusActive)
	creds := mustLogin(t, s, store, "R01", "correct horse")

	for i := 0; i < 20; i++ {
		if _, _, err := s.Authenticate(context.Background(), creds.AccessToken); err != nil {
			t.Fatal(err)
		}
	}
	if store.touches != 0 {
		t.Errorf("twenty requests in the same instant wrote last_seen %d times; the busiest "+
			"table in the clinic does not need that", store.touches)
	}

	store.clock.Advance(2 * time.Minute)
	if _, _, err := s.Authenticate(context.Background(), creds.AccessToken); err != nil {
		t.Fatal(err)
	}
	if store.touches != 1 {
		t.Errorf("last_seen was written %d times after two minutes, want 1", store.touches)
	}
}

// --- logging out ---

// TestLogoutAlsoEndsTheRefreshToken.
//
// Revoking the session alone leaves a refresh token that mints a new one — a logout that
// logs nobody out.
func TestLogoutAlsoEndsTheRefreshToken(t *testing.T) {
	s, store := newSessions(t)
	store.addUser("R01", "correct horse", StatusActive)
	creds := mustLogin(t, s, store, "R01", "correct horse")

	if err := s.Logout(context.Background(), creds.Session.ID, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refresh(context.Background(), creds.RefreshToken); err == nil {
		t.Fatal("the refresh token outlived the logout and could mint a new session")
	}
}

func TestLogoutEverywhereReachesDevicesYouNoLongerHave(t *testing.T) {
	s, store := newSessions(t)
	user := store.addUser("R01", "correct horse", StatusActive)

	lost := mustLogin(t, s, store, "R01", "correct horse")
	kept := mustLogin(t, s, store, "R01", "correct horse")

	n, err := s.LogoutEverywhere(context.Background(), user, nil, "password may be known")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("ended %d sessions, want 2", n)
	}
	for name, token := range map[string]string{"lost phone": lost.AccessToken, "desk": kept.AccessToken} {
		if _, _, err := s.Authenticate(context.Background(), token); !errors.Is(err, ErrSessionInvalid) {
			t.Errorf("the %s session survived a logout-everywhere", name)
		}
	}
}

func TestSessionListShowsOnlyLiveLogins(t *testing.T) {
	s, store := newSessions(t)
	user := store.addUser("R01", "correct horse", StatusActive)

	one := mustLogin(t, s, store, "R01", "correct horse")
	mustLogin(t, s, store, "R01", "correct horse")

	if err := s.Logout(context.Background(), one.Session.ID, nil, ""); err != nil {
		t.Fatal(err)
	}

	live, err := s.Sessions(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 {
		t.Errorf("listed %d live sessions, want 1", len(live))
	}
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

func newSessions(t *testing.T) (*Sessions, *memSessions) {
	t.Helper()
	store := &memSessions{
		facility:         uuid.New(),
		clock:            clock.NewFixed(time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)),
		users:            map[uuid.UUID]User{},
		passwords:        map[uuid.UUID]string{},
		byCode:           map[string]uuid.UUID{},
		sessions:         map[uuid.UUID]*Session{},
		sessionByDigest:  map[string]uuid.UUID{},
		refresh:          map[uuid.UUID]*RefreshToken{},
		refreshByDigest:  map[string]uuid.UUID{},
		failuresByCode:   map[string]int{},
		failuresByClient: map[string]int{},
		hasher:           &fakeHasher{},
	}

	svc := NewSessions(SessionsConfig{
		Store: store, Hasher: store.hasher, Clock: store.clock,
		Throttle:  ThrottlePolicy{Free: 2, Base: time.Second, Max: 30 * time.Second, Window: 15 * time.Minute},
		Lifetimes: DefaultLifetimes(),
		Sleep: func(_ context.Context, d time.Duration) {
			store.slept = append(store.slept, d)
			store.sleptAfterLookup = store.lookups > 0
		},
	})
	return svc, store
}

func mustLogin(t *testing.T, s *Sessions, store *memSessions, code, password string) Credentials {
	t.Helper()
	creds, err := s.Login(context.Background(), LoginRequest{
		FacilityID: store.facility, Code: code, Password: password, UserAgent: "test"})
	if err != nil {
		t.Fatalf("logging in as %s: %v", code, err)
	}
	return creds
}

// fakeHasher stands in for argon2id, which lives in internal/auth/pwhash and needs a
// cryptography library. Everything the service does *around* hashing is what these tests
// are for, and none of it depends on the hash being real.
type fakeHasher struct {
	verifications int
	sawDummy      bool
	needsRehash   bool
}

const dummyHash = "$fake$dummy"

func (h *fakeHasher) Hash(password string) (string, error) { return "$fake$" + password, nil }
func (h *fakeHasher) Dummy() string                        { return dummyHash }

func (h *fakeHasher) Verify(password, encoded string) (bool, bool, error) {
	h.verifications++
	if encoded == dummyHash {
		h.sawDummy = true
		return false, false, nil
	}
	return encoded == "$fake$"+password, h.needsRehash, nil
}

type memSessions struct {
	facility uuid.UUID
	clock    *clock.Fixed
	hasher   *fakeHasher

	users     map[uuid.UUID]User
	passwords map[uuid.UUID]string
	byCode    map[string]uuid.UUID

	sessions        map[uuid.UUID]*Session
	sessionByDigest map[string]uuid.UUID
	refresh         map[uuid.UUID]*RefreshToken
	refreshByDigest map[string]uuid.UUID

	attempts         []Attempt
	failuresByCode   map[string]int
	failuresByClient map[string]int

	slept            []time.Duration
	sleptAfterLookup bool
	lookups          int
	touches          int
	revocations      int
	rehashed         bool
}

func (m *memSessions) addUser(code, password string, status Status) uuid.UUID {
	id := uuid.New()
	m.users[id] = User{ID: id, FacilityID: m.facility, Code: code, Status: status}
	m.passwords[id] = "$fake$" + password
	m.byCode[code] = id
	return id
}

func (m *memSessions) setStatus(id uuid.UUID, status Status) {
	u := m.users[id]
	u.Status = status
	m.users[id] = u
}

func (m *memSessions) CredentialsByCode(_ context.Context, _ uuid.UUID, code string) (User, string, error) {
	m.lookups++
	id, ok := m.byCode[code]
	if !ok {
		return User{}, "", fmt.Errorf("no user with code %s", code)
	}
	return m.users[id], m.passwords[id], nil
}

func (m *memSessions) UpdatePasswordHash(_ context.Context, id uuid.UUID, hash string) error {
	m.passwords[id] = hash
	m.rehashed = true
	return nil
}

func (m *memSessions) RecentFailures(_ context.Context, _ uuid.UUID, code string, _ time.Time) (int, error) {
	return m.failuresByCode[code], nil
}

func (m *memSessions) RecentFailuresForClient(_ context.Context, digest []byte, _ time.Time) (int, error) {
	for address, n := range m.failuresByClient {
		if string(DigestOfRaw([]byte(address))) == string(digest) {
			return n, nil
		}
	}
	return 0, nil
}

func (m *memSessions) RecordAttempt(_ context.Context, a Attempt) error {
	m.attempts = append(m.attempts, a)
	return nil
}

func (m *memSessions) CreateSession(_ context.Context, s Session, digest []byte) (Session, error) {
	s.ID = uuid.New()
	m.sessions[s.ID] = &s
	m.sessionByDigest[string(digest)] = s.ID
	return s, nil
}

func (m *memSessions) SessionByToken(_ context.Context, digest []byte) (Session, error) {
	id, ok := m.sessionByDigest[string(digest)]
	if !ok {
		return Session{}, errors.New("no such session")
	}
	return *m.sessions[id], nil
}

func (m *memSessions) SessionByID(_ context.Context, id uuid.UUID) (Session, error) {
	s, ok := m.sessions[id]
	if !ok {
		return Session{}, errors.New("no such session")
	}
	return *s, nil
}

func (m *memSessions) UserByID(_ context.Context, id uuid.UUID) (User, error) {
	u, ok := m.users[id]
	if !ok {
		return User{}, errors.New("no such user")
	}
	return u, nil
}

func (m *memSessions) TouchSession(_ context.Context, id uuid.UUID, at time.Time) error {
	m.touches++
	m.sessions[id].LastSeenAt = at
	return nil
}

func (m *memSessions) RevokeSession(_ context.Context, id uuid.UUID, by *uuid.UUID, reason string) error {
	s, ok := m.sessions[id]
	if !ok {
		return errors.New("no such session")
	}
	now := m.clock.Now()
	s.RevokedAt, s.RevokeReason = &now, reason
	m.revocations++
	return nil
}

func (m *memSessions) RevokeSessionsForUser(_ context.Context, userID uuid.UUID, _ *uuid.UUID, reason string) (int, error) {
	now := m.clock.Now()
	n := 0
	for _, s := range m.sessions {
		if s.UserID == userID && s.RevokedAt == nil {
			s.RevokedAt, s.RevokeReason = &now, reason
			n++
		}
	}
	return n, nil
}

func (m *memSessions) SessionsForUser(_ context.Context, userID uuid.UUID) ([]Session, error) {
	var out []Session
	for _, s := range m.sessions {
		if s.UserID == userID {
			out = append(out, *s)
		}
	}
	return out, nil
}

func (m *memSessions) CreateRefresh(_ context.Context, r RefreshToken, digest []byte) (RefreshToken, error) {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	m.refresh[r.ID] = &r
	m.refreshByDigest[string(digest)] = r.ID
	return r, nil
}

func (m *memSessions) RefreshByToken(_ context.Context, digest []byte) (RefreshToken, error) {
	id, ok := m.refreshByDigest[string(digest)]
	if !ok {
		return RefreshToken{}, errors.New("no such refresh token")
	}
	return *m.refresh[id], nil
}

func (m *memSessions) RotateRefresh(ctx context.Context, spent uuid.UUID, next RefreshToken,
	nextDigest []byte, accessDigest []byte, accessExpiry time.Time, at time.Time) error {
	old, ok := m.refresh[spent]
	if !ok {
		return errors.New("no such refresh token")
	}
	old.UsedAt = &at
	old.ReplacedBy = &next.ID

	if _, err := m.CreateRefresh(ctx, next, nextDigest); err != nil {
		return err
	}

	session, ok := m.sessions[next.SessionID]
	if !ok {
		return errors.New("no such session")
	}
	// Re-keying drops the old digest, which is what makes the previous access token stop
	// working the instant the new one exists.
	for digest, id := range m.sessionByDigest {
		if id == session.ID {
			delete(m.sessionByDigest, digest)
		}
	}
	m.sessionByDigest[string(accessDigest)] = session.ID
	session.ExpiresAt = accessExpiry
	session.LastSeenAt = at
	return nil
}

func (m *memSessions) RevokeFamily(_ context.Context, familyID uuid.UUID, reason string) (int, error) {
	now := m.clock.Now()
	n := 0
	for _, r := range m.refresh {
		if r.FamilyID != familyID {
			continue
		}
		if r.RevokedAt == nil {
			r.RevokedAt, r.RevokeReason = &now, reason
			n++
		}
		if s, ok := m.sessions[r.SessionID]; ok && s.RevokedAt == nil {
			s.RevokedAt, s.RevokeReason = &now, reason
		}
	}
	return n, nil
}

func (m *memSessions) RevokeRefreshForSession(_ context.Context, sessionID uuid.UUID, reason string) error {
	now := m.clock.Now()
	for _, r := range m.refresh {
		if r.SessionID == sessionID && r.RevokedAt == nil {
			r.RevokedAt, r.RevokeReason = &now, reason
		}
	}
	return nil
}
