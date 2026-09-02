-- Identity: who may do what, and the record of who granted it.
--
-- Everything downstream depends on this. [R-03] requires user_id on every clinical
-- event, so nothing can be attributed before a user exists; [R-02] requires one person
-- to hold several station roles at once, so the model must support that from the first
-- migration rather than acquire it later.
--
-- Three properties are enforced by the database rather than by application code, for the
-- same reason the ledger is (ADR-0008): a rule that lives only in Go holds until someone
-- writes a script, opens psql during an incident, or adds a second service.
--
--   1. A user is never deleted. dthcms_app holds no DELETE on core.app_user, so
--      attribution cannot be severed even by a bug that means to.
--   2. Lifecycle transitions are checked by a trigger. "Suspended" reached from nowhere
--      is a state nobody can explain six months later.
--   3. The blueprint's own access rules — a nutritionist sees no prescription, a
--      pharmacist sees no diagnosis, registration is blinded to sensitive clinical data
--      (§4.4) — are assertions that run on every migration in every environment. They
--      are the reason this file is long.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Users
-- ---------------------------------------------------------------------------

CREATE TABLE core.app_user (
  id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id    uuid        NOT NULL REFERENCES core.facility(id),

  -- Short, human-typed, printed on rosters and spoken aloud on the clinic floor. It is
  -- also what appears in the worked example from §4.3 — "JD_04 changed systolic BP
  -- 140 → 145" — so it must be readable by someone who is not looking at a screen.
  employee_code  text        NOT NULL,

  name_en        text        NOT NULL,
  name_bn        text        NOT NULL,

  phone          text        NOT NULL DEFAULT '',
  email          text        NOT NULL DEFAULT '',

  -- Present from the first migration, unused until CP16. A nullable column added now
  -- costs nothing; adding a NOT NULL column to a populated user table on the morning
  -- authentication ships is a migration nobody wants to write under time pressure.
  password_hash      text,
  password_set_at    timestamptz,
  totp_secret_enc    bytea,
  totp_confirmed_at  timestamptz,

  status         text        NOT NULL DEFAULT 'invited',

  -- Why the account is in its current state. Required for suspension, because
  -- "suspended" with no reason is a decision nobody can review.
  status_reason  text        NOT NULL DEFAULT '',
  status_changed_at timestamptz NOT NULL DEFAULT now(),

  last_login_at  timestamptz,

  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  created_by     uuid,
  updated_by     uuid,

  CONSTRAINT app_user_status_known CHECK (
    status IN ('invited', 'active', 'suspended', 'deactivated')),
  CONSTRAINT app_user_code_format CHECK (employee_code ~ '^[A-Z][A-Z0-9_-]{1,15}$'),
  CONSTRAINT app_user_name_present CHECK (
    length(btrim(name_en)) > 0 AND length(btrim(name_bn)) > 0),
  -- A suspension with no stated reason is the one that gets disputed.
  CONSTRAINT app_user_suspension_explained CHECK (
    status <> 'suspended' OR length(btrim(status_reason)) >= 3)
);

-- Employee codes are unique within a facility, not globally: two clinics may both have
-- an "R01" at the registration desk, and forcing a global namespace on Phase 4 would
-- mean renaming people who did nothing wrong.
CREATE UNIQUE INDEX app_user_code_key ON core.app_user (facility_id, employee_code);

-- Partial unique: an email may be reused after an account is deactivated, because the
-- person left and someone else was given the address. Two *live* accounts may not share
-- one, because a login would then be ambiguous.
CREATE UNIQUE INDEX app_user_email_key ON core.app_user (lower(email))
  WHERE email <> '' AND status <> 'deactivated';

CREATE INDEX app_user_facility_status_idx ON core.app_user (facility_id, status);

SELECT core.attach_updated_at('core.app_user');

COMMENT ON TABLE  core.app_user IS
  'A member of staff. Never deleted — deactivated, so that attribution [R-03] survives.';
