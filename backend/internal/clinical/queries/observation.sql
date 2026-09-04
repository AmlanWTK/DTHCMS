-- Observations and the registry that gives them meaning (CP42).

-- name: ObservationCodes :many
-- The whole registry, with each code's canonical unit. One query rather than three, because
-- a station app fetches this once on start-up and then validates every entry against it,
-- offline, for the rest of the clinic session.
SELECT c.code, c.category, c.value_type, c.dimension, c.loinc,
       c.display_en, c.display_bn, c.min_canonical, c.max_canonical, c.write_permission,
       -- COALESCE, because a unitless code has no canonical unit and sqlc types this
       -- column from the cast: without it the driver is handed a NULL it cannot scan into
       -- a string, and every read of the registry is a 500.
       COALESCE((SELECT u.code FROM core.unit u
                  WHERE u.dimension = c.dimension AND u.is_canonical), '')::text AS canonical_unit
  FROM core.observation_code c
 WHERE c.retired_at IS NULL
 ORDER BY c.category, c.code;

-- name: ObservationCodeByCode :one
SELECT c.code, c.category, c.value_type, c.dimension, c.loinc,
       c.display_en, c.display_bn, c.min_canonical, c.max_canonical, c.write_permission,
       c.retired_at,
       -- COALESCE, because a unitless code has no canonical unit and sqlc types this
       -- column from the cast: without it the driver is handed a NULL it cannot scan into
       -- a string, and every read of the registry is a 500.
       COALESCE((SELECT u.code FROM core.unit u
                  WHERE u.dimension = c.dimension AND u.is_canonical), '')::text AS canonical_unit
  FROM core.observation_code c
 WHERE c.code = $1;

-- name: Units :many
SELECT * FROM core.unit ORDER BY dimension, is_canonical DESC, code;

-- name: UnitByCode :one
SELECT * FROM core.unit WHERE code = $1;

-- name: ObservationByID :one
SELECT * FROM read.observation WHERE id = $1 AND facility_id = $2;

-- name: ObservationsForPatient :many
-- The current value of everything, newest first. Corrected and superseded rows are history,
-- and history is read through ObservationHistoryForCode.
SELECT * FROM read.observation
 WHERE patient_id = $1 AND facility_id = $2 AND status = 'ACTIVE'
   AND ($3::text = '' OR category = $3::text)
 ORDER BY effective_at DESC, code
 LIMIT $4;

-- name: ObservationsForVisit :many
SELECT * FROM read.observation
 WHERE visit_id = $1 AND facility_id = $2 AND status = 'ACTIVE'
 ORDER BY effective_at, code;

-- name: ObservationHistoryForCode :many
-- Every value ever recorded for one code on one patient, the replaced ones included. This is
-- what a trend line reads, and what somebody asking "what did it say before" reads.
SELECT * FROM read.observation
 WHERE patient_id = $1 AND facility_id = $2 AND code = $3
 ORDER BY effective_at DESC, recorded_at DESC
 LIMIT $4;
