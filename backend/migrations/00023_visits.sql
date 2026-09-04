-- Visits and encounters (CP38, blueprint §3, §11.1, §14.2).
--
-- The patient's journey through the clinic as first-class data. §11.1's visit memory
-- ("which patient came when, with what problem" answerable in one query, forever), §14.2's
-- throughput analytics, and every station's context all hang from this.
--
-- The design decision that costs nothing now and buys everything later: **an encounter per
-- station touch, with a start and an end**. Bottleneck analysis is then a query rather than
-- a project, and "the counselling station is where mornings go" becomes a fact somebody can
-- act on rather than an impression.
--
-- Four rules are in the database rather than only in Go, because each is one careless
-- handler away from being violated and none of them announce themselves when they are:
--
--   * a visit's state machine, as a trigger, so an illegal transition is impossible
--   * a closed visit is not silently editable
--   * an encounter cannot end before it started, and cannot be open twice at one station
--   * a closed visit carries what §11.1 says it must

-- +goose Up

-- ---------------------------------------------------------------------------
-- The permissions the catalogue lacked
-- ---------------------------------------------------------------------------

-- A visit is not a demographic record and not an observation. Reusing
-- `patient.write.demographics` to open one would mean a physician closing a visit needs the
-- permission to rewrite a name, which is exactly the kind of over-grant §4.4 exists to stop.
-- `scope` is the third segment of the code, not a policy word: the table's CHECK requires
-- `code = resource.action[.scope]`. Where a permission is genuinely narrowed — attending is
-- one's own station only — the narrowing lives in the RBAC engine's rules, which is where
-- §4.4's station scoping already is.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('visit.open', 'visit', 'open', '',
   'Open a visit for a patient arriving at the clinic', false),
  ('visit.close', 'visit', 'close', '',
   'Close a visit, recording diagnoses, plan and the next review interval', false),
  ('visit.read', 'visit', 'read', '',
   'See a patient''s visits and the stations they passed through', false),
  ('visit.attend', 'visit', 'attend', '',
   'Start and finish an encounter at one''s own station', false)
ON CONFLICT (code) DO UPDATE SET
  resource = EXCLUDED.resource, action = EXCLUDED.action, scope = EXCLUDED.scope,
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

-- Who gets them. Registration opens; the physician and QA close; every station role attends
-- and reads, because a station operator with no visit context is an operator asking the
-- patient what they came for.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'visit.open' FROM core.role r WHERE r.code IN ('REGISTRATION', 'FIELD_WORKER', 'ADMIN')
ON CONFLICT DO NOTHING;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'visit.close' FROM core.role r WHERE r.code IN ('PHYSICIAN', 'QA', 'ADMIN')
ON CONFLICT DO NOTHING;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE p.code IN ('visit.read', 'visit.attend')
   AND r.code IN ('REGISTRATION', 'ANTHROPOMETRY', 'COUNSELOR', 'HISTORY', 'CLINICAL_ASSISTANT',
                  'JUNIOR_DOCTOR', 'RECORDS', 'NUTRITIONIST', 'EXERCISE', 'PHYSICIAN', 'QA',
                  'RX_EDUCATOR', 'CRM', 'FIELD_WORKER', 'ADMIN')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The visit
-- ---------------------------------------------------------------------------

