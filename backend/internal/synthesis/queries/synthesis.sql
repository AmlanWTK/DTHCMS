-- The pre-consultation synthesis (CP71, §7.1).
--
-- # The query that is deliberately absent
--
-- There is no `DELETE`. A summary a physician was shown is evidence of what they were shown, and a
-- re-run replaces nothing: it inserts the next generation and stamps the previous one superseded.
-- The application's grant holds no DELETE on this table either, so this is a rule rather than a
-- habit — the same shape as the interaction log it sits beside.

-- name: InsertSynthesis :one
-- Creates a run. Called inside the transaction that decided the work was needed, beside the
-- `EnqueueTx` that queues it: a run recorded without its job, or a job without its run, would be
-- exactly the split-brain the queue's whole design exists to avoid.
INSERT INTO core.ai_synthesis (
  id, facility_id, visit_id, patient_id, generation, state, trigger,
  context, material_sha256, job_id, requested_at, requested_by, sla_deadline
) VALUES (
  @id, @facility_id, @visit_id, @patient_id,
  -- The generation is computed here rather than passed in. Two stations finishing in the same
  -- second both read "there are two runs" and both write a third; the unique index on
  -- (visit_id, generation) turns that into one winner and one refusal, which is the outcome the
  -- caller can handle. A number chosen in Go from a prior SELECT could not be.
  (SELECT coalesce(max(generation), 0) + 1 FROM core.ai_synthesis WHERE visit_id = @visit_id),
  @state, @trigger, @context, @material_sha256,
  sqlc.narg('job_id'), @requested_at, sqlc.narg('requested_by'), sqlc.narg('sla_deadline')
)
RETURNING id, facility_id, visit_id, patient_id, generation, state, trigger,
          context, material_sha256, output, ai_interaction_id, prompt_version, model_version,
          job_id, requested_at, requested_by, started_at, finished_at,
          sla_deadline, met_sla, failure_kind, failure_detail,
          grounding_state, grounding_findings, superseded_at;

-- name: CurrentSynthesis :one
-- The newest run for a visit. What the physician's screen reads, and what the re-run check
-- compares its freshly assembled hash against.
SELECT id, facility_id, visit_id, patient_id, generation, state, trigger,
       context, material_sha256, output, ai_interaction_id, prompt_version, model_version,
       job_id, requested_at, requested_by, started_at, finished_at,
       sla_deadline, met_sla, failure_kind, failure_detail,
       grounding_state, grounding_findings, superseded_at
  FROM core.ai_synthesis
 WHERE visit_id = @visit_id AND facility_id = @facility_id
 ORDER BY generation DESC
 LIMIT 1;

-- name: SynthesisByID :one
SELECT id, facility_id, visit_id, patient_id, generation, state, trigger,
       context, material_sha256, output, ai_interaction_id, prompt_version, model_version,
       job_id, requested_at, requested_by, started_at, finished_at,
       sla_deadline, met_sla, failure_kind, failure_detail,
       grounding_state, grounding_findings, superseded_at
  FROM core.ai_synthesis
 WHERE id = @id AND facility_id = @facility_id;

-- name: SynthesisHistory :many
-- Every run for a visit, newest first. The audit view: what was on the screen at each point of the
-- consultation, and why the earlier one was replaced.
SELECT id, facility_id, visit_id, patient_id, generation, state, trigger,
       context, material_sha256, output, ai_interaction_id, prompt_version, model_version,
       job_id, requested_at, requested_by, started_at, finished_at,
       sla_deadline, met_sla, failure_kind, failure_detail,
       grounding_state, grounding_findings, superseded_at
  FROM core.ai_synthesis
 WHERE visit_id = @visit_id AND facility_id = @facility_id
 ORDER BY generation DESC;

-- name: StartSynthesis :one
-- A worker has picked the run up. The `state = 'PENDING'` guard is what makes at-least-once
-- delivery safe here: a lease that expired and returned the job to the queue must not restart a
-- run another worker is already inside, and the second claim gets no row rather than a race.
UPDATE core.ai_synthesis
   SET state = 'RUNNING', started_at = @started_at
 WHERE id = @id AND state = 'PENDING'
RETURNING id, facility_id, visit_id, patient_id, generation, state, trigger,
          context, material_sha256, output, ai_interaction_id, prompt_version, model_version,
          job_id, requested_at, requested_by, started_at, finished_at,
          sla_deadline, met_sla, failure_kind, failure_detail,
          grounding_state, grounding_findings, superseded_at;

-- name: FinishSynthesis :one
-- The terminal write: READY, FAILED or UNCHANGED, with everything the row has to be able to
-- account for afterwards. `met_sla` is computed here against the deadline the queue stamped, so
-- that §7.1's five minutes is measured from one clock rather than from whichever process was
-- asked.
UPDATE core.ai_synthesis
   SET state = @state,
       context = @context,
       material_sha256 = @material_sha256,
       output = sqlc.narg('output'),
       ai_interaction_id = sqlc.narg('ai_interaction_id'),
       prompt_version = sqlc.narg('prompt_version'),
       model_version = sqlc.narg('model_version'),
       finished_at = @finished_at,
       met_sla = CASE WHEN sla_deadline IS NULL THEN NULL
                      ELSE @finished_at <= sla_deadline END,
       failure_kind = sqlc.narg('failure_kind'),
       failure_detail = @failure_detail,
       -- CP72. Written in the same statement as the state, which is what makes
       -- `ai_synthesis_ready_is_grounded` a guarantee rather than a race: there is no moment at
       -- which a row is READY and its verdict has not yet arrived.
       grounding_state = @grounding_state,
       grounding_findings = @grounding_findings
 WHERE id = @id AND state IN ('PENDING', 'RUNNING')
RETURNING id, facility_id, visit_id, patient_id, generation, state, trigger,
          context, material_sha256, output, ai_interaction_id, prompt_version, model_version,
          job_id, requested_at, requested_by, started_at, finished_at,
          sla_deadline, met_sla, failure_kind, failure_detail,
          grounding_state, grounding_findings, superseded_at;

-- name: SupersedeEarlierSyntheses :exec
-- Stamps every earlier run for this visit as superseded. Run when a new one becomes READY, so the
-- history reads as a sequence rather than as a set of competing summaries.
UPDATE core.ai_synthesis
   SET superseded_at = @at
 WHERE visit_id = @visit_id AND generation < @generation AND superseded_at IS NULL;

-- name: SynthesisSLA :one
-- Acceptance criterion 1, as a number somebody can put on a screen: of the runs that finished in
-- this window, how many made §7.1's deadline.
--
-- `UNCHANGED` runs count. They are a completed promise — the physician's page was checked and was
-- already current — and excluding them would let a clinic hit 95% by re-requesting summaries it
-- knew were fresh.
SELECT count(*)::bigint AS finished,
       count(*) FILTER (WHERE met_sla)::bigint AS met,
       count(*) FILTER (WHERE state = 'FAILED')::bigint AS failed,
       count(*) FILTER (WHERE trigger = 'MANUAL')::bigint AS manual,
       coalesce(
         percentile_disc(0.95) WITHIN GROUP (
           ORDER BY extract(epoch FROM (finished_at - requested_at))), 0)::double precision
         AS p95_seconds
  FROM core.ai_synthesis
 WHERE facility_id = @facility_id
   AND finished_at >= @since
   AND finished_at <= @until;
