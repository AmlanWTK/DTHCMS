// Command migrate applies database migrations.
//
// It is a separate binary, run as an explicit step, and never on application start-up.
// A service that migrates its own database at boot will, sooner or later, migrate it
// during an unplanned restart in the middle of a clinic.
//
// Usage:
//
//	migrate up          apply every pending migration, then verify invariants
//	migrate status      show which migrations have been applied
//	migrate version     print the current schema version
//	migrate verify      check checksums and invariants without applying anything
//	migrate down        roll back one migration (refused in production)
//	migrate dev-roles   create local login roles (refused in production)
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/migrate"
	"github.com/AmlanWTK/DTHCMS/backend/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	// ConfigOnly: this binary connects with the migration URL, not the application one,
	// so Boot must not open the application pool.
	// NoTelemetry: this process lives for a few seconds, which is shorter than the
	// metric push interval, so a meter provider here would export nothing and delay
	// exit while it tried.
	rt, err := platform.Boot(ctx, platform.Options{
		Service:     "migrate",
		ConfigOnly:  true,
		NoTelemetry: true,
	})
	if err != nil {
		return err
	}
	defer rt.Close()

	runner, err := migrate.New(migrate.Options{
		FS:         migrations.FS,
		DSN:        rt.Config.Postgres.MigrationURL,
		Logger:     rt.Logger,
		Production: rt.Config.Env.IsProduction(),
	})
	if err != nil {
		return err
	}

	switch command {
	case "up":
		return runner.Up(ctx)

	case "status":
		return runner.Status(ctx)

	case "version":
		version, err := runner.Version(ctx)
		if err != nil {
			return err
		}
		rt.Logger.Info("schema version", "version", version)
		return nil

	case "verify":
		return runner.Verify(ctx)

	case "down":
		return runner.Down(ctx)

	case "dev-roles":
		return runner.EnsureDevRoles(ctx, rt.Config.Postgres.DevRolePassword)

	case "help", "-h", "--help":
		usage()
		return nil

	default:
		usage()
		return fmt.Errorf("unknown command %q", command)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `
migrate — DTHCMS database migrations

  up          apply every pending migration, then verify database invariants
  status      show which migrations have been applied
  version     print the current schema version
  verify      check file checksums and database invariants; apply nothing
  down        roll back one migration (refused in production)
  dev-roles   create local login roles with production-shaped privileges

Connection comes from DTHCMS_POSTGRES_MIGRATION_URL, which is deliberately not the
application's DTHCMS_POSTGRES_URL.
`)
}
