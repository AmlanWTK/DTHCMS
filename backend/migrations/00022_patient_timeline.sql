-- The patient timeline (CP37, blueprint §8).
--
-- One chronological read model that everything downstream reads instead of writing its own
-- query: the physician dashboard (CP73), the timeline visualisation (CP74), the AI synthesis
-- (CP71) and the records chronology (CP107). Four divergent queries over the ledger is four
-- places for a fact to be missing from one of them, and the one that is missing is always the
-- one somebody is looking at.
--
-- Three decisions are structural, and the plan's own risk note is the reason for the first.
--
--   **A uniform row, extensible by design.** `occurred_at, kind, label, value, unit,
--   attribution, flags` — the same shape for an observation, a diagnosis, a prescription, a
--   visit, a document and a message. New kinds are rows, not columns. The alternative is a
--   table that gains three columns per checkpoint and a query that has to know all of them.
--
--   **Attribution on every row, never joined in.** §8's hover-to-see-who has to work
--   everywhere, and a timeline row whose author is resolved by a join is a row that loses its
--   author when the join is expensive or when the user is deactivated. Who did it, in which
--   role, at which station, on which device, denormalised.
--
--   **`event_id` is the identity.** Exactly one row per (event, item), so a replay produces
--   the timeline it produced before and a re-delivered event does not double an entry. That
--   is acceptance criteria 1 and 4 held by a unique index rather than by care.

-- +goose Up

CREATE TABLE read.patient_timeline (
  id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  patient_id  uuid        NOT NULL,
  facility_id uuid        NOT NULL,

  -- When it happened in the world, not when it was recorded. A vital taken at 09:10 and
  -- entered at 11:40 belongs at 09:10; the difference is itself worth seeing, so both are
  -- kept.
  occurred_at timestamptz NOT NULL,
  recorded_at timestamptz NOT NULL,

  -- The kind, and the family it belongs to. `category` is what a filter offers a clinician
  -- ("show me only medication"); `kind` is what the row actually is. A closed list on
  -- category and an open one on kind: a new observation type must not need a migration, and
  -- a new *category* should be a decision.
  category    text        NOT NULL CHECK (category IN (
                'registration', 'visit', 'observation', 'diagnosis', 'medication',
                'document', 'communication', 'alert', 'consent', 'administrative')),
  kind        text        NOT NULL,

  -- What a person reads. Rendered at projection time in both languages, because a timeline
  -- assembled at read time in the reader's language is a timeline whose rows change meaning
  -- when the interface language changes — and because the physician and the counsellor
  -- looking at the same screen may be reading different ones.
  label_en    text        NOT NULL,
  label_bn    text        NOT NULL DEFAULT '',
  -- The value as it should be shown, and its unit. Text, deliberately: a timeline holds a
  -- blood pressure, a drug name and a document title, and a numeric column would hold one
  -- of them.
  value       text        NOT NULL DEFAULT '',
  unit        text        NOT NULL DEFAULT '',
  -- The numeric value when there is one, for a chart that plots the row rather than reading
  -- it. Null for everything that is not a measurement.
  value_num   numeric,

  -- Attribution, denormalised (§8).
  actor_id      uuid,
  actor_code    text      NOT NULL DEFAULT '',
  actor_role    text      NOT NULL DEFAULT '',
  actor_station text      NOT NULL DEFAULT '',
  device_id     uuid,
  source        text      NOT NULL DEFAULT '',

  -- Flags a screen acts on: 'critical', 'corrected', 'amended', 'high', 'low'. An array
  -- rather than columns, because the list grows and a row usually has none.
  flags       text[]      NOT NULL DEFAULT '{}',

  -- What produced this row, and where in the ledger to look.
  event_id    uuid        NOT NULL,
  event_type  text        NOT NULL,
  global_seq  bigint      NOT NULL,
  -- One event can produce several rows — a blood pressure is systolic and diastolic, a
  -- prescription is several drugs. `item` distinguishes them and is '' for the common case.
  item        text        NOT NULL DEFAULT '',

  -- The permission a reader needs to see this row. Carried per row so RBAC filtering is a
  -- WHERE clause rather than a post-filter in Go — a post-filter is how a count comes back
  -- larger than the rows returned, and how a paging cursor skips what it hid.
  needs_permission text   NOT NULL DEFAULT 'patient.read.demographics',

  -- Whether this row is still current. A corrected observation supersedes an earlier one;
  -- neither is deleted, and a timeline showing only the current values is the default while
  -- the superseded ones remain askable for.
  superseded_by uuid,

  CONSTRAINT patient_timeline_once UNIQUE (event_id, item)
);

-- The query this table exists for: one patient, a date range, newest first.
CREATE INDEX read_patient_timeline_range
  ON read.patient_timeline (patient_id, occurred_at DESC, id DESC);
-- Filtering by category inside that range, which is what a "medication only" toggle does.
CREATE INDEX read_patient_timeline_category
  ON read.patient_timeline (patient_id, category, occurred_at DESC);
-- The facility scope, for anything that counts across patients.
CREATE INDEX read_patient_timeline_facility
  ON read.patient_timeline (facility_id, occurred_at DESC);
-- Replay and rebuild walk by sequence.
CREATE INDEX read_patient_timeline_seq ON read.patient_timeline (global_seq);

COMMENT ON TABLE read.patient_timeline IS
  'Everything known about a patient, in one chronological shape, attributed per row (CP37, §8).';
COMMENT ON COLUMN read.patient_timeline.needs_permission IS
  'The permission a reader needs. Filtering happens in SQL so a hidden row cannot skew a count or a cursor.';

-- ---------------------------------------------------------------------------
-- The derivation
-- ---------------------------------------------------------------------------

-- A single entry point taking an array of rows, because one event can produce several and
-- they must appear together or not at all.
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

REVOKE EXECUTE ON FUNCTION read.apply_timeline(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.apply_timeline(jsonb) TO dthcms_app, dthcms_projector;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.reset_patient_timeline()
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, pg_catalog
AS $$
BEGIN
  DELETE FROM read.patient_timeline;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION read.reset_patient_timeline() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.reset_patient_timeline() TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariant
-- ---------------------------------------------------------------------------

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

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_timeline_rows_are_attributed', 'every timeline row says who recorded it and what it says', 37)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name = 'assert_timeline_rows_are_attributed';
DROP FUNCTION IF EXISTS core.assert_timeline_rows_are_attributed();
DROP FUNCTION IF EXISTS read.reset_patient_timeline();
DROP FUNCTION IF EXISTS read.apply_timeline(jsonb);
DROP TABLE IF EXISTS read.patient_timeline;
