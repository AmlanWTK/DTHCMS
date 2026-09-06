-- The counselling gate, and the valve that keeps it usable (CP57, §5.5, §5.4).
--
-- # Two gates in this system, and they are deliberately different shapes
--
-- CP54's allergy gate has **no** override, and the reason is in its own header: three honest
-- answers exist, all of them take five seconds, and an override would become the fast one.
--
-- This gate is not like that. What it asks for is *seven conversations*, and the situations
-- that make them impossible are real and common — the patient's daughter arrives with the
-- car, the interpreter does not come, the insulin corner is closed, the consultant is leaving
-- for the day. The plan says so plainly: "a rigid gate with no escape valve will be worked
-- around". A gate people route around is worse than a gate with a recorded valve, because the
-- routing-around is invisible and the valve is not.
--
-- So there is an override, and everything about it is built to be *seen*: it is its own event,
-- its own permission, it requires a reason, it names what was skipped at the moment it was
-- granted, and there is a rate view for the person whose job is to ask why it is being used
-- eleven times a day.
--
-- # Where the gate lives
--
-- On `core.queue_entry`, like CP54's, and for the same reason: the plan's criterion 1 says
-- "enforced server-side", and the honest reading of that is the path nobody remembers — the
-- support script, the second client, the integration written after everyone who read the plan
-- has left. The API refuses first and names the missing items, because a trigger's message is
-- not a screen; the trigger refuses everything else.
--
-- # What counts as missing
--
-- Two things, and the second is the one that matters:
--
--   1. a session that is open or finished with mandatory items nobody ticked, and
--   2. **a checklist this patient's record calls for that nobody ever opened.**
--
-- Without (2) the way past this gate is to never start a checklist, which is not a loophole
-- somebody has to find — it is what happens on a busy morning when the counselling room is
-- skipped. So a checklist called for and never opened counts as all of its mandatory items
-- outstanding.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The permission
-- ---------------------------------------------------------------------------

-- Its own permission, held by nobody who merely works at a station. Which roles hold it is an
-- operational decision the clinic has not made (the plan lists it as open), so it is seeded to
-- the two roles that can already be held accountable for it — and moving it is a row.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('counseling.gate.override', 'counseling', 'gate', 'override',
   'Let a patient past the counselling gate, with a recorded reason', true)
ON CONFLICT (code) DO UPDATE SET
  resource = EXCLUDED.resource, action = EXCLUDED.action, scope = EXCLUDED.scope,
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'counseling.gate.override' FROM core.role r
 WHERE r.code IN ('PHYSICIAN', 'ADMIN')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- What is missing, for one visit
-- ---------------------------------------------------------------------------

-- One function, read by the gate trigger, by the API that produces the message, by the
-- physician's panel and by the phone. Three implementations of "what is still missing" is how a
-- screen shows a green tick while a gate refuses the patient standing in front of it.
--
-- `session_id` is null for a checklist nobody opened, and that is the difference between "go
-- back and finish it" and "nobody has started this at all" — two different remediation paths,
-- and a blocked screen that could not tell them apart would send half the patients to the wrong
-- room.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.counseling_gate_missing(p_visit uuid)
RETURNS TABLE (
  template_id   uuid,
  template_code text,
  title_en      text,
  title_bn      text,
  session_id    uuid,
  item_code     text,
  room          text,
  room_en       text,
  room_bn       text,
  room_step     integer,
  room_station  text,
  text_en       text,
  text_bn       text)
