-- Attribution on every clinical record (CP61, §4.2, [R-03]).
--
-- §4.2 asks that a reviewer see who entered a value "instantly, without digging". Three read
-- models were dropping half the answer on the floor: an observation records the device and the
-- station it was typed at, and a history item, an allergy and an allergy assertion did not —
-- although the event behind every one of them carries both in its envelope.
--
-- That asymmetry is not a design; it is what happens when each checkpoint writes the columns its
-- own screen happened to need. The failure it produces is small and permanent: "which tablet
-- recorded this penicillin allergy" is answerable for a weight and unanswerable for the allergy,
-- and nobody notices until the question is asked about a device that turned out to be shared.
--
-- The columns are nullable and are filled from the envelope by the projection. Rows written
-- before this migration keep a null device and an empty station — honest, and visibly different
-- from a row that says it was typed at the counselling station.

-- +goose Up

ALTER TABLE read.history_item
  ADD COLUMN IF NOT EXISTS device_id uuid,
  ADD COLUMN IF NOT EXISTS station_code text NOT NULL DEFAULT '',
  -- How the record reached the server (§7.2): a station entry, an OCR read of a paper the
  -- patient brought, a field worker's phone. CP61's criterion 3 is that an OCR-sourced value is
  -- visibly different from one somebody typed, and a screen cannot draw a distinction the
  -- record does not carry.
  ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT '';

ALTER TABLE read.allergy
  ADD COLUMN IF NOT EXISTS device_id uuid,
  ADD COLUMN IF NOT EXISTS station_code text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT '';

ALTER TABLE read.allergy_assertion
  ADD COLUMN IF NOT EXISTS device_id uuid,
  ADD COLUMN IF NOT EXISTS station_code text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT '';

ALTER TABLE read.counseling_tick
  ADD COLUMN IF NOT EXISTS device_id uuid,
  ADD COLUMN IF NOT EXISTS station_code text NOT NULL DEFAULT '';

