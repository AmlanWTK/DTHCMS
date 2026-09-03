package eventstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
)

// The ledger against a real database: CP23's five acceptance criteria and the tests the
// plan names — concurrency, append-only, tamper detection, partitions, performance.

type harness struct {
	db       *testsupport.DB
	pool     *pgxpool.Pool
	store    *eventstore.Store
	clock    *clock.Fixed
	facility uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, db.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	h := &harness{db: db, pool: pool, clock: clock.NewFixed(time.Date(2026, 9, 3, 4, 42, 0, 0, time.UTC)),
		user: uuid.New(), device: uuid.New()}
	if err := db.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}
	h.store = eventstore.New(eventstore.Config{Pool: pool, Clock: h.clock})
	return h
}

// height is a complete, valid HEIGHT_RECORDED envelope for one visit.
func (h *harness) height(visit uuid.UUID, cm float64) eventstore.Envelope {
	patient := uuid.New()
	return eventstore.Envelope{
		EventID: uuid.Must(uuid.NewV7()), AggregateType: "VISIT", AggregateID: visit, PatientID: &patient, VisitID: &visit,
		EventType: "HEIGHT_RECORDED", EventVersion: 1, OccurredAt: h.clock.Now().Add(-5 * time.Second),
		Actor:    eventstore.Actor{UserID: h.user, DeviceID: h.device, Role: "ANTHROPOMETRY", Station: "ANTHROPOMETRY", FacilityID: h.facility},
		Source:   eventstore.SourceMobileOnline,
		Payload:  json.RawMessage(fmt.Sprintf(`{"code":"HEIGHT","value":%g,"unit":"cm","method":"stadiometer"}`, cm)),
		Metadata: map[string]any{"app_version": "1.4.2", "correlation_id": "req_test", "client_tz": "Asia/Dhaka"},
	}
}

func TestAppendAssignsSequencesAndChains(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	visit := uuid.New()

	first, err := h.store.Append(ctx, h.height(visit, 150))
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || first.GlobalSeq != 1 || string(first.PrevHash) != string(eventstore.Genesis) {
		t.Fatalf("first event: seq %d global %d prev %x", first.Sequence, first.GlobalSeq, first.PrevHash)
	}
	if !first.RecordedAt.Equal(h.clock.Now()) {
		t.Errorf("recorded_at = %s, want the server clock %s", first.RecordedAt, h.clock.Now())
	}

	// The canonical 140/150 case (§7.7): the correction is a new event that names the
	// original, carries the previous value, and says why.
	h.clock.Advance(38 * time.Minute)
	correction := h.height(visit, 140)
	correction.EventType = "HEIGHT_CORRECTED"
	correction.Previous = json.RawMessage(`{"value":150,"unit":"cm"}`)
	correction.Correction = &eventstore.Correction{CorrectsEventID: first.EventID, ReasonCode: "TRANSCRIPTION", ReasonText: "misread stadiometer"}
	second, err := h.store.Append(ctx, correction)
	if err != nil {
		t.Fatal(err)
	}
	if second.Sequence != 2 || string(second.PrevHash) != string(first.Hash) {
		t.Fatalf("second event: seq %d, links to %x want %x", second.Sequence, second.PrevHash, first.Hash)
	}
	if second.Correction == nil || second.Correction.CorrectsEventID != first.EventID {
		t.Errorf("the correction did not come back: %+v", second.Correction)
	}

	// The original still exists, still says 150, still attributed.
	original, err := h.store.ByID(ctx, first.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(original.Payload), `"value": 150`) && !strings.Contains(string(original.Payload), `"value":150`) {
		t.Errorf("the original payload changed: %s", original.Payload)
	}
	if original.Actor.Role != "ANTHROPOMETRY" || original.Actor.DeviceID != h.device {
		t.Errorf("attribution lost: %+v", original.Actor)
	}

	// Replaying the first event is a no-op that returns the original outcome (§7.5).
	again, err := h.store.Append(ctx, h.heightWithID(visit, 155, first.EventID))
	if err != nil {
		t.Fatal(err)
	}
	if !again.Duplicate || again.Sequence != 1 || again.GlobalSeq != first.GlobalSeq {
		t.Fatalf("replay: duplicate=%v seq=%d global=%d", again.Duplicate, again.Sequence, again.GlobalSeq)
	}
	if n, _ := h.store.Count(ctx); n != 2 {
		t.Errorf("count = %d after a replay, want 2", n)
	}

	// Optimistic concurrency (§7.9): a command that expected head 1 finds head 2.
	stale := h.height(visit, 160)
	stale.ExpectedSequence = 1
	if _, err := h.store.Append(ctx, stale); !errors.Is(err, eventstore.ErrSequenceConflict) {
		t.Fatalf("stale append: %v", err)
	}
	fresh := h.height(visit, 160)
	fresh.ExpectedSequence = 2
	if _, err := h.store.Append(ctx, fresh); err != nil {
		t.Fatalf("append at the right head: %v", err)
	}

	stream, err := h.store.Stream(ctx, "VISIT", visit, 1, 0)
	if err != nil || len(stream) != 3 {
		t.Fatalf("stream: %d events, %v", len(stream), err)
	}
	v, err := h.store.Verify(ctx)
	if err != nil || !v.OK || v.Events != 3 || v.Aggregates != 1 {
		t.Fatalf("verify: %+v %v", v, err)
	}
}

