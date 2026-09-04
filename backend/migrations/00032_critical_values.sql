-- Values that mean somebody has to act now, and what happens if nobody does (CP50, §3 step 5,
-- §4.4).
--
-- # The third band, and why it is a third table
--
-- CP42 gave every code a registry band: outside it the number cannot be stored at all.
-- CP46 added plausibility: a height of 205 cm is possible and wants confirming.
-- CP49 added the reference range: what is ordinary for this patient.
--
-- None of those is this. A systolic of 210 is storable, plausible, outside the normal range —
-- and none of that makes a phone ring. A **critical value** is the one where the clinical
-- answer is not "flag it on the screen" but "find the consultant now", and conflating it with
-- the amber flag is precisely how a clinic learns to ignore both.
--
-- So: registry → storable at all. Plausibility → probably not a typing error. Reference range
-- → ordinary. Critical → act now. Four bands, four meanings, four tables, and none of them
-- pretending to be another.
--
-- # Every number here is a proposal
--
-- D-27 is open and blocking: the critical-value table and the escalation chain are Dr.
-- Nahid's to author. `approved_at` is null on every row below and the API says so, because a
-- screen that showed these as settled would overstate what anybody has agreed to.
--
-- Two of them are not proposals. SpO2 below 92% and a blood pressure above 180/110 are named
-- in the blueprint itself (§3 step 5), and they are seeded here as written.
--
-- What is *not* a proposal is the mechanism: raising, delivery, acknowledgement, escalation
-- and the audit trail are built and tested. When D-27 lands it changes rows, not code — which
-- is the whole reason the thresholds are rows.
--
-- # Raised inside the transaction that stored the value
--
-- The alert event is appended in the same transaction as OBSERVATION_RECORDED. Not after it,
-- and not from a job that scans for them: a design where the value commits and the alert is
-- raised afterwards has a window in which a dangerous number is in the record and nothing is
-- coming. There is no such window here. Either both facts exist or neither does.
--
-- **Delivery is a separate question from raising**, and the difference is criterion 4. An
-- alert is raised whatever the state of the network; whether it reached a live screen is
-- recorded on the row, and when it did not, the operator who typed the value is told to walk.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The permissions
-- ---------------------------------------------------------------------------

INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('alert.read', 'alert', 'read', '',
   'Read the clinic''s open critical-value alerts', true),
  ('alert.acknowledge', 'alert', 'acknowledge', '',
   'Acknowledge a critical-value alert, stopping its escalation', true)
ON CONFLICT (code) DO UPDATE SET
  resource = EXCLUDED.resource, action = EXCLUDED.action, scope = EXCLUDED.scope,
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

-- Who receives an alert, and who may stop it escalating.
--
-- Both permissions go to the two roles who can act on the finding at the moment it is made.
-- Deliberately not to the officer who typed the value: they already know — the alert sounded
-- in their hand — and an acknowledgement from the person who entered the number would let a
-- clinic close its own alerts without a clinician ever seeing one.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code
  FROM core.role r
 CROSS JOIN (VALUES ('alert.read'), ('alert.acknowledge')) AS p(code)
 WHERE r.code IN ('PHYSICIAN', 'JUNIOR_DOCTOR')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The thresholds
-- ---------------------------------------------------------------------------

CREATE TABLE core.critical_value_rule (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  code text NOT NULL REFERENCES core.observation_code(code),

  -- Who it applies to. NULL means anyone; the most specific match wins, resolved by the
  -- database exactly as CP46's plausibility and CP49's ranges are, so a client that held a
  -- cached copy and a server that read the table cannot disagree about which rule fired.
  sex           text CHECK (sex IN ('male', 'female')),
  min_age_years numeric(5,2),
  max_age_years numeric(5,2),

  -- Canonical units. Strictly outside: value < low, or value > high.
  --
  -- Either edge may be absent, and usually one is. Oxygen saturation has a floor and no
  -- ceiling; a rule with an invented upper bound is a rule that fires on healthy patients
  -- until somebody turns the sound off.
  low  numeric,
  high numeric,

  -- What the operator and the consultant are told, in the language they are reading. Not a
  -- template assembled in code: the sentence for a hypoglycaemia is not the sentence for a
  -- hypertensive emergency, and the useful half of an alert is what to do next.
  action_en text NOT NULL DEFAULT '',
  action_bn text NOT NULL DEFAULT '',

  approved_by uuid REFERENCES core.app_user(id),
  approved_at timestamptz,
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT critical_rule_age_band_is_ordered
    CHECK (min_age_years IS NULL OR max_age_years IS NULL OR min_age_years < max_age_years),
  CONSTRAINT critical_rule_is_ordered
    CHECK (low IS NULL OR high IS NULL OR low < high),
  CONSTRAINT critical_rule_says_something
    CHECK (num_nonnulls(low, high) > 0)
);