LANGUAGE sql
STABLE
SET search_path = read, core, pg_catalog
AS $$
  -- The room's own names travel with the item, and the checklist's title with the row. A caller
  -- that had only the codes would fetch the room catalogue and the template list to render one
  -- refusal — two extra requests on a clinic link, to say words this query already has.
  WITH outstanding AS (
    -- (1) Sessions that exist, with mandatory items nobody has ticked.
    SELECT t.id AS template_id, t.code AS template_code, t.title_en, t.title_bn,
           s.id AS session_id, o.item_code, o.room, o.text_en, o.text_bn
      FROM read.counseling_session s
      JOIN core.counseling_template t ON t.id = s.template_id
      CROSS JOIN LATERAL core.counseling_outstanding(s.id) o
     WHERE s.visit_id = p_visit

    UNION ALL

    -- (2) Checklists the patient's record calls for that nobody opened. Every mandatory item is
    -- outstanding, because nobody has been asked any of them.
    SELECT t.id, t.code, t.title_en, t.title_bn,
           NULL::uuid, i.item_code, i.room, i.text_en, i.text_bn
      FROM core.visit vis
      JOIN read.history_item h
        ON h.patient_id = vis.patient_id
       AND h.kind = 'COMORBIDITY' AND h.status = 'ACTIVE' AND h.removed_at IS NULL
       AND h.code IS NOT NULL
      JOIN core.counseling_assignment a
        ON a.code_system = h.code_system AND a.code_version = h.code_version
       AND h.code LIKE a.code_prefix || '%' AND a.retired_at IS NULL
      JOIN core.counseling_template t ON t.id = a.template_id AND t.retired_at IS NULL
      JOIN core.counseling_template_version v
        ON v.template_id = t.id AND v.status = 'PUBLISHED'
      JOIN core.counseling_item i
        ON i.template_id = v.template_id AND i.version = v.version AND i.is_mandatory
     WHERE vis.id = p_visit
       AND NOT EXISTS (SELECT 1 FROM read.counseling_session s
                        WHERE s.visit_id = p_visit AND s.template_id = t.id)
  )
  SELECT o.template_id, o.template_code, o.title_en, o.title_bn, o.session_id,
         o.item_code, o.room,
         coalesce(r.display_en, ''), coalesce(r.display_bn, ''), coalesce(r.ordering, 0),
         coalesce(r.station_code, ''),
         o.text_en, o.text_bn
    FROM outstanding o
    LEFT JOIN core.counseling_room r ON r.room = o.room
   -- Ordered, because an unordered UNION ALL comes back in whatever order the planner produced:
   -- the same unchanged visit would render its missing list differently twice, and a physician
   -- re-reading it would think something had moved.
   ORDER BY o.template_code, coalesce(r.ordering, 0), o.item_code
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION core.counseling_gate_missing(uuid) TO dthcms_app, dthcms_projector;

-- ---------------------------------------------------------------------------
-- The override
-- ---------------------------------------------------------------------------

CREATE TABLE read.counseling_gate_override (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL,
  visit_id    uuid NOT NULL,
  patient_id  uuid NOT NULL,

  granted_at   timestamptz NOT NULL,
  granted_by   uuid NOT NULL,
  granted_role text NOT NULL DEFAULT '',

  -- Required, and required to say something. "Override" in a reason field is the same as no
  -- reason, and the only defence against that is a person reading them — which is what the rate
  -- view exists for.
  reason text NOT NULL CHECK (btrim(reason) <> ''),

  -- What was outstanding at the moment it was granted. Kept on the row rather than recomputed,
  -- because the whole point of the record is what was skipped *then*: items ticked afterwards
  -- would make a recomputed list say the override was for nothing.
  missing_at_grant text[] NOT NULL,

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL
);

-- One per visit. A second override is not another act — it is the same one, and answering
-- "already overridden" is more honest than stacking rows nobody reads.
CREATE UNIQUE INDEX counseling_gate_override_once_per_visit
  ON read.counseling_gate_override (visit_id);

CREATE INDEX counseling_gate_override_by_day
  ON read.counseling_gate_override (facility_id, granted_at DESC);

GRANT SELECT ON read.counseling_gate_override TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON read.counseling_gate_override TO dthcms_projector;

COMMENT ON TABLE read.counseling_gate_override IS
  'A patient let past the counselling gate, by a named person, with a reason (CP57).';

-- ---------------------------------------------------------------------------
-- The gate
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.counseling_gate_blocks(p_visit uuid) RETURNS boolean
LANGUAGE sql
STABLE
SET search_path = read, core, pg_catalog
AS $$
  SELECT EXISTS (SELECT 1 FROM core.counseling_gate_missing(p_visit))
     AND NOT EXISTS (SELECT 1 FROM read.counseling_gate_override o WHERE o.visit_id = p_visit);
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION core.counseling_gate_blocks(uuid) TO dthcms_app, dthcms_projector;

-- The station's own `sequence_hint` decides what "step 9" means, so a clinic that reorders its
-- floor does not silently move the gate. Everything before the consultation is untouched: the
-- counselling rooms are themselves before it, and gating them would be a checkpoint that
-- refuses the patient at the door of the room where it would be satisfied.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.counseling_gates_the_consultation() RETURNS trigger
LANGUAGE plpgsql
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  target_seq int;
  gate_seq   int;
  missing    text;
