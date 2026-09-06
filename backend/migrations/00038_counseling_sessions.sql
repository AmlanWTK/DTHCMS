-- Counselling on the floor: sessions and ticks (CP56, §5.3, [R-01] [R-07]).
--
-- # What a session is, and why it is not an observation
--
-- The plan says CP56 needs no new schema and uses CP42's observation tables. It is wrong here
-- for the reason ADR-0028 already gave about history and allergies: an observation is a value
-- measured at a moment, and a counselling session is a *walk through a list* that starts in
-- one room, continues in another, and is finished or not. Two of the four acceptance criteria
-- are about the walk rather than about any value in it — each tick individually attributed,
-- and progress visible on the board — and neither is a sentence one can say about a
-- measurement. Modelled as observations, "which mandatory items are still missing" becomes a
-- query that reassembles a list from rows that do not know they are a list, and CP57's gate
-- would be built on that reassembly.
--
-- # The version is on the session, not looked up
--
-- CP55's criterion 2: a completed session retains the version it used. The session holds
-- `template_version` and a foreign key to the frozen version row, so a checklist republished
-- while a counsellor is halfway through it does not change what they are being asked to cover.
-- The column has no ON UPDATE and a trigger refuses a change to it, because the one way this
-- guarantee dies quietly is a well-meant UPDATE that "fixes" a session onto the current
-- version.
--
-- # A tick is a row, and one row per person per item
--
-- Criterion 1: every tick carries its own attribution and timestamp. That is a property of the
-- row here and of the event behind it — there is no endpoint that ticks a list, and
-- `read.counseling_tick` has no way to represent one. Twenty items covered is twenty events by
-- whoever covered them, which is the only shape in which §5.4's spot-questioning ("who told
-- you about injection sites?") has an answer.
--
-- # Un-ticking keeps the row
--
-- Criterion 3: un-ticking requires a reason and is recorded. A DELETE would satisfy the words
-- and destroy the point — the interesting record is that somebody ticked an item and then
-- somebody took it back. So the row stays, `undone_at`/`undone_by`/`undone_reason` fill in, and
-- `undo_count` remembers how often it has happened even after a re-tick, because "this item
-- was ticked and un-ticked three times" is exactly what a quality review is looking for.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------

-- `counseling.tick` already exists (00006) and is what a counsellor holds. Reading a session
-- is separate: the physician's panel (CP57) and the traffic board read sessions and never tick
-- one, and a panel that needed the tick permission would be a physician's screen carrying the
-- right to write on somebody else's checklist.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('counseling.session.read', 'counseling', 'session', 'read',
   'Read counselling sessions and what has been covered', false)
ON CONFLICT (code) DO UPDATE SET
  resource = EXCLUDED.resource, action = EXCLUDED.action, scope = EXCLUDED.scope,
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'counseling.session.read' FROM core.role r
 WHERE r.code IN ('COUNSELOR', 'NUTRITIONIST', 'EXERCISE', 'RX_EDUCATOR',
                  'CLINICAL_ASSISTANT', 'JUNIOR_DOCTOR', 'PHYSICIAN', 'QA', 'ADMIN')
ON CONFLICT DO NOTHING;

-- Ticking reaches one more role than CP15 gave it, and the reason is §5.2: a counselling session
-- walks *three rooms*, and the nutrition room is the nutritionist's. Only the counsellor held
-- `counseling.tick`, which would have meant the nutrition items ticked by somebody who was not in
-- the room — the precise failure §5.4's spot-questioning exists to catch, built into the
-- permission table.
--
-- The exercise specialist and the prescription educator are deliberately **not** here. Both
-- staff stations of their own, and no room maps to either: the insulin corner is seeded as part
-- of the counselling station because that is what §5.2 says. Who actually stands in the insulin
-- corner is an operational decision, and when it is made it is two rows — a `station_code` on
-- the room and a grant here — rather than a guess made in a migration written months earlier.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'counseling.tick' FROM core.role r
 WHERE r.code IN ('NUTRITIONIST')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The session
-- ---------------------------------------------------------------------------