func (h *harness) heightWithID(visit uuid.UUID, cm float64, id uuid.UUID) eventstore.Envelope {
	e := h.height(visit, cm)
	e.EventID = id
	return e
}

// Criterion 5: every event carries the full attribution envelope; an append missing any
// envelope field is rejected — by the module, and by the database behind it.
func TestAnIncompleteEnvelopeIsRejected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	visit := uuid.New()

	cases := map[string]func(e *eventstore.Envelope){
		"event_id":          func(e *eventstore.Envelope) { e.EventID = uuid.Nil },
		"aggregate_type":    func(e *eventstore.Envelope) { e.AggregateType = "" },
		"aggregate_id":      func(e *eventstore.Envelope) { e.AggregateID = uuid.Nil },
		"event_type":        func(e *eventstore.Envelope) { e.EventType = "" },
		"event_version":     func(e *eventstore.Envelope) { e.EventVersion = 0 },
		"occurred_at":       func(e *eventstore.Envelope) { e.OccurredAt = time.Time{} },
		"actor.user_id":     func(e *eventstore.Envelope) { e.Actor.UserID = uuid.Nil },
		"actor.device_id":   func(e *eventstore.Envelope) { e.Actor.DeviceID = uuid.Nil },
		"actor.role":        func(e *eventstore.Envelope) { e.Actor.Role = " " },
		"actor.facility_id": func(e *eventstore.Envelope) { e.Actor.FacilityID = uuid.Nil },
		"source":            func(e *eventstore.Envelope) { e.Source = "EMAIL" },
		"payload":           func(e *eventstore.Envelope) { e.Payload = nil },
		"correction":        func(e *eventstore.Envelope) { e.Correction = &eventstore.Correction{ReasonCode: "X"} },
	}
	for field, strip := range cases {
		e := h.height(visit, 150)
		strip(&e)
		_, err := h.store.Append(ctx, e)
		if !errors.Is(err, eventstore.ErrIncomplete) || !strings.Contains(err.Error(), field) {
			t.Errorf("without %s: err = %v", field, err)
		}
	}
	if n, _ := h.store.Count(ctx); n != 0 {
		t.Fatalf("%d events landed from incomplete envelopes", n)
	}

	// And the table itself refuses a row without a device, even from the owner.
	_, err := h.db.SQL.Exec(`INSERT INTO ledger.event (event_id, aggregate_type, aggregate_id, sequence, event_type, occurred_at,
		actor_user_id, actor_device_id, actor_role, facility_id, source, payload, prev_hash, hash)
		VALUES ($1, 'VISIT', $2, 1, 'HEIGHT_RECORDED', now(), $3, NULL, 'ANTHROPOMETRY', $4, 'WEB', '{}', $5, $5)`,
		uuid.New(), visit, h.user, h.facility, eventstore.Genesis)
	if err == nil || !strings.Contains(err.Error(), "actor_device_id") {
		t.Fatalf("the database accepted a row with no device: %v", err)
	}
}

