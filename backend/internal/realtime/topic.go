package realtime

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Topic is where a message is published and what a client subscribes to (§4.1, CP26).
//
// Four kinds, and the list is closed on purpose. A topic namespace clients can invent
// entries in is one where a subscription's meaning — and therefore what it is safe to
// deliver on it — is decided by the client.
//
//	patient:{uuid}     everything about one patient
//	station:{uuid}     everything happening at one station
//	queue:{facility}   the traffic board
//	user:{uuid}        messages addressed to one person
//
// A topic is not a permission. Subscribing to `patient:{id}` says "tell me about this
// patient"; whether a given message about that patient may be delivered is decided per
// message, against the subscriber's role, every time (criterion 2).
type Topic string

// TopicKind is the part before the colon.
type TopicKind string

const (
	TopicPatient TopicKind = "patient"
	TopicStation TopicKind = "station"
	TopicQueue   TopicKind = "queue"
	TopicUser    TopicKind = "user"
)

var topicKinds = map[TopicKind]bool{
	TopicPatient: true, TopicStation: true, TopicQueue: true, TopicUser: true,
}

// PatientTopic and the rest are the constructors. Building a topic by string concatenation
// at a call site is how a typo becomes a subscription nobody ever receives anything on.
func PatientTopic(id uuid.UUID) Topic     { return Topic("patient:" + id.String()) }
func StationTopic(id uuid.UUID) Topic     { return Topic("station:" + id.String()) }
func QueueTopic(facility uuid.UUID) Topic { return Topic("queue:" + facility.String()) }
func UserTopic(id uuid.UUID) Topic        { return Topic("user:" + id.String()) }

// Split returns the kind and the identifier.
func (t Topic) Split() (TopicKind, string, bool) {
	kind, id, found := strings.Cut(string(t), ":")
	if !found {
		return "", "", false
	}
	return TopicKind(kind), id, true
}

// Kind is the topic's kind, or "" if it is malformed.
func (t Topic) Kind() TopicKind {
	kind, _, _ := t.Split()
	return kind
}

// ID is the topic's identifier as a UUID.
func (t Topic) ID() (uuid.UUID, bool) {
	_, id, ok := t.Split()
	if !ok {
		return uuid.Nil, false
	}
	parsed, err := uuid.Parse(id)
	return parsed, err == nil
}

// Validate refuses a topic that is not one of the four kinds, or whose identifier is not a
// UUID. Both halves matter: an unknown kind is a client inventing a namespace, and a
// non-UUID identifier is either a bug or an attempt to subscribe to a wildcard.
func (t Topic) Validate() error {
	kind, id, ok := t.Split()
	if !ok {
		return fmt.Errorf("realtime: %q is not a topic; the form is kind:uuid", t)
	}
	if !topicKinds[kind] {
		return fmt.Errorf("realtime: %q is not a topic kind; it is one of patient, station, queue, user", kind)
	}
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("realtime: %q is not a valid identifier for a %s topic", id, kind)
	}
	return nil
}

// MaxTopicsPerConnection bounds a subscription list. A client that subscribes to ten
// thousand topics is either broken or hostile, and either way the memory is the server's.
const MaxTopicsPerConnection = 200
