-- The second factor (CP17, D-45): TOTP seeds, recovery codes, the short-lived tokens that
-- carry a person across a login challenge or a step-up, and the record of what happened.
--
-- Three properties are structural, in the house style:
--
--   * a TOTP seed is never in the database in the clear — the column holds a ciphertext and
--     the id of the key it was sealed under (ADR-0012), and an assertion refuses any column
--     that looks like it holds a seed;
--   * a recovery code is never in the database at all — a digest is, and a used digest stays
--     used, because "worked once" is the whole promise;
--   * nothing here can be deleted by the application role. A seed is disabled, a code is
--     spent, a token is consumed, an event is written once.
--
-- The placeholder columns CP15 left on core.app_user for this go away here: the seed needs a
-- key id, a replay guard and a pending state beside it, which is a row, not two columns.

-- +goose Up

-- ---------------------------------------------------------------------------
-- TOTP seeds
-- ---------------------------------------------------------------------------

CREATE TABLE core.user_totp (
  user_id       uuid        PRIMARY KEY REFERENCES core.app_user(id),
  facility_id   uuid        NOT NULL REFERENCES core.facility(id),

  -- AES-256-GCM ciphertext of the base32 seed, nonce prepended, sealed with the user id as
  -- associated data so a row copied onto another user does not decrypt (secretbox).
  secret_sealed bytea       NOT NULL,
  -- Which key sealed it. Rotation is: new key gets a new id, this row is re-sealed on the
  -- next successful verification, old key retires when no row names it.
  key_id        text        NOT NULL,

  -- Null until the person has proved they can produce a code. An unconfirmed seed is a
  -- half-finished enrolment, not a second factor: it neither protects nor is required.
  confirmed_at  timestamptz,

  -- The replay guard. A code is valid for a thirty-second step and the verifier allows a
  -- step of drift either way; a code that was right sixty seconds ago is still "right". This
  -- records the last step a code was accepted for, and a second code for the same or an
  -- earlier step is refused — so a shoulder-surfed code cannot be reused inside its window.
  last_used_step bigint,

  disabled_at   timestamptz,
  disabled_by   uuid        REFERENCES core.app_user(id),
  disable_reason text       NOT NULL DEFAULT '',

  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT user_totp_key_id_present CHECK (length(key_id) > 0),
  CONSTRAINT user_totp_disable_coherent CHECK (
    (disabled_at IS NULL AND disabled_by IS NULL AND disable_reason = '')
    OR (disabled_at IS NOT NULL AND length(btrim(disable_reason)) >= 3))
);

COMMENT ON TABLE core.user_totp IS
  'One TOTP enrolment per user. The seed is a secretbox ciphertext, never plaintext.';
COMMENT ON COLUMN core.user_totp.last_used_step IS
  'Replay guard: the last 30-second step a code was accepted for. Earlier or equal steps are refused.';

SELECT core.attach_updated_at('core.user_totp');

-- The columns CP15 reserved. Nothing wrote to them; their contents, if any, are dropped.
ALTER TABLE core.app_user DROP COLUMN totp_secret_enc;
ALTER TABLE core.app_user DROP COLUMN totp_confirmed_at;

-- ---------------------------------------------------------------------------
-- Recovery codes
-- ---------------------------------------------------------------------------

CREATE TABLE core.recovery_code (
  id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       uuid        NOT NULL REFERENCES core.app_user(id),
  facility_id   uuid        NOT NULL REFERENCES core.facility(id),

  -- All codes from one enrolment share a batch; a re-enrolment revokes the previous batch
  -- wholesale, so an old sheet of codes in a drawer stops working the day a new one exists.
  batch_id      uuid        NOT NULL,

  -- SHA-256 of the code. A recovery code is sixteen random base32 characters — eighty bits
  -- — which is a token, not a password: it cannot be guessed and a slow hash would buy nothing.
  code_digest   bytea       NOT NULL UNIQUE,

  used_at       timestamptz,
  used_from_client bytea,
  revoked_at    timestamptz,

  created_at    timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT recovery_code_digest_length CHECK (octet_length(code_digest) = 32),
  CONSTRAINT recovery_code_client_length CHECK (
    used_from_client IS NULL OR octet_length(used_from_client) = 32)
);

CREATE INDEX recovery_code_user_live_idx
  ON core.recovery_code (user_id) WHERE used_at IS NULL AND revoked_at IS NULL;

