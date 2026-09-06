package cache_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/cache"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
)

// The counter behind CP65's per-device rate limits, against a real Redis.
//
// Against a real one rather than a fake, because the property that matters most here — that two
// API instances hitting the same bucket at the same moment cannot both be allowed past the last
// unit of budget — is a property of the script running in Redis, and a fake would assert it about
// Go code that production never executes.

func limiter(t *testing.T) (*cache.Limiter, context.Context) {
	t.Helper()
	redis := testsupport.Redis(t)
	return cache.NewLimiter(redis.Client, redis.Prefix), context.Background()
}

func TestABurstIsAllowedThroughAndThenTheRateBites(t *testing.T) {
	lim, ctx := limiter(t)
	rule := httpx.Rule{Burst: 3, Every: 5 * time.Second}
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)

	for i := 1; i <= 3; i++ {
		got, err := lim.Take(ctx, "push|device:tablet-1", rule, now)
		if err != nil {
			t.Fatalf("take %d: %v", i, err)
		}
		if !got.Allowed {
			t.Fatalf("request %d of a burst of 3 was refused: a tablet coming back into signal "+
				"sends several batches back to back and must not be slowed down for it", i)
		}
	}

	got, err := lim.Take(ctx, "push|device:tablet-1", rule, now)
	if err != nil {
		t.Fatalf("take 4: %v", err)
	}
	if got.Allowed {
		t.Fatal("the fourth request in an instant was allowed: the burst is not bounded")
	}
	if got.RetryAfter <= 0 || got.RetryAfter > 5*time.Second {
		t.Fatalf("RetryAfter = %s, want something inside one interval", got.RetryAfter)
	}
}

func TestTheBudgetRefillsOneUnitAtATime(t *testing.T) {
	lim, ctx := limiter(t)
	rule := httpx.Rule{Burst: 2, Every: 10 * time.Second}
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ {
		if got, _ := lim.Take(ctx, "push|device:tablet-2", rule, now); !got.Allowed {
			t.Fatalf("request %d of the burst was refused", i)
		}
	}
	if got, _ := lim.Take(ctx, "push|device:tablet-2", rule, now); got.Allowed {
		t.Fatal("the burst was not bounded")
	}

	// One interval later: exactly one more, not the whole burst again.
	later := now.Add(10 * time.Second)
	if got, _ := lim.Take(ctx, "push|device:tablet-2", rule, later); !got.Allowed {
		t.Fatal("one interval bought no budget back")
	}
	if got, _ := lim.Take(ctx, "push|device:tablet-2", rule, later); got.Allowed {
		t.Fatal("one interval bought back the whole burst: the bucket refills at the rate, not in jumps")
	}
}

func TestAnIdleBucketRefillsCompletelyAndNoFurther(t *testing.T) {
	lim, ctx := limiter(t)
	rule := httpx.Rule{Burst: 3, Every: time.Second}
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		_, _ = lim.Take(ctx, "push|device:tablet-3", rule, now)
	}

	// An hour idle. The budget is full, and it is not more than full: a device that was off for a
	// week must not come back with a week's worth of allowance in hand.
	later := now.Add(time.Hour)
	allowed := 0
	for i := 0; i < 10; i++ {
		if got, _ := lim.Take(ctx, "push|device:tablet-3", rule, later); got.Allowed {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("an idle hour bought %d requests, want exactly the burst of 3", allowed)
	}
}

func TestTwoKeysAreTwoBuckets(t *testing.T) {
	lim, ctx := limiter(t)
	rule := httpx.Rule{Burst: 1, Every: time.Minute}
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)

	if got, _ := lim.Take(ctx, "push|device:a", rule, now); !got.Allowed {
		t.Fatal("the first device was refused its first request")
	}
	if got, _ := lim.Take(ctx, "push|device:b", rule, now); !got.Allowed {
		t.Fatal("a second device was refused because the first had spent its budget")
	}
}

func TestConcurrentTakesCannotBothWinTheLastUnit(t *testing.T) {
	// The reason this is one Lua script and not a read-modify-write from Go. Twenty goroutines,
	// one unit of budget: a limiter a client can beat by sending two requests at once is not a
	// limiter, and this is the failure mode that a single-process test would never show.
	lim, ctx := limiter(t)
	rule := httpx.Rule{Burst: 1, Every: time.Minute}
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)

	const racers = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0

	wg.Add(racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		go func() {
			defer wg.Done()
			<-start
			got, err := lim.Take(ctx, "push|device:contended", rule, now)
			if err == nil && got.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if allowed != 1 {
		t.Fatalf("%d of %d concurrent requests were allowed past a budget of 1", allowed, racers)
	}
}

func TestTheKeyExpiresOnceTheBucketIsFull(t *testing.T) {
	// Otherwise every device that ever synced costs a key in Redis forever.
	redis := testsupport.Redis(t)
	lim := cache.NewLimiter(redis.Client, redis.Prefix)
	ctx := context.Background()
	rule := httpx.Rule{Burst: 2, Every: 2 * time.Second}

	if _, err := lim.Take(ctx, "push|device:ttl", rule, time.Now().UTC()); err != nil {
		t.Fatalf("take: %v", err)
	}

	ttl, err := redis.Client.PTTL(ctx, redis.Prefix+"push|device:ttl").Result()
	if err != nil {
		t.Fatalf("reading the ttl: %v", err)
	}
	// interval + tolerance is 4s; the implementation adds a second of slack. What matters is that
	// it is set at all, and that it is not longer than a full refill plus that slack.
	if ttl <= 0 {
		t.Fatalf("the bucket has no expiry (%s): a key per device, forever", ttl)
	}
	if ttl > 6*time.Second {
		t.Fatalf("ttl = %s, longer than a full refill of this rule", ttl)
	}
}

func TestAnImpossibleRuleIsRefusedRatherThanApplied(t *testing.T) {
	// A rule nobody can satisfy would turn a typo into a clinic that cannot sync. Refused as a
	// misconfiguration — and because the middleware fails open on an error, the effect is an
	// unlimited route and a warning, not a stopped clinic.
	lim, ctx := limiter(t)
	for _, rule := range []httpx.Rule{
		{Burst: 0, Every: time.Second},
		{Burst: -1, Every: time.Second},
		{Burst: 1, Every: 0},
	} {
		t.Run(fmt.Sprintf("burst=%d every=%s", rule.Burst, rule.Every), func(t *testing.T) {
			if _, err := lim.Take(ctx, "push|device:bad", rule, time.Now().UTC()); err == nil {
				t.Fatal("an impossible rule was applied instead of refused")
			}
		})
	}
}

func TestTheRetryAfterIsLongEnoughToActuallyHelp(t *testing.T) {
	// A refusal that says "try again in 0" sends a well-behaved client straight back into it.
	lim, ctx := limiter(t)
	rule := httpx.Rule{Burst: 1, Every: 30 * time.Second}
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)

	_, _ = lim.Take(ctx, "push|device:wait", rule, now)
	got, err := lim.Take(ctx, "push|device:wait", rule, now)
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if got.Allowed {
		t.Fatal("the second request was allowed")
	}
	if got.RetryAfter < 29*time.Second || got.RetryAfter > 30*time.Second {
		t.Fatalf("RetryAfter = %s, want close to the 30s interval", got.RetryAfter)
	}

	// And waiting that long is actually enough.
	if next, _ := lim.Take(ctx, "push|device:wait", rule, now.Add(got.RetryAfter)); !next.Allowed {
		t.Fatal("waiting exactly as long as the server asked was still refused")
	}
}
