package auth_test

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
)

// These tests need a database. They skip without DTHCMS_TEST_POSTGRES_URL rather than
// failing, because a developer running the unit tests on a laptop with nothing started
// should get a pass and a note, not a wall of red — but CI sets the variable, so skipping
// is never how they finish.
//
// What they exist for: the migration and this package hold two representations of one set
// of rules — the permission catalogue, the lifecycle, the §4.4 access constraints. Two
// representations drift. These compare them exactly, in both directions.

// TestCatalogueConstantsMatchTheDatabase.
//
// Both directions, deliberately. A permission added to the migration and not to Go is a
// check nobody can write; a constant added to Go and not to the migration is a check that
// silently never passes — which fails safe, and is therefore the one nobody notices.
func TestCatalogueConstantsMatchTheDatabase(t *testing.T) {
	db := testsupport.Postgres(t)

	compare(t, "permissions", auth.AllPermissions, scan(t, db.SQL, `SELECT code FROM core.permission`))

	roles := make([]string, len(auth.AllRoles))
	for i, r := range auth.AllRoles {
		roles[i] = string(r)
	}
	compare(t, "roles", roles, scan(t, db.SQL, `SELECT code FROM core.role`))

	stations := make([]string, len(auth.AllStations))
	for i, s := range auth.AllStations {
		stations[i] = string(s)
	}
	compare(t, "stations", stations,
		scan(t, db.SQL, `SELECT code FROM core.station WHERE is_active`))

	compare(t, "sensitive permissions", auth.SensitivePermissions,
		scan(t, db.SQL, `SELECT code FROM core.permission WHERE is_sensitive`))
}

// TestLifecycleMatchesTheDatabase puts all sixteen ordered pairs to the real trigger.
//
// The Go table exists so the caller gets a 422 naming the transition rather than a 500
// naming a trigger. That is only worth having if the two agree, and agreement between two
// hand-written tables is not something to assume.
func TestLifecycleMatchesTheDatabase(t *testing.T) {
	db := testsupport.Postgres(t)

	for _, from := range auth.AllStatuses {
		for _, to := range auth.AllStatuses {
			t.Run(fmt.Sprintf("%s_to_%s", from, to), func(t *testing.T) {
				code := fmt.Sprintf("T_%s_%s", strings.ToUpper(string(from)[:3]), strings.ToUpper(string(to)[:3]))
				id := insertUser(t, db.SQL, code)

				// Reach the starting state by a route the machine allows.
				for _, step := range routeTo(from) {
					mustUpdateStatus(t, db.SQL, id, step)
				}

				_, err := db.SQL.Exec(
					`UPDATE core.app_user SET status = $2, status_reason = 'test' WHERE id = $1`, id, to)

				permittedByGo := auth.CanTransition(from, to)
				permittedByDB := err == nil

				if permittedByGo != permittedByDB {
					t.Fatalf("%s → %s: Go says %v, the database says %v (%v)",
						from, to, permittedByGo, permittedByDB, err)
				}
			})
		}
	}
}

// TestBlueprintAccessRulesHold is the manual verification this checkpoint asks for, done by
// a machine so it is done every time rather than once.
func TestBlueprintAccessRulesHold(t *testing.T) {
	db := testsupport.Postgres(t)

	for _, c := range []struct {
		name, query string
	}{
		{"§4.4 the nutritionist holds no prescription permission", `
			SELECT rp.permission_code FROM core.role_permission rp
			  JOIN core.role r ON r.id = rp.role_id
			  JOIN core.permission p ON p.code = rp.permission_code
			 WHERE r.code = 'NUTRITIONIST' AND p.resource = 'prescription'`},
		{"§4.4 diagnoses are hidden from the pharmacist", `
			SELECT rp.permission_code FROM core.role_permission rp
			  JOIN core.role r ON r.id = rp.role_id
			  JOIN core.permission p ON p.code = rp.permission_code
			 WHERE r.code = 'PHARMACIST' AND (p.resource = 'diagnosis' OR p.is_sensitive)`},
		{"§4.4 registration is blinded to clinical data", `
			SELECT rp.permission_code FROM core.role_permission rp
			  JOIN core.role r ON r.id = rp.role_id
			  JOIN core.permission p ON p.code = rp.permission_code
			 WHERE r.code = 'REGISTRATION' AND p.is_sensitive`},
		{"D-48 the researcher reaches nothing identifiable", `
			SELECT rp.permission_code FROM core.role_permission rp
			  JOIN core.role r ON r.id = rp.role_id
			  JOIN core.permission p ON p.code = rp.permission_code
			 WHERE r.code = 'RESEARCHER' AND p.resource <> 'research'`},
		{"every permission is held by some role", `
			SELECT p.code FROM core.permission p
			 WHERE NOT EXISTS (SELECT 1 FROM core.role_permission rp WHERE rp.permission_code = p.code)`},
		{"every active station has a role that works it", `
			SELECT s.code FROM core.station s
			 WHERE s.is_active AND NOT EXISTS (SELECT 1 FROM core.role r WHERE r.station_code = s.code)`},
	} {
		if got := scan(t, db.SQL, c.query); len(got) > 0 {
			t.Errorf("%s — violated by: %s", c.name, strings.Join(got, ", "))
		}
	}

	// And the assertion the database runs for itself, which is the same set. Calling it
	// here means a future migration that drops it from assert_invariants still fails.
	if _, err := db.SQL.Exec(`SELECT core.assert_rbac_constraints()`); err != nil {
		t.Fatalf("core.assert_rbac_constraints() failed: %v", err)
	}
}

