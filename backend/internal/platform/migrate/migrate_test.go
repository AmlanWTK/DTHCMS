package migrate_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/migrate"
	"github.com/AmlanWTK/DTHCMS/backend/migrations"
)

// These tests need a real PostgreSQL. They are skipped without one rather than mocked,
// because everything they assert is a property of PostgreSQL's privilege system: a mock
// would only confirm that the mock was written to agree with the test.
//
//	DTHCMS_TEST_POSTGRES_URL=postgres://dthcms:dthcms_local_only@127.0.0.1:5433/postgres?sslmode=disable
//
// The URL must belong to a role that can CREATE DATABASE and CREATE ROLE; each test
// builds its own database and drops it afterwards.
const testURLEnv = "DTHCMS_TEST_POSTGRES_URL"

// permissionDenied is PostgreSQL's SQLSTATE for insufficient privilege. Matching the
// code rather than the message keeps these tests working on a non-English server.
const permissionDenied = "42501"

func TestMigrationsApplyToAnEmptyDatabase(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	runner := newRunner(t, dsn, migrations.FS)

	if err := runner.Up(ctx); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}

	version, err := runner.Version(ctx)
	if err != nil {
		t.Fatalf("reading version: %v", err)
	}
	if version < 4 {
		t.Fatalf("schema version = %d, want at least 4", version)
	}

	db := open(t, dsn)

	for _, schema := range []string{"core", "ledger", "read", "ops", "docs", "research"} {
		var exists bool
		if err := db.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)", schema).Scan(&exists); err != nil {
			t.Fatalf("checking schema %s: %v", schema, err)
		}
		if !exists {
			t.Errorf("schema %q was not created", schema)
		}
	}

	for _, extension := range []string{"pgcrypto", "pg_trgm", "btree_gist"} {
		var exists bool
		if err := db.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = $1)", extension).Scan(&exists); err != nil {
			t.Fatalf("checking extension %s: %v", extension, err)
		}
		if !exists {
			t.Errorf("extension %q was not installed", extension)
		}
	}
}

func TestSeededFacilityHasAStableIdentity(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	mustMigrate(t, ctx, dsn)

	db := open(t, dsn)

	var id, nameEN, nameBN, timezone string
	err := db.QueryRowContext(ctx,
		`SELECT id::text, name_en, name_bn, timezone FROM core.facility WHERE code = 'DTHC-FRD'`).
		Scan(&id, &nameEN, &nameBN, &timezone)
	if err != nil {
		t.Fatalf("reading the seeded facility: %v", err)
	}

	// The identifier is fixed on purpose: fixtures, screenshots and support
	// conversations must refer to the same facility in every environment.
	const wantID = "0190a000-0000-7000-8000-000000000001"
	if id != wantID {
		t.Errorf("facility id = %s, want %s", id, wantID)
	}
	if timezone != "Asia/Dhaka" {
		t.Errorf("timezone = %s, want Asia/Dhaka", timezone)
	}
	if nameEN == "" || nameBN == "" {
		t.Error("both the English and Bangla names must be seeded")
	}

	var fromFunction string
	if err := db.QueryRowContext(ctx, "SELECT core.default_facility()::text").Scan(&fromFunction); err != nil {
		t.Fatalf("core.default_facility(): %v", err)
	}
	if fromFunction != wantID {
		t.Errorf("core.default_facility() = %s, want %s", fromFunction, wantID)
	}
}

