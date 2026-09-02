package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// --- the lifecycle ---

// TestLifecycleIsExhaustive walks every ordered pair of statuses.
//
// Written as a full sixteen-cell table rather than a handful of examples, because the
// interesting cases in a state machine are the ones nobody thought to write down. The same
// sixteen pairs are put to the real database in TestLifecycleMatchesTheDatabase, so the Go
// table and the trigger cannot drift apart.
func TestLifecycleIsExhaustive(t *testing.T) {
	allowed := map[[2]Status]bool{
		{StatusInvited, StatusInvited}:         true, // no-op
		{StatusInvited, StatusActive}:          true, // the invitation is accepted
		{StatusInvited, StatusSuspended}:       false,
		{StatusInvited, StatusDeactivated}:     true, // never started
		{StatusActive, StatusInvited}:          false,
		{StatusActive, StatusActive}:           true,
		{StatusActive, StatusSuspended}:        true,
		{StatusActive, StatusDeactivated}:      true,
		{StatusSuspended, StatusInvited}:       false,
		{StatusSuspended, StatusActive}:        true, // reinstated
		{StatusSuspended, StatusSuspended}:     true,
		{StatusSuspended, StatusDeactivated}:   true,
		{StatusDeactivated, StatusInvited}:     false,
		{StatusDeactivated, StatusActive}:      true, // came back — one row, one history
		{StatusDeactivated, StatusSuspended}:   false,
		{StatusDeactivated, StatusDeactivated}: true,
	}

	if len(allowed) != len(AllStatuses)*len(AllStatuses) {
		t.Fatalf("the table has %d pairs; %d statuses need %d",
			len(allowed), len(AllStatuses), len(AllStatuses)*len(AllStatuses))
	}

	for pair, want := range allowed {
		if got := CanTransition(pair[0], pair[1]); got != want {
			t.Errorf("%s → %s: got %v, want %v", pair[0], pair[1], got, want)
		}
	}
}

// TestSuspendingAnInvitedUserIsRefusedClearly.
//
// Not just that it fails — that the message tells an administrator what to do instead.
// "Invalid status transition" is a sentence nobody can act on.
func TestSuspendingAnInvitedUserIsRefusedClearly(t *testing.T) {
	err := Transition(StatusInvited, StatusSuspended)
	if err == nil {
		t.Fatal("suspending an invited user was permitted")
	}

	var e *ErrTransition
	if !errors.As(err, &e) {
		t.Fatalf("expected an ErrTransition, got %T", err)
	}
	for _, want := range []string{"invited", "suspended", "deactivated"} {
		if !contains(err.Error(), want) {
			t.Errorf("the message does not mention %q: %s", want, err)
		}
	}
}

func TestUnknownStatusIsRefused(t *testing.T) {
	if err := Transition(StatusActive, Status("retired")); err == nil {
		t.Fatal("an unknown status was accepted")
	}
}

func TestOnlySuspensionRequiresAReason(t *testing.T) {
	for _, s := range AllStatuses {
		want := s == StatusSuspended
		if got := RequiresReason(s); got != want {
			t.Errorf("RequiresReason(%s) = %v, want %v", s, got, want)
		}
	}
}

// --- the catalogue ---

func TestCatalogueIsWhatTheBlueprintSays(t *testing.T) {
	if len(AllRoles) != 18 {
		t.Errorf("blueprint §6.3 names 18 roles; the catalogue has %d", len(AllRoles))
	}
	if len(AllStations) != 12 {
		t.Errorf("blueprint §3 names 12 stations; the catalogue has %d", len(AllStations))
	}

	for name, codes := range map[string][]string{
		"permissions": AllPermissions,
		"sensitive":   SensitivePermissions,
	} {
		seen := map[string]bool{}
		for _, c := range codes {
			if seen[c] {
				t.Errorf("%s: %q appears twice", name, c)
			}
			seen[c] = true
		}
	}

	// Every sensitive permission must also be in the full catalogue, or a blinding rule
	// is written against a permission nobody can hold.
	all := NewPermissionSet(AllPermissions...)
	for _, c := range SensitivePermissions {
		if !all.Has(c) {
			t.Errorf("sensitive permission %q is not in the catalogue", c)
		}
	}
}

// --- permission sets ---

func TestPermissionSetResolvesTheUnion(t *testing.T) {
	// An assistant covering anthropometry and vitals holds two roles that both grant
	// observation.read.values. [R-02] makes that the normal case, not the exception.
	anthro := NewPermissionSet(PermPatientReadDemographics, PermObservationWriteAnthro, PermObservationReadValues)
	vitals := NewPermissionSet(PermPatientReadDemographics, PermObservationWriteVitals, PermObservationReadValues)

	both := NewPermissionSet().Union(anthro).Union(vitals)

	if both.Len() != 4 {
		t.Errorf("union of 3 and 3 with 2 shared should be 4, got %d: %v", both.Len(), both.Codes())
	}
	if !both.HasAll(PermObservationWriteAnthro, PermObservationWriteVitals) {
		t.Error("the union lost a permission held by only one role")
	}
	if both.Has(PermPrescriptionSign) {
		t.Error("the union invented a permission neither role held")
	}
}

