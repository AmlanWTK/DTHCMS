package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// The API, on the role the API actually deploys with.
//
// # The gap this closes, which cost a day
//
// `internal/platform/migrate` has tested the privilege system since CP06, and tests it well:
// the application may not UPDATE the ledger, may not write a read model, and is refused with
// SQLSTATE 42501 rather than merely declining to try. Those tests are the reason the
// append-only guarantee is a property of the database rather than a habit of the code.
//
// Every one of them runs against a **probe table the test creates itself**. So the suite
// proved the policy held on `ledger.probe_event`, and nothing anywhere proved that
// `dthcms_app` could do the things `cmd/api` does. Meanwhile every domain test connects as
// the schema owner, for whom privileges are not a question.
//
// Two privileges were missing for as long as they had existed, and both stopped the running
// API dead:
//
//   - `REVOKE ALL ON SCHEMA public FROM PUBLIC` (00002) took USAGE with it, and pg_trgm lives
//     in `public`. Every patient search and every registration failed — registration too,
//     because the duplicate check runs the same operator.
//   - `read.apply_observation` was granted to `dthcms_projector` alone (00026). But the
//     observation projection is **synchronous**: `dthcms_app` runs it inside the append
//     transaction. Every clinical value recorded through the API failed.
//
// Neither is subtle. Both survived because the seam between "the role the tests use" and "the
// role the deployment uses" had nothing standing on it. That is what this file is.
//
// # Why it asserts against derived lists rather than a list somebody maintains
//
// A hand-written list of required privileges is a list that goes stale on the day somebody
// adds a projection, which is exactly the day it needed to be right. So the synchronous
// projections come from `projection.Default` — the same registry `clinicalStore` passes to
// the event store — and the function names come from the projection source itself. Add a
// synchronous projection tomorrow and this test covers it without being edited.

const appRole = "dthcms_app_local"

// appRolePassword is what `migrate dev-roles` sets in the local stack and in CI.
const appRolePassword = "dthcms_local_only"

// applyCall finds every `read.apply_*` named anywhere in the projection source.
//
// **It was narrower, and the narrow version had exactly the hole this file exists to close.**
// Most projections call a helper — `call(ctx, tx, "read.apply_x", …)` — so the first version
// matched a double-quoted literal, which looked principled and derived. It missed
// `read.apply_observation`, because that projection inlines its SQL in a backtick string
// instead. So a test written to catch the day `read.apply_observation` lost its grant did not
// catch a deliberate replay of that exact bug: it reported four of five conventions and
// called itself complete.
//
// It now matches the identifier in any context — quoted, back-quoted, or in a comment. A
// comment that names a function costs one harmless assertion; a call form nobody anticipated
// costs a clinical write failing in a clinic. The asymmetry decides the pattern.
var applyCall = regexp.MustCompile(`read\.apply_[a-z0-9_]+`)

func TestTheApplicationRoleCanRunEverySynchronousProjection(t *testing.T) {
	db := testsupport.Postgres(t)
	mustCreateDevRoles(t, db)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	app := db.OpenAs(t, appRole, appRolePassword)

	// The functions the synchronous set will call, read out of the source that calls them.
	functions := synchronousApplyFunctions(t)
	if len(functions) == 0 {
		t.Fatal("found no read.apply_* functions in the projection source; the convention this " +
			"test reads has changed and the test no longer checks anything")
	}

	var missing []string
	for _, fn := range functions {
		var permitted bool
		// has_function_privilege wants a signature; every apply function takes one jsonb.
		err := app.QueryRowContext(ctx,
			`SELECT has_function_privilege($1, $2 || '(jsonb)', 'EXECUTE')`, appRole, fn).Scan(&permitted)
		if err != nil {
			// A function named in the source that does not exist in the database is its own
			// bug — a projection calling something no migration created.
			t.Errorf("%s: %v", fn, err)
			continue
		}
		if !permitted {
			missing = append(missing, fn)
		}
	}

	if len(missing) > 0 {
		t.Fatalf("%s cannot execute %d of the %d functions the synchronous projections call: %s.\n"+
			"These run inside the append transaction, as the application, so every clinical write "+
			"that touches one of them fails with \"permission denied for function\" — in production, "+
			"at a station, while somebody is taking a blood pressure.",
			appRole, len(missing), len(functions), strings.Join(missing, ", "))
	}
}

