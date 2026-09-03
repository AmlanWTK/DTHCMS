package eventstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Verification is the outcome of walking the ledger.
type Verification struct {
	OK         bool
	Aggregates int64
	Events     int64
	// Where it broke, when !OK.
	BrokenAggregateType string
	BrokenAggregateID   uuid.UUID
	BrokenSequence      int64
	Problem             string
	// Anchors checked and the first day that disagreed, when any.
	Anchors   int64
	BrokenDay string
	// Strays is the number of rows in the default partition.
	Strays int64
}

// Verify walks every aggregate's chain from its first event and recomputes every hash,
// then re-folds every daily anchor. Linear in the ledger, by design; the nightly job runs
// it over the previous day's partition and the monthly one over a sample of history
// (§7.10), both through the same code.
func (s *Store) Verify(ctx context.Context) (Verification, error) {
	var out Verification
	afterType, afterID := "", uuid.Nil
	for {
		page, err := s.q.Aggregates(ctx, dbgen.AggregatesParams{Limit: 200, AfterType: afterType, AfterID: afterID})
		if err != nil {
			return Verification{}, err
		}
		if len(page) == 0 {
			break
		}
		for _, agg := range page {
			n, problem, at, err := s.verifyAggregate(ctx, agg.AggregateType, agg.AggregateID, agg.HeadSequence)
			if err != nil {
				return Verification{}, err
			}
			out.Events += n
			out.Aggregates++
			if problem != "" {
				out.BrokenAggregateType, out.BrokenAggregateID, out.BrokenSequence, out.Problem = agg.AggregateType, agg.AggregateID, at, problem
				return out, nil
			}
		}
		last := page[len(page)-1]
		afterType, afterID = last.AggregateType, last.AggregateID
	}

	anchors, err := s.q.Anchors(ctx)
	if err != nil {
		return Verification{}, err
	}
	prev := Genesis
	for _, a := range anchors {
		day := a.Day.Format("2006-01-02")
		if string(a.PrevAnchor) != string(prev) {
			out.BrokenDay, out.Problem = day, fmt.Sprintf("the anchor for %s does not link to the day before", day)
			return out, nil
		}
		recomputed, count, err := s.foldDay(ctx, a.Day, prev)
		if err != nil {
			return Verification{}, err
		}
		if count != a.EventCount || string(recomputed) != string(a.Anchor) {
			out.BrokenDay, out.Problem = day, fmt.Sprintf("the anchor for %s does not match its events (%d then, %d now)", day, a.EventCount, count)
			return out, nil
		}
		prev = a.Anchor
		out.Anchors++
	}

	strays, err := s.q.EventDefaultPartitionCount(ctx)
	if err != nil {
		return Verification{}, err
	}
	out.Strays = strays
	out.OK = true
	return out, nil
}

// verifyAggregate walks one chain. Returns the number of events checked, a problem or
// "", and the sequence the problem is at.
func (s *Store) verifyAggregate(ctx context.Context, aggregateType string, aggregateID uuid.UUID, head int64) (int64, string, int64, error) {
	const slice = 500
	prev := Genesis
	expected := int64(1)
	var checked int64
	for {
		events, err := s.Stream(ctx, aggregateType, aggregateID, expected, slice)
		if err != nil {
			return checked, "", 0, err
		}
		if len(events) == 0 {
			break
		}
		for _, ev := range events {
			if ev.Sequence != expected {
				return checked, fmt.Sprintf("sequence %d is missing; the next row is %d", expected, ev.Sequence), expected, nil
			}
			if string(ev.PrevHash) != string(prev) {
				return checked, fmt.Sprintf("sequence %d does not link to %d", ev.Sequence, ev.Sequence-1), ev.Sequence, nil
			}
			if string(hashOf(ev.PrevHash, ev.Sequence, ev.GlobalSeq, ev.RecordedAt, ev.Envelope)) != string(ev.Hash) {
				return checked, fmt.Sprintf("sequence %d does not hash to what it claims", ev.Sequence), ev.Sequence, nil
			}
			prev = ev.Hash
			expected = ev.Sequence + 1
			checked++
		}
		if len(events) < slice {
			break
		}
	}
	if checked != head {
		return checked, fmt.Sprintf("the key table says %d events, the ledger has %d", head, checked), head, nil
	}
	return checked, "", 0, nil
}