CREATE TABLE read.counseling_session (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL,
  patient_id  uuid NOT NULL,
  visit_id    uuid NOT NULL,

  -- The frozen version, by foreign key. Not `template_id` alone: the whole of CP55's
  -- criterion 2 is that this session is against *that* list, whatever is live tomorrow.
  template_id      uuid NOT NULL,
  template_version integer NOT NULL,

  started_at   timestamptz NOT NULL,
  started_by   uuid NOT NULL,
  started_role text NOT NULL DEFAULT '',

  -- Finished by a person, never by arithmetic. A session that closed itself the moment the
  -- last mandatory item was ticked would take the counsellor's judgement out of the one place
  -- §5.4 relies on it: whether the patient actually understood.
  completed_at timestamptz,
  completed_by uuid,

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL,

  CONSTRAINT counseling_session_completion_is_attributed
    CHECK ((completed_at IS NULL) = (completed_by IS NULL)),

  CONSTRAINT counseling_session_version_exists
    FOREIGN KEY (template_id, template_version)
    REFERENCES core.counseling_template_version (template_id, version)
);

-- One session per checklist per visit. A patient with two conditions gets two checklists, and
-- a counsellor who reopens the app mid-session must land back in the one that is open rather
-- than starting a second, half-ticked copy of the same list.
CREATE UNIQUE INDEX counseling_session_once_per_visit
  ON read.counseling_session (visit_id, template_id);

CREATE INDEX counseling_session_by_patient
  ON read.counseling_session (patient_id, started_at DESC);

CREATE INDEX counseling_session_open_by_facility
  ON read.counseling_session (facility_id, started_at)
  WHERE completed_at IS NULL;

GRANT SELECT ON read.counseling_session TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON read.counseling_session TO dthcms_projector;

COMMENT ON TABLE read.counseling_session IS
  'One walk through one checklist, for one visit (CP56). Holds the template version it used.';

-- ---------------------------------------------------------------------------
-- The ticks
-- ---------------------------------------------------------------------------

CREATE TABLE read.counseling_tick (
  session_id uuid NOT NULL REFERENCES read.counseling_session(id) ON DELETE RESTRICT,
  item_code  text NOT NULL,

  -- Denormalised from the session so the physician's panel and the gate can read ticks
  -- without a join, and so a facility scope check has something to read on this row.
  facility_id uuid NOT NULL,
  patient_id  uuid NOT NULL,

  ticked_at   timestamptz NOT NULL,
  ticked_by   uuid NOT NULL,
  ticked_role text NOT NULL DEFAULT '',

  -- §5.3's optional per-item note. Not a second checklist: it is what the counsellor wants the
  -- physician to know about this item for this patient.
  note text NOT NULL DEFAULT '',

  -- Criterion 3. A reason with no un-tick, or an un-tick with no reason, is refused.
  undone_at     timestamptz,
  undone_by     uuid,
  undone_reason text NOT NULL DEFAULT '',
  undo_count    integer NOT NULL DEFAULT 0 CHECK (undo_count >= 0),

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL,

  PRIMARY KEY (session_id, item_code),

  CONSTRAINT counseling_tick_undo_is_complete CHECK (
    (undone_at IS NULL AND undone_by IS NULL AND btrim(undone_reason) = '')
    OR (undone_at IS NOT NULL AND undone_by IS NOT NULL AND btrim(undone_reason) <> ''))
);

CREATE INDEX counseling_tick_live_by_session
  ON read.counseling_tick (session_id) WHERE undone_at IS NULL;

CREATE INDEX counseling_tick_by_operator
  ON read.counseling_tick (ticked_by, ticked_at DESC);

GRANT SELECT ON read.counseling_tick TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON read.counseling_tick TO dthcms_projector;

COMMENT ON TABLE read.counseling_tick IS
  'One item covered, by one person, at one time (CP56 criterion 1). Un-ticks keep the row.';

-- ---------------------------------------------------------------------------
-- The session's version is fixed at its start
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.counseling_session_keeps_its_version() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.template_id <> OLD.template_id OR NEW.template_version <> OLD.template_version THEN
    RAISE EXCEPTION 'a counselling session keeps the checklist version it started on'
      USING ERRCODE = 'raise_exception';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER counseling_session_version_is_fixed
  BEFORE UPDATE ON read.counseling_session
  FOR EACH ROW EXECUTE FUNCTION core.counseling_session_keeps_its_version();

