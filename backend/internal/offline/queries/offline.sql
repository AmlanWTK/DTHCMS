-- The offline sync protocol (CP65).

-- name: DeviceForSync :one
-- Who is sending, and whether they may. The status decides between accepting and quarantining, so
-- it is read once per batch rather than per event — a device does not get revoked halfway through
-- fifty blood pressures, and reading it fifty times would only make the answer inconsistent.
SELECT d.id, d.facility_id, d.status, d.name, coalesce(d.status_reason, '') AS status_reason
  FROM core.device d
 WHERE d.id = $1;

-- name: BatchReceipt :one
-- What happened to a batch, for a client that lost the response.
--
-- The case this exists for is not a duplicate submission — the ledger absorbs those. It is the
-- client that sent fifty events, the server processed them, and the answer was lost on the way
-- back. Without this the client can only resend and hope, or drop and hope.
SELECT id, device_id, user_id, facility_id, received_at, closed_at, client_clock, skew_ms,
       events, accepted, duplicated, rejected, quarantined, blocked
  FROM ops.sync_batch
 WHERE id = $1;

-- name: RecordBatch :one
-- Opened with nothing counted yet, and closed below once every event has an answer.
--
-- Two reasons for the split, and the second is what made it necessary rather than tidy. A
-- quarantined event points at its batch, so the batch must exist before the first one is held. And
-- a crash halfway through leaves a receipt saying nothing was processed — honest, and what a client
-- should re-send against, where a missing receipt would leave it unable to tell a batch that was
-- never seen from one that was half done.
INSERT INTO ops.sync_batch
  (id, device_id, user_id, facility_id, received_at, client_clock, skew_ms)
VALUES ($1, $2, $3, $4, sqlc.arg(received_at), sqlc.narg(client_clock), sqlc.narg(skew_ms))
RETURNING id, received_at;

-- name: CloseBatch :exec
UPDATE ops.sync_batch
   SET closed_at = sqlc.arg(now), events = sqlc.arg(events), accepted = sqlc.arg(accepted),
       duplicated = sqlc.arg(duplicated), rejected = sqlc.arg(rejected),
       quarantined = sqlc.arg(quarantined), blocked = sqlc.arg(blocked)
 WHERE id = sqlc.arg(id);

-- name: RecordResult :exec
INSERT INTO ops.sync_result (batch_id, event_id, outcome, reason_code, reason, global_seq)
VALUES ($1, $2, $3, $4, $5, sqlc.narg(global_seq))
ON CONFLICT (batch_id, event_id) DO NOTHING;

-- name: ResultsFor :many
SELECT batch_id, event_id, outcome, reason_code, reason, global_seq
  FROM ops.sync_result
 WHERE batch_id = $1
 ORDER BY event_id;

-- name: Quarantine :one
INSERT INTO ops.sync_quarantine
  (id, batch_id, event_id, device_id, user_id, facility_id,
   reason_code, reason, envelope, event_type, occurred_at, patient_id, held_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, sqlc.narg(patient_id), sqlc.arg(held_at))
ON CONFLICT DO NOTHING
RETURNING id;

-- name: HeldEvents :many
-- The triage list. Denormalised columns so a supervisor sees "eleven blood pressures and two
-- weights" rather than eleven identifiers, without opening a single envelope — the envelope holds
-- clinical values, and a list view should not need to.
SELECT q.id, q.batch_id, q.event_id, q.device_id, q.user_id, q.facility_id,
       q.reason_code, q.reason, q.event_type, q.occurred_at, q.patient_id,
       q.held_at, q.status, q.resolved_at, q.resolved_by, q.resolution_note,
       q.released_event_id,
       coalesce(d.name, '')          AS device_name,
       coalesce(u.employee_code, '') AS operator_code,
       coalesce(u.name_en, '')       AS operator_name_en,
       coalesce(u.name_bn, '')       AS operator_name_bn
  FROM ops.sync_quarantine q
  LEFT JOIN core.device d   ON d.id = q.device_id
  LEFT JOIN core.app_user u ON u.id = q.user_id
 WHERE q.facility_id = sqlc.arg(facility_id)
   AND (sqlc.narg(status)::text IS NULL OR q.status = sqlc.narg(status)::text)
   AND (sqlc.narg(device_id)::uuid IS NULL OR q.device_id = sqlc.narg(device_id)::uuid)
 ORDER BY q.held_at DESC
 LIMIT sqlc.arg(row_limit);

