-- The job queue (CP69, §8.4, §8.5, §7.1).
--
-- # Why this is a table here rather than River
--
-- ADR-0031, in full. The short version: §8.4's reason for River is *"Postgres-backed: no dual-write
-- between the database and a broker"*, and that property is what a table in this database is. What
-- River would additionally have brought — a framework's own `args` column — is the one thing this
-- checkpoint cannot afford, because *"job payloads may reference but must not embed PHI"* is a rule
-- this system enforces with a check constraint and an invariant rather than with a convention.
--
-- # The four things this schema exists to make true
--
--  1. **A job enqueued in a rolled-back transaction never runs.** It is a row. There is nothing to
--     roll back separately, and no non-transactional path to enqueue through.
--  2. **A failed job retries per policy and then dead-letters visibly.** `ops.job_attempt` keeps
--     every failure with its error, so a discarded job is a thing an operator can read rather than
--     a counter that went up.
--  3. **The SLA is measured.** `sla_deadline` is stamped at enqueue from the kind's own budget and
--     resolved to `met_sla` at completion. §7.1's five minutes becomes a number rather than a claim.
--  4. **A worker restart causes no duplicate execution** — or rather, no duplicate *effect*.
--     Delivery is at-least-once and honestly so: a lease that expires returns the job to the queue,
--     which is the only recovery available to a queue that is not writing its completion inside the
--     work's own transaction. Every job here is idempotent, which §8.5 already required.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The PHI key list, in the database
-- ---------------------------------------------------------------------------

-- `logging.PHIKeys` has been the single source of truth for "this key must never carry a value",
-- enforced statically by dthclint and dynamically by the log handler. A job argument needs the same
-- rule checked by a third thing — a constraint — and a constraint cannot read a Go map.
--
-- So the list is a table, and a Go test compares the two in both directions. The same treatment the
-- permission catalogue and the event registry get, for the same reason: two representations of one
-- list is a thing that drifts, and the drift is silent.
CREATE TABLE ops.phi_key (
  key text PRIMARY KEY,
  -- What to do instead. A rule that does not say gets worked around.
  guidance text NOT NULL,
  CONSTRAINT phi_key_lowercase CHECK (key = lower(key)),
  CONSTRAINT phi_key_says_what_to_do_instead CHECK (btrim(guidance) <> '')
);

GRANT SELECT ON ops.phi_key TO dthcms_app;

INSERT INTO ops.phi_key (key, guidance) VALUES
  ('name',            'log patient_id instead'),
  ('patient_name',    'log patient_id instead'),
  ('full_name',       'log patient_id instead'),
  ('name_bn',         'log patient_id instead'),
  ('name_en',         'log patient_id instead'),
  ('nid',             'national IDs must never be logged, not even masked'),
  ('national_id',     'national IDs must never be logged, not even masked'),
  ('national_id_raw', 'national IDs must never be logged'),
  ('phone',           'log patient_id instead'),
  ('mobile',          'log patient_id instead'),
  ('address',         'log patient_id instead'),
  ('dob',             'log age_years or age_months if you need it'),
  ('date_of_birth',   'log age_years or age_months if you need it'),
  ('email',           'log user_id instead'),
  ('photo',           'never log image data or its location'),
  ('diagnosis',       'clinical detail belongs in the event ledger, not in logs'),
  ('prescription',    'clinical detail belongs in the event ledger, not in logs'),
  ('password',        'never log credentials, even hashed'),
  ('token',           'never log credentials'),
  ('secret',          'never log credentials'),
  ('otp',             'never log authentication codes'),
  ('totp_secret',     'never log authentication secrets')
ON CONFLICT (key) DO UPDATE SET guidance = EXCLUDED.guidance;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('ops', 'phi_key', 'A list of words that must not appear is not a clinic''s data.')
ON CONFLICT DO NOTHING;

