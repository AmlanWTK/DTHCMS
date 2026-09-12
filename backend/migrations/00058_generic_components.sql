-- What a medicine is made of (CP78 Task 1, closing the hole docs/medication-rules.md §10 named).
--
-- # The defect this migration exists to close
--
-- `core.generic` holds a fixed-dose combination as one molecule name. "Sitagliptin + Metformin
-- hydrochloride" is a single row whose `name` is that whole string, and `DUP-GENERIC` — the
-- seeded duplicate-therapy rule — compares one generic name with another. So **Siglimet
-- prescribed alongside Comet does not match**: two different generic names, no duplicate, no
-- finding. That is metformin twice, and it is one of the commonest real prescribing errors in a
-- diabetes clinic.
--
-- CP77's author found it, wrote it up as "the single largest hole in the seeded rule set", and
-- said it should be closed before CP78 claims duplicate-therapy coverage. This is that closure.
-- The rule model needs nothing new: what changes is that a drug now knows its molecules, and the
-- duplicate predicate compares molecule *sets* rather than generic names.
--
-- # Why a table rather than parsing the name
--
-- The obvious cheap fix is to split `name` on " + " at read time. It is wrong three times over,
-- and each of the three is a silent wrong answer rather than an error:
--
--  1. "Cholecalciferol (Vitamin D3)" and "Insulin human (rDNA) - soluble/regular" are single
--     molecules whose names contain punctuation a parser has to be taught about, and
--     "Insulin degludec + Insulin aspart (premixed)" ends in a qualifier that belongs to the
--     product rather than to the second molecule. A parser that gets one of those wrong produces
--     a component nobody notices is missing.
--  2. The component name and the generic name are **different vocabularies**. "Metformin
--     hydrochloride" is a generic; the molecule is metformin. "Losartan potassium +
--     Hydrochlorothiazide" contains hydrochlorothiazide, which this clinic does not stock as a
--     generic at all. A components model that could only name things in the formulary would be a
--     model that cannot describe half the combinations in it.
--  3. **A parse cannot be reviewed.** Dr. Nahid can read 59 rows of "this medicine is made of
--     these molecules" and correct the ones that are wrong. He cannot review a regular expression,
--     and the thing that goes wrong with an unreviewable decomposition is not that it is wrong —
--     it is that nobody notices it is wrong.
--
-- So every one of the 59 is written out below, by name, with the reasoning where a name is not
-- self-evident. Anything not written out is **UNDETERMINED**, and undetermined is the default.
--
-- # Undetermined fails closed, and that is the whole design
--
-- `components_status` defaults to UNDETERMINED. A generic added tomorrow by a CSV import, by the
-- admin screen, or by a migration that forgot this table, is undetermined — and CP78's duplicate
-- detection answers **"cannot verify"** for it rather than "no duplicate". That is the direction
-- the default has to point: a new generic arriving as "determined, and composed of nothing" would
-- be a drug that silently cannot collide with anything.
--
-- Two locks on it, for the same reason CP77 put two on rule approval:
--
--   * `assert_every_determined_generic_has_its_components()` (invariant 116) refuses a DETERMINED
--     generic with no component rows, after every migration, in every environment;
--   * `formulary.Composition.Determined` in Go is `status = DETERMINED AND len(components) > 0`,
--     so a row that arrived some other way still fails closed at the point of use.
--
-- # Nothing here is deleted
--
-- A component is corrected by writing the correct set, not by removing the wrong one and leaving
-- a generic briefly composed of nothing — which is precisely the window in which a duplicate
-- check would answer "no duplicate" instead of "cannot verify".

-- +goose Up

-- ---------------------------------------------------------------------------
-- 1. The components
-- ---------------------------------------------------------------------------

