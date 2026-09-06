package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The worker runtime.
//
// # Registration fails at startup, not at three in the morning
//
// The same treatment the route registry gets, for the same reason. A handler registered for a kind
// the database does not know is a typo that would otherwise show up as a job that is never claimed;
// a kind in the database with no handler on any worker is work that is enqueued, waits, and is
// never done — and the queue-health page would show it as a growing depth with nobody able to say
// why. [Registry.Check] refuses both, and `cmd/worker` calls it before it starts polling.
//
// The asymmetry is deliberate: a worker may legitimately handle a *subset* of the kinds, because
// queues exist so that document processing cannot slow down a blood pressure. So the check is
// against the queues this worker actually serves, not against the whole catalogue.

// Handler does one job. The context carries the deadline; the args are whatever was enqueued.
//
// **A handler must be idempotent.** Delivery is at-least-once by construction (see the note on
// lease expiry in the queries file), so a handler that runs twice must produce one effect. This is
// not a new burden — §8.5 already required it — and the codebase already has the mechanism: event
// ids derived deterministically from their inputs, so the ledger absorbs a repeat.
type Handler func(ctx context.Context, job Running) error

// Running is a job in the hands of a worker.
type Running struct {
	ID          uuid.UUID
	Kind        string
	Args        json.RawMessage
	Attempt     int
	MaxAttempts int
	EnqueuedAt  time.Time
	// SLADeadline is when this job has to be finished by, or nil for a kind with no promise. On
	// the handler rather than only in the database, so work that can degrade gracefully — a
	// synthesis that could return a shorter summary — can see how much time is left.
	SLADeadline *time.Time
}

// Deadline is how long is left before this job misses its SLA, and whether it has one.
func (r Running) Deadline(now time.Time) (time.Duration, bool) {
	if r.SLADeadline == nil {
		return 0, false
	}
	return r.SLADeadline.Sub(now), true
}

// Registry maps a kind to the handler that does it.
type Registry struct {
	handlers map[string]Handler
}

// NewRegistry builds an empty one.
func NewRegistry() *Registry { return &Registry{handlers: map[string]Handler{}} }

// Register attaches a handler to a kind. Registering twice is a programming error and panics: it
// means two packages both believe they own the same work, and the loser would be decided by
// initialisation order.
func (r *Registry) Register(kind string, h Handler) {
	if _, exists := r.handlers[kind]; exists {
		panic("jobs: two handlers registered for " + kind)
	}
	r.handlers[kind] = h
}

// Check refuses a mismatch between what this worker can do and what the database expects, before
// the worker starts polling.
//
// Both directions, and both matter:
//
//   - a handler for a kind that is not in the catalogue is a typo, and the job it was written for
//     would never be claimed;
//   - a kind in the catalogue whose queue this worker serves, with no handler, is work that is
//     enqueued, waits, and is never done — the queue-health page would show a rising depth and
//     nobody able to say why.
func (r *Registry) Check(kinds []Kind, queues []string) error {
	serving := map[string]bool{}
	for _, q := range queues {
		serving[q] = true
	}
	known := map[string]bool{}
	var missing []string
	for _, k := range kinds {
		known[k.Kind] = true
		if serving[k.Queue] && r.handlers[k.Kind] == nil {
			missing = append(missing, k.Kind)
		}
	}
	var unknown []string
	for kind := range r.handlers {
		if !known[kind] {
			unknown = append(unknown, kind)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("jobs: handler registered for a kind the database does not know: %v", unknown)
	}
	if len(missing) > 0 {
		return fmt.Errorf("jobs: no handler for %v, which this worker's queues would claim", missing)
	}
	return nil
}

// WorkerConfig is how one worker behaves.
type WorkerConfig struct {
	// Name identifies this worker in a lease and in an attempt record. A pod name, a hostname.
	Name string
	// Queues is what it will claim from. Serving a subset is the normal case: queues exist so
	// that a burst of document processing cannot slow down a blood pressure.
	Queues []string
	// Concurrency is how many jobs it runs at once.
	Concurrency int
	// PollInterval is how often it looks for work when it found none last time.
	PollInterval time.Duration
	// LeaseDuration is how long a claim is held before the reaper may take it back. It must
	// comfortably exceed the longest job, because the heartbeat extends it but a worker that is
	// wedged rather than dead will not send one.
	LeaseDuration time.Duration
	// HeartbeatInterval is how often a running job's lease is extended. Without it a job
	// legitimately taking longer than one lease would be reaped out from under a healthy worker
	// and run twice — the difference between at-least-once because a worker died and
	// at-least-once because we were impatient.
	HeartbeatInterval time.Duration

	Store    *Store
	Registry *Registry
	Kinds    map[string]Kind
	Clock    interface{ Now() time.Time }
	Logger   *slog.Logger

	// Jitter is the randomness in the retry backoff, injectable so a test is deterministic.
	Jitter func() float64
	// Metrics records what each job did. Nil is fine: a worker without telemetry runs unchanged.
	Metrics *Instruments
}

// Worker claims and runs jobs.
type Worker struct {
	cfg WorkerConfig
	// running counts what is in flight, so a poll asks for only what it can take.
	mu      sync.Mutex
	running int
}

// NewWorker builds one with sensible defaults for anything left unset.
func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = time.Minute
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = cfg.LeaseDuration / 3
	}
	if cfg.Jitter == nil {
		cfg.Jitter = defaultJitter
	}
	return &Worker{cfg: cfg}
}

