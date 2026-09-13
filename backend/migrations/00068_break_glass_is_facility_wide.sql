-- The emergency door gets a permission of its own, and it is facility-wide (CP22, ADR-0036 §2(b)).
--
-- # What was wrong
--
-- `POST /v1/audit/break-glass` declared `patient.read.clinical` or `patient.read.demographics`,
-- whichever the caller happened to hold. Both begin with `patient.`, which `rbac.isClinical`
-- reads as a permission about a patient, so `rbac.scopeFor` gives both station scope for the
-- nine station roles. The route judges no resource — there is nothing to judge, since the
-- whole act is "let me reach something my station does not" — so the guard refused it with
-- `scope_not_enforced` for exactly those roles.
--
-- **Measured consequence: the nutritionist, the exercise specialist, the history officer, the
-- counsellor, the anthropometry officer, the clinical assistant, the pharmacist and the
-- prescription educator could not open the emergency door at all.** ADR-0036 §1 had just made
-- that door the only way past a reach refusal, so for those eight there was no way past.
--
-- Two independent arguments say the old shape was wrong, and neither is about convenience:
--
--   1. Break-glass exists *to cross a station boundary*. Scoping it by station is a fire
--      escape locked from the inside.
--   2. It is the same incoherence ADR-0036 §2 found in registration. A permission exercised
--      *before or outside* a station relationship cannot be narrowed by one; there is no
--      relationship yet for the rule to read.
--
-- And a third thing the old shape did quietly, which is worse than either: `anyOf` takes the
-- *weaker* of the two permissions. `patient.read.demographics` is facility-wide for the
-- registration desk and the patient relations officer (§2 and the reviewing roles), so both
-- could open the emergency door, while the nutritionist standing in front of the patient
-- could not. A door whose width is decided by whichever read permission the caller happens to
-- carry is not a door with a policy.
--
-- # Why facility-wide is safe here, said plainly
--
-- Because scope was never what made this act safe. Four other things are, and each of them is
-- somewhere a reviewer can check:
--
--   * **Its own sensitive permission**, held by nine roles and by no administrative desk.
--     Before this migration there was no break-glass permission at all.
--   * **A step-up with its own purpose.** `httpx.RequireStepUp` with `break_glass` consumes a
--     fresh second factor and spends it; the next door asks again.
--   * **A bound lifetime.** Four hours by default, twenty-four at most, and the ceiling is the
--     `break_glass_bounded` CHECK on this table in 00012 rather than a constant in Go.
--   * **A record written before the access is usable.** `BreakGlass.Open` closes the door again
--     if the audit row cannot be chained, and raises a high-severity alert on every
--     administrator's console in the same breath.
--
-- # The name
--
-- `emergency.break_glass`, with no `patient.` prefix, and that is load-bearing rather than
-- tidy. `rbac.scopeFor` decides reach by prefix; a permission called `patient.break_glass`
-- would acquire station scope the moment somebody named it that way in a later tidy-up, and
-- put the door straight back behind the refusal it was just taken out of — with every test
-- still green, because the permission would still be held by the same nine roles. Invariant
-- 129 below holds that property from the database side, exactly as invariant 128 does for
-- `reference.read`.

-- +goose Up

-- Sensitive, and the reason is the blinding rather than the danger. The permission reveals no
-- diagnosis by itself; what it widens the reach *to* is a patient's whole record, and §4.4's
-- blinded roles are not the ones who decide to open one. Marking it sensitive means
-- `assert_rbac_constraints()` refuses the grant to REGISTRATION and PHARMACIST outright, so a
-- later migration cannot quietly restore the state this one is correcting.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('emergency.break_glass', 'emergency', 'break_glass', '',
   'Open the emergency door: reach a patient record this role does not reach, with a typed justification, a fresh second factor, a bounded lifetime and an alert on every administrator''s console.',
   true)
ON CONFLICT (code) DO UPDATE SET
  resource = EXCLUDED.resource, action = EXCLUDED.action,
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

