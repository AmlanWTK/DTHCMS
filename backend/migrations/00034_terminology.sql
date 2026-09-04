-- Coded diagnoses and complaints, without a licensing blocker (CP52, §8, §9.1, D-24).
--
-- # What D-24 leaves open, and what it does not
--
-- D-24 is about **SNOMED CT**: its use requires an Affiliate licence, and whether Bangladesh
-- confers free use has to be verified with SNOMED International before any SNOMED content is
-- embedded. Nothing here is SNOMED-derived, and the mapping table below exists precisely so
-- that it can be layered on the day the answer comes back.
--
-- What is *not* open is ICD. WHO publishes ICD-10 and ICD-11 under far more permissive terms,
-- and the plan's own recommendation (option A) is to use ICD as the backbone with an internal
-- concept dictionary for complaints. That is what this builds.
--
-- The one thing still genuinely undecided is **ICD-10 or ICD-11**, and this schema does not
-- care: a code system is a row, a version is a row, and a coding stores both. That is not
-- future-proofing for its own sake — it is criterion 2, "every coding stores its system and
-- version", and it is what makes a diagnosis recorded in 2026 still interpretable in 2032 when
-- the clinic has moved to ICD-11 and the code means something slightly different.
--
-- # Why the ranking is one statement
--
-- Criterion 1 asks that the twenty most common DTHC diagnoses each be findable within three
-- keystrokes, and criterion 4 puts p95 under 150 ms. Both are properties of the *ranking*, and
-- a ranking split between a SQL query and a Go sort is a ranking nobody can reason about or
-- reproduce. So it is one statement, in `internal/terminology/queries`, and the tier it
-- matched is returned with every row — because "why is that third" is the question every
-- search gets asked, and a ranking nobody can inspect is a ranking nobody can tune.
--
-- The predicate it asks twice — once for the favourites, once for everything else — is a
-- function here, so it gets fixed once.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Who may read the catalogue
-- ---------------------------------------------------------------------------

-- Reading the classification is not reading a patient. There is no person in these tables:
-- they hold the WHO's list of diseases and the clinic's own list of complaints, and the
-- alternative — guarding the picker with `diagnosis.read` — would mean a receptionist who is
-- allowed to type a complaint needs the permission to read somebody's diagnoses.
--
-- So it is its own permission, it is not sensitive, and it is granted to the roles that fill
-- in a coded field or read one back: the history officer taking a complaint (CP53), the
-- clinical assistant and the two grades of doctor, and the QA officer who reviews what they
-- wrote. It is deliberately not granted to the board display, the research account or the
-- field worker — the first has no fields to fill in, the second reads the de-identified
-- schema where codes arrive already resolved, and the third works offline from a form that
-- carries no catalogue.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('terminology.read', 'terminology', 'read', '',
   'Search the coded diagnosis and complaint catalogue', false)
ON CONFLICT (code) DO UPDATE SET
  resource = EXCLUDED.resource, action = EXCLUDED.action, scope = EXCLUDED.scope,
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'terminology.read' FROM core.role r
 WHERE r.code IN ('HISTORY', 'CLINICAL_ASSISTANT', 'JUNIOR_DOCTOR', 'PHYSICIAN', 'QA')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Code systems and their versions
-- ---------------------------------------------------------------------------

CREATE TABLE core.code_system (
  code text PRIMARY KEY,

  title_en text NOT NULL,
  title_bn text NOT NULL,

  -- The authority, and what may be done with its content. Stored rather than assumed: the
  -- next person to add a system will be tempted to add SNOMED, and this column is where they
  -- find out that they cannot yet.
  publisher    text NOT NULL,
  licence_note text NOT NULL DEFAULT '',

  -- False until the licence question is answered. A system nobody may use is still worth
  -- describing — that is how the map below can name it as a target before it holds any rows.
  usable boolean NOT NULL DEFAULT true,

  CONSTRAINT code_system_code_format CHECK (code ~ '^[A-Z][A-Z0-9_-]{1,31}$')
);

CREATE TABLE core.code_system_version (
  system  text NOT NULL REFERENCES core.code_system(code),
  version text NOT NULL,

  released_on date,
  -- Exactly one default per system: the version a new coding is recorded under. Older
  -- versions stay readable forever, which is the whole point of storing the version.
  is_default boolean NOT NULL DEFAULT false,

  PRIMARY KEY (system, version)
);

CREATE UNIQUE INDEX code_system_one_default
  ON core.code_system_version (system) WHERE is_default;

-- ---------------------------------------------------------------------------
-- The concepts
-- ---------------------------------------------------------------------------

CREATE TABLE core.terminology_concept (
  system  text NOT NULL,
  version text NOT NULL,
  code    text NOT NULL,

  display_en text NOT NULL,
  -- Bengali is not optional for the favourites and is allowed to be empty elsewhere. §5.1's
  -- bilingual requirement is about what a clinician and a patient read together, and a
  -- transliterated ICD title nobody uses would be worse than the English one they do.
  display_bn text NOT NULL DEFAULT '',

  -- The chapter or block a picker groups by. Free text because ICD-10 and ICD-11 disagree
  -- about what a chapter is, and a normalised hierarchy would be a second thing to migrate.
  --
  -- Called `heading` rather than `grouping`: GROUPING is a reserved word in PostgreSQL, and a
  -- column named after one is a column that works everywhere except inside a function's
  -- RETURNS TABLE, which is exactly where this one is needed.
  heading text NOT NULL DEFAULT '',

  -- The same heading in Bangla. A separate column rather than a lookup table because the set
  -- is small and closed, and a join to resolve nine strings is a join on every keystroke.
  --
  -- It is here because the first screenshot of the picker in Bangla showed a list of Bengali
  -- diagnoses filed under English chapter names. Half-bilingual is its own failure: it reads
  -- as an interface somebody translated the easy parts of.
  heading_bn text NOT NULL DEFAULT '',

  -- Dr. Nahid's own list, ranked. Criterion 1 is "findable within three keystrokes", and the
  -- honest way to reach it is not a cleverer search — it is knowing which twenty diagnoses
  -- this clinic actually makes.
  favourite_rank int,

  retired_at timestamptz,

  PRIMARY KEY (system, version, code),
  FOREIGN KEY (system, version) REFERENCES core.code_system_version(system, version),
  CONSTRAINT terminology_concept_has_display CHECK (btrim(display_en) <> ''),
  CONSTRAINT terminology_concept_favourite_rank CHECK (favourite_rank IS NULL OR favourite_rank > 0)
);

