-- Impossible inputs, refused while the patient is still in the room (CP46, §3 step 2).
--
-- The cheapest defence against a data-quality failure there is. A height of 15 cm typed for
-- an adult is caught in the second it is typed, when the operator is holding the tape measure
-- and the patient is standing in front of them. The same value found in an audit six months
-- later cannot be fixed at all: nobody can go back and re-measure a person on a Tuesday
-- morning in September.
--
-- # Three kinds of wrong, and only one of them is a refusal
--
--   * **Impossible.** A height of 15 cm. No client can store it, ever.
--   * **Implausible but possible.** A height of 205 cm. Rare, real, and a typing error far
--     more often than not — so it is accepted *with an explicit confirmation*, and the
--     confirmation is recorded. A system that refused it would be a system that cannot record
--     the tallest patient in Faridpur.
--   * **A surprising change.** 12 cm of height gained since March, in an adult. The value is
--     ordinary; the *delta* is not.
--
-- Conflating the second with the first is the classic failure of validation in clinical
-- software: rules tuned to refuse typing errors end up refusing the patients who most need
-- recording. So the middle band exists, it warns, and it takes a confirmation — and because
-- every confirmation is an event, "which rules do staff override every day" is a question the
-- clinic can answer from its own data rather than from opinion.
--
-- # Why the rules are rows
--
-- Criterion 4: editable without a code release. A band that needs a deployment to change is a
-- band nobody changes, and the numbers below are **proposed values requiring Dr. Nahid's
-- approval** — the first week of real use will move several of them.

-- +goose Up

CREATE TABLE core.plausibility_rule (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  code text NOT NULL REFERENCES core.observation_code(code),

  -- Who the rule applies to. NULL means "anyone", and the most specific matching rule wins.
  -- An adult's plausible height band is not a two-year-old's, and one band wide enough for
  -- both is a band that catches nothing.
  sex           text CHECK (sex IN ('male', 'female')),
  min_age_years numeric(5,2),
  max_age_years numeric(5,2),

  -- Impossible. Nothing may store a value outside this, by any path.
  --
  -- Narrower than the registry's own min_canonical/max_canonical, never wider — an invariant
  -- checks it. Two places that could each widen the other is two places nobody can reason
  -- about.
  absolute_min numeric,
  absolute_max numeric,

  -- Implausible but possible. Outside this and inside the absolute band, a value is accepted
  -- only with an explicit confirmation, which is recorded.
  plausible_min numeric,
  plausible_max numeric,

  -- How far this value may move from the patient's own last recorded one, in canonical
  -- units. Direction matters: 3 kg gained in a week is a different clinical story from 3 kg
  -- lost, and an adult gaining 12 cm of height is not a story at all.
  max_increase numeric CHECK (max_increase IS NULL OR max_increase >= 0),
  max_decrease numeric CHECK (max_decrease IS NULL OR max_decrease >= 0),

  -- The same, per day, for values that legitimately move fast. A child grows; an adult does
  -- not. Applied when the two measurements are far enough apart for a rate to mean anything.
  max_increase_per_day numeric CHECK (max_increase_per_day IS NULL OR max_increase_per_day >= 0),
  max_decrease_per_day numeric CHECK (max_decrease_per_day IS NULL OR max_decrease_per_day >= 0),

  -- Why this rule exists, for whoever reads it in two years wondering whether to widen it.
  note_en text NOT NULL DEFAULT '',
  note_bn text NOT NULL DEFAULT '',

  -- Proposed values need approving; approved values need a name against them.
  approved_by uuid REFERENCES core.app_user(id),
  approved_at timestamptz,

  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT plausibility_age_band_is_ordered
    CHECK (min_age_years IS NULL OR max_age_years IS NULL OR min_age_years < max_age_years),
  CONSTRAINT plausibility_absolute_is_ordered
    CHECK (absolute_min IS NULL OR absolute_max IS NULL OR absolute_min < absolute_max),
  CONSTRAINT plausibility_plausible_is_ordered
    CHECK (plausible_min IS NULL OR plausible_max IS NULL OR plausible_min < plausible_max),
  -- The soft band sits inside the hard one. A soft limit outside the absolute limit is a
  -- warning that can never fire, which reads as a rule doing its job while doing nothing.
  CONSTRAINT plausibility_soft_within_hard
    CHECK ((plausible_min IS NULL OR absolute_min IS NULL OR plausible_min >= absolute_min)
       AND (plausible_max IS NULL OR absolute_max IS NULL OR plausible_max <= absolute_max)),
  CONSTRAINT plausibility_rule_says_something
    CHECK (num_nonnulls(absolute_min, absolute_max, plausible_min, plausible_max,
                        max_increase, max_decrease,
                        max_increase_per_day, max_decrease_per_day) > 0)
);

