-- Projections (CP25, blueprint §7.8): the read models derived from the ledger, the
-- checkpoint that says how far each has got, and the dead-letter queue for the events one
-- could not handle.
--
-- The rule this schema exists under was set at CP03 and is not relaxed here:
-- `core.assert_read_models_derived()` refuses to start a service where `dthcms_app` holds
-- INSERT, UPDATE or DELETE on anything in `read`. A read model the application can edit is
-- a read model that will be corrected by hand one afternoon, after which it agrees with
-- nothing and no rebuild can be trusted.
--
-- That leaves a question the plan poses directly: a *synchronous* projection runs inside
-- the append transaction, as the application, and the application may not write here. The
-- answer is that a synchronous projection is a SECURITY DEFINER function (`read.apply_…`)
-- with a pinned search_path: the application may call it, and may do nothing else to the
-- table. The derivation is one auditable object, and the rebuild calls the same function,
-- so "rebuilt" and "incrementally built" are the same code and not merely tested to agree.
-- ADR-0017 records the reasoning.
--
-- Asynchronous projections are Go, run by `cmd/projector` as `dthcms_projector`, and catch
-- up from `global_seq`.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The register: which projections exist, at what version, and how far each has got
-- ---------------------------------------------------------------------------

CREATE TABLE read.projection_state (
  name          text        PRIMARY KEY,

  -- The version of the *derivation*. A change to how a read model is computed makes it a
  -- new version, and a projection whose registered version differs from the stored one is
  -- rebuilt before it is trusted (§7.8). This is what stops a subtle logic change leaving
  -- half the rows computed the old way and half the new.
  version       integer     NOT NULL CHECK (version >= 1),

  mode          text        NOT NULL CHECK (mode IN ('synchronous', 'asynchronous')),

  -- The global sequence of the last event applied. Zero means nothing yet.
  checkpoint    bigint      NOT NULL DEFAULT 0 CHECK (checkpoint >= 0),

  -- healthy: keeping up. degraded: an event was dead-lettered and skipped, so the model is
  -- knowingly incomplete. rebuilding: a rebuild is in flight and these rows are not to be
  -- trusted until it finishes.
  status        text        NOT NULL DEFAULT 'healthy'
                CHECK (status IN ('healthy', 'degraded', 'rebuilding')),

  -- When the last applied event was *recorded*, for lag in seconds. A projection that has
  -- seen every event is not behind merely because the clinic is quiet.
  applied_at    timestamptz,
  updated_at    timestamptz NOT NULL DEFAULT now(),
  rebuilt_at    timestamptz
);

COMMENT ON TABLE read.projection_state IS
  'One row per projection: version, mode, how far it has got, and whether it is healthy (CP25).';

-- ---------------------------------------------------------------------------
-- The dead-letter queue
-- ---------------------------------------------------------------------------

-- An event a projection could not handle. The runner records it, marks the projection
-- degraded and moves on: a poison event must not stop every later event from being
-- projected, and it must certainly not stop the ledger accepting new ones (criterion 4).
CREATE TABLE read.projection_dead_letter (
  id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  projection  text        NOT NULL REFERENCES read.projection_state(name) ON DELETE CASCADE,
  global_seq  bigint      NOT NULL,
  event_id    uuid        NOT NULL,
  event_type  text        NOT NULL,

  -- The error and how many attempts produced it. No payload: the event is in the ledger
  -- and can be read from there, and copying a clinical payload into an operational table
  -- would put patient data somewhere the access model does not reach.
  error       text        NOT NULL,
  attempts    integer     NOT NULL DEFAULT 1 CHECK (attempts >= 1),
  failed_at   timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz,
  resolution  text,

  CONSTRAINT dead_letter_once UNIQUE (projection, global_seq),
  CONSTRAINT dead_letter_resolution CHECK ((resolved_at IS NULL) = (resolution IS NULL))
);

CREATE INDEX projection_dead_letter_open ON read.projection_dead_letter (projection, failed_at)
  WHERE resolved_at IS NULL;

COMMENT ON TABLE read.projection_dead_letter IS
  'Events a projection could not apply. The projection stays degraded until each is resolved (CP25).';

-- ---------------------------------------------------------------------------
-- read.visit_vital — the vitals strip. Synchronous.
-- ---------------------------------------------------------------------------

