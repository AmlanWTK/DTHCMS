-- The education station's reads (CP88, CP92).
--
-- Every statement here is a SELECT. The station writes through `clinical.Service`, which writes
-- through the ledger and the observation projection; `dthcms_app` holds no INSERT on
-- `read.observation` and none on any table in this file.

-- name: EducationDeviceTypes :many
SELECT code, name_en, name_bn, ordering
  FROM core.education_device_type
 WHERE retired_at IS NULL
 ORDER BY ordering;

-- name: EducationChecklists :many
SELECT c.code, c.device_type, c.title_en, c.title_bn
  FROM core.education_checklist c
  JOIN core.education_device_type d ON d.code = c.device_type
 WHERE c.retired_at IS NULL AND d.retired_at IS NULL
 ORDER BY d.ordering;

-- name: EducationChecklistItems :many
SELECT i.checklist_code, i.ordinal, i.observation_code, i.text_en, i.text_bn, i.is_critical
  FROM core.education_checklist_item i
  JOIN core.education_checklist c ON c.code = i.checklist_code
 WHERE i.retired_at IS NULL AND c.retired_at IS NULL
 ORDER BY i.checklist_code, i.ordinal;

-- name: EducationItemStates :many
-- The three states, read off the observation vocabulary rather than off a list in Go.
--
-- One item's answers stand for all of them: the invariant in migration 00070 asserts that every
-- live checklist item is answerable in exactly these three, so reading one code's vocabulary and
-- reading all thirty-eight would give the same answer — and reading one keeps the reference
-- payload from carrying a hundred and fourteen identical rows.
SELECT a.value_code, a.display_en, a.display_bn, a.ordering
  FROM core.observation_answer a
 WHERE a.code = (SELECT i.observation_code
                   FROM core.education_checklist_item i
                   JOIN core.education_checklist c ON c.code = i.checklist_code
                  WHERE i.retired_at IS NULL AND c.retired_at IS NULL
                  ORDER BY i.checklist_code, i.ordinal
                  LIMIT 1)
   AND a.retired_at IS NULL
 ORDER BY a.ordering;

-- name: EducationReeducationPolicy :one
SELECT unable_raises_flag, corrected_today_threshold
  FROM core.education_reeducation_policy
 WHERE code = 'DEFAULT';

-- name: MedicationMissReasons :many
SELECT code, display_en, display_bn, ordering
  FROM core.medication_miss_reason
 WHERE retired_at IS NULL
 ORDER BY ordering;

-- name: ProScale :one
SELECT code, question_en, question_bn, min_value, max_value, neutral_value
  FROM core.pro_scale
 WHERE observation_code = @observation_code::text AND retired_at IS NULL;

-- name: ProScaleAnchors :many
SELECT from_value, to_value, label_en, label_bn, face_rank, ordering
  FROM core.pro_scale_anchor
 WHERE scale_code = @scale_code::text
 ORDER BY ordering;

-- name: ProNotApplicableReasons :many
SELECT code, display_en, display_bn, ordering
  FROM core.pro_not_applicable_reason
 WHERE retired_at IS NULL
 ORDER BY ordering;

