-- Session queries.
--
-- Every one of these works in digests. No statement in this file accepts or returns a token,
-- because a token exists exactly twice: in the response that issues it, and in the
-- Authorization header that presents it.

-- name: CreateSession :one
INSERT INTO core.session (facility_id, user_id, token_digest, issued_at, expires_at, last_seen_at, user_agent)
VALUES ($1, $2, $3, $4, $5, $4, $6)
RETURNING *;

-- SessionByToken is the authentication path.
--
-- One equality lookup on a unique index, run on every authenticated request. It is not
-- filtered on revoked_at or expires_at here deliberately: the caller decides, and a row that
-- comes back revoked is a different thing from no row at all when something has to be logged.
--
-- name: SessionByToken :one
SELECT * FROM core.session WHERE token_digest = $1;

-- name: SessionByID :one
SELECT * FROM core.session WHERE id = $1;

-- name: TouchSession :exec
UPDATE core.session SET last_seen_at = $2 WHERE id = $1;

-- name: RevokeSession :exec
UPDATE core.session
   SET revoked_at = now(), revoked_by = $2, revoke_reason = $3
 WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeSessionsForUser :execrows
UPDATE core.session
   SET revoked_at = now(), revoked_by = $2, revoke_reason = $3
 WHERE user_id = $1 AND revoked_at IS NULL;

-- name: SessionsForUser :many
SELECT * FROM core.session
 WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
 ORDER BY last_seen_at DESC;

-- The id is supplied rather than defaulted, because a rotation has to name the successor
-- in the predecessor's replaced_by column in the same transaction that inserts it.
--
-- name: CreateRefreshToken :one
INSERT INTO core.refresh_token (id, facility_id, session_id, family_id, token_digest, issued_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: RefreshTokenByDigest :one
SELECT * FROM core.refresh_token WHERE token_digest = $1;

-- MarkRefreshUsed and RekeySession are the two halves of a rotation.
--
-- They are separate statements and must be run in one transaction. The intermediate states
-- are both wrong: marking used without inserting the successor locks the user out, and
-- inserting without marking leaves two live tokens in a lineage whose entire purpose is that
-- there is exactly one. The store method that calls them owns the transaction.
--
-- name: MarkRefreshUsed :exec
UPDATE core.refresh_token
   SET used_at = $2, replaced_by = $3
 WHERE id = $1 AND used_at IS NULL;

-- name: RekeySession :exec
UPDATE core.session
   SET token_digest = $2, expires_at = $3, last_seen_at = $4
 WHERE id = $1 AND revoked_at IS NULL;

-- Reuse detection calls both of these, in one transaction.
--
-- Two plain statements rather than one CTE that updates two tables: the CTE is cleverer and
-- the store has to hold a transaction either way, so the cleverness buys nothing and costs
-- the next person a minute working out what it does.
--
-- Revoking the tokens alone would not be enough. The access tokens already issued under
-- that family keep working until they expire, which is up to their full lifetime after the
-- theft was detected — so the sessions go too.

-- name: RevokeRefreshFamilyTokens :execrows
UPDATE core.refresh_token
   SET revoked_at = now(), revoke_reason = $2
 WHERE family_id = $1 AND revoked_at IS NULL;

-- name: RevokeSessionsInFamily :execrows
UPDATE core.session
   SET revoked_at = now(), revoke_reason = $2
 WHERE revoked_at IS NULL
   AND id IN (SELECT session_id FROM core.refresh_token WHERE family_id = $1);

-- name: RevokeRefreshForSession :exec
UPDATE core.refresh_token
   SET revoked_at = now(), revoke_reason = $2
 WHERE session_id = $1 AND revoked_at IS NULL;

-- name: RecordLoginAttempt :exec
INSERT INTO core.login_attempt (facility_id, employee_code, user_id, succeeded, failure_kind, client_digest, attempted_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- RecentFailuresForCode counts against what was typed, not against a user.
--
-- The code may name nobody. Counting only real accounts would answer "does this person work
-- here" by how quickly the server refuses.
--
-- name: RecentFailuresForCode :one
SELECT count(*) FROM core.login_attempt
 WHERE facility_id = $1 AND employee_code = $2 AND NOT succeeded AND attempted_at >= $3;

-- name: RecentFailuresForClient :one
SELECT count(*) FROM core.login_attempt
 WHERE client_digest = $1 AND NOT succeeded AND attempted_at >= $2;

-- CredentialsByCode is the login lookup.
--
-- Named separately from GetUserByEmployeeCode, which is identical today, because the two
-- have different futures: this one is allowed to read password_hash and the other is not.
-- Keeping them apart from the start means the day somebody narrows the ordinary lookup to
-- exclude the secret columns, there is already a query for the one caller that needs them.
--
-- name: CredentialsByCode :one
SELECT * FROM core.app_user
 WHERE facility_id = $1 AND employee_code = $2;

-- name: SetPasswordHash :exec
UPDATE core.app_user
   SET password_hash = $2, password_set_at = now(), updated_by = $3
 WHERE id = $1;
