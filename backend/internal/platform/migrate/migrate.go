// Package migrate applies database migrations and verifies what they promised.
//
// Three things here are not standard migration-tool behaviour, and each exists because
// of a specific way migrations go wrong:
//
//   - An advisory lock is held for the whole run. Two instances starting at once during
//     a rolling deploy will otherwise both begin migrating, and the loser fails
//     somewhere in the middle of a transaction it did not expect to be in.
//
//   - A SHA-256 of every migration file is stored when it is applied, and checked on
//     every later run. A migration that is edited after it has run will never run again,
//     so the file and the database diverge in silence — and the next environment built
//     from scratch gets a schema that no existing environment has.
//
//   - core.assert_invariants() runs at the end of every run. The grants that make the
//     event ledger append-only depend on conditions no code review can see (see
//     migrations/00003_conventions.sql). Checking is cheap; finding out later is not.
package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pressly/goose/v3"

	// Registers the pgx driver under the name "pgx" for database/sql, which goose needs.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// lockKey identifies the DTHCMS migration lock. Any constant works as long as it never
// changes; this one is arbitrary and deliberately unlikely to collide with another
// application sharing the cluster.
const lockKey int64 = 8271394655120006

// versionTable is where goose records applied versions. It lives in ops rather than
// public because public is stripped of privileges in migration 00002, and because
// migration state is operational data like everything else in that schema.
const versionTable = "ops.schema_migration"

// Runner applies migrations from an embedded filesystem.
type Runner struct {
	fsys       fs.FS
	dsn        string
	logger     *slog.Logger
	production bool
}

// Options configures a Runner.
type Options struct {
	// FS holds the .sql files. Normally migrations.FS.
	FS fs.FS
	// DSN is the migration connection string. This is not the application's connection:
	// migrations create objects and grant privileges, which the application role must
	// not be able to do.
	DSN string
	// Logger receives progress. Required.
	Logger *slog.Logger
	// Production disables the operations that must never run against real patient data.
	Production bool
}

// New builds a Runner.
func New(opts Options) (*Runner, error) {
	if opts.FS == nil {
		return nil, errors.New("migrate: FS is required")
	}
	if opts.DSN == "" {
		return nil, errors.New("migrate: DSN is required")
	}
	if opts.Logger == nil {
		return nil, errors.New("migrate: Logger is required")
	}
	return &Runner{
		fsys:       opts.FS,
		dsn:        opts.DSN,
		logger:     opts.Logger,
		production: opts.Production,
	}, nil
}

// Up applies every pending migration, then verifies the database's invariants.
func (r *Runner) Up(ctx context.Context) error {
	return r.withLock(ctx, func(db *sql.DB) error {
		if err := r.verifyChecksums(ctx, db); err != nil {
			return err
		}

		before, err := currentVersion(ctx, db)
		if err != nil {
			return err
		}

		if err := bootstrapSchema(ctx, db); err != nil {
			return err
		}

		if err := r.configureGoose(); err != nil {
			return err
		}
		if err := goose.UpContext(ctx, db, "."); err != nil {
			return fmt.Errorf("applying migrations: %w", err)
		}

		after, err := currentVersion(ctx, db)
		if err != nil {
			return err
		}

		if after == before {
			r.logger.Info("database already up to date", "version", after)
		} else {
			r.logger.Info("migrations applied", "from_version", before, "to_version", after)
		}

		if err := r.recordChecksums(ctx, db); err != nil {
			return err
		}
		return r.verifyInvariants(ctx, db)
	})
}

// Status prints which migrations have been applied.
func (r *Runner) Status(ctx context.Context) error {
	return r.connect(ctx, func(db *sql.DB) error {
		if err := r.configureGoose(); err != nil {
			return err
		}
		return goose.StatusContext(ctx, db, ".")
	})
}

// Version reports the current schema version.
func (r *Runner) Version(ctx context.Context) (int64, error) {
	var version int64
	err := r.connect(ctx, func(db *sql.DB) error {
		v, err := currentVersion(ctx, db)
		version = v
		return err
	})
	return version, err
}

