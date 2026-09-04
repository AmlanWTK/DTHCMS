-- The station queue (CP39).

-- name: EnterQueue :one
INSERT INTO core.queue_entry (id, facility_id, visit_id, patient_id, station_code, position,
                              priority, priority_reason, entered_at, clinic_day)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: CallNextAtStation :one
SELECT * FROM core.call_next_at_station($1, $2, $3, $4);

-- name: QueueEntryByID :one
SELECT * FROM core.queue_entry WHERE id = $1 AND facility_id = $2;

-- name: StationQueue :many
SELECT * FROM core.queue_entry
 WHERE facility_id = $1 AND station_code = $2
   AND status IN ('waiting', 'called', 'in_service')
 ORDER BY priority DESC, entered_at, id;

-- name: QueueForVisit :many
SELECT * FROM core.queue_entry
 WHERE visit_id = $1 AND facility_id = $2
 ORDER BY position, entered_at;

-- name: StartQueueService :one
UPDATE core.queue_entry
   SET status = 'in_service', started_at = $3, encounter_id = $4
 WHERE id = $1 AND facility_id = $2 AND status = 'called'
RETURNING *;

-- name: LeaveQueue :one
UPDATE core.queue_entry
   SET status = $3, ended_at = $4, outcome = $5, outcome_reason = $6, rerouted_to = $7
 WHERE id = $1 AND facility_id = $2 AND status IN ('waiting', 'called', 'in_service')
RETURNING *;

-- name: StationSequence :many
SELECT position, station_code, required
  FROM core.station_sequence
 WHERE facility_id = $1 AND visit_type = $2
 ORDER BY position;

-- name: StationBoard :many
-- What the traffic board reads: one row per station, with the numbers a supervisor acts on.
SELECT station_code,
       count(*) FILTER (WHERE status = 'waiting')::bigint    AS waiting,
       count(*) FILTER (WHERE status = 'called')::bigint     AS called,
       count(*) FILTER (WHERE status = 'in_service')::bigint AS in_service,
       coalesce(max(EXTRACT(EPOCH FROM ($3::timestamptz - entered_at)))
                FILTER (WHERE status = 'waiting'), 0)::bigint AS longest_wait_seconds,
       coalesce(avg(EXTRACT(EPOCH FROM ($3::timestamptz - entered_at)))
                FILTER (WHERE status = 'waiting'), 0)::bigint AS average_wait_seconds
  FROM core.queue_entry
 WHERE facility_id = $1 AND clinic_day = $2
   AND status IN ('waiting', 'called', 'in_service')
 GROUP BY station_code
 ORDER BY station_code;
