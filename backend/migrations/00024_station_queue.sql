-- The station queue (CP39, blueprint §5.2, §5.5, §14.2).
--
-- Know where every patient is, and what is next. The Clinic Traffic Control board (CP40), the
-- counselling gate (§5.5) and throughput analytics all read this.
--
-- The whole checkpoint turns on one sentence in the acceptance criteria: *no patient is ever
-- assigned to two operators at the same station*. Two operators pressing "call next" in the
-- same second is the ordinary case, not the edge case — a station with two chairs does it all
-- morning — and it is a race no amount of care in a handler wins.
--
-- So the claim is `UPDATE ... WHERE status = 'waiting'` on **one row chosen under
-- `FOR UPDATE SKIP LOCKED`**. The first caller locks the head of the queue and takes it; the
-- second does not block behind it and does not get the same row — it skips to the next
-- waiting patient, or finds none. That is the correct behaviour at a desk as well as the
-- correct behaviour in the database: the second operator wants *a* patient, not *that* patient.

-- +goose Up

CREATE TABLE core.queue_entry (
  id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid        NOT NULL REFERENCES core.facility(id),
  visit_id    uuid        NOT NULL REFERENCES core.visit(id) ON DELETE RESTRICT,
  patient_id  uuid        NOT NULL REFERENCES core.patient(id) ON DELETE RESTRICT,

  station_code text       NOT NULL,
  -- Where this station sits in the patient's planned journey. Used for ordering the board
  -- and for "what is next", not for enforcement: a patient sent back from QA has a position
  -- behind them, and refusing that would be refusing the clinic's actual flow.
  position    integer     NOT NULL DEFAULT 0,

  -- waiting   → in the queue
  -- called    → an operator has claimed them; they are being fetched
  -- in_service→ the encounter is open
  -- done      → this station is finished with them
  -- skipped   → not needed for this patient
  -- rerouted  → sent somewhere else, with a reason
  status      text        NOT NULL DEFAULT 'waiting'
              CHECK (status IN ('waiting', 'called', 'in_service', 'done', 'skipped', 'rerouted')),

  -- 0 is ordinary. Higher jumps the queue: §4.4's critical findings, and a physician's own
  -- judgement. An integer rather than a boolean because "urgent" and "this one now" are
  -- different, and a clinic will discover it needs the difference.
  priority    integer     NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 9),
  priority_reason text    NOT NULL DEFAULT '',

  -- The clock the whole board runs on. `entered_at` is when the patient joined *this*
  -- queue, which is what a waiting time is measured from — not when the visit opened.
  entered_at  timestamptz NOT NULL DEFAULT now(),
  called_at   timestamptz,
  called_by   uuid        REFERENCES core.app_user(id),
  started_at  timestamptz,
  ended_at    timestamptz,

  -- Set when the entry leaves the queue for a reason a person has to give.
  outcome        text     NOT NULL DEFAULT '',
  outcome_reason text     NOT NULL DEFAULT '',
  rerouted_to    text     NOT NULL DEFAULT '',
  -- The encounter this entry produced, once one exists (CP38).
  encounter_id   uuid     REFERENCES core.encounter(id),

  clinic_day  date        NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT queue_called_has_a_caller
    CHECK (status <> 'called' OR (called_at IS NOT NULL AND called_by IS NOT NULL)),
  CONSTRAINT queue_reroute_has_a_reason
    CHECK (status <> 'rerouted' OR (btrim(outcome_reason) <> '' AND btrim(rerouted_to) <> '')),
  CONSTRAINT queue_priority_has_a_reason
    CHECK (priority = 0 OR btrim(priority_reason) <> ''),
  CONSTRAINT queue_times_are_ordered
    CHECK ((called_at IS NULL OR called_at >= entered_at)
       AND (started_at IS NULL OR called_at IS NULL OR started_at >= called_at)
       AND (ended_at IS NULL OR started_at IS NULL OR ended_at >= started_at))
);

-- One live entry per visit per station. A patient waiting twice at one station is a patient
-- who will be called twice, and the second call is the one nobody can explain.
CREATE UNIQUE INDEX queue_one_live_per_station
  ON core.queue_entry (visit_id, station_code)
  WHERE status IN ('waiting', 'called', 'in_service');

