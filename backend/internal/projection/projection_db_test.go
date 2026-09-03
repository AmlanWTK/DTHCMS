package projection_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// The projection framework against a real database (CP25).
//
// Everything worth asserting here is a property of the real thing — that the application
// role cannot write a read model, that a synchronous projection commits with its event,
// that a replay produces the same bytes — so nothing is mocked.

type harness struct {
	db       *testsupport.DB
	pool     *pgxpool.Pool
	events   *eventstore.Store
	engine   *projection.Engine
	runner   *projection.Runner
	registry *projection.Registry
	clock    *clock.Fixed
	facility uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
	seq      int
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newHarness builds a ledger with the synchronous projections attached — the API's wiring —
// and a runner for the asynchronous ones — the projector's.
func newHarness(t *testing.T, projections ...projection.Projection) *harness {
	t.Helper()
	if len(projections) == 0 {
		projections = []projection.Projection{projection.VisitVital{}, projection.StationActivity{}}
	}
	db := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, db.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	registry := projection.NewRegistry(projections...)
	h := &harness{
		db: db, pool: pool, registry: registry,
		clock: clock.NewFixed(time.Date(2026, 9, 3, 4, 42, 0, 0, time.UTC)),
		user:  uuid.New(), device: uuid.New(),
	}
	if err := db.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}
	h.events = eventstore.New(eventstore.Config{
		Pool: pool, Clock: h.clock, Synchronous: projection.NewSyncSet(registry),
	})
	h.engine = projection.NewEngineWithEvents(pool, registry, h.events)
	if err := h.engine.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.runner = projection.NewRunner(h.engine, h.events, projection.RunnerConfig{
		BatchSize: 50, MaxAttempts: 2, RetryPause: time.Millisecond, Logger: testLogger(),
	})
	return h
}

// measurement is a complete, valid envelope. Each call moves the clock on a second, so
// recorded_at is distinct and the clinic day is deterministic.
func (h *harness) measurement(visit, patient uuid.UUID, eventType, code string, value float64, station string) eventstore.Envelope {
	role := station
	if role == "" {
		// A physician at a desk: a real role wearing no station. The board must ignore
		// these; the vitals strip must not.
		role = "PHYSICIAN"
	}
	h.seq++
	h.clock.Advance(time.Second)
	return eventstore.Envelope{
		EventID: uuid.Must(uuid.NewV7()), AggregateType: "VISIT", AggregateID: visit,
		PatientID: &patient, VisitID: &visit,
		EventType: eventType, EventVersion: 1, OccurredAt: h.clock.Now().Add(-5 * time.Second),
		Actor:   eventstore.ActorForTest(h.user, h.device, h.facility, role, station),
		Source:  eventstore.SourceMobileOnline,
		Payload: json.RawMessage(fmt.Sprintf(`{"code":%q,"value":%g,"unit":%q}`, code, value, unitFor(code))),
	}
}

func (h *harness) bloodPressure(visit, patient uuid.UUID, systolic, diastolic float64, station string) eventstore.Envelope {
	h.seq++
	h.clock.Advance(time.Second)
	return eventstore.Envelope{
		EventID: uuid.Must(uuid.NewV7()), AggregateType: "VISIT", AggregateID: visit,
		PatientID: &patient, VisitID: &visit,
		EventType: "BP_RECORDED", EventVersion: 1, OccurredAt: h.clock.Now().Add(-5 * time.Second),
		Actor:   eventstore.ActorForTest(h.user, h.device, h.facility, station, station),
		Source:  eventstore.SourceMobileOnline,
		Payload: json.RawMessage(fmt.Sprintf(`{"systolic":%g,"diastolic":%g,"unit":"mmHg"}`, systolic, diastolic)),
	}
}

func unitFor(code string) string {
	switch code {
	case "HEIGHT", "WAIST", "HIP":
		return "cm"
	case "WEIGHT":
		return "kg"
	case "PULSE":
		return "bpm"
	case "SPO2":
		return "%"
	case "TEMP":
		return "C"
	}
	return ""
}

