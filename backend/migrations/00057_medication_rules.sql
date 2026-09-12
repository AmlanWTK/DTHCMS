-- The medication safety rule library, its versions, and the allergen cross-reactivity map
-- (CP77, §6.3, §7.2, D-22, D-23).
--
-- # The one property this migration exists to guarantee
--
-- **A rule nobody has approved cannot fire.**
--
-- Thirty-four rules ship in this file. I drafted every one of them from published guidance — ADA
-- Standards of Care, KDIGO, the FDA labels, the ATA thyroid guidelines, the BNF — and cited the
-- source on the row. Not one of them has been read by Dr. Nahid, and D-22 says the physician
-- authors every clinical rule. So a seeded rule must behave differently from a rule he wrote, and
-- "differently" has to mean *inert*, not *marked*.
--
-- The mechanism is the same one CP75 used for an unverified price, because it worked:
--
--   * a version is live only inside `[effective_from, effective_to)`, and an `EXCLUDE` constraint
--     makes at most one version of a rule live at any instant;
--   * `rule_version_published_is_approved` refuses a PUBLISHED row, or a row with an effective
--     period, that does not name an approver and the instant they approved it;
--   * every seeded row is DRAFT with `approved_at` null and no period, so the query that loads a
--     ruleset cannot return it — not "filters it out", *cannot return it*: there is no instant
--     at which it was live;
--   * `assert_no_unapproved_rule_is_live()` (invariant 113) re-checks the whole table after every
--     migration, because a constraint added later does not validate what is already there.
--
-- The Go layer checks `Version.Approved()` a second time before evaluating. Two locks on one door
-- is right here: the thing behind it is a rule I wrote silently stopping a prescription Dr. Nahid
-- meant to write, or — worse — silently failing to stop one he did not.
--
-- # Why a version has a period rather than a flag
--
-- Criterion 3: rule versions are retained and historical checks are reproducible. A schema in
-- which the live version is `WHERE is_current` answers "what would this prescription do today",
-- and there is no query that recovers what it did in March. A half-open period answers both, with
-- the same statement, and that statement is `RulesetAt`.
--
-- `timestamptz` rather than `date`, unlike a price. A price is a commercial fact that holds for a
-- day; a rule change takes effect at the moment the physician presses publish, and two
-- prescriptions written eleven minutes apart can legitimately be checked against different
-- versions.
--
-- # Why the condition is JSONB and not eight tables
--
-- One column holding a validated, canonical document, rather than a table per rule type with a
-- join per predicate. The argument is reproducibility again: what must be re-evaluable in three
-- years is *the exact statement that was published*, and a statement spread over four tables is
-- one that a later migration can alter without any single row looking wrong. The document is
-- validated in Go before it is stored (`medsafety.Condition.Validate`), canonicalised so that two
-- savings of the same meaning produce the same bytes, and rejected on read if it carries a field
-- this version of the model does not know — because a published rule whose unknown half is
-- silently ignored is the quiet wrong answer this module is built to avoid.
--
-- The cost is that PostgreSQL cannot check a condition's shape. That is accepted, and the
-- compensation is a CHECK that it is at least an object with the two keys, plus a test that every
-- seeded row round-trips through the Go validator.
--
-- # Nothing is deleted
--
-- A withdrawn rule is deactivated and its version's period is closed. Every version stays, because
-- a check run against version 1 has to stay reproducible after version 2 is published and after
-- the whole rule is retired. `dthcms_app` holds no DELETE on any table here.

-- +goose Up

-- ---------------------------------------------------------------------------
-- 1. Five ICD-10 codes the seeded rules need
-- ---------------------------------------------------------------------------

-- CP52 seeded the 49 ICD-10 codes an endocrinology clinic diagnoses *with*. A contraindication
-- rule needs the codes a clinic diagnoses *around* — the comorbidity that makes a drug wrong —
-- and five of those were absent. A rule keyed to a code nobody can record never fires and nothing
-- says so, which is the exact failure mode this checkpoint's verification bar is about.
--
-- All five are real ICD-10 codes, added to the same 2019 version CP52 loaded. The Bengali is
-- mine, and belongs in D-24's review of the clinical register along with the rest.
INSERT INTO core.terminology_concept (system, version, code, display_en, display_bn, heading, heading_bn)
VALUES
  ('ICD10', '2019', 'I50.9', 'Heart failure, unspecified', 'হৃদযন্ত্রের বিকলতা, অনির্দিষ্ট',
   'Diseases of the circulatory system', 'সংবহনতন্ত্রের রোগ'),
  ('ICD10', '2019', 'K74.6', 'Unspecified cirrhosis of liver', 'যকৃতের সিরোসিস, অনির্দিষ্ট',
   'Diseases of the digestive system', 'পরিপাকতন্ত্রের রোগ'),
  ('ICD10', '2019', 'E31.2', 'Multiple endocrine neoplasia (MEN) syndrome',
   'মাল্টিপল এন্ডোক্রাইন নিওপ্লাসিয়া (MEN) সিনড্রোম',
   'Endocrine, nutritional and metabolic diseases', 'অন্তঃক্ষরা, পুষ্টি ও বিপাকীয় রোগ'),
  ('ICD10', '2019', 'E87.2', 'Acidosis', 'অ্যাসিডোসিস',
   'Endocrine, nutritional and metabolic diseases', 'অন্তঃক্ষরা, পুষ্টি ও বিপাকীয় রোগ'),
  ('ICD10', '2019', 'N18.5', 'Chronic kidney disease, stage 5', 'দীর্ঘমেয়াদি বৃক্করোগ, পর্যায় 5',
   'Diseases of the genitourinary system', 'মূত্র ও জননতন্ত্রের রোগ')
ON CONFLICT (system, version, code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn;

-- ---------------------------------------------------------------------------
-- 2. Allergens, their members, and what cross-reacts with what
-- ---------------------------------------------------------------------------

-- An allergen group is what a patient says they react to — "penicillin" — rather than the
-- molecule they were given. Global rather than facility-scoped: which drugs are penicillins is
-- pharmacology, not this clinic's data.
--
-- **Every group and every cross-reaction below is seeded unapproved, exactly like a rule.** The
-- sulfonamide row in particular is a clinical claim that contradicts something most prescribers
-- believe, and it is not going to start suppressing warnings because I read a paper.
CREATE TABLE core.allergen_group (
  code text PRIMARY KEY,

  name_en text NOT NULL,
  name_bn text NOT NULL,
  notes_en text NOT NULL DEFAULT '',
  notes_bn text NOT NULL DEFAULT '',

  -- Where the grouping came from. Required for the same reason a rule's is.
  source_citation text NOT NULL,
  origin text NOT NULL DEFAULT 'SEED' CHECK (origin IN ('SEED', 'AUTHORED')),

  approved_by uuid REFERENCES core.app_user(id),
  approved_at timestamptz,

  is_active boolean NOT NULL DEFAULT true,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT allergen_group_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,59}$'),
  CONSTRAINT allergen_group_bilingual CHECK (btrim(name_en) <> '' AND btrim(name_bn) <> ''),
  CONSTRAINT allergen_group_names_its_source CHECK (btrim(source_citation) <> ''),
  -- The same shape as a price's: approval is a person and an instant, or it is neither.
  CONSTRAINT allergen_group_approval_is_attributed
    CHECK ((approved_at IS NULL) = (approved_by IS NULL))
);

SELECT core.attach_updated_at('core.allergen_group');
GRANT SELECT, INSERT, UPDATE ON core.allergen_group TO dthcms_app;

-- Which molecules and which classes are in a group.
CREATE TABLE core.allergen_group_member (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  group_code text NOT NULL REFERENCES core.allergen_group(code),

  -- GENERIC names a molecule by name rather than by id, because most of these molecules are not
  -- in this clinic's formulary and never will be — a patient's penicillin allergy is a fact
  -- about the patient, not about what the pharmacy stocks.
  match_kind text NOT NULL CHECK (match_kind IN ('GENERIC', 'CLASS')),
  match_value text NOT NULL,

  source_citation text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT allergen_member_value_present CHECK (btrim(match_value) <> ''),
  CONSTRAINT allergen_member_names_its_source CHECK (btrim(source_citation) <> '')
);

CREATE UNIQUE INDEX allergen_group_member_key
  ON core.allergen_group_member (group_code, match_kind, lower(match_value));

SELECT core.attach_updated_at('core.allergen_group_member');
GRANT SELECT, INSERT, UPDATE ON core.allergen_group_member TO dthcms_app;

-- What a reaction to one group implies about another.
--
-- Directed, because the implication is not symmetric in practice: a documented penicillin allergy
-- raises a small question about cephalosporins, and a cephalosporin allergy raises a different
-- and smaller one about penicillins. Two rows if both are meant.
CREATE TABLE core.allergen_cross_reaction (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  from_group text NOT NULL REFERENCES core.allergen_group(code),
  to_group   text NOT NULL REFERENCES core.allergen_group(code),

  -- NONE is a real and useful answer: the sulfonamide-antibiotic to sulphonylurea row exists
  -- precisely to record that the cross-reaction most prescribers assume is not supported.
  risk text NOT NULL CHECK (risk IN ('NONE', 'LOW', 'MODERATE', 'HIGH')),

  note_en text NOT NULL,
  note_bn text NOT NULL,
  source_citation text NOT NULL,
  origin text NOT NULL DEFAULT 'SEED' CHECK (origin IN ('SEED', 'AUTHORED')),

  approved_by uuid REFERENCES core.app_user(id),
  approved_at timestamptz,
  is_active boolean NOT NULL DEFAULT true,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT cross_reaction_is_between_two_groups CHECK (from_group <> to_group),
  CONSTRAINT cross_reaction_bilingual CHECK (btrim(note_en) <> '' AND btrim(note_bn) <> ''),
  CONSTRAINT cross_reaction_names_its_source CHECK (btrim(source_citation) <> ''),
  CONSTRAINT cross_reaction_approval_is_attributed
    CHECK ((approved_at IS NULL) = (approved_by IS NULL))
);

CREATE UNIQUE INDEX allergen_cross_reaction_key
  ON core.allergen_cross_reaction (from_group, to_group);

SELECT core.attach_updated_at('core.allergen_cross_reaction');
GRANT SELECT, INSERT, UPDATE ON core.allergen_cross_reaction TO dthcms_app;

-- ---------------------------------------------------------------------------
-- 3. The rule
-- ---------------------------------------------------------------------------

-- The identity that outlives its versions. Facility-scoped: which rules this clinic has agreed to
-- is this clinic's decision, and D-61 has more clinics coming.
CREATE TABLE core.medication_rule (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  -- The handle a finding cites and a person quotes across a room. Never reused.
  code text NOT NULL,

  -- §6.3's eight, closed. A ninth is a schema change and a conversation.
  rule_type text NOT NULL CHECK (rule_type IN (
    'INTERACTION', 'CONTRAINDICATION', 'RENAL', 'HEPATIC',
    'PREGNANCY', 'PAEDIATRIC', 'DUPLICATE_THERAPY', 'MAX_DOSE')),

  is_active boolean NOT NULL DEFAULT true,
  withdrawn_at timestamptz,
  withdrawn_reason text,
  withdrawn_by uuid REFERENCES core.app_user(id),

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT medication_rule_code_format CHECK (code ~ '^[A-Z][A-Z0-9-]{2,59}$'),
  -- Withdrawal is attributed or it did not happen, exactly as a product's is.
  CONSTRAINT medication_rule_withdrawal_is_attributed
    CHECK ((withdrawn_at IS NULL) = (withdrawn_by IS NULL)
           AND (withdrawn_at IS NULL OR btrim(coalesce(withdrawn_reason, '')) <> '')),
  CONSTRAINT medication_rule_withdrawn_is_inactive
    CHECK (withdrawn_at IS NULL OR NOT is_active)
);

