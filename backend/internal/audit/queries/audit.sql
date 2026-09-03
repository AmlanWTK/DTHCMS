-- The audit chain (CP22). Append and read; the schema forbids anything else.

-- name: AuditHead :one
-- The last link, for the recorder to chain onto. Called under the advisory lock.
SELECT seq, hash FROM ledger.audit_event ORDER BY seq DESC LIMIT 1;

-- name: AppendAuditEvent :one
INSERT INTO ledger.audit_event (
  seq, facility_id, kind, actor_user_id, actor_code, actor_role,
  target_user_id, target_code, patient_id, device_id, session_id,
  reason, details, client_digest, recorded_at, prev_hash, hash
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
)
RETURNING *;

-- name: AuditEventsPage :many
-- The viewer's query: newest first, filtered by whatever the person narrowed to. A NULL
-- filter means "any". `before` is the seq cursor for the next page.
SELECT * FROM ledger.audit_event
 WHERE facility_id = $1
   AND (sqlc.narg('before')::bigint IS NULL OR seq < sqlc.narg('before'))
   AND (sqlc.narg('kind')::text IS NULL OR kind = sqlc.narg('kind'))
   AND (sqlc.narg('actor_code')::text IS NULL OR actor_code = sqlc.narg('actor_code'))
   AND (sqlc.narg('subject_code')::text IS NULL
        OR actor_code = sqlc.narg('subject_code') OR target_code = sqlc.narg('subject_code'))
   AND (sqlc.narg('patient_id')::uuid IS NULL OR patient_id = sqlc.narg('patient_id'))
   AND (sqlc.narg('since')::timestamptz IS NULL OR recorded_at >= sqlc.narg('since'))
   AND (sqlc.narg('until')::timestamptz IS NULL OR recorded_at < sqlc.narg('until'))
 ORDER BY seq DESC
 LIMIT $2;

-- name: AuditEventsFrom :many
-- The verifier walks the chain forwards in slices.
SELECT * FROM ledger.audit_event WHERE seq >= $1 ORDER BY seq ASC LIMIT $2;

-- name: AuditEventCount :one
SELECT count(*) FROM ledger.audit_event;

-- name: AuditDefaultPartitionCount :one
-- Rows that landed in the safety-net partition: zero unless somebody forgot the monthly
-- partitions. The verifier reports it.
SELECT count(*) FROM ledger.audit_event_default;

-- name: OpenBreakGlass :one
INSERT INTO core.break_glass_access (
  facility_id, user_id, active_role, scope_kind, scope_ref, justification, granted_at, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: LinkBreakGlassAudit :exec
UPDATE core.break_glass_access SET audit_seq = $2 WHERE id = $1;

-- name: ActiveBreakGlass :many
SELECT * FROM core.break_glass_access
 WHERE facility_id = $1 AND ended_at IS NULL AND expires_at > $2
 ORDER BY granted_at DESC;

-- name: BreakGlassByID :one
SELECT * FROM core.break_glass_access WHERE id = $1;

-- name: BreakGlassForUser :many
-- What a person currently holds through the glass: the clinical checkpoints ask this.
SELECT * FROM core.break_glass_access
 WHERE user_id = $1 AND ended_at IS NULL AND expires_at > $2
 ORDER BY granted_at DESC;

-- name: EndBreakGlass :one
UPDATE core.break_glass_access
   SET ended_at = $2, ended_by = $3, end_reason = $4
 WHERE id = $1 AND ended_at IS NULL
RETURNING *;

-- name: AcknowledgeBreakGlass :one
UPDATE core.break_glass_access
   SET acknowledged_by = $2, acknowledged_at = $3
 WHERE id = $1 AND acknowledged_at IS NULL
RETURNING *;

-- name: RaiseAdminAlert :one
INSERT INTO core.admin_alert (facility_id, kind, severity, message_en, message_bn, reference, audit_seq, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: OpenAdminAlerts :many
SELECT * FROM core.admin_alert
 WHERE facility_id = $1 AND acknowledged_at IS NULL
 ORDER BY created_at DESC
 LIMIT $2;

-- name: AcknowledgeAdminAlert :one
UPDATE core.admin_alert
   SET acknowledged_by = $2, acknowledged_at = $3
 WHERE id = $1 AND facility_id = $4 AND acknowledged_at IS NULL
RETURNING *;
