-- Grounding validation and the AI evaluation harness (CP72, §10.2 step 4, §10.5, §10.6 invariant 5).
--
-- # The one sentence this migration exists to make true
--
-- *A summary carrying a claim that cannot be traced to what the model was shown is unable to reach
-- the state the physician's screen reads.* Not "is logged", not "raises an alert" — **unable**.
--
-- A validator that reported a fabricated HbA1c and let the sentence through would be a metric, and
-- §10.2 is not asking for a metric. So the rule is written three times, in three places that fail
-- independently, exactly as CP70 wrote the tier guard:
--
--   1. **Go.** `ai.Gateway.Invoke` runs the check and returns `ErrUngrounded` instead of an answer,
--      so no agent ever receives ungrounded output to store in the first place. The gateway is the
--      only path to a model, which is what makes this the layer a future caller cannot forget.
--   2. **A check constraint**, twice. `core.ai_interaction` refuses to record a failed grounding
--      verdict against a successful status; `core.ai_synthesis` refuses a `READY` row whose
--      grounding state is anything but `PASSED`.
--   3. **An invariant**, over the whole table, for the row that arrived some other way — a hand
--      edit, a restore, a future code path nobody has written yet.
--
-- Each is sufficient on its own. That is the point of writing it three times: the interesting
-- failure is never the one you defended against.
--
-- # Why the grounding requirement is resolved from this table and never declared by a caller
--
-- ADR-0032's argument, applied a second time. A `Request` field saying "this one does not need
-- grounding" is a claim nobody checks, set in a branch that turns out to be reachable with a real
-- patient in it. So there is no such field and nowhere to add one: `core.ai_agent` carries
-- `grounding_required`, it **defaults to true**, and an agent that is exempt must say why in
-- twenty characters that a person wrote.
--
-- The default matters more than the column. A future checkpoint that adds an agent and forgets
-- this table gets grounding, because forgetting means taking the default, and the default is the
-- safe answer. Forgetting the other way round is impossible: the exemption is a row somebody had
-- to write a sentence into.
--
-- And the register being wrong is survivable, because `core.ai_synthesis` does not consult it: its
-- constraint demands `PASSED` outright, so flipping `clinical.synthesis` to exempt would not
-- quietly disable the block — it would refuse to store any summary at all, loudly, on the first
-- run. That is deliberate: the third layer is only worth having if it is independent of the
-- second.
--
-- # Why a defect and not a retry
--
-- CP70 retries a malformed answer, and it is right to: JSON that does not parse is a transport
-- problem, and the second attempt usually fixes it. A model that invents an HbA1c is not a
-- transport problem. Retrying it spends money to destroy the evidence — the next answer probably
-- passes, nobody ever learns the first one was wrong, and the *rate* of the failure this whole
-- checkpoint exists to measure reads as zero.
--
-- So `core.ai_grounding_defect` is written per finding, is not deletable by the application, and
-- carries what somebody needs a week later: which call, which prompt version, which model version,
-- which arm of the check fired, the token that failed, and the sentence around it. Plus a review
-- verdict, because criterion 2's false-positive rate is only measurable in production if a human
-- can record that the validator was wrong.
--
-- # What this migration does to the twenty summaries CP71 already produced
--
-- They were produced before the check existed, so they have never been grounded. An unchecked
-- summary is not one a physician may read — that is the rule this file is written to enforce, and
-- exempting the rows that happen to predate it would be the rule defeated on its first day. So
-- they are moved to `FAILED` with kind `UNGROUNDED`, keeping their context, their output and their
-- interaction, and the screen shows the structured record with a sentence saying why.
--
-- On a system with real patients in it that would be a decision for Dr. Nahid rather than for a
-- migration. There are none: every one of these twenty is a fabricated subject from
-- `cmd/synthload`, and `tools/synthshots` regenerates them in a minute.

-- +goose Up

-- ---------------------------------------------------------------------------
-- 1. Which agents must be grounded
-- ---------------------------------------------------------------------------

