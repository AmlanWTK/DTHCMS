package eventstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// InTransaction is work that runs inside the append transaction, after the event row is
// written and before the commit (CP25).
//
// It exists for the projections whose staleness would be clinical: the junior doctor's
// screen must show the measurement the nurse entered a second ago (§4.1). Being in the same
// transaction is the whole point — the event and the read model commit together, or neither
// does, so there is no window in which the ledger has a fact the screen does not.
//
// It is an interface here and implemented in internal/projection because the ledger may not
// depend on what is derived from it: eventstore imports platform and nothing else.
//
// A failure fails the append. That is the correct trade for a *synchronous* projection and
// the reason there are very few of them: everything that can tolerate a second of lag is
// asynchronous and cannot affect a write at all (criterion 4).
type InTransaction interface {
	ApplyInTx(ctx context.Context, tx pgx.Tx, e Event) error
}

// Store is the ledger: Append and the reads a projection or a verifier needs.
type Store struct {
	pool        *pgxpool.Pool
	q           *dbgen.Queries
	registry    *Registry
	clock       clock.Clock
	synchronous InTransaction
}

type Config struct {
	Pool     *pgxpool.Pool
	Registry *Registry
	Clock    clock.Clock
	// Synchronous runs inside the append transaction. Nil means no synchronous
	// projections, which is what the ledger's own tests want and what the service was
	// before CP25.
	Synchronous InTransaction
}

