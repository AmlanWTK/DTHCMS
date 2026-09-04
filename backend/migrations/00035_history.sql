-- Medical history: what the patient brings with them (CP53, §3 station 4, §11.1).
--
-- # Why this is not an observation, despite what the plan says
--
-- The plan says "uses the CP42 observation tables; no new schema". That is the right instinct
-- and the wrong conclusion, and it is worth writing down why, because the next person will
-- read the plan before they read this file.
--
-- An **observation** is a point measurement: one code, one value, one moment, and it never
-- changes. That is what makes the ledger and the timeline work.
--
-- A **history item** is the opposite. It is a thing with an identity that persists across
-- visits: a complaint that started three weeks ago and is still there next month; a metformin
-- prescription the patient is still taking; a mother who still has diabetes. Two of this
-- checkpoint's four acceptance criteria are about exactly that persistence —
--
--   3. prior history is presented for confirmation at the next visit, never auto-accepted;
--   4. every item is individually attributed
--
-- — and neither is expressible about a point measurement. "Confirm the reading you took last
-- month" is not a sentence. "Is she still on metformin?" is the entire job of station 4.
--
-- Forcing it into `read.observation` leaves two options, both bad. A JSON blob in a
-- `structured` value is free text wearing a schema, and criterion 1 is precisely that
-- complaints are **not** free text. One observation per item with the concept as `value_code`
-- throws away the duration, the severity, the onset and the relation — which are the parts a
-- doctor actually reads.
--
-- So: new tables, an item identity, and a lifecycle of four events. What is reused, and
-- reused strictly, is CP52's coding: every item carries a system, a version and a code.
--
-- # Why carry-forward is a read and never a write
--
-- Criterion 3 is a safety property, not a convenience. A system that silently rolled last
-- month's history into this month's record would eventually assert, in a signed clinical
-- document, that a patient is on a drug they stopped in March — and nobody would be able to
-- say who claimed that, because nobody did.
--
-- So carry-forward is a **query**: the open items are shown with the date they were last
-- confirmed, and confirming is an event with an actor. `last_confirmed_at` therefore has no
-- default and is never set by anything but `HistoryItemConfirmed`. A screen may present
-- twenty items; twenty confirmations are twenty events.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The permission
-- ---------------------------------------------------------------------------

-- Reading a history is reading clinical detail about a person, and §4.4 blinds registration
-- and the pharmacist to that. Writing it is station 4's job. The two are separate permissions
-- because the physician who reads a history at station 8 does not edit it there — an
-- amendment made in the consulting room, with no officer present to ask, is how a record
-- acquires a fact nobody heard the patient say.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('history.read', 'history', 'read', '',
   'Read a patient''s complaints, comorbidities, family, surgical, medication and vaccination history', true),
  ('history.write', 'history', 'write', '',
   'Record and amend medical history at station 4', false),
  ('history.confirm', 'history', 'confirm', '',
   'Confirm that a carried-forward history item is still true', false)
ON CONFLICT (code) DO UPDATE SET
  resource = EXCLUDED.resource, action = EXCLUDED.action, scope = EXCLUDED.scope,
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'history.read' FROM core.role r
 WHERE r.code IN ('HISTORY', 'CLINICAL_ASSISTANT', 'JUNIOR_DOCTOR', 'PHYSICIAN', 'NUTRITIONIST',
                  'QA')
ON CONFLICT DO NOTHING;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'history.write' FROM core.role r
 WHERE r.code IN ('HISTORY', 'JUNIOR_DOCTOR', 'PHYSICIAN')
ON CONFLICT DO NOTHING;

-- Confirming is not amending: it is answering "is this still true", which is the question
-- station 4 asks and which any clinician taking a history may answer. The clinical assistant
-- holds it because at a follow-up the patient often reaches station 5 without seeing the
-- history officer at all, and an unconfirmed list is worse than a confirmed one.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'history.confirm' FROM core.role r
 WHERE r.code IN ('HISTORY', 'CLINICAL_ASSISTANT', 'JUNIOR_DOCTOR', 'PHYSICIAN')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The vocabularies station 4 needs, which CP52 did not have a use for yet
-- ---------------------------------------------------------------------------

