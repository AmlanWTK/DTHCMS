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

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config is the subset of settings the pool needs.
type Config struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	ConnectTimeout  time.Duration
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
