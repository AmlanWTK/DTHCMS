package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/jobs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/synthesis"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// The pre-consultation synthesis, wired (CP71).
//
// Two adapters, and both exist because of the same rule: `architecture.json` gives `synthesis` no
// access to `jobs`, and gives `visit` no access to `synthesis`. Neither module may know about the
// other, and the composition root is the only place allowed to know about both — the same shape as
// `boardBridge` (visit → realtime) and `assessmentDeriver` (assessment → clinical).

// synthesisQueue puts a synthesis run on the job queue.
//
// It is three lines and it earns them. `jobs.Store.EnqueueTx` deliberately takes the caller's
// transaction and there is no helper that opens its own — so this adapter cannot quietly acquire
// one either, and a station touch that rolls back takes its queued summary with it.
type synthesisQueue struct{ store *jobs.Store }

func (q synthesisQueue) EnqueueSynthesis(ctx context.Context, tx pgx.Tx, now time.Time,
	args synthesis.JobArgs, dedupeKey string) (synthesis.Enqueued, error) {

	job, err := q.store.EnqueueTx(ctx, tx, now, jobs.Enqueueing{
		Kind: synthesis.JobKind, Args: args, DedupeKey: dedupeKey,
	})
	if err != nil {
		return synthesis.Enqueued{}, err
	}
	// The deadline comes back from the queue rather than being computed here. §7.1's five minutes
	// is configured once, in `ops.job_kind.sla_seconds`, and a second copy in Go would be the one
	// that disagreed after somebody tuned the first.
	return synthesis.Enqueued{JobID: job.ID, SLADeadline: job.SLADeadline}, nil
}

// synthesisHook is `visit`'s station hook pointed at the synthesis service.
//
// A named type rather than passing the service directly, so that the direction of the dependency is
// visible in this file: `visit` knows it has a hook, and only this line knows the hook is an AI
// summary.
type synthesisHook struct{ service *synthesis.Service }

func (h synthesisHook) EncounterFinished(ctx context.Context, tx pgx.Tx, touch visit.StationTouch) error {
	return h.service.EncounterFinished(ctx, tx, touch)
}

// synthesisBudget reads §7.1's deadline from the queue catalogue.
//
// From `ops.job_kind` rather than from a constant, because that row is where the number is
// configured and the SLA report has to be comparing the measurement against the budget that is
// actually being applied. A catalogue that cannot be read yields zero, and the report says the
// budget is unknown rather than printing five minutes as though somebody had set it — a report that
// invented its own target would be a report that always looked reassuring.
func synthesisBudget(ctx context.Context, store *jobs.Store, logger *slog.Logger) int {
	kinds, err := store.Kinds(ctx)
	if err != nil {
		logger.Warn("the job catalogue could not be read; the synthesis SLA report will not show its budget",
			"error", err.Error())
		return 0
	}
	for _, kind := range kinds {
		if kind.Kind == synthesis.JobKind && kind.SLASeconds != nil {
			return *kind.SLASeconds
		}
	}
	return 0
}