CREATE INDEX critical_value_rule_by_code ON core.critical_value_rule (code);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'critical_value_rule',
   'A saturation of 88% is an emergency in every clinic. Critical values are clinical reference data, not a facility setting (CP50).')
ON CONFLICT (schema_name, table_name) DO NOTHING;

GRANT SELECT ON core.critical_value_rule TO dthcms_app;
GRANT SELECT ON core.critical_value_rule TO dthcms_projector;

COMMENT ON TABLE core.critical_value_rule IS
  'Values that require immediate action (CP50). Not plausibility (CP46), not a normal range (CP49). Every row is proposed until D-27 approves it.';

-- ---------------------------------------------------------------------------
-- The escalation chain
-- ---------------------------------------------------------------------------
--
-- Who is told, and after how long, when nobody has acknowledged. Rows rather than code for
-- the same reason as the thresholds: the chain in a twelve-station clinic where everybody is
-- within thirty metres is not the chain in a hospital, and the first month of real use will
-- move these numbers.
--
-- The last step names no role on purpose. When the consultant and the junior doctor have both
-- had five minutes and nothing has come back, the remaining escalation is a person walking to
-- another person — so the step tells the operator who typed the value to go and find someone.
-- A chain whose last link is another notification is a chain with no end.
CREATE TABLE core.escalation_step (
  step_number int PRIMARY KEY CHECK (step_number > 0),

  -- Seconds after the alert was raised. Step 1 is 0: the consultant is told immediately.
  after_seconds int NOT NULL CHECK (after_seconds >= 0),

  -- The role notified at this step. NULL means "tell the operator to escalate in person",
  -- which is the end of the chain.
  notify_role text REFERENCES core.role(code),

  note_en text NOT NULL DEFAULT '',
  note_bn text NOT NULL DEFAULT '',

  approved_by uuid REFERENCES core.app_user(id),
  approved_at timestamptz,
  updated_at  timestamptz NOT NULL DEFAULT now()
);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'escalation_step',
   'The escalation chain is named by role, and the roles are the same in every facility (CP50). Per-facility chains arrive with the second clinic, not before.')
ON CONFLICT (schema_name, table_name) DO NOTHING;

GRANT SELECT ON core.escalation_step TO dthcms_app;
GRANT SELECT ON core.escalation_step TO dthcms_projector;

COMMENT ON TABLE core.escalation_step IS
  'Acknowledge-or-escalate: who is told after how long (CP50). Proposed until D-27 approves it.';

INSERT INTO core.escalation_step (step_number, after_seconds, notify_role, note_en, note_bn) VALUES
  (1, 0, 'PHYSICIAN',
   'The consultant, immediately. §4.4: a critical finding bypasses the queue.',
   'প্রধান পরামর্শকের কাছে সঙ্গে সঙ্গে। §৪.৪: জরুরি ফল সারি অতিক্রম করে।'),
  (2, 120, 'JUNIOR_DOCTOR',
   'After two minutes with no acknowledgement, the junior doctor as well — not instead.',
   'দুই মিনিটে কেউ স্বীকার না করলে সহকারী চিকিৎসককেও — বদলে নয়, সঙ্গে।'),
  (3, 300, NULL,
   'After five minutes, the operator who entered the value is told to go and find somebody. A chain whose last link is another notification has no end.',
   'পাঁচ মিনিট পরেও সাড়া না মিললে যিনি মানটি লিখেছেন তাঁকে সরাসরি কাউকে খুঁজে আনতে বলা হয়। শেষ ধাপে আরেকটি বার্তা পাঠানো মানে শৃঙ্খলের কোনো শেষ নেই।');

