-- The projection register and its dead-letter queue (CP25).

-- name: RegisterProjection :one
-- Insert the projection's row, or return the one already there. The version is not
-- overwritten here: a version that differs from the code's is exactly what the runner has
-- to notice, and silently updating it would erase the signal.
INSERT INTO read.projection_state (name, version, mode)
VALUES ($1, $2, $3)
ON CONFLICT (name) DO UPDATE SET mode = EXCLUDED.mode, updated_at = now()
RETURNING *;

-- name: ProjectionState :one
SELECT * FROM read.projection_state WHERE name = $1;

-- name: AllProjectionState :many
SELECT * FROM read.projection_state ORDER BY name;

-- name: AdvanceCheckpoint :exec
-- GREATEST rather than assignment: a runner that re-applied a batch after a crash must not
-- move its checkpoint backwards.
UPDATE read.projection_state
   SET checkpoint = GREATEST(checkpoint, $2),
       applied_at = GREATEST(COALESCE(applied_at, '-infinity'::timestamptz), $3),
       updated_at = now()
 WHERE name = $1;

-- name: SetProjectionStatus :exec
UPDATE read.projection_state SET status = $2, updated_at = now() WHERE name = $1;

-- name: BeginRebuild :exec
UPDATE read.projection_state
   SET status = 'rebuilding', checkpoint = 0, applied_at = NULL, version = $2, updated_at = now()
 WHERE name = $1;

-- name: FinishRebuild :exec
UPDATE read.projection_state
   SET status = $2, checkpoint = $3, applied_at = $4, rebuilt_at = now(), updated_at = now()
 WHERE name = $1;

-- name: RecordDeadLetter :exec
INSERT INTO read.projection_dead_letter (projection, global_seq, event_id, event_type, error, attempts)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (projection, global_seq) DO UPDATE
   SET error = EXCLUDED.error, attempts = read.projection_dead_letter.attempts + 1,
       failed_at = now(), resolved_at = NULL, resolution = NULL;

-- name: OpenDeadLetters :many
SELECT * FROM read.projection_dead_letter
 WHERE projection = $1 AND resolved_at IS NULL
 ORDER BY global_seq;

-- name: CountOpenDeadLetters :one
SELECT count(*) FROM read.projection_dead_letter WHERE projection = $1 AND resolved_at IS NULL;

-- name: ResolveDeadLetter :exec
UPDATE read.projection_dead_letter
   SET resolved_at = now(), resolution = $2
 WHERE id = $1 AND resolved_at IS NULL;

-- name: ClearDeadLetters :exec
-- A rebuild starts from nothing, so the failures of the previous derivation are history.
DELETE FROM read.projection_dead_letter WHERE projection = $1;

-- name: LedgerHead :one
-- The highest global sequence in the ledger, for the lag metric. Zero when empty.
SELECT COALESCE(max(global_seq), 0)::bigint FROM ledger.event_key;