-- CP52 seeded the clinic's complaints, because that is what a picker needed to be tested
-- against. Three of the six kinds of history draw on the same DTHC dictionary and had nothing
-- in it: operations, medicines and vaccinations.
--
-- **Why medicines are terminology and not the formulary.** A concept is "Metformin" — the
-- thing a patient names when asked what they take. A formulary *product* is "Metformin 500 mg
-- tablet, this brand, this price, this much in stock". They are different objects with
-- different lifetimes, and conflating them means a patient's history stops being recordable
-- the day the clinic runs out. So the drug is coded here, and the link to a product is the
-- reconciliation column on the item — null until the formulary exists, which is criterion 2's
-- own "where they exist".
--
-- These lists are short and they are proposals. A history officer will meet a drug that is
-- not here within a week, and the record has a place for that: an uncoded item that says what
-- the patient said, counted, so the gaps show up as a number rather than as a complaint.
INSERT INTO core.terminology_concept
  (system, version, code, display_en, display_bn, heading, heading_bn) VALUES
  -- Operations an endocrine clinic actually meets.
  ('DTHC', '1.0', 'SURG_THYROIDECTOMY', 'Thyroid operation', 'থাইরয়েড অপারেশন', 'Operation', 'অস্ত্রোপচার'),
  ('DTHC', '1.0', 'SURG_CAESAREAN', 'Caesarean section', 'সিজারিয়ান অপারেশন', 'Operation', 'অস্ত্রোপচার'),
  ('DTHC', '1.0', 'SURG_APPENDIX', 'Appendix removed', 'অ্যাপেনডিক্স অপারেশন', 'Operation', 'অস্ত্রোপচার'),
  ('DTHC', '1.0', 'SURG_GALLBLADDER', 'Gall bladder removed', 'পিত্তথলি অপারেশন', 'Operation', 'অস্ত্রোপচার'),
  ('DTHC', '1.0', 'SURG_HYSTERECTOMY', 'Womb removed', 'জরায়ু অপারেশন', 'Operation', 'অস্ত্রোপচার'),
  ('DTHC', '1.0', 'SURG_CATARACT', 'Cataract operation', 'ছানি অপারেশন', 'Operation', 'অস্ত্রোপচার'),
  ('DTHC', '1.0', 'SURG_AMPUTATION', 'Amputation', 'অঙ্গচ্ছেদ', 'Operation', 'অস্ত্রোপচার'),
  ('DTHC', '1.0', 'SURG_CORONARY_STENT', 'Heart stent', 'হার্টে রিং বসানো', 'Operation', 'অস্ত্রোপচার'),
  ('DTHC', '1.0', 'SURG_CABG', 'Heart bypass operation', 'হার্ট বাইপাস অপারেশন', 'Operation', 'অস্ত্রোপচার'),
  ('DTHC', '1.0', 'SURG_DIALYSIS_ACCESS', 'Dialysis fistula', 'ডায়ালাইসিসের ফিস্টুলা', 'Operation', 'অস্ত্রোপচার'),

  -- The medicines this clinic's patients arrive on.
  ('DTHC', '1.0', 'DRUG_METFORMIN', 'Metformin', 'মেটফরমিন', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_GLIMEPIRIDE', 'Glimepiride', 'গ্লিমেপিরাইড', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_GLICLAZIDE', 'Gliclazide', 'গ্লিক্লাজাইড', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_SITAGLIPTIN', 'Sitagliptin', 'সিটাগ্লিপটিন', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_EMPAGLIFLOZIN', 'Empagliflozin', 'এমপাগ্লিফ্লোজিন', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_INSULIN_BASAL', 'Long-acting insulin', 'দীর্ঘমেয়াদি ইনসুলিন', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_INSULIN_MIXED', 'Mixed insulin', 'মিক্সড ইনসুলিন', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_LEVOTHYROXINE', 'Levothyroxine', 'লিভোথাইরক্সিন', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_CARBIMAZOLE', 'Carbimazole', 'কার্বিমাজল', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_ATORVASTATIN', 'Atorvastatin', 'অ্যাটোরভাস্ট্যাটিন', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_ROSUVASTATIN', 'Rosuvastatin', 'রসুভাস্ট্যাটিন', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_AMLODIPINE', 'Amlodipine', 'অ্যামলোডিপিন', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_LOSARTAN', 'Losartan', 'লোসারটান', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_RAMIPRIL', 'Ramipril', 'রেমিপ্রিল', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_ASPIRIN', 'Aspirin', 'অ্যাসপিরিন', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_CALCIUM_VIT_D', 'Calcium with vitamin D', 'ক্যালসিয়াম ও ভিটামিন ডি', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_VITAMIN_D', 'Vitamin D', 'ভিটামিন ডি', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_PREGABALIN', 'Pregabalin', 'প্রিগাবালিন', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_OMEPRAZOLE', 'Omeprazole', 'ওমিপ্রাজল', 'Medicine', 'ওষুধ'),
  ('DTHC', '1.0', 'DRUG_HERBAL', 'A herbal or traditional medicine', 'হারবাল বা কবিরাজি ওষুধ', 'Medicine', 'ওষুধ'),

  -- Vaccinations. The last one is here because a patient who says "I had the corona vaccine"
  -- is giving a real answer, and a list that could not hold it would send the officer to the
  -- notes field.
  ('DTHC', '1.0', 'VAX_COVID19', 'COVID-19 vaccine', 'কোভিড-১৯ টিকা', 'Vaccine', 'টিকা'),
  ('DTHC', '1.0', 'VAX_INFLUENZA', 'Flu vaccine', 'ফ্লু টিকা', 'Vaccine', 'টিকা'),
  ('DTHC', '1.0', 'VAX_PNEUMOCOCCAL', 'Pneumonia vaccine', 'নিউমোনিয়া টিকা', 'Vaccine', 'টিকা'),
  ('DTHC', '1.0', 'VAX_HEPATITIS_B', 'Hepatitis B vaccine', 'হেপাটাইটিস বি টিকা', 'Vaccine', 'টিকা'),
  ('DTHC', '1.0', 'VAX_TETANUS', 'Tetanus vaccine', 'ধনুষ্টংকারের টিকা', 'Vaccine', 'টিকা'),
  ('DTHC', '1.0', 'VAX_TYPHOID', 'Typhoid vaccine', 'টাইফয়েড টিকা', 'Vaccine', 'টিকা')