-- ---------------------------------------------------------------------------
-- The proposed thresholds (D-27)
-- ---------------------------------------------------------------------------
--
-- Paediatric bands throughout, because a pulse of 150 is ordinary in a two-year-old and an
-- emergency in an adult, and one band wide enough for both is a band that never fires for a
-- child. The blueprint asks for the bands by name.
INSERT INTO core.critical_value_rule (code, sex, min_age_years, max_age_years, low, high, action_en, action_bn) VALUES

  -- Named in the blueprint (§3 step 5). Not a proposal.
  ('SPO2', NULL, NULL, NULL, 92, NULL,
   'Check the probe and repeat on room air. If it is real, oxygen and the consultant now.',
   'প্রোব দেখে ঘরের বাতাসে আবার মাপুন। সত্যি হলে এখনই অক্সিজেন ও পরামর্শক ডাকুন।'),
  ('BP_SYSTOLIC', NULL, 18, NULL, 80, 180,
   'Repeat on the other arm with the patient seated and rested. Above 180 systolic the consultant sees the patient before they leave the station.',
   'রোগীকে বসিয়ে বিশ্রাম দিয়ে অন্য বাহুতে আবার মাপুন। সিস্টোলিক ১৮০-এর উপরে হলে স্টেশন ছাড়ার আগেই পরামর্শক দেখবেন।'),
  ('BP_DIASTOLIC', NULL, 18, NULL, 50, 110,
   'Repeat on the other arm with the patient seated and rested. Above 110 diastolic the consultant sees the patient before they leave the station.',
   'রোগীকে বসিয়ে বিশ্রাম দিয়ে অন্য বাহুতে আবার মাপুন। ডায়াস্টোলিক ১১০-এর উপরে হলে স্টেশন ছাড়ার আগেই পরামর্শক দেখবেন।'),

  -- Paediatric blood pressure is properly read off a height- and age-indexed table. These
  -- bands are a coarse screen until that table is authored, and they say so — an unflagged
  -- child is worse than a roughly-flagged one.
  ('BP_SYSTOLIC',  NULL, 12, 18, 80, 160,
   'A coarse band. Paediatric blood pressure is read off a height- and age-indexed table, which D-27 will supply.',
   'একটি মোটামুটি সীমা। শিশুর রক্তচাপ উচ্চতা ও বয়সভিত্তিক টেবিল থেকে পড়তে হয়; D-27 তা দেবে।'),
  ('BP_SYSTOLIC',  NULL, 1, 12, 70, 140,
   'A coarse band. Paediatric blood pressure is read off a height- and age-indexed table, which D-27 will supply.',
   'একটি মোটামুটি সীমা। শিশুর রক্তচাপ উচ্চতা ও বয়সভিত্তিক টেবিল থেকে পড়তে হয়; D-27 তা দেবে।'),
  ('BP_DIASTOLIC', NULL, 12, 18, 45, 100,
   'A coarse band, as above.', 'উপরের মতোই একটি মোটামুটি সীমা।'),
  ('BP_DIASTOLIC', NULL, 1, 12, 40, 95,
   'A coarse band, as above.', 'উপরের মতোই একটি মোটামুটি সীমা।'),

  ('HEART_RATE', NULL, 18, NULL, 40, 130, '', ''),
  ('HEART_RATE', NULL, 12, 18,   45, 140, '', ''),
  ('HEART_RATE', NULL, 6,  12,   50, 150, '', ''),
  ('HEART_RATE', NULL, 1,  6,    60, 180, '', ''),
  ('HEART_RATE', NULL, NULL, 1,  80, 200, '', ''),

  ('RESP_RATE', NULL, 18, NULL,  8, 30, '', ''),
  ('RESP_RATE', NULL, 12, 18,   10, 32, '', ''),
  ('RESP_RATE', NULL, 6,  12,   12, 36, '', ''),
  ('RESP_RATE', NULL, 1,  6,    15, 45, '', ''),
  ('RESP_RATE', NULL, NULL, 1,  20, 65, '', ''),

  ('BODY_TEMP', NULL, NULL, NULL, 35.0, 39.5,
   'Recheck the site before acting. Hypothermia below 35 °C is as urgent as the fever above.',
   'কাজ করার আগে মাপার জায়গা যাচাই করুন। ৩৫ °সে-এর নিচে শরীর ঠান্ডা হয়ে যাওয়াও জ্বরের মতোই জরুরি।'),
  ('BODY_TEMP', NULL, NULL, 1, 36.0, 38.0,
   'An infant. A fever in the first months of life is an emergency in a way it is not at any other age.',
   'শিশু। জীবনের প্রথম কয়েক মাসে জ্বর অন্য যেকোনো বয়সের চেয়ে অনেক বেশি জরুরি।'),

  -- A diabetes clinic. These two are the reason the alert exists at all: a hypoglycaemia in
  -- the waiting area is the emergency this system is most likely to meet.
  ('GLUCOSE_RANDOM', NULL, 18, NULL, 3.0, 25.0,
   'Below 3.0 mmol/L: oral glucose now, do not wait for the consultation. Above 25: check ketones and call the consultant.',
   '৩.০ মিলিমোল/লি-এর নিচে: এখনই মুখে গ্লুকোজ দিন, পরামর্শের অপেক্ষা করবেন না। ২৫-এর উপরে: কিটোন দেখে পরামর্শক ডাকুন।'),
  ('GLUCOSE_RANDOM', NULL, NULL, 18, 3.3, 20.0,
   'A child. Treat a low reading immediately and recheck in fifteen minutes.',
   'শিশু। কম মান পেলে সঙ্গে সঙ্গে চিকিৎসা দিন এবং পনেরো মিনিট পরে আবার মাপুন।'),
  ('GLUCOSE_FASTING', NULL, 18, NULL, 3.0, 25.0,
   'Below 3.0 mmol/L: oral glucose now, do not wait for the consultation. Above 25: check ketones and call the consultant.',
   '৩.০ মিলিমোল/লি-এর নিচে: এখনই মুখে গ্লুকোজ দিন, পরামর্শের অপেক্ষা করবেন না। ২৫-এর উপরে: কিটোন দেখে পরামর্শক ডাকুন।'),
  ('GLUCOSE_FASTING', NULL, NULL, 18, 3.3, 20.0,
   'A child. Treat a low reading immediately and recheck in fifteen minutes.',
   'শিশু। কম মান পেলে সঙ্গে সঙ্গে চিকিৎসা দিন এবং পনেরো মিনিট পরে আবার মাপুন।');