ALTER TABLE core.ai_agent
  -- True unless somebody deliberately said otherwise. See the header: the default is the whole
  -- of the guarantee, and the column is only how an exception is written down.
  ADD COLUMN grounding_required boolean NOT NULL DEFAULT true,
  -- Twenty characters, the same floor `core.ai_synthetic_subject.reason` uses and for the same
  -- reason: "n/a" is not a reason, and an exemption nobody can defend in a sentence is one nobody
  -- should have granted.
  ADD COLUMN grounding_exempt_reason text NOT NULL DEFAULT '',
  ADD CONSTRAINT ai_agent_exemption_is_argued CHECK (
    grounding_required OR length(btrim(grounding_exempt_reason)) >= 20);

COMMENT ON COLUMN core.ai_agent.grounding_required IS
  'Whether §10.2 step 4 applies to this agent''s answers. Defaults to true; an exemption must name its reason. Never taken from a request (ADR-0032, ADR-0034).';

-- The gateway's own fixture is the one exemption, and it is worth stating exactly what is being
-- exempted. `gateway.echo` answers with a one-line restatement and **a count of the payload's
-- top-level fields** — a number that is correct, is derived from what it was shown, and is not in
-- the payload anywhere. It is also, by CP70's own description, an agent that summarises nothing
-- and is shown to nobody. Grounding a fixture whose whole job is to return a number it computed
-- would prove only that the check works, at the cost of every test in CP70's suite.
UPDATE core.ai_agent
   SET grounding_required = false,
       grounding_exempt_reason =
         'The gateway''s own fixture: its answer is a field count it computed rather than a clinical claim, and no clinician is ever shown it.'
 WHERE agent_code = 'gateway.echo';

-- ---------------------------------------------------------------------------
-- 2. The verdict on every call
-- ---------------------------------------------------------------------------

ALTER TABLE core.ai_interaction
  DROP CONSTRAINT ai_interaction_status_check;

ALTER TABLE core.ai_interaction
  ADD CONSTRAINT ai_interaction_status_check CHECK (status IN (
    'IN_FLIGHT',
    'SUCCEEDED',
    'CACHED',
    'INVALID_OUTPUT',
    'PROVIDER_ERROR',
    'TIMEOUT',
    'CIRCUIT_OPEN',
    'REFUSED_TIER',
    'REFUSED_PHI',
    -- New at CP72, and deliberately a sibling of INVALID_OUTPUT rather than a kind of it. A
    -- malformed answer is a transport failure and is retried; an answer that says something the
    -- model was not shown is a **quality** failure and is not. They are counted separately on the
    -- dashboard because they call for different work: one is a prompt that has drifted from its
    -- schema, the other is a model that is inventing.
    'UNGROUNDED'));

ALTER TABLE core.ai_interaction
  -- NOT_CHECKED is the honest state of every row written before this migration, and it is a
  -- different fact from PASSED. A backfill that wrote PASSED across the history would be this
  -- system asserting that it had checked things it had never looked at, which is the exact
  -- category of claim the checkpoint exists to make impossible.
  ADD COLUMN grounding_state text NOT NULL DEFAULT 'NOT_CHECKED'
    CHECK (grounding_state IN ('NOT_CHECKED', 'NOT_REQUIRED', 'PASSED', 'FAILED')),
  ADD COLUMN grounding_findings integer NOT NULL DEFAULT 0 CHECK (grounding_findings >= 0),

  -- **The sharp one.** An answer whose grounding failed may not be recorded as anything a caller
  -- would treat as an answer. This is the constraint that makes "violations block display" true
  -- of the record rather than of the code path: a future gateway that logged the verdict and
  -- returned the output anyway would write a row PostgreSQL refuses.
  ADD CONSTRAINT ai_interaction_a_failed_grounding_is_not_a_success CHECK (
    grounding_state <> 'FAILED' OR status = 'UNGROUNDED'),

  -- And the other way round: an UNGROUNDED row names how many findings it had, so that a row
  -- claiming the status without the evidence cannot exist. Zero findings with a failed verdict is
  -- a validator that refused an answer it could not name a fault in.
  ADD CONSTRAINT ai_interaction_ungrounded_says_what_was_wrong CHECK (
    status <> 'UNGROUNDED' OR (grounding_state = 'FAILED' AND grounding_findings > 0));

