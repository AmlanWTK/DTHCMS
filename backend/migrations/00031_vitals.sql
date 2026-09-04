-- Station 5's vitals: the context a blood pressure needs, and what counts as normal (CP49).
--
-- # Three different things a number can be outside
--
-- CP42 gave every code a **plausibility** band: outside it, the number is a typing error.
-- CP46 narrowed that per patient and added the soft band and the delta checks. Neither says
-- anything clinical — a systolic of 210 is entirely plausible and entirely alarming.
--
-- This adds the third: a **reference range**, what is normal for this patient. A value
-- outside it is flagged on the screen the moment it is typed, which is criterion 3. It is
-- still not an alert — critical values, the audible warning and the escalation chain are
-- CP50, and conflating "outside normal" with "call the consultant" is how a clinic ends up
-- ignoring both.
--
-- So: plausible → possible at all. Reference range → ordinary for this patient. Critical →
-- somebody has to act now. Three tables, three meanings, and none of them pretending to be
-- another.
--
-- # Why a blood pressure needs two more fields
--
-- A BP taken on the left arm sitting and one taken on the right arm lying down are different
-- measurements, and a series that mixes them silently is a series nobody can read a trend
-- from. Both are coded observations recorded at the same instant as the numbers, rather than
-- columns on a BP row, because the observation model has one shape and adding per-code
-- columns to it is how ten stations became ten tables in every system this one is replacing.

-- +goose Up

INSERT INTO core.observation_code
  (code, category, value_type, dimension, loinc, display_en, display_bn,
   min_canonical, max_canonical, write_permission) VALUES

  ('BP_ARM', 'VITAL', 'coded', NULL, '41904-4',
   'Blood pressure — arm', 'রক্তচাপ — বাহু', NULL, NULL, 'observation.write.vitals'),
  ('BP_POSITION', 'VITAL', 'coded', NULL, '8357-6',
   'Blood pressure — position', 'রক্তচাপ — অবস্থান', NULL, NULL, 'observation.write.vitals'),
  ('BP_CUFF', 'VITAL', 'coded', NULL, '8358-4',
   'Blood pressure — cuff size', 'রক্তচাপ — কাফের মাপ', NULL, NULL, 'observation.write.vitals')

ON CONFLICT (code) DO UPDATE SET
  category = EXCLUDED.category, value_type = EXCLUDED.value_type,
  dimension = EXCLUDED.dimension, loinc = EXCLUDED.loinc,
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  write_permission = EXCLUDED.write_permission;

-- What is normal, as data.
--
-- **Every row below is a proposed value requiring Dr. Nahid's approval**, and the plan says
-- so: "Normal ranges per age band (clinical confirmation, overlaps D-27)". `approved_at` is
-- null until it has it, and the API says so, because a screen that presented these as settled
-- would overstate what anybody has agreed to.
CREATE TABLE core.reference_range (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code text NOT NULL REFERENCES core.observation_code(code),

  -- Who it applies to. NULL means anyone; the most specific match wins, resolved by the
  -- database so a client and the server cannot disagree about which range applies.
  sex           text CHECK (sex IN ('male', 'female')),
  min_age_years numeric(5,2),
  max_age_years numeric(5,2),

  -- Canonical units. Either edge may be absent: a pulse oximeter reading has a floor and no
  -- ceiling worth naming, and a range with an invented upper bound is a range that flags
  -- healthy patients until staff stop looking at it.
  low  numeric,
  high numeric,

  note_en text NOT NULL DEFAULT '',
  note_bn text NOT NULL DEFAULT '',

  approved_by uuid REFERENCES core.app_user(id),
  approved_at timestamptz,
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT reference_range_age_band_is_ordered
    CHECK (min_age_years IS NULL OR max_age_years IS NULL OR min_age_years < max_age_years),
  CONSTRAINT reference_range_is_ordered
    CHECK (low IS NULL OR high IS NULL OR low < high),
  CONSTRAINT reference_range_says_something
    CHECK (num_nonnulls(low, high) > 0)
);

CREATE INDEX reference_range_by_code ON core.reference_range (code);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'reference_range',
   'A normal pulse for a six-year-old is the same in every clinic. Reference ranges are clinical reference data, not a facility setting (CP49).')
ON CONFLICT (schema_name, table_name) DO NOTHING;

GRANT SELECT ON core.reference_range TO dthcms_app;
GRANT SELECT ON core.reference_range TO dthcms_projector;

COMMENT ON TABLE core.reference_range IS
  'What is normal for a patient, per code and age band (CP49). Not plausibility (CP46) and not a critical value (CP50).';

