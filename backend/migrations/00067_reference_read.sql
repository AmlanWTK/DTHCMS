-- `reference.read`: the clinic's dictionary gets a permission of its own (CP85, ADR-0036 §5).
--
-- # What was wrong
--
-- Thirteen routes serve reference data — units of measurement, the food composition table,
-- the published growth curves, the plausibility bands, the reference ranges, the allergy
-- reaction vocabulary, the observation code registry and its answer lists, the assessment
-- instruments, the exercise contraindication questions, the correction reason codes and the
-- prescription state machine. Every one of them declared a *patient* permission:
-- `observation.read.values`, `patient.read.allergies`, `prescription.read`.
--
-- Those permissions reach only the station being worked for the nine station roles
-- (ADR-0036 §1). A route serving a list of units has no patient in it, so no handler can
-- judge a resource, so the route guard refused it rather than enter a handler nobody would
-- check. **Measured consequence: nine stations' clinical forms could not load their
-- pickers.** The permission was the wrong one from the start; there is no downstream fix
-- that is not a resource invented to satisfy a checker.
--
-- # Who holds it: every role in the catalogue
--
-- There is no patient in this data, nothing sensitive, and nothing commercially secret.
-- Withholding it from a role protects nothing and breaks that role's forms. A permission
-- granted to everybody is close to no permission at all, and that is the honest description
-- of a dictionary — what it still buys is the one thing that would be lost by making these
-- routes public, which is that they require a valid session. Serving the clinic's reference
-- tables to the open internet is a different and much larger decision.
--
-- # The name
--
-- No `patient.` or `observation.` prefix, deliberately. `rbac.scopeFor` decides a role's
-- reach by prefix (`rbac.isClinical`), and a permission called `observation.reference.read`
-- would acquire station scope and recreate the defect exactly. Invariant 128 below holds
-- that property from the database side.

-- +goose Up

INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('reference.read', 'reference', 'read', '',
   'Read the clinic''s reference data: units, food composition, growth curves, plausibility bands, reference ranges, vocabularies and state machines. No patient in any of it.',
   false)
ON CONFLICT (code) DO UPDATE SET
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

-- Every role but RESEARCHER, without naming the rest: a list here would be a second
-- catalogue to keep in step, and the decision is precisely "all of them".
--
-- RESEARCHER is excluded because D-48 is the stronger rule and it is not about this data.
-- `assert_rbac_constraints()` refuses that role any permission whose resource is not
-- `research`, on the ground that identifiable access is a separate role granted separately
-- and visible as such — the same reason `formulary.read` is off it (CP75), and a formulary
-- holds no patient either. It costs nothing: a researcher holds none of the permissions the
-- thirteen routes used to declare, so there is no screen of theirs this would have fixed.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'reference.read' FROM core.role r
 WHERE r.code <> 'RESEARCHER'
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Invariant 128: reference data is served under a permission that is not about a patient
-- ---------------------------------------------------------------------------

-- **What this can and cannot assert, said plainly.**
--
-- The rule worth holding is "every route serving reference data declares `reference.read`
-- and not a patient permission". A database cannot hold that: there is no route table in
-- this schema and there should not be one — the route table is the router, and a copy of it
-- in SQL would be a second thing to keep in step that could be right while the router was
-- wrong. The route-level half is held instead by
-- `TestEveryReferenceRouteDeclaresTheReferencePermission` in cmd/api, which walks the real
-- router that `run()` assembles and checks each of the thirteen by name.
--
-- What a database *can* assert is the catalogue property the route rule depends on, and it
-- is not a formality — it is the half that would fail silently. Three things:
--
--   1. `reference.read` exists and is not sensitive. A migration that marked it sensitive
--      would blind registration and the pharmacist to the food table, via the deny rules,
--      and nothing in the route declarations would change.
--   2. Every role but RESEARCHER holds it. A role added later without the grant is a
--      station whose forms stop loading, and the failure appears as an empty picker rather
--      than as an error. RESEARCHER is named as an exception rather than left out silently,
--      because D-48 already refuses it and an invariant that quietly tolerated a second
--      missing role would be no check at all.
--   3. Its code carries none of the prefixes `rbac.isClinical` reads. This is the one that
--      catches the dangerous mistake: renaming it to `observation.reference.read` in a
--      later tidy-up would hand it station scope again and put all thirteen routes straight
--      back where they were, with every test still green because the permission would still
--      be held by everybody.
--
-- The prefix list is duplicated here from Go, which is a cost. It is paid because the thing
-- being guarded against is somebody editing the name in one place, and a check that read the
-- name from the same place it was edited would not be a check.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_reference_data_is_not_scoped_to_a_patient() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  clinical_prefixes text[] := ARRAY[
    'patient.', 'observation.', 'counseling.tick', 'records.', 'lab.', 'diagnosis.',
    'prescription.', 'ai.', 'education.', 'qa.'];
  prefix     text;
  sensitive  boolean;
  ungranted  text;
