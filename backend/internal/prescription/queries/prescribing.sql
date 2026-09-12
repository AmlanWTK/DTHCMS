-- The suggestions the editor offers, and the sentences it offers with them (CP81).
--
-- Two reads and two writes. The writes are the only ones in this module that are not events, and
-- that is correct: a prescribing default is not a clinical fact about a patient, it is a piece of
-- the clinic's own content — the same kind of row as a medication rule or a price, which CP77 and
-- CP75 also write directly. What goes in the ledger is the prescription the physician wrote, and
-- that already records the dose he accepted rather than the one he was offered.

-- name: PrescribingDefaults :many
-- Every default this facility holds, with the instruction it offers and the molecule it is about.
--
-- Returned whole rather than filtered by drug. There are twenty-eight rows; the editor holds them
-- and answers a keystroke from memory, exactly as CP76 holds the whole formulary. A per-line
-- round trip to look up "what is the usual dose of metformin" is latency on the one interaction
-- this checkpoint is measured on.
SELECT d.id, d.facility_id, d.generic_id, g.name AS generic_name, g.class_code,
       d.strength, d.dose, d.daily_dose, d.dose_unit,
       d.frequency, d.frequency_bn, d.duration_days, d.route,
       d.instruction_template_id, t.code AS instruction_code,
       d.rationale_en, d.rationale_bn, d.source_citation,
       d.origin, d.status, d.approved_at, u.name_en AS approved_by_name, u.name_bn AS approved_by_name_bn,
       d.updated_at
  FROM core.prescribing_default d
  JOIN core.generic g ON g.id = d.generic_id
  LEFT JOIN core.instruction_template t ON t.id = d.instruction_template_id
  LEFT JOIN core.app_user u ON u.id = d.approved_by
 WHERE d.facility_id = @facility_id::uuid
 ORDER BY g.name, d.strength;

-- name: InstructionTemplates :many
-- The bilingual sentences a line can be given, with the molecule each belongs to when it belongs
-- to one.
SELECT t.id, t.facility_id, t.code, t.generic_id, g.name AS generic_name,
       t.label_en, t.label_bn, t.text_en, t.text_bn, t.ordering,
       t.source_citation, t.origin, t.status, t.approved_at,
       u.name_en AS approved_by_name, u.name_bn AS approved_by_name_bn, t.updated_at
  FROM core.instruction_template t
  LEFT JOIN core.generic g ON g.id = t.generic_id
  LEFT JOIN core.app_user u ON u.id = t.approved_by
 WHERE t.facility_id = @facility_id::uuid
 ORDER BY t.ordering, t.code;

-- name: ApprovePrescribingDefault :one
-- A physician putting his name on a suggestion. Idempotent by construction: approving an already
-- approved row returns it unchanged rather than re-stamping somebody else's name onto it.
UPDATE core.prescribing_default
   SET status = 'APPROVED',
       approved_by = CASE WHEN status = 'APPROVED' THEN approved_by ELSE @actor_id::uuid END,
       approved_at = CASE WHEN status = 'APPROVED' THEN approved_at ELSE @at::timestamptz END,
       updated_by = @actor_id::uuid
 WHERE id = @id::uuid AND facility_id = @facility_id::uuid
 RETURNING id, status, approved_at;

-- name: ApproveInstructionTemplate :one
UPDATE core.instruction_template
   SET status = 'APPROVED',
       approved_by = CASE WHEN status = 'APPROVED' THEN approved_by ELSE @actor_id::uuid END,
       approved_at = CASE WHEN status = 'APPROVED' THEN approved_at ELSE @at::timestamptz END,
       updated_by = @actor_id::uuid
 WHERE id = @id::uuid AND facility_id = @facility_id::uuid
 RETURNING id, status, approved_at;
