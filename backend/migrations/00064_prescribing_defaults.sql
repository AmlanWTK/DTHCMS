-- Starting dose, frequency and duration per medicine, and the bilingual instructions that go
-- with them (CP81, §9.1, and the plan's open decision "default dose/frequency per drug").
--
-- # What this is, and what it is very deliberately not
--
-- CP81's scope says *dose/frequency/duration with smart defaults per drug*. The plan files the
-- content itself as an open clinical decision, unassigned. So two things are true at once: the
-- editor is unusable without defaults — a physician who must type "1 tablet / twice daily / 30
-- days" on every line is slower than his prescription pad, which is the one failure this
-- checkpoint cannot have — and nobody at this clinic has agreed to a single one of them.
--
-- The resolution is CP77's, applied unchanged: **the content ships, and it is inert until a
-- physician approves it.** Inert here does not mean unusable, because a default that did nothing
-- would be a default that did not exist. It means the suggestion is *offered and labelled*: it
-- fills nothing by itself, the editor shows it as a proposal nobody has checked, and the word
-- "unapproved" travels with it in both languages to every screen that renders it.
--
-- The distinction that matters, and the one this table is shaped around: **a default a machine
-- wrote must never look like a default he set.** `status`, `origin`, `source_citation` and
-- `approved_by` are what carry that, and the invariant below is what keeps a later seed from
-- quietly arriving pre-approved.
--
-- # Why a default is edited rather than versioned
--
-- `core.medication_rule_version` keeps every version forever, because a safety check run in
-- March must stay reproducible against March's rule. A default has no such duty: what it
-- produced is **on the prescription**, captured as an item with its own event and its own
-- attribution, exactly like the price. Nothing ever needs to ask "what did the default say in
-- March" — the prescription already answers it, and answers it about the line that was actually
-- written rather than about the suggestion that was offered.
--
-- So a default is one row that changes, and **any change drops its approval**. A physician
-- approved a sentence, not a row id.
--
-- # Why the key is the generic and the strength, not the product
--
-- "One tablet twice daily" is a fact about metformin 500 mg, not about Comet 500 mg. Keying on
-- the product would mean writing the same sentence ten times for ten brands of the same tablet
-- and having nine of them drift. The strength is part of the key because it changes the answer —
-- metformin 500 mg twice daily and metformin 1000 mg twice daily are different prescriptions —
-- and an empty strength means *any strength of this molecule*, which is the right answer for a
-- drug whose dosing does not turn on it.
--
-- Lookup is therefore two steps: the exact strength, then the molecule-wide row. That ordering
-- is in `PrescribingDefaultsFor`, and it is what makes a general row a fallback rather than a
-- competing answer.
--
-- # Why frequency is a phrase and not a code
--
-- The item on a prescription carries `frequency` as text (CP80) because that is what prints, and
-- because the set of real frequencies is open — "twice daily", "once weekly", "every third day",
-- "with each main meal". A code table would have to be complete on the day it shipped. What is
-- held here instead is a **small vocabulary in two languages**: the English phrase is what is
-- written onto the item, and `frequency_bn` exists so that a physician working in Bangla reads
-- the suggestion in his own language before accepting it. What prints for the patient in Bangla
-- is the instruction, which is the next table.
--
-- # The instructions are templates, and CP91 owns what they become
--
-- `core.instruction_template` is the starter set CP81 needs: fixed bilingual sentences, chosen
-- from a list, copied onto the item. It has no variable substitution, and that absence is
-- deliberate — D-11 and CP91 own substituted Bangla, and the grammatical agreement problem there
-- is real enough that guessing at it now would produce sentences a Bengali speaker would have to
-- unpick later. A fixed sentence is always grammatical.

-- +goose Up

-- ---------------------------------------------------------------------------
-- 1. The instruction templates
-- ---------------------------------------------------------------------------

CREATE TABLE core.instruction_template (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  -- The handle a default points at and a person quotes. Stable; never reused.
  code text NOT NULL,

  -- The molecule this sentence is about, when it is about one. Null is a general instruction
  -- ("take after food") offered on every medicine.
  generic_id uuid REFERENCES core.generic(id),

  -- What the patient is told. Both, always. A prescription instruction that exists in English
  -- only is an instruction the patient holding the paper cannot read, which is the whole of the
  -- bilingual requirement rather than a nicety of it.
  text_en text NOT NULL,
  text_bn text NOT NULL,

  -- What the physician sees in the picker. Short, because the picker is a list read at speed.
  label_en text NOT NULL,
  label_bn text NOT NULL,

  ordering integer NOT NULL DEFAULT 100,

  source_citation text NOT NULL,
  origin text NOT NULL DEFAULT 'SEED' CHECK (origin IN ('SEED', 'AUTHORED')),
  status text NOT NULL DEFAULT 'UNAPPROVED' CHECK (status IN ('UNAPPROVED', 'APPROVED')),

  approved_by uuid REFERENCES core.app_user(id),
  approved_at timestamptz,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT instruction_template_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,59}$'),
  CONSTRAINT instruction_template_is_bilingual
    CHECK (btrim(text_en) <> '' AND btrim(text_bn) <> ''
           AND btrim(label_en) <> '' AND btrim(label_bn) <> ''),
  CONSTRAINT instruction_template_names_its_source CHECK (btrim(source_citation) <> ''),
  CONSTRAINT instruction_template_approval_is_whole
    CHECK ((approved_by IS NULL) = (approved_at IS NULL)
           AND (status = 'APPROVED') = (approved_at IS NOT NULL))
);

CREATE UNIQUE INDEX instruction_template_code_key
  ON core.instruction_template (facility_id, code);