// Verify runs the invariant assertions without applying anything. Cheap enough to run
// from a health check or a nightly job.
func (r *Runner) Verify(ctx context.Context) error {
	return r.connect(ctx, func(db *sql.DB) error {
		if err := r.verifyChecksums(ctx, db); err != nil {
			return err
		}
		return r.verifyInvariants(ctx, db)
	})
}

// Down rolls back the most recent migration.
//
// It refuses in production. Rollback is a development and test facility: a down
// migration that drops a column drops the data in it, and no clinical record should be
// destroyed by a command whose safety depends on the operator having read the file
// first. Production recovery is restore-from-backup plus a forward migration.
func (r *Runner) Down(ctx context.Context) error {
	if r.production {
		return errors.New(
			"migrate: down migrations are refused in production; " +
				"recover by restoring a backup and applying a forward migration")
	}
	return r.withLock(ctx, func(db *sql.DB) error {
		if err := r.configureGoose(); err != nil {
			return err
		}
		if err := goose.DownContext(ctx, db, "."); err != nil {
			return fmt.Errorf("rolling back: %w", err)
		}

		version, err := currentVersion(ctx, db)
		if err != nil {
			return err
		}
		r.logger.Info("rolled back one migration", "version", version)

		// The rolled-back version's checksum no longer describes anything applied.
		if _, err := db.ExecContext(ctx,
			`DELETE FROM ops.migration_checksum WHERE version > $1`, version); err != nil &&
			!isUndefinedTable(err) {
			return fmt.Errorf("clearing checksums: %w", err)
		}
		return nil
	})
}

// EnsureDevRoles creates login roles for local development and grants them membership of
// the group roles created by migration 00002.
//
// It refuses in production, where credentials are issued by the platform and never by a
// migration binary holding a hard-coded password.
//
// Its purpose is that the application on a developer's machine runs with exactly the
// privileges it has in production. A developer connecting as the owning superuser can
// UPDATE the ledger all day and discover the restriction only when staging rejects it —
// which is to say, after the code that depends on it is written.
func (r *Runner) EnsureDevRoles(ctx context.Context, password string) error {
	if r.production {
		return errors.New("migrate: dev-roles is refused in production")
	}
	if password == "" {
		return errors.New("migrate: dev-roles requires a password")
	}
	if strings.ContainsAny(password, "'\\") {
		return errors.New("migrate: dev-roles password may not contain quotes or backslashes")
	}

	roles := []struct{ login, group string }{
		{"dthcms_app_local", "dthcms_app"},
		{"dthcms_projector_local", "dthcms_projector"},
		{"dthcms_research_local", "dthcms_research"},
	}

	return r.connect(ctx, func(db *sql.DB) error {
		for _, role := range roles {
			// Identifiers are constants above, and the password is checked for quoting
			// characters, so this string is not attacker-influenced. CREATE ROLE cannot
			// take parameters, which is why it is built this way at all.
			stmt := fmt.Sprintf(`
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%[1]s') THEN
    CREATE ROLE %[1]s LOGIN PASSWORD '%[2]s';
  ELSE
    ALTER ROLE %[1]s LOGIN PASSWORD '%[2]s';
  END IF;
  GRANT %[3]s TO %[1]s;
END
$$;`, role.login, password, role.group)

			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("creating role %s: %w", role.login, err)
			}
			r.logger.Info("development login role ready", "role", role.login, "member_of", role.group)
		}

		// The database must also be connectable by them.
		var database string
		if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
			return fmt.Errorf("reading current database: %w", err)
		}
		for _, role := range roles {
			if _, err := db.ExecContext(ctx,
				fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s", quoteIdent(database), role.login)); err != nil {
				return fmt.Errorf("granting connect to %s: %w", role.login, err)
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------------

func (r *Runner) configureGoose() error {
	goose.SetBaseFS(r.fsys)
	goose.SetTableName(versionTable)
	goose.SetLogger(gooseLogger{r.logger})
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("configuring goose: %w", err)
	}
	return nil
}

// bootstrapSchema creates the schema that holds the version table.
//
// goose creates its version table before it applies the first migration, so the schema
// that table lives in cannot itself be created by a migration. The alternative is to
// leave the version table in public — which migration 00002 strips of privileges, and
// which is the one schema every other application on a shared cluster also writes to.
// One CREATE SCHEMA IF NOT EXISTS is the smaller compromise.
func bootstrapSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS ops"); err != nil {
		return fmt.Errorf("creating the ops schema for migration bookkeeping: %w", err)
	}
	return nil
}