CREATE TABLE core.generic_component (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  generic_id uuid NOT NULL REFERENCES core.generic(id),

  -- The order the molecules are written in on the pack. Carried so a screen can say
  -- "metformin + sitagliptin" the way the box does, rather than alphabetically.
  ordinal int NOT NULL,

  -- The molecule, in the **component vocabulary** rather than the generic vocabulary: the salt
  -- is dropped ("Metformin", not "Metformin hydrochloride"), the parenthetical synonym is
  -- dropped ("Cholecalciferol", not "Cholecalciferol (Vitamin D3)"), and the dose qualifier is
  -- dropped ("Aspirin", not "Aspirin (low dose)").
  --
  -- Dropping the salt is not cosmetic. Metformin hydrochloride and metformin embonate are the
  -- same molecule for every purpose a duplicate check has, and a clinic that later stocks the
  -- embonate would otherwise get two molecules that never collide. The same argument applies to
  -- amlodipine besilate against amlodipine maleate, and to every "-ate", "-ide" and "-ium" in
  -- the list below.
  molecule text NOT NULL,

  -- Where the decomposition came from. Required, like a rule's citation and a price's
  -- provenance: a component list with nothing behind it is one nobody can argue with.
  source_citation text NOT NULL,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT generic_component_molecule_present CHECK (btrim(molecule) <> ''),
  CONSTRAINT generic_component_ordinal_is_a_position CHECK (ordinal >= 1),
  CONSTRAINT generic_component_names_its_source CHECK (btrim(source_citation) <> '')
);

-- One molecule appears once in a medicine. A combination listing metformin twice is a data
-- error that would make a duplicate check answer about the medicine rather than the prescription.
CREATE UNIQUE INDEX generic_component_key
  ON core.generic_component (generic_id, lower(molecule));
CREATE UNIQUE INDEX generic_component_position
  ON core.generic_component (generic_id, ordinal);
-- The index the duplicate check reads: "which medicines contain this molecule".
CREATE INDEX generic_component_molecule_idx
  ON core.generic_component (lower(molecule));

SELECT core.attach_updated_at('core.generic_component');
GRANT SELECT, INSERT, UPDATE ON core.generic_component TO dthcms_app;
REVOKE DELETE, TRUNCATE ON core.generic_component FROM dthcms_app;

-- `core.generic` is global rather than facility-scoped (which molecules exist is pharmacology),
-- so its components are too.
INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'generic_component',
   'What a medicine is made of is pharmacology, not a clinic''s data — the same reason core.generic itself is unscoped.')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- 2. Whether anybody has determined them
-- ---------------------------------------------------------------------------

ALTER TABLE core.generic
  ADD COLUMN components_status text NOT NULL DEFAULT 'UNDETERMINED',
  ADD COLUMN components_source text NOT NULL DEFAULT '',
  ADD COLUMN components_determined_at timestamptz;

ALTER TABLE core.generic
  ADD CONSTRAINT generic_components_status_is_one_of_two
    CHECK (components_status IN ('DETERMINED', 'UNDETERMINED')),
  -- A determination is a claim somebody made on a day from a source, or it is not a
  -- determination. The same shape as a price's verification and a rule's approval.
  ADD CONSTRAINT generic_components_determination_is_attributed
    CHECK (components_status = 'UNDETERMINED'
           OR (btrim(components_source) <> '' AND components_determined_at IS NOT NULL)),
  ADD CONSTRAINT generic_components_undetermined_claims_nothing
    CHECK (components_status = 'DETERMINED'
           OR (btrim(components_source) = '' AND components_determined_at IS NULL));

COMMENT ON COLUMN core.generic.components_status IS
  'DETERMINED means somebody has written out which molecules this medicine contains. UNDETERMINED is the default and makes CP78 duplicate detection answer "cannot verify" rather than "no duplicate".';

