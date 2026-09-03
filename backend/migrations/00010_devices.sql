-- Devices (CP18, D-46): the tablets and phones the clinic enrols, the keys that prove a
-- request came from one, and the record of everything that happened to each.
--
-- [R-03] says every clinical entry carries the device it was made on. A device id the
-- client types into a header is a claim; a device id the server derives from a signature
-- only that device's private key could have produced is evidence. This migration holds the
-- public half of those keys. The private half never leaves the device.
--
-- Three structural properties, in the house style:
--
--   * an enrolment code is never in the database in the clear — a digest is, it expires,
--     and it is consumed once;
--   * a device is never deleted — it is revoked, or marked lost, and the row stays, because
--     an event written from it last year still names it;
--   * a device event is written once and never rewritten.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Devices
-- ---------------------------------------------------------------------------

CREATE TABLE core.device (
  id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id   uuid        NOT NULL REFERENCES core.facility(id),

  -- What the clinic calls it: "Anthropometry tablet 2". Unique per facility so that the
  -- admin screen and the attribution line say the same thing.
  name          text        NOT NULL,
  kind          text        NOT NULL,

  -- pending:   an administrator has issued an enrolment code; no key yet.
  -- active:    enrolled; may authenticate and, from the checkpoint that has them, write
  --            clinical events.
  -- suspended: temporarily refused, reversible (a tablet left in a taxi that turned up).
  -- revoked:   permanently refused. Terminal.
  -- lost:      permanently refused, and flagged so that queued events arriving from it are
  --            quarantined rather than accepted. Terminal.
  status        text        NOT NULL DEFAULT 'pending',

  enrolled_by   uuid        REFERENCES core.app_user(id),
  enrolled_at   timestamptz,

  -- What the device said about itself at enrolment and at its most recent request. Shown
  -- on the admin screen so that "which one is the old Samsung" is answerable.
  model         text        NOT NULL DEFAULT '',
  os_version    text        NOT NULL DEFAULT '',
  app_version   text        NOT NULL DEFAULT '',
  last_seen_at  timestamptz,

  status_changed_at timestamptz NOT NULL DEFAULT now(),
  status_changed_by uuid     REFERENCES core.app_user(id),
  status_reason text        NOT NULL DEFAULT '',

  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT device_name_present CHECK (length(btrim(name)) >= 2),
  CONSTRAINT device_kind_known CHECK (kind IN ('tablet', 'phone', 'desktop')),
  CONSTRAINT device_status_known CHECK (
    status IN ('pending', 'active', 'suspended', 'revoked', 'lost')),
  CONSTRAINT device_enrolment_coherent CHECK (
    (status = 'pending' AND enrolled_at IS NULL)
    OR (status <> 'pending' AND enrolled_at IS NOT NULL)),
  CONSTRAINT device_terminal_has_reason CHECK (
    status NOT IN ('revoked', 'lost', 'suspended') OR length(btrim(status_reason)) >= 3)
);

CREATE UNIQUE INDEX device_facility_name_idx ON core.device (facility_id, lower(name));
CREATE INDEX device_facility_status_idx ON core.device (facility_id, status);

COMMENT ON TABLE core.device IS
  'An enrolled clinic device. Never deleted: revoked or lost, the row stays for attribution.';
COMMENT ON COLUMN core.device.status IS
  'pending → active → (suspended ↔ active) → revoked | lost. revoked and lost are terminal.';

SELECT core.attach_updated_at('core.device');

-- ---------------------------------------------------------------------------
-- Keys
--
-- One live key per device; a retired key stays so that a signature made under it can still
-- be attributed. Ed25519: 32-byte public keys, 64-byte signatures, no parameters to get
-- wrong, and in the Go standard library.
-- ---------------------------------------------------------------------------

