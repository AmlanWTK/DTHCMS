package ai

import (
	"sync"
	"time"
)

// The circuit breaker (§10.3 step 5).
//
// # What it is actually for
//
// Not the provider. Google does not need protecting from this clinic. It is for *us*: when Gemini
// is down, every synthesis job in the queue spends its whole timeout waiting, three times over, and
// §7.1's five-minute promise fails for every patient in the building rather than for the one whose
// call happened to be in flight. An open breaker converts a slow, expensive, repeated failure into
// an immediate one, which is what lets D-15's degraded mode actually be reached in time to be
// useful — "AI summary unavailable, here is the structured record" is only a useful screen if it
// appears before the consultation rather than five minutes into it.
//
// # Per model version, and honestly in-process
//
// The key is the model version rather than the provider, because a Pro-class model being rate
// limited says nothing about a Flash-class one, and the registered fallback (§10.4 A1) is exactly
// the thing that must still be reachable when the primary is not.
//
// The state is per process and is not shared. With two API instances and a worker, three breakers
// open independently and each pays its own three failures to learn what the others already know.
// That is a real cost and it is accepted: a shared breaker means a round trip to Redis on the
// hot path of every AI call to save nine wasted requests during an outage, plus a new failure mode
// where the store holding the breaker is itself what is down. The clinic runs one worker.

// Breaker is a set of per-model circuits.
type Breaker struct {
	mu       sync.Mutex
	circuits map[string]*circuit

	// failures is how many consecutive failures open a circuit. Three: one failure is a packet,
	// two is a coincidence, three is an outage.
	failures int
	// cooldown is how long it stays open. Thirty seconds: long enough that a queue of jobs drains
	// into the degraded state quickly, short enough that a brief blip does not keep the AI dark
	// for a whole clinic session.
	cooldown time.Duration
	now      func() time.Time
}

type circuit struct {
	consecutive int
	openedAt    time.Time
	// probing is the half-open state: exactly one call is let through after the cooldown, and
	// until it answers, everything else is still refused. Without it, a queue of forty jobs all
	// arrive the instant the cooldown expires and all forty hit a provider that is still down.
	probing bool
}

// NewBreaker builds one.
func NewBreaker(failures int, cooldown time.Duration, now func() time.Time) *Breaker {
	if failures < 1 {
		failures = 3
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	if now == nil {
		now = time.Now
	}
	return &Breaker{circuits: map[string]*circuit{}, failures: failures, cooldown: cooldown, now: now}
}

// Allow reports whether a call to this model may proceed.
func (b *Breaker) Allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	c := b.circuits[key]
	if c == nil || c.openedAt.IsZero() {
		return true
	}
	if b.now().Sub(c.openedAt) < b.cooldown {
		return false
	}
	if c.probing {
		// Somebody else is already finding out. Refusing here rather than joining them is the
		// whole point of half-open: one request learns the answer, the rest fail fast.
		return false
	}
	c.probing = true
	return true
}

// Succeeded closes the circuit.
func (b *Breaker) Succeeded(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.circuits, key)
}

// Failed counts one failure and opens the circuit at the threshold.
func (b *Breaker) Failed(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	c := b.circuits[key]
	if c == nil {
		c = &circuit{}
		b.circuits[key] = c
	}
	// A failed probe re-opens the circuit for a fresh cooldown rather than counting towards the
	// threshold again: the probe already told us the provider is still down, and making it earn
	// three more failures would mean three more full timeouts per cooldown for as long as the
	// outage lasts.
	if c.probing {
		c.probing = false
		c.openedAt = b.now()
		return
	}
	c.consecutive++
	if c.consecutive >= b.failures {
		c.openedAt = b.now()
		c.consecutive = 0
	}
}

// Open reports whether a circuit is currently refusing, for the operator screen and the metric.
//
// Deliberately not `!Allow(key)`: Allow *takes* the half-open probe, so a metric callback asking
// the breaker's state every fifteen seconds would consume the one request that was meant to find
// out whether the provider had recovered — and the circuit would then stay open for as long as
// anything was watching it. An observer must not be able to change what it observes.
func (b *Breaker) Open(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	c := b.circuits[key]
	if c == nil || c.openedAt.IsZero() {
		return false
	}
	return b.now().Sub(c.openedAt) < b.cooldown || c.probing
}
