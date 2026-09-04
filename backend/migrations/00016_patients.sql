-- Patients (CP28, blueprint §3 Step 1): the schema everything downstream hangs from.
--
-- Four decisions are worth reading before the columns, because each of them is difficult
-- to change once a clinic has registered a thousand people.
--
-- **The date of birth is load-bearing** [R-06]. Pediatric percentile calculation depends
-- on exact age, so `birth_date` is NOT NULL and validated — no future dates, no implausible
-- ages — and it carries two companions: `dob_precision`, because a patient who knows only
-- the year must be recordable without pretending to a day, and `dob_verified_by`, because
-- "the national ID says so" and "the patient thinks so" are different evidence and a
-- researcher must be able to tell them apart.
--
-- **The National ID is hashed and sealed, never plain** (D-47, confirmed). The salted hash
-- does duplicate detection; the full number is sealed with the CP12 key ring and shown
-- masked. Revealing it in full is a step-up and an audit entry. `assert_no_plaintext_identifiers`
-- checks the shape on every start, because a column named `sealed` that quietly holds a
-- readable number is exactly the failure nobody notices.
--
-- **The socio-economic baseline is fixed, not extensible** (§12, confirmed with Dr Nahid).
-- Six fields, as CHECK constraints rather than lookup tables: a research variable whose
-- category list can be edited from the application is a variable whose cohorts stop being
-- comparable, quietly, between one paper and the next. Adding a category is a migration,
-- which is the review this deserves.
--
-- **The research identity is separately governed** (§12, plan §9.8). `research.research_subject`
-- holds the anonymised row a researcher queries. The link back to the patient lives in its
-- own schema, `identity_link`, which `dthcms_research` cannot reach and which `dthcms_app`
-- may write and may not read. Anonymisation that depends on an analyst querying the right
-- schema is not anonymisation. ADR-0020 records all four.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The human-readable clinical id
-- ---------------------------------------------------------------------------

-- One counter per facility per year. A row lock rather than a sequence, deliberately: a
-- sequence leaves gaps when a transaction rolls back, and a clinic that finds patient
-- DTHC-26-000138 with no 000137 will spend an afternoon looking for the missing person.
-- The lock serialises registrations within one facility-year, which at a clinic's rate of
-- a few hundred a day costs nothing.
CREATE TABLE core.clinical_id_counter (
  facility_id uuid        NOT NULL REFERENCES core.facility(id),
  year        smallint    NOT NULL CHECK (year BETWEEN 2020 AND 2200),
  next_value  integer     NOT NULL DEFAULT 1 CHECK (next_value >= 1),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (facility_id, year)
);

COMMENT ON TABLE core.clinical_id_counter IS
  'The next clinical id per facility per year. Locked per row, so ids are gapless (CP28).';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.next_clinical_id(p_facility uuid, p_year smallint DEFAULT NULL)
RETURNS text
LANGUAGE plpgsql AS $$
DECLARE
  v_year  smallint := coalesce(p_year, extract(year from (now() AT TIME ZONE 'Asia/Dhaka'))::smallint);
  v_next  integer;
  v_code  text;
BEGIN
  -- The clinic's own year, not UTC's: a patient registered at 00:30 on 1 January in Dhaka
  -- belongs to the new year, and a clinical id that says otherwise is one the registration
  -- desk will not believe.
  INSERT INTO core.clinical_id_counter (facility_id, year, next_value)
  VALUES (p_facility, v_year, 1)
  ON CONFLICT (facility_id, year) DO NOTHING;

  UPDATE core.clinical_id_counter
     SET next_value = next_value + 1, updated_at = now()
   WHERE facility_id = p_facility AND year = v_year
  RETURNING next_value - 1 INTO v_next;

  SELECT f.code INTO v_code FROM core.facility f WHERE f.id = p_facility;
  IF v_code IS NULL THEN
    RAISE EXCEPTION 'no facility %', p_facility;
  END IF;

  -- DTHC-FRD-26-000137: spoken aloud at a desk, written on a card, typed by a person who
  -- is looking at the patient rather than the screen (§15.2).
  RETURN format('%s-%s-%s', v_code, to_char(v_year, 'FM0000'), to_char(v_next, 'FM000000'));
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.next_clinical_id(uuid, smallint) IS
  'The next gapless clinical id for a facility and clinic year (CP28).';

