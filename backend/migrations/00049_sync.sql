-- The offline sync protocol, server side (CP65, §15.2, §7.2).
--
-- # What the event store already gives, and what is left
--
-- Two of the five acceptance criteria are already true before this migration exists, and it is
-- worth saying which so that nothing here re-implements them:
--
--   * **"Duplicate submission produces no duplicate events."** `EventID` is client-generated and is
--     the ledger's primary key; `Append` returns the first row with `Duplicate: true`. A batch
--     replayed in full lands as forty duplicates and no new rows.
--   * **"`occurred_at` is preserved; `recorded_at` reflects arrival."** The envelope has carried
--     both since CP23, for exactly this checkpoint.
--
-- What is left is the batch: **each event independently**, ordering within an aggregate, a receipt
-- so that a client which lost the response can ask what happened rather than guess, and the one
-- genuinely hard case — a device that was revoked while it was offline.
--
-- # The revoked device, which is the whole reason this migration has tables
--
-- A station tablet is revoked at nine in the morning. It has been offline since eight and it holds
-- forty real blood pressures, taken by a real operator, on real patients who have gone home.
--
-- Accepting them defeats revocation: whatever made somebody revoke that device — it was stolen,
-- the operator left, the key leaked — is exactly the reason not to trust what it sends. Dropping
-- them loses a morning of clinical measurements and, worse, loses them *silently*: the operator
-- believes the work is recorded, and nobody discovers otherwise until a physician wonders why a
-- patient has no vitals.
--
-- So neither. The events are **held, in full, outside the ledger**, and somebody with authority
-- decides. That is `ops.sync_quarantine`, and everything about its shape follows from the two
-- things it must never do: lose the content, and let it into the record without a person.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The receipt
-- ---------------------------------------------------------------------------

-- What happened to one batch, kept so a client can ask again.
--
-- The case this exists for is not a duplicate submission — the ledger already absorbs those. It is
-- the client that sent fifty events, the server processed them, and the response was lost on the
-- way back. Without a receipt that client has two choices: resend and hope, or drop and hope. With
-- one it asks what happened to batch `…` and is told, per event.
CREATE TABLE ops.sync_batch (
  id uuid PRIMARY KEY,

  device_id   uuid NOT NULL REFERENCES core.device(id),
  user_id     uuid NOT NULL REFERENCES core.app_user(id),
  facility_id uuid NOT NULL,

  received_at timestamptz NOT NULL DEFAULT now(),

  -- What the device's own clock said when it sent this. Kept rather than only compared, because
  -- "this device was four minutes fast on Tuesday" is a thing somebody investigating a
  -- misordered timeline needs to be able to look up afterwards.
  client_clock timestamptz,
  -- Positive when the device is ahead of the server.
  skew_ms bigint,

  -- When the last event got its answer. Null for a batch that was opened and never finished —
  -- a server interrupted halfway. The distinction is load-bearing: a receipt is answered from the
  -- record only when it is closed, because an open one reports nothing, and telling a client "your
  -- fifty events produced no results" for ever is worse than reprocessing them (which is safe,
  -- since event_id is the ledger's key).
  closed_at   timestamptz,

  events      integer NOT NULL DEFAULT 0,
  accepted    integer NOT NULL DEFAULT 0,
  duplicated  integer NOT NULL DEFAULT 0,
  rejected    integer NOT NULL DEFAULT 0,
  quarantined integer NOT NULL DEFAULT 0,
  blocked     integer NOT NULL DEFAULT 0,

  CONSTRAINT sync_batch_counts_add_up
    CHECK (accepted + duplicated + rejected + quarantined + blocked = events)
);

CREATE INDEX sync_batch_by_device ON ops.sync_batch (device_id, received_at DESC);

GRANT SELECT, INSERT, UPDATE ON ops.sync_batch TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('ops', 'sync_batch', 'Carries its own facility_id; it is a delivery receipt rather than a clinical record.')
ON CONFLICT DO NOTHING;

-- The per-event answer, so a receipt can be re-read rather than recomputed.
CREATE TABLE ops.sync_result (
  batch_id uuid NOT NULL REFERENCES ops.sync_batch(id) ON DELETE CASCADE,
  event_id uuid NOT NULL,

  -- ACCEPTED: it is in the ledger now.
  -- DUPLICATE: it was already there. Not an error — it is what a resent batch looks like.
  -- REJECTED: it will never be accepted as sent, and `reason` says why.
  -- QUARANTINED: it is being held for a person to decide about.
  -- BLOCKED: an earlier event for the same aggregate failed and this one declared it depends on
  --   that aggregate's state, so attempting it would only produce a second failure. The client
  --   keeps it and sends it again.
  outcome text NOT NULL
    CHECK (outcome IN ('ACCEPTED', 'DUPLICATE', 'REJECTED', 'QUARANTINED', 'BLOCKED')),

  -- A machine code the client can branch on, and a sentence a person can read. Both, because a
  -- client that had only prose would string-match it and a person who had only a code would have
  -- to look it up.
  reason_code text NOT NULL DEFAULT '',
  reason      text NOT NULL DEFAULT '',

  -- Where it landed, for the ones that did. Stored as well as returned, because the receipt
  -- exists for the client that lost the response — and that client needs the cursor as much as
  -- the outcome, or it pulls its own writes back down.
  global_seq bigint,

  PRIMARY KEY (batch_id, event_id),
  CONSTRAINT sync_result_failures_say_why
    CHECK (outcome IN ('ACCEPTED', 'DUPLICATE') OR btrim(reason) <> '')
);