func TestPermissionSetCodesAreStablyOrdered(t *testing.T) {
	set := NewPermissionSet(PermUserSuspend, PermPatientMerge, PermAuditRead)
	first := fmt.Sprint(set.Codes())
	for i := 0; i < 50; i++ {
		if got := fmt.Sprint(set.Codes()); got != first {
			t.Fatalf("Codes() reordered between calls: %s then %s", first, got)
		}
	}
}

func TestSensitiveDetectsClinicalReads(t *testing.T) {
	blinded := NewPermissionSet(PermPatientReadDemographics, PermPatientWriteDemographics)
	if blinded.Sensitive() {
		t.Error("a demographics-only set was reported as sensitive")
	}

	clinical := NewPermissionSet(PermPatientReadDemographics, PermDiagnosisRead)
	if !clinical.Sensitive() {
		t.Error("a set holding diagnosis.read was not reported as sensitive")
	}
}

// --- the service rules ---

func TestGrantingNeedsThePermissionToGrant(t *testing.T) {
	s, store := newTestService()
	target := store.addUser(StatusActive)

	nobody := Actor{UserID: uuid.New(), Permissions: NewPermissionSet(PermUserRead)}
	if _, err := s.Grant(context.Background(), nobody, target, RolePharmacist); !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("expected ErrNotPermitted, got %v", err)
	}

	admin := Actor{UserID: uuid.New(), Permissions: NewPermissionSet(PermRoleGrant)}
	if _, err := s.Grant(context.Background(), admin, target, RolePharmacist); err != nil {
		t.Fatalf("an administrator could not grant a role: %v", err)
	}
}

func TestARoleIsNotGrantedTwice(t *testing.T) {
	s, store := newTestService()
	target := store.addUser(StatusActive)
	admin := Actor{UserID: uuid.New(), Permissions: NewPermissionSet(PermRoleGrant)}

	if _, err := s.Grant(context.Background(), admin, target, RoleNutritionist); err != nil {
		t.Fatalf("first grant: %v", err)
	}
	if _, err := s.Grant(context.Background(), admin, target, RoleNutritionist); !errors.Is(err, ErrAlreadyHeld) {
		t.Fatalf("expected ErrAlreadyHeld, got %v", err)
	}
}

func TestADeactivatedUserCannotBeGrantedARole(t *testing.T) {
	s, store := newTestService()
	target := store.addUser(StatusDeactivated)
	admin := Actor{UserID: uuid.New(), Permissions: NewPermissionSet(PermRoleGrant)}

	if _, err := s.Grant(context.Background(), admin, target, RoleCrm); !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("granting to a deactivated user should be refused, got %v", err)
	}
}

func TestRevocationRequiresAReason(t *testing.T) {
	s, store := newTestService()
	target := store.addUser(StatusActive)
	admin := Actor{UserID: uuid.New(), Permissions: NewPermissionSet(PermRoleGrant, PermRoleRevoke)}

	if _, err := s.Grant(context.Background(), admin, target, RoleQa); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revoke(context.Background(), admin, target, RoleQa, "  "); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("expected ErrReasonRequired, got %v", err)
	}
	if _, err := s.Revoke(context.Background(), admin, target, RoleQa, "moved to another clinic"); err != nil {
		t.Fatalf("a reasoned revocation failed: %v", err)
	}
}

// TestAnAdministratorCannotRemoveTheirOwnAdminRole.
//
// This is D-70 in miniature. A clinic with no administrator is locked out of its own
// system, and the most likely way to get there is one person tidying up their own account.
// Another administrator may do it, which also means two people were involved.
func TestAnAdministratorCannotRemoveTheirOwnAdminRole(t *testing.T) {
	s, store := newTestService()
	admin := store.addUser(StatusActive)
	actor := Actor{UserID: admin, Permissions: NewPermissionSet(PermRoleGrant, PermRoleRevoke)}

	if _, err := s.Grant(context.Background(), actor, admin, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revoke(context.Background(), actor, admin, RoleAdmin, "tidying up"); !errors.Is(err, ErrSelfAction) {
		t.Fatalf("expected ErrSelfAction, got %v", err)
	}

	other := Actor{UserID: uuid.New(), Permissions: NewPermissionSet(PermRoleRevoke)}
	if _, err := s.Revoke(context.Background(), other, admin, RoleAdmin, "role reassigned"); err != nil {
		t.Fatalf("a second administrator should be able to do it: %v", err)
	}
}

func TestAnAdministratorCannotSuspendThemselves(t *testing.T) {
	s, store := newTestService()
	me := store.addUser(StatusActive)
	actor := Actor{UserID: me, Permissions: NewPermissionSet(PermUserSuspend, PermUserDeactivate)}

	for _, to := range []Status{StatusSuspended, StatusDeactivated} {
		if _, err := s.ChangeStatus(context.Background(), actor, me, to, "clearing my account"); !errors.Is(err, ErrSelfAction) {
			t.Errorf("moving your own account to %s should be refused, got %v", to, err)
		}
	}
}

func TestSuspensionWithoutAReasonIsRefused(t *testing.T) {
	s, store := newTestService()
	target := store.addUser(StatusActive)
	admin := Actor{UserID: uuid.New(), Permissions: NewPermissionSet(PermUserSuspend)}

	if _, err := s.ChangeStatus(context.Background(), admin, target, StatusSuspended, ""); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("expected ErrReasonRequired, got %v", err)
	}
	if _, err := s.ChangeStatus(context.Background(), admin, target, StatusSuspended, "under review"); err != nil {
		t.Fatalf("a reasoned suspension failed: %v", err)
	}
}