// Run polls until the context is cancelled, then waits for what is in flight.
//
// A shutdown does not abandon running jobs: the lease would expire and they would be run again,
// which is correct but wasteful, and for a job with an SLA it is the difference between meeting it
// and missing it by a whole lease.
func (w *Worker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case <-ticker.C:
		}

		claimed, err := w.claim(ctx)
		if err != nil {
			// A failed poll is an operational annoyance; stopping the worker over one would turn
			// it into an outage. Logged and retried on the next tick.
			w.cfg.Logger.ErrorContext(ctx, "could not claim jobs",
				"worker", w.cfg.Name, "error", err.Error())
			continue
		}
		for _, job := range claimed {
			wg.Add(1)
			go func(job Running) {
				defer wg.Done()
				w.execute(ctx, job)
			}(job)
		}
	}
}

// claim takes as much work as this worker has room for.
func (w *Worker) claim(ctx context.Context) ([]Running, error) {
	w.mu.Lock()
	room := w.cfg.Concurrency - w.running
	w.mu.Unlock()
	if room <= 0 {
		return nil, nil
	}

	rows, err := w.cfg.Store.q.ClaimJobs(ctx, dbgen.ClaimJobsParams{
		Worker: w.cfg.Name, Queues: w.cfg.Queues, RowLimit: int32(room),
		LeaseSeconds: int32(w.cfg.LeaseDuration.Seconds()),
		Now:          w.cfg.Clock.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Running, 0, len(rows))
	for _, row := range rows {
		out = append(out, Running{
			ID: row.ID, Kind: row.Kind, Args: row.Args,
			Attempt: int(row.Attempt), MaxAttempts: int(row.MaxAttempts),
			EnqueuedAt: row.EnqueuedAt, SLADeadline: row.SlaDeadline,
		})
	}
	w.mu.Lock()
	w.running += len(out)
	w.mu.Unlock()
	return out, nil
}

// execute runs one job and records what happened.
func (w *Worker) execute(parent context.Context, job Running) {
	defer func() {
		w.mu.Lock()
		w.running--
		w.mu.Unlock()
	}()

	handler := w.cfg.Registry.handlers[job.Kind]
	if handler == nil {
		// Unreachable if Check ran, which is the point of Check. Reachable if a kind was added
		// to the database while this worker was up, which is worth failing loudly rather than
		// silently holding the lease until it expires.
		w.fail(parent, job, time.Duration(0),
			errors.New("this worker has no handler for that kind"))
		return
	}

	// Detached from the poll loop's context so that a shutdown does not cancel work already
	// started — an abandoned job is run again after its lease expires, which for a job with an
	// SLA is the difference between meeting it and missing it by a whole lease.
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	defer cancel()

	stop := w.heartbeat(ctx, job)
	defer stop()

	started := w.cfg.Clock.Now()
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				// A panic in one handler must not take the worker down with it. The stack goes
				// to the log; the job's own error is the short version, because it is rendered
				// on a screen an operator reads.
				w.cfg.Logger.ErrorContext(ctx, "a job handler panicked",
					"kind", job.Kind, "job_id", job.ID.String(),
					"stack", string(debug.Stack()))
				err = fmt.Errorf("the handler panicked: %v", r)
			}
		}()
		return handler(ctx, job)
	}()
	took := w.cfg.Clock.Now().Sub(started)

	if err != nil {
		w.fail(ctx, job, took, err)
		return
	}
	// Whether the promise was kept, decided the same way the database decides it, so the metric
	// and the row cannot disagree.
	var met *bool
	if job.SLADeadline != nil {
		inTime := !w.cfg.Clock.Now().After(*job.SLADeadline)
		met = &inTime
	}
	w.cfg.Metrics.Finished(ctx, job.Kind, "succeeded", took, met)

	if err := w.cfg.Store.q.CompleteJob(ctx, dbgen.CompleteJobParams{
		ID: job.ID, Worker: w.cfg.Name, Now: w.cfg.Clock.Now().UTC(),
	}); err != nil {
		// The work is done and the row says otherwise. The lease will expire and the job will
		// run again — which is exactly the at-least-once case every handler is required to
		// survive, and the reason that requirement is not negotiable.
		w.cfg.Logger.ErrorContext(ctx, "a job finished but could not be marked complete",
			"kind", job.Kind, "job_id", job.ID.String(), "error", err.Error())
	}
}

