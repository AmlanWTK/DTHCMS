-- The allergy hard stop (CP54, §3 step 4's checkpoint, [R-01]).
--
-- # What a hard stop actually has to be
--
-- Criterion 4 is four words long and it decides the whole design: **the gate cannot be
-- bypassed by any client.** Not "the handler refuses it". Not "the mobile app disables the
-- button". A rule enforced in an application is a rule that holds for the paths somebody
-- remembered — and the path nobody remembers is the one that matters, because it is a
-- projection rebuild, a support script, or next year's second client.
--
-- So the gate is a trigger on `core.queue_entry`. A patient cannot be put in the queue for
-- any station after the history station unless somebody has recorded their allergy status.
-- Every client meets it, including the ones that do not exist yet.
--
-- # Why "unable to assess" exists, and why there is no override
--
-- The obvious objection to an absolute gate is the unconscious patient, or the child brought
-- in by a neighbour who does not know. Blocking them from the consultant would be a worse
-- outcome than an unrecorded allergy, so the usual answer is an override: a button that
-- advances the patient anyway, with a reason.
--
-- That answer is wrong here, and the plan says why in its own risk note: *"operators may
-- assert NKA reflexively to clear the gate"*. An override is the same failure with better
-- manners — a gate with a way past it is a gate people learn the shape of, and within a month
-- the override is the normal path.
--
-- The honest answer is a third state. **UNABLE_TO_ASSESS** is allergy status: somebody looked,
-- somebody is named, and the record says what was found — which is *not* that the patient has
-- no allergies. The medication safety engine will treat the two very differently, and it can
-- only do that because they are different rows rather than one row and a missing one.
--
-- So: three ways to satisfy the gate, all of them a positive act by a named person, and no way
-- around it.
--
-- # Why allergies are not history items
--
-- CP54 says allergies are "deliberately separate because of the hard stop", and the separation
-- survives into the schema (ADR-0028). Everything in a history is a fact somebody may record
-- or not; an allergy is a **gate**, and the difference is not the content but what the rest of
-- the system may do when the answer is missing. A module boundary is what stops a later change
-- to history's write path quietly weakening a prescribing block.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------

-- Reading allergies is not sensitive in §4.4's sense, and that is deliberate rather than an
-- oversight. `patient.read.allergies` already exists and is held by the pharmacist and the
-- prescription educator — roles blinded to diagnoses — precisely because an allergy has to
-- reach the person handing over the medicine. Recording one is the history station's act.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('allergy.write', 'allergy', 'write', '',
   'Record an allergy, or assert that there are none known', false)
ON CONFLICT (code) DO UPDATE SET
  resource = EXCLUDED.resource, action = EXCLUDED.action, scope = EXCLUDED.scope,
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'allergy.write' FROM core.role r
 WHERE r.code IN ('HISTORY', 'CLINICAL_ASSISTANT', 'JUNIOR_DOCTOR', 'PHYSICIAN')
ON CONFLICT DO NOTHING;

-- Every clinical role that meets the patient can already read allergies; make sure the ones
-- added since that grant have it, because criterion 3 is "on every screen".
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'patient.read.allergies' FROM core.role r
 WHERE r.code IN ('HISTORY', 'CLINICAL_ASSISTANT', 'JUNIOR_DOCTOR', 'PHYSICIAN',
                  'NUTRITIONIST', 'EXERCISE', 'PHARMACIST', 'RX_EDUCATOR', 'QA')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- What an allergy did
-- ---------------------------------------------------------------------------

-- Coded, for the reason every vocabulary in this system is coded: "rash", "Rash" and "skin
-- came out" are one reaction, and a safety engine cannot match on four spellings. The set is
-- short on purpose — a reaction list nobody can hold in their head is one people pick the
-- first item from.
CREATE TABLE core.allergy_reaction (
  reaction text PRIMARY KEY,

  display_en text NOT NULL,
  display_bn text NOT NULL,

  -- Whether this reaction is, on its own, an emergency. Stored rather than inferred from the
  -- severity the operator chose: anaphylaxis is severe whatever anybody ticked, and a screen
  -- that let "anaphylaxis, mild" through would be recording a contradiction.
  is_emergency boolean NOT NULL DEFAULT false,

  ordering int NOT NULL,

  CONSTRAINT allergy_reaction_format CHECK (reaction ~ '^[A-Z][A-Z_]{1,31}$')
);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'allergy_reaction', 'A catalogue of reactions. The same in every clinic.')
ON CONFLICT DO NOTHING;