// catchUp drives every asynchronous projection to the head of the ledger.
func (h *harness) catchUp(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, p := range h.registry.InMode(projection.Asynchronous) {
		for pass := 0; pass < 200; pass++ {
			n, err := h.runner.Advance(ctx, p)
			if err != nil {
				t.Fatalf("%s: %v", p.Name(), err)
			}
			if n == 0 {
				break
			}
		}
	}
}

// checksums is every read model's content, in a form two runs can be compared by. Row
// order is made deterministic by the ORDER BY; `updated_at` is excluded because it is the
// wall clock, not the derivation — a rebuild that produced identical rows an hour later
// would otherwise "differ".
func (h *harness) checksums(t *testing.T) map[string]string {
	t.Helper()
	queries := map[string]string{
		"visit_vital": `SELECT visit_id, code, facility_id, patient_id, value, unit, value_2,
		                       taken_at, recorded_at, actor_user_id, actor_role, actor_station,
		                       event_id, global_seq, corrected
		                  FROM read.visit_vital ORDER BY visit_id, code`,
		"station_activity": `SELECT facility_id, clinic_day, station, events, last_seq
		                       FROM read.station_activity ORDER BY facility_id, clinic_day, station`,
		"station_activity_visit": `SELECT facility_id, clinic_day, station, visit_id
		                             FROM read.station_activity_visit
		                            ORDER BY facility_id, clinic_day, station, visit_id`,
	}
	out := map[string]string{}
	for name, query := range queries {
		var digest string
		if err := h.db.SQL.QueryRow(
			`SELECT COALESCE(md5(string_agg(row::text, '|' ORDER BY row::text)), 'empty')
			   FROM (` + query + `) row`).Scan(&digest); err != nil {
			t.Fatalf("checksum of %s: %v", name, err)
		}
		out[name] = digest
	}
	return out
}