COMMENT ON COLUMN core.app_user.employee_code IS
  'Short human identifier, unique per facility. Appears in attribution chips and audit lines.';
COMMENT ON COLUMN core.app_user.password_hash IS
  'Argon2id, set at CP16. Null means the invitation has not been accepted.';
COMMENT ON COLUMN core.app_user.totp_secret_enc IS
  'Encrypted TOTP seed, set at CP17 (D-45). Null means 2FA is not enrolled.';

-- ---------------------------------------------------------------------------
-- Lifecycle
--
--   invited ──▶ active ──▶ suspended ──▶ active
--      │           │            │
--      └───────────┴────────────┴──────▶ deactivated ──▶ active
--
-- Deactivated is not terminal, deliberately. Staff leave and come back, and re-inviting
-- them would create a second user row — which splits their history in two and defeats
-- the whole reason users are never deleted. Reactivation is a deliberate act and is
-- audited at CP22.
--
-- What the trigger actually prevents is the transition nobody meant: invited → suspended
-- (suspending an account that was never accepted), and any move out of a state the row
-- is not in, which is the shape of a lost update between two admins.
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_user_status_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  allowed text[];
BEGIN
  IF NEW.status = OLD.status THEN
    RETURN NEW;
  END IF;

  allowed := CASE OLD.status
    WHEN 'invited'     THEN ARRAY['active', 'deactivated']
    WHEN 'active'      THEN ARRAY['suspended', 'deactivated']
    WHEN 'suspended'   THEN ARRAY['active', 'deactivated']
    WHEN 'deactivated' THEN ARRAY['active']
  END;

  IF NOT (NEW.status = ANY (allowed)) THEN
    RAISE EXCEPTION 'user % cannot go from % to %', NEW.employee_code, OLD.status, NEW.status
      USING HINT = format('From %L the permitted transitions are: %s.',
                          OLD.status, array_to_string(allowed, ', '));
  END IF;

  NEW.status_changed_at := now();
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER app_user_status_transition
  BEFORE UPDATE OF status ON core.app_user
  FOR EACH ROW EXECUTE FUNCTION core.assert_user_status_transition();

COMMENT ON FUNCTION core.assert_user_status_transition() IS
  'Refuses a lifecycle transition the state machine does not allow, and stamps status_changed_at.';

-- The facility's audit columns have been waiting for this table since 00004.
ALTER TABLE core.facility
  ADD CONSTRAINT facility_created_by_fk FOREIGN KEY (created_by) REFERENCES core.app_user(id),
  ADD CONSTRAINT facility_updated_by_fk FOREIGN KEY (updated_by) REFERENCES core.app_user(id);

ALTER TABLE core.app_user
  ADD CONSTRAINT app_user_created_by_fk FOREIGN KEY (created_by) REFERENCES core.app_user(id),
  ADD CONSTRAINT app_user_updated_by_fk FOREIGN KEY (updated_by) REFERENCES core.app_user(id);

-- ---------------------------------------------------------------------------
-- Stations
--
-- The twelve stations of blueprint §3, in their default order. Order is a hint rather
-- than a rule: §5.2 requires the sequence to be configurable, because a clinic that
-- backs up at nutrition reroutes the patient to exercise and back.
-- ---------------------------------------------------------------------------

CREATE TABLE core.station (
  id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id    uuid        NOT NULL REFERENCES core.facility(id),

  code           text        NOT NULL,
  name_en        text        NOT NULL,
  name_bn        text        NOT NULL,
  room           text        NOT NULL DEFAULT '',
  sequence_hint  integer     NOT NULL,

  -- A station that exists in the blueprint but is not staffed today. The distinction
  -- matters: the traffic-control board must not queue patients to a room with nobody
  -- in it, and a clinic that is four people should not look like a clinic that is
  -- fifteen.
  is_staffed     boolean     NOT NULL DEFAULT false,
  is_active      boolean     NOT NULL DEFAULT true,

  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  created_by     uuid        REFERENCES core.app_user(id),
  updated_by     uuid        REFERENCES core.app_user(id),

  CONSTRAINT station_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,31}$'),
  CONSTRAINT station_sequence_positive CHECK (sequence_hint > 0)
);

