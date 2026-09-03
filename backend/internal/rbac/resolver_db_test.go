package rbac_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// Against the real schema: the Go grant table matches the migration's, and a revoked
// role stops working within the documented window (CP19 criterion 5).

type stack struct {
	db       *testsupport.DB
	store    *auth.PostgresStore
	clock    *clock.Fixed
	resolver *rbac.Resolver
	service  *auth.Service
	facility uuid.UUID
}

func newStack(t *testing.T) *stack {
	t.Helper()
	db := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, db.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	var facility uuid.UUID
	if err := db.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&facility); err != nil {
		t.Fatal(err)
	}
	s := &stack{db: db, facility: facility, clock: clock.NewFixed(time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC))}
	s.store = auth.NewPostgresStore(pool)
	s.resolver = rbac.NewResolver(rbac.ResolverConfig{Grants: s.store, Clock: s.clock})
	s.service = auth.NewService(s.store).WithInvalidator(s.resolver)
	return s
}

func (s *stack) user(t *testing.T, code string, roles ...auth.RoleCode) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := s.db.SQL.QueryRow(`
		INSERT INTO core.app_user (facility_id, employee_code, name_en, name_bn)
		VALUES ($1, $2, 'Test User', 'পরীক্ষামূলক ব্যবহারকারী') RETURNING id`, s.facility, code).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.SQL.Exec(`UPDATE core.app_user SET status = 'active' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	for _, role := range roles {
		if _, err := s.db.SQL.Exec(`
			INSERT INTO core.user_role (user_id, role_id, facility_id)
			SELECT $1, id, $2 FROM core.role WHERE code = $3`, id, s.facility, string(role)); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestRolePermissionsMatchTheDatabase(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	for _, role := range auth.AllRoles {
		fromDB, err := s.store.PermissionsForRole(ctx, role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		sort.Strings(fromDB)
		fromGo := rbac.RolePermissions[role].Codes()
		if len(fromDB) != len(fromGo) {
			t.Errorf("%s: database grants %d permissions, Go table has %d\n db: %v\n go: %v", role, len(fromDB), len(fromGo), fromDB, fromGo)
			continue
		}
		for i := range fromDB {
			if fromDB[i] != fromGo[i] {
				t.Errorf("%s: database and Go tables differ at %q vs %q", role, fromDB[i], fromGo[i])
			}
		}
	}
	if len(rbac.RolePermissions) != len(auth.AllRoles) {
		t.Errorf("Go table has %d roles, catalogue has %d", len(rbac.RolePermissions), len(auth.AllRoles))
	}
}

func TestRevocationTakesEffectWithinTheWindow(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := s.user(t, "A001", auth.RoleAdmin)
	nurse := s.user(t, "N001", auth.RoleAnthropometry, auth.RoleCounselor)
	station := uuid.New()
	patient := rbac.Resource{Kind: "patient", FacilityID: s.facility, StationID: &station}

	can := func(action string) bool {
		subject, err := s.resolver.Subject(ctx, nurse, s.facility, "", &station)
		if err != nil {
			t.Fatal(err)
		}
		return rbac.Can(subject, action, patient).Allowed
	}

	if !can(auth.PermCounselingTick) {
		t.Fatal("a counselor must tick counseling")
	}

	// Revoke through the service: the cache is dropped, and the next decision denies.
	actor := auth.Actor{UserID: admin, FacilityID: s.facility, Permissions: rbac.RolePermissions[auth.RoleAdmin]}
	if _, err := s.service.Revoke(ctx, actor, nurse, auth.RoleCounselor, "moved to anthropometry only"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if can(auth.PermCounselingTick) {
		t.Fatal("criterion 5: a revocation through the service must be felt on the next request")
	}
	if !can(auth.PermObservationWriteAnthro) {
		t.Fatal("the role still held must still work")
	}

	// The abnormal case: a grant written by something that did not tell the cache — a
	// second process, a hand edit. The stale answer lives at most the window.
	if _, err := s.db.SQL.Exec(`
		UPDATE core.user_role SET revoked_at = now(), revoke_reason = 'left the clinic'
		 WHERE user_id = $1 AND revoked_at IS NULL`, nurse); err != nil {
		t.Fatal(err)
	}
	if !can(auth.PermObservationWriteAnthro) {
		t.Fatal("inside the window the cache still answers; that is the documented bound")
	}
	s.clock.Advance(rbac.CacheWindow - time.Second)
	if !can(auth.PermObservationWriteAnthro) {
		t.Fatal("still inside the window")
	}
	s.clock.Advance(2 * time.Second)
	if can(auth.PermObservationWriteAnthro) {
		t.Fatalf("criterion 5: a revocation must take effect within %s even when the cache was not told", rbac.CacheWindow)
	}

	// Suspension, through the service, is immediate too — and total.
	if _, err := s.service.ChangeStatus(ctx, actor, nurse, auth.StatusSuspended, "under review"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	subject, err := s.resolver.Subject(ctx, nurse, s.facility, "", &station)
	if err != nil {
		t.Fatal(err)
	}
	if len(subject.Roles) != 0 || subject.Permissions.Len() != 0 {
		t.Fatalf("a suspended account must resolve to no roles: %+v", subject)
	}
}
