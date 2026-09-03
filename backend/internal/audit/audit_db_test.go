package audit_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
)

// The chain against a real database (CP22 criteria 1 and the hash-chain test).

type chainHarness struct {
	db       *testsupport.DB
	pool     *pgxpool.Pool
	store    *audit.PostgresStore
	recorder *audit.Recorder
	facility uuid.UUID
	clock    *clock.Fixed
	admin    uuid.UUID
}

func newChain(t *testing.T) *chainHarness {
	t.Helper()
	db := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, db.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h := &chainHarness{db: db, pool: pool, clock: clock.NewFixed(time.Date(2026, 9, 3, 4, 42, 0, 0, time.UTC))}
	if err := db.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}
	if err := db.SQL.QueryRow(`INSERT INTO core.app_user (facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, 'A001', 'Admin', 'অ্যাডমিন', 'active') RETURNING id`, h.facility).Scan(&h.admin); err != nil {
		t.Fatal(err)
	}
	h.store = audit.NewPostgresStore(pool)
	h.recorder = audit.NewRecorder(h.store, h.clock, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return h
}

func (h *chainHarness) entry(kind string, details map[string]any) audit.Entry {
	return audit.Entry{
		Kind: kind, FacilityID: h.facility, ActorID: &h.admin, ActorCode: "A001", ActorRole: "ADMIN",
		TargetCode: "N002", Reason: "because", Details: details, ClientDigest: []byte{1, 2, 3},
	}
}

func TestTheChainLinksAndVerifies(t *testing.T) {
	h := newChain(t)
	ctx := context.Background()

	var last []byte
	for i := 1; i <= 25; i++ {
		h.clock.Advance(time.Second)
		ev, err := h.recorder.Record(ctx, h.entry("role.granted", map[string]any{"role": "NUTRITIONIST", "n": i}))
		if err != nil {
			t.Fatal(err)
		}
		if ev.Seq != int64(i) {
			t.Fatalf("row %d got seq %d", i, ev.Seq)
		}
		if i == 1 && string(ev.PrevHash) != string(audit.Genesis) {
			t.Fatal("the first row must link to the genesis hash")
		}
		if i > 1 && string(ev.PrevHash) != string(last) {
			t.Fatalf("row %d does not link to row %d", i, i-1)
		}
		last = ev.Hash
	}

	v, err := h.recorder.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK || v.Checked != 25 || v.HeadSeq != 25 {
		t.Fatalf("verification = %+v", v)
	}
	if v.Strays != 0 {
		t.Errorf("rows landed in the default partition: %d", v.Strays)
	}
}