-- The rule that applies to a patient and a code. Same resolution as CP46 and CP49, and the
-- same reason: a client that ranked them itself would one day sound an alarm the server did
-- not raise, or stay silent when the server did.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.critical_value_for(
  p_code text, p_sex text, p_age_years numeric
) RETURNS SETOF core.critical_value_rule
LANGUAGE sql STABLE AS $$
  SELECT r.*
    FROM core.critical_value_rule r
   WHERE r.code = p_code
     AND (r.sex IS NULL OR r.sex = p_sex)
     AND (r.min_age_years IS NULL OR p_age_years IS NULL OR p_age_years >= r.min_age_years)
     AND (r.max_age_years IS NULL OR p_age_years IS NULL OR p_age_years <  r.max_age_years)
   ORDER BY num_nonnulls(r.sex, r.min_age_years, r.max_age_years) DESC,
            coalesce(r.max_age_years, 200) - coalesce(r.min_age_years, 0),
            r.updated_at
   LIMIT 1;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION core.critical_value_for(text, text, numeric) TO dthcms_app;

-- ---------------------------------------------------------------------------
-- The alerts
-- ---------------------------------------------------------------------------

CREATE TABLE read.critical_alert (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL,
  patient_id  uuid NOT NULL,
  visit_id    uuid,

  -- What set it off. The observation id rather than a copy of the value would be tidier and
  -- wrong: an alert is a statement about a number at a moment, and the observation can be
  -- corrected afterwards. The alert must still read as what the consultant was told.
  observation_id uuid NOT NULL,
  code           text NOT NULL REFERENCES core.observation_code(code),
  value_num      numeric NOT NULL,
  unit           text REFERENCES core.unit(code),

  rule_id   uuid REFERENCES core.critical_value_rule(id),
  breached  text NOT NULL CHECK (breached IN ('low', 'high')),
  threshold numeric NOT NULL,
  action_en text NOT NULL DEFAULT '',
  action_bn text NOT NULL DEFAULT '',

  raised_at    timestamptz NOT NULL,
  raised_by    uuid NOT NULL,
  raised_role  text NOT NULL DEFAULT '',
  station_code text NOT NULL DEFAULT '',

  -- OPEN         → nobody has acknowledged it
  -- ACKNOWLEDGED → a clinician has, and said what they are doing
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'ACKNOWLEDGED')),

  acknowledged_by   uuid,
  acknowledged_at   timestamptz,
  acknowledgement   text NOT NULL DEFAULT '',

  -- How far down the chain this alert has travelled. 1 the moment it is raised, because step
  -- 1 is the consultant and it happens immediately.
  escalation_step int NOT NULL DEFAULT 1,
  escalated_at    timestamptz,

  -- Whether the alert reached a live screen, and how many (criterion 4).
  --
  -- Written by a second event rather than by the first, because delivery is not knowable
  -- when the first is written: the alert is appended inside the transaction that stores the
  -- value, and nothing may be published until that transaction has committed. So the alert
  -- is raised, the transaction commits, delivery is attempted, and what happened is appended
  -- as its own fact.
  --
  -- The default is therefore "nobody was told", and that is the right default: an API that
  -- died between the commit and the publish leaves an alert that reads as undelivered, which
  -- is what it is.
  notified_at    timestamptz,
  recipients     int NOT NULL DEFAULT 0 CHECK (recipients >= 0),
  delivery_error text NOT NULL DEFAULT '',

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL,

  CONSTRAINT critical_alert_acknowledgement_is_complete
    CHECK ((status = 'ACKNOWLEDGED') = (acknowledged_by IS NOT NULL))
);