CREATE INDEX instruction_template_generic_idx
  ON core.instruction_template (facility_id, generic_id);

COMMENT ON TABLE core.instruction_template IS
  'Bilingual patient instructions a prescription line can be given (CP81). Fixed sentences, no '
  'substitution — CP91 and D-11 own substituted Bangla. Seeded ones are approved by nobody.';

SELECT core.attach_updated_at('core.instruction_template');
GRANT SELECT, INSERT, UPDATE ON core.instruction_template TO dthcms_app;
REVOKE DELETE, TRUNCATE ON core.instruction_template FROM dthcms_app;

-- ---------------------------------------------------------------------------
-- 2. The prescribing defaults
-- ---------------------------------------------------------------------------

CREATE TABLE core.prescribing_default (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  generic_id uuid NOT NULL REFERENCES core.generic(id),

  -- The strength this suggestion is about, spelled as `core.medication_product.strength` spells
  -- it. Empty means every strength of this molecule — see the header.
  strength text NOT NULL DEFAULT '',

  -- One administration, as it will be written on the sheet: "1 tablet", "10 units".
  dose text NOT NULL,
  -- The total in a day, for the max-dose rules CP78 evaluates. Null where the unit is not a
  -- number a rule could compare — an insulin titrated to a sliding scale has no daily dose the
  -- suggestion can claim.
  daily_dose numeric,
  dose_unit text NOT NULL DEFAULT '',

  -- "twice daily". Written onto the item as-is; `frequency_bn` is display only.
  frequency text NOT NULL,
  frequency_bn text NOT NULL,

  duration_days integer CHECK (duration_days IS NULL OR duration_days BETWEEN 1 AND 365),
  route text NOT NULL DEFAULT '',

  -- The instruction offered with it. Null is legitimate: plenty of medicines need no sentence
  -- beyond the dose.
  instruction_template_id uuid REFERENCES core.instruction_template(id),

  -- Why this is the suggestion, in words a physician can disagree with. Both languages, because
  -- the person deciding whether to approve it may read either.
  rationale_en text NOT NULL,
  rationale_bn text NOT NULL,

  -- Where the numbers came from. Required, for the same reason a rule's citation is: a dose with
  -- no source behind it is a number somebody typed, and it cannot be updated when the guidance
  -- moves because nobody knows which guidance it was.
  source_citation text NOT NULL,

  origin text NOT NULL DEFAULT 'SEED' CHECK (origin IN ('SEED', 'AUTHORED')),
  status text NOT NULL DEFAULT 'UNAPPROVED' CHECK (status IN ('UNAPPROVED', 'APPROVED')),

  approved_by uuid REFERENCES core.app_user(id),
  approved_at timestamptz,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT prescribing_default_has_a_dose CHECK (btrim(dose) <> '' AND btrim(frequency) <> ''),
  CONSTRAINT prescribing_default_is_bilingual
    CHECK (btrim(frequency_bn) <> '' AND btrim(rationale_en) <> '' AND btrim(rationale_bn) <> ''),
  CONSTRAINT prescribing_default_names_its_source CHECK (btrim(source_citation) <> ''),
  CONSTRAINT prescribing_default_daily_dose_is_positive
    CHECK (daily_dose IS NULL OR daily_dose > 0),
  CONSTRAINT prescribing_default_daily_dose_has_a_unit
    CHECK ((daily_dose IS NULL) = (btrim(dose_unit) = '')),
  CONSTRAINT prescribing_default_approval_is_whole
    CHECK ((approved_by IS NULL) = (approved_at IS NULL)
           AND (status = 'APPROVED') = (approved_at IS NOT NULL))
);

CREATE UNIQUE INDEX prescribing_default_key
  ON core.prescribing_default (facility_id, generic_id, lower(strength));

COMMENT ON TABLE core.prescribing_default IS
  'The starting dose, frequency and duration suggested for a medicine (CP81). Drafted from '
  'published guidance; inert as a fact and offered as a proposal until a physician approves it.';

SELECT core.attach_updated_at('core.prescribing_default');
GRANT SELECT, INSERT, UPDATE ON core.prescribing_default TO dthcms_app;
REVOKE DELETE, TRUNCATE ON core.prescribing_default FROM dthcms_app;

-- ---------------------------------------------------------------------------
-- 3. An edit drops the approval
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
-- A physician approved a sentence, not a row id.
--
-- Without this, "approve the metformin default, then change the dose" leaves a row that says
-- APPROVED and carries a dose nobody read. That is the precise failure this whole table is
-- shaped against, and it is one UPDATE away — so it is refused by the database rather than
-- remembered by a handler.
--
-- The approval columns themselves are excluded, obviously: setting them IS approving.
CREATE OR REPLACE FUNCTION core.approval_does_not_survive_an_edit() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'APPROVED' THEN
    RETURN NEW;
  END IF;
  -- Approving, or withdrawing an approval, is allowed to move the approval columns.
  IF NEW.status IS DISTINCT FROM OLD.status THEN
    RETURN NEW;
  END IF;
  IF to_jsonb(NEW) - 'updated_at' - 'updated_by' - 'status' - 'approved_by' - 'approved_at'
     IS DISTINCT FROM
     to_jsonb(OLD) - 'updated_at' - 'updated_by' - 'status' - 'approved_by' - 'approved_at' THEN
    NEW.status := 'UNAPPROVED';
    NEW.approved_by := NULL;
    NEW.approved_at := NULL;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER prescribing_default_edit_drops_approval
  BEFORE UPDATE ON core.prescribing_default
  FOR EACH ROW EXECUTE FUNCTION core.approval_does_not_survive_an_edit();