-- ---------------------------------------------------------------------------
-- The patient
-- ---------------------------------------------------------------------------

CREATE TABLE core.patient (
  id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id   uuid        NOT NULL REFERENCES core.facility(id),

  -- Human-readable, spoken, printed. Unique across the deployment, not merely the
  -- facility: a card carried between two DTHC clinics must not mean two people.
  clinical_id   text        NOT NULL,

  name_en       text        NOT NULL,
  -- Bangla is prompted and skippable at registration (the confirmed required set), so it
  -- is empty-string-able rather than NULL — an absent name is '' everywhere in this
  -- schema, which keeps every search and every sort from having to think about NULL.
  name_bn       text        NOT NULL DEFAULT '',

  sex           text        NOT NULL CHECK (sex IN ('female', 'male', 'other')),

  -- [R-06]. Exact, mandatory, validated by trigger below.
  birth_date    date        NOT NULL,
  -- How exact it is. 'day' is a real date of birth; 'month' and 'year' mean the rest of
  -- the date is a placeholder and a percentile computed from it carries that uncertainty.
  dob_precision text        NOT NULL DEFAULT 'day'
                CHECK (dob_precision IN ('day', 'month', 'year')),
  -- What established it. A researcher comparing pediatric growth needs to know whether an
  -- age came from a birth certificate or from a grandmother's recollection.
  dob_verified_by text      NOT NULL
                CHECK (dob_verified_by IN ('birth_certificate', 'national_id', 'passport',
                                           'immunisation_card', 'patient_stated',
                                           'guardian_stated', 'estimated')),
  dob_verified_at      timestamptz,
  dob_verified_user_id uuid REFERENCES core.app_user(id),

  -- Contact. One Bangladeshi mobile is required (the confirmed rule); the second field
  -- takes a landline or an overseas number and is optional.
  phone_primary   text      NOT NULL,
  phone_secondary text      NOT NULL DEFAULT '',

  -- Address, as Bangladesh is administratively divided. Prompted, skippable.
  division      text        NOT NULL DEFAULT '',
  district      text        NOT NULL DEFAULT '',
  upazila       text        NOT NULL DEFAULT '',
  address_line  text        NOT NULL DEFAULT '',
  postcode      text        NOT NULL DEFAULT '',

  emergency_name     text   NOT NULL DEFAULT '',
  emergency_relation text   NOT NULL DEFAULT '',
  emergency_phone    text   NOT NULL DEFAULT '',

  -- ---------------------------------------------------------------------
  -- The socio-economic baseline (§12 cohorting)
  --
  -- NULL and 'unknown' mean different things and both are needed. NULL is "not captured"
  -- — the desk skipped it to keep the queue moving. 'unknown' is "asked, and the patient
  -- does not know", which is itself a finding: a household that cannot state its monthly
  -- income is a different cohort from one that was never asked.
  -- ---------------------------------------------------------------------
  education_level     text CHECK (education_level IN (
                        'none', 'primary', 'secondary', 'higher_secondary',
                        'graduate', 'postgraduate', 'madrasa', 'unknown')),
  occupation_category text CHECK (occupation_category IN (
                        'agriculture', 'day_labour', 'factory_worker', 'service_private',
                        'service_government', 'business', 'homemaker', 'student',
                        'retired', 'unemployed', 'other', 'unknown')),
  -- Monthly household income in BDT. Bands rather than a number: an exact figure is
  -- rarely known, is answered less honestly, and is not what a cohort comparison uses.
  income_band         text CHECK (income_band IN (
                        'under_10k', '10k_25k', '25k_50k', '50k_100k', 'over_100k', 'unknown')),
  household_size      smallint CHECK (household_size BETWEEN 1 AND 40),
  residence_type      text CHECK (residence_type IN ('urban', 'semi_urban', 'rural', 'unknown')),
  -- Who pays for medicines. §12 names affordability of semaglutide and tirzepatide as an
  -- output, and "can the patient afford it" and "does an employer pay" are different
  -- questions with different answers.
  medicine_payer      text CHECK (medicine_payer IN (
                        'self', 'family', 'employer', 'ngo', 'government', 'unknown')),

  -- The object key in blob storage. The bytes are not in the database (CP34).
  photo_object_key text NOT NULL DEFAULT '',

  status        text        NOT NULL DEFAULT 'active'
                CHECK (status IN ('active', 'inactive', 'deceased', 'merged')),
  -- Where a merged record now points (CP30). Never a delete: a duplicate that is removed
  -- takes its history with it.
  merged_into_id uuid REFERENCES core.patient(id),
  status_reason text        NOT NULL DEFAULT '',

  registered_by uuid        REFERENCES core.app_user(id),
  registered_at timestamptz NOT NULL DEFAULT now(),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT patient_clinical_id_unique UNIQUE (clinical_id),
  CONSTRAINT patient_merged_has_target CHECK ((status = 'merged') = (merged_into_id IS NOT NULL)),
  CONSTRAINT patient_not_merged_into_itself CHECK (merged_into_id IS DISTINCT FROM id),
  CONSTRAINT patient_status_reason_when_not_active CHECK (
    status = 'active' OR length(btrim(status_reason)) > 0 OR status = 'merged'),
  -- The confirmed required set: an English name, a sex, a date of birth, one mobile.
  CONSTRAINT patient_name_present CHECK (length(btrim(name_en)) > 0),
  CONSTRAINT patient_phone_present CHECK (length(btrim(phone_primary)) > 0),
  -- Normalised to +880 by the application, and held to it here: a phone stored three ways
  -- is a phone that matches nothing, and SMS reminders (§11) fail silently for a fraction
  -- of patients with no way to tell which.
  CONSTRAINT patient_phone_normalised CHECK (phone_primary ~ '^\+8801[3-9][0-9]{8}$'),
  CONSTRAINT patient_phone_secondary_shape CHECK (
    phone_secondary = '' OR phone_secondary ~ '^\+[0-9]{8,15}$')
);

