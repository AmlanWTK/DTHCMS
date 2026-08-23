-- The facility, and the tenancy convention that follows from it.
--
-- DTHC is one clinic today. §15.3 Phase 4 anticipates more. Adding a tenancy
-- discriminator to a populated clinical database later is among the worst migrations
-- there is — every table rewritten, every index rebuilt, every query audited, on data
-- that must not be lost and a system that must not be down. Adding an unused column now
-- costs a few bytes a row (D-61).
--
-- So: every table holding facility-scoped data carries facility_id from the day it is
-- created, and core.assert_facility_scoping() below refuses to let one be created
-- without either the column or a written exemption. The exemption is deliberately
-- awkward — a row in a table, with a reason — because the point is that skipping the
-- convention should require a decision rather than an oversight.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Audit column convention
--
-- Every mutable table in core, ops and docs carries:
--
--   created_at  timestamptz NOT NULL DEFAULT now()
--   updated_at  timestamptz NOT NULL DEFAULT now()   + core.attach_updated_at(...)
--   created_by  uuid                                  -- core.app_user, from CP15
--   updated_by  uuid
--
-- The ledger carries none of them. An event already records who wrote it, from which
-- device, at which station, at what time, inside the envelope [R-03]; a second,
-- differently-shaped record of the same fact is a second thing to disagree with.
--
-- created_by and updated_by have no foreign key yet because core.app_user does not
-- exist until CP15. The constraint is added there, in the same migration that creates
-- the table it points at.
-- ---------------------------------------------------------------------------

CREATE TABLE core.facility (
  id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),

  -- Short, stable, human-typed. Appears on printed reports and in support
  -- conversations, where a UUID is unusable.
  code           text        NOT NULL,
  name_en        text        NOT NULL,
  name_bn        text        NOT NULL,

  facility_type  text        NOT NULL,

  address_en     text        NOT NULL DEFAULT '',
  address_bn     text        NOT NULL DEFAULT '',
  district       text        NOT NULL DEFAULT '',
  division       text        NOT NULL DEFAULT '',
  country        text        NOT NULL DEFAULT 'Bangladesh',
  phone          text        NOT NULL DEFAULT '',
  email          text        NOT NULL DEFAULT '',

  -- Stored per facility rather than assumed globally. Clinical timestamps are UTC in
  -- the database and rendered in the facility's zone; a system that assumes one zone
  -- gets the first branch in another one wrong, silently, in the direction of
  -- appointment times and overnight fasting windows.
  timezone       text        NOT NULL DEFAULT 'Asia/Dhaka',

  is_active      boolean     NOT NULL DEFAULT true,
  opened_on      date,

  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  created_by     uuid,
  updated_by     uuid,

  CONSTRAINT facility_code_format CHECK (code ~ '^[A-Z][A-Z0-9-]{2,15}$'),
  CONSTRAINT facility_type_known CHECK (
    facility_type IN ('clinic', 'diagnostic_centre', 'hospital', 'satellite'))
  -- timezone is validated by a trigger rather than a CHECK: the set of valid zone names
  -- lives in pg_timezone_names, and a CHECK constraint may not contain a subquery.
);

CREATE UNIQUE INDEX facility_code_key ON core.facility (code);

-- Partial index: the common query is "the active facilities", and there will never be
-- many rows. This is here as the convention, not as an optimisation.
CREATE INDEX facility_active_idx ON core.facility (is_active) WHERE is_active;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_valid_timezone() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_timezone_names WHERE name = NEW.timezone) THEN
    RAISE EXCEPTION 'timezone %L is not an IANA zone name known to this server', NEW.timezone
      USING HINT = 'Use a name from pg_timezone_names, for example Asia/Dhaka.';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER facility_timezone_valid
  BEFORE INSERT OR UPDATE OF timezone ON core.facility
  FOR EACH ROW EXECUTE FUNCTION core.assert_valid_timezone();

SELECT core.attach_updated_at('core.facility');

COMMENT ON TABLE  core.facility IS
  'A physical clinic. Every facility-scoped table references this (D-61).';
COMMENT ON COLUMN core.facility.code IS
  'Short human-readable identifier, printed on reports. Immutable in practice.';