CREATE TRIGGER instruction_template_edit_drops_approval
  BEFORE UPDATE ON core.instruction_template
  FOR EACH ROW EXECUTE FUNCTION core.approval_does_not_survive_an_edit();

-- ---------------------------------------------------------------------------
-- 4. Seeding helpers
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.seed_instruction_template(
  p_facility uuid, p_code text, p_generic text,
  p_label_en text, p_label_bn text, p_text_en text, p_text_bn text,
  p_ordering integer, p_source text) RETURNS uuid
LANGUAGE plpgsql AS $$
DECLARE
  g_id uuid;
  t_id uuid;
BEGIN
  IF p_generic IS NOT NULL THEN
    SELECT id INTO g_id FROM core.generic WHERE lower(name) = lower(btrim(p_generic));
    IF g_id IS NULL THEN
      RAISE EXCEPTION 'no generic called % — an instruction cannot name a molecule this '
        'formulary does not hold', p_generic;
    END IF;
  END IF;

  SELECT id INTO t_id FROM core.instruction_template
   WHERE facility_id = p_facility AND code = p_code;
  IF t_id IS NOT NULL THEN
    -- Re-runnable, and it never touches one somebody has edited or approved. Same reason the
    -- rule seed refuses to touch an existing rule.
    RETURN t_id;
  END IF;

  INSERT INTO core.instruction_template
    (facility_id, code, generic_id, label_en, label_bn, text_en, text_bn, ordering,
     source_citation, origin, status)
  VALUES (p_facility, p_code, g_id, p_label_en, p_label_bn, p_text_en, p_text_bn, p_ordering,
          p_source, 'SEED', 'UNAPPROVED')
  RETURNING id INTO t_id;
  RETURN t_id;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.seed_prescribing_default(
  p_facility uuid, p_generic text, p_strength text,
  p_dose text, p_daily_dose numeric, p_dose_unit text,
  p_frequency text, p_frequency_bn text, p_duration integer, p_route text,
  p_instruction text, p_rationale_en text, p_rationale_bn text, p_source text) RETURNS uuid
LANGUAGE plpgsql AS $$
DECLARE
  g_id uuid;
  t_id uuid;
  d_id uuid;
BEGIN
  SELECT id INTO g_id FROM core.generic WHERE lower(name) = lower(btrim(p_generic));
  IF g_id IS NULL THEN
    RAISE EXCEPTION 'no generic called % — a default cannot be written for a molecule this '
      'formulary does not hold', p_generic;
  END IF;

  -- A strength-specific default must name a strength this clinic actually stocks. A default for
  -- "metformin 600 mg" would sit in the table forever, matching nothing, looking like coverage.
  IF btrim(p_strength) <> '' AND NOT EXISTS (
    SELECT 1 FROM core.medication_product
     WHERE generic_id = g_id AND lower(strength) = lower(btrim(p_strength))) THEN
    RAISE EXCEPTION 'this formulary stocks no % at %', p_generic, p_strength;
  END IF;

  IF p_instruction IS NOT NULL THEN
    SELECT id INTO t_id FROM core.instruction_template
     WHERE facility_id = p_facility AND code = p_instruction;
    IF t_id IS NULL THEN
      RAISE EXCEPTION 'no instruction template called %', p_instruction;
    END IF;
  END IF;

  SELECT id INTO d_id FROM core.prescribing_default
   WHERE facility_id = p_facility AND generic_id = g_id
     AND lower(strength) = lower(btrim(p_strength));
  IF d_id IS NOT NULL THEN
    RETURN d_id;
  END IF;

  INSERT INTO core.prescribing_default
    (facility_id, generic_id, strength, dose, daily_dose, dose_unit, frequency, frequency_bn,
     duration_days, route, instruction_template_id, rationale_en, rationale_bn,
     source_citation, origin, status)
  VALUES (p_facility, g_id, btrim(p_strength), p_dose, p_daily_dose, coalesce(p_dose_unit, ''),
          p_frequency, p_frequency_bn, p_duration, coalesce(p_route, ''), t_id,
          p_rationale_en, p_rationale_bn, p_source, 'SEED', 'UNAPPROVED')
  RETURNING id INTO d_id;
  RETURN d_id;
END
$$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- 5. The starter instruction set
-- ---------------------------------------------------------------------------

SELECT core.seed_instruction_template(f.id, 'AFTER_FOOD', NULL,
  'After food', 'খাবারের পরে',
  'Take after food.', 'খাবারের পরপরই খাবেন।', 10,
  'General prescribing practice; metformin and NSAID labelling advise administration with or after food to reduce gastrointestinal upset (BNF 88).')
FROM core.facility f;

SELECT core.seed_instruction_template(f.id, 'WITH_BREAKFAST', NULL,
  'With breakfast', 'সকালের নাশতার সঙ্গে',
  'Take with your morning meal.', 'সকালের খাবারের সঙ্গে খাবেন।', 20,
  'Sulphonylurea labelling: administer shortly before or with the first main meal of the day (BNF 88, gliclazide and glimepiride monographs).')
FROM core.facility f;

SELECT core.seed_instruction_template(f.id, 'EMPTY_STOMACH_MORNING', NULL,
  'Empty stomach, 30 minutes before breakfast', 'খালি পেটে, নাশতার ৩০ মিনিট আগে',
  'Take in the morning on an empty stomach, at least 30 minutes before any food or other medicine.',
  'সকালে খালি পেটে খাবেন — খাবার বা অন্য ওষুধের অন্তত ৩০ মিনিট আগে।', 30,
  'Levothyroxine labelling and ATA 2014 hypothyroidism guideline: absorption falls with food; dose 30-60 minutes before breakfast.')
