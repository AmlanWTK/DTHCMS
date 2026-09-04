-- The observation model and the units framework (CP42, blueprint §6, §11).
--
-- Ten stations record measured values. Ten bespoke tables would make the timeline, the
-- research extract and the FHIR mapping ten times harder, and would guarantee that the
-- eleventh station invents an eleventh shape. So there is one table, one event family, and
-- a code registry that says what each kind of value *is*.
--
-- # The unit rule, and why it is structural
--
-- Unit errors are a classic patient-safety failure: a weight of 154 stored without its unit
-- is 154 kg or 154 lb depending on who is looking, and a drug dose computed from it is
-- wrong by a factor of 2.2. The blueprint asks that this be *structurally prevented*, which
-- means it cannot be a validation somebody remembers to run.
--
-- Three things together make it structural:
--
--   1. `core.observation_code` says whether a code is unit-bearing, by naming its dimension.
--      A dimension is not optional metadata — it is what makes a conversion possible at all.
--   2. `core.observation_is_well_formed()` is a trigger on the read model, so a row that
--      lacks the unit its code requires cannot be inserted. Not "is rejected by the API" —
--      cannot be inserted, by anything, including a projection rebuild.
--   3. `core.assert_measurements_carry_their_units()` is a standing invariant, so a schema
--      change that weakened the trigger fails the deployment rather than the next audit.
--
-- # Both values are stored, and that is the point
--
-- The canonical value is what everything computes from: one unit per dimension, so a query
-- summing weights never has to ask what unit each row is in. The *entered* value and unit
-- are stored beside it, exactly as typed.
--
-- That second column is what makes criterion 2 true rather than approximately true. Showing
-- a value back in the unit it was entered in is a read, not a round trip — 154 lb comes back
-- as 154 lb, not as 69.85 kg converted back to 153.99 lb. The documented tolerance applies
-- only to the canonical value, which is the one arithmetic uses, and `numeric` rather than
-- `double precision` keeps even that exact for every decimal conversion in the table.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Units
-- ---------------------------------------------------------------------------

CREATE TABLE core.unit (
  code      text PRIMARY KEY,
  -- What kind of quantity this measures. Two units convert into each other if and only if
  -- they share a dimension, which is the whole of the conversion rule.
  --
  -- Deliberately analyte-specific where the chemistry demands it: `glucose_concentration`
  -- and `cholesterol_concentration` are separate dimensions even though both are mmol/L and
  -- mg/dL, because the factor between them is a molar mass and glucose's is not
  -- cholesterol's. One shared "concentration" dimension would silently convert a cholesterol
  -- with glucose's constant, which is a wrong number that looks entirely plausible.
  dimension text NOT NULL,

  -- The canonical unit of its dimension. Exactly one per dimension; see the index below.
  is_canonical boolean NOT NULL DEFAULT false,

  -- canonical = entered * factor + offset.
  --
  -- The offset exists for exactly one unit in this table — Fahrenheit — and leaving it out
  -- would have meant either no Fahrenheit or a special case in code. A column is cheaper
  -- than a special case, and a clinic that buys an imported thermometer will need it.
  factor  numeric NOT NULL CHECK (factor <> 0),
  "offset" numeric NOT NULL DEFAULT 0,

  display_en text NOT NULL,
  display_bn text NOT NULL,
  -- How many decimals this unit is written with. A weight in kg is 69.9; the same weight in
  -- grams is not 69850.0. Display precision is a property of the unit, not of the screen.
  decimals integer NOT NULL DEFAULT 1 CHECK (decimals BETWEEN 0 AND 4),

  CONSTRAINT unit_canonical_is_identity
    CHECK (NOT is_canonical OR (factor = 1 AND "offset" = 0))
);

-- One canonical unit per dimension. Two would mean "convert to canonical" had two answers.
CREATE UNIQUE INDEX unit_one_canonical_per_dimension
  ON core.unit (dimension) WHERE is_canonical;

CREATE INDEX unit_by_dimension ON core.unit (dimension);

GRANT SELECT ON core.unit TO dthcms_app;
GRANT SELECT ON core.unit TO dthcms_projector;

COMMENT ON TABLE core.unit IS
  'Units and their conversion to their dimension''s canonical unit. Two units convert if and only if they share a dimension (CP42).';