-- name: HeldEvent :one
-- One held event **with its envelope**. Separate from the list on purpose: the list is a triage
-- view and needs no clinical content, and this one is the read that shows a blood pressure to a
-- person deciding whether it belongs in a record.
SELECT q.id, q.batch_id, q.event_id, q.device_id, q.user_id, q.facility_id,
       q.reason_code, q.reason, q.envelope, q.event_type, q.occurred_at, q.patient_id,
       q.held_at, q.status, q.resolved_at, q.resolved_by, q.resolution_note,
       q.released_event_id,
       coalesce(d.name, '')          AS device_name,
       coalesce(u.employee_code, '') AS operator_code,
       coalesce(u.name_en, '')       AS operator_name_en,
       coalesce(u.name_bn, '')       AS operator_name_bn
  FROM ops.sync_quarantine q
  LEFT JOIN core.device d   ON d.id = q.device_id
  LEFT JOIN core.app_user u ON u.id = q.user_id
 WHERE q.id = sqlc.arg(id) AND q.facility_id = sqlc.arg(facility_id);

-- name: ReleaseHeldEvent :one
-- Marked released only once the append has actually happened, and in the same transaction as it.
-- The other order would let "released" be a status somebody set while the append failed, and the
-- measurement would be marked recovered and not be there — worse than never releasing it, because
-- now nobody is looking. Invariant 95 checks the whole table for the same reason.
UPDATE ops.sync_quarantine
   SET status = 'RELEASED', resolved_at = sqlc.arg(now), resolved_by = sqlc.arg(resolved_by),
       resolution_note = sqlc.arg(note), released_event_id = sqlc.arg(released_event_id)
 WHERE id = sqlc.arg(id) AND facility_id = sqlc.arg(facility_id) AND status = 'HELD'
RETURNING id, status, released_event_id;

-- name: DiscardHeldEvent :one
UPDATE ops.sync_quarantine
   SET status = 'DISCARDED', resolved_at = sqlc.arg(now), resolved_by = sqlc.arg(resolved_by),
       resolution_note = sqlc.arg(note)
 WHERE id = sqlc.arg(id) AND facility_id = sqlc.arg(facility_id) AND status = 'HELD'
RETURNING id, status;

-- name: SyncState :one
SELECT device_id, last_pulled_seq, last_pulled_at, last_pushed_at, last_skew_ms,
       pushed_total, quarantined_total
  FROM ops.device_sync_state
 WHERE device_id = $1;

-- name: NotePush :exec
INSERT INTO ops.device_sync_state
  (device_id, last_pushed_at, last_skew_ms, pushed_total, quarantined_total)
VALUES (sqlc.arg(device_id), sqlc.arg(now), sqlc.narg(skew_ms),
        sqlc.arg(pushed), sqlc.arg(quarantined))
ON CONFLICT (device_id) DO UPDATE SET
  last_pushed_at = EXCLUDED.last_pushed_at,
  last_skew_ms = EXCLUDED.last_skew_ms,
  pushed_total = ops.device_sync_state.pushed_total + EXCLUDED.pushed_total,
  quarantined_total = ops.device_sync_state.quarantined_total + EXCLUDED.quarantined_total;

-- name: NotePull :exec
INSERT INTO ops.device_sync_state (device_id, last_pulled_seq, last_pulled_at)
VALUES (sqlc.arg(device_id), sqlc.arg(seq), sqlc.arg(now))
ON CONFLICT (device_id) DO UPDATE SET
  -- Never moves backwards. A client that replays an old cursor is asking for old events, which is
  -- fine; recording that as its position would make the *next* pull re-send everything since.
  last_pulled_seq = greatest(ops.device_sync_state.last_pulled_seq, EXCLUDED.last_pulled_seq),
  last_pulled_at = EXCLUDED.last_pulled_at;