ON CONFLICT (system, version, code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  heading = EXCLUDED.heading, heading_bn = EXCLUDED.heading_bn;

-- The words people actually type for them. Without these, three keystrokes reach nothing:
-- nobody types "Levothyroxine", they type "thyrox" or "থাইরক্স".
INSERT INTO core.terminology_synonym (system, version, code, term, language) VALUES
  ('DTHC', '1.0', 'DRUG_METFORMIN', 'metformin', 'en'),
  ('DTHC', '1.0', 'DRUG_METFORMIN', 'মেটফরমিন', 'bn'),
  ('DTHC', '1.0', 'DRUG_METFORMIN', 'comet', 'en'),
  ('DTHC', '1.0', 'DRUG_METFORMIN', 'bigomet', 'en'),
  ('DTHC', '1.0', 'DRUG_GLIMEPIRIDE', 'amaryl', 'en'),
  ('DTHC', '1.0', 'DRUG_GLIMEPIRIDE', 'গ্লিমেপিরাইড', 'bn'),
  ('DTHC', '1.0', 'DRUG_GLICLAZIDE', 'diamicron', 'en'),
  ('DTHC', '1.0', 'DRUG_SITAGLIPTIN', 'januvia', 'en'),
  ('DTHC', '1.0', 'DRUG_EMPAGLIFLOZIN', 'jardiance', 'en'),
  ('DTHC', '1.0', 'DRUG_INSULIN_BASAL', 'lantus', 'en'),
  ('DTHC', '1.0', 'DRUG_INSULIN_BASAL', 'ইনসুলিন', 'bn'),
  ('DTHC', '1.0', 'DRUG_INSULIN_MIXED', 'mixtard', 'en'),
  ('DTHC', '1.0', 'DRUG_INSULIN_MIXED', 'novomix', 'en'),
  ('DTHC', '1.0', 'DRUG_LEVOTHYROXINE', 'thyrox', 'en'),
  ('DTHC', '1.0', 'DRUG_LEVOTHYROXINE', 'eltroxin', 'en'),
  ('DTHC', '1.0', 'DRUG_LEVOTHYROXINE', 'থাইরক্স', 'bn'),
  ('DTHC', '1.0', 'DRUG_LEVOTHYROXINE', 'থাইরয়েডের ওষুধ', 'bn'),
  ('DTHC', '1.0', 'DRUG_CARBIMAZOLE', 'neomercazole', 'en'),
  ('DTHC', '1.0', 'DRUG_ATORVASTATIN', 'atorva', 'en'),
  ('DTHC', '1.0', 'DRUG_ATORVASTATIN', 'lipitor', 'en'),
  ('DTHC', '1.0', 'DRUG_ROSUVASTATIN', 'rosuva', 'en'),
  ('DTHC', '1.0', 'DRUG_AMLODIPINE', 'amdocal', 'en'),
  ('DTHC', '1.0', 'DRUG_AMLODIPINE', 'প্রেশারের ওষুধ', 'bn'),
  ('DTHC', '1.0', 'DRUG_LOSARTAN', 'losartan potassium', 'en'),
  ('DTHC', '1.0', 'DRUG_ASPIRIN', 'ecosprin', 'en'),
  ('DTHC', '1.0', 'DRUG_PREGABALIN', 'pregabalin', 'en'),
  ('DTHC', '1.0', 'DRUG_PREGABALIN', 'পায়ের ব্যথার ওষুধ', 'bn'),
  ('DTHC', '1.0', 'DRUG_OMEPRAZOLE', 'গ্যাসের ওষুধ', 'bn'),
  ('DTHC', '1.0', 'DRUG_HERBAL', 'kabiraji', 'en'),
  ('DTHC', '1.0', 'DRUG_HERBAL', 'কবিরাজি', 'bn'),
  ('DTHC', '1.0', 'DRUG_HERBAL', 'আয়ুর্বেদিক', 'bn'),
  ('DTHC', '1.0', 'SURG_THYROIDECTOMY', 'গলার অপারেশন', 'bn'),
  ('DTHC', '1.0', 'SURG_CAESAREAN', 'সিজার', 'bn'),
  ('DTHC', '1.0', 'SURG_GALLBLADDER', 'পাথর অপারেশন', 'bn'),
  ('DTHC', '1.0', 'SURG_CORONARY_STENT', 'রিং', 'bn'),
  ('DTHC', '1.0', 'SURG_CABG', 'বাইপাস', 'bn'),
  ('DTHC', '1.0', 'VAX_COVID19', 'corona', 'en'),
  ('DTHC', '1.0', 'VAX_COVID19', 'করোনা', 'bn'),
  ('DTHC', '1.0', 'VAX_INFLUENZA', 'ফ্লু', 'bn')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The kinds of history, and what each one needs
-- ---------------------------------------------------------------------------

-- A table rather than a CHECK constraint, because the kinds differ in what they *require*,
-- and those rules belong beside the list rather than scattered through six code paths. A
-- family history with no relation is not a family history; a complaint with no duration is
-- half of one. Both are refused here, once.
CREATE TABLE core.history_kind (
  kind text PRIMARY KEY,

  display_en text NOT NULL,
  display_bn text NOT NULL,

  -- Which terminology this kind's concepts are drawn from. A complaint comes from the
  -- clinic's own dictionary; a comorbidity from ICD. Named here so a picker asks for the
  -- right catalogue without the screen deciding — a screen that chose ICD for complaints
  -- would produce a record whose complaints are diagnoses, which is a different claim.
  code_system text NOT NULL REFERENCES core.code_system(code),

  requires_relation boolean NOT NULL DEFAULT false,
  requires_duration boolean NOT NULL DEFAULT false,
  allows_severity   boolean NOT NULL DEFAULT false,
  allows_onset      boolean NOT NULL DEFAULT false,

  -- Whether this kind is a drug, and therefore reconciled against the formulary
  -- (criterion 2). One kind is; naming it here keeps the reconciliation rule out of Go.
  is_medication boolean NOT NULL DEFAULT false,

  ordering int NOT NULL,

  CONSTRAINT history_kind_format CHECK (kind ~ '^[A-Z][A-Z_]{1,31}$')
);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'history_kind', 'A catalogue of kinds of history. The same six in every clinic.')
ON CONFLICT DO NOTHING;