CREATE UNIQUE INDEX medication_rule_code_key ON core.medication_rule (facility_id, code);
CREATE INDEX medication_rule_type_idx ON core.medication_rule (facility_id, rule_type);

SELECT core.attach_updated_at('core.medication_rule');
GRANT SELECT, INSERT, UPDATE ON core.medication_rule TO dthcms_app;

-- ---------------------------------------------------------------------------
-- 4. The version — the frozen statement
-- ---------------------------------------------------------------------------

CREATE TABLE core.medication_rule_version (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  rule_id uuid NOT NULL REFERENCES core.medication_rule(id),
  version integer NOT NULL,

  severity text NOT NULL CHECK (severity IN ('BLOCK', 'WARN', 'INFO')),

  -- The short label, and the sentence the physician reads when it fires. Both in both
  -- languages, and the constraint is not negotiable: a rule that fires in English at a
  -- counsellor who works in Bangla has not fired.
  name_en text NOT NULL,
  name_bn text NOT NULL,
  message_en text NOT NULL,
  message_bn text NOT NULL,
  -- What to do instead. Optional, because "avoid" is sometimes the whole of it — but never in
  -- one language only.
  advice_en text NOT NULL DEFAULT '',
  advice_bn text NOT NULL DEFAULT '',

  -- The statement itself. Validated and canonicalised in Go; see the header.
  condition jsonb NOT NULL,

  -- Where the clinical content came from. Required. A rule that stops a prescription with
  -- nothing behind it cannot be argued with, and cannot be updated when the guidance moves.
  source_citation text NOT NULL,

  origin text NOT NULL CHECK (origin IN ('SEED', 'AUTHORED', 'IMPORTED')),
  status text NOT NULL CHECK (status IN ('DRAFT', 'PUBLISHED', 'SUPERSEDED', 'WITHDRAWN')),

  authored_by uuid REFERENCES core.app_user(id),
  authored_at timestamptz NOT NULL DEFAULT now(),

  approved_by uuid REFERENCES core.app_user(id),
  approved_at timestamptz,

  -- The period this version was the live one. Half-open. Null on a draft, which is what makes a
  -- draft unreachable by the ruleset query rather than merely filtered out of it.
  effective_from timestamptz,
  effective_to timestamptz,

  notes text NOT NULL DEFAULT '',

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT rule_version_is_numbered CHECK (version >= 1),
  CONSTRAINT rule_version_is_bilingual
    CHECK (btrim(name_en) <> '' AND btrim(name_bn) <> ''
           AND btrim(message_en) <> '' AND btrim(message_bn) <> ''
           AND (btrim(advice_en) = '') = (btrim(advice_bn) = '')),
  CONSTRAINT rule_version_names_its_source CHECK (btrim(source_citation) <> ''),
  CONSTRAINT rule_version_condition_is_a_statement
    CHECK (jsonb_typeof(condition) = 'object'
           AND condition ? 'subject' AND condition ? 'when'
           AND jsonb_typeof(condition -> 'when') = 'array'
           AND jsonb_array_length(condition -> 'when') > 0),

  -- The four constraints that carry "a rule nobody approved cannot fire".
  CONSTRAINT rule_version_approval_is_attributed
    CHECK ((approved_at IS NULL) = (approved_by IS NULL)),
  -- Being live and being approved are the same thing, in both directions.
  CONSTRAINT rule_version_live_means_approved
    CHECK ((effective_from IS NOT NULL) = (approved_at IS NOT NULL)),
  CONSTRAINT rule_version_status_matches_its_period
    CHECK (
      (status = 'DRAFT' AND effective_from IS NULL AND effective_to IS NULL)
      OR (status = 'PUBLISHED' AND effective_from IS NOT NULL AND effective_to IS NULL)
      OR (status IN ('SUPERSEDED', 'WITHDRAWN')
          AND effective_from IS NOT NULL AND effective_to IS NOT NULL)),
  CONSTRAINT rule_version_period_is_forwards
    CHECK (effective_to IS NULL OR effective_to > effective_from),

  -- At most one version of a rule is live at any instant. The same guarantee, and the same
  -- mechanism, as one price per medicine per day — so "which version decided this check" has
  -- exactly one answer, enforced by PostgreSQL rather than by the application being careful.
  CONSTRAINT rule_version_periods_do_not_overlap EXCLUDE USING gist (
    rule_id WITH =,
    tstzrange(effective_from, effective_to, '[)') WITH &&
  ) WHERE (effective_from IS NOT NULL)
);

CREATE UNIQUE INDEX medication_rule_version_key
  ON core.medication_rule_version (rule_id, version);
-- The index RulesetAt runs on: the live version of each rule at an instant.
CREATE INDEX medication_rule_version_live_idx
  ON core.medication_rule_version (rule_id, effective_from DESC)
  WHERE effective_from IS NOT NULL;
CREATE INDEX medication_rule_version_unapproved_idx
  ON core.medication_rule_version (rule_id) WHERE approved_at IS NULL;

SELECT core.attach_updated_at('core.medication_rule_version');
-- INSERT and UPDATE, no DELETE. The UPDATE closes `effective_to` and edits a draft; a published
-- version's content is frozen by the trigger below.
GRANT SELECT, INSERT, UPDATE ON core.medication_rule_version TO dthcms_app;

-- +goose StatementBegin
-- A published version is frozen. Everything a check depended on — the severity, both messages,
-- the condition, the citation — is refused once the version has gone live, exactly as a
-- superseded price is refused.
--
-- What may still change is the period's end, because that is what publishing a successor and
-- withdrawing a rule both do. Nothing else.
CREATE OR REPLACE FUNCTION core.medication_rule_version_is_frozen() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.effective_from IS NULL THEN
    RETURN NEW;  -- still a draft; the author may edit it freely
  END IF;

  IF NEW.severity IS DISTINCT FROM OLD.severity
     OR NEW.name_en IS DISTINCT FROM OLD.name_en
     OR NEW.name_bn IS DISTINCT FROM OLD.name_bn
     OR NEW.message_en IS DISTINCT FROM OLD.message_en
     OR NEW.message_bn IS DISTINCT FROM OLD.message_bn
     OR NEW.advice_en IS DISTINCT FROM OLD.advice_en
     OR NEW.advice_bn IS DISTINCT FROM OLD.advice_bn
     OR NEW.condition IS DISTINCT FROM OLD.condition
     OR NEW.source_citation IS DISTINCT FROM OLD.source_citation
     OR NEW.origin IS DISTINCT FROM OLD.origin
     OR NEW.version IS DISTINCT FROM OLD.version
     OR NEW.rule_id IS DISTINCT FROM OLD.rule_id
     OR NEW.approved_by IS DISTINCT FROM OLD.approved_by
     OR NEW.approved_at IS DISTINCT FROM OLD.approved_at
     OR NEW.effective_from IS DISTINCT FROM OLD.effective_from THEN
    RAISE EXCEPTION
      'medication rule version % has been published and cannot be edited; publish a new version',
      OLD.version
      USING HINT = 'A check run while this version was live has to stay reproducible. '
                   'Draft version N+1 instead.';
  END IF;

  IF OLD.effective_to IS NOT NULL AND NEW.effective_to IS DISTINCT FROM OLD.effective_to THEN
    RAISE EXCEPTION 'the period of medication rule version % has already closed', OLD.version;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER medication_rule_version_frozen
  BEFORE UPDATE ON core.medication_rule_version
  FOR EACH ROW EXECUTE FUNCTION core.medication_rule_version_is_frozen();

-- ---------------------------------------------------------------------------
-- 5. Scope exemptions
-- ---------------------------------------------------------------------------

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'allergen_group',
   'Which molecules are penicillins is pharmacology, not a clinic''s data. Which rules a clinic has agreed to is on core.medication_rule, which is scoped.'),
  ('core', 'allergen_group_member',
   'The membership of an allergen group is pharmacology, not a clinic''s data.'),
  ('core', 'allergen_cross_reaction',
   'What cross-reacts with what is published immunology, not a clinic''s data.'),
  ('core', 'medication_rule_version',
   'A version belongs to core.medication_rule, which carries the facility. Repeating it here would let the two disagree.')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- 5b. Nothing here is deleted
-- ---------------------------------------------------------------------------

-- Explicit, because `ALTER DEFAULT PRIVILEGES` on `core` grants the application role full CRUD
-- and the GRANTs above are additive rather than exclusive. Without these five lines every table
-- in this file is deletable by the running application, and the guarantee that a withdrawn rule
-- keeps its versions would be a comment. `assert_medication_rule_history_is_kept()` re-checks it
-- after every migration for exactly that reason.
REVOKE DELETE, TRUNCATE ON core.medication_rule          FROM dthcms_app;
REVOKE DELETE, TRUNCATE ON core.medication_rule_version  FROM dthcms_app;
REVOKE DELETE, TRUNCATE ON core.allergen_group           FROM dthcms_app;
REVOKE DELETE, TRUNCATE ON core.allergen_group_member    FROM dthcms_app;
REVOKE DELETE, TRUNCATE ON core.allergen_cross_reaction  FROM dthcms_app;

-- ---------------------------------------------------------------------------
-- 6. Who may do what
-- ---------------------------------------------------------------------------

-- **None of these is sensitive, and that is an argued decision rather than an omission.**
--
-- §4.4 blinds registration and the pharmacist from *diagnoses and clinical interpretations about
-- a patient*. A rule library holds neither. "Pioglitazone is contraindicated in heart failure" is
-- a sentence from a drug label; it is about pioglitazone, not about anybody in the register, and
-- there is no patient identifier anywhere in these tables.
--
-- What follows from that is a grant decision, not a blinding one, and it is genuinely open:
-- **the pharmacist is not granted the read**, because a screen of contraindication rules is a
-- screen full of diagnosis codes and §4.4's spirit is that the pharmacist's screen does not
-- carry those. Dr. Nahid may reasonably want the opposite — a pharmacist who can see the renal
-- dosing rules is a pharmacist who can catch the prescription that got past everybody. It is
-- recorded in docs/medication-rules.md as his to decide.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('medication.rule.read', 'medication', 'rule', 'read',
   'Read the medication safety rule library and its versions', false),
  ('medication.rule.write', 'medication', 'rule', 'write',
   'Draft and edit medication safety rules, and test them in the sandbox', false),
  ('medication.rule.publish', 'medication', 'rule', 'publish',
   'Approve and publish a medication safety rule, or withdraw one', false)
ON CONFLICT (code) DO UPDATE SET
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

-- Reading is the physician, the junior doctor who prescribes under him, QA (CP83 runs the
-- interaction and duplicate checks as part of clearance) and the administrator.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'medication.rule.read'
  FROM core.role r WHERE r.code IN ('PHYSICIAN', 'JUNIOR_DOCTOR', 'QA', 'ADMIN')
ON CONFLICT DO NOTHING;

-- **Writing and publishing are the physician alone. That is D-22, stated as a grant.**
--
-- Not the administrator, and that is deliberate rather than an oversight: the administrator can
-- grant himself any role in this system, so the grant is not what stops him. What the grant does
-- is make the ordinary path — an administrator tidying up a rule at the end of a day — not exist.
-- A clinical rule with an administrator's name on it is exactly what D-22 decided against.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'medication.rule.write'
  FROM core.role r WHERE r.code IN ('PHYSICIAN')
