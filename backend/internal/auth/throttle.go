package auth

import (
	"fmt"
	"time"
)

/*
 * Progressive delay on failed logins.
 *
 * Two things this deliberately is not.
 *
 * It is not a lockout. Locking an account after N failures hands anybody who knows an
 * employee code — which is printed on a roster and spoken across a clinic floor — the
 * ability to keep a doctor out of the system on the morning they need it. A delay costs an
 * attacker their throughput without costing the real user their job.
 *
 * It is not conditional on the account existing. The delay is computed from attempts against
 * the code that was typed, whether or not any user has it. A throttle that only counted real
 * accounts would answer "does this person work here" by how fast it refuses.
 */

// ThrottlePolicy is how quickly the refusals slow down.
type ThrottlePolicy struct {
	// Free is how many failures pass without any delay. Someone mistyping a password twice
	// is a Tuesday, not an attack.
	Free int
	// Base is the delay after the first non-free failure; it doubles from there.
	Base time.Duration
	// Max caps it, because an unbounded delay is a lockout wearing a different hat, and
	// holds a request open long enough to become its own denial of service.
	Max time.Duration
	// Window is how far back failures count. Old failures stop mattering, or a busy Monday
	// three weeks ago still slows down a login today.
	Window time.Duration
}

// DefaultThrottle: two free attempts, then 1s doubling to 30s, counting the last 15 minutes.
func DefaultThrottle() ThrottlePolicy {
	return ThrottlePolicy{
		Free:   2,
		Base:   time.Second,
		Max:    30 * time.Second,
		Window: 15 * time.Minute,
	}
}

// Delay is how long to wait before answering, given recent failures.
//
// Applied before the answer rather than after: waiting first means the attacker cannot use
// the response time to distinguish a wrong password from an unknown account, because both
// arrive at the same moment.
func (p ThrottlePolicy) Delay(recentFailures int) time.Duration {
	if recentFailures <= p.Free {
		return 0
	}

	delay := p.Base
	for i := p.Free + 1; i < recentFailures; i++ {
		delay *= 2
		if delay >= p.Max {
			return p.Max
		}
	}
	if delay > p.Max {
		return p.Max
	}
	return delay
}

// Since is the earliest attempt that still counts, given the clock.
func (p ThrottlePolicy) Since(now time.Time) time.Time { return now.Add(-p.Window) }

// ErrThrottled is returned when a caller is being asked to slow down.
//
// It is never shown to the person logging in. The response to every failed login is the
// same, whatever the cause — the retry hint would otherwise confirm that the account exists
// and is worth attacking. This is for the log and for the administrator reading it.
type ErrThrottled struct {
	Delay    time.Duration
	Failures int
}

func (e *ErrThrottled) Error() string {
	return fmt.Sprintf("throttled after %d recent failures; delayed %s", e.Failures, e.Delay)
}