func New(cfg Config) *Store {
	if cfg.Registry == nil {
		cfg.Registry = Default
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	return &Store{
		pool: cfg.Pool, q: dbgen.New(cfg.Pool), registry: cfg.Registry,
		clock: cfg.Clock, synchronous: cfg.Synchronous,
	}
}

// aggregateLock is the advisory lock key for one aggregate: appends to the same
// aggregate serialise, appends to different aggregates do not. Two aggregates hashing to
// the same key serialise needlessly and harmlessly.
func aggregateLock(aggregateType string, aggregateID uuid.UUID) int64 {
	h := fnv.New64a()
	h.Write([]byte(aggregateType))
	h.Write([]byte{0})
	h.Write(aggregateID[:])
	return int64(h.Sum64()) //nolint:gosec // a lock key, not a quantity
}

// Append is the single write path (§5.4).
//
// In order: the envelope is complete (criterion 5); the type and version are registered
// and the payload matches (§7.10); the event_id is new, or the original is returned
// (§7.5); under the aggregate's lock, the next sequence is the head plus one and the
// expected sequence, if given, is the head (§7.9); the hash is computed over everything
// including the sequence and the recorded time; the row and its key are written in one
// transaction. Gapless because the sequence is read and written under one lock; linear
// because the hash covers the previous one.
func (s *Store) Append(ctx context.Context, e Envelope) (Event, error) {
	if err := s.check(&e); err != nil {
		return Event{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Event{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	written, err := s.append(ctx, tx, e)
	if err != nil {
		// Two retries of one event racing each other: the second lost the primary key and
		// its transaction is poisoned, so it is abandoned and the winner's row is read on
		// a fresh connection. That is the idempotent answer (§7.5), and it is only
		// available here — a caller who owns the transaction owns that decision, which is
		// why AppendInTx returns the error instead.
		if errors.Is(err, errKeyRace) {
			_ = tx.Rollback(ctx)
			if existing, lookup := s.q.EventByID(ctx, e.EventID); lookup == nil {
				ev := eventFromRow(existing)
				ev.Duplicate = true
				return ev, nil
			}
		}
		return Event{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Event{}, err
	}
	return written, nil
}

// AppendInTx appends inside a transaction the caller already holds, and does not commit.
//
// This exists for the one shape that genuinely needs it: a write whose non-clinical rows
// and whose event must land together or not at all. Patient registration is the first —
// the patient row, the identifiers, the anonymised research row and PATIENT_REGISTERED are
// one act, and a patient with no event behind it is a fact with no history (CP29).
//
// Everything Append does before the commit is done here, in the same order, including the
// synchronous projections. What is *not* done is the recovery from a losing race on
// `event_key_pkey`: that requires abandoning the transaction, which is the caller's to do.
func (s *Store) AppendInTx(ctx context.Context, tx pgx.Tx, e Envelope) (Event, error) {
	if err := s.check(&e); err != nil {
		return Event{}, err
	}
	return s.append(ctx, tx, e)
}

// check is everything that can be decided before a transaction exists.
func (s *Store) check(e *Envelope) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if _, err := s.registry.Decode(e.EventType, e.EventVersion, e.Payload); err != nil {
		return err
	}
	if t, _ := s.registry.Lookup(e.EventType, e.EventVersion); t.Aggregate != e.AggregateType {
		return fmt.Errorf("%w: %s belongs to %s aggregates, not %s", ErrInvalidPayload, e.EventType, t.Aggregate, e.AggregateType)
	}
	if e.Metadata == nil {
		e.Metadata = map[string]any{}
	}
	return nil
}

// errKeyRace marks the one failure Append can recover from and AppendInTx cannot.
var errKeyRace = errors.New("eventstore: the event key was taken by a concurrent append")

func (s *Store) append(ctx context.Context, tx pgx.Tx, e Envelope) (Event, error) {
	q := s.q.WithTx(tx)

	// Idempotency first, outside the lock: a retry of an event that landed costs one
	// index lookup and takes no lock.
	if existing, err := q.EventByID(ctx, e.EventID); err == nil {
		ev := eventFromRow(existing)
		ev.Duplicate = true
		return ev, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Event{}, err
	}

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, aggregateLock(e.AggregateType, e.AggregateID)); err != nil {
		return Event{}, fmt.Errorf("taking the aggregate lock: %w", err)
	}

	prev := Genesis
	seq := int64(1)
	head, err := q.AggregateHead(ctx, dbgen.AggregateHeadParams{AggregateType: e.AggregateType, AggregateID: e.AggregateID})
	switch {
	case err == nil:
		prev = head.Hash
		seq = head.Sequence + 1
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return Event{}, fmt.Errorf("reading the aggregate head: %w", err)
	}
	if e.ExpectedSequence != 0 && e.ExpectedSequence != seq-1 {
		return Event{}, fmt.Errorf("%w: expected head %d, found %d", ErrSequenceConflict, e.ExpectedSequence, seq-1)
	}

	// The global sequence is drawn here rather than by the column default, because the
	// hash covers it and the hash must be computed before the row exists.
	var globalSeq int64
	if err := tx.QueryRow(ctx, `SELECT nextval('ledger.event_global_seq')`).Scan(&globalSeq); err != nil {
		return Event{}, fmt.Errorf("drawing the global sequence: %w", err)
	}
	recordedAt := s.clock.Now().UTC().Truncate(time.Microsecond)
	e.OccurredAt = e.OccurredAt.UTC().Truncate(time.Microsecond)
	hash := hashOf(prev, seq, globalSeq, recordedAt, e)

	metadata, _ := json.Marshal(e.Metadata)
	var correction []byte
	if e.Correction != nil {
		correction, _ = json.Marshal(e.Correction)
	}
	row, err := q.AppendEvent(ctx, dbgen.AppendEventParams{
		GlobalSeq: globalSeq, EventID: e.EventID, AggregateType: e.AggregateType, AggregateID: e.AggregateID, Sequence: seq,
		PatientID: nullUUID(e.PatientID), VisitID: nullUUID(e.VisitID),
		EventType: e.EventType, EventVersion: int16(e.EventVersion), //nolint:gosec // validated ≥ 1, small
		OccurredAt: e.OccurredAt, RecordedAt: recordedAt,
		ActorUserID: e.Actor.userID, ActorDeviceID: e.Actor.deviceID, ActorRole: e.Actor.role,
		ActorStation: nullString(e.Actor.station), FacilityID: e.Actor.facilityID, Source: string(e.Source),
		Payload: e.Payload, Previous: e.Previous, Correction: correction, Metadata: metadata,
		PrevHash: prev, Hash: hash,
	})
	if err != nil {
		return Event{}, err
	}
	if err := q.InsertEventKey(ctx, dbgen.InsertEventKeyParams{
		EventID: e.EventID, AggregateType: e.AggregateType, AggregateID: e.AggregateID, Sequence: seq,
		GlobalSeq: globalSeq, RecordedAt: recordedAt, Hash: hash, FacilityID: e.Actor.facilityID,
	}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "event_key_pkey" {
			return Event{}, fmt.Errorf("%w: %w", errKeyRace, err)
		}
		return Event{}, err
	}
	written := eventFromRow(row)
	if s.synchronous != nil {
		if err := s.synchronous.ApplyInTx(ctx, tx, written); err != nil {
			return Event{}, fmt.Errorf("a synchronous projection refused the event: %w", err)
		}
	}
	return written, nil
}

// ByID reads one event.
func (s *Store) ByID(ctx context.Context, id uuid.UUID) (Event, error) {
	row, err := s.q.EventByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Event{}, ErrNotFound
		}
		return Event{}, err
	}
	return eventFromRow(row), nil
}

// Stream reads an aggregate's events from a sequence, in order — the replay read.
func (s *Store) Stream(ctx context.Context, aggregateType string, aggregateID uuid.UUID, fromSeq int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.q.EventsForAggregate(ctx, dbgen.EventsForAggregateParams{
		AggregateType: aggregateType, AggregateID: aggregateID, Sequence: fromSeq, Limit: int32(limit), //nolint:gosec // bounded
	})
	if err != nil {
		return nil, err
	}
	return eventsFromRows(rows), nil
}

// FromGlobal reads events from a global sequence, in order — the projection read.
func (s *Store) FromGlobal(ctx context.Context, fromGlobal int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.q.EventsFromGlobal(ctx, dbgen.EventsFromGlobalParams{GlobalSeq: fromGlobal, Limit: int32(limit)}) //nolint:gosec // bounded
	if err != nil {
		return nil, err
	}
	return eventsFromRows(rows), nil
}

// Strays counts rows in the default partition.
func (s *Store) Strays(ctx context.Context) (int64, error) {
	return s.q.EventDefaultPartitionCount(ctx)
}

// Count is the number of events.
func (s *Store) Count(ctx context.Context) (int64, error) {
	return s.q.EventCount(ctx)
}

// Registry is the store's registry, for callers that decode payloads.
func (s *Store) Registry() *Registry { return s.registry }

// Decode reads an event's payload as the current version of its type, upcasting an old
// version on the way (§7.10).
func (s *Store) Decode(ev Event) (Payload, error) {
	raw, version, err := s.registry.Upcast(ev.EventType, ev.EventVersion, ev.Payload)
	if err != nil {
		return nil, err
	}
	return s.registry.Decode(ev.EventType, version, raw)
}

func eventsFromRows(rows []dbgen.LedgerEvent) []Event {
	out := make([]Event, 0, len(rows))
	for _, row := range rows {
		out = append(out, eventFromRow(row))
	}
	return out
}

func eventFromRow(row dbgen.LedgerEvent) Event {
	ev := Event{
		Envelope: Envelope{
			EventID: row.EventID, AggregateType: row.AggregateType, AggregateID: row.AggregateID,
			PatientID: uuidPtr(row.PatientID), VisitID: uuidPtr(row.VisitID),
			EventType: row.EventType, EventVersion: int(row.EventVersion), OccurredAt: row.OccurredAt,
			Actor: Actor{
				userID: row.ActorUserID, deviceID: row.ActorDeviceID, role: row.ActorRole,
				station: deref(row.ActorStation), facilityID: row.FacilityID,
			},
			Source: Source(row.Source), Payload: json.RawMessage(row.Payload),
			Previous: json.RawMessage(row.Previous), Metadata: map[string]any{},
		},
		GlobalSeq: row.GlobalSeq, Sequence: row.Sequence, RecordedAt: row.RecordedAt,
		PrevHash: row.PrevHash, Hash: row.Hash,
	}
	if len(row.Previous) == 0 {
		ev.Previous = nil
	}
	if len(row.Correction) > 0 {
		var c Correction
		if err := json.Unmarshal(row.Correction, &c); err == nil {
			ev.Correction = &c
		}
	}
	if len(row.Metadata) > 0 {
		_ = json.Unmarshal(row.Metadata, &ev.Metadata)
	}
	return ev
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func uuidPtr(id uuid.NullUUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	v := id.UUID
	return &v
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