-- The current value of each measurement on a visit, with who took it and when [R-03].
-- Synchronous because staleness here is clinical: the junior doctor reading a value the
-- nurse entered a moment ago must see it (§4.1), not a spinner.
--
-- One row per (visit, code); a correction overwrites the value it corrects, and
-- `global_seq` decides which of two events is later. That is also what makes the
-- projection idempotent — applying the same event twice writes the same row, and applying
-- an older event after a newer one writes nothing.
CREATE TABLE read.visit_vital (
  visit_id      uuid        NOT NULL,
  code          text        NOT NULL,
  facility_id   uuid        NOT NULL,
  patient_id    uuid,

  value         numeric     NOT NULL,
  unit          text        NOT NULL,
  -- The second component of a paired reading: the diastolic, when the code is BP.
  value_2       numeric,

  taken_at      timestamptz NOT NULL,
  recorded_at   timestamptz NOT NULL,

  actor_user_id uuid        NOT NULL,
  actor_role    text        NOT NULL,
  actor_station text,

  event_id      uuid        NOT NULL,
  global_seq    bigint      NOT NULL,
  corrected     boolean     NOT NULL DEFAULT false,

  PRIMARY KEY (visit_id, code)
);

CREATE INDEX visit_vital_by_patient ON read.visit_vital (patient_id, taken_at DESC);

COMMENT ON TABLE read.visit_vital IS
  'The current value of each measurement on a visit, with its attribution. Synchronous projection (CP25).';

-- ---------------------------------------------------------------------------
-- read.station_activity — the traffic board's substrate. Asynchronous.
-- ---------------------------------------------------------------------------

CREATE TABLE read.station_activity (
  facility_id  uuid        NOT NULL,
  clinic_day   date        NOT NULL,
  station      text        NOT NULL,
  events       bigint      NOT NULL DEFAULT 0,
  -- The highest global_seq counted here. A replayed event whose sequence is not above it
  -- is ignored, which is what makes a *counter* idempotent — a counter alone could not
  -- promise that.
  last_seq     bigint      NOT NULL DEFAULT 0,
  updated_at   timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (facility_id, clinic_day, station)
);

-- The visits a station saw that day, one row each, so "distinct visits" is a count rather
-- than a set kept in a column.
CREATE TABLE read.station_activity_visit (
  facility_id uuid NOT NULL,
  clinic_day  date NOT NULL,
  station     text NOT NULL,
  visit_id    uuid NOT NULL,

  PRIMARY KEY (facility_id, clinic_day, station, visit_id)
);

COMMENT ON TABLE read.station_activity IS
  'Per-station, per-day activity for the traffic board. Asynchronous projection (CP25).';

-- ---------------------------------------------------------------------------
-- The synchronous derivation
-- ---------------------------------------------------------------------------

-- read.apply_visit_vital applies one event to read.visit_vital and advances the
-- projection's checkpoint, in the caller's transaction.
--
-- SECURITY DEFINER, because the caller inside the append transaction is `dthcms_app`,
-- which may not write to `read` and must not be given the privilege to. The function is
-- the only thing it can do to this table: it cannot delete a row, cannot set a value the
-- events do not imply, and cannot move the checkpoint backwards.
--
-- search_path is pinned. An unpinned SECURITY DEFINER function is the classic PostgreSQL
-- privilege escalation: a caller who can create a table in a schema earlier on the path
-- redirects the writes into their own.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_visit_vital(event jsonb)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, pg_catalog
AS $$
DECLARE
  -- v_ prefixes throughout: an unprefixed `code` would be ambiguous against the column of
  -- that name in the INSERT below, and plpgsql resolves the ambiguity by raising rather
  -- than by guessing — which is the right behaviour and worth naming around.
  v_seq        bigint  := (event ->> 'global_seq')::bigint;
  v_code       text    := event ->> 'code';
  v_corrected  boolean := coalesce((event ->> 'corrected')::boolean, false);
