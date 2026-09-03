package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The second-factor half of PostgresStore. Same store, same pool, same translate().

var _ SecondFactorStore = (*PostgresStore)(nil)

func (s *PostgresStore) TotpByUser(ctx context.Context, userID uuid.UUID) (TotpEnrolment, error) {
	row, err := s.q.TotpByUser(ctx, userID)
	if err != nil {
		return TotpEnrolment{}, translate(err)
	}
	return totpFromRow(row), nil
}

func (s *PostgresStore) BeginTotpEnrolment(ctx context.Context, userID, facilityID uuid.UUID, sealed []byte, keyID string) (TotpEnrolment, error) {
	row, err := s.q.BeginTotpEnrolment(ctx, dbgen.BeginTotpEnrolmentParams{
		UserID: userID, FacilityID: facilityID, SecretSealed: sealed, KeyID: keyID,
	})
	if err != nil {
		// The statement's WHERE refuses to overwrite a confirmed, undisabled row, and an
		// INSERT ... ON CONFLICT whose DO UPDATE is filtered out returns no row at all.
		// That absence is the refusal.
		if translate(err) == ErrNotFound {
			return TotpEnrolment{}, ErrAlreadyEnrolled
		}
		return TotpEnrolment{}, translate(err)
	}
	return totpFromRow(row), nil
}

func (s *PostgresStore) ConfirmTotp(ctx context.Context, userID uuid.UUID, at time.Time, step int64) error {
	n, err := s.q.ConfirmTotp(ctx, dbgen.ConfirmTotpParams{UserID: userID, ConfirmedAt: &at, LastUsedStep: &step})
	if err != nil {
		return translate(err)
	}
	if n == 0 {
		return ErrAlreadyEnrolled
	}
	return nil
}

func (s *PostgresStore) RecordTotpUse(ctx context.Context, userID uuid.UUID, step int64, sealed []byte, keyID string) (bool, error) {
	n, err := s.q.RecordTotpUse(ctx, dbgen.RecordTotpUseParams{
		UserID: userID, LastUsedStep: &step, SecretSealed: sealed, KeyID: keyID,
	})
	if err != nil {
		return false, translate(err)
	}
	return n > 0, nil
}

func (s *PostgresStore) DisableTotp(ctx context.Context, userID uuid.UUID, by *uuid.UUID, reason string) error {
	n, err := s.q.DisableTotp(ctx, dbgen.DisableTotpParams{
		UserID: userID, DisabledBy: nullUUID(by), DisableReason: reason,
	})
	if err != nil {
		return translate(err)
	}
	if n == 0 {
		return ErrNotEnrolled
	}
	return nil
}

func totpFromRow(row dbgen.CoreUserTotp) TotpEnrolment {
	return TotpEnrolment{
		UserID: row.UserID, FacilityID: row.FacilityID,
		SecretSealed: row.SecretSealed, KeyID: row.KeyID,
		ConfirmedAt: row.ConfirmedAt, LastUsedStep: row.LastUsedStep, DisabledAt: row.DisabledAt,
	}
}

// --- recovery codes ---

