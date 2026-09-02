-- Sessions, refresh families, and the record of who tried to log in.
--
-- Three properties are structural here rather than conventional, in the house style: what
-- the application may do to these rows is a privilege, not a habit; a token never exists in
-- the database; and a refresh token knows its lineage, which is what makes theft detectable.
--
-- On the last one, because it is the least obvious. A refresh token is exchanged for a new
-- one and marked used. If a *used* token is presented again, exactly one of two things has
-- happened: the client retried after a dropped response, or someone else has a copy. The
-- server cannot tell which, and the safe reading of "someone else has a copy" is that the
-- whole lineage is compromised. So the family is revoked and everyone logs in again. That is
-- disruptive on a bad network and correct on a bad day, and the trade is deliberate.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Sessions
-- ---------------------------------------------------------------------------

CREATE TABLE core.session (
  id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id   uuid        NOT NULL REFERENCES core.facility(id),
  user_id       uuid        NOT NULL REFERENCES core.app_user(id),

  -- The device that holds this session. Null until CP18 enrols devices; the foreign key
  -- and the not-null arrive there, in the migration that creates the table it points at.
  -- Present now because adding a column to a populated session table is a migration nobody
  -- wants to write on the morning device binding ships.
  device_id     uuid,

  -- SHA-256 of the access token. The token itself is 32 random bytes and is never stored,
  -- so a leak of this table yields nothing that can be presented as a credential.
  --
  -- SHA-256 rather than argon2id is deliberate and is not an inconsistency with the
  -- password column: a password is low-entropy and must be expensive to guess, while a
  -- 256-bit random token cannot be guessed at all. A slow hash here would buy nothing and
  -- cost latency on every request the clinic makes (ADR-0011).
  token_digest  bytea       NOT NULL,

  issued_at     timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  last_seen_at  timestamptz NOT NULL DEFAULT now(),

  -- CP17 stamps this when a second factor is completed, so a session can be asked to prove
  -- itself again before it signs a prescription. A row can be updated after issue; a signed
  -- token could not be, which is part of why the token is opaque.
  stepped_up_at timestamptz,

  revoked_at    timestamptz,
  revoked_by    uuid        REFERENCES core.app_user(id),
  revoke_reason text        NOT NULL DEFAULT '',

  -- Shown in "where am I logged in", so a user can recognise a session well enough to end
  -- it. Truncated, and deliberately not an IP address: the session list is a usability
  -- feature, and forensics belongs in core.login_attempt where the retention rules differ.
  user_agent    text        NOT NULL DEFAULT '',

  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT session_expires_after_issue CHECK (expires_at > issued_at),
  CONSTRAINT session_digest_length CHECK (octet_length(token_digest) = 32),
  CONSTRAINT session_revocation_coherent CHECK (
    (revoked_at IS NULL AND revoked_by IS NULL AND revoke_reason = '')
    OR revoked_at IS NOT NULL),
  CONSTRAINT session_user_agent_bounded CHECK (length(user_agent) <= 256)
);

-- The authentication path is this index. Every authenticated request is one equality lookup
-- on it, and criterion 3 — revocation within one request — is why it happens every time.
CREATE UNIQUE INDEX session_token_key ON core.session (token_digest);

CREATE INDEX session_live_by_user_idx ON core.session (user_id, expires_at)
  WHERE revoked_at IS NULL;

SELECT core.attach_updated_at('core.session');

COMMENT ON TABLE  core.session IS
  'A live login. The access token is opaque and stored only as a digest (ADR-0011).';
COMMENT ON COLUMN core.session.device_id IS
  'The device holding this session. Constrained at CP18, when devices exist.';
COMMENT ON COLUMN core.session.stepped_up_at IS
  'When a second factor was last completed for this session. Set at CP17.';

-- ---------------------------------------------------------------------------
-- Refresh tokens, and their families
--
-- family_id is the lineage: every token descended from one login shares it. Rotation
-- inserts a child and marks the parent used. Reuse of a used token revokes the family —
-- which is the whole reason the lineage is recorded rather than each token standing alone.
-- ---------------------------------------------------------------------------

CREATE TABLE core.refresh_token (
  id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id   uuid        NOT NULL REFERENCES core.facility(id),
  session_id    uuid        NOT NULL REFERENCES core.session(id),

  -- Shared by every token in the lineage, including across the sessions that rotation
  -- creates. Indexed, because revoking a family is the one operation that must be fast at
  -- exactly the moment something has gone wrong.
  family_id     uuid        NOT NULL,

  token_digest  bytea       NOT NULL,

  issued_at     timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,

  -- Set when the token is exchanged. Presenting a token that already has this set is the
  -- signal that something holds a copy it should not.
  used_at       timestamptz,
  replaced_by   uuid        REFERENCES core.refresh_token(id),

  revoked_at    timestamptz,
  revoke_reason text        NOT NULL DEFAULT '',

  CONSTRAINT refresh_expires_after_issue CHECK (expires_at > issued_at),
  CONSTRAINT refresh_digest_length CHECK (octet_length(token_digest) = 32),
  CONSTRAINT refresh_replacement_implies_use CHECK (replaced_by IS NULL OR used_at IS NOT NULL)
);

CREATE UNIQUE INDEX refresh_token_key ON core.refresh_token (token_digest);
CREATE INDEX refresh_family_idx ON core.refresh_token (family_id);
CREATE INDEX refresh_session_idx ON core.refresh_token (session_id);

COMMENT ON TABLE core.refresh_token IS
  'Rotating refresh tokens with lineage. Reuse of a used token revokes the whole family.';
COMMENT ON COLUMN core.refresh_token.family_id IS
  'The lineage descended from one login. Revoked as a unit when reuse is detected.';

