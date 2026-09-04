-- Station 2's remaining codes (CP45, blueprint §3 step 2, §5.2).
--
-- Two codes and no new machinery, which is the point of CP42's registry: a station becomes
-- real by adding rows, not by adding tables. What is here is the pair the anthropometry
-- screen needs and the observation registry did not yet have — the muscle mass a body
-- composition scale reports, and the ideal body weight the screen shows beside it.
--
-- Ideal body weight is a DERIVED code rather than something an operator types, and the
-- distinction is the whole of CP43: nobody measures an ideal weight, so a record that let
-- one be typed would be a record where a clinical number could arrive with no formula behind
-- it. The trigger from 00027 already refuses that; this row is what makes the value
-- storable at all.

-- +goose Up

INSERT INTO core.observation_code
  (code, category, value_type, dimension, loinc, display_en, display_bn,
   min_canonical, max_canonical, write_permission) VALUES

  -- Reported by the bioimpedance scale, or left blank when the clinic is using a plain
  -- balance. The plan's open decision — which scale, and whether it can be read
  -- electronically — is about how this value *arrives*, not about what it is, so the code
  -- exists either way and the screen simply offers a field.
  ('MUSCLE_MASS', 'ANTHRO', 'numeric', 'mass', '73964-9',
   'Muscle mass', 'পেশির ভর', 1, 120, 'observation.write.anthro'),

  -- Devine. A *dosing* weight by origin, which is worth remembering when it appears on a
  -- screen beside a patient: it is not a target, and CP60's nutrition plan does not compute
  -- from it. The band is wide because the formula is defined from 120 cm upward and a very
  -- tall patient is not an error.
  ('IBW', 'DERIVED', 'numeric', 'mass', '',
   'Ideal body weight (Devine)', 'আদর্শ ওজন (ডিভাইন)', 20, 200, 'observation.write.anthro')

ON CONFLICT (code) DO UPDATE SET
  category = EXCLUDED.category, value_type = EXCLUDED.value_type,
  dimension = EXCLUDED.dimension, loinc = EXCLUDED.loinc,
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  min_canonical = EXCLUDED.min_canonical, max_canonical = EXCLUDED.max_canonical,
  write_permission = EXCLUDED.write_permission;

-- Every code a station screen offers has to be enterable in a unit the operator's instrument
-- actually reads in. The registry already guarantees a code's dimension exists; this says
-- something narrower and more practical — that an ANTHRO or VITAL code has at least one
-- *alternative* to the canonical unit, or is on a list of codes where there genuinely is
-- only one unit in use.
--
-- The failure it prevents is small and constant: a scale that reads in pounds, an operator
-- who has to convert in their head, and a weight that is wrong by a factor of 2.2 twice a
-- year. CP44 made the display dual-unit; this makes the *entry* dual-unit, and makes it a
-- property of the registry rather than of whoever built the screen.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_stations_can_enter_what_they_measure() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  -- Codes measured in exactly one unit anywhere in this clinic. Each is here because the
  -- instrument has no other scale, not because nobody got around to adding one.
  single_unit text[] := ARRAY[
    'BODY_FAT_PCT',   -- a body composition scale reads per cent, and nothing else
    'SPO2',           -- likewise a pulse oximeter
    'HEART_RATE',     -- beats per minute
    'RESP_RATE'       -- breaths per minute
  ];
  offender text;
BEGIN
  SELECT c.code INTO offender
  FROM core.observation_code c
  WHERE c.category IN ('ANTHRO', 'VITAL')
    AND c.value_type = 'numeric'
    AND c.dimension IS NOT NULL
    AND c.retired_at IS NULL
    AND NOT (c.code = ANY(single_unit))
    AND (SELECT count(*) FROM core.unit u WHERE u.dimension = c.dimension) < 2
  LIMIT 1;

  IF offender IS NOT NULL THEN
    RAISE EXCEPTION '% can only be entered in one unit', offender
      USING HINT = 'A station screen with no unit selector is an operator converting in '
                   'their head. Add the unit the instrument reads in, or add the code to '
                   'the single_unit list in this function and say why.';
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_stations_can_enter_what_they_measure() IS
  'Raises if a measured code offers no alternative to its canonical unit (CP45).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_stations_can_enter_what_they_measure',
   'every measured code can be entered in the unit the instrument reads in', 46)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name = 'assert_stations_can_enter_what_they_measure';
DROP FUNCTION IF EXISTS core.assert_stations_can_enter_what_they_measure();
DELETE FROM core.observation_code WHERE code IN ('MUSCLE_MASS', 'IBW');
