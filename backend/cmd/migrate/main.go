// Command migrate applies database migrations.
//
// It is a separate binary, run as an explicit step with an approval gate, and never on
// application start-up. A service that migrates its own database at boot will, sooner or
// later, migrate it during an unplanned restart in the middle of a clinic.
//
// The migration framework arrives at CP06.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
)

func main() {
	ctx := context.Background()

	rt, err := platform.Boot(ctx, platform.Options{
		Service: "migrate",
		NeedsDB: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: cannot start: %v\n", err)
		os.Exit(1)
	}
	defer rt.Close()

	rt.Logger.Info("database reachable; no migrations to apply",
		"note", "the migration framework and the first schema arrive at CP06")
}
