// Command worker runs background jobs: AI synthesis, OCR orchestration, notifications,
// nightly audits and projection rebuilds.
//
// It is a separate process from the API so that a burst of document processing can never
// slow down a clinician entering a blood pressure. The job framework itself arrives at
// CP69; this binary exists now so that deployment, configuration and shutdown are shared
// with the API rather than invented later.
//
// One job runs here already, because it has no dependency on the framework and its absence
// would be felt: the idempotency purge (CP24). The plan names table growth as this
// checkpoint's risk, and a TTL nothing enforces is not a TTL.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/idempotency"
)

// idempotencyPurgeInterval is how often expired response records are removed. Hourly: the
// records live 24 hours, so an hour's granularity keeps at most an hour of dead rows, and
// a delete of a few thousand rows on an indexed column costs nothing.
const idempotencyPurgeInterval = time.Hour

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

	rt.Logger.Info("worker started",
		"note", "the job queue arrives at CP69; the idempotency purge runs on its own ticker")

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	go purgeIdempotency(ctx, rt)

	<-ctx.Done()
	rt.Logger.Info("worker shutting down")
}

// purgeIdempotency deletes expired response records until the process is asked to stop.
//
// A failure is logged and the loop continues: a purge that could not run is an operational
// annoyance, and stopping the worker over one would turn it into an outage.
func purgeIdempotency(ctx context.Context, rt *platform.Runtime) {
	store := idempotency.New(rt.DB.Pool)
	ticker := time.NewTicker(idempotencyPurgeInterval)
	defer ticker.Stop()

	run := func() {
		// Its own timeout: a purge must never hold a connection past the next tick.
		attempt, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		removed, err := store.Purge(attempt, time.Now().UTC())
		if err != nil {
			rt.Logger.ErrorContext(ctx, "idempotency purge failed", "error", err.Error())
			return
		}
		if removed > 0 {
			rt.Logger.InfoContext(ctx, "idempotency records purged", "removed", removed)
		}
	}

	run() // once at start, so a worker restarted after a long outage tidies up immediately
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
