-- The pre-consultation synthesis (CP71, §7.1, §10.4 A1, R-05, R-06, D-15).
--
-- # What this schema holds, and what it deliberately does not
--
-- One row per *run* of the synthesis agent against one visit. A run holds three things: the
-- **assembled context** the deterministic step produced, the **model's answer**, and the
-- **measurement** of whether the promise in §7.1 — ready before the patient reaches step 9 — was
-- kept. Nothing here is a clinical fact. §10.6's first permanent invariant is *"AI never writes to
-- the clinical record"*, and the shape of that rule in this schema is that `core.ai_synthesis` is
-- referenced by nothing in the ledger, feeds no projection, and is read by exactly one screen.
--
-- The narrative is a **draft addressed to the physician**, which is why the row carries no
-- `accepted` column. Per-item acceptance is D-28 and CP73's; a boolean here would let a future
-- reader think a summary had been agreed with when all that happened was a page load.
--
-- # Why the assembled context is stored, and why it is stored *here*
--
-- The checkpoint's load-bearing phrase is "deterministic assembly": what reaches the model is built
-- by code from stored facts, not fetched by the model. That is only a property if the artefact
-- exists somewhere a test and a reviewer can read. `core.ai_interaction.outbound` already holds
-- what was *sent* — but it holds it only for calls that were made, and the interesting failures
-- here are the ones where no call happened: a refused payload, an open circuit, a queue that never
-- ran. Storing the context on the synthesis row means a failed synthesis can still show the
-- physician what the system knew, which is most of D-15's degraded state.
--
-- It is also what makes CP72 cheap. Grounding is *"every number in the output matches a stored
-- fact"*, and with the context stored beside the output that becomes a comparison of two columns of
-- one row rather than a re-derivation of the patient's record months later, against code that has
-- moved on.
--
-- # The check constraint on that column is the sharp one
--
-- `ops.carries_identifier` guards `context` exactly as it guards `core.ai_interaction.outbound`.
-- The gateway's minimiser would catch an identifier on the way out; this catches it one step
-- earlier, in the artefact the assembler is responsible for, and it catches it whether or not a
-- call was ever made.
--
-- The cost is real and is accepted: an assembler that copies a clinician's note containing a
-- telephone number cannot store its context, so that visit gets a failed synthesis and the degraded
-- screen instead of a summary. That is the correct direction to fail in — the alternative is a fact
-- index in which a phone number is a citable "fact", which is precisely what CP72's grounding check
-- would then certify as true. The assembler withholds any free-text field that trips the shared
-- pattern list rather than letting it reach this constraint, so the constraint fires only when the
-- assembler itself has a defect.
--
-- # One row per run, and `superseded_at` rather than an update
--
-- §7.1 asks for an incremental re-run when material new data arrives. A re-run that overwrote the
-- previous row would destroy the only evidence of what the physician was shown ten minutes earlier,
-- which is exactly the question a medico-legal review asks. So a re-run is a new row with the next
-- generation number, and the previous row is stamped `superseded_at`. The current synthesis for a
-- visit is the highest generation; the history is every row.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The result
-- ---------------------------------------------------------------------------