SELECT core.attach_updated_at('core.patient');

COMMENT ON TABLE core.patient IS
  'A person the clinic knows. Demographics, contact, the socio-economic baseline, and the date of birth pediatric percentiles depend on (CP28).';
COMMENT ON COLUMN core.patient.dob_precision IS
  'day | month | year. Below "day", the rest of birth_date is a placeholder and any age derived from it carries that uncertainty.';
COMMENT ON COLUMN core.patient.income_band IS
  'Monthly household income in BDT. NULL means not captured; ''unknown'' means asked and not known.';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_plausible_birth_date() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  -- A trigger and not a CHECK: a CHECK may not call a non-immutable function, and
  -- "not in the future" is a statement about now.
  IF NEW.birth_date > (now() AT TIME ZONE 'Asia/Dhaka')::date THEN
    RAISE EXCEPTION 'birth_date % is in the future', NEW.birth_date
      USING ERRCODE = 'check_violation', COLUMN = 'birth_date';
  END IF;
  IF NEW.birth_date < (now() AT TIME ZONE 'Asia/Dhaka')::date - interval '130 years' THEN
    RAISE EXCEPTION 'birth_date % implies an age over 130', NEW.birth_date
      USING ERRCODE = 'check_violation', COLUMN = 'birth_date';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER patient_birth_date_plausible
  BEFORE INSERT OR UPDATE OF birth_date ON core.patient
  FOR EACH ROW EXECUTE FUNCTION core.assert_plausible_birth_date();

