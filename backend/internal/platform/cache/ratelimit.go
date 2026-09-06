package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// gcra is the whole limiter, and it is one script on purpose.
//
// The generic cell rate algorithm keeps a single number per bucket: the theoretical
// arrival time of the next request that would be exactly on schedule. Everything else is
// arithmetic on that number, which is why it fits in one round trip and one key — and why
// it is atomic without a lock. A read-modify-write from Go would race two API instances
// against each other, and a limiter that a client can beat by sending two requests at once
// is not a limiter.
//
// Reading it:
//
//	tat       when the next perfectly-spaced request is due
//	interval  one unit of budget, in milliseconds
//	tolerance how far ahead of schedule a client may run — (burst-1) intervals, so that
//	          exactly `burst` requests may arrive together from an idle bucket
//
// A request is allowed when `tat - tolerance` is not in the future. The TTL is
// `interval + tolerance`, which is exactly how long an idle bucket takes to refill
// completely: after that the key's absence and its presence mean the same thing, and
// letting it expire is what stops every device that ever synced from costing a key
// forever.
var gcra = redis.NewScript(`
local tat = tonumber(redis.call('GET', KEYS[1]))
local now = tonumber(ARGV[1])
local interval = tonumber(ARGV[2])
local tolerance = tonumber(ARGV[3])
if not tat or tat < now then
  tat = now
end
local allow_at = tat - tolerance
if allow_at > now then
  return {0, math.ceil(allow_at - now)}
end
redis.call('SET', KEYS[1], tat + interval, 'PX', math.ceil(interval + tolerance) + 1000)
return {1, 0}
`)

// Limiter counts requests in Redis, shared across every API instance.
type Limiter struct {
	client redis.Scripter
	prefix string
}

// NewLimiter builds one. The prefix namespaces its keys away from sessions and nonces.
func NewLimiter(client redis.Scripter, prefix string) *Limiter {
	return &Limiter{client: client, prefix: prefix}
}

// Take consumes one unit of budget, and reports whether there was one.
//
// The time comes from the caller rather than from Redis so that a test can move it, and so
// that every instance in a deployment agrees about now — Redis TIME would be the same
// clock for all of them, but at the cost of a second round trip per request for a
// difference that NTP already keeps under a second.
func (l *Limiter) Take(ctx context.Context, key string, rule httpx.Rule, now time.Time) (httpx.Decision, error) {
	if rule.Burst < 1 || rule.Every <= 0 {
		// A rule nobody can satisfy would refuse the clinic's work on a typo. Refused as a
		// misconfiguration rather than applied.
		return httpx.Decision{}, fmt.Errorf("cache: a rate limit rule needs a burst of at least 1 and a positive interval, got %d and %s",
			rule.Burst, rule.Every)
	}

	interval := float64(rule.Every.Milliseconds())
	tolerance := interval * float64(rule.Burst-1)

	out, err := gcra.Run(ctx, l.client, []string{l.prefix + key},
		now.UnixMilli(), interval, tolerance).Slice()
	if err != nil {
		return httpx.Decision{}, fmt.Errorf("cache: taking from the rate limit bucket: %w", err)
	}
	if len(out) != 2 {
		return httpx.Decision{}, fmt.Errorf("cache: the rate limit script returned %d values, expected 2", len(out))
	}

	allowed, ok := out[0].(int64)
	if !ok {
		return httpx.Decision{}, fmt.Errorf("cache: the rate limit script returned %T for its verdict", out[0])
	}
	if allowed == 1 {
		return httpx.Decision{Allowed: true}, nil
	}
	wait, ok := out[1].(int64)
	if !ok {
		return httpx.Decision{}, fmt.Errorf("cache: the rate limit script returned %T for its wait", out[1])
	}
	return httpx.Decision{Allowed: false, RetryAfter: time.Duration(wait) * time.Millisecond}, nil
}