GRANT SELECT, INSERT ON ops.sync_result TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('ops', 'sync_result', 'Scoped by the batch it belongs to.')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The quarantine
-- ---------------------------------------------------------------------------

-- Events that were not accepted and must not be lost.
--
-- **The payload is here in full, and that is deliberate**, which makes this the one table outside
-- `ledger` and `read` that can hold clinical values. The alternative is a row saying "forty events
-- from device X were refused" and no way to recover them, which is silent loss wearing a table's
-- clothes. The consequences are taken seriously rather than waved at: reading it needs its own
-- sensitive permission, releasing it needs another, and nothing here is ever deleted.
CREATE TABLE ops.sync_quarantine (
  id       uuid PRIMARY KEY,
  batch_id uuid NOT NULL REFERENCES ops.sync_batch(id),
  event_id uuid NOT NULL,

  device_id   uuid NOT NULL REFERENCES core.device(id),
  user_id     uuid NOT NULL REFERENCES core.app_user(id),
  facility_id uuid NOT NULL,

  -- Why it was held. Not a free string: a quarantine somebody has to read prose to categorise is
  -- one nobody triages.
  reason_code text NOT NULL CHECK (reason_code IN
    ('DEVICE_REVOKED', 'DEVICE_SUSPENDED', 'CLOCK_IMPLAUSIBLE', 'UNKNOWN_EVENT_TYPE')),
  reason      text NOT NULL,

  -- The event as it was sent, whole. `envelope` is everything the ledger would have needed.
  envelope jsonb NOT NULL,
  -- Denormalised so the triage list can be read and ordered without opening every envelope, and
  -- so a supervisor can see "eleven blood pressures and two weights" rather than eleven uuids.
  event_type  text NOT NULL,
  occurred_at timestamptz NOT NULL,
  patient_id  uuid,

  held_at timestamptz NOT NULL DEFAULT now(),

  -- RELEASED: appended to the ledger, with its original occurred_at and actor.
  -- DISCARDED: deliberately not appended, by a named person, with a reason.
  status text NOT NULL DEFAULT 'HELD'
    CHECK (status IN ('HELD', 'RELEASED', 'DISCARDED')),
  resolved_at     timestamptz,
  resolved_by     uuid REFERENCES core.app_user(id),
  resolution_note text NOT NULL DEFAULT '',
  -- What it became. Set on release, so the quarantine row points at the ledger row and the
  -- question "what happened to that morning's blood pressures" has an answer that is a link.
  released_event_id uuid,

  CONSTRAINT sync_quarantine_resolution_is_whole
    CHECK ((status = 'HELD') = (resolved_at IS NULL)
           AND (status = 'HELD') = (resolved_by IS NULL)),
  -- A discarded clinical measurement with no explanation is exactly the silent loss this table
  -- exists to prevent, one step later.
  CONSTRAINT sync_quarantine_discard_says_why
    CHECK (status <> 'DISCARDED' OR btrim(resolution_note) <> ''),
  CONSTRAINT sync_quarantine_release_names_the_event
    CHECK ((status = 'RELEASED') = (released_event_id IS NOT NULL))
);

CREATE INDEX sync_quarantine_held ON ops.sync_quarantine (facility_id, held_at DESC)
  WHERE status = 'HELD';
CREATE INDEX sync_quarantine_by_device ON ops.sync_quarantine (device_id, held_at DESC);
CREATE UNIQUE INDEX sync_quarantine_one_per_event ON ops.sync_quarantine (event_id)
  WHERE status <> 'DISCARDED';

GRANT SELECT, INSERT, UPDATE ON ops.sync_quarantine TO dthcms_app;

-- Nothing is ever deleted from here, for the same reason nothing is deleted from the audit trail:
-- a record of what was refused is only worth having if it cannot be tidied away.
REVOKE DELETE ON ops.sync_quarantine FROM dthcms_app;

-- ---------------------------------------------------------------------------
-- Where each device has got to
-- ---------------------------------------------------------------------------

-- The pull cursor, and the last thing each device said.
--
-- Kept on the server as well as on the device because a phone that was wiped and re-enrolled has
-- lost its cursor, and "start from the beginning" for a device that has been in service a year is
-- a download nobody wants. It is also the row an operator reads to answer "has that tablet synced
-- today", which is the first question asked when somebody's work is missing.
CREATE TABLE ops.device_sync_state (
  device_id uuid PRIMARY KEY REFERENCES core.device(id),

  last_pulled_seq bigint      NOT NULL DEFAULT 0,
  last_pulled_at  timestamptz,
  last_pushed_at  timestamptz,
  last_skew_ms    bigint,

  -- Running totals, so the operations view can say "this device has had eleven events quarantined
  -- since Tuesday" without scanning the quarantine.
  pushed_total      bigint NOT NULL DEFAULT 0,
  quarantined_total bigint NOT NULL DEFAULT 0
);