COMMENT ON COLUMN core.ai_interaction.grounding_state IS
  'The §10.2 step 4 verdict on this answer. NOT_CHECKED means the call predates CP72 or never produced an answer; NOT_REQUIRED means core.ai_agent exempts this agent.';

-- The quality query the dashboard runs: how did this agent's answers ground, over a window.
CREATE INDEX ai_interaction_grounding
  ON core.ai_interaction (agent_code, started_at DESC, grounding_state);

-- ---------------------------------------------------------------------------
-- 3. The defect
-- ---------------------------------------------------------------------------

-- One row per finding, not per call: a summary that invented two numbers and a date is three
-- things somebody has to look at, and collapsing them to "this answer failed" loses the shape of
-- the failure — which is the first thing anybody asks when the rate moves.
CREATE TABLE core.ai_grounding_defect (
  id uuid PRIMARY KEY,
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  -- The call this came out of. NOT NULL and a real foreign key, because the defect is worthless
  -- without the payload and the raw answer beside it, and both live on that row. This is the
  -- opposite of the decision taken for `subject_patient_id` below, and the difference is that an
  -- interaction always exists — the gateway writes it before the provider is contacted.
  ai_interaction_id uuid NOT NULL REFERENCES core.ai_interaction(id),

  -- Copied rather than joined. §10.5's regression question is "which prompt version and which
  -- model version were producing these", and answering it must not depend on a join that a
  -- retention sweep of the interaction table would one day break. It is also what makes a defect
  -- readable on its own, which is what a week-old defect has to be.
  agent_code     text NOT NULL REFERENCES core.ai_agent(agent_code),
  prompt_version text NOT NULL,
  model_version  text NOT NULL,

  -- Who the answer was about. Not a foreign key, for the reason `core.ai_interaction` gives at
  -- length: the subject of an AI call is whatever the calling agent named, and refusing to record
  -- a defect because its subject is not a patient row would refuse exactly the defects whose
  -- provenance is least clear.
  subject_patient_id uuid,
  subject_pseudonym  text NOT NULL DEFAULT '',

  -- Which arm of the check fired. Four, because they fail for different reasons and are fixed by
  -- different people: CITATION is a model ignoring the instruction, NUMBER and DATE are a model
  -- inventing, DRUG is a name nothing in this system can verify.
  arm text NOT NULL CHECK (arm IN ('CITATION', 'NUMBER', 'DATE', 'DRUG')),

  -- Where in the answer, in the agent's own vocabulary: `narrative_en`, `red_flags[0].statement`.
  -- A reviewer reads this before they read anything else.
  path text NOT NULL,
  -- The offending token itself, and the sentence around it. Both bounded, and the excerpt carries
  -- the same identifier check the outbound payload does — see the constraint below.
  token   text NOT NULL CHECK (btrim(token) <> '' AND length(token) <= 200),
  excerpt text NOT NULL DEFAULT '' CHECK (length(excerpt) <= 400),
  reason  text NOT NULL,

  detected_at timestamptz NOT NULL DEFAULT now(),

  -- The review. Open until somebody says otherwise, and what they say is the measurement
  -- criterion 2 is really about: a validator's false-positive rate in production is not knowable
  -- from a frozen test set, and this column is the only place it can come from.
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'REVIEWED')),
  classification text CHECK (classification IS NULL OR classification IN (
    -- The model really did say something it was not shown. The defect is the model's.
    'TRUE_POSITIVE',
    -- The claim was fine and the check was wrong about it. The defect is ours, and this is the
    -- number that decides whether the check may stay as strict as it is.
    'FALSE_POSITIVE',
    -- Neither: the check fired on something that is not a claim at all — a parsing fault in the
    -- validator itself. Kept apart from FALSE_POSITIVE because the fix is different and because
    -- burying a parser bug inside a clinical false-positive rate would flatter both numbers.
    'VALIDATOR_DEFECT')),
  reviewed_by uuid REFERENCES core.app_user(id),
  reviewed_at timestamptz,
  review_note text NOT NULL DEFAULT '',

  CONSTRAINT ai_grounding_defect_review_is_complete CHECK (
    (status = 'REVIEWED') = (classification IS NOT NULL AND reviewed_at IS NOT NULL)),

  -- Saying the check was wrong requires saying why. A false-positive rate assembled from
  -- unexplained clicks is a number that will be used to loosen the check, and the loosening will
  -- be defended with the number.
  CONSTRAINT ai_grounding_defect_disagreement_is_argued CHECK (
    classification IS NULL OR classification = 'TRUE_POSITIVE'
    OR length(btrim(review_note)) >= 20),

  -- The excerpt comes from the model's answer **before** the gateway restores the subject's
  -- identifiers, so it names a pseudonym rather than a person. This catches the case where that
  -- is not true — a future path that validated the restored answer instead — and it is the same
  -- function `core.ai_interaction.outbound` and `core.ai_synthesis.context` are guarded by.
  --
  -- The residual risk is CP70's, unchanged: a bare given name in prose is caught by nothing here,
  -- and the mitigation is that a model writing a name at all has broken an instruction the
  -- citation arm is looking for.
  CONSTRAINT ai_grounding_defect_excerpt_names_nobody CHECK (
    NOT ops.text_carries_identifier(excerpt))
);

