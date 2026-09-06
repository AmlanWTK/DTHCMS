// Package offline is the sync protocol's server side (CP65, §15.2, §7.2).
//
// # Why the package is not called `sync`
//
// The standard library owns that name, and a package that shadows it inside a codebase full of
// concurrency is a package somebody will alias badly at three in the morning. `offline` is also
// the more honest name: this is not general-purpose synchronisation, it is the half of the offline
// guarantee that runs on the server.
//
// # What the event store already gives, and what is left
//
// Two of the five acceptance criteria hold before a line of this package exists, and it matters to
// say which so that nothing here reimplements them:
//
//   - **"Duplicate submission produces no duplicate events."** `EventID` is client-generated and is
//     the ledger's key; `eventstore.Append` returns the first row with `Duplicate` set. A batch
//     replayed in full lands as forty duplicates and no new rows.
//   - **"`occurred_at` is preserved; `recorded_at` reflects arrival."** The envelope has carried
//     both since CP23, for exactly this checkpoint.
//
// What is left is the batch, and three decisions in it.
//
// # One transaction per event, not per batch
//
// Criterion 1 — *a bad event never blocks the rest of its batch* — is not achievable with a batch
// transaction, and the tempting middle ground (savepoints) buys nothing here: a rejected event has
// nothing to roll back, because the reason it was rejected is that it never got written.
//
// So each event is appended on its own. The cost is that a batch is not atomic, and the honest
// consequence is stated rather than hidden: a client whose connection drops halfway has some of its
// events in the ledger and some not. That is exactly the case `EventID` idempotency exists for —
// resend the whole batch and the landed ones come back `DUPLICATE`.
//
// # Ordering, and what "preserved" can honestly mean
//
// Criterion 4 asks that per-aggregate ordering be preserved. Events are therefore applied in the
// order the client sent them, and events for one aggregate are applied **serially**.
//
// The interesting case is the failure. If the third of forty observations for one patient is
// rejected, blocking the remaining thirty-seven would hold a morning's work hostage to one typo —
// and they do not depend on it: an observation is a fact about a moment, not a mutation of a state.
// So they proceed.
//
// The exception is an event that *declares* a dependency, by carrying `ExpectedSequence` (§7.9).
// That event is saying "apply me only if the aggregate is where I think it is", and after an
// earlier failure it no longer is. Attempting it would produce a second failure that says something
// misleading — a sequence conflict rather than "the thing before this one did not land" — so it is
// reported `BLOCKED` without being attempted, and the client sends it again after resolving the
// first.
//
// # The revoked device
//
// See `docs/sync.md` and the migration. Held, in full, for a person to decide about: accepting
// defeats revocation, dropping loses a morning of measurements silently, and there is no third
// automatic answer that is not one of those two wearing a disguise.
package offline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Errors this package returns.
var (
	// ErrUnknownDevice is a device id that is not enrolled here.
	ErrUnknownDevice = errors.New("offline: unknown device")
	// ErrWrongFacility is a device enrolled at another clinic. Answered as not-found upstream:
	// whether a device belongs to this facility is itself something an error would disclose.
	ErrWrongFacility = errors.New("offline: that device belongs to another facility")
	// ErrBatchTooLarge is a batch above the limit. Refused rather than truncated — a client told
	// "accepted" for forty of five hundred events and left to work out which is a client that
	// will drop the rest.
	ErrBatchTooLarge = errors.New("offline: too many events in one batch")
	// ErrEmptyBatch is a push with nothing in it.
	ErrEmptyBatch = errors.New("offline: a batch with no events is not a batch")
	// ErrNotFound is a quarantined event that is not there.
	ErrNotFound = errors.New("offline: not found")
	// ErrAlreadyResolved is a release or discard of something somebody already decided about.
	ErrAlreadyResolved = errors.New("offline: that event has already been released or discarded")
	// ErrNotPullable is an event type nobody has decided may be synchronised to a device.
	ErrNotPullable = errors.New("offline: that event type is not pullable")
	// ErrDeviceMismatch is a batch whose body names a device other than the one that signed it.
	ErrDeviceMismatch = errors.New("offline: this batch names a device other than the one that signed it")
	// ErrQuarantineFull is a refused device whose held events have reached the per-device cap
	// (migration 00050). The whole batch is turned away without a row being written, because
	// every event in it would have been held and there is no room for any of them. A device that
	// is *not* refused is never turned away like this: its valid events still land, and only the
	// ones that would have needed a hold come back BLOCKED.
	ErrQuarantineFull = errors.New("offline: this device has as many events awaiting review as it may have")
)

