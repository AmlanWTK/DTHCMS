package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/pwhash"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
)

// The administrator console's service (CP21): what Dr. Nahid or an administrator does to
// staff accounts without a developer.
//
// Nothing here is new authority. Every action is one the identity service, the sessions
// service or the second-factor service already knew how to do; this layer strings them
// together for a person at a console, records each one for the audit trail, and holds the
// rules that only make sense at the console — a password set in person must be strong, an
// administrator cannot reset their own factor from a session that factor protects, and so
// on. The step-up that every one of these requires is the route's (admin_http.go), not
// the service's: a service that checked step-up tokens would need to know about HTTP.

// AuditRecorder is what the console tells the audit trail (CP22). An interface, because
// auth may not import the audit module; the composition root connects the two. Nil means
// nothing is recorded — which the tests use and production must not.
type AuditRecorder interface {
	RecordAudit(ctx context.Context, entry AuditEntry) error
}

// AuditEntry is one administrative act, described without PHI: ids, codes, reasons and
// the before/after of what changed.
type AuditEntry struct {
	// Kind names the act: "user.invited", "user.status_changed", "role.granted",
	// "role.revoked", "sessions.ended", "password.set", "second_factor.reset".
	Kind         string
	ActorID      uuid.UUID
	ActorCode    string
	ActorRole    string
	FacilityID   uuid.UUID
	TargetUserID *uuid.UUID
	TargetCode   string
	Reason       string
	Before       map[string]any
	After        map[string]any
	ClientDigest []byte
	At           time.Time
}

// AdminStore is what the console reads and writes beyond the services it composes.
type AdminStore interface {
	Store
	ListUsers(ctx context.Context, facilityID uuid.UUID, status *Status) ([]User, error)
	UserByCode(ctx context.Context, facilityID uuid.UUID, code string) (User, error)
	GrantHistory(ctx context.Context, userID uuid.UUID) ([]Grant, error)
	UpdatePasswordHash(ctx context.Context, userID uuid.UUID, hash string) error
	RolesForUser(ctx context.Context, userID uuid.UUID) ([]Role, error)
	ListRoles(ctx context.Context) ([]Role, error)
}

// Admin is the service.
type Admin struct {
	store        AdminStore
	identity     *Service
	sessions     *Sessions
	secondFactor *SecondFactor
	hasher       *pwhash.Hasher
	audit        AuditRecorder
	clock        clock.Clock
}

// AdminConfig assembles it.
type AdminConfig struct {
	Store        AdminStore
	Identity     *Service
	Sessions     *Sessions
	SecondFactor *SecondFactor
	Hasher       *pwhash.Hasher
	Audit        AuditRecorder
	Clock        clock.Clock
}

func NewAdmin(cfg AdminConfig) *Admin {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	return &Admin{
		store: cfg.Store, identity: cfg.Identity, sessions: cfg.Sessions, secondFactor: cfg.SecondFactor,
		hasher: cfg.Hasher, audit: cfg.Audit, clock: cfg.Clock,
	}
}

// Errors the handlers map.
var (
	ErrUserNotFound     = errors.New("no such user")
	ErrEmployeeCodeUsed = errors.New("that employee code is already in use")
	ErrWeakPassword     = errors.New("the password does not meet the policy")
	ErrUnknownRole      = errors.New("no such role")
)

// --- reading ---

// AccountView is a user as the console shows them: the account, its live roles, the
// permissions those confer, and the second factor's state.
type AccountView struct {
	User         User
	Roles        []Role
	Permissions  PermissionSet
	SecondFactor SecondFactorStatus
	Sessions     []Session
	History      []Grant
}

