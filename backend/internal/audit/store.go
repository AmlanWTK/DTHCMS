package audit

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

// PostgresStore is the chain in ledger.audit_event, plus break-glass rows and alerts in
// core. The application role can insert into and read the chain and nothing else; the
// database, not this file, is what makes that true (migration 00012).
type PostgresStore struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, q: dbgen.New(pool)}
}

var _ Store = (*PostgresStore)(nil)

// ErrNotFound is what the service sees when a row is absent.
var ErrNotFound = errors.New("audit: not found")

func translate(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// chainLock is the advisory lock key every append takes. One key for the whole log: the
// sequence is facility-wide and the chain is one chain. At the volumes of §9.4 this
// serialisation costs nothing measurable; at the volumes where it would, the log is
// partitioned per facility and so is the key.
const chainLock = 0x4448434d5f415544 // "DHCM_AUD"

func (s *PostgresStore) Append(ctx context.Context, e Entry, chain ChainFunc, at time.Time) (Event, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Event{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, chainLock); err != nil {
		return Event{}, fmt.Errorf("taking the chain lock: %w", err)
	}
	q := s.q.WithTx(tx)

	prev := Genesis
	seq := int64(1)
	head, err := q.AuditHead(ctx)
	switch {
	case err == nil:
		prev = head.Hash
		seq = head.Seq + 1
	case errors.Is(err, pgx.ErrNoRows):
		// The first row.
	default:
		return Event{}, fmt.Errorf("reading the chain head: %w", err)
	}

	details, err := json.Marshal(e.Details)
	if err != nil {
		return Event{}, fmt.Errorf("encoding details: %w", err)
	}
	hash := chain(prev, seq, at, e)
	row, err := q.AppendAuditEvent(ctx, dbgen.AppendAuditEventParams{
		Seq: seq, FacilityID: e.FacilityID, Kind: e.Kind,
		ActorUserID: nullUUID(e.ActorID), ActorCode: e.ActorCode, ActorRole: e.ActorRole,
		TargetUserID: nullUUID(e.TargetUserID), TargetCode: e.TargetCode,
		PatientID: nullUUID(e.PatientID), DeviceID: nullUUID(e.DeviceID), SessionID: nullUUID(e.SessionID),
		Reason: e.Reason, Details: details, ClientDigest: e.ClientDigest,
		RecordedAt: at, PrevHash: prev, Hash: hash,
	})
	if err != nil {
		return Event{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Event{}, err
	}
	return eventFromRow(row), nil
}

func (s *PostgresStore) Walk(ctx context.Context, fromSeq int64, limit int) ([]Event, error) {
	rows, err := s.q.AuditEventsFrom(ctx, dbgen.AuditEventsFromParams{Seq: fromSeq, Limit: int32(limit)}) //nolint:gosec // bounded by the caller
	if err != nil {
		return nil, err
	}
	return eventsFromRows(rows), nil
}

func (s *PostgresStore) Head(ctx context.Context) (Event, bool, error) {
	head, err := s.q.AuditHead(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, false, nil
	}
	if err != nil {
		return Event{}, false, err
	}
	events, err := s.Walk(ctx, head.Seq, 1)
	if err != nil || len(events) == 0 {
		return Event{}, false, err
	}
	return events[0], true, nil
}

func (s *PostgresStore) Strays(ctx context.Context) (int64, error) {
	return s.q.AuditDefaultPartitionCount(ctx)
}

func (s *PostgresStore) Query(ctx context.Context, q Query) ([]Event, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	params := dbgen.AuditEventsPageParams{FacilityID: q.FacilityID, Limit: int32(limit)} //nolint:gosec // bounded just above
	if q.Before > 0 {
		params.Before = &q.Before
	}
	if q.Kind != "" {
		params.Kind = &q.Kind
	}
	if q.ActorCode != "" {
		params.ActorCode = &q.ActorCode
	}
	if q.SubjectCode != "" {
		params.SubjectCode = &q.SubjectCode
	}
	params.PatientID = nullUUID(q.PatientID)
	if !q.Since.IsZero() {
		since := q.Since
		params.Since = &since
	}
	if !q.Until.IsZero() {
		until := q.Until
		params.Until = &until
	}
	rows, err := s.q.AuditEventsPage(ctx, params)
	if err != nil {
		return nil, err
	}
	return eventsFromRows(rows), nil
}

func eventsFromRows(rows []dbgen.LedgerAuditEvent) []Event {
	out := make([]Event, 0, len(rows))
	for _, row := range rows {
		out = append(out, eventFromRow(row))
	}
	return out
}

func eventFromRow(row dbgen.LedgerAuditEvent) Event {
	return Event{
		Entry: Entry{
			Kind: row.Kind, FacilityID: row.FacilityID,
			ActorID: uuidPtr(row.ActorUserID), ActorCode: row.ActorCode, ActorRole: row.ActorRole,
			TargetUserID: uuidPtr(row.TargetUserID), TargetCode: row.TargetCode,
			PatientID: uuidPtr(row.PatientID), DeviceID: uuidPtr(row.DeviceID), SessionID: uuidPtr(row.SessionID),
			Reason: row.Reason, Details: detailsFromJSON(row.Details), ClientDigest: row.ClientDigest,
			At: row.RecordedAt,
		},
		Seq: row.Seq, RecordedAt: row.RecordedAt, PrevHash: row.PrevHash, Hash: row.Hash,
	}
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