func (r *Runner) connect(ctx context.Context, fn func(*sql.DB) error) error {
	db, err := sql.Open("pgx", r.dsn)
	if err != nil {
		return fmt.Errorf("opening migration connection: %w", err)
	}
	defer func() { _ = db.Close() }()

	// One connection: migrations, the advisory lock and the version table must all be
	// the same session, and a pool would hand them out at random.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("cannot reach postgres for migration: %w", err)
	}

	return fn(db)
}

func (r *Runner) withLock(ctx context.Context, fn func(*sql.DB) error) error {
	return r.connect(ctx, func(db *sql.DB) error {
		// pg_advisory_lock blocks. A deploy that waits for the previous one is correct;
		// a deploy that starts migrating alongside it is not.
		r.logger.Info("acquiring migration lock", "key", lockKey)
		if _, err := db.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
			return fmt.Errorf("acquiring migration lock: %w", err)
		}
		defer func() {
			// A fresh context: the caller's may already be cancelled, and failing to
			// release the lock would block every later migration on this cluster.
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if _, err := db.ExecContext(releaseCtx, "SELECT pg_advisory_unlock($1)", lockKey); err != nil {
				r.logger.Error("releasing migration lock", "error", err.Error())
			}
		}()

		return fn(db)
	})
}

func (r *Runner) verifyInvariants(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "SELECT core.assert_invariants()"); err != nil {
		if isUndefinedFunction(err) {
			// Before 00003 the assertions do not exist yet. That is only legitimate
			// mid-run on a database that has not reached 00003.
			r.logger.Warn("invariant assertions are not present yet",
				"note", "expected only before migration 00003 has been applied")
			return nil
		}
		return fmt.Errorf("database invariants are violated: %w", err)
	}
	// Read back rather than hard-coded. The previous version listed four guarantees in a
	// string literal; CP15 added two more, so six ran and the log named four. A log line
	// that reports what it verified has to report what it actually verified, or the person
	// reading it mid-incident is misled by a system that looks precise.
	var checked string
	if err := db.QueryRowContext(ctx, "SELECT core.invariants_checked()").Scan(&checked); err != nil {
		// Before 00007 the function does not exist. Not worth failing a migration over.
		checked = "see ops.invariant"
	}
	r.logger.Info("database invariants verified", "checked", checked)
	return nil
}

// checksums hashes every embedded migration file, keyed by version.
func (r *Runner) checksums() (map[int64]fileSum, error) {
	entries, err := fs.ReadDir(r.fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("reading migrations: %w", err)
	}

	out := make(map[int64]fileSum)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		version, err := versionOf(name)
		if err != nil {
			return nil, err
		}
		data, err := fs.ReadFile(r.fsys, name)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		sum := sha256.Sum256(data)
		out[version] = fileSum{Name: name, SHA256: hex.EncodeToString(sum[:])}
	}
	if len(out) == 0 {
		return nil, errors.New("migrate: no migration files found; the embed is empty")
	}
	return out, nil
}

type fileSum struct {
	Name   string
	SHA256 string
}

