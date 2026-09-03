package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/totp"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/secretbox"
)

/*
 * The second factor (CP17, D-45).
 *
 * TOTP, because it works with no network and no SMS bill, and because every phone in the
 * clinic can run an authenticator app. Two places it is demanded:
 *
 *   - at sign-in, for anyone who has enrolled — and enrolment is mandatory for the roles
 *     D-45 names: physician, administrator, pharmacist, researcher;
 *   - at a step-up, when a signed-in person is about to do something the blueprint calls
 *     privileged: sign a prescription, change a role, export research data, override a
 *     safety rule, disable this very factor.
 *
 * What the seed is protected by: it is sealed (secretbox) before it reaches the database and
 * opened only for the milliseconds a verification takes. What a code is protected by: a
 * replay guard, so a code somebody read over a shoulder cannot be typed in a second time
 * inside its window. What recovery codes are for: the phone in the river. Ten, shown once,
 * each good once.
 */

// TOTPRequiredRoles are the roles for which enrolment is mandatory (D-45). A person holding
// any of these who has not enrolled can sign in, but the interface takes them to enrolment
// before anything else, and no step-up — hence no privileged action — is possible until it
// is done.
var TOTPRequiredRoles = map[RoleCode]bool{
	RolePhysician:  true,
	RoleAdmin:      true,
	RolePharmacist: true,
	RoleResearcher: true,
}

// TOTPRequired reports whether any of the roles mandates a second factor.
func TOTPRequired(roles []Role) bool {
	for _, r := range roles {
		if TOTPRequiredRoles[r.Code] {
			return true
		}
	}
	return false
}

// Step-up purposes. A token is minted for one of these and is good for nothing else.
const (
	PurposeDisableSecondFactor = "second_factor.disable"
	PurposeRecoveryCodes       = "second_factor.recovery_codes"
	PurposeSignPrescription    = "prescription.sign"
	PurposeChangeRole          = "rbac.change"
	PurposeResearchExport      = "research.export"
	PurposeOverride            = "clinical.override"
	// CP21: the administrator console. Managing an account (status, roles, sessions) and
	// resetting a credential (password, authenticator) are separate purposes, so a token
	// minted to suspend somebody cannot be spent resetting their password.
	PurposeManageUsers     = "user.manage"
	PurposeResetCredential = "credential.reset"
	// PurposeBreakGlass opens the emergency door (CP22). audit.PurposeBreakGlass is the
	// same string; the contract test keeps them equal.
	PurposeBreakGlass = "break_glass"
)

// knownPurposes is the closed list. A purpose nobody declared is a purpose nobody reviewed.
var knownPurposes = map[string]bool{
	PurposeDisableSecondFactor: true, PurposeRecoveryCodes: true,
	PurposeSignPrescription: true, PurposeChangeRole: true,
	PurposeResearchExport: true, PurposeOverride: true,
	PurposeManageUsers: true, PurposeResetCredential: true, PurposeBreakGlass: true,
}

// KnownPurpose reports whether a step-up purpose is one of the declared ones.
func KnownPurpose(p string) bool { return knownPurposes[p] }

const (
	// Issuer is what the authenticator app shows beside the account.
	Issuer = "DTHCMS"
	// DriftWindow is how many thirty-second steps either side of now a code may be for.
	// One: a phone whose clock is a minute out still works; one that is two minutes out
	// is a phone whose clock needs fixing, and saying so is kinder than silently allowing it.
	DriftWindow = 1
	// RecoveryCodeCount is how many codes an enrolment gets.
	RecoveryCodeCount = 10
	// recoveryCodeBytes: ten random bytes, eighty bits, sixteen base32 characters.
	recoveryCodeBytes = 10
	// ChallengeLifetime is how long a person has to type the code after the password.
	ChallengeLifetime = 5 * time.Minute
	// ChallengeMaxFailures kills a challenge after this many wrong codes. The password
	// behind it was right; five wrong codes is somebody without the phone.
	ChallengeMaxFailures = 5
	// StepUpLifetime is how long a step-up token stays presentable. Short: it is minted
	// for one action the person is in the middle of.
	StepUpLifetime = 5 * time.Minute
)

