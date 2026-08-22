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

	"github.com/redis/go-redis/v9"
)

// Config is the subset of settings the client needs.
type Config struct {
	Addr     string
	Password string
	DB       int
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