ON CONFLICT DO NOTHING;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'medication.rule.publish'
  FROM core.role r WHERE r.code IN ('PHYSICIAN')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- 7. The allergen groups, and what cross-reacts with what
-- ---------------------------------------------------------------------------

-- Six groups and four cross-reactions, every one of them seeded with `approved_at` null.
--
-- None of these molecules is in this clinic's formulary and most never will be: an endocrinology
-- clinic does not stock penicillins. They are here because a patient's reported allergy is a fact
-- about the patient, and CP78 has to be able to say what "penicillin" covers when somebody
-- reports one.
INSERT INTO core.allergen_group (code, name_en, name_bn, notes_en, notes_bn, source_citation) VALUES
  ('PENICILLIN', 'Penicillins', 'পেনিসিলিন',
   'The beta-lactam group patients most often report. Most reported penicillin allergy is not allergy: around 90% of people labelled penicillin-allergic tolerate it on testing.',
   'রোগীরা সবচেয়ে বেশি যে বিটা-ল্যাকটাম শ্রেণির কথা বলেন। বেশিরভাগ ক্ষেত্রেই তা প্রকৃত অ্যালার্জি নয়: পেনিসিলিন-অ্যালার্জি লেখা রোগীদের প্রায় 90% পরীক্ষায় সহ্য করতে পারেন।',
   'Shenoy ES et al., Evaluation and Management of Penicillin Allergy, JAMA 2019;321:188.'),
  ('CEPHALOSPORIN', 'Cephalosporins', 'সেফালোস্পোরিন',
   'Beta-lactams sharing a ring with the penicillins. Cross-reaction depends on the side chain, not on the ring.',
   'পেনিসিলিনের সঙ্গে একই রিং-যুক্ত বিটা-ল্যাকটাম। ক্রস-রিঅ্যাকশন নির্ভর করে সাইড-চেইনের উপর, রিং-এর উপর নয়।',
   'Zagursky RJ, Pichichero ME, Cross-reactivity in Beta-Lactam Allergy, J Allergy Clin Immunol Pract 2018;6:72.'),
  ('CARBAPENEM', 'Carbapenems', 'কার্বাপেনেম',
   'Beta-lactams. Cross-reaction with penicillin is well under 1% in prospective studies.',
   'বিটা-ল্যাকটাম। সম্ভাব্য গবেষণায় পেনিসিলিনের সঙ্গে ক্রস-রিঅ্যাকশন 1%-এরও অনেক কম।',
   'Picard M et al., Cross-Reactivity to Cephalosporins and Carbapenems in Penicillin-Allergic Patients, J Allergy Clin Immunol Pract 2019;7:2722.'),
  ('SULFONAMIDE_ANTIBIOTIC', 'Sulfonamide antibiotics', 'সালফোনামাইড অ্যান্টিবায়োটিক',
   'Co-trimoxazole and its relatives. The arylamine group at position N4 is what the immune system recognises, and it is what the non-antibiotic sulfonamides do not have.',
   'কো-ট্রাইমক্সাজল ও তার সমগোত্রীয়। N4 অবস্থানের অ্যারিলঅ্যামিন গ্রুপটিই রোগ প্রতিরোধ ব্যবস্থা চেনে, আর অ্যান্টিবায়োটিক-নয় এমন সালফোনামাইডে সেটি থাকে না।',
   'Strom BL et al., N Engl J Med 2003;349:1628; Wulf NR, Matuszewski KA, Sulfonamide cross-reactivity, Am J Health Syst Pharm 2013;70:1483.'),
  ('SULFONAMIDE_NON_ANTIBIOTIC', 'Non-antibiotic sulfonamides', 'অ্যান্টিবায়োটিক-নয় সালফোনামাইড',
   'Sulphonylureas, thiazides, furosemide, acetazolamide and the coxibs. Chemically sulfonamides, immunologically not the same thing.',
   'সালফোনাইলইউরিয়া, থায়াজাইড, ফিউরোসেমাইড, অ্যাসিটাজোলামাইড ও কক্সিব। রাসায়নিকভাবে সালফোনামাইড, প্রতিরোধতাত্ত্বিকভাবে এক নয়।',
   'Strom BL et al., N Engl J Med 2003;349:1628.'),
  ('INSULIN_ANIMAL', 'Animal-source insulin', 'প্রাণিজ উৎসের ইনসুলিন',
   'Bovine and porcine insulin. Recorded because older patients in Bangladesh may report a reaction to insulin from before recombinant human insulin was universal; it says nothing about the analogues.',
   'গরু ও শূকরের ইনসুলিন। নথিভুক্ত করা হয়েছে কারণ বাংলাদেশের বয়স্ক রোগীরা রিকম্বিন্যান্ট হিউম্যান ইনসুলিনের আগেকার সময়ের প্রতিক্রিয়ার কথা বলতে পারেন; অ্যানালগ সম্পর্কে এটি কিছু বলে না।',
   'Ghazavi MK, Johnston GA, Insulin allergy, Clin Dermatol 2011;29:300.')
ON CONFLICT (code) DO NOTHING;

INSERT INTO core.allergen_group_member (group_code, match_kind, match_value, source_citation)
VALUES
  ('PENICILLIN', 'GENERIC', 'benzylpenicillin', 'WHO Model List of Essential Medicines 23rd list, 2023.'),
  ('PENICILLIN', 'GENERIC', 'phenoxymethylpenicillin', 'WHO Model List of Essential Medicines 23rd list, 2023.'),
  ('PENICILLIN', 'GENERIC', 'amoxicillin', 'WHO Model List of Essential Medicines 23rd list, 2023.'),
  ('PENICILLIN', 'GENERIC', 'ampicillin', 'WHO Model List of Essential Medicines 23rd list, 2023.'),
  ('PENICILLIN', 'GENERIC', 'flucloxacillin', 'BNF 88, penicillins.'),
  ('PENICILLIN', 'GENERIC', 'piperacillin', 'BNF 88, penicillins.'),
  ('CEPHALOSPORIN', 'GENERIC', 'cefixime', 'BNF 88, cephalosporins.'),
  ('CEPHALOSPORIN', 'GENERIC', 'ceftriaxone', 'BNF 88, cephalosporins.'),
  ('CEPHALOSPORIN', 'GENERIC', 'cefuroxime', 'BNF 88, cephalosporins.'),
  ('CEPHALOSPORIN', 'GENERIC', 'cefalexin', 'BNF 88, cephalosporins.'),
  ('CARBAPENEM', 'GENERIC', 'meropenem', 'BNF 88, carbapenems.'),
  ('CARBAPENEM', 'GENERIC', 'imipenem', 'BNF 88, carbapenems.'),
  ('SULFONAMIDE_ANTIBIOTIC', 'GENERIC', 'sulfamethoxazole', 'BNF 88, sulfonamides and trimethoprim.'),
  ('SULFONAMIDE_ANTIBIOTIC', 'GENERIC', 'co-trimoxazole', 'BNF 88, sulfonamides and trimethoprim.'),
  ('SULFONAMIDE_ANTIBIOTIC', 'GENERIC', 'sulfadiazine', 'BNF 88, sulfonamides.'),
  -- These two are in this clinic's formulary, so they are named by class rather than by
  -- molecule: a sulphonylurea added next year joins the group without anybody remembering to.
  ('SULFONAMIDE_NON_ANTIBIOTIC', 'CLASS', 'SULPHONYLUREA', 'Strom BL et al., N Engl J Med 2003;349:1628.'),
  ('SULFONAMIDE_NON_ANTIBIOTIC', 'CLASS', 'SULPHONYLUREA_BIGUANIDE_FDC', 'Strom BL et al., N Engl J Med 2003;349:1628.'),
  ('SULFONAMIDE_NON_ANTIBIOTIC', 'GENERIC', 'hydrochlorothiazide', 'Strom BL et al., N Engl J Med 2003;349:1628.'),
  ('SULFONAMIDE_NON_ANTIBIOTIC', 'GENERIC', 'furosemide', 'Wulf NR, Matuszewski KA, Am J Health Syst Pharm 2013;70:1483.'),
  ('INSULIN_ANIMAL', 'GENERIC', 'bovine insulin', 'Ghazavi MK, Johnston GA, Clin Dermatol 2011;29:300.'),
  ('INSULIN_ANIMAL', 'GENERIC', 'porcine insulin', 'Ghazavi MK, Johnston GA, Clin Dermatol 2011;29:300.')
ON CONFLICT DO NOTHING;

INSERT INTO core.allergen_cross_reaction (from_group, to_group, risk, note_en, note_bn, source_citation)
VALUES
  ('PENICILLIN', 'CEPHALOSPORIN', 'LOW',
   'Around 2% overall, and close to zero for cephalosporins whose R1 side chain differs from the penicillin involved. A documented penicillin anaphylaxis still warrants caution; a childhood rash does not.',
   'সামগ্রিকভাবে প্রায় 2%, আর যেসব সেফালোস্পোরিনের R1 সাইড-চেইন সংশ্লিষ্ট পেনিসিলিন থেকে আলাদা, তাতে প্রায় শূন্য। নথিভুক্ত পেনিসিলিন অ্যানাফাইল্যাক্সিস থাকলে সাবধানতা দরকার; ছোটবেলার র‍্যাশে নয়।',
   'Zagursky RJ, Pichichero ME, J Allergy Clin Immunol Pract 2018;6:72; AAAAI/ACAAI Drug Allergy Practice Parameter 2022.'),
  ('PENICILLIN', 'CARBAPENEM', 'LOW',
   'Under 1% in prospective challenge studies. A carbapenem is usually safe after a penicillin reaction that was not anaphylaxis.',
   'সম্ভাব্য চ্যালেঞ্জ গবেষণায় 1%-এরও কম। অ্যানাফাইল্যাক্সিস না হওয়া পেনিসিলিন প্রতিক্রিয়ার পর কার্বাপেনেম সাধারণত নিরাপদ।',
   'Picard M et al., J Allergy Clin Immunol Pract 2019;7:2722.'),
  ('CEPHALOSPORIN', 'PENICILLIN', 'LOW',
   'The reverse direction, and it is a different number: the cephalosporins are a wider group, so a reaction to one says less about the penicillins than the other way round.',
   'উল্টো দিক, এবং সংখ্যাটিও আলাদা: সেফালোস্পোরিন একটি বড় শ্রেণি, তাই এর একটিতে প্রতিক্রিয়া পেনিসিলিন সম্পর্কে উল্টোটির চেয়ে কম কিছু বলে।',
   'Romano A et al., Cross-reactivity among beta-lactams, Curr Allergy Asthma Rep 2016;16:24.'),
  ('SULFONAMIDE_ANTIBIOTIC', 'SULFONAMIDE_NON_ANTIBIOTIC', 'NONE',
   'No meaningful cross-reactivity. The cohort of 970 patients with a documented sulfonamide antibiotic allergy found that the raised risk of a reaction to a non-antibiotic sulfonamide was explained by a general predisposition to allergy, not by the sulfonamide group. Withholding a sulphonylurea, a thiazide or furosemide on the strength of a co-trimoxazole rash costs the patient a useful drug for no benefit.',
   'অর্থবহ কোনো ক্রস-রিঅ্যাকশন নেই। 970 জন নথিভুক্ত সালফোনামাইড-অ্যান্টিবায়োটিক অ্যালার্জি রোগীর গবেষণায় দেখা গেছে, অ্যান্টিবায়োটিক-নয় সালফোনামাইডে প্রতিক্রিয়ার বাড়তি ঝুঁকির কারণ সাধারণভাবে অ্যালার্জির প্রবণতা, সালফোনামাইড গ্রুপ নয়। কো-ট্রাইমক্সাজলের র‍্যাশের কারণে সালফোনাইলইউরিয়া, থায়াজাইড বা ফিউরোসেমাইড বাদ দিলে রোগী বিনা লাভে একটি কাজের ওষুধ হারান।',
   'Strom BL et al., N Engl J Med 2003;349:1628; AAAAI/ACAAI Drug Allergy Practice Parameter 2022.')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- 8. The seed
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
-- Seeds one rule and its first version, as a DRAFT nobody has approved.
--
-- **Re-runnable, and it never touches a rule that already exists.** The same reasoning as the
-- formulary's price seed: this runs on every migration in every environment, and a seed that
-- overwrote what was there would quietly replace a rule Dr. Nahid approved with the draft I wrote.
-- If the code is already present, this does nothing at all.
--
-- The version is inserted with `approved_by`, `approved_at`, `effective_from` and `effective_to`
-- all null, which is not a convention — `rule_version_status_matches_its_period` and
-- `rule_version_live_means_approved` make any other combination impossible for a DRAFT. There is
-- no argument to this function that could produce an approved row.
CREATE OR REPLACE FUNCTION core.seed_medication_rule(
  p_facility uuid, p_code text, p_type text, p_severity text,
  p_name_en text, p_name_bn text,
  p_message_en text, p_message_bn text,
  p_advice_en text, p_advice_bn text,
  p_source text, p_condition jsonb) RETURNS uuid