-- The board's query, and the ordering the claim uses: priority first, then arrival. A DESC
-- on priority with an ASC on entered_at in one index is what makes "call next" an index scan
-- of exactly one row.
CREATE INDEX queue_next_at_station
  ON core.queue_entry (facility_id, station_code, priority DESC, entered_at)
  WHERE status = 'waiting';

CREATE INDEX queue_by_day ON core.queue_entry (facility_id, clinic_day, station_code);
CREATE INDEX queue_by_visit ON core.queue_entry (visit_id, position);
CREATE INDEX queue_live
  ON core.queue_entry (facility_id, station_code, status)
  WHERE status IN ('waiting', 'called', 'in_service');

COMMENT ON TABLE core.queue_entry IS
  'Where every patient is and what is next. The traffic board, the counselling gate and throughput all read this (CP39).';
COMMENT ON COLUMN core.queue_entry.entered_at IS
  'When the patient joined *this* queue. A waiting time is measured from here, not from when the visit opened.';

-- ---------------------------------------------------------------------------
-- Call next: one row, under SKIP LOCKED
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.call_next_at_station(
  p_facility uuid, p_station text, p_user uuid, p_at timestamptz)
-- SETOF, not a bare composite. A function returning a composite returns a row of NULLs when
-- it has nothing, and `SELECT * FROM f()` then hands the driver a row it cannot scan — an
-- empty queue would be a 500. SETOF returns *no rows*, which is what an empty queue is.
RETURNS SETOF core.queue_entry
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = core, pg_catalog
AS $$
DECLARE
  v_id uuid;
