-- The patient read model (CP29): what a screen shows, derived from PATIENT_REGISTERED.
--
-- Synchronous, and the reason is the registration desk itself. A patient is registered and
-- immediately walks to Anthropometry, whose operator searches for them. A read model that
-- is a second stale there is a queue stopped at the second station while somebody retypes a
-- name, which is exactly the failure §4.1 describes for vitals and is worse here because it
-- happens to every patient rather than to some.
--
-- The columns mirror the event payload one for one, deliberately: the derivation is then a
-- copy rather than a mapping, `TestTheProjectionMatchesTheEventPayloadExactly` can compare
-- them field by field, and there is no place for a field to be silently dropped between the
-- ledger and the screen.

-- +goose Up

CREATE TABLE read.patient (
  patient_id    uuid        PRIMARY KEY,
  facility_id   uuid        NOT NULL,
  clinical_id   text        NOT NULL UNIQUE,

  name_en       text        NOT NULL,
  name_bn       text        NOT NULL DEFAULT '',
  sex           text        NOT NULL,

  birth_date    date        NOT NULL,
  dob_precision text        NOT NULL,
  dob_source    text        NOT NULL,

  phone_primary   text      NOT NULL,
  phone_secondary text      NOT NULL DEFAULT '',

  division      text        NOT NULL DEFAULT '',
  district      text        NOT NULL DEFAULT '',
  upazila       text        NOT NULL DEFAULT '',
  address_line  text        NOT NULL DEFAULT '',
  postcode      text        NOT NULL DEFAULT '',

  emergency_name     text   NOT NULL DEFAULT '',
  emergency_relation text   NOT NULL DEFAULT '',
  emergency_phone    text   NOT NULL DEFAULT '',

  education_level     text,
  occupation_category text,
  income_band         text,
  household_size      smallint,
  residence_type      text,
  medicine_payer      text,

  -- Which official numbers exist, never the numbers. A screen shows "National ID on file"
  -- and asks core.patient_identifier for the mask when it needs one.
  identifier_kinds text[]   NOT NULL DEFAULT '{}',

  consent_reference text     NOT NULL,

  status        text        NOT NULL DEFAULT 'active',

  registered_at      timestamptz NOT NULL,
  registered_by      uuid        NOT NULL,
  registered_role    text        NOT NULL,
  registered_station text        NOT NULL DEFAULT '',

  event_id      uuid        NOT NULL,
  global_seq    bigint      NOT NULL
);

-- Search (CP31) and the duplicate probe (CP30) read from here, not from core.
CREATE INDEX read_patient_name_en_trgm ON read.patient USING gin (name_en gin_trgm_ops);
CREATE INDEX read_patient_name_bn_trgm ON read.patient USING gin (name_bn gin_trgm_ops);
CREATE INDEX read_patient_phone ON read.patient (phone_primary);
CREATE INDEX read_patient_by_facility ON read.patient (facility_id, registered_at DESC);
CREATE INDEX read_patient_dob_sex ON read.patient (facility_id, birth_date, sex);

COMMENT ON TABLE read.patient IS
  'The patient as a screen shows them, derived from PATIENT_REGISTERED. Synchronous projection (CP29).';
COMMENT ON COLUMN read.patient.identifier_kinds IS
  'Which official numbers are on file. Never the numbers themselves — those are sealed in core.patient_identifier (D-47).';

-- ---------------------------------------------------------------------------
-- The derivation
-- ---------------------------------------------------------------------------

-- SECURITY DEFINER for the CP03 reason: the caller inside the append transaction is
-- `dthcms_app`, which may not write to `read` and must not be given the privilege to.
-- search_path is pinned, or a caller who can create a table in an earlier schema redirects
-- the writes into their own.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_patient_registered(event jsonb)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, pg_catalog
AS $$
DECLARE
  v_seq     bigint := (event ->> 'global_seq')::bigint;
  v_patient uuid   := (event ->> 'patient_id')::uuid;