LANGUAGE plpgsql AS $$
DECLARE rule_id uuid;
BEGIN
  SELECT id INTO rule_id
    FROM core.medication_rule
   WHERE facility_id = p_facility AND code = p_code;
  IF rule_id IS NOT NULL THEN
    RETURN rule_id;
  END IF;

  INSERT INTO core.medication_rule (facility_id, code, rule_type)
  VALUES (p_facility, p_code, p_type)
  RETURNING id INTO rule_id;

  INSERT INTO core.medication_rule_version
    (rule_id, version, severity, name_en, name_bn, message_en, message_bn,
     advice_en, advice_bn, condition, source_citation, origin, status, notes)
  VALUES
    (rule_id, 1, p_severity, p_name_en, p_name_bn, p_message_en, p_message_bn,
     coalesce(p_advice_en, ''), coalesce(p_advice_bn, ''), p_condition, p_source,
     'SEED', 'DRAFT',
     'Drafted from published guidance and shipped with migration 00057. Not reviewed by a physician at this clinic. It does nothing until somebody approves it.');

  RETURN rule_id;
END
$$;
-- +goose StatementEnd
SELECT core.seed_medication_rule(f.id, 'MET-RENAL-30', 'RENAL', 'BLOCK',
  'Metformin below eGFR 30', 'eGFR 30-এর নিচে মেটফরমিন',
  'Metformin is contraindicated below an eGFR of 30 mL/min/1.73m2. The risk of lactic acidosis outweighs any glycaemic benefit.',
  'eGFR 30 mL/min/1.73m2-এর নিচে মেটফরমিন দেওয়া যাবে না। ল্যাকটিক অ্যাসিডোসিসের ঝুঁকি রক্তে শর্করা কমানোর লাভের চেয়ে বেশি।',
  'Stop metformin. Linagliptin needs no dose change at any eGFR; insulin is the other safe option.',
  'মেটফরমিন বন্ধ করুন। যেকোনো eGFR-এ লিনাগ্লিপটিনের মাত্রা বদলাতে হয় না; ইনসুলিনও নিরাপদ বিকল্প।',
  'ADA Standards of Care in Diabetes 2025 s11 (CKD); KDIGO 2022 Clinical Practice Guideline for Diabetes Management in CKD; US FDA metformin labelling, 2016 revision.',
  '{"subject":{"match":"GENERIC","generics":["empagliflozin + metformin hydrochloride","glimepiride + metformin hydrochloride","linagliptin + metformin hydrochloride","metformin hydrochloride","pioglitazone + metformin hydrochloride","sitagliptin + metformin hydrochloride","vildagliptin + metformin hydrochloride"]},"when":[{"kind":"EGFR","operator":"LT","value":30,"unit":"mL/min/1.73m2"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'MET-RENAL-45', 'RENAL', 'WARN',
  'Metformin below eGFR 45', 'eGFR 45-এর নিচে মেটফরমিন',
  'Do not start metformin below an eGFR of 45 mL/min/1.73m2. If the patient is already on it, reassess the benefit and do not exceed 1000 mg a day.',
  'eGFR 45 mL/min/1.73m2-এর নিচে নতুন করে মেটফরমিন শুরু করবেন না। আগে থেকে চললে লাভ-ক্ষতি বিবেচনা করুন এবং দিনে 1000 mg-এর বেশি নয়।',
  'Recheck creatinine every three months at this level of function.',
  'এই মাত্রার কিডনি কার্যকারিতায় প্রতি তিন মাসে ক্রিয়েটিনিন দেখুন।',
  'US FDA metformin labelling, 2016 revision; KDIGO 2022 Diabetes in CKD, recommendation 1.3.',
  '{"subject":{"match":"GENERIC","generics":["empagliflozin + metformin hydrochloride","glimepiride + metformin hydrochloride","linagliptin + metformin hydrochloride","metformin hydrochloride","pioglitazone + metformin hydrochloride","sitagliptin + metformin hydrochloride","vildagliptin + metformin hydrochloride"]},"when":[{"kind":"EGFR","operator":"LT","value":45,"unit":"mL/min/1.73m2"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'SGLT2-RENAL-25', 'RENAL', 'WARN',
  'SGLT2 inhibitor below eGFR 25', 'eGFR 25-এর নিচে SGLT2 ইনহিবিটর',
  'Below an eGFR of 25 mL/min/1.73m2 an SGLT2 inhibitor no longer lowers glucose meaningfully. It may still be continued for its kidney and heart benefit, but not started for glycaemic control.',
  'eGFR 25 mL/min/1.73m2-এর নিচে SGLT2 ইনহিবিটর দিয়ে রক্তে শর্করা আর তেমন কমে না। কিডনি ও হৃদযন্ত্রের সুরক্ষার জন্য চালিয়ে যাওয়া যেতে পারে, কিন্তু শর্করা নিয়ন্ত্রণের জন্য নতুন করে শুরু নয়।',
  'Add or switch to insulin for glycaemic control.',
  'শর্করা নিয়ন্ত্রণের জন্য ইনসুলিন যোগ করুন বা তাতে বদলান।',
  'ADA Standards of Care in Diabetes 2025 s11; KDIGO 2022 Diabetes in CKD; dapagliflozin and empagliflozin EU SmPCs.',
  '{"subject":{"match":"CLASS","classes":["SGLT2_INHIBITOR","SGLT2_INHIBITOR_BIGUANIDE_FDC"]},"when":[{"kind":"EGFR","operator":"LT","value":25,"unit":"mL/min/1.73m2"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'SITA-RENAL-45', 'RENAL', 'WARN',
  'Sitagliptin below eGFR 45', 'eGFR 45-এর নিচে সিটাগ্লিপটিন',
  'Sitagliptin is renally cleared. Below an eGFR of 45 mL/min/1.73m2 the daily dose is 50 mg.',
  'সিটাগ্লিপটিন কিডনি দিয়ে বের হয়। eGFR 45 mL/min/1.73m2-এর নিচে দৈনিক মাত্রা 50 mg।',
  'Halve the dose, or use linagliptin, which needs no adjustment.',
  'মাত্রা অর্ধেক করুন, অথবা লিনাগ্লিপটিন দিন — তাতে মাত্রা বদলাতে হয় না।',
  'US FDA sitagliptin labelling (Januvia), dosage in renal impairment; BNF 88.',
  '{"subject":{"match":"GENERIC","generics":["sitagliptin","sitagliptin + metformin hydrochloride"]},"when":[{"kind":"EGFR","operator":"LT","value":45,"unit":"mL/min/1.73m2"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'SITA-RENAL-30', 'RENAL', 'WARN',
  'Sitagliptin below eGFR 30', 'eGFR 30-এর নিচে সিটাগ্লিপটিন',
  'Below an eGFR of 30 mL/min/1.73m2 the sitagliptin dose is 25 mg a day.',
  'eGFR 30 mL/min/1.73m2-এর নিচে সিটাগ্লিপটিনের মাত্রা দিনে 25 mg।',
  'Use linagliptin instead if a 25 mg sitagliptin tablet is not stocked.',
  '25 mg সিটাগ্লিপটিন না থাকলে লিনাগ্লিপটিন দিন।',
  'US FDA sitagliptin labelling (Januvia), dosage in renal impairment; BNF 88.',
  '{"subject":{"match":"GENERIC","generics":["sitagliptin","sitagliptin + metformin hydrochloride"]},"when":[{"kind":"EGFR","operator":"LT","value":30,"unit":"mL/min/1.73m2"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'GLIB-RENAL-60', 'RENAL', 'WARN',
  'Glibenclamide in renal impairment', 'কিডনির দুর্বলতায় গ্লিবেনক্লামাইড',
  'Glibenclamide has active metabolites that accumulate in renal impairment and cause prolonged hypoglycaemia. Avoid below an eGFR of 60 mL/min/1.73m2.',
  'গ্লিবেনক্লামাইডের সক্রিয় উপাদান কিডনির দুর্বলতায় জমে থাকে এবং দীর্ঘস্থায়ী হাইপোগ্লাইসেমিয়া ঘটায়। eGFR 60 mL/min/1.73m2-এর নিচে এড়িয়ে চলুন।',
  'Gliclazide is the safer sulphonylurea here; glimepiride at a reduced dose is acceptable.',
  'এখানে গ্লিক্লাজাইড নিরাপদ; কম মাত্রায় গ্লিমেপিরাইডও চলে।',
  'KDIGO 2022 Diabetes in CKD; BNF 88, glibenclamide renal impairment; Scottish Intercollegiate Guidelines Network 154.',
  '{"subject":{"match":"GENERIC","generics":["glibenclamide"]},"when":[{"kind":"EGFR","operator":"LT","value":60,"unit":"mL/min/1.73m2"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'GLIM-RENAL-30', 'RENAL', 'WARN',
  'Glimepiride below eGFR 30', 'eGFR 30-এর নিচে গ্লিমেপিরাইড',
  'Below an eGFR of 30 mL/min/1.73m2 glimepiride carries a high risk of prolonged hypoglycaemia. Start at 1 mg and titrate slowly, or avoid.',
  'eGFR 30 mL/min/1.73m2-এর নিচে গ্লিমেপিরাইডে দীর্ঘস্থায়ী হাইপোগ্লাইসেমিয়ার ঝুঁকি বেশি। 1 mg দিয়ে শুরু করে ধীরে বাড়ান, বা এড়িয়ে চলুন।',
  'Insulin or linagliptin is usually the better choice at this level of function.',
  'এই মাত্রার কিডনি কার্যকারিতায় সাধারণত ইনসুলিন বা লিনাগ্লিপটিনই ভালো।',
  'BNF 88, glimepiride renal impairment; KDIGO 2022 Diabetes in CKD.',
  '{"subject":{"match":"GENERIC","generics":["glimepiride","glimepiride + metformin hydrochloride","pioglitazone + glimepiride"]},"when":[{"kind":"EGFR","operator":"LT","value":30,"unit":"mL/min/1.73m2"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'RAAS-RENAL-30', 'RENAL', 'INFO',
  'ACE inhibitor or ARB below eGFR 30', 'eGFR 30-এর নিচে ACE ইনহিবিটর বা ARB',
  'An ACE inhibitor or ARB is usually continued at this level of kidney function for its protective effect, but potassium and creatinine need checking one to two weeks after any dose change.',
  'এই মাত্রার কিডনি কার্যকারিতায় ACE ইনহিবিটর বা ARB সাধারণত সুরক্ষার জন্য চালিয়ে যাওয়া হয়, তবে মাত্রা বদলানোর 1-2 সপ্তাহ পর পটাশিয়াম ও ক্রিয়েটিনিন দেখতে হবে।',
  'Order serum potassium and creatinine at the next visit.',
  'পরের ভিজিটে সিরাম পটাশিয়াম ও ক্রিয়েটিনিন দিন।',
  'KDIGO 2024 CKD Evaluation and Management, s3.3; ADA Standards of Care 2025 s11.',
  '{"subject":{"match":"CLASS","classes":["ACE_INHIBITOR","ANGIOTENSIN_II_RECEPTOR_BLOCKER","ARB_CCB_FDC","ARB_THIAZIDE_FDC"]},"when":[{"kind":"EGFR","operator":"LT","value":30,"unit":"mL/min/1.73m2"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'PIO-HEPATIC', 'HEPATIC', 'BLOCK',
  'Pioglitazone in severe hepatic impairment', 'তীব্র যকৃৎ-দুর্বলতায় পায়োগ্লিটাজোন',
  'Pioglitazone is contraindicated in severe hepatic impairment.',
  'তীব্র যকৃৎ-দুর্বলতায় পায়োগ্লিটাজোন দেওয়া যাবে না।',
  'Use insulin. Check ALT before starting any thiazolidinedione.',
  'ইনসুলিন দিন। থায়াজোলিডিনডায়ন শুরুর আগে ALT দেখুন।',
  'US FDA pioglitazone labelling (Actos), contraindications and hepatic effects; BNF 88.',
  '{"subject":{"match":"CLASS","classes":["THIAZOLIDINEDIONE","THIAZOLIDINEDIONE_BIGUANIDE_FDC","THIAZOLIDINEDIONE_SULPHONYLUREA_FDC"]},"when":[{"kind":"HEPATIC","states":["SEVERE"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'STATIN-HEPATIC', 'HEPATIC', 'BLOCK',
  'Statin in severe hepatic impairment', 'তীব্র যকৃৎ-দুর্বলতায় স্ট্যাটিন',
  'A statin is contraindicated in active liver disease or severe hepatic impairment.',
  'সক্রিয় যকৃৎ-রোগ বা তীব্র যকৃৎ-দুর্বলতায় স্ট্যাটিন দেওয়া যাবে না।',
  'Treat the liver disease first and reassess the cardiovascular risk afterwards.',
  'আগে যকৃতের রোগের চিকিৎসা করুন, পরে হৃদরোগের ঝুঁকি আবার বিবেচনা করুন।',
  'BNF 88, statins contraindications; US FDA atorvastatin and rosuvastatin labelling.',
  '{"subject":{"match":"CLASS","classes":["STATIN"]},"when":[{"kind":"HEPATIC","states":["SEVERE"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'MET-HEPATIC', 'HEPATIC', 'WARN',
  'Metformin in severe hepatic impairment', 'তীব্র যকৃৎ-দুর্বলতায় মেটফরমিন',
  'Severe hepatic impairment raises the risk of lactic acidosis on metformin, because the liver can no longer clear lactate.',
  'তীব্র যকৃৎ-দুর্বলতায় মেটফরমিনে ল্যাকটিক অ্যাসিডোসিসের ঝুঁকি বাড়ে, কারণ যকৃৎ আর ল্যাকটেট সরাতে পারে না।',
  'Avoid metformin where there is cirrhosis with decompensation or ongoing alcohol use.',
  'ডিকম্পেনসেটেড সিরোসিস বা চলমান মদ্যপান থাকলে মেটফরমিন এড়িয়ে চলুন।',
  'US FDA metformin labelling, warnings on lactic acidosis; BNF 88.',
  '{"subject":{"match":"GENERIC","generics":["empagliflozin + metformin hydrochloride","glimepiride + metformin hydrochloride","linagliptin + metformin hydrochloride","metformin hydrochloride","pioglitazone + metformin hydrochloride","sitagliptin + metformin hydrochloride","vildagliptin + metformin hydrochloride"]},"when":[{"kind":"HEPATIC","states":["SEVERE"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'ACE-PREG', 'PREGNANCY', 'BLOCK',
  'ACE inhibitor in pregnancy', 'গর্ভাবস্থায় ACE ইনহিবিটর',
  'An ACE inhibitor is contraindicated in pregnancy. Exposure in the second and third trimesters causes fetal renal failure, oligohydramnios and skull hypoplasia.',
  'গর্ভাবস্থায় ACE ইনহিবিটর দেওয়া যাবে না। দ্বিতীয় ও তৃতীয় ত্রৈমাসিকে এটি ভ্রূণের কিডনি বিকল, অলিগোহাইড্রামনিওস ও খুলির অসম্পূর্ণ গঠন ঘটায়।',
  'Stop it today. Labetalol, methyldopa or nifedipine are the antihypertensives of pregnancy.',
  'আজই বন্ধ করুন। গর্ভাবস্থায় ল্যাবেটালল, মিথাইলডোপা বা নিফেডিপিন ব্যবহার করুন।',
  'US FDA boxed warning, fetal toxicity, ACE inhibitor class labelling; ACOG Practice Bulletin 203, Chronic Hypertension in Pregnancy.',
  '{"subject":{"match":"CLASS","classes":["ACE_INHIBITOR"]},"when":[{"kind":"PREGNANCY","states":["PREGNANT"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'ARB-PREG', 'PREGNANCY', 'BLOCK',
  'ARB in pregnancy', 'গর্ভাবস্থায় ARB',
  'An angiotensin receptor blocker is contraindicated in pregnancy, for the same fetal renal toxicity as an ACE inhibitor.',
  'গর্ভাবস্থায় অ্যাঞ্জিওটেনসিন রিসেপ্টর ব্লকার দেওয়া যাবে না; ACE ইনহিবিটরের মতোই ভ্রূণের কিডনির ক্ষতি করে।',
  'Stop it today and switch to labetalol, methyldopa or nifedipine.',
  'আজই বন্ধ করে ল্যাবেটালল, মিথাইলডোপা বা নিফেডিপিনে বদলান।',
  'US FDA boxed warning, fetal toxicity, ARB class labelling; ACOG Practice Bulletin 203.',
  '{"subject":{"match":"CLASS","classes":["ANGIOTENSIN_II_RECEPTOR_BLOCKER","ARB_CCB_FDC","ARB_THIAZIDE_FDC"]},"when":[{"kind":"PREGNANCY","states":["PREGNANT"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'STATIN-PREG', 'PREGNANCY', 'WARN',
  'Statin in pregnancy or breastfeeding', 'গর্ভাবস্থা বা স্তন্যদানে স্ট্যাটিন',
  'A statin is normally stopped in pregnancy and while breastfeeding. The FDA removed the blanket contraindication in 2021, so continuing is a decision to take deliberately in very high cardiovascular risk, not a default.',
  'গর্ভাবস্থা ও স্তন্যদানের সময় সাধারণত স্ট্যাটিন বন্ধ রাখা হয়। FDA 2021 সালে পূর্ণ নিষেধাজ্ঞা তুলে নিয়েছে, তাই খুব বেশি হৃদরোগ-ঝুঁকিতে চালিয়ে যাওয়া একটি সচেতন সিদ্ধান্ত, নিয়মমাফিক কিছু নয়।',
  'Stop unless there is homozygous familial hypercholesterolaemia or established cardiovascular disease, and record the reason.',
  'হোমোজাইগাস ফ্যামিলিয়াল হাইপারকোলেস্টেরোলেমিয়া বা প্রতিষ্ঠিত হৃদরোগ না থাকলে বন্ধ করুন, এবং কারণ লিখে রাখুন।',
  'US FDA Drug Safety Communication, 20 July 2021, removing the contraindication of statins in pregnancy; BNF 88.',
  '{"subject":{"match":"CLASS","classes":["STATIN"]},"when":[{"kind":"PREGNANCY","states":["BREASTFEEDING","PREGNANT"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'CBZ-PREG', 'PREGNANCY', 'WARN',
  'Carbimazole or methimazole in pregnancy', 'গর্ভাবস্থায় কার্বিমাজল বা মিথিমাজল',
  'Carbimazole and methimazole cause a recognised embryopathy when taken in the first trimester. Propylthiouracil is preferred up to about 16 weeks; after that the balance reverses because of propylthiouracil hepatotoxicity.',
  'প্রথম ত্রৈমাসিকে কার্বিমাজল ও মিথিমাজল ভ্রূণের বিকৃতি ঘটাতে পারে। প্রায় 16 সপ্তাহ পর্যন্ত প্রোপাইলথায়োইউরাসিলই পছন্দনীয়; এরপর প্রোপাইলথায়োইউরাসিলের যকৃৎ-ক্ষতির কারণে হিসাবটা উল্টে যায়।',
  'Confirm the gestational age. Switch to propylthiouracil if under 16 weeks.',
  'গর্ভকাল নিশ্চিত করুন। 16 সপ্তাহের কম হলে প্রোপাইলথায়োইউরাসিলে বদলান।',
  'American Thyroid Association 2017 Guidelines for the Diagnosis and Management of Thyroid Disease During Pregnancy and the Postpartum, recommendations 27-29.',
  '{"subject":{"match":"GENERIC","generics":["carbimazole","methimazole"]},"when":[{"kind":"PREGNANCY","states":["PREGNANT"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'PTU-PREG', 'PREGNANCY', 'INFO',
  'Propylthiouracil in pregnancy', 'গর্ভাবস্থায় প্রোপাইলথায়োইউরাসিল',
  'Propylthiouracil is the antithyroid drug of the first trimester. After about 16 weeks the usual advice is to switch to carbimazole or methimazole, because propylthiouracil carries a risk of severe liver injury.',
  'প্রথম ত্রৈমাসিকে প্রোপাইলথায়োইউরাসিলই ব্যবহার্য। প্রায় 16 সপ্তাহের পর সাধারণত কার্বিমাজল বা মিথিমাজলে বদলানোর পরামর্শ দেওয়া হয়, কারণ প্রোপাইলথায়োইউরাসিলে তীব্র যকৃৎ-ক্ষতির ঝুঁকি আছে।',
  'Check the gestational age and the liver function.',
  'গর্ভকাল ও যকৃতের কার্যকারিতা দেখুন।',
  'American Thyroid Association 2017 Guidelines for Thyroid Disease During Pregnancy, recommendation 29.',
  '{"subject":{"match":"GENERIC","generics":["propylthiouracil"]},"when":[{"kind":"PREGNANCY","states":["PREGNANT"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'NONINSULIN-PREG', 'PREGNANCY', 'WARN',
  'Non-insulin glucose-lowering drug in pregnancy', 'গর্ভাবস্থায় ইনসুলিন ছাড়া শর্করা-কমানো ওষুধ',
  'Insulin is the treatment of hyperglycaemia in pregnancy. SGLT2 inhibitors, GLP-1 receptor agonists, DPP-4 inhibitors, sulphonylureas and thiazolidinediones are not recommended and most have no safety data.',
  'গর্ভাবস্থায় রক্তে শর্করা বেশি হলে ইনসুলিনই চিকিৎসা। SGLT2 ইনহিবিটর, GLP-1 রিসেপ্টর অ্যাগোনিস্ট, DPP-4 ইনহিবিটর, সালফোনাইলইউরিয়া ও থায়াজোলিডিনডায়ন পরামর্শযোগ্য নয়, এবং বেশিরভাগেরই নিরাপত্তার তথ্য নেই।',
  'Convert to insulin. Metformin may be continued where it was already in use and the patient is well controlled.',
  'ইনসুলিনে বদলান। আগে থেকে মেটফরমিন চলছে এবং নিয়ন্ত্রণ ভালো থাকলে তা চালিয়ে যাওয়া যায়।',
  'ADA Standards of Care in Diabetes 2025 s15, Management of Diabetes in Pregnancy.',
  '{"subject":{"match":"CLASS","classes":["DPP_4_INHIBITOR","DPP_4_INHIBITOR_BIGUANIDE_FDC","GLP_1_RECEPTOR_AGONIST","SGLT2_INHIBITOR","SGLT2_INHIBITOR_BIGUANIDE_FDC","SULPHONYLUREA","SULPHONYLUREA_BIGUANIDE_FDC","THIAZOLIDINEDIONE","THIAZOLIDINEDIONE_BIGUANIDE_FDC","THIAZOLIDINEDIONE_SULPHONYLUREA_FDC"]},"when":[{"kind":"PREGNANCY","states":["PREGNANT"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'GLP1-PREG-PLAN', 'PREGNANCY', 'WARN',
  'GLP-1 receptor agonist when pregnancy is planned', 'গর্ভধারণের পরিকল্পনায় GLP-1 রিসেপ্টর অ্যাগোনিস্ট',
  'A GLP-1 receptor agonist should be stopped before conception. Semaglutide has a long half-life and the label asks for it to be discontinued at least two months before a planned pregnancy.',
  'গর্ভধারণের আগে GLP-1 রিসেপ্টর অ্যাগোনিস্ট বন্ধ করা উচিত। সেমাগ্লুটাইডের অর্ধায়ু দীর্ঘ, তাই পরিকল্পিত গর্ভধারণের অন্তত 2 মাস আগে বন্ধ করতে বলা হয়েছে।',
  'Stop it and plan the switch to insulin or metformin now, not at the first missed period.',
  'এখনই বন্ধ করে ইনসুলিন বা মেটফরমিনে যাওয়ার পরিকল্পনা করুন, মাসিক বন্ধ হওয়ার অপেক্ষায় নয়।',
  'US FDA semaglutide labelling (Ozempic, Wegovy), use in specific populations; ADA Standards of Care 2025 s15.',
  '{"subject":{"match":"CLASS","classes":["GLP_1_RECEPTOR_AGONIST"]},"when":[{"kind":"PREGNANCY","states":["PLANNING"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'PIO-PAED', 'PAEDIATRIC', 'WARN',
  'Pioglitazone under 18', '18 বছরের কম বয়সে পায়োগ্লিটাজোন',
  'Pioglitazone has not been shown to be safe or effective in anyone under 18.',
  '18 বছরের কম বয়সে পায়োগ্লিটাজোনের নিরাপত্তা ও কার্যকারিতা প্রমাণিত নয়।',
  'Metformin and insulin are the options with paediatric evidence.',
  'শিশু-কিশোরদের ক্ষেত্রে প্রমাণ আছে মেটফরমিন ও ইনসুলিনের।',
  'US FDA pioglitazone labelling (Actos), paediatric use; BNF for Children 2024.',
  '{"subject":{"match":"CLASS","classes":["THIAZOLIDINEDIONE","THIAZOLIDINEDIONE_BIGUANIDE_FDC","THIAZOLIDINEDIONE_SULPHONYLUREA_FDC"]},"when":[{"kind":"AGE","operator":"LT","value":18,"unit":"years"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'SGLT2-PAED', 'PAEDIATRIC', 'WARN',
  'SGLT2 inhibitor under 18', '18 বছরের কম বয়সে SGLT2 ইনহিবিটর',
  'SGLT2 inhibitors are not established below 18 except for empagliflozin, which is licensed from 10 years in type 2 diabetes in some jurisdictions.',
  '18 বছরের কম বয়সে SGLT2 ইনহিবিটর প্রতিষ্ঠিত নয়; কিছু দেশে টাইপ 2 ডায়াবেটিসে 10 বছর থেকে এমপাগ্লিফ্লোজিন অনুমোদিত।',
  'Check the patient''s age and the licence before prescribing.',
  'দেওয়ার আগে রোগীর বয়স ও অনুমোদন দেখে নিন।',
  'US FDA empagliflozin labelling (Jardiance), paediatric use, 2023 revision; ISPAD Clinical Practice Consensus Guidelines 2022.',
  '{"subject":{"match":"CLASS","classes":["SGLT2_INHIBITOR","SGLT2_INHIBITOR_BIGUANIDE_FDC"]},"when":[{"kind":"AGE","operator":"LT","value":18,"unit":"years"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'SU-PAED', 'PAEDIATRIC', 'WARN',
  'Sulphonylurea under 18', '18 বছরের কম বয়সে সালফোনাইলইউরিয়া',
  'A sulphonylurea in a young person needs the diagnosis confirming first. Monogenic diabetes responds to it; type 1 does not, and treating type 1 with it is dangerous.',
  'অল্পবয়সীতে সালফোনাইলইউরিয়া দেওয়ার আগে রোগনির্ণয় নিশ্চিত করতে হবে। মনোজেনিক ডায়াবেটিসে এটি কাজ করে; টাইপ 1-এ করে না, বরং বিপজ্জনক।',
  'Confirm the diabetes type before prescribing.',
  'দেওয়ার আগে ডায়াবেটিসের ধরন নিশ্চিত করুন।',
  'ISPAD Clinical Practice Consensus Guidelines 2022, monogenic diabetes; BNF for Children 2024.',
  '{"subject":{"match":"CLASS","classes":["SULPHONYLUREA","SULPHONYLUREA_BIGUANIDE_FDC","THIAZOLIDINEDIONE_SULPHONYLUREA_FDC"]},"when":[{"kind":"AGE","operator":"LT","value":18,"unit":"years"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'PGB-PAED', 'PAEDIATRIC', 'WARN',
  'Pregabalin or gabapentin under 18', '18 বছরের কম বয়সে প্রেগাবালিন বা গ্যাবাপেন্টিন',
  'Pregabalin is not licensed for neuropathic pain under 18, and diabetic neuropathy at that age should prompt a review of the diagnosis.',
  '18 বছরের কম বয়সে স্নায়ুব্যথায় প্রেগাবালিন অনুমোদিত নয়; এই বয়সে ডায়াবেটিক নিউরোপ্যাথি দেখা দিলে রোগনির্ণয় আবার দেখা উচিত।',
  'Reconsider the diagnosis before treating the pain.',
  'ব্যথার চিকিৎসার আগে রোগনির্ণয় আবার ভাবুন।',
  'BNF for Children 2024, pregabalin; UK SmPC for pregabalin, paediatric population.',
  '{"subject":{"match":"CLASS","classes":["NEUROPATHIC_PAIN_DIABETIC_NEUROPATHY"]},"when":[{"kind":"AGE","operator":"LT","value":18,"unit":"years"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'ACE-ARB-DUAL', 'INTERACTION', 'WARN',
  'ACE inhibitor with an ARB', 'ACE ইনহিবিটরের সঙ্গে ARB',
  'Blocking the renin-angiotensin system twice gives no extra benefit and causes more hyperkalaemia, hypotension and acute kidney injury than either drug alone.',
  'রেনিন-অ্যাঞ্জিওটেনসিন ব্যবস্থাকে দুবার আটকালে বাড়তি লাভ হয় না, বরং একটির তুলনায় হাইপারক্যালেমিয়া, রক্তচাপ পড়ে যাওয়া ও হঠাৎ কিডনি বিকলের ঝুঁকি বাড়ে।',
  'Keep one. Increase its dose instead of adding the other.',
  'একটিই রাখুন। অন্যটি যোগ না করে এটিরই মাত্রা বাড়ান।',
  'ONTARGET trial, N Engl J Med 2008;358:1547; ADA Standards of Care 2025 s10; NICE NG136.',
  '{"subject":{"match":"CLASS","classes":["ACE_INHIBITOR"]},"when":[{"kind":"CO_PRESCRIBED","with":{"match":"CLASS","classes":["ANGIOTENSIN_II_RECEPTOR_BLOCKER","ARB_CCB_FDC","ARB_THIAZIDE_FDC"]},"current_medications":true}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'SU-INSULIN', 'INTERACTION', 'WARN',
  'Sulphonylurea with insulin', 'সালফোনাইলইউরিয়ার সঙ্গে ইনসুলিন',
  'A sulphonylurea alongside insulin multiplies the risk of hypoglycaemia, particularly overnight and particularly in the elderly.',
  'ইনসুলিনের সঙ্গে সালফোনাইলইউরিয়া দিলে হাইপোগ্লাইসেমিয়ার ঝুঁকি অনেক বেড়ে যায়, বিশেষত রাতে এবং বয়স্কদের ক্ষেত্রে।',
  'Reduce or stop the sulphonylurea when basal insulin is started, and warn the patient about night-time hypoglycaemia.',
  'বেসাল ইনসুলিন শুরু করলে সালফোনাইলইউরিয়া কমান বা বন্ধ করুন, এবং রাতের হাইপোগ্লাইসেমিয়া সম্পর্কে রোগীকে সতর্ক করুন।',
  'ADA Standards of Care in Diabetes 2025 s9, combination injectable therapy; BNF 88 interactions.',
  '{"subject":{"match":"CLASS","classes":["SULPHONYLUREA","SULPHONYLUREA_BIGUANIDE_FDC","THIAZOLIDINEDIONE_SULPHONYLUREA_FDC"]},"when":[{"kind":"CO_PRESCRIBED","with":{"match":"CLASS","classes":["INSULIN_INTERMEDIATE_ACTING_HUMAN","INSULIN_LONG_ACTING_ANALOGUE","INSULIN_PREMIXED_ANALOGUE","INSULIN_PREMIXED_HUMAN","INSULIN_RAPID_ACTING_ANALOGUE","INSULIN_SHORT_ACTING_HUMAN","INSULIN_ULTRA_LONG_ACTING_ANALOGUE"]},"current_medications":true}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'STATIN-FIBRATE', 'INTERACTION', 'WARN',
  'Statin with a fibrate', 'স্ট্যাটিনের সঙ্গে ফাইব্রেট',
  'A statin with a fibrate raises the risk of myopathy and rhabdomyolysis. Fenofibrate is the safer of the fibrates in this combination; gemfibrozil should not be combined at all.',
  'স্ট্যাটিনের সঙ্গে ফাইব্রেট দিলে পেশির ক্ষতি ও র‍্যাবডোমায়োলাইসিসের ঝুঁকি বাড়ে। এই সংমিশ্রণে ফেনোফাইব্রেটই তুলনামূলক নিরাপদ; জেমফাইব্রোজিল একেবারেই একসঙ্গে দেওয়া উচিত নয়।',
  'Warn the patient to report muscle pain. Check creatine kinase if it occurs.',
  'পেশিতে ব্যথা হলে জানাতে বলুন। হলে ক্রিয়েটিন কাইনেজ দেখুন।',
  'US FDA statin labelling, myopathy and rhabdomyolysis; BNF 88 interactions; ACC/AHA 2018 Cholesterol Guideline.',
  '{"subject":{"match":"CLASS","classes":["STATIN"]},"when":[{"kind":"CO_PRESCRIBED","with":{"match":"CLASS","classes":["FIBRATE"]},"current_medications":true}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'LEVO-CALCIUM', 'INTERACTION', 'WARN',
  'Levothyroxine with a calcium supplement', 'লেভোথাইরক্সিনের সঙ্গে ক্যালসিয়াম',
  'Calcium binds levothyroxine in the gut and reduces its absorption. Taken together, the TSH will not settle whatever the dose.',
  'ক্যালসিয়াম অন্ত্রে লেভোথাইরক্সিনের সঙ্গে যুক্ত হয়ে শোষণ কমিয়ে দেয়। একসঙ্গে খেলে মাত্রা যাই হোক TSH ঠিক হবে না।',
  'Levothyroxine on an empty stomach in the morning; the calcium at least four hours later.',
  'সকালে খালি পেটে লেভোথাইরক্সিন; ক্যালসিয়াম অন্তত 4 ঘণ্টা পরে।',
  'American Thyroid Association 2014 Guidelines for Hypothyroidism, recommendation 22; BNF 88 interactions.',
  '{"subject":{"match":"GENERIC","generics":["levothyroxine sodium"]},"when":[{"kind":"CO_PRESCRIBED","with":{"match":"CLASS","classes":["CALCIUM_VITAMIN_D_SUPPLEMENT"]},"current_medications":true}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'GLP1-DPP4', 'INTERACTION', 'WARN',
  'GLP-1 receptor agonist with a DPP-4 inhibitor', 'GLP-1 রিসেপ্টর অ্যাগোনিস্টের সঙ্গে DPP-4 ইনহিবিটর',
  'Both act on the incretin pathway. Adding a DPP-4 inhibitor to a GLP-1 receptor agonist adds cost and side effects without adding glycaemic benefit.',
  'দুটিই ইনক্রেটিন পথে কাজ করে। GLP-1 রিসেপ্টর অ্যাগোনিস্টের সঙ্গে DPP-4 ইনহিবিটর যোগ করলে খরচ ও পার্শ্বপ্রতিক্রিয়া বাড়ে, শর্করা নিয়ন্ত্রণে লাভ হয় না।',
  'Stop the DPP-4 inhibitor.',
  'DPP-4 ইনহিবিটর বন্ধ করুন।',
  'ADA Standards of Care in Diabetes 2025 s9, pharmacologic approaches; EASD/ADA consensus report 2022.',
  '{"subject":{"match":"CLASS","classes":["GLP_1_RECEPTOR_AGONIST"]},"when":[{"kind":"CO_PRESCRIBED","with":{"match":"CLASS","classes":["DPP_4_INHIBITOR","DPP_4_INHIBITOR_BIGUANIDE_FDC"]},"current_medications":true}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'PIO-HEART-FAILURE', 'CONTRAINDICATION', 'BLOCK',
  'Pioglitazone in heart failure', 'হৃদযন্ত্রের বিকলতায় পায়োগ্লিটাজোন',
  'Pioglitazone causes fluid retention and is contraindicated in symptomatic heart failure. It can precipitate decompensation within weeks.',
  'পায়োগ্লিটাজোন শরীরে পানি ধরে রাখে এবং লক্ষণযুক্ত হৃদযন্ত্রের বিকলতায় দেওয়া যাবে না। কয়েক সপ্তাহের মধ্যেই অবস্থার অবনতি ঘটাতে পারে।',
  'An SGLT2 inhibitor is the glucose-lowering drug that helps heart failure rather than worsening it.',
  'হৃদযন্ত্রের বিকলতায় SGLT2 ইনহিবিটরই সেই ওষুধ যা অবস্থা খারাপ না করে বরং ভালো করে।',
  'US FDA boxed warning, congestive heart failure, pioglitazone labelling (Actos); ADA Standards of Care 2025 s10.',
  '{"subject":{"match":"CLASS","classes":["THIAZOLIDINEDIONE","THIAZOLIDINEDIONE_BIGUANIDE_FDC","THIAZOLIDINEDIONE_SULPHONYLUREA_FDC"]},"when":[{"kind":"DIAGNOSIS","diagnosis_codes":["I50.9"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'GLP1-MTC', 'CONTRAINDICATION', 'BLOCK',
  'GLP-1 receptor agonist with medullary thyroid carcinoma or MEN2', 'মেডুলারি থাইরয়েড কার্সিনোমা বা MEN2-এ GLP-1 রিসেপ্টর অ্যাগোনিস্ট',
  'A GLP-1 receptor agonist is contraindicated where there is a personal or family history of medullary thyroid carcinoma or multiple endocrine neoplasia type 2.',
  'নিজের বা পরিবারে মেডুলারি থাইরয়েড কার্সিনোমা বা MEN টাইপ 2 থাকলে GLP-1 রিসেপ্টর অ্যাগোনিস্ট দেওয়া যাবে না।',
  'Use an SGLT2 inhibitor or insulin instead.',
  'বদলে SGLT2 ইনহিবিটর বা ইনসুলিন দিন।',
  'US FDA boxed warning, thyroid C-cell tumours, GLP-1 receptor agonist class labelling (Ozempic, Trulicity, Victoza).',
  '{"subject":{"match":"CLASS","classes":["GLP_1_RECEPTOR_AGONIST"]},"when":[{"kind":"DIAGNOSIS","diagnosis_codes":["C73","E31.2"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'MET-ACIDOSIS', 'CONTRAINDICATION', 'BLOCK',
  'Metformin in acidosis or ketoacidosis', 'অ্যাসিডোসিস বা কিটোঅ্যাসিডোসিসে মেটফরমিন',
  'Metformin is contraindicated in acute metabolic acidosis, including diabetic ketoacidosis.',
  'তীব্র বিপাকীয় অ্যাসিডোসিসে, ডায়াবেটিক কিটোঅ্যাসিডোসিস সহ, মেটফরমিন দেওয়া যাবে না।',
  'Treat with insulin and fluids. Restart metformin only after the acidosis has resolved.',
  'ইনসুলিন ও স্যালাইন দিয়ে চিকিৎসা করুন। অ্যাসিডোসিস সেরে গেলে তবেই মেটফরমিন আবার শুরু করুন।',
  'US FDA metformin labelling, contraindications; ADA Standards of Care 2025 s16, hyperglycaemic crises.',
  '{"subject":{"match":"GENERIC","generics":["empagliflozin + metformin hydrochloride","glimepiride + metformin hydrochloride","linagliptin + metformin hydrochloride","metformin hydrochloride","pioglitazone + metformin hydrochloride","sitagliptin + metformin hydrochloride","vildagliptin + metformin hydrochloride"]},"when":[{"kind":"DIAGNOSIS","diagnosis_codes":["E10.1","E87.2"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'SULFA-SU-ALLERGY', 'CONTRAINDICATION', 'INFO',
  'Sulphonylurea after a reported sulfonamide antibiotic allergy', 'সালফোনামাইড অ্যান্টিবায়োটিকে অ্যালার্জির পর সালফোনাইলইউরিয়া',
  'A reported allergy to a sulfonamide antibiotic is not by itself a reason to avoid a sulphonylurea. The cross-reactivity most prescribers assume is not supported by the evidence, and avoiding the whole class costs the patient a useful drug.',
  'সালফোনামাইড অ্যান্টিবায়োটিকে অ্যালার্জির কথা শুনে সালফোনাইলইউরিয়া বাদ দেওয়ার যথেষ্ট কারণ নেই। বেশিরভাগ চিকিৎসক যে ক্রস-রিঅ্যাকশন ধরে নেন, প্রমাণ তা সমর্থন করে না, আর গোটা শ্রেণিটি বাদ দিলে রোগী একটি কাজের ওষুধ হারান।',
  'Ask what the reaction actually was. A rash years ago is not anaphylaxis.',
  'প্রতিক্রিয়াটি ঠিক কী ছিল জিজ্ঞাসা করুন। বহু বছর আগের র‍্যাশ অ্যানাফাইল্যাক্সিস নয়।',
  'Strom BL et al., Absence of cross-reactivity between sulfonamide antibiotics and sulfonamide nonantibiotics, N Engl J Med 2003;349:1628; AAAAI/ACAAI Drug Allergy Practice Parameter 2022.',
  '{"subject":{"match":"CLASS","classes":["SULPHONYLUREA","SULPHONYLUREA_BIGUANIDE_FDC","THIAZOLIDINEDIONE_SULPHONYLUREA_FDC"]},"when":[{"kind":"ALLERGY","allergen_group":"SULFONAMIDE_ANTIBIOTIC"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'DUP-GENERIC', 'DUPLICATE_THERAPY', 'WARN',
  'The same molecule twice', 'একই অণু দুবার',
  'The same molecule appears more than once on this prescription. Split dosing is sometimes intended; two brands of the same drug usually is not.',
  'এই ব্যবস্থাপত্রে একই অণু একাধিকবার আছে। কখনও ভাগ করে মাত্রা দেওয়া হয়, কিন্তু একই ওষুধের দুটি ব্র্যান্ড সাধারণত ভুল।',
  'Keep one, or confirm that split dosing is what you meant.',
  'একটি রাখুন, নয়তো নিশ্চিত করুন যে ভাগ করে মাত্রা দেওয়াই উদ্দেশ্য।',
  'Drafted here. Corresponds to the duplicate-therapy check the plan names for CP78.',
  '{"subject":{"match":"ANY"},"when":[{"kind":"DUPLICATE","with":{"match":"GENERIC"}}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'DUP-SULPHONYLUREA', 'DUPLICATE_THERAPY', 'WARN',
  'Two sulphonylureas', 'দুটি সালফোনাইলইউরিয়া',
  'Two sulphonylureas together add hypoglycaemia without adding glycaemic control. This most often happens when a fixed-dose combination is prescribed alongside its own component.',
  'দুটি সালফোনাইলইউরিয়া একসঙ্গে দিলে শর্করা নিয়ন্ত্রণ বাড়ে না, হাইপোগ্লাইসেমিয়াই বাড়ে। সাধারণত এটি ঘটে যখন কোনো কম্বিনেশন ওষুধের সঙ্গে তারই একটি উপাদান আলাদা করে দেওয়া হয়।',
  'Keep one sulphonylurea and adjust its dose.',
  'একটি সালফোনাইলইউরিয়া রেখে তার মাত্রা ঠিক করুন।',
  'ADA Standards of Care in Diabetes 2025 s9; BNF 88, sulphonylureas.',
  '{"subject":{"match":"CLASS","classes":["SULPHONYLUREA","SULPHONYLUREA_BIGUANIDE_FDC","THIAZOLIDINEDIONE_SULPHONYLUREA_FDC"]},"when":[{"kind":"DUPLICATE","with":{"match":"CLASS"},"current_medications":true}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'DUP-STATIN', 'DUPLICATE_THERAPY', 'WARN',
  'Two statins', 'দুটি স্ট্যাটিন',
  'Two statins together give no extra LDL reduction and multiply the risk of myopathy.',
  'দুটি স্ট্যাটিন একসঙ্গে দিলে LDL বাড়তি কমে না, বরং পেশির ক্ষতির ঝুঁকি বহুগুণ বাড়ে।',
  'Keep one and raise its dose, or add ezetimibe.',
  'একটি রেখে মাত্রা বাড়ান, নয়তো ইজেটিমাইব যোগ করুন।',
  'ACC/AHA 2018 Guideline on the Management of Blood Cholesterol; BNF 88, statins.',
  '{"subject":{"match":"CLASS","classes":["STATIN"]},"when":[{"kind":"DUPLICATE","with":{"match":"CLASS"},"current_medications":true}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'MET-MAXDOSE', 'MAX_DOSE', 'WARN',
  'Metformin above 2550 mg a day', 'দিনে 2550 mg-এর বেশি মেটফরমিন',
  'The licensed maximum for immediate-release metformin is 2550 mg a day; for the extended-release forms it is 2000 mg. Above that there is no extra glycaemic benefit, only more gastrointestinal upset.',
  'তাৎক্ষণিক-নিঃসারী মেটফরমিনের অনুমোদিত সর্বোচ্চ মাত্রা দিনে 2550 mg; দীর্ঘ-নিঃসারী রূপে 2000 mg। এর বেশি দিলে শর্করা আর কমে না, শুধু পেটের সমস্যা বাড়ে।',
  'Add a second agent rather than raising metformin further.',
  'মেটফরমিন আর না বাড়িয়ে দ্বিতীয় একটি ওষুধ যোগ করুন।',
  'US FDA metformin labelling, dosage and administration; BNF 88.',
  '{"subject":{"match":"GENERIC","generics":["empagliflozin + metformin hydrochloride","glimepiride + metformin hydrochloride","linagliptin + metformin hydrochloride","metformin hydrochloride","pioglitazone + metformin hydrochloride","sitagliptin + metformin hydrochloride","vildagliptin + metformin hydrochloride"]},"when":[{"kind":"DAILY_DOSE","operator":"GT","value":2550,"unit":"mg"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'GLIM-MAXDOSE', 'MAX_DOSE', 'WARN',
  'Glimepiride above 8 mg a day', 'দিনে 8 mg-এর বেশি গ্লিমেপিরাইড',
  'The licensed maximum for glimepiride is 8 mg a day, and most of the effect is reached by 4 mg.',
  'গ্লিমেপিরাইডের অনুমোদিত সর্বোচ্চ মাত্রা দিনে 8 mg, আর 4 mg-এই বেশিরভাগ কাজ হয়ে যায়।',
  'Add a second agent rather than raising the sulphonylurea.',
  'সালফোনাইলইউরিয়া না বাড়িয়ে দ্বিতীয় একটি ওষুধ যোগ করুন।',
  'US FDA glimepiride labelling (Amaryl), dosage; BNF 88.',
  '{"subject":{"match":"GENERIC","generics":["glimepiride","glimepiride + metformin hydrochloride","pioglitazone + glimepiride"]},"when":[{"kind":"DAILY_DOSE","operator":"GT","value":8,"unit":"mg"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'PIO-MAXDOSE', 'MAX_DOSE', 'WARN',
  'Pioglitazone above 45 mg a day', 'দিনে 45 mg-এর বেশি পায়োগ্লিটাজোন',
  'The licensed maximum for pioglitazone is 45 mg a day. Oedema and heart-failure risk rise with the dose.',
  'পায়োগ্লিটাজোনের অনুমোদিত সর্বোচ্চ মাত্রা দিনে 45 mg। মাত্রা বাড়ার সঙ্গে পানি জমা ও হৃদযন্ত্রের বিকলতার ঝুঁকি বাড়ে।',
  'Do not exceed 45 mg; use 30 mg where there is any oedema.',
  '45 mg-এর বেশি নয়; কোথাও পানি জমলে 30 mg দিন।',
  'US FDA pioglitazone labelling (Actos), dosage; BNF 88.',
  '{"subject":{"match":"CLASS","classes":["THIAZOLIDINEDIONE","THIAZOLIDINEDIONE_BIGUANIDE_FDC","THIAZOLIDINEDIONE_SULPHONYLUREA_FDC"]},"when":[{"kind":"DAILY_DOSE","operator":"GT","value":45,"unit":"mg"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'ATORVA-MAXDOSE', 'MAX_DOSE', 'WARN',
  'Atorvastatin above 80 mg a day', 'দিনে 80 mg-এর বেশি অ্যাটরভাস্ট্যাটিন',
  'The licensed maximum for atorvastatin is 80 mg a day.',
  'অ্যাটরভাস্ট্যাটিনের অনুমোদিত সর্বোচ্চ মাত্রা দিনে 80 mg।',
  'Add ezetimibe rather than exceeding 80 mg.',
  '80 mg ছাড়ানোর বদলে ইজেটিমাইব যোগ করুন।',
  'US FDA atorvastatin labelling (Lipitor), dosage; BNF 88.',
  '{"subject":{"match":"GENERIC","generics":["atorvastatin calcium"]},"when":[{"kind":"DAILY_DOSE","operator":"GT","value":80,"unit":"mg"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'PGB-MAXDOSE', 'MAX_DOSE', 'WARN',
  'Pregabalin above 600 mg a day', 'দিনে 600 mg-এর বেশি প্রেগাবালিন',
  'The licensed maximum for pregabalin is 600 mg a day, and in renal impairment it is much lower.',
  'প্রেগাবালিনের অনুমোদিত সর্বোচ্চ মাত্রা দিনে 600 mg, আর কিডনির দুর্বলতা থাকলে আরও অনেক কম।',
  'Check the eGFR before going above 300 mg a day.',
  'দিনে 300 mg ছাড়ানোর আগে eGFR দেখে নিন।',
  'US FDA pregabalin labelling (Lyrica), dosage and renal adjustment; BNF 88.',
  '{"subject":{"match":"GENERIC","generics":["pregabalin"]},"when":[{"kind":"DAILY_DOSE","operator":"GT","value":600,"unit":"mg"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'LEVO-MAXDOSE', 'MAX_DOSE', 'INFO',
  'Levothyroxine above 300 mcg a day', 'দিনে 300 mcg-এর বেশি লেভোথাইরক্সিন',
  'A daily levothyroxine requirement above 300 mcg is unusual. It usually means the tablets are not being absorbed or not being taken, rather than that the dose is too low.',
  'দিনে 300 mcg-এর বেশি লেভোথাইরক্সিন লাগা অস্বাভাবিক। সাধারণত এর মানে ওষুধ শোষিত হচ্ছে না বা খাওয়া হচ্ছে না — মাত্রা কম, তা নয়।',
  'Ask about coeliac disease, calcium and iron timing, and whether the tablets are actually taken.',
  'সিলিয়াক রোগ, ক্যালসিয়াম ও আয়রনের সময়, এবং ওষুধটি আদৌ খাওয়া হচ্ছে কি না জিজ্ঞাসা করুন।',
  'American Thyroid Association 2014 Guidelines for Hypothyroidism, recommendations 22 and 23.',
  '{"subject":{"match":"GENERIC","generics":["levothyroxine sodium"]},"when":[{"kind":"DAILY_DOSE","operator":"GT","value":300,"unit":"mcg"}]}'::jsonb) FROM core.facility f;