CREATE INDEX terminology_concept_by_display_trgm
  ON core.terminology_concept USING gin (display_en gin_trgm_ops);
CREATE INDEX terminology_concept_by_display_bn_trgm
  ON core.terminology_concept USING gin (display_bn gin_trgm_ops);
CREATE INDEX terminology_concept_by_code
  ON core.terminology_concept (system, version, code text_pattern_ops);
CREATE INDEX terminology_concept_favourites
  ON core.terminology_concept (system, version, favourite_rank) WHERE favourite_rank IS NOT NULL;

-- Synonyms are how three keystrokes reach a diagnosis whose official title nobody says out
-- loud. "Sugar" finds diabetes; "থাইরয়েড কম" finds hypothyroidism. Without them, criterion 1
-- is a promise about the WHO's vocabulary rather than about this clinic's.
CREATE TABLE core.terminology_synonym (
  system   text NOT NULL,
  version  text NOT NULL,
  code     text NOT NULL,
  term     text NOT NULL,
  language text NOT NULL CHECK (language IN ('en', 'bn')),

  PRIMARY KEY (system, version, code, term),
  FOREIGN KEY (system, version, code)
    REFERENCES core.terminology_concept(system, version, code) ON DELETE CASCADE,
  CONSTRAINT terminology_synonym_has_term CHECK (btrim(term) <> '')
);

CREATE INDEX terminology_synonym_trgm
  ON core.terminology_synonym USING gin (term gin_trgm_ops);

-- The map, empty and waiting. Its shape is the deliverable: when D-24 answers, SNOMED becomes
-- rows here rather than a migration that touches everything already coded.
CREATE TABLE core.terminology_map (
  from_system  text NOT NULL,
  from_version text NOT NULL,
  from_code    text NOT NULL,
  to_system    text NOT NULL REFERENCES core.code_system(code),
  to_version   text NOT NULL,
  to_code      text NOT NULL,

  -- The relationship, in the vocabulary every terminology mapping uses. A map that only said
  -- "these are related" would be a map nobody could safely compute with.
  equivalence text NOT NULL CHECK (
    equivalence IN ('equivalent', 'broader', 'narrower', 'related', 'unmatched')),

  PRIMARY KEY (from_system, from_version, from_code, to_system, to_version, to_code),
  FOREIGN KEY (from_system, from_version, from_code)
    REFERENCES core.terminology_concept(system, version, code) ON DELETE CASCADE,
  FOREIGN KEY (to_system, to_version) REFERENCES core.code_system_version(system, version)
);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'code_system', 'A terminology is the same in every clinic (CP52).'),
  ('core', 'code_system_version', 'A terminology version is the same in every clinic (CP52).'),
  ('core', 'terminology_concept', 'ICD is published by the WHO, not by a facility (CP52).'),
  ('core', 'terminology_synonym', 'Search synonyms are clinic-wide vocabulary, not per-facility settings (CP52).'),
  ('core', 'terminology_map', 'A map between two published terminologies is not a facility setting (CP52).')
ON CONFLICT (schema_name, table_name) DO NOTHING;

GRANT SELECT ON core.code_system, core.code_system_version,
                core.terminology_concept, core.terminology_synonym, core.terminology_map
  TO dthcms_app;
GRANT SELECT ON core.code_system, core.code_system_version,
                core.terminology_concept, core.terminology_synonym, core.terminology_map
  TO dthcms_projector;

COMMENT ON TABLE core.terminology_concept IS
  'Coded diagnoses and complaints, per system and version (CP52). A coding stores both.';
COMMENT ON TABLE core.terminology_map IS
  'Between two terminologies. Empty until D-24 answers whether SNOMED may be used here (CP52).';

-- ---------------------------------------------------------------------------
-- The systems
-- ---------------------------------------------------------------------------

INSERT INTO core.code_system (code, title_en, title_bn, publisher, licence_note, usable) VALUES
  ('ICD10', 'ICD-10', 'আইসিডি-১০', 'World Health Organization',
   'Published by WHO under permissive terms. The backbone until D-24 confirms ICD-10 or ICD-11.', true),
  ('ICD11', 'ICD-11', 'আইসিডি-১১', 'World Health Organization',
   'Published by WHO under permissive terms. Registered here so a migration to it is rows rather than schema.', true),
  ('DTHC', 'DTHC concept dictionary', 'ডিটিএইচসি ধারণা অভিধান', 'Diabetes, Thyroid & Hormone Clinic',
   'The clinic''s own vocabulary for chief complaints, where no published system says what a patient actually said.', true),
  ('SNOMED', 'SNOMED CT', 'স্নোমেড সিটি', 'SNOMED International',
   'NOT USABLE until D-24 answers. Use requires an Affiliate licence, and whether Bangladesh confers free use must be verified with SNOMED International before any SNOMED content is embedded. Registered here only so the map can name it.', false)
ON CONFLICT (code) DO UPDATE SET
  title_en = EXCLUDED.title_en, title_bn = EXCLUDED.title_bn,
  publisher = EXCLUDED.publisher, licence_note = EXCLUDED.licence_note, usable = EXCLUDED.usable;

INSERT INTO core.code_system_version (system, version, released_on, is_default) VALUES
  ('ICD10', '2019', DATE '2019-01-01', true),
  -- Registered, not loaded. ICD-11 is the other half of D-24's open question, and a row here
  -- with no content and no default is the honest shape of "we can move to it without a schema
  -- change": a caller who names it is told to name a version, rather than handed an empty
  -- list that reads as "there is no such diagnosis".
  ('ICD11', '2024-01', DATE '2024-01-01', false),
  ('DTHC', '1.0', DATE '2026-09-01', true)
  -- SNOMED deliberately has no version row at all. Inventing one called 'unlicensed' would put
  -- a fact in the version column that is not a version, and the default-version invariant only
  -- asks about systems that actually hold concepts.
ON CONFLICT (system, version) DO UPDATE SET
  released_on = EXCLUDED.released_on, is_default = EXCLUDED.is_default;