// Errors the service tells apart. The HTTP layer collapses most of them to one 401, as with
// passwords; the distinctions are for the log and for the one screen that needs them.
var (
	ErrAlreadyEnrolled    = errors.New("a second factor is already enrolled and confirmed")
	ErrNotEnrolled        = errors.New("no second factor is enrolled")
	ErrEnrolmentPending   = errors.New("the enrolment has not been confirmed")
	ErrBadCode            = errors.New("the code is not right")
	ErrCodeReplayed       = errors.New("that code has already been used")
	ErrChallengeInvalid   = errors.New("the challenge is unknown, expired or spent")
	ErrChallengeExhausted = errors.New("the challenge has had too many wrong codes")
	ErrUnknownPurpose     = errors.New("that is not a declared step-up purpose")
	ErrStepUpInvalid      = errors.New("the step-up token is unknown, expired, spent or for something else")
)

// TotpEnrolment is a user's row in core.user_totp.
type TotpEnrolment struct {
	UserID       uuid.UUID
	FacilityID   uuid.UUID
	SecretSealed []byte
	KeyID        string
	ConfirmedAt  *time.Time
	LastUsedStep *int64
	DisabledAt   *time.Time
}

// Active reports whether the factor protects the account right now.
func (e TotpEnrolment) Active() bool { return e.ConfirmedAt != nil && e.DisabledAt == nil }

// RecoveryCode is a row in core.recovery_code.
type RecoveryCode struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	BatchID   uuid.UUID
	UsedAt    *time.Time
	RevokedAt *time.Time
}

// Live reports whether the code may still be spent.
func (c RecoveryCode) Live() bool { return c.UsedAt == nil && c.RevokedAt == nil }

// ShortToken is a row in core.short_token: a login challenge or a step-up.
type ShortToken struct {
	ID         uuid.UUID
	FacilityID uuid.UUID
	UserID     uuid.UUID
	SessionID  *uuid.UUID
	Kind       string
	Purpose    string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	Failures   int
}

const (
	KindLoginChallenge = "login_challenge"
	KindStepUp         = "step_up"
)

// Usable reports whether the token may be presented now.
func (t ShortToken) Usable(now time.Time) bool {
	return t.ConsumedAt == nil && now.Before(t.ExpiresAt)
}

// SecurityEvent is a row in core.security_event.
type SecurityEvent struct {
	FacilityID   uuid.UUID
	UserID       *uuid.UUID
	SessionID    *uuid.UUID
	ActorID      *uuid.UUID
	Kind         string
	Outcome      string
	Detail       map[string]any
	ClientDigest []byte
	At           time.Time
}

// SecondFactorStore is what the service needs from the database.
type SecondFactorStore interface {
	TotpByUser(ctx context.Context, userID uuid.UUID) (TotpEnrolment, error)
	// BeginTotpEnrolment writes a fresh unconfirmed seed. It must not replace a confirmed,
	// undisabled one; when asked to, it returns ErrAlreadyEnrolled.
	BeginTotpEnrolment(ctx context.Context, userID, facilityID uuid.UUID, sealed []byte, keyID string) (TotpEnrolment, error)
	ConfirmTotp(ctx context.Context, userID uuid.UUID, at time.Time, step int64) error
	// RecordTotpUse advances the replay guard and re-seals. It reports false when the step
	// has already been used or passed, which is the replay refusal.
	RecordTotpUse(ctx context.Context, userID uuid.UUID, step int64, sealed []byte, keyID string) (bool, error)
	DisableTotp(ctx context.Context, userID uuid.UUID, by *uuid.UUID, reason string) error

	// ReplaceRecoveryCodes revokes every live code and inserts the new batch, atomically.
	ReplaceRecoveryCodes(ctx context.Context, userID, facilityID, batchID uuid.UUID, digests [][]byte) error
	RecoveryCodeByDigest(ctx context.Context, digest []byte) (RecoveryCode, error)
	UseRecoveryCode(ctx context.Context, id uuid.UUID, clientDigest []byte) (bool, error)
	CountLiveRecoveryCodes(ctx context.Context, userID uuid.UUID) (int, error)

	CreateShortToken(ctx context.Context, t ShortToken, digest, clientDigest []byte) (ShortToken, error)
	ShortTokenByDigest(ctx context.Context, digest []byte) (ShortToken, error)
	ConsumeShortToken(ctx context.Context, id uuid.UUID, at time.Time) (bool, error)
	RecordShortTokenFailure(ctx context.Context, id uuid.UUID) (int, error)

	RecordSecurityEvent(ctx context.Context, e SecurityEvent) error
}