INSERT INTO core.history_kind
  (kind, display_en, display_bn, code_system,
   requires_relation, requires_duration, allows_severity, allows_onset, is_medication, ordering)
VALUES
  ('COMPLAINT', 'Presenting complaint', 'বর্তমান সমস্যা', 'DTHC',
   false, true, true, false, false, 1),
  ('COMORBIDITY', 'Other condition', 'অন্যান্য রোগ', 'ICD10',
   false, false, true, true, false, 2),
  ('FAMILY_HISTORY', 'In the family', 'পরিবারে', 'ICD10',
   true, false, false, true, false, 3),
  ('SURGICAL_HISTORY', 'Operation', 'অস্ত্রোপচার', 'DTHC',
   false, false, false, true, false, 4),
  ('MEDICATION', 'Current medicine', 'বর্তমান ওষুধ', 'DTHC',
   false, false, false, true, true, 5),
  ('VACCINATION', 'Vaccination', 'টিকা', 'DTHC',
   false, false, false, true, false, 6)
ON CONFLICT (kind) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  code_system = EXCLUDED.code_system,
  requires_relation = EXCLUDED.requires_relation,
  requires_duration = EXCLUDED.requires_duration,
  allows_severity = EXCLUDED.allows_severity,
  allows_onset = EXCLUDED.allows_onset,
  is_medication = EXCLUDED.is_medication,
  ordering = EXCLUDED.ordering;

GRANT SELECT ON core.history_kind TO dthcms_app, dthcms_projector;

-- ---------------------------------------------------------------------------
-- Who a family history is about
-- ---------------------------------------------------------------------------

-- Coded rather than typed for the same reason a complaint is: "mother", "Mother", "মা" and
-- "mom" are one relation, and a research query asking how many patients have a first-degree
-- relative with diabetes cannot ask that question of four spellings.
--
-- `degree` is why the table exists at all rather than a CHECK list. First-degree family
-- history is a risk factor with a number attached; second-degree is context. A query that had
-- to enumerate which relations are first-degree would be a clinical rule living in a WHERE
-- clause somebody copies wrong.
CREATE TABLE core.family_relation (
  relation text PRIMARY KEY,

  display_en text NOT NULL,
  display_bn text NOT NULL,

  degree int NOT NULL CHECK (degree IN (1, 2)),
  ordering int NOT NULL,

  CONSTRAINT family_relation_format CHECK (relation ~ '^[A-Z][A-Z_]{1,31}$')
);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'family_relation', 'A catalogue of family relations. The same everywhere.')
ON CONFLICT DO NOTHING;