COMMENT ON TABLE core.recovery_code IS
  'Single-use recovery codes, stored as digests. A used row stays; that is the proof it was used once.';

-- ---------------------------------------------------------------------------
-- Short-lived tokens: the login challenge and the step-up
-- ---------------------------------------------------------------------------
--
-- Two moments need to carry a person a few minutes forward without a session doing it:
--
--   login_challenge — the password was right and the account has a second factor. No
--                     session exists yet; this is what proves, five minutes later, that the
--                     code arriving belongs to the password that was just verified.
--   step_up         — a session exists and a privileged action needs a fresh second factor.
--                     The token is bound to the session, to one purpose, and to one use.
--
-- Same shape, same rules (digest only, expires, consumed once), so one table.

CREATE TABLE core.short_token (
  id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id   uuid        NOT NULL REFERENCES core.facility(id),
  user_id       uuid        NOT NULL REFERENCES core.app_user(id),
  -- Null for a login challenge (there is no session yet); required for a step-up.
  session_id    uuid        REFERENCES core.session(id),

  kind          text        NOT NULL,
  -- What a step-up token is for. Empty for a challenge. A token for "sign a prescription"
  -- is not a token for "change a role", however fresh it is.
  purpose       text        NOT NULL DEFAULT '',

  token_digest  bytea       NOT NULL UNIQUE,
  client_digest bytea,

  issued_at     timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  consumed_at   timestamptz,

  -- For a challenge: how many wrong codes have been offered against it. A challenge that
  -- has absorbed five is dead, whatever the clock says — the password behind it may be
  -- known to somebody who does not have the phone.
  failures      integer     NOT NULL DEFAULT 0,

  CONSTRAINT short_token_kind_known CHECK (kind IN ('login_challenge', 'step_up')),
  CONSTRAINT short_token_step_up_has_session CHECK (
    kind <> 'step_up' OR session_id IS NOT NULL),
  CONSTRAINT short_token_step_up_has_purpose CHECK (
    kind <> 'step_up' OR length(purpose) > 0),
  CONSTRAINT short_token_expires_after_issue CHECK (expires_at > issued_at),
  CONSTRAINT short_token_digest_length CHECK (octet_length(token_digest) = 32),
  CONSTRAINT short_token_client_length CHECK (
    client_digest IS NULL OR octet_length(client_digest) = 32)
);

COMMENT ON TABLE core.short_token IS
  'Minutes-long tokens for a login challenge or a step-up. Digest only; consumed once; never deleted by the app.';

-- ---------------------------------------------------------------------------
-- Two more ways a login can stop short of a session
--
-- The password was right but a code is now owed (second_factor_pending), and the code that
-- came back was wrong (bad_second_factor). Both are failures in the throttle's sense — the
-- second especially, since a wrong code against a known-good password is exactly the pattern
-- of somebody who has the password and not the phone.
-- ---------------------------------------------------------------------------

ALTER TABLE core.login_attempt DROP CONSTRAINT login_attempt_failure_kind_known;
ALTER TABLE core.login_attempt ADD CONSTRAINT login_attempt_failure_kind_known CHECK (
  failure_kind IN ('', 'no_such_user', 'bad_password', 'not_active', 'throttled',
                   'no_password_set', 'second_factor_pending', 'bad_second_factor'));

-- ---------------------------------------------------------------------------
-- Security events
--
-- Enrolment, disablement, step-up success and failure: the things an administrator reads
-- after the fact. CP22 builds the human-readable trail; this is the append-only substrate
-- it reads from, created now because the events start now.
-- ---------------------------------------------------------------------------

CREATE TABLE core.security_event (
  id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  facility_id   uuid        NOT NULL REFERENCES core.facility(id),
  user_id       uuid        REFERENCES core.app_user(id),
  session_id    uuid        REFERENCES core.session(id),
  -- Who did it, when it was not the user themselves (an administrator disabling a factor).
  actor_id      uuid        REFERENCES core.app_user(id),

  kind          text        NOT NULL,
  outcome       text        NOT NULL,
  -- Free of secrets and of PHI by construction: a purpose, a reason, a count. Never a
  -- code, never a seed, never a name.
  detail        jsonb       NOT NULL DEFAULT '{}'::jsonb,
  client_digest bytea,

  at            timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT security_event_kind_known CHECK (kind IN (
    'totp_enrolment_started', 'totp_enrolment_confirmed', 'totp_disabled',
    'totp_challenge_passed', 'totp_challenge_failed',
    'step_up_passed', 'step_up_failed', 'step_up_used',
    'recovery_code_used', 'recovery_codes_regenerated')),
  CONSTRAINT security_event_outcome_known CHECK (outcome IN ('ok', 'refused')),
  CONSTRAINT security_event_client_length CHECK (
    client_digest IS NULL OR octet_length(client_digest) = 32)
);