COMMENT ON COLUMN core.unit.dimension IS
  'Analyte-specific where the chemistry demands it: glucose and cholesterol concentrations are separate dimensions because the mmol/L↔mg/dL factor is a molar mass.';

INSERT INTO core.unit (code, dimension, is_canonical, factor, "offset", display_en, display_bn, decimals) VALUES
  -- Length. Centimetres are canonical because every anthropometric reference range and
  -- every growth chart this clinic will use is written in them.
  ('cm',     'length', true,  1,        0, 'cm',     'সেমি',      1),
  ('m',      'length', false, 100,      0, 'm',      'মিটার',     2),
  ('in',     'length', false, 2.54,     0, 'in',     'ইঞ্চি',      1),
  ('[ft_i]', 'length', false, 30.48,    0, 'ft',     'ফুট',       2),

  -- Mass.
  ('kg',     'mass',   true,  1,        0, 'kg',     'কেজি',      1),
  ('g',      'mass',   false, 0.001,    0, 'g',      'গ্রাম',      0),
  ('[lb_av]','mass',   false, 0.45359237, 0, 'lb',   'পাউন্ড',     1),

  -- Pressure. mmHg is canonical for the same reason cm is: it is the unit every blood
  -- pressure guideline in the building is written in.
  ('mm[Hg]', 'pressure', true, 1,       0, 'mmHg',   'মিমি পারদ',  0),
  ('kPa',    'pressure', false, 7.50062, 0, 'kPa',   'কিপিএ',     1),

  -- Temperature. The one offset conversion in the table.
  ('Cel',    'temperature', true, 1,     0, '°C',    '°সে',       1),
  ('[degF]', 'temperature', false, 0.5555555555555556, -17.7777777777777778, '°F', '°ফা', 1),

  -- Rates and fractions.
  ('/min',   'rate',     true, 1,        0, '/min',  '/মিনিট',     0),
  ('%',      'fraction', true, 1,        0, '%',     '%',         0),
  ('kcal/d', 'energy_rate', true, 1,     0, 'kcal/day', 'কিলোক্যালরি/দিন', 0),
  ('kg/m2',  'bmi',      true, 1,        0, 'kg/m²', 'কেজি/মি²',   1),
  ('m2',     'area',     true, 1,        0, 'm²',    'মি²',       2),
  ('1',      'ratio',    true, 1,        0, '',      '',          2),
  ('mL/min/{1.73_m2}', 'gfr', true, 1,   0, 'mL/min/1.73m²', 'মিলি/মিনিট/১.৭৩মি²', 0),

  -- Concentrations, one dimension per analyte. The mg/dL factors are 1/molar-mass in
  -- mg·dL⁻¹ per mmol·L⁻¹, from the analyte's molecular weight:
  --   glucose      180.16 g/mol → 1 mg/dL = 0.0555 mmol/L
  --   cholesterol  386.65 g/mol → 1 mg/dL = 0.02586 mmol/L
  --   triglyceride 885.4  g/mol → 1 mg/dL = 0.01129 mmol/L
  --   creatinine   113.12 g/mol → 1 mg/dL = 88.42 µmol/L
  ('mmol/L',  'glucose_concentration',     true,  1,       0, 'mmol/L', 'মিলিমোল/লি', 1),
  ('mg/dL',   'glucose_concentration',     false, 0.05551, 0, 'mg/dL',  'মিগ্রা/ডেসিলি', 0),
  ('mmol/L#chol', 'cholesterol_concentration', true, 1,    0, 'mmol/L', 'মিলিমোল/লি', 2),
  ('mg/dL#chol',  'cholesterol_concentration', false, 0.02586, 0, 'mg/dL', 'মিগ্রা/ডেসিলি', 0),
  ('mmol/L#trig', 'triglyceride_concentration', true, 1,   0, 'mmol/L', 'মিলিমোল/লি', 2),
  ('mg/dL#trig',  'triglyceride_concentration', false, 0.01129, 0, 'mg/dL', 'মিগ্রা/ডেসিলি', 0),
  ('umol/L',  'creatinine_concentration',  true,  1,       0, 'µmol/L', 'মাইক্রোমোল/লি', 0),
  ('mg/dL#cr','creatinine_concentration',  false, 88.42,   0, 'mg/dL',  'মিগ্রা/ডেসিলি', 2),

  -- HbA1c. IFCC mmol/mol is canonical and NGSP % converts into it by the master equation,
  -- IFCC = (NGSP − 2.15) × 10.929, which is linear and so fits factor and offset exactly.
  -- The clinic reads and prescribes in NGSP %, and CP44's dual display is where that is
  -- solved — by showing both, which is the blueprint's rule for every clinical value.
  ('mmol/mol', 'hba1c', true,  1,       0, 'mmol/mol', 'মিলিমোল/মোল', 0),
  ('%#ngsp',   'hba1c', false, 10.929, -23.49735, '%', '%',           1)
