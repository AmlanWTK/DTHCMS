package testsupport_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
)

// The harness tests itself.
//
// CP13's scope asks for exactly this, and the reason is that isolation is the one property
// nothing else will catch. A suite whose tests quietly share a database passes for months
// and then fails on the day somebody adds a test that inserts a row — at which point the
// failure appears in an unrelated test, on CI only, and takes a day to understand.
//
// So the claim is made explicitly: two tests running at the same time cannot see each
// other's rows or each other's keys.

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return c
}

func TestEachTestGetsItsOwnDatabase(t *testing.T) {
	t.Parallel()

	first := testsupport.FreshDatabase(t)
	second := testsupport.FreshDatabase(t)

	if first.Name == second.Name {
		t.Fatalf("both databases are named %s — they are the same database", first.Name)
	}

	first.Seed(t, `CREATE TABLE isolation_probe (id int)`, `INSERT INTO isolation_probe VALUES (1)`)

	// The same table name, in the other database. If they shared one, this would fail as
	// "relation already exists" — which is precisely the failure this proves cannot happen.
	second.Seed(t, `CREATE TABLE isolation_probe (id int)`)

	var count int
	if err := second.SQL.QueryRowContext(ctx(t), `SELECT count(*) FROM isolation_probe`).Scan(&count); err != nil {
		t.Fatalf("counting rows in the second database: %v", err)
	}
	if count != 0 {
		t.Errorf("the second database sees %d rows from the first; it must see none", count)
	}
}

func TestMigrationsAreAppliedToAFreshDatabase(t *testing.T) {
	t.Parallel()

	db := testsupport.Postgres(t)

	// The schemas CP06 creates. A domain test that opens this database gets the schema in
	// migrations/, not whatever a previous test happened to leave behind.
	for _, schema := range []string{"core", "ledger", "read", "ops", "docs", "research"} {
		var exists bool
		if err := db.SQL.QueryRowContext(ctx(t),
			`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, schema).Scan(&exists); err != nil {
			t.Fatalf("checking schema %s: %v", schema, err)
		}
		if !exists {
			t.Errorf("schema %q is missing — migrations did not run", schema)
		}
	}
}

func TestParallelDatabasesDoNotCollide(t *testing.T) {
	// Two sibling tests, both parallel, both writing the same table name. They run at the
	// same time by construction; if the harness handed them one database, one of them
	// fails.
	for _, name := range []string{"alpha", "beta"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := testsupport.Postgres(t)
			db.Seed(t,
				`CREATE TABLE parallel_probe (who text)`,
				`INSERT INTO parallel_probe VALUES ('`+name+`')`)

			var who string
			if err := db.SQL.QueryRowContext(ctx(t), `SELECT who FROM parallel_probe`).Scan(&who); err != nil {
				t.Fatalf("reading back: %v", err)
			}
			if who != name {
				t.Errorf("this database holds %q, which belongs to the other test", who)
			}
		})
	}
}

func TestEachTestGetsItsOwnCacheKeyspace(t *testing.T) {
	t.Parallel()

	first := testsupport.Redis(t)
	second := testsupport.Redis(t)

	if first.Prefix == second.Prefix {
		t.Fatalf("both tests share the prefix %s", first.Prefix)
	}

	if err := first.Client.Set(ctx(t), first.Key("session"), "alpha", time.Minute).Err(); err != nil {
		t.Fatalf("writing: %v", err)
	}

	// The same logical key, in the other test's namespace.
	value, err := second.Client.Get(ctx(t), second.Key("session")).Result()
	if err == nil {
		t.Errorf("the second test reads %q from the first test's key", value)
	}
}

func TestRedactHidesThePassword(t *testing.T) {
	t.Parallel()

	// Test output is pasted into issues far more often than anyone intends.
	got := testsupport.Redact("postgres://dthcms:hunter2@127.0.0.1:5433/postgres?sslmode=disable")

	if got == "" || got == "(unparseable dsn)" {
		t.Fatalf("Redact returned %q", got)
	}
	if strings.Contains(got, "hunter2") {
		t.Errorf("Redact left the password in: %s", got)
	}
	if !strings.Contains(got, "127.0.0.1") {
		t.Errorf("Redact removed too much — the host is what makes the message useful: %s", got)
	}
}
