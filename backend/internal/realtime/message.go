// Package realtime is the WebSocket gateway (CP26, blueprint §4.1): the thing that makes
// "the junior doctor's screen updates instantly — no refresh" true.
//
// # The shape of it
//
//	a write commits  →  the publisher turns the event into a Message
//	                 →  Redis pub/sub, so every instance sees it
//	                 →  each instance fans it out to its own connections
//	                 →  each connection's RBAC filter decides whether that subscriber
//	                    may see it, per message, before it reaches the socket
//
// Four things about that ordering are load-bearing:
//
//   - **After commit only.** A message published from inside a transaction is a message
//     that may describe a write which then rolls back, and there is no un-publishing it.
//   - **RBAC per message, not per subscription.** A subscription is a topic; a topic is not
//     a permission. A nutritionist and a physician may both watch `patient:{id}` and must
//     see different things (criterion 2).
//   - **The socket is a nicety, the pull is the truth.** A dropped connection never loses
//     data because the client reconciles by reading (criterion 4). The resume cursor makes
//     that reconciliation cheap, not correct — it is already correct.
//   - **The gateway never reads the ledger to decide what to send.** It relays what it is
//     given. A gateway that queried would be a second, slower, differently-permissioned
//     read path over clinical data.
package realtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Message is one thing that happened, as a subscriber sees it.
//
// Deliberately not eventstore.Event. The gateway may not import the ledger — the
// architecture forbids it, and the reason is this: a realtime message is a *notification*,
// and a notification that carried a full clinical payload would be a second copy of the
// record travelling over a channel with its own access rules. What travels is enough to
// know that something changed and to fetch it: the topic, the kind, the identifiers, and a
// small, deliberately-chosen summary.
type Message struct {
	// Seq is the ledger's global sequence, and the resume cursor. Monotonic within a
	// topic and across topics, which is what makes "everything after N" answerable.
	Seq int64 `json:"seq"`

	// Topic is where it is published. A message reaches only the sockets subscribed to it.
	Topic Topic `json:"topic"`

	// Kind is what happened: "measurement.recorded", "queue.changed". Dotted, like the
	// audit trail's kinds, and from the same instinct: a name a person can read in a log.
	Kind string `json:"kind"`

	// EventID, PatientID, VisitID identify what to fetch. Never a name, never a value that
	// would be PHI on its own.
	EventID   string `json:"event_id,omitempty"`
	PatientID string `json:"patient_id,omitempty"`
	VisitID   string `json:"visit_id,omitempty"`

	// Requires is the permission a subscriber must hold to receive this message. The
	// filter is a permission check and not a role check, so a message does not have to
	// know which roles exist (criterion 2).
	Requires string `json:"-"`

	// Sensitive marks a message carrying a diagnosis or a clinical interpretation. A
	// blinded role is refused these whatever their permissions say — the same rule the
	// serialiser applies to a response body.
	Sensitive bool `json:"-"`

	// FacilityID scopes the message. A subscriber in another facility never receives it,
	// which is checked before the permission is.
	FacilityID string `json:"-"`

	// Station is where the write happened, when it happened at one. It is what a
	// station-scoped role's reach is measured against: an anthropometry officer receives
	// what was recorded at anthropometry, and a physician — whose reach is the clinic —
	// receives it wherever it was recorded. Empty for a write that belongs to no station,
	// which only a clinic-wide role will then receive.
	Station string `json:"-"`

	// Summary is the small, non-identifying detail a screen can render without a fetch: a
	// station's queue length, a status. Never a measurement value, never a name.
	Summary map[string]any `json:"summary,omitempty"`

	// At is when the write was recorded.
	At time.Time `json:"at"`
}

// Validate refuses a message that would be unsafe or useless to publish.
func (m Message) Validate() error {
	var missing []string
	if m.Seq <= 0 {
		missing = append(missing, "seq")
	}
	if err := m.Topic.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(m.Kind) == "" {
		missing = append(missing, "kind")
	}
	if strings.TrimSpace(m.Requires) == "" {
		// A message nobody needs a permission for is a message everybody receives, which
		// is never what was meant.
		missing = append(missing, "requires")
	}
	if strings.TrimSpace(m.FacilityID) == "" {
		missing = append(missing, "facility_id")
	}
	if len(missing) > 0 {
		return fmt.Errorf("realtime: the message is incomplete: %s", strings.Join(missing, ", "))
	}
	return nil
}

// wire is what actually crosses Redis: the public fields plus the filtering facts, which
// the receiving instance needs and the subscriber must never see.
type wire struct {
	Message
	Requires   string `json:"requires"`
	Sensitive  bool   `json:"sensitive"`
	FacilityID string `json:"facility_id"`
	Station    string `json:"station,omitempty"`
}

func encode(m Message) ([]byte, error) {
	return json.Marshal(wire{
		Message: m, Requires: m.Requires, Sensitive: m.Sensitive,
		FacilityID: m.FacilityID, Station: m.Station,
	})
}

func decode(raw []byte) (Message, error) {
	var w wire
	if err := json.Unmarshal(raw, &w); err != nil {
		return Message{}, err
	}
	m := w.Message
	m.Requires, m.Sensitive, m.FacilityID, m.Station = w.Requires, w.Sensitive, w.FacilityID, w.Station
	return m, nil
}

// Envelope is what a subscriber receives: the frame around a message, or around one of the
// protocol's own replies.
type Envelope struct {
	Type string `json:"type"`
	// Message is set when Type is "message".
	Message *Message `json:"message,omitempty"`
	// Topics is set on a subscribe or unsubscribe acknowledgement.
	Topics []Topic `json:"topics,omitempty"`
	// Cursor is the highest sequence this connection has been sent, on "welcome" and
	// "resumed". A client stores it and offers it on reconnect.
	Cursor int64 `json:"cursor,omitempty"`
	// Error is set when Type is "error": a code the client can branch on and a sentence
	// for the log.
	Error  string `json:"error,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Dropped counts messages this connection missed because it could not keep up. A
	// non-zero value is the client's instruction to reconcile by pull (criterion 4).
	Dropped int64     `json:"dropped,omitempty"`
	At      time.Time `json:"at,omitempty"`
}

// Command is what a subscriber sends.
type Command struct {
	// Type is "subscribe", "unsubscribe", "resume" or "ping".
	Type   string  `json:"type"`
	Topics []Topic `json:"topics,omitempty"`
	// Since is the last sequence the client saw, on "resume".
	Since int64 `json:"since,omitempty"`
}