ON CONFLICT (code) DO UPDATE SET
  dimension = EXCLUDED.dimension, is_canonical = EXCLUDED.is_canonical,
  factor = EXCLUDED.factor, "offset" = EXCLUDED."offset",
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  decimals = EXCLUDED.decimals;

-- ---------------------------------------------------------------------------
-- The code registry
-- ---------------------------------------------------------------------------

CREATE TABLE core.observation_code (
  code text PRIMARY KEY,

  -- §6's seven. A discriminator rather than seven tables: the difference between a vital
  -- and a lab result is what it means, not what shape it is.
  category text NOT NULL CHECK (category IN
    ('ANTHRO', 'VITAL', 'EXAM', 'LAB', 'DERIVED', 'SCREENING', 'PRO')),

  value_type text NOT NULL CHECK (value_type IN
    ('numeric', 'text', 'boolean', 'coded', 'structured')),

  -- NULL means unitless: a boolean finding, a coded result, a free-text note. A numeric
  -- code with no dimension is a number with no unit, which is the thing this table exists
  -- to make impossible — see the CHECK below.
  dimension text,

  -- LOINC where it is known and left **empty where it is not**.
  --
  -- An empty code is honest and a wrong one is a mapping error that surfaces years later,
  -- in an export, in somebody else's system, after the person who guessed has left. The
  -- gaps here are real gaps and CP150's FHIR mapping is where they get filled, by somebody
  -- looking them up rather than remembering them.
  loinc text NOT NULL DEFAULT '',

  display_en text NOT NULL,
  display_bn text NOT NULL,

  -- Plausibility, in the canonical unit. Not clinical judgement — that is CP50's
  -- critical-value table. This is the band outside which a number is a typing error, and
  -- refusing it at the point of entry is the only place it can be corrected cheaply.
  min_canonical numeric,
  max_canonical numeric,

  -- Which permission writing this code needs (§4.4). Per category in practice, but per code
  -- in the schema, because "the nutritionist writes diet-related values and not vitals" is a
  -- rule about *values*, and the day one category needs splitting the column is already
  -- there.
  write_permission text NOT NULL REFERENCES core.permission(code),

  -- Retired rather than deleted: an observation recorded under a code that was later
  -- withdrawn is still a fact about a patient.
  retired_at timestamptz,

  CONSTRAINT observation_code_units_are_for_numbers
    CHECK (dimension IS NULL OR value_type = 'numeric'),
  CONSTRAINT observation_code_range_is_ordered
    CHECK (min_canonical IS NULL OR max_canonical IS NULL OR min_canonical <= max_canonical)
);

CREATE INDEX observation_code_by_category ON core.observation_code (category)
  WHERE retired_at IS NULL;

GRANT SELECT ON core.observation_code TO dthcms_app;
GRANT SELECT ON core.observation_code TO dthcms_projector;

COMMENT ON TABLE core.observation_code IS
  'What each kind of measured value is: its category, its shape, its unit dimension, its plausibility band and who may write it (CP42).';
COMMENT ON COLUMN core.observation_code.loinc IS
  'Empty where the LOINC code is not known. An empty code is honest; a guessed one is a mapping error that surfaces years later in somebody else''s system.';

