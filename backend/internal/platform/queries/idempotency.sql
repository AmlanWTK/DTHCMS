-- Idempotency records (CP24). Claimed before the handler runs, completed after it.

-- name: ClaimIdempotency :one
-- Insert the claim, or return nothing if the key is already held. ON CONFLICT DO NOTHING
-- rather than a SELECT-then-INSERT: two concurrent retries of one request must not both
-- believe they are the first.
INSERT INTO ops.idempotency_record (facility_id, user_id, key, fingerprint, expires_at, claimed_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id, key) DO NOTHING
RETURNING *;

-- name: IdempotencyRecord :one
SELECT * FROM ops.idempotency_record WHERE user_id = $1 AND key = $2;

-- name: CompleteIdempotency :exec
UPDATE ops.idempotency_record
   SET state = 'complete', status = $3, headers = $4, body = $5, completed_at = $6
 WHERE user_id = $1 AND key = $2;

-- name: ReleaseIdempotency :exec
-- A handler that failed leaves no claim behind: the client may retry, and a claim nobody
-- completed would refuse them until it expired.
DELETE FROM ops.idempotency_record
 WHERE user_id = $1 AND key = $2 AND state = 'in_progress';

-- name: PurgeExpiredIdempotency :one
SELECT ops.purge_expired_idempotency($1);
