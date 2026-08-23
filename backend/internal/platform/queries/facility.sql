-- Facility lookups.
--
-- Facilities are platform-level reference data: every module needs a facility_id, and
-- rendering any clinical timestamp needs the facility's timezone. They are read
-- constantly and written almost never, which is why the queries live here rather than
-- in a domain module that would own them exclusively.

-- name: GetFacilityByID :one
SELECT * FROM core.facility WHERE id = $1;

-- name: GetFacilityByCode :one
SELECT * FROM core.facility WHERE code = $1;

-- name: ListActiveFacilities :many
SELECT * FROM core.facility WHERE is_active ORDER BY code;