-- The queue a reviewer works: what is open, newest first.
CREATE INDEX ai_grounding_defect_open
  ON core.ai_grounding_defect (facility_id, detected_at DESC)
  WHERE status = 'OPEN';
-- The rate query: by agent, by arm, over a window.
CREATE INDEX ai_grounding_defect_rate
  ON core.ai_grounding_defect (facility_id, agent_code, detected_at DESC);
CREATE INDEX ai_grounding_defect_by_interaction
  ON core.ai_grounding_defect (ai_interaction_id);

GRANT SELECT, INSERT, UPDATE ON core.ai_grounding_defect TO dthcms_app;
-- A defect that can be deleted is a defect that gets deleted, on the morning the rate is being
-- reported. The same rule `core.ai_interaction` follows, for a stronger reason.
REVOKE DELETE ON core.ai_grounding_defect FROM dthcms_app;

COMMENT ON TABLE core.ai_grounding_defect IS
  'One grounding violation: what the model said, which arm of the check caught it, and a human''s verdict on whether the check was right (CP72). Never retried, never deleted.';

-- ---------------------------------------------------------------------------
-- 4. The summary the physician reads
-- ---------------------------------------------------------------------------

ALTER TABLE core.ai_synthesis
  DROP CONSTRAINT ai_synthesis_failure_kind_check;

ALTER TABLE core.ai_synthesis
  ADD CONSTRAINT ai_synthesis_failure_kind_check CHECK (
    failure_kind IS NULL OR failure_kind IN
      ('ASSEMBLY', 'REFUSED', 'PROVIDER', 'TIMEOUT', 'INVALID_OUTPUT', 'INTERNAL',
       -- The answer arrived, passed its schema, and said something the record does not support.
       -- Its own class because the sentence the physician is shown is different from all six
       -- others: the system is not unavailable, it is withholding.
       'UNGROUNDED'));

ALTER TABLE core.ai_synthesis
  ADD COLUMN grounding_state text NOT NULL DEFAULT 'NOT_CHECKED'
    CHECK (grounding_state IN ('NOT_CHECKED', 'PASSED', 'FAILED')),
  ADD COLUMN grounding_findings integer NOT NULL DEFAULT 0 CHECK (grounding_findings >= 0);

COMMENT ON COLUMN core.ai_synthesis.grounding_state IS
  'The §10.2 step 4 verdict. A READY row must be PASSED — see ai_synthesis_ready_is_grounded. NOT_CHECKED belongs to runs that predate CP72 or never reached a model.';

