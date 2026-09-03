package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Store is the persistence this package needs, and nothing more.
//
// An interface rather than the generated queries directly, for two reasons. It says exactly
// what identity requires of a database, which is a shorter and more reviewable list than
// everything sqlc will generate. And it lets the rules below be tested against an in-memory
// implementation, so a rule about who may grant a role does not need a container to prove.
//
// The pgx implementation is a thin adapter over the generated Querier.
type Store interface {
	GetUser(ctx context.Context, id uuid.UUID) (User, error)
	CreateUser(ctx context.Context, u User, by uuid.UUID) (User, error)
	SetUserStatus(ctx context.Context, id uuid.UUID, status Status, reason string, by uuid.UUID) (User, error)

	GetRoleByCode(ctx context.Context, code RoleCode) (Role, error)
	LiveGrants(ctx context.Context, userID uuid.UUID) ([]Grant, error)
	GrantRole(ctx context.Context, userID, roleID, facilityID uuid.UUID, by uuid.UUID) (Grant, error)
	RevokeRole(ctx context.Context, userID, roleID uuid.UUID, by uuid.UUID, reason string) (Grant, error)

	PermissionsForUser(ctx context.Context, userID uuid.UUID) ([]string, error)
	PermissionsForRole(ctx context.Context, code RoleCode) ([]string, error)
}

// Service holds the identity rules that are not the database's to enforce.
//
// The division is deliberate. Structural guarantees — a user cannot be deleted, a
// transition cannot skip a state, a nutritionist cannot hold a prescription permission —
// live in the database, because they must hold for every writer. The rules here are the
// ones that need context the database does not have: who is asking, and why.
type Service struct {
	store Store
	// invalidator is told when a person's membership changed, so that the RBAC engine's
	// cache (CP19) drops them and the change is felt on the next request rather than at
	// the end of the cache window.
	invalidator MembershipInvalidator
}

// MembershipInvalidator is the one thing the identity service needs from the RBAC engine.
// An interface, because auth may not import rbac (architecture.json: it is the other way
// round); the composition root connects the two.
type MembershipInvalidator interface {
	Invalidate(ctx context.Context, userID uuid.UUID) error
}

// WithInvalidator connects the engine's cache. Nil is allowed and means no cache to drop.
func (s *Service) WithInvalidator(inv MembershipInvalidator) *Service {
	s.invalidator = inv
	return s
}

func (s *Service) invalidate(ctx context.Context, userID uuid.UUID) {
	if s.invalidator != nil {
		// A cache that could not be dropped is bounded by its window; the change itself
		// has already been written.
		_ = s.invalidator.Invalidate(ctx, userID)
	}
}

func NewService(store Store) *Service { return &Service{store: store} }

// Errors a caller is expected to distinguish and map to a status code.
var (
	// ErrNotPermitted — the actor lacks the permission for this administrative action.
	ErrNotPermitted = errors.New("the actor does not hold the permission for this action")

	// ErrReasonRequired — the transition needs a stated reason and none was given.
	ErrReasonRequired = errors.New("this change requires a stated reason")

	// ErrSelfAction — an administrator acting on their own account in a way that could
	// lock them out.
	ErrSelfAction = errors.New("an administrator may not do this to their own account")

	// ErrAlreadyHeld — the role is already granted and live.
	ErrAlreadyHeld = errors.New("the user already holds this role")

	// ErrNotHeld — revoking a role the user does not hold.
	ErrNotHeld = errors.New("the user does not hold this role")
)

// PermissionsFor resolves everything a user may do, across every live role.
func (s *Service) PermissionsFor(ctx context.Context, userID uuid.UUID) (PermissionSet, error) {
	codes, err := s.store.PermissionsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("resolving permissions: %w", err)
	}
	return NewPermissionSet(codes...), nil
}