func TestMigratingTwiceChangesNothing(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	runner := newRunner(t, dsn, migrations.FS)

	if err := runner.Up(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first, err := runner.Version(ctx)
	if err != nil {
		t.Fatalf("reading version: %v", err)
	}

	if err := runner.Up(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second, err := runner.Version(ctx)
	if err != nil {
		t.Fatalf("reading version: %v", err)
	}

	if first != second {
		t.Errorf("version changed on a no-op run: %d then %d", first, second)
	}

	// One seeded facility, not two. A seed that is not idempotent produces a duplicate
	// on the second deploy of the same release.
	db := open(t, dsn)
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM core.facility").Scan(&count); err != nil {
		t.Fatalf("counting facilities: %v", err)
	}
	if count != 1 {
		t.Errorf("facility count = %d after two runs, want 1", count)
	}
}

// TestMigrationsRunOverExistingData is the empty-database test's more important sibling.
//
// Every migration after the first runs against a database that already holds data, and
// most migration accidents — a NOT NULL without a default, a unique index over values
// that are not unique, a seed that inserts a second copy — only appear there.
//
// Its coverage is limited today: there is one wave of migrations and no production
// snapshot to restore, because there is no production data yet. When the event store
// arrives (CP23) this test gains a second wave to run over, and the restore-from-backup
// variant becomes possible. That gap is recorded rather than assumed away.
func TestMigrationsRunOverExistingData(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	runner := newRunner(t, dsn, migrations.FS)
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}

	db := open(t, dsn)
	mustExec(t, ctx, db, `
INSERT INTO core.facility (code, name_en, name_bn, facility_type, district)
VALUES ('DTHC-DHK', 'DTHC Dhaka', 'ডিটিএইচসি ঢাকা', 'satellite', 'Dhaka')`)

	// A second branch: the case D-61 exists for.
	mustExec(t, ctx, db, `
CREATE TABLE ledger.probe_event (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid NOT NULL REFERENCES core.facility(id),
  payload jsonb NOT NULL
)`)
	mustExec(t, ctx, db,
		`INSERT INTO ledger.probe_event (facility_id, payload) VALUES (core.default_facility(), '{"n":1}')`)

	if err := runner.Up(ctx); err != nil {
		t.Fatalf("re-running migrations over populated data: %v", err)
	}

	var facilities, events int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM core.facility").Scan(&facilities); err != nil {
		t.Fatalf("counting facilities: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM ledger.probe_event").Scan(&events); err != nil {
		t.Fatalf("counting events: %v", err)
	}
	if facilities != 2 {
		t.Errorf("facility count = %d, want 2 — a migration touched existing rows", facilities)
	}
	if events != 1 {
		t.Errorf("event count = %d, want 1", events)
	}

	// The invariants must still hold over tables that did not exist when 00002 ran.
	if _, err := db.ExecContext(ctx, "SELECT core.assert_invariants()"); err != nil {
		t.Errorf("invariants after migrating over existing data: %v", err)
	}
}

// TestTheLedgerIsAppendOnly is the acceptance criterion this whole checkpoint exists for.
//
// It does not test a Go function. It connects as the role the application actually uses
// and tries to rewrite history, the way a bug or a well-meant hotfix would.
func TestTheLedgerIsAppendOnly(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	mustMigrate(t, ctx, dsn)
	mustCreateDevRoles(t, ctx, dsn)

	owner := open(t, dsn)
	mustExec(t, ctx, owner, `
CREATE TABLE ledger.probe_event (
  id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid        NOT NULL REFERENCES core.facility(id),
  payload     jsonb       NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT now()
)`)

	app := openAs(t, dsn, "dthcms_app_local")

	mustExec(t, ctx, app,
		`INSERT INTO ledger.probe_event (facility_id, payload) VALUES (core.default_facility(), '{"probe":1}')`)

	var count int
	if err := app.QueryRowContext(ctx, "SELECT count(*) FROM ledger.probe_event").Scan(&count); err != nil {
		t.Fatalf("the application must be able to read the ledger: %v", err)
	}
	if count != 1 {
		t.Fatalf("ledger row count = %d, want 1", count)
	}

	forbidden := []struct {
		name string
		stmt string
	}{
		{"UPDATE", `UPDATE ledger.probe_event SET payload = '{"probe":2}'`},
		{"DELETE", `DELETE FROM ledger.probe_event`},
		{"TRUNCATE", `TRUNCATE ledger.probe_event`},
	}
	for _, tc := range forbidden {
		t.Run(tc.name, func(t *testing.T) {
			_, err := app.ExecContext(ctx, tc.stmt)
			if err == nil {
				t.Fatalf("%s against the ledger succeeded; it must be refused by the database, "+
					"not merely avoided by the application", tc.name)
			}
			if state := sqlState(err); state != permissionDenied {
				t.Fatalf("%s failed with SQLSTATE %s (%v); want %s (insufficient privilege). "+
					"A different failure means the statement was rejected for the wrong reason",
					tc.name, state, err, permissionDenied)
			}
		})
	}

	// And the row is still there, unchanged.
	var payload string
	if err := app.QueryRowContext(ctx, "SELECT payload::text FROM ledger.probe_event").Scan(&payload); err != nil {
		t.Fatalf("re-reading the ledger: %v", err)
	}
	if !strings.Contains(payload, `"probe": 1`) && !strings.Contains(payload, `"probe":1`) {
		t.Errorf("ledger payload = %s, want the originally written value", payload)
	}
}

