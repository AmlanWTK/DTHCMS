-- The Clinic Traffic Control board (CP40).
--
-- Every read here goes through `core.board_row`, never through `core.queue_entry` joined to
-- `core.visit`. The view is an allowlist of columns safe to project onto a wall in a public
-- waiting area, and `core.assert_the_board_shows_nothing_clinical()` fails the deployment if
-- it ever grows. Querying the base tables here would route around both.

-- name: BoardSetting :one
SELECT * FROM core.board_setting WHERE facility_id = $1;

-- name: BoardRows :many
-- Everyone still in the building, in the order the board draws them.
--
-- `position` before `station_code` so a station's column appears where the journey puts it:
-- registration on the left, follow-up booking on the right, which is the order staff walk.
SELECT * FROM core.board_row
 WHERE facility_id = $1 AND clinic_day = $2
   AND status IN ('waiting', 'called', 'in_service')
 ORDER BY position, station_code, priority DESC, entered_at, entry_id;

-- name: RerouteQueueEntry :one
SELECT * FROM core.reroute_queue_entry($1, $2, $3, $4, $5, $6, $7);

-- name: StationDepth :one
-- How many are waiting at one station right now. The board's realtime delta carries this so
-- a screen can redraw one column without a round trip; a count is not clinical.
SELECT count(*)::bigint FROM core.queue_entry
 WHERE facility_id = $1 AND station_code = $2 AND status = 'waiting';