-- The nine roles that stand in front of a patient. Named one by one rather than derived,
-- because "which roles may open the emergency door" is a list somebody should have to edit
-- deliberately — a `WHERE` clause that happened to match a role added next year would grant it
-- silently, and this is the one permission where that is unacceptable.
--
-- REGISTRATION and PHARMACIST are absent because §4.4 blinds them and the assertion above
-- refuses them. RECORDS is absent because ADR-0036 §2 already gives it the facility. QA, CRM,
-- ADMIN, HR, RESEARCHER and FIELD_WORKER are absent because none of them delivers care at a
-- station, and the administrator in particular is the person who *acknowledges* the alarm.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'emergency.break_glass' FROM core.role r
 WHERE r.code IN ('ANTHROPOMETRY', 'COUNSELOR', 'HISTORY', 'CLINICAL_ASSISTANT',
                  'NUTRITIONIST', 'EXERCISE', 'RX_EDUCATOR', 'JUNIOR_DOCTOR', 'PHYSICIAN')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Invariant 129: the emergency door is not scoped to a station
-- ---------------------------------------------------------------------------

-- **What this can and cannot assert.**
--
-- The rule worth holding is "the break-glass route is usable by a station role". A database
-- cannot hold that: there is no route table in this schema and there should not be one, for
-- the reason invariant 128 gives at length. The route-level half is held by
-- `TestAStationRoleCanBreakTheGlassThroughTheRealRouter` in cmd/api, which drives the real
-- router and the real middleware as a nutritionist.
--
-- What a database *can* assert is the catalogue property that route rule depends on, and it is
-- the half that would fail silently. Four things:
--
--   1. Exactly one permission in the catalogue is the emergency door. The door is found by
--      `action = 'break_glass'` and **not** by its code, which is what lets check 2 mean
--      anything: a check that looked the row up by the name it was about to assert could
--      only ever confirm that a literal equals itself.
--   2. The code that row carries begins with none of the prefixes `rbac.isClinical` reads.
--      This is the one that catches the dangerous mistake, and the mistake looks like tidying
--      up: renaming it to `patient.break_glass` hands it station scope again and recreates
--      the defect exactly, with every test still green, because the permission would still be
--      held by the same nine roles and every Go constant would still resolve.
--   3. It is sensitive, and no blinded role holds it. The first half is what makes the deny
--      rules and `assert_rbac_constraints` refuse the grant to registration and the
--      pharmacist; the second is asserted here anyway rather than left to that function,
--      because an invariant that depends on another invariant still being present is not a
--      check.
--   4. The nine roles that are supposed to hold it do. A role quietly dropped from the grant
--      is a clinician who discovers at the bedside that the fire escape is locked, and the
--      failure appears as a missing sidebar entry rather than as an error.
--
-- The prefix list is duplicated here from Go, which is a cost, and it is paid for the reason
-- 00067 paid it: the thing being guarded against is somebody editing the name in one place,
-- and a check that read the name from the same place it was edited would not be a check.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_break_glass_is_not_station_scoped() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  clinical_prefixes text[] := ARRAY[
    'patient.', 'observation.', 'counseling.tick', 'records.', 'lab.', 'diagnosis.',
    'prescription.', 'ai.', 'education.', 'qa.'];
  door_roles text[] := ARRAY[
    'ANTHROPOMETRY', 'COUNSELOR', 'HISTORY', 'CLINICAL_ASSISTANT', 'NUTRITIONIST',
    'EXERCISE', 'RX_EDUCATOR', 'JUNIOR_DOCTOR', 'PHYSICIAN'];
  door      text;
  doors     int;
  prefix    text;
  sensitive boolean;
  offending text;
