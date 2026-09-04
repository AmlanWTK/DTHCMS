-- Patients (CP28). The registration path and the reads it needs.

-- name: NextClinicalID :one
SELECT core.next_clinical_id($1, $2)::text;

-- name: InsertPatient :one
INSERT INTO core.patient (
  facility_id, clinical_id, name_en, name_bn, sex,
  birth_date, dob_precision, dob_verified_by, dob_verified_at, dob_verified_user_id,
  phone_primary, phone_secondary,
  division, district, upazila, address_line, postcode,
  emergency_name, emergency_relation, emergency_phone,
  education_level, occupation_category, income_band, household_size, residence_type, medicine_payer,
  registered_by, registered_at)
VALUES (
  $1, $2, $3, $4, $5,
  $6, $7, $8, $9, $10,
  $11, $12,
  $13, $14, $15, $16, $17,
  $18, $19, $20,
  $21, $22, $23, $24, $25, $26,
  $27, $28)
RETURNING *;

-- name: PatientByID :one
SELECT * FROM core.patient WHERE id = $1 AND facility_id = $2;

-- name: PatientByClinicalID :one
SELECT * FROM core.patient WHERE clinical_id = $1;

-- name: InsertPatientIdentifier :one
INSERT INTO core.patient_identifier (
  facility_id, patient_id, kind, digest, sealed, key_id, masked, capture_method)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: IdentifiersForPatient :many
SELECT * FROM core.patient_identifier WHERE patient_id = $1 ORDER BY kind;

-- name: PatientByIdentifierDigest :one
-- The duplicate check with a number in hand. The unique constraint is what actually
-- prevents the duplicate; this is what lets the desk be told about it politely first.
SELECT p.* FROM core.patient p
  JOIN core.patient_identifier i ON i.patient_id = p.id
 WHERE i.facility_id = $1 AND i.kind = $2 AND i.digest = $3
   AND p.status <> 'merged';

-- name: InsertResearchSubject :exec
INSERT INTO research.research_subject (
  research_id, facility_code, enrolled_month, birth_year, sex,
  education_level, occupation_category, income_band, household_size, residence_type, medicine_payer)
VALUES (
  sqlc.arg(research_id), sqlc.arg(facility_code), sqlc.arg(enrolled_month)::date,
  sqlc.arg(birth_year), sqlc.arg(sex),
  sqlc.arg(education_level), sqlc.arg(occupation_category), sqlc.arg(income_band),
  sqlc.arg(household_size), sqlc.arg(residence_type), sqlc.arg(medicine_payer));

-- name: LinkResearchSubject :exec
-- The application may INSERT here and may not SELECT (migration 00016). Going from a
-- research finding back to a person is a governed act, not a query a handler can make.
INSERT INTO identity_link.research_subject (patient_id, research_id, facility_id)
VALUES ($1, $2, $3);

-- name: FacilityCode :one
SELECT code FROM core.facility WHERE id = $1;

-- name: ReadPatientByID :one
SELECT * FROM read.patient WHERE patient_id = $1 AND facility_id = $2;

-- name: ReadPatientByClinicalID :one
SELECT * FROM read.patient WHERE clinical_id = $1 AND facility_id = $2;

-- name: PatientByPhoneAndBirthDate :one
-- The second deterministic duplicate rule (CP30). A household shares a telephone; a
-- household does not share a telephone *and* an exact date of birth.
SELECT p.* FROM core.patient p
 WHERE p.facility_id = $1 AND p.phone_primary = $2 AND p.birth_date = $3
   AND p.status <> 'merged'
 ORDER BY p.registered_at
 LIMIT 1;

-- name: MatchCandidates :many
-- The blocking query for the probabilistic pass: everyone this registration could plausibly
-- be, narrowed cheaply so that scoring runs over a handful of rows rather than the register.
--
-- Four blocks, unioned: the same phonetic key, a similar phonetic key, a similar name in
-- either script, and the same birth date. Each is index-backed. The scoring in Go then
-- decides; this only has to not miss.
SELECT DISTINCT ON (patient_id)
       patient_id, clinical_id, name_en, name_bn, name_key_en, sex,
       birth_date, phone_primary, district, upazila, registered_at
  FROM read.patient
 WHERE facility_id = @facility_id
   AND status <> 'merged'
   AND (
        (@name_key_en::text <> '' AND name_key_en = @name_key_en)
     OR (@name_key_en::text <> '' AND name_key_en % @name_key_en)
     OR (@name_en::text <> '' AND name_en % @name_en)
     OR (@name_bn::text <> '' AND name_bn <> '' AND name_bn % @name_bn)
     OR (@phone::text <> '' AND phone_primary = @phone)
     OR birth_date = @birth_date
   )
 LIMIT 200;