-- ---------------------------------------------------------------------------
-- Login attempts
--
-- Written for every attempt, successful or not, and — importantly — whether or not the
-- employee code exists. Throttling that only counted attempts against real accounts would
-- answer "does this account exist" by how fast it refuses.
-- ---------------------------------------------------------------------------

CREATE TABLE core.login_attempt (
  id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  facility_id   uuid        NOT NULL REFERENCES core.facility(id),

  -- What was typed, not who it matched. May name no user at all.
  employee_code text        NOT NULL,

  -- Set only when the code matched. Null is not a failure marker — a failed attempt
  -- against a real account still records the user, so "who is being targeted" is answerable.
  user_id       uuid        REFERENCES core.app_user(id),

  succeeded     boolean     NOT NULL,

  -- Why it failed, in terms the operator never sees. The response is identical for every
  -- failure; this column is what an administrator reads afterwards.
  failure_kind  text        NOT NULL DEFAULT '',

  -- SHA-256 of the client address and a server-side pepper, so per-address throttling
  -- works without the address itself sitting in a table for years.
  client_digest bytea,

  attempted_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT login_attempt_failure_kind_known CHECK (
    failure_kind IN ('', 'no_such_user', 'bad_password', 'not_active', 'throttled', 'no_password_set')),
  CONSTRAINT login_attempt_success_has_no_reason CHECK (NOT succeeded OR failure_kind = ''),
  CONSTRAINT login_attempt_client_digest_length CHECK (
    client_digest IS NULL OR octet_length(client_digest) = 32)
);

-- The throttle asks two questions on every login: how many recent failures for this code,
-- and how many for this client. Both are covered here.
CREATE INDEX login_attempt_code_idx ON core.login_attempt (facility_id, employee_code, attempted_at DESC)
  WHERE NOT succeeded;
CREATE INDEX login_attempt_client_idx ON core.login_attempt (client_digest, attempted_at DESC)
  WHERE NOT succeeded AND client_digest IS NOT NULL;

COMMENT ON TABLE core.login_attempt IS
  'Every login attempt, whether or not the account exists. Pruned by the retention job at CP23.';
COMMENT ON COLUMN core.login_attempt.client_digest IS
  'SHA-256 of the client address and a server pepper. Throttling without keeping addresses.';

-- ---------------------------------------------------------------------------
-- What the application may do
--
-- It revokes; it never deletes. Pruning old attempts and expired sessions is a maintenance
-- job that runs as the owner (CP23), not something a request handler can reach.
-- ---------------------------------------------------------------------------

REVOKE DELETE ON core.session       FROM dthcms_app;
REVOKE DELETE ON core.refresh_token FROM dthcms_app;
REVOKE DELETE ON core.login_attempt FROM dthcms_app;

-- A login attempt is evidence. Rewriting one would let a compromised account tidy up after
-- itself, which is the one thing this table exists to prevent.
REVOKE UPDATE ON core.login_attempt FROM dthcms_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_credentials_undeletable() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  SELECT string_agg(format('core.%s', t), ', ' ORDER BY t) INTO offending
  FROM unnest(ARRAY['session', 'refresh_token', 'login_attempt']) AS t
  WHERE has_table_privilege('dthcms_app', 'core.' || t, 'DELETE');

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'dthcms_app can delete session or attempt records: %', offending
      USING HINT = 'Sessions are revoked, never deleted; attempts are evidence. '
                   'Pruning is a maintenance job running as the owner (CP23).';
  END IF;

  IF has_table_privilege('dthcms_app', 'core.login_attempt', 'UPDATE') THEN
    RAISE EXCEPTION 'dthcms_app can rewrite login attempts'
      USING HINT = 'An attempt log a compromised account can edit is not a log.';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_credentials_undeletable() IS
  'Raises if the application role can delete a session or rewrite a login attempt.';

-- ---------------------------------------------------------------------------
-- No token is ever stored
--
-- The rule is "digests only", and a rule of that shape lasts exactly until someone adds a
-- convenience column during an incident. Making it a schema property means the migration
-- that adds it fails instead.
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_plaintext_credentials() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  SELECT string_agg(format('%s.%s.%s', c.table_schema, c.table_name, c.column_name), ', '
                    ORDER BY c.table_name, c.column_name)
    INTO offending
  FROM information_schema.columns c
  WHERE c.table_schema = 'core'
    AND c.table_name IN ('session', 'refresh_token')
    AND (c.column_name IN ('token', 'secret', 'refresh_token', 'access_token', 'plaintext')
      OR (c.column_name LIKE '%token%' AND c.column_name NOT LIKE '%digest%'
          AND c.column_name NOT IN ('token_digest')));

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'a credential appears to be stored in plaintext: %', offending
      USING HINT = 'Sessions and refresh tokens are stored as SHA-256 digests only. '
                   'If a column genuinely needs another name, it still may not hold a token.';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_no_plaintext_credentials() IS
  'Raises if a column on core.session or core.refresh_token looks like it holds a token rather than a digest.';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_credentials_undeletable',  'sessions are revoked rather than deleted and attempts cannot be rewritten', 55),
  ('assert_no_plaintext_credentials', 'no credential is stored in plaintext', 56)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant
 WHERE function_name IN ('assert_credentials_undeletable', 'assert_no_plaintext_credentials');

DROP FUNCTION IF EXISTS core.assert_no_plaintext_credentials();
DROP FUNCTION IF EXISTS core.assert_credentials_undeletable();

DROP TABLE IF EXISTS core.login_attempt;
DROP TABLE IF EXISTS core.refresh_token;
DROP TABLE IF EXISTS core.session;
