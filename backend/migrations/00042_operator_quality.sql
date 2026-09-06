-- The operator quality record (CP63, §4.3).
--
-- # This checkpoint is where the correction workflow either pays for itself or becomes paperwork
--
-- §4.3 names its own purpose: *"recurring patterns per operator surface so targeted retraining
-- happens and the same mistake does not repeat."* CP62 built the routing; without this the
-- corrections are a pile of rows nobody reads.
--
-- # The risk the plan states, and what it forces
--
-- The plan's own risk line: *"a metric that feels punitive damages data honesty — staff hide
-- errors instead of correcting them."* That is not a caveat to put in a README. It is a design
-- constraint, and it decided four things here:
--
-- 1. **An operator sees their own record, always, without asking.** Transparency is the whole
--    defence: a number somebody can see is a number they can argue with, and a number they can
--    argue with is one they will not hide from.
-- 2. **A rate is never shown without its denominator.** "Three corrections" against four hundred
--    entries and against forty are different facts, and only one of them is a problem.
-- 3. **A flag is raised on a threshold that is a row, unapproved until a clinician approves it.**
--    Same shape as the critical-value table (CP50): the mechanism ships, the numbers are a
--    proposal, and the API says so.
-- 4. **HR does not get this.** `hr.performance.read` already exists and HR holds it. It stays
--    what it is — throughput reporting — and the correction record gets its own permission,
--    granted to the clinical supervisor and to QA. The plan puts "performance-linked pay or
--    discipline" out of scope; a permission that hands an operator's error history to the
--    department that sets pay puts it back in, whatever the intention.
--
-- # Why there is no `operator_quality_records` aggregation table
--
-- The plan names one, and it is not built. This clinic records on the order of two thousand
-- values a day; a thirty-day window is sixty thousand rows, and counting them grouped by author
-- against an index is milliseconds. A nightly job would buy nothing and cost the one failure
-- this feature cannot survive: a job that stops quietly, leaving a supervisor reading numbers
-- that were true last Tuesday, and an operator told about a pattern they fixed a week ago.
--
-- The **flags** are stored, because a flag is a thing that happened: raised on a window that has
-- since slid past, acknowledged by a named person, and still legible a year later. The counts
-- behind it are frozen into the row at the moment it is raised, so the evidence does not move
-- when the window does.
--
-- # Why this is `core` and an audit entry, rather than the clinical ledger
--
-- ADR-0003 event-sources clinical data. A retraining flag is not clinical data — there is no
-- patient in it, and there must not be. It is an administrative fact about a member of staff,
-- which is exactly what `core.break_glass_access` and `core.admin_alert` already are: rows that
-- are never deleted, acknowledged in place, and linked to the security audit trail by
-- `audit_seq`. This follows them.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The thresholds
-- ---------------------------------------------------------------------------

-- What counts as a pattern worth somebody's attention. Rows because the plan lists the numbers
-- as an open decision requiring approval, and because a threshold in a constant is a threshold
-- that needs a release to argue with.
CREATE TABLE core.quality_threshold (
  code text PRIMARY KEY,

  -- Which of §4.3's three shapes this is. The plan names all three:
  --   TRANSCRIPTION  — repeated transcription errors
  --   SAME_CODE      — the same measurement type going wrong again and again
  --   END_OF_SHIFT   — corrections clustering late in the working day
  pattern text NOT NULL CHECK (pattern IN ('TRANSCRIPTION', 'SAME_CODE', 'END_OF_SHIFT')),

  -- How many, over how many days. Both on the row: "three in thirty days" and "three in a day"
  -- are different claims about the same operator.
  window_days integer NOT NULL CHECK (window_days BETWEEN 1 AND 365),
  min_count   integer NOT NULL CHECK (min_count BETWEEN 2 AND 100),

  -- A floor under the denominator. Three corrections out of five entries is a new operator on
  -- their first morning, and flagging them for retraining on their first morning is how a clinic
  -- teaches its staff to stop asking for help.
  min_entries integer NOT NULL DEFAULT 20 CHECK (min_entries >= 0),

  -- For END_OF_SHIFT: the hour, clinic-local, after which a correction counts towards the
  -- cluster. Null for the other patterns.
  after_hour integer CHECK (after_hour IS NULL OR after_hour BETWEEN 0 AND 23),

  -- What a supervisor reads, and what the operator reads about themselves. Both languages,
  -- because the operator is more likely to read the Bangla and they are the one it is about.
  display_en text NOT NULL,
  display_bn text NOT NULL,
  -- What to do about it. A flag with no suggested action is a complaint.
  action_en text NOT NULL,
  action_bn text NOT NULL,

  -- Null until a clinician says these numbers are right. The API reports it, the same way the
  -- critical-value table does, so nobody mistakes a proposal for a policy.
  approved_at timestamptz,
  approved_by uuid REFERENCES core.app_user(id),

  active     boolean NOT NULL DEFAULT true,
  ordering   integer NOT NULL DEFAULT 100,

  CONSTRAINT quality_threshold_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,39}$'),
  CONSTRAINT quality_threshold_shift_hour  CHECK ((pattern = 'END_OF_SHIFT') = (after_hour IS NOT NULL)),
  CONSTRAINT quality_threshold_approval    CHECK ((approved_at IS NULL) = (approved_by IS NULL))
);