// MaxBatch is how many events one push may carry.
//
// Five hundred, which is the number the checkpoint's own test names and roughly a full clinic day
// for one station. The plan lists the limit as tunable by measurement; what matters more than the
// number is that exceeding it is a **refusal** rather than a truncation, so a client is never told
// "accepted" for a prefix and left to work out which.
const MaxBatch = 500

// checkViolation is PostgreSQL's SQLSTATE for a failed CHECK or a trigger that raised with it.
// Matched by code rather than by message, which keeps this working on a non-English server and
// through any rewording of the sentence the trigger raises.
const checkViolation = "23514"

// ClockTolerance is how far ahead of the server a device's clock may be before its events are held.
//
// Five minutes. Behind is unbounded and always fine — a device offline for a week is the entire
// point of this checkpoint. Ahead is the dangerous direction: an observation dated next Tuesday
// sorts to the top of every timeline forever, and nothing downstream distinguishes it from a real
// future-dated record because there is no such thing.
const ClockTolerance = 5 * time.Minute

// Outcome is what happened to one event.
type Outcome string

const (
	// Accepted: it is in the ledger now.
	Accepted Outcome = "ACCEPTED"
	// Duplicate: it was already there. Not an error — it is what a resent batch looks like, and
	// a client that treated it as one would refuse to make progress after any lost response.
	Duplicate Outcome = "DUPLICATE"
	// Rejected: it will never be accepted as sent.
	Rejected Outcome = "REJECTED"
	// Quarantined: held for a person to decide about.
	Quarantined Outcome = "QUARANTINED"
	// Blocked: an earlier event for the same aggregate failed and this one declared it depends
	// on that aggregate's state. Not attempted, because attempting it would produce a misleading
	// second failure. The client sends it again.
	Blocked Outcome = "BLOCKED"
)

// Reason codes, which are what a client branches on. The sentence beside each is what a person
// reads; neither is a substitute for the other.
const (
	ReasonDeviceRevoked    = "DEVICE_REVOKED"
	ReasonDeviceSuspended  = "DEVICE_SUSPENDED"
	ReasonClockImplausible = "CLOCK_IMPLAUSIBLE"
	ReasonUnknownType      = "UNKNOWN_EVENT_TYPE"
	ReasonInvalidPayload   = "INVALID_PAYLOAD"
	ReasonSequenceConflict = "SEQUENCE_CONFLICT"
	ReasonEarlierFailed    = "EARLIER_EVENT_FAILED"
	ReasonRefused          = "REFUSED"
	// ReasonQuarantineFull: this device has reached the cap on events awaiting review
	// (migration 00050). Paired with BLOCKED, never REJECTED — the event is real and the client
	// should keep it and come back, because what is exhausted is a supervisor's attention, not
	// the event's validity. Rejecting would make a conforming client drop a blood pressure
	// because somebody is behind on their reading.
	ReasonQuarantineFull = "QUARANTINE_FULL"
)

// Result is one event's answer.
type Result struct {
	EventID uuid.UUID `json:"event_id"`
	Outcome Outcome   `json:"outcome"`
	// ReasonCode is empty for ACCEPTED and DUPLICATE. Never `omitempty` on the outcome itself:
	// a client reading a missing field as success would treat a quarantine as an accept.
	ReasonCode string `json:"reason_code,omitempty"`
	Reason     string `json:"reason,omitempty"`
	// GlobalSeq is where it landed, for the ones that did. A client uses it to advance its pull
	// cursor past its own writes rather than pulling them back down — which is why it is stored
	// as well as returned: the receipt exists for the client that lost the response, and that
	// client needs the cursor as much as the outcome.
	GlobalSeq int64 `json:"global_seq,omitempty"`
}

// Receipt is what happened to a whole batch.
type Receipt struct {
	BatchID    uuid.UUID `json:"batch_id"`
	ReceivedAt time.Time `json:"received_at"`

	Events      int `json:"events"`
	Accepted    int `json:"accepted"`
	Duplicated  int `json:"duplicated"`
	Rejected    int `json:"rejected"`
	Quarantined int `json:"quarantined"`
	Blocked     int `json:"blocked"`

	// ClockSkewMS is how far ahead of the server the device's clock was, in milliseconds, positive
	// when the device is ahead. Reported on every receipt rather than only when it is large,
	// because a client that only learns about skew when it is already a problem cannot correct
	// for it gradually.
	ClockSkewMS *int64 `json:"clock_skew_ms,omitempty"`
	// ServerTime is what the server's clock said. A client with no other time source can use this.
	ServerTime time.Time `json:"server_time"`

	Results []Result `json:"results"`

	// Closed is false for a batch that was opened and never finished — a server interrupted
	// halfway. Such a receipt reports nothing, and answering from it would tell a client its
	// fifty events produced no results for ever, so a push under that id is reprocessed instead.
	Closed bool `json:"closed"`
	// Replayed marks a receipt that was read rather than computed — the client asked again about
	// a batch it had already sent. Distinguishable so a client can tell "I have already been
	// processed" from "I have just been processed", which are the same outcome and different
	// stories about what the network did.
	Replayed bool `json:"replayed"`
}

