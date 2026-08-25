// Package testsupport is the integration-test harness every later checkpoint builds on.
//
// The rule it exists to enforce is isolation: a test gets its own database, created before
// it runs and dropped after, so that tests may run in parallel and none of them can see
// another's rows. Shared fixtures are how an integration suite becomes a suite that must
// run in one order, on one machine, and eventually not at all.
//
// It needs a real PostgreSQL and a real Redis. Neither is mocked, because everything worth
// asserting here is a property of the real thing — the privilege system that makes the
// ledger append-only, the constraints, the transaction semantics. A mock would only
// confirm that the mock agrees with the test.
//
//	DTHCMS_TEST_POSTGRES_URL=postgres://dthcms:dthcms_local_only@127.0.0.1:5433/postgres?sslmode=disable
//	DTHCMS_TEST_REDIS_URL=redis://127.0.0.1:6380
//
// Both come up with `make up`. Without them, tests that use this package skip with a
// message saying so rather than failing — a suite that cannot run on a fresh clone is a
// suite people learn to ignore, and a red build nobody can fix is worse than a skipped one.
//
// # On testcontainers
//
// The implementation plan names testcontainers-go. It is deliberately not used yet, and
// the reasoning is recorded in docs/testing.md: CP04's compose stack already provides both
// services, CI already declares them, and testcontainers' contribution is convenience
// rather than capability. Adding it is a small change confined to this package — provision
// a container when the environment variables are absent — and it can be made the day
// somebody is annoyed enough by typing `make up`.
package testsupport

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/migrate"
	"github.com/AmlanWTK/DTHCMS/backend/migrations"
)

// PostgresURLEnv names the server tests connect to. The role must be able to CREATE
// DATABASE, because every test makes its own.
const PostgresURLEnv = "DTHCMS_TEST_POSTGRES_URL"

// sequence disambiguates databases created in the same nanosecond by parallel tests.
var sequence atomic.Uint64

// DB is a test's private database.
type DB struct {
	// SQL is connected to the fresh database, as the owning role.
	SQL *sql.DB
	// DSN addresses that database. Pass it to anything that opens its own pool.
	DSN string
	// Name is the database's name, for error messages and for OpenAs.
	Name string
}

// Postgres gives the test its own database with every migration applied.
//
// This is what a domain test wants. It costs roughly the time of the migration run, which
// is the price of knowing that the schema under test is the schema in migrations/ rather
// than whatever a previous test left behind.
func Postgres(t *testing.T) *DB {
	t.Helper()

	db := FreshDatabase(t)
	runner, err := migrate.New(migrate.Options{
		FS:     migrations.FS,
		DSN:    db.DSN,
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("building the migration runner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := runner.Up(ctx); err != nil {
		t.Fatalf("applying migrations to %s: %v", db.Name, err)
	}
	return db
}

// FreshDatabase gives the test its own empty database, without migrations.
//
// For the tests that are about migration itself, and for anything that wants to build a
// schema by hand.
func FreshDatabase(t *testing.T) *DB {
	t.Helper()

	base := os.Getenv(PostgresURLEnv)
	if base == "" {
		t.Skipf("set %s to run integration tests — `make up` starts one "+
			"(see internal/platform/testsupport)", PostgresURLEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("connecting to %s: %v", PostgresURLEnv, err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	if err := admin.PingContext(ctx); err != nil {
		t.Fatalf("cannot reach the test database server: %v", err)
	}

	name := databaseName()
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		t.Fatalf("creating test database %s: %v", name, err)
	}

	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		// Terminate stragglers first. A lingering connection makes DROP DATABASE fail,
		// and a suite that leaks one database per run fills a developer's disk quietly.
		_, _ = admin.ExecContext(dropCtx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, name)
		if _, err := admin.ExecContext(dropCtx, `DROP DATABASE IF EXISTS "`+name+`"`); err != nil {
			t.Logf("could not drop test database %s: %v", name, err)
		}
	})

	dsn := withDatabase(t, base, name)
	return &DB{SQL: openDSN(t, dsn), DSN: dsn, Name: name}
}

// OpenAs connects to the same database as a different role.
//
// The point of the test suite that uses this: the ledger is append-only because of a
// database privilege, not because the application is careful. Proving that means
// connecting as the application's role and being refused.
func (d *DB) OpenAs(t *testing.T, role, password string) *sql.DB {
	t.Helper()

	parsed, err := url.Parse(d.DSN)
	if err != nil {
		t.Fatalf("parsing dsn: %v", err)
	}
	parsed.User = url.UserPassword(role, password)

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

// databaseName is unique per process, per test, and short enough for PostgreSQL's
// 63-byte identifier limit.
func databaseName() string {
	return fmt.Sprintf("dthcms_test_%d_%d", os.Getpid(), sequence.Add(1))
}

func withDatabase(t *testing.T, base, name string) string {
	t.Helper()

	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parsing %s: %v", PostgresURLEnv, err)
	}
	parsed.Path = "/" + name
	return parsed.String()
}

func openDSN(t *testing.T, dsn string) *sql.DB {
	t.Helper()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening %s: %v", Redact(dsn), err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Redact removes the password from a connection string before it reaches a log or a test
// failure. Test output is pasted into issues far more often than anyone intends.
func Redact(dsn string) string {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "(unparseable dsn)"
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			parsed.User = url.UserPassword(parsed.User.Username(), "xxxxx")
		}
	}
	return parsed.String()
}

// Seed executes SQL against the test's database, failing the test if any statement does.
//
// Entity builders — a patient with a visit, a visit with observations — arrive with the
// entities themselves, from CP29 onward. Inventing them now would mean building fixtures
// for tables that do not exist. This is the piece that is useful before then.
func (d *DB) Seed(t *testing.T, statements ...string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for i, statement := range statements {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := d.SQL.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seeding statement %d: %v", i+1, err)
		}
	}
}