CREATE TABLE core.ai_synthesis (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL REFERENCES core.facility(id),
  visit_id    uuid NOT NULL REFERENCES core.visit(id),
  patient_id  uuid NOT NULL REFERENCES core.patient(id),

  -- Generations start at 1 and count re-runs. A number rather than a timestamp ordering, because
  -- "the second synthesis of this visit" is what a reviewer says and two rows written in the same
  -- millisecond would be indistinguishable by time.
  generation integer NOT NULL CHECK (generation >= 1),

  state text NOT NULL CHECK (state IN ('PENDING', 'RUNNING', 'READY', 'FAILED', 'UNCHANGED')),

  -- How this run came to exist. §7.1's own distinction: the automatic trigger is the normal path
  -- and the button is the fallback, so a deployment where MANUAL dominates has failed acceptance
  -- criterion 2 and this column is how anybody would know.
  trigger text NOT NULL CHECK (trigger IN ('AUTOMATIC', 'MANUAL', 'RERUN')),

  -- The assembled context, and its hash over the *material* subset (see internal/synthesis).
  -- The hash is what decides whether a re-run is warranted; the whole context is what the
  -- physician's screen and CP72's validator read.
  context        jsonb NOT NULL,
  material_sha256 text NOT NULL CHECK (material_sha256 ~ '^[0-9a-f]{64}$'),

  -- The model's validated answer, absent until there is one.
  output jsonb,

  -- Which call produced it. Null for a run that never reached the gateway — a queue that never
  -- drained, a context that could not be assembled — and that difference is the first thing an
  -- operator asks about a failure.
  ai_interaction_id uuid REFERENCES core.ai_interaction(id),
  prompt_version    text,
  model_version     text,

  -- The queue job carrying this run. Nullable because a run is created in the same transaction as
  -- its job and the job id is known then, but a manual re-request that is absorbed as a duplicate
  -- has no job of its own.
  job_id uuid,

  requested_at timestamptz NOT NULL,
  requested_by uuid REFERENCES core.app_user(id),
  started_at   timestamptz,
  finished_at  timestamptz,

  -- §7.1's five minutes, measured rather than claimed. The deadline is the queue's — `ops.job_kind`
  -- holds the budget for `clinical.synthesis` and stamps it on the job at enqueue — and it is
  -- copied here so that the SLA report for the *clinical* promise does not depend on the job row
  -- still existing after a retention sweep.
  sla_deadline timestamptz,
  met_sla      boolean,

  -- Why it failed, in a word an operator can group by, plus the sentence. The classes are
  -- deliberately about *what the physician should do*, not about which Go error was returned.
  failure_kind   text CHECK (failure_kind IS NULL OR failure_kind IN
    ('ASSEMBLY', 'REFUSED', 'PROVIDER', 'TIMEOUT', 'INVALID_OUTPUT', 'INTERNAL')),
  failure_detail text NOT NULL DEFAULT '',

  superseded_at timestamptz,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT ai_synthesis_generation_once UNIQUE (visit_id, generation),

  -- A finished run says when it finished; an unfinished one does not claim to have.
  CONSTRAINT ai_synthesis_finished_when_terminal CHECK (
    (state IN ('READY', 'FAILED', 'UNCHANGED')) = (finished_at IS NOT NULL)),

  -- A failure names its class. "It failed" with no kind is a row nobody can act on, and the
  -- degraded screen D-15 asks for has nothing to say beyond "unavailable".
  CONSTRAINT ai_synthesis_failure_is_classified CHECK (
    (state = 'FAILED') = (failure_kind IS NOT NULL)),

  -- Criterion 3, structurally. A READY row is one the physician is shown, and §10.6's fourth
  -- permanent invariant requires every AI output to be stored with the prompt and model version
  -- that produced it. A summary on a screen whose provenance cannot be named is a summary nobody
  -- can defend after the fact.
  CONSTRAINT ai_synthesis_ready_names_its_model CHECK (
    state <> 'READY' OR (output IS NOT NULL AND ai_interaction_id IS NOT NULL
                         AND btrim(coalesce(prompt_version, '')) <> ''
                         AND btrim(coalesce(model_version, '')) <> '')),

  -- The same rule the gateway's outbound column carries, one step earlier. See the header.
  CONSTRAINT ai_synthesis_context_names_nobody CHECK (NOT ops.carries_identifier(context))
);

CREATE INDEX ai_synthesis_current
  ON core.ai_synthesis (visit_id, generation DESC);
CREATE INDEX ai_synthesis_by_patient
  ON core.ai_synthesis (facility_id, patient_id, requested_at DESC);
-- The SLA report reads this one: everything finished in a window, with its verdict.
CREATE INDEX ai_synthesis_sla
  ON core.ai_synthesis (facility_id, finished_at DESC)
  WHERE finished_at IS NOT NULL;

SELECT core.attach_updated_at('core.ai_synthesis');

GRANT SELECT, INSERT, UPDATE ON core.ai_synthesis TO dthcms_app;

COMMENT ON TABLE core.ai_synthesis IS
  'One run of the pre-consultation synthesis agent against one visit: the assembled context, the model''s draft, and whether §7.1''s five minutes were kept (CP71).';
COMMENT ON COLUMN core.ai_synthesis.context IS
  'What the deterministic assembler produced. Every number and date in `output` must be traceable to something in here — that is what CP72 will check.';
COMMENT ON COLUMN core.ai_synthesis.material_sha256 IS
  'Hash over the material subset of the context. Two runs with the same hash would produce the same summary, so the second one does not call a model.';

-- ---------------------------------------------------------------------------
-- The agent
-- ---------------------------------------------------------------------------

-- §7.2's A1, and the first entry in this table that is not the gateway's own fixture. GENERATIVE,
-- because unlike A2 it really does put a language model in front of a clinician — which is why it
-- is also the agent §10.4 gives the highest-quality tier in the stack.
INSERT INTO core.ai_agent (agent_code, technology, description_en, description_bn) VALUES
  ('clinical.synthesis', 'GENERATIVE',
   'The pre-consultation synthesis: one page of narrative and a draft plan, assembled from the stations the patient has already passed',
   'পরামর্শ-পূর্ব সারসংক্ষেপ: রোগী যে কেন্দ্রগুলি ইতিমধ্যে পার করেছেন তার তথ্য থেকে তৈরি এক পৃষ্ঠার বিবরণ ও খসড়া পরিকল্পনা')
