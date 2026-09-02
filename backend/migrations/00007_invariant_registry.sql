-- The invariant set becomes data.
--
-- Written because of a small lie CP15 introduced. The migration runner logs
--
--   database invariants verified  checked="ledger append-only, read models derived,
--                                          research isolated, facility scoping"
--
-- and that string was hard-coded. CP15 added two more assertions, so six ran and the log
-- named four. Nobody would notice until an incident, when someone reads that line to find
-- out what was actually checked and is told less than the truth by a system that appears
-- to be reporting precisely.
--
-- The fix is not a longer string. A list maintained in two places drifts again the next
-- time; this is the second time already, counting the one that prompted it. So the set
-- becomes a table, core.assert_invariants() iterates it, and the runner logs what the
-- table says. Adding an assertion is one INSERT, and the log line is then correct by
-- construction rather than by remembering.

-- +goose Up

CREATE TABLE ops.invariant (
  schema_name   text        NOT NULL DEFAULT 'core',
  function_name text        NOT NULL,

  -- Read by a human during an incident, so it says what is guaranteed rather than what
  -- the function is called.
  description   text        NOT NULL,

  -- Order of evaluation. Cheap structural checks run before the ones that scan the
  -- catalogue, so the first failure is usually the root cause rather than a consequence.
  sequence      integer     NOT NULL,

  added_at      timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (schema_name, function_name),
  CONSTRAINT invariant_description_meaningful CHECK (length(description) >= 10)
);

COMMENT ON TABLE ops.invariant IS
  'Every structural guarantee DTHCMS makes about its database. core.assert_invariants() runs exactly this list.';

-- A row naming a function that does not exist would turn every migration run into a
-- runtime error with an unhelpful message. Refuse it at the moment it is written instead.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ops.assert_invariant_exists() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF to_regprocedure(format('%I.%I()', NEW.schema_name, NEW.function_name)) IS NULL THEN
    -- Plain %, not %I: RAISE has no identifier-quoting verb, so %I emitted a literal
    -- "I" and the message read "no such function coreI.assert_nonexistentI()". An error
    -- message that is itself wrong is worse than none.
    RAISE EXCEPTION 'no such function %.%()', NEW.schema_name, NEW.function_name
      USING HINT = 'Register an invariant in the same migration that creates it, after '
                   'the CREATE FUNCTION statement.';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER invariant_function_exists
  BEFORE INSERT OR UPDATE ON ops.invariant
  FOR EACH ROW EXECUTE FUNCTION ops.assert_invariant_exists();

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_ledger_append_only',  'the event ledger is append-only', 10),
  ('assert_read_models_derived', 'read models are derived, not written by the application', 20),
  ('assert_research_isolated',   'research cannot reach identifiable data', 30),
  ('assert_facility_scoping',    'every table is facility-scoped or explicitly exempt', 40),
  ('assert_users_undeletable',   'users and role grants cannot be deleted', 50),
  ('assert_rbac_constraints',    'the blueprint''s access rules hold', 60)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_invariants() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  inv record;
BEGIN
  FOR inv IN
    SELECT schema_name, function_name FROM ops.invariant ORDER BY sequence, function_name
  LOOP
    EXECUTE format('SELECT %I.%I()', inv.schema_name, inv.function_name);
  END LOOP;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_invariants() IS
  'Runs every assertion registered in ops.invariant, in order. Adding one is an INSERT.';

-- What the runner logs. Returned from the same table it iterates, so the log cannot
-- describe a different set from the one that ran.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.invariants_checked() RETURNS text
LANGUAGE sql STABLE AS $$
  SELECT coalesce(string_agg(description, ', ' ORDER BY sequence, function_name), 'none registered')
    FROM ops.invariant
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.invariants_checked() IS
  'Human-readable list of what assert_invariants() just verified, for the migration log.';

-- +goose Down

DROP FUNCTION IF EXISTS core.invariants_checked();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_invariants() RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM core.assert_ledger_append_only();
  PERFORM core.assert_read_models_derived();
  PERFORM core.assert_research_isolated();
  PERFORM core.assert_facility_scoping();
  PERFORM core.assert_users_undeletable();
  PERFORM core.assert_rbac_constraints();
END
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS invariant_function_exists ON ops.invariant;
DROP FUNCTION IF EXISTS ops.assert_invariant_exists();
DROP TABLE IF EXISTS ops.invariant;
