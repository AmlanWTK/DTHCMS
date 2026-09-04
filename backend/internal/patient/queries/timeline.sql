-- The patient timeline (CP37, §8).
--
-- The permission filter is in SQL, not in Go. A post-filter is how a count comes back larger
-- than the rows returned, and how a paging cursor skips what it hid.

-- name: PatientTimeline :many
SELECT occurred_at, recorded_at, category, kind, label_en, label_bn, value, unit, value_num,
       actor_id, actor_code, actor_role, actor_station, device_id, source, flags,
       event_id, event_type, global_seq, item
  FROM read.patient_timeline
 WHERE patient_id = $1
   AND facility_id = $2
   AND occurred_at >= $3
   AND occurred_at < $4
   AND (cardinality(@categories::text[]) = 0 OR category = ANY(@categories::text[]))
   AND needs_permission = ANY(@permissions::text[])
 ORDER BY occurred_at DESC, id DESC
 LIMIT $5 OFFSET $6;

-- name: PatientTimelineCount :one
SELECT count(*)
  FROM read.patient_timeline
 WHERE patient_id = $1
   AND facility_id = $2
   AND occurred_at >= $3
   AND occurred_at < $4
   AND (cardinality(@categories::text[]) = 0 OR category = ANY(@categories::text[]))
   AND needs_permission = ANY(@permissions::text[]);
