-- The timeline learns to render clinical rows (CP37 v2, feeding CP74).
--
-- CP37 shipped a derivation that knew registration, corrections, merges, photographs and
-- consent — five things, all of them administrative. Everything a physician actually opens a
-- timeline to see (a weight, a visit, a critical value, a diet recall) arrived in the
-- checkpoints since, and none of it reached `read.patient_timeline`. On the loaded synthetic
-- cohort that is 62 rows out of 6,760 events: a timeline screen built on it would be blank.
--
-- Adding those kinds needs one thing from the database, and this migration is that thing.
--
-- # Why the labels are resolved here and not in Go
--
-- Every new clinical row needs a name a person reads, in **both** languages, and those names
-- already exist: `core.observation_code.display_en/display_bn`, `core.station.name_en/name_bn`,
-- `core.food`, `core.food_measure`, `core.meal`. Copying them into a Go map would be a second
-- copy of a bilingual clinical vocabulary that Dr. Nahid reviews in one place — and the failure
-- mode of a second copy is a Bangla label that is right in the picker and stale on the timeline,
-- which nobody notices because the two screens are never open together.
--
-- So the derivation hands `apply_timeline` a **label with holes in it** — `Arrived at {1}` — and a
-- list of codes to fill them from. The same pattern the function already uses for `actor_code`:
-- the ledger holds the durable fact (the code), and the rendering is looked up at derivation
-- time, so a corrected translation corrects the whole history on the next rebuild.
--
-- # And why the conversion is here
--
-- ADR-0017: a projection rebuild and a live write must go through the same conversion code.
-- `core.to_canonical` is that code. A timeline row whose `value_num` was converted in Go would
-- be a second implementation of the unit table, and the day the two disagree is the day a chart
-- and a value list show different numbers for the same measurement.
--
-- An unknown code renders as itself rather than as blank. `core.assert_timeline_rows_are_attributed()`
-- refuses a blank label, and a code on a screen is a bad label while a blank one is a bug report.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Bilingual display for a code, from whichever registry owns it
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.timeline_display(
  p_kind text, p_code text, p_facility uuid DEFAULT NULL,
  OUT name_en text, OUT name_bn text)
LANGUAGE plpgsql STABLE
AS $$
BEGIN
  IF p_code IS NULL OR btrim(p_code) = '' THEN
    name_en := ''; name_bn := '';
    RETURN;
  END IF;

  IF p_kind = 'observation_code' THEN
    SELECT oc.display_en, oc.display_bn INTO name_en, name_bn
      FROM core.observation_code oc WHERE oc.code = p_code;

  ELSIF p_kind = 'station' THEN
    -- Station codes are unique per facility. The row's own facility first; any facility as a
    -- fallback, because a station renamed out of one clinic is still what the row means.
    SELECT s.name_en, s.name_bn INTO name_en, name_bn
      FROM core.station s
     WHERE s.code = p_code
     ORDER BY (s.facility_id = p_facility) DESC, s.sequence_hint
     LIMIT 1;

  ELSIF p_kind = 'food' THEN
    SELECT f.name_en, f.name_bn INTO name_en, name_bn
      FROM core.food f WHERE f.code = p_code;

  ELSIF p_kind = 'food_measure' THEN
    SELECT m.name_en, m.name_bn INTO name_en, name_bn
      FROM core.food_measure m WHERE m.code = p_code;

  ELSIF p_kind = 'meal' THEN
    SELECT m.name_en, m.name_bn INTO name_en, name_bn
      FROM core.meal m WHERE m.code = p_code;

  ELSIF p_kind = 'unit' THEN
    SELECT u.display_en, u.display_bn INTO name_en, name_bn
      FROM core.unit u WHERE u.code = p_code;
  END IF;

  -- A code nobody has a name for renders as the code. The alternative is an empty cell on a
  -- clinician's screen, and the attribution invariant refuses an empty label anyway.
  IF name_en IS NULL OR btrim(name_en) = '' THEN name_en := p_code; END IF;
  IF name_bn IS NULL OR btrim(name_bn) = '' THEN name_bn := name_en; END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION read.timeline_display(text, text, uuid) IS
  'The bilingual name of a code, from whichever registry owns it. Falls back to the code, never to blank (CP37).';