func TestPayloadsAreCheckedAgainstTheRegistry(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	visit := uuid.New()

	refused := func(name string, mutate func(e *eventstore.Envelope), want error) {
		t.Helper()
		e := h.height(visit, 150)
		mutate(&e)
		if _, err := h.store.Append(ctx, e); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", name, err, want)
		}
	}
	refused("an unregistered type", func(e *eventstore.Envelope) { e.EventType = "HEIGHT_GUESSED" }, eventstore.ErrUnknownEventType)
	refused("an unregistered version", func(e *eventstore.Envelope) { e.EventVersion = 9 }, eventstore.ErrUnknownEventType)
	refused("a field the schema lacks", func(e *eventstore.Envelope) {
		e.Payload = json.RawMessage(`{"code":"HEIGHT","value":150,"unit":"cm","shoes":true}`)
	}, eventstore.ErrInvalidPayload)
	refused("the wrong unit", func(e *eventstore.Envelope) {
		e.Payload = json.RawMessage(`{"code":"HEIGHT","value":59,"unit":"in"}`)
	}, eventstore.ErrInvalidPayload)
	refused("an implausible value", func(e *eventstore.Envelope) {
		e.Payload = json.RawMessage(`{"code":"HEIGHT","value":1500,"unit":"cm"}`)
	}, eventstore.ErrInvalidPayload)
	refused("a type on the wrong aggregate", func(e *eventstore.Envelope) { e.AggregateType = "PATIENT" }, eventstore.ErrInvalidPayload)
	refused("diastolic above systolic", func(e *eventstore.Envelope) {
		e.EventType = "BP_RECORDED"
		e.Payload = json.RawMessage(`{"systolic":80,"diastolic":120,"unit":"mmHg"}`)
	}, eventstore.ErrInvalidPayload)
}

// Criterion 1: sequences are gapless per aggregate under concurrent load.
func TestSequencesAreGaplessUnderConcurrency(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	visit := uuid.New()
	const writers = 100

	var wg sync.WaitGroup
	results := make(chan int64, writers)
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ev, err := h.store.Append(ctx, h.height(visit, 100+float64(i)))
			if err != nil {
				errs <- err
				return
			}
			results <- ev.Sequence
		}(i)
	}
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		t.Fatal(err)
	}
	var seqs []int64
	for s := range results {
		seqs = append(seqs, s)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for i, s := range seqs {
		if s != int64(i+1) {
			t.Fatalf("sequences are not gapless: %v", seqs)
		}
	}
	if len(seqs) != writers {
		t.Fatalf("%d sequences for %d writers", len(seqs), writers)
	}
	v, err := h.store.Verify(ctx)
	if err != nil || !v.OK || v.Events != writers {
		t.Fatalf("verify after concurrent appends: %+v %v", v, err)
	}

	// And the same event retried concurrently lands once.
	id := uuid.Must(uuid.NewV7())
	var dup sync.WaitGroup
	landed := make(chan eventstore.Event, 8)
	for i := 0; i < 8; i++ {
		dup.Add(1)
		go func() {
			defer dup.Done()
			ev, err := h.store.Append(ctx, h.heightWithID(visit, 170, id))
			if err != nil {
				t.Error(err)
				return
			}
			landed <- ev
		}()
	}
	dup.Wait()
	close(landed)
	originals := 0
	for ev := range landed {
		if !ev.Duplicate {
			originals++
		}
		if ev.Sequence != writers+1 {
			t.Errorf("the retried event has sequence %d", ev.Sequence)
		}
	}
	if originals != 1 {
		t.Fatalf("%d of 8 concurrent retries were treated as new", originals)
	}
}

