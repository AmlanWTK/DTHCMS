// Command worker runs background jobs: AI synthesis, OCR orchestration, notifications,
// nightly audits and projection rebuilds.
//
// It is a separate process from the API so that a burst of document processing can never slow
// down a clinician entering a blood pressure. That separation is also why the worker serves
// **queues** rather than everything: `DTHCMS_WORKER_QUEUES` decides what this process claims, so a
// deployment can put the clinical queue on its own instances and leave document processing to
// share.
//
// # What runs here, and in which of the two shapes
//
// Two mechanisms coexist on purpose.
//
// **The queue (CP69)** is for work somebody asked for: a synthesis for this visit, an SMS to this
// patient. It is enqueued in the transaction that decided the work was needed, retried on a policy,
// dead-lettered visibly, and measured against an SLA.
//
// **Two sweeps** are not queued, because they are not about a request. The escalation sweep (CP50)
// reads durable state and advances whatever fell due while nothing was running; the reaper returns
// jobs abandoned by a stopped worker. Both must run when nobody has enqueued anything — and the
// reaper in particular cannot be a queued job, because the failure it exists for is exactly the one
// where the queue has stopped being drained.
//
// The idempotency purge (CP24) moved onto the queue, where it belongs: it is periodic work with a
// retry policy, and it was on its own ticker only because there was no queue to put it on.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/jobs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/idempotency"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
)

