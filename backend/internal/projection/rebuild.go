package projection

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Rebuild: throw a read model away and derive it again from event one.
//
// This is the operation that makes "the event log is the source of truth" true rather than
// aspirational, and it is deliberately one command:
//
//	projector rebuild                  every projection
//	projector rebuild visit_vital      one
//
// It is also the mechanism behind projection versioning: a derivation that changed is a new
// version, the runner refuses to advance a model built by the old one, and the repair is a
// rebuild.

// RebuildResult is what a rebuild did, for the log and for the audit entry the command
// writes.
type RebuildResult struct {
	Projection string
	Version    int
	Events     int64
	Applied    int64
	Checkpoint int64
	Duration   time.Duration
}

// RebuildOptions tunes a rebuild. The defaults are what the command uses.
type RebuildOptions struct {
	// BatchSize is how many events are read at a time. Rebuild is one long transaction
	// per batch, not one per event: a hundred thousand transactions is slow for no
	// benefit, since a rebuild that fails is restarted from the beginning anyway.
	BatchSize int
	Logger    *slog.Logger
	// Progress, when set, is called after each batch. The command prints it; the tests
	// ignore it.
	Progress func(RebuildResult)
}

// Rebuild rebuilds one projection. It runs as the projector: emptying a read model is a
// privilege the application does not have and must not be given.
//
// The sequence is: mark the projection `rebuilding` and its checkpoint zero, so that a
// runner in another process stands off and a reader can see the rows are not to be trusted;
// empty it; replay every event through the same Apply the incremental path uses; mark it
// healthy at the ledger's head. The dead letters of the previous derivation are cleared,
// because they belonged to a model that no longer exists.
//
// Everything after the first mark is in the rebuilding state, so a rebuild that dies
// halfway leaves the projection visibly `rebuilding` rather than silently half-derived.
func (e *Engine) Rebuild(ctx context.Context, name string, opts RebuildOptions) (RebuildResult, error) {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	p, ok := e.registry.Lookup(name)
	if !ok {
		return RebuildResult{}, fmt.Errorf("no projection named %q is registered", name)
	}
	if e.events == nil {
		return RebuildResult{}, fmt.Errorf("this engine has no event store; construct it with NewEngineWithEvents")
	}

	started := time.Now()
	if _, err := e.q.RegisterProjection(ctx, dbgen.RegisterProjectionParams{
		Name: p.Name(), Version: int32(p.Version()), Mode: string(p.Mode()), //nolint:gosec // ≥ 1, small
	}); err != nil {
		return RebuildResult{}, err
	}
	if err := e.q.BeginRebuild(ctx, dbgen.BeginRebuildParams{
		Name: p.Name(), Version: int32(p.Version()), //nolint:gosec // ≥ 1, small
	}); err != nil {
		return RebuildResult{}, err
	}
	if err := e.q.ClearDeadLetters(ctx, p.Name()); err != nil {
		return RebuildResult{}, err
	}

	// Emptying is its own transaction: a rebuild of a large model should not hold the
	// delete's locks for the whole replay.
	if err := e.inTx(ctx, func(tx pgxTx) error { return p.Reset(ctx, tx) }); err != nil {
		return RebuildResult{}, fmt.Errorf("emptying %s: %w", p.Name(), err)
	}

	result := RebuildResult{Projection: p.Name(), Version: p.Version()}
	var lastRecorded *time.Time
	from := int64(1)
	for {
		events, err := e.events.FromGlobal(ctx, from, opts.BatchSize)
		if err != nil {
			return result, err
		}
		if len(events) == 0 {
			break
		}
		batch := events
		if err := e.inTx(ctx, func(tx pgxTx) error {
			for _, ev := range batch {
				if !p.Handles(ev.EventType) {
					continue
				}
				if err := p.Apply(ctx, tx, ev); err != nil {
					return fmt.Errorf("global_seq %d (%s): %w", ev.GlobalSeq, ev.EventType, err)
				}
				result.Applied++
			}
			return nil
		}); err != nil {
			return result, err
		}
		last := events[len(events)-1]
		recorded := last.RecordedAt
		lastRecorded = &recorded
		result.Events += int64(len(events))
		result.Checkpoint = last.GlobalSeq
		from = last.GlobalSeq + 1
		if opts.Progress != nil {
			opts.Progress(result)
		}
	}

	if err := e.q.FinishRebuild(ctx, dbgen.FinishRebuildParams{
		Name: p.Name(), Status: string(Healthy), Checkpoint: result.Checkpoint, AppliedAt: lastRecorded,
	}); err != nil {
		return result, err
	}
	result.Duration = time.Since(started)
	opts.Logger.InfoContext(ctx, "projection rebuilt",
		"projection", p.Name(), "version", p.Version(), "events", result.Events,
		"applied", result.Applied, "checkpoint", result.Checkpoint, "took", result.Duration.String())
	return result, nil
}

// RebuildAll rebuilds every registered projection, in name order.
func (e *Engine) RebuildAll(ctx context.Context, opts RebuildOptions) ([]RebuildResult, error) {
	var out []RebuildResult
	for _, name := range e.registry.Names() {
		result, err := e.Rebuild(ctx, name, opts)
		if err != nil {
			return out, err
		}
		out = append(out, result)
	}
	return out, nil
}

// NewEngineWithEvents is the engine a rebuilder needs: the register plus the ledger to
// replay from.
func NewEngineWithEvents(pool pgxPool, registry *Registry, events *eventstore.Store) *Engine {
	e := NewEngine(pool, registry)
	e.events = events
	return e
}
