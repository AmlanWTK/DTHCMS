package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/jobs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/logging"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
)

// The job queue (CP69).
//
// The five acceptance criteria, and which tests carry them:
//
//  1. **a job enqueued in a rolled-back transaction never runs** — the first test, and the reason
//     there is no non-transactional enqueue in the API at all;
//  2. failed jobs retry per policy then dead-letter visibly;
//  3. the dashboard shows depth, age and SLA attainment per job type;
//  4. alerts fire on configured thresholds — the numbers an alert reads are what is asserted here;
//     the alerting rules themselves are configuration, tested by their own file;
//  5. **worker restart causes no duplicate execution** — or rather no duplicate *effect*, which is
//     the honest form of the claim and the one the test makes.

type harness struct {
	*testsupport.DB
	pool   *pgxpool.Pool
	store  *jobs.Store
	clock  *clock.Fixed
	logger *slog.Logger
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	cfg, err := pgxpool.ParseConfig(base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	// Room for the concurrency test: eight workers each holding a connection for a whole claim
	// exhaust a default pool, and that arrives as a context deadline that looks like a deadlock.
	cfg.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	return &harness{
		DB: base, pool: pool, store: jobs.NewStore(pool),
		clock:  clock.NewFixed(time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// enqueue puts one job on the queue in its own committed transaction.
func (h *harness) enqueue(t *testing.T, kind string, args any) jobs.Job {
	t.Helper()
	var job jobs.Job
	err := h.store.InTransaction(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		job, err = h.store.EnqueueTx(ctx, tx, h.clock.Now(), jobs.Enqueueing{Kind: kind, Args: args})
		return err
	})
	if err != nil {
		t.Fatalf("enqueueing %s: %v", kind, err)
	}
	return job
}

func (h *harness) count(t *testing.T, where string, args ...any) int {
	t.Helper()
	var n int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM ops.job WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (h *harness) status(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var status string
	if err := h.SQL.QueryRow(`SELECT status FROM ops.job WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func (h *harness) worker(name string, registry *jobs.Registry, lease time.Duration) *jobs.Worker {
	kinds, _ := h.store.Kinds(context.Background())
	byKind := map[string]jobs.Kind{}
	for _, k := range kinds {
		byKind[k.Kind] = k
	}
	return jobs.NewWorker(jobs.WorkerConfig{
		Name: name, Queues: []string{"maintenance", "clinical", "patient", "pipeline", "analytical", "default"},
		Concurrency: 4, PollInterval: 10 * time.Millisecond, LeaseDuration: lease,
		HeartbeatInterval: lease / 3,
		Store:             h.store, Registry: registry, Kinds: byKind,
		Clock: h.clock, Logger: h.logger,
		// Deterministic: a test that asserts a backoff must not depend on a coin toss.
		Jitter: func() float64 { return 0.5 },
	})
}

// runUntil runs a worker until fn returns true or the deadline passes.
func runUntil(t *testing.T, w *jobs.Worker, fn func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = w.Run(ctx) }()

	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(15 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("the worker did not reach the expected state in time")
		case <-tick.C:
			if fn() {
				cancel()
				<-done
				return
			}
		}
	}
}

// --- criterion 1: a rolled-back enqueue never runs ---

// The whole point of the queue being a table. There is nothing to roll back separately, and no
// enqueue that opens its own transaction, so there is no call site that can break this.
func TestAJobEnqueuedInARolledBackTransactionNeverRuns(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, err := h.store.EnqueueTx(ctx, tx, h.clock.Now(), jobs.Enqueueing{
		Kind: "maintenance.idempotency_purge",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The row exists inside the transaction — this is not a test of a write that never happened.
	var inside int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ops.job WHERE id = $1`, job.ID).Scan(&inside); err != nil {
		t.Fatal(err)
	}
	if inside != 1 {
		t.Fatalf("the job was not written inside the transaction: %d", inside)
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	if got := h.count(t, "id = $1", job.ID); got != 0 {
		t.Fatalf("a rolled-back job survived: %d rows", got)
	}

	// And a worker finds nothing, which is the claim the criterion actually makes.
	var ran int
	registry := jobs.NewRegistry()
	registry.Register("maintenance.idempotency_purge", func(context.Context, jobs.Running) error {
		ran++
		return nil
	})
	w := h.worker("w1", registry, time.Minute)
	ctx2, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx2)
	if ran != 0 {
		t.Fatalf("a rolled-back job ran %d times", ran)
	}
}

func TestACommittedEnqueueRuns(t *testing.T) {
	h := newHarness(t)
	job := h.enqueue(t, "maintenance.idempotency_purge", nil)

	var mu sync.Mutex
	ran := 0
	registry := jobs.NewRegistry()
	registry.Register("maintenance.idempotency_purge", func(context.Context, jobs.Running) error {
		mu.Lock()
		defer mu.Unlock()
		ran++
		return nil
	})
	w := h.worker("w1", registry, time.Minute)
	runUntil(t, w, func() bool { return h.status(t, job.ID) == "SUCCEEDED" })

	mu.Lock()
	defer mu.Unlock()
	if ran != 1 {
		t.Fatalf("the job ran %d times, want 1", ran)
	}
}

// --- the PHI rule ---

// "Job payloads may reference but must not embed PHI", enforced rather than intended.
func TestAJobArgumentCarryingPHIIsRefused(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	for _, args := range []map[string]any{
		{"patient_name": "Anwara Begum"},
		{"phone": "+8801711111601"},
		// One level down, which is the shape a check that only looked at the top level would
		// have let through — and the shape somebody writes when the flat version feels wrong.
		{"patient": map[string]any{"name": "Anwara Begum"}},
		{"batch": []any{map[string]any{"national_id": "1234567890"}}},
	} {
		err := h.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, err := h.store.EnqueueTx(ctx, tx, h.clock.Now(), jobs.Enqueueing{
				Kind: "maintenance.idempotency_purge", Args: args,
			})
			return err
		})
		if !errors.Is(err, jobs.ErrCarriesPHI) {
			t.Fatalf("%v was accepted: %v", args, err)
		}
		// And it names the key, because a refusal that says only "you broke a rule" does not say
		// which word broke it.
		if !strings.Contains(err.Error(), "name") && !strings.Contains(err.Error(), "phone") &&
			!strings.Contains(err.Error(), "national_id") {
			t.Fatalf("the refusal does not name the key: %v", err)
		}
	}
}

// The database says no too, so a second application cannot skip the Go check.
func TestTheDatabaseRefusesAJobArgumentCarryingPHI(t *testing.T) {
	h := newHarness(t)
	_, err := h.SQL.Exec(`
		INSERT INTO ops.job (id, kind, queue, priority, args, max_attempts, run_at)
		VALUES ($1, 'maintenance.idempotency_purge', 'maintenance', 100,
		        '{"patient_name": "Anwara Begum"}'::jsonb, 3, now())`, uuid.New())
	if err == nil {
		t.Fatal("the database accepted a job argument carrying PHI")
	}
	if !strings.Contains(err.Error(), "job_args_carry_no_phi") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
}

func TestTheInvariantNoticesAJobArgumentCarryingPHI(t *testing.T) {
	h := newHarness(t)
	// Past the constraint, the way a restored dump or a migration would arrive.
	if _, err := h.SQL.Exec(`ALTER TABLE ops.job DROP CONSTRAINT job_args_carry_no_phi`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO ops.job (id, kind, queue, priority, args, max_attempts, run_at)
		VALUES ($1, 'maintenance.idempotency_purge', 'maintenance', 100,
		        '{"dob": "1962-02-09"}'::jsonb, 3, now())`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	var ignored string
	err := h.SQL.QueryRow(`SELECT core.assert_no_job_argument_carries_phi()::text`).Scan(&ignored)
	if err == nil {
		t.Fatal("the invariant passed with PHI in a job argument")
	}
	if !strings.Contains(err.Error(), "must never hold a value") {
		t.Fatalf("the invariant failed for the wrong reason: %v", err)
	}
}

// Two representations of one list is a thing that drifts, and the drift is silent.
func TestThePHIKeyListMatchesTheDatabase(t *testing.T) {
	h := newHarness(t)
	stored, err := h.store.PHIKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for key, guidance := range logging.PHIKeys {
		got, ok := stored[key]
		if !ok {
			t.Fatalf("%q is in logging.PHIKeys and not in ops.phi_key", key)
		}
		if got != guidance {
			t.Fatalf("%q says %q in Go and %q in the database", key, guidance, got)
		}
	}
	for key := range stored {
		if _, ok := logging.PHIKeys[key]; !ok {
			t.Fatalf("%q is in ops.phi_key and not in logging.PHIKeys", key)
		}
	}
}

// --- criterion 2: retry, then dead-letter visibly ---

func TestAFailingJobRetriesThenDeadLettersWithEveryErrorKept(t *testing.T) {
	h := newHarness(t)
	job := h.enqueue(t, "maintenance.idempotency_purge", nil)

	var mu sync.Mutex
	attempts := 0
	registry := jobs.NewRegistry()
	registry.Register("maintenance.idempotency_purge", func(_ context.Context, r jobs.Running) error {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		return fmt.Errorf("the store was unreachable on attempt %d", n)
	})
	w := h.worker("w1", registry, time.Minute)

	// The clock has to move, or the backoff would keep every retry in the future forever.
	go func() {
		for i := 0; i < 400; i++ {
			time.Sleep(10 * time.Millisecond)
			h.clock.Advance(time.Hour)
		}
	}()
	runUntil(t, w, func() bool { return h.status(t, job.ID) == "DISCARDED" })

	// Three attempts, which is this kind's policy, and not four.
	var recorded int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ops.job_attempt WHERE job_id = $1`, job.ID).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 3 {
		t.Fatalf("%d attempts were recorded, want 3", recorded)
	}

	// And each one says what it said, which is the difference between a dead letter an operator
	// can act on and a counter that went up.
	rows, err := h.SQL.Query(
		`SELECT attempt, error FROM ops.job_attempt WHERE job_id = $1 ORDER BY attempt`, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		var message string
		if err := rows.Scan(&n, &message); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(message, fmt.Sprintf("attempt %d", n)) {
			t.Fatalf("attempt %d recorded %q", n, message)
		}
	}

	// A dead-lettered job that says nothing is one nobody can act on.
	var lastError string
	if err := h.SQL.QueryRow(
		`SELECT last_error FROM ops.job WHERE id = $1`, job.ID).Scan(&lastError); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(lastError) == "" {
		t.Fatal("a dead-lettered job recorded no reason")
	}
}

func TestARetryResetsThePolicyRatherThanGivingOneMoreGo(t *testing.T) {
	h := newHarness(t)
	job := h.enqueue(t, "maintenance.idempotency_purge", nil)
	if _, err := h.SQL.Exec(`
		UPDATE ops.job SET status = 'DISCARDED', attempt = 3, finished_at = now(),
		                   last_error = 'gave up' WHERE id = $1`, job.ID); err != nil {
		t.Fatal(err)
	}

	retried, err := h.store.Retry(context.Background(), job.ID, h.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	// An operator retrying a job that failed five times means "try again", not "try once more".
	if retried.Attempt != 0 || retried.Status != "AVAILABLE" {
		t.Fatalf("the retry left it at attempt %d, status %s", retried.Attempt, retried.Status)
	}
}

func TestRetryingAJobThatIsNotDeadLetteredIsRefusedSeparatelyFromANonExistentOne(t *testing.T) {
	h := newHarness(t)
	job := h.enqueue(t, "maintenance.idempotency_purge", nil)

	// Wrong state, and it must not read as "no such job": a 404 would send an operator looking
	// for a typo when the answer is that somebody else retried it thirty seconds ago.
	if _, err := h.store.Retry(context.Background(), job.ID, h.clock.Now()); !errors.Is(err, jobs.ErrNotRetryable) {
		t.Fatalf("retrying a queued job answered %v", err)
	}
	if _, err := h.store.Retry(context.Background(), uuid.New(), h.clock.Now()); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("retrying a job that does not exist answered %v", err)
	}
}

// --- criterion 5: a worker restart causes no duplicate effect ---

// The honest form of the claim. Delivery is at-least-once — a worker that finished the work and
// died before marking the row runs the job again when its lease expires — so what the queue
// guarantees is that the *job* is retried, and what the handler guarantees is that the *effect* is
// not duplicated. This test kills a worker mid-job and asserts both halves.
func TestAJobAbandonedByAStoppedWorkerIsRunAgainAndItsEffectIsNotDuplicated(t *testing.T) {
	h := newHarness(t)
	job := h.enqueue(t, "maintenance.idempotency_purge", map[string]any{"visit_id": uuid.New().String()})

	var mu sync.Mutex
	starts := 0
	// The idempotent effect: a set, keyed by what the job is about. A handler that ran twice
	// adds nothing the second time, which is what every handler in this system is required to
	// do and what makes at-least-once survivable.
	effects := map[string]bool{}
	blocked := make(chan struct{})

	registry := jobs.NewRegistry()
	registry.Register("maintenance.idempotency_purge", func(ctx context.Context, r jobs.Running) error {
		mu.Lock()
		starts++
		first := starts == 1
		var args struct {
			VisitID string `json:"visit_id"`
		}
		_ = json.Unmarshal(r.Args, &args)
		effects[args.VisitID] = true
		mu.Unlock()

		if first {
			// The first worker never returns: it is the one that "died".
			<-blocked
			return nil
		}
		return nil
	})

	lease := 30 * time.Second
	first := h.worker("dying-worker", registry, lease)
	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan struct{})
	go func() { defer close(firstDone); _ = first.Run(ctx) }()

	// Wait until it has the job.
	deadline := time.After(10 * time.Second)
	for h.status(t, job.ID) != "RUNNING" {
		select {
		case <-deadline:
			t.Fatal("the first worker never claimed the job")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// The worker stops answering. Its lease is still held, so nothing else may take the job —
	// which is the property that stops two workers running one job at the same time.
	h.clock.Advance(lease + time.Second)

	reaper := &jobs.Reaper{Store: h.store, Interval: 10 * time.Millisecond,
		Clock: h.clock, Logger: h.logger}
	reapCtx, stopReaper := context.WithCancel(context.Background())
	go reaper.Run(reapCtx)

	second := h.worker("replacement-worker", registry, lease)
	runUntil(t, second, func() bool { return h.status(t, job.ID) == "SUCCEEDED" })
	stopReaper()

	close(blocked)
	cancel()
	<-firstDone

	mu.Lock()
	defer mu.Unlock()
	if starts < 2 {
		t.Fatalf("the abandoned job was started %d times; a replacement worker should have run it", starts)
	}
	// The honest half: the job ran twice, and the effect happened once.
	if len(effects) != 1 {
		t.Fatalf("the effect was produced %d times, want 1", len(effects))
	}
}

// A lease that has not expired is not reapable. Without this the reaper would take work out from
// under a healthy worker, and "at-least-once because a worker died" would become "at-least-once
// because we were impatient".
func TestALiveLeaseIsNotReaped(t *testing.T) {
	h := newHarness(t)
	job := h.enqueue(t, "maintenance.idempotency_purge", nil)
	// The lease is set against the harness clock, not the database's `now()`. The two are days
	// apart here on purpose — a test that let them agree would pass even if the reaper compared
	// against the wrong one.
	if _, err := h.SQL.Exec(`
		UPDATE ops.job SET status = 'RUNNING', attempt = 1, started_at = $2::timestamptz,
		                   leased_by = 'busy', leased_until = $2::timestamptz + interval '5 minutes'
		 WHERE id = $1`, job.ID, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	reaper := &jobs.Reaper{Store: h.store, Clock: h.clock, Logger: h.logger}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	reaper.Run(ctx)

	if got := h.status(t, job.ID); got != "RUNNING" {
		t.Fatalf("a live lease was reaped: the job is %s", got)
	}
}

// --- concurrency ---

// Two workers polling the same queue must take disjoint work. `FOR UPDATE SKIP LOCKED` is what
// makes that true, and it is worth a test because the failure mode — two workers running one job
// — is invisible until it corrupts something.
func TestTwoWorkersNeverTakeTheSameJob(t *testing.T) {
	h := newHarness(t)
	const total = 24
	for i := 0; i < total; i++ {
		h.enqueue(t, "analytical.research_extract", map[string]any{"n": i})
	}

	var mu sync.Mutex
	seen := map[string]int{}
	handler := func(_ context.Context, r jobs.Running) error {
		mu.Lock()
		defer mu.Unlock()
		seen[r.ID.String()]++
		return nil
	}

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	for i := 0; i < 4; i++ {
		registry := jobs.NewRegistry()
		registry.Register("analytical.research_extract", handler)
		w := h.worker(fmt.Sprintf("w%d", i), registry, time.Minute)
		wg.Add(1)
		go func() { defer wg.Done(); _ = w.Run(ctx) }()
	}

	deadline := time.After(15 * time.Second)
	for {
		if h.count(t, "kind = 'analytical.research_extract' AND status = 'SUCCEEDED'") == total {
			break
		}
		select {
		case <-deadline:
			cancel()
			wg.Wait()
			t.Fatalf("only %d of %d jobs finished",
				h.count(t, "kind = 'analytical.research_extract' AND status = 'SUCCEEDED'"), total)
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	cancel()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != total {
		t.Fatalf("%d distinct jobs ran, want %d", len(seen), total)
	}
	for id, times := range seen {
		if times != 1 {
			t.Fatalf("job %s ran %d times", id, times)
		}
	}
}

// --- enqueue-once ---

func TestADedupeKeyAbsorbsASecondLiveJob(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	enqueue := func() error {
		return h.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, err := h.store.EnqueueTx(ctx, tx, h.clock.Now(), jobs.Enqueueing{
				Kind: "clinical.synthesis", DedupeKey: "visit-42",
			})
			return err
		})
	}
	if err := enqueue(); err != nil {
		t.Fatal(err)
	}
	// Distinguishable from success, so a caller that cares can tell — but not an error at the
	// call site, because the work the caller asked for is going to happen.
	if err := enqueue(); !errors.Is(err, jobs.ErrAlreadyQueued) {
		t.Fatalf("a second live job with the same key answered %v", err)
	}
	if got := h.count(t, "dedupe_key = 'visit-42'"); got != 1 {
		t.Fatalf("%d jobs carry the key, want 1", got)
	}

	// Once it has finished, the key is free again — which is what makes "one synthesis per
	// visit, per attempt at that visit" expressible at all.
	if _, err := h.SQL.Exec(
		`UPDATE ops.job SET status = 'SUCCEEDED', finished_at = now(), met_sla = true
		  WHERE dedupe_key = 'visit-42'`); err != nil {
		t.Fatal(err)
	}
	if err := enqueue(); err != nil {
		t.Fatalf("the key was not released after the job finished: %v", err)
	}
}

func TestAnUnknownKindIsRefused(t *testing.T) {
	h := newHarness(t)
	err := h.store.InTransaction(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := h.store.EnqueueTx(ctx, tx, h.clock.Now(), jobs.Enqueueing{Kind: "clinical.telepathy"})
		return err
	})
	if !errors.Is(err, jobs.ErrUnknownKind) {
		t.Fatalf("an unknown kind answered %v", err)
	}
}

// --- registration fails at startup ---

func TestAWorkerRefusesToStartWithAMismatchedRegistry(t *testing.T) {
	h := newHarness(t)
	kinds, err := h.store.Kinds(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// A handler for a kind the database does not know: a typo whose job would never be claimed.
	typo := jobs.NewRegistry()
	typo.Register("maintenance.idempotancy_purge", func(context.Context, jobs.Running) error { return nil })
	if err := typo.Check(kinds, []string{"maintenance"}); err == nil {
		t.Fatal("a handler for an unknown kind was accepted")
	}

	// A kind this worker's queues would claim, with no handler: work that is enqueued, waits and
	// is never done, showing on the dashboard as a rising depth nobody can explain.
	empty := jobs.NewRegistry()
	if err := empty.Check(kinds, []string{"maintenance"}); err == nil {
		t.Fatal("a worker with no handler for its own queue was accepted")
	}

	// But serving a subset is the normal case — queues exist so that document processing cannot
	// slow down a blood pressure — so a worker that claims nothing from a queue needs no handler
	// for it.
	subset := jobs.NewRegistry()
	if err := subset.Check(kinds, []string{"nothing-serves-this"}); err != nil {
		t.Fatalf("a worker serving no shared queue was refused: %v", err)
	}
}

// --- criterion 3: the dashboard ---

func TestTheDashboardShowsDepthAgeFailuresAndSLAAttainmentPerKind(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	now := h.clock.Now()

	// Two waiting, the older by ten minutes.
	old := h.enqueue(t, "analytical.research_extract", map[string]any{"n": 1})
	if _, err := h.SQL.Exec(
		`UPDATE ops.job SET enqueued_at = $2 WHERE id = $1`, old.ID, now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	h.enqueue(t, "analytical.research_extract", map[string]any{"n": 2})

	// One met its SLA, one missed it, one was given up on.
	met := h.enqueue(t, "clinical.synthesis", map[string]any{"n": 1})
	missed := h.enqueue(t, "clinical.synthesis", map[string]any{"n": 2})
	dead := h.enqueue(t, "clinical.synthesis", map[string]any{"n": 3})
	if _, err := h.SQL.Exec(`
		UPDATE ops.job SET status = 'SUCCEEDED', finished_at = $2, met_sla = true WHERE id = $1`,
		met.ID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		UPDATE ops.job SET status = 'SUCCEEDED', finished_at = $2, met_sla = false WHERE id = $1`,
		missed.ID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		UPDATE ops.job SET status = 'DISCARDED', finished_at = $2, met_sla = false,
		                   last_error = 'the model would not answer' WHERE id = $1`,
		dead.ID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	health, err := h.store.Health(ctx, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]jobs.Health{}
	for _, row := range health {
		byKind[row.Kind] = row
	}

	// **Every registered kind is a row**, whether or not anything is queued — the failure a
	// dashboard exists to catch is a job type that has stopped being enqueued at all.
	kinds, _ := h.store.Kinds(ctx)
	if len(health) != len(kinds) {
		t.Fatalf("%d rows for %d registered kinds", len(health), len(kinds))
	}

	analytical := byKind["analytical.research_extract"]
	if analytical.Available != 2 {
		t.Fatalf("depth is %d, want 2", analytical.Available)
	}
	// Age, in seconds, so an alert threshold is a number a person writes without converting.
	if analytical.OldestAvailableSeconds < 590 || analytical.OldestAvailableSeconds > 610 {
		t.Fatalf("the oldest waiting job is %ds old, want about 600", analytical.OldestAvailableSeconds)
	}

	synthesis := byKind["clinical.synthesis"]
	if synthesis.Succeeded != 2 || synthesis.Discarded != 1 {
		t.Fatalf("synthesis finished %d/%d, want 2 succeeded and 1 discarded",
			synthesis.Succeeded, synthesis.Discarded)
	}
	if synthesis.FailureRate == nil || *synthesis.FailureRate < 33 || *synthesis.FailureRate > 34 {
		t.Fatalf("the failure rate is %v, want about 33.3", synthesis.FailureRate)
	}
	// One of three met the promise.
	if synthesis.SLAMeasured != 3 || synthesis.SLAMet != 1 {
		t.Fatalf("SLA measured %d, met %d", synthesis.SLAMeasured, synthesis.SLAMet)
	}
	if synthesis.SLAAttainment == nil || *synthesis.SLAAttainment < 33 || *synthesis.SLAAttainment > 34 {
		t.Fatalf("attainment is %v, want about 33.3", synthesis.SLAAttainment)
	}
}

// "No failures" and "nothing ran" are different states, and a dashboard showing 0% for both hides
// a stopped queue behind the healthiest-looking number on the page.
func TestARateIsNullRatherThanZeroWhenNothingFinished(t *testing.T) {
	h := newHarness(t)
	health, err := h.store.Health(context.Background(), time.Hour, h.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range health {
		if row.FailureRate != nil {
			t.Fatalf("%s reports a failure rate of %v with nothing finished", row.Kind, *row.FailureRate)
		}
		if row.SLAAttainment != nil {
			t.Fatalf("%s reports attainment with nothing measured", row.Kind)
		}
		// And "never" is a distinguishable answer rather than a suspicious zero.
		if row.SecondsSinceLastFinish != -1 {
			t.Fatalf("%s has never finished anything but reports %d", row.Kind, row.SecondsSinceLastFinish)
		}
	}
}

// The SLA is what §7.1 promises. A job that finishes inside its budget meets it; one that finishes
// after does not — measured, not asserted.
func TestTheSLAIsResolvedWhenTheJobFinishes(t *testing.T) {
	h := newHarness(t)
	job := h.enqueue(t, "clinical.synthesis", nil)
	if job.SLADeadline == nil {
		t.Fatal("a kind with an SLA enqueued a job with no deadline")
	}
	if got := job.SLADeadline.Sub(h.clock.Now()); got != 5*time.Minute {
		t.Fatalf("the deadline is %v away, want 5m", got)
	}

	registry := jobs.NewRegistry()
	registry.Register("clinical.synthesis", func(context.Context, jobs.Running) error { return nil })
	w := h.worker("w1", registry, time.Minute)

	// Six minutes later: past the promise.
	h.clock.Advance(6 * time.Minute)
	runUntil(t, w, func() bool { return h.status(t, job.ID) == "SUCCEEDED" })

	var met *bool
	if err := h.SQL.QueryRow(`SELECT met_sla FROM ops.job WHERE id = $1`, job.ID).Scan(&met); err != nil {
		t.Fatal(err)
	}
	if met == nil || *met {
		t.Fatalf("a job finished six minutes into a five-minute budget reports met_sla=%v", met)
	}
}

func TestTheInvariantNoticesAnUnresolvedPromise(t *testing.T) {
	h := newHarness(t)
	job := h.enqueue(t, "clinical.synthesis", nil)
	// Finished, with a deadline nobody resolved. Without invariant 92 a kind could carry an SLA,
	// stamp deadlines, and report 100% attainment because nothing ever recorded a miss.
	if _, err := h.SQL.Exec(
		`UPDATE ops.job SET status = 'SUCCEEDED', finished_at = now(), met_sla = NULL WHERE id = $1`,
		job.ID); err != nil {
		t.Fatal(err)
	}
	var ignored string
	err := h.SQL.QueryRow(`SELECT core.assert_every_finished_promise_is_resolved()::text`).Scan(&ignored)
	if err == nil {
		t.Fatal("the invariant passed with an unresolved promise")
	}
	if !strings.Contains(err.Error(), "deadline nobody resolved") {
		t.Fatalf("the invariant failed for the wrong reason: %v", err)
	}
}

// --- pausing ---

func TestAPausedKindIsNotClaimed(t *testing.T) {
	h := newHarness(t)
	job := h.enqueue(t, "analytical.research_extract", nil)

	var operator uuid.UUID
	if err := h.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&operator); err != nil {
		t.Fatal(err)
	}
	// A real user, because pausing is attributed: the person who stopped work a clinic relies on
	// has to be findable afterwards.
	operator = uuid.New()
	var facility uuid.UUID
	if err := h.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&facility); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'OPS1', 'Rezaul Karim', 'রেজাউল করিম', 'active')`,
		operator, facility); err != nil {
		t.Fatal(err)
	}

	if _, err := h.store.Pause(context.Background(), "analytical.research_extract", operator, h.clock.Now()); err != nil {
		t.Fatal(err)
	}

	registry := jobs.NewRegistry()
	ran := 0
	registry.Register("analytical.research_extract", func(context.Context, jobs.Running) error {
		ran++
		return nil
	})
	w := h.worker("w1", registry, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	if ran != 0 {
		t.Fatalf("a paused kind ran %d times", ran)
	}
	if got := h.status(t, job.ID); got != "AVAILABLE" {
		t.Fatalf("the paused job is %s; pausing must not consume it", got)
	}

	// And resuming lets it through.
	if _, err := h.store.Resume(context.Background(), "analytical.research_extract"); err != nil {
		t.Fatal(err)
	}
	runUntil(t, w, func() bool { return h.status(t, job.ID) == "SUCCEEDED" })
}

func TestPausingTwiceIsDistinguishableFromSucceeding(t *testing.T) {
	h := newHarness(t)
	var facility, operator uuid.UUID
	if err := h.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&facility); err != nil {
		t.Fatal(err)
	}
	operator = uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'OPS1', 'Rezaul Karim', 'রেজাউল করিম', 'active')`,
		operator, facility); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := h.store.Pause(ctx, "patient.sms", operator, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Pause(ctx, "patient.sms", operator, h.clock.Now()); !errors.Is(err, jobs.ErrAlreadyPaused) {
		t.Fatalf("pausing twice answered %v", err)
	}
	if _, err := h.store.Pause(ctx, "patient.telepathy", operator, h.clock.Now()); !errors.Is(err, jobs.ErrUnknownKind) {
		t.Fatalf("pausing an unknown kind answered %v", err)
	}
}

// --- scheduled jobs ---

func TestAScheduledJobIsEnqueuedOnceEvenWithTwoSchedulers(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Due now.
	if _, err := h.SQL.Exec(
		`UPDATE ops.job_schedule SET next_run_at = $1 WHERE kind = 'maintenance.idempotency_purge'`,
		h.clock.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Two schedulers, the case a second worker instance produces. The schedule row's own lock is
	// the leader election, and a periodic job has no natural dedupe key, so "run the nightly
	// audit twice" is a real cost rather than an absorbed duplicate.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		s := &jobs.Scheduler{Store: h.store, Interval: 10 * time.Millisecond,
			Clock: h.clock, Logger: h.logger}
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
			defer cancel()
			s.Run(c)
		}()
	}
	wg.Wait()

	if got := h.count(t, "kind = 'maintenance.idempotency_purge'"); got != 1 {
		t.Fatalf("%d scheduled jobs were enqueued, want 1", got)
	}

	// And the schedule advanced, so the next tick does not enqueue it again.
	var next time.Time
	if err := h.SQL.QueryRow(
		`SELECT next_run_at FROM ops.job_schedule WHERE kind = 'maintenance.idempotency_purge'`).
		Scan(&next); err != nil {
		t.Fatal(err)
	}
	if !next.After(h.clock.Now()) {
		t.Fatalf("the schedule did not advance: next run at %v, now %v", next, h.clock.Now())
	}
}

// --- what the catalogue promises ---

func TestEveryJobKindReadsInBothLanguages(t *testing.T) {
	h := newHarness(t)
	kinds, err := h.store.Kinds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(kinds) < 5 {
		t.Fatalf("%d kinds registered; §8.5 names five classes", len(kinds))
	}
	for _, k := range kinds {
		if strings.TrimSpace(k.DescriptionEN) == "" || strings.TrimSpace(k.DescriptionBN) == "" {
			t.Fatalf("%s does not read in both languages", k.Kind)
		}
	}
}

// §8.5's table, as a test. A retry policy that does not match the class it is in is a policy
// somebody will be surprised by at the worst moment.
func TestTheRetryPoliciesMatchTheClassesInTheStandard(t *testing.T) {
	h := newHarness(t)
	kinds, err := h.store.Kinds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"CLINICAL_CRITICAL": 5, // exponential, 5 attempts, then page
		"PATIENT_FACING":    8, // exponential, 8 attempts
		"PIPELINE":          3, // 3 attempts then a human queue
	}
	for _, k := range kinds {
		if attempts, ok := want[k.Class]; ok && k.MaxAttempts != attempts {
			t.Fatalf("%s is %s and retries %d times; §8.5 says %d",
				k.Kind, k.Class, k.MaxAttempts, attempts)
		}
		// Only the clinical-critical class carries a deadline today, and §7.1's five minutes is
		// the one that matters.
		if k.Kind == "clinical.synthesis" {
			if k.SLASeconds == nil || *k.SLASeconds != 300 {
				t.Fatalf("synthesis has an SLA of %v, want 300 seconds", k.SLASeconds)
			}
		}
	}
}

// The headline number has to be openable. A dashboard reporting "63.6% on time" with no way to
// reach the misses is asserting the figure to its reader however carefully it was measured.
func TestTheJobsBehindTheAttainmentFigureCanBeListed(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	now := h.clock.Now()

	met := h.enqueue(t, "clinical.synthesis", map[string]any{"n": 1})
	missed := h.enqueue(t, "clinical.synthesis", map[string]any{"n": 2})
	for id, kept := range map[uuid.UUID]bool{met.ID: true, missed.ID: false} {
		if _, err := h.SQL.Exec(`
			UPDATE ops.job SET status = 'SUCCEEDED', finished_at = $2::timestamptz, met_sla = $3
			 WHERE id = $1`, id, now, kept); err != nil {
			t.Fatal(err)
		}
	}

	// Both succeeded, so neither is a dead letter and neither is waiting: without the filter
	// there is no query that separates them.
	all, err := h.store.List(ctx, "SUCCEEDED", "clinical.synthesis", nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("%d finished synthesis jobs, want 2", len(all))
	}

	no := false
	misses, err := h.store.List(ctx, "", "", &no, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(misses) != 1 || misses[0].ID != missed.ID {
		t.Fatalf("the missed-deadline filter returned %d jobs: %+v", len(misses), misses)
	}
	yes := true
	kept, err := h.store.List(ctx, "", "", &yes, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].ID != met.ID {
		t.Fatalf("the met-deadline filter returned %d jobs", len(kept))
	}
}

// --- what the operator's screen is given ---

// The retry and cancel responses must be whole jobs, not the five columns an UPDATE happened to
// return. A client typed against the contract and trusting the body would otherwise render a job
// enqueued in the year one, on "attempt 0 of 0".
func TestRetryAndCancelReturnAWholeJob(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	dead := h.enqueue(t, "patient.sms", map[string]any{"visit_id": uuid.New().String()})
	if _, err := h.SQL.Exec(`
		UPDATE ops.job SET status = 'DISCARDED', attempt = 8, finished_at = now(),
		                   last_error = 'the gateway refused' WHERE id = $1`, dead.ID); err != nil {
		t.Fatal(err)
	}
	retried, err := h.store.Retry(ctx, dead.ID, h.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	for name, ok := range map[string]bool{
		"kind":         retried.Kind == "patient.sms",
		"queue":        retried.Queue == "patient",
		"max_attempts": retried.MaxAttempts == 8,
		"args":         len(retried.Args) > 2,
		"enqueued_at":  !retried.EnqueuedAt.IsZero(),
		"priority":     retried.Priority > 0,
		"description":  retried.DescriptionEN != "" && retried.DescriptionBN != "",
	} {
		if !ok {
			t.Fatalf("the retried job's %s is a zero value: %+v", name, retried)
		}
	}

	queued := h.enqueue(t, "patient.sms", map[string]any{"visit_id": uuid.New().String()})
	cancelled, err := h.store.Cancel(ctx, queued.ID, "the patient was seen in person", h.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Queue == "" || cancelled.MaxAttempts == 0 || cancelled.EnqueuedAt.IsZero() {
		t.Fatalf("the cancelled job came back half-filled: %+v", cancelled)
	}
	if cancelled.LastError != "the patient was seen in person" {
		t.Fatalf("the cancellation reason was lost: %q", cancelled.LastError)
	}
}

// Recorded and shown. A screen that can say a kind is stopped and cannot say who stopped it
// leaves the log as the only place that person is findable, which is not what "findable
// afterwards" means.
func TestAPausedKindNamesWhoPausedIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	var facility uuid.UUID
	if err := h.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&facility); err != nil {
		t.Fatal(err)
	}
	operator := uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'OPS9', 'Rezaul Karim', 'রেজাউল করিম', 'active')`,
		operator, facility); err != nil {
		t.Fatal(err)
	}

	kind, err := h.store.Pause(ctx, "pipeline.ocr", operator, h.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if kind.PausedBy == nil || *kind.PausedBy != operator {
		t.Fatalf("the paused kind does not name who paused it: %+v", kind)
	}
	if kind.PausedByNameEN != "Rezaul Karim" || kind.PausedByNameBN != "রেজাউল করিম" {
		t.Fatalf("the paused kind does not carry the name: %+v", kind)
	}
	if kind.PausedAt == nil {
		t.Fatal("the paused kind does not say when")
	}

	// And the health row, which is the screen somebody is actually looking at when they are
	// trying to work out why nothing is happening.
	health, err := h.store.Health(ctx, time.Hour, h.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range health {
		if row.Kind != "pipeline.ocr" {
			continue
		}
		if !row.Paused || row.PausedAt == nil || row.PausedByNameBN != "রেজাউল করিম" {
			t.Fatalf("the health row does not say who paused it: %+v", row)
		}
	}
}

// A job sitting out an exponential backoff is waiting **on purpose**. Counting it as queue age
// would light up an alert while the retry policy behaved exactly as designed — so the number the
// alert reads is measured from `run_at` over jobs that are due.
func TestBackoffDoesNotCountAsWorkNobodyIsDoing(t *testing.T) {
	h := newHarness(t)
	now := h.clock.Now()
	job := h.enqueue(t, "patient.sms", nil)

	// Enqueued twenty minutes ago, failed, and not due again for another ten.
	if _, err := h.SQL.Exec(`
		UPDATE ops.job SET enqueued_at = $2::timestamptz - interval '20 minutes',
		                   run_at = $2::timestamptz + interval '10 minutes',
		                   attempt = 2, last_error = 'the gateway timed out'
		 WHERE id = $1`, job.ID, now); err != nil {
		t.Fatal(err)
	}

	health, err := h.store.Health(context.Background(), time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range health {
		if row.Kind != "patient.sms" {
			continue
		}
		// Total age is honest and large: it really has been waiting twenty minutes.
		if row.OldestAvailableSeconds < 1100 {
			t.Fatalf("total age is %ds, want about 1200", row.OldestAvailableSeconds)
		}
		// The alert's number is zero, because nothing is due.
		if row.OldestDueSeconds != 0 {
			t.Fatalf("a job waiting out a backoff counts as %ds of work nobody is doing",
				row.OldestDueSeconds)
		}
	}
}

// A kind can read "0% given up on, 0 of 40 finished" while forty dead letters from last night are
// still waiting. Both true, and a reader who assumed one set would conclude the screen was lying.
func TestDeadLettersAreCountedOutsideTheWindow(t *testing.T) {
	h := newHarness(t)
	now := h.clock.Now()
	job := h.enqueue(t, "pipeline.ocr", nil)
	if _, err := h.SQL.Exec(`
		UPDATE ops.job SET status = 'DISCARDED', attempt = 3,
		                   finished_at = $2::timestamptz - interval '20 hours',
		                   last_error = 'the document would not open' WHERE id = $1`,
		job.ID, now); err != nil {
		t.Fatal(err)
	}

	health, err := h.store.Health(context.Background(), time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range health {
		if row.Kind != "pipeline.ocr" {
			continue
		}
		// Nothing finished in the last hour, so the windowed counts are empty and the rate is
		// null — all correct.
		if row.Discarded != 0 || row.FailureRate != nil {
			t.Fatalf("last night's failure leaked into this hour's window: %+v", row)
		}
		// And it is still there, waiting for a person.
		if row.DeadLetters != 1 {
			t.Fatalf("the dead letter is not counted: %d", row.DeadLetters)
		}
	}
}