-- name: EventsSince :many
-- The incremental pull, scoped to the facility and to the event types the caller may read.
--
-- **Fail closed**: the type list is passed in by the service from a declared map, so an event type
-- nobody has decided about is not pullable at all. The alternative — everything except a denylist
-- — means a type added next month is on every phone in the clinic before anybody notices.
--
-- The payload comes with it: a station that has pulled an observation needs to render it offline,
-- and a reference to a value it cannot read is not synchronisation.
SELECT e.event_id, e.global_seq, e.aggregate_type, e.aggregate_id, e.patient_id, e.visit_id,
       e.event_type, e.event_version, e.occurred_at, e.recorded_at, e.payload,
       e.actor_user_id, e.actor_role, e.actor_station, e.facility_id, e.source
  FROM ledger.event e
 WHERE e.global_seq > sqlc.arg(since)
   AND e.facility_id = sqlc.arg(facility_id)
   AND e.event_type = ANY(sqlc.arg(event_types)::text[])
 ORDER BY e.global_seq
 LIMIT sqlc.arg(row_limit);

-- name: LatestGlobalSeq :one
-- Where the ledger is now, so a client knows whether its page was the last one without asking for
-- an empty one.
SELECT coalesce(max(global_seq), 0)::bigint AS latest FROM ledger.event;

-- name: ReferenceVersions :many
-- What each reference catalogue looks like now, so a phone holding it for a morning can tell in
-- one small request whether any of it has moved.
--
-- A count and a fingerprint rather than a timestamp, because several of these tables have no
-- updated_at and adding one to each would be five migrations to answer a question a hash already
-- answers. The fingerprint changes when any row does; it does not say which.
SELECT 'observation_code' AS catalogue,
       count(*)::bigint   AS rows,
       md5(coalesce(string_agg(code || ':' || value_type || ':' ||
                               coalesce(dimension, ''), ',' ORDER BY code), ''))::text AS fingerprint
  FROM core.observation_code WHERE retired_at IS NULL
UNION ALL
SELECT 'food', count(*)::bigint,
       md5(coalesce(string_agg(code || ':' || name_en, ',' ORDER BY code), ''))::text
  FROM core.food WHERE retired_at IS NULL
UNION ALL
SELECT 'exercise', count(*)::bigint,
       md5(coalesce(string_agg(code || ':' || name_en, ',' ORDER BY code), ''))::text
  FROM core.exercise WHERE retired_at IS NULL
UNION ALL
SELECT 'contraindication', count(*)::bigint,
       md5(coalesce(string_agg(code || ':' || name_en, ',' ORDER BY code), ''))::text
  FROM core.contraindication WHERE retired_at IS NULL
UNION ALL
SELECT 'instrument', count(*)::bigint,
       md5(coalesce(string_agg(code || ':' || name_en, ',' ORDER BY code), ''))::text
  FROM core.instrument WHERE retired_at IS NULL
UNION ALL
SELECT 'counseling_template', count(*)::bigint,
       md5(coalesce(string_agg(t.code || ':' || v.version::text, ',' ORDER BY t.code), ''))::text
  FROM core.counseling_template t
  JOIN core.counseling_template_version v
    ON v.template_id = t.id AND v.published_at IS NOT NULL
ORDER BY catalogue;

-- name: QuarantineLoad :one
-- How much of this device's quarantine allowance is spent, and what the allowance is.
--
-- Both in one row, from one place, because the cap belongs to the database (00050) and a constant
-- in Go beside it would be a second number that agrees until somebody changes one. Read once per
-- batch for the same reason the device's status is: it cannot meaningfully change halfway through
-- fifty blood pressures, and reading it fifty times would only make the answer inconsistent.
--
-- `status = 'HELD'` and not every row this device ever had held: the allowance is cleared by a
-- person working through the triage list, which is the right thing to tie it to — the resource
-- actually being protected is somebody's attention.
SELECT count(*)::bigint AS held, ops.quarantine_cap()::integer AS cap
  FROM ops.sync_quarantine
 WHERE device_id = $1 AND status = 'HELD';