BEGIN
  SELECT sequence_hint INTO target_seq
    FROM core.station WHERE facility_id = NEW.facility_id AND code = NEW.station_code;
  SELECT sequence_hint INTO gate_seq
    FROM core.station WHERE facility_id = NEW.facility_id AND code = 'STN_CONSULTATION';

  -- A facility with no consultation station has no step 9 to gate.
  IF target_seq IS NULL OR gate_seq IS NULL OR target_seq < gate_seq THEN
    RETURN NEW;
  END IF;

  IF core.counseling_gate_blocks(NEW.visit_id) THEN
    SELECT string_agg(DISTINCT m.item_code, ', ') INTO missing
      FROM core.counseling_gate_missing(NEW.visit_id) m;
    RAISE EXCEPTION 'counselling is not finished for this visit: %', missing
      USING ERRCODE = 'raise_exception',
            HINT = 'Cover the items that are missing, or have somebody who may override the '
                   'gate record why this patient is going through without them.';
  END IF;

  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER counseling_gate_guards_the_consultation
  BEFORE INSERT ON core.queue_entry
  FOR EACH ROW EXECUTE FUNCTION core.counseling_gates_the_consultation();

-- ---------------------------------------------------------------------------
-- The projection
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_counseling_gate_overridden(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.counseling_gate_override (
    id, facility_id, visit_id, patient_id,
    granted_at, granted_by, granted_role, reason, missing_at_grant, event_id, global_seq)
  VALUES (
    (p->>'override_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'visit_id')::uuid,
    (p->>'patient_id')::uuid,
    (p->>'granted_at')::timestamptz,
    (p->>'granted_by')::uuid,
    coalesce(p->>'granted_role', ''),
    p->>'reason',
    coalesce(
      (SELECT array_agg(value::text) FROM jsonb_array_elements_text(p->'missing')),
      ARRAY[]::text[]),
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (id) DO NOTHING;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_counseling_gate_overridden(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- The gate is a trigger, and a migration that dropped it while keeping the function would leave
-- a clinic with no gate **and no error** — the worst available outcome, because everybody would
-- still believe there was one. The same guard CP54's gate carries, for the same reason.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_the_counseling_gate_is_wired() RETURNS void
LANGUAGE plpgsql
SET search_path = core, pg_catalog
AS $$
DECLARE
  wired boolean;
BEGIN
  SELECT EXISTS (
    SELECT 1 FROM pg_trigger t
      JOIN pg_class c ON c.oid = t.tgrelid
      JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'core' AND c.relname = 'queue_entry'
       AND t.tgname = 'counseling_gate_guards_the_consultation'
       AND NOT t.tgisinternal)
    INTO wired;

  IF NOT wired THEN
    RAISE EXCEPTION 'the counselling gate is not wired to core.queue_entry; '
                    'a gate that is only a function is a gate nothing calls';
  END IF;
END
$$;
-- +goose StatementEnd

-- An override with no reason is the failure this whole design is arranged around: the valve is
-- acceptable *because* it is legible. The CHECK guards rows written from here on; this notices
-- one that arrived some other way.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_override_says_why() RETURNS void
LANGUAGE plpgsql
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM read.counseling_gate_override
   WHERE btrim(reason) = '' OR granted_by IS NULL;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% counselling gate overrides have no reason or no author', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_the_counseling_gate_is_wired',
   'the counselling gate is a trigger on the queue, not a check in an application', 73),
  ('assert_every_override_says_why',
   'every counselling gate override names a person and a reason', 74)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_the_counseling_gate_is_wired', 'assert_every_override_says_why');
DROP FUNCTION IF EXISTS core.assert_every_override_says_why();
DROP FUNCTION IF EXISTS core.assert_the_counseling_gate_is_wired();
DROP FUNCTION IF EXISTS read.apply_counseling_gate_overridden(jsonb);
DROP TRIGGER IF EXISTS counseling_gate_guards_the_consultation ON core.queue_entry;
DROP FUNCTION IF EXISTS core.counseling_gates_the_consultation();
DROP FUNCTION IF EXISTS core.counseling_gate_blocks(uuid);
DROP TABLE IF EXISTS read.counseling_gate_override;
DROP FUNCTION IF EXISTS core.counseling_gate_missing(uuid);
DELETE FROM core.role_permission WHERE permission_code = 'counseling.gate.override';
DELETE FROM core.permission WHERE code = 'counseling.gate.override';