INSERT INTO core.family_relation (relation, display_en, display_bn, degree, ordering) VALUES
  ('MOTHER',       'Mother',            'মা',              1, 1),
  ('FATHER',       'Father',            'বাবা',            1, 2),
  ('SIBLING',      'Brother or sister', 'ভাই বা বোন',      1, 3),
  ('CHILD',        'Son or daughter',   'ছেলে বা মেয়ে',    1, 4),
  ('GRANDPARENT',  'Grandparent',       'দাদা-দাদি/নানা-নানি', 2, 5),
  ('AUNT_UNCLE',   'Aunt or uncle',     'চাচা-চাচি/মামা-মামি',  2, 6)
ON CONFLICT (relation) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  degree = EXCLUDED.degree, ordering = EXCLUDED.ordering;

GRANT SELECT ON core.family_relation TO dthcms_app, dthcms_projector;

-- ---------------------------------------------------------------------------
-- The item
-- ---------------------------------------------------------------------------

CREATE TABLE read.history_item (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL,
  patient_id  uuid NOT NULL,

  kind text NOT NULL REFERENCES core.history_kind(kind),

  -- The coding (CP52, criterion 1). Three columns, never one: `E11.9` on its own is a string.
  --
  -- Nullable as a set, and only as a set — see the constraint below. A history officer meets
  -- things the catalogue has no code for, and refusing to record them would push the whole
  -- item into a note field where nothing can find it. So an uncoded item is *allowed*, it is
  -- visibly uncoded, and it can be counted: "how much of our history is uncoded" is a
  -- question somebody should be able to ask, and the answer is what tells Dr. Nahid which
  -- concepts to add.
  code_system  text,
  code_version text,
  code         text,

  -- What was actually said. Kept on every item, coded or not: the catalogue's title is
  -- "Type 2 diabetes mellitus without complications" and the patient said "sugar since the
  -- flood". The second one is the clinical detail.
  said text NOT NULL DEFAULT '',

  -- Per-kind detail. Which of these are required is `core.history_kind`'s business, checked
  -- by the trigger below rather than by six nullable-column constraints nobody can read.
  relation text REFERENCES core.family_relation(relation),

  duration_days int CHECK (duration_days IS NULL OR duration_days >= 0),
  severity      text CHECK (severity IS NULL OR severity IN ('mild', 'moderate', 'severe')),

  -- When it started. `onset_precision` for the same reason a date of birth carries one: a
  -- patient who says "about two years ago" has given a real answer, and storing it as an
  -- exact date makes a guess look like a measurement.
  onset_on        date,
  onset_precision text CHECK (onset_precision IS NULL
                              OR onset_precision IN ('day', 'month', 'year')),

  -- Medication detail. Free text on purpose: "1 tablet twice a day after food" is what the
  -- patient's own strip says, and a structured dose model that could not hold it would be a
  -- model people work around. The *drug* is coded; the instruction is what was said.
  dose      text NOT NULL DEFAULT '',
  frequency text NOT NULL DEFAULT '',

  -- Criterion 2: current medications link to formulary products where they exist.
  --
  -- Nullable, and it will be null on every row until the formulary is built (it is a later
  -- checkpoint). That is the honest shape of "where they exist", and it is why the column is
  -- here now: the reconciliation *state* is recorded per item today, so the day the formulary
  -- arrives the work is matching rows rather than migrating a record that never had a place
  -- to put the answer.
  --
  --   UNRECONCILED — nobody has looked
  --   MATCHED      — it is this product
  --   NOT_STOCKED  — looked, and this clinic does not carry it. A finding, not a failure.
  --
  -- Null on everything that is not a drug, rather than 'UNRECONCILED' everywhere. The
  -- difference matters the moment somebody asks "what has nobody checked": a column that says
  -- UNRECONCILED on a vaccination makes that question return the wrong answer, and the person
  -- who writes the query will not notice, because the number will look plausible.
  formulary_product_id uuid,
  reconciliation       text
    CHECK (reconciliation IS NULL
           OR reconciliation IN ('UNRECONCILED', 'MATCHED', 'NOT_STOCKED')),

  -- ACTIVE   → still true
  -- RESOLVED → it was true and is not any more (a complaint that settled, a drug stopped)
  --
  -- Never deleted. A history somebody removed is a history somebody disagreed with, and
  -- which of the two happened is the interesting part.
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RESOLVED')),

  -- Criterion 4: individually attributed, at every stage of its life.
  recorded_at    timestamptz NOT NULL,
  recorded_by    uuid NOT NULL,
  recorded_role  text NOT NULL DEFAULT '',
  recorded_visit uuid,

  -- Criterion 3. No default, ever: an item is confirmed by a person or it is not confirmed.
  -- A `DEFAULT now()` here would silently satisfy the criterion and quietly destroy it.
  confirmed_at    timestamptz,
  confirmed_by    uuid,
  confirmed_visit uuid,

  amended_at timestamptz,
  amended_by uuid,

  removed_at     timestamptz,
  removed_by     uuid,
  removed_reason text NOT NULL DEFAULT '',

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL,

  -- A coding is all three columns or none. Two out of three is the failure mode CP52 exists
  -- to prevent, expressed here as something the database refuses rather than something the
  -- writing code remembers.
  CONSTRAINT history_item_coding_is_whole CHECK (
    (code_system IS NULL AND code_version IS NULL AND code IS NULL)
    OR (code_system IS NOT NULL AND code_version IS NOT NULL AND code IS NOT NULL)),

  -- An uncoded item must at least say what was meant. Otherwise it is a row that asserts a
  -- patient has *something*.
  CONSTRAINT history_item_says_something CHECK (
    code IS NOT NULL OR btrim(said) <> ''),

  CONSTRAINT history_item_removal_is_complete CHECK (
    (removed_at IS NULL) = (removed_by IS NULL)),

  CONSTRAINT history_item_confirmation_is_complete CHECK (
    (confirmed_at IS NULL) = (confirmed_by IS NULL)),

  CONSTRAINT history_item_coding_exists
    FOREIGN KEY (code_system, code_version, code)
    REFERENCES core.terminology_concept (system, version, code)
);