-- ---------------------------------------------------------------------------
-- The concepts (proposed — the favourites are Dr. Nahid's to rank)
-- ---------------------------------------------------------------------------
--
-- An endocrine-focused subset rather than the whole of ICD-10. Loading twelve thousand
-- codes to make twenty of them findable would be a search whose top result is a
-- coincidence — and the twenty that matter are a property of this clinic's caseload, not
-- of the WHO's classification. The rest of ICD arrives as a data load when it is needed,
-- against the same schema.
--
-- **The favourite ranks are a proposal.** Criterion 1 — the twenty most common DTHC
-- diagnoses in three keystrokes — is a claim about Dr. Nahid's own list, and until he
-- ranks it these are drawn from the clinic's stated case-mix.

INSERT INTO core.terminology_concept
  (system, version, code, display_en, display_bn, heading, favourite_rank) VALUES
  ('ICD10', '2019', 'E11.9', 'Type 2 diabetes mellitus without complications', 'টাইপ ২ ডায়াবেটিস, জটিলতাবিহীন', 'Diabetes', 1),
  ('ICD10', '2019', 'E11.7', 'Type 2 diabetes mellitus with multiple complications', 'টাইপ ২ ডায়াবেটিস, একাধিক জটিলতাসহ', 'Diabetes', 5),
  ('ICD10', '2019', 'E11.2', 'Type 2 diabetes mellitus with renal complications', 'টাইপ ২ ডায়াবেটিস, কিডনির জটিলতাসহ', 'Diabetes', 7),
  ('ICD10', '2019', 'E11.3', 'Type 2 diabetes mellitus with ophthalmic complications', 'টাইপ ২ ডায়াবেটিস, চোখের জটিলতাসহ', 'Diabetes', 8),
  ('ICD10', '2019', 'E11.4', 'Type 2 diabetes mellitus with neurological complications', 'টাইপ ২ ডায়াবেটিস, স্নায়ুর জটিলতাসহ', 'Diabetes', 6),
  ('ICD10', '2019', 'E11.5', 'Type 2 diabetes mellitus with peripheral circulatory complications', 'টাইপ ২ ডায়াবেটিস, রক্তসঞ্চালনের জটিলতাসহ', 'Diabetes', 13),
  ('ICD10', '2019', 'E11.0', 'Type 2 diabetes mellitus with hyperosmolarity', 'টাইপ ২ ডায়াবেটিস, হাইপারঅসমোলার অবস্থা', 'Diabetes', NULL),
  ('ICD10', '2019', 'E10.9', 'Type 1 diabetes mellitus without complications', 'টাইপ ১ ডায়াবেটিস, জটিলতাবিহীন', 'Diabetes', 4),
  ('ICD10', '2019', 'E10.1', 'Type 1 diabetes mellitus with ketoacidosis', 'টাইপ ১ ডায়াবেটিস, কিটোঅ্যাসিডোসিসসহ', 'Diabetes', NULL),
  ('ICD10', '2019', 'E13.9', 'Other specified diabetes mellitus without complications', 'অন্যান্য নির্দিষ্ট ডায়াবেটিস, জটিলতাবিহীন', 'Diabetes', NULL),
  ('ICD10', '2019', 'E14.9', 'Unspecified diabetes mellitus without complications', 'অনির্দিষ্ট ডায়াবেটিস, জটিলতাবিহীন', 'Diabetes', NULL),
  ('ICD10', '2019', 'O24.4', 'Diabetes mellitus arising in pregnancy', 'গর্ভকালীন ডায়াবেটিস', 'Diabetes', 9),
  ('ICD10', '2019', 'R73.0', 'Abnormal glucose tolerance test', 'অস্বাভাবিক গ্লুকোজ সহনশীলতা পরীক্ষা', 'Diabetes', 3),
  ('ICD10', '2019', 'E16.2', 'Hypoglycaemia, unspecified', 'হাইপোগ্লাইসেমিয়া, অনির্দিষ্ট', 'Diabetes', NULL),
  ('ICD10', '2019', 'G63.2', 'Diabetic polyneuropathy', 'ডায়াবেটিক পলিনিউরোপ্যাথি', 'Diabetes', 14),
  ('ICD10', '2019', 'H36.0', 'Diabetic retinopathy', 'ডায়াবেটিক রেটিনোপ্যাথি', 'Diabetes', 15),
  ('ICD10', '2019', 'L97', 'Ulcer of lower limb, not elsewhere classified', 'পায়ের ঘা, অন্যত্র শ্রেণিবদ্ধ নয়', 'Diabetes', NULL),
  ('ICD10', '2019', 'E03.9', 'Hypothyroidism, unspecified', 'হাইপোথাইরয়েডিজম, অনির্দিষ্ট', 'Thyroid', 2),
  ('ICD10', '2019', 'E03.1', 'Congenital hypothyroidism without goitre', 'জন্মগত হাইপোথাইরয়েডিজম, গলগণ্ডবিহীন', 'Thyroid', NULL),
  ('ICD10', '2019', 'E06.3', 'Autoimmune thyroiditis', 'অটোইমিউন থাইরয়েডাইটিস', 'Thyroid', 10),
  ('ICD10', '2019', 'E05.0', 'Thyrotoxicosis with diffuse goitre', 'থাইরোটক্সিকোসিস, ছড়ানো গলগণ্ডসহ', 'Thyroid', 11),
  ('ICD10', '2019', 'E05.2', 'Thyrotoxicosis with toxic multinodular goitre', 'থাইরোটক্সিকোসিস, বহু-গুটিযুক্ত বিষাক্ত গলগণ্ডসহ', 'Thyroid', NULL),
  ('ICD10', '2019', 'E05.9', 'Thyrotoxicosis, unspecified', 'থাইরোটক্সিকোসিস, অনির্দিষ্ট', 'Thyroid', 12),
  ('ICD10', '2019', 'E04.0', 'Non-toxic diffuse goitre', 'অ-বিষাক্ত ছড়ানো গলগণ্ড', 'Thyroid', NULL),
  ('ICD10', '2019', 'E04.1', 'Non-toxic single thyroid nodule', 'অ-বিষাক্ত একক থাইরয়েড গুটি', 'Thyroid', 17),
  ('ICD10', '2019', 'E04.2', 'Non-toxic multinodular goitre', 'অ-বিষাক্ত বহু-গুটিযুক্ত গলগণ্ড', 'Thyroid', NULL),
  ('ICD10', '2019', 'E01.0', 'Iodine-deficiency related diffuse goitre', 'আয়োডিনের অভাবজনিত ছড়ানো গলগণ্ড', 'Thyroid', NULL),
  ('ICD10', '2019', 'E02', 'Subclinical iodine-deficiency hypothyroidism', 'উপসর্গহীন আয়োডিন-অভাবজনিত হাইপোথাইরয়েডিজম', 'Thyroid', NULL),
  ('ICD10', '2019', 'E89.0', 'Postprocedural hypothyroidism', 'চিকিৎসা-পরবর্তী হাইপোথাইরয়েডিজম', 'Thyroid', NULL),
  ('ICD10', '2019', 'C73', 'Malignant neoplasm of thyroid gland', 'থাইরয়েড গ্রন্থির ক্যান্সার', 'Thyroid', NULL),
  ('ICD10', '2019', 'E66.9', 'Obesity, unspecified', 'স্থূলতা, অনির্দিষ্ট', 'Obesity & lipids', 16),
  ('ICD10', '2019', 'E66.0', 'Obesity due to excess calories', 'অতিরিক্ত ক্যালরিজনিত স্থূলতা', 'Obesity & lipids', NULL),
  ('ICD10', '2019', 'E78.5', 'Hyperlipidaemia, unspecified', 'হাইপারলিপিডেমিয়া, অনির্দিষ্ট', 'Obesity & lipids', 18),
  ('ICD10', '2019', 'E78.0', 'Pure hypercholesterolaemia', 'বিশুদ্ধ হাইপারকোলেস্টেরলেমিয়া', 'Obesity & lipids', NULL),
  ('ICD10', '2019', 'E78.1', 'Pure hyperglyceridaemia', 'বিশুদ্ধ হাইপারগ্লিসারিডেমিয়া', 'Obesity & lipids', NULL),
  ('ICD10', '2019', 'E28.2', 'Polycystic ovarian syndrome', 'পলিসিস্টিক ওভারি সিনড্রোম', 'Reproductive', 19),
  ('ICD10', '2019', 'E29.1', 'Testicular hypofunction', 'অণ্ডকোষের কার্যকারিতা হ্রাস', 'Reproductive', NULL),
  ('ICD10', '2019', 'E30.1', 'Precocious puberty', 'অকাল বয়ঃসন্ধি', 'Reproductive', NULL),
  ('ICD10', '2019', 'E22.1', 'Hyperprolactinaemia', 'হাইপারপ্রোল্যাকটিনেমিয়া', 'Pituitary & adrenal', 20),
  ('ICD10', '2019', 'E23.0', 'Hypopituitarism', 'হাইপোপিটুইটারিজম', 'Pituitary & adrenal', NULL),
  ('ICD10', '2019', 'E24.9', 'Cushing syndrome, unspecified', 'কুশিং সিনড্রোম, অনির্দিষ্ট', 'Pituitary & adrenal', NULL),
  ('ICD10', '2019', 'E27.1', 'Primary adrenocortical insufficiency', 'প্রাথমিক অ্যাড্রিনাল অপর্যাপ্ততা', 'Pituitary & adrenal', NULL),
  ('ICD10', '2019', 'E20.9', 'Hypoparathyroidism, unspecified', 'হাইপোপ্যারাথাইরয়েডিজম, অনির্দিষ্ট', 'Parathyroid & bone', NULL),
  ('ICD10', '2019', 'E21.0', 'Primary hyperparathyroidism', 'প্রাথমিক হাইপারপ্যারাথাইরয়েডিজম', 'Parathyroid & bone', NULL),
  ('ICD10', '2019', 'E55.9', 'Vitamin D deficiency, unspecified', 'ভিটামিন ডি-এর অভাব, অনির্দিষ্ট', 'Parathyroid & bone', NULL),
  ('ICD10', '2019', 'E34.3', 'Short stature, not elsewhere classified', 'খর্বাকৃতি, অন্যত্র শ্রেণিবদ্ধ নয়', 'Growth', NULL),
  ('ICD10', '2019', 'I10', 'Essential (primary) hypertension', 'প্রাথমিক উচ্চ রক্তচাপ', 'Comorbidity', NULL),
  ('ICD10', '2019', 'N18.3', 'Chronic kidney disease, stage 3', 'দীর্ঘস্থায়ী কিডনি রোগ, স্তর ৩', 'Comorbidity', NULL),
  ('ICD10', '2019', 'N18.4', 'Chronic kidney disease, stage 4', 'দীর্ঘস্থায়ী কিডনি রোগ, স্তর ৪', 'Comorbidity', NULL),
  ('DTHC', '1.0', 'POLYURIA', 'Passing urine often', 'ঘন ঘন প্রস্রাব', 'Complaint', 1),
  ('DTHC', '1.0', 'POLYDIPSIA', 'Excessive thirst', 'অতিরিক্ত পিপাসা', 'Complaint', 2),
  ('DTHC', '1.0', 'POLYPHAGIA', 'Excessive hunger', 'অতিরিক্ত ক্ষুধা', 'Complaint', NULL),
  ('DTHC', '1.0', 'WEIGHT_LOSS', 'Losing weight', 'ওজন কমে যাওয়া', 'Complaint', 3),
  ('DTHC', '1.0', 'WEIGHT_GAIN', 'Putting on weight', 'ওজন বেড়ে যাওয়া', 'Complaint', NULL),
  ('DTHC', '1.0', 'FATIGUE', 'Tiredness', 'ক্লান্তি', 'Complaint', 4),
  ('DTHC', '1.0', 'BLURRED_VISION', 'Blurred vision', 'ঝাপসা দেখা', 'Complaint', 5),
  ('DTHC', '1.0', 'NUMB_FEET', 'Numbness of the feet', 'পায়ে অবশ ভাব', 'Complaint', 6),
  ('DTHC', '1.0', 'BURNING_FEET', 'Burning in the feet', 'পায়ে জ্বালাপোড়া', 'Complaint', 7),
  ('DTHC', '1.0', 'NECK_SWELLING', 'Swelling in the neck', 'গলা ফোলা', 'Complaint', 8),
  ('DTHC', '1.0', 'PALPITATIONS', 'Palpitations', 'বুক ধড়ফড়', 'Complaint', 9),
  ('DTHC', '1.0', 'HEAT_INTOLERANCE', 'Cannot tolerate heat', 'গরম সহ্য হয় না', 'Complaint', NULL),
  ('DTHC', '1.0', 'COLD_INTOLERANCE', 'Cannot tolerate cold', 'ঠান্ডা সহ্য হয় না', 'Complaint', NULL),
  ('DTHC', '1.0', 'HAIR_LOSS', 'Hair falling', 'চুল পড়া', 'Complaint', NULL),
  ('DTHC', '1.0', 'MENSTRUAL_IRREGULAR', 'Irregular periods', 'অনিয়মিত মাসিক', 'Complaint', 10),
  ('DTHC', '1.0', 'HIRSUTISM', 'Unwanted hair growth', 'অবাঞ্ছিত লোম', 'Complaint', NULL),
  ('DTHC', '1.0', 'ERECTILE_DYSFUNCTION', 'Difficulty with erection', 'যৌন দুর্বলতা', 'Complaint', NULL),
  ('DTHC', '1.0', 'FOOT_ULCER', 'A sore on the foot', 'পায়ে ঘা', 'Complaint', 11),
  ('DTHC', '1.0', 'SLOW_HEALING', 'A wound that will not heal', 'ক্ষত শুকায় না', 'Complaint', NULL),
  ('DTHC', '1.0', 'DIZZINESS', 'Dizziness', 'মাথা ঘোরা', 'Complaint', NULL),
  ('DTHC', '1.0', 'TREMOR', 'Shaking of the hands', 'হাত কাঁপা', 'Complaint', NULL),
  ('DTHC', '1.0', 'CONSTIPATION', 'Constipation', 'কোষ্ঠকাঠিন্য', 'Complaint', NULL),
  ('DTHC', '1.0', 'SNORING', 'Snoring or stopping breathing in sleep', 'নাক ডাকা বা ঘুমে শ্বাস আটকানো', 'Complaint', NULL),
  ('DTHC', '1.0', 'GENITAL_ITCHING', 'Itching in the genital area', 'গোপনাঙ্গে চুলকানি', 'Complaint', NULL)
ON CONFLICT (system, version, code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  heading = EXCLUDED.heading, favourite_rank = EXCLUDED.favourite_rank;

-- The headings in Bangla. Set from a closed list rather than repeated on all seventy-three
-- rows, because that is what it is: nine groupings the clinic thinks in, not a per-concept
-- fact. A heading added later without a line here fails the bilingual invariant below, which
-- is the point — the failure arrives at migration time rather than on a screen.
UPDATE core.terminology_concept c SET heading_bn = m.bn
  FROM (VALUES
    ('Diabetes',           'ডায়াবেটিস'),
    ('Thyroid',            'থাইরয়েড'),
    ('Pituitary & adrenal', 'পিটুইটারি ও অ্যাড্রিনাল'),
    ('Parathyroid & bone', 'প্যারাথাইরয়েড ও হাড়'),
    ('Reproductive',       'প্রজনন'),
    ('Obesity & lipids',   'স্থূলতা ও চর্বি'),
    ('Growth',             'বৃদ্ধি'),
    ('Comorbidity',        'সহঅসুস্থতা'),
    ('Complaint',          'উপসর্গ')
  ) AS m(en, bn)
 WHERE c.heading = m.en;

-- Synonyms are how three keystrokes reach a diagnosis whose official title nobody says
-- out loud. "Sugar" finds diabetes; "থাইরয়েড কম" finds hypothyroidism. Without them,
-- criterion 1 is a promise about the WHO's vocabulary rather than about this clinic's.
INSERT INTO core.terminology_synonym (system, version, code, term, language) VALUES
  ('ICD10', '2019', 'E11.9', 'type 2 diabetes', 'en'),
  ('ICD10', '2019', 'E11.9', 't2dm', 'en'),
  ('ICD10', '2019', 'E11.9', 'sugar', 'en'),
  ('ICD10', '2019', 'E11.9', 'dm2', 'en'),
  ('ICD10', '2019', 'E11.9', 'ডায়াবেটিস', 'bn'),
  ('ICD10', '2019', 'E11.9', 'বহুমূত্র', 'bn'),
  ('ICD10', '2019', 'E11.9', 'চিনির অসুখ', 'bn'),
  ('ICD10', '2019', 'E11.7', 't2dm complications', 'en'),
  ('ICD10', '2019', 'E11.7', 'জটিলতাসহ ডায়াবেটিস', 'bn'),
  ('ICD10', '2019', 'E11.2', 'diabetic nephropathy', 'en'),
  ('ICD10', '2019', 'E11.2', 'kidney diabetes', 'en'),
  ('ICD10', '2019', 'E11.2', 'ডায়াবেটিক কিডনি রোগ', 'bn'),
  ('ICD10', '2019', 'E11.3', 'diabetic eye', 'en'),
  ('ICD10', '2019', 'E11.3', 'ডায়াবেটিক চোখ', 'bn'),
  ('ICD10', '2019', 'E11.4', 'diabetic neuropathy', 'en'),
  ('ICD10', '2019', 'E11.4', 'nerve diabetes', 'en'),
  ('ICD10', '2019', 'E11.4', 'ডায়াবেটিক স্নায়ুরোগ', 'bn'),
  ('ICD10', '2019', 'E11.5', 'diabetic foot', 'en'),
  ('ICD10', '2019', 'E11.5', 'ডায়াবেটিক ফুট', 'bn'),
  ('ICD10', '2019', 'E11.0', 'hhs', 'en'),
  ('ICD10', '2019', 'E11.0', 'hyperosmolar', 'en'),
  ('ICD10', '2019', 'E10.9', 'type 1 diabetes', 'en'),
  ('ICD10', '2019', 'E10.9', 't1dm', 'en'),
  ('ICD10', '2019', 'E10.9', 'insulin dependent', 'en'),
  ('ICD10', '2019', 'E10.9', 'টাইপ ১', 'bn'),
  ('ICD10', '2019', 'E10.1', 'dka', 'en'),
  ('ICD10', '2019', 'E10.1', 'ketoacidosis', 'en'),
  ('ICD10', '2019', 'E10.1', 'কিটোঅ্যাসিডোসিস', 'bn'),
  ('ICD10', '2019', 'O24.4', 'gdm', 'en'),
  ('ICD10', '2019', 'O24.4', 'gestational diabetes', 'en'),
  ('ICD10', '2019', 'O24.4', 'pregnancy diabetes', 'en'),
  ('ICD10', '2019', 'O24.4', 'গর্ভকালীন', 'bn'),
  ('ICD10', '2019', 'R73.0', 'prediabetes', 'en'),
  ('ICD10', '2019', 'R73.0', 'impaired glucose tolerance', 'en'),
  ('ICD10', '2019', 'R73.0', 'igt', 'en'),
  ('ICD10', '2019', 'R73.0', 'প্রি-ডায়াবেটিস', 'bn'),
  ('ICD10', '2019', 'R73.0', 'ডায়াবেটিসের আগের অবস্থা', 'bn'),
  ('ICD10', '2019', 'E16.2', 'low sugar', 'en'),
  ('ICD10', '2019', 'E16.2', 'hypo', 'en'),
  ('ICD10', '2019', 'E16.2', 'রক্তে চিনি কমে যাওয়া', 'bn'),
  ('ICD10', '2019', 'G63.2', 'numb feet', 'en'),
  ('ICD10', '2019', 'G63.2', 'burning feet', 'en'),
  ('ICD10', '2019', 'G63.2', 'পা ঝিমঝিম', 'bn'),
  ('ICD10', '2019', 'H36.0', 'eye damage', 'en'),
  ('ICD10', '2019', 'H36.0', 'চোখের রেটিনার ক্ষতি', 'bn'),
  ('ICD10', '2019', 'L97', 'foot ulcer', 'en'),
  ('ICD10', '2019', 'L97', 'পায়ে ঘা', 'bn'),
  ('ICD10', '2019', 'E03.9', 'underactive thyroid', 'en'),
  ('ICD10', '2019', 'E03.9', 'low thyroid', 'en'),
  ('ICD10', '2019', 'E03.9', 'hypothyroid', 'en'),
  ('ICD10', '2019', 'E03.9', 'থাইরয়েড কম', 'bn'),
  ('ICD10', '2019', 'E03.9', 'থাইরয়েডের কার্যকারিতা কম', 'bn'),
  ('ICD10', '2019', 'E06.3', 'hashimoto', 'en'),
  ('ICD10', '2019', 'E06.3', 'thyroid antibody', 'en'),
  ('ICD10', '2019', 'E06.3', 'হাশিমোটো', 'bn'),
  ('ICD10', '2019', 'E05.0', 'graves', 'en'),
  ('ICD10', '2019', 'E05.0', 'graves disease', 'en'),
  ('ICD10', '2019', 'E05.0', 'গ্রেভস', 'bn'),
  ('ICD10', '2019', 'E05.9', 'overactive thyroid', 'en'),
  ('ICD10', '2019', 'E05.9', 'hyperthyroid', 'en'),
  ('ICD10', '2019', 'E05.9', 'থাইরয়েড বেশি', 'bn'),
  ('ICD10', '2019', 'E04.0', 'goitre', 'en'),
  ('ICD10', '2019', 'E04.0', 'গলগণ্ড', 'bn'),
  ('ICD10', '2019', 'E04.1', 'thyroid nodule', 'en'),
  ('ICD10', '2019', 'E04.1', 'থাইরয়েড গুটি', 'bn'),
  ('ICD10', '2019', 'E04.2', 'multinodular', 'en'),
  ('ICD10', '2019', 'E01.0', 'iodine', 'en'),
  ('ICD10', '2019', 'E01.0', 'আয়োডিন', 'bn'),
  ('ICD10', '2019', 'E02', 'subclinical', 'en'),
  ('ICD10', '2019', 'E89.0', 'after surgery thyroid', 'en'),
  ('ICD10', '2019', 'E89.0', 'post radioiodine', 'en'),
  ('ICD10', '2019', 'C73', 'thyroid cancer', 'en'),
  ('ICD10', '2019', 'C73', 'থাইরয়েড ক্যান্সার', 'bn'),
  ('ICD10', '2019', 'E66.9', 'obesity', 'en'),
  ('ICD10', '2019', 'E66.9', 'overweight', 'en'),
  ('ICD10', '2019', 'E66.9', 'স্থূলতা', 'bn'),
  ('ICD10', '2019', 'E66.9', 'ওজন বেশি', 'bn'),
  ('ICD10', '2019', 'E78.5', 'high cholesterol', 'en'),
  ('ICD10', '2019', 'E78.5', 'lipid', 'en'),
  ('ICD10', '2019', 'E78.5', 'চর্বি বেশি', 'bn'),
  ('ICD10', '2019', 'E78.5', 'কোলেস্টেরল', 'bn'),
  ('ICD10', '2019', 'E78.1', 'triglyceride', 'en'),
  ('ICD10', '2019', 'E78.1', 'ট্রাইগ্লিসারাইড', 'bn'),
  ('ICD10', '2019', 'E28.2', 'pcos', 'en'),
  ('ICD10', '2019', 'E28.2', 'পিসিওএস', 'bn'),
  ('ICD10', '2019', 'E28.2', 'ডিম্বাশয়ে গুটি', 'bn'),
  ('ICD10', '2019', 'E29.1', 'low testosterone', 'en'),
  ('ICD10', '2019', 'E29.1', 'hypogonadism', 'en'),
  ('ICD10', '2019', 'E30.1', 'early puberty', 'en'),
  ('ICD10', '2019', 'E30.1', 'আগেই বয়ঃসন্ধি', 'bn'),
  ('ICD10', '2019', 'E22.1', 'prolactin', 'en'),
  ('ICD10', '2019', 'E22.1', 'প্রোল্যাকটিন', 'bn'),
  ('ICD10', '2019', 'E24.9', 'cushing', 'en'),
  ('ICD10', '2019', 'E24.9', 'কুশিং', 'bn'),
  ('ICD10', '2019', 'E27.1', 'addison', 'en'),
  ('ICD10', '2019', 'E27.1', 'অ্যাডিসন', 'bn'),
  ('ICD10', '2019', 'E21.0', 'pth', 'en'),
  ('ICD10', '2019', 'E21.0', 'parathyroid', 'en'),
  ('ICD10', '2019', 'E55.9', 'vitamin d', 'en'),
  ('ICD10', '2019', 'E55.9', 'ভিটামিন ডি', 'bn'),
  ('ICD10', '2019', 'E34.3', 'short height', 'en'),
  ('ICD10', '2019', 'E34.3', 'growth failure', 'en'),
  ('ICD10', '2019', 'E34.3', 'উচ্চতা কম', 'bn'),
  ('ICD10', '2019', 'I10', 'hypertension', 'en'),
  ('ICD10', '2019', 'I10', 'high bp', 'en'),
  ('ICD10', '2019', 'I10', 'উচ্চ রক্তচাপ', 'bn'),
  ('ICD10', '2019', 'I10', 'প্রেশার', 'bn'),
  ('ICD10', '2019', 'N18.3', 'ckd 3', 'en'),
  ('ICD10', '2019', 'N18.3', 'কিডনি রোগ', 'bn'),
  ('ICD10', '2019', 'N18.4', 'ckd 4', 'en'),
  ('DTHC', '1.0', 'POLYURIA', 'frequent urination', 'en'),
  ('DTHC', '1.0', 'POLYURIA', 'urine', 'en'),
  ('DTHC', '1.0', 'POLYURIA', 'প্রস্রাব', 'bn'),
  ('DTHC', '1.0', 'POLYDIPSIA', 'thirst', 'en'),
  ('DTHC', '1.0', 'POLYDIPSIA', 'পিপাসা', 'bn'),
  ('DTHC', '1.0', 'POLYDIPSIA', 'পানি তেষ্টা', 'bn'),
  ('DTHC', '1.0', 'POLYPHAGIA', 'hunger', 'en'),
  ('DTHC', '1.0', 'POLYPHAGIA', 'ক্ষুধা', 'bn'),
  ('DTHC', '1.0', 'WEIGHT_LOSS', 'weight loss', 'en'),
  ('DTHC', '1.0', 'WEIGHT_LOSS', 'ওজন কমা', 'bn'),
  ('DTHC', '1.0', 'WEIGHT_GAIN', 'weight gain', 'en'),
  ('DTHC', '1.0', 'WEIGHT_GAIN', 'ওজন বাড়া', 'bn'),
  ('DTHC', '1.0', 'FATIGUE', 'tired', 'en'),
  ('DTHC', '1.0', 'FATIGUE', 'weakness', 'en'),
  ('DTHC', '1.0', 'FATIGUE', 'দুর্বলতা', 'bn'),
  ('DTHC', '1.0', 'BLURRED_VISION', 'vision', 'en'),
  ('DTHC', '1.0', 'BLURRED_VISION', 'চোখে ঝাপসা', 'bn'),
  ('DTHC', '1.0', 'NUMB_FEET', 'numbness', 'en'),
  ('DTHC', '1.0', 'NUMB_FEET', 'পা অবশ', 'bn'),
  ('DTHC', '1.0', 'BURNING_FEET', 'burning', 'en'),
  ('DTHC', '1.0', 'BURNING_FEET', 'পা জ্বালা', 'bn'),
  ('DTHC', '1.0', 'NECK_SWELLING', 'goitre', 'en'),
  ('DTHC', '1.0', 'NECK_SWELLING', 'neck lump', 'en'),
  ('DTHC', '1.0', 'NECK_SWELLING', 'গলগণ্ড', 'bn'),
  ('DTHC', '1.0', 'PALPITATIONS', 'heart racing', 'en'),
  ('DTHC', '1.0', 'PALPITATIONS', 'ধড়ফড়', 'bn'),
  ('DTHC', '1.0', 'HEAT_INTOLERANCE', 'sweating', 'en'),
  ('DTHC', '1.0', 'HEAT_INTOLERANCE', 'গরম', 'bn'),
  ('DTHC', '1.0', 'COLD_INTOLERANCE', 'cold', 'en'),
  ('DTHC', '1.0', 'COLD_INTOLERANCE', 'ঠান্ডা', 'bn'),
  ('DTHC', '1.0', 'HAIR_LOSS', 'hair fall', 'en'),
  ('DTHC', '1.0', 'HAIR_LOSS', 'চুল', 'bn'),
  ('DTHC', '1.0', 'MENSTRUAL_IRREGULAR', 'periods', 'en'),
  ('DTHC', '1.0', 'MENSTRUAL_IRREGULAR', 'মাসিক', 'bn'),
  ('DTHC', '1.0', 'HIRSUTISM', 'facial hair', 'en'),
  ('DTHC', '1.0', 'HIRSUTISM', 'লোম', 'bn'),
  ('DTHC', '1.0', 'ERECTILE_DYSFUNCTION', 'ed', 'en'),
  ('DTHC', '1.0', 'ERECTILE_DYSFUNCTION', 'যৌন', 'bn'),
  ('DTHC', '1.0', 'FOOT_ULCER', 'ulcer', 'en'),
  ('DTHC', '1.0', 'FOOT_ULCER', 'wound', 'en'),
  ('DTHC', '1.0', 'FOOT_ULCER', 'ঘা', 'bn'),
  ('DTHC', '1.0', 'SLOW_HEALING', 'healing', 'en'),
  ('DTHC', '1.0', 'SLOW_HEALING', 'ক্ষত', 'bn'),
  ('DTHC', '1.0', 'DIZZINESS', 'giddy', 'en'),
  ('DTHC', '1.0', 'DIZZINESS', 'মাথা ঘোরা', 'bn'),
  ('DTHC', '1.0', 'TREMOR', 'shaking', 'en'),
  ('DTHC', '1.0', 'TREMOR', 'কাঁপা', 'bn'),
  ('DTHC', '1.0', 'CONSTIPATION', 'bowel', 'en'),
  ('DTHC', '1.0', 'CONSTIPATION', 'কোষ্ঠ', 'bn'),
  ('DTHC', '1.0', 'SNORING', 'sleep apnoea', 'en'),
  ('DTHC', '1.0', 'SNORING', 'নাক ডাকা', 'bn'),
  ('DTHC', '1.0', 'GENITAL_ITCHING', 'itching', 'en'),
  ('DTHC', '1.0', 'GENITAL_ITCHING', 'চুলকানি', 'bn')
ON CONFLICT (system, version, code, term) DO NOTHING;
-- ---------------------------------------------------------------------------
-- Search
-- ---------------------------------------------------------------------------
--
-- The ranking lives in the query layer (`internal/terminology/queries`), in one statement.
-- What lives here is the predicate the ranking asks twice — once for the favourites and once
-- for everything else — because a predicate written out twice is a predicate that gets fixed
-- once.
--
-- The tiers, in the order a clinician expects, each for a reason somebody would complain
-- about:
--
--   1. the code itself, typed. "E11" should be the first thing "E11" finds.
--   2. a favourite whose words start with what was typed. Dr. Nahid's twenty, first.
--   3. any title or synonym whose words start with it.
--   4. anything the trigram index thinks is close, for a misspelling.
--
-- Favourite rank breaks ties inside a tier, so the twenty are reachable in three keystrokes
-- (criterion 1) without pushing everything else off the list.
-- Whether a concept's own words start with what was typed.
--
-- Its own function because the ranking below asks the same question twice — once for the
-- favourites and once for everything else — and a predicate written out twice is a predicate
-- that gets fixed once.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.terminology_matches(
  p_system text, p_version text, p_code text,
  p_display_en text, p_display_bn text, p_prefix text, p_word text
) RETURNS boolean
LANGUAGE sql STABLE AS $$
  SELECT lower(p_display_en) LIKE p_prefix || '%'
      OR lower(p_display_bn) LIKE p_prefix || '%'
      OR lower(p_display_en) SIMILAR TO p_word
      OR lower(p_display_bn) SIMILAR TO p_word
      OR EXISTS (
        SELECT 1 FROM core.terminology_synonym s
         WHERE s.system = p_system AND s.version = p_version AND s.code = p_code
           AND (lower(s.term) LIKE p_prefix || '%' OR lower(s.term) SIMILAR TO p_word));
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION core.terminology_matches(text, text, text, text, text, text, text) TO dthcms_app;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- Nothing SNOMED-derived may be embedded until D-24 answers, and "we remembered not to" is not
-- a control. This is.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_unlicensed_terminology_is_embedded() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  SELECT count(*) INTO offending
    FROM core.terminology_concept c
    JOIN core.code_system s ON s.code = c.system
   WHERE NOT s.usable;
  IF offending > 0 THEN
    RAISE EXCEPTION 'a terminology that may not be used here holds % concept(s)', offending
      USING HINT = 'D-24: SNOMED CT requires an Affiliate licence, and whether Bangladesh '
                   'confers free use must be verified before any SNOMED content is embedded.';
  END IF;
END;
$$;
-- +goose StatementEnd

-- Every favourite has both languages. Criterion 3 asks for it, and a picker that shows an
-- English title to a Bengali-reading clinician on the one list they use every day is a picker
-- that makes the language toggle look like decoration.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_favourites_are_bilingual() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender text;
BEGIN
  SELECT c.code INTO offender
    FROM core.terminology_concept c
   WHERE c.favourite_rank IS NOT NULL AND c.retired_at IS NULL
     AND btrim(c.display_bn) = ''
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION '% is a favourite with no Bengali display term', offender;
  END IF;

  -- And its grouping, for every concept rather than only the favourites. A list of Bengali
  -- diagnoses filed under English chapter names is what half-bilingual looks like on a
  -- screen, and it is worse than either language alone because it reads as an interface
  -- somebody translated the easy parts of.
  SELECT c.code INTO offender
    FROM core.terminology_concept c
   WHERE c.retired_at IS NULL
     AND btrim(c.heading) <> '' AND btrim(c.heading_bn) = ''
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'the heading on % has no Bengali form', offender
      USING HINT = 'Add it to the heading list in migration 00034.';
  END IF;
END;
$$;
-- +goose StatementEnd

-- Every concept names a version that exists, and every system that holds any says which
-- version a new coding uses. The first is a foreign key; the second is what makes "which
-- version was this recorded under" answerable at all.
--
-- Scoped to systems that actually hold concepts, deliberately: ICD-11 is registered here with
-- no default and no content, so that moving to it is rows rather than a schema change. A
-- registered-but-empty system is a plan, not a gap.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_terminology_has_a_default_version() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender text;
BEGIN
  SELECT s.code INTO offender
    FROM core.code_system s
   WHERE EXISTS (SELECT 1 FROM core.terminology_concept c WHERE c.system = s.code)
     AND NOT EXISTS (SELECT 1 FROM core.code_system_version v
                      WHERE v.system = s.code AND v.is_default)
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION '% holds concepts but has no default version', offender
      USING HINT = 'Nothing can be newly coded under it: there is no answer to "which version".';
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_no_unlicensed_terminology_is_embedded() IS
  'Raises if content from a terminology that may not be used here has been loaded (D-24, CP52).';
COMMENT ON FUNCTION core.assert_favourites_are_bilingual() IS
  'Raises if a favourite diagnosis has no Bengali display term (CP52 criterion 3).';
COMMENT ON FUNCTION core.assert_every_terminology_has_a_default_version() IS
  'Raises if a usable code system has no default version (CP52 criterion 2).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_no_unlicensed_terminology_is_embedded',
   'no content from an unlicensed terminology has been loaded', 59),
  ('assert_favourites_are_bilingual',
   'every favourite diagnosis, and every grouping, reads in both languages', 60),
  ('assert_every_terminology_has_a_default_version',
   'every code system that holds concepts says which version a new coding uses', 61)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_no_unlicensed_terminology_is_embedded', 'assert_favourites_are_bilingual',
  'assert_every_terminology_has_a_default_version');
DROP FUNCTION IF EXISTS core.assert_every_terminology_has_a_default_version();
DROP FUNCTION IF EXISTS core.assert_favourites_are_bilingual();
DROP FUNCTION IF EXISTS core.assert_no_unlicensed_terminology_is_embedded();
DROP FUNCTION IF EXISTS core.terminology_matches(text, text, text, text, text, text, text);
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name IN (
   'code_system', 'code_system_version', 'terminology_concept',
   'terminology_synonym', 'terminology_map');
DROP TABLE IF EXISTS core.terminology_map;
DROP TABLE IF EXISTS core.terminology_synonym;
DROP TABLE IF EXISTS core.terminology_concept;
DROP TABLE IF EXISTS core.code_system_version;
DROP TABLE IF EXISTS core.code_system;
DELETE FROM core.role_permission WHERE permission_code = 'terminology.read';
DELETE FROM core.permission WHERE code = 'terminology.read';
