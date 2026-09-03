package projection_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// The runner: checkpoints, lag, dead letters, versioning, and the boundary between a
// projection's failure and the ledger's health.

// --- criterion 4: a failing projection does not block event appends ---

// poison fails on one nominated event and works on every other. It is the shape of the
// real failure: not a projection that is broken, but a projection that cannot handle one
// row — a payload from a version it does not know, a reference that has gone.
type poison struct {
	failOn  string
	applied int
}

func (p *poison) Name() string                        { return "poison" }
func (p *poison) Version() int                        { return 1 }
func (p *poison) Mode() projection.Mode               { return projection.Asynchronous }
func (p *poison) Handles(string) bool                 { return true }
func (p *poison) Reset(context.Context, pgx.Tx) error { return nil }

func (p *poison) Apply(_ context.Context, _ pgx.Tx, e eventstore.Event) error {
	if e.EventType == p.failOn {
		return fmt.Errorf("this projection cannot handle %s", e.EventType)
	}
	p.applied++
	return nil
}

func TestAFailingProjectionDoesNotBlockAppends(t *testing.T) {
	bad := &poison{failOn: "WEIGHT_RECORDED"}
	h := newHarness(t, projection.VisitVital{}, bad)
	ctx := context.Background()
	visit, patient := uuid.New(), uuid.New()

	h.append(t, h.measurement(visit, patient, "HEIGHT_RECORDED", "HEIGHT", 150, "ANTHROPOMETRY"))
	h.append(t, h.measurement(visit, patient, "WEIGHT_RECORDED", "WEIGHT", 70, "ANTHROPOMETRY"))
	h.append(t, h.measurement(visit, patient, "PULSE_RECORDED", "PULSE", 72, "VITALS"))
	h.catchUp(t)

	// The appends all succeeded — the projection's failure is not the ledger's business.
	if n, _ := h.events.Count(ctx); n != 3 {
		t.Fatalf("the ledger holds %d events; a projection failure blocked an append", n)
	}
	// And the projection carried on past the event it could not handle.
	if bad.applied != 2 {
		t.Errorf("the projection applied %d events, want the 2 it could handle", bad.applied)
	}

	state, err := h.engine.State(ctx, "poison")
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != projection.Degraded {
		t.Errorf("status = %s, want degraded: a projection that skipped an event is incomplete and must say so", state.Status)
	}
	if state.OpenDeadLetters != 1 {
		t.Errorf("%d dead letters, want 1", state.OpenDeadLetters)
	}
	head, _ := h.engine.Head(ctx)
	if state.Checkpoint != head {
		t.Errorf("checkpoint %d, ledger head %d: a poison event must not freeze the projection in the past", state.Checkpoint, head)
	}

	letters, err := h.runner.DeadLetters(ctx, "poison")
	if err != nil {
		t.Fatal(err)
	}
	if len(letters) != 1 {
		t.Fatalf("%d dead letters", len(letters))
	}
	if letters[0].EventType != "WEIGHT_RECORDED" || !strings.Contains(letters[0].Error, "cannot handle") {
		t.Errorf("the dead letter does not say what failed: %+v", letters[0])
	}
	if letters[0].Attempts < 2 {
		t.Errorf("the event was tried %d times; a transient failure deserves a retry", letters[0].Attempts)
	}

	// And the other projection, in the same process, is untouched.
	vitals, err := h.engine.State(ctx, "visit_vital")
	if err != nil {
		t.Fatal(err)
	}
	if vitals.Status != projection.Healthy {
		t.Errorf("one projection's failure degraded another: %s", vitals.Status)
	}
	if got := h.count(t, `SELECT count(*) FROM read.visit_vital WHERE visit_id = $1`, visit); got != 3 {
		t.Errorf("the healthy projection has %d rows, want 3", got)
	}
}

