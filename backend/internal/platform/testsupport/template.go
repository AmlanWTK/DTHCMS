package testsupport

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
	"os"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/migrate"
	"github.com/AmlanWTK/DTHCMS/backend/migrations"
)

// A template database, so that the migrations run once for the whole suite rather than
// once for every test that wants a database.
//
// # Why this exists
//
// `Postgres(t)` promises each test its own database with the real schema in it, and the
// first implementation kept that promise the obvious way: create a database, run all fifty
// migrations, hand it over. On a Linux host talking to a local server that costs about a
// second and a half, and nobody noticed. On Windows with Docker Desktop every statement
// crosses a port proxy into a virtual machine, the same run costs twenty to thirty seconds,
// and a package with forty database tests spends twenty minutes applying the same
// migrations forty times — past Go's ten-minute per-package timeout, which is reported as
// a panic in whichever test happened to be running.
//
// So a suite that was merely slow on one machine was **unrunnable** on another, and the
// consequence is the one `docs/testing.md` argues against at length: a suite people stop
// running is a suite that stops being true.
//
// PostgreSQL can copy a database at the file level — `CREATE DATABASE x TEMPLATE y` — which
// costs a few hundred milliseconds regardless of how many migrations built `y`. So the
// migrations run once into a template, and every test after that gets a copy.
//
// # The template is named after its contents, and that is the safety property
//
// A fixed name would be faster to write and would serve a **stale schema** the moment
// somebody edited a migration: the template exists, so it is reused, so the tests pass
// against last week's database. That is the same failure as a suite that skips silently —
// a green run that checked nothing — and this package exists to prevent exactly that.
//
// The name therefore carries a hash of every migration file's name and bytes. Change a
// migration and the hash changes, no template with that name exists, and one is built. The
// old one is left alone rather than dropped, because another `go test ./...` may be part
// way through using it; `make test-templates-drop` removes them when they accumulate.
//
// # Building it exactly once, across processes
//
// `go test ./...` runs each package as its own process, in parallel, so a `sync.Once` would
// build the template once per package — better, but still forty-odd times for the suite. A
// PostgreSQL advisory lock keyed on the hash is the cross-process version: the first process
// to arrive builds it while the others wait, and they all then copy.
//
// The build happens under a **provisional name** and is renamed on success. A process killed
// half way through migration would otherwise leave a partly-migrated database under the
// final name, which every later test would copy and trust — the stale-schema failure again,
// arriving through a crash instead of an edit.

// NoTemplateEnv disables all of this and goes back to migrating each test's own database.
//
// For when the template machinery is itself under suspicion: if a test passes with this set
// and fails without it, the fault is here rather than in the code under test, and that is
// worth being able to establish in one command.
const NoTemplateEnv = "DTHCMS_TEST_NO_TEMPLATE"

// templateLockKey is the advisory-lock namespace. Arbitrary and constant; it only has to
// avoid colliding with another advisory lock on the same server, and this suite takes no
// others.
const templateLockKey int64 = 0x4454_4843 // "DTHC"

// templateName is the current migrations' template database.
//
// Computed rather than stored, so there is no cached answer to go stale. Twelve hex digits
// of SHA-256 is far more than enough to distinguish the handful of schema versions a
// developer's machine will ever hold, and keeps the name well inside PostgreSQL's 63-byte
// identifier limit.
func templateName(fsys fs.FS) (string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return "", fmt.Errorf("reading the migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	// Sorted, because fs.ReadDir's order is only guaranteed for embed.FS and this function
	// should give the same answer for the same content whatever the FS.
	sort.Strings(names)

	sum := sha256.New()
	for _, name := range names {
		body, err := fs.ReadFile(fsys, path.Join(".", name))
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", name, err)
		}
		// The name as well as the bytes: renaming 00050 to 00051 changes which migrations
		// run in which order, and a hash over contents alone would call that the same schema.
		fmt.Fprintf(sum, "%s\x00%d\x00", name, len(body))
		sum.Write(body)
	}
	return "dthcms_template_" + hex.EncodeToString(sum.Sum(nil))[:12], nil
}