ON CONFLICT (agent_code) DO UPDATE SET
  technology = EXCLUDED.technology,
  description_en = EXCLUDED.description_en, description_bn = EXCLUDED.description_bn;

-- ---------------------------------------------------------------------------
-- The permission on the button
-- ---------------------------------------------------------------------------

-- §7.1 gives the button to *"the final assistant in the flow"*, and reading the summary is already
-- `ai.synthesis.read`. Asking for one is a separate, narrower permission and not the same act:
-- requesting costs money and queue time and reveals nothing, while reading reveals the whole
-- clinical picture. The exercise specialist and the nutritionist must be able to press it and are
-- not entitled to read what comes back — which is exactly what two permissions buy and one would
-- not.
--
-- Not sensitive: it exposes no clinical content. It is the only permission this checkpoint adds.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('ai.synthesis.request', 'ai', 'synthesis', 'request',
   'Ask for the pre-consultation summary to be prepared for a visit', false)
ON CONFLICT (code) DO UPDATE SET
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'ai.synthesis.request'
  FROM core.role r
 -- The four roles that staff a station between anthropometry and the consultation room, plus the
 -- two who read the result. The physician is on the list because §7.1 says they *can* trigger it
 -- while promising they never need to; a physician who could not would be stuck when the automatic
 -- trigger has failed, which is the moment the button exists for.
 WHERE r.code IN ('CLINICAL_ASSISTANT', 'JUNIOR_DOCTOR', 'NUTRITIONIST', 'EXERCISE',
                  'PHYSICIAN', 'ADMIN')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- Criterion 3, over the whole table rather than row by row. The check constraint above refuses a
-- READY row without its provenance from here on; this notices one that arrived some other way, and
-- it is the check that "the physician was shown something nobody can trace" is really about.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_synthesis_names_what_produced_it() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.ai_synthesis
   WHERE state = 'READY'
     AND (output IS NULL OR ai_interaction_id IS NULL
          OR btrim(coalesce(prompt_version, '')) = ''
          OR btrim(coalesce(model_version, '')) = '');
  IF offenders > 0 THEN
    RAISE EXCEPTION '% AI summaries do not name the prompt and model that produced them', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- D-15, made checkable. *"Fail visible, never fail silent, never fail invented."* The visible half
-- is a screen's job; the half a database can hold is that a run which stopped has a reason attached
-- to it, and that a run still claiming to be in flight long after its deadline is a lie the SLA
-- report would otherwise repeat.
--
-- The window is deliberately generous — four times §7.1's five minutes — because this is an
-- invariant, not an alert: it should fire when something is structurally wrong, not when a worker
-- was slow. `ops.job` and the queue-health page are where slowness belongs.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_synthesis_is_stuck_in_flight() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.ai_synthesis
   WHERE state IN ('PENDING', 'RUNNING')
     AND requested_at < now() - interval '20 minutes';
  IF offenders > 0 THEN
    RAISE EXCEPTION
      '% pre-consultation summaries have been in flight for over twenty minutes; the physician''s screen is showing "preparing" for a job nobody is doing',
      offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- Every run has to be reproducible from its own row: the context that was assembled, and the hash
-- that decided whether a model was worth paying. A row with an empty context is a run whose
-- "deterministic assembly" produced nothing, and the summary beside it would be a model's
-- imagination with a prompt version stamped on it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_synthesis_kept_its_input() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.ai_synthesis
   WHERE context IS NULL
      OR jsonb_typeof(context) <> 'object'
      OR context = '{}'::jsonb
      OR jsonb_array_length(coalesce(context -> 'facts', '[]'::jsonb)) = 0;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% pre-consultation summaries do not keep the context they were assembled from', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_synthesis_names_what_produced_it',
   'every AI summary shown to a physician names the prompt version, model version and call that produced it', 103),
  ('assert_no_synthesis_is_stuck_in_flight',
   'no pre-consultation summary has been in flight long enough for its screen to be lying', 104),
  ('assert_every_synthesis_kept_its_input',
   'every pre-consultation summary keeps the assembled context it was produced from', 105)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_every_synthesis_names_what_produced_it',
  'assert_no_synthesis_is_stuck_in_flight',
  'assert_every_synthesis_kept_its_input');
DROP FUNCTION IF EXISTS core.assert_every_synthesis_kept_its_input();
DROP FUNCTION IF EXISTS core.assert_no_synthesis_is_stuck_in_flight();
DROP FUNCTION IF EXISTS core.assert_every_synthesis_names_what_produced_it();
DELETE FROM core.role_permission WHERE permission_code = 'ai.synthesis.request';
DELETE FROM core.permission WHERE code = 'ai.synthesis.request';
DELETE FROM core.ai_agent WHERE agent_code = 'clinical.synthesis';
DROP TABLE IF EXISTS core.ai_synthesis;