CREATE INDEX plausibility_rule_by_code ON core.plausibility_rule (code);

-- Reference data, like the registry: nobody's clinic, no facility column.
INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'plausibility_rule',
   'A height of 15 cm is impossible in every clinic. Plausibility is a property of human '
   'bodies, not of a facility.')
ON CONFLICT (schema_name, table_name) DO NOTHING;

GRANT SELECT ON core.plausibility_rule TO dthcms_app;
GRANT SELECT ON core.plausibility_rule TO dthcms_projector;

COMMENT ON TABLE core.plausibility_rule IS
  'Per-code plausibility bands and delta limits (CP46). Data, so a clinic can retune them.';

-- The effective rule for one patient and one code: the most specific match.
--
-- Specificity is counted rather than ranked, and the count is deliberate: a rule naming a sex
-- and an age band beats one naming only an age band, which beats the general rule. A
-- hand-maintained precedence column would be a column somebody forgets to renumber when they
-- insert a rule between two others.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.plausibility_for(
  p_code text, p_sex text, p_age_years numeric
) RETURNS SETOF core.plausibility_rule
LANGUAGE sql STABLE AS $$
  SELECT r.*
    FROM core.plausibility_rule r
   WHERE r.code = p_code
     AND (r.sex IS NULL OR r.sex = p_sex)
     AND (r.min_age_years IS NULL OR p_age_years IS NULL OR p_age_years >= r.min_age_years)
     AND (r.max_age_years IS NULL OR p_age_years IS NULL OR p_age_years <  r.max_age_years)
   ORDER BY num_nonnulls(r.sex, r.min_age_years, r.max_age_years) DESC,
            -- A stable tie-break, so two equally specific rules do not alternate between
            -- deployments. The narrower age band wins; then the older row.
            coalesce(r.max_age_years, 200) - coalesce(r.min_age_years, 0),
            r.updated_at
   LIMIT 1;
$$;
-- +goose StatementEnd

-- SETOF rather than a plain composite return, deliberately. A function returning
-- `core.plausibility_rule` yields one all-NULL row when nothing matches, and a caller then
-- has to tell "no rule" apart from "a rule whose every column is null" — which is how a
-- missing rule becomes a rule that permits everything. SETOF returns nothing, and nothing is
-- unambiguous.

-- One registry band widened first, because a rule below is narrower than it only if it is.
--
-- BODY_WEIGHT's lower edge was 1 kg, which is a value nobody would type by accident — and
-- also 400 g above the smallest premature newborn a clinic sees. The registry is the outer
-- edge of what the code can hold at all; the *clinically* interesting floor is the infant
-- rule below, at 0.4 kg. Widening it here rather than narrowing the rule is deliberate: a
-- band that excludes a real patient is a band that makes staff work around the system.
UPDATE core.observation_code SET min_canonical = 0.4 WHERE code = 'BODY_WEIGHT';