// TestPermissionsAreTheUnionOfLiveRoles.
//
// [R-02] in one test: one operator, three roles, one set of permissions — and revoking a
// role takes effect at once rather than at the next session.
func TestPermissionsAreTheUnionOfLiveRoles(t *testing.T) {
	db := testsupport.Postgres(t)
	id := insertUser(t, db.SQL, "MULTI_01")
	mustUpdateStatus(t, db.SQL, id, auth.StatusActive)

	granted := []auth.RoleCode{auth.RoleAnthropometry, auth.RoleClinicalAssistant, auth.RoleCounselor}
	for _, role := range granted {
		grant(t, db.SQL, id, role)
	}

	want := map[string]bool{}
	for _, role := range granted {
		for _, code := range scan(t, db.SQL, `
			SELECT p.code FROM core.permission p
			  JOIN core.role_permission rp ON rp.permission_code = p.code
			  JOIN core.role r ON r.id = rp.role_id
			 WHERE r.code = $1`, string(role)) {
			want[code] = true
		}
	}

	got := scan(t, db.SQL, permissionsForUser, id)
	if len(got) != len(want) {
		t.Fatalf("union has %d permissions, expected %d", len(got), len(want))
	}
	for _, code := range got {
		if !want[code] {
			t.Errorf("the union produced %q, which no granted role carries", code)
		}
	}

	// Revoke one and the permissions only that role carried must disappear.
	before := len(got)
	if _, err := db.SQL.Exec(`
		UPDATE core.user_role SET revoked_at = now(), revoke_reason = 'test'
		 WHERE user_id = $1 AND role_id = (SELECT id FROM core.role WHERE code = 'CLINICAL_ASSISTANT')`, id); err != nil {
		t.Fatal(err)
	}
	if after := len(scan(t, db.SQL, permissionsForUser, id)); after >= before {
		t.Errorf("revoking a role left %d permissions, was %d — revocation is not immediate", after, before)
	}
}

// TestSuspensionRemovesEveryPermissionAtOnce.
//
// Suspension has to work in the minute it is needed. Walking a user's grants to revoke each
// one is slower, is a partial state if it fails halfway, and has to be undone one by one to
// reinstate them.
func TestSuspensionRemovesEveryPermissionAtOnce(t *testing.T) {
	db := testsupport.Postgres(t)
	id := insertUser(t, db.SQL, "SUSP_01")
	mustUpdateStatus(t, db.SQL, id, auth.StatusActive)
	grant(t, db.SQL, id, auth.RolePhysician)

	if len(scan(t, db.SQL, permissionsForUser, id)) == 0 {
		t.Fatal("an active physician resolved to no permissions")
	}

	if _, err := db.SQL.Exec(
		`UPDATE core.app_user SET status = 'suspended', status_reason = 'under review' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if got := scan(t, db.SQL, permissionsForUser, id); len(got) != 0 {
		t.Errorf("a suspended user still holds %d permissions", len(got))
	}

	// And reinstating restores them without re-granting anything.
	if _, err := db.SQL.Exec(
		`UPDATE core.app_user SET status = 'active', status_reason = '' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if len(scan(t, db.SQL, permissionsForUser, id)) == 0 {
		t.Error("reinstating a user did not restore their permissions")
	}
}

// TestUsersCannotBeHardDeleted — the property behind [R-03]. If a user row can vanish,
// every value they entered loses its author.
func TestUsersCannotBeHardDeleted(t *testing.T) {
	db := testsupport.Postgres(t)
	id := insertUser(t, db.SQL, "DEL_01")

	tx, err := db.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`SET ROLE dthcms_app`); err != nil {
		t.Fatalf("assuming the application role: %v", err)
	}

	// Each refusal gets its own savepoint. Postgres aborts the whole transaction on the
	// first error, so without these the second DELETE and the UPDATE fail with
	// "current transaction is aborted" — which looks exactly like the privilege refusal
	// this test is trying to observe, and would have passed for the wrong reason if the
	// assertions had been the other way round.
	refused := func(what, statement string) {
		t.Helper()
		if _, err := tx.Exec(`SAVEPOINT probe`); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(statement, id)
		if err == nil {
			t.Errorf("the application role %s", what)
		}
		if _, err := tx.Exec(`ROLLBACK TO SAVEPOINT probe`); err != nil {
			t.Fatal(err)
		}
	}

	refused("deleted a user", `DELETE FROM core.app_user WHERE id = $1`)
	refused("deleted a role grant", `DELETE FROM core.user_role WHERE user_id = $1`)

	// But it must still be able to deactivate, or it cannot manage staff at all.
	if _, err := tx.Exec(
		`UPDATE core.app_user SET status = 'deactivated' WHERE id = $1`, id); err != nil {
		t.Fatalf("the application role cannot deactivate a user: %v", err)
	}
}