-- The twenty summaries CP71 left behind, moved **before** the constraint below is added rather
-- than exempted from it. See the header for why, and for why this is safe here and would not be
-- on a system holding real patients. The order is the point: a constraint added first would have
-- refused the migration, which is the correct behaviour of a constraint and the wrong shape for a
-- deployment — the decision about the old rows has to be taken explicitly, here, in writing.
UPDATE core.ai_synthesis
   SET state = 'FAILED',
       failure_kind = 'UNGROUNDED',
       grounding_state = 'NOT_CHECKED',
       failure_detail =
         'Produced before the grounding check existed (CP71, migration 00053) and therefore never checked. An unchecked summary is not one a physician may read; request a new one.'
 WHERE state = 'READY'
   AND grounding_state = 'NOT_CHECKED';

-- **The constraint this checkpoint is about.** `READY` is the one state whose narrative reaches a
-- screen. It now requires a verdict, and the only acceptable verdict is `PASSED`.
--
-- Note what is *not* accepted: `NOT_REQUIRED`. `core.ai_agent` could be edited to exempt
-- `clinical.synthesis` tomorrow, and this constraint does not consult it — an exemption on the
-- agent that briefs a physician would stop summaries being stored at all rather than stop them
-- being checked. Two guarantees are only two if the second does not read the first.
ALTER TABLE core.ai_synthesis
  ADD CONSTRAINT ai_synthesis_ready_is_grounded CHECK (
    state <> 'READY' OR grounding_state = 'PASSED');

-- ---------------------------------------------------------------------------
-- 5. The evaluation harness
-- ---------------------------------------------------------------------------

-- §10.5: *"every change to a prompt or model runs the frozen evaluation set in CI; results are
-- recorded, and a regression blocks the merge."* This is where they are recorded.
--
-- In `ops` rather than `core`, and singular rather than the plan's `ai_evaluation_runs`, because
-- both of those are house conventions this repository has kept for fifty migrations: `core` is
-- the clinic's records and this is an engineering measurement about a build, and every table in
-- this database is named for one row of itself.
--
-- Deliberately **not** facility-scoped. A run measures a prompt and a model against a frozen set
-- of fabricated cases; it is a property of the software, and giving it a facility would invite a
-- reading in which one clinic's build had been evaluated and another's had not.
CREATE TABLE ops.ai_evaluation_run (
  id uuid PRIMARY KEY,

  -- What was evaluated. All three are copied on to the row rather than looked up, for the same
  -- reason the defect copies them: this row has to answer "what was true when this passed" years
  -- after the prompt file has moved on.
  agent_code     text NOT NULL REFERENCES core.ai_agent(agent_code),
  prompt_version text NOT NULL,
  prompt_sha256  text NOT NULL CHECK (prompt_sha256 ~ '^[0-9a-f]{64}$'),
  model_version  text NOT NULL,

  -- **The frozen set's own hash**, and it is what makes the word "frozen" mean something. A gate
  -- run against a case set somebody quietly edited is a gate that passes by construction; the
  -- harness recomputes this from the files it just read and refuses to run if it disagrees with
  -- the committed manifest.
  case_set        text NOT NULL,
  case_set_sha256 text NOT NULL CHECK (case_set_sha256 ~ '^[0-9a-f]{64}$'),

  -- Which commit produced the numbers, when CI knows. Empty from a developer's laptop, which is
  -- honest: a result that cannot be tied to a revision is a result, not a gate.
  git_sha text NOT NULL DEFAULT '',
  -- 'replay' re-checks the frozen answers with no model contacted; 'live' calls the gateway.
  -- On the row because every number below means something different between the two, and a
  -- dashboard that mixed them would report a replay's zero cost as a saving.
  mode text NOT NULL CHECK (mode IN ('replay', 'live')),

  cases integer NOT NULL CHECK (cases > 0),

  -- Criterion 1: every injected hallucination is detected. `injected` counts the cases whose
  -- expected verdict is "this must be caught"; `detected` counts the ones that were.
  injected integer NOT NULL DEFAULT 0 CHECK (injected >= 0),
  detected integer NOT NULL DEFAULT 0 CHECK (detected >= 0),

  -- Criterion 2: the false-positive rate on outputs known to be correct. `correct` counts the
  -- cases whose expected verdict is "this must pass"; `blocked` counts the ones the validator
  -- wrongly refused. The rate is the quotient and is deliberately not stored — a stored quotient
  -- is a number that stops agreeing with its own numerator.
  correct integer NOT NULL DEFAULT 0 CHECK (correct >= 0),
  blocked integer NOT NULL DEFAULT 0 CHECK (blocked >= 0),

  -- The other three metrics the checkpoint names. Null rather than zero in replay mode, because
  -- a replay contacts nobody and a zero here would read as a free, instant model.
  schema_failures integer NOT NULL DEFAULT 0 CHECK (schema_failures >= 0),
  latency_p50_ms  integer CHECK (latency_p50_ms IS NULL OR latency_p50_ms >= 0),
  latency_p95_ms  integer CHECK (latency_p95_ms IS NULL OR latency_p95_ms >= 0),
  cost_micro_usd  bigint  CHECK (cost_micro_usd IS NULL OR cost_micro_usd >= 0),

  verdict text NOT NULL CHECK (verdict IN ('PASS', 'FAIL')),
  -- What regressed, in the sentence CI printed. Required on a failure: a red gate that does not
  -- say what it caught is a gate somebody re-runs until it is green.
  regression_detail text NOT NULL DEFAULT '',

  started_at  timestamptz NOT NULL,
  finished_at timestamptz NOT NULL,

  CONSTRAINT ai_evaluation_run_counts_are_possible CHECK (
    detected <= injected AND blocked <= correct AND injected + correct <= cases),
  CONSTRAINT ai_evaluation_run_failure_says_why CHECK (
    verdict <> 'FAIL' OR btrim(regression_detail) <> ''),
  -- A live run measures latency; a replay cannot. Stated as a constraint so that the mode and the
  -- numbers cannot disagree.
  CONSTRAINT ai_evaluation_run_live_measures_time CHECK (
    mode <> 'live' OR latency_p50_ms IS NOT NULL),
  CONSTRAINT ai_evaluation_run_replay_measures_nothing_it_did_not_do CHECK (
    mode <> 'replay' OR (latency_p50_ms IS NULL AND latency_p95_ms IS NULL AND cost_micro_usd IS NULL))
);