// UserReader is the slice of the identity store the second factor needs: who a user is,
// and which roles they hold.
type UserReader interface {
	UserByID(ctx context.Context, id uuid.UUID) (User, error)
	RolesForUser(ctx context.Context, userID uuid.UUID) ([]Role, error)
}

// SecondFactor is the service.
type SecondFactor struct {
	store SecondFactorStore
	users UserReader
	ring  *secretbox.Ring
	clock clock.Clock
	// audit, when set, receives every step-up that passed: the trail's record of who
	// confirmed what with their authenticator (CP22).
	audit AuditRecorder
}

// WithAudit connects the security audit log. Returns the service, for chaining.
func (s *SecondFactor) WithAudit(recorder AuditRecorder) *SecondFactor {
	s.audit = recorder
	return s
}

// SecondFactorConfig assembles it.
type SecondFactorConfig struct {
	Store SecondFactorStore
	Users UserReader
	Ring  *secretbox.Ring
	Clock clock.Clock
}

func NewSecondFactor(cfg SecondFactorConfig) *SecondFactor {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	return &SecondFactor{store: cfg.Store, users: cfg.Users, ring: cfg.Ring, clock: cfg.Clock}
}

// --- status ---

// SecondFactorStatus is what a screen needs to know to decide what to show.
type SecondFactorStatus struct {
	// Required: one of the person's roles mandates a second factor.
	Required bool
	// Enrolled: a seed is confirmed and not disabled.
	Enrolled bool
	// Pending: a seed exists but the person has not yet proved they can produce a code.
	Pending bool
	// RecoveryCodesLeft: live codes remaining. Zero with Enrolled true is a warning.
	RecoveryCodesLeft int
	ConfirmedAt       *time.Time
}

// Status reports the second-factor state of a user.
func (s *SecondFactor) Status(ctx context.Context, userID uuid.UUID) (SecondFactorStatus, error) {
	roles, err := s.users.RolesForUser(ctx, userID)
	if err != nil {
		return SecondFactorStatus{}, err
	}
	status := SecondFactorStatus{Required: TOTPRequired(roles)}

	enrolment, err := s.store.TotpByUser(ctx, userID)
	switch {
	case errors.Is(err, ErrNotFound):
		return status, nil
	case err != nil:
		return SecondFactorStatus{}, err
	}
	status.Enrolled = enrolment.Active()
	status.Pending = enrolment.ConfirmedAt == nil && enrolment.DisabledAt == nil
	status.ConfirmedAt = enrolment.ConfirmedAt
	if status.Enrolled {
		left, err := s.store.CountLiveRecoveryCodes(ctx, userID)
		if err != nil {
			return SecondFactorStatus{}, err
		}
		status.RecoveryCodesLeft = left
	}
	return status, nil
}

