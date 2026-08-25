package testsupport

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisURLEnv names the Redis a test connects to.
const RedisURLEnv = "DTHCMS_TEST_REDIS_URL"

// Cache is a test's isolated slice of Redis.
//
// Isolation is by key prefix rather than by database index. Redis offers sixteen numbered
// databases, which is a hard ceiling on parallel tests and an unpleasant one to discover:
// the seventeenth test does not fail, it quietly shares state with the first. A prefix has
// no ceiling, and it survives a Redis configured with `databases 1`, which is how most
// managed instances ship.
type Cache struct {
	Client *redis.Client
	// Prefix is unique to this test. Everything it writes must start with it.
	Prefix string
}

// Key namespaces a key to this test.
func (c *Cache) Key(name string) string { return c.Prefix + name }

// Redis gives the test an isolated slice of a real Redis, cleaned up afterwards.
func Redis(t *testing.T) *Cache {
	t.Helper()

	raw := os.Getenv(RedisURLEnv)
	if raw == "" {
		t.Skipf("set %s to run cache integration tests — `make up` starts one", RedisURLEnv)
	}

	options, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatalf("parsing %s: %v", RedisURLEnv, err)
	}

	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("cannot reach the test Redis: %v", err)
	}

	cache := &Cache{
		Client: client,
		Prefix: fmt.Sprintf("dthcms_test:%d:%d:", os.Getpid(), sequence.Add(1)),
	}

	// Deleted afterwards rather than flushed. FLUSHDB would take the other parallel tests
	// down with it, and on a developer's machine it would take their local stack's cache
	// with it too.
	t.Cleanup(func() { cache.deleteAll(t) })

	return cache
}

func (c *Cache) deleteAll(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var cursor uint64
	for {
		keys, next, err := c.Client.Scan(ctx, cursor, c.Prefix+"*", 256).Result()
		if err != nil {
			t.Logf("could not scan test keys under %s: %v", c.Prefix, err)
			return
		}
		if len(keys) > 0 {
			if err := c.Client.Del(ctx, keys...).Err(); err != nil {
				t.Logf("could not delete test keys under %s: %v", c.Prefix, err)
				return
			}
		}
		if next == 0 {
			return
		}
		cursor = next
	}
}