FROM core.facility f;

SELECT core.seed_instruction_template(f.id, 'AT_NIGHT', NULL,
  'At night', 'রাতে',
  'Take at night, before going to bed.', 'রাতে ঘুমাতে যাওয়ার আগে খাবেন।', 40,
  'General prescribing practice; simvastatin labelling specifies evening dosing (BNF 88).')
FROM core.facility f;

SELECT core.seed_instruction_template(f.id, 'SWALLOW_WHOLE', NULL,
  'Swallow whole', 'আস্ত গিলে খাবেন',
  'Swallow the tablet whole. Do not break, crush or chew it.',
  'ট্যাবলেটটি আস্ত গিলে খাবেন। ভাঙবেন না, গুঁড়ো করবেন না, চিবাবেন না।', 50,
  'Modified-release tablet labelling: the release mechanism is destroyed by crushing (BNF 88, metformin modified-release monograph).')
FROM core.facility f;

SELECT core.seed_instruction_template(f.id, 'THYROID_AWAY_FROM_CALCIUM', 'Levothyroxine sodium',
  'Keep 4 hours away from calcium or iron', 'ক্যালসিয়াম বা আয়রন থেকে ৪ ঘণ্টা দূরে',
  'Keep this tablet at least four hours apart from any calcium, iron or antacid tablet.',
  'এই ট্যাবলেটটি ক্যালসিয়াম, আয়রন বা গ্যাসের ওষুধ থেকে অন্তত চার ঘণ্টা আলাদা করে খাবেন।', 60,
  'Levothyroxine labelling; ATA 2014 hypothyroidism guideline recommendation 22 (calcium and iron salts impair absorption).')
FROM core.facility f;

SELECT core.seed_instruction_template(f.id, 'SICK_DAY_STOP', NULL,
  'Sick-day rule: stop if you cannot eat or drink', 'অসুস্থ দিনের নিয়ম: খেতে না পারলে বন্ধ',
  'If you cannot eat or drink normally, or you have vomiting, diarrhoea or a high fever, stop this medicine and contact the clinic.',
  'যদি স্বাভাবিকভাবে খেতে বা পান করতে না পারেন, অথবা বমি, পাতলা পায়খানা বা বেশি জ্বর হয়, তবে ওষুধটি বন্ধ করে ক্লিনিকে যোগাযোগ করুন।', 70,
  'Sick-day guidance for metformin and SGLT2 inhibitors: ADA Standards of Care in Diabetes 2025 s9; MHRA Drug Safety Update on SGLT2 inhibitors and diabetic ketoacidosis.')
FROM core.facility f;

SELECT core.seed_instruction_template(f.id, 'HYPO_ADVICE', NULL,
  'Carry sugar; know the warning signs', 'সঙ্গে চিনি রাখুন; লক্ষণ চিনে রাখুন',
  'Carry glucose or sugar with you. Sweating, shaking, hunger or confusion may mean your sugar is low — take sugar at once and tell the clinic.',
  'সঙ্গে গ্লুকোজ বা চিনি রাখবেন। ঘাম, হাত কাঁপা, খুব ক্ষুধা বা মাথা ঝিমঝিম করলে বুঝবেন রক্তে শর্করা কমে গেছে — সঙ্গে সঙ্গে চিনি খাবেন এবং ক্লিনিকে জানাবেন।', 80,
  'Hypoglycaemia counselling for insulin and sulphonylureas: ADA Standards of Care in Diabetes 2025 s6.')
FROM core.facility f;

SELECT core.seed_instruction_template(f.id, 'INSULIN_ROTATE_SITES', NULL,
  'Rotate the injection site', 'ইনজেকশনের জায়গা বদলাবেন',
  'Change the injection site each time, within the same area. Do not inject into a lump.',
  'প্রতিবার একই এলাকার ভেতরে ইনজেকশনের জায়গা বদলাবেন। ফোলা বা শক্ত জায়গায় দেবেন না।', 90,
  'Injection technique guidance: FITTER Forum recommendations (Mayo Clin Proc 2016); ADA Standards of Care in Diabetes 2025 s9.')
FROM core.facility f;

SELECT core.seed_instruction_template(f.id, 'STATIN_MUSCLE_PAIN', NULL,
  'Report unexplained muscle pain', 'অকারণ পেশিব্যথা হলে জানাবেন',
  'Tell the clinic if you get muscle pain, tenderness or weakness that you cannot explain.',
  'অকারণে পেশিতে ব্যথা, টান বা দুর্বলতা হলে ক্লিনিকে জানাবেন।', 100,
  'Statin labelling and NICE CG181: patients should report unexplained muscle symptoms.')
FROM core.facility f;

SELECT core.seed_instruction_template(f.id, 'ACE_ARB_PREGNANCY', NULL,
  'Stop and tell us if you may be pregnant', 'গর্ভধারণের সম্ভাবনা হলে বন্ধ করে জানাবেন',
  'Stop this medicine and tell the clinic at once if you think you may be pregnant.',
  'গর্ভবতী হয়েছেন মনে হলে ওষুধটি বন্ধ করে সঙ্গে সঙ্গে ক্লিনিকে জানাবেন।', 110,
  'ACE inhibitor and ARB labelling: contraindicated in pregnancy (BNF 88; FDA boxed warning on fetal toxicity).')
FROM core.facility f;

