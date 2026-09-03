// Package idempotency is the Postgres store behind httpx's Idempotency-Key middleware
// (CP24, blueprint §7.5 layer 2).
//
// It is deliberately dull: claim a key, complete it with a response, release it when the
// handler produced nothing worth replaying, and purge what has expired. The rules about
// what may be replayed live in the middleware; what is here is the SQL.
package idempotency

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

type Store struct {
	q *dbgen.Queries
}

func New(pool *pgxpool.Pool) *Store { return &Store{q: dbgen.New(pool)} }

var _ httpx.IdempotencyStore = (*Store)(nil)

func (s *Store) Claim(ctx context.Context, userID, facilityID, key string, fingerprint []byte, claimed, expires time.Time) (bool, httpx.IdempotencyRecord, error) {
	user, err := uuid.Parse(userID)
	if err != nil {
		return false, httpx.IdempotencyRecord{}, err
	}
	facility, err := uuid.Parse(facilityID)
	if err != nil {
		return false, httpx.IdempotencyRecord{}, err
	}

	row, err := s.q.ClaimIdempotency(ctx, dbgen.ClaimIdempotencyParams{
		FacilityID: facility, UserID: user, Key: key, Fingerprint: fingerprint,
		ExpiresAt: expires.UTC(), ClaimedAt: claimed.UTC(),
	})
	switch {
	case err == nil:
		return true, recordOf(row), nil
	case errors.Is(err, pgx.ErrNoRows):
		// Somebody else holds the key. Read it back so the middleware can decide whether
		// this is a retry of the same request or a key reused for another.
		existing, err := s.q.IdempotencyRecord(ctx, dbgen.IdempotencyRecordParams{UserID: user, Key: key})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// The holder released or purged it between the two statements. Treating
				// that as "not won, not complete" makes the caller retry, which is right.
				return false, httpx.IdempotencyRecord{}, nil
			}
			return false, httpx.IdempotencyRecord{}, err
		}
		return false, recordOf(existing), nil
	default:
		return false, httpx.IdempotencyRecord{}, err
	}
}

func (s *Store) Complete(ctx context.Context, userID, key string, status int, headers map[string]string, body []byte, at time.Time) error {
	user, err := uuid.Parse(userID)
	if err != nil {
		return err
	}
	encoded, err := httpx.EncodeHeaders(headers)
	if err != nil {
		return err
	}
	completed := at.UTC()
	code := int32(status) //nolint:gosec // an HTTP status
	return s.q.CompleteIdempotency(ctx, dbgen.CompleteIdempotencyParams{
		UserID: user, Key: key, Status: &code, Headers: encoded, Body: body, CompletedAt: &completed,
	})
}

func (s *Store) Release(ctx context.Context, userID, key string) error {
	user, err := uuid.Parse(userID)
	if err != nil {
		return err
	}
	return s.q.ReleaseIdempotency(ctx, dbgen.ReleaseIdempotencyParams{UserID: user, Key: key})
}

// Purge deletes expired records. Called by the cleanup job; returns how many went.
func (s *Store) Purge(ctx context.Context, cutoff time.Time) (int, error) {
	n, err := s.q.PurgeExpiredIdempotency(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

func recordOf(row dbgen.OpsIdempotencyRecord) httpx.IdempotencyRecord {
	out := httpx.IdempotencyRecord{
		Fingerprint: row.Fingerprint,
		Complete:    row.State == "complete",
		Headers:     httpx.DecodeHeaders(row.Headers),
		Body:        row.Body,
	}
	if row.Status != nil {
		out.Status = int(*row.Status)
	}
	return out
}