-- ---------------------------------------------------------------------------
-- 3. The backfill
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
-- Records one medicine's molecules, by generic name, re-runnably.
--
-- **It never overwrites a determination somebody made.** Same reasoning as CP77's rule seed and
-- CP75's price seed: this function runs on every migration in every environment, and a backfill
-- that replaced what was there would quietly undo a correction Dr. Nahid made. If the generic is
-- already DETERMINED, this does nothing at all.
--
-- A generic name this clinic does not hold is skipped silently rather than raising: the 59 below
-- are this seed formulary's, and a facility that curated its own list should not fail to migrate
-- because it does not stock voglibose.
CREATE OR REPLACE FUNCTION core.determine_generic_components(
  p_generic_name text, p_molecules text[], p_source text) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  g_id uuid;
  g_status text;
  i int;
BEGIN
  SELECT id, components_status INTO g_id, g_status
    FROM core.generic WHERE lower(name) = lower(p_generic_name);
  IF g_id IS NULL OR g_status = 'DETERMINED' THEN
    RETURN;
  END IF;

  FOR i IN 1 .. array_length(p_molecules, 1) LOOP
    INSERT INTO core.generic_component (generic_id, ordinal, molecule, source_citation)
    VALUES (g_id, i, p_molecules[i], p_source)
    ON CONFLICT DO NOTHING;
  END LOOP;

  UPDATE core.generic
     SET components_status = 'DETERMINED',
         components_source = p_source,
         components_determined_at = now()
   WHERE id = g_id;
END
$$;
-- +goose StatementEnd

-- The citation for all 59. The molecule names are the INNs; the mapping from this clinic's
-- generic names to them is a reading of those names, which is why the column says so rather than
-- claiming a guideline said it.
--
-- **Every row below is Dr. Nahid's to correct.** None of it is a clinical judgement in the sense
-- a rule is — "Siglimet contains metformin" is written on the box — but the two places it could
-- be wrong are both places that matter: a combination decomposed into the wrong molecules would
-- produce a false duplicate, and one left out of the list entirely reads as UNDETERMINED and
-- makes every duplicate check involving it say "cannot verify".

-- --- single-molecule generics: the degenerate case of one component ---------
--
-- Written out rather than derived, so that "this generic has exactly one molecule" is a claim
-- somebody made rather than a consequence of the name having no plus sign in it.