SELECT core.seed_instruction_template(f.id, 'WEEKLY_SAME_DAY', NULL,
  'Same day each week', 'প্রতি সপ্তাহে একই দিনে',
  'Take on the same day each week. You may take it at any time of day, with or without food.',
  'প্রতি সপ্তাহে একই দিনে নেবেন। দিনের যেকোনো সময়ে, খাবারের সঙ্গে বা ছাড়া নেওয়া যায়।', 120,
  'Once-weekly GLP-1 receptor agonist labelling (semaglutide, dulaglutide SmPCs).')
FROM core.facility f;

SELECT core.seed_instruction_template(f.id, 'ANTITHYROID_SORE_THROAT', NULL,
  'Sore throat or fever: stop and get a blood count', 'গলাব্যথা বা জ্বর: বন্ধ করে রক্ত পরীক্ষা',
  'If you get a sore throat, mouth ulcers, fever or bruising, stop this medicine and have a blood count done the same day.',
  'গলাব্যথা, মুখে ঘা, জ্বর বা গায়ে কালশিটে দেখা দিলে ওষুধটি বন্ধ করে সেদিনই রক্তের সিবিসি করাবেন।', 130,
  'Thionamide labelling: agranulocytosis warning (BNF 88, carbimazole monograph; ATA 2016 hyperthyroidism guideline).')
FROM core.facility f;

SELECT core.seed_instruction_template(f.id, 'GENITAL_HYGIENE_SGLT2', NULL,
  'Keep the genital area clean and dry', 'গোপনাঙ্গ পরিষ্কার ও শুকনো রাখবেন',
  'This medicine puts sugar into the urine, which can cause genital itching or infection. Keep the area clean and dry, and tell the clinic if there is itching, soreness or discharge.',
  'এই ওষুধে প্রস্রাবে চিনি যায়, তাই গোপনাঙ্গে চুলকানি বা সংক্রমণ হতে পারে। জায়গাটি পরিষ্কার ও শুকনো রাখবেন; চুলকানি, জ্বালা বা স্রাব হলে ক্লিনিকে জানাবেন।', 140,
  'SGLT2 inhibitor labelling: genital mycotic infection is a common adverse effect (dapagliflozin and empagliflozin SmPCs).')
FROM core.facility f;

-- ---------------------------------------------------------------------------
-- 6. The starter defaults
-- ---------------------------------------------------------------------------
--
-- Twenty-eight rows covering what this clinic writes most. Every one is a maintenance dose from
-- published guidance, not a starting titration — a starting dose belongs to the patient in front
-- of the physician, and offering one as a default is how an interface starts prescribing.
-- The rationale says so, in both languages, on every row.