// --- the daily anchor ---

// Anchor is one day's fold.
type Anchor struct {
	Day        time.Time
	EventCount int64
	FirstSeq   *int64
	LastSeq    *int64
	PrevAnchor []byte
	Anchor     []byte
	ComputedAt time.Time
}

// ErrAnchorExists means the day was anchored already; anchors are never rewritten.
var ErrAnchorExists = errors.New("eventstore: the day is already anchored")

// AnchorDay folds a clinic day's events (in Asia/Dhaka, since the day is the clinic's)
// onto the previous anchor and writes the result. Called by the nightly job for
// yesterday; calling it twice for one day is refused.
func (s *Store) AnchorDay(ctx context.Context, facilityID uuid.UUID, day time.Time) (Anchor, error) {
	day = dayOf(day)
	if _, err := s.q.AnchorForDay(ctx, day); err == nil {
		return Anchor{}, ErrAnchorExists
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Anchor{}, err
	}
	prev := Genesis
	if last, err := s.q.LatestAnchorBefore(ctx, day); err == nil {
		prev = last.Anchor
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Anchor{}, err
	}
	anchor, count, err := s.foldDay(ctx, day, prev)
	if err != nil {
		return Anchor{}, err
	}
	first, last, err := s.dayBounds(ctx, day)
	if err != nil {
		return Anchor{}, err
	}
	row, err := s.q.InsertAnchor(ctx, dbgen.InsertAnchorParams{
		Day: day, FacilityID: facilityID, EventCount: count, FirstGlobalSeq: first, LastGlobalSeq: last,
		PrevAnchor: prev, Anchor: anchor, ComputedAt: s.clock.Now(),
	})
	if err != nil {
		return Anchor{}, err
	}
	return Anchor{
		Day: row.Day, EventCount: row.EventCount, FirstSeq: row.FirstGlobalSeq, LastSeq: row.LastGlobalSeq,
		PrevAnchor: row.PrevAnchor, Anchor: row.Anchor, ComputedAt: row.ComputedAt,
	}, nil
}

// Dhaka is the clinic's clock; a "day" of events is a Dhaka day.
var Dhaka = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Dhaka")
	if err != nil {
		return time.FixedZone("BDT", 6*3600)
	}
	return loc
}()

func dayOf(t time.Time) time.Time {
	local := t.In(Dhaka)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

func dayRange(day time.Time) (time.Time, time.Time) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, Dhaka)
	return start, start.AddDate(0, 0, 1)
}

func (s *Store) foldDay(ctx context.Context, day time.Time, prev []byte) ([]byte, int64, error) {
	start, end := dayRange(day)
	rows, err := s.q.HashesForDay(ctx, dbgen.HashesForDayParams{RecordedAt: start, RecordedAt_2: end})
	if err != nil {
		return nil, 0, err
	}
	hashes := make([][]byte, 0, len(rows))
	for _, r := range rows {
		hashes = append(hashes, r.Hash)
	}
	return anchorOf(prev, day.Format("2006-01-02"), hashes), int64(len(rows)), nil
}

func (s *Store) dayBounds(ctx context.Context, day time.Time) (*int64, *int64, error) {
	start, end := dayRange(day)
	rows, err := s.q.HashesForDay(ctx, dbgen.HashesForDayParams{RecordedAt: start, RecordedAt_2: end})
	if err != nil || len(rows) == 0 {
		return nil, nil, err
	}
	first, last := rows[0].GlobalSeq, rows[len(rows)-1].GlobalSeq
	return &first, &last, nil
}