GRANT SELECT, INSERT, UPDATE ON ops.device_sync_state TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('ops', 'device_sync_state', 'Scoped by the device, which belongs to a facility.'),
  ('ops', 'sync_quarantine', 'Carries its own facility_id.')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- Criterion 5, standing: *quarantined and surfaced, never silently dropped.* A resolved row that
-- names nobody is a decision with no author, which is the shape silent loss takes once somebody
-- has built a screen to prevent it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_quarantine_decision_has_an_author() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM ops.sync_quarantine
   WHERE status <> 'HELD' AND (resolved_by IS NULL OR resolved_at IS NULL);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% quarantined events were resolved by nobody', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- A released event has to actually be in the ledger. Without this, "released" could be a status
-- somebody set while the append failed, and the clinical measurement would be marked as recovered
-- and not be there — which is worse than never releasing it, because now nobody is looking.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_released_event_reached_the_ledger() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM ops.sync_quarantine q
   WHERE q.status = 'RELEASED'
     AND NOT EXISTS (SELECT 1 FROM ledger.event e WHERE e.event_id = q.released_event_id);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% events are marked released and are not in the ledger', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- The receipt must agree with itself, or a client reading it would be told a different story from
-- the one the server acted out.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_sync_receipt_adds_up() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM ops.sync_batch b
   WHERE b.events <> (SELECT count(*) FROM ops.sync_result r WHERE r.batch_id = b.id);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% sync receipts disagree with their own per-event results', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_quarantine_decision_has_an_author',
   'every quarantined event that was released or discarded names the person who decided', 94),
  ('assert_every_released_event_reached_the_ledger',
   'every event marked released is actually in the ledger', 95),
  ('assert_every_sync_receipt_adds_up',
   'every sync receipt agrees with its own per-event results', 96)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- ---------------------------------------------------------------------------
-- Who may see what was held, and who may let it in
-- ---------------------------------------------------------------------------

-- Reading is **sensitive**, and it is the only permission in this system that lets somebody see
-- clinical values outside the ledger. The quarantine holds whole envelopes, so `sync.quarantine.read`
-- is a blood pressure on a screen; an access review should have to justify it in those terms.
--
-- Releasing is separate and narrower. Letting an event from a revoked device into the permanent
-- clinical record is a decision about trust rather than about data — the question is not "is this
-- number plausible" but "do we believe this device was in the hands of the person whose name is on
-- it" — and it is the one act in this checkpoint somebody has to answer for.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('sync.quarantine.read', 'sync', 'quarantine', 'read',
   'See offline events that were held rather than accepted, including their clinical content', true),
  ('sync.quarantine.release', 'sync', 'quarantine', 'release',
   'Decide whether a held offline event enters the permanent record', true)
ON CONFLICT (code) DO UPDATE SET description = EXCLUDED.description;

-- The physician reads it because the held events are clinical measurements on their patients and
-- the question "should this be in the record" is a clinical one. The administrator reads it
-- because they revoked the device and are the one who knows why.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'sync.quarantine.read'
  FROM core.role r
 WHERE r.code IN ('PHYSICIAN', 'ADMIN')
ON CONFLICT DO NOTHING;

-- Releasing is the physician's alone, and the asymmetry with the administrator is the point: the
-- administrator revoked the device, and somebody who can both refuse a device and then admit its
-- data has undone their own control. The person who decides a measurement belongs in a patient's
-- record should be the person who is answerable for that record.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'sync.quarantine.release'
  FROM core.role r
 WHERE r.code = 'PHYSICIAN'
ON CONFLICT DO NOTHING;

-- +goose Down

DELETE FROM core.role_permission
 WHERE permission_code IN ('sync.quarantine.read', 'sync.quarantine.release');
DELETE FROM core.permission
 WHERE code IN ('sync.quarantine.read', 'sync.quarantine.release');
DELETE FROM ops.invariant WHERE function_name IN (
  'assert_every_quarantine_decision_has_an_author',
  'assert_every_released_event_reached_the_ledger',
  'assert_every_sync_receipt_adds_up');
DROP FUNCTION IF EXISTS core.assert_every_sync_receipt_adds_up();
DROP FUNCTION IF EXISTS core.assert_every_released_event_reached_the_ledger();
DROP FUNCTION IF EXISTS core.assert_every_quarantine_decision_has_an_author();
DROP TABLE IF EXISTS ops.device_sync_state;
DROP TABLE IF EXISTS ops.sync_quarantine;
DROP TABLE IF EXISTS ops.sync_result;
DROP TABLE IF EXISTS ops.sync_batch;
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'ops'
   AND table_name IN ('sync_batch', 'sync_result', 'sync_quarantine', 'device_sync_state');