CREATE INDEX security_event_user_at_idx ON core.security_event (user_id, at DESC);

COMMENT ON TABLE core.security_event IS
  'Append-only record of second-factor and step-up events. CP22 renders it; nothing rewrites it.';

-- ---------------------------------------------------------------------------
-- What the application may not do
-- ---------------------------------------------------------------------------

REVOKE DELETE ON core.user_totp      FROM dthcms_app;
REVOKE DELETE ON core.recovery_code  FROM dthcms_app;
REVOKE DELETE ON core.short_token    FROM dthcms_app;
REVOKE DELETE ON core.security_event FROM dthcms_app;
REVOKE UPDATE ON core.security_event FROM dthcms_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_second_factor_undeletable() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  SELECT string_agg(format('core.%s', t), ', ' ORDER BY t) INTO offending
  FROM unnest(ARRAY['user_totp', 'recovery_code', 'short_token', 'security_event']) AS t
  WHERE has_table_privilege('dthcms_app', 'core.' || t, 'DELETE');

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'dthcms_app can delete second-factor records: %', offending
      USING HINT = 'Seeds are disabled, codes are spent, tokens are consumed, events are kept.';
  END IF;

  IF has_table_privilege('dthcms_app', 'core.security_event', 'UPDATE') THEN
    RAISE EXCEPTION 'dthcms_app can rewrite security events'
      USING HINT = 'An event log the application can edit is not a log.';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_second_factor_undeletable() IS
  'Raises if the application role can delete a seed, code, token or event, or rewrite an event.';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_plaintext_second_factor() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  -- A seed column must be the sealed one; a code column must be a digest.
  SELECT string_agg(format('%s.%s.%s', c.table_schema, c.table_name, c.column_name), ', '
                    ORDER BY c.table_name, c.column_name)
    INTO offending
  FROM information_schema.columns c
  WHERE c.table_schema = 'core'
    AND (
      (c.table_name = 'user_totp'
        AND (c.column_name IN ('secret', 'seed', 'totp_secret', 'secret_plain')
          OR (c.column_name LIKE '%secret%' AND c.column_name <> 'secret_sealed')))
      OR (c.table_name = 'recovery_code'
        AND (c.column_name IN ('code', 'plaintext')
          OR (c.column_name LIKE '%code%' AND c.column_name <> 'code_digest')))
      OR (c.table_name = 'short_token'
        AND c.column_name LIKE '%token%' AND c.column_name <> 'token_digest')
      OR (c.table_name = 'app_user' AND c.column_name LIKE 'totp%'));

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'a second-factor secret appears to be stored in the clear: %', offending
      USING HINT = 'Seeds are sealed with secretbox; codes and tokens are SHA-256 digests.';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_no_plaintext_second_factor() IS
  'Raises if a column on the second-factor tables looks like it holds a seed, code or token in the clear.';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_second_factor_undeletable',  'second-factor records are disabled, spent or consumed, never deleted', 57),
  ('assert_no_plaintext_second_factor', 'no TOTP seed, recovery code or short token is stored in the clear', 58)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant
 WHERE function_name IN ('assert_second_factor_undeletable', 'assert_no_plaintext_second_factor');

DROP FUNCTION IF EXISTS core.assert_no_plaintext_second_factor();
DROP FUNCTION IF EXISTS core.assert_second_factor_undeletable();

DROP TABLE IF EXISTS core.security_event;
DROP TABLE IF EXISTS core.short_token;
DROP TABLE IF EXISTS core.recovery_code;
DROP TABLE IF EXISTS core.user_totp;

ALTER TABLE core.app_user ADD COLUMN totp_secret_enc bytea;
ALTER TABLE core.app_user ADD COLUMN totp_confirmed_at timestamptz;

ALTER TABLE core.login_attempt DROP CONSTRAINT login_attempt_failure_kind_known;
ALTER TABLE core.login_attempt ADD CONSTRAINT login_attempt_failure_kind_known CHECK (
  failure_kind IN ('', 'no_such_user', 'bad_password', 'not_active', 'throttled', 'no_password_set'));
