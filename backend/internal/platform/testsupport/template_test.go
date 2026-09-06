package testsupport

import (
	"context"
	"database/sql"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"time"
)

// The template must produce the same database the migrations would have.
//
// This is the test that makes the optimisation safe to have. `Postgres(t)` no longer runs the
// migrations; it copies a database that was built from them once, and every guarantee the
// suite makes now rests on that copy being indistinguishable from the real thing. If it is
// not — an object that does not survive a template copy, a stale template served after a
// migration was edited — then every DB-backed test in this repository is asserting things
// about the wrong schema, and passing.
//
// That is the same failure as a suite that skips silently, which is the failure this package
// spends its comments warning about. So it is checked rather than reasoned about.

// fingerprint is everything about a database's shape that a test could depend on.
//
// Deliberately more than "the tables are the same". Most of what this repository proves in
// SQL is proved with the things a shallower comparison would miss: privileges (the ledger is
// append-only because `dthcms_app` lacks DELETE), constraints and triggers (the quarantine
// cap, the PHI check), and functions (the invariants, the permitted-exercise filter). A
// fingerprint that compared only column names would pass while the interesting half of the
// schema was absent.
func fingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	queries := []string{
		// Columns, with their types and nullability and defaults.
		`SELECT table_schema, table_name, column_name, data_type, is_nullable,
		        coalesce(column_default, '')
		   FROM information_schema.columns
		  WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
		  ORDER BY 1, 2, 3`,
		// Constraints, including the CHECKs that carry the rules.
		`SELECT n.nspname, t.relname, c.conname, pg_get_constraintdef(c.oid)
		   FROM pg_constraint c
		   JOIN pg_class t ON t.oid = c.conrelid
		   JOIN pg_namespace n ON n.oid = t.relnamespace
		  WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
		  ORDER BY 1, 2, 3`,
		// Indexes.
		`SELECT schemaname, tablename, indexname, indexdef
		   FROM pg_indexes
		  WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
		  ORDER BY 1, 2, 3`,
		// Triggers.
		`SELECT n.nspname, t.relname, g.tgname, pg_get_triggerdef(g.oid)
		   FROM pg_trigger g
		   JOIN pg_class t ON t.oid = g.tgrelid
		   JOIN pg_namespace n ON n.oid = t.relnamespace
		  WHERE NOT g.tgisinternal
		    AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		  ORDER BY 1, 2, 3`,
		// Functions, whole. The invariants and the projections live here.
		`SELECT n.nspname, p.proname, md5(pg_get_functiondef(p.oid))
		   FROM pg_proc p
		   JOIN pg_namespace n ON n.oid = p.pronamespace
		  WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
		    AND p.prokind IN ('f', 'p')
		  ORDER BY 1, 2, 3`,
		// Table privileges. The append-only ledger is a privilege, not a convention.
		`SELECT table_schema, table_name, grantee, privilege_type
		   FROM information_schema.role_table_grants
		  WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
		  ORDER BY 1, 2, 3, 4`,
		// Extensions, which are per-database objects and would be a plausible thing for a
		// template copy to lose.
		`SELECT extname FROM pg_extension ORDER BY 1`,
		// The seeded catalogues every domain test reads: permissions, roles, invariants,
		// event types. A schema can be identical while the rows a test needs are missing.
		`SELECT 'permission', count(*)::text FROM core.permission
		  UNION ALL SELECT 'role', count(*)::text FROM core.role
		  UNION ALL SELECT 'invariant', count(*)::text FROM ops.invariant
		  UNION ALL SELECT 'migration', count(*)::text FROM ops.schema_migration
		  ORDER BY 1`,
	}

	var out strings.Builder
	for _, query := range queries {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			t.Fatalf("fingerprinting: %v", err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatalf("fingerprinting: %v", err)
		}
		for rows.Next() {
			cells := make([]any, len(columns))
			for i := range cells {
				cells[i] = new(sql.NullString)
			}
			if err := rows.Scan(cells...); err != nil {
				t.Fatalf("fingerprinting: %v", err)
			}
			for _, cell := range cells {
				out.WriteString(cell.(*sql.NullString).String)
				out.WriteByte('\x1f')
			}
			out.WriteByte('\n')
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("fingerprinting: %v", err)
		}
		_ = rows.Close()
	}
	return out.String()
}