// Incoming is one event as a device sends it.
type Incoming struct {
	EventID       uuid.UUID       `json:"event_id"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   uuid.UUID       `json:"aggregate_id"`
	PatientID     *uuid.UUID      `json:"patient_id,omitempty"`
	VisitID       *uuid.UUID      `json:"visit_id,omitempty"`
	EventType     string          `json:"event_type"`
	EventVersion  int             `json:"event_version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Payload       json.RawMessage `json:"payload"`
	// ExpectedSequence declares that this event depends on the aggregate being where the client
	// thinks it is. Setting it is what makes an event orderable — and what makes it BLOCKED
	// rather than attempted after an earlier failure on the same aggregate.
	ExpectedSequence int64          `json:"expected_sequence,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

// Batch is one push.
type Batch struct {
	BatchID  uuid.UUID  `json:"batch_id"`
	DeviceID uuid.UUID  `json:"device_id"`
	Events   []Incoming `json:"events"`
	// ClientClock is what the device's own clock said when it sent this. Optional, because a
	// client that cannot read its clock should still be able to sync; the skew is then unknown
	// rather than assumed to be zero.
	ClientClock *time.Time `json:"client_clock,omitempty"`
}

// Held is a quarantined event in the triage list.
//
// No envelope: the list is a triage view, and a list that carried clinical values would put a
// blood pressure on every screen that shows a count.
type Held struct {
	ID       uuid.UUID `json:"id"`
	BatchID  uuid.UUID `json:"batch_id"`
	EventID  uuid.UUID `json:"event_id"`
	DeviceID uuid.UUID `json:"device_id"`
	UserID   uuid.UUID `json:"user_id"`

	ReasonCode string `json:"reason_code"`
	Reason     string `json:"reason"`

	EventType  string     `json:"event_type"`
	OccurredAt time.Time  `json:"occurred_at"`
	PatientID  *uuid.UUID `json:"patient_id,omitempty"`
	HeldAt     time.Time  `json:"held_at"`

	Status          string     `json:"status"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy      *uuid.UUID `json:"resolved_by,omitempty"`
	ResolutionNote  string     `json:"resolution_note,omitempty"`
	ReleasedEventID *uuid.UUID `json:"released_event_id,omitempty"`

	DeviceName     string `json:"device_name,omitempty"`
	OperatorCode   string `json:"operator_code,omitempty"`
	OperatorNameEN string `json:"operator_name_en,omitempty"`
	OperatorNameBN string `json:"operator_name_bn,omitempty"`

	// Envelope is present only on the single-event read, never in a list.
	Envelope json.RawMessage `json:"envelope,omitempty"`
}

// PulledEvent is one event on its way down to a device.
type PulledEvent struct {
	EventID       uuid.UUID       `json:"event_id"`
	GlobalSeq     int64           `json:"global_seq"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   uuid.UUID       `json:"aggregate_id"`
	PatientID     *uuid.UUID      `json:"patient_id,omitempty"`
	VisitID       *uuid.UUID      `json:"visit_id,omitempty"`
	EventType     string          `json:"event_type"`
	EventVersion  int             `json:"event_version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	RecordedAt    time.Time       `json:"recorded_at"`
	Payload       json.RawMessage `json:"payload"`
	ActorUserID   uuid.UUID       `json:"actor_user_id"`
	ActorRole     string          `json:"actor_role,omitempty"`
	ActorStation  string          `json:"actor_station,omitempty"`
	Source        string          `json:"source"`
}

// ReferenceVersion is one catalogue's fingerprint.
type ReferenceVersion struct {
	Catalogue string `json:"catalogue"`
	Rows      int64  `json:"rows"`
	// Fingerprint changes when any row does. It does not say which — a client that needs to know
	// re-fetches the catalogue, which is one request and the thing it was going to do anyway.
	Fingerprint string `json:"fingerprint"`
}