GRANT SELECT ON core.quality_threshold TO dthcms_app;

COMMENT ON TABLE core.quality_threshold IS
  'When a pattern of corrections is worth somebody looking at (CP63). Proposals until approved.';

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'quality_threshold', 'What counts as a pattern is a policy, not a clinic''s data.')
ON CONFLICT DO NOTHING;

-- The three the plan names. Every number here is a proposal.
INSERT INTO core.quality_threshold
  (code, pattern, window_days, min_count, min_entries, after_hour,
   display_en, display_bn, action_en, action_bn, ordering) VALUES

  ('TRANSCRIPTION_3_IN_30', 'TRANSCRIPTION', 30, 3, 20, NULL,
   'Three or more transcription corrections in thirty days',
   'ত্রিশ দিনে তিন বা তার বেশি লেখার ভুল সংশোধন',
   'Sit with them at the station for one session and watch how the reading is transferred to the screen.',
   'একটি সেশনে তাঁর পাশে বসে দেখুন যন্ত্রের পাঠ কীভাবে স্ক্রিনে তোলা হচ্ছে।', 10),

  ('SAME_CODE_3_IN_30', 'SAME_CODE', 30, 3, 20, NULL,
   'The same measurement corrected three or more times in thirty days',
   'ত্রিশ দিনে একই মাপ তিন বা তার বেশি বার সংশোধিত',
   'Check the instrument and the unit before concluding anything about the operator.',
   'অপারেটর সম্পর্কে কিছু ভাবার আগে যন্ত্র ও একক পরীক্ষা করুন।', 20),

  ('END_OF_SHIFT_3_IN_14', 'END_OF_SHIFT', 14, 3, 20, 16,
   'Three or more corrections on values recorded late in the day, within a fortnight',
   'চৌদ্দ দিনে দিনের শেষভাগে নেওয়া তিন বা তার বেশি মান সংশোধিত',
   'This is usually a rota problem rather than a person problem. Look at the shift before the operator.',
   'এটি সাধারণত ব্যক্তির নয়, রোস্টারের সমস্যা। অপারেটরের আগে শিফটটি দেখুন।', 30)
ON CONFLICT (code) DO UPDATE SET
  pattern = EXCLUDED.pattern, window_days = EXCLUDED.window_days,
  min_count = EXCLUDED.min_count, min_entries = EXCLUDED.min_entries,
  after_hour = EXCLUDED.after_hour,
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  action_en = EXCLUDED.action_en, action_bn = EXCLUDED.action_bn,
  ordering = EXCLUDED.ordering;

-- ---------------------------------------------------------------------------
-- The flag
-- ---------------------------------------------------------------------------