// TestTheApplicationRoleCanAppendAClinicalValue is the same claim made the expensive way.
//
// The privilege check above is fast and names the offending function. This one proves the
// whole path: the real composition-root store, the real synchronous set, a real event, on a
// pool connected as the deployed role. It is the test that would have failed on the morning
// `read.apply_observation` was revoked, whatever a privilege table said.
func TestTheApplicationRoleCanAppendAClinicalValue(t *testing.T) {
	db := testsupport.Postgres(t)
	mustCreateDevRoles(t, db)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool := appPool(t, ctx, db)
	facility, operator, device, patient := seedForAppend(t, ctx, db)

	// clinicalStore is the composition root's own function — not a rebuild of it — so this
	// cannot pass while the API fails.
	store := clinicalStore(pool)

	payload, err := json.Marshal(map[string]any{
		"observation_id": uuid.New().String(),
		"facility_id":    facility.String(),
		"patient_id":     patient.String(),
		"code":           "BP_SYSTOLIC",
		"value":          126,
		"unit":           "mm[Hg]",
		"effective_at":   time.Now().UTC(),
		"source":         "STATION",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Append(ctx, eventstore.Envelope{
		EventID: uuid.New(), AggregateType: "PATIENT", AggregateID: patient,
		PatientID: &patient, EventType: "OBSERVATION_RECORDED", EventVersion: 1,
		OccurredAt: time.Now().UTC(),
		Actor:      eventstore.ActorForTest(operator, device, facility, "CLINICAL_ASSISTANT", "STN_VITALS"),
		Source:     eventstore.SourceWeb, Payload: payload,
	}); err != nil {
		t.Fatalf("appending a blood pressure as %s: %v\n"+
			"The synchronous projections run inside this transaction, as this role. A permission "+
			"failure here is every clinical write in the clinic failing.", appRole, err)
	}

	// And it reached the read model, which is the half a privilege check cannot see.
	var recorded int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM read.observation WHERE patient_id = $1`, patient).Scan(&recorded); err != nil {
		t.Fatalf("reading the projection back as %s: %v", appRole, err)
	}
	if recorded != 1 {
		t.Fatalf("read.observation holds %d rows for this patient, want 1: the append succeeded "+
			"and the projection did not run", recorded)
	}
}

// TestTheApplicationRoleCanSearchTheRegister covers the other half of what was broken.
//
// The duplicate check and the register search both use pg_trgm, which lives in `public`. The
// application needs USAGE on that schema to resolve `%` and `similarity()` at all, and a
// migration that revokes ALL from PUBLIC takes USAGE with it. The symptom is
// "operator does not exist: text % text" on every registration.
func TestTheApplicationRoleCanSearchTheRegister(t *testing.T) {
	db := testsupport.Postgres(t)
	mustCreateDevRoles(t, db)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	app := db.OpenAs(t, appRole, appRolePassword)

	var similar bool
	if err := app.QueryRowContext(ctx, `SELECT 'Fatema'::text % 'Fatima'::text`).Scan(&similar); err != nil {
		t.Fatalf("the trigram operator is unusable by %s: %v\n"+
			"Every patient search and every registration runs this — registration too, because "+
			"the duplicate check uses the same operator.", appRole, err)
	}

	var score float64
	if err := app.QueryRowContext(ctx,
		`SELECT similarity('Fatema Begum'::text, 'Fatima Begum'::text)`).Scan(&score); err != nil {
		t.Fatalf("similarity() is unusable by %s: %v", appRole, err)
	}
	if score <= 0 {
		t.Fatalf("similarity of two near-identical names is %v; the extension is present but "+
			"not behaving", score)
	}
}

// TestEveryExtensionTheSchemaInstallsIsUsableByTheApplication generalises the trigram case.
//
// pg_trgm was the one that bit, but pgcrypto and btree_gist are installed by the same
// migration into the same schema and would fail the same way. Derived from `pg_extension`
// rather than listed, so an extension added later is covered without this test being edited.
func TestEveryExtensionTheSchemaInstallsIsUsableByTheApplication(t *testing.T) {
	db := testsupport.Postgres(t)
	mustCreateDevRoles(t, db)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	owner := db.SQL
	rows, err := owner.QueryContext(ctx, `
		SELECT e.extname, n.nspname
		  FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace
		 WHERE e.extname <> 'plpgsql'
		 ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	type ext struct{ name, schema string }
	var installed []ext
	for rows.Next() {
		var e ext
		if err := rows.Scan(&e.name, &e.schema); err != nil {
			t.Fatal(err)
		}
		installed = append(installed, e)
	}
	_ = rows.Close()
	if len(installed) == 0 {
		t.Fatal("no extensions found; this test is checking nothing")
	}

	app := db.OpenAs(t, appRole, appRolePassword)
	for _, e := range installed {
		var permitted bool
		if err := app.QueryRowContext(ctx,
			`SELECT has_schema_privilege($1, $2, 'USAGE')`, appRole, e.schema).Scan(&permitted); err != nil {
			t.Fatal(err)
		}
		if !permitted {
			t.Errorf("%s has no USAGE on schema %q, where extension %q lives. Every operator and "+
				"function that extension provides is unresolvable to the application — which is "+
				"reported as \"operator does not exist\", not as a permission problem, so it reads "+
				"like a missing extension rather than a missing grant.", appRole, e.schema, e.name)
		}
	}
}

// --- helpers ---

// synchronousApplyFunctions reads the projection source for the SQL functions its synchronous
// members call.
//
// Source-scanned rather than listed for the reason in the file comment: a list is stale on the
// day a projection is added. It scans the whole package rather than trying to attribute each
// literal to a projection type — the attribution would be guesswork, and the cost of covering
// an asynchronous projection's function too is that the application is granted something it
// does not strictly need. That is the safe direction to be wrong in: the failure this test
// exists to catch is a *missing* grant during a clinical write, and the projector's exclusive
// right to write read models is enforced by table privileges, which this does not touch.
func synchronousApplyFunctions(t *testing.T) []string {
	t.Helper()

	dir := filepath.Join("..", "..", "internal", "projection")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the projection source: %v", err)
	}

	seen := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, match := range applyCall.FindAllString(string(body), -1) {
			seen[match] = true
		}
	}

	out := make([]string, 0, len(seen))
	for fn := range seen {
		out = append(out, fn)
	}
	sort.Strings(out)

	// Sanity: the registry must have synchronous members at all, or the premise is wrong.
	if len(projection.Default.InMode(projection.Synchronous)) == 0 {
		t.Fatal("no synchronous projections are registered; this test's premise no longer holds")
	}
	return out
}