SELECT core.determine_generic_components('Acarbose', ARRAY['Acarbose'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Amlodipine besilate', ARRAY['Amlodipine'],
  'WHO INN; the besilate is a salt and is dropped, so that amlodipine maleate would be the same molecule (CP78).');
SELECT core.determine_generic_components('Aspirin (low dose)', ARRAY['Aspirin'],
  'WHO INN; "(low dose)" is a dose qualifier carried in this formulary''s generic name, not part of the molecule (CP78).');
SELECT core.determine_generic_components('Atorvastatin calcium', ARRAY['Atorvastatin'],
  'WHO INN; the calcium is a salt and is dropped (CP78).');
SELECT core.determine_generic_components('Carbimazole', ARRAY['Carbimazole'],
  'WHO INN. Carbimazole is a prodrug of methimazole and they are NOT recorded as one molecule — see docs/medication-rules.md, open for Dr. Nahid (CP78).');
SELECT core.determine_generic_components('Cholecalciferol (Vitamin D3)', ARRAY['Cholecalciferol'],
  'WHO INN; "(Vitamin D3)" is a synonym in the generic name, not a second molecule (CP78).');
SELECT core.determine_generic_components('Dapagliflozin propanediol', ARRAY['Dapagliflozin'],
  'WHO INN; the propanediol is the hydrate form and is dropped (CP78).');
SELECT core.determine_generic_components('Dulaglutide', ARRAY['Dulaglutide'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Empagliflozin', ARRAY['Empagliflozin'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Enalapril maleate', ARRAY['Enalapril'],
  'WHO INN; the maleate is a salt and is dropped (CP78).');
SELECT core.determine_generic_components('Ezetimibe', ARRAY['Ezetimibe'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Fenofibrate', ARRAY['Fenofibrate'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Folic acid', ARRAY['Folic acid'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Gabapentin', ARRAY['Gabapentin'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Glibenclamide', ARRAY['Glibenclamide'],
  'WHO INN (glyburide in the USP). Molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Gliclazide', ARRAY['Gliclazide'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Glimepiride', ARRAY['Glimepiride'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Insulin aspart', ARRAY['Insulin aspart'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Insulin degludec', ARRAY['Insulin degludec'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Insulin detemir', ARRAY['Insulin detemir'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Insulin glargine', ARRAY['Insulin glargine'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Insulin glulisine', ARRAY['Insulin glulisine'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Insulin human (rDNA) - soluble/regular', ARRAY['Insulin human soluble'],
  'WHO INN "insulin human"; the soluble/regular form named so that the soluble half of a human premix is the same molecule (CP78).');
SELECT core.determine_generic_components('Insulin lispro', ARRAY['Insulin lispro'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Isophane insulin human (NPH)', ARRAY['Isophane insulin human'],
  'WHO INN "insulin human, isophane"; "(NPH)" is a synonym in the generic name (CP78).');
SELECT core.determine_generic_components('Levothyroxine sodium', ARRAY['Levothyroxine'],
  'WHO INN; the sodium is a salt and is dropped (CP78).');
SELECT core.determine_generic_components('Linagliptin', ARRAY['Linagliptin'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Liraglutide', ARRAY['Liraglutide'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Lisinopril', ARRAY['Lisinopril'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Losartan potassium', ARRAY['Losartan'],
  'WHO INN; the potassium is a salt and is dropped (CP78).');
SELECT core.determine_generic_components('Metformin hydrochloride', ARRAY['Metformin'],
  'WHO INN; the hydrochloride is a salt and is dropped. This is the row the whole migration is for: every metformin combination below names the same molecule (CP78).');
SELECT core.determine_generic_components('Methimazole', ARRAY['Methimazole'],
  'WHO INN (thiamazole). See the note on carbimazole (CP78).');
SELECT core.determine_generic_components('Olmesartan medoxomil', ARRAY['Olmesartan'],
  'WHO INN; the medoxomil is the prodrug ester and is dropped (CP78).');
SELECT core.determine_generic_components('Pioglitazone', ARRAY['Pioglitazone'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Pregabalin', ARRAY['Pregabalin'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Propylthiouracil', ARRAY['Propylthiouracil'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Ramipril', ARRAY['Ramipril'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Rosuvastatin calcium', ARRAY['Rosuvastatin'],
  'WHO INN; the calcium is a salt and is dropped (CP78).');
SELECT core.determine_generic_components('Saxagliptin', ARRAY['Saxagliptin'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Semaglutide', ARRAY['Semaglutide'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Simvastatin', ARRAY['Simvastatin'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Sitagliptin', ARRAY['Sitagliptin'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Telmisartan', ARRAY['Telmisartan'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Vildagliptin', ARRAY['Vildagliptin'],
  'WHO INN; molecule name read from core.generic.name (CP78).');
SELECT core.determine_generic_components('Voglibose', ARRAY['Voglibose'],
  'WHO INN; molecule name read from core.generic.name (CP78).');

-- --- the fourteen fixed-dose combinations ----------------------------------
--
-- These are the rows that close the hole. Seven of the fourteen contain metformin.

SELECT core.determine_generic_components(
  'Calcium lactate gluconate + Calcium carbonate + Vitamin D3',
  ARRAY['Calcium lactate gluconate', 'Calcium carbonate', 'Cholecalciferol'],
  'Product composition. Vitamin D3 is cholecalciferol and is named so, which is what makes this tablet collide with a separate vitamin D capsule (CP78).');
SELECT core.determine_generic_components(
  'Empagliflozin + Metformin hydrochloride', ARRAY['Empagliflozin', 'Metformin'],
  'Product composition; WHO INNs (CP78).');
SELECT core.determine_generic_components(
  'Glimepiride + Metformin hydrochloride', ARRAY['Glimepiride', 'Metformin'],
  'Product composition; WHO INNs (CP78).');
SELECT core.determine_generic_components(
  'Insulin aspart + Insulin aspart protamine', ARRAY['Insulin aspart', 'Insulin aspart protamine'],
  'Biphasic insulin aspart 30. The protaminated fraction is recorded as its own molecule, so that a premix beside a rapid analogue is visible rather than invisible (CP78).');
SELECT core.determine_generic_components(
  'Insulin degludec + Insulin aspart (premixed)', ARRAY['Insulin degludec', 'Insulin aspart'],
  'Insulin degludec/insulin aspart 70/30. "(premixed)" qualifies the product, not the second molecule (CP78).');
SELECT core.determine_generic_components(
  'Insulin lispro protamine + Insulin lispro', ARRAY['Insulin lispro protamine', 'Insulin lispro'],
  'Insulin lispro protamine suspension 75/25, written in the order the pack names it (CP78).');
SELECT core.determine_generic_components(
  'Linagliptin + Metformin hydrochloride', ARRAY['Linagliptin', 'Metformin'],
  'Product composition; WHO INNs (CP78).');
SELECT core.determine_generic_components(
  'Losartan potassium + Hydrochlorothiazide', ARRAY['Losartan', 'Hydrochlorothiazide'],
  'Product composition; WHO INNs. Hydrochlorothiazide is not stocked as a generic here, which is why a component is not a foreign key to core.generic (CP78).');
SELECT core.determine_generic_components(
  'Pioglitazone + Glimepiride', ARRAY['Pioglitazone', 'Glimepiride'],
  'Product composition; WHO INNs (CP78).');
SELECT core.determine_generic_components(
  'Pioglitazone + Metformin hydrochloride', ARRAY['Pioglitazone', 'Metformin'],
  'Product composition; WHO INNs (CP78).');
SELECT core.determine_generic_components(
  'Regular insulin human + Isophane insulin human (premix)',
  ARRAY['Insulin human soluble', 'Isophane insulin human'],
  'Biphasic isophane insulin 30/70. Both molecules are named exactly as the two single-agent human insulins above, which is what makes a premix beside regular insulin collide (CP78).');
SELECT core.determine_generic_components(
  'Sitagliptin + Metformin hydrochloride', ARRAY['Sitagliptin', 'Metformin'],
  'Product composition; WHO INNs. This is the Siglimet of docs/medication-rules.md §10 (CP78).');
SELECT core.determine_generic_components(
  'Telmisartan + Amlodipine besilate', ARRAY['Telmisartan', 'Amlodipine'],
  'Product composition; WHO INNs (CP78).');
SELECT core.determine_generic_components(
  'Vildagliptin + Metformin hydrochloride', ARRAY['Vildagliptin', 'Metformin'],
  'Product composition; WHO INNs (CP78).');

-- ---------------------------------------------------------------------------
-- 4. Invariants
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
-- A generic that says its components are known has some.
--
-- The hole this closes is narrow and nasty: a DETERMINED generic with no component rows would
-- have an empty molecule set, which intersects nothing, which reads to a duplicate check as
-- "definitely not a duplicate of anything". That is the exact false negative this whole migration
-- exists to remove, reintroduced by a row rather than by a rule.
--
-- The other direction is refused too. Component rows under an UNDETERMINED generic mean somebody
-- wrote the molecules and did not finish the determination, and a check reading the status would
-- ignore work that is sitting right there.
CREATE OR REPLACE FUNCTION core.assert_every_determined_generic_has_its_components() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  SELECT string_agg(g.name, ', ' ORDER BY g.name) INTO offender
    FROM core.generic g
   WHERE g.components_status = 'DETERMINED'
     AND NOT EXISTS (SELECT 1 FROM core.generic_component c WHERE c.generic_id = g.id);
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION
      'these generics claim their components are determined but have none: %', offender
      USING HINT = 'An empty molecule set collides with nothing, so a duplicate check would answer "no duplicate" instead of "cannot verify".';
  END IF;

  SELECT string_agg(g.name, ', ' ORDER BY g.name) INTO offender
    FROM core.generic g
   WHERE g.components_status = 'UNDETERMINED'
     AND EXISTS (SELECT 1 FROM core.generic_component c WHERE c.generic_id = g.id);
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION
      'these generics have component rows but are still marked UNDETERMINED: %', offender
      USING HINT = 'Finish the determination or remove the rows; a half-written decomposition is ignored by every check that reads the status.';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- One molecule, one spelling.
--
-- The component vocabulary is free text, which is what lets it name hydrochlorothiazide without
-- this clinic stocking it. The cost of free text is drift: "Metformin" in one row and "metformin
-- hydrochloride" in another are two molecules that never collide, and the resulting false
-- negative looks exactly like a correct answer. Matching is case-insensitive, so this checks that
-- no two rows differ only in case — a difference that would be invisible on every screen.
CREATE OR REPLACE FUNCTION core.assert_generic_components_have_one_spelling() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  SELECT string_agg(spellings, '; ') INTO offender
    FROM (
      SELECT string_agg(DISTINCT molecule, ' / ' ORDER BY molecule) AS spellings
        FROM core.generic_component
       GROUP BY lower(molecule)
      HAVING count(DISTINCT molecule) > 1
    ) drifted;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'one molecule is spelled more than one way: %', offender
      USING HINT = 'Two spellings of one molecule are two molecules that never collide.';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A component list is corrected by writing the right one, never by deleting the wrong one.
--
-- The same argument as CP77's `assert_medication_rule_history_is_kept`: `ALTER DEFAULT
-- PRIVILEGES` on `core` grants the application full CRUD, the GRANT above is additive, and
-- without the REVOKE the window between "delete the old components" and "insert the new ones" is
-- a window in which a DETERMINED generic is composed of nothing.
CREATE OR REPLACE FUNCTION core.assert_generic_components_are_not_deletable() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  SELECT string_agg(table_name, ', ' ORDER BY table_name) INTO offender
    FROM information_schema.role_table_grants
   WHERE grantee = 'dthcms_app' AND table_schema = 'core' AND privilege_type = 'DELETE'
     AND table_name = 'generic_component';
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION
      'the application role can delete from core.%, which opens a window in which a medicine is composed of nothing',
      offender;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_determined_generic_has_its_components',
   'no generic claims its molecules are known while having none — an empty molecule set collides with nothing', 116),
  ('assert_generic_components_have_one_spelling',
   'one molecule is spelled one way, so that two spellings are not two molecules that never collide', 117),
  ('assert_generic_components_are_not_deletable',
   'the application role cannot delete a generic''s components', 118)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_every_determined_generic_has_its_components',
  'assert_generic_components_have_one_spelling',
  'assert_generic_components_are_not_deletable');
DROP FUNCTION IF EXISTS core.assert_every_determined_generic_has_its_components();
DROP FUNCTION IF EXISTS core.assert_generic_components_have_one_spelling();
DROP FUNCTION IF EXISTS core.assert_generic_components_are_not_deletable();
DROP FUNCTION IF EXISTS core.determine_generic_components(text, text[], text);

ALTER TABLE core.generic
  DROP CONSTRAINT IF EXISTS generic_components_status_is_one_of_two,
  DROP CONSTRAINT IF EXISTS generic_components_determination_is_attributed,
  DROP CONSTRAINT IF EXISTS generic_components_undetermined_claims_nothing;
ALTER TABLE core.generic
  DROP COLUMN IF EXISTS components_status,
  DROP COLUMN IF EXISTS components_source,
  DROP COLUMN IF EXISTS components_determined_at;

DROP TABLE IF EXISTS core.generic_component;

DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name = 'generic_component';