-- ---------------------------------------------------------------------------
-- 9. Invariants
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
-- **The one this whole file is for.** Nothing that nobody approved has ever been live.
--
-- Registered rather than left as a constraint, because a CHECK added in this migration validates
-- only what passes through it afterwards. This runs against the whole table after every migration
-- in every environment, which is what catches a restore, a hand edit, or a future migration that
-- sets an effective period without thinking about the approval beside it.
CREATE OR REPLACE FUNCTION core.assert_no_unapproved_rule_is_live() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.medication_rule_version
   WHERE effective_from IS NOT NULL AND (approved_at IS NULL OR approved_by IS NULL);
  IF offenders > 0 THEN
    RAISE EXCEPTION
      '% medication rule versions have been live without anybody approving them', offenders
      USING HINT = 'A rule that stops a prescription must name the physician who agreed to it.';
  END IF;

  SELECT count(*) INTO offenders
    FROM core.medication_rule_version
   WHERE status = 'PUBLISHED' AND effective_to IS NOT NULL;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% medication rule versions are PUBLISHED with a closed period', offenders;
  END IF;

  -- Every seeded version still says where it came from. A citation stripped by an import or an
  -- edit is a rule nobody can check against the guidance it claims to follow.
  SELECT count(*) INTO offenders
    FROM core.medication_rule_version
   WHERE btrim(source_citation) = '';
  IF offenders > 0 THEN
    RAISE EXCEPTION '% medication rule versions cite no source', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Nothing here is ever deleted, and the application role is not able to.