INSERT INTO core.observation_code
  (code, category, value_type, dimension, loinc, display_en, display_bn,
   min_canonical, max_canonical, write_permission) VALUES
  -- ANTHRO (§3 step 2, CP45)
  ('BODY_HEIGHT',   'ANTHRO', 'numeric', 'length',   '8302-2',  'Height', 'উচ্চতা', 30, 250, 'observation.write.anthro'),
  ('BODY_WEIGHT',   'ANTHRO', 'numeric', 'mass',     '29463-7', 'Weight', 'ওজন', 1, 400, 'observation.write.anthro'),
  ('WAIST_CIRC',    'ANTHRO', 'numeric', 'length',   '8280-0',  'Waist circumference', 'কোমরের মাপ', 20, 250, 'observation.write.anthro'),
  ('HIP_CIRC',      'ANTHRO', 'numeric', 'length',   '',        'Hip circumference', 'নিতম্বের মাপ', 20, 250, 'observation.write.anthro'),
  ('MID_ARM_CIRC',  'ANTHRO', 'numeric', 'length',   '',        'Mid-upper arm circumference', 'বাহুর মাপ', 5, 80, 'observation.write.anthro'),
  ('BODY_FAT_PCT',  'ANTHRO', 'numeric', 'fraction', '',        'Body fat', 'দেহের চর্বি', 1, 70, 'observation.write.anthro'),

  -- VITAL (CP49)
  ('BP_SYSTOLIC',   'VITAL', 'numeric', 'pressure',    '8480-6',  'Systolic blood pressure', 'সিস্টোলিক রক্তচাপ', 40, 300, 'observation.write.vitals'),
  ('BP_DIASTOLIC',  'VITAL', 'numeric', 'pressure',    '8462-4',  'Diastolic blood pressure', 'ডায়াস্টোলিক রক্তচাপ', 20, 200, 'observation.write.vitals'),
  ('HEART_RATE',    'VITAL', 'numeric', 'rate',        '8867-4',  'Pulse', 'নাড়ির গতি', 20, 250, 'observation.write.vitals'),
  ('RESP_RATE',     'VITAL', 'numeric', 'rate',        '9279-1',  'Respiratory rate', 'শ্বাসের হার', 4, 80, 'observation.write.vitals'),
  ('BODY_TEMP',     'VITAL', 'numeric', 'temperature', '8310-5',  'Temperature', 'তাপমাত্রা', 30, 45, 'observation.write.vitals'),
  ('SPO2',          'VITAL', 'numeric', 'fraction',    '59408-5', 'Oxygen saturation', 'অক্সিজেন সম্পৃক্তি', 40, 100, 'observation.write.vitals'),

  -- LAB (CP5x). Ranges are plausibility, not reference intervals.
  ('GLUCOSE_FASTING', 'LAB', 'numeric', 'glucose_concentration',     '1558-6',  'Fasting plasma glucose', 'খালি পেটে গ্লুকোজ', 0.5, 60, 'observation.write.history'),
  ('GLUCOSE_RANDOM',  'LAB', 'numeric', 'glucose_concentration',     '',        'Random plasma glucose', 'যেকোনো সময়ের গ্লুকোজ', 0.5, 60, 'observation.write.history'),
  ('HBA1C',           'LAB', 'numeric', 'hba1c',                     '4548-4',  'HbA1c', 'এইচবিএ১সি', 10, 200, 'observation.write.history'),
  ('CREATININE',      'LAB', 'numeric', 'creatinine_concentration',  '2160-0',  'Serum creatinine', 'সিরাম ক্রিয়েটিনিন', 10, 2000, 'observation.write.history'),
  ('CHOL_TOTAL',      'LAB', 'numeric', 'cholesterol_concentration', '2093-3',  'Total cholesterol', 'মোট কোলেস্টেরল', 0.5, 30, 'observation.write.history'),
  ('CHOL_HDL',        'LAB', 'numeric', 'cholesterol_concentration', '2085-9',  'HDL cholesterol', 'এইচডিএল', 0.1, 10, 'observation.write.history'),
  ('CHOL_LDL',        'LAB', 'numeric', 'cholesterol_concentration', '13457-7', 'LDL cholesterol', 'এলডিএল', 0.1, 25, 'observation.write.history'),
  ('TRIGLYCERIDE',    'LAB', 'numeric', 'triglyceride_concentration','2571-8',  'Triglycerides', 'ট্রাইগ্লিসারাইড', 0.1, 60, 'observation.write.history'),

  -- DERIVED (CP43). Written by the server, never typed, which is why the permission is the
  -- one held by whoever recorded the inputs.
  ('BMI',       'DERIVED', 'numeric', 'bmi',   '39156-5', 'Body mass index', 'বিএমআই', 5, 100, 'observation.write.anthro'),
  ('WHR',       'DERIVED', 'numeric', 'ratio', '',        'Waist-hip ratio', 'কোমর-নিতম্ব অনুপাত', 0.3, 3, 'observation.write.anthro'),
  ('BSA',       'DERIVED', 'numeric', 'area',  '',        'Body surface area', 'দেহতলের ক্ষেত্রফল', 0.1, 4, 'observation.write.anthro'),
  ('BMR',       'DERIVED', 'numeric', 'energy_rate', '',  'Basal metabolic rate', 'বিএমআর', 200, 6000, 'observation.write.anthro'),
  ('EGFR',      'DERIVED', 'numeric', 'gfr',   '',        'eGFR (CKD-EPI 2021)', 'ইজিএফআর', 1, 200, 'observation.write.history'),
  ('PACK_YEARS','DERIVED', 'numeric', 'ratio', '',        'Pack-years', 'প্যাক-ইয়ার', 0, 200, 'observation.write.lifestyle'),

  -- EXAM (CP51). Not numbers, which is the point of the value_type column.
  ('FOOT_ULCER_PRESENT', 'EXAM', 'boolean', NULL, '', 'Foot ulcer present', 'পায়ে ঘা আছে', NULL, NULL, 'observation.write.history'),
  ('MONOFILAMENT_LEFT',  'EXAM', 'coded',   NULL, '', 'Monofilament, left foot', 'মনোফিলামেন্ট, বাঁ পা', NULL, NULL, 'observation.write.history'),
  ('MONOFILAMENT_RIGHT', 'EXAM', 'coded',   NULL, '', 'Monofilament, right foot', 'মনোফিলামেন্ট, ডান পা', NULL, NULL, 'observation.write.history'),
  ('FUNDUS_FINDING',     'EXAM', 'structured', NULL, '', 'Fundus examination', 'চক্ষু পরীক্ষা', NULL, NULL, 'observation.write.history'),

  -- SCREENING (CP58, CP145)
  ('DIABETES_SCREEN', 'SCREENING', 'coded', NULL, '', 'Diabetes screening result', 'ডায়াবেটিস স্ক্রিনিং ফল', NULL, NULL, 'observation.write.history'),

  -- PRO — patient-reported (CP88, [R-11])
  ('IMPROVEMENT_SCORE', 'PRO', 'numeric', 'ratio', '', 'How much better do you feel? (1–10)', 'কতটা ভালো বোধ করছেন? (১–১০)', 1, 10, 'observation.write.history'),
  ('SYMPTOM_NOTE',      'PRO', 'text',    NULL,    '', 'In the patient''s own words', 'রোগীর নিজের ভাষায়', NULL, NULL, 'observation.write.history')