COMMENT ON FUNCTION core.assert_plausible_birth_date() IS
  'Refuses a birth date in the future or over 130 years ago, by the clinic''s calendar (CP28, R-06).';

-- Search and duplicate detection (CP30, CP31). Trigram indexes because a Bengali name
-- typed by two operators differs by a conjunct as often as by a letter.
CREATE INDEX patient_name_en_trgm ON core.patient USING gin (name_en gin_trgm_ops);
CREATE INDEX patient_name_bn_trgm ON core.patient USING gin (name_bn gin_trgm_ops);
CREATE INDEX patient_phone_primary ON core.patient (phone_primary);
CREATE INDEX patient_by_facility_registered ON core.patient (facility_id, registered_at DESC);
-- The duplicate-detection probe: same date of birth and same sex is the cheap first cut.
CREATE INDEX patient_dob_sex ON core.patient (facility_id, birth_date, sex);

-- ---------------------------------------------------------------------------
-- Identifiers
-- ---------------------------------------------------------------------------

-- D-47, confirmed: the hash finds duplicates, the sealed value is shown masked and read
-- in full only under a step-up. Neither column ever holds a readable number.
CREATE TABLE core.patient_identifier (
  id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid        NOT NULL REFERENCES core.facility(id),
  patient_id  uuid        NOT NULL REFERENCES core.patient(id) ON DELETE RESTRICT,

  kind        text        NOT NULL CHECK (kind IN (
                'national_id', 'birth_certificate', 'passport', 'driving_licence', 'other')),

  -- HMAC-SHA-256 of the normalised number under a per-deployment pepper. A plain hash of a
  -- ten-digit NID is reversible by anyone with a laptop and a weekend; the pepper is what
  -- makes the digest useless outside this system.
  digest      bytea       NOT NULL,
  -- secretbox, under the CP12 key ring. Openable by the service, not by a database dump.
  sealed      bytea       NOT NULL,
  key_id      text        NOT NULL,
  -- What a screen shows without a step-up: '**** **** 4821'.
  masked      text        NOT NULL,

  verified_at      timestamptz,
  verified_by      uuid REFERENCES core.app_user(id),
  -- 'ocr' when it was read from a photograph of the card (§3 Step 1), 'typed' otherwise.
  capture_method   text NOT NULL DEFAULT 'typed'
                   CHECK (capture_method IN ('typed', 'ocr', 'imported')),

  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  -- One number belongs to one person, within a facility. This is the constraint that makes
  -- "strict duplicate-record prevention" (§3 Step 1) a property of the database rather
  -- than of a check somebody remembered to run.
  CONSTRAINT patient_identifier_unique UNIQUE (facility_id, kind, digest),
  CONSTRAINT patient_identifier_digest_length CHECK (length(digest) = 32),
  CONSTRAINT patient_identifier_sealed_present CHECK (length(sealed) > 0),
  CONSTRAINT patient_identifier_masked_present CHECK (length(btrim(masked)) > 0)
);

SELECT core.attach_updated_at('core.patient_identifier');

CREATE INDEX patient_identifier_by_patient ON core.patient_identifier (patient_id);

COMMENT ON TABLE core.patient_identifier IS
  'A patient''s official numbers: a peppered digest for matching, a sealed value for retrieval, a mask for display (CP28, D-47).';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_plaintext_identifiers() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  -- A `sealed` column holding something that reads like a number is the failure nobody
  -- notices: the schema says sealed, the dump says 1990123456789, and everybody assumes
  -- the other layer handled it.
  SELECT count(*) INTO offending
  FROM core.patient_identifier
  WHERE encode(sealed, 'escape') ~ '[0-9]{6,}'
     OR masked !~ '\*';

  IF offending > 0 THEN
    RAISE EXCEPTION 'patient identifiers are not sealed or not masked: % row(s)', offending
      USING HINT = 'sealed holds secretbox output; masked holds a display form with the '
                   'digits hidden. Neither may hold a readable number (D-47).';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_no_plaintext_identifiers() IS
  'Raises if a patient identifier looks like it holds a readable number (CP28, D-47).';

