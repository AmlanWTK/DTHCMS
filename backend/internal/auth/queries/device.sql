-- Device queries (CP18).
--
-- Nothing here deletes. A device is revoked, a key retired, a code consumed.

-- name: CreateDevice :one
INSERT INTO core.device (facility_id, name, kind, status_changed_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: DeviceByID :one
SELECT * FROM core.device WHERE id = $1;

-- name: DevicesForFacility :many
SELECT * FROM core.device WHERE facility_id = $1 ORDER BY lower(name);

-- ActivateDevice moves a device to active on enrolment, in the same statement that records
-- what it said about itself. A pending device becomes active; an active or suspended one
-- being re-enrolled (reinstalled app, new key) stays or becomes active and keeps its
-- original enrolled_at. A revoked or lost device cannot be brought back this way — that is
-- what terminal means — and the WHERE returns no row for it.
--
-- name: ActivateDevice :one
UPDATE core.device
   SET status = 'active', enrolled_by = COALESCE(enrolled_by, $2), enrolled_at = COALESCE(enrolled_at, $3),
       model = $4, os_version = $5, app_version = $6, last_seen_at = $3,
       status_changed_at = $3, status_changed_by = $2, status_reason = ''
 WHERE id = $1 AND status IN ('pending', 'active', 'suspended')
RETURNING *;

-- name: ChangeDeviceStatus :one
UPDATE core.device
   SET status = $2, status_changed_at = $5, status_changed_by = $3, status_reason = $4
 WHERE id = $1
RETURNING *;

-- TouchDevice records the request just verified: when, and what version of the app made
-- it. The version travels with every signed request so the admin screen is current
-- without an update endpoint.
--
-- name: TouchDevice :exec
UPDATE core.device
   SET last_seen_at = $2,
       app_version = CASE WHEN $3::text = '' THEN app_version ELSE $3::text END
 WHERE id = $1;

-- --- keys ---

-- name: InsertDeviceKey :one
INSERT INTO core.device_key (device_id, facility_id, public_key)
VALUES ($1, $2, $3)
RETURNING *;

-- name: LiveDeviceKey :one
SELECT * FROM core.device_key WHERE device_id = $1 AND retired_at IS NULL;

-- name: RetireDeviceKeys :execrows
UPDATE core.device_key
   SET retired_at = $2, retire_reason = $3
 WHERE device_id = $1 AND retired_at IS NULL;

-- --- enrolment codes ---

-- name: CreateDeviceEnrolment :one
INSERT INTO core.device_enrolment (device_id, facility_id, issued_by, code_digest, issued_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: DeviceEnrolmentByDigest :one
SELECT * FROM core.device_enrolment WHERE code_digest = $1;

-- name: ConsumeDeviceEnrolment :execrows
UPDATE core.device_enrolment
   SET consumed_at = $2
 WHERE id = $1 AND consumed_at IS NULL;

-- ExpirePendingEnrolments consumes every open code for a device, so that issuing a new one
-- leaves exactly one that works.
--
-- name: ExpirePendingEnrolments :execrows
UPDATE core.device_enrolment
   SET consumed_at = $2
 WHERE device_id = $1 AND consumed_at IS NULL;

-- --- events ---

-- name: InsertDeviceEvent :exec
INSERT INTO core.device_event (device_id, facility_id, actor_id, kind, detail, at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: DeviceEventsForDevice :many
SELECT * FROM core.device_event WHERE device_id = $1 ORDER BY at DESC, id DESC LIMIT $2;

-- --- sessions ---

-- name: SessionsForDevice :many
SELECT * FROM core.session
 WHERE device_id = $1 AND revoked_at IS NULL AND expires_at > $2
 ORDER BY issued_at DESC;

-- RevokeSessionsForDevice ends every live session on a device the moment it is revoked, so
-- that revocation is effective on the next request rather than at the next refresh.
--
-- name: RevokeSessionsForDevice :execrows
UPDATE core.session
   SET revoked_at = $2, revoked_by = $3, revoke_reason = $4
 WHERE device_id = $1 AND revoked_at IS NULL;