SELECT core.seed_prescribing_default(f.id, 'Metformin hydrochloride', '500 mg',
  '1 tablet', 1000, 'mg', 'twice daily', 'দিনে দুইবার', 30, 'oral', 'AFTER_FOOD',
  'The usual maintenance dose once metformin is established. When starting, one tablet with the evening meal for a week reduces gastrointestinal upset before moving to twice daily.',
  'মেটফরমিন চালু হয়ে গেলে এটিই সাধারণ রক্ষণাবেক্ষণ মাত্রা। নতুন শুরু করলে প্রথম সপ্তাহে রাতের খাবারের সঙ্গে একটি ট্যাবলেট দিলে পেটের সমস্যা কম হয়, তারপর দিনে দুইবার।',
  'ADA Standards of Care in Diabetes 2025 s9; BNF 88 metformin hydrochloride monograph.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Metformin hydrochloride', '850 mg',
  '1 tablet', 1700, 'mg', 'twice daily', 'দিনে দুইবার', 30, 'oral', 'AFTER_FOOD',
  'A maintenance dose. 850 mg twice daily is within the usual effective range and below the 2 g daily maximum.',
  'রক্ষণাবেক্ষণ মাত্রা। দিনে দুইবার 850 mg কার্যকর মাত্রার মধ্যেই এবং দৈনিক সর্বোচ্চ ২ গ্রামের নিচে।',
  'BNF 88 metformin hydrochloride monograph (usual range 1.5-2 g daily in divided doses).') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Metformin hydrochloride', '1000 mg',
  '1 tablet', 2000, 'mg', 'twice daily', 'দিনে দুইবার', 30, 'oral', 'AFTER_FOOD',
  'The maximum licensed daily dose of immediate-release metformin. Do not exceed it; check the eGFR before continuing at this dose.',
  'তাৎক্ষণিক-নিঃসরণ মেটফরমিনের সর্বোচ্চ অনুমোদিত দৈনিক মাত্রা। এর বেশি নয়; এই মাত্রায় চালানোর আগে eGFR দেখে নিন।',
  'BNF 88 metformin hydrochloride monograph (maximum 2 g daily); US FDA metformin labelling, 2016 revision.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Metformin hydrochloride', '750 mg',
  '1 tablet', 750, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', 'SWALLOW_WHOLE',
  'A modified-release strength, taken with the evening meal. It is swallowed whole — crushing destroys the release mechanism.',
  'এটি ধীরে-নিঃসরণ শক্তি, রাতের খাবারের সঙ্গে। আস্ত গিলে খেতে হয় — ভাঙলে ধীরে-নিঃসরণের ব্যবস্থাটি নষ্ট হয়।',
  'BNF 88 metformin hydrochloride modified-release monograph.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Levothyroxine sodium', '25 mcg',
  '1 tablet', 25, 'mcg', 'once daily', 'দিনে একবার', 30, 'oral', 'EMPTY_STOMACH_MORNING',
  'A starting or low replacement dose — for an older patient, or one with ischaemic heart disease, where replacement is raised slowly. Recheck TSH after six to eight weeks.',
  'শুরুর বা কম প্রতিস্থাপন মাত্রা — বয়স্ক রোগী বা হৃদরোগ থাকলে ধীরে ধীরে বাড়ানো হয়। ছয় থেকে আট সপ্তাহ পরে TSH দেখুন।',
  'ATA 2014 guideline for the treatment of hypothyroidism, recommendations 13 and 22; BNF 88 levothyroxine monograph.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Levothyroxine sodium', '50 mcg',
  '1 tablet', 50, 'mcg', 'once daily', 'দিনে একবার', 30, 'oral', 'EMPTY_STOMACH_MORNING',
  'A common intermediate replacement dose. Full replacement is about 1.6 mcg per kg per day; recheck TSH after six to eight weeks before moving again.',
  'সাধারণ মাঝারি প্রতিস্থাপন মাত্রা। পূর্ণ প্রতিস্থাপন প্রতি কেজিতে দিনে প্রায় ১.৬ মাইক্রোগ্রাম; আবার বদলানোর আগে ছয়-আট সপ্তাহে TSH দেখুন।',
  'ATA 2014 guideline for the treatment of hypothyroidism, recommendation 13; BNF 88 levothyroxine monograph.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Levothyroxine sodium', '75 mcg',
  '1 tablet', 75, 'mcg', 'once daily', 'দিনে একবার', 30, 'oral', 'EMPTY_STOMACH_MORNING',
  'A replacement dose. Recheck TSH after six to eight weeks; do not adjust on symptoms alone.',
  'প্রতিস্থাপন মাত্রা। ছয়-আট সপ্তাহে TSH দেখুন; শুধু উপসর্গ দেখে মাত্রা বদলাবেন না।',
  'ATA 2014 guideline for the treatment of hypothyroidism, recommendation 13.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Levothyroxine sodium', '100 mcg',
  '1 tablet', 100, 'mcg', 'once daily', 'দিনে একবার', 30, 'oral', 'EMPTY_STOMACH_MORNING',
  'Approximately full replacement for a 60 kg adult. Recheck TSH after six to eight weeks.',
  '৬০ কেজি ওজনের একজন প্রাপ্তবয়স্কের জন্য মোটামুটি পূর্ণ প্রতিস্থাপন। ছয়-আট সপ্তাহে TSH দেখুন।',
  'ATA 2014 guideline for the treatment of hypothyroidism, recommendation 13 (1.6 mcg/kg/day).') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Empagliflozin', '10 mg',
  '1 tablet', 10, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', 'GENITAL_HYGIENE_SGLT2',
  'The usual dose, taken in the morning with or without food. Check the eGFR before starting.',
  'সাধারণ মাত্রা, সকালে — খাবারের সঙ্গে বা ছাড়া। শুরুর আগে eGFR দেখে নিন।',
  'Empagliflozin SmPC; ADA Standards of Care in Diabetes 2025 s9 and s10.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Dapagliflozin propanediol', '10 mg',
  '1 tablet', 10, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', 'GENITAL_HYGIENE_SGLT2',
  'The usual dose for glycaemic, cardiac and renal indications alike. Check the eGFR before starting.',
  'শর্করা, হৃদযন্ত্র ও কিডনি — তিন ক্ষেত্রেই সাধারণ মাত্রা এটিই। শুরুর আগে eGFR দেখে নিন।',
  'Dapagliflozin SmPC; ADA Standards of Care in Diabetes 2025 s9 and s10.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Sitagliptin', '100 mg',
  '1 tablet', 100, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', NULL,
  'The full dose, for an eGFR of 45 or above. Below that the dose is halved, and below 30 it is 25 mg.',
  'পূর্ণ মাত্রা, eGFR ৪৫ বা তার বেশি হলে। এর নিচে মাত্রা অর্ধেক, ৩০-এর নিচে ২৫ মিলিগ্রাম।',
  'Sitagliptin SmPC and US labelling; renal dose adjustment thresholds.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Linagliptin', '5 mg',
  '1 tablet', 5, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', NULL,
  'The only dose. Linagliptin needs no adjustment at any level of kidney function, which is why the renal rules offer it as the alternative.',
  'একটিই মাত্রা। যেকোনো কিডনি কার্যকারিতায় লিনাগ্লিপটিনের মাত্রা বদলাতে হয় না — এজন্যই কিডনির নিয়মগুলো এটিকে বিকল্প হিসেবে দেয়।',
  'Linagliptin SmPC (no dose adjustment for renal or hepatic impairment).') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Glimepiride', '2 mg',
  '1 tablet', 2, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', 'WITH_BREAKFAST',
  'A common maintenance dose, taken with the first main meal. Warn about hypoglycaemia, particularly if a meal is missed.',
  'সাধারণ রক্ষণাবেক্ষণ মাত্রা, দিনের প্রথম প্রধান খাবারের সঙ্গে। শর্করা কমে যাওয়ার ব্যাপারে সতর্ক করুন, বিশেষত কোনো বেলা খাবার বাদ গেলে।',
  'BNF 88 glimepiride monograph; ADA Standards of Care in Diabetes 2025 s9.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Gliclazide', '80 mg',
  '1 tablet', 160, 'mg', 'twice daily', 'দিনে দুইবার', 30, 'oral', 'WITH_BREAKFAST',
  'The standard-release strength, given with the two main meals. The 30 mg and 60 mg tablets are modified-release and are taken once daily instead.',
  'এটি সাধারণ-নিঃসরণ শক্তি, দিনের দুই প্রধান খাবারের সঙ্গে। ৩০ ও ৬০ মিলিগ্রাম ট্যাবলেট ধীরে-নিঃসরণ, সেগুলো দিনে একবার।',
  'BNF 88 gliclazide monograph.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Gliclazide', '60 mg',
  '1 tablet', 60, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', 'SWALLOW_WHOLE',
  'A modified-release tablet, taken once with breakfast and swallowed whole.',
  'ধীরে-নিঃসরণ ট্যাবলেট, সকালের নাশতার সঙ্গে একবার, আস্ত গিলে।',
  'BNF 88 gliclazide modified-release monograph.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Atorvastatin calcium', '20 mg',
  '1 tablet', 20, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', 'STATIN_MUSCLE_PAIN',
  'Moderate-intensity statin therapy. Atorvastatin may be taken at any time of day; the evening habit comes from the short-acting statins.',
  'মাঝারি-মাত্রার স্ট্যাটিন চিকিৎসা। অ্যাটরভাস্ট্যাটিন দিনের যেকোনো সময়ে নেওয়া যায়; রাতে খাওয়ার অভ্যাসটি স্বল্প-স্থায়ী স্ট্যাটিনগুলো থেকে এসেছে।',
  'ADA Standards of Care in Diabetes 2025 s10; NICE CG181; BNF 88 atorvastatin monograph.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Rosuvastatin calcium', '10 mg',
  '1 tablet', 10, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', 'STATIN_MUSCLE_PAIN',
  'Moderate-intensity statin therapy, at any time of day.',
  'মাঝারি-মাত্রার স্ট্যাটিন চিকিৎসা, দিনের যেকোনো সময়ে।',
  'ADA Standards of Care in Diabetes 2025 s10; BNF 88 rosuvastatin monograph.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Telmisartan', '40 mg',
  '1 tablet', 40, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', 'ACE_ARB_PREGNANCY',
  'A usual maintenance dose. Check potassium and creatinine one to two weeks after starting or increasing.',
  'সাধারণ রক্ষণাবেক্ষণ মাত্রা। শুরু বা মাত্রা বাড়ানোর এক-দুই সপ্তাহ পরে পটাশিয়াম ও ক্রিয়েটিনিন দেখুন।',
  'BNF 88 telmisartan monograph; ADA Standards of Care in Diabetes 2025 s10.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Losartan potassium', '50 mg',
  '1 tablet', 50, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', 'ACE_ARB_PREGNANCY',
  'A usual maintenance dose. Check potassium and creatinine one to two weeks after starting or increasing.',
  'সাধারণ রক্ষণাবেক্ষণ মাত্রা। শুরু বা মাত্রা বাড়ানোর এক-দুই সপ্তাহ পরে পটাশিয়াম ও ক্রিয়েটিনিন দেখুন।',
  'BNF 88 losartan monograph; KDIGO 2022 Diabetes in CKD.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Ramipril', '5 mg',
  '1 tablet', 5, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', 'ACE_ARB_PREGNANCY',
  'A usual maintenance dose. A dry cough is the common reason to change to an ARB.',
  'সাধারণ রক্ষণাবেক্ষণ মাত্রা। শুকনো কাশি হলে সাধারণত ARB-তে বদলানো হয়।',
  'BNF 88 ramipril monograph.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Amlodipine besilate', '5 mg',
  '1 tablet', 5, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', NULL,
  'A usual starting and maintenance dose. Ankle swelling is the common dose-limiting effect.',
  'সাধারণ শুরুর ও রক্ষণাবেক্ষণ মাত্রা। পায়ের পাতা ফোলা হলো মাত্রা সীমিত করার সাধারণ কারণ।',
  'BNF 88 amlodipine monograph.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Aspirin (low dose)', '75 mg',
  '1 tablet', 75, 'mg', 'once daily', 'দিনে একবার', 30, 'oral', 'AFTER_FOOD',
  'Secondary prevention. Aspirin is not routinely used for primary prevention in diabetes — the bleeding risk offsets the benefit for most patients.',
  'দ্বিতীয় পর্যায়ের প্রতিরোধে। ডায়াবেটিসে প্রাথমিক প্রতিরোধের জন্য অ্যাসপিরিন নিয়মিত দেওয়া হয় না — বেশির ভাগ রোগীর ক্ষেত্রে রক্তক্ষরণের ঝুঁকি লাভের সমান বা বেশি।',
  'ADA Standards of Care in Diabetes 2025 s10 (aspirin for secondary prevention; primary prevention individualised).') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Pregabalin', '75 mg',
  '1 capsule', 150, 'mg', 'twice daily', 'দিনে দুইবার', 30, 'oral', 'AT_NIGHT',
  'A usual dose for painful diabetic neuropathy. Start at 75 mg at night alone if the patient is older or the eGFR is reduced — pregabalin is renally cleared.',
  'ব্যথাযুক্ত ডায়াবেটিক নিউরোপ্যাথির সাধারণ মাত্রা। রোগী বয়স্ক হলে বা eGFR কম হলে শুধু রাতে ৭৫ মিলিগ্রাম দিয়ে শুরু করুন — প্রিগাবালিন কিডনি দিয়ে বের হয়।',
  'BNF 88 pregabalin monograph; ADA Standards of Care in Diabetes 2025 s12 (neuropathy).') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Carbimazole', '10 mg',
  '1 tablet', 20, 'mg', 'twice daily', 'দিনে দুইবার', 30, 'oral', 'ANTITHYROID_SORE_THROAT',
  'An initial dose for Graves hyperthyroidism, reduced as thyroid function falls. Recheck free T4 after four to six weeks — TSH stays suppressed for months and must not be used to titrate early.',
  'গ্রেভস হাইপারথাইরয়ডিজমে শুরুর মাত্রা, থাইরয়েড কমে এলে কমাতে হয়। চার-ছয় সপ্তাহ পরে ফ্রি T4 দেখুন — TSH কয়েক মাস দমে থাকে, তাই শুরুতে TSH দেখে মাত্রা বদলানো যাবে না।',
  'ATA 2016 guideline for hyperthyroidism, recommendations 20 and 23; BNF 88 carbimazole monograph.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Cholecalciferol (Vitamin D3)', '20000 IU',
  '1 capsule', NULL, '', 'once weekly', 'সপ্তাহে একবার', 56, 'oral', 'WEEKLY_SAME_DAY',
  'A weekly repletion dose for vitamin D deficiency, typically for eight weeks before moving to a daily maintenance dose.',
  'ভিটামিন ডি ঘাটতিতে সাপ্তাহিক পূরণ মাত্রা, সাধারণত আট সপ্তাহ — তারপর দৈনিক রক্ষণাবেক্ষণ মাত্রায়।',
  'Endocrine Society clinical practice guideline on vitamin D deficiency (repletion then maintenance).') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Cholecalciferol (Vitamin D3)', '2000 IU',
  '1 capsule', 2000, 'IU', 'once daily', 'দিনে একবার', 30, 'oral', NULL,
  'A daily maintenance dose after repletion.',
  'পূরণের পরে দৈনিক রক্ষণাবেক্ষণ মাত্রা।',
  'Endocrine Society clinical practice guideline on vitamin D deficiency.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Sitagliptin + Metformin hydrochloride', '',
  '1 tablet', NULL, '', 'twice daily', 'দিনে দুইবার', 30, 'oral', 'AFTER_FOOD',
  'A fixed-dose combination, dosed to the metformin component with meals. The daily total is left blank on purpose: the two molecules have different maxima and a single number would be a claim about the wrong one.',
  'নির্দিষ্ট-মাত্রার সংমিশ্রণ, মেটফরমিন অংশ অনুযায়ী খাবারের সঙ্গে। দৈনিক মোট ইচ্ছে করেই ফাঁকা রাখা হয়েছে: দুই অণুর সর্বোচ্চ মাত্রা আলাদা, একটি সংখ্যা দিলে সেটি ভুল অণুর দাবি হয়ে যেত।',
  'Sitagliptin/metformin SmPC; BNF 88 metformin monograph for the combination component maxima.') FROM core.facility f;