-- ---------------------------------------------------------------------------
-- Research identity
-- ---------------------------------------------------------------------------

-- The anonymised row a researcher queries. No name, no identifier, no address, no exact
-- date of birth — a birth year and the cohorting variables, which is what §12's analyses
-- actually use.
CREATE TABLE research.research_subject (
  research_id   text        PRIMARY KEY,
  -- Which clinic, for multi-site comparison. Not a re-identifier at one site and worth
  -- having before there are two.
  facility_code text        NOT NULL,
  -- The month, not the day: a registration date to the day plus a birth year plus a sex
  -- narrows a small population further than a cohort study needs.
  enrolled_month date       NOT NULL,
  birth_year    smallint    NOT NULL CHECK (birth_year BETWEEN 1890 AND 2200),
  sex           text        NOT NULL CHECK (sex IN ('female', 'male', 'other')),

  education_level     text,
  occupation_category text,
  income_band         text,
  household_size      smallint,
  residence_type      text,
  medicine_payer      text,

  created_at    timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT research_id_opaque CHECK (research_id ~ '^RS-[0-9A-HJKMNP-TV-Z]{26}$')
);

COMMENT ON TABLE research.research_subject IS
  'The anonymised subject a researcher queries. Carries no identifier and no join path to core (CP28, §12).';
COMMENT ON COLUMN research.research_subject.research_id IS
  'Opaque, from a cryptographic random source. Not derivable from the clinical id, and carrying no ordering.';

-- The link, in its own schema.
--
-- `dthcms_research` holds no USAGE here at all, which is what makes the anonymisation
-- structural rather than a convention. `dthcms_app` may INSERT — registration assigns the
-- research id in the same transaction as the patient — and may not SELECT: the clinical
-- application never needs to know a patient's research id, and going the other way, from
-- a research finding back to a person, is an IRB decision carried out by the owner, not a
-- query a handler can make.
CREATE SCHEMA IF NOT EXISTS identity_link;

COMMENT ON SCHEMA identity_link IS
  'The only join between a patient and their research subject. Reachable by neither the application nor research (CP28, plan §9.8).';