// heartbeat extends the lease while the job runs, and returns a function that stops it.
func (w *Worker) heartbeat(ctx context.Context, job Running) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(w.cfg.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := w.cfg.Store.q.HeartbeatJob(ctx, dbgen.HeartbeatJobParams{
					ID: job.ID, Worker: w.cfg.Name,
					LeaseSeconds: int32(w.cfg.LeaseDuration.Seconds()),
					Now:          w.cfg.Clock.Now().UTC(),
				}); err != nil {
					w.cfg.Logger.WarnContext(ctx, "could not extend a job lease",
						"kind", job.Kind, "job_id", job.ID.String(), "error", err.Error())
				}
			}
		}
	}()
	return func() { close(done) }
}

// fail records the attempt and decides, in one statement, whether the job goes back to the queue
// or dead-letters.
func (w *Worker) fail(ctx context.Context, job Running, took time.Duration, cause error) {
	now := w.cfg.Clock.Now().UTC()
	kind := w.cfg.Kinds[job.Kind]

	// The attempt record first: if the second statement fails, an operator still has the error.
	// The other order would lose the one piece of information they need.
	if err := w.cfg.Store.q.RecordAttempt(ctx, dbgen.RecordAttemptParams{
		JobID: job.ID, Attempt: int32(job.Attempt), Worker: w.cfg.Name,
		FailedAt: now, RanForMs: took.Milliseconds(), Error: truncate(cause.Error(), 2000),
	}); err != nil {
		w.cfg.Logger.ErrorContext(ctx, "could not record a job attempt",
			"kind", job.Kind, "job_id", job.ID.String(), "error", err.Error())
	}

	backoff := backoffFor(kind, job.Attempt, w.cfg.Jitter)
	row, err := w.cfg.Store.q.FailJob(ctx, dbgen.FailJobParams{
		ID: job.ID, Worker: w.cfg.Name, Now: now,
		BackoffSeconds: int32(backoff.Seconds()),
		Error:          truncate(cause.Error(), 2000),
	})
	if err != nil {
		w.cfg.Logger.ErrorContext(ctx, "could not record a job failure",
			"kind", job.Kind, "job_id", job.ID.String(), "error", err.Error())
		return
	}
	outcome := "failed"
	if row.Status == "DISCARDED" {
		outcome = "discarded"
	}
	var met *bool
	if row.Status == "DISCARDED" && job.SLADeadline != nil {
		missed := false
		met = &missed
	}
	w.cfg.Metrics.Finished(ctx, job.Kind, outcome, took, met)

	if row.Status == "DISCARDED" {
		// The one log line an alert is built on. Deliberately at error level even though the
		// system is behaving correctly: a dead-lettered job is work that will not happen unless
		// a person does something.
		w.cfg.Logger.ErrorContext(ctx, "a job was dead-lettered",
			"kind", job.Kind, "job_id", job.ID.String(),
			"attempts", job.Attempt, "error", cause.Error())
	}
}