CREATE TABLE core.visit (
  id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid        NOT NULL REFERENCES core.facility(id),
  patient_id  uuid        NOT NULL REFERENCES core.patient(id) ON DELETE RESTRICT,

  -- Spoken at a desk, like the clinical id. Per facility per clinic day, so "V-2026-0914-017"
  -- means the seventeenth patient of that morning and a queue board can say it aloud.
  visit_code  text        NOT NULL,

  -- new: first ever visit. follow_up: has been before. outreach_referral: came from a camp
  -- or a field worker, which §14 counts separately because it is a different funnel.
  visit_type  text        NOT NULL CHECK (visit_type IN ('new', 'follow_up', 'outreach_referral')),

  -- §11.1: what the patient came with, in their own words where possible. Free text on
  -- purpose — a coded complaint at the registration desk is a coded guess.
  chief_complaint text    NOT NULL DEFAULT '',

  -- open      → the patient is in the building
  -- closed    → the physician finished; §11.1's summary is recorded
  -- abandoned → the patient left before being seen. Not "closed": a visit that ended with
  --             nobody seen is a different fact, and §14.2's throughput must not count it
  --             as a completed journey
  status      text        NOT NULL DEFAULT 'open'
              CHECK (status IN ('open', 'closed', 'abandoned')),
  status_reason text      NOT NULL DEFAULT '',

  -- The clinic day this visit belongs to, in Asia/Dhaka. A separate column rather than a
  -- cast of opened_at, because a visit opened at 23:50 and closed at 00:10 belongs to one
  -- day and the queue board asks for it by that day all night.
  clinic_day  date        NOT NULL,

  opened_at   timestamptz NOT NULL DEFAULT now(),
  opened_by   uuid        NOT NULL REFERENCES core.app_user(id),
  closed_at   timestamptz,
  closed_by   uuid        REFERENCES core.app_user(id),

  -- §11.1's four, recorded at close. Diagnoses and plan are free text at this checkpoint;
  -- CP52's terminology service gives diagnoses codes, and this column keeps the words.
  diagnoses   text        NOT NULL DEFAULT '',
  plan        text        NOT NULL DEFAULT '',
  -- The next review interval, in days. §11.1 names it, and CP1xx's outreach engine reads it
  -- to decide who is due — which is why it is a number and not "in three months".
  next_review_days integer CHECK (next_review_days IS NULL OR next_review_days BETWEEN 1 AND 3650),
  next_review_on   date,

  -- Reopening. A visit reopened after close is a fact somebody has to be able to see; the
  -- policy for when it is allowed is an operational confirmation Dr. Nahid owes, and until
  -- then the code allows it only to the role that closed it and always records why.
  reopened_count integer  NOT NULL DEFAULT 0 CHECK (reopened_count >= 0),

  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT visit_code_per_facility UNIQUE (facility_id, visit_code),
  CONSTRAINT visit_closed_has_a_time
    CHECK (status <> 'closed' OR (closed_at IS NOT NULL AND closed_by IS NOT NULL)),
  CONSTRAINT visit_abandoned_has_a_reason
    CHECK (status <> 'abandoned' OR btrim(status_reason) <> ''),
  CONSTRAINT visit_closes_after_it_opens
    CHECK (closed_at IS NULL OR closed_at >= opened_at)
);

-- One open visit per patient at a time. Two would mean two queues for one person and two
-- places for the physician's note to go — and the desk would not know which.
CREATE UNIQUE INDEX visit_one_open_per_patient
  ON core.visit (patient_id) WHERE status = 'open';

CREATE INDEX visit_by_day ON core.visit (facility_id, clinic_day DESC, opened_at DESC);
CREATE INDEX visit_by_patient ON core.visit (patient_id, opened_at DESC);
CREATE INDEX visit_open ON core.visit (facility_id, opened_at) WHERE status = 'open';

COMMENT ON TABLE core.visit IS
  'One journey through the clinic. §11.1''s visit memory and §14.2''s throughput both read this (CP38).';
COMMENT ON COLUMN core.visit.clinic_day IS
  'Asia/Dhaka. Separate from opened_at because a visit spanning midnight belongs to one day.';

-- ---------------------------------------------------------------------------
-- The encounter: one station touch
-- ---------------------------------------------------------------------------

CREATE TABLE core.encounter (
  id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid        NOT NULL REFERENCES core.facility(id),
  visit_id    uuid        NOT NULL REFERENCES core.visit(id) ON DELETE RESTRICT,
  patient_id  uuid        NOT NULL REFERENCES core.patient(id) ON DELETE RESTRICT,

  station_code text       NOT NULL,

  -- in_progress → somebody is with the patient now
  -- finished    → the station is done with them
  -- bounced     → sent back, e.g. by QA (CP83). Its own state because §14.2 counts rework
  --               and a bounce recorded as "finished" makes rework invisible
  status      text        NOT NULL DEFAULT 'in_progress'
              CHECK (status IN ('in_progress', 'finished', 'bounced')),

  started_at  timestamptz NOT NULL DEFAULT now(),
  started_by  uuid        NOT NULL REFERENCES core.app_user(id),
  ended_at    timestamptz,
  ended_by    uuid        REFERENCES core.app_user(id),

  -- The role and the device, denormalised, for the same reason the timeline denormalises
  -- them: attribution resolved by a join is attribution that disappears (§8).
  started_role text       NOT NULL DEFAULT '',
  device_id    uuid,

  outcome     text        NOT NULL DEFAULT '',
  notes       text        NOT NULL DEFAULT '',

  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT encounter_ends_after_it_starts
    CHECK (ended_at IS NULL OR ended_at >= started_at),
  CONSTRAINT encounter_finished_has_an_end
    CHECK (status = 'in_progress' OR (ended_at IS NOT NULL AND ended_by IS NOT NULL))
);