CREATE OR REPLACE FUNCTION core.assert_medication_rule_history_is_kept() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  SELECT string_agg(table_name, ', ' ORDER BY table_name) INTO offender
    FROM information_schema.role_table_grants
   WHERE grantee = 'dthcms_app' AND table_schema = 'core' AND privilege_type = 'DELETE'
     AND table_name IN ('medication_rule', 'medication_rule_version',
                        'allergen_group', 'allergen_group_member', 'allergen_cross_reaction');
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION
      'the application role can delete from core.%, and a withdrawn rule keeps its versions',
      offender;
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Every rule has at least one version, and its versions are numbered without gaps from 1.
-- A rule with no versions is a row that cannot be read, edited or published and looks, in every
-- listing, like a mistake; a gap in the numbering means a version was removed, which is the thing
-- reproducibility rests on not happening.
CREATE OR REPLACE FUNCTION core.assert_every_medication_rule_has_its_versions() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  SELECT string_agg(r.code, ', ' ORDER BY r.code) INTO offender
    FROM core.medication_rule r
   WHERE NOT EXISTS (SELECT 1 FROM core.medication_rule_version v WHERE v.rule_id = r.id);
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'medication rules with no versions at all: %', offender;
  END IF;

  SELECT string_agg(code, ', ' ORDER BY code) INTO offender
    FROM (
      SELECT r.code
        FROM core.medication_rule r
        JOIN core.medication_rule_version v ON v.rule_id = r.id
       GROUP BY r.id, r.code
      HAVING max(v.version) <> count(*) OR min(v.version) <> 1
    ) gaps;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'medication rules whose version numbering has a gap: %', offender;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_no_unapproved_rule_is_live',
   'no medication rule version has ever been live without a named physician approving it', 113),
  ('assert_every_medication_rule_has_its_versions',
   'every medication rule has its versions, numbered from 1 without a gap', 114),
  ('assert_medication_rule_history_is_kept',
   'the application role cannot delete a medication rule, a version, or an allergen mapping', 115)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_no_unapproved_rule_is_live',
  'assert_every_medication_rule_has_its_versions',
  'assert_medication_rule_history_is_kept');