func main() {
	ctx := context.Background()

	rt, err := platform.Boot(ctx, platform.Options{
		Service:    "worker",
		NeedsDB:    true,
		NeedsCache: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker: cannot start: %v\n", err)
		os.Exit(1)
	}
	defer rt.Close()

	cfg := rt.Config.Worker
	store := jobs.NewStore(rt.DB.Pool)

	kinds, err := store.Kinds(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker: cannot read the job catalogue: %v\n", err)
		os.Exit(1)
	}
	byKind := make(map[string]jobs.Kind, len(kinds))
	for _, k := range kinds {
		byKind[k.Kind] = k
	}

	registry := jobs.NewRegistry()
	registerHandlers(registry, rt, store)

	// Fail at startup, not at three in the morning. A handler for a kind the database does not
	// know is a typo whose job would never be claimed; a kind this worker's queues would claim
	// with no handler is work that is enqueued, waits, and is never done — and the queue-health
	// page would show a rising depth with nobody able to say why.
	if err := registry.Check(kinds, cfg.Queues); err != nil {
		fmt.Fprintf(os.Stderr, "worker: %v\n", err)
		os.Exit(1)
	}

	rt.Logger.Info("worker started",
		"worker", cfg.Name, "queues", strings.Join(cfg.Queues, ","),
		"concurrency", cfg.Concurrency, "kinds", len(kinds))

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	run := func(name string, fn func(context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(ctx)
			rt.Logger.Info("worker loop stopped", "loop", name)
		}()
	}

	// Criterion 4's numbers. Registered on the worker rather than the API because the depth and
	// age gauges read the table, and a dozen API instances all polling it would be a dozen
	// identical time series and twelve times the queries — the worker is the one process that is
	// already awake for this.
	var instruments *jobs.Instruments
	if rt.Telemetry.Active() {
		meter := rt.Telemetry.Meter("dthcms/jobs")
		if err := jobs.RegisterMetrics(jobs.MetricsConfig{
			Store: store, Meter: meter, Clock: clock.Real{}, Logger: rt.Logger,
		}); err != nil {
			// Worth having, not worth refusing to serve for: a clinic that cannot run because its
			// dashboard is unavailable is a worse system than one that runs blind for an hour.
			rt.Logger.Warn("queue metrics unavailable", "error", err.Error())
		}
		if built, err := jobs.NewInstruments(meter); err != nil {
			rt.Logger.Warn("job instruments unavailable", "error", err.Error())
		} else {
			instruments = built
		}
	}

	worker := jobs.NewWorker(jobs.WorkerConfig{
		Name: cfg.Name, Queues: cfg.Queues, Concurrency: cfg.Concurrency,
		PollInterval: cfg.PollInterval, LeaseDuration: cfg.LeaseDuration,
		Store: store, Registry: registry, Kinds: byKind,
		Clock: clock.Real{}, Logger: rt.Logger, Metrics: instruments,
	})
	run("queue", func(ctx context.Context) { _ = worker.Run(ctx) })

	// The reaper cannot itself be a queued job: the failure it exists for is precisely the one
	// where the queue has stopped being drained.
	reaper := &jobs.Reaper{
		Store: store, Interval: cfg.ReapInterval, Clock: clock.Real{}, Logger: rt.Logger,
	}
	run("reaper", reaper.Run)

	scheduler := &jobs.Scheduler{
		Store: store, Interval: cfg.ScheduleInterval, Clock: clock.Real{}, Logger: rt.Logger,
	}
	run("scheduler", scheduler.Run)

	// Acknowledge-or-escalate (CP50). A sweep rather than a queued job, and the distinction is
	// the same one the reaper makes: it reads durable state and advances whatever fell due while
	// nothing was running, so a worker started thirty seconds late catches up in its first pass.
	// A queued job would have needed something to enqueue it, and that something is what has just
	// been down.
	if rt.Cache != nil {
		run("escalation", func(ctx context.Context) { escalateUnacknowledged(ctx, rt) })
	} else {
		rt.Logger.Warn("no cache configured; critical-value escalations will be recorded but not announced")
	}

	<-ctx.Done()
	rt.Logger.Info("worker shutting down; waiting for work in flight")
	wg.Wait()
	rt.Logger.Info("worker stopped")
}

// registerHandlers attaches the handler for every kind this build knows how to do.
//
// The composition root, and deliberately here rather than in `internal/jobs`: the queue package is
// allowed `platform` and `rbac` only, so a handler that needs a clinical service is wired where the
// rest of the wiring already lives. A kind whose handler is not here yet — synthesis, OCR, SMS —
// arrives with the checkpoint that implements it, and until then `Registry.Check` refuses to start
// a worker whose queues would claim it.
func registerHandlers(registry *jobs.Registry, rt *platform.Runtime, store *jobs.Store) {
	registry.Register("maintenance.idempotency_purge", purgeIdempotency(rt))
	registry.Register("maintenance.lease_reap", func(context.Context, jobs.Running) error {
		// The reaper runs on its own loop, for the reason above. The kind exists so that the
		// schedule row has something to point at and the dashboard can show the sweep is
		// registered; the handler is a no-op that says so rather than a second reaper racing the
		// first.
		return nil
	})
}

// purgeIdempotency deletes expired response records (CP24).
//
// A queued job now rather than its own ticker: it is periodic work with a retry policy, and it was
// on a ticker only because there was no queue to put it on. The plan names table growth as CP24's
// risk, and a TTL nothing enforces is not a TTL.
func purgeIdempotency(rt *platform.Runtime) jobs.Handler {
	return func(ctx context.Context, _ jobs.Running) error {
		attempt, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		removed, err := idempotency.New(rt.DB.Pool).Purge(attempt, time.Now().UTC())
		if err != nil {
			return err
		}
		if removed > 0 {
			rt.Logger.InfoContext(ctx, "idempotency records purged", "removed", removed)
		}
		return nil
	}
}

// escalateUnacknowledged runs the CP50 sweep.
//
// It writes through the same ledger the API does, with the same synchronous projections
// inside the transaction — an escalation that was recorded but not projected would leave a
// consultant's board saying an alert is still on its first step.
func escalateUnacknowledged(ctx context.Context, rt *platform.Runtime) {
	store := clinical.NewStore(rt.DB.Pool)
	events := eventstore.New(eventstore.Config{
		Pool:        rt.DB.Pool,
		Clock:       clock.Real{},
		Synchronous: projection.NewSyncSet(projection.Default),
	})
	sweep := &escalator{
		alerts:    store,
		service:   clinical.NewService(store, events, clock.Real{}),
		publisher: realtime.NewPublisher(rt.Cache.Client, rt.Logger),
		logger:    rt.Logger,
		now:       time.Now,
	}
	sweep.run(ctx)
}
