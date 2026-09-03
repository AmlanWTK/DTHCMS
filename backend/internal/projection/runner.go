package projection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The asynchronous runner.
//
// One goroutine per projection, each reading the ledger from its own checkpoint in global
// order. There is no job queue: the events are already a durable, ordered, gapless log, and
// a queue on top of one would be a second thing to keep in step with it. A projection that
// falls behind catches up by reading further; a projection that crashes resumes from its
// checkpoint and re-applies at most one batch, which is why every projection must be
// idempotent.
//
// Criterion 4 — a failing projection does not block event appends — is structural here:
// the runner is a separate process from the API, and nothing in the append path waits on
// it. The tests prove it anyway, because "structural" is a claim.

// RunnerConfig configures Run.
type RunnerConfig struct {
	// BatchSize is how many events one pass reads. Large enough that catching up is fast,
	// small enough that one bad batch is a small retry.
	BatchSize int
	// Interval is how long a runner waits after finding nothing. It is a poll rather than
	// a notification because a poll cannot miss a wake-up: a LISTEN that drops while the
	// runner reconnects would leave a projection quietly stalled.
	Interval time.Duration
	// MaxAttempts is how many times one event is retried before it is dead-lettered and
	// skipped. A transient failure — a deadlock, a connection reset — is worth retrying; a
	// payload the projection cannot understand will never be.
	MaxAttempts int
	// RetryPause is how long to wait between attempts at one event.
	RetryPause time.Duration
	Logger     *slog.Logger
}

