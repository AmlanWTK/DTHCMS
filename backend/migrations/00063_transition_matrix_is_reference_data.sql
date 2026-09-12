-- +goose Up

-- The application may not rewrite the rules it is judged by (CP80).
--
-- # What was wrong
--
-- CP80 made the prescription state machine *data*: `core.prescription_transition` holds the
-- twelve legal edges, the Go machine is built from those rows at start-up, and the trigger
-- `prescription_transitions_legally` checks a proposed change against them. That is the right
-- design — the application and the database cannot disagree about what is legal — and it puts
-- the whole guarantee into one table.
--
-- `ALTER DEFAULT PRIVILEGES` then granted `dthcms_app` INSERT, UPDATE and DELETE on it, as it
-- does for every new table in `core`. So the application role could write the matrix it is
-- judged by. Verified against the running database as `dthcms_app_local`:
--
--   DELETE FROM core.prescription_transition;                    -- 12 rows -> 0
--   INSERT INTO core.prescription_transition (...)               -- 12 rows -> 13
--     VALUES ('SIGNED','DRAFT','PRESCRIPTION_UNSIGNED', ...);
--
-- The second one is the escalation, and it defeats CP80's first acceptance criterion —
-- *signed prescriptions cannot be modified by any path*. With `SIGNED -> DRAFT` made legal,
-- a signed prescription moves back to DRAFT, and `prescription_item_is_frozen()` permits item
-- writes precisely while the parent is DRAFT. Three triggers hold, and the fourth is handed
-- the key. The first one is a denial of service: an empty matrix refuses every transition, so
-- no prescription in the clinic can be signed, printed or dispensed until somebody notices.
--
-- This is the same shape as the defect CP75 found on its own new tables, and the reason is the
-- same: reference data that the application only ever reads inherits write privileges from a
-- default that was written for the tables the application does write.
--
-- # What is done about it
--
-- The two reference tables become read-only to the application, and an invariant says so, so
-- that the next table of this kind fails `migrate verify` rather than being found by hand.
-- `core.prescription_status` goes with it for the same reason: a status vocabulary the
-- application can edit is a machine whose states the application can invent.

REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON core.prescription_transition FROM dthcms_app;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON core.prescription_status     FROM dthcms_app;

COMMENT ON TABLE core.prescription_transition IS
  'The legal edges of the prescription state machine. Reference data: migrations write it, '
  'the application only reads it, and invariant 124 keeps it that way (CP80).';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_the_state_machine_is_not_writable_by_the_application()
RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  -- Written as a list rather than one table, because the next one added to it is the point.
  -- A table here is one the application reads to find out what it is allowed to do; being
  -- able to write it is being able to decide that for itself.
  SELECT string_agg(t.relname || ' (' || t.privilege || ')', ', ' ORDER BY t.relname, t.privilege)
    INTO offender
    FROM (
      SELECT c.relname, p.privilege
        FROM (VALUES ('prescription_transition'), ('prescription_status')) AS want(relname)
        JOIN pg_class c ON c.relname = want.relname
        JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'core'
       CROSS JOIN (VALUES ('INSERT'), ('UPDATE'), ('DELETE'), ('TRUNCATE')) AS p(privilege)
       WHERE has_table_privilege('dthcms_app', c.oid, p.privilege)
    ) AS t;

  IF offender IS NOT NULL THEN
    RAISE EXCEPTION
      'the application can write the state machine it is judged by: %', offender
      USING HINT =
        'core.prescription_transition is what prescription_transitions_legally() checks against. '
        'An application that can INSERT into it can make SIGNED -> DRAFT legal and then edit a '
        'signed prescription, because items are writable while the parent is DRAFT. One that can '
        'DELETE from it can stop every prescription in the clinic being signed. REVOKE rather '
        'than relying on ALTER DEFAULT PRIVILEGES, which grants writes on every new core table.';
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION core.assert_the_state_machine_is_not_writable_by_the_application() FROM PUBLIC;

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_the_state_machine_is_not_writable_by_the_application',
   'the application cannot write the prescription state machine or its status vocabulary', 124)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant
 WHERE function_name = 'assert_the_state_machine_is_not_writable_by_the_application';
DROP FUNCTION IF EXISTS core.assert_the_state_machine_is_not_writable_by_the_application();

GRANT INSERT, UPDATE, DELETE ON core.prescription_transition TO dthcms_app;
GRANT INSERT, UPDATE, DELETE ON core.prescription_status     TO dthcms_app;