// SyncState is where one device has got to.
type SyncState struct {
	DeviceID         uuid.UUID  `json:"device_id"`
	LastPulledSeq    int64      `json:"last_pulled_seq"`
	LastPulledAt     *time.Time `json:"last_pulled_at,omitempty"`
	LastPushedAt     *time.Time `json:"last_pushed_at,omitempty"`
	LastSkewMS       *int64     `json:"last_skew_ms,omitempty"`
	PushedTotal      int64      `json:"pushed_total"`
	QuarantinedTotal int64      `json:"quarantined_total"`
}

// Store reads and writes the sync tables.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewStore builds one.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: dbgen.New(pool)} }

// Device is what the push needs to know about who is sending.
type Device struct {
	ID         uuid.UUID
	FacilityID uuid.UUID
	Status     string
	Name       string
	Reason     string
}

// Trusted reports whether this device's events go straight into the ledger.
func (d Device) Trusted() bool { return d.Status == "active" }

// HoldReason says why an untrusted device's events are held, or empty when they are not.
func (d Device) HoldReason() (code, sentence string) {
	switch d.Status {
	case "revoked", "lost":
		return ReasonDeviceRevoked,
			"this device was " + d.Status + " while it was offline: " + d.Reason
	case "suspended":
		return ReasonDeviceSuspended, "this device is suspended: " + d.Reason
	}
	return "", ""
}

// DeviceFor reads the sending device.
func (s *Store) DeviceFor(ctx context.Context, id, facility uuid.UUID) (Device, error) {
	row, err := s.q.DeviceForSync(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, ErrUnknownDevice
	}
	if err != nil {
		return Device{}, err
	}
	if row.FacilityID != facility {
		return Device{}, ErrWrongFacility
	}
	return Device{
		ID: row.ID, FacilityID: row.FacilityID, Status: row.Status,
		Name: row.Name, Reason: row.StatusReason,
	}, nil
}

// QuarantineLoad reads how many events this device has awaiting review, and how many it may have.
//
// The cap comes from the database rather than from a constant here, because that is where it is
// enforced: `ops.sync_quarantine` has a trigger that refuses the insert, and a number in Go beside
// it would be a second number that agrees until somebody changes one. This read is so that the
// service can answer a client *in the receipt*, in a way the client can act on, instead of letting
// it discover the ceiling as a failed insert.
func (s *Store) QuarantineLoad(ctx context.Context, device uuid.UUID) (held int64, limit int, err error) {
	row, err := s.q.QuarantineLoad(ctx, device)
	if err != nil {
		return 0, 0, err
	}
	return row.Held, int(row.Cap), nil
}

// Receipt reads a batch that was already processed, or reports that it was not.
func (s *Store) Receipt(ctx context.Context, batch uuid.UUID) (Receipt, bool, error) {
	row, err := s.q.BatchReceipt(ctx, batch)
	if errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, err
	}
	out := Receipt{
		BatchID: row.ID, ReceivedAt: row.ReceivedAt,
		Events: int(row.Events), Accepted: int(row.Accepted), Duplicated: int(row.Duplicated),
		Rejected: int(row.Rejected), Quarantined: int(row.Quarantined), Blocked: int(row.Blocked),
		ClockSkewMS: row.SkewMs, Replayed: true, Closed: row.ClosedAt != nil,
	}
	results, err := s.q.ResultsFor(ctx, batch)
	if err != nil {
		return Receipt{}, false, err
	}
	out.Results = make([]Result, 0, len(results))
	for _, r := range results {
		result := Result{
			EventID: r.EventID, Outcome: Outcome(r.Outcome),
			ReasonCode: r.ReasonCode, Reason: r.Reason,
		}
		if r.GlobalSeq != nil {
			result.GlobalSeq = *r.GlobalSeq
		}
		out.Results = append(out.Results, result)
	}
	return out, true, nil
}

// Held is the triage list.
func (s *Store) Held(ctx context.Context, facility uuid.UUID,
	status string, device *uuid.UUID, limit int) ([]Held, error) {

	params := dbgen.HeldEventsParams{FacilityID: facility, RowLimit: int32(limit)}
	if status != "" {
		value := status
		params.Status = &value
	}
	if device != nil {
		params.DeviceID = uuid.NullUUID{UUID: *device, Valid: true}
	}
	rows, err := s.q.HeldEvents(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]Held, 0, len(rows))
	for _, row := range rows {
		out = append(out, heldOf(row))
	}
	return out, nil
}

