-- The attribution directory (CP61).

-- name: DirectoryStaff :many
-- Everybody who has ever recorded anything, deactivated staff included.
--
-- **Deactivated people are in the list on purpose.** Attribution on a value taken last March
-- names whoever took it, and half of what a reviewer asks about is somebody who has since left.
-- A directory that listed only current staff would render a blank for exactly the person the
-- question is about.
--
-- Nothing sensitive is here: a name, a staff code and whether they are still with the clinic. No
-- contact details, no credentials, no role grants — the role a value carries is the role its
-- author was wearing at the time, which is on the value rather than on the person.
SELECT id, employee_code, name_en, name_bn, status
  FROM core.app_user
 WHERE facility_id = $1
 ORDER BY employee_code;

-- name: DirectoryDevices :many
-- The tablets and phones, so "which device typed this" is a name rather than a uuid. Retired
-- devices stay listed, for the same reason retired staff do.
SELECT id, name, kind, status
  FROM core.device
 WHERE facility_id = $1
 ORDER BY name;

-- name: DirectoryStations :many
SELECT code, name_en, name_bn, sequence_hint
  FROM core.station
 WHERE facility_id = $1
 ORDER BY sequence_hint;