-- A patient cannot be at one station twice at once. This is the concurrency rule the
-- acceptance criteria name, and it is an index rather than a check in Go because two tablets
-- pressing "start" in the same second is a race no amount of care in a handler wins.
CREATE UNIQUE INDEX encounter_one_open_per_station
  ON core.encounter (visit_id, station_code) WHERE status = 'in_progress';

CREATE INDEX encounter_by_visit ON core.encounter (visit_id, started_at);
CREATE INDEX encounter_by_station
  ON core.encounter (facility_id, station_code, started_at DESC);
CREATE INDEX encounter_open
  ON core.encounter (facility_id, station_code) WHERE status = 'in_progress';

COMMENT ON TABLE core.encounter IS
  'One station touch, with start, end and attribution. What makes §14.2 bottleneck analysis a query (CP38).';

-- ---------------------------------------------------------------------------
-- The state machine, in the database
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.visit_transition_is_legal()
RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = OLD.status THEN
    -- Not a transition. But a *closed* visit is not silently editable either: a closed
    -- record that changes without saying so is the failure §4.3 exists to prevent, and the
    -- reopen path is what a correction goes through.
    IF OLD.status = 'closed' AND NEW.reopened_count = OLD.reopened_count THEN
      IF NEW.chief_complaint IS DISTINCT FROM OLD.chief_complaint
         OR NEW.diagnoses IS DISTINCT FROM OLD.diagnoses
         OR NEW.plan IS DISTINCT FROM OLD.plan
         OR NEW.next_review_days IS DISTINCT FROM OLD.next_review_days
         OR NEW.visit_type IS DISTINCT FROM OLD.visit_type THEN
        RAISE EXCEPTION 'visit % is closed and cannot be edited in place', OLD.id
          USING HINT = 'Reopen it, which is recorded, or record a correction. A closed '
                       'visit that changes without saying so is what §4.3 forbids (CP38).';
      END IF;
    END IF;
    RETURN NEW;
  END IF;

  -- open      → closed, abandoned
  -- closed    → open  (a reopen, which must increment reopened_count)
  -- abandoned → open  (the patient came back the same day)
  IF NOT (
       (OLD.status = 'open'      AND NEW.status IN ('closed', 'abandoned'))
    OR (OLD.status = 'closed'    AND NEW.status = 'open')
    OR (OLD.status = 'abandoned' AND NEW.status = 'open')
  ) THEN
    RAISE EXCEPTION 'a visit cannot go from % to %', OLD.status, NEW.status
      USING HINT = 'Legal transitions: open→closed, open→abandoned, closed→open (a reopen), '
                   'abandoned→open (CP38).';
  END IF;

  IF OLD.status = 'closed' AND NEW.status = 'open'
     AND NEW.reopened_count <= OLD.reopened_count THEN
    RAISE EXCEPTION 'reopening visit % must record that it was reopened', OLD.id
      USING HINT = 'reopened_count is how a reader knows this visit was closed once (CP38).';
  END IF;

  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER visit_transition_is_legal
  BEFORE UPDATE ON core.visit
  FOR EACH ROW EXECUTE FUNCTION core.visit_transition_is_legal();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.encounter_transition_is_legal()
RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status <> OLD.status AND OLD.status <> 'in_progress' THEN
    RAISE EXCEPTION 'encounter % is % and cannot change to %', OLD.id, OLD.status, NEW.status
      USING HINT = 'An encounter ends once. A patient returning to a station is a new '
                   'encounter, which is what makes rework countable (CP38, §14.2).';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER encounter_transition_is_legal
  BEFORE UPDATE ON core.encounter
  FOR EACH ROW EXECUTE FUNCTION core.encounter_transition_is_legal();

-- Neither is ever deleted: an encounter is evidence of who saw a patient and when.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.visits_are_not_deleted()
RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'visits and encounters are never deleted'
    USING HINT = 'A visit that ended with nobody seen is `abandoned`, which is a fact. '
                 'Deleting it removes evidence of who was in the building (CP38).';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER visit_undeletable BEFORE DELETE ON core.visit
  FOR EACH ROW EXECUTE FUNCTION core.visits_are_not_deleted();
CREATE TRIGGER encounter_undeletable BEFORE DELETE ON core.encounter
  FOR EACH ROW EXECUTE FUNCTION core.visits_are_not_deleted();

GRANT SELECT, INSERT, UPDATE ON core.visit TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON core.encounter TO dthcms_app;

-- ---------------------------------------------------------------------------
-- The visit code: gapless per facility per clinic day
-- ---------------------------------------------------------------------------

