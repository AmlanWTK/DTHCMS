// Package eventstore is the clinical event ledger (CP23, blueprint §7): the single write
// path for everything clinical, the append-only table behind it, the hash chain that
// makes "append-only" tamper-evident, and the registry that says which events exist and
// what their payloads look like.
//
// Every observation, diagnosis, prescription and correction is an Envelope handed to
// Append. The store assigns the sequence and the global sequence, computes the hash,
// writes the row, and returns the Event as written. Nothing is ever updated; a correction
// is a new event that names the one it corrects (§7.7). Projections (CP25) read the rows
// in global order and derive everything a screen shows.
//
// The module depends on platform only. Who is allowed to append what is the RBAC engine's
// decision (CP20), made before an envelope reaches here; that an envelope is complete is
// this module's, and an incomplete one is refused.
package eventstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Source says how an event reached the server (§7.2).
type Source string

const (
	SourceMobileOnline      Source = "MOBILE_ONLINE"
	SourceMobileOfflineSync Source = "MOBILE_OFFLINE_SYNC"
	SourceWeb               Source = "WEB"
	SourceOCR               Source = "OCR"
	SourceField             Source = "FIELD"
	SourceSystem            Source = "SYSTEM"
)

var knownSources = map[Source]bool{
	SourceMobileOnline: true, SourceMobileOfflineSync: true, SourceWeb: true,
	SourceOCR: true, SourceField: true, SourceSystem: true,
}

// Correction is what a correcting event carries (§7.7): what it corrects and why, with a
// structured code and a free-text reason. The database CHECK agrees.
type Correction struct {
	CorrectsEventID uuid.UUID `json:"corrects_event_id"`
	ReasonCode      string    `json:"reason_code"`
	ReasonText      string    `json:"reason_text"`
}

// Envelope is an event as the caller describes it. The store adds the sequence, the
// global sequence, the recorded time and the chain.
type Envelope struct {
	// EventID is client-generated, UUIDv7, and is the idempotency key: the same envelope
	// appended twice is stored once and the second call returns the first row.
	EventID uuid.UUID

	AggregateType string
	AggregateID   uuid.UUID
	PatientID     *uuid.UUID
	VisitID       *uuid.UUID

	EventType    string
	EventVersion int

	// OccurredAt is when it happened by the client's clock — clinically meaningful and
	// kept apart from RecordedAt, which the server assigns (§7.2).
	OccurredAt time.Time

	Actor  Actor
	Source Source

	// Payload is the event's own content, validated against the registry's schema for
	// EventType and EventVersion. Previous is the value being corrected; Correction says
	// why. Metadata is the client's context: app version, correlation id, offline
	// queueing, client time zone.
	Payload    json.RawMessage
	Previous   json.RawMessage
	Correction *Correction
	Metadata   map[string]any

	// ExpectedSequence, when set, is optimistic concurrency (§7.9): the append succeeds
	// only if the aggregate's head is exactly this. Zero means "no expectation".
	ExpectedSequence int64
}

// Event is an Envelope once it is in the ledger.
type Event struct {
	Envelope
	GlobalSeq  int64
	Sequence   int64
	RecordedAt time.Time
	PrevHash   []byte
	Hash       []byte
	// Duplicate is true when Append found the event already there and returned it rather
	// than writing again (§7.5).
	Duplicate bool
}

var (
	ErrIncomplete       = errors.New("eventstore: the envelope is incomplete")
	ErrUnknownEventType = errors.New("eventstore: unknown event type or version")
	ErrInvalidPayload   = errors.New("eventstore: the payload does not match its schema")
	ErrSequenceConflict = errors.New("eventstore: the aggregate has moved on")
	ErrNotFound         = errors.New("eventstore: not found")
)

var (
	aggregatePattern = regexp.MustCompile(`^[A-Z][A-Z_]+$`)
	eventTypePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)
)

// Validate is criterion 5: every field of the attribution envelope is present, or the
// append is refused before the database sees it. The database refuses it again.
func (e Envelope) Validate() error {
	var missing []string
	if e.EventID == uuid.Nil {
		missing = append(missing, "event_id")
	}
	if !aggregatePattern.MatchString(e.AggregateType) {
		missing = append(missing, "aggregate_type")
	}
	if e.AggregateID == uuid.Nil {
		missing = append(missing, "aggregate_id")
	}
	if !eventTypePattern.MatchString(e.EventType) {
		missing = append(missing, "event_type")
	}
	if e.EventVersion < 1 {
		missing = append(missing, "event_version")
	}
	if e.OccurredAt.IsZero() {
		missing = append(missing, "occurred_at")
	}
	if e.Actor.userID == uuid.Nil {
		missing = append(missing, "actor.user_id")
	}
	if e.Actor.deviceID == uuid.Nil {
		missing = append(missing, "actor.device_id")
	}
	if strings.TrimSpace(e.Actor.role) == "" {
		missing = append(missing, "actor.role")
	}
	if e.Actor.facilityID == uuid.Nil {
		missing = append(missing, "actor.facility_id")
	}
	if !knownSources[e.Source] {
		missing = append(missing, "source")
	}
	if len(e.Payload) == 0 || !json.Valid(e.Payload) {
		missing = append(missing, "payload")
	}
	if e.Correction != nil {
		if e.Correction.CorrectsEventID == uuid.Nil || strings.TrimSpace(e.Correction.ReasonCode) == "" || strings.TrimSpace(e.Correction.ReasonText) == "" {
			missing = append(missing, "correction")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrIncomplete, strings.Join(missing, ", "))
	}
	return nil
}
