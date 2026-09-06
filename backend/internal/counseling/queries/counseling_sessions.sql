-- Counselling sessions and ticks (CP56).

-- name: StartedCounselingSession :one
-- One session, with the checklist it is walking named and its author resolved.
--
-- The names are joined rather than stored, for the reason every attribution in this system is
-- joined: a staff member who marries and changes their name should read correctly on work they
-- did last year, and a copy taken at write time would not. `approved_at` comes from the version
-- because D-53 is open — the seeded content is a proposal, and a panel that could not say so
-- would present it as a clinician's list.
SELECT s.id, s.facility_id, s.patient_id, s.visit_id, s.template_id, s.template_version,
       s.started_at, s.started_by, s.started_role, s.completed_at, s.completed_by,
       t.code AS template_code, t.title_en, t.title_bn, v.approved_at,
       coalesce(o.employee_code, '') AS started_by_code,
       coalesce(o.name_en, '')       AS started_by_name_en,
       coalesce(o.name_bn, '')       AS started_by_name_bn,
       coalesce(c.employee_code, '') AS completed_by_code,
       coalesce(c.name_en, '')       AS completed_by_name_en,
       coalesce(c.name_bn, '')       AS completed_by_name_bn
  FROM read.counseling_session s
  JOIN core.counseling_template t ON t.id = s.template_id
  JOIN core.counseling_template_version v
    ON v.template_id = s.template_id AND v.version = s.template_version
  LEFT JOIN core.app_user o ON o.id = s.started_by
  LEFT JOIN core.app_user c ON c.id = s.completed_by
 WHERE s.id = $1;

-- name: CounselingSessionForVisitTemplate :one
-- The open session a counsellor should land back in when they reopen the app. One per
-- checklist per visit, which a unique index enforces: a second half-ticked copy of the same
-- list is how two counsellors each cover half of it and both believe the other did the rest.
SELECT id, facility_id, patient_id, visit_id, template_id, template_version,
       started_at, started_by, started_role, completed_at, completed_by
  FROM read.counseling_session
 WHERE visit_id = $1 AND template_id = $2;

-- name: CounselingSessionsForVisit :many
-- The index carries the same fields the single read does. A client reading the contract cannot
-- see which endpoint omits what, so an index that dropped the names and the approval date would
-- have every panel rendering blanks until a second request landed.
SELECT s.id, s.facility_id, s.patient_id, s.visit_id, s.template_id, s.template_version,
       s.started_at, s.started_by, s.started_role, s.completed_at, s.completed_by,
       t.code AS template_code, t.title_en, t.title_bn, v.approved_at,
       coalesce(o.employee_code, '') AS started_by_code,
       coalesce(o.name_en, '')       AS started_by_name_en,
       coalesce(o.name_bn, '')       AS started_by_name_bn,
       coalesce(c.employee_code, '') AS completed_by_code,
       coalesce(c.name_en, '')       AS completed_by_name_en,
       coalesce(c.name_bn, '')       AS completed_by_name_bn
  FROM read.counseling_session s
  JOIN core.counseling_template t ON t.id = s.template_id
  JOIN core.counseling_template_version v
    ON v.template_id = s.template_id AND v.version = s.template_version
  LEFT JOIN core.app_user o ON o.id = s.started_by
  LEFT JOIN core.app_user c ON c.id = s.completed_by
 WHERE s.visit_id = $1
 ORDER BY s.started_at;

-- name: CounselingTicks :many
-- Every tick on one session, live and taken back alike. The withdrawn ones are returned rather
-- than filtered: section 5.4's panel asks "what was covered", and an item ticked at 11:02 and
-- taken back at 11:04 is an answer to that question rather than the absence of one.
SELECT t.session_id, t.item_code, t.ticked_at, t.ticked_by, t.ticked_role, t.note,
       t.undone_at, t.undone_by, t.undone_reason, t.undo_count, t.event_id,
       t.device_id, t.station_code,
       coalesce(u.employee_code, '') AS ticked_by_code,
       coalesce(u.name_en, '')       AS ticked_by_name_en,
       coalesce(u.name_bn, '')       AS ticked_by_name_bn,
       coalesce(w.employee_code, '') AS undone_by_code,
       coalesce(w.name_en, '')       AS undone_by_name_en,
       coalesce(w.name_bn, '')       AS undone_by_name_bn
  FROM read.counseling_tick t
  LEFT JOIN core.app_user u ON u.id = t.ticked_by
  LEFT JOIN core.app_user w ON w.id = t.undone_by
 WHERE t.session_id = $1
 ORDER BY t.ticked_at;

-- name: CounselingTick :one
SELECT session_id, item_code, ticked_at, ticked_by, ticked_role, note,
       undone_at, undone_by, undone_reason, undo_count, event_id
  FROM read.counseling_tick
 WHERE session_id = $1 AND item_code = $2;

-- What is still outstanding is deliberately *not* here. `core.counseling_outstanding(uuid)` is
-- a set-returning function, and sqlc reads one as a single composite column -- so a query
-- against it would come back as an opaque record rather than four fields. It is called from Go
-- with pgx instead (see `Store.Outstanding`), which keeps the definition in the one place the
-- gate, the phone and the physician's panel all read.