CREATE TABLE core.visit_counter (
  facility_id uuid NOT NULL REFERENCES core.facility(id),
  clinic_day  date NOT NULL,
  next_value  integer NOT NULL DEFAULT 1 CHECK (next_value >= 1),
  PRIMARY KEY (facility_id, clinic_day)
);

GRANT SELECT, INSERT, UPDATE ON core.visit_counter TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'visit_counter', 'Keyed by facility already; it is the counter, not a facility''s data.')
ON CONFLICT DO NOTHING;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.next_visit_code(p_facility uuid, p_day date)
RETURNS text
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = core, pg_catalog
AS $$
DECLARE
  v_next integer;
BEGIN
  -- Under a row lock, like the clinical id (CP28). Two registration desks opening a visit in
  -- the same millisecond must not produce the same code, and a sequence would leave gaps a
  -- desk would read as lost patients.
  INSERT INTO core.visit_counter (facility_id, clinic_day, next_value)
  VALUES (p_facility, p_day, 2)
  ON CONFLICT (facility_id, clinic_day) DO UPDATE
    SET next_value = core.visit_counter.next_value + 1
  RETURNING next_value - 1 INTO v_next;

  RETURN 'V-' || to_char(p_day, 'YYYY-MMDD') || '-' || lpad(v_next::text, 3, '0');
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION core.next_visit_code(uuid, date) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.next_visit_code(uuid, date) TO dthcms_app;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_closed_visits_are_complete() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  -- §11.1: every visit records date, chief complaint, diagnoses, plan and the next review
  -- interval. A closed visit missing them is a visit that cannot answer "which patient came
  -- when, with what problem" — which is the whole point of recording it.
  SELECT count(*) INTO offending
  FROM core.visit
  WHERE status = 'closed'
    AND (btrim(chief_complaint) = '' OR btrim(diagnoses) = '' OR btrim(plan) = ''
         OR next_review_days IS NULL);

  IF offending > 0 THEN
    RAISE EXCEPTION 'closed visits missing §11.1''s summary: % row(s)', offending
      USING HINT = 'A closed visit records the complaint, the diagnoses, the plan and the '
                   'next review interval (CP38, §11.1).';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_encounters_are_timed() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  SELECT count(*) INTO offending
  FROM core.encounter
  WHERE (ended_at IS NOT NULL AND ended_at < started_at)
     OR btrim(station_code) = ''
     OR started_by IS NULL;

  IF offending > 0 THEN
    RAISE EXCEPTION 'encounters with impossible timing or no attribution: % row(s)', offending
      USING HINT = 'An encounter is a station touch with a start, an end and a person. '
                   'Without all three §14.2''s bottleneck analysis is guesswork (CP38).';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_closed_visits_are_complete() IS
  'Raises if a closed visit is missing §11.1''s summary (CP38).';
COMMENT ON FUNCTION core.assert_encounters_are_timed() IS
  'Raises if an encounter has impossible timing or no attribution (CP38).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_closed_visits_are_complete', 'every closed visit carries §11.1''s summary', 38),
  ('assert_encounters_are_timed', 'every encounter has a start, an end and a person', 39)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN
  ('assert_closed_visits_are_complete', 'assert_encounters_are_timed');
DROP FUNCTION IF EXISTS core.assert_encounters_are_timed();
DROP FUNCTION IF EXISTS core.assert_closed_visits_are_complete();
DROP FUNCTION IF EXISTS core.next_visit_code(uuid, date);
DELETE FROM core.facility_scope_exemption WHERE schema_name = 'core' AND table_name = 'visit_counter';
DROP TABLE IF EXISTS core.visit_counter;
DROP TRIGGER IF EXISTS encounter_undeletable ON core.encounter;
DROP TRIGGER IF EXISTS visit_undeletable ON core.visit;
DROP FUNCTION IF EXISTS core.visits_are_not_deleted();
DROP TRIGGER IF EXISTS encounter_transition_is_legal ON core.encounter;
DROP FUNCTION IF EXISTS core.encounter_transition_is_legal();
DROP TRIGGER IF EXISTS visit_transition_is_legal ON core.visit;
DROP FUNCTION IF EXISTS core.visit_transition_is_legal();
DROP TABLE IF EXISTS core.encounter;
DROP TABLE IF EXISTS core.visit;
DELETE FROM core.role_permission WHERE permission_code IN
  ('visit.open', 'visit.close', 'visit.read', 'visit.attend');
DELETE FROM core.permission WHERE code IN
  ('visit.open', 'visit.close', 'visit.read', 'visit.attend');
