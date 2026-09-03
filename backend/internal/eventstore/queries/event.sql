-- The clinical event ledger (CP23). Append and read; the schema forbids anything else.

-- name: AggregateHead :one
-- The last link of one aggregate's chain. Called under the aggregate's advisory lock.
SELECT sequence, hash, global_seq FROM ledger.event_key
 WHERE aggregate_type = $1 AND aggregate_id = $2
 ORDER BY sequence DESC LIMIT 1;

-- name: EventKeyByID :one
SELECT * FROM ledger.event_key WHERE event_id = $1;

-- name: AppendEvent :one
INSERT INTO ledger.event (
  global_seq, event_id, aggregate_type, aggregate_id, sequence, patient_id, visit_id,
  event_type, event_version, occurred_at, recorded_at,
  actor_user_id, actor_device_id, actor_role, actor_station, facility_id, source,
  payload, previous, correction, metadata, prev_hash, hash
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23
)
RETURNING *;

-- name: InsertEventKey :exec
INSERT INTO ledger.event_key (event_id, aggregate_type, aggregate_id, sequence, global_seq, recorded_at, hash, facility_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: EventByID :one
SELECT e.* FROM ledger.event e JOIN ledger.event_key k ON k.global_seq = e.global_seq AND k.recorded_at = e.recorded_at
 WHERE k.event_id = $1;

-- name: EventsForAggregate :many
-- Replay order: the aggregate's own sequence.
SELECT * FROM ledger.event
 WHERE aggregate_type = $1 AND aggregate_id = $2 AND sequence >= $3
 ORDER BY sequence ASC
 LIMIT $4;

-- name: EventsFromGlobal :many
-- Projection order (§7.8): global sequence.
SELECT * FROM ledger.event WHERE global_seq >= $1 ORDER BY global_seq ASC LIMIT $2;

-- name: EventsForPatient :many
SELECT * FROM ledger.event
 WHERE patient_id = $1
 ORDER BY occurred_at DESC, global_seq DESC
 LIMIT $2;

-- name: Aggregates :many
-- Every aggregate with at least one event, for the verifier's walk. Paged by the pair.
SELECT aggregate_type, aggregate_id, max(sequence)::bigint AS head_sequence
  FROM ledger.event_key
 WHERE (aggregate_type, aggregate_id) > (sqlc.arg('after_type')::text, sqlc.arg('after_id')::uuid)
 GROUP BY aggregate_type, aggregate_id
 ORDER BY aggregate_type, aggregate_id
 LIMIT $1;

-- name: EventCount :one
SELECT count(*) FROM ledger.event;

-- name: EventDefaultPartitionCount :one
SELECT count(*) FROM ledger.event_default;

-- name: HashesForDay :many
-- The day's events in global order, for the anchor. Bounded by recorded_at so the query
-- prunes to the month's partition.
SELECT global_seq, hash FROM ledger.event
 WHERE recorded_at >= $1 AND recorded_at < $2
 ORDER BY global_seq ASC;

-- name: InsertAnchor :one
INSERT INTO ledger.chain_anchor (day, facility_id, event_count, first_global_seq, last_global_seq, prev_anchor, anchor, computed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: AnchorForDay :one
SELECT * FROM ledger.chain_anchor WHERE day = $1;

-- name: LatestAnchorBefore :one
SELECT * FROM ledger.chain_anchor WHERE day < $1 ORDER BY day DESC LIMIT 1;

-- name: Anchors :many
SELECT * FROM ledger.chain_anchor ORDER BY day ASC;