-- The proposed bands.
--
-- **Every number below needs Dr. Nahid's approval** (`approved_by` is null until it has it).
-- They are chosen to refuse typing errors and nothing else: the absolute limits are outside
-- any human being who has ever been measured, and the soft limits are set where a value stops
-- being ordinary and starts being worth a second look.
INSERT INTO core.plausibility_rule
  (code, sex, min_age_years, max_age_years,
   absolute_min, absolute_max, plausible_min, plausible_max,
   max_increase, max_decrease, max_increase_per_day, max_decrease_per_day,
   note_en, note_bn) VALUES

  -- Height. The absolute band spans a newborn to the tallest person on record; the soft band
  -- is adult. The delta rule is the interesting one: an adult does not gain height, and a
  -- 12 cm gain since the last visit is the single commonest anthropometry typing error.
  ('BODY_HEIGHT', NULL, 18, NULL, 100, 230, 135, 200, 2, 4, NULL, NULL,
   'An adult''s height does not change. A large difference from the last visit is a '
   'measuring or typing error far more often than it is a finding.',
   'প্রাপ্তবয়স্কের উচ্চতা বদলায় না। গতবারের সঙ্গে বড় পার্থক্য প্রায় সবসময়ই মাপ বা টাইপের ভুল।'),
  ('BODY_HEIGHT', NULL, 2, 18, 70, 210, 78, 195, NULL, 2, 0.15, NULL,
   'A child grows, so height is limited by rate rather than by total change.',
   'শিশু বাড়ে, তাই উচ্চতার সীমা মোট পরিবর্তনে নয়, দৈনিক হারে।'),
  ('BODY_HEIGHT', NULL, NULL, 2, 30, 110, 44, 100, NULL, 1, 0.35, NULL,
   'Length, measured lying down, from birth.',
   'শুইয়ে মাপা দৈর্ঘ্য, জন্ম থেকে।'),

  -- Weight. The soft band is where an adult weight stops being ordinary; the delta limits are
  -- where a change stops being one and starts being a different patient.
  ('BODY_WEIGHT', NULL, 18, NULL, 20, 350, 30, 180, 15, 15, 0.5, 0.5,
   'A change of more than 15 kg since the last visit, or half a kilogram a day, is '
   'either a serious clinical event or the wrong patient.',
   'গতবারের চেয়ে ১৫ কেজির বেশি পরিবর্তন, বা দিনে আধা কেজি, হয় গুরুতর অবস্থা নয়তো ভুল রোগী।'),
  ('BODY_WEIGHT', NULL, 2, 18, 5, 250, 8, 150, NULL, 10, 0.2, 0.2,
   'A growing child gains; the limit is on the rate and on loss.',
   'বেড়ে ওঠা শিশুর ওজন বাড়ে; সীমা হারে ও ওজন কমায়।'),
  ('BODY_WEIGHT', NULL, NULL, 2, 0.4, 40, 1.5, 25, NULL, 2, 0.1, 0.05,
   'From a premature newborn upward. Weight loss in an infant is never routine.',
   'অপরিণত নবজাতক থেকে। শিশুর ওজন কমা কখনোই স্বাভাবিক নয়।'),

  -- Circumferences. A waist larger than a hip is possible and clinically important, so
  -- neither is bounded by the other here — that comparison belongs to CP58's risk scoring.
  ('WAIST_CIRC', NULL, NULL, NULL, 30, 230, 45, 160, 20, 20, NULL, NULL,
   'A tape measure read at the wrong mark is the usual cause of a large jump.',
   'ফিতার ভুল দাগ পড়াই বড় লাফের সাধারণ কারণ।'),
  ('HIP_CIRC', NULL, NULL, NULL, 30, 230, 55, 170, 20, 20, NULL, NULL, '', ''),
  ('MID_ARM_CIRC', NULL, NULL, NULL, 5, 80, 9, 55, 10, 10, NULL, NULL, '', ''),

  -- Body composition. A scale that has lost contact with a bare foot reports absurd figures
  -- rather than nothing, which is why the absolute band matters more here than elsewhere.
  ('BODY_FAT_PCT', 'female', 18, NULL, 2, 70, 12, 55, 15, 15, NULL, NULL, '', ''),
  ('BODY_FAT_PCT', 'male',   18, NULL, 2, 70, 4,  50, 15, 15, NULL, NULL, '', ''),
  ('BODY_FAT_PCT', NULL, NULL, NULL, 1, 70, 5, 60, 20, 20, NULL, NULL, '', ''),
  ('MUSCLE_MASS',  NULL, NULL, NULL, 1, 120, 10, 70, 10, 10, NULL, NULL, '', ''),

  -- Vitals. The soft bands here are *plausibility*, not clinical alerting: a systolic of 210
  -- is entirely possible and is a CP50 critical value, not a typing error. The band is set
  -- where a number stops being a blood pressure at all.
  ('BP_SYSTOLIC',  NULL, NULL, NULL, 50, 290, 70, 240, NULL, NULL, NULL, NULL,
   'Below 70 or above 240 is usually a cuff, a machine or a transposed pair of digits.',
   '৭০-এর নিচে বা ২৪০-এর উপরে সাধারণত কাফ, যন্ত্র বা সংখ্যা উল্টে টাইপ করার ফল।'),
  ('BP_DIASTOLIC', NULL, NULL, NULL, 25, 190, 40, 140, NULL, NULL, NULL, NULL, '', ''),
  ('HEART_RATE',   NULL, NULL, NULL, 25, 240, 40, 180, NULL, NULL, NULL, NULL, '', ''),
  ('RESP_RATE',    NULL, 12, NULL,   5,  70,  8,  40, NULL, NULL, NULL, NULL, '', ''),
  ('RESP_RATE',    NULL, NULL, 12,   8,  80, 12,  60, NULL, NULL, NULL, NULL,
   'Children breathe faster; a rate normal in a two-year-old would be alarming in an adult.',
   'শিশুরা দ্রুত শ্বাস নেয়; দুই বছরের শিশুর স্বাভাবিক হার প্রাপ্তবয়স্কে উদ্বেগজনক।'),
  ('BODY_TEMP',    NULL, NULL, NULL, 30, 45, 34.5, 41.5, NULL, NULL, NULL, NULL,
   'Outside 34.5–41.5 °C, check the thermometer and the site before recording.',
   '৩৪.৫–৪১.৫ °সে-এর বাইরে হলে থার্মোমিটার ও মাপার জায়গা দেখে নিন।'),
  ('SPO2',         NULL, NULL, NULL, 40, 100, 85, 100, NULL, NULL, NULL, NULL,
   'Below 85% is usually a poor probe trace. It is also a critical value, so check the '
   'trace and then record what you see.',
   '৮৫%-এর নিচে সাধারণত প্রোবের সংকেত দুর্বল। এটি জরুরি মানও বটে — সংকেত দেখে যা পান তাই লিখুন।');

