package projection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// State is a projection's row in the register.
type State struct {
	Name       string
	Version    int
	Mode       Mode
	Checkpoint int64
	Status     Status
	AppliedAt  *time.Time
	RebuiltAt  *time.Time
	// OpenDeadLetters is how many events this projection could not apply and has not had
	// resolved. Non-zero means the model is knowingly incomplete.
	OpenDeadLetters int64
}

// Stale reports that the stored derivation version is not the code's, which means the rows
// were computed by a different derivation and must be rebuilt before they are trusted
// (§7.10).
func (s State) Stale(p Projection) bool { return s.Version != p.Version() }

// Engine is the register and the reads over it: what exists, how far each has got, and
// what failed. The runner and the rebuilder are built on it.
type Engine struct {
	pool     *pgxpool.Pool
	q        *dbgen.Queries
	registry *Registry
	// events is the ledger a rebuild replays from. Nil in a process that only reads the
	// register — the API, which reports lag but never rebuilds.
	events *eventstore.Store
}

// pgxPool and pgxTx name the pgx types this package takes, so the rebuild's signature reads
// without dragging the import into every file.
type (
	pgxPool = *pgxpool.Pool
	pgxTx   = pgx.Tx
)

// inTx runs fn in one transaction.
func (e *Engine) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func NewEngine(pool *pgxpool.Pool, registry *Registry) *Engine {
	if registry == nil {
		registry = Default
	}
	return &Engine{pool: pool, q: dbgen.New(pool), registry: registry}
}

func (e *Engine) Registry() *Registry { return e.registry }

// Register makes sure every projection has a row, so that a checkpoint exists to advance
// and a lag metric has something to read. Called at start by every process that projects.
func (e *Engine) Register(ctx context.Context) error {
	for _, name := range e.registry.Names() {
		p, _ := e.registry.Lookup(name)
		if _, err := e.q.RegisterProjection(ctx, dbgen.RegisterProjectionParams{
			Name: p.Name(), Version: int32(p.Version()), Mode: string(p.Mode()), //nolint:gosec // ≥ 1, small
		}); err != nil {
			return fmt.Errorf("registering projection %s: %w", p.Name(), err)
		}
	}
	return nil
}

// State reads one projection's row.
func (e *Engine) State(ctx context.Context, name string) (State, error) {
	row, err := e.q.ProjectionState(ctx, name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return State{}, fmt.Errorf("projection %s is not registered", name)
		}
		return State{}, err
	}
	open, err := e.q.CountOpenDeadLetters(ctx, name)
	if err != nil {
		return State{}, err
	}
	return stateOf(row, open), nil
}

// States reads every projection's row, in name order.
func (e *Engine) States(ctx context.Context) ([]State, error) {
	rows, err := e.q.AllProjectionState(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]State, 0, len(rows))
	for _, row := range rows {
		open, err := e.q.CountOpenDeadLetters(ctx, row.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, stateOf(row, open))
	}
	return out, nil
}

// Head is the ledger's highest global sequence — the other half of the lag metric.
func (e *Engine) Head(ctx context.Context) (int64, error) { return e.q.LedgerHead(ctx) }

func stateOf(row dbgen.ReadProjectionState, open int64) State {
	return State{
		Name: row.Name, Version: int(row.Version), Mode: Mode(row.Mode),
		Checkpoint: row.Checkpoint, Status: Status(row.Status),
		AppliedAt: row.AppliedAt, RebuiltAt: row.RebuiltAt, OpenDeadLetters: open,
	}
}

// --- the synchronous side ---

// SyncSet is the set of synchronous projections, as eventstore.InTransaction.
//
// It is what the API's event store is given: `eventstore.New(Config{Synchronous: …})`.
// Nothing else in the process needs to know these projections exist.
type SyncSet struct {
	projections []Projection
}

var _ eventstore.InTransaction = (*SyncSet)(nil)

// NewSyncSet collects the synchronous projections from a registry.
func NewSyncSet(registry *Registry) *SyncSet {
	if registry == nil {
		registry = Default
	}
	return &SyncSet{projections: registry.InMode(Synchronous)}
}

// ApplyInTx runs every interested synchronous projection inside the append transaction.
//
// An error fails the append. That is the trade a synchronous projection makes and the
// reason there are so few: a read model whose staleness would be clinical is worth a write
// that fails loudly, and everything else is asynchronous.
func (s *SyncSet) ApplyInTx(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	for _, p := range s.projections {
		if !p.Handles(e.EventType) {
			continue
		}
		if err := p.Apply(ctx, tx, e); err != nil {
			return fmt.Errorf("%s: %w", p.Name(), err)
		}
	}
	return nil
}