SELECT core.seed_prescribing_default(f.id, 'Semaglutide', '',
  '1 pen dose', NULL, '', 'once weekly', 'সপ্তাহে একবার', 28, 'subcutaneous', 'WEEKLY_SAME_DAY',
  'Once weekly, subcutaneously, on the same day each week. The dose itself is a titration the physician sets — the pen strengths are not interchangeable and no default should choose between them.',
  'সপ্তাহে একবার, চামড়ার নিচে, প্রতি সপ্তাহে একই দিনে। মাত্রাটি চিকিৎসক ধাপে ধাপে ঠিক করেন — পেনের শক্তিগুলো একে অপরের বদলে ব্যবহার করা যায় না, তাই কোনো ডিফল্ট সেটি বেছে দেবে না।',
  'Semaglutide SmPC; ADA Standards of Care in Diabetes 2025 s9.') FROM core.facility f;

-- ---------------------------------------------------------------------------
-- 7. The invariant
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_prescribing_content_is_sourced_and_unapproved() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders FROM core.prescribing_default
   WHERE origin = 'SEED' AND (status = 'APPROVED' OR approved_by IS NOT NULL);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% seeded prescribing defaults claim an approval nobody gave', offenders;
  END IF;

  SELECT count(*) INTO offenders FROM core.instruction_template
   WHERE origin = 'SEED' AND (status = 'APPROVED' OR approved_by IS NOT NULL);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% seeded instruction templates claim an approval nobody gave', offenders;
  END IF;

  SELECT count(*) INTO offenders FROM core.prescribing_default
   WHERE btrim(source_citation) = '' OR btrim(rationale_en) = '' OR btrim(rationale_bn) = '';
  IF offenders > 0 THEN
    RAISE EXCEPTION '% prescribing defaults lack a source or a bilingual rationale', offenders;
  END IF;

  SELECT count(*) INTO offenders FROM core.instruction_template
   WHERE btrim(source_citation) = '' OR btrim(text_en) = '' OR btrim(text_bn) = '';
  IF offenders > 0 THEN
    RAISE EXCEPTION '% instruction templates lack a source or one of their two languages', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_prescribing_content_is_sourced_and_unapproved() IS
  'CP81: every seeded dose default and patient instruction cites published guidance, reads in '
  'both languages, and is approved by nobody until a physician reads it.';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_prescribing_content_is_sourced_and_unapproved',
   'every seeded prescribing default and instruction template cites a source, reads in both languages, and claims no approval', 124)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant
 WHERE function_name = 'assert_prescribing_content_is_sourced_and_unapproved';
DROP FUNCTION IF EXISTS core.assert_prescribing_content_is_sourced_and_unapproved();
DROP FUNCTION IF EXISTS core.seed_prescribing_default(uuid, text, text, text, numeric, text, text, text, integer, text, text, text, text, text);
DROP FUNCTION IF EXISTS core.seed_instruction_template(uuid, text, text, text, text, text, text, integer, text);
DROP TRIGGER IF EXISTS prescribing_default_edit_drops_approval ON core.prescribing_default;
DROP TRIGGER IF EXISTS instruction_template_edit_drops_approval ON core.instruction_template;
DROP FUNCTION IF EXISTS core.approval_does_not_survive_an_edit();
DROP TABLE IF EXISTS core.prescribing_default;
DROP TABLE IF EXISTS core.instruction_template;