func mustCreateDevRoles(t *testing.T, db *testsupport.DB) {
	t.Helper()
	// Roles are cluster-wide, so this is idempotent across the suite's parallel databases;
	// the GRANTs that matter are per-database and arrive with the migrations.
	if _, err := db.SQL.Exec(`SELECT 1`); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DO $$ BEGIN
		   IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dthcms_app_local') THEN
		     CREATE ROLE dthcms_app_local LOGIN PASSWORD '` + appRolePassword + `' IN ROLE dthcms_app;
		   END IF;
		 END $$`,
		`GRANT CONNECT ON DATABASE "` + db.Name + `" TO dthcms_app_local`,
	} {
		if _, err := db.SQL.Exec(stmt); err != nil {
			t.Fatalf("creating the local application role: %v", err)
		}
	}
}

func appPool(t *testing.T, ctx context.Context, db *testsupport.DB) *pgxpool.Pool {
	t.Helper()

	parsed, err := url.Parse(db.DSN)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(appRole, appRolePassword)

	pool, err := pgxpool.New(ctx, parsed.String())
	if err != nil {
		t.Fatalf("opening a pool as %s: %v", appRole, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedForAppend(t *testing.T, ctx context.Context, db *testsupport.DB) (facility, operator, device, patient uuid.UUID) {
	t.Helper()

	if err := db.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&facility); err != nil {
		t.Fatal(err)
	}
	operator, device, patient = uuid.New(), uuid.New(), uuid.New()

	if _, err := db.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'APR01', 'App Role Probe', 'অ্যাপ রোল প্রোব', 'active')`,
		operator, facility); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'App Role Probe Tablet', 'tablet', 'active', now())`,
		device, facility); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-009001', 'App Role Probe Patient', 'female',
		        DATE '1980-01-01', 'day', 'national_id', '+8801700009001', 'active', $3, now())`,
		patient, facility, operator); err != nil {
		t.Fatal(err)
	}
	_ = ctx
	return facility, operator, device, patient
}