ON CONFLICT (code) DO UPDATE SET
  category = EXCLUDED.category, value_type = EXCLUDED.value_type,
  dimension = EXCLUDED.dimension, loinc = EXCLUDED.loinc,
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  min_canonical = EXCLUDED.min_canonical, max_canonical = EXCLUDED.max_canonical,
  write_permission = EXCLUDED.write_permission;

-- Every code's dimension must be a dimension some unit actually has. A foreign key cannot
-- say that — `dimension` is not a table — so it is a trigger, and it fires on the registry
-- rather than on every observation.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.observation_code_dimension_exists() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.dimension IS NOT NULL
     AND NOT EXISTS (SELECT 1 FROM core.unit WHERE dimension = NEW.dimension AND is_canonical) THEN
    RAISE EXCEPTION 'no canonical unit for dimension %', NEW.dimension
      USING HINT = 'A code declares a dimension so its values can be converted. A dimension '
                   'with no canonical unit has nothing to convert *to* (CP42).';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER observation_code_dimension_exists
  BEFORE INSERT OR UPDATE ON core.observation_code
  FOR EACH ROW EXECUTE FUNCTION core.observation_code_dimension_exists();

-- Both registries are deployment-wide reference data, not one clinic's.
--
-- A kilogram does not belong to a facility, and neither does the meaning of BODY_HEIGHT: a
-- second DTHC clinic that recorded weights against its own private code list would produce
-- records the first clinic could not read, which is the opposite of what a shared code
-- registry is for. Facility scoping is for *patient* data.
INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'unit',
   'A kilogram belongs to no clinic. Units are deployment-wide reference data (CP42).'),
  ('core', 'observation_code',
   'The meaning of BODY_HEIGHT is the same in every DTHC clinic. A per-facility code list would produce records other clinics could not read (CP42).')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Conversion
-- ---------------------------------------------------------------------------