func TestTheVerifierDetectsTampering(t *testing.T) {
	h := newChain(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		h.clock.Advance(time.Second)
		if _, err := h.recorder.Record(ctx, h.entry("session.login", nil)); err != nil {
			t.Fatal(err)
		}
	}

	// The owner role can do what the application role cannot: this is the superuser
	// tamper of the manual verification, done by a test. Each case is a separate
	// transaction rolled back afterwards, so the chain is intact for the next.
	tamper := func(t *testing.T, statement string, wantAt int64, wantProblem string) {
		t.Helper()
		if _, err := h.db.SQL.Exec(statement); err != nil {
			t.Fatalf("tampering: %v", err)
		}
		v, err := h.recorder.Verify(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if v.OK {
			t.Fatalf("%s: the verifier said the chain is intact", statement)
		}
		if v.BrokenAt != wantAt {
			t.Errorf("%s: broken at %d, want %d (%s)", statement, v.BrokenAt, wantAt, v.Problem)
		}
		if !strings.Contains(v.Problem, wantProblem) {
			t.Errorf("%s: problem %q, want it to mention %q", statement, v.Problem, wantProblem)
		}
	}

	t.Run("a changed reason is detected at that row", func(t *testing.T) {
		tamper(t, `UPDATE ledger.audit_event SET reason = 'something else' WHERE seq = 4`, 4, "does not hash")
		if _, err := h.db.SQL.Exec(`UPDATE ledger.audit_event SET reason = 'because' WHERE seq = 4`); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a changed hash is detected at the next row", func(t *testing.T) {
		var original []byte
		if err := h.db.SQL.QueryRow(`SELECT hash FROM ledger.audit_event WHERE seq = 6`).Scan(&original); err != nil {
			t.Fatal(err)
		}
		tamper(t, `UPDATE ledger.audit_event SET hash = decode(repeat('ab', 32), 'hex') WHERE seq = 6`, 6, "does not hash")
		if _, err := h.db.SQL.Exec(`UPDATE ledger.audit_event SET hash = $1 WHERE seq = 6`, original); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a removed row is detected as a gap", func(t *testing.T) {
		// Copy the row, delete it, verify, put it back.
		if _, err := h.db.SQL.Exec(`CREATE TEMP TABLE keep AS SELECT * FROM ledger.audit_event WHERE seq = 7`); err != nil {
			t.Fatal(err)
		}
		tamper(t, `DELETE FROM ledger.audit_event WHERE seq = 7`, 7, "missing")
		if _, err := h.db.SQL.Exec(`INSERT INTO ledger.audit_event SELECT * FROM keep`); err != nil {
			t.Fatal(err)
		}
	})

	v, err := h.recorder.Verify(ctx)
	if err != nil || !v.OK {
		t.Fatalf("after restoring, verification = %+v, %v", v, err)
	}
}

func TestTheSequenceIsGaplessUnderConcurrency(t *testing.T) {
	h := newChain(t)
	ctx := context.Background()
	const workers = 40

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := h.recorder.Record(ctx, h.entry("session.login", map[string]any{"worker": i}))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	var count, maxSeq, distinct int64
	if err := h.db.SQL.QueryRow(`SELECT count(*), max(seq), count(DISTINCT seq) FROM ledger.audit_event`).Scan(&count, &maxSeq, &distinct); err != nil {
		t.Fatal(err)
	}
	if count != workers || maxSeq != workers || distinct != workers {
		t.Fatalf("count=%d max=%d distinct=%d, want all %d", count, maxSeq, distinct, workers)
	}
	v, err := h.recorder.Verify(ctx)
	if err != nil || !v.OK {
		t.Fatalf("verification after concurrent appends = %+v, %v", v, err)
	}
}

// Criterion 1: the application role cannot update or delete an audit row — nor a
// break-glass record, nor an alert.
func TestTheApplicationRoleCannotRewriteTheTrail(t *testing.T) {
	h := newChain(t)
	ctx := context.Background()
	if _, err := h.recorder.Record(ctx, h.entry("session.login", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.SQL.Exec(`INSERT INTO core.break_glass_access (facility_id, user_id, scope_kind, scope_ref, justification, expires_at)
		VALUES ($1, $2, 'other', 'the ward', 'a justification long enough to pass', now() + interval '1 hour')`, h.facility, h.admin); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.SQL.Exec(`INSERT INTO core.admin_alert (facility_id, kind, message_en, message_bn)
		VALUES ($1, 'break_glass', 'a message', 'একটি বার্তা')`, h.facility); err != nil {
		t.Fatal(err)
	}

	tx, err := h.db.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SET ROLE dthcms_app`); err != nil {
		t.Fatal(err)
	}
	refused := func(what, statement string) {
		t.Helper()
		if _, err := tx.Exec(`SAVEPOINT probe`); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(statement)
		if _, rb := tx.Exec(`ROLLBACK TO SAVEPOINT probe`); rb != nil {
			t.Fatal(rb)
		}
		if err == nil {
			t.Fatalf("%s: the application role was allowed to", what)
		}
		if !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s: refused for the wrong reason: %v", what, err)
		}
	}
	refused("update an audit row", `UPDATE ledger.audit_event SET reason = 'x' WHERE seq = 1`)
	refused("delete an audit row", `DELETE FROM ledger.audit_event WHERE seq = 1`)
	refused("truncate the trail", `TRUNCATE ledger.audit_event`)
	refused("delete a break-glass record", `DELETE FROM core.break_glass_access`)
	refused("delete an alert", `DELETE FROM core.admin_alert`)

	// And the invariant registry says so on every start.
	var out sql.NullString
	if err := h.db.SQL.QueryRow(`SELECT core.assert_audit_trail_kept()::text`).Scan(&out); err != nil {
		t.Fatalf("assert_audit_trail_kept: %v", err)
	}
}

func TestAnUnknownKindIsRefused(t *testing.T) {
	h := newChain(t)
	_, err := h.recorder.Record(context.Background(), h.entry("something.nobody_registered", nil))
	if err == nil || !strings.Contains(err.Error(), "unknown event kind") {
		t.Fatalf("err = %v, want the unknown-kind refusal", err)
	}
}

func TestTheViewerQueryNarrows(t *testing.T) {
	h := newChain(t)
	ctx := context.Background()
	patient := uuid.New()
	for i := 0; i < 5; i++ {
		h.clock.Advance(time.Minute)
		e := h.entry("session.login", nil)
		if i == 2 {
			e.Kind = "break_glass.opened"
			e.PatientID = &patient
			e.Details = map[string]any{"scope": "patient " + patient.String(), "until": "2026-09-03T08:00:00Z"}
		}
		if i == 4 {
			e.ActorCode = "JD01"
		}
		if _, err := h.recorder.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got := func(q audit.Query) []int64 {
		t.Helper()
		q.FacilityID = h.facility
		events, err := h.store.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		seqs := make([]int64, 0, len(events))
		for _, ev := range events {
			seqs = append(seqs, ev.Seq)
		}
		return seqs
	}
	if s := got(audit.Query{}); fmt.Sprint(s) != "[5 4 3 2 1]" {
		t.Errorf("newest first: %v", s)
	}
	if s := got(audit.Query{Kind: "break_glass.opened"}); fmt.Sprint(s) != "[3]" {
		t.Errorf("by kind: %v", s)
	}
	if s := got(audit.Query{PatientID: &patient}); fmt.Sprint(s) != "[3]" {
		t.Errorf("by patient: %v", s)
	}
	if s := got(audit.Query{ActorCode: "JD01"}); fmt.Sprint(s) != "[5]" {
		t.Errorf("by actor: %v", s)
	}
	if s := got(audit.Query{SubjectCode: "N002"}); fmt.Sprint(s) != "[5 4 3 2 1]" {
		t.Errorf("by person as target: %v", s)
	}
	if s := got(audit.Query{Before: 3, Limit: 2}); fmt.Sprint(s) != "[2 1]" {
		t.Errorf("paged: %v", s)
	}
}
