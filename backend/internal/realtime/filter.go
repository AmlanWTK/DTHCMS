package realtime

import (
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The fan-out filter (CP26 criterion 2): a subscriber never receives data their role
// cannot read, proven by test.
//
// This is the checkpoint's whole security question. A topic is a routing key, not a
// permission — a nutritionist and a physician may both be watching the same patient — so
// the decision is made per message, per connection, immediately before the bytes reach the
// socket. It is the same engine that decides an HTTP request (CP20): one set of rules,
// asked twice, rather than two sets that drift.

// Filter decides whether one subscriber may receive one message.
type Filter interface {
	Allow(subject rbac.Subject, m Message) bool
}

// RBACFilter is the engine as a Filter.
type RBACFilter struct{}

var _ Filter = RBACFilter{}

// Allow asks rbac.Can with the message described as a resource.
//
// Three facts make the resource: the facility (a subscriber never sees another facility's
// traffic), sensitivity (a blinded role is refused a diagnosis whatever its permissions
// say), and the station where the topic is a station's (a station-scoped role sees its own
// station). The permission asked for is the message's own `Requires`, so a new kind of
// message states its own requirement rather than being added to a list here.
func (RBACFilter) Allow(subject rbac.Subject, m Message) bool {
	facility, err := uuid.Parse(m.FacilityID)
	if err != nil {
		// A message with no facility is not routable to anybody. Fail closed.
		return false
	}
	if subject.FacilityID != facility {
		return false
	}

	resource := rbac.Resource{
		Kind:       string(m.Topic.Kind()),
		FacilityID: facility,
		Sensitive:  m.Sensitive,
	}
	// Where the write happened. A station-scoped role — anthropometry, vitals, the
	// pharmacy — reaches what was recorded at the station it is working, and the message
	// is the only thing that knows which that was. A station topic names it directly; every
	// other topic carries it on the message.
	if station, err := uuid.Parse(m.Station); err == nil {
		resource.StationID = &station
	}
	if m.Topic.Kind() == TopicStation {
		if id, ok := m.Topic.ID(); ok {
			resource.StationID = &id
		}
	}

	return rbac.Can(subject, m.Requires, resource).Allowed
}

// maySubscribe decides whether a subject may open a subscription at all.
//
// It is a coarse check on top of the per-message one, and both are needed. Per-message
// filtering already makes a wrong subscription harmless — nothing would be delivered — but
// a subscription is also an observable: a `user:{someone else}` subscription that silently
// delivered nothing would still let a client enumerate which users exist by watching for
// the ones that error.
//
// So: a user topic is your own. A patient, station or queue topic is your facility's, and
// needs the permission to read anything about that kind of thing at all.
func maySubscribe(subject rbac.Subject, facility uuid.UUID, t Topic) bool {
	kind, _, ok := t.Split()
	if !ok {
		return false
	}
	if subject.FacilityID != facility {
		return false
	}
	switch kind {
	case TopicUser:
		id, ok := t.ID()
		return ok && id == subject.UserID
	case TopicQueue:
		// The traffic board's own topic, and the only one the wall display subscribes to.
		// It asks for `board.read` rather than a clinical permission precisely so that the
		// machine bolted to the wall can hold that one permission and nothing else — a
		// display that had to hold `patient.read.demographics` to watch a queue would be a
		// display that could pull a patient's demographics (CP40).
		return rbac.Holds(subject, permBoardRead)
	case TopicPatient, TopicStation:
		// rbac.Holds and not rbac.Can, deliberately: a subscription names no resource, so
		// there is no station to measure a station-scoped role's reach against. Scope is
		// enforced on every message instead, where the station is known. What this check
		// is for is coarser and still worth having — somebody with no clinical read
		// permission at all has no business opening a clinical subscription.
		return rbac.Holds(subject, permPatientRead)
	default:
		return false
	}
}

// permPatientRead is what the subscription check asks for. Named here as a string rather
// than imported, because realtime may not import auth; rbac may, and does, and the
// catalogue's strings are the contract between them. `TestTheSubscriptionPermissionIsInTheCatalogue`
// keeps the two in step.
//
// Everyone who works a clinic station holds it, which is the right coarseness for
// *subscribing*: what a subscriber may actually receive is decided per message.
const permPatientRead = "patient.read.demographics"

// permBoardRead is the traffic board's. CP26 left a note here saying the queue topic would
// get a permission of its own at CP40; this is it. The station topic deliberately did not
// move with it — a station feed carries what was recorded at that station, which is
// clinical, and the wall display has no business subscribing to one.
const permBoardRead = "board.read"