// List returns every account of the facility, optionally by status.
func (a *Admin) List(ctx context.Context, actor Actor, status *Status) ([]AccountView, error) {
	if !actor.Permissions.Has(PermUserRead) {
		return nil, ErrNotPermitted
	}
	users, err := a.store.ListUsers(ctx, actor.FacilityID, status)
	if err != nil {
		return nil, err
	}
	out := make([]AccountView, 0, len(users))
	for _, u := range users {
		roles, err := a.store.RolesForUser(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		view := AccountView{User: u, Roles: roles, Permissions: NewPermissionSet()}
		for _, r := range roles {
			perms, err := a.store.PermissionsForRole(ctx, r.Code)
			if err != nil {
				return nil, err
			}
			view.Permissions.Union(NewPermissionSet(perms...))
		}
		if a.secondFactor != nil {
			if st, err := a.secondFactor.Status(ctx, u.ID); err == nil {
				view.SecondFactor = st
			}
		}
		out = append(out, view)
	}
	return out, nil
}

// Get returns one account with everything the detail screen shows.
func (a *Admin) Get(ctx context.Context, actor Actor, userID uuid.UUID) (AccountView, error) {
	if !actor.Permissions.Has(PermUserRead) {
		return AccountView{}, ErrNotPermitted
	}
	user, err := a.user(ctx, actor, userID)
	if err != nil {
		return AccountView{}, err
	}
	view := AccountView{User: user, Permissions: NewPermissionSet()}
	if view.Roles, err = a.store.RolesForUser(ctx, userID); err != nil {
		return AccountView{}, err
	}
	for _, r := range view.Roles {
		perms, err := a.store.PermissionsForRole(ctx, r.Code)
		if err != nil {
			return AccountView{}, err
		}
		view.Permissions.Union(NewPermissionSet(perms...))
	}
	if a.secondFactor != nil {
		if st, err := a.secondFactor.Status(ctx, userID); err == nil {
			view.SecondFactor = st
		}
	}
	if a.sessions != nil {
		if view.Sessions, err = a.sessions.Sessions(ctx, userID); err != nil {
			return AccountView{}, err
		}
	}
	if view.History, err = a.store.GrantHistory(ctx, userID); err != nil {
		return AccountView{}, err
	}
	return view, nil
}

// Roles returns the catalogue with each role's permissions, for the effective-permission
// preview: what granting a role would add.
func (a *Admin) Roles(ctx context.Context, actor Actor) (map[RoleCode][]string, []Role, error) {
	if !actor.Permissions.Has(PermUserRead) {
		return nil, nil, ErrNotPermitted
	}
	roles, err := a.store.ListRoles(ctx)
	if err != nil {
		return nil, nil, err
	}
	perms := make(map[RoleCode][]string, len(roles))
	for _, r := range roles {
		p, err := a.store.PermissionsForRole(ctx, r.Code)
		if err != nil {
			return nil, nil, err
		}
		perms[r.Code] = p
	}
	return perms, roles, nil
}

// --- inviting ---

// Invitation is what the console sends to create an account.
type Invitation struct {
	Code   string
	NameEN string
	NameBN string
	Phone  string
	Email  string
	Roles  []RoleCode
	// Password is set in person, at the desk. The clinic has a front desk and no e-mail
	// infrastructure; a reset link would be a second sign-in flow to secure. Empty leaves
	// the account without one: invited, not active. Self-service change of one's own
	// password is D-72, open.
	Password string
}

var employeeCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,15}$`)

// Invite creates the account, grants its roles, sets its password if one was given, and
// activates it when it has one. Each step is the existing service's; the console records
// one audit entry for the whole act.
func (a *Admin) Invite(ctx context.Context, actor Actor, inv Invitation, client []byte) (AccountView, error) {
	if !actor.Permissions.Has(PermUserInvite) {
		return AccountView{}, ErrNotPermitted
	}
	inv.Code = strings.ToUpper(strings.TrimSpace(inv.Code))
	if !employeeCodePattern.MatchString(inv.Code) {
		return AccountView{}, fmt.Errorf("employee code: %w", errValidation("2–16 characters: a letter, then letters, digits or underscores"))
	}
	if strings.TrimSpace(inv.NameEN) == "" || strings.TrimSpace(inv.NameBN) == "" {
		return AccountView{}, fmt.Errorf("name: %w", errValidation("both names are required"))
	}
	if inv.Password != "" {
		if err := CheckPasswordPolicy(inv.Password); err != nil {
			return AccountView{}, err
		}
	}
	for _, role := range inv.Roles {
		if _, err := a.store.GetRoleByCode(ctx, role); err != nil {
			return AccountView{}, fmt.Errorf("%s: %w", role, ErrUnknownRole)
		}
	}
	if _, err := a.store.UserByCode(ctx, actor.FacilityID, inv.Code); err == nil {
		return AccountView{}, ErrEmployeeCodeUsed
	}

	user, err := a.store.CreateUser(ctx, User{
		FacilityID: actor.FacilityID, Code: inv.Code,
		NameEN: strings.TrimSpace(inv.NameEN), NameBN: strings.TrimSpace(inv.NameBN),
		Phone: strings.TrimSpace(inv.Phone), Email: strings.TrimSpace(inv.Email),
	}, actor.UserID)
	if err != nil {
		return AccountView{}, err
	}
	granted := make([]string, 0, len(inv.Roles))
	for _, role := range inv.Roles {
		if _, err := a.identity.Grant(ctx, actor, user.ID, role); err != nil {
			return AccountView{}, fmt.Errorf("granting %s: %w", role, err)
		}
		granted = append(granted, string(role))
	}
	if inv.Password != "" {
		hash, err := a.hasher.Hash(inv.Password)
		if err != nil {
			return AccountView{}, err
		}
		if err := a.store.UpdatePasswordHash(ctx, user.ID, hash); err != nil {
			return AccountView{}, err
		}
		if _, err := a.identity.ChangeStatus(ctx, actor, user.ID, StatusActive, ""); err != nil {
			return AccountView{}, fmt.Errorf("activating: %w", err)
		}
	}
	a.record(ctx, actor, AuditEntry{
		Kind: "user.invited", TargetUserID: &user.ID, TargetCode: user.Code,
		After: map[string]any{"roles": granted, "password_set": inv.Password != ""}, ClientDigest: client,
	})
	return a.Get(ctx, actor, user.ID)
}

// --- status ---

// ChangeStatus moves an account through the lifecycle, with the identity service's rules.
func (a *Admin) ChangeStatus(ctx context.Context, actor Actor, userID uuid.UUID, to Status, reason string, client []byte) (AccountView, error) {
	before, err := a.user(ctx, actor, userID)
	if err != nil {
		return AccountView{}, err
	}
	after, err := a.identity.ChangeStatus(ctx, actor, userID, to, reason)
	if err != nil {
		return AccountView{}, err
	}
	// A suspended or deactivated account holds no session worth keeping.
	if to == StatusSuspended || to == StatusDeactivated {
		_, _ = a.sessions.LogoutEverywhere(ctx, userID, &actor.UserID, "account "+string(to))
	}
	a.record(ctx, actor, AuditEntry{
		Kind: "user.status_changed", TargetUserID: &userID, TargetCode: before.Code, Reason: reason,
		Before: map[string]any{"status": string(before.Status)}, After: map[string]any{"status": string(after.Status)},
		ClientDigest: client,
	})
	return a.Get(ctx, actor, userID)
}

// --- roles ---

// Grant adds a role. A role that requires a second factor is granted regardless; the
// person is taken to enrolment at their next sign-in (D-45).
func (a *Admin) Grant(ctx context.Context, actor Actor, userID uuid.UUID, role RoleCode, client []byte) (AccountView, error) {
	target, err := a.user(ctx, actor, userID)
	if err != nil {
		return AccountView{}, err
	}
	if _, err := a.identity.Grant(ctx, actor, userID, role); err != nil {
		return AccountView{}, err
	}
	a.record(ctx, actor, AuditEntry{
		Kind: "role.granted", TargetUserID: &userID, TargetCode: target.Code,
		After: map[string]any{"role": string(role)}, ClientDigest: client,
	})
	return a.Get(ctx, actor, userID)
}

// Revoke ends a grant, with a reason. Sessions stay; the engine's cache is dropped by the
// identity service, so the role stops working on the next request.
func (a *Admin) Revoke(ctx context.Context, actor Actor, userID uuid.UUID, role RoleCode, reason string, client []byte) (AccountView, error) {
	target, err := a.user(ctx, actor, userID)
	if err != nil {
		return AccountView{}, err
	}
	if _, err := a.identity.Revoke(ctx, actor, userID, role, reason); err != nil {
		return AccountView{}, err
	}
	a.record(ctx, actor, AuditEntry{
		Kind: "role.revoked", TargetUserID: &userID, TargetCode: target.Code, Reason: reason,
		Before: map[string]any{"role": string(role)}, ClientDigest: client,
	})
	return a.Get(ctx, actor, userID)
}

// --- sessions ---

// EndSessions signs a person out everywhere. Forced logout: a tablet left somewhere, a
// person who has just been told bad news.
func (a *Admin) EndSessions(ctx context.Context, actor Actor, userID uuid.UUID, reason string, client []byte) (int, error) {
	if !actor.Permissions.Has(PermUserCredentialReset) {
		return 0, ErrNotPermitted
	}
	target, err := a.user(ctx, actor, userID)
	if err != nil {
		return 0, err
	}
	if len(strings.TrimSpace(reason)) < 3 {
		return 0, ErrReasonRequired
	}
	n, err := a.sessions.LogoutEverywhere(ctx, userID, &actor.UserID, reason)
	if err != nil {
		return 0, err
	}
	a.record(ctx, actor, AuditEntry{
		Kind: "sessions.ended", TargetUserID: &userID, TargetCode: target.Code, Reason: reason,
		After: map[string]any{"sessions_ended": n}, ClientDigest: client,
	})
	return n, nil
}

// --- credentials ---

// SetPassword sets a password in person. Every session of the account is ended: whoever
// held the old password is out, and the person signs in fresh with the new one.
func (a *Admin) SetPassword(ctx context.Context, actor Actor, userID uuid.UUID, password, reason string, client []byte) error {
	if !actor.Permissions.Has(PermUserCredentialReset) {
		return ErrNotPermitted
	}
	target, err := a.user(ctx, actor, userID)
	if err != nil {
		return err
	}
	if err := CheckPasswordPolicy(password); err != nil {
		return err
	}
	if len(strings.TrimSpace(reason)) < 3 {
		return ErrReasonRequired
	}
	hash, err := a.hasher.Hash(password)
	if err != nil {
		return err
	}
	if err := a.store.UpdatePasswordHash(ctx, userID, hash); err != nil {
		return err
	}
	_, _ = a.sessions.LogoutEverywhere(ctx, userID, &actor.UserID, "password reset by an administrator")
	// An invited account with a password can now sign in.
	if target.Status == StatusInvited {
		if _, err := a.identity.ChangeStatus(ctx, actor, userID, StatusActive, ""); err != nil {
			return fmt.Errorf("activating: %w", err)
		}
	}
	a.record(ctx, actor, AuditEntry{
		Kind: "password.set", TargetUserID: &userID, TargetCode: target.Code, Reason: reason, ClientDigest: client,
	})
	return nil
}

// ResetSecondFactor disables a person's authenticator and revokes their recovery codes —
// the path for a lost phone and lost codes (CP17 §8.7). Done in person, by an
// administrator who is not the person: an administrator cannot reset their own factor from
// the session that factor protects, because that is exactly what a stolen session would do.
func (a *Admin) ResetSecondFactor(ctx context.Context, actor Actor, userID uuid.UUID, reason string, client []byte) error {
	if !actor.Permissions.Has(PermUserCredentialReset) {
		return ErrNotPermitted
	}
	if actor.UserID == userID {
		return ErrSelfAction
	}
	target, err := a.user(ctx, actor, userID)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(reason)) < 3 {
		return ErrReasonRequired
	}
	if err := a.secondFactor.Disable(ctx, target, &actor.UserID, reason, client); err != nil && !errors.Is(err, ErrNotEnrolled) {
		return err
	}
	_, _ = a.sessions.LogoutEverywhere(ctx, userID, &actor.UserID, "second factor reset by an administrator")
	a.record(ctx, actor, AuditEntry{
		Kind: "second_factor.reset", TargetUserID: &userID, TargetCode: target.Code, Reason: reason, ClientDigest: client,
	})
	return nil
}

// --- helpers ---

func (a *Admin) user(ctx context.Context, actor Actor, userID uuid.UUID) (User, error) {
	user, err := a.store.GetUser(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return User{}, ErrUserNotFound
		}
		return User{}, err
	}
	// Another facility's staff do not exist to this administrator.
	if user.FacilityID != actor.FacilityID {
		return User{}, ErrUserNotFound
	}
	return user, nil
}

func (a *Admin) record(ctx context.Context, actor Actor, e AuditEntry) {
	if a.audit == nil {
		return
	}
	e.ActorID, e.FacilityID, e.At = actor.UserID, actor.FacilityID, a.clock.Now()
	e.ActorRole = actor.ActiveRole
	if u, err := a.store.GetUser(ctx, actor.UserID); err == nil {
		e.ActorCode = u.Code
	}
	// An audit entry that could not be written is logged by the recorder; the act itself
	// has happened and is not undone by a failure to describe it.
	_ = a.audit.RecordAudit(ctx, e)
}

// CheckPasswordPolicy is the one rule about passwords: at least twelve characters. Length
// is the property that matters against an offline attack on an argon2id hash; composition
// rules produce "Password1!" and nothing else. Twelve, because the clinic's staff type it
// on a tablet many times a day and a passphrase of three Bangla words is both long and
// memorable.
func CheckPasswordPolicy(password string) error {
	if utf8.RuneCountInString(password) < 12 {
		return fmt.Errorf("%w: at least twelve characters", ErrWeakPassword)
	}
	if utf8.RuneCountInString(password) > 128 {
		return fmt.Errorf("%w: at most 128 characters", ErrWeakPassword)
	}
	return nil
}

// validationError carries a field message for the handler.
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func errValidation(msg string) error { return &validationError{msg: msg} }
