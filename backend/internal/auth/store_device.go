package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The device half of PostgresStore.

var _ DeviceStore = (*PostgresStore)(nil)

func (s *PostgresStore) CreateDevice(ctx context.Context, facilityID uuid.UUID, name string, kind DeviceKind, by uuid.UUID) (Device, error) {
	row, err := s.q.CreateDevice(ctx, dbgen.CreateDeviceParams{
		FacilityID: facilityID, Name: name, Kind: string(kind), StatusChangedBy: nullUUID(&by),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Device{}, ErrDeviceNameTaken
		}
		return Device{}, translate(err)
	}
	return deviceFromRow(row), nil
}

func (s *PostgresStore) DeviceByID(ctx context.Context, id uuid.UUID) (Device, error) {
	row, err := s.q.DeviceByID(ctx, id)
	if err != nil {
		return Device{}, translate(err)
	}
	return deviceFromRow(row), nil
}

func (s *PostgresStore) DevicesForFacility(ctx context.Context, facilityID uuid.UUID) ([]Device, error) {
	rows, err := s.q.DevicesForFacility(ctx, facilityID)
	if err != nil {
		return nil, translate(err)
	}
	out := make([]Device, 0, len(rows))
	for _, row := range rows {
		out = append(out, deviceFromRow(row))
	}
	return out, nil
}

func (s *PostgresStore) ActivateDevice(ctx context.Context, id, by uuid.UUID, at time.Time, meta DeviceMetadata) (Device, error) {
	row, err := s.q.ActivateDevice(ctx, dbgen.ActivateDeviceParams{
		ID: id, StatusChangedBy: nullUUID(&by), LastSeenAt: &at,
		Model: meta.Model, OsVersion: meta.OSVersion, AppVersion: meta.AppVersion,
	})
	if err != nil {
		return Device{}, translate(err)
	}
	return deviceFromRow(row), nil
}

func (s *PostgresStore) ChangeDeviceStatus(ctx context.Context, id uuid.UUID, to DeviceStatus, by *uuid.UUID, reason string, at time.Time) (Device, error) {
	row, err := s.q.ChangeDeviceStatus(ctx, dbgen.ChangeDeviceStatusParams{
		ID: id, Status: string(to), StatusChangedBy: nullUUID(by), StatusReason: reason, StatusChangedAt: at,
	})
	if err != nil {
		return Device{}, translate(err)
	}
	return deviceFromRow(row), nil
}

func (s *PostgresStore) TouchDevice(ctx context.Context, id uuid.UUID, at time.Time, appVersion string) error {
	return translate(s.q.TouchDevice(ctx, dbgen.TouchDeviceParams{ID: id, LastSeenAt: &at, Column3: appVersion}))
}

func deviceFromRow(row dbgen.CoreDevice) Device {
	return Device{
		ID: row.ID, FacilityID: row.FacilityID, Name: row.Name,
		Kind: DeviceKind(row.Kind), Status: DeviceStatus(row.Status),
		EnrolledBy: uuidPtr(row.EnrolledBy), EnrolledAt: row.EnrolledAt,
		Model: row.Model, OSVersion: row.OsVersion, AppVersion: row.AppVersion, LastSeenAt: row.LastSeenAt,
		StatusChangedAt: row.StatusChangedAt, StatusChangedBy: uuidPtr(row.StatusChangedBy),
		StatusReason: row.StatusReason, CreatedAt: row.CreatedAt,
	}
}

// --- keys ---

func (s *PostgresStore) InsertDeviceKey(ctx context.Context, deviceID, facilityID uuid.UUID, pub ed25519.PublicKey) (DeviceKey, error) {
	row, err := s.q.InsertDeviceKey(ctx, dbgen.InsertDeviceKeyParams{
		DeviceID: deviceID, FacilityID: facilityID, PublicKey: []byte(pub),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return DeviceKey{}, ErrDeviceKeyInUse
		}
		return DeviceKey{}, translate(err)
	}
	return keyFromRow(row), nil
}

func (s *PostgresStore) LiveDeviceKey(ctx context.Context, deviceID uuid.UUID) (DeviceKey, error) {
	row, err := s.q.LiveDeviceKey(ctx, deviceID)
	if err != nil {
		return DeviceKey{}, translate(err)
	}
	return keyFromRow(row), nil
}

func (s *PostgresStore) RetireDeviceKeys(ctx context.Context, deviceID uuid.UUID, at time.Time, reason string) (int, error) {
	n, err := s.q.RetireDeviceKeys(ctx, dbgen.RetireDeviceKeysParams{DeviceID: deviceID, RetiredAt: &at, RetireReason: reason})
	if err != nil {
		return 0, translate(err)
	}
	return int(n), nil
}