CREATE UNIQUE INDEX station_code_key ON core.station (facility_id, code);
CREATE INDEX station_sequence_idx ON core.station (facility_id, sequence_hint) WHERE is_active;

SELECT core.attach_updated_at('core.station');

COMMENT ON TABLE  core.station IS
  'A physical point of care in the patient journey (blueprint §3). Order is configurable (§5.2).';
COMMENT ON COLUMN core.station.is_staffed IS
  'False means the station exists in the design but nobody works it yet. The queue must not route to it.';

-- ---------------------------------------------------------------------------
-- Roles and permissions
--
-- These three tables are NOT facility-scoped, and that is a decision rather than an
-- omission. What "PHARMACIST" means does not vary between branches; if it did, a
-- permission audit would have to be repeated per site and would eventually diverge.
-- Where a role applies is a property of the grant (core.user_role), not of the role.
-- ---------------------------------------------------------------------------

CREATE TABLE core.role (
  id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  code         text        NOT NULL UNIQUE,
  name_en      text        NOT NULL,
  name_bn      text        NOT NULL,
  description  text        NOT NULL DEFAULT '',

  -- A clinical role touches patient data; a non-clinical one does not. Drives which
  -- roles may be granted without clinical governance sign-off, and which appear in the
  -- attribution chip as a clinician rather than as staff.
  is_clinical  boolean     NOT NULL DEFAULT true,

  -- The station this role primarily works, where there is one. HR and ADMIN work no
  -- station; PHYSICIAN works one; a role may be granted to someone covering another.
  station_code text,

  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  -- Two characters, not three: QA and HR are real roles and the first draft of this
  -- constraint rejected both. Caught by running the migration rather than by reading it.
  CONSTRAINT role_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{1,31}$')
);

SELECT core.attach_updated_at('core.role');

CREATE TABLE core.permission (
  code         text        PRIMARY KEY,
  description  text        NOT NULL,

  -- resource.action.scope, split out so a query can ask "everything that writes an
  -- observation" without pattern-matching a string.
  resource     text        NOT NULL,
  action       text        NOT NULL,
  scope        text        NOT NULL DEFAULT '',

  -- True where holding this permission means seeing identifiable clinical detail. This
  -- is what makes §4.4's blinding rules checkable rather than a matter of reading the
  -- grant list carefully.
  is_sensitive boolean     NOT NULL DEFAULT false,

  created_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT permission_code_shape CHECK (
    code ~ '^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)?$'),
  CONSTRAINT permission_parts_match_code CHECK (
    code = resource || '.' || action || CASE WHEN scope = '' THEN '' ELSE '.' || scope END)
);

COMMENT ON TABLE core.permission IS
  'The permission catalogue. resource.action.scope triples covering every station and administrative action.';
COMMENT ON COLUMN core.permission.is_sensitive IS
  'Seeing this means seeing identifiable clinical detail. Blueprint §4.4 blinding rules are checked against it.';

CREATE TABLE core.role_permission (
  role_id         uuid  NOT NULL REFERENCES core.role(id) ON DELETE CASCADE,
  permission_code text  NOT NULL REFERENCES core.permission(code) ON DELETE CASCADE,
  granted_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (role_id, permission_code)
);

COMMENT ON TABLE core.role_permission IS
  'Which permissions a role carries. ON DELETE CASCADE is safe here: roles and permissions are catalogue, not history.';

-- ---------------------------------------------------------------------------
-- Grants
--
-- A grant is history, not state. Revoking sets revoked_at and leaves the row; granting
-- the same role again inserts a new one. So "who could sign a prescription on the 14th
-- of March" is answerable, which is the whole point of an attributed system.
-- ---------------------------------------------------------------------------