CREATE INDEX ai_evaluation_run_recent ON ops.ai_evaluation_run (agent_code, finished_at DESC);

GRANT SELECT, INSERT ON ops.ai_evaluation_run TO dthcms_app;
REVOKE UPDATE, DELETE ON ops.ai_evaluation_run FROM dthcms_app;

COMMENT ON TABLE ops.ai_evaluation_run IS
  'One run of the frozen evaluation set against one prompt version and model version (CP72, §10.5). Append-only: a trend somebody can edit is not a trend.';

-- The per-case detail. Without it the run row says "nineteen of twenty" and nobody can find the
-- twentieth, which is the only case anybody wants.
CREATE TABLE ops.ai_evaluation_case (
  run_id  uuid NOT NULL REFERENCES ops.ai_evaluation_run(id) ON DELETE CASCADE,
  case_id text NOT NULL,

  -- What the frozen set says should happen to this case.
  expectation text NOT NULL CHECK (expectation IN ('GROUNDED', 'HALLUCINATION')),
  -- What did happen. The four are the confusion matrix, named for what they mean rather than for
  -- their position in it, because "false negative" is the one everybody reads backwards.
  outcome text NOT NULL CHECK (outcome IN (
    'CLEAN',          -- expected to pass, passed
    'DETECTED',       -- expected to be caught, caught
    'MISSED',         -- expected to be caught, passed. Criterion 1 fails on any of these.
    'FALSE_POSITIVE', -- expected to pass, was blocked. Criterion 2 is the count of these.
    'SCHEMA_FAILURE')), -- the answer no longer satisfies the agent's schema at all

  findings jsonb NOT NULL DEFAULT '[]'::jsonb,
  latency_ms integer CHECK (latency_ms IS NULL OR latency_ms >= 0),

  PRIMARY KEY (run_id, case_id)
);

