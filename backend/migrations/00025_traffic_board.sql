-- The Clinic Traffic Control board (CP40, blueprint §5.2).
--
-- The board is the operational nerve centre of a twelve-station clinic, and it is the one
-- screen in the building that **patients can read**. It hangs on a wall. People sitting in
-- the waiting area look at it for forty minutes. That single fact drives the whole design:
--
--   1. A board that *could* show a diagnosis will one day show one. Not because anyone
--      decides to, but because somebody adds a column to a query six months from now and
--      the reviewer is tired. So the board does not query `core.visit`. It queries
--      `core.board_row`, a view that exposes an explicit list of safe columns, and
--      `core.assert_the_board_shows_nothing_clinical()` fails the deployment if that list
--      ever grows. The safety property is a schema object, not a habit.
--
--   2. `core.visit` carries `diagnoses`, `plan` and `chief_complaint`. `core.queue_entry`
--      carries `priority_reason`, which in practice reads "critical glucose, seen first" —
--      a diagnosis in all but name. None of the four reaches the view. The board shows
--      *that* a patient is prioritised, never *why*.
--
--   3. How a patient is named on the board is a decision Dr. Nahid owns, because it depends
--      on where the screen physically hangs. It is a per-facility setting with the cautious
--      default (the visit code alone, which means nothing to anyone without the card).
--
-- Bottleneck thresholds are seeded here as *proposed values requiring approval*, in a table
-- rather than a constant — the same instinct as CP39's station sequences. A threshold in Go
-- is a threshold that needs a deployment to change, and a clinic tunes these in the first
-- week.

-- +goose Up

-- ---------------------------------------------------------------------------
-- What the board is allowed to say
-- ---------------------------------------------------------------------------

CREATE TABLE core.board_setting (
  facility_id uuid PRIMARY KEY REFERENCES core.facility(id),

  -- How a waiting patient is named on a screen strangers can read.
  --
  --   code           the visit code only — "V-2026-0914-017". Meaningless without the card
  --                  the patient is holding, which is the point.
  --   code_initials  the visit code and initials — "V-2026-0914-017 · K.M.N."
  --   code_clinical  the visit code and the clinical id, for a screen only staff can see.
  --
  -- Default `code`. The safe end of the range is the default because the unsafe end is a
  -- decision somebody has to make deliberately, in a room, looking at where the screen is.
  identify_by text NOT NULL DEFAULT 'code'
              CHECK (identify_by IN ('code', 'code_initials', 'code_clinical')),

  -- Amber. Proposed: fifteen minutes, or four people deep.
  busy_wait_seconds       integer NOT NULL DEFAULT 900  CHECK (busy_wait_seconds > 0),
  busy_depth              integer NOT NULL DEFAULT 4    CHECK (busy_depth > 0),
  -- Red. Proposed: half an hour, or seven people deep.
  bottleneck_wait_seconds integer NOT NULL DEFAULT 1800 CHECK (bottleneck_wait_seconds > 0),
  bottleneck_depth        integer NOT NULL DEFAULT 7    CHECK (bottleneck_depth > 0),

  updated_at timestamptz NOT NULL DEFAULT now(),

  -- Amber before red, in both dimensions. A configuration where the bottleneck threshold is
  -- lower than the busy one produces a board that goes red before it goes amber, which is
  -- not a bug anybody would report — they would just stop trusting the colours.
  CONSTRAINT board_thresholds_ascend
    CHECK (bottleneck_wait_seconds >= busy_wait_seconds AND bottleneck_depth >= busy_depth)
);

GRANT SELECT ON core.board_setting TO dthcms_app;

COMMENT ON TABLE core.board_setting IS
  'How the wall board names patients and when it goes amber and red. Data, not code: both are decisions the clinic owns (CP40).';
COMMENT ON COLUMN core.board_setting.identify_by IS
  'Patient identification convention on a screen patients can read. Defaults to the visit code alone.';