-- Whether a JSON object carries a key that must never hold a value.
--
-- Recursive, because `{"patient": {"name": "..."}}` hides it one level down and a check that only
-- looked at the top level would pass exactly the payload somebody was most likely to write.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ops.carries_phi(p jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
  k text;
  v jsonb;
BEGIN
  IF p IS NULL OR jsonb_typeof(p) = 'null' THEN
    RETURN false;
  END IF;
  IF jsonb_typeof(p) = 'array' THEN
    FOR v IN SELECT value FROM jsonb_array_elements(p) LOOP
      IF ops.carries_phi(v) THEN
        RETURN true;
      END IF;
    END LOOP;
    RETURN false;
  END IF;
  IF jsonb_typeof(p) <> 'object' THEN
    RETURN false;
  END IF;
  FOR k, v IN SELECT key, value FROM jsonb_each(p) LOOP
    -- The suffix match is the same one the log handler does: `patient_name` and
    -- `guardian.phone` are the shapes a developer reaches for when the bare key feels wrong.
    IF EXISTS (SELECT 1 FROM ops.phi_key
                WHERE lower(k) = key
                   OR lower(k) LIKE '%\_' || key) THEN
      RETURN true;
    END IF;
    IF ops.carries_phi(v) THEN
      RETURN true;
    END IF;
  END LOOP;
  RETURN false;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION ops.carries_phi(jsonb) TO dthcms_app;

-- ---------------------------------------------------------------------------
-- The catalogue of job kinds
-- ---------------------------------------------------------------------------

-- A registered catalogue rather than free strings, and the reason is the dashboard.
--
-- A queue-health page built from what is currently in the queue can only show job types that have
-- something in flight. It therefore cannot show the failure it exists to catch: a job type that has
-- **stopped being enqueued at all**. The synthesis job silently not running is exactly the thing
-- §7.1 is worried about, and a dashboard that renders an empty list when nothing is queued reports
-- it as health.
CREATE TABLE ops.job_kind (
  kind text PRIMARY KEY,

  -- §8.5's five classes. The class decides the defaults; a kind may still differ.
  job_class text NOT NULL CHECK (job_class IN
    ('CLINICAL_CRITICAL', 'PATIENT_FACING', 'PIPELINE', 'ANALYTICAL', 'MAINTENANCE')),

  queue text NOT NULL DEFAULT 'default',
  -- Higher runs first. A single integer rather than a priority per class, because a worker
  -- claiming work needs one ORDER BY and an operator reading the table needs one number.
  priority integer NOT NULL DEFAULT 100,

  max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 20),
  -- The first retry waits this long; each subsequent one doubles, with jitter, capped.
  backoff_seconds integer NOT NULL DEFAULT 10 CHECK (backoff_seconds BETWEEN 1 AND 3600),
  backoff_cap_seconds integer NOT NULL DEFAULT 3600
    CHECK (backoff_cap_seconds BETWEEN 1 AND 86400),

  -- How long this kind has, from enqueue to finish, before it has missed. Null for a kind with no
  -- promise attached — most of them. §7.1's synthesis is the one that matters.
  sla_seconds integer CHECK (sla_seconds IS NULL OR sla_seconds BETWEEN 1 AND 86400),

  -- Bilingual, because the queue-health page is read by whoever is on the floor when it goes red.
  description_en text NOT NULL,
  description_bn text NOT NULL,

  -- An operator can stop a kind without a deploy. The alternative during an incident is a deploy.
  paused_at timestamptz,
  paused_by uuid REFERENCES core.app_user(id),

  CONSTRAINT job_kind_format CHECK (kind ~ '^[a-z][a-z0-9_.]{2,63}$'),
  CONSTRAINT job_kind_bilingual
    CHECK (btrim(description_en) <> '' AND btrim(description_bn) <> ''),
  CONSTRAINT job_kind_pause_is_attributed CHECK ((paused_at IS NULL) = (paused_by IS NULL)),
  CONSTRAINT job_kind_backoff_cap_is_a_cap CHECK (backoff_cap_seconds >= backoff_seconds)
);

GRANT SELECT, UPDATE ON ops.job_kind TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('ops', 'job_kind', 'The list of things this system does in the background is not a clinic''s data.')
ON CONFLICT DO NOTHING;