// TestSeedIsIdempotent — `migrate up` on an existing database must change nothing. A seed
// that is only correct the first time is a seed that is wrong in staging.
func TestSeedIsIdempotent(t *testing.T) {
	db := testsupport.Postgres(t)

	counts := func() [4]int {
		var c [4]int
		for i, q := range []string{
			`SELECT count(*) FROM core.role`,
			`SELECT count(*) FROM core.permission`,
			`SELECT count(*) FROM core.role_permission`,
			`SELECT count(*) FROM core.station`,
		} {
			if err := db.SQL.QueryRow(q).Scan(&c[i]); err != nil {
				t.Fatal(err)
			}
		}
		return c
	}

	before := counts()
	if before[0] != 18 || before[3] != 12 {
		t.Fatalf("expected 18 roles and 12 stations, got %d and %d", before[0], before[3])
	}

	// Applying the catalogue a second time is what a re-run of `migrate up` does.
	if _, err := db.SQL.Exec(`SELECT core.assert_invariants()`); err != nil {
		t.Fatalf("invariants failed on a freshly migrated database: %v", err)
	}
	if after := counts(); after != before {
		t.Errorf("counts changed on re-assertion: %v then %v", before, after)
	}
}

// --- helpers ---

const permissionsForUser = `
	SELECT DISTINCT rp.permission_code
	  FROM core.app_user u
	  JOIN core.user_role ur ON ur.user_id = u.id AND ur.revoked_at IS NULL
	  JOIN core.role_permission rp ON rp.role_id = ur.role_id
	 WHERE u.id = $1 AND u.status = 'active'`

func scan(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("query failed: %v\n%s", err, query)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func compare(t *testing.T, what string, inGo, inDB []string) {
	t.Helper()

	goSet, dbSet := map[string]bool{}, map[string]bool{}
	for _, s := range inGo {
		goSet[s] = true
	}
	for _, s := range inDB {
		dbSet[s] = true
	}

	for s := range dbSet {
		if !goSet[s] {
			t.Errorf("%s: %q is in the migration but has no Go constant — nothing can check it", what, s)
		}
	}
	for s := range goSet {
		if !dbSet[s] {
			t.Errorf("%s: %q is a Go constant with no row in the database — every check against it "+
				"fails safe and silently", what, s)
		}
	}
}

func insertUser(t *testing.T, db *sql.DB, code string) string {
	t.Helper()
	var id string
	err := db.QueryRow(`
		INSERT INTO core.app_user (facility_id, employee_code, name_en, name_bn)
		VALUES (core.default_facility(), $1, 'Test User', 'পরীক্ষামূলক ব্যবহারকারী')
		RETURNING id`, code).Scan(&id)
	if err != nil {
		t.Fatalf("inserting %s: %v", code, err)
	}
	return id
}

func mustUpdateStatus(t *testing.T, db *sql.DB, id string, to auth.Status) {
	t.Helper()
	reason := ""
	if auth.RequiresReason(to) {
		reason = "test"
	}
	if _, err := db.Exec(
		`UPDATE core.app_user SET status = $2, status_reason = $3 WHERE id = $1`, id, to, reason); err != nil {
		t.Fatalf("moving the user to %s: %v", to, err)
	}
}

func grant(t *testing.T, db *sql.DB, userID string, role auth.RoleCode) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO core.user_role (user_id, role_id, facility_id)
		SELECT $1, r.id, core.default_facility() FROM core.role r WHERE r.code = $2`,
		userID, string(role)); err != nil {
		t.Fatalf("granting %s: %v", role, err)
	}
}

// routeTo returns the moves needed to put a freshly created (invited) user into a state.
func routeTo(target auth.Status) []auth.Status {
	switch target {
	case auth.StatusInvited:
		return nil
	case auth.StatusActive:
		return []auth.Status{auth.StatusActive}
	case auth.StatusSuspended:
		return []auth.Status{auth.StatusActive, auth.StatusSuspended}
	case auth.StatusDeactivated:
		return []auth.Status{auth.StatusDeactivated}
	}
	return nil
}