-- To the canonical unit of the unit's own dimension. Refuses a unit it does not know rather
-- than returning the value unchanged, which is how a wrong number survives a conversion.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.to_canonical(p_value numeric, p_unit text)
RETURNS numeric
LANGUAGE plpgsql STABLE
AS $$
DECLARE
  u core.unit;
BEGIN
  SELECT * INTO u FROM core.unit WHERE code = p_unit;
  IF u.code IS NULL THEN
    RAISE EXCEPTION 'unknown unit %', p_unit
      USING HINT = 'Units are a closed set (core.unit). A unit nobody declared is a unit '
                   'nothing can convert (CP42).';
  END IF;
  RETURN p_value * u.factor + u."offset";
END
$$;
-- +goose StatementEnd

-- And back. The pair round-trips exactly for every unit in the table because `numeric` is
-- exact decimal arithmetic and every factor here is a terminating decimal — see
-- `docs/observations.md` for the tolerance this does *not* cover.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.from_canonical(p_value numeric, p_unit text)
RETURNS numeric
LANGUAGE plpgsql STABLE
AS $$
DECLARE
  u core.unit;
BEGIN
  SELECT * INTO u FROM core.unit WHERE code = p_unit;
  IF u.code IS NULL THEN
    RAISE EXCEPTION 'unknown unit %', p_unit;
  END IF;
  RETURN (p_value - u."offset") / u.factor;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION core.to_canonical(numeric, text) TO dthcms_app;
GRANT EXECUTE ON FUNCTION core.from_canonical(numeric, text) TO dthcms_app;

-- ---------------------------------------------------------------------------
-- The read model
-- ---------------------------------------------------------------------------

CREATE TABLE read.observation (
  id          uuid        PRIMARY KEY,
  facility_id uuid        NOT NULL,
  patient_id  uuid        NOT NULL,
  visit_id    uuid,
  encounter_id uuid,

  code     text NOT NULL REFERENCES core.observation_code(code),
  -- Denormalised from the registry so the timeline and the research extract can filter
  -- without a join. The trigger keeps them honest.
  category text NOT NULL,
  value_type text NOT NULL,

  -- The canonical value, for arithmetic. NULL for the non-numeric value types.
  value_num numeric,
  -- The canonical unit of this code's dimension. NULL exactly when the code is unitless.
  unit text REFERENCES core.unit(code),

  -- As typed, exactly. This is what a screen shows back, so 154 lb returns as 154 lb rather
  -- than as 69.85 kg converted back to 153.99 lb.
  entered_num  numeric,
  entered_unit text REFERENCES core.unit(code),

  value_text text    NOT NULL DEFAULT '',
  value_bool boolean,
  value_code text    NOT NULL DEFAULT '',
  value_json jsonb,

  -- When the thing was true, and when it was written down. A blood pressure taken at 09:05
  -- and entered at 09:20 has two different times, and a timeline that used the second would
  -- put it in the wrong order beside a reading somebody entered promptly.
  effective_at timestamptz NOT NULL,
  recorded_at  timestamptz NOT NULL,

  -- Where it came from. OCR and PATIENT are not STATION, and a physician reading a value
  -- deserves to know which — a number a patient reported at home and a number an operator
  -- measured are different evidence.
  source text NOT NULL CHECK (source IN ('STATION', 'OCR', 'FIELD', 'DEVICE', 'PATIENT')),

  -- ACTIVE    → this is the value
  -- CORRECTED → it was wrong and has been replaced; the replacement points back
  -- SUPERSEDED→ a later measurement of the same thing in the same encounter replaced it
  status text NOT NULL DEFAULT 'ACTIVE'
         CHECK (status IN ('ACTIVE', 'CORRECTED', 'SUPERSEDED')),
  -- The row that replaced this one, when one did.
  replaced_by uuid,

  -- Attribution, from the envelope. The id is the durable fact; the code and role are what
  -- a person reads.
  recorded_by  uuid NOT NULL,
  recorded_role text NOT NULL,
  station_code  text NOT NULL DEFAULT '',
  device_id     uuid,

  event_id  uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL,

  -- A note the operator typed with the value: the cuff size, which arm, "patient could not
  -- stand". Free text because a coded list of caveats is a list that never has the one that
  -- happened.
  note text NOT NULL DEFAULT '',

  CONSTRAINT observation_status_replacement
    CHECK ((status = 'ACTIVE') = (replaced_by IS NULL)),
  CONSTRAINT observation_numeric_has_a_number
    CHECK (value_type <> 'numeric' OR value_num IS NOT NULL),
  CONSTRAINT observation_boolean_has_a_boolean
    CHECK (value_type <> 'boolean' OR value_bool IS NOT NULL),
  CONSTRAINT observation_coded_has_a_code
    CHECK (value_type <> 'coded' OR btrim(value_code) <> ''),
  CONSTRAINT observation_text_has_text
    CHECK (value_type <> 'text' OR btrim(value_text) <> ''),
  CONSTRAINT observation_structured_has_json
    CHECK (value_type <> 'structured' OR value_json IS NOT NULL),
  -- The entered pair is all-or-nothing. Half of it is a value whose unit nobody recorded.
  CONSTRAINT observation_entered_pair
    CHECK ((entered_num IS NULL) = (entered_unit IS NULL))
);