func TestTheServiceRefusesATransitionTheStateMachineForbids(t *testing.T) {
	s, store := newTestService()
	target := store.addUser(StatusInvited)
	admin := Actor{UserID: uuid.New(), Permissions: NewPermissionSet(PermUserSuspend)}

	var e *ErrTransition
	_, err := s.ChangeStatus(context.Background(), admin, target, StatusSuspended, "not started")
	if !errors.As(err, &e) {
		t.Fatalf("expected an ErrTransition, got %v", err)
	}
}

// --- an in-memory Store ---
//
// Exists so a rule about who may grant a role can be proved without a container. The rules
// this exercises are the ones the database cannot enforce, because they depend on who is
// asking; the ones it can are tested against the real thing in identity_db_test.go.

type memStore struct {
	users  map[uuid.UUID]User
	roles  map[RoleCode]Role
	grants []Grant
}

func newTestService() (*Service, *memStore) {
	m := &memStore{users: map[uuid.UUID]User{}, roles: map[RoleCode]Role{}}
	for _, code := range AllRoles {
		m.roles[code] = Role{ID: uuid.New(), Code: code}
	}
	return NewService(m), m
}

func (m *memStore) addUser(status Status) uuid.UUID {
	id := uuid.New()
	m.users[id] = User{ID: id, FacilityID: uuid.New(), Status: status}
	return id
}

func (m *memStore) GetUser(_ context.Context, id uuid.UUID) (User, error) {
	u, ok := m.users[id]
	if !ok {
		return User{}, fmt.Errorf("no such user %s", id)
	}
	return u, nil
}

func (m *memStore) CreateUser(_ context.Context, u User, _ uuid.UUID) (User, error) {
	u.ID = uuid.New()
	m.users[u.ID] = u
	return u, nil
}

func (m *memStore) SetUserStatus(_ context.Context, id uuid.UUID, status Status, reason string, _ uuid.UUID) (User, error) {
	u, ok := m.users[id]
	if !ok {
		return User{}, fmt.Errorf("no such user %s", id)
	}
	u.Status, u.StatusNote = status, reason
	m.users[id] = u
	return u, nil
}

func (m *memStore) GetRoleByCode(_ context.Context, code RoleCode) (Role, error) {
	r, ok := m.roles[code]
	if !ok {
		return Role{}, fmt.Errorf("no such role %s", code)
	}
	return r, nil
}

func (m *memStore) LiveGrants(_ context.Context, userID uuid.UUID) ([]Grant, error) {
	var out []Grant
	for _, g := range m.grants {
		if g.UserID == userID && g.Live() {
			out = append(out, g)
		}
	}
	return out, nil
}

func (m *memStore) GrantRole(_ context.Context, userID, roleID, facilityID, by uuid.UUID) (Grant, error) {
	var code RoleCode
	for c, r := range m.roles {
		if r.ID == roleID {
			code = c
		}
	}
	g := Grant{ID: uuid.New(), UserID: userID, RoleID: roleID, RoleCode: code,
		FacilityID: facilityID, GrantedBy: &by}
	m.grants = append(m.grants, g)
	return g, nil
}

func (m *memStore) RevokeRole(_ context.Context, userID, roleID, by uuid.UUID, reason string) (Grant, error) {
	now := nowForTest()
	for i, g := range m.grants {
		if g.UserID == userID && g.RoleID == roleID && g.Live() {
			m.grants[i].RevokedAt = &now
			m.grants[i].RevokedBy = &by
			m.grants[i].RevokeReason = reason
			return m.grants[i], nil
		}
	}
	return Grant{}, ErrNotHeld
}

func (m *memStore) PermissionsForUser(_ context.Context, userID uuid.UUID) ([]string, error) {
	if u, ok := m.users[userID]; !ok || u.Status != StatusActive {
		return nil, nil
	}
	return []string{PermPatientReadDemographics}, nil
}

func (m *memStore) PermissionsForRole(context.Context, RoleCode) ([]string, error) { return nil, nil }

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}())
}

func nowForTest() time.Time { return time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC) }