INSERT INTO core.allergy_reaction (reaction, display_en, display_bn, is_emergency, ordering) VALUES
  ('ANAPHYLAXIS',   'Collapse or anaphylaxis',        'অজ্ঞান হয়ে যাওয়া বা অ্যানাফাইল্যাক্সিস', true,  1),
  ('BREATHING',     'Difficulty breathing',            'শ্বাস নিতে কষ্ট',                      true,  2),
  ('SWELLING_FACE', 'Swelling of the face or throat',  'মুখ বা গলা ফুলে যাওয়া',                true,  3),
  ('RASH',          'Rash or hives',                   'র‍্যাশ বা চাকা চাকা দাগ',              false, 4),
  ('ITCHING',       'Itching',                         'চুলকানি',                              false, 5),
  ('VOMITING',      'Vomiting or stomach upset',       'বমি বা পেট খারাপ',                     false, 6),
  ('DIARRHOEA',     'Loose motion',                    'পাতলা পায়খানা',                        false, 7),
  ('OTHER',         'Something else',                  'অন্য কিছু',                            false, 8)
ON CONFLICT (reaction) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  is_emergency = EXCLUDED.is_emergency, ordering = EXCLUDED.ordering;

GRANT SELECT ON core.allergy_reaction TO dthcms_app, dthcms_projector;

-- ---------------------------------------------------------------------------
-- The substances, in the clinic's own dictionary
-- ---------------------------------------------------------------------------

