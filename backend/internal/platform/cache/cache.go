// Package cache owns the Redis client.
//
// Redis holds cache entries, session state, rate-limit counters and pub/sub fan-out for
// the realtime gateway. It holds no durable clinical data — that lives in Postgres, and
// the job queue lives there too (ADR-0003), so losing Redis degrades the system without
// losing anything.
package cache

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

// Config is the subset of settings the client needs.
type Config struct {
	Addr     string
	Password string
	DB       int

	// Trace produces a span for every Redis command.
	//
	// Redis keys in DTHCMS embed identifiers — session:{user_id}, queue:{station_id} —
	// which are safe by the same rule that makes patient_id safe to log. What would not
	// be safe is a command argument: a cached search result keyed by what an operator
	// typed. redisotel records the command name and key, not argument values, and the
	// redacting span exporter scrubs whatever slips past that.
	Trace bool
}

// Client wraps the Redis client.
type Client struct {
	*redis.Client
}

// Open creates the client and verifies the server answers.
func Open(ctx context.Context, cfg Config) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	if cfg.Trace {
		if err := redisotel.InstrumentTracing(rdb); err != nil {
			_ = rdb.Close()
			return nil, fmt.Errorf("instrumenting redis tracing: %w", err)
		}
		if err := redisotel.InstrumentMetrics(rdb); err != nil {
			_ = rdb.Close()
			return nil, fmt.Errorf("instrumenting redis metrics: %w", err)
		}
	}

	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("cannot reach redis at %s: %w", cfg.Addr, err)
	}

	return &Client{Client: rdb}, nil
}

// Identity describes the server actually answering on the configured address.
//
// Redis-compatible servers exist for Windows (Memurai among them) and answer on the
// standard port indistinguishably from Redis itself. Reporting the version and operating
// system makes it obvious when the connection has gone somewhere unintended: this stack's
// Redis runs on Linux, so "os: Windows" means something else answered.
type Identity struct {
	Version string
	OS      string
	Addr    string
}

// Identify asks the server what it is. Failure is not fatal; it is diagnostic only.
func (c *Client) Identify(ctx context.Context) Identity {
	id := Identity{Version: "unknown", OS: "unknown"}
	if c == nil || c.Client == nil {
		return id
	}
	id.Addr = c.Options().Addr

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	info, err := c.Info(ctx, "server").Result()
	if err != nil {
		return id
	}

	for _, line := range strings.Split(info, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found {
			continue
		}
		switch key {
		case "redis_version":
			id.Version = value
		case "os":
			id.OS = value
		}
	}
	return id
}

// Check reports whether Redis is reachable. Used by the readiness endpoint.
func (c *Client) Check(ctx context.Context) error {
	if c == nil || c.Client == nil {
		return fmt.Errorf("redis client is not initialised")
	}
	return c.Ping(ctx).Err()
}

// Close releases the client.
func (c *Client) Close() error {
	if c == nil || c.Client == nil {
		return nil
	}
	return c.Client.Close()
}

// Remember records a key for ttl and reports whether it was new.
//
// SET NX EX in one round trip: the first caller to present a key wins, every later one
// inside the ttl is told it was seen. This is the replay guard for device-signed requests
// (CP18) — a nonce is remembered for twice the clock skew, which is as long as a captured
// request could possibly still verify. A Redis failure is an error, not a "fresh": a guard
// that fails open under load is a guard that fails exactly when it is being tested.
func (c *Client) Remember(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if c == nil || c.Client == nil {
		return false, fmt.Errorf("redis client is not initialised")
	}
	return c.SetNX(ctx, key, "1", ttl).Result()
}

// Get, Set and Delete make the client a bounded memo for the RBAC resolver (CP19): a
// value with a TTL, dropped on invalidation. Get reports absence rather than erroring on
// it, so a cold cache is a slower request and not a failed one.
func (c *Client) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if c == nil || c.Client == nil {
		return nil, false, fmt.Errorf("redis client is not initialised")
	}
	raw, err := c.Client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if c == nil || c.Client == nil {
		return fmt.Errorf("redis client is not initialised")
	}
	return c.Client.Set(ctx, key, value, ttl).Err()
}

func (c *Client) Delete(ctx context.Context, key string) error {
	if c == nil || c.Client == nil {
		return fmt.Errorf("redis client is not initialised")
	}
	return c.Client.Del(ctx, key).Err()
}
