package audit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
)

// Recorder appends entries to the chain.
//
// One instance for the process. Every caller that has something to say — the admin
// console, the session service, the break-glass handler, the exporter — hands it an Entry
// and gets back the Event as written. A failure to record is returned to the caller, who
// decides what it means: the console logs it and lets the act stand (the act has its own
// database traces); the break-glass handler refuses the access, because an emergency
// access nobody can review is exactly what must not exist.
type Recorder struct {
	store  Store
	clock  clock.Clock
	logger *slog.Logger
}

func NewRecorder(store Store, clk clock.Clock, logger *slog.Logger) *Recorder {
	if clk == nil {
		clk = clock.Real{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Recorder{store: store, clock: clk, logger: logger}
}

// Record appends one entry. The kind must be registered; the time defaults to now.
func (r *Recorder) Record(ctx context.Context, e Entry) (Event, error) {
	if !Known(e.Kind) {
		return Event{}, fmt.Errorf("%w: %q", ErrUnknownKind, e.Kind)
	}
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	at := e.At
	if at.IsZero() {
		at = r.clock.Now()
	}
	// Truncated to the microsecond the database keeps, so the hash is computed over the
	// time that will be read back, not one with nanoseconds the column drops.
	at = at.UTC().Truncate(time.Microsecond)
	e.At = at
	ev, err := r.store.Append(ctx, e, hashOf, at)
	if err != nil {
		r.logger.ErrorContext(ctx, "audit entry not recorded", "kind", e.Kind, "error", err)
		return Event{}, fmt.Errorf("recording %s: %w", e.Kind, err)
	}
	return ev, nil
}

// Verification is the result of walking the whole chain.
type Verification struct {
	OK      bool
	Checked int64
	HeadSeq int64
	// BrokenAt is the first sequence whose hash or link did not agree, when !OK.
	BrokenAt int64
	Problem  string
	// Strays is the number of rows in the default partition.
	Strays int64
}

// Verify walks the chain from the first row and recomputes every hash. Linear in the size
// of the log, by design: a verifier that sampled would be a verifier that could be fooled.
// Run from the console on demand and from the ops job nightly.
func (r *Recorder) Verify(ctx context.Context) (Verification, error) {
	const slice = 500
	var (
		prev     = Genesis
		expected = int64(1)
		result   Verification
	)
	for {
		events, err := r.store.Walk(ctx, expected, slice)
		if err != nil {
			return Verification{}, err
		}
		if len(events) == 0 {
			break
		}
		for _, ev := range events {
			if ev.Seq != expected {
				return broken(result, expected, fmt.Sprintf("sequence %d is missing; the next row is %d", expected, ev.Seq)), nil
			}
			if string(ev.PrevHash) != string(prev) {
				return broken(result, ev.Seq, fmt.Sprintf("row %d does not link to row %d", ev.Seq, ev.Seq-1)), nil
			}
			if string(hashOf(ev.PrevHash, ev.Seq, ev.RecordedAt, ev.Entry)) != string(ev.Hash) {
				return broken(result, ev.Seq, fmt.Sprintf("row %d does not hash to what it claims", ev.Seq)), nil
			}
			prev = ev.Hash
			expected = ev.Seq + 1
			result.Checked++
			result.HeadSeq = ev.Seq
		}
		if len(events) < slice {
			break
		}
	}
	strays, err := r.store.Strays(ctx)
	if err != nil {
		return Verification{}, err
	}
	result.Strays = strays
	result.OK = true
	return result, nil
}

func broken(v Verification, at int64, problem string) Verification {
	v.OK = false
	v.BrokenAt = at
	v.Problem = problem
	return v
}