GRANT SELECT, INSERT ON ops.ai_evaluation_case TO dthcms_app;
REVOKE UPDATE, DELETE ON ops.ai_evaluation_case FROM dthcms_app;

COMMENT ON TABLE ops.ai_evaluation_case IS
  'What happened to each case in one evaluation run. The run row carries the totals; this is where the case that regressed is named.';

-- ---------------------------------------------------------------------------
-- 6. The permission on the review
-- ---------------------------------------------------------------------------

-- Reading a defect is `ai.gateway.read`, which already exists and already reaches exactly the
-- right three roles. A second read permission over the same content — a defect *is* an excerpt of
-- the outbound log's answer — would be an access rule that has to be kept in step with the one it
-- copies, and the day it drifted the drift would be in the permissive direction.
--
-- Classifying one is its own permission, and sensitive. Sensitive because you cannot classify what
-- you have not read, and a permission that let somebody act on content they may not see would be a
-- rule with a hole in the shape of the act. Its own permission because recording "the check was
-- wrong" is the input to the number that decides whether the check may be loosened, and that is a
-- judgement the clinic should be able to grant separately from the ability to read the log.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('ai.quality.review', 'ai', 'quality', 'review',
   'Record a verdict on a grounding defect: whether the model invented something or the check was wrong',
   true)
ON CONFLICT (code) DO UPDATE SET
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'ai.quality.review'
  FROM core.role r
 -- The three roles that already read the outbound log. QA owns the queue in practice (§14.1);
 -- the physician is on it because deciding whether a clinical claim was really unsupported is a
 -- clinical judgement, and admin because somebody has to be able to when neither is in.
 WHERE r.code IN ('QA', 'PHYSICIAN', 'ADMIN')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- 7. The invariants
-- ---------------------------------------------------------------------------

-- The check constraint above refuses such a row from here on. This notices one that arrived some
-- other way — a restore from a dump taken before this migration, a hand edit, a future code path.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_ungrounded_answer_was_recorded_as_a_success() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.ai_interaction
   WHERE grounding_state = 'FAILED' AND status <> 'UNGROUNDED';
  IF offenders > 0 THEN
    RAISE EXCEPTION
      '% AI answers failed the grounding check and are recorded as though they had not', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- *"Grounding violations recorded as defects, not silently retried."* An UNGROUNDED interaction
-- with no defect beside it is the silent retry happening: the answer was thrown away and nothing
-- was written down about what it said, which is the failure mode this checkpoint's whole design is
-- arranged against.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_ungrounded_answer_left_a_defect() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.ai_interaction i
   WHERE i.status = 'UNGROUNDED'
     AND NOT EXISTS (SELECT 1 FROM core.ai_grounding_defect d WHERE d.ai_interaction_id = i.id);
  IF offenders > 0 THEN
    RAISE EXCEPTION
      '% AI answers were refused for grounding and left no defect record; the evidence has been discarded',
      offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- §10.6's fifth permanent invariant, over the one table whose rows reach a physician:
-- *"every numeric claim is grounded against structured data before display."*
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_summary_a_physician_reads_is_ungrounded() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.ai_synthesis
   WHERE state = 'READY' AND grounding_state <> 'PASSED';
  IF offenders > 0 THEN
    RAISE EXCEPTION
      '% pre-consultation summaries are readable by a physician without having passed the grounding check',
      offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- A run whose prompt hash, model version or case-set hash is missing is a green tick nobody can