// Active reports whether the user's second factor protects sign-in right now.
func (s *SecondFactor) Active(ctx context.Context, userID uuid.UUID) (bool, error) {
	enrolment, err := s.store.TotpByUser(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enrolment.Active(), nil
}

// --- enrolment ---

// Enrolment is what the person needs to put the seed into their app: the URI for a QR
// code, and the seed itself for typing in by hand. Both are the secret; neither is stored
// or logged by anything on this side of the response.
type Enrolment struct {
	Secret string
	URI    string
}

// BeginEnrolment mints a seed and stores it sealed and unconfirmed.
//
// Restarting a pending enrolment is allowed — the person scanned nothing, or scanned and
// closed the app — and simply replaces the seed. Replacing a confirmed one is not: that is
// a disable followed by a fresh enrolment, each with its own event, so the trail shows
// that a working factor was taken down before another was put up.
func (s *SecondFactor) BeginEnrolment(ctx context.Context, user User, client []byte) (Enrolment, error) {
	secret, err := totp.NewSecret()
	if err != nil {
		return Enrolment{}, err
	}
	sealed, keyID, err := s.ring.Seal([]byte(secret), user.ID[:])
	if err != nil {
		return Enrolment{}, err
	}
	if _, err := s.store.BeginTotpEnrolment(ctx, user.ID, user.FacilityID, sealed, keyID); err != nil {
		return Enrolment{}, err
	}
	s.event(ctx, user, nil, "totp_enrolment_started", "ok", nil, client)
	return Enrolment{Secret: secret, URI: totp.ProvisioningURI(Issuer, user.Code, secret)}, nil
}

// ConfirmEnrolment proves the app has the seed, activates the factor, and issues the
// recovery codes — which are returned exactly once, here, and never again.
func (s *SecondFactor) ConfirmEnrolment(ctx context.Context, user User, code string, client []byte) ([]string, error) {
	enrolment, err := s.store.TotpByUser(ctx, user.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotEnrolled
	}
	if err != nil {
		return nil, err
	}
	if enrolment.Active() {
		return nil, ErrAlreadyEnrolled
	}

	step, ok, err := s.verifyAgainst(ctx, enrolment, code)
	if err != nil {
		return nil, err
	}
	if !ok {
		s.event(ctx, user, nil, "totp_enrolment_confirmed", "refused", nil, client)
		return nil, ErrBadCode
	}

	now := s.clock.Now()
	if err := s.store.ConfirmTotp(ctx, user.ID, now, step); err != nil {
		return nil, err
	}
	codes, err := s.issueRecoveryCodes(ctx, user)
	if err != nil {
		return nil, err
	}
	s.event(ctx, user, nil, "totp_enrolment_confirmed", "ok", nil, client)
	return codes, nil
}

// RegenerateRecoveryCodes replaces the sheet. The old codes stop working at once.
func (s *SecondFactor) RegenerateRecoveryCodes(ctx context.Context, user User, client []byte) ([]string, error) {
	active, err := s.Active(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrNotEnrolled
	}
	codes, err := s.issueRecoveryCodes(ctx, user)
	if err != nil {
		return nil, err
	}
	s.event(ctx, user, nil, "recovery_codes_regenerated", "ok", nil, client)
	return codes, nil
}

func (s *SecondFactor) issueRecoveryCodes(ctx context.Context, user User) ([]string, error) {
	codes := make([]string, 0, RecoveryCodeCount)
	digests := make([][]byte, 0, RecoveryCodeCount)
	for i := 0; i < RecoveryCodeCount; i++ {
		raw := make([]byte, recoveryCodeBytes)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("reading randomness for a recovery code: %w", err)
		}
		code := totp.Encoding.EncodeToString(raw)
		codes = append(codes, formatRecoveryCode(code))
		digests = append(digests, DigestOfRaw([]byte(normaliseRecoveryCode(code))))
	}
	if err := s.store.ReplaceRecoveryCodes(ctx, user.ID, user.FacilityID, uuid.New(), digests); err != nil {
		return nil, err
	}
	return codes, nil
}