COMMENT ON COLUMN core.facility.timezone IS
  'IANA zone. Clinical timestamps are stored UTC and rendered in this zone.';
COMMENT ON COLUMN core.facility.created_by IS
  'core.app_user.id. The foreign key is added in CP15, when that table exists.';

-- ---------------------------------------------------------------------------
-- The seed
--
-- Fixed UUID, deliberately. A generated one would differ between the developer's
-- machine, CI and production, so every fixture, every screenshot and every support
-- conversation would refer to a different facility. Seeds that identify real,
-- singular things get stable identifiers.
-- ---------------------------------------------------------------------------

INSERT INTO core.facility (id, code, name_en, name_bn, facility_type, district, division, country)
VALUES (
  '0190a000-0000-7000-8000-000000000001',
  'DTHC-FRD',
  'Diabetes, Thyroid & Hormone Clinic, Faridpur',
  'ডায়াবেটিস, থাইরয়েড ও হরমোন ক্লিনিক, ফরিদপুর',
  'clinic',
  'Faridpur',
  'Dhaka',
  'Bangladesh'
)
ON CONFLICT (id) DO NOTHING;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.default_facility() RETURNS uuid
LANGUAGE sql STABLE AS $$
  SELECT id FROM core.facility WHERE code = 'DTHC-FRD'
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.default_facility() IS
  'The founding facility. For backfilling facility_id when a later migration adds it to existing rows.';

-- ---------------------------------------------------------------------------
-- Enforcing the convention
-- ---------------------------------------------------------------------------

CREATE TABLE core.facility_scope_exemption (
  schema_name  text        NOT NULL,
  table_name   text        NOT NULL,
  reason       text        NOT NULL,
  exempted_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (schema_name, table_name),
  CONSTRAINT exemption_reason_meaningful CHECK (length(reason) >= 20)
);

COMMENT ON TABLE core.facility_scope_exemption IS
  'Tables deliberately not facility-scoped. A row here is a decision on the record, not an oversight.';

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'facility',
   'It is the facility. A self-reference would be circular and carries no information.'),
  ('core', 'facility_scope_exemption',
   'Registry of exemptions; it describes the schema itself and belongs to no facility.')
ON CONFLICT DO NOTHING;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_facility_scoping() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  missing text;
BEGIN
  SELECT string_agg(format('%s.%s', s, t), ', ' ORDER BY s, t) INTO missing
  FROM (
    SELECT n.nspname AS s, c.relname AS t
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname IN ('core', 'ledger', 'read', 'docs')
      AND c.relkind IN ('r', 'p')
      AND NOT EXISTS (
        SELECT 1 FROM pg_attribute a
        WHERE a.attrelid = c.oid AND a.attname = 'facility_id' AND a.attnum > 0 AND NOT a.attisdropped)
      AND NOT EXISTS (
        SELECT 1 FROM core.facility_scope_exemption e
        WHERE e.schema_name = n.nspname AND e.table_name = c.relname)
  ) unscoped;

  IF missing IS NOT NULL THEN
    RAISE EXCEPTION 'tables are neither facility-scoped nor exempt: %', missing
      USING HINT = 'Add a facility_id uuid NOT NULL REFERENCES core.facility(id) column, '
                   'or insert a row into core.facility_scope_exemption explaining why the '
                   'table belongs to no facility (D-61).';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_facility_scoping() IS
  'Raises unless every table in core, ledger, read and docs carries facility_id or is explicitly exempt.';

-- Fold it into the invariant set the runner checks after every migration.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_invariants() RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM core.assert_ledger_append_only();
  PERFORM core.assert_read_models_derived();
  PERFORM core.assert_research_isolated();
  PERFORM core.assert_facility_scoping();
END
$$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_invariants() RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM core.assert_ledger_append_only();
  PERFORM core.assert_read_models_derived();
  PERFORM core.assert_research_isolated();
END
$$;
-- +goose StatementEnd

DROP FUNCTION IF EXISTS core.assert_facility_scoping();
DROP TABLE IF EXISTS core.facility_scope_exemption;
DROP FUNCTION IF EXISTS core.default_facility();
DROP TABLE IF EXISTS core.facility;
DROP FUNCTION IF EXISTS core.assert_valid_timezone();
