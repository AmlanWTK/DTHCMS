package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
)

// PasswordHasher is what the session service needs of argon2id, and nothing more.
//
// An interface because the implementation is the one part of authentication that needs a
// cryptography library, and every rule around it — the throttle, the lifecycle, the
// rotation — is worth testing without one. internal/auth/pwhash provides the real thing.
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encoded string) (ok bool, needsRehash bool, err error)
	// Dummy is a valid hash of nothing, verified against when no user is found so that a
	// login for an account that does not exist costs the same time as one that does.
	Dummy() string
}

// SessionStore is the persistence the session service needs.
type SessionStore interface {
	// CredentialsByCode returns the user and their stored password hash. The hash is
	// returned separately rather than on User because nothing else in the system has any
	// business reading it.
	CredentialsByCode(ctx context.Context, facilityID uuid.UUID, code string) (User, string, error)
	UpdatePasswordHash(ctx context.Context, userID uuid.UUID, hash string) error

	RecentFailures(ctx context.Context, facilityID uuid.UUID, code string, since time.Time) (int, error)
	RecentFailuresForClient(ctx context.Context, clientDigest []byte, since time.Time) (int, error)
	RecordAttempt(ctx context.Context, a Attempt) error

	CreateSession(ctx context.Context, s Session, tokenDigest []byte) (Session, error)
	SessionByToken(ctx context.Context, tokenDigest []byte) (Session, error)
	TouchSession(ctx context.Context, id uuid.UUID, at time.Time) error
	RevokeSession(ctx context.Context, id uuid.UUID, by *uuid.UUID, reason string) error
	RevokeSessionsForUser(ctx context.Context, userID uuid.UUID, by *uuid.UUID, reason string) (int, error)
	SessionsForUser(ctx context.Context, userID uuid.UUID) ([]Session, error)

	SessionByID(ctx context.Context, id uuid.UUID) (Session, error)
	UserByID(ctx context.Context, id uuid.UUID) (User, error)

	CreateRefresh(ctx context.Context, r RefreshToken, tokenDigest []byte) (RefreshToken, error)
	RefreshByToken(ctx context.Context, tokenDigest []byte) (RefreshToken, error)

	// RotateRefresh spends one token and issues its successor, re-keying the session's
	// access token in the same breath — and in one transaction.
	//
	// One call rather than three because the intermediate states are all wrong. Marking the
	// old token used and then failing to insert the new one locks the user out; inserting
	// first and then failing to mark leaves two live tokens in a lineage whose whole purpose
	// is that there is exactly one. Neither is a state to recover from at three in the
	// morning, so neither is a state that can exist.
	RotateRefresh(ctx context.Context, spent uuid.UUID, next RefreshToken, nextDigest []byte,
		accessDigest []byte, accessExpiry time.Time, at time.Time) error

	// RevokeFamily ends every token *and every session* descended from one login.
	//
	// Both, in one transaction. Revoking the tokens alone leaves the access tokens already
	// issued under that family working until they expire — up to a full lifetime after the
	// theft was detected, which is the window this whole mechanism exists to close.
	RevokeFamily(ctx context.Context, familyID uuid.UUID, reason string) (int, error)
	RevokeRefreshForSession(ctx context.Context, sessionID uuid.UUID, reason string) error
}

// Lifetimes are how long each credential lasts.
//
// The access token is short because it is the one that travels on every request; the
// refresh token is long because it is what stops a nurse logging in twice a shift. D-44
// ratified the shape; ADR-0011 changed the access token from signed to opaque and left
// these untouched.
type Lifetimes struct {
	Access  time.Duration
	Refresh time.Duration
}

// DefaultLifetimes: fifteen minutes and fourteen days.
func DefaultLifetimes() Lifetimes {
	return Lifetimes{Access: 15 * time.Minute, Refresh: 14 * 24 * time.Hour}
}

// Sessions issues, refreshes and ends logins.
type Sessions struct {
	store    SessionStore
	hasher   PasswordHasher
	clock    clock.Clock
	throttle ThrottlePolicy
	lifetime Lifetimes

	// sleep is injectable so a test can prove the delay was applied without waiting for it.
	// Nothing outside a test replaces it; the point is to make the delay observable, not
	// optional.
	sleep func(context.Context, time.Duration)
}

// SessionsConfig assembles the service.
type SessionsConfig struct {
	Store     SessionStore
	Hasher    PasswordHasher
	Clock     clock.Clock
	Throttle  ThrottlePolicy
	Lifetimes Lifetimes
	Sleep     func(context.Context, time.Duration)
}