CREATE TABLE identity_link.research_subject (
  patient_id  uuid        PRIMARY KEY REFERENCES core.patient(id) ON DELETE RESTRICT,
  research_id text        NOT NULL UNIQUE REFERENCES research.research_subject(research_id),
  facility_id uuid        NOT NULL REFERENCES core.facility(id),
  linked_at   timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE identity_link.research_subject IS
  'patient_id ↔ research_id. Written once at registration; read only by the owner, under governance.';

GRANT USAGE ON SCHEMA identity_link TO dthcms_app;
GRANT INSERT ON identity_link.research_subject TO dthcms_app;
-- Deliberately no SELECT, no UPDATE, no DELETE, and nothing at all for dthcms_research.

-- The application writes the anonymised row too, in the same transaction, so a subject
-- cannot exist in the link without existing in research.
--
-- USAGE on the schema is granted here rather than in 00002 because until this migration
-- the application had no business in `research` at all. Without it the whole registration
-- transaction fails as the production role while passing as the owner in every test that
-- forgets to change roles — which is why `TestTheProductionRoleCanRegisterAPatient` runs
-- the real path as `dthcms_app_local`.
GRANT USAGE ON SCHEMA research TO dthcms_app;
GRANT INSERT, SELECT ON research.research_subject TO dthcms_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_research_link_sealed() RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  IF has_schema_privilege('dthcms_research', 'identity_link', 'USAGE') THEN
    RAISE EXCEPTION 'dthcms_research can reach identity_link'
      USING HINT = 'The link between a patient and a research subject is the one thing '
                   'anonymisation depends on. Research holds nothing here (plan §9.8).';
  END IF;

  IF has_table_privilege('dthcms_app', 'identity_link.research_subject', 'SELECT') THEN
    RAISE EXCEPTION 'dthcms_app can read identity_link.research_subject'
      USING HINT = 'The application assigns a research id and never needs to read one '
                   'back. Going from a research finding to a person is a governed act.';
  END IF;

  IF has_table_privilege('dthcms_app', 'identity_link.research_subject', 'DELETE')
     OR has_table_privilege('dthcms_app', 'identity_link.research_subject', 'UPDATE') THEN
    RAISE EXCEPTION 'dthcms_app can rewrite identity_link.research_subject'
      USING HINT = 'A research subject is assigned once. Re-pointing one silently '
                   'invalidates every cohort it appears in.';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_research_link_sealed() IS
  'Raises if research can reach the patient link, or if the application can read or rewrite it (CP28).';

-- The research isolation assertion from 00003, extended to the new schema. Replacing the
-- function rather than editing 00003 keeps every applied migration's checksum stable.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_research_isolated() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  reachable text;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dthcms_research') THEN
    RAISE EXCEPTION 'role dthcms_research does not exist; migration 00002 has not been applied';
  END IF;

  SELECT string_agg(s, ', ' ORDER BY s) INTO reachable
  FROM unnest(ARRAY['core', 'ledger', 'read', 'docs', 'identity_link']) AS s
  WHERE has_schema_privilege('dthcms_research', s, 'USAGE');

  IF reachable IS NOT NULL THEN
    RAISE EXCEPTION 'dthcms_research can reach identified schemas: %', reachable
      USING HINT = 'Research holds USAGE on the research schema only (implementation plan 9.8).';
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_no_plaintext_identifiers', 'patient identifiers are hashed and sealed, never readable', 57),
  ('assert_research_link_sealed',     'the patient-to-research link is reachable by neither research nor the application', 31)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- ---------------------------------------------------------------------------
-- Facility scoping
-- ---------------------------------------------------------------------------

-- research.research_subject and the counter are exempt for opposite reasons, and both are
-- recorded rather than the assertion weakened (D-61).
INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('research', 'research_subject',
   'Deliberately carries no facility_id: a uuid that appears in both core and research is a join path, which is the one thing anonymisation may not have. The facility travels as a code.')
ON CONFLICT (schema_name, table_name) DO NOTHING;

-- +goose Down

DELETE FROM ops.invariant
 WHERE function_name IN ('assert_no_plaintext_identifiers', 'assert_research_link_sealed');
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'research' AND table_name = 'research_subject';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_research_isolated() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  reachable text;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dthcms_research') THEN
    RAISE EXCEPTION 'role dthcms_research does not exist; migration 00002 has not been applied';
  END IF;
  SELECT string_agg(s, ', ' ORDER BY s) INTO reachable
  FROM unnest(ARRAY['core', 'ledger', 'read', 'docs']) AS s
  WHERE has_schema_privilege('dthcms_research', s, 'USAGE');
  IF reachable IS NOT NULL THEN
    RAISE EXCEPTION 'dthcms_research can reach identified schemas: %', reachable;
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE USAGE ON SCHEMA research FROM dthcms_app;

DROP FUNCTION IF EXISTS core.assert_research_link_sealed();
DROP FUNCTION IF EXISTS core.assert_no_plaintext_identifiers();
DROP TABLE IF EXISTS identity_link.research_subject;
DROP SCHEMA IF EXISTS identity_link;
DROP TABLE IF EXISTS research.research_subject;
DROP TABLE IF EXISTS core.patient_identifier;
DROP TRIGGER IF EXISTS patient_birth_date_plausible ON core.patient;
DROP FUNCTION IF EXISTS core.assert_plausible_birth_date();
DROP TABLE IF EXISTS core.patient;
DROP FUNCTION IF EXISTS core.next_clinical_id(uuid, smallint);
DROP TABLE IF EXISTS core.clinical_id_counter;
