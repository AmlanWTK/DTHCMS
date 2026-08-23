// Package platform assembles the shared runtime every DTHCMS binary needs: validated
// configuration, a logger that cannot leak patient identity, and connections to the
// database, cache and object store.
//
// Assembling it once means the api, worker, realtime and migrate binaries cannot drift
// apart in how they start, what they log, or how they shut down.
package platform

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/blobstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/cache"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/config"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/db"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/logging"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/telemetry"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/version"
)

// Runtime holds everything a binary needs.
type Runtime struct {
	Config    *config.Config
	Logger    *slog.Logger
	DB        *db.Pool
	Cache     *cache.Client
	Blob      blobstore.Store
	Clock     clock.Clock
	IDs       ids.Generator
	Telemetry *telemetry.Provider
}

// Options controls what a binary needs. The worker needs the database; the realtime
// gateway needs the cache; neither needs everything.
type Options struct {
	Service     string
	NeedsDB     bool
	NeedsCache  bool
	NeedsBlob   bool
	ConfigOnly  bool
	LogToStdout bool

	// NoTelemetry suppresses tracing and metrics for this process regardless of
	// configuration. Set by short-lived commands where a metric push interval longer
	// than the process lifetime would export nothing anyway.
	NoTelemetry bool
}

// Boot loads configuration, builds the logger, and connects what the service needs.
//
// Any failure returns an error rather than a partially-initialised runtime. A process
// that starts without its database and discovers that on the first clinical write has
// turned a deployment problem into a patient-facing one.
func Boot(ctx context.Context, opts Options) (*Runtime, error) {
	cfg, err := config.Load(opts.Service, version.Current().Version)
	if err != nil {
		return nil, err
	}

	logger := logging.New(os.Stdout, logging.Options{
		Level:   cfg.Log.Level,
		Format:  cfg.Log.Format,
		Service: opts.Service,
		Version: cfg.Version,
	})

	rt := &Runtime{
		Config: cfg,
		Logger: logger,
		Clock:  clock.Real{},
		IDs:    ids.UUIDv7{},
		Blob:   blobstore.Unconfigured{},
	}

	// version is already attached to every line by the logger, so it is not repeated here.
	logger.Info("starting",
		"env", string(cfg.Env),
		"commit", version.Current().Commit,
		"ai_tier", string(cfg.AI.Tier))

	// Telemetry starts before the database and the cache, so that their own
	// instrumentation has a provider to register against and the connection attempts
	// themselves appear in a trace. A failure here is a failure to configure, not a
	// failure to reach the collector — that one is tolerated by design.
	telemetryCfg := telemetry.Config{
		Enabled:        cfg.Telemetry.Enabled && !opts.NoTelemetry,
		Endpoint:       cfg.Telemetry.Endpoint,
		Insecure:       cfg.Telemetry.Insecure,
		SampleRatio:    cfg.Telemetry.SampleRatio,
		MetricInterval: cfg.Telemetry.MetricInterval,
		ExportTimeout:  cfg.Telemetry.ExportTimeout,
		Service:        opts.Service,
		Version:        cfg.Version,
		Environment:    string(cfg.Env),
		Instance:       hostname(),
	}

	tel, err := telemetry.Start(ctx, telemetryCfg, logger)
	if err != nil {
		return nil, fmt.Errorf("telemetry: %w", err)
	}
	rt.Telemetry = tel

	if tel.Active() {
		if err := telemetry.RegisterProcessMetrics(tel.Meter("dthcms/process")); err != nil {
			// Saturation metrics are worth having and not worth refusing to serve for.
			logger.Warn("process metrics unavailable", "error", err.Error())
		}
	}

	if opts.NeedsDB {
		pool, err := db.Open(ctx, db.Config{
			URL:             cfg.Postgres.URL,
			MaxConns:        cfg.Postgres.MaxConns,
			MinConns:        cfg.Postgres.MinConns,
			MaxConnLifetime: cfg.Postgres.MaxConnLifetime,
			ConnectTimeout:  cfg.Postgres.ConnectTimeout,
			Trace:           tel.Active(),
		})
		if err != nil {
			return nil, fmt.Errorf("postgres: %w", err)
		}
		rt.DB = pool

		// Report which server answered, not merely that one did. Two evenings were lost
		// to a database and a cache that answered correctly while being the wrong ones.
		dbID := pool.Identify(ctx)
		logger.Info("connected to postgres",
			"host", dbID.Host,
			"database", dbID.Database,
			"server", dbID.Version,
			"max_conns", cfg.Postgres.MaxConns)

		if tel.Active() {
			if err := pool.RegisterMetrics(tel.Meter("dthcms/db")); err != nil {
				logger.Warn("database pool metrics unavailable", "error", err.Error())
			}
		}
	}

	if opts.NeedsCache {
		client, err := cache.Open(ctx, cache.Config{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
			Trace:    tel.Active(),
		})
		if err != nil {
			rt.Close()
			return nil, fmt.Errorf("redis: %w", err)
		}
		rt.Cache = client

		cacheID := client.Identify(ctx)
		logger.Info("connected to redis",
			"addr", cacheID.Addr,
			"server_version", cacheID.Version,
			"server_os", cacheID.OS)
	}

	return rt, nil
}

// Close releases every connection the runtime holds. Safe to call more than once.
func (r *Runtime) Close() {
	if r == nil {
		return
	}
	if r.Cache != nil {
		if err := r.Cache.Close(); err != nil && r.Logger != nil {
			r.Logger.Error("closing redis", "error", err.Error())
		}
		r.Cache = nil
	}
	if r.DB != nil {
		r.DB.Close()
		r.DB = nil
	}
	if r.Telemetry != nil {
		// Flushed last and with its own deadline. The spans still in the buffer at
		// shutdown are usually the ones describing why the shutdown happened.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.Telemetry.Shutdown(ctx); err != nil && r.Logger != nil {
			r.Logger.Error("flushing telemetry", "error", err.Error())
		}
		r.Telemetry = nil
	}
}

// hostname identifies this process instance in telemetry. Failure is unimportant:
// "unknown" is a perfectly serviceable instance id on a single-instance deployment.
func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return name
}