INSERT INTO core.board_setting (facility_id) VALUES (core.default_facility())
ON CONFLICT (facility_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- The board's only source of rows
-- ---------------------------------------------------------------------------

-- Every column here is one somebody has decided is safe to project onto a wall. Adding one
-- is a deliberate act with an invariant standing behind it, which is the entire point of
-- routing the board through a view rather than letting it join `core.visit` directly.
--
-- Notably absent, and each for its own reason:
--   visit.diagnoses, visit.plan, visit.chief_complaint  — clinical, obviously
--   queue_entry.priority_reason                          — "why is that person first" is a
--                                                          diagnosis read aloud
--   patient.name_en, patient.name_bn                     — a name is the identifier that
--                                                          makes every other field
--                                                          identifiable
CREATE VIEW core.board_row AS
SELECT
  q.id             AS entry_id,
  q.facility_id,
  q.clinic_day,
  q.station_code,
  q.position,
  q.status,
  -- The level, never the reason.
  q.priority,
  q.entered_at,
  q.called_at,
  q.started_at,
  q.visit_id,
  v.visit_code,
  v.visit_type,
  p.clinical_id,
  -- Initials from the English name, upper-cased and dotted: "Kazi Md Nahid" → "K.M.N."
  -- Computed here rather than in Go so that the name itself never leaves the database on
  -- the board's path. A field that is never selected cannot be logged by accident.
  -- COALESCE and the cast are both load-bearing: `string_agg` over no rows is NULL of an
  -- indeterminate type, which sqlc renders as `interface{}` and pgx then cannot scan.
  COALESCE(
    (SELECT string_agg(upper(left(word, 1)), '.' ORDER BY ordinality) || '.'
       FROM unnest(regexp_split_to_array(btrim(p.name_en), '\s+')) WITH ORDINALITY AS t(word, ordinality)
      WHERE word <> ''),
    '')::text AS initials,
  -- §5.5's counselling tick. The gate itself is CP55–57; until then the honest answer is
  -- "has this visit finished at the counselling station", which is what the board shows and
  -- what a supervisor actually acts on.
  EXISTS (
    SELECT 1 FROM core.queue_entry c
     WHERE c.visit_id = q.visit_id
       AND c.station_code = 'STN_COUNSELING'
       AND c.status = 'done'
  ) AS counseling_done
FROM core.queue_entry q
JOIN core.visit v   ON v.id = q.visit_id
JOIN core.patient p ON p.id = q.patient_id;

GRANT SELECT ON core.board_row TO dthcms_app;

COMMENT ON VIEW core.board_row IS
  'The only rows the wall board may read. An explicit allowlist of columns safe to project in a public waiting area (CP40).';

-- ---------------------------------------------------------------------------
-- Rerouting, atomically
-- ---------------------------------------------------------------------------

-- A reroute is two writes that must not be one without the other: the patient leaves one
-- queue and joins another. Half of that is a patient who has left anthropometry and is
-- standing in no queue at all — invisible to the board, which is precisely the failure the
-- board exists to prevent.
--
-- SECURITY DEFINER for the same reason `call_next_at_station` is: the ordering and the
-- exclusivity are properties of the statement, and a statement the application could
-- assemble differently is a statement it will eventually assemble differently.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.reroute_queue_entry(
  p_entry uuid, p_facility uuid, p_to text, p_reason text,
  p_user uuid, p_at timestamptz, p_new_entry uuid)
RETURNS SETOF core.queue_entry
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = core, pg_catalog
AS $$
DECLARE
  v_from core.queue_entry;
  v_pos  integer;
BEGIN
  SELECT * INTO v_from
    FROM core.queue_entry
   WHERE id = p_entry AND facility_id = p_facility
     AND status IN ('waiting', 'called', 'in_service')
   FOR UPDATE;

  IF v_from.id IS NULL THEN
    RETURN;
  END IF;

  UPDATE core.queue_entry
     SET status = 'rerouted', ended_at = p_at, outcome = 'rerouted',
         outcome_reason = btrim(p_reason), rerouted_to = p_to
   WHERE id = v_from.id;

  -- The destination's planned position, so the board orders the patient where the journey
  -- says they belong rather than at the end.
  SELECT s.position INTO v_pos
    FROM core.station_sequence s
    JOIN core.visit vi ON vi.id = v_from.visit_id
   WHERE s.facility_id = p_facility AND s.visit_type = vi.visit_type
     AND s.station_code = p_to;

  RETURN QUERY
    INSERT INTO core.queue_entry
      (id, facility_id, visit_id, patient_id, station_code, position,
       status, priority, priority_reason, entered_at, clinic_day)
    VALUES
      (p_new_entry, p_facility, v_from.visit_id, v_from.patient_id, p_to,
       COALESCE(v_pos, 0), 'waiting', v_from.priority, v_from.priority_reason,
       p_at, v_from.clinic_day)
    RETURNING *;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION
  core.reroute_queue_entry(uuid, uuid, text, text, uuid, timestamptz, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
  core.reroute_queue_entry(uuid, uuid, text, text, uuid, timestamptz, uuid) TO dthcms_app;

-- ---------------------------------------------------------------------------
-- Who may watch, and who may move people
-- ---------------------------------------------------------------------------

-- Two permissions, because they are two different jobs.
--
-- `board.read` is the wall display's own permission rather than `visit.read`. The screen in
-- the waiting area needs an account, and that account should be able to do exactly one
-- thing. Reusing `visit.read` would give the machine bolted to the wall the ability to pull
-- any patient's visit history, which is the kind of over-grant §4.4 exists to prevent.
--
-- `visit.reroute` is a floor supervisor's action, not a station operator's. Rerouting is
-- deciding somebody else's queue is wrong, and an anthropometry officer who can push their
-- own queue onto the next station is an anthropometry officer with a bad morning.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('board.read', 'board', 'read', '',
   'See the clinic traffic board: who is waiting where, and for how long', false),
  ('visit.reroute', 'visit', 'reroute', '',
   'Move a waiting patient from one station''s queue to another, with a reason', false)
ON CONFLICT (code) DO UPDATE SET
  resource = EXCLUDED.resource, action = EXCLUDED.action, scope = EXCLUDED.scope,
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

-- Everyone on the floor can read the board; it is the shared picture the parallel model
-- runs on, and a station operator who cannot see the whole floor cannot help with it.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'board.read' FROM core.role r
 WHERE r.code IN ('REGISTRATION', 'ANTHROPOMETRY', 'COUNSELOR', 'HISTORY', 'CLINICAL_ASSISTANT',
                  'JUNIOR_DOCTOR', 'RECORDS', 'NUTRITIONIST', 'EXERCISE', 'PHYSICIAN', 'QA',
                  'RX_EDUCATOR', 'CRM', 'ADMIN')
ON CONFLICT DO NOTHING;

-- Rerouting is narrower: the desk that runs the floor, the physician, QA, and an
-- administrator.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'visit.reroute' FROM core.role r
 WHERE r.code IN ('REGISTRATION', 'PHYSICIAN', 'QA', 'ADMIN')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The invariant
-- ---------------------------------------------------------------------------

-- Criterion 2, as a standing check rather than a code review.
--
-- The allowlist is written out longhand. A rule expressed as "no column whose name contains
-- 'diagnos'" would pass a column called `impression`, and the whole reason this check exists
-- is that the dangerous change is the one nobody thought of.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_the_board_shows_nothing_clinical() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  allowed text[] := ARRAY[
    'entry_id', 'facility_id', 'clinic_day', 'station_code', 'position', 'status',
    'priority', 'entered_at', 'called_at', 'started_at', 'visit_id', 'visit_code',
    'visit_type', 'clinical_id', 'initials', 'counseling_done'
  ];
  extra text;
BEGIN
  SELECT string_agg(column_name, ', ' ORDER BY ordinal_position) INTO extra
    FROM information_schema.columns
   WHERE table_schema = 'core' AND table_name = 'board_row'
     AND column_name <> ALL (allowed);

  IF extra IS NOT NULL THEN
    RAISE EXCEPTION 'the wall board would show: %', extra
      USING HINT = 'core.board_row is an allowlist of columns safe to project in a public '
                   'waiting area. Adding one is a decision about what patients sitting in '
                   'that room may read about each other, so it is made here, deliberately, '
                   'and not in a query (CP40).';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_the_board_shows_nothing_clinical() IS
  'Raises if core.board_row grows a column outside the allowlist safe for a public screen (CP40).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_the_board_shows_nothing_clinical',
   'the wall board exposes only columns safe to project in a public waiting area', 42)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM core.role_permission WHERE permission_code IN ('board.read', 'visit.reroute');
DELETE FROM core.permission WHERE code IN ('board.read', 'visit.reroute');
DELETE FROM ops.invariant WHERE function_name = 'assert_the_board_shows_nothing_clinical';
DROP FUNCTION IF EXISTS core.assert_the_board_shows_nothing_clinical();
DROP FUNCTION IF EXISTS core.reroute_queue_entry(uuid, uuid, text, text, uuid, timestamptz, uuid);
DROP VIEW IF EXISTS core.board_row;
DROP TABLE IF EXISTS core.board_setting;
