-- Second-factor queries (CP17).
--
-- Seeds arrive and leave this file sealed; codes and tokens arrive as digests. Nothing here
-- accepts a plaintext seed, code or token.

-- ---------------------------------------------------------------------------
-- TOTP seeds
-- ---------------------------------------------------------------------------

-- Starting or restarting an enrolment replaces the row wholesale: a new seed, unconfirmed,
-- no replay state, no disablement. A confirmed seed is never overwritten by this — the
-- service checks first — but the statement is written so that even a mistaken call cannot
-- turn a confirmed factor into an unconfirmed one.
--
-- name: BeginTotpEnrolment :one
INSERT INTO core.user_totp (user_id, facility_id, secret_sealed, key_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE
   SET secret_sealed = EXCLUDED.secret_sealed,
       key_id        = EXCLUDED.key_id,
       confirmed_at  = NULL,
       last_used_step = NULL,
       disabled_at   = NULL,
       disabled_by   = NULL,
       disable_reason = ''
 WHERE core.user_totp.confirmed_at IS NULL OR core.user_totp.disabled_at IS NOT NULL
RETURNING *;

-- name: TotpByUser :one
SELECT * FROM core.user_totp WHERE user_id = $1;

-- name: ConfirmTotp :execrows
UPDATE core.user_totp
   SET confirmed_at = $2, last_used_step = $3
 WHERE user_id = $1 AND confirmed_at IS NULL AND disabled_at IS NULL;

-- The replay guard and the re-seal, in one statement. The step only moves forward; a row
-- whose step has already passed the one offered is left alone and the caller sees zero rows,
-- which is the refusal.
--
-- name: RecordTotpUse :execrows
UPDATE core.user_totp
   SET last_used_step = $2, secret_sealed = $3, key_id = $4
 WHERE user_id = $1 AND (last_used_step IS NULL OR last_used_step < $2);

-- name: DisableTotp :execrows
UPDATE core.user_totp
   SET disabled_at = now(), disabled_by = $2, disable_reason = $3
 WHERE user_id = $1 AND disabled_at IS NULL;

-- ---------------------------------------------------------------------------
-- Recovery codes
-- ---------------------------------------------------------------------------

-- name: InsertRecoveryCode :exec
INSERT INTO core.recovery_code (user_id, facility_id, batch_id, code_digest)
VALUES ($1, $2, $3, $4);

-- name: RevokeRecoveryCodes :execrows
UPDATE core.recovery_code
   SET revoked_at = now()
 WHERE user_id = $1 AND used_at IS NULL AND revoked_at IS NULL;

-- name: RecoveryCodeByDigest :one
SELECT * FROM core.recovery_code WHERE code_digest = $1;

-- Spending is conditional on the row still being live, so two concurrent presentations of
-- one code cannot both succeed: the second UPDATE finds used_at set and touches nothing.
--
-- name: UseRecoveryCode :execrows
UPDATE core.recovery_code
   SET used_at = now(), used_from_client = $2
 WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL;

-- name: CountLiveRecoveryCodes :one
SELECT count(*) FROM core.recovery_code
 WHERE user_id = $1 AND used_at IS NULL AND revoked_at IS NULL;

-- ---------------------------------------------------------------------------
-- Short-lived tokens
-- ---------------------------------------------------------------------------

-- name: CreateShortToken :one
INSERT INTO core.short_token (facility_id, user_id, session_id, kind, purpose, token_digest, client_digest, issued_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ShortTokenByDigest :one
SELECT * FROM core.short_token WHERE token_digest = $1;

-- name: ConsumeShortToken :execrows
UPDATE core.short_token
   SET consumed_at = $2
 WHERE id = $1 AND consumed_at IS NULL;

-- name: RecordShortTokenFailure :one
UPDATE core.short_token
   SET failures = failures + 1
 WHERE id = $1
RETURNING failures;

-- ---------------------------------------------------------------------------
-- Security events
-- ---------------------------------------------------------------------------

-- name: InsertSecurityEvent :exec
INSERT INTO core.security_event (facility_id, user_id, session_id, actor_id, kind, outcome, detail, client_digest, at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: SecurityEventsForUser :many
SELECT * FROM core.security_event
 WHERE user_id = $1
 ORDER BY at DESC, id DESC
 LIMIT $2;
