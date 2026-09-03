// Command projector builds the read models from the event ledger (CP25).
//
//	projector run                      follow the ledger and keep every asynchronous
//	                                   projection up to date
//	projector rebuild [name ...]        throw the read models away and derive them again
//	                                   from event one; no names means all of them
//	projector status                    each projection's checkpoint, lag and health
//
// It is a separate binary because it connects as a different database role. `dthcms_app`
// may not write to the `read` schema at all — `core.assert_read_models_derived()` refuses
// to start a service where it can — so the process that writes read models is the one that
// connects as `dthcms_projector`, and nothing else in the system holds that privilege.
//
// A rebuild is audited: the trail records who ran it, on what, and how much was replayed.
// That is the one thing about a rebuild that must be answerable afterwards, because it is
// the only operation in DTHCMS that legitimately deletes derived clinical data.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/db"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

func main() { os.Exit(run()) }

func run() int {
	flags := flag.NewFlagSet("projector", flag.ContinueOnError)
	reason := flags.String("reason", "", "why this rebuild is being run (recorded in the audit trail)")
	operator := flags.String("operator", "", "the employee code of the person running the rebuild")
	batch := flags.Int("batch", 1000, "events per batch")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return 2
	}

	command := "run"
	if args := flags.Args(); len(args) > 0 {
		command = args[0]
	}

	ctx := context.Background()
	rt, err := platform.Boot(ctx, platform.Options{
		Service: "projector",
		NeedsDB: true,
		// A rebuild is a one-shot command whose lifetime is usually shorter than the
		// metric push interval, so telemetry would export nothing but cost a connection.
		NoTelemetry: command != "run",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "projector: cannot start: %v\n", err)
		return 1
	}
	defer rt.Close()

	// The projector's own connection. rt.DB is the application's role, which may read the
	// read models and write none of them.
	pool, err := db.Open(ctx, db.Config{
		URL:             rt.Config.Postgres.ProjectorURL,
		MaxConns:        rt.Config.Postgres.MaxConns,
		MinConns:        rt.Config.Postgres.MinConns,
		MaxConnLifetime: rt.Config.Postgres.MaxConnLifetime,
		ConnectTimeout:  rt.Config.Postgres.ConnectTimeout,
	})
	if err != nil {
		rt.Logger.Error("cannot connect as the projection role",
			"hint", "DTHCMS_POSTGRES_PROJECTOR_URL; locally, run `migrate dev-roles`",
			"error", err.Error())
		return 1
	}
	defer pool.Close()

	events := eventstore.New(eventstore.Config{Pool: pool.Pool, Clock: clock.Real{}})
	engine := projection.NewEngineWithEvents(pool.Pool, projection.Default, events)

	switch command {
	case "run":
		return follow(ctx, rt, engine, events)
	case "rebuild":
		return rebuild(ctx, rt, engine, flags.Args()[1:], *reason, *operator, *batch)
	case "status":
		return status(ctx, engine)
	default:
		fmt.Fprintf(os.Stderr, "projector: unknown command %q (want run, rebuild or status)\n", command)
		return 2
	}
}

func follow(ctx context.Context, rt *platform.Runtime, engine *projection.Engine, events *eventstore.Store) int {
	unregister, err := projection.RegisterMetrics(projection.MetricsConfig{
		Engine: engine, Provider: rt.Telemetry, Logger: rt.Logger,
	})
	if err != nil {
		rt.Logger.Error("cannot publish projection lag", "error", err.Error())
		return 1
	}
	defer func() { _ = unregister() }()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner := projection.NewRunner(engine, events, projection.RunnerConfig{Logger: rt.Logger})
	rt.Logger.Info("projector following the ledger",
		"projections", strings.Join(engine.Registry().Names(), ", "))

	if err := runner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		rt.Logger.Error("projector stopped with an error", "error", err.Error())
		return 1
	}
	rt.Logger.Info("projector shutting down")
	return 0
}

func rebuild(ctx context.Context, rt *platform.Runtime, engine *projection.Engine,
	names []string, reason, operator string, batch int) int {

	if strings.TrimSpace(reason) == "" {
		fmt.Fprintln(os.Stderr, "projector: -reason is required for a rebuild; it goes in "+
			"the audit trail, and deleting derived clinical data with no recorded reason is "+
			"not an operation this system offers")
		return 2
	}
	if len(names) == 0 {
		names = engine.Registry().Names()
	}

	opts := projection.RebuildOptions{BatchSize: batch, Logger: rt.Logger}
	recorder := audit.NewRecorder(audit.NewPostgresStore(rt.DB.Pool), clock.Real{}, rt.Logger)

	for _, name := range names {
		result, err := engine.Rebuild(ctx, name, opts)
		if err != nil {
			rt.Logger.Error("rebuild failed", "projection", name, "error", err.Error())
			record(ctx, rt, recorder, name, operator, reason, result, err)
			return 1
		}
		fmt.Printf("%s v%d: %d events, %d applied, checkpoint %d, %s\n",
			result.Projection, result.Version, result.Events, result.Applied,
			result.Checkpoint, result.Duration.Round(time.Millisecond))
		record(ctx, rt, recorder, name, operator, reason, result, nil)
	}
	return 0
}

func status(ctx context.Context, engine *projection.Engine) int {
	lags, err := engine.Lags(ctx, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "projector: %v\n", err)
		return 1
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "PROJECTION\tVERSION\tMODE\tSTATUS\tCHECKPOINT\tBEHIND\tAGE\tDEAD")
	for _, l := range lags {
		age := "-"
		if l.Age > 0 {
			age = l.Age.Round(time.Second).String()
		}
		_, _ = fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%d\t%d\t%s\t%d\n",
			l.Name, l.Version, l.Mode, l.Status, l.Checkpoint, l.Behind, age, l.OpenDeadLetters)
	}
	_ = w.Flush()
	return 0
}

// record writes the audit entry. A rebuild that cannot be audited still runs — refusing to
// rebuild because the trail is unavailable would turn a logging problem into an outage —
// but it says so, loudly, in the log.
func record(ctx context.Context, rt *platform.Runtime, recorder *audit.Recorder,
	name, operator, reason string, result projection.RebuildResult, cause error) {

	facility, err := defaultFacility(ctx, rt)
	if err != nil {
		rt.Logger.Warn("rebuild not audited: no facility", "error", err.Error())
		return
	}
	details := map[string]any{
		"projection": name, "version": result.Version, "events": result.Events,
		"applied": result.Applied, "checkpoint": result.Checkpoint, "reason": reason,
	}
	kind := "projection.rebuilt"
	if cause != nil {
		kind = "projection.rebuild_failed"
		details["error"] = cause.Error()
	}
	if _, err := recorder.Record(ctx, audit.Entry{
		Kind: kind, FacilityID: facility, ActorCode: operatorOrUnknown(operator),
		ActorRole: "OPERATOR", Reason: reason, Details: details,
	}); err != nil {
		rt.Logger.Error("rebuild not audited", "error", err.Error())
	}
}

func operatorOrUnknown(operator string) string {
	if strings.TrimSpace(operator) == "" {
		return "UNKNOWN"
	}
	return operator
}

func defaultFacility(ctx context.Context, rt *platform.Runtime) (uuid.UUID, error) {
	var id uuid.UUID
	err := rt.DB.Pool.QueryRow(ctx, `SELECT core.default_facility()`).Scan(&id)
	return id, err
}