CREATE INDEX critical_alert_open_by_facility
  ON read.critical_alert (facility_id, raised_at DESC) WHERE status = 'OPEN';
CREATE INDEX critical_alert_by_patient
  ON read.critical_alert (patient_id, raised_at DESC);
CREATE INDEX critical_alert_by_observation
  ON read.critical_alert (observation_id);

GRANT SELECT ON read.critical_alert TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON read.critical_alert TO dthcms_projector;

COMMENT ON TABLE read.critical_alert IS
  'One row per critical value raised (CP50). Written only by the projector; the ledger is the record.';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_critical_value_alerted(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.critical_alert (
    id, facility_id, patient_id, visit_id,
    observation_id, code, value_num, unit,
    rule_id, breached, threshold, action_en, action_bn,
    raised_at, raised_by, raised_role, station_code,
    event_id, global_seq)
  VALUES (
    (p->>'alert_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    nullif(p->>'visit_id', '')::uuid,
    (p->>'observation_id')::uuid,
    p->>'code',
    (p->>'value_num')::numeric,
    nullif(p->>'unit', ''),
    nullif(p->>'rule_id', '')::uuid,
    p->>'breached',
    (p->>'threshold')::numeric,
    coalesce(p->>'action_en', ''),
    coalesce(p->>'action_bn', ''),
    (p->>'raised_at')::timestamptz,
    (p->>'raised_by')::uuid,
    coalesce(p->>'raised_role', ''),
    coalesce(p->>'station_code', ''),
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (event_id) DO NOTHING;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_critical_value_delivery_attempted(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  -- The first attempt is the one recorded. A retry that succeeded after a failure is a
  -- different fact and belongs in the ledger, which has it; what this column answers is
  -- "was the clinic told when it mattered", and that is decided in the first few seconds.
  UPDATE read.critical_alert
     SET notified_at    = (p->>'attempted_at')::timestamptz,
         recipients     = coalesce((p->>'recipients')::int, 0),
         delivery_error = coalesce(p->>'error', '')
   WHERE id = (p->>'alert_id')::uuid
     AND notified_at IS NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_critical_value_acknowledged(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  -- Only an open alert. A second acknowledgement is not an error worth failing a projection
  -- over — two clinicians can both reach for it — but the record keeps the first, because the
  -- first is the one that stopped the escalation.
  UPDATE read.critical_alert
     SET status          = 'ACKNOWLEDGED',
         acknowledged_by = (p->>'acknowledged_by')::uuid,
         acknowledged_at = (p->>'acknowledged_at')::timestamptz,
         acknowledgement = coalesce(p->>'note', '')
   WHERE id = (p->>'alert_id')::uuid
     AND status = 'OPEN';
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_critical_value_escalated(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  -- Never backwards. A replay that arrived out of order must not un-escalate an alert.
  UPDATE read.critical_alert
     SET escalation_step = (p->>'step')::int,
         escalated_at    = (p->>'escalated_at')::timestamptz
   WHERE id = (p->>'alert_id')::uuid
     AND escalation_step < (p->>'step')::int;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_critical_value_alerted(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_critical_value_delivery_attempted(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_critical_value_acknowledged(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_critical_value_escalated(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- A critical threshold the write path would refuse is a threshold that can never fire.
--
-- If a rule said "critical above 350" for a systolic whose absolute plausibility band stops
-- at 290, the value is refused before the alert is ever evaluated. The rule would sit in the
-- table looking like a safety net with nothing under it, which is worse than no rule at all —
-- somebody would tick it off a checklist.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_critical_thresholds_can_fire() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender record;
BEGIN
  SELECT r.code, r.low, r.high, c.min_canonical, c.max_canonical INTO offender
    FROM core.critical_value_rule r
    JOIN core.observation_code c ON c.code = r.code
   WHERE (r.low  IS NOT NULL AND c.min_canonical IS NOT NULL AND r.low  <= c.min_canonical)
      OR (r.high IS NOT NULL AND c.max_canonical IS NOT NULL AND r.high >= c.max_canonical)
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'the critical thresholds for % (% .. %) cannot fire: the code itself only holds % .. %',
      offender.code, offender.low, offender.high, offender.min_canonical, offender.max_canonical
      USING HINT = 'A threshold outside the storable band is a safety net with nothing under it.';
  END IF;
END;
$$;
-- +goose StatementEnd

-- A value cannot be normal and critical at once.
--
-- If a critical low sat above the reference range's low, there would be values the screen
-- called ordinary and the alarm called an emergency. Whichever a clinician believed, they
-- would learn to disbelieve the other.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_critical_sits_outside_normal() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender record;
BEGIN
  SELECT c.code, c.low AS critical_low, c.high AS critical_high,
         n.low AS normal_low, n.high AS normal_high
    INTO offender
    FROM core.critical_value_rule c
    JOIN core.reference_range n ON n.code = c.code
                              AND (c.sex IS NOT DISTINCT FROM n.sex)
                              AND (c.min_age_years IS NOT DISTINCT FROM n.min_age_years)
                              AND (c.max_age_years IS NOT DISTINCT FROM n.max_age_years)
   WHERE (c.low  IS NOT NULL AND n.low  IS NOT NULL AND c.low  > n.low)
      OR (c.high IS NOT NULL AND n.high IS NOT NULL AND c.high < n.high)
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'for % the critical band (% .. %) is inside the normal band (% .. %)',
      offender.code, offender.critical_low, offender.critical_high,
      offender.normal_low, offender.normal_high
      USING HINT = 'A value the screen calls ordinary and the alarm calls an emergency '
                   'teaches a clinician to disbelieve one of them.';
  END IF;
END;
$$;
-- +goose StatementEnd

-- The chain has a beginning and an end.
--
-- A chain whose first step is not immediate delays every alert. A chain whose last step names
-- a role is a chain that loops forever if that role is not on the floor. Both are the kind of
-- mistake that looks fine in a table and fails at four in the afternoon.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_the_escalation_chain_is_walkable() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  first_step record;
  last_step  record;
  gap        record;
BEGIN
  SELECT * INTO first_step FROM core.escalation_step ORDER BY step_number LIMIT 1;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'there is no escalation chain'
      USING HINT = 'An alert with nowhere to escalate is an alert that waits forever.';
  END IF;
  IF first_step.after_seconds <> 0 OR first_step.notify_role IS NULL THEN
    RAISE EXCEPTION 'the first escalation step must notify a role immediately (step % waits %s for %)',
      first_step.step_number, first_step.after_seconds, coalesce(first_step.notify_role, 'nobody');
  END IF;

  SELECT * INTO last_step FROM core.escalation_step ORDER BY step_number DESC LIMIT 1;
  IF last_step.notify_role IS NOT NULL THEN
    RAISE EXCEPTION 'the escalation chain ends by notifying %, which is not an end',
      last_step.notify_role
      USING HINT = 'The last step must name no role: it tells the operator to find somebody in person.';
  END IF;

  SELECT a.step_number INTO gap
    FROM core.escalation_step a
    JOIN core.escalation_step b ON b.step_number = a.step_number + 1
   WHERE b.after_seconds <= a.after_seconds
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'escalation step % does not wait longer than the one before it', gap.step_number;
  END IF;
END;
$$;
-- +goose StatementEnd

-- Every named critical code is one an operator can actually enter.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_critical_rules_name_live_codes() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender text;
BEGIN
  SELECT r.code INTO offender
    FROM core.critical_value_rule r
    JOIN core.observation_code c ON c.code = r.code
   WHERE c.retired_at IS NOT NULL OR c.value_type <> 'numeric'
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION '% has a critical-value rule but is retired or is not a number', offender;
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_critical_thresholds_can_fire() IS
  'Raises if a critical threshold sits outside the band the code can hold (CP50).';
COMMENT ON FUNCTION core.assert_critical_sits_outside_normal() IS
  'Raises if a critical band falls inside the matching normal range (CP50).';
COMMENT ON FUNCTION core.assert_the_escalation_chain_is_walkable() IS
  'Raises if the escalation chain has no immediate first step, no terminal last step, or does not advance (CP50).';
COMMENT ON FUNCTION core.assert_critical_rules_name_live_codes() IS
  'Raises if a critical-value rule names a retired or non-numeric code (CP50).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_critical_thresholds_can_fire',
   'every critical threshold is inside the band its code can hold', 53),
  ('assert_critical_sits_outside_normal',
   'no critical band falls inside the normal range for the same patients', 54),
  ('assert_the_escalation_chain_is_walkable',
   'the escalation chain starts immediately, advances, and ends with a person', 55),
  ('assert_critical_rules_name_live_codes',
   'every critical-value rule names a live numeric code', 56)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_critical_thresholds_can_fire', 'assert_critical_sits_outside_normal',
  'assert_the_escalation_chain_is_walkable', 'assert_critical_rules_name_live_codes');
DROP FUNCTION IF EXISTS core.assert_critical_rules_name_live_codes();
DROP FUNCTION IF EXISTS core.assert_the_escalation_chain_is_walkable();
DROP FUNCTION IF EXISTS core.assert_critical_sits_outside_normal();
DROP FUNCTION IF EXISTS core.assert_critical_thresholds_can_fire();
DROP FUNCTION IF EXISTS read.apply_critical_value_escalated(jsonb);
DROP FUNCTION IF EXISTS read.apply_critical_value_delivery_attempted(jsonb);
DROP FUNCTION IF EXISTS read.apply_critical_value_acknowledged(jsonb);
DROP FUNCTION IF EXISTS read.apply_critical_value_alerted(jsonb);
DROP TABLE IF EXISTS read.critical_alert;
DROP FUNCTION IF EXISTS core.critical_value_for(text, text, numeric);
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name IN ('critical_value_rule', 'escalation_step');
DROP TABLE IF EXISTS core.escalation_step;
DROP TABLE IF EXISTS core.critical_value_rule;
DELETE FROM core.role_permission WHERE permission_code IN ('alert.read', 'alert.acknowledge');
DELETE FROM core.permission WHERE code IN ('alert.read', 'alert.acknowledge');
