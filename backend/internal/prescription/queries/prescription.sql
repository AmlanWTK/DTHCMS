-- The prescription read model (CP80).
--
-- Every statement here is a SELECT. The module writes through the ledger and the projection
-- functions; `dthcms_app` holds no INSERT, UPDATE or DELETE on either table, so there is no
-- shape of query this file could hold that would write one.

-- name: PrescriptionStatuses :many
-- The seven states and what each means, in both languages. Reference data a screen fetches once
-- rather than a switch statement it keeps its own copy of.
SELECT status, name_en, name_bn, meaning_en, meaning_bn, is_frozen, is_terminal
  FROM core.prescription_status
 ORDER BY ordering;

-- name: PrescriptionTransitions :many
-- The legal edges. Read by the Go state machine at start-up, so that the matrix the application
-- enforces and the matrix the trigger enforces are one table rather than two lists that agree
-- until somebody edits one.
SELECT from_status, to_status, event_type, note_en, note_bn, owned_by
  FROM core.prescription_transition
 ORDER BY from_status, to_status;

-- name: Prescription :one
SELECT id, facility_id, patient_id, visit_id, status,
       created_at, created_by, created_role,
       submitted_at, submitted_by, bounced_at, bounced_by,
       signed_at, signed_by, printed_at, printed_by,
       dispensed_at, dispensed_by,
       cancelled_at, cancelled_by, cancelled_reason,
       corrected_at, corrected_by,
       corrects_prescription_id, correction_reason, corrected_by_prescription_id,
       corrects_dispensed_original, carried_forward_from,
       created_event_id, last_event_id, last_global_seq, updated_at
  FROM read.prescription
 WHERE id = @id::uuid AND facility_id = @facility_id::uuid;

-- name: PrescriptionItems :many
-- Every line ever on this sheet, removed ones included.
--
-- Removed rows are present rather than filtered, because "what was on this prescription at
-- 14:05" has to stay answerable after the item came off it at 14:06, and a caller that wants
-- only the live lines has `removed_at` to filter on. A query that hid them would make the
-- removal invisible to every reader who did not know to ask.
SELECT id, prescription_id, facility_id, line_no,
       product_id, product_label, generic_name, strength, form_code,
       dose, daily_dose, dose_unit, frequency, duration_days, route, quantity,
       instructions_en, instructions_bn,
       price_poisha, price_id, price_effective_from, price_verification, price_captured_at,
       carried_forward_from_item,
       recorded_at, recorded_by, modified_at, modified_by,
       removed_at, removed_by, removed_reason, event_id, global_seq
  FROM read.prescription_item
 WHERE prescription_id = @prescription_id::uuid
 ORDER BY line_no, recorded_at;

-- name: PrescriptionsForPatient :many
-- This patient's prescriptions, newest first.
SELECT id, facility_id, patient_id, visit_id, status,
       created_at, created_by, created_role,
       submitted_at, submitted_by, bounced_at, bounced_by,
       signed_at, signed_by, printed_at, printed_by,
       dispensed_at, dispensed_by,
       cancelled_at, cancelled_by, cancelled_reason,
       corrected_at, corrected_by,
       corrects_prescription_id, correction_reason, corrected_by_prescription_id,
       corrects_dispensed_original, carried_forward_from,
       created_event_id, last_event_id, last_global_seq, updated_at
  FROM read.prescription
 WHERE patient_id = @patient_id::uuid AND facility_id = @facility_id::uuid
 ORDER BY created_at DESC
 LIMIT @row_limit::int;

-- name: NextPrescriptionLine :one
-- The next free line number on a draft.
--
-- Computed from the table rather than counted in Go, because two items added from two tabs would
-- otherwise both be line 3. Removed lines keep their numbers: reusing one would make two rows in
-- the same sheet's history claim the same position.
SELECT coalesce(max(line_no), 0)::int + 1 AS next_line
  FROM read.prescription_item
 WHERE prescription_id = @prescription_id::uuid;

-- name: PrescriptionItem :one
SELECT id, prescription_id, facility_id, line_no, product_id, product_label,
       dose, daily_dose, dose_unit, frequency, duration_days, route, quantity,
       instructions_en, instructions_bn, removed_at
  FROM read.prescription_item
 WHERE id = @id::uuid;
