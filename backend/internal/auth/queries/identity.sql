-- Identity queries.
--
-- Scoped to what CP15 owns: creating staff, moving them through their lifecycle, granting
-- and revoking roles, and resolving what a user may do. Authentication (CP16), session
-- handling (CP16) and enforcement (CP19/CP20) each add their own queries in their own
-- checkpoint, beside this file.

-- name: CreateUser :one
INSERT INTO core.app_user (
  facility_id, employee_code, name_en, name_bn, phone, email, created_by, updated_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
RETURNING *;

-- name: GetUser :one
SELECT * FROM core.app_user WHERE id = $1;

-- name: GetUserByEmployeeCode :one
SELECT * FROM core.app_user
 WHERE facility_id = $1 AND employee_code = $2;

-- name: ListUsers :many
SELECT * FROM core.app_user
 WHERE facility_id = $1
   AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
 ORDER BY employee_code;

-- SetUserStatus moves a user through the lifecycle.
--
-- The transition itself is checked by core.assert_user_status_transition, so an illegal
-- move raises rather than silently writing. The application checks first (auth.Lifecycle)
-- so the caller gets a 422 naming the transition rather than a 500 naming a trigger; the
-- database check is what makes that guarantee true for every other writer.
--
-- name: SetUserStatus :one
UPDATE core.app_user
   SET status = $2, status_reason = $3, updated_by = $4
 WHERE id = $1
RETURNING *;

-- name: GrantRole :one
INSERT INTO core.user_role (user_id, role_id, facility_id, granted_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- RevokeRole ends a grant without deleting it, so "who could sign on the 14th of March"
-- stays answerable.
--
-- name: RevokeRole :one
UPDATE core.user_role
   SET revoked_at = now(), revoked_by = $3, revoke_reason = $4
 WHERE user_id = $1 AND role_id = $2 AND revoked_at IS NULL
RETURNING *;

-- name: RolesForUser :many
SELECT r.*
  FROM core.role r
  JOIN core.user_role ur ON ur.role_id = r.id
 WHERE ur.user_id = $1 AND ur.revoked_at IS NULL
 ORDER BY r.code;

-- PermissionsForUser resolves the union across every live role [R-02].
--
-- Two conditions carry weight here. `ur.revoked_at IS NULL` means a revoked role stops
-- granting immediately rather than at the next session refresh. `u.status = 'active'`
-- means suspending an account takes effect at once, without walking its grants — which is
-- what makes suspension usable in the minute it is needed.
--
-- name: PermissionsForUser :many
SELECT DISTINCT rp.permission_code
  FROM core.app_user u
  JOIN core.user_role ur ON ur.user_id = u.id AND ur.revoked_at IS NULL
  JOIN core.role_permission rp ON rp.role_id = ur.role_id
 WHERE u.id = $1 AND u.status = 'active'
 ORDER BY rp.permission_code;

-- name: GrantHistoryForUser :many
SELECT ur.*, r.code AS role_code
  FROM core.user_role ur
  JOIN core.role r ON r.id = ur.role_id
 WHERE ur.user_id = $1
 ORDER BY ur.granted_at DESC;

-- name: ListRoles :many
SELECT * FROM core.role ORDER BY code;

-- name: GetRoleByCode :one
SELECT * FROM core.role WHERE code = $1;

-- name: ListPermissions :many
SELECT * FROM core.permission ORDER BY code;

-- name: PermissionsForRole :many
SELECT p.*
  FROM core.permission p
  JOIN core.role_permission rp ON rp.permission_code = p.code
  JOIN core.role r ON r.id = rp.role_id
 WHERE r.code = $1
 ORDER BY p.code;

-- name: ListStations :many
SELECT * FROM core.station
 WHERE facility_id = $1 AND is_active
 ORDER BY sequence_hint;

-- SetStationStaffed turns a station on or off for the queue. A station nobody works must
-- not receive patients (§5.2).
--
-- name: SetStationStaffed :one
UPDATE core.station
   SET is_staffed = $3, updated_by = $4
 WHERE facility_id = $1 AND code = $2
RETURNING *;