func TestTheApplicationCannotWriteReadModels(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	mustMigrate(t, ctx, dsn)
	mustCreateDevRoles(t, ctx, dsn)

	owner := open(t, dsn)
	mustExec(t, ctx, owner, `
CREATE TABLE read.probe_projection (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid NOT NULL REFERENCES core.facility(id),
  summary     text NOT NULL
)`)

	app := openAs(t, dsn, "dthcms_app_local")

	var count int
	if err := app.QueryRowContext(ctx, "SELECT count(*) FROM read.probe_projection").Scan(&count); err != nil {
		t.Fatalf("the application must be able to read projections: %v", err)
	}

	for _, tc := range []struct{ name, stmt string }{
		{"INSERT", `INSERT INTO read.probe_projection (facility_id, summary) VALUES (core.default_facility(), 'x')`},
		{"UPDATE", `UPDATE read.probe_projection SET summary = 'y'`},
		{"DELETE", `DELETE FROM read.probe_projection`},
	} {
		if _, err := app.ExecContext(ctx, tc.stmt); err == nil {
			t.Errorf("%s against a read model succeeded; projections must only ever be "+
				"written by the projector, or a hand correction will silently disagree "+
				"with the events it is derived from", tc.name)
		} else if state := sqlState(err); state != permissionDenied {
			t.Errorf("%s failed with SQLSTATE %s, want %s", tc.name, state, permissionDenied)
		}
	}

	// The projector, by contrast, owns them.
	projector := openAs(t, dsn, "dthcms_projector_local")
	mustExec(t, ctx, projector,
		`INSERT INTO read.probe_projection (facility_id, summary) VALUES (core.default_facility(), 'projected')`)
}

func TestResearchCannotReachIdentifiedData(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	mustMigrate(t, ctx, dsn)
	mustCreateDevRoles(t, ctx, dsn)

	owner := open(t, dsn)
	mustExec(t, ctx, owner, `CREATE TABLE core.probe_identified (id uuid PRIMARY KEY, facility_id uuid, note text)`)
	mustExec(t, ctx, owner, `CREATE TABLE research.probe_mart (bucket text PRIMARY KEY, n int NOT NULL)`)
	mustExec(t, ctx, owner, `INSERT INTO research.probe_mart VALUES ('a', 1)`)

	research := openAs(t, dsn, "dthcms_research_local")

	var n int
	if err := research.QueryRowContext(ctx, "SELECT n FROM research.probe_mart WHERE bucket = 'a'").Scan(&n); err != nil {
		t.Fatalf("research must be able to read its own marts: %v", err)
	}

	for _, table := range []string{"core.probe_identified", "core.facility"} {
		if _, err := research.ExecContext(ctx, "SELECT 1 FROM "+table); err == nil {
			t.Errorf("the research role could read %s; anonymisation that depends on the "+
				"analyst querying the right schema is not anonymisation", table)
		}
	}
}

// TestEditingAnAppliedMigrationIsDetected covers the failure that produces two
// environments with different schemas and no error message anywhere.
func TestEditingAnAppliedMigrationIsDetected(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	mustMigrate(t, ctx, dsn)

	edited := copyFSWithEdit(t, migrations.FS, "00003_conventions.sql",
		"\n-- a later edit to a migration that has already run\n")

	runner := newRunner(t, dsn, edited)
	err := runner.Verify(ctx)
	if err == nil {
		t.Fatal("editing an applied migration was not detected; the repository and the " +
			"database would now describe different schemas")
	}
	if !strings.Contains(err.Error(), "00003_conventions.sql") {
		t.Errorf("the error must name the changed file, got: %v", err)
	}

	// And Up must refuse too, rather than applying the remaining migrations on top of a
	// database that no longer matches the repository.
	if err := runner.Up(ctx); err == nil {
		t.Error("Up proceeded despite drift")
	}
}