func (c *RunnerConfig) defaults() {
	if c.BatchSize <= 0 {
		c.BatchSize = 500
	}
	if c.Interval <= 0 {
		c.Interval = time.Second
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.RetryPause <= 0 {
		c.RetryPause = 100 * time.Millisecond
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Runner drives the asynchronous projections.
type Runner struct {
	engine *Engine
	events *eventstore.Store
	cfg    RunnerConfig
}

func NewRunner(engine *Engine, events *eventstore.Store, cfg RunnerConfig) *Runner {
	cfg.defaults()
	return &Runner{engine: engine, events: events, cfg: cfg}
}

// Run projects until the context is cancelled. Each projection runs in its own goroutine:
// one that is slow, or degraded, or rebuilding, does not hold up another.
func (r *Runner) Run(ctx context.Context) error {
	if err := r.engine.Register(ctx); err != nil {
		return err
	}
	projections := r.engine.registry.InMode(Asynchronous)
	if len(projections) == 0 {
		<-ctx.Done()
		return nil
	}

	done := make(chan struct{}, len(projections))
	for _, p := range projections {
		go func(p Projection) {
			defer func() { done <- struct{}{} }()
			r.follow(ctx, p)
		}(p)
	}
	for range projections {
		<-done
	}
	return ctx.Err()
}

// follow is one projection's loop.
func (r *Runner) follow(ctx context.Context, p Projection) {
	log := r.cfg.Logger.With("projection", p.Name())
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		applied, err := r.Advance(ctx, p)
		switch {
		case err != nil && ctx.Err() == nil:
			log.ErrorContext(ctx, "projection pass failed", "error", err.Error())
		case applied > 0:
			log.DebugContext(ctx, "projected", "events", applied)
		}

		// A full batch means there is more waiting: go again without pausing.
		wait := r.cfg.Interval
		if applied >= r.cfg.BatchSize {
			wait = 0
		}
		timer.Reset(wait)
	}
}

// Advance projects one batch and returns how many events it applied. Exported because the
// tests — and the rebuild — want a pass they can drive rather than a loop they must race.
func (r *Runner) Advance(ctx context.Context, p Projection) (int, error) {
	state, err := r.engine.State(ctx, p.Name())
	if err != nil {
		return 0, err
	}
	if state.Status == Rebuilding {
		// Somebody else owns these rows right now.
		return 0, nil
	}
	if state.Stale(p) {
		// The derivation changed under a model built by the old one. Refusing to advance
		// is the safe answer: the rows are wrong in a way no further event will correct,
		// and a rebuild is one command away (§7.10).
		return 0, fmt.Errorf("%w: stored version %d, code version %d — run `projector rebuild %s`",
			ErrStaleVersion, state.Version, p.Version(), p.Name())
	}

	events, err := r.events.FromGlobal(ctx, state.Checkpoint+1, r.cfg.BatchSize)
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}
	return r.apply(ctx, p, events)
}

// ErrStaleVersion is returned when a projection's stored derivation version is not the
// code's. It is a refusal to continue, not a failure to work.
var ErrStaleVersion = errors.New("projection: the stored version is not the code's")

// apply writes one batch. The batch and its checkpoint are one transaction, so a crash
// re-applies the batch rather than skipping it — which is safe because every projection is
// idempotent, and is why that requirement is not negotiable.
func (r *Runner) apply(ctx context.Context, p Projection, events []eventstore.Event) (int, error) {
	applied := 0
	for _, e := range events {
		if err := r.applyOne(ctx, p, e); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

func (r *Runner) applyOne(ctx context.Context, p Projection, e eventstore.Event) error {
	var lastErr error
	for attempt := 1; attempt <= r.cfg.MaxAttempts; attempt++ {
		lastErr = r.attempt(ctx, p, e)
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt < r.cfg.MaxAttempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(r.cfg.RetryPause):
			}
		}
	}
	// Out of attempts. Record it, mark the projection degraded, and move past it: a poison
	// event that stopped the loop would stop every later event from being projected too,
	// and the model would go from "missing one row" to "frozen in the past" — which is
	// worse and much harder to notice.
	return r.deadLetter(ctx, p, e, lastErr)
}

// attempt applies one event and advances the checkpoint, in one transaction.
func (r *Runner) attempt(ctx context.Context, p Projection, e eventstore.Event) error {
	tx, err := r.engine.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if p.Handles(e.EventType) {
		if err := p.Apply(ctx, tx, e); err != nil {
			return err
		}
	}
	recorded := e.RecordedAt
	if err := r.engine.q.WithTx(tx).AdvanceCheckpoint(ctx, dbgen.AdvanceCheckpointParams{
		Name: p.Name(), Checkpoint: e.GlobalSeq, AppliedAt: &recorded,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// deadLetter records the failure, advances past the event and marks the projection
// degraded — all in one transaction, so a model is never recorded as having skipped an
// event it in fact retries forever.
func (r *Runner) deadLetter(ctx context.Context, p Projection, e eventstore.Event, cause error) error {
	tx, err := r.engine.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.engine.q.WithTx(tx)

	if err := q.RecordDeadLetter(ctx, dbgen.RecordDeadLetterParams{
		Projection: p.Name(), GlobalSeq: e.GlobalSeq, EventID: e.EventID, EventType: e.EventType,
		Error: cause.Error(), Attempts: int32(r.cfg.MaxAttempts), //nolint:gosec // small
	}); err != nil {
		return err
	}
	recorded := e.RecordedAt
	if err := q.AdvanceCheckpoint(ctx, dbgen.AdvanceCheckpointParams{
		Name: p.Name(), Checkpoint: e.GlobalSeq, AppliedAt: &recorded,
	}); err != nil {
		return err
	}
	if err := q.SetProjectionStatus(ctx, dbgen.SetProjectionStatusParams{
		Name: p.Name(), Status: string(Degraded),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	r.cfg.Logger.ErrorContext(ctx, "event dead-lettered; the read model is now incomplete",
		"projection", p.Name(), "global_seq", e.GlobalSeq, "event_type", e.EventType,
		"error", cause.Error())
	return nil
}

// DeadLetters lists a projection's unresolved failures.
func (r *Runner) DeadLetters(ctx context.Context, name string) ([]dbgen.ReadProjectionDeadLetter, error) {
	return r.engine.q.OpenDeadLetters(ctx, name)
}

// Resolve marks one dead letter handled and, when it was the last, returns the projection
// to healthy. Resolving does not re-apply the event: a projection that skipped an event is
// missing whatever that event implied, and the honest repair is a rebuild.
func (r *Runner) Resolve(ctx context.Context, name string, id int64, resolution string) error {
	tx, err := r.engine.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.engine.q.WithTx(tx)

	if err := q.ResolveDeadLetter(ctx, dbgen.ResolveDeadLetterParams{ID: id, Resolution: &resolution}); err != nil {
		return err
	}
	open, err := q.CountOpenDeadLetters(ctx, name)
	if err != nil {
		return err
	}
	if open == 0 {
		if err := q.SetProjectionStatus(ctx, dbgen.SetProjectionStatusParams{
			Name: name, Status: string(Healthy),
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
