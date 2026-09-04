-- Visits and encounters (CP38).

-- name: OpenVisit :one
INSERT INTO core.visit (facility_id, patient_id, visit_code, visit_type, chief_complaint,
                        clinic_day, opened_at, opened_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: NextVisitCode :one
SELECT core.next_visit_code($1, $2) AS code;

-- name: VisitByID :one
SELECT * FROM core.visit WHERE id = $1 AND facility_id = $2;

-- name: OpenVisitForPatient :one
SELECT * FROM core.visit WHERE patient_id = $1 AND facility_id = $2 AND status = 'open';

-- name: VisitsForPatient :many
SELECT * FROM core.visit
 WHERE patient_id = $1 AND facility_id = $2
 ORDER BY opened_at DESC
 LIMIT $3;

-- name: VisitsOnDay :many
SELECT * FROM core.visit
 WHERE facility_id = $1 AND clinic_day = $2
 ORDER BY opened_at;

-- name: CloseVisit :one
UPDATE core.visit
   SET status = 'closed', closed_at = $3, closed_by = $4,
       chief_complaint = $5, diagnoses = $6, plan = $7,
       next_review_days = $8, next_review_on = $9, updated_at = now()
 WHERE id = $1 AND facility_id = $2 AND status = 'open'
RETURNING *;

-- name: AbandonVisit :one
UPDATE core.visit
   SET status = 'abandoned', status_reason = $3, closed_at = $4, closed_by = $5, updated_at = now()
 WHERE id = $1 AND facility_id = $2 AND status = 'open'
RETURNING *;

-- name: ReopenVisit :one
UPDATE core.visit
   SET status = 'open', reopened_count = reopened_count + 1,
       status_reason = $3, closed_at = NULL, closed_by = NULL, updated_at = now()
 WHERE id = $1 AND facility_id = $2 AND status IN ('closed', 'abandoned')
RETURNING *;

-- name: StartEncounter :one
INSERT INTO core.encounter (id, facility_id, visit_id, patient_id, station_code,
                            started_at, started_by, started_role, device_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: FinishEncounter :one
UPDATE core.encounter
   SET status = $3, ended_at = $4, ended_by = $5, outcome = $6, notes = $7
 WHERE id = $1 AND facility_id = $2 AND status = 'in_progress'
RETURNING *;

-- name: EncounterByID :one
SELECT * FROM core.encounter WHERE id = $1 AND facility_id = $2;

-- name: EncountersForVisit :many
SELECT * FROM core.encounter WHERE visit_id = $1 AND facility_id = $2 ORDER BY started_at;

-- name: OpenEncounterAtStation :one
SELECT * FROM core.encounter
 WHERE visit_id = $1 AND facility_id = $2 AND station_code = $3 AND status = 'in_progress';

-- name: StationCodes :many
SELECT code FROM core.station WHERE facility_id = $1 ORDER BY sequence_hint;