-- The kinds that exist today. §8.5's table, made concrete; the rest arrive with the checkpoints
-- that need them, each as one row in a migration.
INSERT INTO ops.job_kind
  (kind, job_class, queue, priority, max_attempts, backoff_seconds, backoff_cap_seconds,
   sla_seconds, description_en, description_bn) VALUES

  -- §7.1's five minutes, and the only kind in this migration that carries a deadline. The job
  -- itself is CP71's; the kind is registered now so that the dashboard can show it missing rather
  -- than show nothing.
  ('clinical.synthesis', 'CLINICAL_CRITICAL', 'clinical', 900, 5, 15, 300, 300,
   'Assemble a patient''s AI summary before they reach the consultation room',
   'রোগী পরামর্শ কক্ষে পৌঁছানোর আগে তাঁর এআই সারসংক্ষেপ তৈরি করা'),

  ('clinical.alert_escalation', 'CLINICAL_CRITICAL', 'clinical', 900, 5, 10, 120, 60,
   'Advance an unacknowledged critical value to the next step of its escalation chain',
   'অস্বীকৃত জরুরি ফলাফলকে এসকেলেশন ধাপের পরের ধাপে নেওয়া'),

  ('patient.sms', 'PATIENT_FACING', 'patient', 700, 8, 20, 1800, NULL,
   'Send a message to a patient''s phone',
   'রোগীর ফোনে বার্তা পাঠানো'),

  ('patient.prescription_pdf', 'PATIENT_FACING', 'patient', 700, 8, 20, 1800, NULL,
   'Render a prescription for printing',
   'ছাপার জন্য ব্যবস্থাপত্র তৈরি করা'),

  ('pipeline.ocr', 'PIPELINE', 'pipeline', 500, 3, 30, 900, NULL,
   'Read an uploaded document and extract what it says',
   'আপলোড করা কাগজ পড়ে তার তথ্য বের করা'),

  ('analytical.research_extract', 'ANALYTICAL', 'analytical', 200, 3, 300, 3600, NULL,
   'Refresh the de-identified research extract',
   'পরিচয়হীন গবেষণা তথ্যভাণ্ডার হালনাগাদ করা'),

  ('maintenance.idempotency_purge', 'MAINTENANCE', 'maintenance', 100, 3, 60, 3600, NULL,
   'Remove expired idempotency records',
   'মেয়াদোত্তীর্ণ আইডেমপোটেন্সি রেকর্ড মুছে ফেলা'),

  ('maintenance.ledger_verify', 'MAINTENANCE', 'maintenance', 100, 3, 60, 3600, NULL,
   'Verify the event ledger''s hash chain',
   'ইভেন্ট লেজারের হ্যাশ চেইন যাচাই করা'),

  ('maintenance.lease_reap', 'MAINTENANCE', 'maintenance', 100, 3, 30, 600, NULL,
   'Return jobs abandoned by a stopped worker to the queue',
   'বন্ধ হয়ে যাওয়া ওয়ার্কারের ফেলে রাখা কাজ আবার সারিতে ফেরানো')
ON CONFLICT (kind) DO UPDATE SET
  job_class = EXCLUDED.job_class, queue = EXCLUDED.queue, priority = EXCLUDED.priority,
  max_attempts = EXCLUDED.max_attempts, backoff_seconds = EXCLUDED.backoff_seconds,
  backoff_cap_seconds = EXCLUDED.backoff_cap_seconds, sla_seconds = EXCLUDED.sla_seconds,
  description_en = EXCLUDED.description_en, description_bn = EXCLUDED.description_bn;

-- ---------------------------------------------------------------------------
-- The jobs
-- ---------------------------------------------------------------------------