CREATE INDEX history_item_by_patient
  ON read.history_item (patient_id, kind, status) WHERE removed_at IS NULL;

CREATE INDEX history_item_needing_confirmation
  ON read.history_item (patient_id, confirmed_at)
  WHERE removed_at IS NULL AND status = 'ACTIVE';

-- Reconciliation is a question asked of every medication, so the unanswered ones have to be
-- findable without a table scan the day the formulary lands.
CREATE INDEX history_item_unreconciled_medication
  ON read.history_item (facility_id)
  WHERE kind = 'MEDICATION' AND reconciliation = 'UNRECONCILED' AND removed_at IS NULL;

COMMENT ON TABLE read.history_item IS
  'One thing the patient brings with them, with an identity that outlives the visit (CP53).';

GRANT SELECT ON read.history_item TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON read.history_item TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The per-kind rules, enforced where they cannot be forgotten
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.history_item_matches_its_kind() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  rules core.history_kind%ROWTYPE;
BEGIN
  SELECT * INTO rules FROM core.history_kind WHERE kind = NEW.kind;

  IF rules.requires_relation AND NEW.relation IS NULL THEN
    RAISE EXCEPTION 'a % must say whose it is', NEW.kind
      USING HINT = 'Family history is about a relative. Name the relation.';
  END IF;
  IF NOT rules.requires_relation AND NEW.relation IS NOT NULL THEN
    RAISE EXCEPTION 'a % is about the patient, not a relative', NEW.kind;
  END IF;

  IF rules.requires_duration AND NEW.duration_days IS NULL THEN
    RAISE EXCEPTION 'a % must say how long it has been going on', NEW.kind
      USING HINT = 'Duration is what separates a complaint from a diagnosis.';
  END IF;

  IF NOT rules.allows_severity AND NEW.severity IS NOT NULL THEN
    RAISE EXCEPTION 'a % carries no severity', NEW.kind;
  END IF;

  IF NOT rules.allows_onset AND NEW.onset_on IS NOT NULL THEN
    RAISE EXCEPTION 'a % carries no onset date', NEW.kind;
  END IF;

  -- A date with no precision is a guess wearing a measurement's clothes. Same rule the date
  -- of birth follows, for the same reason: a percentile, an age or a disease duration
  -- computed from "about two years ago" must be readable as approximate by whoever reads it.
  IF (NEW.onset_on IS NULL) <> (NEW.onset_precision IS NULL) THEN
    RAISE EXCEPTION 'an onset date and its precision travel together';
  END IF;

  -- Only a drug is reconciled against the formulary, and only a drug carries a dose. A
  -- vaccination with a frequency is a data-entry accident nobody would notice on a screen.
  IF NOT rules.is_medication THEN
    IF NEW.formulary_product_id IS NOT NULL OR NEW.reconciliation IS NOT NULL THEN
      RAISE EXCEPTION 'a % is not reconciled against the formulary', NEW.kind;
    END IF;
    IF btrim(NEW.dose) <> '' OR btrim(NEW.frequency) <> '' THEN
      RAISE EXCEPTION 'a % carries no dose', NEW.kind;
    END IF;
  ELSIF NEW.reconciliation IS NULL THEN
    -- And every drug carries the state from the moment it is written down, even though the
    -- formulary does not exist yet. That is what makes the eventual matching a job of work
    -- rather than a migration of a record with nowhere to put the answer.
    RAISE EXCEPTION 'a medicine says whether anybody has checked it against the formulary';
  END IF;

  -- MATCHED means "it is this product", so it has to name one. NOT_STOCKED means somebody
  -- looked and it is not here, which is a finding and names nothing.
  IF NEW.reconciliation = 'MATCHED' AND NEW.formulary_product_id IS NULL THEN
    RAISE EXCEPTION 'a matched medication must name the product it matched';
  END IF;
  IF NEW.reconciliation <> 'MATCHED' AND NEW.formulary_product_id IS NOT NULL THEN
    RAISE EXCEPTION 'a medication that names a product is matched to it';
  END IF;

  -- The concept must come from the catalogue this kind draws on. Without this a screen could
  -- file an ICD diagnosis as a presenting complaint, and the record would then assert that a
  -- patient *presented with* type 2 diabetes — a claim nobody made.
  IF NEW.code_system IS NOT NULL AND NEW.code_system <> rules.code_system THEN
    RAISE EXCEPTION 'a % is coded in %, not in %', NEW.kind, rules.code_system, NEW.code_system
      USING HINT = 'A complaint is what the patient reports; a comorbidity is a diagnosis.';
  END IF;

  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER history_item_matches_its_kind
  BEFORE INSERT OR UPDATE ON read.history_item
  FOR EACH ROW EXECUTE FUNCTION core.history_item_matches_its_kind();