func TestACopiedTemplateIsTheSameDatabaseTheMigrationsWouldHaveBuilt(t *testing.T) {
	t.Parallel()

	// The two paths, in the same process, so the comparison is of the code and not of two
	// runs that could have seen different migrations.
	copied := Postgres(t)
	migrated := migratedDatabase(t)

	want := fingerprint(t, migrated.SQL)
	got := fingerprint(t, copied.SQL)

	if got == want {
		return
	}

	// Not a diff of two hundred kilobytes. The first line that differs is what somebody needs.
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			t.Fatalf("a database copied from the template differs from one the migrations built.\n"+
				"Every DB-backed test in this repository now runs against the copy, so this is "+
				"the whole suite asserting things about the wrong schema.\n"+
				"first difference at line %d:\n  migrated: %q\n  copied:   %q", i+1, w, g)
		}
	}
	t.Fatal("the fingerprints differ but no line does; the fingerprint is wrong")
}

func TestTheTemplateIsNamedAfterTheMigrationsSoAnEditCannotBeMissed(t *testing.T) {
	t.Parallel()

	// The dangerous version of this optimisation is a fixed template name: the template
	// exists, so it is reused, so an edited migration never reaches a test and the suite goes
	// green against last week's schema. The name carries a hash of the migrations' bytes
	// precisely so that cannot happen, and this is that claim.
	before, err := templateName(fakeMigrations{"00001_a.sql": "CREATE TABLE a ();"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := templateName(fakeMigrations{"00001_a.sql": "CREATE TABLE a (id int);"})
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("editing a migration did not change the template's name: an edited migration " +
			"would be served from a template built before it")
	}

	// A renamed migration is a reordered migration, which is a different schema even when
	// every byte of every file is the same.
	renamed, err := templateName(fakeMigrations{"00002_a.sql": "CREATE TABLE a ();"})
	if err != nil {
		t.Fatal(err)
	}
	if renamed == before {
		t.Fatal("renaming a migration did not change the template's name")
	}

	// And the same content really is the same name, or every run rebuilds and the whole
	// exercise buys nothing.
	again, err := templateName(fakeMigrations{"00001_a.sql": "CREATE TABLE a ();"})
	if err != nil {
		t.Fatal(err)
	}
	if again != before {
		t.Fatal("the same migrations produced two different template names")
	}
}

// fakeMigrations is an in-memory migration set, for the naming test alone.
type fakeMigrations map[string]string

func (f fakeMigrations) Open(name string) (fs.File, error) {
	body, ok := f[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return &fakeFile{name: name, Reader: strings.NewReader(body)}, nil
}

func (f fakeMigrations) ReadDir(string) ([]fs.DirEntry, error) {
	names := make([]string, 0, len(f))
	for name := range f {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]fs.DirEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, fakeEntry{name: name})
	}
	return entries, nil
}

type fakeFile struct {
	name string
	*strings.Reader
}

func (f *fakeFile) Stat() (fs.FileInfo, error) { return fakeInfo{name: f.name, size: f.Size()}, nil }
func (f *fakeFile) Close() error               { return nil }

type fakeEntry struct{ name string }

func (e fakeEntry) Name() string               { return e.name }
func (e fakeEntry) IsDir() bool                { return false }
func (e fakeEntry) Type() fs.FileMode          { return 0 }
func (e fakeEntry) Info() (fs.FileInfo, error) { return fakeInfo{name: e.name}, nil }

type fakeInfo struct {
	name string
	size int64
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return i.size }
func (i fakeInfo) Mode() fs.FileMode  { return 0 }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return false }
func (i fakeInfo) Sys() any           { return nil }