CREATE TABLE ops.job (
  id uuid PRIMARY KEY,
  kind text NOT NULL REFERENCES ops.job_kind(kind),
  queue text NOT NULL,
  priority integer NOT NULL,

  -- **May reference, must not embed.** Ids, codes, counts. Never a name, a number, a value.
  -- The constraint is the enforcement and invariant 90 re-checks the whole table, because a
  -- constraint added later does not validate what is already there.
  args jsonb NOT NULL DEFAULT '{}'::jsonb,

  -- Enqueue-once. A partial unique index below makes a second live job with the same key
  -- impossible, which is what lets a caller enqueue from a retry loop without thinking.
  dedupe_key text,

  status text NOT NULL DEFAULT 'AVAILABLE'
    CHECK (status IN ('AVAILABLE', 'RUNNING', 'SUCCEEDED', 'DISCARDED', 'CANCELLED')),

  attempt      integer NOT NULL DEFAULT 0,
  max_attempts integer NOT NULL,

  -- When it may next be picked up: now for an ordinary enqueue, later for a backoff or a
  -- scheduled job.
  run_at      timestamptz NOT NULL,
  enqueued_at timestamptz NOT NULL DEFAULT now(),

  -- Who holds it and until when. A lease rather than a lock, because a lock dies with the
  -- connection that took it and the case this has to survive is a worker that stopped answering
  -- rather than one that disconnected politely.
  leased_by    text,
  leased_until timestamptz,

  started_at  timestamptz,
  finished_at timestamptz,

  -- The promise, stamped at enqueue from the kind's budget rather than computed at read time, so
  -- that changing a kind's SLA does not retroactively rewrite whether last week was met.
  sla_deadline timestamptz,
  met_sla      boolean,

  -- The last failure, in the same terms an operator reads on the dashboard. Also PHI-checked: an
  -- error string is the classic place a patient's name reaches a screen nobody audited.
  last_error text NOT NULL DEFAULT '',

  CONSTRAINT job_args_carry_no_phi CHECK (NOT ops.carries_phi(args)),
  CONSTRAINT job_attempts_within_policy CHECK (attempt <= max_attempts),
  CONSTRAINT job_lease_is_whole CHECK ((leased_by IS NULL) = (leased_until IS NULL)),
  CONSTRAINT job_running_holds_a_lease
    CHECK (status <> 'RUNNING' OR (leased_by IS NOT NULL AND started_at IS NOT NULL)),
  CONSTRAINT job_finished_says_when
    CHECK ((status IN ('SUCCEEDED', 'DISCARDED', 'CANCELLED')) = (finished_at IS NOT NULL)),
  -- A dead-lettered job that says nothing is one nobody can act on, which defeats the point of
  -- making dead-lettering visible.
  CONSTRAINT job_discarded_says_why
    CHECK (status <> 'DISCARDED' OR btrim(last_error) <> '')
);

-- The claim query's index: available work, best first.
CREATE INDEX job_claimable ON ops.job (queue, priority DESC, run_at, enqueued_at)
  WHERE status = 'AVAILABLE';
-- The reaper's index: leases that have run out.
CREATE INDEX job_leased ON ops.job (leased_until) WHERE status = 'RUNNING';
-- The dashboard's index.
CREATE INDEX job_by_kind ON ops.job (kind, status, finished_at DESC);
-- Enqueue-once, while it is still live. A key may be reused once the job has finished, which is
-- what makes "one synthesis per visit, per attempt at that visit" expressible.
CREATE UNIQUE INDEX job_dedupe_live ON ops.job (kind, dedupe_key)
  WHERE dedupe_key IS NOT NULL AND status IN ('AVAILABLE', 'RUNNING');

GRANT SELECT, INSERT, UPDATE ON ops.job TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('ops', 'job', 'A work queue is the system''s own bookkeeping, not a clinic''s records.')
ON CONFLICT DO NOTHING;

-- Every failure, kept.
--
-- Separate from `last_error` because "it failed five times" and "here is what it said each time"
-- are different questions, and the second is the one somebody asks at the point they are trying to
-- fix it. Bounded by max_attempts, so a row here costs at most twenty per job.
CREATE TABLE ops.job_attempt (
  job_id  uuid NOT NULL REFERENCES ops.job(id) ON DELETE CASCADE,
  attempt integer NOT NULL,

  worker    text NOT NULL,
  failed_at timestamptz NOT NULL,
  -- How long it ran before it failed. A job that fails after four minutes and one that fails
  -- immediately are different problems.
  ran_for_ms bigint NOT NULL DEFAULT 0,
  error      text NOT NULL,

  PRIMARY KEY (job_id, attempt),
  CONSTRAINT job_attempt_says_something CHECK (btrim(error) <> '')
);

GRANT SELECT, INSERT ON ops.job_attempt TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('ops', 'job_attempt', 'Scoped by the job it belongs to, which is the system''s own bookkeeping.')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Periodic jobs
-- ---------------------------------------------------------------------------