-- ---------------------------------------------------------------------------
-- A tick names an item that is on the list the session holds
-- ---------------------------------------------------------------------------

-- Not a nicety. A tick against an item code the version does not contain would count towards
-- nothing, satisfy no gate, and appear on the physician's panel as a line nobody can explain —
-- and the most likely way to produce one is a client caching a list across a republish, which
-- is exactly the situation the frozen version exists for.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.counseling_tick_is_on_the_list() RETURNS trigger
LANGUAGE plpgsql
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  known boolean;
BEGIN
  SELECT EXISTS (
    SELECT 1
      FROM read.counseling_session s
      JOIN core.counseling_item i
        ON i.template_id = s.template_id AND i.version = s.template_version
     WHERE s.id = NEW.session_id AND i.item_code = NEW.item_code)
    INTO known;

  IF NOT known THEN
    RAISE EXCEPTION 'item % is not on the checklist version this session is walking',
      NEW.item_code
      USING ERRCODE = 'raise_exception';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER counseling_tick_matches_the_version
  BEFORE INSERT ON read.counseling_tick
  FOR EACH ROW EXECUTE FUNCTION core.counseling_tick_is_on_the_list();

-- ---------------------------------------------------------------------------
-- What is still missing, as one function
-- ---------------------------------------------------------------------------

-- The gate (CP57), the mobile progress indicator and the physician's panel all ask the same
-- question, so they ask it in one place. A second implementation of "what is outstanding" is
-- how a gate and a screen come to disagree about whether a patient may move on.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.counseling_outstanding(p_session uuid)
RETURNS TABLE (item_code text, room text, text_en text, text_bn text)
LANGUAGE sql
STABLE
SET search_path = read, core, pg_catalog
AS $$
  SELECT i.item_code, i.room, i.text_en, i.text_bn
    FROM read.counseling_session s
    JOIN core.counseling_item i
      ON i.template_id = s.template_id AND i.version = s.template_version
   WHERE s.id = p_session
     AND i.is_mandatory
     AND NOT EXISTS (
       SELECT 1 FROM read.counseling_tick t
        WHERE t.session_id = s.id AND t.item_code = i.item_code AND t.undone_at IS NULL)
   ORDER BY i.ordering;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION core.counseling_outstanding(uuid) TO dthcms_app, dthcms_projector;