func keyFromRow(row dbgen.CoreDeviceKey) DeviceKey {
	return DeviceKey{
		ID: row.ID, DeviceID: row.DeviceID, PublicKey: ed25519.PublicKey(row.PublicKey),
		CreatedAt: row.CreatedAt, RetiredAt: row.RetiredAt,
	}
}

// --- enrolment codes ---

func (s *PostgresStore) CreateDeviceEnrolment(ctx context.Context, e DeviceEnrolment, digest []byte) (DeviceEnrolment, error) {
	row, err := s.q.CreateDeviceEnrolment(ctx, dbgen.CreateDeviceEnrolmentParams{
		DeviceID: e.DeviceID, FacilityID: e.FacilityID, IssuedBy: e.IssuedBy,
		CodeDigest: digest, IssuedAt: e.IssuedAt, ExpiresAt: e.ExpiresAt,
	})
	if err != nil {
		return DeviceEnrolment{}, translate(err)
	}
	return enrolmentFromRow(row), nil
}

func (s *PostgresStore) DeviceEnrolmentByDigest(ctx context.Context, digest []byte) (DeviceEnrolment, error) {
	row, err := s.q.DeviceEnrolmentByDigest(ctx, digest)
	if err != nil {
		return DeviceEnrolment{}, translate(err)
	}
	return enrolmentFromRow(row), nil
}

func (s *PostgresStore) ConsumeDeviceEnrolment(ctx context.Context, id uuid.UUID, at time.Time) (bool, error) {
	n, err := s.q.ConsumeDeviceEnrolment(ctx, dbgen.ConsumeDeviceEnrolmentParams{ID: id, ConsumedAt: &at})
	if err != nil {
		return false, translate(err)
	}
	return n > 0, nil
}

func (s *PostgresStore) ExpirePendingEnrolments(ctx context.Context, deviceID uuid.UUID, at time.Time) (int, error) {
	n, err := s.q.ExpirePendingEnrolments(ctx, dbgen.ExpirePendingEnrolmentsParams{DeviceID: deviceID, ConsumedAt: &at})
	if err != nil {
		return 0, translate(err)
	}
	return int(n), nil
}

func enrolmentFromRow(row dbgen.CoreDeviceEnrolment) DeviceEnrolment {
	return DeviceEnrolment{
		ID: row.ID, DeviceID: row.DeviceID, FacilityID: row.FacilityID, IssuedBy: row.IssuedBy,
		IssuedAt: row.IssuedAt, ExpiresAt: row.ExpiresAt, ConsumedAt: row.ConsumedAt,
	}
}

// --- events ---

func (s *PostgresStore) RecordDeviceEvent(ctx context.Context, e DeviceEvent) error {
	detail, err := json.Marshal(e.Detail)
	if err != nil {
		return fmt.Errorf("encoding the event detail: %w", err)
	}
	err = s.q.InsertDeviceEvent(ctx, dbgen.InsertDeviceEventParams{
		DeviceID: e.DeviceID, FacilityID: e.FacilityID, ActorID: nullUUID(e.ActorID),
		Kind: e.Kind, Detail: detail, At: e.At,
	})
	if err != nil {
		return fmt.Errorf("recording the %s device event: %w", e.Kind, translate(err))
	}
	return nil
}

func (s *PostgresStore) DeviceEventsForDevice(ctx context.Context, deviceID uuid.UUID, limit int) ([]DeviceEvent, error) {
	rows, err := s.q.DeviceEventsForDevice(ctx, dbgen.DeviceEventsForDeviceParams{DeviceID: deviceID, Limit: int32(limit)}) //nolint:gosec // bounded by the service
	if err != nil {
		return nil, translate(err)
	}
	out := make([]DeviceEvent, 0, len(rows))
	for _, row := range rows {
		var detail map[string]any
		_ = json.Unmarshal(row.Detail, &detail)
		out = append(out, DeviceEvent{
			DeviceID: row.DeviceID, FacilityID: row.FacilityID, ActorID: uuidPtr(row.ActorID),
			Kind: row.Kind, Detail: detail, At: row.At,
		})
	}
	return out, nil
}

// --- sessions ---

func (s *PostgresStore) RevokeSessionsForDevice(ctx context.Context, deviceID uuid.UUID, at time.Time, by *uuid.UUID, reason string) (int, error) {
	n, err := s.q.RevokeSessionsForDevice(ctx, dbgen.RevokeSessionsForDeviceParams{
		DeviceID: nullUUID(&deviceID), RevokedAt: &at, RevokedBy: nullUUID(by), RevokeReason: reason,
	})
	if err != nil {
		return 0, translate(err)
	}
	return int(n), nil
}