-- The plan leaves the source of this list open pending clinical confirmation. Coded in DTHC
-- rather than ICD because ICD codes the *reaction* ("allergy to penicillin" is T88.7, which
-- is a diagnosis) and what the safety engine needs to match is the **substance**.
--
-- Both the drug classes and the individual drugs are here on purpose. A patient says "penicillin"
-- far more often than they say "amoxicillin", and a list that only held the specific one would
-- push the commonest answer into free text.
INSERT INTO core.terminology_concept
  (system, version, code, display_en, display_bn, heading, heading_bn) VALUES
  ('DTHC', '1.0', 'ALLERGEN_PENICILLIN', 'Penicillin group', 'পেনিসিলিন গ্রুপ', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_SULFA', 'Sulfa drugs', 'সালফা জাতীয় ওষুধ', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_NSAID', 'Painkillers (NSAIDs)', 'ব্যথার ওষুধ (এনএসএআইডি)', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_ASPIRIN', 'Aspirin', 'অ্যাসপিরিন', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_CEPHALOSPORIN', 'Cephalosporin group', 'সেফালোস্পোরিন গ্রুপ', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_CIPROFLOXACIN', 'Ciprofloxacin', 'সিপ্রোফ্লক্সাসিন', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_METFORMIN', 'Metformin', 'মেটফরমিন', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_INSULIN', 'Insulin', 'ইনসুলিন', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_SULFONYLUREA', 'Sulfonylurea group', 'সালফোনাইলইউরিয়া গ্রুপ', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_STATIN', 'Statin group', 'স্ট্যাটিন গ্রুপ', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_IODINE_CONTRAST', 'X-ray dye (iodine contrast)', 'এক্স-রের রং (আয়োডিন)', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_LOCAL_ANAESTHETIC', 'Local anaesthetic', 'অবশ করার ইনজেকশন', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_EGG', 'Egg', 'ডিম', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_MILK', 'Milk', 'দুধ', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_SEAFOOD', 'Fish or prawn', 'মাছ বা চিংড়ি', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_BEEF', 'Beef', 'গরুর মাংস', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_NUTS', 'Nuts', 'বাদাম', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_BRINJAL', 'Brinjal', 'বেগুন', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_DUST', 'Dust', 'ধুলা', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_LATEX', 'Rubber or latex', 'রাবার বা ল্যাটেক্স', 'Allergen', 'অ্যালার্জেন'),
  ('DTHC', '1.0', 'ALLERGEN_STICKING_PLASTER', 'Sticking plaster', 'ব্যান্ডেজের আঠা', 'Allergen', 'অ্যালার্জেন')
ON CONFLICT (system, version, code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  heading = EXCLUDED.heading, heading_bn = EXCLUDED.heading_bn;

INSERT INTO core.terminology_synonym (system, version, code, term, language) VALUES
  ('DTHC', '1.0', 'ALLERGEN_PENICILLIN', 'penicillin', 'en'),
  ('DTHC', '1.0', 'ALLERGEN_PENICILLIN', 'amoxicillin', 'en'),
  ('DTHC', '1.0', 'ALLERGEN_PENICILLIN', 'ampicillin', 'en'),
  ('DTHC', '1.0', 'ALLERGEN_PENICILLIN', 'পেনিসিলিন', 'bn'),
  ('DTHC', '1.0', 'ALLERGEN_SULFA', 'cotrimoxazole', 'en'),
  ('DTHC', '1.0', 'ALLERGEN_SULFA', 'সালফা', 'bn'),
  ('DTHC', '1.0', 'ALLERGEN_NSAID', 'diclofenac', 'en'),
  ('DTHC', '1.0', 'ALLERGEN_NSAID', 'ibuprofen', 'en'),
  ('DTHC', '1.0', 'ALLERGEN_NSAID', 'ব্যথার ওষুধ', 'bn'),
  ('DTHC', '1.0', 'ALLERGEN_CEPHALOSPORIN', 'cefixime', 'en'),
  ('DTHC', '1.0', 'ALLERGEN_CEPHALOSPORIN', 'ceftriaxone', 'en'),
  ('DTHC', '1.0', 'ALLERGEN_SEAFOOD', 'prawn', 'en'),
  ('DTHC', '1.0', 'ALLERGEN_SEAFOOD', 'চিংড়ি', 'bn'),
  ('DTHC', '1.0', 'ALLERGEN_NUTS', 'চিনাবাদাম', 'bn'),
  ('DTHC', '1.0', 'ALLERGEN_IODINE_CONTRAST', 'contrast', 'en'),
  ('DTHC', '1.0', 'ALLERGEN_LOCAL_ANAESTHETIC', 'lignocaine', 'en')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The allergy
-- ---------------------------------------------------------------------------

CREATE TABLE read.allergy (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL,
  patient_id  uuid NOT NULL,

  -- The substance. Coded from the clinic's own dictionary, and uncoded where the catalogue
  -- has nothing — the same escape hatch as a history item, and for the same reason: an
  -- allergy nobody could code is far more dangerous in a note field than it is here, marked.
  code_system  text,
  code_version text,
  code         text,
  said         text NOT NULL DEFAULT '',

  reaction text NOT NULL REFERENCES core.allergy_reaction(reaction),

  -- What happened, how sure anybody is. `certainty` matters to the safety engine: a suspected
  -- reaction thirty years ago and a confirmed anaphylaxis are both worth recording and they
  -- are not the same warning.
  severity  text NOT NULL CHECK (severity IN ('mild', 'moderate', 'severe', 'life_threatening')),
  certainty text NOT NULL CHECK (certainty IN ('suspected', 'confirmed')),

  note text NOT NULL DEFAULT '',

  recorded_at   timestamptz NOT NULL,
  recorded_by   uuid NOT NULL,
  recorded_role text NOT NULL DEFAULT '',
  recorded_visit uuid,

  -- Never deleted. An allergy somebody withdrew is an allergy somebody disagreed with, and a
  -- deletion would leave the record unable to say which.
  removed_at     timestamptz,
  removed_by     uuid,
  removed_reason text NOT NULL DEFAULT '',

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL,

  CONSTRAINT allergy_coding_is_whole CHECK (
    (code_system IS NULL AND code_version IS NULL AND code IS NULL)
    OR (code_system IS NOT NULL AND code_version IS NOT NULL AND code IS NOT NULL)),

  CONSTRAINT allergy_says_something CHECK (code IS NOT NULL OR btrim(said) <> ''),

  CONSTRAINT allergy_removal_is_complete CHECK ((removed_at IS NULL) = (removed_by IS NULL)),

  CONSTRAINT allergy_coding_exists
    FOREIGN KEY (code_system, code_version, code)
    REFERENCES core.terminology_concept (system, version, code)
);

CREATE INDEX allergy_live_by_patient
  ON read.allergy (patient_id) WHERE removed_at IS NULL;

COMMENT ON TABLE read.allergy IS
  'One recorded allergy. Never deleted; withdrawn ones keep their reason (CP54).';

GRANT SELECT ON read.allergy TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON read.allergy TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The assertion
-- ---------------------------------------------------------------------------

-- Criterion 2: "No Known Allergies" is an explicit, attributed assertion — never a default or
-- an empty field. This table is that sentence made structural. There is no column anywhere
-- that means "no allergies" by being empty; the only way to say it is to write a row here with
-- somebody's name on it.
CREATE TABLE read.allergy_assertion (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL,
  patient_id  uuid NOT NULL,

  -- NO_KNOWN_ALLERGY  → asked, and the patient knows of none
  -- UNABLE_TO_ASSESS  → asked, and the answer could not be got: unconscious, no attendant,
  --                     a child too young to say. This is allergy status and it is *not* a
  --                     claim that there are none — the safety engine treats them very
  --                     differently, and it can only do that because they are different rows.
  kind text NOT NULL CHECK (kind IN ('NO_KNOWN_ALLERGY', 'UNABLE_TO_ASSESS')),

  -- Why, for UNABLE_TO_ASSESS. Required for it and empty for the other: "we could not ask"
  -- with no reason is a row that cannot be reviewed, and the whole point of the third state is
  -- that it is reviewable rather than a silent gap.
  reason text NOT NULL DEFAULT '',

  asserted_at   timestamptz NOT NULL,
  asserted_by   uuid NOT NULL,
  asserted_role text NOT NULL DEFAULT '',
  asserted_visit uuid,

  -- Withdrawn when it stops being true — which happens by itself the moment an allergy is
  -- recorded, and by hand when somebody realises they asserted it on the wrong patient.
  withdrawn_at     timestamptz,
  withdrawn_by     uuid,
  withdrawn_reason text NOT NULL DEFAULT '',

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL,

  CONSTRAINT allergy_assertion_says_why CHECK (
    (kind = 'UNABLE_TO_ASSESS') = (btrim(reason) <> '')),

  CONSTRAINT allergy_assertion_withdrawal_is_complete CHECK (
    (withdrawn_at IS NULL) = (withdrawn_by IS NULL))
);

CREATE INDEX allergy_assertion_live_by_patient
  ON read.allergy_assertion (patient_id, asserted_at DESC) WHERE withdrawn_at IS NULL;

-- Surfacing the NKA rate per operator is the plan's own mitigation for the risk it names:
-- operators asserting NKA reflexively to clear the gate. A partial index so that question is
-- a query rather than a project.
CREATE INDEX allergy_assertion_by_operator
  ON read.allergy_assertion (facility_id, asserted_by, asserted_at);

COMMENT ON TABLE read.allergy_assertion IS
  'An explicit, attributed statement about allergy status. Criterion 2 (CP54).';

GRANT SELECT ON read.allergy_assertion TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON read.allergy_assertion TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The status, in one place
-- ---------------------------------------------------------------------------

-- One function, called by the gate, by the API and by the tests, so that "does this patient
-- have allergy status" has exactly one answer. Two implementations of this would eventually
-- disagree, and the disagreement would be invisible until somebody was prescribed penicillin.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.allergy_status(p_patient uuid) RETURNS text
LANGUAGE plpgsql STABLE AS $$
DECLARE
  latest text;
BEGIN
  -- A recorded allergy outranks any assertion, and it does so without withdrawing it: an NKA
  -- asserted in March and an allergy found in June are both true statements about their own
  -- moment, and the record should keep both. What the *status* is now is decided here.
  IF EXISTS (SELECT 1 FROM read.allergy
              WHERE patient_id = p_patient AND removed_at IS NULL) THEN
    RETURN 'ALLERGIES_RECORDED';
  END IF;

  SELECT kind INTO latest
    FROM read.allergy_assertion
   WHERE patient_id = p_patient AND withdrawn_at IS NULL
   ORDER BY asserted_at DESC, global_seq DESC
   LIMIT 1;

  RETURN coalesce(latest, 'NONE_RECORDED');
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.allergy_status(uuid) IS
  'ALLERGIES_RECORDED | NO_KNOWN_ALLERGY | UNABLE_TO_ASSESS | NONE_RECORDED (CP54).';

GRANT EXECUTE ON FUNCTION core.allergy_status(uuid) TO dthcms_app, dthcms_projector;

-- ---------------------------------------------------------------------------
-- The gate
-- ---------------------------------------------------------------------------

-- Criterion 1 and criterion 4, in one trigger.
--
-- On `core.queue_entry` rather than in a handler, because "cannot be bypassed by any client"
-- has to mean cannot. A check in Go holds for the paths somebody remembered; this holds for
-- the support script, the second client, and the one written after everybody who read the
-- plan has left.
--
-- The station's own `sequence_hint` decides what "past step 4" means, so a clinic that
-- reorders its floor does not silently move the gate. Re-entering the history station itself
-- is always allowed — that is where the status gets recorded.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.allergy_status_gates_the_queue() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  target_seq int;
  gate_seq   int;
  status     text;
BEGIN
  SELECT sequence_hint INTO target_seq
    FROM core.station
   WHERE facility_id = NEW.facility_id AND code = NEW.station_code;

  SELECT sequence_hint INTO gate_seq
    FROM core.station
   WHERE facility_id = NEW.facility_id AND code = 'STN_HISTORY';

  -- A facility with no history station has no step 4 to gate. Refusing everything there would
  -- be a clinic that cannot see patients because of a checkpoint it does not run.
  IF target_seq IS NULL OR gate_seq IS NULL OR target_seq <= gate_seq THEN
    RETURN NEW;
  END IF;

  status := core.allergy_status(NEW.patient_id);
  IF status = 'NONE_RECORDED' THEN
    RAISE EXCEPTION 'allergy status is required before %', NEW.station_code
      USING ERRCODE = 'raise_exception',
            HINT = 'Record an allergy, or assert no known allergies, or record that it could '
                   'not be assessed and why. There is no way past this.';
  END IF;

  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER allergy_status_gates_the_queue
  BEFORE INSERT ON core.queue_entry
  FOR EACH ROW EXECUTE FUNCTION core.allergy_status_gates_the_queue();

-- ---------------------------------------------------------------------------
-- The projections
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_allergy_recorded(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.allergy (
    id, facility_id, patient_id,
    code_system, code_version, code, said,
    reaction, severity, certainty, note,
    recorded_at, recorded_by, recorded_role, recorded_visit,
    event_id, global_seq)
  VALUES (
    (p->>'allergy_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    nullif(p->>'code_system', ''),
    nullif(p->>'code_version', ''),
    nullif(p->>'code', ''),
    coalesce(p->>'said', ''),
    p->>'reaction',
    p->>'severity',
    p->>'certainty',
    coalesce(p->>'note', ''),
    (p->>'recorded_at')::timestamptz,
    (p->>'recorded_by')::uuid,
    coalesce(p->>'recorded_role', ''),
    nullif(p->>'visit_id', '')::uuid,
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (event_id) DO NOTHING;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_allergy_removed(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  UPDATE read.allergy
     SET removed_at     = (p->>'removed_at')::timestamptz,
         removed_by     = (p->>'removed_by')::uuid,
         removed_reason = coalesce(p->>'reason', '')
   WHERE id = (p->>'allergy_id')::uuid
     AND removed_at IS NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_allergy_status_asserted(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.allergy_assertion (
    id, facility_id, patient_id, kind, reason,
    asserted_at, asserted_by, asserted_role, asserted_visit,
    event_id, global_seq)
  VALUES (
    (p->>'assertion_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    p->>'kind',
    coalesce(p->>'reason', ''),
    (p->>'asserted_at')::timestamptz,
    (p->>'asserted_by')::uuid,
    coalesce(p->>'asserted_role', ''),
    nullif(p->>'visit_id', '')::uuid,
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (event_id) DO NOTHING;

  -- One live assertion per patient. A new one supersedes the old rather than sitting beside
  -- it, because "no known allergies" and "we could not ask" cannot both be the current answer
  -- and a status function picking between them by timestamp is a coin toss waiting to happen.
  UPDATE read.allergy_assertion
     SET withdrawn_at     = (p->>'asserted_at')::timestamptz,
         withdrawn_by     = (p->>'asserted_by')::uuid,
         withdrawn_reason = 'superseded'
   WHERE patient_id = (p->>'patient_id')::uuid
     AND id <> (p->>'assertion_id')::uuid
     AND withdrawn_at IS NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_allergy_assertion_withdrawn(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  UPDATE read.allergy_assertion
     SET withdrawn_at     = (p->>'withdrawn_at')::timestamptz,
         withdrawn_by     = (p->>'withdrawn_by')::uuid,
         withdrawn_reason = coalesce(p->>'reason', '')
   WHERE id = (p->>'assertion_id')::uuid
     AND withdrawn_at IS NULL;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_allergy_recorded(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_allergy_removed(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_allergy_status_asserted(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_allergy_assertion_withdrawn(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- Criterion 2, as a standing rule. There must be no way to mean "no known allergies" by
-- leaving something empty — every such claim is a row with a person's name on it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_known_allergies_is_always_asserted() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender uuid;
BEGIN
  SELECT id INTO offender FROM read.allergy_assertion
   WHERE asserted_by IS NULL OR btrim(asserted_at::text) = '' LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'allergy assertion % names nobody', offender
      USING HINT = 'Criterion 2: NKA is an explicit, attributed assertion, never a default.';
  END IF;

  -- And the column may not acquire a default, which is how this would actually be lost: a
  -- `DEFAULT` on `kind` would make every row that touched this table assert something.
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
     WHERE table_schema = 'read' AND table_name = 'allergy_assertion'
       AND column_name IN ('kind', 'asserted_by')
       AND column_default IS NOT NULL) THEN
    RAISE EXCEPTION 'an assertion column has acquired a default'
      USING HINT = 'A default here makes the software assert on a clinician''s behalf.';
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_no_known_allergies_is_always_asserted() IS
  'Raises if an allergy assertion has no person behind it (CP54 criterion 2).';

-- Criterion 1 and 4. The gate must exist, on the table, as a trigger — not merely as a
-- function somebody may or may not have wired up. A migration that dropped the trigger and
-- kept the function would leave a clinic with no hard stop and no error.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_the_allergy_gate_is_wired() RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger t
      JOIN pg_class c ON c.oid = t.tgrelid
      JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'core' AND c.relname = 'queue_entry'
       AND t.tgname = 'allergy_status_gates_the_queue'
       AND NOT t.tgisinternal
       AND t.tgenabled <> 'D') THEN
    RAISE EXCEPTION 'the allergy gate is not on the queue'
      USING HINT = 'CP54 criterion 4: the gate cannot be bypassed by any client, which means '
                   'it is a trigger on core.queue_entry and not a check in an application.';
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_the_allergy_gate_is_wired() IS
  'Raises if the queue is not gated on allergy status (CP54 criteria 1 and 4).';

-- Every recorded allergy names a reaction that exists and a substance the catalogue knows, or
-- says in words what it was. An allergy nobody can read is worse than none: it produces a
-- warning that cannot be acted on, and warnings that cannot be acted on are how a clinic
-- learns to click past them.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_allergy_is_legible() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender uuid;
BEGIN
  SELECT id INTO offender FROM read.allergy
   WHERE removed_at IS NULL
     AND code IS NULL AND btrim(said) = ''
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'allergy % names no substance', offender;
  END IF;

  SELECT a.id INTO offender
    FROM read.allergy a
    LEFT JOIN core.allergy_reaction r ON r.reaction = a.reaction
   WHERE r.reaction IS NULL LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'allergy % names a reaction nobody can render', offender;
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_every_allergy_is_legible() IS
  'Raises if a live allergy names no substance or an unknown reaction (CP54 criterion 3).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_no_known_allergies_is_always_asserted',
   'no known allergies is an attributed assertion, never a default', 65),
  ('assert_the_allergy_gate_is_wired',
   'the queue is gated on allergy status, in the database', 66),
  ('assert_every_allergy_is_legible',
   'every live allergy names a substance and a reaction that can be rendered', 67)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_no_known_allergies_is_always_asserted', 'assert_the_allergy_gate_is_wired',
  'assert_every_allergy_is_legible');
DROP FUNCTION IF EXISTS core.assert_every_allergy_is_legible();
DROP FUNCTION IF EXISTS core.assert_the_allergy_gate_is_wired();
DROP FUNCTION IF EXISTS core.assert_no_known_allergies_is_always_asserted();
DROP TRIGGER IF EXISTS allergy_status_gates_the_queue ON core.queue_entry;
DROP FUNCTION IF EXISTS core.allergy_status_gates_the_queue();
DROP FUNCTION IF EXISTS read.apply_allergy_assertion_withdrawn(jsonb);
DROP FUNCTION IF EXISTS read.apply_allergy_status_asserted(jsonb);
DROP FUNCTION IF EXISTS read.apply_allergy_removed(jsonb);
DROP FUNCTION IF EXISTS read.apply_allergy_recorded(jsonb);
DROP FUNCTION IF EXISTS core.allergy_status(uuid);
DROP TABLE IF EXISTS read.allergy_assertion;
DROP TABLE IF EXISTS read.allergy;
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name = 'allergy_reaction';
DROP TABLE IF EXISTS core.allergy_reaction;
DELETE FROM core.role_permission WHERE permission_code = 'allergy.write';
DELETE FROM core.permission WHERE code = 'allergy.write';