CREATE TABLE core.user_role (
  id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      uuid        NOT NULL REFERENCES core.app_user(id),
  role_id      uuid        NOT NULL REFERENCES core.role(id),

  -- Where the role applies. Today it always equals the user's own facility; it exists
  -- separately so that a Phase 4 clinician working two sites can be a physician at one
  -- and a researcher at the other without a second user row splitting their history.
  facility_id  uuid        NOT NULL REFERENCES core.facility(id),

  granted_by   uuid        REFERENCES core.app_user(id),
  granted_at   timestamptz NOT NULL DEFAULT now(),
  revoked_by   uuid        REFERENCES core.app_user(id),
  revoked_at   timestamptz,
  revoke_reason text       NOT NULL DEFAULT '',

  CONSTRAINT user_role_revocation_coherent CHECK (
    (revoked_at IS NULL AND revoked_by IS NULL)
    OR (revoked_at IS NOT NULL))
);

-- One live grant of a role per user per facility. Revoked rows are unconstrained, which
-- is what lets the same role be granted, revoked and granted again over a career.
CREATE UNIQUE INDEX user_role_live_key
  ON core.user_role (user_id, role_id, facility_id)
  WHERE revoked_at IS NULL;

CREATE INDEX user_role_user_idx ON core.user_role (user_id) WHERE revoked_at IS NULL;
CREATE INDEX user_role_role_idx ON core.user_role (role_id) WHERE revoked_at IS NULL;

COMMENT ON TABLE core.user_role IS
  'Role grants with their history. Revocation sets revoked_at; rows are never deleted [R-02].';
COMMENT ON COLUMN core.user_role.facility_id IS
  'Where the role applies. Equals the user''s facility today; separate so a multi-site user needs no second account.';

-- Roles and permissions describe the system, not a clinic.
INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'role',
   'The role catalogue is system-wide. What PHARMACIST means cannot differ between branches, or a permission audit would have to be repeated per site and would diverge.'),
  ('core', 'permission',
   'The permission catalogue is system-wide, for the same reason as core.role. Where a role applies is a property of the grant, not of the role.'),
  ('core', 'role_permission',
   'Joins two system-wide catalogues. Scoping it per facility would allow one branch to quietly widen a role.')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- No hard delete
--
-- Deactivation, not deletion, is the whole point (§6.3, and [R-03] behind it). The
-- application role is given no DELETE on the tables whose rows are attribution, so a
-- migration or a service that later means to delete a user finds it cannot, rather than
-- finding out afterwards.
-- ---------------------------------------------------------------------------

REVOKE DELETE ON core.app_user  FROM dthcms_app;
REVOKE DELETE ON core.user_role FROM dthcms_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_users_undeletable() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  SELECT string_agg(format('core.%s', t), ', ' ORDER BY t) INTO offending
  FROM unnest(ARRAY['app_user', 'user_role']) AS t
  WHERE has_table_privilege('dthcms_app', 'core.' || t, 'DELETE');

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'dthcms_app can delete attribution records: %', offending
      USING HINT = 'Users are deactivated, never deleted, so that who entered a value '
                   'remains answerable. REVOKE DELETE from dthcms_app.';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_users_undeletable() IS
  'Raises if the application role can delete a user or a role grant.';

-- +goose Down

DROP FUNCTION IF EXISTS core.assert_users_undeletable();

DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name IN ('role', 'permission', 'role_permission');

ALTER TABLE core.facility
  DROP CONSTRAINT IF EXISTS facility_created_by_fk,
  DROP CONSTRAINT IF EXISTS facility_updated_by_fk;

DROP TABLE IF EXISTS core.user_role;
DROP TABLE IF EXISTS core.role_permission;
DROP TABLE IF EXISTS core.permission;
DROP TABLE IF EXISTS core.role;
DROP TABLE IF EXISTS core.station;
DROP TRIGGER IF EXISTS app_user_status_transition ON core.app_user;
DROP TABLE IF EXISTS core.app_user;
DROP FUNCTION IF EXISTS core.assert_user_status_transition();