// Resolving the last dead letter returns the projection to healthy. It does not re-apply
// the event: a projection that skipped one is missing what that event implied, and the
// honest repair is a rebuild — which the message says.
func TestResolvingTheLastDeadLetterRestoresHealth(t *testing.T) {
	bad := &poison{failOn: "WEIGHT_RECORDED"}
	h := newHarness(t, bad)
	ctx := context.Background()
	visit, patient := uuid.New(), uuid.New()

	h.append(t, h.measurement(visit, patient, "WEIGHT_RECORDED", "WEIGHT", 70, "ANTHROPOMETRY"))
	h.append(t, h.measurement(visit, patient, "WEIGHT_RECORDED", "WEIGHT", 71, "ANTHROPOMETRY"))
	h.catchUp(t)

	letters, err := h.runner.DeadLetters(ctx, "poison")
	if err != nil || len(letters) != 2 {
		t.Fatalf("%d dead letters, %v", len(letters), err)
	}

	if err := h.runner.Resolve(ctx, "poison", letters[0].ID, "rebuilt after the payload fix"); err != nil {
		t.Fatal(err)
	}
	if state, _ := h.engine.State(ctx, "poison"); state.Status != projection.Degraded {
		t.Errorf("status = %s with one letter still open", state.Status)
	}
	if err := h.runner.Resolve(ctx, "poison", letters[1].ID, "rebuilt after the payload fix"); err != nil {
		t.Fatal(err)
	}
	state, _ := h.engine.State(ctx, "poison")
	if state.Status != projection.Healthy {
		t.Errorf("status = %s once every letter is resolved", state.Status)
	}
	if state.OpenDeadLetters != 0 {
		t.Errorf("%d letters still open", state.OpenDeadLetters)
	}
}

// A rebuild clears the failures of the derivation that no longer exists.
func TestARebuildClearsTheDeadLetters(t *testing.T) {
	bad := &poison{failOn: "WEIGHT_RECORDED"}
	h := newHarness(t, bad)
	ctx := context.Background()
	visit, patient := uuid.New(), uuid.New()
	h.append(t, h.measurement(visit, patient, "WEIGHT_RECORDED", "WEIGHT", 70, "ANTHROPOMETRY"))
	h.catchUp(t)

	if state, _ := h.engine.State(ctx, "poison"); state.OpenDeadLetters != 1 {
		t.Fatalf("%d dead letters before the fix", state.OpenDeadLetters)
	}
	bad.failOn = "" // the fix
	if _, err := h.engine.Rebuild(ctx, "poison", projection.RebuildOptions{Logger: testLogger()}); err != nil {
		t.Fatal(err)
	}
	state, _ := h.engine.State(ctx, "poison")
	if state.OpenDeadLetters != 0 || state.Status != projection.Healthy {
		t.Errorf("after a rebuild: %d letters, status %s", state.OpenDeadLetters, state.Status)
	}
}

// --- projection versioning ---

// versioned is the same projection at two versions of its derivation.
type versioned struct{ version int }

func (v *versioned) Name() string                                          { return "versioned" }
func (v *versioned) Version() int                                          { return v.version }
func (v *versioned) Mode() projection.Mode                                 { return projection.Asynchronous }
func (v *versioned) Handles(string) bool                                   { return true }
func (v *versioned) Apply(context.Context, pgx.Tx, eventstore.Event) error { return nil }
func (v *versioned) Reset(context.Context, pgx.Tx) error                   { return nil }

// A derivation that changed under rows computed by the old one is a refusal, not a silent
// continuation: the rows are wrong in a way no further event will correct (§7.10).
func TestAChangedDerivationRefusesToAdvanceUntilItIsRebuilt(t *testing.T) {
	p := &versioned{version: 1}
	h := newHarness(t, p)
	ctx := context.Background()
	visit, patient := uuid.New(), uuid.New()
	h.append(t, h.measurement(visit, patient, "HEIGHT_RECORDED", "HEIGHT", 150, "ANTHROPOMETRY"))
	h.catchUp(t)

	// The derivation changes. The stored rows were computed by version 1.
	p.version = 2
	h.append(t, h.measurement(visit, patient, "WEIGHT_RECORDED", "WEIGHT", 70, "ANTHROPOMETRY"))

	_, err := h.runner.Advance(ctx, p)
	if !errors.Is(err, projection.ErrStaleVersion) {
		t.Fatalf("err = %v, want ErrStaleVersion", err)
	}
	if !strings.Contains(err.Error(), "projector rebuild versioned") {
		t.Errorf("the refusal must say what to do about it: %v", err)
	}

	// And a rebuild is what clears it.
	if _, err := h.engine.Rebuild(ctx, "versioned", projection.RebuildOptions{Logger: testLogger()}); err != nil {
		t.Fatal(err)
	}
	state, err := h.engine.State(ctx, "versioned")
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != 2 || state.Stale(p) {
		t.Errorf("after a rebuild the stored version is %d", state.Version)
	}
	if _, err := h.runner.Advance(ctx, p); err != nil {
		t.Errorf("advancing after a rebuild: %v", err)
	}
}

