// Command worker runs background jobs: AI synthesis, OCR orchestration, notifications,
// nightly audits and projection rebuilds.
//
// It is a separate process from the API so that a burst of document processing can never
// slow down a clinician entering a blood pressure. The job framework itself arrives at
// CP69; this binary exists now so that deployment, configuration and shutdown are shared
// with the API rather than invented later.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
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

	rt.Logger.Info("worker started",
		"note", "no job queue is registered yet; the framework arrives at CP69")

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	rt.Logger.Info("worker shutting down")
}
