-- Derived values carry the formula that produced them (CP43, blueprint §3, §6.4).
--
-- A BMI in a record is not a measurement. It is the output of an equation applied to two
-- measurements, and three facts about it have to survive: **which** equation, **which
-- version** of it, and **what it was given**.
--
-- The middle one is the one people forget, and it is the one that matters most. CKD-EPI was
-- revised in 2021 to remove a race coefficient that raised the estimate for Black patients
-- with no physiological justification and delayed referrals as a result. A system that
-- stored "eGFR 62" with no version could not afterwards tell which equation produced it, and
-- could not tell a clinician whether a value from 2019 was comparable with one from today.
-- A system that silently recomputed the old values with the new equation would be worse: it
-- would rewrite history.
--
-- So a DERIVED observation without a formula, a version and its inputs cannot be stored. Not
-- "is rejected by the API" — cannot, by the same trigger that enforces CP42's unit rule.

-- +goose Up

ALTER TABLE read.observation
  -- The formula's name, e.g. `egfr_ckd_epi_2021`. Matches a key of calc.Formulas() in Go and
  -- FORMULAS in @dthcms/clinical-calc — the two are held together by CP43's shared fixtures.
  ADD COLUMN formula text NOT NULL DEFAULT '',
  -- That formula's version at the moment this value was computed.
  ADD COLUMN formula_version text NOT NULL DEFAULT '',
  -- What it was given, in canonical units, as a flat object: {"weight_kg": 69.5, ...}.
  --
  -- Stored rather than re-derived from the patient's other observations, because the inputs
  -- are what the formula *actually saw*: a weight corrected an hour later does not change
  -- what the BMI was computed from, and a record that implied otherwise would be a record
  -- that could not explain its own numbers.
  ADD COLUMN inputs jsonb;

COMMENT ON COLUMN read.observation.formula IS
  'For a DERIVED value: which equation produced it. Empty for a measurement (CP43).';
COMMENT ON COLUMN read.observation.formula_version IS
  'The equation''s version at computation time. CKD-EPI has already changed once; a value without this cannot be interpreted later (CP43).';
COMMENT ON COLUMN read.observation.inputs IS
  'What the formula was given, in canonical units. What it *actually saw* — a later correction to an input does not change what this value was computed from (CP43).';

-- Criterion 3, in the same trigger that enforces the unit rule, so both are true of every
-- row however it arrived.
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

  -- CP43. A derived value with no formula is a number nobody can reproduce; one with no
  -- version is a number nobody can interpret two years from now.
  IF spec.category = 'DERIVED' THEN
    IF btrim(coalesce(NEW.formula, '')) = '' OR btrim(coalesce(NEW.formula_version, '')) = ''
       OR NEW.inputs IS NULL THEN
      RAISE EXCEPTION '% is a derived value and must say which formula produced it, which version, and from what', NEW.code
        USING HINT = 'CKD-EPI was revised in 2021 to remove its race coefficient. A stored '
                     'eGFR with no version cannot afterwards be told apart from one computed '
                     'under the old equation (CP43).';
    END IF;
  ELSIF btrim(coalesce(NEW.formula, '')) <> '' THEN
    RAISE EXCEPTION '% is a measurement, not a derived value, and names a formula', NEW.code;
  END IF;

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

-- The projection gains the three columns. Redefined whole rather than patched, because a
-- projection function that differs between the migration that created it and the one that
-- amended it is a function nobody can read in one sitting.
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
    formula, formula_version, inputs)
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
    CASE WHEN p ? 'inputs' THEN p->'inputs' END)
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_derived_values_name_their_formula() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  SELECT count(*) INTO offending
    FROM read.observation
   WHERE category = 'DERIVED'
     AND (btrim(coalesce(formula, '')) = ''
          OR btrim(coalesce(formula_version, '')) = ''
          OR inputs IS NULL);

  IF offending > 0 THEN
    RAISE EXCEPTION 'derived values with no formula, version or inputs: % row(s)', offending
      USING HINT = 'A derived value has to say which equation produced it, which version of '
                   'that equation, and what it was given. Without the version, a value '
                   'computed under a superseded equation is indistinguishable from a '
                   'current one (CP43).';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_derived_values_name_their_formula() IS
  'Raises if any DERIVED observation lacks its formula, version or inputs (CP43).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_derived_values_name_their_formula',
   'every derived value names the formula, the version and the inputs that produced it', 45)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name = 'assert_derived_values_name_their_formula';
DROP FUNCTION IF EXISTS core.assert_derived_values_name_their_formula();
ALTER TABLE read.observation
  DROP COLUMN IF EXISTS inputs,
  DROP COLUMN IF EXISTS formula_version,
  DROP COLUMN IF EXISTS formula;
