-- The correction workflow (CP62).

-- name: CorrectionReasons :many
SELECT code, display_en, display_bn, is_transcription, ordering
  FROM core.correction_reason
 WHERE retired_at IS NULL
 ORDER BY ordering;

-- name: CorrectionRequestByID :one
SELECT r.id, r.facility_id, r.patient_id, r.visit_id, r.observation_id, r.code,
       r.requested_at, r.requested_by, r.requested_role,
       r.reason_code, r.note, r.assigned_to, r.status,
       r.resolved_at, r.resolved_by, r.resolved_role, r.resolution_note, r.replacement_id,
       r.recomputed,
       coalesce(q.employee_code, '') AS requested_by_code,
       coalesce(q.name_en, '')       AS requested_by_name_en,
       coalesce(q.name_bn, '')       AS requested_by_name_bn,
       coalesce(a.employee_code, '') AS assigned_to_code,
       coalesce(a.name_en, '')       AS assigned_to_name_en,
       coalesce(a.name_bn, '')       AS assigned_to_name_bn
  FROM read.correction_request r
  LEFT JOIN core.app_user q ON q.id = r.requested_by
  LEFT JOIN core.app_user a ON a.id = r.assigned_to
 WHERE r.id = $1 AND r.facility_id = $2;

-- name: OpenCorrectionRequestFor :one
-- The open request on one value, if there is one. A second flag on the same value is the same
-- conversation, and two would route two corrections at one number.
SELECT id FROM read.correction_request
 WHERE observation_id = $1 AND status = 'OPEN';

-- name: CorrectionRequestsForOperator :many
-- What an operator's own device asks: "what am I being asked to fix". Open ones only, oldest
-- first — a queue answered newest-first is a queue where the oldest request is never answered.
SELECT r.id, r.facility_id, r.patient_id, r.visit_id, r.observation_id, r.code,
       r.requested_at, r.requested_by, r.requested_role,
       r.reason_code, r.note, r.assigned_to, r.status,
       r.resolved_at, r.resolved_by, r.resolved_role, r.resolution_note, r.replacement_id,
       r.recomputed,
       coalesce(q.employee_code, '') AS requested_by_code,
       coalesce(q.name_en, '')       AS requested_by_name_en,
       coalesce(q.name_bn, '')       AS requested_by_name_bn,
       coalesce(a.employee_code, '') AS assigned_to_code,
       coalesce(a.name_en, '')       AS assigned_to_name_en,
       coalesce(a.name_bn, '')       AS assigned_to_name_bn
  FROM read.correction_request r
  LEFT JOIN core.app_user q ON q.id = r.requested_by
  LEFT JOIN core.app_user a ON a.id = r.assigned_to
 WHERE r.facility_id = $1 AND r.assigned_to = $2
   AND ($3::bool IS FALSE OR r.status = 'OPEN')
 ORDER BY r.requested_at
 LIMIT $4;

-- name: CorrectionRequestsForPatient :many
-- Every flag ever raised on this patient's values, newest first. What the value-history screen
-- reads beside the observation chain.
SELECT r.id, r.facility_id, r.patient_id, r.visit_id, r.observation_id, r.code,
       r.requested_at, r.requested_by, r.requested_role,
       r.reason_code, r.note, r.assigned_to, r.status,
       r.resolved_at, r.resolved_by, r.resolved_role, r.resolution_note, r.replacement_id,
       r.recomputed,
       coalesce(q.employee_code, '') AS requested_by_code,
       coalesce(q.name_en, '')       AS requested_by_name_en,
       coalesce(q.name_bn, '')       AS requested_by_name_bn,
       coalesce(a.employee_code, '') AS assigned_to_code,
       coalesce(a.name_en, '')       AS assigned_to_name_en,
       coalesce(a.name_bn, '')       AS assigned_to_name_bn
  FROM read.correction_request r
  LEFT JOIN core.app_user q ON q.id = r.requested_by
  LEFT JOIN core.app_user a ON a.id = r.assigned_to
 WHERE r.patient_id = $1 AND r.facility_id = $2
 ORDER BY r.requested_at DESC
 LIMIT $3;

-- name: LiveDerivedValue :one
-- The live derived value of one code on one patient, if there is one.
--
-- The cascade asks this per derivation rather than asking the database which derived values read a
-- given code, and the reason is a small trap: `inputs` stores the names the formula's own paper
-- uses — `height_cm`, not `BODY_HEIGHT` — so a query matching the corrected code against the keys
-- of `inputs` matches nothing and recomputes nothing, silently. Which derivation reads which code
-- is stated in Go, beside the formula that reads it.
SELECT id, code, formula, formula_version, effective_at
  FROM read.observation
 WHERE patient_id = $1 AND facility_id = $2 AND code = $3
   AND status = 'ACTIVE' AND formula <> ''
 ORDER BY effective_at DESC, global_seq DESC
 LIMIT 1;