// ReplaceRecoveryCodes revokes the live sheet and writes the new one in one transaction, so
// there is never a moment with two sheets live, nor one with none when there should be ten.
func (s *PostgresStore) ReplaceRecoveryCodes(ctx context.Context, userID, facilityID, batchID uuid.UUID, digests [][]byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning the recovery code replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.q.WithTx(tx)
	if _, err := q.RevokeRecoveryCodes(ctx, userID); err != nil {
		return fmt.Errorf("revoking the previous recovery codes: %w", err)
	}
	for _, digest := range digests {
		if err := q.InsertRecoveryCode(ctx, dbgen.InsertRecoveryCodeParams{
			UserID: userID, FacilityID: facilityID, BatchID: batchID, CodeDigest: digest,
		}); err != nil {
			return fmt.Errorf("inserting a recovery code: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) RecoveryCodeByDigest(ctx context.Context, digest []byte) (RecoveryCode, error) {
	row, err := s.q.RecoveryCodeByDigest(ctx, digest)
	if err != nil {
		return RecoveryCode{}, translate(err)
	}
	return RecoveryCode{
		ID: row.ID, UserID: row.UserID, BatchID: row.BatchID, UsedAt: row.UsedAt, RevokedAt: row.RevokedAt,
	}, nil
}

func (s *PostgresStore) UseRecoveryCode(ctx context.Context, id uuid.UUID, clientDigest []byte) (bool, error) {
	n, err := s.q.UseRecoveryCode(ctx, dbgen.UseRecoveryCodeParams{ID: id, UsedFromClient: clientDigest})
	if err != nil {
		return false, translate(err)
	}
	return n > 0, nil
}

func (s *PostgresStore) CountLiveRecoveryCodes(ctx context.Context, userID uuid.UUID) (int, error) {
	n, err := s.q.CountLiveRecoveryCodes(ctx, userID)
	if err != nil {
		return 0, translate(err)
	}
	return int(n), nil
}

// --- short tokens ---

func (s *PostgresStore) CreateShortToken(ctx context.Context, t ShortToken, digest, clientDigest []byte) (ShortToken, error) {
	row, err := s.q.CreateShortToken(ctx, dbgen.CreateShortTokenParams{
		FacilityID: t.FacilityID, UserID: t.UserID, SessionID: nullUUID(t.SessionID),
		Kind: t.Kind, Purpose: t.Purpose, TokenDigest: digest, ClientDigest: clientDigest,
		IssuedAt: t.IssuedAt, ExpiresAt: t.ExpiresAt,
	})
	if err != nil {
		return ShortToken{}, translate(err)
	}
	return shortTokenFromRow(row), nil
}

func (s *PostgresStore) ShortTokenByDigest(ctx context.Context, digest []byte) (ShortToken, error) {
	row, err := s.q.ShortTokenByDigest(ctx, digest)
	if err != nil {
		return ShortToken{}, translate(err)
	}
	return shortTokenFromRow(row), nil
}

func (s *PostgresStore) ConsumeShortToken(ctx context.Context, id uuid.UUID, at time.Time) (bool, error) {
	n, err := s.q.ConsumeShortToken(ctx, dbgen.ConsumeShortTokenParams{ID: id, ConsumedAt: &at})
	if err != nil {
		return false, translate(err)
	}
	return n > 0, nil
}

func (s *PostgresStore) RecordShortTokenFailure(ctx context.Context, id uuid.UUID) (int, error) {
	n, err := s.q.RecordShortTokenFailure(ctx, id)
	if err != nil {
		return 0, translate(err)
	}
	return int(n), nil
}

func shortTokenFromRow(row dbgen.CoreShortToken) ShortToken {
	return ShortToken{
		ID: row.ID, FacilityID: row.FacilityID, UserID: row.UserID, SessionID: uuidPtr(row.SessionID),
		Kind: row.Kind, Purpose: row.Purpose, IssuedAt: row.IssuedAt, ExpiresAt: row.ExpiresAt,
		ConsumedAt: row.ConsumedAt, Failures: int(row.Failures),
	}
}

// --- security events ---

func (s *PostgresStore) RecordSecurityEvent(ctx context.Context, e SecurityEvent) error {
	detail, err := json.Marshal(e.Detail)
	if err != nil {
		return fmt.Errorf("encoding the event detail: %w", err)
	}
	err = s.q.InsertSecurityEvent(ctx, dbgen.InsertSecurityEventParams{
		FacilityID: e.FacilityID, UserID: nullUUID(e.UserID), SessionID: nullUUID(e.SessionID),
		ActorID: nullUUID(e.ActorID), Kind: e.Kind, Outcome: e.Outcome, Detail: detail,
		ClientDigest: e.ClientDigest, At: e.At,
	})
	if err != nil {
		return fmt.Errorf("recording the %s event: %w", e.Kind, translate(err))
	}
	return nil
}