CREATE INDEX observation_by_patient
  ON read.observation (patient_id, code, effective_at DESC) WHERE status = 'ACTIVE';
CREATE INDEX observation_by_visit ON read.observation (visit_id, effective_at);
CREATE INDEX observation_by_encounter ON read.observation (encounter_id);
CREATE INDEX observation_by_category
  ON read.observation (facility_id, category, effective_at DESC);
CREATE INDEX observation_by_seq ON read.observation (global_seq);

GRANT SELECT ON read.observation TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON read.observation TO dthcms_projector;

COMMENT ON TABLE read.observation IS
  'Every measured clinical value, in one shape: canonical value for arithmetic, entered value for display, unit metadata for both (CP42).';
COMMENT ON COLUMN read.observation.entered_num IS
  'As typed. Showing a value back in the unit it was entered in is a read, not a round trip.';

-- ---------------------------------------------------------------------------
-- The unit rule, as a trigger
-- ---------------------------------------------------------------------------

-- Criterion 1, and the reason it is here rather than in a handler: "cannot be stored" has to
-- mean cannot, including by a projection rebuild and by a hand-written UPDATE at three in
-- the morning.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.observation_is_well_formed() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  spec core.observation_code;
  canonical text;
  entered_dimension text;
BEGIN
  SELECT * INTO spec FROM core.observation_code WHERE code = NEW.code;
  IF spec.code IS NULL THEN
    RAISE EXCEPTION 'unknown observation code %', NEW.code;
  END IF;

  -- The category and value type are the registry's, never the caller's. Denormalising them
  -- is a read optimisation; letting them disagree would be a second source of truth.
  NEW.category   := spec.category;
  NEW.value_type := spec.value_type;

  IF spec.dimension IS NULL THEN
    IF NEW.unit IS NOT NULL OR NEW.entered_unit IS NOT NULL THEN
      RAISE EXCEPTION '% is unitless but carries a unit', NEW.code;
    END IF;
    RETURN NEW;
  END IF;

  -- Unit-bearing from here down.
  SELECT code INTO canonical FROM core.unit
   WHERE dimension = spec.dimension AND is_canonical;

  IF NEW.entered_unit IS NULL OR NEW.entered_num IS NULL THEN
    RAISE EXCEPTION '% needs a value and the unit it was entered in', NEW.code
      USING HINT = 'A measurement without its unit is 154 kg or 154 lb depending on who is '
                   'reading it, and a dose computed from it is wrong by a factor of 2.2. '
                   'The unit is not optional metadata (CP42).';
  END IF;

  SELECT dimension INTO entered_dimension FROM core.unit WHERE code = NEW.entered_unit;
  IF entered_dimension IS DISTINCT FROM spec.dimension THEN
    RAISE EXCEPTION '% is a % and cannot be recorded in %',
      NEW.code, spec.dimension, NEW.entered_unit
      USING HINT = 'Two units convert into each other if and only if they share a '
                   'dimension. Recording a weight in centimetres is not a conversion '
                   'problem, it is a different measurement (CP42).';
  END IF;

  -- The stored unit is the canonical one, always. A row is free to have been *entered* in
  -- pounds; it is not free to be *stored* in them, because then a query summing weights
  -- would have to ask each row what it meant.
  NEW.unit := canonical;
  NEW.value_num := core.to_canonical(NEW.entered_num, NEW.entered_unit);

  IF (spec.min_canonical IS NOT NULL AND NEW.value_num < spec.min_canonical)
     OR (spec.max_canonical IS NOT NULL AND NEW.value_num > spec.max_canonical) THEN
    RAISE EXCEPTION '% of % % is outside the plausible range', NEW.code, NEW.entered_num, NEW.entered_unit
      USING HINT = 'Plausibility, not clinical judgement: this is the band outside which a '
                   'number is a typing error. Critical values are CP50 (CP42).';
  END IF;

  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER observation_is_well_formed
  BEFORE INSERT OR UPDATE ON read.observation
  FOR EACH ROW EXECUTE FUNCTION core.observation_is_well_formed();

