-- Conventions every later migration relies on.
--
-- Three things live here, and nothing else: the updated_at trigger that every mutable
-- table will use, the bookkeeping table the migration runner writes its checksums to,
-- and the assertion functions that check the guarantees of 00002 are still true.
--
-- The assertions matter more than they look. ALTER DEFAULT PRIVILEGES applies only to
-- objects created by the role that issued it, so the grants in 00002 hold exactly as
-- long as every migration is applied by the same role. That is a real condition, and it
-- is invisible: nothing fails, the tables simply arrive without the intended privileges,
-- and the ledger becomes writable without anyone touching a line of policy. So it is
-- checked, by the runner, at the end of every migration run, and by a test in CI.

-- +goose Up

-- ---------------------------------------------------------------------------
-- updated_at
--
-- Set by the database, not by the application. An application-set timestamp records
-- when the application believed it wrote; this records when the row actually changed.
-- The difference shows up the first time a row is corrected by hand during an incident.
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.set_updated_at() IS
  'BEFORE UPDATE trigger: stamps updated_at from the database clock.';

-- Attaching the trigger by hand in every migration is three lines that are easy to
-- forget and impossible to notice missing. This makes it one.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.attach_updated_at(target regclass) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  trigger_name text := 'set_updated_at';
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger
    WHERE tgrelid = target AND tgname = trigger_name AND NOT tgisinternal
  ) THEN
    EXECUTE format(
      'CREATE TRIGGER %I BEFORE UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION core.set_updated_at()',
      trigger_name, target::text);
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.attach_updated_at(regclass) IS
  'Idempotently attaches the set_updated_at trigger to a table.';

-- ---------------------------------------------------------------------------
-- Migration bookkeeping
--
-- goose records which migrations ran. It does not record what they contained. An
-- already-applied migration that is later edited — a typo fixed, a column widened —
-- will never run again, so the file on disk and the schema in the database quietly
-- disagree, and the next environment built from scratch gets a different database from
-- the one already in production. Storing a hash of each file at the moment it is
-- applied turns that into an error message instead of a mystery.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS ops.migration_checksum (
  version      bigint      PRIMARY KEY,
  filename     text        NOT NULL,
  sha256       text        NOT NULL,
  applied_at   timestamptz NOT NULL DEFAULT now(),
  applied_by   text        NOT NULL DEFAULT current_user
);

COMMENT ON TABLE ops.migration_checksum IS
  'SHA-256 of each migration file as applied. Drift means a migration was edited after it ran.';
COMMENT ON COLUMN ops.migration_checksum.applied_by IS
  'The database role that applied it. Grants from ALTER DEFAULT PRIVILEGES depend on this being stable.';

-- ---------------------------------------------------------------------------
-- Assertions
--
-- These are not tests. Tests run where someone chose to run them; these run on every
-- migration, in every environment, including the one nobody remembered to test.
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_ledger_append_only() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dthcms_app') THEN
    RAISE EXCEPTION 'role dthcms_app does not exist; migration 00002 has not been applied';
  END IF;

  -- Any privilege beyond SELECT and INSERT on a ledger table breaks the guarantee that
  -- a clinical event, once written, is a fact of history rather than a mutable row.
  SELECT string_agg(format('%s.%s (%s)', schemaname, tablename, privs), ', ' ORDER BY tablename)
    INTO offending
  FROM (
    SELECT n.nspname AS schemaname,
           c.relname AS tablename,
           concat_ws('+',
             CASE WHEN has_table_privilege('dthcms_app', c.oid, 'UPDATE')   THEN 'UPDATE'   END,
             CASE WHEN has_table_privilege('dthcms_app', c.oid, 'DELETE')   THEN 'DELETE'   END,
             CASE WHEN has_table_privilege('dthcms_app', c.oid, 'TRUNCATE') THEN 'TRUNCATE' END
           ) AS privs
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'ledger'
      AND c.relkind IN ('r', 'p')
      AND (has_table_privilege('dthcms_app', c.oid, 'UPDATE')
        OR has_table_privilege('dthcms_app', c.oid, 'DELETE')
        OR has_table_privilege('dthcms_app', c.oid, 'TRUNCATE'))
  ) violations;

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION
      'the event ledger is not append-only: dthcms_app holds % ', offending
      USING HINT = 'A migration granted more than SELECT and INSERT on a ledger table, '
                   'or was applied by a role other than the one that ran 00002, so '
                   'ALTER DEFAULT PRIVILEGES did not apply. Revoke the privilege and '
                   'apply migrations as a single owner role.';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_ledger_append_only() IS
  'Raises unless dthcms_app holds only SELECT and INSERT on every table in ledger.';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_read_models_derived() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  -- A projection the application can write to is a projection that will, one afternoon,
  -- be corrected directly instead of by replaying the events. After that it agrees with
  -- nothing and no rebuild can be trusted.
  SELECT string_agg(format('read.%s', c.relname), ', ' ORDER BY c.relname)
    INTO offending
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'read'
    AND c.relkind IN ('r', 'p')
    AND (has_table_privilege('dthcms_app', c.oid, 'INSERT')
      OR has_table_privilege('dthcms_app', c.oid, 'UPDATE')
      OR has_table_privilege('dthcms_app', c.oid, 'DELETE'));

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'dthcms_app can write to derived read models: %', offending
      USING HINT = 'Read models are rebuilt from the ledger by the projector. '
                   'Revoke write privileges from dthcms_app.';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_read_models_derived() IS
  'Raises if dthcms_app holds any write privilege in the read schema.';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_research_isolated() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  reachable text;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dthcms_research') THEN
    RAISE EXCEPTION 'role dthcms_research does not exist; migration 00002 has not been applied';
  END IF;

  -- Anonymisation that depends on the analyst querying the right schema is not
  -- anonymisation. The identified schemas must be unreachable, not merely discouraged.
  SELECT string_agg(s, ', ' ORDER BY s) INTO reachable
  FROM unnest(ARRAY['core', 'ledger', 'read', 'docs']) AS s
  WHERE has_schema_privilege('dthcms_research', s, 'USAGE');

  IF reachable IS NOT NULL THEN
    RAISE EXCEPTION 'dthcms_research can reach identified schemas: %', reachable
      USING HINT = 'Research holds USAGE on the research schema only (implementation plan 9.8).';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_research_isolated() IS
  'Raises if dthcms_research can reach any schema holding identifiable data.';

-- One call for the runner to make. Adding a guarantee later means adding it here, not
-- remembering to add it to the Go code as well.
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

COMMENT ON FUNCTION core.assert_invariants() IS
  'Every structural guarantee DTHCMS makes about its database. Called after each migration run.';

-- +goose Down

DROP FUNCTION IF EXISTS core.assert_invariants();
DROP FUNCTION IF EXISTS core.assert_research_isolated();
DROP FUNCTION IF EXISTS core.assert_read_models_derived();
DROP FUNCTION IF EXISTS core.assert_ledger_append_only();
DROP TABLE IF EXISTS ops.migration_checksum;
DROP FUNCTION IF EXISTS core.attach_updated_at(regclass);
DROP FUNCTION IF EXISTS core.set_updated_at();