func NewSessions(cfg SessionsConfig) *Sessions {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Throttle == (ThrottlePolicy{}) {
		cfg.Throttle = DefaultThrottle()
	}
	if cfg.Lifetimes == (Lifetimes{}) {
		cfg.Lifetimes = DefaultLifetimes()
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepOrCancel
	}
	return &Sessions{
		store: cfg.Store, hasher: cfg.Hasher, clock: cfg.Clock,
		throttle: cfg.Throttle, lifetime: cfg.Lifetimes, sleep: cfg.Sleep,
	}
}

func sleepOrCancel(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// ErrAuthentication is the only error a failed login produces.
//
// One error, one message, whatever went wrong: unknown code, wrong password, suspended
// account, throttled. Distinguishing them for the caller would tell an attacker which of
// their guesses was half right. The reason is written to core.login_attempt, where an
// administrator can read it and an attacker cannot.
var ErrAuthentication = errors.New("the employee code or password is not correct")

// ErrSessionInvalid means the presented token authenticates nobody — unknown, expired or
// revoked, and again without saying which.
var ErrSessionInvalid = errors.New("the session is not valid")

// Login exchanges a code and a password for a session.
func (s *Sessions) Login(ctx context.Context, req LoginRequest) (Credentials, error) {
	now := s.clock.Now()

	// The delay comes first, before anything is looked up. Applying it afterwards, or only
	// on failure, would let the response time say whether the account exists.
	if err := s.applyDelay(ctx, req, now); err != nil {
		return Credentials{}, err
	}

	user, hash, err := s.store.CredentialsByCode(ctx, req.FacilityID, req.Code)
	if err != nil {
		// No user. Spend the time anyway, so this path is not measurably shorter than the
		// one that verifies a real hash.
		_, _, _ = s.hasher.Verify(req.Password, s.hasher.Dummy())
		s.record(ctx, req, nil, false, FailureNoSuchUser, now)
		return Credentials{}, ErrAuthentication
	}

	if hash == "" {
		s.record(ctx, req, &user.ID, false, FailureNoPasswordSet, now)
		return Credentials{}, ErrAuthentication
	}

	ok, needsRehash, err := s.hasher.Verify(req.Password, hash)
	if err != nil {
		return Credentials{}, fmt.Errorf("verifying the password: %w", err)
	}
	if !ok {
		s.record(ctx, req, &user.ID, false, FailureBadPassword, now)
		return Credentials{}, ErrAuthentication
	}

	// The password was right. Whether the account may be used is a separate question, and
	// answering it after verification rather than before means an attacker cannot learn a
	// suspended account's status without also knowing its password.
	if user.Status != StatusActive {
		s.record(ctx, req, &user.ID, false, FailureNotActive, now)
		return Credentials{}, ErrAuthentication
	}

	// A correct password verified under weaker parameters than we now require is the one
	// moment the plaintext is available to upgrade it. Failing to rehash is not a reason to
	// refuse a valid login.
	if needsRehash {
		if upgraded, err := s.hasher.Hash(req.Password); err == nil {
			_ = s.store.UpdatePasswordHash(ctx, user.ID, upgraded)
		}
	}

	s.record(ctx, req, &user.ID, true, FailureNone, now)
	return s.issue(ctx, user, uuid.New(), req.UserAgent, now)
}

// LoginRequest is what a login needs to know.
type LoginRequest struct {
	FacilityID uuid.UUID
	Code       string
	Password   string
	UserAgent  string
	// ClientDigest fingerprints the caller's address for throttling. Nil where the address
	// is unknown; the per-code throttle still applies.
	ClientDigest []byte
}

func (s *Sessions) applyDelay(ctx context.Context, req LoginRequest, now time.Time) error {
	since := s.throttle.Since(now)

	byCode, err := s.store.RecentFailures(ctx, req.FacilityID, req.Code, since)
	if err != nil {
		return fmt.Errorf("reading recent failures: %w", err)
	}
	worst := byCode

	if len(req.ClientDigest) > 0 {
		byClient, err := s.store.RecentFailuresForClient(ctx, req.ClientDigest, since)
		if err != nil {
			return fmt.Errorf("reading recent failures for the client: %w", err)
		}
		if byClient > worst {
			worst = byClient
		}
	}

	if delay := s.throttle.Delay(worst); delay > 0 {
		s.sleep(ctx, delay)
	}
	return nil
}

func (s *Sessions) record(ctx context.Context, req LoginRequest, userID *uuid.UUID,
	succeeded bool, kind FailureKind, now time.Time) {
	// Deliberately not returning the error. A login that succeeded must not be refused
	// because the audit write failed, and a login that failed is already being refused.
	// The write failing is itself logged by the store.
	_ = s.store.RecordAttempt(ctx, Attempt{
		FacilityID: req.FacilityID, Code: req.Code, UserID: userID,
		Succeeded: succeeded, Failure: kind, ClientDigest: req.ClientDigest, At: now,
	})
}

// issue mints a session and the first refresh token of a family.
func (s *Sessions) issue(ctx context.Context, user User, familyID uuid.UUID,
	userAgent string, now time.Time) (Credentials, error) {
	access, err := NewToken()
	if err != nil {
		return Credentials{}, err
	}
	refresh, err := NewToken()
	if err != nil {
		return Credentials{}, err
	}

	accessExpiry := now.Add(s.lifetime.Access)
	refreshExpiry := now.Add(s.lifetime.Refresh)

	session, err := s.store.CreateSession(ctx, Session{
		FacilityID: user.FacilityID, UserID: user.ID,
		IssuedAt: now, ExpiresAt: accessExpiry, LastSeenAt: now,
		UserAgent: truncate(userAgent, 256),
	}, access.Digest)
	if err != nil {
		return Credentials{}, fmt.Errorf("creating the session: %w", err)
	}

	if _, err := s.store.CreateRefresh(ctx, RefreshToken{
		// The id is allocated here rather than by the database, so that rotation can name
		// the successor in the predecessor's replaced_by column without a round trip.
		ID:        uuid.New(),
		SessionID: session.ID, FamilyID: familyID, FacilityID: user.FacilityID,
		IssuedAt: now, ExpiresAt: refreshExpiry,
	}, refresh.Digest); err != nil {
		return Credentials{}, fmt.Errorf("creating the refresh token: %w", err)
	}

	return Credentials{
		Session: session, AccessToken: access.Plaintext, RefreshToken: refresh.Plaintext,
		AccessExpiry: accessExpiry, RefreshExpiry: refreshExpiry,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ---------------------------------------------------------------------------
// Refresh
// ---------------------------------------------------------------------------

// ErrRefreshReused means a token that had already been exchanged was presented again.
//
// Separate from ErrSessionInvalid for the log and for the alert, never for the response:
// the client is told the same thing either way. What it means operationally is that two
// parties hold the same refresh token, and the server cannot tell which of them is the
// staff member.
var ErrRefreshReused = errors.New("a spent refresh token was presented again; the family was revoked")

// Refresh exchanges a refresh token for a new pair.
//
// The reuse rule is the reason this is not simply a lookup. A refresh token is spent when
// it is exchanged. If a spent one arrives, exactly one of two things has happened: a client
// retried after a dropped response, or somebody else has a copy. There is no way to tell
// them apart from here, and the safe reading of the second is that the whole lineage is
// compromised — so the family is revoked and everyone in it logs in again.
//
// That is disruptive on a bad network and correct on a bad day. It is the standard answer
// because the alternative — assuming a retry — means a stolen token keeps working.
func (s *Sessions) Refresh(ctx context.Context, plaintext string) (Credentials, error) {
	now := s.clock.Now()

	token, err := s.store.RefreshByToken(ctx, DigestOf(plaintext))
	if err != nil {
		return Credentials{}, ErrSessionInvalid
	}

	if token.Spent() {
		if _, err := s.store.RevokeFamily(ctx, token.FamilyID, "refresh token reused"); err != nil {
			return Credentials{}, fmt.Errorf("revoking the compromised family: %w", err)
		}
		return Credentials{}, ErrRefreshReused
	}

	if !token.Usable(now) {
		return Credentials{}, ErrSessionInvalid
	}

	session, err := s.store.SessionByID(ctx, token.SessionID)
	if err != nil {
		return Credentials{}, ErrSessionInvalid
	}
	if session.RevokedAt != nil {
		return Credentials{}, ErrSessionInvalid
	}

	// A refresh is a fresh authorisation decision, so the account is checked again. This is
	// what makes suspension bite on a session that was already open: the access token
	// expires within fifteen minutes and the refresh that would extend it is refused.
	user, err := s.store.UserByID(ctx, session.UserID)
	if err != nil || user.Status != StatusActive {
		return Credentials{}, ErrSessionInvalid
	}

	access, err := NewToken()
	if err != nil {
		return Credentials{}, err
	}
	next, err := NewToken()
	if err != nil {
		return Credentials{}, err
	}

	accessExpiry := now.Add(s.lifetime.Access)

	// The successor keeps the family and the session, and — deliberately — the original
	// refresh expiry rather than a fresh one. Otherwise a session that is used often never
	// expires, and "log in again every fortnight" quietly becomes "never log in again".
	successor := RefreshToken{
		ID:        uuid.New(),
		SessionID: session.ID,
		FamilyID:  token.FamilyID,
		// A rotated token inherits the facility of the token it replaces, not of whoever
		// is asking: a session cannot change facility by being refreshed.
		FacilityID: token.FacilityID,
		IssuedAt:   now,
		ExpiresAt:  token.ExpiresAt,
	}

	if err := s.store.RotateRefresh(ctx, token.ID, successor, next.Digest,
		access.Digest, accessExpiry, now); err != nil {
		return Credentials{}, fmt.Errorf("rotating the refresh token: %w", err)
	}

	session.ExpiresAt = accessExpiry
	session.LastSeenAt = now

	return Credentials{
		Session: session, AccessToken: access.Plaintext, RefreshToken: next.Plaintext,
		AccessExpiry: accessExpiry, RefreshExpiry: successor.ExpiresAt,
	}, nil
}

// ---------------------------------------------------------------------------
// Authenticating a request
// ---------------------------------------------------------------------------

// touchInterval is how stale last_seen_at may get before a request writes it.
//
// Without it every authenticated request is a write, which turns a read-mostly path into a
// row lock on the busiest table in the clinic. A minute of imprecision on "when was this
// session last used" costs nobody anything.
const touchInterval = time.Minute

// Authenticate resolves an access token to the user holding it.
//
// This runs on every authenticated request, and it is where acceptance criterion 3 lives:
// revocation takes effect within one request because the registry is consulted every time,
// not because a cache was invalidated somewhere and we hope it propagated.
func (s *Sessions) Authenticate(ctx context.Context, plaintext string) (User, Session, error) {
	now := s.clock.Now()

	session, err := s.store.SessionByToken(ctx, DigestOf(plaintext))
	if err != nil {
		return User{}, Session{}, ErrSessionInvalid
	}
	if !session.Live(now) {
		return User{}, Session{}, ErrSessionInvalid
	}

	user, err := s.store.UserByID(ctx, session.UserID)
	if err != nil {
		return User{}, Session{}, ErrSessionInvalid
	}
	// Suspension has to bite immediately, and walking a user's sessions to revoke each one
	// would be slower, would be a partial state if it failed halfway, and would have to be
	// undone one by one to reinstate them. One status check does it.
	if user.Status != StatusActive {
		return User{}, Session{}, ErrSessionInvalid
	}

	if now.Sub(session.LastSeenAt) > touchInterval {
		// Best effort. A failed bookkeeping write must not refuse a valid request.
		_ = s.store.TouchSession(ctx, session.ID, now)
		session.LastSeenAt = now
	}

	return user, session, nil
}

// ---------------------------------------------------------------------------
// Ending sessions
// ---------------------------------------------------------------------------

// Logout ends one session and the refresh tokens that could extend it.
//
// Both, because ending the session alone would leave a refresh token that mints a new one —
// a logout that logs nobody out.
func (s *Sessions) Logout(ctx context.Context, sessionID uuid.UUID, by *uuid.UUID, reason string) error {
	if reason == "" {
		reason = "signed out"
	}
	if err := s.store.RevokeSession(ctx, sessionID, by, reason); err != nil {
		return fmt.Errorf("revoking the session: %w", err)
	}
	if err := s.store.RevokeRefreshForSession(ctx, sessionID, reason); err != nil {
		return fmt.Errorf("revoking the session's refresh tokens: %w", err)
	}
	return nil
}

// LogoutEverywhere ends every live session a user holds.
//
// The button exists for the moment somebody thinks their password is known, so it must end
// sessions on devices they no longer have — which is exactly the case a client-side logout
// cannot reach.
func (s *Sessions) LogoutEverywhere(ctx context.Context, userID uuid.UUID, by *uuid.UUID, reason string) (int, error) {
	if reason == "" {
		reason = "signed out everywhere"
	}
	n, err := s.store.RevokeSessionsForUser(ctx, userID, by, reason)
	if err != nil {
		return 0, fmt.Errorf("revoking the user's sessions: %w", err)
	}
	return n, nil
}

// Sessions lists a user's live logins, for the screen that lets them end one.
func (s *Sessions) Sessions(ctx context.Context, userID uuid.UUID) ([]Session, error) {
	all, err := s.store.SessionsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	now := s.clock.Now()
	live := make([]Session, 0, len(all))
	for _, session := range all {
		if session.Live(now) {
			live = append(live, session)
		}
	}
	return live, nil
}