BEGIN
  IF v_seq IS NULL OR v_patient IS NULL THEN
    RAISE EXCEPTION 'read.apply_patient_registered: the event carries no global_seq or patient_id';
  END IF;

  INSERT INTO read.patient (
    patient_id, facility_id, clinical_id, name_en, name_bn, sex,
    birth_date, dob_precision, dob_source, phone_primary, phone_secondary,
    division, district, upazila, address_line, postcode,
    emergency_name, emergency_relation, emergency_phone,
    education_level, occupation_category, income_band, household_size,
    residence_type, medicine_payer, identifier_kinds, consent_reference,
    registered_at, registered_by, registered_role, registered_station,
    event_id, global_seq)
  VALUES (
    v_patient, (event ->> 'facility_id')::uuid, event ->> 'clinical_id',
    event ->> 'name_en', coalesce(event ->> 'name_bn', ''), event ->> 'sex',
    (event ->> 'birth_date')::date, event ->> 'dob_precision', event ->> 'dob_source',
    event ->> 'phone_primary', coalesce(event ->> 'phone_secondary', ''),
    coalesce(event ->> 'division', ''), coalesce(event ->> 'district', ''),
    coalesce(event ->> 'upazila', ''), coalesce(event ->> 'address_line', ''),
    coalesce(event ->> 'postcode', ''),
    coalesce(event ->> 'emergency_name', ''), coalesce(event ->> 'emergency_relation', ''),
    coalesce(event ->> 'emergency_phone', ''),
    nullif(event ->> 'education_level', ''), nullif(event ->> 'occupation_category', ''),
    nullif(event ->> 'income_band', ''), nullif(event ->> 'household_size', '')::smallint,
    nullif(event ->> 'residence_type', ''), nullif(event ->> 'medicine_payer', ''),
    coalesce(
      (SELECT array_agg(value::text ORDER BY ordinality)
         FROM jsonb_array_elements_text(coalesce(event -> 'identifier_kinds', '[]'::jsonb))
              WITH ORDINALITY AS t(value, ordinality)),
      '{}'::text[]),
    event ->> 'consent_reference',
    (event ->> 'registered_at')::timestamptz, (event ->> 'registered_by')::uuid,
    event ->> 'registered_role', coalesce(event ->> 'registered_station', ''),
    (event ->> 'event_id')::uuid, v_seq)
  -- A replay writes the same row; an older event after a newer one writes nothing. That is
  -- the whole of this projection's idempotence, and it is what makes a re-submitted
  -- registration (criterion 4) cost nothing.
  ON CONFLICT (patient_id) DO UPDATE
     SET clinical_id = excluded.clinical_id, name_en = excluded.name_en,
         name_bn = excluded.name_bn, sex = excluded.sex,
         birth_date = excluded.birth_date, dob_precision = excluded.dob_precision,
         dob_source = excluded.dob_source,
         phone_primary = excluded.phone_primary, phone_secondary = excluded.phone_secondary,
         division = excluded.division, district = excluded.district, upazila = excluded.upazila,
         address_line = excluded.address_line, postcode = excluded.postcode,
         emergency_name = excluded.emergency_name, emergency_relation = excluded.emergency_relation,
         emergency_phone = excluded.emergency_phone,
         education_level = excluded.education_level,
         occupation_category = excluded.occupation_category,
         income_band = excluded.income_band, household_size = excluded.household_size,
         residence_type = excluded.residence_type, medicine_payer = excluded.medicine_payer,
         identifier_kinds = excluded.identifier_kinds,
         consent_reference = excluded.consent_reference,
         registered_at = excluded.registered_at, registered_by = excluded.registered_by,
         registered_role = excluded.registered_role,
         registered_station = excluded.registered_station,
         event_id = excluded.event_id, global_seq = excluded.global_seq
   WHERE read.patient.global_seq < excluded.global_seq;

  UPDATE read.projection_state
     SET checkpoint = GREATEST(checkpoint, v_seq),
         applied_at = GREATEST(coalesce(applied_at, '-infinity'::timestamptz),
                               (event ->> 'recorded_at')::timestamptz),
         updated_at = now()
   WHERE name = 'patient';
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION read.apply_patient_registered(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.apply_patient_registered(jsonb) TO dthcms_app, dthcms_projector;

COMMENT ON FUNCTION read.apply_patient_registered(jsonb) IS
  'The read.patient derivation. The application may call it and may not write the table (CP29, ADR-0017).';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.reset_patient()
RETURNS void
LANGUAGE plpgsql
SET search_path = read, pg_catalog
AS $$
BEGIN
  DELETE FROM read.patient;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION read.reset_patient() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.reset_patient() TO dthcms_projector;

-- +goose Down

DROP FUNCTION IF EXISTS read.reset_patient();
DROP FUNCTION IF EXISTS read.apply_patient_registered(jsonb);
DROP TABLE IF EXISTS read.patient;