-- The soft band lives inside the hard one, and the hard one lives inside the registry's.
--
-- Without this, a rule could be written whose absolute band is wider than the code's own
-- min_canonical/max_canonical — and the value would then be refused by the registry with a
-- message about a range the operator was never shown. Two limits that can each be wider than
-- the other is a system nobody can predict.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_plausibility_bands_sit_inside_the_registry() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender record;
BEGIN
  SELECT r.code, r.absolute_min, r.absolute_max, c.min_canonical, c.max_canonical
    INTO offender
    FROM core.plausibility_rule r
    JOIN core.observation_code c ON c.code = r.code
   WHERE (r.absolute_min IS NOT NULL AND c.min_canonical IS NOT NULL
          AND r.absolute_min < c.min_canonical)
      OR (r.absolute_max IS NOT NULL AND c.max_canonical IS NOT NULL
          AND r.absolute_max > c.max_canonical)
   LIMIT 1;

  IF FOUND THEN
    RAISE EXCEPTION 'plausibility rule for % is wider than the code itself (% .. % vs % .. %)',
      offender.code, offender.absolute_min, offender.absolute_max,
      offender.min_canonical, offender.max_canonical
      USING HINT = 'Widen the registry band first, deliberately. A rule wider than the '
                   'registry shows the operator one range and refuses them with another.';
  END IF;
END;
$$;
-- +goose StatementEnd

-- Every code a station types into has a rule.
--
-- The failure this prevents is quiet: somebody adds a measured code at CP51, no rule is
-- written for it, and that one field silently accepts anything for a year.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_measured_code_has_a_plausibility_rule() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender text;
BEGIN
  SELECT c.code INTO offender
    FROM core.observation_code c
   WHERE c.category IN ('ANTHRO', 'VITAL')
     AND c.value_type = 'numeric'
     AND c.retired_at IS NULL
     AND NOT EXISTS (SELECT 1 FROM core.plausibility_rule r WHERE r.code = c.code)
   LIMIT 1;

  IF offender IS NOT NULL THEN
    RAISE EXCEPTION '% is typed at a station and has no plausibility rule', offender
      USING HINT = 'A measured code with no rule accepts anything an operator can type. '
                   'Add a rule, even a wide one — a wide rule is a decision, an absent '
                   'rule is an oversight.';
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_plausibility_bands_sit_inside_the_registry() IS
  'Raises if a plausibility rule is wider than its code''s own band (CP46).';