-- name: InsertPatientMerge :exec
INSERT INTO core.patient_merge (
  facility_id, survivor_id, merged_id, score, decision, justification,
  candidates, merged_by, event_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: MarkPatientMerged :exec
UPDATE core.patient
   SET status = 'merged', merged_into_id = $2, status_reason = $3, updated_at = now()
 WHERE id = $1 AND facility_id = $4;

-- name: MergesForSurvivor :many
SELECT * FROM core.patient_merge WHERE survivor_id = $1 ORDER BY merged_at;

-- name: SurvivingPatient :one
SELECT read.surviving_patient($1)::uuid;

-- name: CountTodaysPatients :one
-- The today's-patients fast path (CP31). Every station uses it dozens of times an hour, and
-- it must never become a scan of the register.
SELECT count(*) FROM read.patient
 WHERE facility_id = $1
   AND registered_at >= $2 AND registered_at < $3
   AND status <> 'merged';

-- name: TodaysPatients :many
SELECT patient_id, clinical_id, name_en, name_bn, sex, birth_date, dob_precision,
       phone_primary, district, upazila, status, merged_into_id, registered_at
  FROM read.patient
 WHERE facility_id = $1
   AND registered_at >= $2 AND registered_at < $3
   AND status <> 'merged'
 ORDER BY registered_at DESC
 LIMIT $4;

-- name: PatientsByClinicalID :many
-- The exact-handle route (CP31). A clinical id, whole or as the six digits somebody reads
-- off a card. Two index lookups, no trigram work: an exact handle is not a guess, and
-- making it pay for fuzzy name matching spends most of the search budget on the one route
-- that should be instant.
SELECT patient_id, clinical_id, name_en, name_bn, sex, birth_date, dob_precision,
       phone_primary, district, upazila, status, merged_into_id, registered_at
  FROM read.patient
 WHERE facility_id = @facility_id
   AND (@include_merged::boolean OR status <> 'merged')
   AND (clinical_id = @clinical_id::text
        OR (@serial::text <> '' AND right(clinical_id, 6) = @serial::text))
 ORDER BY registered_at DESC
 LIMIT @page_size::int;

-- name: PatientsByPhone :many
SELECT patient_id, clinical_id, name_en, name_bn, sex, birth_date, dob_precision,
       phone_primary, district, upazila, status, merged_into_id, registered_at
  FROM read.patient
 WHERE facility_id = @facility_id
   AND (@include_merged::boolean OR status <> 'merged')
   AND phone_primary = @phone::text
 ORDER BY registered_at DESC
 LIMIT @page_size::int;

-- name: PatientsByName :many
-- The fuzzy route. Trigram indexes on both name columns and on the phonetic key, ranked by
-- the best of them.
--
-- `bangla` and `latin` say which scripts the term is written in, decided in Go. Without
-- them a Latin search still pays to compute `similarity(name_bn, 'Rahim')` for every
-- matching row — a comparison that can never be above zero — and at fifty thousand patients
-- that is a measurable share of the search budget (CP31).
SELECT patient_id, clinical_id, name_en, name_bn, sex, birth_date, dob_precision,
       phone_primary, district, upazila, status, merged_into_id, registered_at,
       GREATEST(
         CASE WHEN @name_key::text <> '' AND name_key_en = @name_key::text THEN 0.92 ELSE 0 END,
         CASE WHEN @latin::boolean THEN similarity(name_en, @term::text) ELSE 0 END,
         CASE WHEN @bangla::boolean AND name_bn <> '' THEN similarity(name_bn, @term::text) ELSE 0 END,
         CASE WHEN @latin::boolean AND @name_key::text <> '' AND name_key_en <> ''
              THEN similarity(name_key_en, @name_key::text) * 0.9 ELSE 0 END
       )::real AS rank
  FROM read.patient
 WHERE facility_id = @facility_id
   AND (@include_merged::boolean OR status <> 'merged')
   AND (
        (@latin::boolean AND name_en % @term::text)
     OR (@bangla::boolean AND name_bn <> '' AND name_bn % @term::text)
     OR (@latin::boolean AND @name_key::text <> '' AND name_key_en <> ''
         AND name_key_en % @name_key::text)
   )
 ORDER BY rank DESC, registered_at DESC, patient_id
 LIMIT @page_size::int OFFSET @page_offset::int;

-- name: InsertPatientPhoto :one
INSERT INTO core.patient_photo (
  facility_id, patient_id, object_class, object_key, content_type, byte_size, sha256,
  width, height, captured_by, captured_at, device_id, event_id, replaces_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING *;

-- name: RetirePatientPhoto :exec
UPDATE core.patient_photo SET replaced_at = $2 WHERE id = $1 AND replaced_at IS NULL;

-- name: CurrentPatientPhoto :one
SELECT * FROM core.patient_photo
 WHERE patient_id = $1 AND facility_id = $2 AND replaced_at IS NULL;

-- name: PatientPhotoHistory :many
SELECT * FROM core.patient_photo
 WHERE patient_id = $1 AND facility_id = $2
 ORDER BY captured_at DESC;

-- name: PatientCorrections :many
SELECT * FROM read.patient_correction
 WHERE patient_id = $1 AND facility_id = $2
 ORDER BY corrected_at DESC, id DESC;

-- name: DerivedDependencies :many
-- What a correction to a field invalidates (CP35). Read from the register rather than
-- from a list in the code, so a checkpoint that adds a derived value adds a row and the
-- correction path picks it up without being edited.
SELECT derived_name, depends_on, action, description
  FROM ops.derived_dependency
 WHERE depends_on = ANY(@fields::text[])
 ORDER BY derived_name, depends_on;

-- name: CorrectPatient :exec
-- The write side of a demographic correction (CP35). Every field is supplied — the caller
-- has already merged what changed with what did not — so this cannot half-apply.
UPDATE core.patient SET
  name_en = $3, name_bn = $4, sex = $5,
  birth_date = $6, dob_precision = $7, dob_verified_by = $8,
  phone_primary = $9, phone_secondary = $10,
  division = $11, district = $12, upazila = $13, address_line = $14, postcode = $15,
  updated_at = now()
WHERE id = $1 AND facility_id = $2;