-- ---------------------------------------------------------------------------
-- The projections, extended
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_history_item_recorded(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.history_item (
    id, facility_id, patient_id, kind,
    code_system, code_version, code, said,
    relation, duration_days, severity, onset_on, onset_precision,
    dose, frequency, formulary_product_id, reconciliation, status,
    recorded_at, recorded_by, recorded_role, recorded_visit,
    device_id, station_code, source,
    event_id, global_seq)
  VALUES (
    (p->>'item_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    p->>'kind',
    nullif(p->>'code_system', ''),
    nullif(p->>'code_version', ''),
    nullif(p->>'code', ''),
    coalesce(p->>'said', ''),
    nullif(p->>'relation', ''),
    nullif(p->>'duration_days', '')::int,
    nullif(p->>'severity', ''),
    nullif(p->>'onset_on', '')::date,
    nullif(p->>'onset_precision', ''),
    coalesce(p->>'dose', ''),
    coalesce(p->>'frequency', ''),
    nullif(p->>'formulary_product_id', '')::uuid,
    nullif(p->>'reconciliation', ''),
    'ACTIVE',
    (p->>'recorded_at')::timestamptz,
    (p->>'recorded_by')::uuid,
    coalesce(p->>'recorded_role', ''),
    nullif(p->>'visit_id', '')::uuid,
    nullif(p->>'device_id', '')::uuid,
    coalesce(p->>'station_code', ''),
    coalesce(p->>'source', ''),
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (id) DO NOTHING;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_allergy_recorded(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.allergy (
    id, facility_id, patient_id,
    code_system, code_version, code, said,
    reaction, severity, certainty, note,
    recorded_at, recorded_by, recorded_role, recorded_visit,
    device_id, station_code, source,
    event_id, global_seq)
  VALUES (
    (p->>'allergy_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    nullif(p->>'code_system', ''),
    nullif(p->>'code_version', ''),
    nullif(p->>'code', ''),
    coalesce(p->>'said', ''),
    p->>'reaction',
    p->>'severity',
    p->>'certainty',
    coalesce(p->>'note', ''),
    (p->>'recorded_at')::timestamptz,
    (p->>'recorded_by')::uuid,
    coalesce(p->>'recorded_role', ''),
    nullif(p->>'visit_id', '')::uuid,
    nullif(p->>'device_id', '')::uuid,
    coalesce(p->>'station_code', ''),
    coalesce(p->>'source', ''),
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (event_id) DO NOTHING;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_allergy_status_asserted(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.allergy_assertion (
    id, facility_id, patient_id, kind, reason,
    asserted_at, asserted_by, asserted_role, asserted_visit,
    device_id, station_code, source,
    event_id, global_seq)
  VALUES (
    (p->>'assertion_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    p->>'kind',
    coalesce(p->>'reason', ''),
    (p->>'asserted_at')::timestamptz,
    (p->>'asserted_by')::uuid,
    coalesce(p->>'asserted_role', ''),
    nullif(p->>'visit_id', '')::uuid,
    nullif(p->>'device_id', '')::uuid,
    coalesce(p->>'station_code', ''),
    coalesce(p->>'source', ''),
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (event_id) DO NOTHING;

  -- One live assertion per patient. Unchanged from 00036: a new one supersedes the old rather
  -- than sitting beside it, because "no known allergies" and "we could not ask" cannot both be
  -- the current answer.
  UPDATE read.allergy_assertion
     SET withdrawn_at     = (p->>'asserted_at')::timestamptz,
         withdrawn_by     = (p->>'asserted_by')::uuid,
         withdrawn_reason = 'superseded'
   WHERE patient_id = (p->>'patient_id')::uuid
     AND id <> (p->>'assertion_id')::uuid
     AND withdrawn_at IS NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_counseling_item_ticked(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  -- Unchanged from 00038 but for the two attribution columns: a re-tick after an un-tick lands
  -- on the same row, and the undo count remembers that it went round once.
  INSERT INTO read.counseling_tick (
    session_id, item_code, facility_id, patient_id,
    ticked_at, ticked_by, ticked_role, note, device_id, station_code, event_id, global_seq)
  VALUES (
    (p->>'session_id')::uuid,
    p->>'item_code',
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    (p->>'ticked_at')::timestamptz,
    (p->>'ticked_by')::uuid,
    coalesce(p->>'ticked_role', ''),
    coalesce(p->>'note', ''),
    nullif(p->>'device_id', '')::uuid,
    coalesce(p->>'station_code', ''),
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (session_id, item_code) DO UPDATE SET
    ticked_at     = EXCLUDED.ticked_at,
    ticked_by     = EXCLUDED.ticked_by,
    ticked_role   = EXCLUDED.ticked_role,
    note          = EXCLUDED.note,
    device_id     = EXCLUDED.device_id,
    station_code  = EXCLUDED.station_code,
    undone_at     = NULL,
    undone_by     = NULL,
    undone_reason = '',
    event_id      = EXCLUDED.event_id,
    global_seq    = EXCLUDED.global_seq
  WHERE read.counseling_tick.global_seq < EXCLUDED.global_seq;
END
$$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- The invariant
-- ---------------------------------------------------------------------------

-- §4.2 in one sentence, standing. Every clinical record names a person; this is what notices a
-- projection that stopped filling one in — which is exactly the failure this migration was
-- written to correct, and it went unnoticed for three checkpoints.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_clinical_record_is_attributed() RETURNS void
LANGUAGE plpgsql
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  offenders bigint;
BEGIN
  SELECT (SELECT count(*) FROM read.observation WHERE recorded_by IS NULL)
       + (SELECT count(*) FROM read.history_item WHERE recorded_by IS NULL)
       + (SELECT count(*) FROM read.allergy WHERE recorded_by IS NULL)
       + (SELECT count(*) FROM read.allergy_assertion WHERE asserted_by IS NULL)
    INTO offenders;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% clinical records name nobody', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_clinical_record_is_attributed',
   'every clinical record names the person who entered it', 75)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name = 'assert_every_clinical_record_is_attributed';
DROP FUNCTION IF EXISTS core.assert_every_clinical_record_is_attributed();

ALTER TABLE read.counseling_tick
  DROP COLUMN IF EXISTS station_code,
  DROP COLUMN IF EXISTS device_id;

ALTER TABLE read.allergy_assertion
  DROP COLUMN IF EXISTS source,
  DROP COLUMN IF EXISTS station_code,
  DROP COLUMN IF EXISTS device_id;

ALTER TABLE read.allergy
  DROP COLUMN IF EXISTS source,
  DROP COLUMN IF EXISTS station_code,
  DROP COLUMN IF EXISTS device_id;

ALTER TABLE read.history_item
  DROP COLUMN IF EXISTS source,
  DROP COLUMN IF EXISTS station_code,
  DROP COLUMN IF EXISTS device_id;