func (r *Runner) verifyChecksums(ctx context.Context, db *sql.DB) error {
	onDisk, err := r.checksums()
	if err != nil {
		return err
	}

	rows, err := db.QueryContext(ctx,
		`SELECT version, filename, sha256 FROM ops.migration_checksum ORDER BY version`)
	if err != nil {
		if isUndefinedTable(err) || isUndefinedSchema(err) {
			// First run against an empty database. Nothing to compare against yet.
			return nil
		}
		return fmt.Errorf("reading migration checksums: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var drift []string
	for rows.Next() {
		var version int64
		var filename, stored string
		if err := rows.Scan(&version, &filename, &stored); err != nil {
			return fmt.Errorf("reading migration checksums: %w", err)
		}
		current, present := onDisk[version]
		switch {
		case !present:
			drift = append(drift, fmt.Sprintf(
				"%s (version %d) was applied to this database but is no longer in the repository",
				filename, version))
		case current.SHA256 != stored:
			drift = append(drift, fmt.Sprintf(
				"%s (version %d) has changed since it was applied", current.Name, version))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading migration checksums: %w", err)
	}

	if len(drift) > 0 {
		sort.Strings(drift)
		return fmt.Errorf(
			"migration files have changed after being applied:\n  - %s\n"+
				"An applied migration never runs again, so this database and the repository "+
				"now describe different schemas. Revert the edit and add a new migration instead",
			strings.Join(drift, "\n  - "))
	}
	return nil
}

func (r *Runner) recordChecksums(ctx context.Context, db *sql.DB) error {
	onDisk, err := r.checksums()
	if err != nil {
		return err
	}

	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}

	for version := range applied {
		sum, present := onDisk[version]
		if !present {
			continue
		}
		if _, err := db.ExecContext(ctx, `
INSERT INTO ops.migration_checksum (version, filename, sha256)
VALUES ($1, $2, $3)
ON CONFLICT (version) DO NOTHING`, version, sum.Name, sum.SHA256); err != nil {
			if isUndefinedTable(err) || isUndefinedSchema(err) {
				return nil
			}
			return fmt.Errorf("recording checksum for %s: %w", sum.Name, err)
		}
	}
	return nil
}

func appliedVersions(ctx context.Context, db *sql.DB) (map[int64]bool, error) {
	rows, err := db.QueryContext(ctx,
		fmt.Sprintf("SELECT version_id FROM %s WHERE is_applied", versionTable))
	if err != nil {
		if isUndefinedTable(err) || isUndefinedSchema(err) {
			return map[int64]bool{}, nil
		}
		return nil, fmt.Errorf("reading applied versions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64]bool{}
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("reading applied versions: %w", err)
		}
		out[version] = true
	}
	return out, rows.Err()
}

func currentVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var version sql.NullInt64
	err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT max(version_id) FROM %s WHERE is_applied", versionTable)).Scan(&version)
	if err != nil {
		if isUndefinedTable(err) || isUndefinedSchema(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading schema version: %w", err)
	}
	if !version.Valid {
		return 0, nil
	}
	return version.Int64, nil
}

// versionOf extracts the leading numeric version from a goose filename.
func versionOf(name string) (int64, error) {
	digits := name
	if i := strings.IndexAny(name, "_-"); i > 0 {
		digits = name[:i]
	}
	version, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf(
			"migration %q does not start with a version number (expected 00005_description.sql)", name)
	}
	return version, nil
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// PostgreSQL error codes. Matching on the code rather than the message keeps this
// working when the server's locale is not English.
func isUndefinedTable(err error) bool    { return hasSQLState(err, "42P01") }
func isUndefinedSchema(err error) bool   { return hasSQLState(err, "3F000") }
func isUndefinedFunction(err error) bool { return hasSQLState(err, "42883") }

func hasSQLState(err error, code string) bool {
	var stater interface{ SQLState() string }
	if errors.As(err, &stater) {
		return stater.SQLState() == code
	}
	return false
}

// gooseLogger routes goose's own output into the structured logger, so a migration run
// produces one log stream rather than two interleaved ones.
type gooseLogger struct{ logger *slog.Logger }

func (g gooseLogger) Printf(format string, v ...any) {
	g.logger.Info("goose: " + strings.TrimSpace(fmt.Sprintf(format, v...)))
}

func (g gooseLogger) Fatalf(format string, v ...any) {
	// Never exits the process. goose calls this on failures it also returns as errors,
	// and a library that kills the program removes the caller's chance to clean up.
	g.logger.Error("goose: " + strings.TrimSpace(fmt.Sprintf(format, v...)))
}