-- Intervals rather than cron expressions, and ADR-0031 says why: the three periodic jobs this
-- system has are all "every N", and a cron parser is a dependency bought for a case that does not
-- exist yet.
--
-- The row is also the leader election. A worker claims the schedule with the same
-- `FOR UPDATE SKIP LOCKED` it claims a job with, so two workers running at once produce one
-- enqueue rather than two — which matters more here than for ordinary jobs, since a periodic job
-- has no natural dedupe key and "run the nightly audit twice" is a real cost.
CREATE TABLE ops.job_schedule (
  kind text PRIMARY KEY REFERENCES ops.job_kind(kind),

  every_seconds integer NOT NULL CHECK (every_seconds BETWEEN 10 AND 86400),
  args jsonb NOT NULL DEFAULT '{}'::jsonb,

  next_run_at timestamptz NOT NULL DEFAULT now(),
  last_run_at timestamptz,

  paused_at timestamptz,

  CONSTRAINT job_schedule_args_carry_no_phi CHECK (NOT ops.carries_phi(args))
);

GRANT SELECT, UPDATE ON ops.job_schedule TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('ops', 'job_schedule', 'When the system does its own housekeeping is not a clinic''s data.')
ON CONFLICT DO NOTHING;

INSERT INTO ops.job_schedule (kind, every_seconds) VALUES
  ('maintenance.idempotency_purge', 3600),
  ('maintenance.lease_reap', 60)
ON CONFLICT (kind) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Claiming, finishing, reaping
-- ---------------------------------------------------------------------------

-- Claiming, finishing and reaping are **not functions here**, and the reason is mundane rather
-- than architectural: the code generator cannot resolve the columns of a `RETURNS TABLE` function,
-- so a function would have had to be paired with a hand-written scanner or with a second copy of
-- the same SQL in the query file. One copy, in `internal/jobs/queries/jobs.sql`, generated and
-- type-checked against this schema, is better than two copies and a test holding them together.
--
-- Three things that would otherwise have been buried in a function body are worth stating here,
-- where somebody reading the schema will find them.
--
-- **Claiming uses `FOR UPDATE ... SKIP LOCKED`.** That is the whole concurrency story: two workers
-- running the claim at the same moment take disjoint sets rather than blocking on each other, and a
-- third arriving mid-statement takes whatever neither has locked. A paused kind is skipped rather
-- than claimed-and-requeued, so a pause takes effect on the next poll rather than after one more
-- run of everything already in flight.
--
-- **Reaping an expired lease is what criterion 5 rests on, and it buys at-least-once delivery, not
-- exactly-once.** A worker that finished the work and died before marking the row will run that job
-- again when its lease expires, and no queue that is not writing its completion inside the work's
-- own transaction can do better. Every job here is idempotent, which §8.5 already required. A job
-- whose attempts are spent is discarded rather than requeued, or one poisonous job would take the
-- queue down with it, over and over, forever.
--
-- **Health is computed over `ops.job_kind LEFT JOIN ops.job`**, so every registered kind is a row
-- whether or not anything is queued. A dashboard assembled from what is in the queue cannot report
-- the failure it exists to catch: a job type that has stopped being enqueued at all. `docs/jobs.md`
-- carries the query as a runbook snippet for an operator with psql at three in the morning.

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- The constraint guards rows written from here on; this notices one that arrived some other way,
-- and it is the check the "must not embed PHI" rule is really about.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_job_argument_carries_phi() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders FROM ops.job WHERE ops.carries_phi(args);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% job arguments carry a key that must never hold a value', offenders;
  END IF;
  SELECT count(*) INTO offenders FROM ops.job_schedule WHERE ops.carries_phi(args);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% scheduled job arguments carry a key that must never hold a value', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- Criterion 2's other half. A job that ran more times than its kind allows is a retry policy that
-- is not a policy, and the way it happens is a kind's `max_attempts` being lowered underneath jobs
-- already in flight.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_job_outlives_its_retry_policy() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders FROM ops.job WHERE attempt > max_attempts;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% jobs have run more times than their policy allows', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- A promise that was made must be resolved. Without this, a kind could carry an SLA, stamp
-- deadlines onto its jobs, and report 100% attainment because nothing ever recorded a miss.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_finished_promise_is_resolved() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM ops.job
   WHERE sla_deadline IS NOT NULL
     AND status IN ('SUCCEEDED', 'DISCARDED')
     AND met_sla IS NULL;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% finished jobs carried a deadline nobody resolved', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- Every kind reads in both languages, for the same reason every other catalogue here does: the