// Criterion 2: the application role cannot mutate or delete events — and neither can
// anyone through the parent table, by rule, nor through a partition, by trigger.
func TestTheLedgerCannotBeRewritten(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ev, err := h.store.Append(ctx, h.height(uuid.New(), 150))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("the application role holds no privilege to rewrite, and nothing changes", func(t *testing.T) {
		// The rules rewrite an UPDATE or DELETE into nothing before the privilege is even
		// consulted, so the application sees neither an error nor an effect. The privilege
		// is absent all the same — the invariant checks it — and TRUNCATE, which no rule
		// touches, is refused outright.
		for _, table := range []string{"ledger.event", "ledger.event_key", "ledger.chain_anchor", "ledger.aggregate_snapshot"} {
			for _, priv := range []string{"UPDATE", "DELETE", "TRUNCATE"} {
				var held bool
				if err := h.db.SQL.QueryRow(`SELECT has_table_privilege('dthcms_app', $1, $2)`, table, priv).Scan(&held); err != nil {
					t.Fatal(err)
				}
				if held {
					t.Errorf("dthcms_app may %s %s", priv, table)
				}
			}
		}
		tx, err := h.db.SQL.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(`SET ROLE dthcms_app`); err != nil {
			t.Fatal(err)
		}
		for what, stmt := range map[string]string{
			"update an event": `UPDATE ledger.event SET payload = '{}' WHERE global_seq = 1`,
			"delete an event": `DELETE FROM ledger.event WHERE global_seq = 1`,
			"delete a key":    `DELETE FROM ledger.event_key`,
		} {
			res, err := tx.Exec(stmt)
			if err != nil {
				t.Errorf("%s: %v (the rule should have made it nothing)", what, err)
				continue
			}
			if n, _ := res.RowsAffected(); n != 0 {
				t.Errorf("%s: %d rows affected", what, n)
			}
		}
		if _, err := tx.Exec(`SAVEPOINT p`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`TRUNCATE ledger.event`); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("truncate: %v", err)
		}
		_, _ = tx.Exec(`ROLLBACK TO SAVEPOINT p`)
		var role string
		if err := tx.QueryRow(`SELECT actor_role FROM ledger.event WHERE global_seq = $1`, ev.GlobalSeq).Scan(&role); err != nil || role != "ANTHROPOMETRY" {
			t.Errorf("the row changed: %q %v", role, err)
		}
	})

	t.Run("the owner is refused by rule and by trigger", func(t *testing.T) {
		// Through the parent: the rule turns the statement into nothing.
		res, err := h.db.SQL.Exec(`UPDATE ledger.event SET actor_role = 'NOBODY' WHERE global_seq = $1`, ev.GlobalSeq)
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := res.RowsAffected(); n != 0 {
			t.Errorf("the rule let %d rows change", n)
		}
		res, err = h.db.SQL.Exec(`DELETE FROM ledger.event WHERE global_seq = $1`, ev.GlobalSeq)
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := res.RowsAffected(); n != 0 {
			t.Errorf("the rule let %d rows go", n)
		}
		// Through the partition by name: the trigger raises.
		partition := "ledger.event_" + ev.RecordedAt.In(time.UTC).Format("2006_01")
		_, err = h.db.SQL.Exec(`UPDATE ` + partition + ` SET actor_role = 'NOBODY'`)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("the partition accepted an update: %v", err)
		}
		_, err = h.db.SQL.Exec(`DELETE FROM ` + partition)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("the partition accepted a delete: %v", err)
		}
		var role string
		if err := h.db.SQL.QueryRow(`SELECT actor_role FROM ledger.event WHERE global_seq = $1`, ev.GlobalSeq).Scan(&role); err != nil || role != "ANTHROPOMETRY" {
			t.Errorf("the row changed after all: %q %v", role, err)
		}
	})

	// The invariant says the same on every start.
	if _, err := h.db.SQL.Exec(`SELECT core.assert_event_store_immutable()`); err != nil {
		t.Fatalf("assert_event_store_immutable: %v", err)
	}
}