-- ---------------------------------------------------------------------------
-- Projections
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_counseling_session_started(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.counseling_session (
    id, facility_id, patient_id, visit_id, template_id, template_version,
    started_at, started_by, started_role, event_id, global_seq)
  VALUES (
    (p->>'session_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    (p->>'visit_id')::uuid,
    (p->>'template_id')::uuid,
    (p->>'template_version')::int,
    (p->>'started_at')::timestamptz,
    (p->>'started_by')::uuid,
    coalesce(p->>'started_role', ''),
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (id) DO NOTHING;
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
  -- A re-tick after an un-tick lands on the same row: the item is covered again, by whoever
  -- covered it this time, and the undo count remembers that it went round once. Keeping a
  -- second row instead would make "is this item covered" a question with two answers.
  INSERT INTO read.counseling_tick (
    session_id, item_code, facility_id, patient_id,
    ticked_at, ticked_by, ticked_role, note, event_id, global_seq)
  VALUES (
    (p->>'session_id')::uuid,
    p->>'item_code',
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    (p->>'ticked_at')::timestamptz,
    (p->>'ticked_by')::uuid,
    coalesce(p->>'ticked_role', ''),
    coalesce(p->>'note', ''),
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (session_id, item_code) DO UPDATE SET
    ticked_at     = EXCLUDED.ticked_at,
    ticked_by     = EXCLUDED.ticked_by,
    ticked_role   = EXCLUDED.ticked_role,
    note          = EXCLUDED.note,
    undone_at     = NULL,
    undone_by     = NULL,
    undone_reason = '',
    event_id      = EXCLUDED.event_id,
    global_seq    = EXCLUDED.global_seq
  WHERE read.counseling_tick.global_seq < EXCLUDED.global_seq;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_counseling_item_unticked(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  UPDATE read.counseling_tick
     SET undone_at     = (p->>'undone_at')::timestamptz,
         undone_by     = (p->>'undone_by')::uuid,
         undone_reason = p->>'reason',
         undo_count    = undo_count + 1,
         global_seq    = (p->>'global_seq')::bigint
   WHERE session_id = (p->>'session_id')::uuid
     AND item_code  = p->>'item_code'
     AND undone_at IS NULL
     AND global_seq < (p->>'global_seq')::bigint;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_counseling_session_completed(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  UPDATE read.counseling_session
     SET completed_at = (p->>'completed_at')::timestamptz,
         completed_by = (p->>'completed_by')::uuid,
         global_seq   = (p->>'global_seq')::bigint
   WHERE id = (p->>'session_id')::uuid
     AND completed_at IS NULL;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_counseling_session_started(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_counseling_item_ticked(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_counseling_item_unticked(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_counseling_session_completed(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- Criterion 1, as a standing check. A tick with no author is a tick nobody can be asked about,
-- and the panel §5.4 describes would be showing a completed list with an empty column.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_tick_is_attributed() RETURNS void
LANGUAGE plpgsql
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM read.counseling_tick
   WHERE ticked_by IS NULL OR ticked_at IS NULL;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% counselling ticks have no author or no time', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- The session's version has to exist and has to be a published or retired one — never a draft.
-- A draft on the floor is a half-written checklist being read to a patient.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_sessions_walk_published_versions() RETURNS void
LANGUAGE plpgsql
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  offender uuid;
BEGIN
  SELECT s.id INTO offender
    FROM read.counseling_session s
    JOIN core.counseling_template_version v
      ON v.template_id = s.template_id AND v.version = s.template_version
   WHERE v.status = 'DRAFT'
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'counselling session % is walking a draft checklist', offender;
  END IF;
END
$$;
-- +goose StatementEnd

-- Criterion 3. An un-tick with no reason is the failure this criterion exists to prevent, and
-- the CHECK constraint above only guards rows written from now on; this notices one that
-- arrived some other way.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_untick_reasons_are_recorded() RETURNS void
LANGUAGE plpgsql
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM read.counseling_tick
   WHERE undone_at IS NOT NULL AND btrim(undone_reason) = '';
  IF offenders > 0 THEN
    RAISE EXCEPTION '% counselling items were un-ticked with no reason given', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_tick_is_attributed',
   'every counselling tick names who ticked it and when', 70),
  ('assert_sessions_walk_published_versions',
   'no counselling session is walking a draft checklist', 71),
  ('assert_untick_reasons_are_recorded',
   'every un-ticked counselling item carries the reason it was taken back', 72)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_every_tick_is_attributed', 'assert_sessions_walk_published_versions',
  'assert_untick_reasons_are_recorded');
DROP FUNCTION IF EXISTS core.assert_untick_reasons_are_recorded();
DROP FUNCTION IF EXISTS core.assert_sessions_walk_published_versions();
DROP FUNCTION IF EXISTS core.assert_every_tick_is_attributed();
DROP FUNCTION IF EXISTS read.apply_counseling_session_completed(jsonb);
DROP FUNCTION IF EXISTS read.apply_counseling_item_unticked(jsonb);
DROP FUNCTION IF EXISTS read.apply_counseling_item_ticked(jsonb);
DROP FUNCTION IF EXISTS read.apply_counseling_session_started(jsonb);
DROP FUNCTION IF EXISTS core.counseling_outstanding(uuid);
DROP TRIGGER IF EXISTS counseling_tick_matches_the_version ON read.counseling_tick;
DROP FUNCTION IF EXISTS core.counseling_tick_is_on_the_list();
DROP TRIGGER IF EXISTS counseling_session_version_is_fixed ON read.counseling_session;
DROP FUNCTION IF EXISTS core.counseling_session_keeps_its_version();
DROP TABLE IF EXISTS read.counseling_tick;
DROP TABLE IF EXISTS read.counseling_session;
DELETE FROM core.role_permission WHERE permission_code = 'counseling.session.read';
DELETE FROM core.role_permission
 WHERE permission_code = 'counseling.tick'
   AND role_id IN (SELECT id FROM core.role WHERE code IN ('NUTRITIONIST'));
DELETE FROM core.permission WHERE code = 'counseling.session.read';