-- ---------------------------------------------------------------------------
-- The projection
-- ---------------------------------------------------------------------------

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
    event_id, global_seq, note)
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
    coalesce(p->>'note', ''))
  ON CONFLICT (event_id) DO NOTHING;

  -- A correction or a repeat: the earlier row stops being the value and says which row took
  -- its place. Never deleted — CP35's rule, and the reason a correction is answerable.
  IF v_replaces IS NOT NULL THEN
    UPDATE read.observation
       SET status = coalesce(nullif(p->>'replaced_status', ''), 'CORRECTED'),
           replaced_by = (p->>'observation_id')::uuid
     WHERE id = v_replaces AND status = 'ACTIVE';
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION read.apply_observation(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.apply_observation(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_measurements_carry_their_units() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  -- Criterion 1, as a standing check. The trigger prevents it going in; this catches the
  -- day somebody drops the trigger.
  SELECT count(*) INTO offending
    FROM read.observation o
    JOIN core.observation_code c ON c.code = o.code
   WHERE c.dimension IS NOT NULL
     AND (o.unit IS NULL OR o.value_num IS NULL
          OR o.entered_unit IS NULL OR o.entered_num IS NULL
          OR o.unit <> (SELECT code FROM core.unit
                         WHERE dimension = c.dimension AND is_canonical));

  IF offending > 0 THEN
    RAISE EXCEPTION 'measurements stored without a valid unit: % row(s)', offending
      USING HINT = 'A unit-bearing observation carries the canonical unit, the canonical '
                   'value, and the value and unit it was entered in. A weight of 154 with '
                   'no unit is 154 kg or 154 lb depending on who reads it (CP42).';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_units_convert_both_ways() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  bad text;
BEGIN
  -- Criterion 2, checked against the table itself rather than against a fixture: every unit
  -- must survive a round trip through both conversion functions within the documented
  -- tolerance. `numeric` makes this exact for every factor in the table today; the check
  -- exists so that adding a unit with a repeating factor is caught here rather than in a
  -- patient's weight three months later.
  SELECT string_agg(code, ', ') INTO bad
    FROM core.unit
   WHERE abs(core.from_canonical(core.to_canonical(1234.5, code), code) - 1234.5) > 0.000001;

  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'units that do not round-trip: %', bad
      USING HINT = 'to_canonical and from_canonical must be inverses within 1e-6. A unit '
                   'that loses precision in a round trip loses it in a patient''s record '
                   '(CP42).';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_measurements_carry_their_units() IS
  'Raises if any unit-bearing observation lacks its unit or is not stored canonically (CP42).';
COMMENT ON FUNCTION core.assert_units_convert_both_ways() IS
  'Raises if any unit fails to round-trip through the conversion functions within 1e-6 (CP42).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_measurements_carry_their_units',
   'every unit-bearing observation carries its unit and is stored canonically', 43),
  ('assert_units_convert_both_ways',
   'every unit round-trips through the conversion functions', 44)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name IN ('unit', 'observation_code');
DELETE FROM ops.invariant WHERE function_name IN
  ('assert_measurements_carry_their_units', 'assert_units_convert_both_ways');
DROP FUNCTION IF EXISTS core.assert_units_convert_both_ways();
DROP FUNCTION IF EXISTS core.assert_measurements_carry_their_units();
DROP FUNCTION IF EXISTS read.apply_observation(jsonb);
DROP TABLE IF EXISTS read.observation;
DROP FUNCTION IF EXISTS core.observation_is_well_formed();
DROP FUNCTION IF EXISTS core.from_canonical(numeric, text);
DROP FUNCTION IF EXISTS core.to_canonical(numeric, text);
DROP TRIGGER IF EXISTS observation_code_dimension_exists ON core.observation_code;
DROP FUNCTION IF EXISTS core.observation_code_dimension_exists();
DROP TABLE IF EXISTS core.observation_code;
DROP TABLE IF EXISTS core.unit;
