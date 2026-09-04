package visit

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Telling the floor that the floor changed (CP40 criterion 1: the board updates within two
// seconds of a station event).
//
// # Why the interface is declared here
//
// `visit` may not import `realtime` — architecture.json says so, and the reason is worth
// restating: a module that could publish to the gateway could publish *anything* to the
// gateway, and the gateway's whole safety story is that what travels on it is a
// notification and never a record. So the dependency is inverted, the same way CP36
// inverted `consent.Sender`. This module declares what it needs said; the composition root
// is the only place that knows how to say it.
//
// # Why it is fire-and-forget, and why it runs after the commit
//
// A notification published inside a transaction describes a write that may still roll back,
// and there is no un-publishing it. So every call site here runs after `InTransaction`
// returns nil.
//
// And a failure to notify is never a failure to write. CP26's design says it plainly: the
// socket is a nicety, the pull is the truth. A client that misses a message reconciles by
// reading, which it does anyway on reconnect. Returning an error from `QueueChanged` would
// invite a call site to fail a queue write because a Redis connection blipped — the patient
// is standing at the desk either way.
type Notifier interface {
	QueueChanged(ctx context.Context, change QueueChange)
}

// QueueChange is one thing that happened on the floor.
//
// There is no PatientID field, deliberately and for the same reason `BoardEntry` has none:
// the queue topic is what the wall display subscribes to, and a patient id on that channel
// is a join key handed to a machine in a public waiting area. What a board needs is "this
// station changed, refetch"; what it gets is exactly that.
type QueueChange struct {
	FacilityID  uuid.UUID
	VisitID     uuid.UUID
	EntryID     uuid.UUID
	StationCode string
	Status      QueueStatus
	// Kind is the dotted name the gateway publishes: queue.entered, queue.called,
	// queue.started, queue.left, queue.rerouted.
	Kind string
	At   time.Time
	// Waiting is the station's depth after the change, so a board can redraw one column
	// without a round trip. A count is not clinical and it is what the screen is showing.
	Waiting int
}

// The kinds. Named constants rather than literals at the call sites, because a typo in a
// message kind is a subscriber filter that silently never matches.
const (
	KindQueueEntered  = "queue.entered"
	KindQueueCalled   = "queue.called"
	KindQueueStarted  = "queue.started"
	KindQueueLeft     = "queue.left"
	KindQueueRerouted = "queue.rerouted"
)

// nopNotifier is the default. A service constructed without a gateway — every database
// test, the synthetic generator, a one-off script — does the write and says nothing, rather
// than needing a nil check at each of the five call sites.
type nopNotifier struct{}

func (nopNotifier) QueueChanged(context.Context, QueueChange) {}

// Notify attaches a gateway. Returns the service so the composition root can write
// `visit.NewService(...).Notify(bridge)`.
func (s *Service) Notify(n Notifier) *Service {
	if n != nil {
		s.notifier = n
	}
	return s
}

// announce is the one call site shape, so that "after the commit, never inside it" is a
// property of this file rather than a thing five call sites each remember.
func (s *Service) announce(ctx context.Context, facility uuid.UUID, entry QueueEntry, kind string) {
	waiting := 0
	if depth, err := s.store.q.StationDepth(ctx, dbgen.StationDepthParams{
		FacilityID: facility, StationCode: entry.StationCode,
	}); err == nil {
		waiting = int(depth)
	}
	s.notifier.QueueChanged(ctx, QueueChange{
		FacilityID: facility, VisitID: entry.VisitID, EntryID: entry.ID,
		StationCode: entry.StationCode, Status: entry.Status,
		Kind: kind, At: s.clock.Now().UTC(), Waiting: waiting,
	})
}