CREATE TABLE core.device_key (
  id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  device_id     uuid        NOT NULL REFERENCES core.device(id),
  facility_id   uuid        NOT NULL REFERENCES core.facility(id),

  algorithm     text        NOT NULL DEFAULT 'ed25519',
  public_key    bytea       NOT NULL,

  created_at    timestamptz NOT NULL DEFAULT now(),
  retired_at    timestamptz,
  retire_reason text        NOT NULL DEFAULT '',

  CONSTRAINT device_key_algorithm_known CHECK (algorithm = 'ed25519'),
  CONSTRAINT device_key_length CHECK (octet_length(public_key) = 32),
  CONSTRAINT device_key_retire_coherent CHECK (
    (retired_at IS NULL AND retire_reason = '')
    OR (retired_at IS NOT NULL AND length(btrim(retire_reason)) >= 3))
);

-- Exactly one live key per device, enforced rather than assumed.
CREATE UNIQUE INDEX device_key_live_idx ON core.device_key (device_id) WHERE retired_at IS NULL;
-- A public key belongs to one device. Two devices presenting the same key would be one
-- private key in two places, which is the thing this whole table exists to rule out.
CREATE UNIQUE INDEX device_key_public_idx ON core.device_key (public_key);

COMMENT ON TABLE core.device_key IS
  'Ed25519 public keys. The private key is generated on the device and never leaves it.';

-- ---------------------------------------------------------------------------
-- Enrolment codes
--
-- An administrator issues one from the console; somebody types it into the tablet within
-- fifteen minutes; the tablet sends it with a fresh public key; the code is spent. Fifty
-- bits of randomness and a fifteen-minute expiry: a code cannot be guessed inside its
-- window. A code for a device that already has a key is a re-enrolment — the phone was
-- reinstalled and the Keystore with it — and retires the old key.
-- ---------------------------------------------------------------------------

CREATE TABLE core.device_enrolment (
  id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  device_id     uuid        NOT NULL REFERENCES core.device(id),
  facility_id   uuid        NOT NULL REFERENCES core.facility(id),
  issued_by     uuid        NOT NULL REFERENCES core.app_user(id),

  code_digest   bytea       NOT NULL UNIQUE,
  issued_at     timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  consumed_at   timestamptz,

  CONSTRAINT device_enrolment_digest_length CHECK (octet_length(code_digest) = 32),
  CONSTRAINT device_enrolment_expires_after_issue CHECK (expires_at > issued_at)
);

CREATE INDEX device_enrolment_device_idx ON core.device_enrolment (device_id);

COMMENT ON TABLE core.device_enrolment IS
  'One-time enrolment codes, digest only, minutes-long, consumed once.';

-- ---------------------------------------------------------------------------
-- Device events
--
-- Enrolment, key rotation, every status change, and every refused signature: what an
-- administrator reads when a device is in question. CP22 renders it.
-- ---------------------------------------------------------------------------

CREATE TABLE core.device_event (
  id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  device_id     uuid        NOT NULL REFERENCES core.device(id),
  facility_id   uuid        NOT NULL REFERENCES core.facility(id),
  actor_id      uuid        REFERENCES core.app_user(id),

  kind          text        NOT NULL,
  -- A reason, a version string, a key id. Never a key, never a code, never a name.
  detail        jsonb       NOT NULL DEFAULT '{}'::jsonb,
  at            timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT device_event_kind_known CHECK (kind IN (
    'enrolment_issued', 'enrolment_failed', 'enrolled', 'key_rotated',
    'suspended', 'reinstated', 'revoked', 'lost',
    'signature_refused', 'session_bound'))
);

CREATE INDEX device_event_device_at_idx ON core.device_event (device_id, at DESC);

COMMENT ON TABLE core.device_event IS
  'Append-only record of what happened to a device. Nothing rewrites it.';