DROP FUNCTION IF EXISTS core.assert_no_unapproved_rule_is_live();
DROP FUNCTION IF EXISTS core.assert_every_medication_rule_has_its_versions();
DROP FUNCTION IF EXISTS core.assert_medication_rule_history_is_kept();

DROP FUNCTION IF EXISTS core.seed_medication_rule(uuid, text, text, text, text, text, text, text,
                                                  text, text, text, jsonb);

DELETE FROM core.role_permission
 WHERE permission_code IN ('medication.rule.read', 'medication.rule.write',
                           'medication.rule.publish');
DELETE FROM core.permission
 WHERE code IN ('medication.rule.read', 'medication.rule.write', 'medication.rule.publish');

DROP TRIGGER IF EXISTS medication_rule_version_frozen ON core.medication_rule_version;
DROP FUNCTION IF EXISTS core.medication_rule_version_is_frozen();

DROP TABLE IF EXISTS core.medication_rule_version;
DROP TABLE IF EXISTS core.medication_rule;
DROP TABLE IF EXISTS core.allergen_cross_reaction;
DROP TABLE IF EXISTS core.allergen_group_member;
DROP TABLE IF EXISTS core.allergen_group;

DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core'
   AND table_name IN ('allergen_group', 'allergen_group_member', 'allergen_cross_reaction',
                      'medication_rule_version');

-- The five ICD-10 codes stay. They are real codes in the WHO's own classification, a diagnosis
-- may already have been recorded against one, and CP52's catalogue is not this migration's to
-- shrink on the way out.