REVOKE EXECUTE ON FUNCTION read.timeline_display(text, text, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.timeline_display(text, text, uuid) TO dthcms_app, dthcms_projector;

-- ---------------------------------------------------------------------------
-- The derivation, extended
-- ---------------------------------------------------------------------------

-- Three optional keys are new on a row, and everything CP37 wrote still means what it meant:
--
--   `lookups`      [{kind, code}, …]  fills `{1}`, `{2}`, … in label_en and label_bn
--   `obs_code` +
--   `entered_num` +
--   `entered_unit`                    canonical conversion: value_num, unit, and the shown value
--   `unit_code` + `unit_kind`         a unit's display, for a row that carries its own number
--
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_timeline(rows jsonb)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  v_row      jsonb;
  v_lookup   jsonb;
  v_slot     integer;
  v_name_en  text;
  v_name_bn  text;
  v_label_en text;
  v_label_bn text;
  v_value    text;
  v_unit     text;
  v_num      numeric;
  v_entered  numeric;
  v_in_unit  text;
  v_dimension text;
  v_canonical core.unit;
BEGIN
  FOR v_row IN SELECT * FROM jsonb_array_elements(coalesce(rows, '[]'::jsonb))
  LOOP
    v_label_en := coalesce(v_row ->> 'label_en', '');
    v_label_bn := coalesce(v_row ->> 'label_bn', '');
    v_value    := coalesce(v_row ->> 'value', '');
    v_unit     := coalesce(v_row ->> 'unit', '');
    v_num      := nullif(v_row ->> 'value_num', '')::numeric;

    -- 1. Fill the holes in the label from the registries that own the names.
    v_slot := 1;
    FOR v_lookup IN SELECT * FROM jsonb_array_elements(coalesce(v_row -> 'lookups', '[]'::jsonb))
    LOOP
      SELECT d.name_en, d.name_bn INTO v_name_en, v_name_bn
        FROM read.timeline_display(v_lookup ->> 'kind', v_lookup ->> 'code',
                                   nullif(v_row ->> 'facility_id', '')::uuid) d;
      v_label_en := replace(v_label_en, '{' || v_slot || '}', v_name_en);
      v_label_bn := replace(v_label_bn, '{' || v_slot || '}', v_name_bn);
      v_slot := v_slot + 1;
    END LOOP;

    -- 2. A measured value, converted by the same function the write path uses (ADR-0017).
    v_entered := nullif(v_row ->> 'entered_num', '')::numeric;
    v_in_unit := nullif(v_row ->> 'entered_unit', '');
    IF v_entered IS NOT NULL AND (v_row ? 'obs_code') THEN
      SELECT oc.dimension INTO v_dimension
        FROM core.observation_code oc WHERE oc.code = v_row ->> 'obs_code';

      SELECT * INTO v_canonical FROM core.unit
       WHERE dimension = v_dimension AND is_canonical;

      IF v_canonical.code IS NULL
         OR v_in_unit IS NULL
         OR NOT EXISTS (SELECT 1 FROM core.unit WHERE code = v_in_unit) THEN
        -- Unitless (a count, a ratio recorded without one), or a unit this deployment has
        -- never heard of. Converting either would be inventing a factor, so the number is
        -- carried as it was entered and the row says which unit that was.
        v_num  := v_entered;
        v_unit := coalesce(v_in_unit, '');
      ELSE
        v_num  := core.to_canonical(v_entered, v_in_unit);
        v_unit := v_canonical.display_en;
        -- `value` is rounded to the unit's own display precision — a weight in kg is 69.9 and
        -- the same weight in grams is not 69850.0 — and `value_num` deliberately is **not**.
        -- One is a cell a person reads and the other is what a chart does arithmetic on, and
        -- rounding the second would put a rounding step between the ledger and every trend
        -- drawn from it. `read.observation` makes the same split for the same reason.
        v_value := round(v_num, v_canonical.decimals)::text;
      END IF;
    ELSIF nullif(v_row ->> 'unit_code', '') IS NOT NULL THEN
      -- A row that carries its own number and names the registry its unit lives in: a unit
      -- code for a critical value, a household measure for a diet recall.
      SELECT d.name_en INTO v_unit
        FROM read.timeline_display(coalesce(nullif(v_row ->> 'unit_kind', ''), 'unit'),
                                   v_row ->> 'unit_code', NULL) d;
    END IF;

    INSERT INTO read.patient_timeline (
      patient_id, facility_id, occurred_at, recorded_at, category, kind,
      label_en, label_bn, value, unit, value_num,
      actor_id, actor_code, actor_role, actor_station, device_id, source,
      flags, event_id, event_type, global_seq, item, needs_permission)
    VALUES (
      (v_row ->> 'patient_id')::uuid, (v_row ->> 'facility_id')::uuid,
      (v_row ->> 'occurred_at')::timestamptz, (v_row ->> 'recorded_at')::timestamptz,
      v_row ->> 'category', v_row ->> 'kind',
      v_label_en, v_label_bn, v_value, v_unit, v_num,
      nullif(v_row ->> 'actor_id', '')::uuid,
      -- The employee code, resolved here rather than carried in the event. The ledger holds
      -- the user id — that is the durable fact — and the code is a rendering of it, so a
      -- rebuild looks it up rather than replaying a string that may since have changed.
      coalesce((SELECT employee_code FROM core.app_user
                 WHERE id = nullif(v_row ->> 'actor_id', '')::uuid), ''),
      coalesce(v_row ->> 'actor_role', ''), coalesce(v_row ->> 'actor_station', ''),
      nullif(v_row ->> 'device_id', '')::uuid, coalesce(v_row ->> 'source', ''),
      coalesce(ARRAY(SELECT jsonb_array_elements_text(v_row -> 'flags')), '{}'::text[]),
      (v_row ->> 'event_id')::uuid, v_row ->> 'event_type', (v_row ->> 'global_seq')::bigint,
      coalesce(v_row ->> 'item', ''),
      coalesce(v_row ->> 'needs_permission', 'patient.read.demographics'))
    -- Idempotent by identity, not by care. A replayed event produces the same rows, and a
    -- re-delivered one does not double an entry (criteria 1 and 4).
    ON CONFLICT (event_id, item) DO UPDATE SET
      occurred_at = excluded.occurred_at,
      recorded_at = excluded.recorded_at,
      category    = excluded.category,
      kind        = excluded.kind,
      label_en    = excluded.label_en,
      label_bn    = excluded.label_bn,
      value       = excluded.value,
      unit        = excluded.unit,
      value_num   = excluded.value_num,
      flags       = excluded.flags,
      -- The permission a row needs is part of the derivation, not part of the row's history.
      -- Left out of this list, a row derived under CP37's rules would keep
      -- `patient.read.demographics` for ever — which is how an observation value becomes
      -- readable by a registration clerk after a re-delivery.
      needs_permission = excluded.needs_permission,
      global_seq  = excluded.global_seq;
  END LOOP;

  UPDATE read.projection_state
     SET checkpoint = GREATEST(checkpoint,
           coalesce((SELECT max((r ->> 'global_seq')::bigint)
                       FROM jsonb_array_elements(coalesce(rows, '[]'::jsonb)) r), checkpoint)),
         updated_at = now()
   WHERE name = 'patient_timeline';
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION read.apply_timeline(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.apply_timeline(jsonb) TO dthcms_app, dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariant, tightened
-- ---------------------------------------------------------------------------

-- CP37's invariant refuses a row with no actor and no English label. It said nothing about
-- the Bangla one, because at CP37 every label was written by hand a few lines apart and the
-- gap was visible. It no longer is: most labels are now assembled from a registry lookup, and
-- a registry row with an empty `display_bn` would produce a timeline that is silently
-- monolingual for exactly the codes nobody checked.
--
-- The clinic's floor staff read Bangla. A blank Bangla label is not a cosmetic defect there.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_timeline_rows_are_attributed() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  -- §8's hover-to-see-who has to work everywhere. A row with no actor is a row that answers
  -- "who recorded this" with a shrug, and the question is asked about the rows that matter.
  SELECT count(*) INTO offending
  FROM read.patient_timeline
  WHERE actor_id IS NULL OR btrim(actor_code) = ''
     OR btrim(label_en) = '' OR btrim(label_bn) = '';

  IF offending > 0 THEN
    RAISE EXCEPTION 'timeline rows with no attribution or no label: % row(s)', offending
      USING HINT = 'Every timeline row carries who recorded it, in which role, and what it '
                   'says — in both languages. Attribution resolved by a join is attribution '
                   'that disappears when the join is expensive (CP37, §8).';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_timeline_rows_are_attributed() IS
  'Raises if a timeline row has no actor or is missing a label in either language (CP37, §8).';

UPDATE ops.invariant
   SET description = 'every timeline row says who recorded it and what it says, in both languages'
 WHERE function_name = 'assert_timeline_rows_are_attributed';

-- +goose Down

-- Back to CP37's derivation, byte for byte — `TestRollbackUndoesTheLastMigration` compares
-- function bodies, so a down that restores an earlier definition *nearly* right fails.
--
-- `read.patient_timeline` itself is untouched by this migration: it is derived, and the honest
-- repair after a rollback is a rebuild, not an UPDATE over a decade of somebody's history.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_timeline(rows jsonb)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, pg_catalog
AS $$
DECLARE
  v_row jsonb;
BEGIN
  FOR v_row IN SELECT * FROM jsonb_array_elements(coalesce(rows, '[]'::jsonb))
  LOOP
    INSERT INTO read.patient_timeline (
      patient_id, facility_id, occurred_at, recorded_at, category, kind,
      label_en, label_bn, value, unit, value_num,
      actor_id, actor_code, actor_role, actor_station, device_id, source,
      flags, event_id, event_type, global_seq, item, needs_permission)
    VALUES (
      (v_row ->> 'patient_id')::uuid, (v_row ->> 'facility_id')::uuid,
      (v_row ->> 'occurred_at')::timestamptz, (v_row ->> 'recorded_at')::timestamptz,
      v_row ->> 'category', v_row ->> 'kind',
      v_row ->> 'label_en', coalesce(v_row ->> 'label_bn', ''),
      coalesce(v_row ->> 'value', ''), coalesce(v_row ->> 'unit', ''),
      nullif(v_row ->> 'value_num', '')::numeric,
      nullif(v_row ->> 'actor_id', '')::uuid,
      -- The employee code, resolved here rather than carried in the event. The ledger holds
      -- the user id — that is the durable fact — and the code is a rendering of it, so a
      -- rebuild looks it up rather than replaying a string that may since have changed.
      coalesce((SELECT employee_code FROM core.app_user
                 WHERE id = nullif(v_row ->> 'actor_id', '')::uuid), ''),
      coalesce(v_row ->> 'actor_role', ''), coalesce(v_row ->> 'actor_station', ''),
      nullif(v_row ->> 'device_id', '')::uuid, coalesce(v_row ->> 'source', ''),
      coalesce(ARRAY(SELECT jsonb_array_elements_text(v_row -> 'flags')), '{}'::text[]),
      (v_row ->> 'event_id')::uuid, v_row ->> 'event_type', (v_row ->> 'global_seq')::bigint,
      coalesce(v_row ->> 'item', ''),
      coalesce(v_row ->> 'needs_permission', 'patient.read.demographics'))
    -- Idempotent by identity, not by care. A replayed event produces the same rows, and a
    -- re-delivered one does not double an entry (criteria 1 and 4).
    ON CONFLICT (event_id, item) DO UPDATE SET
      occurred_at = excluded.occurred_at,
      recorded_at = excluded.recorded_at,
      category    = excluded.category,
      kind        = excluded.kind,
      label_en    = excluded.label_en,
      label_bn    = excluded.label_bn,
      value       = excluded.value,
      unit        = excluded.unit,
      value_num   = excluded.value_num,
      flags       = excluded.flags,
      global_seq  = excluded.global_seq;
  END LOOP;

  UPDATE read.projection_state
     SET checkpoint = GREATEST(checkpoint,
           coalesce((SELECT max((r ->> 'global_seq')::bigint)
                       FROM jsonb_array_elements(coalesce(rows, '[]'::jsonb)) r), checkpoint)),
         updated_at = now()
   WHERE name = 'patient_timeline';
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_timeline_rows_are_attributed() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  -- §8's hover-to-see-who has to work everywhere. A row with no actor is a row that answers
  -- "who recorded this" with a shrug, and the question is asked about the rows that matter.
  SELECT count(*) INTO offending
  FROM read.patient_timeline
  WHERE actor_id IS NULL OR btrim(actor_code) = '' OR btrim(label_en) = '';

  IF offending > 0 THEN
    RAISE EXCEPTION 'timeline rows with no attribution or no label: % row(s)', offending
      USING HINT = 'Every timeline row carries who recorded it, in which role, and what it '
                   'says. Attribution resolved by a join is attribution that disappears '
                   'when the join is expensive (CP37, §8).';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_timeline_rows_are_attributed() IS
  'Raises if a timeline row has no actor or no label (CP37, §8).';

UPDATE ops.invariant
   SET description = 'every timeline row says who recorded it and what it says'
 WHERE function_name = 'assert_timeline_rows_are_attributed';

DROP FUNCTION IF EXISTS read.timeline_display(text, text, uuid);
