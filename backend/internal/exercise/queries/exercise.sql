-- Station 8's exercise assessment and plan (CP60, §3 step 8).
--
-- # What is deliberately missing from this file
--
-- There is no query that returns `core.exercise` unfiltered for a patient. Criterion 1 —
-- *"contraindicated exercises are excluded, not warned"* — is a property of what leaves the
-- server, and a query that could return the whole library is one a handler eventually calls by
-- mistake. `PermittedExercises` joins `core.exercises_permitted`, the function the migration put
-- in the database precisely so that this rule cannot be reimplemented differently by a second
-- reader.
--
-- `LibrarySize` returns a *count*, which is what makes the exclusion honest without making it an
-- offer: a physician looking at eight options needs to know the library holds twelve.

-- name: Contraindications :many
-- What station 8 asks. The questions rather than the labels, because a checkbox saying
-- "neuropathy" gets ticked for tingling toes and one naming the monofilament test does not.
SELECT code, name_en, name_bn, question_en, question_bn, from_observation, ordering
  FROM core.contraindication
 WHERE retired_at IS NULL
 ORDER BY ordering, code;

-- name: LibrarySize :one
-- How many exercises exist, so a short list can say it is short on purpose.
SELECT count(*)::bigint AS total
  FROM core.exercise
 WHERE retired_at IS NULL;

-- name: PermittedExercises :many
-- What this patient may be offered.
--
-- The filter is `core.exercises_permitted`, in the database, reading the assessment that was
-- actually recorded. A client cannot widen this list by claiming the patient has no
-- contraindications, because nothing the client sends reaches the predicate.
SELECT e.code, e.name_en, e.name_bn, e.how_en, e.how_bn,
       e.kind, e.intensity, e.impact, e.needs_equipment, e.can_do_at_home,
       e.approved_at, e.ordering
  FROM core.exercise e
 WHERE e.code IN (SELECT p.code
                    FROM core.exercises_permitted(sqlc.arg(patient_id)::uuid,
                                                  sqlc.arg(facility_id)::uuid) p)
 ORDER BY e.ordering, e.code;

-- name: ExclusionReasons :many
-- Why the list is short, named by condition rather than by exercise.
--
-- The excluded exercises are never sent — that is the whole checkpoint — but *why* they are
-- absent is sent, per condition, with the count each one accounts for. A physician who disagrees
-- with an exclusion needs the sentence to disagree with; an operator needs to know a gap is a
-- decision rather than a missing row.
--
-- **Two kinds of reason, reported apart.** `APPLIES` is a finding about the patient; `NOT_ASKED`
-- is a question somebody still has to put — which happens when a condition is added to the
-- catalogue after this assessment was taken. Folding them into one would tell an operator that a
-- patient has a condition nobody has asked them about.
--
-- The counts overlap on purpose: jogging is excluded by neuropathy *and* by an open ulcer, and
-- reporting it under both is what makes each reason true on its own. The total is
-- `LibrarySize` minus what came back permitted, never the sum of these.
SELECT c.code, c.name_en, c.name_bn,
       bool_or(c.code = ANY(a.contraindications)) AS applies,
       count(DISTINCT x.exercise_code)::bigint AS excluded
  FROM read.exercise_assessment a
  JOIN core.contraindication c
    ON c.retired_at IS NULL
   AND (c.code = ANY(a.contraindications) OR NOT (c.code = ANY(a.asked)))
  JOIN core.exercise_contraindication x ON x.contraindication_code = c.code
  JOIN core.exercise e ON e.code = x.exercise_code AND e.retired_at IS NULL
 WHERE a.id = $1
 GROUP BY c.code, c.name_en, c.name_bn, c.ordering
 ORDER BY c.ordering, c.code;

-- name: LiveAssessment :one
-- What station 8 found, as it stands. Superseded rows stay in the table; this is the one a plan
-- is filtered against.
SELECT a.id, a.patient_id, a.visit_id, a.walks_unaided, a.walk_minutes, a.joint_pain,
       a.contraindications, a.asked, a.note, a.status,
       a.recorded_at, a.recorded_by, a.recorded_role, a.station_code, a.device_id, a.source,
       coalesce(u.employee_code, '') AS recorded_by_code,
       coalesce(u.name_en, '')       AS recorded_by_name_en,
       coalesce(u.name_bn, '')       AS recorded_by_name_bn
  FROM read.exercise_assessment a
  LEFT JOIN core.app_user u ON u.id = a.recorded_by
 WHERE a.patient_id = $1 AND a.facility_id = $2 AND a.status = 'ACTIVE';

-- name: AssessmentByID :one
SELECT a.id, a.patient_id, a.visit_id, a.walks_unaided, a.walk_minutes, a.joint_pain,
       a.contraindications, a.asked, a.note, a.status,
       a.recorded_at, a.recorded_by, a.recorded_role, a.station_code, a.device_id, a.source,
       coalesce(u.employee_code, '') AS recorded_by_code,
       coalesce(u.name_en, '')       AS recorded_by_name_en,
       coalesce(u.name_bn, '')       AS recorded_by_name_bn
  FROM read.exercise_assessment a
  LEFT JOIN core.app_user u ON u.id = a.recorded_by
 WHERE a.id = $1 AND a.facility_id = $2;