-- ---------------------------------------------------------------------------
-- Sessions are now bound to devices
--
-- The column has existed since CP16 with nothing to point at. Nullable still: a browser is
-- not an enrolled device, and a physician at a desktop signs in without one. What a
-- device-less session may not do is write a clinical event — that rule lives in the
-- middleware, and the event store enforces it again at CP23.
-- ---------------------------------------------------------------------------

ALTER TABLE core.session
  ADD CONSTRAINT session_device_fk FOREIGN KEY (device_id) REFERENCES core.device(id);

CREATE INDEX session_device_idx ON core.session (device_id) WHERE device_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- What the application may not do
-- ---------------------------------------------------------------------------

REVOKE DELETE ON core.device            FROM dthcms_app;
REVOKE DELETE ON core.device_key        FROM dthcms_app;
REVOKE DELETE ON core.device_enrolment  FROM dthcms_app;
REVOKE DELETE ON core.device_event      FROM dthcms_app;
REVOKE UPDATE ON core.device_event      FROM dthcms_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_devices_undeletable() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  SELECT string_agg(format('core.%s', t), ', ' ORDER BY t) INTO offending
  FROM unnest(ARRAY['device', 'device_key', 'device_enrolment', 'device_event']) AS t
  WHERE has_table_privilege('dthcms_app', 'core.' || t, 'DELETE');

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'dthcms_app can delete device records: %', offending
      USING HINT = 'Devices are revoked, keys retired, codes consumed, events kept.';
  END IF;

  IF has_table_privilege('dthcms_app', 'core.device_event', 'UPDATE') THEN
    RAISE EXCEPTION 'dthcms_app can rewrite device events'
      USING HINT = 'An event log the application can edit is not a log.';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_devices_undeletable() IS
  'Raises if the application role can delete a device, key, enrolment code or event, or rewrite an event.';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_device_keys_sound() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  -- An active device with no live key cannot sign anything; a pending one with a key was
  -- enrolled without the status change. Both are states the code never writes, so a row in
  -- either is a bug or a hand edit.
  SELECT string_agg(d.id::text, ', ') INTO offending
  FROM core.device d
  LEFT JOIN core.device_key k ON k.device_id = d.id AND k.retired_at IS NULL
  WHERE (d.status = 'active' AND k.id IS NULL)
     OR (d.status = 'pending' AND k.id IS NOT NULL);

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'device status and live key disagree: %', offending
      USING HINT = 'An active device has exactly one live key; a pending one has none.';
  END IF;

  -- The private key column that must never exist.
  SELECT string_agg(format('%s.%s', c.table_name, c.column_name), ', ') INTO offending
  FROM information_schema.columns c
  WHERE c.table_schema = 'core' AND c.table_name LIKE 'device%'
    AND (c.column_name LIKE '%private%' OR c.column_name LIKE '%secret%'
         OR (c.table_name = 'device_enrolment' AND c.column_name LIKE '%code%'
             AND c.column_name <> 'code_digest'));

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'a device secret appears to be stored in the clear: %', offending
      USING HINT = 'Private keys stay on the device; enrolment codes are digests.';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_device_keys_sound() IS
  'Raises if an active device lacks a live key, a pending one has one, or a device table holds a secret in the clear.';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_devices_undeletable', 'device records are revoked, retired or consumed, never deleted', 59),
  ('assert_device_keys_sound',   'device status agrees with its live key; no device secret is stored', 60)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant
 WHERE function_name IN ('assert_devices_undeletable', 'assert_device_keys_sound');

DROP FUNCTION IF EXISTS core.assert_device_keys_sound();
DROP FUNCTION IF EXISTS core.assert_devices_undeletable();

DROP INDEX IF EXISTS core.session_device_idx;
ALTER TABLE core.session DROP CONSTRAINT IF EXISTS session_device_fk;

DROP TABLE IF EXISTS core.device_event;
DROP TABLE IF EXISTS core.device_enrolment;
DROP TABLE IF EXISTS core.device_key;
DROP TABLE IF EXISTS core.device;