// Grant gives a user a role.
//
// The actor's own permission is checked here rather than left to the middleware, because
// granting a role is the action that creates every other permission in the system. A
// mistake here is not one user seeing one screen; it is the authorisation model itself.
func (s *Service) Grant(ctx context.Context, actor Actor, userID uuid.UUID, role RoleCode) (Grant, error) {
	if !actor.Permissions.Has(PermRoleGrant) {
		return Grant{}, fmt.Errorf("granting %s: %w", role, ErrNotPermitted)
	}

	target, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return Grant{}, fmt.Errorf("loading the user: %w", err)
	}
	if target.Status == StatusDeactivated {
		return Grant{}, fmt.Errorf("granting %s to a deactivated user: %w", role, ErrNotPermitted)
	}

	r, err := s.store.GetRoleByCode(ctx, role)
	if err != nil {
		return Grant{}, fmt.Errorf("loading the role %s: %w", role, err)
	}

	held, err := s.store.LiveGrants(ctx, userID)
	if err != nil {
		return Grant{}, fmt.Errorf("reading current grants: %w", err)
	}
	for _, g := range held {
		if g.RoleCode == role {
			return Grant{}, fmt.Errorf("%s: %w", role, ErrAlreadyHeld)
		}
	}

	grant, err := s.store.GrantRole(ctx, userID, r.ID, target.FacilityID, actor.UserID)
	if err == nil {
		s.invalidate(ctx, userID)
	}
	return grant, err
}

// Revoke ends a grant. The row is not deleted; revoked_at is set, so the history of who
// could do what remains answerable.
//
// A reason is required. A revocation nobody explained is the one that gets disputed, and by
// then the person who did it has forgotten.
func (s *Service) Revoke(ctx context.Context, actor Actor, userID uuid.UUID, role RoleCode, reason string) (Grant, error) {
	if !actor.Permissions.Has(PermRoleRevoke) {
		return Grant{}, fmt.Errorf("revoking %s: %w", role, ErrNotPermitted)
	}
	if strings.TrimSpace(reason) == "" {
		return Grant{}, fmt.Errorf("revoking %s: %w", role, ErrReasonRequired)
	}

	// Revoking your own administrator role is how a clinic ends up with no administrator
	// at all, which D-70 exists to prevent. Another administrator may do it.
	if actor.UserID == userID && role == RoleAdmin {
		return Grant{}, fmt.Errorf("revoking your own %s role: %w", role, ErrSelfAction)
	}

	r, err := s.store.GetRoleByCode(ctx, role)
	if err != nil {
		return Grant{}, fmt.Errorf("loading the role %s: %w", role, err)
	}

	held, err := s.store.LiveGrants(ctx, userID)
	if err != nil {
		return Grant{}, fmt.Errorf("reading current grants: %w", err)
	}
	found := false
	for _, g := range held {
		if g.RoleCode == role {
			found = true
		}
	}
	if !found {
		return Grant{}, fmt.Errorf("%s: %w", role, ErrNotHeld)
	}

	grant, err := s.store.RevokeRole(ctx, userID, r.ID, actor.UserID, reason)
	if err == nil {
		s.invalidate(ctx, userID)
	}
	return grant, err
}

// ChangeStatus moves a user through the lifecycle.
//
// The transition is validated here so the caller gets an error naming both states, and
// again by the database so the rule holds for every other writer.
func (s *Service) ChangeStatus(ctx context.Context, actor Actor, userID uuid.UUID, to Status, reason string) (User, error) {
	required := map[Status]string{
		StatusActive:      PermUserInvite,
		StatusSuspended:   PermUserSuspend,
		StatusDeactivated: PermUserDeactivate,
	}[to]

	if required == "" || !actor.Permissions.Has(required) {
		return User{}, fmt.Errorf("moving a user to %s: %w", to, ErrNotPermitted)
	}

	// Suspending or deactivating yourself locks you out with no way back in. If it is
	// genuinely intended, another administrator does it — which also leaves a record of
	// two people rather than one.
	if actor.UserID == userID && (to == StatusSuspended || to == StatusDeactivated) {
		return User{}, fmt.Errorf("moving your own account to %s: %w", to, ErrSelfAction)
	}

	if RequiresReason(to) && strings.TrimSpace(reason) == "" {
		return User{}, fmt.Errorf("moving a user to %s: %w", to, ErrReasonRequired)
	}

	current, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return User{}, fmt.Errorf("loading the user: %w", err)
	}
	if err := Transition(current.Status, to); err != nil {
		return User{}, err
	}

	user, err := s.store.SetUserStatus(ctx, userID, to, reason, actor.UserID)
	if err == nil {
		s.invalidate(ctx, userID)
	}
	return user, err
}

// Actor is who is asking, and what they may do.
//
// Passed explicitly rather than read from the context inside the service, so that a rule
// about who may act is visible in the signature of the function that enforces it.
type Actor struct {
	UserID      uuid.UUID
	FacilityID  uuid.UUID
	Permissions PermissionSet
	// ActiveRole is the hat the actor named for the request [R-02], for the audit trail.
	ActiveRole string
}