-- The proposed ranges.
--
-- Adult vitals from the ranges in ordinary clinical use; paediatric ranges by age band,
-- because a pulse of 130 is normal in a two-year-old and alarming in an adult and a single
-- band wide enough for both flags nobody.
INSERT INTO core.reference_range (code, sex, min_age_years, max_age_years, low, high, note_en, note_bn) VALUES
  ('BP_SYSTOLIC',  NULL, 18, NULL, 90, 130, '', ''),
  ('BP_DIASTOLIC', NULL, 18, NULL, 60, 85,  '', ''),
  -- Paediatric blood pressure is properly read off a height- and age-indexed table, which is
  -- CP50's work. These two bands are a coarse screen so the field is not unflagged until
  -- then, and they are marked as such.
  ('BP_SYSTOLIC',  NULL, 1, 18, 80, 120,
   'A coarse band. Paediatric blood pressure is read off a height- and age-indexed table (CP50).',
   'একটি মোটামুটি সীমা। শিশুর রক্তচাপ উচ্চতা ও বয়সভিত্তিক টেবিল থেকে পড়া হয় (CP50)।'),
  ('BP_DIASTOLIC', NULL, 1, 18, 45, 80,
   'A coarse band. Paediatric blood pressure is read off a height- and age-indexed table (CP50).',
   'একটি মোটামুটি সীমা। শিশুর রক্তচাপ উচ্চতা ও বয়সভিত্তিক টেবিল থেকে পড়া হয় (CP50)।'),

  ('HEART_RATE', NULL, 18, NULL, 60, 100, '', ''),
  ('HEART_RATE', NULL, 12, 18,   60, 110, '', ''),
  ('HEART_RATE', NULL, 6,  12,   70, 120, '', ''),
  ('HEART_RATE', NULL, 1,  6,    80, 140, '', ''),
  ('HEART_RATE', NULL, NULL, 1, 100, 160,
   'Infants run fast. A rate that would be a tachycardia in an adult is ordinary here.',
   'শিশুদের নাড়ি দ্রুত চলে। প্রাপ্তবয়স্কে যা দ্রুত, শিশুতে তা স্বাভাবিক।'),

  ('RESP_RATE', NULL, 18, NULL, 12, 20, '', ''),
  ('RESP_RATE', NULL, 12, 18,   12, 22, '', ''),
  ('RESP_RATE', NULL, 6,  12,   18, 26, '', ''),
  ('RESP_RATE', NULL, 1,  6,    20, 34, '', ''),
  ('RESP_RATE', NULL, NULL, 1,  30, 55, '', ''),

  ('BODY_TEMP', NULL, NULL, NULL, 36.1, 37.5,
   'Oral or axillary. A rectal reading runs about half a degree higher.',
   'মুখ বা বগলে মাপা। মলদ্বারে মাপলে প্রায় আধা ডিগ্রি বেশি আসে।'),

  -- A floor and no ceiling: 100% is the top of the scale, and a range that flagged it would
  -- flag every healthy patient until staff stopped reading the flag at all.
  ('SPO2', NULL, NULL, NULL, 95, NULL,
   'Below 95% on room air is worth a second reading; below 92% is a critical value (CP50).',
   'ঘরের বাতাসে ৯৫%-এর নিচে হলে আবার মাপুন; ৯২%-এর নিচে জরুরি মান (CP50)।');

-- The most specific range for a patient and a code. Same resolution as CP46's plausibility,
-- and the same reason: a client that ranked them itself would one day flag a value the
-- server considered normal.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.reference_range_for(
  p_code text, p_sex text, p_age_years numeric
) RETURNS SETOF core.reference_range
LANGUAGE sql STABLE AS $$
  SELECT r.*
    FROM core.reference_range r
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

-- A normal range must sit inside what is plausible.
--
-- Without this, a range could flag values the write path refuses outright — an operator
-- watching a field turn amber for a number they are then not allowed to save. Worse, a range
-- *wider* than plausibility would make the flag unreachable at one end, which reads as a
-- rule doing its job while doing nothing.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_normal_sits_inside_plausible() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender record;
BEGIN
  SELECT r.code, r.low, r.high, c.min_canonical, c.max_canonical INTO offender
    FROM core.reference_range r
    JOIN core.observation_code c ON c.code = r.code
   WHERE (r.low  IS NOT NULL AND c.min_canonical IS NOT NULL AND r.low  < c.min_canonical)
      OR (r.high IS NOT NULL AND c.max_canonical IS NOT NULL AND r.high > c.max_canonical)
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'the normal range for % (% .. %) falls outside what the code can hold (% .. %)',
      offender.code, offender.low, offender.high, offender.min_canonical, offender.max_canonical;
  END IF;
END;
$$;
-- +goose StatementEnd

-- Every vital an operator types has a range, or the field is silently unflagged.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_vital_has_a_normal_range() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender text;
BEGIN
  SELECT c.code INTO offender
    FROM core.observation_code c
   WHERE c.category = 'VITAL' AND c.value_type = 'numeric' AND c.retired_at IS NULL
     AND NOT EXISTS (SELECT 1 FROM core.reference_range r WHERE r.code = c.code)
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION '% is a vital with no reference range', offender
      USING HINT = 'A vital with no range is a field that never turns amber, which reads '
                   'to an operator as "this value is fine".';
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_normal_sits_inside_plausible() IS
  'Raises if a normal range falls outside the code''s own band (CP49).';
COMMENT ON FUNCTION core.assert_every_vital_has_a_normal_range() IS
  'Raises if a measured vital has no reference range at all (CP49).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_normal_sits_inside_plausible',
   'no normal range falls outside what its code can hold', 51),
  ('assert_every_vital_has_a_normal_range',
   'every vital an operator types is flagged against a range', 52)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_normal_sits_inside_plausible', 'assert_every_vital_has_a_normal_range');
DROP FUNCTION IF EXISTS core.assert_every_vital_has_a_normal_range();
DROP FUNCTION IF EXISTS core.assert_normal_sits_inside_plausible();
DROP FUNCTION IF EXISTS core.reference_range_for(text, text, numeric);
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name = 'reference_range';
DROP TABLE IF EXISTS core.reference_range;
DELETE FROM core.observation_code WHERE code IN ('BP_ARM', 'BP_POSITION', 'BP_CUFF');
