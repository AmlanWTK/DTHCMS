// Package db owns the PostgreSQL connection pool.
//
// It deliberately exposes no query helpers. Queries belong to the module that owns the
// table, in that module's repo.go, so that no package can quietly read another module's
// data (docs/architecture-boundaries.md).
package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Config is the subset of settings the pool needs.
type Config struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	ConnectTimeout  time.Duration

	// Trace produces a span for every query, nested under the request that caused it.
	// This is what turns "the endpoint is slow" into "this one query is slow", which is
	// the difference between a guess and a fix.
	Trace bool
}

// Pool is a PostgreSQL connection pool.
type Pool struct {
	*pgxpool.Pool
}

// Open creates the pool and verifies it can actually reach the database.
//
// Verifying at startup is deliberate: a process that starts happily and fails on its
// first request has moved a deployment problem into the clinic's working day.
func Open(ctx context.Context, cfg Config) (*Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		// The URL may contain a password, so the parse error is not wrapped verbatim.
		return nil, fmt.Errorf("postgres URL is not valid")
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	if cfg.Trace {
		// Query parameters are deliberately NOT included. otelpgx offers
		// WithIncludeQueryParameters, and enabling it would put every value the
		// application writes — names, national IDs, diagnoses — into a span attribute
		// bound for a telemetry backend. The statement text alone is what makes a slow
		// query identifiable, and it carries only placeholders.
		poolCfg.ConnConfig.Tracer = otelpgx.NewTracer(
			otelpgx.WithTrimSQLInSpanName(),
		)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating postgres pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("cannot reach postgres: %w", err)
	}

	return &Pool{Pool: pool}, nil
}

// Identity describes the server actually on the other end of the connection.
//
// It exists because "something answered" is not the same as "the right thing answered".
// A PostgreSQL installed on the developer's machine will accept a connection on the
// standard port and behave perfectly — while being an entirely different database. The
// same mistake in production would mean writing patient records to the wrong server.
// Logging what we reached makes the substitution visible immediately.
type Identity struct {
	Database string
	Version  string
	Host     string
}

// Identify asks the server what it is. Failure is not fatal: this is diagnostic
// information, and a service must not refuse to start because it could not be gathered.
func (p *Pool) Identify(ctx context.Context) Identity {
	id := Identity{Database: "unknown", Version: "unknown"}
	if p == nil || p.Pool == nil {
		return id
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var database, version string
	if err := p.QueryRow(ctx, "select current_database(), version()").Scan(&database, &version); err != nil {
		return id
	}

	id.Database = database
	// version() returns a long banner; the first three words identify the server.
	if fields := strings.Fields(version); len(fields) >= 3 {
		id.Version = strings.Join(fields[:3], " ")
	} else {
		id.Version = version
	}

	if cfg := p.Config(); cfg != nil && cfg.ConnConfig != nil {
		id.Host = fmt.Sprintf("%s:%d", cfg.ConnConfig.Host, cfg.ConnConfig.Port)
	}
	return id
}

// RegisterMetrics publishes connection pool saturation.
//
// This is the measurement that explains a whole class of incident. When every request
// suddenly takes seconds, the cause is usually not a slow query — it is that every
// connection is busy and requests are queuing to get one. Without this gauge that looks
// identical to "the database got slow", and the two have opposite fixes.
func (p *Pool) RegisterMetrics(meter metric.Meter) error {
	if p == nil || p.Pool == nil {
		return nil
	}

	acquired, err := meter.Int64ObservableGauge("db.client.connection.count",
		metric.WithDescription("Connections currently checked out of the pool."),
		metric.WithUnit("{connection}"))
	if err != nil {
		return fmt.Errorf("creating the connection count gauge: %w", err)
	}

	idle, err := meter.Int64ObservableGauge("db.client.connection.idle",
		metric.WithDescription("Connections open but not in use."),
		metric.WithUnit("{connection}"))
	if err != nil {
		return fmt.Errorf("creating the idle connection gauge: %w", err)
	}

	max, err := meter.Int64ObservableGauge("db.client.connection.max",
		metric.WithDescription("Pool size. The ceiling the other two are measured against."),
		metric.WithUnit("{connection}"))
	if err != nil {
		return fmt.Errorf("creating the max connection gauge: %w", err)
	}

	waiting, err := meter.Int64ObservableGauge("db.client.connection.pending_requests",
		metric.WithDescription("Callers blocked waiting for a connection. Above zero means the pool is the bottleneck."),
		metric.WithUnit("{request}"))
	if err != nil {
		return fmt.Errorf("creating the pending request gauge: %w", err)
	}

	// The key is the OpenTelemetry conventional label for a connection pool and the
	// value is a compile-time constant, so "name" here is not a person's.
	poolName := attribute.String("pool.name", "dthcms") // phicheck:ignore conventional pool label, constant value
	poolAttr := metric.WithAttributes(poolName)

	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		stat := p.Stat()
		observer.ObserveInt64(acquired, int64(stat.AcquiredConns()), poolAttr)
		observer.ObserveInt64(idle, int64(stat.IdleConns()), poolAttr)
		observer.ObserveInt64(max, int64(stat.MaxConns()), poolAttr)
		observer.ObserveInt64(waiting, stat.EmptyAcquireCount(), poolAttr)
		return nil
	}, acquired, idle, max, waiting)
	if err != nil {
		return fmt.Errorf("registering the pool metrics callback: %w", err)
	}
	return nil
}

// Check reports whether the database is reachable. Used by the readiness endpoint.
func (p *Pool) Check(ctx context.Context) error {
	if p == nil || p.Pool == nil {
		return fmt.Errorf("postgres pool is not initialised")
	}
	return p.Ping(ctx)
}

// Close releases the pool.
func (p *Pool) Close() {
	if p != nil && p.Pool != nil {
		p.Pool.Close()
	}
}