// ensureTemplate returns the name of a database holding every migration, building it if
// this is the first test on this server to ask.
func ensureTemplate(t *testing.T, admin *sql.DB, base string) string {
	t.Helper()

	name, err := templateName(migrations.FS)
	if err != nil {
		t.Fatalf("naming the template database: %v", err)
	}

	// Generous, and deliberately so: on the machine this was written for, building the
	// template is the twenty-to-thirty-second migration run that used to happen per test.
	// Every other test waits on the advisory lock while it happens.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	if exists(t, ctx, admin, name) {
		return name
	}

	// One builder. The lock is held on a dedicated connection, because an advisory lock
	// belongs to a session and `*sql.DB` is a pool that would hand the unlock to a
	// different connection than the lock.
	conn, err := admin.Conn(ctx)
	if err != nil {
		t.Fatalf("taking a connection for the template lock: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1, $2)`,
		templateLockKey, lockPart(name)); err != nil {
		t.Fatalf("locking to build the template database: %v", err)
	}
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer unlockCancel()
		_, _ = conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock($1, $2)`,
			templateLockKey, lockPart(name))
	}()

	// Someone else built it while we queued.
	if exists(t, ctx, admin, name) {
		return name
	}

	// Built under a provisional name and renamed on success, so that a process killed part
	// way through leaves nothing that looks finished. A leftover `_building` database is
	// visible, is never copied, and is dropped by the next builder.
	building := name + "_building"
	if _, err := admin.ExecContext(ctx, `DROP DATABASE IF EXISTS "`+building+`"`); err != nil {
		t.Fatalf("clearing a previous half-built template: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE "`+building+`"`); err != nil {
		t.Fatalf("creating the template database: %v", err)
	}

	runner, err := migrate.New(migrate.Options{
		FS:     migrations.FS,
		DSN:    withDatabase(t, base, building),
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("building the migration runner: %v", err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("applying migrations to the template database: %v", err)
	}

	// Nothing may be connected to a database being copied, and the migration runner has
	// just been connected to this one. Its own pool is closed by now; this catches anything
	// the driver kept.
	disconnect(ctx, admin, building)

	if _, err := admin.ExecContext(ctx,
		`ALTER DATABASE "`+building+`" RENAME TO "`+name+`"`); err != nil {
		t.Fatalf("naming the finished template database: %v", err)
	}
	return name
}

// copyTemplate creates this test's database as a file-level copy of the template.
func copyTemplate(t *testing.T, admin *sql.DB, template, name string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Retried, because `CREATE DATABASE ... TEMPLATE` refuses while anything is connected to
	// the source, and something transiently can be — a `psql` somebody left open, or a
	// connection from a sibling package that has not finished closing. Retrying is right and
	// failing is not: the condition clears on its own within moments, and a flake in the
	// harness is indistinguishable, to whoever reads the output, from a flake in the system.
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		_, err := admin.ExecContext(ctx,
			`CREATE DATABASE "`+name+`" TEMPLATE "`+template+`"`)
		if err == nil {
			return
		}
		lastErr = err
		if !strings.Contains(err.Error(), "being accessed by other users") {
			break
		}
		disconnect(ctx, admin, template)
		select {
		case <-ctx.Done():
			t.Fatalf("copying the template database: %v", ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatalf("copying the template database %s into %s: %v", template, name, lastErr)
}

func exists(t *testing.T, ctx context.Context, admin *sql.DB, name string) bool {
	t.Helper()

	var found bool
	err := admin.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&found)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("looking for the template database: %v", err)
	}
	return found
}

// disconnect ends every session in a database, so that it can be copied or dropped.
func disconnect(ctx context.Context, admin *sql.DB, name string) {
	_, _ = admin.ExecContext(ctx,
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		  WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
}

// lockPart turns the template's name into the second half of an advisory-lock key.
//
// **Thirty-one bits, not thirty-two**, and the missing bit is the whole story. `pg_advisory_lock`
// takes two `int4`s; the first version of this took four bytes of the hash into a `uint32` and
// widened that to `int64`, which is positive as the comment claimed and is larger than `int4` can
// hold whenever the first byte of the hash has its top bit set. That is half of all schema
// versions, and the failure is total: every database test in the repository fails at the moment it
// asks for the template, with an encoding error that mentions neither templates nor locks.
//
// It survived from CP04 to CP70 because it depends on the *hash of the migration set*, so it lay
// dormant through fifty-one migrations and then appeared, on an unrelated change, as "nothing
// works". Masking the top bit costs one bit of a key that only has to be the same number for the
// same name.
func lockPart(name string) int64 {
	sum := sha256.Sum256([]byte(name))
	return int64(uint32(sum[0])<<24|uint32(sum[1])<<16|uint32(sum[2])<<8|uint32(sum[3])) & 0x7fffffff
}

// templatesDisabled reports whether the escape hatch is set.
func templatesDisabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(NoTemplateEnv)))
	return value != "" && value != "0" && value != "false"
}