-- queue-health page is read by whoever is on the floor when it goes red.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_job_kind_reads_in_both_languages() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  SELECT kind INTO offender FROM ops.job_kind
   WHERE btrim(description_en) = '' OR btrim(description_bn) = '' LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'job kind % does not read in both languages', offender;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_no_job_argument_carries_phi',
   'no job argument carries a key that must never hold a value', 90),
  ('assert_no_job_outlives_its_retry_policy',
   'no job has run more times than its kind allows', 91),
  ('assert_every_finished_promise_is_resolved',
   'every finished job that carried an SLA deadline records whether it met it', 92),
  ('assert_every_job_kind_reads_in_both_languages',
   'every job kind reads in both languages', 93)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- ---------------------------------------------------------------------------
-- Who may see the queue, and who may touch it
-- ---------------------------------------------------------------------------

-- Two permissions rather than one, and the split is the same one CP50 made between reading the
-- alert board and acknowledging an alert. Reading queue health is looking at a graph; retrying a
-- dead-lettered job runs code against a patient's record, and pausing a kind stops the synthesis
-- that §7.1 promises will be ready before the consultation. Somebody who needs to know whether the
-- queue is healthy — the floor supervisor at nine in the morning — should not thereby be able to
-- turn it off.
--
-- Neither is sensitive: there is no patient in the queue by construction (invariant 90), and an
-- access review that had to justify "can look at a graph of background work" would be an access
-- review with one more line and no more meaning.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('ops.jobs.read', 'ops', 'jobs', 'read',
   'See the background work queue: depth, age, failures and whether the SLA is being met', false),
  ('ops.jobs.manage', 'ops', 'jobs', 'manage',
   'Retry or cancel a background job, and pause or resume a job type', false)
ON CONFLICT (code) DO UPDATE SET description = EXCLUDED.description;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'ops.jobs.read'
  FROM core.role r
 -- The physician is here for a reason particular to §7.1: the five minutes this queue
 -- promises is the wait before *their* consultation. A physician who can see that synthesis
 -- is eight minutes behind starts the consultation without it rather than waiting for
 -- something that is not coming, and that is a clinical decision rather than an operational
 -- one. QA reads it because a queue that has silently stopped is exactly the kind of thing
 -- their audits exist to notice.
 WHERE r.code IN ('ADMIN', 'QA', 'PHYSICIAN')
ON CONFLICT DO NOTHING;

-- Managing is narrower — the administrator alone. A physician who can see that synthesis is behind
-- does not need to be the one who pauses it, and the person who does needs to be findable
-- afterwards.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'ops.jobs.manage'
  FROM core.role r
 WHERE r.code = 'ADMIN'
ON CONFLICT DO NOTHING;

-- +goose Down

DELETE FROM core.role_permission WHERE permission_code IN ('ops.jobs.read', 'ops.jobs.manage');
DELETE FROM core.permission WHERE code IN ('ops.jobs.read', 'ops.jobs.manage');
DELETE FROM ops.invariant WHERE function_name IN (
  'assert_no_job_argument_carries_phi',
  'assert_no_job_outlives_its_retry_policy',
  'assert_every_finished_promise_is_resolved',
  'assert_every_job_kind_reads_in_both_languages');
DROP FUNCTION IF EXISTS core.assert_every_job_kind_reads_in_both_languages();
DROP FUNCTION IF EXISTS core.assert_every_finished_promise_is_resolved();
DROP FUNCTION IF EXISTS core.assert_no_job_outlives_its_retry_policy();
DROP FUNCTION IF EXISTS core.assert_no_job_argument_carries_phi();
DROP TABLE IF EXISTS ops.job_schedule;
DROP TABLE IF EXISTS ops.job_attempt;
DROP TABLE IF EXISTS ops.job;
DROP TABLE IF EXISTS ops.job_kind;
DROP FUNCTION IF EXISTS ops.carries_phi(jsonb);
DROP TABLE IF EXISTS ops.phi_key;
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'ops' AND table_name IN ('phi_key', 'job_kind', 'job', 'job_attempt', 'job_schedule');