-- name: EducationChecklistsForProducts :many
-- CP92's first acceptance criterion, as one query.
--
-- Which checklist a prescribed line brings up is decided by what the formulary says the product
-- *is* — its class, what it is dispensed in, what form it takes — and never by its name. A match
-- on a trade name silently stops matching the day a manufacturer renames a brand, and a
-- checklist that silently fails to appear is indistinguishable, on every screen, from a patient
-- who is not on a device.
--
-- A NULL column on a rule means "any". `match_class_code` alone would put the vial checklist in
-- front of a pen patient, because insulin in a pen and insulin in a vial are the same class;
-- `match_dispense_unit` alone would put the insulin checklist in front of anybody prescribed
-- anything in a pen. Both together is why the rule table has four nullable columns rather than
-- one.
--
-- # The molecule outranks the class, and that is this query's own rule (spec §6.4)
--
-- Semaglutide and dulaglutide are weekly; liraglutide is once daily; all three are one class. A
-- rule naming the molecule therefore has to beat a rule naming only the class — otherwise a
-- Victoza patient collects both checklists and is asked, on one screen, both which day of the
-- week and what time of day they inject.
--
-- The precedence is here rather than as a `priority` column because it is a property of
-- *matching* and not of any one row: "the most specific rule that matched this product wins" is
-- a statement about the set, and a number on each row would let two people disagree about it by
-- editing one of them. The NOT EXISTS below is that sentence.
SELECT DISTINCT r.checklist_code, c.device_type, p.id AS product_id
  FROM core.medication_product p
  JOIN core.generic g ON g.id = p.generic_id
  JOIN core.education_device_rule r
    ON r.retired_at IS NULL
   AND (r.match_generic_id IS NULL OR r.match_generic_id = p.generic_id)
   AND (r.match_class_code IS NULL OR r.match_class_code = g.class_code)
   AND (r.match_dispense_unit IS NULL OR r.match_dispense_unit = p.dispense_unit)
   AND (r.match_form_code IS NULL OR r.match_form_code = p.form_code)
  JOIN core.education_checklist c ON c.code = r.checklist_code AND c.retired_at IS NULL
 WHERE p.id = ANY (@product_ids::uuid[])
   AND p.facility_id = @facility_id::uuid
   AND (r.match_generic_id IS NOT NULL
        OR NOT EXISTS (
          SELECT 1 FROM core.education_device_rule specific
           WHERE specific.retired_at IS NULL
             AND specific.match_generic_id = p.generic_id
             AND (specific.match_dispense_unit IS NULL
                  OR specific.match_dispense_unit = p.dispense_unit)
             AND (specific.match_form_code IS NULL
                  OR specific.match_form_code = p.form_code)));

-- name: EducationUnclassifiedDevices :many
-- Prescribed lines the station should recognise and does not.
--
-- # What this is for, and why an empty checklist list is not enough
--
-- A patient on tablets alone brings up no checklist, and that is the right answer. A patient on a
-- GLP-1 this formulary gained last year also brings up no checklist — and on the officer's screen
-- those two look identical. The second one is a patient who is about to walk out having been
-- taught nothing about the pen in their bag.
--
-- So the two are separated here. A product is *unclassified* when its class is one this station
-- has rules for — some molecule in it selects a checklist — and the product itself matches none
-- of them. Metformin's class has no rules at all and is correctly silent; a new GLP-1's class
-- does, and is not.
--
-- This is the runtime half of spec §6.4's decision not to give the GLP-1 rules a class-level
-- fallback. Falling back to the weekly checklist would assert a dosing rhythm nobody confirmed;
-- falling back to nothing is safer only if somebody is told, and this is who tells them.
SELECT p.id AS product_id, g.name AS generic_name, g.class_code,
       m.name_en AS class_name_en, m.name_bn AS class_name_bn
  FROM core.medication_product p
  JOIN core.generic g ON g.id = p.generic_id
  JOIN core.medication_class m ON m.code = g.class_code
 WHERE p.id = ANY (@product_ids::uuid[])
   AND p.facility_id = @facility_id::uuid
   -- Some molecule in this class reaches a checklist, so the station knows this kind of thing.
   AND EXISTS (
     SELECT 1 FROM core.education_device_rule r
       JOIN core.generic sibling ON sibling.id = r.match_generic_id
      WHERE r.retired_at IS NULL AND sibling.class_code = g.class_code)
   -- And this one reaches none.
   AND NOT EXISTS (
     SELECT 1 FROM core.education_device_rule r
      WHERE r.retired_at IS NULL
        AND (r.match_generic_id IS NULL OR r.match_generic_id = p.generic_id)
        AND (r.match_class_code IS NULL OR r.match_class_code = g.class_code)
        AND (r.match_dispense_unit IS NULL OR r.match_dispense_unit = p.dispense_unit)
        AND (r.match_form_code IS NULL OR r.match_form_code = p.form_code));

