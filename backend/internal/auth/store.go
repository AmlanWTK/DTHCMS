package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// PostgresStore is the persistence behind Sessions and Service.
//
// It is a translation layer and nothing else: the generated types speak in nullable
// wrappers and the domain speaks in pointers, and one of those has to give. Every rule
// lives above this file, where it can be tested without a database.
type PostgresStore struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, q: dbgen.New(pool)}
}

// ErrNotFound is what the service sees when a row is absent.
//
// Translated here rather than leaking pgx.ErrNoRows upward, because the service's answer to
// "no such session" and "no such user" is the same refusal either way, and a domain that
// imports a driver's sentinel is a domain tied to a driver.
var ErrNotFound = errors.New("not found")

func translate(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// --- users and credentials ---

func (s *PostgresStore) CredentialsByCode(ctx context.Context, facilityID uuid.UUID, code string) (User, string, error) {
	row, err := s.q.CredentialsByCode(ctx, dbgen.CredentialsByCodeParams{
		FacilityID: facilityID, EmployeeCode: code})
	if err != nil {
		return User{}, "", translate(err)
	}

	hash := ""
	if row.PasswordHash != nil {
		hash = *row.PasswordHash
	}
	return userFromRow(row), hash, nil
}

func (s *PostgresStore) UserByID(ctx context.Context, id uuid.UUID) (User, error) {
	row, err := s.q.GetUser(ctx, id)
	if err != nil {
		return User{}, translate(err)
	}
	return userFromRow(row), nil
}

func (s *PostgresStore) UpdatePasswordHash(ctx context.Context, userID uuid.UUID, hash string) error {
	return s.q.SetPasswordHash(ctx, dbgen.SetPasswordHashParams{
		ID: userID, PasswordHash: &hash, UpdatedBy: nullUUID(&userID)})
}

func userFromRow(row dbgen.CoreAppUser) User {
	return User{
		ID: row.ID, FacilityID: row.FacilityID, Code: row.EmployeeCode,
		NameEN: row.NameEn, NameBN: row.NameBn,
		Phone: row.Phone, Email: row.Email,
		Status: Status(row.Status), StatusNote: row.StatusReason,
		StatusSince: row.StatusChangedAt, LastLoginAt: row.LastLoginAt,
		CreatedAt: row.CreatedAt,
	}
}

// PermissionsForUser resolves the union across every live role [R-02].
//
// Needed at CP16 rather than at CP19 because /v1/auth/me returns it: an interface that knows
// what the operator may do can hide what they may not, which is a courtesy rather than a
// control. The control is server-side and arrives at CP20 — a screen that hides a button is
// not an authorisation mechanism, and this method existing does not make it one.
func (s *PostgresStore) PermissionsForUser(ctx context.Context, userID uuid.UUID) ([]string, error) {
	codes, err := s.q.PermissionsForUser(ctx, userID)
	if err != nil {
		return nil, translate(err)
	}
	return codes, nil
}

// RolesForUser lists the roles a user currently holds, for the same screen.
func (s *PostgresStore) RolesForUser(ctx context.Context, userID uuid.UUID) ([]Role, error) {
	rows, err := s.q.RolesForUser(ctx, userID)
	if err != nil {
		return nil, translate(err)
	}
	out := make([]Role, 0, len(rows))
	for _, row := range rows {
		role := Role{
			ID: row.ID, Code: RoleCode(row.Code), NameEN: row.NameEn, NameBN: row.NameBn,
			Description: row.Description, IsClinical: row.IsClinical,
		}
		if row.StationCode != nil {
			role.Station = StationCode(*row.StationCode)
		}
		out = append(out, role)
	}
	return out, nil
}

// --- login attempts ---

func (s *PostgresStore) RecordAttempt(ctx context.Context, a Attempt) error {
	return s.q.RecordLoginAttempt(ctx, dbgen.RecordLoginAttemptParams{
		FacilityID: a.FacilityID, EmployeeCode: a.Code, UserID: nullUUID(a.UserID),
		Succeeded: a.Succeeded, FailureKind: string(a.Failure),
		ClientDigest: a.ClientDigest, AttemptedAt: a.At,
	})
}

func (s *PostgresStore) RecentFailures(ctx context.Context, facilityID uuid.UUID, code string, since time.Time) (int, error) {
	n, err := s.q.RecentFailuresForCode(ctx, dbgen.RecentFailuresForCodeParams{
		FacilityID: facilityID, EmployeeCode: code, AttemptedAt: since})
	return int(n), err
}

func (s *PostgresStore) RecentFailuresForClient(ctx context.Context, digest []byte, since time.Time) (int, error) {
	n, err := s.q.RecentFailuresForClient(ctx, dbgen.RecentFailuresForClientParams{
		ClientDigest: digest, AttemptedAt: since})
	return int(n), err
}

// --- sessions ---

func (s *PostgresStore) CreateSession(ctx context.Context, session Session, digest []byte) (Session, error) {
	row, err := s.q.CreateSession(ctx, dbgen.CreateSessionParams{
		FacilityID: session.FacilityID, UserID: session.UserID, TokenDigest: digest,
		IssuedAt: session.IssuedAt, ExpiresAt: session.ExpiresAt, UserAgent: session.UserAgent,
		DeviceID: nullUUID(session.DeviceID),
	})
	if err != nil {
		return Session{}, translate(err)
	}
	return sessionFromRow(row), nil
}

func (s *PostgresStore) SessionByToken(ctx context.Context, digest []byte) (Session, error) {
	row, err := s.q.SessionByToken(ctx, digest)
	if err != nil {
		return Session{}, translate(err)
	}
	return sessionFromRow(row), nil
}

func (s *PostgresStore) SessionByID(ctx context.Context, id uuid.UUID) (Session, error) {
	row, err := s.q.SessionByID(ctx, id)
	if err != nil {
		return Session{}, translate(err)
	}
	return sessionFromRow(row), nil
}

func (s *PostgresStore) TouchSession(ctx context.Context, id uuid.UUID, at time.Time) error {
	return s.q.TouchSession(ctx, dbgen.TouchSessionParams{ID: id, LastSeenAt: at})
}

func (s *PostgresStore) RevokeSession(ctx context.Context, id uuid.UUID, by *uuid.UUID, reason string) error {
	return s.q.RevokeSession(ctx, dbgen.RevokeSessionParams{
		ID: id, RevokedBy: nullUUID(by), RevokeReason: reason})
}

func (s *PostgresStore) RevokeSessionsForUser(ctx context.Context, userID uuid.UUID, by *uuid.UUID, reason string) (int, error) {
	n, err := s.q.RevokeSessionsForUser(ctx, dbgen.RevokeSessionsForUserParams{
		UserID: userID, RevokedBy: nullUUID(by), RevokeReason: reason})
	return int(n), err
}

func (s *PostgresStore) SessionsForUser(ctx context.Context, userID uuid.UUID) ([]Session, error) {
	rows, err := s.q.SessionsForUser(ctx, userID)
	if err != nil {
		return nil, translate(err)
	}
	out := make([]Session, 0, len(rows))
	for _, row := range rows {
		out = append(out, sessionFromRow(row))
	}
	return out, nil
}

func sessionFromRow(row dbgen.CoreSession) Session {
	return Session{
		ID: row.ID, FacilityID: row.FacilityID, UserID: row.UserID,
		DeviceID:  uuidPtr(row.DeviceID),
		IssuedAt:  row.IssuedAt,
		ExpiresAt: row.ExpiresAt, LastSeenAt: row.LastSeenAt,
		SteppedUpAt: row.SteppedUpAt, RevokedAt: row.RevokedAt,
		RevokeReason: row.RevokeReason, UserAgent: row.UserAgent,
	}
}

// --- refresh tokens ---

func (s *PostgresStore) CreateRefresh(ctx context.Context, r RefreshToken, digest []byte) (RefreshToken, error) {
	row, err := s.q.CreateRefreshToken(ctx, dbgen.CreateRefreshTokenParams{
		ID: r.ID, FacilityID: r.FacilityID, SessionID: r.SessionID, FamilyID: r.FamilyID,
		TokenDigest: digest, IssuedAt: r.IssuedAt, ExpiresAt: r.ExpiresAt,
	})
	if err != nil {
		return RefreshToken{}, translate(err)
	}
	return refreshFromRow(row), nil
}

func (s *PostgresStore) RefreshByToken(ctx context.Context, digest []byte) (RefreshToken, error) {
	row, err := s.q.RefreshTokenByDigest(ctx, digest)
	if err != nil {
		return RefreshToken{}, translate(err)
	}
	return refreshFromRow(row), nil
}

// RotateRefresh issues the successor, spends the predecessor and re-keys the session —
// atomically.
//
// The transaction is the whole reason this method exists rather than three calls from the
// service. Marking the old token used and then failing to insert the new one locks the user
// out of a session they still hold; inserting first and then failing to mark leaves two live
// tokens in a lineage whose entire purpose is that there is exactly one, which would make
// the next legitimate refresh look like theft and revoke the family.
//
// Neither is a state to discover at three in the morning, so neither is a state that exists.
//
// The order inside the transaction is successor first. replaced_by is a foreign key, and
// PostgreSQL checks foreign keys at the end of each statement, not at commit — so naming a
// successor that does not yet exist fails immediately, transaction or no transaction. The
// first version of this method did exactly that, and every refresh returned 401. The
// in-memory store could not have shown it; the database test did, on its first run.
func (s *PostgresStore) RotateRefresh(ctx context.Context, spent uuid.UUID, next RefreshToken,
	nextDigest []byte, accessDigest []byte, accessExpiry time.Time, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning the rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.q.WithTx(tx)

	if _, err := q.CreateRefreshToken(ctx, dbgen.CreateRefreshTokenParams{
		ID: next.ID, FacilityID: next.FacilityID, SessionID: next.SessionID,
		FamilyID: next.FamilyID, TokenDigest: nextDigest,
		IssuedAt: next.IssuedAt, ExpiresAt: next.ExpiresAt,
	}); err != nil {
		return fmt.Errorf("issuing the successor: %w", err)
	}

	if err := q.MarkRefreshUsed(ctx, dbgen.MarkRefreshUsedParams{
		ID:         spent,
		UsedAt:     &at,
		ReplacedBy: uuid.NullUUID{UUID: next.ID, Valid: true},
	}); err != nil {
		return fmt.Errorf("spending the refresh token: %w", err)
	}

	if err := q.RekeySession(ctx, dbgen.RekeySessionParams{
		ID: next.SessionID, TokenDigest: accessDigest,
		ExpiresAt: accessExpiry, LastSeenAt: at,
	}); err != nil {
		return fmt.Errorf("re-keying the session: %w", err)
	}

	return tx.Commit(ctx)
}

// RevokeFamily ends every token in a lineage and every session those tokens belong to.
//
// Both, in one transaction. Revoking the tokens alone would leave the access tokens already
// issued under that family working until they expire — up to a full lifetime after the theft
// was detected, which is precisely the window this mechanism exists to close.
func (s *PostgresStore) RevokeFamily(ctx context.Context, familyID uuid.UUID, reason string) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("beginning the revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.q.WithTx(tx)

	tokens, err := q.RevokeRefreshFamilyTokens(ctx, dbgen.RevokeRefreshFamilyTokensParams{
		FamilyID: familyID, RevokeReason: reason})
	if err != nil {
		return 0, fmt.Errorf("revoking the family's tokens: %w", err)
	}

	if _, err := q.RevokeSessionsInFamily(ctx, dbgen.RevokeSessionsInFamilyParams{
		FamilyID: familyID, RevokeReason: reason}); err != nil {
		return 0, fmt.Errorf("revoking the family's sessions: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int(tokens), nil
}

func (s *PostgresStore) RevokeRefreshForSession(ctx context.Context, sessionID uuid.UUID, reason string) error {
	return s.q.RevokeRefreshForSession(ctx, dbgen.RevokeRefreshForSessionParams{
		SessionID: sessionID, RevokeReason: reason})
}

func refreshFromRow(row dbgen.CoreRefreshToken) RefreshToken {
	return RefreshToken{
		ID: row.ID, SessionID: row.SessionID, FamilyID: row.FamilyID,
		FacilityID: row.FacilityID, IssuedAt: row.IssuedAt, ExpiresAt: row.ExpiresAt,
		UsedAt: row.UsedAt, ReplacedBy: uuidPtr(row.ReplacedBy),
		RevokedAt: row.RevokedAt, RevokeReason: row.RevokeReason,
	}
}

// --- the two shapes of nothing ---
//
// pgx says "absent" with a Valid flag; the domain says it with a nil pointer. Converting in
// one pair of functions rather than at thirty call sites is the difference between a
// translation layer and a translation smeared through the code.

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func uuidPtr(n uuid.NullUUID) *uuid.UUID {
	if !n.Valid {
		return nil
	}
	id := n.UUID
	return &id
}

// Compile-time proof that the adapter satisfies what the service asks for. Without it, a
// signature drifting apart from its interface is a failure at wiring time in main rather
// than a red squiggle here.
//
// Only SessionStore, deliberately. The Store interface that Service (role grants, lifecycle)
// needs is not implemented yet: nothing calls it until the administration screens at CP21,
// and an adapter written now would be a hundred lines nobody has run. It lands with its first
// caller.
var _ SessionStore = (*PostgresStore)(nil)