BEGIN
  SELECT p.is_sensitive INTO sensitive FROM core.permission p WHERE p.code = 'reference.read';
  IF sensitive IS NULL THEN
    RAISE EXCEPTION 'reference.read is not in the catalogue'
      USING HINT =
        'Thirteen routes serve the clinic''s reference data under it. Without the row they '
        'fall back to a patient permission, which reaches only the station being worked, '
        'and nine stations lose the pickers their forms are built out of.';
  END IF;

  IF sensitive THEN
    RAISE EXCEPTION 'reference.read is marked sensitive'
      USING HINT =
        'Blueprint §4.4''s blinded roles are refused every sensitive permission outright. '
        'Marking the dictionary sensitive would blind the registration desk and the '
        'pharmacist to a list of units and a food composition table, which is not what the '
        'blinding is for and would break their screens without changing a single route.';
  END IF;

  -- The name. This is the check that matters, because the mistake it catches looks like
  -- tidying up.
  FOREACH prefix IN ARRAY clinical_prefixes LOOP
    IF position(prefix in 'reference.read') = 1 THEN
      RAISE EXCEPTION 'reference.read begins with %, which rbac.isClinical reads as a patient permission', prefix
        USING HINT =
          'rbac.scopeFor gives anything with one of these prefixes station scope for the '
          'nine station roles. A reference permission that acquires station scope is '
          'refused on every route that declares it, because there is no patient for a '
          'handler to judge — which is the exact defect CP85 fixed. Keep the name outside '
          'this list.';
    END IF;
  END LOOP;

  SELECT string_agg(r.code, ', ' ORDER BY r.code) INTO ungranted
    FROM core.role r
   WHERE r.code <> 'RESEARCHER'
     AND NOT EXISTS (
     SELECT 1 FROM core.role_permission rp
      WHERE rp.role_id = r.id AND rp.permission_code = 'reference.read');

  IF ungranted IS NOT NULL THEN
    RAISE EXCEPTION 'these roles do not hold reference.read: %', ungranted
      USING HINT =
        'The dictionary is held by every role but RESEARCHER on purpose: there is no '
        'patient in it, nothing sensitive, and withholding it protects nothing while '
        'breaking that role''s forms. A role added without the grant is a screen whose '
        'pickers come back empty. Grant it, or write down why this one role may not read a '
        'list of units. RESEARCHER is the one exception and it is D-48''s, not this '
        'decision''s: that role reaches the de-identified marts and nothing else.';
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION core.assert_reference_data_is_not_scoped_to_a_patient() FROM PUBLIC;

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_reference_data_is_not_scoped_to_a_patient',
   'reference.read exists, is not sensitive, is held by every role but RESEARCHER (D-48), and carries no prefix that rbac.isClinical would read as a patient permission',
   128)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant
 WHERE function_name = 'assert_reference_data_is_not_scoped_to_a_patient';

DROP FUNCTION IF EXISTS core.assert_reference_data_is_not_scoped_to_a_patient();

DELETE FROM core.role_permission WHERE permission_code = 'reference.read';
DELETE FROM core.permission WHERE code = 'reference.read';