COMMENT ON FUNCTION core.assert_every_measured_code_has_a_plausibility_rule() IS
  'Raises if a measured code has no plausibility rule at all (CP46).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_plausibility_bands_sit_inside_the_registry',
   'no plausibility rule is wider than the code it constrains', 47),
  ('assert_every_measured_code_has_a_plausibility_rule',
   'every code an operator types at a station has a plausibility rule', 48)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- The confirmation, on the record.
--
-- "Every soft-limit confirmation recorded as an event so the pattern is auditable" — and the
-- read model carries it too, so the question "which rules are staff overriding every day"
-- is one query rather than a ledger replay. A rule overridden twenty times a week is a rule
-- that is wrong, and this is how the clinic finds that out from its own data.
ALTER TABLE read.observation
  ADD COLUMN implausible_confirmed boolean NOT NULL DEFAULT false,
  ADD COLUMN implausible_reason text NOT NULL DEFAULT '';

COMMENT ON COLUMN read.observation.implausible_confirmed IS
  'The operator was warned this value was outside the plausible band and confirmed it (CP46).';


-- The projection carries the confirmation through.
--
-- Rewritten whole rather than patched, because a projection function is read years later by
-- somebody debugging a rebuild, and a function assembled from three migrations is a function
-- nobody can read in one sitting.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_observation(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  v_replaces uuid := nullif(p->>'replaces', '')::uuid;
BEGIN
  INSERT INTO read.observation (
    id, facility_id, patient_id, visit_id, encounter_id, code,
    category, value_type,
    entered_num, entered_unit,
    value_text, value_bool, value_code, value_json,
    effective_at, recorded_at, source, status,
    recorded_by, recorded_role, station_code, device_id,
    event_id, global_seq, note,
    formula, formula_version, inputs,
    implausible_confirmed, implausible_reason)
  VALUES (
    (p->>'observation_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    nullif(p->>'visit_id', '')::uuid,
    nullif(p->>'encounter_id', '')::uuid,
    p->>'code',
    -- Placeholders. The trigger overwrites both from the registry, which is the only place
    -- they are allowed to come from.
    'ANTHRO', 'numeric',
    nullif(p->>'entered_num', '')::numeric,
    nullif(p->>'entered_unit', ''),
    coalesce(p->>'value_text', ''),
    CASE WHEN p ? 'value_bool' AND p->>'value_bool' <> '' THEN (p->>'value_bool')::boolean END,
    coalesce(p->>'value_code', ''),
    CASE WHEN p ? 'value_json' THEN p->'value_json' END,
    (p->>'effective_at')::timestamptz,
    (p->>'recorded_at')::timestamptz,
    p->>'source',
    'ACTIVE',
    (p->>'recorded_by')::uuid,
    coalesce(p->>'recorded_role', ''),
    coalesce(p->>'station_code', ''),
    nullif(p->>'device_id', '')::uuid,
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint,
    coalesce(p->>'note', ''),
    coalesce(p->>'formula', ''),
    coalesce(p->>'formula_version', ''),
    CASE WHEN p ? 'inputs' THEN p->'inputs' END,
    coalesce((p->>'implausible_confirmed')::boolean, false),
    coalesce(p->>'implausible_reason', ''))
  ON CONFLICT (event_id) DO NOTHING;

  IF v_replaces IS NOT NULL THEN
    UPDATE read.observation
       SET status = coalesce(nullif(p->>'replaced_status', ''), 'CORRECTED'),
           replaced_by = (p->>'observation_id')::uuid
     WHERE id = v_replaces AND status = 'ACTIVE';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down

ALTER TABLE read.observation
  DROP COLUMN IF EXISTS implausible_reason,
  DROP COLUMN IF EXISTS implausible_confirmed;
DELETE FROM ops.invariant WHERE function_name IN (
  'assert_plausibility_bands_sit_inside_the_registry',
  'assert_every_measured_code_has_a_plausibility_rule');
DROP FUNCTION IF EXISTS core.assert_every_measured_code_has_a_plausibility_rule();
DROP FUNCTION IF EXISTS core.assert_plausibility_bands_sit_inside_the_registry();
DROP FUNCTION IF EXISTS core.plausibility_for(text, text, numeric);
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name = 'plausibility_rule';
DROP TABLE IF EXISTS core.plausibility_rule;