// truncate keeps an error short enough to render.
//
// A stack trace pasted into a table column is a column nobody can read and a screen that scrolls
// sideways. The full text is in the log, joined to this by the job id.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "… (truncated; the full error is in the log, under this job's id)"
}

// Reaper returns abandoned work to the queue.
//
// A separate loop from the worker's, and it must keep running even when this worker is claiming
// nothing: the jobs it rescues were abandoned by a *different* process, and the case it exists for
// is precisely the one where that process is not coming back.
type Reaper struct {
	Store    *Store
	Interval time.Duration
	Clock    interface{ Now() time.Time }
	Logger   *slog.Logger
}

// Run sweeps until the context is cancelled.
func (r *Reaper) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Once at start, so a worker restarted after a long outage rescues everything that fell due
	// while nothing was running, in its first pass rather than one interval later.
	r.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sweep(ctx)
		}
	}
}

func (r *Reaper) sweep(ctx context.Context) {
	rows, err := r.Store.q.ReapExpiredLeases(ctx, r.Clock.Now().UTC())
	if err != nil {
		r.Logger.ErrorContext(ctx, "could not reap expired job leases", "error", err.Error())
		return
	}
	for _, row := range rows {
		if row.Discarded {
			r.Logger.ErrorContext(ctx, "a job was dead-lettered after its worker stopped answering",
				"kind", row.Kind, "job_id", row.ID.String(), "attempts", row.Attempt)
			continue
		}
		r.Logger.WarnContext(ctx, "a job was returned to the queue after its worker stopped answering",
			"kind", row.Kind, "job_id", row.ID.String(), "attempts", row.Attempt)
	}
}

// Scheduler enqueues periodic jobs.
//
// The schedule row is also the leader election: claiming it uses the same `FOR UPDATE SKIP LOCKED`
// an ordinary job claim uses, so two workers running at once produce one enqueue rather than two.
// That matters more here than for ordinary jobs, because a periodic job has no natural dedupe key
// and "run the nightly audit twice" is a real cost rather than an absorbed duplicate.
type Scheduler struct {
	Store    *Store
	Interval time.Duration
	Clock    interface{ Now() time.Time }
	Logger   *slog.Logger
}

// Run enqueues due work until the context is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick claims what is due and enqueues it, **in one transaction**.
//
// Claiming advances `next_run_at` and enqueueing writes the job, and both have to commit together.
// A crash between them in the other order would either skip a run (advanced, not enqueued) or
// enqueue the same periodic job every tick forever (enqueued, not advanced) — and the second is
// how a nightly audit becomes a thousand nightly audits before anybody notices.
func (s *Scheduler) tick(ctx context.Context) {
	now := s.Clock.Now().UTC()
	err := s.Store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		due, err := s.Store.q.WithTx(tx).DueSchedules(ctx, now)
		if err != nil {
			return err
		}
		for _, row := range due {
			if _, err := s.Store.EnqueueTx(ctx, tx, now, Enqueueing{
				Kind: row.Kind, Args: json.RawMessage(row.Args),
				// The dedupe key is the scheduled instant, so two schedulers that somehow both
				// got past the row lock still produce one job rather than two.
				DedupeKey: "schedule:" + row.NextRunAt.Format(time.RFC3339),
			}); err != nil && !errors.Is(err, ErrAlreadyQueued) {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// A failed tick is an operational annoyance: the schedule did not advance, so the next
		// tick tries the same work again. Stopping the scheduler over one would turn a missed
		// minute into a missed night.
		s.Logger.ErrorContext(ctx, "could not enqueue scheduled jobs", "error", err.Error())
	}
}