BEGIN
  -- Found by what it *does*, not by what it is called. Looking the row up by its code and
  -- then asserting something about that code would be asserting that a literal equals
  -- itself; looking it up by its action means a rename is visible to every check below.
  SELECT count(*) INTO doors FROM core.permission p WHERE p.action = 'break_glass';
  IF doors <> 1 THEN
    RAISE EXCEPTION 'the catalogue holds % permissions whose action is break_glass; it must hold exactly one', doors
      USING HINT =
        'ADR-0036 §1 makes break-glass the only way past a reach refusal, so a catalogue with '
        'no door has no way past at all, and one with two has two doors to watch and one '
        'audit trail. Add the permission back, or remove the duplicate.';
  END IF;

  SELECT p.code, p.is_sensitive INTO door, sensitive
    FROM core.permission p WHERE p.action = 'break_glass';

  -- The check that matters. Everything else here is hygiene; this one catches the rename.
  FOREACH prefix IN ARRAY clinical_prefixes LOOP
    IF position(prefix in door) = 1 THEN
      RAISE EXCEPTION '% begins with %, which rbac.isClinical reads as a permission about a patient', door, prefix
        USING HINT =
          'rbac.scopeFor gives anything with one of these prefixes station scope for the nine '
          'station roles, and the break-glass route judges no resource — there is nothing to '
          'judge, because the act is asking to reach past a station. A station-scoped '
          'break-glass is refused for exactly the people who have nowhere else to go, which '
          'is the defect ADR-0036 §2(b) fixed. Keep the name outside this list.';
    END IF;
  END LOOP;

  IF NOT sensitive THEN
    RAISE EXCEPTION '% is not marked sensitive', door
      USING HINT =
        'Sensitivity is what refuses this permission to blueprint §4.4''s blinded roles, in '
        'the deny rules and in assert_rbac_constraints alike. Unmarked, the registration desk '
        'and the pharmacist could be granted the emergency door — which is the state ADR-0036 '
        '§2(b) corrected, where they could open it and the nutritionist could not.';
  END IF;

  SELECT string_agg(r.code, ', ' ORDER BY r.code) INTO offending
    FROM core.role_permission rp
    JOIN core.role r ON r.id = rp.role_id
   WHERE rp.permission_code = door
     AND r.code IN ('REGISTRATION', 'PHARMACIST');

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'blueprint §4.4 blinds these roles, and they hold the emergency door: %', offending
      USING HINT =
        'The door opens a patient''s clinical record. A role blinded to that record is not the '
        'role that decides to open it. Revoke the grant, or amend §4.4 and this assertion in '
        'the same change.';
  END IF;

  -- `want(role_code)` rather than `AS code`: named `code`, the correlated reference inside
  -- the subquery resolves to `core.role.code` — the inner scope wins — so the condition reads
  -- `r.code = r.code`, the EXISTS is true for every role, and the check silently passes
  -- whatever the grants are. It did, and a mutation caught it.
  SELECT string_agg(want.role_code, ', ' ORDER BY want.role_code) INTO offending
    FROM unnest(door_roles) AS want(role_code)
   WHERE NOT EXISTS (
     SELECT 1 FROM core.role_permission rp
       JOIN core.role r ON r.id = rp.role_id
      WHERE r.code = want.role_code AND rp.permission_code = door);

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'these roles do not hold the emergency door: %', offending
      USING HINT =
        'These nine stand in front of a patient and can be refused by ADR-0036 §1''s reach '
        'rule. A role dropped from the grant is a clinician who finds out at the bedside that '
        'the fire escape is locked, and it shows up as a missing sidebar entry rather than as '
        'an error. Restore the grant, or write down why this role may not open the door.';
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION core.assert_break_glass_is_not_station_scoped() FROM PUBLIC;

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_break_glass_is_not_station_scoped',
   'emergency.break_glass exists, is sensitive, carries no prefix rbac.isClinical would read as a permission about a patient, is held by the nine clinical roles and by neither of the two blueprint §4.4 blinds',
   129)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant
 WHERE function_name = 'assert_break_glass_is_not_station_scoped';

DROP FUNCTION IF EXISTS core.assert_break_glass_is_not_station_scoped();

DELETE FROM core.role_permission WHERE permission_code = 'emergency.break_glass';
DELETE FROM core.permission WHERE code = 'emergency.break_glass';