// formatRecoveryCode groups sixteen characters as XXXX-XXXX-XXXX-XXXX for reading aloud.
func formatRecoveryCode(code string) string {
	var b strings.Builder
	for i, r := range code {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// normaliseRecoveryCode undoes what people do to a code: lower-case it, add spaces, keep
// the dashes, drop the dashes. The digest is of the bare upper-case characters.
func normaliseRecoveryCode(code string) string {
	code = strings.ToUpper(code)
	code = strings.NewReplacer("-", "", " ", "", "‑", "").Replace(code)
	return strings.TrimSpace(code)
}

// Disable turns the factor off. The caller has already proved a step-up; that is the whole
// point of routing this through one — the seed is not the thing that authorises removing
// the seed.
func (s *SecondFactor) Disable(ctx context.Context, user User, by *uuid.UUID, reason string, client []byte) error {
	if err := s.store.DisableTotp(ctx, user.ID, by, reason); err != nil {
		return err
	}
	// The codes go with it; a recovery code for a factor that no longer exists is a
	// credential with no purpose, which is a credential to remove.
	if err := s.store.ReplaceRecoveryCodes(ctx, user.ID, user.FacilityID, uuid.New(), nil); err != nil {
		return err
	}
	s.event(ctx, user, nil, "totp_disabled", "ok", map[string]any{"reason": reason}, client)
	return nil
}

// --- verifying a code ---

// Verify checks a TOTP code against the user's active seed, applying the replay guard.
//
// A code that is right but for a step at or before the last accepted one is a replay and
// is refused as such. The re-seal happens here too: if the ring's current key is newer than
// the one the seed was stored under, the seed is written back under the new key on this
// successful verification — rotation as a side effect of use, needing no batch job.
func (s *SecondFactor) Verify(ctx context.Context, userID uuid.UUID, code string) error {
	enrolment, err := s.store.TotpByUser(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return ErrNotEnrolled
	}
	if err != nil {
		return err
	}
	if !enrolment.Active() {
		if enrolment.ConfirmedAt == nil {
			return ErrEnrolmentPending
		}
		return ErrNotEnrolled
	}

	step, ok, err := s.verifyAgainst(ctx, enrolment, code)
	if err != nil {
		return err
	}
	if !ok {
		return ErrBadCode
	}

	sealed, keyID := enrolment.SecretSealed, enrolment.KeyID
	if s.ring.NeedsResealing(keyID) {
		secret, err := s.ring.Open(enrolment.SecretSealed, enrolment.KeyID, userID[:])
		if err != nil {
			return err
		}
		if sealed, keyID, err = s.ring.Seal(secret, userID[:]); err != nil {
			return err
		}
	}
	advanced, err := s.store.RecordTotpUse(ctx, userID, step, sealed, keyID)
	if err != nil {
		return err
	}
	if !advanced {
		return ErrCodeReplayed
	}
	return nil
}

// verifyAgainst opens the seed and checks the code. It does not touch the replay guard.
func (s *SecondFactor) verifyAgainst(ctx context.Context, e TotpEnrolment, code string) (int64, bool, error) {
	_ = ctx
	secret, err := s.ring.Open(e.SecretSealed, e.KeyID, e.UserID[:])
	if err != nil {
		return 0, false, fmt.Errorf("opening the TOTP seed: %w", err)
	}
	step, ok := totp.Verify(string(secret), code, s.clock.Now(), DriftWindow)
	return step, ok, nil
}

// UseRecoveryCode spends a recovery code. Once.
func (s *SecondFactor) UseRecoveryCode(ctx context.Context, user User, code string, client []byte) error {
	digest := DigestOfRaw([]byte(normaliseRecoveryCode(code)))
	found, err := s.store.RecoveryCodeByDigest(ctx, digest)
	if errors.Is(err, ErrNotFound) || (err == nil && (found.UserID != user.ID || !found.Live())) {
		s.event(ctx, user, nil, "recovery_code_used", "refused", nil, client)
		return ErrBadCode
	}
	if err != nil {
		return err
	}
	spent, err := s.store.UseRecoveryCode(ctx, found.ID, client)
	if err != nil {
		return err
	}
	if !spent {
		// Lost a race with another presentation of the same code. Once means once.
		s.event(ctx, user, nil, "recovery_code_used", "refused", nil, client)
		return ErrBadCode
	}
	left, _ := s.store.CountLiveRecoveryCodes(ctx, user.ID)
	s.event(ctx, user, nil, "recovery_code_used", "ok", map[string]any{"remaining": left}, client)
	return nil
}

// Proof is what a person offers as their second factor: a code from the app, or a
// recovery code. Exactly one is set.
type Proof struct {
	Code         string
	RecoveryCode string
}

// prove checks whichever proof was offered.
func (s *SecondFactor) prove(ctx context.Context, user User, proof Proof, client []byte) error {
	switch {
	case proof.RecoveryCode != "":
		return s.UseRecoveryCode(ctx, user, proof.RecoveryCode, client)
	case proof.Code != "":
		return s.Verify(ctx, user.ID, proof.Code)
	}
	return ErrBadCode
}

// --- the login challenge ---

// Challenge is what a sign-in returns instead of a session when a code is owed.
type Challenge struct {
	Token     string
	ExpiresAt time.Time
}

// SecondFactorRequired is the error Login returns when the password was right and a code
// is now owed. It carries the challenge. A caller that does not handle it treats it as a
// refused login, which is the safe default.
type SecondFactorRequired struct{ Challenge Challenge }

func (e *SecondFactorRequired) Error() string { return "a second factor is required" }

// IssueChallenge records that the password was right and hands back a token the code must
// arrive with. Nothing about the account is derivable from the token.
func (s *SecondFactor) IssueChallenge(ctx context.Context, user User, client []byte) (Challenge, error) {
	token, err := NewToken()
	if err != nil {
		return Challenge{}, err
	}
	now := s.clock.Now()
	created, err := s.store.CreateShortToken(ctx, ShortToken{
		FacilityID: user.FacilityID, UserID: user.ID, Kind: KindLoginChallenge,
		IssuedAt: now, ExpiresAt: now.Add(ChallengeLifetime),
	}, token.Digest, client)
	if err != nil {
		return Challenge{}, err
	}
	return Challenge{Token: token.Plaintext, ExpiresAt: created.ExpiresAt}, nil
}

// CompleteChallenge checks the proof against the challenge and, if it holds, returns the
// user the session should be issued to. The challenge is consumed either way it ends:
// on success, and on the failure that exhausts it.
//
// When the challenge was valid but the proof was not, the user is returned *with* the
// error, so the caller can record the failed attempt against the right account — the
// throttle counts wrong codes as it counts wrong passwords.
func (s *SecondFactor) CompleteChallenge(ctx context.Context, challenge string, proof Proof, client []byte) (User, error) {
	token, err := s.store.ShortTokenByDigest(ctx, DigestOf(challenge))
	if errors.Is(err, ErrNotFound) {
		return User{}, ErrChallengeInvalid
	}
	if err != nil {
		return User{}, err
	}
	now := s.clock.Now()
	if token.Kind != KindLoginChallenge || !token.Usable(now) || token.Failures >= ChallengeMaxFailures {
		return User{}, ErrChallengeInvalid
	}

	user, err := s.users.UserByID(ctx, token.UserID)
	if err != nil {
		return User{}, err
	}

	if err := s.prove(ctx, user, proof, client); err != nil {
		failures, ferr := s.store.RecordShortTokenFailure(ctx, token.ID)
		if ferr != nil {
			return User{}, ferr
		}
		s.event(ctx, user, nil, "totp_challenge_failed", "refused",
			map[string]any{"failures": failures}, client)
		if failures >= ChallengeMaxFailures {
			_, _ = s.store.ConsumeShortToken(ctx, token.ID, now)
			return user, ErrChallengeExhausted
		}
		return user, ErrBadCode
	}

	consumed, err := s.store.ConsumeShortToken(ctx, token.ID, now)
	if err != nil {
		return User{}, err
	}
	if !consumed {
		// Two completions raced; only the first gets a session.
		return User{}, ErrChallengeInvalid
	}
	s.event(ctx, user, nil, "totp_challenge_passed", "ok", nil, client)
	return user, nil
}

// --- step-up ---

// StepUp is a minted step-up token.
type StepUp struct {
	Token     string
	ExpiresAt time.Time
	Purpose   string
}

// IssueStepUp checks the proof and, if it holds, mints a token for one purpose, bound to
// the session that asked.
func (s *SecondFactor) IssueStepUp(ctx context.Context, user User, session Session, purpose string, proof Proof, client []byte) (StepUp, error) {
	if !KnownPurpose(purpose) {
		return StepUp{}, ErrUnknownPurpose
	}
	detail := map[string]any{"purpose": purpose}
	if err := s.prove(ctx, user, proof, client); err != nil {
		s.event(ctx, user, &session.ID, "step_up_failed", "refused", detail, client)
		return StepUp{}, err
	}

	token, err := NewToken()
	if err != nil {
		return StepUp{}, err
	}
	now := s.clock.Now()
	created, err := s.store.CreateShortToken(ctx, ShortToken{
		FacilityID: user.FacilityID, UserID: user.ID, SessionID: &session.ID,
		Kind: KindStepUp, Purpose: purpose,
		IssuedAt: now, ExpiresAt: now.Add(StepUpLifetime),
	}, token.Digest, client)
	if err != nil {
		return StepUp{}, err
	}
	s.event(ctx, user, &session.ID, "step_up_passed", "ok", detail, client)
	if s.audit != nil {
		_ = s.audit.RecordAudit(ctx, AuditEntry{
			Kind: "session.step_up", ActorID: user.ID, ActorCode: user.Code, FacilityID: user.FacilityID,
			After: map[string]any{"purpose": purpose, "session_id": session.ID.String()}, ClientDigest: client, At: now,
		})
	}
	return StepUp{Token: token.Plaintext, ExpiresAt: created.ExpiresAt, Purpose: purpose}, nil
}

// ConsumeStepUp verifies a presented token against the session presenting it and the
// purpose of the endpoint it is presented to, and spends it.
//
// All four have to line up — known, unexpired, unconsumed, this session, this purpose —
// and the token is consumed on success, so the privileged action it authorised is the only
// one it will ever authorise.
func (s *SecondFactor) ConsumeStepUp(ctx context.Context, plaintext string, sessionID uuid.UUID, purpose string) error {
	token, err := s.store.ShortTokenByDigest(ctx, DigestOf(plaintext))
	if errors.Is(err, ErrNotFound) {
		return ErrStepUpInvalid
	}
	if err != nil {
		return err
	}
	now := s.clock.Now()
	if token.Kind != KindStepUp || !token.Usable(now) ||
		token.SessionID == nil || *token.SessionID != sessionID || token.Purpose != purpose {
		return ErrStepUpInvalid
	}
	consumed, err := s.store.ConsumeShortToken(ctx, token.ID, now)
	if err != nil {
		return err
	}
	if !consumed {
		return ErrStepUpInvalid
	}
	_ = s.store.RecordSecurityEvent(ctx, SecurityEvent{
		FacilityID: token.FacilityID, UserID: &token.UserID, SessionID: &sessionID,
		Kind: "step_up_used", Outcome: "ok", Detail: map[string]any{"purpose": purpose}, At: now,
	})
	return nil
}

// --- events ---

// event records what happened. Not returning the error is deliberate: a factor that was
// correctly enrolled must not be reported as failed because the audit write did not land,
// and the store logs its own failures.
func (s *SecondFactor) event(ctx context.Context, user User, sessionID *uuid.UUID, kind, outcome string, detail map[string]any, client []byte) {
	if detail == nil {
		detail = map[string]any{}
	}
	_ = s.store.RecordSecurityEvent(ctx, SecurityEvent{
		FacilityID: user.FacilityID, UserID: &user.ID, SessionID: sessionID,
		Kind: kind, Outcome: outcome, Detail: detail, ClientDigest: client, At: s.clock.Now(),
	})
}