// HeldEvent is one, with its envelope.
func (s *Store) HeldEvent(ctx context.Context, id, facility uuid.UUID) (Held, error) {
	row, err := s.q.HeldEvent(ctx, dbgen.HeldEventParams{ID: id, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return Held{}, ErrNotFound
	}
	if err != nil {
		return Held{}, err
	}
	held := Held{
		ID: row.ID, BatchID: row.BatchID, EventID: row.EventID,
		DeviceID: row.DeviceID, UserID: row.UserID,
		ReasonCode: row.ReasonCode, Reason: row.Reason,
		EventType: row.EventType, OccurredAt: row.OccurredAt, HeldAt: row.HeldAt,
		Status: row.Status, ResolvedAt: row.ResolvedAt, ResolutionNote: row.ResolutionNote,
		DeviceName: row.DeviceName, OperatorCode: row.OperatorCode,
		OperatorNameEN: row.OperatorNameEn, OperatorNameBN: row.OperatorNameBn,
		Envelope: row.Envelope,
	}
	if row.PatientID.Valid {
		id := row.PatientID.UUID
		held.PatientID = &id
	}
	if row.ResolvedBy.Valid {
		id := row.ResolvedBy.UUID
		held.ResolvedBy = &id
	}
	if row.ReleasedEventID.Valid {
		id := row.ReleasedEventID.UUID
		held.ReleasedEventID = &id
	}
	return held, nil
}

func heldOf(row dbgen.HeldEventsRow) Held {
	held := Held{
		ID: row.ID, BatchID: row.BatchID, EventID: row.EventID,
		DeviceID: row.DeviceID, UserID: row.UserID,
		ReasonCode: row.ReasonCode, Reason: row.Reason,
		EventType: row.EventType, OccurredAt: row.OccurredAt, HeldAt: row.HeldAt,
		Status: row.Status, ResolvedAt: row.ResolvedAt, ResolutionNote: row.ResolutionNote,
		DeviceName: row.DeviceName, OperatorCode: row.OperatorCode,
		OperatorNameEN: row.OperatorNameEn, OperatorNameBN: row.OperatorNameBn,
	}
	if row.PatientID.Valid {
		id := row.PatientID.UUID
		held.PatientID = &id
	}
	if row.ResolvedBy.Valid {
		id := row.ResolvedBy.UUID
		held.ResolvedBy = &id
	}
	if row.ReleasedEventID.Valid {
		id := row.ReleasedEventID.UUID
		held.ReleasedEventID = &id
	}
	return held
}

// State is where one device has got to.
func (s *Store) State(ctx context.Context, device uuid.UUID) (SyncState, error) {
	row, err := s.q.SyncState(ctx, device)
	if errors.Is(err, pgx.ErrNoRows) {
		// A device that has never synced is not an error. It is every device's first morning.
		return SyncState{DeviceID: device}, nil
	}
	if err != nil {
		return SyncState{}, err
	}
	return SyncState{
		DeviceID: row.DeviceID, LastPulledSeq: row.LastPulledSeq,
		LastPulledAt: row.LastPulledAt, LastPushedAt: row.LastPushedAt,
		LastSkewMS:  row.LastSkewMs,
		PushedTotal: row.PushedTotal, QuarantinedTotal: row.QuarantinedTotal,
	}, nil
}

// Reference is every catalogue's fingerprint.
func (s *Store) Reference(ctx context.Context) ([]ReferenceVersion, error) {
	rows, err := s.q.ReferenceVersions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ReferenceVersion, 0, len(rows))
	for _, row := range rows {
		out = append(out, ReferenceVersion{
			Catalogue: row.Catalogue, Rows: row.Rows, Fingerprint: row.Fingerprint,
		})
	}
	return out, nil
}

// InTransaction runs fn against a transaction on this store's pool.
func (s *Store) InTransaction(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// skewOf measures how far ahead of the server a device's clock is, in milliseconds.
func skewOf(client *time.Time, server time.Time) *int64 {
	if client == nil {
		return nil
	}
	ms := client.Sub(server).Milliseconds()
	return &ms
}

// implausible reports whether an event's own timestamp is far enough in the future to damage a
// timeline.
//
// Only the future direction. A device offline for a week produces week-old timestamps and that is
// the entire point of this checkpoint; a device whose clock is a month fast produces an observation
// dated next month, which sorts above every real measurement forever and which nothing downstream
// can distinguish from a genuine future-dated record, because there is no such thing.
func implausible(occurred, server time.Time) bool {
	return occurred.After(server.Add(ClockTolerance))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
