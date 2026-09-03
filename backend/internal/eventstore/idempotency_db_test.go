package eventstore_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// CP24 criterion 1, at the layer beneath the HTTP one: a duplicate event_id never creates
// two events, under concurrency.
//
// CP23 proved this for eight identical retries. What this adds is the case the field
// actually produces: a tablet that retried an event and, in between, had its payload
// changed — by an operator correcting a typo before the queue drained, or by a bug. The
// event_id is the identity of the fact, so the retry must be answered with the original,
// not with a second row and not with an error the client has to interpret.

func TestARetriedEventIdIsOneEventWhateverTheBodySays(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	visit := uuid.New()
	id := uuid.Must(uuid.NewV7())

	first, err := h.store.Append(ctx, h.heightWithID(visit, 150, id))
	if err != nil {
		t.Fatal(err)
	}

	// The same event_id, a different height. The ledger keeps what it recorded.
	again, err := h.store.Append(ctx, h.heightWithID(visit, 172, id))
	if err != nil {
		t.Fatal(err)
	}
	if !again.Duplicate {
		t.Fatal("the retry was treated as a new event")
	}
	if again.GlobalSeq != first.GlobalSeq || again.Sequence != first.Sequence {
		t.Errorf("the retry got seq %d/%d, want %d/%d", again.Sequence, again.GlobalSeq, first.Sequence, first.GlobalSeq)
	}
	if !strings.Contains(string(again.Payload), "150") {
		t.Errorf("the retry's payload was written: %s", again.Payload)
	}
	if n, _ := h.store.Count(ctx); n != 1 {
		t.Errorf("%d events for one event_id", n)
	}
	// A correction is how the 172 gets recorded, and it is a separate, visible act (§7.7).
}

// Fifty writers, one event_id, at once: one row, one original, forty-nine duplicates that
// all describe the same row.
func TestOneEventIdSurvivesFiftySimultaneousWriters(t *testing.T) {
	if testing.Short() {
		t.Skip("concurrency test")
	}
	h := newHarness(t)
	ctx := context.Background()
	visit := uuid.New()
	id := uuid.Must(uuid.NewV7())

	const writers = 50
	var (
		wg        sync.WaitGroup
		originals atomic.Int64
		failures  atomic.Int64
	)
	seen := make([]eventstore.Event, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ev, err := h.store.Append(ctx, h.heightWithID(visit, 150, id))
			if err != nil {
				failures.Add(1)
				return
			}
			if !ev.Duplicate {
				originals.Add(1)
			}
			seen[i] = ev
		}()
	}
	wg.Wait()

	if failures.Load() != 0 {
		t.Fatalf("%d writers got an error; a retry must never be an error the client has to interpret", failures.Load())
	}
	if originals.Load() != 1 {
		t.Fatalf("%d of %d writers believed they were first", originals.Load(), writers)
	}
	if n, _ := h.store.Count(ctx); n != 1 {
		t.Fatalf("%d rows for one event_id", n)
	}
	// Everybody was told about the same row — same sequence, same hash.
	for i, ev := range seen {
		if ev.Sequence != seen[0].Sequence || string(ev.Hash) != string(seen[0].Hash) {
			t.Fatalf("writer %d saw a different row: seq %d hash %x", i, ev.Sequence, ev.Hash)
		}
	}
	v, err := h.store.Verify(ctx)
	if err != nil || !v.OK || v.Events != 1 {
		t.Errorf("verify: %+v %v", v, err)
	}
}