BEGIN
  IF v_seq IS NULL OR v_code IS NULL THEN
    RAISE EXCEPTION 'read.apply_visit_vital: the event carries no global_seq or code';
  END IF;

  INSERT INTO read.visit_vital (
    visit_id, code, facility_id, patient_id, value, unit, value_2,
    taken_at, recorded_at, actor_user_id, actor_role, actor_station,
    event_id, global_seq, corrected)
  VALUES (
    (event ->> 'visit_id')::uuid, v_code, (event ->> 'facility_id')::uuid,
    nullif(event ->> 'patient_id', '')::uuid,
    (event ->> 'value')::numeric, event ->> 'unit',
    nullif(event ->> 'value_2', '')::numeric,
    (event ->> 'taken_at')::timestamptz, (event ->> 'recorded_at')::timestamptz,
    (event ->> 'actor_user_id')::uuid, event ->> 'actor_role',
    nullif(event ->> 'actor_station', ''),
    (event ->> 'event_id')::uuid, v_seq, v_corrected)
  ON CONFLICT (visit_id, code) DO UPDATE
     SET value = excluded.value, unit = excluded.unit, value_2 = excluded.value_2,
         taken_at = excluded.taken_at, recorded_at = excluded.recorded_at,
         actor_user_id = excluded.actor_user_id, actor_role = excluded.actor_role,
         actor_station = excluded.actor_station, patient_id = excluded.patient_id,
         event_id = excluded.event_id, global_seq = excluded.global_seq,
         corrected = excluded.corrected
   -- Later events win; a replay of an older one changes nothing. This is the whole of the
   -- projection's idempotence and out-of-order tolerance.
   WHERE read.visit_vital.global_seq < excluded.global_seq;

  UPDATE read.projection_state
     SET checkpoint = GREATEST(checkpoint, v_seq),
         applied_at = GREATEST(coalesce(applied_at, '-infinity'::timestamptz),
                               (event ->> 'recorded_at')::timestamptz),
         updated_at = now()
   WHERE name = 'visit_vital';
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION read.apply_visit_vital(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.apply_visit_vital(jsonb) TO dthcms_app, dthcms_projector;

COMMENT ON FUNCTION read.apply_visit_vital(jsonb) IS
  'The visit_vital derivation. The only write the application may make in the read schema (CP25, ADR-0017).';

-- read.reset_visit_vital empties the model for a rebuild. Not SECURITY DEFINER: a rebuild
-- runs as the projector, which owns the read schema, and the application has no business
-- emptying a read model at all.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.reset_visit_vital()
RETURNS void
LANGUAGE plpgsql
SET search_path = read, pg_catalog
AS $$
BEGIN
  DELETE FROM read.visit_vital;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION read.reset_visit_vital() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.reset_visit_vital() TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariant
-- ---------------------------------------------------------------------------

-- The CP03 invariant already refuses write privileges for the application in `read`. What
-- is added here is the other half: the projector must be able to rebuild every read model,
-- and a model it cannot empty is a model that cannot be rebuilt.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_read_models_rebuildable() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  SELECT string_agg(format('read.%s', c.relname), ', ' ORDER BY c.relname)
    INTO offending
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'read'
    AND c.relkind IN ('r', 'p')
    AND NOT (has_table_privilege('dthcms_projector', c.oid, 'INSERT')
         AND has_table_privilege('dthcms_projector', c.oid, 'UPDATE')
         AND has_table_privilege('dthcms_projector', c.oid, 'DELETE'));

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'the projection role cannot rebuild: %', offending
      USING HINT = 'Every table in read must be writable by dthcms_projector, or a '
                   'rebuild stops halfway with half the models rebuilt.';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_read_models_rebuildable() IS
  'Raises if dthcms_projector cannot rewrite a read model, which would make a rebuild impossible.';

-- The registry from 00007 is what the runner iterates; adding an assertion is an INSERT.
INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_read_models_rebuildable', 'every read model can be rebuilt by the projector', 25)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- ---------------------------------------------------------------------------
-- Facility scoping
-- ---------------------------------------------------------------------------

-- The read models carry facility_id. The two operational tables do not, and cannot: a
-- projection is a property of the deployment, not of a clinic, and its checkpoint is one
-- number across every facility the instance serves. D-61's exemption mechanism is for
-- exactly this, and the reason is recorded rather than the assertion weakened.
INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('read', 'projection_state',
   'A projection and its checkpoint belong to the deployment, not to a facility; one read model serves every facility it holds rows for.'),
  ('read', 'projection_dead_letter',
   'An event a projection could not apply is an operational failure of the projection, not a record about a clinic.')
ON CONFLICT (schema_name, table_name) DO NOTHING;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name = 'assert_read_models_rebuildable';
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'read' AND table_name IN ('projection_state', 'projection_dead_letter');

DROP FUNCTION IF EXISTS core.assert_read_models_rebuildable();
DROP FUNCTION IF EXISTS read.reset_visit_vital();
DROP FUNCTION IF EXISTS read.apply_visit_vital(jsonb);
DROP TABLE IF EXISTS read.station_activity_visit;
DROP TABLE IF EXISTS read.station_activity;
DROP TABLE IF EXISTS read.visit_vital;
DROP TABLE IF EXISTS read.projection_dead_letter;
DROP TABLE IF EXISTS read.projection_state;