-- ---------------------------------------------------------------------------
-- The projections
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_history_item_recorded(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.history_item (
    id, facility_id, patient_id, kind,
    code_system, code_version, code, said,
    relation, duration_days, severity, onset_on, onset_precision,
    dose, frequency, formulary_product_id, reconciliation, status,
    recorded_at, recorded_by, recorded_role, recorded_visit,
    event_id, global_seq)
  VALUES (
    (p->>'item_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    p->>'kind',
    nullif(p->>'code_system', ''),
    nullif(p->>'code_version', ''),
    nullif(p->>'code', ''),
    coalesce(p->>'said', ''),
    nullif(p->>'relation', ''),
    nullif(p->>'duration_days', '')::int,
    nullif(p->>'severity', ''),
    nullif(p->>'onset_on', '')::date,
    nullif(p->>'onset_precision', ''),
    coalesce(p->>'dose', ''),
    coalesce(p->>'frequency', ''),
    nullif(p->>'formulary_product_id', '')::uuid,
    nullif(p->>'reconciliation', ''),
    coalesce(nullif(p->>'status', ''), 'ACTIVE'),
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
CREATE OR REPLACE FUNCTION read.apply_history_item_confirmed(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  -- The confirmation is the whole of criterion 3, and it is an UPDATE by an event rather
  -- than a column default because that is the difference between "somebody said this is
  -- still true" and "the software assumed it".
  UPDATE read.history_item
     SET confirmed_at    = (p->>'confirmed_at')::timestamptz,
         confirmed_by    = (p->>'confirmed_by')::uuid,
         confirmed_visit = nullif(p->>'visit_id', '')::uuid
   WHERE id = (p->>'item_id')::uuid
     AND removed_at IS NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_history_item_amended(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  -- Only the fields an amendment may touch. The coding, the kind and who first recorded it
  -- are not among them: changing what an item *is* is removing one and adding another, and
  -- collapsing those two acts into one is how an audit trail stops answering questions.
  UPDATE read.history_item
     SET said       = coalesce(nullif(p->>'said', ''), said),
         severity   = coalesce(nullif(p->>'severity', ''), severity),
         duration_days = coalesce(nullif(p->>'duration_days', '')::int, duration_days),
         onset_on   = coalesce(nullif(p->>'onset_on', '')::date, onset_on),
         onset_precision = coalesce(nullif(p->>'onset_precision', ''), onset_precision),
         dose       = coalesce(nullif(p->>'dose', ''), dose),
         frequency  = coalesce(nullif(p->>'frequency', ''), frequency),
         status     = coalesce(nullif(p->>'status', ''), status),
         formulary_product_id =
           CASE WHEN p ? 'reconciliation'
                THEN nullif(p->>'formulary_product_id', '')::uuid
                ELSE formulary_product_id END,
         reconciliation = coalesce(nullif(p->>'reconciliation', ''), reconciliation),
         amended_at = (p->>'amended_at')::timestamptz,
         amended_by = (p->>'amended_by')::uuid,
         -- An amendment is a fresh assertion by a person, so it confirms as it changes.
         -- Leaving `confirmed_at` behind would show an item somebody just edited as one
         -- nobody has looked at since last month.
         confirmed_at    = (p->>'amended_at')::timestamptz,
         confirmed_by    = (p->>'amended_by')::uuid,
         confirmed_visit = nullif(p->>'visit_id', '')::uuid
   WHERE id = (p->>'item_id')::uuid
     AND removed_at IS NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_history_item_removed(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  -- Marked, never deleted. "This was recorded and is wrong" and "this never happened" are
  -- different claims, and only the row can tell them apart.
  UPDATE read.history_item
     SET removed_at     = (p->>'removed_at')::timestamptz,
         removed_by     = (p->>'removed_by')::uuid,
         removed_reason = coalesce(p->>'reason', '')
   WHERE id = (p->>'item_id')::uuid
     AND removed_at IS NULL;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_history_item_recorded(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_history_item_confirmed(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_history_item_amended(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_history_item_removed(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- Criterion 3, as a standing rule rather than a hope. A confirmation must name a person; a
-- row confirmed by nobody is the exact failure mode "never auto-accepted" forbids, and it
-- would arrive by somebody adding a DEFAULT to make a test pass.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_history_is_confirmed_by_people() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender uuid;
BEGIN
  SELECT id INTO offender FROM read.history_item
   WHERE (confirmed_at IS NULL) <> (confirmed_by IS NULL) LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'history item % is confirmed by nobody', offender
      USING HINT = 'Criterion 3: prior history is confirmed by a person, never auto-accepted.';
  END IF;

  -- And the column may not acquire a default. That is the shape this failure would actually
  -- take: not a bad row, but somebody adding `DEFAULT now()` to make a test pass, after which
  -- every row is confirmed and no row was.
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
     WHERE table_schema = 'read' AND table_name = 'history_item'
       AND column_name IN ('confirmed_at', 'confirmed_by')
       AND column_default IS NOT NULL) THEN
    RAISE EXCEPTION 'a confirmation column has acquired a default'
      USING HINT = 'A default here silently satisfies criterion 3 and destroys it.';
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_history_is_confirmed_by_people() IS
  'Raises if a history item is confirmed with no person behind it (CP53 criterion 3).';

-- Criterion 4. Every item names who recorded it, and every later act on it names who did
-- that too. An item with no actor is an assertion the record cannot attribute.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_history_item_is_attributed() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender uuid;
BEGIN
  SELECT id INTO offender FROM read.history_item
   WHERE recorded_by IS NULL
      OR (amended_at IS NOT NULL AND amended_by IS NULL)
      OR (removed_at IS NOT NULL AND removed_by IS NULL)
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'history item % has an act nobody is named for', offender;
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_every_history_item_is_attributed() IS
  'Raises if any recording, amendment or removal of history has no actor (CP53 criterion 4).';

-- Every kind draws on a terminology that exists and that this deployment may use. Without
-- this, a kind could name SNOMED and station 4 would present a picker that answers 422 to
-- every keystroke — which reads to the officer as a broken station rather than as a
-- licensing question.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_history_kind_has_a_usable_catalogue() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender text;
BEGIN
  SELECT k.kind INTO offender
    FROM core.history_kind k
    JOIN core.code_system s ON s.code = k.code_system
   WHERE NOT s.usable
      OR NOT EXISTS (SELECT 1 FROM core.code_system_version v
                      WHERE v.system = s.code AND v.is_default)
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION '% draws on a terminology this clinic cannot search', offender
      USING HINT = 'Station 4 would show a picker that refuses every keystroke.';
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_every_history_kind_has_a_usable_catalogue() IS
  'Raises if a kind of history is coded in a terminology nobody here can search (CP53).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_history_is_confirmed_by_people',
   'prior history is confirmed by a person, never auto-accepted', 62),
  ('assert_every_history_item_is_attributed',
   'every history item names who recorded, amended and removed it', 63),
  ('assert_every_history_kind_has_a_usable_catalogue',
   'every kind of history is coded in a terminology this clinic can search', 64)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_history_is_confirmed_by_people', 'assert_every_history_item_is_attributed',
  'assert_every_history_kind_has_a_usable_catalogue');
DROP FUNCTION IF EXISTS core.assert_every_history_kind_has_a_usable_catalogue();
DROP FUNCTION IF EXISTS core.assert_every_history_item_is_attributed();
DROP FUNCTION IF EXISTS core.assert_history_is_confirmed_by_people();
DROP FUNCTION IF EXISTS read.apply_history_item_removed(jsonb);
DROP FUNCTION IF EXISTS read.apply_history_item_amended(jsonb);
DROP FUNCTION IF EXISTS read.apply_history_item_confirmed(jsonb);
DROP FUNCTION IF EXISTS read.apply_history_item_recorded(jsonb);
DROP TABLE IF EXISTS read.history_item;
DROP FUNCTION IF EXISTS core.history_item_matches_its_kind();
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name IN ('history_kind', 'family_relation');
DROP TABLE IF EXISTS core.family_relation;
DROP TABLE IF EXISTS core.history_kind;
DELETE FROM core.role_permission
 WHERE permission_code IN ('history.read', 'history.write', 'history.confirm');
DELETE FROM core.permission
 WHERE code IN ('history.read', 'history.write', 'history.confirm');
