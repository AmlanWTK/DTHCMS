// Package clock makes time injectable, so that behaviour depending on it can be tested
// without sleeping and without flaking.
//
// DTHCMS cares about this more than most systems: events carry both when a measurement
// happened and when the server recorded it, and correction workflows reason about the
// order of the two.
package clock

import "time"

// Clock reports the current time.
type Clock interface {
	Now() time.Time
}

// Real is the production clock. Times are UTC: storage is always UTC, and rendering in
// Asia/Dhaka happens at the presentation edge.
type Real struct{}

// Now returns the current UTC time.
func (Real) Now() time.Time { return time.Now().UTC() }

// Fixed is a test clock that does not move unless told to.
type Fixed struct{ Current time.Time }

// NewFixed returns a clock stopped at t.
func NewFixed(t time.Time) *Fixed { return &Fixed{Current: t.UTC()} }

// Now returns the fixed time.
func (f *Fixed) Now() time.Time { return f.Current }

// Advance moves the fixed clock forward.
func (f *Fixed) Advance(d time.Duration) { f.Current = f.Current.Add(d) }