BEGIN
  -- The claim. `FOR UPDATE SKIP LOCKED` is the whole mechanism: the first caller locks the
  -- head of the queue and takes it; the second does not block behind it and does not get the
  -- same row — it skips to the next waiting patient. Which is what the second operator
  -- actually wants: *a* patient, not *that* patient.
  SELECT id INTO v_id
    FROM core.queue_entry
   WHERE facility_id = p_facility AND station_code = p_station AND status = 'waiting'
   ORDER BY priority DESC, entered_at, id
   FOR UPDATE SKIP LOCKED
   LIMIT 1;

  IF v_id IS NULL THEN
    RETURN;
  END IF;

  RETURN QUERY
    UPDATE core.queue_entry
       SET status = 'called', called_at = p_at, called_by = p_user
     WHERE id = v_id AND status = 'waiting'
    RETURNING *;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION core.call_next_at_station(uuid, text, uuid, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.call_next_at_station(uuid, text, uuid, timestamptz) TO dthcms_app;

-- ---------------------------------------------------------------------------
-- The station sequence per visit type
-- ---------------------------------------------------------------------------

-- Which stations a visit of each type is planned to pass through, in order.
--
-- A table rather than a constant because the sequences are an **operational confirmation**
-- Dr. Nahid owes, and a sequence in Go is a sequence that needs a deployment to change. What
-- is seeded here is §3's twelve-station journey as the blueprint describes it; a follow-up
-- skips the stations a returning patient does not repeat.
CREATE TABLE core.station_sequence (
  facility_id uuid    NOT NULL REFERENCES core.facility(id),
  visit_type  text    NOT NULL CHECK (visit_type IN ('new', 'follow_up', 'outreach_referral')),
  position    integer NOT NULL CHECK (position >= 1),
  station_code text   NOT NULL,
  -- Whether a patient may leave without passing through. §5.5's counselling gate will make
  -- one of these false and the QA gate another; until those checkpoints land they are all
  -- advisory and the column says so honestly.
  required    boolean NOT NULL DEFAULT true,

  PRIMARY KEY (facility_id, visit_type, position),
  CONSTRAINT station_sequence_once UNIQUE (facility_id, visit_type, station_code)
);

GRANT SELECT ON core.station_sequence TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON core.queue_entry TO dthcms_app;

COMMENT ON TABLE core.station_sequence IS
  'The planned journey per visit type. Data, not code: the sequences are an operational decision (CP39).';

INSERT INTO core.station_sequence (facility_id, visit_type, position, station_code, required)
SELECT core.default_facility(), 'new', position, code, true
  FROM (VALUES
    (1, 'STN_REGISTRATION'), (2, 'STN_ANTHROPOMETRY'), (3, 'STN_COUNSELING'),
    (4, 'STN_HISTORY'), (5, 'STN_EXAMINATION'), (6, 'STN_RECORDS'),
    (7, 'STN_NUTRITION'), (8, 'STN_EXERCISE'), (9, 'STN_CONSULTATION'),
    (10, 'STN_QA'), (11, 'STN_RX_EDUCATION'), (12, 'STN_FOLLOWUP')
  ) AS s(position, code)
ON CONFLICT DO NOTHING;

-- A returning patient does not repeat the history and records stations; everything clinical
-- is repeated because the numbers are what a follow-up is for.
INSERT INTO core.station_sequence (facility_id, visit_type, position, station_code, required)
SELECT core.default_facility(), 'follow_up', position, code, true
  FROM (VALUES
    (1, 'STN_REGISTRATION'), (2, 'STN_ANTHROPOMETRY'), (3, 'STN_COUNSELING'),
    (4, 'STN_EXAMINATION'), (5, 'STN_CONSULTATION'), (6, 'STN_QA'),
    (7, 'STN_RX_EDUCATION'), (8, 'STN_FOLLOWUP')
  ) AS s(position, code)
ON CONFLICT DO NOTHING;

INSERT INTO core.station_sequence (facility_id, visit_type, position, station_code, required)
SELECT core.default_facility(), 'outreach_referral', position, code, true
  FROM (VALUES
    (1, 'STN_REGISTRATION'), (2, 'STN_ANTHROPOMETRY'), (3, 'STN_HISTORY'),
    (4, 'STN_EXAMINATION'), (5, 'STN_CONSULTATION'), (6, 'STN_QA'),
    (7, 'STN_RX_EDUCATION'), (8, 'STN_FOLLOWUP')
  ) AS s(position, code)
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_queue_claims_are_exclusive() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  -- The acceptance criterion, as a standing check: a visit waiting or called twice at one
  -- station is a patient two operators can fetch.
  SELECT count(*) INTO offending
  FROM (
    SELECT visit_id, station_code
      FROM core.queue_entry
     WHERE status IN ('waiting', 'called', 'in_service')
     GROUP BY visit_id, station_code
    HAVING count(*) > 1
  ) AS duplicated;

  IF offending > 0 THEN
    RAISE EXCEPTION 'patients live twice in one station queue: % case(s)', offending
      USING HINT = 'One live queue entry per visit per station. A patient waiting twice is '
                   'a patient who will be called twice, and the second call is the one '
                   'nobody can explain (CP39).';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_queue_departures_are_explained() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  SELECT count(*) INTO offending
  FROM core.queue_entry
  WHERE (status = 'rerouted' AND (btrim(outcome_reason) = '' OR btrim(rerouted_to) = ''))
     OR (priority > 0 AND btrim(priority_reason) = '');

  IF offending > 0 THEN
    RAISE EXCEPTION 'queue reroutes or priorities with no reason: % row(s)', offending
      USING HINT = 'A reroute says where and why; a priority says why. Jumping a queue '
                   'without a reason is the thing a queue exists to prevent (CP39).';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_queue_claims_are_exclusive() IS
  'Raises if a patient is live twice in one station queue (CP39).';
COMMENT ON FUNCTION core.assert_queue_departures_are_explained() IS
  'Raises if a reroute or a priority has no reason (CP39).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_queue_claims_are_exclusive', 'no patient is live twice in one station queue', 40),
  ('assert_queue_departures_are_explained', 'every reroute and every priority says why', 41)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN
  ('assert_queue_claims_are_exclusive', 'assert_queue_departures_are_explained');
DROP FUNCTION IF EXISTS core.assert_queue_departures_are_explained();
DROP FUNCTION IF EXISTS core.assert_queue_claims_are_exclusive();
DROP FUNCTION IF EXISTS core.call_next_at_station(uuid, text, uuid, timestamptz);
DROP TABLE IF EXISTS core.station_sequence;
DROP TABLE IF EXISTS core.queue_entry;