// Criterion 3: the verifier detects any tampering — done here the way a superuser would
// have to do it, by switching the trigger off and editing the partition directly.
func TestTheVerifierDetectsTampering(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	visit := uuid.New()
	var events []eventstore.Event
	for i := 0; i < 6; i++ {
		h.clock.Advance(time.Minute)
		ev, err := h.store.Append(ctx, h.height(visit, 150+float64(i)))
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, ev)
	}
	partition := "ledger.event_" + events[0].RecordedAt.In(time.UTC).Format("2006_01")

	tamper := func(t *testing.T, stmt string, args ...any) {
		t.Helper()
		tx, err := h.db.SQL.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		// What a superuser can do that the application cannot: silence the trigger.
		if _, err := tx.Exec(`SET LOCAL session_replication_role = replica`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(stmt, args...); err != nil {
			t.Fatalf("tampering: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	restore := func(t *testing.T, stmt string, args ...any) { t.Helper(); tamper(t, stmt, args...) }

	t.Run("a changed value is detected at its sequence", func(t *testing.T) {
		tamper(t, `UPDATE `+partition+` SET payload = jsonb_set(payload, '{value}', '140') WHERE global_seq = $1`, events[2].GlobalSeq)
		v, err := h.store.Verify(ctx)
		if err != nil || v.OK || v.BrokenSequence != 3 || !strings.Contains(v.Problem, "does not hash") {
			t.Fatalf("verify: %+v %v", v, err)
		}
		restore(t, `UPDATE `+partition+` SET payload = jsonb_set(payload, '{value}', '152') WHERE global_seq = $1`, events[2].GlobalSeq)
	})
	t.Run("a changed attribution is detected", func(t *testing.T) {
		tamper(t, `UPDATE `+partition+` SET actor_user_id = $2 WHERE global_seq = $1`, events[4].GlobalSeq, uuid.New())
		v, _ := h.store.Verify(ctx)
		if v.OK || v.BrokenSequence != 5 {
			t.Fatalf("verify: %+v", v)
		}
		restore(t, `UPDATE `+partition+` SET actor_user_id = $2 WHERE global_seq = $1`, events[4].GlobalSeq, h.user)
	})
	t.Run("a removed event is detected as a gap", func(t *testing.T) {
		if _, err := h.db.SQL.Exec(`CREATE TEMP TABLE keep AS SELECT * FROM ledger.event WHERE global_seq = $1`, events[3].GlobalSeq); err != nil {
			t.Fatal(err)
		}
		tamper(t, `DELETE FROM `+partition+` WHERE global_seq = $1`, events[3].GlobalSeq)
		v, _ := h.store.Verify(ctx)
		if v.OK || !strings.Contains(v.Problem, "missing") {
			t.Fatalf("verify: %+v", v)
		}
		restore(t, `INSERT INTO `+partition+` SELECT * FROM keep`)
	})
	t.Run("a re-hashed row is detected at the next link", func(t *testing.T) {
		tamper(t, `UPDATE `+partition+` SET hash = decode(repeat('ab', 32), 'hex') WHERE global_seq = $1`, events[1].GlobalSeq)
		v, _ := h.store.Verify(ctx)
		if v.OK || v.BrokenSequence != 2 {
			t.Fatalf("verify: %+v", v)
		}
		restore(t, `UPDATE `+partition+` SET hash = $2 WHERE global_seq = $1`, events[1].GlobalSeq, events[1].Hash)
	})

	v, err := h.store.Verify(ctx)
	if err != nil || !v.OK || v.Events != 6 {
		t.Fatalf("after restoring: %+v %v", v, err)
	}
}

func TestDailyAnchorsChainTheDays(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	visit := uuid.New()
	for i := 0; i < 3; i++ {
		if _, err := h.store.Append(ctx, h.height(visit, 150)); err != nil {
			t.Fatal(err)
		}
	}
	day1 := h.clock.Now()
	a1, err := h.store.AnchorDay(ctx, h.facility, day1)
	if err != nil {
		t.Fatal(err)
	}
	if a1.EventCount != 3 || string(a1.PrevAnchor) != string(eventstore.Genesis) || a1.FirstSeq == nil || *a1.LastSeq != 3 {
		t.Fatalf("day 1 anchor: %+v", a1)
	}
	if _, err := h.store.AnchorDay(ctx, h.facility, day1); !errors.Is(err, eventstore.ErrAnchorExists) {
		t.Fatalf("anchoring twice: %v", err)
	}

	h.clock.Advance(24 * time.Hour)
	if _, err := h.store.Append(ctx, h.height(visit, 151)); err != nil {
		t.Fatal(err)
	}
	a2, err := h.store.AnchorDay(ctx, h.facility, h.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if string(a2.PrevAnchor) != string(a1.Anchor) || a2.EventCount != 1 {
		t.Fatalf("day 2 anchor does not chain: %+v", a2)
	}
	// An empty day still anchors, so a gap in the calendar is not a gap in the chain.
	h.clock.Advance(24 * time.Hour)
	a3, err := h.store.AnchorDay(ctx, h.facility, h.clock.Now())
	if err != nil || a3.EventCount != 0 || string(a3.PrevAnchor) != string(a2.Anchor) {
		t.Fatalf("empty day: %+v %v", a3, err)
	}

	v, err := h.store.Verify(ctx)
	if err != nil || !v.OK || v.Anchors != 3 {
		t.Fatalf("verify: %+v %v", v, err)
	}

	// An event that appears in an anchored day after the fact — a backdated insert by
	// somebody with the keys — breaks that day's anchor even though its own chain is sound.
	h.clock.Current = day1.Add(time.Hour)
	if _, err := h.store.Append(ctx, h.height(uuid.New(), 160)); err != nil {
		t.Fatal(err)
	}
	v, _ = h.store.Verify(ctx)
	if v.OK || v.BrokenDay != day1.In(eventstore.Dhaka).Format("2006-01-02") {
		t.Fatalf("a backdated event went unnoticed: %+v", v)
	}
}

func TestPartitionsRotateAndCatchStrays(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	var before int
	if err := h.db.SQL.QueryRow(`SELECT count(*) FROM pg_inherits WHERE inhparent = 'ledger.event'::regclass`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before < 16 { // fifteen months ahead plus the default
		t.Fatalf("%d partitions after the migration, want the first fifteen months and a default", before)
	}
	var created int
	if err := h.db.SQL.QueryRow(`SELECT ledger.ensure_event_partitions(40)`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created < 20 {
		t.Fatalf("rotation created %d partitions, want the months not yet covered", created)
	}
	if err := h.db.SQL.QueryRow(`SELECT ledger.ensure_event_partitions(40)`).Scan(&created); err != nil || created != 0 {
		t.Fatalf("rotation is not idempotent: %d %v", created, err)
	}

	// A row for a month nobody created lands in the default partition, and is counted.
	h.clock.Current = h.clock.Now().AddDate(10, 0, 0)
	if _, err := h.store.Append(ctx, h.height(uuid.New(), 150)); err != nil {
		t.Fatalf("an append into an uncovered month was refused: %v", err)
	}
	v, err := h.store.Verify(ctx)
	if err != nil || !v.OK || v.Strays != 1 {
		t.Fatalf("verify: %+v %v", v, err)
	}
}

// Criterion 4: append latency p95 under the budget, 50 ms, measured against a real
// database on this machine. The number reported is the one to compare with the server
// when there is one.
func TestAppendLatencyStaysUnderBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("latency measurement; skipped with -short")
	}
	h := newHarness(t)
	h.store = eventstore.New(eventstore.Config{Pool: h.pool})
	ctx := context.Background()
	visits := make([]uuid.UUID, 20)
	for i := range visits {
		visits[i] = uuid.New()
	}
	const n = 1000
	durations := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		e := h.height(visits[i%len(visits)], 150)
		e.OccurredAt = time.Now()
		start := time.Now()
		if _, err := h.store.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
		durations = append(durations, time.Since(start))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p50, p95, p99 := durations[n/2], durations[n*95/100], durations[n*99/100]
	t.Logf("append latency over %d events: p50 %s, p95 %s, p99 %s", n, p50, p95, p99)
	if p95 > 50*time.Millisecond {
		t.Fatalf("p95 append latency %s exceeds the 50 ms budget", p95)
	}
}

// The plan's 10,000-event insert, across a hundred aggregates and eight writers, with a
// full verification afterwards.
func TestTenThousandEventsVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("bulk insert; skipped with -short")
	}
	h := newHarness(t)
	h.store = eventstore.New(eventstore.Config{Pool: h.pool})
	ctx := context.Background()
	const total, aggregates, writers = 10_000, 100, 8
	visits := make([]uuid.UUID, aggregates)
	for i := range visits {
		visits[i] = uuid.New()
	}
	start := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < total; i += writers {
				e := h.height(visits[i%aggregates], 150)
				e.OccurredAt = time.Now()
				if _, err := h.store.Append(ctx, e); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	t.Logf("%d events in %s (%.0f/s)", total, elapsed, float64(total)/elapsed.Seconds())

	verifyStart := time.Now()
	v, err := h.store.Verify(ctx)
	if err != nil || !v.OK || v.Events != total || v.Aggregates != aggregates {
		t.Fatalf("verify: %+v %v", v, err)
	}
	t.Logf("verified %d events in %s", total, time.Since(verifyStart))
}