-- reproduce, and §10.5's regression policy rests entirely on being able to say *what changed*
-- between two runs. The check constraints make the columns non-empty; this catches the run whose
-- prompt hash names a version this build has never contained, which is what a hand-inserted row
-- looks like.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_evaluation_run_names_what_it_ran() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM ops.ai_evaluation_run r
   WHERE btrim(r.prompt_version) = ''
      OR btrim(r.model_version) = ''
      OR btrim(r.case_set) = ''
      OR NOT EXISTS (
           SELECT 1 FROM core.ai_prompt_version p
            WHERE p.agent_code = r.agent_code AND p.version = r.prompt_version
              AND p.content_sha256 = r.prompt_sha256);
  IF offenders > 0 THEN
    RAISE EXCEPTION
      '% AI evaluation runs do not name a prompt version this database has ever deployed', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_no_ungrounded_answer_was_recorded_as_a_success',
   'no AI answer that failed the grounding check is recorded as though it had succeeded', 106),
  ('assert_every_ungrounded_answer_left_a_defect',
   'every AI answer refused for grounding left a defect record naming what was wrong', 107),
  ('assert_no_summary_a_physician_reads_is_ungrounded',
   'no pre-consultation summary is readable by a physician without having passed the grounding check', 108),
  ('assert_every_evaluation_run_names_what_it_ran',
   'every AI evaluation run names a prompt version and content hash this database has deployed', 109)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_no_ungrounded_answer_was_recorded_as_a_success',
  'assert_every_ungrounded_answer_left_a_defect',
  'assert_no_summary_a_physician_reads_is_ungrounded',
  'assert_every_evaluation_run_names_what_it_ran');
DROP FUNCTION IF EXISTS core.assert_every_evaluation_run_names_what_it_ran();
DROP FUNCTION IF EXISTS core.assert_no_summary_a_physician_reads_is_ungrounded();
DROP FUNCTION IF EXISTS core.assert_every_ungrounded_answer_left_a_defect();
DROP FUNCTION IF EXISTS core.assert_no_ungrounded_answer_was_recorded_as_a_success();

DELETE FROM core.role_permission WHERE permission_code = 'ai.quality.review';
DELETE FROM core.permission WHERE code = 'ai.quality.review';

DROP TABLE IF EXISTS ops.ai_evaluation_case;
DROP TABLE IF EXISTS ops.ai_evaluation_run;

ALTER TABLE core.ai_synthesis
  DROP CONSTRAINT IF EXISTS ai_synthesis_ready_is_grounded,
  DROP COLUMN IF EXISTS grounding_findings,
  DROP COLUMN IF EXISTS grounding_state;
ALTER TABLE core.ai_synthesis DROP CONSTRAINT IF EXISTS ai_synthesis_failure_kind_check;
UPDATE core.ai_synthesis SET failure_kind = 'INTERNAL' WHERE failure_kind = 'UNGROUNDED';
ALTER TABLE core.ai_synthesis
  ADD CONSTRAINT ai_synthesis_failure_kind_check CHECK (
    failure_kind IS NULL OR failure_kind IN
      ('ASSEMBLY', 'REFUSED', 'PROVIDER', 'TIMEOUT', 'INVALID_OUTPUT', 'INTERNAL'));

DROP TABLE IF EXISTS core.ai_grounding_defect;

DROP INDEX IF EXISTS core.ai_interaction_grounding;
ALTER TABLE core.ai_interaction
  DROP CONSTRAINT IF EXISTS ai_interaction_ungrounded_says_what_was_wrong,
  DROP CONSTRAINT IF EXISTS ai_interaction_a_failed_grounding_is_not_a_success,
  DROP COLUMN IF EXISTS grounding_findings,
  DROP COLUMN IF EXISTS grounding_state;
DELETE FROM core.ai_interaction WHERE status = 'UNGROUNDED';
ALTER TABLE core.ai_interaction DROP CONSTRAINT IF EXISTS ai_interaction_status_check;
ALTER TABLE core.ai_interaction
  ADD CONSTRAINT ai_interaction_status_check CHECK (status IN (
    'IN_FLIGHT', 'SUCCEEDED', 'CACHED', 'INVALID_OUTPUT', 'PROVIDER_ERROR',
    'TIMEOUT', 'CIRCUIT_OPEN', 'REFUSED_TIER', 'REFUSED_PHI'));

ALTER TABLE core.ai_agent
  DROP CONSTRAINT IF EXISTS ai_agent_exemption_is_argued,
  DROP COLUMN IF EXISTS grounding_exempt_reason,
  DROP COLUMN IF EXISTS grounding_required;
