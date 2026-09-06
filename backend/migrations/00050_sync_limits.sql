-- A ceiling on what one device may leave behind (CP65, security line: per-device rate limits).
--
-- # The hole this closes
--
-- CP65 opened exactly one route to a device the clinic has **refused**: `POST /v1/sync/events`,
-- so that a tablet revoked while it was offline can hand over the measurements it is holding
-- instead of destroying them. Everything that arrives that way is held in `ops.sync_quarantine`,
-- and `dthcms_app` has no DELETE there — deliberately, because a record of what was refused is
-- only worth having if it cannot be tidied away.
--
-- Those two facts together are a problem nobody had before them. A stolen tablet with a live key
-- can now append permanent rows to a table that is never pruned, and every row is one a supervisor
-- has to read and decide about. An honest client stops when its backlog is delivered. A hostile
-- one has no reason to, and the damage is not "the disk filled" — it is a triage list nobody can
-- finish, which is how the quarantine stops being read at all.
--
-- # Why this is in the database and not only in Go
--
-- The Redis rate limiter added alongside this (see `syncRateLimits` in cmd/api) bounds the *rate*,
-- and it **fails open**: a clinic that stops taking blood pressures because Redis restarted is a
-- worse system than one that runs a minute unlimited. That is a defensible trade only if the
-- consequence it was guarding is bounded somewhere that cannot fail open. This is that somewhere.
--
-- It is also the layer that is true regardless of which process is writing. The service checks the
-- cap so that it can answer a client politely, in the receipt, in a way the client can act on. The
-- trigger is what makes the cap *a fact about the table* rather than a promise one code path keeps.
--
-- # What happens at the ceiling
--
-- The event is **not** discarded, and it is not held either — it is refused in a way that tells the
-- client to keep it and come back. `BLOCKED`, with `QUARANTINE_FULL`, which is the outcome the
-- protocol already has for "not attempted, send it again". The alternative — rejecting it — means
-- a conforming client drops a real blood pressure because a supervisor is behind on their reading,
-- which is precisely the silent loss the quarantine exists to prevent, arriving by a new door.
--
-- So the ceiling is back-pressure, not a bin. It is cleared by a person working through the triage
-- list, which is the right thing for it to be tied to: the resource actually being protected is
-- somebody's attention.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The cap
-- ---------------------------------------------------------------------------

-- One number, in one place, read by the trigger that enforces it and by the service that explains
-- it. A constant in Go and a literal in a trigger would be two numbers that agree until somebody
-- changes one.
--
-- Two thousand held events for one device. The shape of the number matters more than the value:
--
--   * An honest tablet never approaches it. A station generates a couple of hundred events a day,
--     and only a *refused* device has all of them held — a fortnight offline and revoked is under
--     three thousand, which is already the outlier this is sized for.
--   * A supervisor cannot work through two thousand held events in a sitting, so by the time a
--     device reaches this, the answer is not "let it write more" — it is "somebody has to look at
--     this device".
--   * A hostile device writes two thousand rows, once, and then nothing. At roughly two kilobytes
--     an envelope that is four megabytes per device, bounded, and recoverable by a person rather
--     than by a database administrator at three in the morning.
--
-- Tunable by measurement, like the batch size. What is not tunable is that it exists.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ops.quarantine_cap() RETURNS integer
LANGUAGE sql IMMUTABLE AS $$ SELECT 2000 $$;
-- +goose StatementEnd

COMMENT ON FUNCTION ops.quarantine_cap() IS
  'How many events one device may have awaiting review. Cleared by triage, not by time.';

-- Counting held rows for one device has to be cheap, because it happens on every insert. The
-- existing index on (device_id, held_at DESC) covers every row this device ever had held,
-- including the resolved ones, which for a device that has been through triage a few times is
-- most of them. This one is only the rows that count against the cap.
CREATE INDEX sync_quarantine_held_by_device ON ops.sync_quarantine (device_id)
  WHERE status = 'HELD';

-- ---------------------------------------------------------------------------
-- The enforcement
-- ---------------------------------------------------------------------------

-- BEFORE INSERT, counting under the row's own lock.
--
-- A count is racy in the general case — two concurrent inserts can both see 1999 — but the race
-- here is worth naming rather than solving with a lock. The overshoot is bounded by how many
-- pushes one device has in flight, which for a client that waits for its receipt is one; the cost
-- of being wrong is a handful of extra rows; and the price of doing it properly is serialising
-- every quarantine insert in the clinic behind one advisory lock per device. The invariant below
-- allows the same slack, so a legitimate overshoot does not fail the nightly check.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ops.enforce_quarantine_cap() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE held integer;
BEGIN
  SELECT count(*) INTO held
    FROM ops.sync_quarantine
   WHERE device_id = NEW.device_id AND status = 'HELD';

  IF held >= ops.quarantine_cap() THEN
    RAISE EXCEPTION 'device % already has % events awaiting review, which is the limit',
      NEW.device_id, held
      USING ERRCODE = 'check_violation',
            HINT = 'Somebody must work through this device''s quarantine before it can hold more.';
  END IF;

  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER sync_quarantine_respects_the_cap
  BEFORE INSERT ON ops.sync_quarantine
  FOR EACH ROW EXECUTE FUNCTION ops.enforce_quarantine_cap();

-- ---------------------------------------------------------------------------
-- The invariant
-- ---------------------------------------------------------------------------

-- The trigger is what stops it; this is what notices if the trigger was ever dropped, disabled or
-- bypassed by a role that owns the table. `migrate verify` is the second pair of eyes on every
-- structural guarantee in this system, and a cap with nothing checking it is a cap that quietly
-- stopped applying two releases ago.
--
-- The tolerance is the concurrent-insert slack the trigger's comment describes. Ten per cent of a
-- cap of two thousand is two hundred, which no honest race reaches and no leak hides under: a
-- device that is genuinely unbounded passes the cap by orders of magnitude, not by a tenth.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_device_exceeds_its_quarantine_cap() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint; worst bigint;
BEGIN
  SELECT count(*), coalesce(max(held), 0) INTO offenders, worst
    FROM (SELECT count(*) AS held
            FROM ops.sync_quarantine
           WHERE status = 'HELD'
           GROUP BY device_id) AS per_device
   WHERE held > ops.quarantine_cap() * 11 / 10;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% devices hold more quarantined events than the cap allows (worst: %)',
      offenders, worst;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_no_device_exceeds_its_quarantine_cap',
   'no device has more events awaiting review than the per-device cap allows', 97)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name = 'assert_no_device_exceeds_its_quarantine_cap';
DROP FUNCTION IF EXISTS core.assert_no_device_exceeds_its_quarantine_cap();
DROP TRIGGER IF EXISTS sync_quarantine_respects_the_cap ON ops.sync_quarantine;
DROP FUNCTION IF EXISTS ops.enforce_quarantine_cap();
DROP INDEX IF EXISTS ops.sync_quarantine_held_by_device;
DROP FUNCTION IF EXISTS ops.quarantine_cap();