// --- criterion 2: lag is a number somebody can alert on ---

func TestLagIsTheDistanceFromTheLedgerHead(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	visit, patient := uuid.New(), uuid.New()

	// Five events. The synchronous projection commits with each of them; the asynchronous
	// one has not run.
	for i := 0; i < 5; i++ {
		h.append(t, h.measurement(visit, patient, "HEIGHT_RECORDED", "HEIGHT", 150+float64(i), "ANTHROPOMETRY"))
	}

	lags, err := h.engine.Lags(ctx, h.clock.Now().Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]projection.Lag{}
	for _, l := range lags {
		byName[l.Name] = l
	}

	if got := byName["visit_vital"].Behind; got != 0 {
		t.Errorf("the synchronous projection is %d events behind; it commits with the event and can never be", got)
	}
	if got := byName["visit_vital"].Age; got != 0 {
		t.Errorf("the synchronous projection has an age of %s", got)
	}
	if got := byName["station_activity"].Behind; got != 5 {
		t.Errorf("the asynchronous projection is %d events behind, want 5", got)
	}

	h.catchUp(t)
	lags, err = h.engine.Lags(ctx, h.clock.Now().Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lags {
		if l.Behind != 0 {
			t.Errorf("%s is still %d behind after catching up", l.Name, l.Behind)
		}
		if l.Age != 0 {
			t.Errorf("%s reports an age of %s while up to date; a quiet clinic is not a stale projection", l.Name, l.Age)
		}
	}
}

// --- the synchronous guarantee ---

// A synchronous projection commits with its event. The test is the one that matters: when
// the append fails, the read model must not hold the row either.
func TestASynchronousProjectionCommitsWithItsEvent(t *testing.T) {
	h := newHarness(t, projection.VisitVital{})
	ctx := context.Background()
	visit, patient := uuid.New(), uuid.New()

	written := h.append(t, h.measurement(visit, patient, "HEIGHT_RECORDED", "HEIGHT", 150, "ANTHROPOMETRY"))
	if got := h.count(t, `SELECT count(*) FROM read.visit_vital WHERE visit_id = $1`, visit); got != 1 {
		t.Fatalf("the vitals strip has %d rows immediately after the append", got)
	}
	state, err := h.engine.State(ctx, "visit_vital")
	if err != nil {
		t.Fatal(err)
	}
	if state.Checkpoint != written.GlobalSeq {
		t.Errorf("checkpoint %d, event %d: a synchronous projection's checkpoint is the event it just wrote",
			state.Checkpoint, written.GlobalSeq)
	}

	// An append the ledger refuses leaves no row behind. The envelope below is refused by
	// the registry, before the transaction is opened; the one after it is refused by the
	// projection, inside it.
	bad := h.measurement(visit, patient, "HEIGHT_RECORDED", "HEIGHT", 150, "ANTHROPOMETRY")
	bad.VisitID = nil
	if _, err := h.events.Append(ctx, bad); err == nil {
		t.Fatal("an event with no visit was accepted")
	}
	if got := h.count(t, `SELECT count(*) FROM read.visit_vital WHERE visit_id = $1`, visit); got != 1 {
		t.Errorf("a refused append left the read model with %d rows", got)
	}
	if n, _ := h.events.Count(ctx); n != 1 {
		t.Errorf("the ledger holds %d events after a refused append", n)
	}
}

// --- the privilege boundary ---

// The application role may read every read model and write none of them. This is the CP03
// invariant, and the reason a synchronous projection is a SECURITY DEFINER function rather
// than an INSERT from the handler.
func TestTheApplicationRoleCannotWriteAReadModel(t *testing.T) {
	h := newHarness(t)
	visit, patient := uuid.New(), uuid.New()
	h.append(t, h.measurement(visit, patient, "HEIGHT_RECORDED", "HEIGHT", 150, "ANTHROPOMETRY"))

	app := h.db.OpenAs(t, "dthcms_app_local", "dthcms_local_only")

	var value float64
	if err := app.QueryRow(`SELECT value FROM read.visit_vital WHERE visit_id = $1`, visit).Scan(&value); err != nil {
		t.Fatalf("the application cannot read a read model: %v", err)
	}

	for name, statement := range map[string]string{
		"updated a value":    `UPDATE read.visit_vital SET value = 999`,
		"deleted a row":      `DELETE FROM read.visit_vital`,
		"emptied the table":  `TRUNCATE read.visit_vital`,
		"inserted a row":     `INSERT INTO read.station_activity (facility_id, clinic_day, station) VALUES (gen_random_uuid(), current_date, 'X')`,
		"moved a checkpoint": `UPDATE read.projection_state SET checkpoint = 0`,
	} {
		if _, err := app.Exec(statement); err == nil {
			t.Errorf("the application %s; a read model it can edit is no longer derived", name)
		} else if !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s failed for the wrong reason: %v", name, err)
		}
	}

	// And the one door it does have is the derivation itself, which it may call and
	// nothing more. This is the path the append transaction actually takes in production,
	// where the connection is the application's: the write lands, as the projector.
	event := fmt.Sprintf(`{
		"visit_id": %q, "code": "TEMP", "facility_id": %q, "value": 37.1, "unit": "C",
		"taken_at": "2026-09-03T04:42:00Z", "recorded_at": "2026-09-03T04:42:01Z",
		"actor_user_id": %q, "actor_role": "VITALS", "actor_station": "VITALS",
		"event_id": %q, "global_seq": 9999
	}`, visit, h.facility, h.user, uuid.Must(uuid.NewV7()))
	if _, err := app.Exec(`SELECT read.apply_visit_vital($1::jsonb)`, event); err != nil {
		t.Fatalf("the application cannot maintain the synchronous read model: %v", err)
	}
	if got := h.count(t, `SELECT count(*) FROM read.visit_vital WHERE visit_id = $1 AND code = 'TEMP'`, visit); got != 1 {
		t.Errorf("the derivation ran as the application and wrote %d rows", got)
	}

	// It is still only the derivation: nonsense is refused rather than written.
	if _, err := app.Exec(`SELECT read.apply_visit_vital($1::jsonb)`, `{"global_seq": 1}`); err == nil {
		t.Error("apply_visit_vital accepted an event with no code")
	}
	// And emptying the model is not a door it has at all.
	if _, err := app.Exec(`SELECT read.reset_visit_vital()`); err == nil {
		t.Error("the application emptied a read model")
	}

	// The invariants agree, which is what the service checks on every start.
	if _, err := h.db.SQL.Exec(`SELECT core.assert_invariants()`); err != nil {
		t.Errorf("assert_invariants: %v", err)
	}
}

// --- the runner as a loop ---

// Run keeps up with a writer. The point is not throughput; it is that the loop terminates
// on a cancelled context and leaves the checkpoint where the work got to.
func TestTheRunnerFollowsTheLedgerAndStopsCleanly(t *testing.T) {
	h := newHarness(t, projection.StationActivity{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := projection.NewRunner(h.engine, h.events, projection.RunnerConfig{
		BatchSize: 10, Interval: 5 * time.Millisecond, Logger: testLogger(),
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = runner.Run(ctx)
	}()

	visit, patient := uuid.New(), uuid.New()
	for i := 0; i < 25; i++ {
		h.append(t, h.measurement(visit, patient, "HEIGHT_RECORDED", "HEIGHT", 150, "ANTHROPOMETRY"))
	}

	head, err := h.engine.Head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		state, err := h.engine.State(context.Background(), "station_activity")
		if err != nil {
			t.Fatal(err)
		}
		if state.Checkpoint == head {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the runner reached %d of %d", state.Checkpoint, head)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner did not stop when its context was cancelled")
	}

	if got := h.count(t, `SELECT events FROM read.station_activity WHERE station = 'ANTHROPOMETRY'`); got != 25 {
		t.Errorf("the board counted %d events, want 25", got)
	}
}