-- One pattern, noticed once, about one person. Never deleted — a flag that could be removed is
-- a flag a supervisor can be persuaded to remove.
CREATE TABLE core.quality_flag (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  -- Whose record this is on. `app_user` rather than a copied name: a person who changes their
  -- name must still read correctly on a flag raised last year (CP61's reasoning).
  operator_id uuid NOT NULL REFERENCES core.app_user(id),

  threshold_code text NOT NULL REFERENCES core.quality_threshold(code),

  raised_at   timestamptz NOT NULL DEFAULT now(),
  -- The window the count was taken over, frozen. The window slides; this row must not.
  window_from timestamptz NOT NULL,
  window_to   timestamptz NOT NULL,

  -- What was counted, and out of how many. The denominator is on the row because a rate without
  -- one is the number that makes this feature feel punitive.
  observed_count integer NOT NULL CHECK (observed_count > 0),
  entries_count  integer NOT NULL CHECK (entries_count >= 0),

  -- The supporting detail a supervisor reads before saying anything to anybody: the correction
  -- request ids, the observation codes, the reason codes, the hours. **No patient, ever** — a
  -- quality record is about the operator, and a supervisor reading a patient's values through
  -- their staff's error history is reading clinical data through a side door. An invariant
  -- below enforces it rather than trusting the writer.
  evidence jsonb NOT NULL DEFAULT '[]'::jsonb,

  status text NOT NULL DEFAULT 'OPEN'
         CHECK (status IN ('OPEN', 'ACKNOWLEDGED', 'DISMISSED')),

  -- Who looked at it, when, and what they decided to do. A dismissal must say why, for the same
  -- reason a rejected correction must (CP62): "no" with no reason teaches nobody anything.
  resolved_at   timestamptz,
  resolved_by   uuid REFERENCES core.app_user(id),
  resolution    text NOT NULL DEFAULT '',

  -- The security-audit row this raised, so the two read together.
  audit_seq bigint,

  CONSTRAINT quality_flag_window_ordered  CHECK (window_to > window_from),
  CONSTRAINT quality_flag_resolution_complete
    CHECK ((status = 'OPEN') = (resolved_at IS NULL)),
  CONSTRAINT quality_flag_resolution_attributed
    CHECK (resolved_at IS NULL OR resolved_by IS NOT NULL),
  CONSTRAINT quality_flag_dismissal_says_why
    CHECK (status <> 'DISMISSED' OR btrim(resolution) <> ''),
  CONSTRAINT quality_flag_evidence_is_a_list CHECK (jsonb_typeof(evidence) = 'array')
);

-- One open flag per operator per threshold. A pattern noticed twice while nobody has acted on it
-- the first time is the same pattern, and two rows would mean a supervisor acknowledging the
-- same conversation twice.
CREATE UNIQUE INDEX quality_flag_one_open_per_pattern
  ON core.quality_flag (operator_id, threshold_code) WHERE status = 'OPEN';

CREATE INDEX quality_flag_open ON core.quality_flag (facility_id, raised_at DESC)
  WHERE status = 'OPEN';
CREATE INDEX quality_flag_by_operator ON core.quality_flag (operator_id, raised_at DESC);

GRANT SELECT, INSERT, UPDATE ON core.quality_flag TO dthcms_app;

COMMENT ON TABLE core.quality_flag IS
  'A pattern of corrections worth a conversation (CP63). Acknowledged, never deleted, and never naming a patient.';

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- Criterion 2's other half. A flag that cannot show its working is a flag a supervisor cannot
-- take to the person it is about.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_quality_flag_shows_its_working() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.quality_flag f
    JOIN core.quality_threshold t ON t.code = f.threshold_code
   WHERE f.observed_count < t.min_count
      OR jsonb_array_length(f.evidence) = 0
      OR jsonb_array_length(f.evidence) > f.observed_count;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% quality flags cannot show what they were raised on', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- The one that matters for anybody's privacy. A supervisor reads a staff record; they must not
-- be reading a patient's values through it, and "we would never put a patient id in there" is
-- not a mechanism. Any key that looks like a patient reference, anywhere in the evidence, is a
-- failure — including one a future writer adds without reading this comment.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_quality_record_names_a_patient() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.quality_flag f
   WHERE EXISTS (
           SELECT 1
             FROM jsonb_array_elements(f.evidence) AS item,
                  jsonb_object_keys(item) AS key
            WHERE key ILIKE '%patient%'
               OR key ILIKE '%value%'
               OR key ILIKE '%nid%'
         );
  IF offenders > 0 THEN
    RAISE EXCEPTION '% quality flags name a patient or carry a clinical value', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_quality_flag_shows_its_working',
   'every retraining flag can show what it was raised on', 78),
  ('assert_no_quality_record_names_a_patient',
   'a staff quality record names no patient and carries no clinical value', 79)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- ---------------------------------------------------------------------------
-- Who may read whose record
-- ---------------------------------------------------------------------------

-- Criterion 4. `hr.performance.read` exists and HR holds it; it stays what it is. This is the
-- clinical supervisor's permission, and the separation is the point — see the note at the top.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('quality.read.team', 'quality', 'read', 'team',
   'Read the correction record of the operators one supervises', true),
  ('quality.flag.resolve', 'quality', 'flag', 'resolve',
   'Acknowledge or dismiss a retraining flag', false)
ON CONFLICT (code) DO UPDATE SET description = EXCLUDED.description;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code
  FROM core.role r
  CROSS JOIN (VALUES ('quality.read.team'), ('quality.flag.resolve')) AS p(code)
 WHERE r.code IN ('PHYSICIAN', 'QA', 'ADMIN')
ON CONFLICT DO NOTHING;

-- +goose Down

DELETE FROM core.role_permission WHERE permission_code IN ('quality.read.team', 'quality.flag.resolve');
DELETE FROM core.permission WHERE code IN ('quality.read.team', 'quality.flag.resolve');
DELETE FROM ops.invariant WHERE function_name IN (
  'assert_every_quality_flag_shows_its_working',
  'assert_no_quality_record_names_a_patient');
DROP FUNCTION IF EXISTS core.assert_no_quality_record_names_a_patient();
DROP FUNCTION IF EXISTS core.assert_every_quality_flag_shows_its_working();
DROP TABLE IF EXISTS core.quality_flag;
DELETE FROM core.facility_scope_exemption WHERE schema_name = 'core' AND table_name = 'quality_threshold';
DROP TABLE IF EXISTS core.quality_threshold;