-- name: AssessmentsForPatient :many
-- The history. §12.1 compares a patient against themselves across visits, and a contraindication
-- that resolved is as interesting as one that appeared.
SELECT a.id, a.patient_id, a.visit_id, a.walks_unaided, a.walk_minutes, a.joint_pain,
       a.contraindications, a.asked, a.note, a.status,
       a.recorded_at, a.recorded_by, a.recorded_role, a.station_code, a.device_id, a.source,
       coalesce(u.employee_code, '') AS recorded_by_code,
       coalesce(u.name_en, '')       AS recorded_by_name_en,
       coalesce(u.name_bn, '')       AS recorded_by_name_bn
  FROM read.exercise_assessment a
  LEFT JOIN core.app_user u ON u.id = a.recorded_by
 WHERE a.patient_id = $1 AND a.facility_id = $2
 ORDER BY a.recorded_at DESC
 LIMIT $3;

-- name: LivePlan :one
SELECT p.id, p.patient_id, p.visit_id, p.assessment_id, p.status, p.note,
       p.issued_at, p.issued_by, p.issued_role, p.station_code, p.device_id, p.source,
       coalesce(u.employee_code, '') AS issued_by_code,
       coalesce(u.name_en, '')       AS issued_by_name_en,
       coalesce(u.name_bn, '')       AS issued_by_name_bn
  FROM read.exercise_plan p
  LEFT JOIN core.app_user u ON u.id = p.issued_by
 WHERE p.patient_id = $1 AND p.facility_id = $2 AND p.status = 'ACTIVE';

-- name: PlansForPatient :many
SELECT p.id, p.patient_id, p.visit_id, p.assessment_id, p.status, p.note,
       p.issued_at, p.issued_by, p.issued_role, p.station_code, p.device_id, p.source,
       coalesce(u.employee_code, '') AS issued_by_code,
       coalesce(u.name_en, '')       AS issued_by_name_en,
       coalesce(u.name_bn, '')       AS issued_by_name_bn
  FROM read.exercise_plan p
  LEFT JOIN core.app_user u ON u.id = p.issued_by
 WHERE p.patient_id = $1 AND p.facility_id = $2
 ORDER BY p.issued_at DESC
 LIMIT $3;

-- name: PlanItems :many
-- The targets, with the wording that gets printed and handed to the patient (criterion 3). The
-- instruction is joined from the library rather than copied onto the item, so that a correction to
-- the Bangla reaches a sheet reprinted tomorrow.
SELECT i.plan_id, i.exercise_code, i.times_per_week, i.minutes_per_session, i.ordering, i.note,
       coalesce(e.name_en, i.exercise_code) AS name_en,
       coalesce(e.name_bn, i.exercise_code) AS name_bn,
       coalesce(e.how_en, '')  AS how_en,
       coalesce(e.how_bn, '')  AS how_bn,
       coalesce(e.kind, '')      AS kind,
       coalesce(e.intensity, '') AS intensity,
       coalesce(e.impact, '')    AS impact,
       coalesce(e.needs_equipment, false) AS needs_equipment,
       coalesce(e.can_do_at_home, true)   AS can_do_at_home,
       e.approved_at
  FROM read.exercise_plan_item i
  LEFT JOIN core.exercise e ON e.code = i.exercise_code
 WHERE i.plan_id = ANY(sqlc.arg(plan_ids)::uuid[])
 ORDER BY i.plan_id, i.ordering, i.exercise_code;

-- name: WhyExcluded :one
-- The sentence behind one exclusion, for the refusal an operator reads.
--
-- Names both things and says why, in both languages, because "not allowed" sends an operator to
-- the next high-impact option and a reason sends them to a conversation. `applies` distinguishes a
-- finding from a question nobody has asked yet — the operator's next act is different for each.
SELECT e.name_en AS exercise_en, e.name_bn AS exercise_bn,
       c.code AS condition_code, c.name_en AS condition_en, c.name_bn AS condition_bn,
       x.reason_en, x.reason_bn,
       (c.code = ANY(sqlc.arg(applying)::text[])) AS applies
  FROM core.exercise_contraindication x
  JOIN core.exercise e ON e.code = x.exercise_code
  JOIN core.contraindication c ON c.code = x.contraindication_code AND c.retired_at IS NULL
 WHERE x.exercise_code = sqlc.arg(exercise_code)::text
   AND (c.code = ANY(sqlc.arg(applying)::text[])
        OR NOT (c.code = ANY(sqlc.arg(asked)::text[])))
 ORDER BY (c.code = ANY(sqlc.arg(applying)::text[])) DESC, c.ordering, c.code
 LIMIT 1;

-- name: KnowsExercise :one
-- Whether a code is in the library, and whether it is still live.
--
-- Two booleans rather than one, because "retired since you fetched the options" and "not an
-- exercise" mean different things to a client: the first says the list moved and the right act is
-- to fetch again, the second says the request is wrong and refetching would loop.
--
-- A yes-or-no question about a code the caller already named — never a row somebody could offer.
-- The names come back too, because a refusal that says "HEAVY_LIFT is no longer in the library"
-- puts a database identifier in the middle of a Bengali sentence. Every other refusal here names
-- the exercise as the operator saw it, and this one has no reason not to.
SELECT EXISTS (SELECT 1 FROM core.exercise e WHERE e.code = sqlc.arg(code)::text) AS known,
       EXISTS (SELECT 1 FROM core.exercise e
                WHERE e.code = sqlc.arg(code)::text AND e.retired_at IS NULL) AS live,
       (coalesce((SELECT e.name_en FROM core.exercise e
                   WHERE e.code = sqlc.arg(code)::text), sqlc.arg(code)::text))::text AS name_en,
       (coalesce((SELECT e.name_bn FROM core.exercise e
                   WHERE e.code = sqlc.arg(code)::text), sqlc.arg(code)::text))::text AS name_bn;