func TestUnscopedTablesAreRejected(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	mustMigrate(t, ctx, dsn)

	db := open(t, dsn)
	mustExec(t, ctx, db, `CREATE TABLE core.probe_unscoped (id uuid PRIMARY KEY, note text)`)

	_, err := db.ExecContext(ctx, "SELECT core.assert_invariants()")
	if err == nil {
		t.Fatal("a table without facility_id passed the scoping assertion; adding tenancy " +
			"to a populated clinical database later is the migration this exists to prevent")
	}
	if !strings.Contains(err.Error(), "probe_unscoped") {
		t.Errorf("the error must name the offending table, got: %v", err)
	}

	// An explicit, reasoned exemption is the escape hatch — not editing the assertion.
	mustExec(t, ctx, db, `
INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason)
VALUES ('core', 'probe_unscoped', 'Probe table created by the CP06 test suite; belongs to no facility.')`)

	if _, err := db.ExecContext(ctx, "SELECT core.assert_invariants()"); err != nil {
		t.Errorf("an exempted table should pass: %v", err)
	}
}

// TestEveryAssertionIsRegistered.
//
// The failure this catches is the quietest one in the codebase: somebody writes a
// core.assert_* function, folds it into nothing, and it never runs. The guarantee looks
// implemented — the function is there, it is commented, it works when called — and it is
// simply never called. CP15 shipped two assertions and the migration log went on reporting
// the four it knew about, which is the same defect one step removed.
//
// So the registry is the source of truth (ops.invariant, from 00007) and this asserts that
// nothing exists outside it.
func TestEveryAssertionIsRegistered(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	mustMigrate(t, ctx, dsn)
	db := open(t, dsn)

	rows, err := db.QueryContext(ctx, `
		SELECT n.nspname || '.' || p.proname
		  FROM pg_proc p
		  JOIN pg_namespace n ON n.oid = p.pronamespace
		 WHERE n.nspname IN ('core', 'ops')
		   AND p.proname LIKE 'assert\_%'
		   AND p.pronargs = 0
		   -- Trigger functions return trigger, not void, and are invoked by the row they
		   -- guard rather than by the runner. assert_invariants is the runner itself.
		   AND p.prorettype = 'void'::regtype
		   AND p.proname <> 'assert_invariants'
		   AND NOT EXISTS (
		     SELECT 1 FROM ops.invariant i
		      WHERE i.schema_name = n.nspname AND i.function_name = p.proname)
		 ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	var unregistered []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		unregistered = append(unregistered, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if len(unregistered) > 0 {
		t.Errorf("assertions that exist but never run: %s\n"+
			"Register each in ops.invariant, in the migration that creates it.",
			strings.Join(unregistered, ", "))
	}

	// And what the runner logs must describe the same set it just ran.
	var checked string
	if err := db.QueryRowContext(ctx, "SELECT core.invariants_checked()").Scan(&checked); err != nil {
		t.Fatal(err)
	}
	descriptions, err := db.QueryContext(ctx, "SELECT description FROM ops.invariant")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = descriptions.Close() }()

	// Each registered description must appear in the logged line. Counting separators was
	// the first attempt and it was wrong: one description reads "read models are derived,
	// not written by the application", so six guarantees produced seven commas and the test
	// reported a discrepancy that did not exist. Splitting on a character that occurs
	// inside the values is a mistake worth not repeating.
	var registered int
	for descriptions.Next() {
		var d string
		if err := descriptions.Scan(&d); err != nil {
			t.Fatal(err)
		}
		registered++
		if !strings.Contains(checked, d) {
			t.Errorf("the migration log omits a registered guarantee: %q\nlogged: %q", d, checked)
		}
	}
	if err := descriptions.Err(); err != nil {
		t.Fatal(err)
	}
	if registered == 0 {
		t.Fatal("no invariants are registered; assert_invariants() would be a no-op that reports success")
	}
}

// TestAnUnregisteredAssertionCannotBeRegisteredByName guards the other direction: a typo in
// a registration would turn every migration run into a runtime error about a missing
// function, at the worst possible moment.
func TestRegisteringAMissingAssertionIsRefused(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	mustMigrate(t, ctx, dsn)
	db := open(t, dsn)

	_, err := db.ExecContext(ctx, `
		INSERT INTO ops.invariant (function_name, description, sequence)
		VALUES ('assert_something_i_never_wrote', 'a guarantee that does not exist', 999)`)
	if err == nil {
		t.Fatal("a registration naming a non-existent function was accepted")
	}
	if !strings.Contains(err.Error(), "assert_something_i_never_wrote") {
		t.Errorf("the error must name the missing function, got: %v", err)
	}
}

func TestLooseningTheLedgerGrantIsDetected(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	mustMigrate(t, ctx, dsn)

	db := open(t, dsn)
	mustExec(t, ctx, db, `
CREATE TABLE ledger.probe_event (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid NOT NULL REFERENCES core.facility(id),
  payload jsonb NOT NULL
)`)

	// Exactly what a future migration might do by accident — a blanket grant written
	// once and copied thereafter.
	mustExec(t, ctx, db, `GRANT UPDATE ON ledger.probe_event TO dthcms_app`)

	_, err := db.ExecContext(ctx, "SELECT core.assert_invariants()")
	if err == nil {
		t.Fatal("granting UPDATE on a ledger table passed the invariant check")
	}
	if !strings.Contains(err.Error(), "probe_event") || !strings.Contains(err.Error(), "UPDATE") {
		t.Errorf("the error must name the table and the privilege, got: %v", err)
	}
}

func TestRollbackIsRefusedInProduction(t *testing.T) {
	// No database needed: the refusal must not depend on being able to reach one.
	runner, err := migrate.New(migrate.Options{
		FS:         migrations.FS,
		DSN:        "postgres://unreachable.invalid:5432/dthcms",
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Production: true,
	})
	if err != nil {
		t.Fatalf("building runner: %v", err)
	}

	if err := runner.Down(context.Background()); err == nil {
		t.Fatal("Down was permitted in production")
	}
	if err := runner.EnsureDevRoles(context.Background(), "anything"); err == nil {
		t.Fatal("dev-roles was permitted in production")
	}
}

func TestRollbackUndoesTheLastMigration(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	runner := newRunner(t, dsn, migrations.FS)
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}

	before, err := runner.Version(ctx)
	if err != nil {
		t.Fatalf("reading version: %v", err)
	}

	// Fingerprinted rather than checking for one named table.
	//
	// This test used to assert that core.facility was gone, because 00004 created it and
	// 00004 was the last migration. Adding CP15's migrations made the assertion false
	// without making anything wrong — a test coupled to which file happens to be last is a
	// test that fails on the next checkpoint and teaches nobody anything.
	//
	// What actually matters is that down and up are a round trip: the schema must change
	// when a migration is rolled back, and must come back *identical* when it is
	// re-applied. Function bodies are included, so a down migration that restores an
	// earlier definition slightly wrong is caught too.
	db := open(t, dsn)
	fullSchema := schemaFingerprint(t, ctx, db)

	if err := runner.Down(ctx); err != nil {
		t.Fatalf("rolling back: %v", err)
	}

	after, err := runner.Version(ctx)
	if err != nil {
		t.Fatalf("reading version: %v", err)
	}
	if after != before-1 {
		t.Fatalf("version after rollback = %d, want %d", after, before-1)
	}

	if rolledBack := schemaFingerprint(t, ctx, db); rolledBack == fullSchema {
		t.Error("rolling back the last migration changed nothing in the schema")
	}

	// Forward again: a rollback that cannot be re-applied is not reversible, it is just
	// destructive.
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("re-applying after rollback: %v", err)
	}
	final, err := runner.Version(ctx)
	if err != nil {
		t.Fatalf("reading version: %v", err)
	}
	if final != before {
		t.Errorf("version after re-applying = %d, want %d", final, before)
	}

	if restored := schemaFingerprint(t, ctx, db); restored != fullSchema {
		t.Error("the schema after down-then-up differs from the schema before; a rollback " +
			"that cannot be re-applied to the same shape is destructive rather than reversible")
	}
}

// schemaFingerprint hashes the structure of the application schemas: every column of every
// table, and the full definition of every function. Data is deliberately excluded — the
// migration bookkeeping table changes on every run and says nothing about shape.
func schemaFingerprint(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()

	const query = `
		SELECT string_agg(entry, E'\n' ORDER BY entry) FROM (
		  SELECT format('col %s.%s.%s %s %s', table_schema, table_name, column_name,
		                data_type, is_nullable) AS entry
		    FROM information_schema.columns
		   WHERE table_schema IN ('core', 'ledger', 'read', 'docs', 'ops', 'research')
		  UNION ALL
		  SELECT format('fn  %s', pg_get_functiondef(p.oid))
		    FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		   WHERE n.nspname IN ('core', 'ops')
		  UNION ALL
		  -- The access catalogue is reference data that only migrations may change (CP21's
		  -- 00011 adds one permission and nothing structural), so it is part of the shape.
		  SELECT format('perm %s', code) FROM core.permission
		  UNION ALL
		  SELECT format('grant %s %s', r.code, rp.permission_code)
		    FROM core.role_permission rp JOIN core.role r ON r.id = rp.role_id
		) parts`

	var schema sql.NullString
	if err := db.QueryRowContext(ctx, query).Scan(&schema); err != nil {
		t.Fatalf("fingerprinting the schema: %v", err)
	}
	sum := sha256.Sum256([]byte(schema.String))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

func newRunner(t *testing.T, dsn string, fsys fs.FS) *migrate.Runner {
	t.Helper()
	runner, err := migrate.New(migrate.Options{
		FS:     fsys,
		DSN:    dsn,
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("building runner: %v", err)
	}
	return runner
}

func mustMigrate(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()
	if err := newRunner(t, dsn, migrations.FS).Up(ctx); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}
}

func mustCreateDevRoles(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()
	if err := newRunner(t, dsn, migrations.FS).EnsureDevRoles(ctx, "dthcms_local_only"); err != nil {
		t.Fatalf("creating development roles: %v", err)
	}
}

// freshDatabase creates an empty database for one test and drops it afterwards.
//
// Per-test databases rather than a shared one: these tests create tables in fixed
// schemas and change grants, so sharing would make them order-dependent, and an
// order-dependent test that fails intermittently teaches people to re-run the suite.
func freshDatabase(t *testing.T) (context.Context, string) {
	t.Helper()

	base := os.Getenv(testURLEnv)
	if base == "" {
		t.Skipf("set %s to run the database tests (see the package comment)", testURLEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("connecting to %s: %v", testURLEnv, err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	if err := admin.PingContext(ctx); err != nil {
		t.Fatalf("cannot reach the test database server: %v", err)
	}

	name := fmt.Sprintf("dthcms_test_%d_%d", os.Getpid(), time.Now().UnixNano()%1_000_000)
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		t.Fatalf("creating test database: %v", err)
	}

	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		// Terminate stragglers first; a lingering connection makes DROP DATABASE fail
		// and leaves a database behind on every run.
		_, _ = admin.ExecContext(dropCtx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, name)
		if _, err := admin.ExecContext(dropCtx, `DROP DATABASE IF EXISTS "`+name+`"`); err != nil {
			t.Logf("could not drop test database %s: %v", name, err)
		}
	})

	return ctx, withDatabase(t, base, name)
}

func withDatabase(t *testing.T, base, database string) string {
	t.Helper()
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("%s is not a URL: %v", testURLEnv, err)
	}
	parsed.Path = "/" + database
	return parsed.String()
}

func open(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening %s: %v", redact(dsn), err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// openAs connects as one of the restricted development roles, keeping the host, port and
// database of the test database.
func openAs(t *testing.T, dsn, role string) *sql.DB {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parsing dsn: %v", err)
	}
	parsed.User = url.UserPassword(role, "dthcms_local_only")

	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatalf("opening as %s: %v", role, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Ping(); err != nil {
		t.Fatalf("connecting as %s: %v", role, err)
	}
	return db
}

func mustExec(t *testing.T, ctx context.Context, db *sql.DB, stmt string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		t.Fatalf("executing %.60s...: %v", strings.TrimSpace(stmt), err)
	}
}

func sqlState(err error) string {
	var stater interface{ SQLState() string }
	if errors.As(err, &stater) {
		return stater.SQLState()
	}
	return "none"
}

// copyFSWithEdit reproduces the embedded migrations with one file altered, standing in
// for someone editing a migration that has already run.
func copyFSWithEdit(t *testing.T, src fs.FS, filename, suffix string) fs.FS {
	t.Helper()

	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		t.Fatalf("reading migrations: %v", err)
	}

	out := fstest.MapFS{}
	var found bool
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, err := fs.ReadFile(src, entry.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		if entry.Name() == filename {
			data = append(data, []byte(suffix)...)
			found = true
		}
		out[entry.Name()] = &fstest.MapFile{Data: data}
	}
	if !found {
		t.Fatalf("%s is not among the migrations; the test needs updating", filename)
	}
	return out
}

func redact(dsn string) string {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "(unparseable dsn)"
	}
	parsed.User = url.User("...")
	return parsed.String()
}