func (h *harness) count(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := h.db.SQL.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// fixture writes a dataset with the shapes that break naive projections: several visits,
// several stations, repeated measurements of the same code, corrections, a paired reading,
// and events from a role with no station.
func (h *harness) fixture(t *testing.T) {
	t.Helper()
	stations := []string{"ANTHROPOMETRY", "VITALS", "ANTHROPOMETRY"}
	for i := 0; i < 12; i++ {
		visit, patient := uuid.New(), uuid.New()
		station := stations[i%len(stations)]
		h.append(t, h.measurement(visit, patient, "HEIGHT_RECORDED", "HEIGHT", 150+float64(i), station))
		h.append(t, h.measurement(visit, patient, "WEIGHT_RECORDED", "WEIGHT", 60+float64(i), station))
		h.append(t, h.bloodPressure(visit, patient, 120+float64(i), 80, station))
		if i%3 == 0 {
			// The 140/150 case (§7.7): the same code recorded again, then corrected.
			h.append(t, h.measurement(visit, patient, "WEIGHT_RECORDED", "WEIGHT", 61+float64(i), station))
			h.append(t, h.measurement(visit, patient, "WEIGHT_CORRECTED", "WEIGHT", 62+float64(i), station))
		}
		if i%4 == 0 {
			// A physician at a desk: a role with no station, which the board must ignore
			// and the vitals strip must not.
			h.append(t, h.measurement(visit, patient, "PULSE_RECORDED", "PULSE", 70+float64(i), ""))
		}
	}
}

// append writes one envelope, failing the test rather than returning an error nobody
// checks. Every append here runs the synchronous projections too — that is the point.
func (h *harness) append(t *testing.T, e eventstore.Envelope) eventstore.Event {
	t.Helper()
	written, err := h.events.Append(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	return written
}

// --- criterion 1: replay equivalence ---

// TestAReplayProducesTheSameReadModels is the test the plan calls critical, and the one
// that makes "the event log is the source of truth" a fact rather than an aspiration.
//
// It builds the read models incrementally — synchronously inside each append, and
// asynchronously by the runner — then throws every row away, replays the whole ledger, and
// asserts the result is byte-identical. It does not skip under -short: a check that runs
// only when somebody remembers to ask for it is not a check.
func TestAReplayProducesTheSameReadModels(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.fixture(t)
	h.catchUp(t)

	incremental := h.checksums(t)
	if incremental["visit_vital"] == "empty" || incremental["station_activity"] == "empty" {
		t.Fatal("the fixture produced no read model rows; the test would prove nothing")
	}
	rowsBefore := h.count(t, `SELECT count(*) FROM read.visit_vital`)

	results, err := h.engine.RebuildAll(ctx, projection.RebuildOptions{BatchSize: 7, Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("rebuilt %d projections, want 2", len(results))
	}

	replayed := h.checksums(t)
	for table, want := range incremental {
		if replayed[table] != want {
			t.Errorf("%s differs after a replay:\n incremental %s\n replayed    %s", table, want, replayed[table])
		}
	}
	if got := h.count(t, `SELECT count(*) FROM read.visit_vital`); got != rowsBefore {
		t.Errorf("visit_vital has %d rows after a rebuild, had %d", got, rowsBefore)
	}

	// And the register agrees: both projections healthy, both at the ledger's head.
	head, err := h.engine.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states, err := h.engine.States(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range states {
		if s.Status != projection.Healthy {
			t.Errorf("%s is %s after a rebuild", s.Name, s.Status)
		}
		if s.Checkpoint != head {
			t.Errorf("%s is at %d, the ledger is at %d", s.Name, s.Checkpoint, head)
		}
		if s.RebuiltAt == nil {
			t.Errorf("%s does not record when it was rebuilt", s.Name)
		}
	}
}

// A rebuild run twice is the same as a rebuild run once — which is the same property from
// the other side, and the one a nervous operator will actually test in production.
func TestARebuildIsIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.fixture(t)
	h.catchUp(t)

	if _, err := h.engine.RebuildAll(ctx, projection.RebuildOptions{Logger: testLogger()}); err != nil {
		t.Fatal(err)
	}
	first := h.checksums(t)
	if _, err := h.engine.RebuildAll(ctx, projection.RebuildOptions{Logger: testLogger()}); err != nil {
		t.Fatal(err)
	}
	for table, want := range first {
		if got := h.checksums(t)[table]; got != want {
			t.Errorf("%s differs after a second rebuild", table)
		}
	}
}

// Applying the same event twice changes nothing, and applying an older event after a newer
// one changes nothing either. Both are what a crashed runner and a lagging rebuild do.
func TestApplyingAnEventTwiceChangesNothing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	visit, patient := uuid.New(), uuid.New()

	first := h.append(t, h.measurement(visit, patient, "WEIGHT_RECORDED", "WEIGHT", 70, "ANTHROPOMETRY"))
	second := h.append(t, h.measurement(visit, patient, "WEIGHT_CORRECTED", "WEIGHT", 72, "ANTHROPOMETRY"))
	h.catchUp(t)
	after := h.checksums(t)

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, p := range []projection.Projection{projection.VisitVital{}, projection.StationActivity{}} {
		// The newer event again, then the older one after it.
		for _, e := range []eventstore.Event{second, second, first} {
			if err := p.Apply(ctx, tx, e); err != nil {
				t.Fatalf("%s: %v", p.Name(), err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	for table, want := range after {
		if got := h.checksums(t)[table]; got != want {
			t.Errorf("%s changed when events were re-applied out of order", table)
		}
	}
	var value float64
	if err := h.db.SQL.QueryRow(
		`SELECT value FROM read.visit_vital WHERE visit_id = $1 AND code = 'WEIGHT'`, visit).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 72 {
		t.Errorf("the corrected weight is %g, want the later value 72", value)
	}
}