-- name: EducationLatestCompetency :many
-- What this patient was last seen able to do, one row per item.
--
-- DISTINCT ON rather than a window function because the answer wanted is "the most recent state
-- of each item", and a patient seen four times has four rows per item of which exactly one is
-- the current fact. Superseded and corrected rows are excluded by `status = 'ACTIVE'`, which is
-- the observation model's own definition of "this is the value".
--
-- `global_seq` breaks the tie after `effective_at`, and the tie is real rather than theoretical:
-- two assessments recorded in the same second — a correction typed immediately after the
-- original, an offline batch replayed — would otherwise pick whichever row the planner reached
-- first, and "what could this patient do" would change between two identical reads.
SELECT DISTINCT ON (o.code)
       o.code, o.value_code, o.effective_at, o.recorded_at, o.visit_id,
       o.recorded_by, o.recorded_role, o.station_code, o.source,
       i.checklist_code, i.ordinal, i.text_en, i.text_bn, i.is_critical
  FROM read.observation o
  JOIN core.education_checklist_item i ON i.observation_code = o.code
 WHERE o.patient_id = @patient_id::uuid
   AND o.facility_id = @facility_id::uuid
   AND o.status = 'ACTIVE'
 ORDER BY o.code, o.effective_at DESC, o.global_seq DESC;

-- name: EducationComplianceForVisit :many
-- What the patient said about missed doses at this visit, with who asked.
--
-- Both codes in one query and the caller separates them, for the reason the improvement answer
-- is read the same way: the count and the reasons are two different observations and either can
-- be present without the other. A patient who said "three" and would not say why has answered
-- the question, and a screen that required both to draw either would show nothing.
SELECT o.code, o.value_num, o.value_code, o.effective_at, o.recorded_at,
       o.recorded_by, o.recorded_role, o.station_code, o.source
  FROM read.observation o
 WHERE o.patient_id = @patient_id::uuid
   AND o.facility_id = @facility_id::uuid
   AND o.visit_id = @visit_id::uuid
   AND o.status = 'ACTIVE'
   AND o.code IN ('MEDICATION_MISSED_DOSES_7D', 'MEDICATION_MISS_REASON')
 ORDER BY o.code, o.value_code;

-- name: EducationLatestFlag :one
SELECT o.value_bool, o.effective_at, o.visit_id
  FROM read.observation o
 WHERE o.patient_id = @patient_id::uuid
   AND o.facility_id = @facility_id::uuid
   AND o.code = 'EDU_REEDUCATION_FLAG'
   AND o.status = 'ACTIVE'
 ORDER BY o.effective_at DESC, o.global_seq DESC
 LIMIT 1;

-- name: ImprovementAnswerForVisit :many
-- The score row and the not-applicable row for one visit.
--
-- Both codes in one query, and the caller distinguishes them, because the whole point is that
-- they are two different rows and "neither is present" is a third answer. A query that returned
-- only the score would make a not-applicable visit indistinguishable from an unasked one.
SELECT o.code, o.value_num, o.value_code, o.effective_at, o.recorded_at,
       o.recorded_by, o.recorded_role, o.station_code, o.source
  FROM read.observation o
 WHERE o.patient_id = @patient_id::uuid
   AND o.facility_id = @facility_id::uuid
   AND o.visit_id = @visit_id::uuid
   AND o.status = 'ACTIVE'
   AND o.code IN ('IMPROVEMENT_SCORE', 'IMPROVEMENT_SCORE_NA');

-- name: EducationEarlierVisitExists :one
-- Whether this patient has been here before, which is what decides whether §2's question has a
-- comparison point at all.
--
-- Counted against closed and open visits other than this one rather than against observations:
-- a patient who attended and had nothing recorded still has a last visit to compare with, and a
-- question anchored to "your last visit" is anchored to the visit, not to its contents.
SELECT EXISTS (
  SELECT 1 FROM core.visit v
   WHERE v.patient_id = @patient_id::uuid
     AND v.facility_id = @facility_id::uuid
     AND v.id <> @visit_id::uuid
     AND v.opened_at < (SELECT opened_at FROM core.visit WHERE id = @visit_id::uuid)
) AS had_one;
