package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/totp"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/secretbox"
)

// The second factor against an in-memory store. What these prove is the service's rules:
// what a code buys, what a replay is, when a challenge dies, what a step-up token is good
// for. The database's part — that nothing is deleted and nothing is stored in the clear — is
// proved in secondfactor_db_test.go against the real schema.

func ring(t *testing.T, ids ...string) *secretbox.Ring {
	t.Helper()
	keys := make([]secretbox.Key, 0, len(ids))
	for _, id := range ids {
		// Material derived from the id, not the position, so "k1" is the same key in every
		// ring a test builds — which is what makes a rotation test mean anything.
		keys = append(keys, secretbox.Key{ID: id, Material: bytes.Repeat([]byte{id[len(id)-1]}, secretbox.KeySize)})
	}
	r, err := secretbox.NewRing(keys...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func newSecondFactor(t *testing.T, r *secretbox.Ring) (*SecondFactor, *memSecondFactor) {
	t.Helper()
	store := newMemSecondFactor()
	svc := NewSecondFactor(SecondFactorConfig{Store: store, Users: store, Ring: r, Clock: store.clock})
	return svc, store
}

// enrol runs the happy path and returns the seed, so a test can compute codes.
func enrol(t *testing.T, svc *SecondFactor, store *memSecondFactor, user User) (secret string, recovery []string) {
	t.Helper()
	e, err := svc.BeginEnrolment(context.Background(), user, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.Code(e.Secret, totp.Step(store.clock.Now()))
	codes, err := svc.ConfirmEnrolment(context.Background(), user, code, nil)
	if err != nil {
		t.Fatal(err)
	}
	return e.Secret, codes
}

func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := totp.Code(secret, totp.Step(at))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// --- enrolment ---

func TestEnrolmentIsPendingUntilACodeProvesTheApp(t *testing.T) {
	svc, store := newSecondFactor(t, ring(t, "k1"))
	user := store.addUser("E001", RolePhysician)

	e, err := svc.BeginEnrolment(context.Background(), user, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.URI, "otpauth://totp/DTHCMS:E001?") || !strings.Contains(e.URI, "secret="+e.Secret) {
		t.Errorf("provisioning URI = %s", e.URI)
	}

	status, _ := svc.Status(context.Background(), user.ID)
	if !status.Required || status.Enrolled || !status.Pending {
		t.Errorf("after begin: %+v, want required, pending, not enrolled", status)
	}
	// Pending protects nothing.
	if active, _ := svc.Active(context.Background(), user.ID); active {
		t.Error("a pending enrolment counts as active")
	}

	// The seed is not in the store in the clear.
	if bytes.Contains(store.totp[user.ID].SecretSealed, []byte(e.Secret)) {
		t.Fatal("the seed is stored in plaintext")
	}

	if _, err := svc.ConfirmEnrolment(context.Background(), user, "000000", nil); !errors.Is(err, ErrBadCode) {
		t.Errorf("wrong code: %v", err)
	}
	codes, err := svc.ConfirmEnrolment(context.Background(), user, codeAt(t, e.Secret, store.clock.Now()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Errorf("%d recovery codes, want %d", len(codes), RecoveryCodeCount)
	}
	for _, c := range codes {
		if len(c) != 19 || strings.Count(c, "-") != 3 {
			t.Errorf("recovery code %q is not XXXX-XXXX-XXXX-XXXX", c)
		}
	}

	status, _ = svc.Status(context.Background(), user.ID)
	if !status.Enrolled || status.Pending || status.RecoveryCodesLeft != RecoveryCodeCount {
		t.Errorf("after confirm: %+v", status)
	}
	if kinds := store.eventKinds(); !hasKind(kinds, "totp_enrolment_started") || !hasKind(kinds, "totp_enrolment_confirmed") {
		t.Errorf("events = %v", kinds)
	}
}

func TestAPendingEnrolmentCanBeRestartedAndAConfirmedOneCannot(t *testing.T) {
	svc, store := newSecondFactor(t, ring(t, "k1"))
	user := store.addUser("E001", RoleAdmin)

	first, _ := svc.BeginEnrolment(context.Background(), user, nil)
	second, _ := svc.BeginEnrolment(context.Background(), user, nil)
	if first.Secret == second.Secret {
		t.Fatal("restarting did not replace the seed")
	}
	// The first seed is dead: its code confirms nothing.
	if _, err := svc.ConfirmEnrolment(context.Background(), user, codeAt(t, first.Secret, store.clock.Now()), nil); !errors.Is(err, ErrBadCode) {
		t.Errorf("the replaced seed still confirms: %v", err)
	}
	if _, err := svc.ConfirmEnrolment(context.Background(), user, codeAt(t, second.Secret, store.clock.Now()), nil); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.BeginEnrolment(context.Background(), user, nil); !errors.Is(err, ErrAlreadyEnrolled) {
		t.Errorf("a confirmed factor was replaced by a new enrolment: %v", err)
	}
}

// --- verifying ---

func TestACodeWorksOnceAndWithinTheDriftWindow(t *testing.T) {
	svc, store := newSecondFactor(t, ring(t, "k1"))
	user := store.addUser("E001", RolePhysician)
	secret, _ := enrol(t, svc, store, user)

	// Enrolment consumed the current step. Move on one.
	store.clock.Advance(totp.Period)
	now := store.clock.Now()

	if err := svc.Verify(context.Background(), user.ID, codeAt(t, secret, now)); err != nil {
		t.Fatalf("a current code was refused: %v", err)
	}
	// The same code again is a replay, however correct it still is.
	if err := svc.Verify(context.Background(), user.ID, codeAt(t, secret, now)); !errors.Is(err, ErrCodeReplayed) {
		t.Errorf("a replayed code: %v, want ErrCodeReplayed", err)
	}
	// A code for the step before is also behind the guard now.
	if err := svc.Verify(context.Background(), user.ID, codeAt(t, secret, now.Add(-totp.Period))); !errors.Is(err, ErrCodeReplayed) {
		t.Errorf("a code from the previous step: %v, want ErrCodeReplayed", err)
	}
	// The next step's code — a phone thirty seconds ahead — is fine.
	if err := svc.Verify(context.Background(), user.ID, codeAt(t, secret, now.Add(totp.Period))); err != nil {
		t.Errorf("a code one step ahead: %v", err)
	}
	// Two ahead is a phone whose clock is wrong.
	store.clock.Advance(totp.Period)
	if err := svc.Verify(context.Background(), user.ID, codeAt(t, secret, store.clock.Now().Add(2*totp.Period))); !errors.Is(err, ErrBadCode) {
		t.Errorf("a code two steps ahead: %v, want ErrBadCode", err)
	}
	if err := svc.Verify(context.Background(), user.ID, "123456"); !errors.Is(err, ErrBadCode) {
		t.Errorf("a wrong code: %v", err)
	}
}

func TestASuccessfulVerificationResealsUnderTheCurrentKey(t *testing.T) {
	// Yesterday's key sealed the seed. Today's ring has a new current key. The next good
	// code moves the seed onto it, and no batch job had to run.
	svc, store := newSecondFactor(t, ring(t, "k1"))
	user := store.addUser("E001", RolePhysician)
	secret, _ := enrol(t, svc, store, user)
	if store.totp[user.ID].KeyID != "k1" {
		t.Fatalf("sealed under %q", store.totp[user.ID].KeyID)
	}

	rotated := NewSecondFactor(SecondFactorConfig{Store: store, Users: store, Ring: ring(t, "k2", "k1"), Clock: store.clock})
	store.clock.Advance(totp.Period)
	if err := rotated.Verify(context.Background(), user.ID, codeAt(t, secret, store.clock.Now())); err != nil {
		t.Fatal(err)
	}
	if store.totp[user.ID].KeyID != "k2" {
		t.Errorf("after verification the seed is under %q, want k2", store.totp[user.ID].KeyID)
	}
	// And still works, under the new key, on the next step.
	store.clock.Advance(totp.Period)
	if err := rotated.Verify(context.Background(), user.ID, codeAt(t, secret, store.clock.Now())); err != nil {
		t.Errorf("after resealing: %v", err)
	}
}

func TestVerifyDistinguishesNotEnrolledFromPending(t *testing.T) {
	svc, store := newSecondFactor(t, ring(t, "k1"))
	user := store.addUser("E001", RolePhysician)
	if err := svc.Verify(context.Background(), user.ID, "123456"); !errors.Is(err, ErrNotEnrolled) {
		t.Errorf("nobody enrolled: %v", err)
	}
	_, _ = svc.BeginEnrolment(context.Background(), user, nil)
	if err := svc.Verify(context.Background(), user.ID, "123456"); !errors.Is(err, ErrEnrolmentPending) {
		t.Errorf("pending: %v", err)
	}
}

// --- recovery codes ---

func TestARecoveryCodeWorksExactlyOnce(t *testing.T) {
	svc, store := newSecondFactor(t, ring(t, "k1"))
	user := store.addUser("E001", RolePhysician)
	_, codes := enrol(t, svc, store, user)

	// Nothing in the store is a code.
	for _, c := range codes {
		for _, row := range store.recovery {
			if bytes.Contains(row.digest, []byte(normaliseRecoveryCode(c))) {
				t.Fatal("a recovery code is stored in plaintext")
			}
		}
	}

	// People lower-case them, drop the dashes, add spaces. All of that is forgiven.
	sloppy := strings.ToLower(strings.ReplaceAll(codes[0], "-", " "))
	if err := svc.UseRecoveryCode(context.Background(), user, sloppy, nil); err != nil {
		t.Fatalf("a sloppily typed code was refused: %v", err)
	}
	if err := svc.UseRecoveryCode(context.Background(), user, codes[0], nil); !errors.Is(err, ErrBadCode) {
		t.Errorf("a spent code worked again: %v", err)
	}
	status, _ := svc.Status(context.Background(), user.ID)
	if status.RecoveryCodesLeft != RecoveryCodeCount-1 {
		t.Errorf("%d codes left, want %d", status.RecoveryCodesLeft, RecoveryCodeCount-1)
	}

	// Somebody else's code is not this person's.
	other := store.addUser("E002", RolePhysician)
	_, otherCodes := enrol(t, svc, store, other)
	if err := svc.UseRecoveryCode(context.Background(), user, otherCodes[0], nil); !errors.Is(err, ErrBadCode) {
		t.Errorf("another user's recovery code was accepted: %v", err)
	}
}

func TestRegeneratingRecoveryCodesRevokesTheOldSheet(t *testing.T) {
	svc, store := newSecondFactor(t, ring(t, "k1"))
	user := store.addUser("E001", RolePhysician)
	_, old := enrol(t, svc, store, user)

	fresh, err := svc.RegenerateRecoveryCodes(context.Background(), user, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UseRecoveryCode(context.Background(), user, old[3], nil); !errors.Is(err, ErrBadCode) {
		t.Errorf("an old code still works: %v", err)
	}
	if err := svc.UseRecoveryCode(context.Background(), user, fresh[3], nil); err != nil {
		t.Errorf("a fresh code does not: %v", err)
	}
	status, _ := svc.Status(context.Background(), user.ID)
	if status.RecoveryCodesLeft != RecoveryCodeCount-1 {
		t.Errorf("%d left", status.RecoveryCodesLeft)
	}
}

// --- the login challenge ---

func TestAChallengeIsCompletedByACodeAndSpentByIt(t *testing.T) {
	svc, store := newSecondFactor(t, ring(t, "k1"))
	user := store.addUser("E001", RolePhysician)
	secret, _ := enrol(t, svc, store, user)
	store.clock.Advance(totp.Period)

	ch, err := svc.IssueChallenge(context.Background(), user, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ch.ExpiresAt.Equal(store.clock.Now().Add(ChallengeLifetime)) {
		t.Errorf("expires %v", ch.ExpiresAt)
	}

	got, err := svc.CompleteChallenge(context.Background(), ch.Token, Proof{Code: codeAt(t, secret, store.clock.Now())}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != user.ID {
		t.Errorf("completed as %v", got.ID)
	}
	// Spent.
	store.clock.Advance(totp.Period)
	if _, err := svc.CompleteChallenge(context.Background(), ch.Token, Proof{Code: codeAt(t, secret, store.clock.Now())}, nil); !errors.Is(err, ErrChallengeInvalid) {
		t.Errorf("a spent challenge completed again: %v", err)
	}
}

func TestAChallengeAcceptsARecoveryCode(t *testing.T) {
	svc, store := newSecondFactor(t, ring(t, "k1"))
	user := store.addUser("E001", RolePhysician)
	_, codes := enrol(t, svc, store, user)
	ch, _ := svc.IssueChallenge(context.Background(), user, nil)
	if _, err := svc.CompleteChallenge(context.Background(), ch.Token, Proof{RecoveryCode: codes[0]}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAChallengeDiesAfterFiveWrongCodesOrFiveMinutes(t *testing.T) {
	svc, store := newSecondFactor(t, ring(t, "k1"))
	user := store.addUser("E001", RolePhysician)
	secret, _ := enrol(t, svc, store, user)
	store.clock.Advance(totp.Period)

	ch, _ := svc.IssueChallenge(context.Background(), user, nil)
	for i := 1; i < ChallengeMaxFailures; i++ {
		if _, err := svc.CompleteChallenge(context.Background(), ch.Token, Proof{Code: "000000"}, nil); !errors.Is(err, ErrBadCode) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := svc.CompleteChallenge(context.Background(), ch.Token, Proof{Code: "000000"}, nil); !errors.Is(err, ErrChallengeExhausted) {
		t.Fatalf("fifth wrong code: %v, want exhausted", err)
	}
	// The right code no longer helps; the challenge is dead.
	if _, err := svc.CompleteChallenge(context.Background(), ch.Token, Proof{Code: codeAt(t, secret, store.clock.Now())}, nil); !errors.Is(err, ErrChallengeInvalid) {
		t.Errorf("a dead challenge accepted the right code: %v", err)
	}

	// Time kills one too.
	later, _ := svc.IssueChallenge(context.Background(), user, nil)
	store.clock.Advance(ChallengeLifetime + time.Second)
	if _, err := svc.CompleteChallenge(context.Background(), later.Token, Proof{Code: codeAt(t, secret, store.clock.Now())}, nil); !errors.Is(err, ErrChallengeInvalid) {
		t.Errorf("an expired challenge completed: %v", err)
	}
	if _, err := svc.CompleteChallenge(context.Background(), "not-a-challenge", Proof{Code: "000000"}, nil); !errors.Is(err, ErrChallengeInvalid) {
		t.Errorf("an unknown challenge: %v", err)
	}
}

// --- step-up ---

func TestAStepUpTokenIsGoodForOneSessionOnePurposeOnce(t *testing.T) {
	svc, store := newSecondFactor(t, ring(t, "k1"))
	user := store.addUser("E001", RolePhysician)
	secret, _ := enrol(t, svc, store, user)
	store.clock.Advance(totp.Period)
	session := Session{ID: uuid.New(), UserID: user.ID}

	if _, err := svc.IssueStepUp(context.Background(), user, session, "make.tea", Proof{Code: codeAt(t, secret, store.clock.Now())}, nil); !errors.Is(err, ErrUnknownPurpose) {
		t.Fatalf("an undeclared purpose was accepted: %v", err)
	}
	if _, err := svc.IssueStepUp(context.Background(), user, session, PurposeSignPrescription, Proof{Code: "000000"}, nil); !errors.Is(err, ErrBadCode) {
		t.Fatalf("a wrong code minted a step-up: %v", err)
	}

	su, err := svc.IssueStepUp(context.Background(), user, session, PurposeSignPrescription, Proof{Code: codeAt(t, secret, store.clock.Now())}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Wrong purpose, wrong session: refused, and the token is still unspent.
	if err := svc.ConsumeStepUp(context.Background(), su.Token, session.ID, PurposeChangeRole); !errors.Is(err, ErrStepUpInvalid) {
		t.Errorf("used for another purpose: %v", err)
	}
	if err := svc.ConsumeStepUp(context.Background(), su.Token, uuid.New(), PurposeSignPrescription); !errors.Is(err, ErrStepUpInvalid) {
		t.Errorf("used from another session: %v", err)
	}
	// Right session, right purpose: once.
	if err := svc.ConsumeStepUp(context.Background(), su.Token, session.ID, PurposeSignPrescription); err != nil {
		t.Fatalf("a valid step-up was refused: %v", err)
	}
	if err := svc.ConsumeStepUp(context.Background(), su.Token, session.ID, PurposeSignPrescription); !errors.Is(err, ErrStepUpInvalid) {
		t.Errorf("a spent step-up worked again: %v", err)
	}

	// And time runs out.
	store.clock.Advance(totp.Period)
	fresh, _ := svc.IssueStepUp(context.Background(), user, session, PurposeSignPrescription, Proof{Code: codeAt(t, secret, store.clock.Now())}, nil)
	store.clock.Advance(StepUpLifetime + time.Second)
	if err := svc.ConsumeStepUp(context.Background(), fresh.Token, session.ID, PurposeSignPrescription); !errors.Is(err, ErrStepUpInvalid) {
		t.Errorf("an expired step-up was accepted: %v", err)
	}

	kinds := store.eventKinds()
	for _, want := range []string{"step_up_failed", "step_up_passed", "step_up_used"} {
		if !hasKind(kinds, want) {
			t.Errorf("no %s event; have %v", want, kinds)
		}
	}
}

// --- disabling ---

func TestDisablingRemovesTheFactorAndItsRecoveryCodes(t *testing.T) {
	svc, store := newSecondFactor(t, ring(t, "k1"))
	user := store.addUser("E001", RolePhysician)
	secret, codes := enrol(t, svc, store, user)

	if err := svc.Disable(context.Background(), user, &user.ID, "lost phone", nil); err != nil {
		t.Fatal(err)
	}
	status, _ := svc.Status(context.Background(), user.ID)
	if status.Enrolled || status.RecoveryCodesLeft != 0 {
		t.Errorf("after disable: %+v", status)
	}
	store.clock.Advance(totp.Period)
	if err := svc.Verify(context.Background(), user.ID, codeAt(t, secret, store.clock.Now())); !errors.Is(err, ErrNotEnrolled) {
		t.Errorf("a code still verifies after disable: %v", err)
	}
	if err := svc.UseRecoveryCode(context.Background(), user, codes[1], nil); !errors.Is(err, ErrBadCode) {
		t.Errorf("a recovery code still works after disable: %v", err)
	}
	// Re-enrolment is possible afterwards.
	if _, err := svc.BeginEnrolment(context.Background(), user, nil); err != nil {
		t.Errorf("cannot re-enrol after disable: %v", err)
	}
}

func TestWhichRolesRequireIt(t *testing.T) {
	for code, want := range map[RoleCode]bool{
		RolePhysician: true, RoleAdmin: true, RolePharmacist: true, RoleResearcher: true,
		RoleRegistration: false, RoleAnthropometry: false, RoleCrm: false,
	} {
		if got := TOTPRequired([]Role{{Code: code}}); got != want {
			t.Errorf("%s: required=%v, want %v", code, got, want)
		}
	}
	if TOTPRequired([]Role{{Code: RoleRegistration}, {Code: RoleAdmin}}) != true {
		t.Error("holding admin alongside a floor role must require it")
	}
}

func hasKind(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// --- the in-memory store ---

type memRecovery struct {
	RecoveryCode
	digest []byte
}

type memSecondFactor struct {
	facility uuid.UUID
	clock    *clock.Fixed

	users map[uuid.UUID]User
	roles map[uuid.UUID][]Role

	totp     map[uuid.UUID]*TotpEnrolment
	recovery []*memRecovery
	tokens   map[uuid.UUID]*ShortToken
	byDigest map[string]uuid.UUID
	events   []SecurityEvent
}

func newMemSecondFactor() *memSecondFactor {
	return &memSecondFactor{
		facility: uuid.New(),
		clock:    clock.NewFixed(time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)),
		users:    map[uuid.UUID]User{}, roles: map[uuid.UUID][]Role{},
		totp: map[uuid.UUID]*TotpEnrolment{}, tokens: map[uuid.UUID]*ShortToken{},
		byDigest: map[string]uuid.UUID{},
	}
}

func (m *memSecondFactor) addUser(code string, roles ...RoleCode) User {
	u := User{ID: uuid.New(), FacilityID: m.facility, Code: code, Status: StatusActive}
	m.users[u.ID] = u
	for _, r := range roles {
		m.roles[u.ID] = append(m.roles[u.ID], Role{ID: uuid.New(), Code: r})
	}
	return u
}

func (m *memSecondFactor) eventKinds() []string {
	out := make([]string, 0, len(m.events))
	for _, e := range m.events {
		out = append(out, e.Kind)
	}
	return out
}

func (m *memSecondFactor) UserByID(_ context.Context, id uuid.UUID) (User, error) {
	u, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *memSecondFactor) RolesForUser(_ context.Context, id uuid.UUID) ([]Role, error) {
	return m.roles[id], nil
}

func (m *memSecondFactor) TotpByUser(_ context.Context, id uuid.UUID) (TotpEnrolment, error) {
	e, ok := m.totp[id]
	if !ok {
		return TotpEnrolment{}, ErrNotFound
	}
	return *e, nil
}

func (m *memSecondFactor) BeginTotpEnrolment(_ context.Context, userID, facilityID uuid.UUID, sealed []byte, keyID string) (TotpEnrolment, error) {
	if existing, ok := m.totp[userID]; ok && existing.Active() {
		return TotpEnrolment{}, ErrAlreadyEnrolled
	}
	e := &TotpEnrolment{UserID: userID, FacilityID: facilityID, SecretSealed: sealed, KeyID: keyID}
	m.totp[userID] = e
	return *e, nil
}

func (m *memSecondFactor) ConfirmTotp(_ context.Context, userID uuid.UUID, at time.Time, step int64) error {
	e := m.totp[userID]
	e.ConfirmedAt = &at
	e.LastUsedStep = &step
	return nil
}

func (m *memSecondFactor) RecordTotpUse(_ context.Context, userID uuid.UUID, step int64, sealed []byte, keyID string) (bool, error) {
	e := m.totp[userID]
	if e.LastUsedStep != nil && *e.LastUsedStep >= step {
		return false, nil
	}
	e.LastUsedStep = &step
	e.SecretSealed, e.KeyID = sealed, keyID
	return true, nil
}

func (m *memSecondFactor) DisableTotp(_ context.Context, userID uuid.UUID, _ *uuid.UUID, _ string) error {
	now := m.clock.Now()
	m.totp[userID].DisabledAt = &now
	return nil
}

func (m *memSecondFactor) ReplaceRecoveryCodes(_ context.Context, userID, _, batchID uuid.UUID, digests [][]byte) error {
	now := m.clock.Now()
	for _, r := range m.recovery {
		if r.UserID == userID && r.Live() {
			r.RevokedAt = &now
		}
	}
	for _, d := range digests {
		m.recovery = append(m.recovery, &memRecovery{
			RecoveryCode: RecoveryCode{ID: uuid.New(), UserID: userID, BatchID: batchID}, digest: d,
		})
	}
	return nil
}

func (m *memSecondFactor) RecoveryCodeByDigest(_ context.Context, digest []byte) (RecoveryCode, error) {
	for _, r := range m.recovery {
		if bytes.Equal(r.digest, digest) {
			return r.RecoveryCode, nil
		}
	}
	return RecoveryCode{}, ErrNotFound
}

func (m *memSecondFactor) UseRecoveryCode(_ context.Context, id uuid.UUID, _ []byte) (bool, error) {
	for _, r := range m.recovery {
		if r.ID == id && r.Live() {
			now := m.clock.Now()
			r.UsedAt = &now
			return true, nil
		}
	}
	return false, nil
}

func (m *memSecondFactor) CountLiveRecoveryCodes(_ context.Context, userID uuid.UUID) (int, error) {
	n := 0
	for _, r := range m.recovery {
		if r.UserID == userID && r.Live() {
			n++
		}
	}
	return n, nil
}

func (m *memSecondFactor) CreateShortToken(_ context.Context, t ShortToken, digest, _ []byte) (ShortToken, error) {
	t.ID = uuid.New()
	m.tokens[t.ID] = &t
	m.byDigest[string(digest)] = t.ID
	return t, nil
}

func (m *memSecondFactor) ShortTokenByDigest(_ context.Context, digest []byte) (ShortToken, error) {
	id, ok := m.byDigest[string(digest)]
	if !ok {
		return ShortToken{}, ErrNotFound
	}
	return *m.tokens[id], nil
}

func (m *memSecondFactor) ConsumeShortToken(_ context.Context, id uuid.UUID, at time.Time) (bool, error) {
	t := m.tokens[id]
	if t.ConsumedAt != nil {
		return false, nil
	}
	t.ConsumedAt = &at
	return true, nil
}

func (m *memSecondFactor) RecordShortTokenFailure(_ context.Context, id uuid.UUID) (int, error) {
	m.tokens[id].Failures++
	return m.tokens[id].Failures, nil
}

func (m *memSecondFactor) RecordSecurityEvent(_ context.Context, e SecurityEvent) error {
	m.events = append(m.events, e)
	return nil
}
