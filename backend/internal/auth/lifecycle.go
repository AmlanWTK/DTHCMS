package auth

import "fmt"

// The user lifecycle.
//
//	invited ──▶ active ──▶ suspended ──▶ active
//	   │           │            │
//	   └───────────┴────────────┴──────▶ deactivated ──▶ active
//
// Deactivated is not terminal, deliberately. Staff leave and come back, and re-inviting
// them would create a second user row — which splits one person's history in two and
// defeats the reason users are never deleted in the first place.
//
// This table is the same one core.assert_user_status_transition enforces.
// TestLifecycleMatchesTheDatabase walks all sixteen ordered pairs against a real database
// and fails if the two ever disagree, so the duplication cannot rot quietly.
var transitions = map[Status][]Status{
	StatusInvited:     {StatusActive, StatusDeactivated},
	StatusActive:      {StatusSuspended, StatusDeactivated},
	StatusSuspended:   {StatusActive, StatusDeactivated},
	StatusDeactivated: {StatusActive},
}

// AllStatuses is every lifecycle state, for exhaustive tests and for a status filter.
var AllStatuses = []Status{StatusInvited, StatusActive, StatusSuspended, StatusDeactivated}

// CanTransition reports whether a user may move from one status to another.
//
// A move to the status the user already holds is permitted and does nothing — an
// administrator suspending an already-suspended account has not made a mistake worth an
// error message.
func CanTransition(from, to Status) bool {
	if from == to {
		return true
	}
	for _, allowed := range transitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// AllowedTransitions lists where a user in this status may go. Ordered, so an interface
// offering the choices does not reorder them between renders.
func AllowedTransitions(from Status) []Status {
	out := make([]Status, len(transitions[from]))
	copy(out, transitions[from])
	return out
}

// ErrTransition explains a refused lifecycle move.
//
// It names both states and what was possible instead, because "invalid status transition"
// tells the administrator reading it nothing about what to do next.
type ErrTransition struct {
	From, To Status
	Allowed  []Status
}

func (e *ErrTransition) Error() string {
	if len(e.Allowed) == 0 {
		return fmt.Sprintf("a %s user cannot change status", e.From)
	}
	return fmt.Sprintf("cannot move a user from %s to %s; from %s the permitted moves are %v",
		e.From, e.To, e.From, e.Allowed)
}

// Transition validates a lifecycle move and returns the reason it is refused.
func Transition(from, to Status) error {
	if !isKnownStatus(to) {
		return fmt.Errorf("unknown status %q; the lifecycle has %v", to, AllStatuses)
	}
	if !CanTransition(from, to) {
		return &ErrTransition{From: from, To: to, Allowed: AllowedTransitions(from)}
	}
	return nil
}

func isKnownStatus(s Status) bool {
	for _, known := range AllStatuses {
		if known == s {
			return true
		}
	}
	return false
}

// RequiresReason reports whether a transition must carry a stated reason.
//
// Suspension does. A suspension nobody explained is the one that gets disputed six months
// later, by which time nobody remembers. The database enforces the same rule with a CHECK.
func RequiresReason(to Status) bool { return to == StatusSuspended }
