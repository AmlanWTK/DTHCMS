package migrate_test

import (
	"strings"
	"testing"
)

// The workstation invariants, broken on purpose (CP82, ADR-0021, invariants 125 and 126).
//
// An invariant that has never been seen to fail is an invariant nobody has checked. Each
// case here writes exactly the row the guarantee forbids — by direct SQL, as a hand-edit or
// a future refactor would — and asserts that `core.assert_invariants()` refuses. That is the
// same function `migrate verify` and every `migrate up` run at the end, so a failure here is
// a failure of the deployment gate.
//
// The escalation each one prevents is in the assertion's own HINT, and the tests assert on
// the message too: an invariant that fires with a message nobody can act on has stopped one
// incident and started another.

func TestATabletMayNotCarryAWorkstationCode(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	mustMigrate(t, ctx, dsn)
	db := open(t, dsn)

	// The constraint first. A data scan alone would pass in the window between somebody
	// dropping the CHECK and somebody using the hole, which is the window in which the
	// change gets reviewed and merged.
	mustExec(t, ctx, db, `ALTER TABLE core.device DROP CONSTRAINT device_workstation_code_is_a_desktop`)
	_, err := db.ExecContext(ctx, "SELECT core.assert_invariants()")
	if err == nil {
		t.Fatal("core.device lost the constraint that keeps a workstation code on a desktop and " +
			"the invariants passed; a tablet could then be given a printed code, and a claim " +
			"anybody can type would be attributed to a device whose id is otherwise a signature")
	}
	if !strings.Contains(err.Error(), "device_workstation_code_is_a_desktop") &&
		!strings.Contains(err.Error(), "lost the constraint") {
		t.Errorf("the error must say which guarantee went missing, got: %v", err)
	}

	// Now the data scan. The bad row is written while the constraint is gone, and the
	// constraint is then put back NOT VALID — which is exactly how a hand-repair leaves a
	// database: the rule is enforced from now on, and the row that broke it is still there.
	mustExec(t, ctx, db, `
		INSERT INTO core.device (id, facility_id, name, kind, status, workstation_code)
		VALUES ('11111111-1111-4111-8111-111111111111', core.default_facility(),
		        'Mutation tablet', 'tablet', 'pending', 'FRD-MUT-1')`)
	mustExec(t, ctx, db, `
		ALTER TABLE core.device
		  ADD CONSTRAINT device_workstation_code_is_a_desktop
		  CHECK (workstation_code IS NULL OR kind = 'desktop') NOT VALID`)

	_, err = db.ExecContext(ctx, "SELECT core.assert_invariants()")
	if err == nil {
		t.Fatal("a tablet with a workstation code passed the invariants")
	}
	if !strings.Contains(err.Error(), "11111111-1111-4111-8111-111111111111") {
		t.Errorf("the error must name the offending device, got: %v", err)
	}
}

func TestNoSessionMayRecordAProvenDesktop(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	mustMigrate(t, ctx, dsn)
	db := open(t, dsn)

	mustExec(t, ctx, db, `
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at, workstation_code)
		VALUES ('22222222-2222-4222-8222-222222222222', core.default_facility(),
		        'Mutation desk', 'desktop', 'active', now(), 'FRD-MUT-2')`)
	mustExec(t, ctx, db, `
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ('33333333-3333-4333-8333-333333333333', core.default_facility(),
		        'MUT01', 'Mutation', 'মিউটেশন', 'active')`)
	mustExec(t, ctx, db, `
		INSERT INTO core.session (id, facility_id, user_id, device_id, device_binding, token_digest, expires_at)
		VALUES ('44444444-4444-4444-8444-444444444444', core.default_facility(),
		        '33333333-3333-4333-8333-333333333333', '22222222-2222-4222-8222-222222222222',
		        'PROVEN', decode(repeat('ab', 32), 'hex'), now() + interval '1 hour')`)

	_, err := db.ExecContext(ctx, "SELECT core.assert_invariants()")
	if err == nil {
		t.Fatal("a session recorded PROVEN against a desktop and the invariants passed; a typed " +
			"label would then be indistinguishable from a signature everywhere downstream, " +
			"which is the single misreading ADR-0021 exists to make impossible")
	}
	if !strings.Contains(err.Error(), "44444444-4444-4444-8444-444444444444") {
		t.Errorf("the error must name the offending session, got: %v", err)
	}

	// The binding corrected to the truth, and the database is sound again.
	mustExec(t, ctx, db, `
		UPDATE core.session SET device_binding = 'NAMED'
		 WHERE id = '44444444-4444-4444-8444-444444444444'`)
	if _, err := db.ExecContext(ctx, "SELECT core.assert_invariants()"); err != nil {
		t.Fatalf("a NAMED desktop session should pass: %v", err)
	}

	// And the coherence constraint is guarded too: without it a session can name a machine
	// while saying nothing about how the machine got there, and the person who finds that row
	// in an incident has to guess.
	mustExec(t, ctx, db, `ALTER TABLE core.session DROP CONSTRAINT session_device_binding_coherent`)
	if _, err := db.ExecContext(ctx, "SELECT core.assert_invariants()"); err == nil {
		t.Fatal("core.session lost session_device_binding_coherent and the invariants passed")
	}
}

// TestANamedWorkstationMayNotHoldASigningKey is the converse invariant 60 gained at CP82.
//
// A desk that could both sign and be named by a printed code would carry two claims of
// different strengths behind one device_id, and every reader downstream sees only the id.
func TestANamedWorkstationMayNotHoldASigningKey(t *testing.T) {
	ctx, dsn := freshDatabase(t)
	mustMigrate(t, ctx, dsn)
	db := open(t, dsn)

	mustExec(t, ctx, db, `
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at, workstation_code)
		VALUES ('55555555-5555-4555-8555-555555555555', core.default_facility(),
		        'Mutation desk 2', 'desktop', 'active', now(), 'FRD-MUT-3')`)

	// Active, keyless and named: the state CP82 introduces, which invariant 60 had to be
	// widened to permit. If this fails, the widening is wrong.
	if _, err := db.ExecContext(ctx, "SELECT core.assert_invariants()"); err != nil {
		t.Fatalf("an active named workstation with no key must pass; a desk has nowhere to keep "+
			"a key and leaving it pending forever is worse: %v", err)
	}

	mustExec(t, ctx, db, `
		INSERT INTO core.device_key (device_id, facility_id, public_key)
		VALUES ('55555555-5555-4555-8555-555555555555', core.default_facility(),
		        decode(repeat('cd', 32), 'hex'))`)

	_, err := db.ExecContext(ctx, "SELECT core.assert_invariants()")
	if err == nil {
		t.Fatal("a named workstation was given a live signing key and the invariants passed")
	}
	if !strings.Contains(err.Error(), "FRD-MUT-3") {
		t.Errorf("the error must name the offending workstation, got: %v", err)
	}
}
