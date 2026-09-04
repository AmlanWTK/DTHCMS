-- Demographic corrections (CP35, blueprint §4.3).
--
-- §4.3's correction principle applies to demographics as much as to clinical values, and
-- the date of birth is the reason it has to: a wrong one changes every pediatric percentile
-- ever computed for that patient, and those numbers have already been read, acted on and in
-- some cases printed.
--
-- So a correction is an event, never an overwrite. This table is the *derived* history —
-- what a screen shows when somebody asks "why does this record say something different from
-- the letter I have" — and it is rebuilt from the ledger like any other read model. The
-- original values live in `ledger.event` and are retrievable forever (criterion 1).

-- +goose Up

CREATE TABLE read.patient_correction (
  id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  patient_id  uuid        NOT NULL,
  facility_id uuid        NOT NULL,

  field       text        NOT NULL,
  -- Rendered, not typed: this is a history for a person to read, and "1985-06-14" is what
  -- they need to see whatever the column's type is.
  previous    text        NOT NULL,
  current     text        NOT NULL,

  -- Required, free text. "Correction" is not a reason; "the NID card says 1985, the
  -- registration desk typed 1958" is.
  reason      text        NOT NULL,
  -- True for the fields that change what has already been computed. A high-impact
  -- correction is the one somebody will come looking for.
  high_impact boolean     NOT NULL DEFAULT false,

  corrected_by      uuid        NOT NULL,
  corrected_by_code text        NOT NULL DEFAULT '',
  corrected_at      timestamptz NOT NULL,
  event_id          uuid        NOT NULL,
  global_seq        bigint      NOT NULL,

  -- One row per field per event. A correction that changes three fields is three rows and
  -- one event, which is what makes "what changed" a query rather than a JSON parse.
  CONSTRAINT patient_correction_once UNIQUE (event_id, field)
);

CREATE INDEX read_patient_correction_by_patient
  ON read.patient_correction (patient_id, corrected_at DESC);
CREATE INDEX read_patient_correction_high_impact
  ON read.patient_correction (facility_id, corrected_at DESC) WHERE high_impact;

COMMENT ON TABLE read.patient_correction IS
  'The demographic change history, derived from PATIENT_DEMOGRAPHICS_CORRECTED. The originals are in the ledger (CP35).';

-- ---------------------------------------------------------------------------
-- Derived values, and what depends on what
-- ---------------------------------------------------------------------------

-- A register of which derived values depend on which demographic field.
--
-- The plan's own risk note asks for this: "enumerate dependent values explicitly rather
-- than relying on memory". A table rather than a comment, because the failure it prevents
-- is a checkpoint two years from now adding a growth chart and nobody remembering that a
-- date-of-birth correction has to invalidate it — and the symptom is a percentile that is
-- quietly wrong for one patient, which nothing announces.
CREATE TABLE ops.derived_dependency (
  derived_name text        NOT NULL,
  depends_on   text        NOT NULL,
  -- What must happen when the field changes: 'recompute' (the value is a function of the
  -- field and can be rebuilt) or 'review' (a person has to look, because the old value was
  -- acted on).
  action       text        NOT NULL CHECK (action IN ('recompute', 'review')),
  description  text        NOT NULL,
  added_at     timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (derived_name, depends_on)
);

COMMENT ON TABLE ops.derived_dependency IS
  'Which derived values depend on which demographic fields, so a correction knows what it invalidates (CP35).';

INSERT INTO ops.derived_dependency (derived_name, depends_on, action, description) VALUES
  ('read.patient', 'birth_date', 'recompute',
   'The patient read model carries the date and its precision; both are copied from the event.'),
  ('read.patient', 'name_en', 'recompute',
   'The search key is computed from the English name and must be rebuilt with it.'),
  ('read.patient', 'name_bn', 'recompute', 'Trigram search reads the Bangla name directly.'),
  ('read.patient', 'phone_primary', 'recompute', 'Duplicate detection matches on the normalised number.'),
  ('research.research_subject', 'birth_date', 'recompute',
   'The anonymised row carries the birth year, which is a cohorting variable.'),
  ('research.research_subject', 'sex', 'recompute', 'A cohorting variable.')
ON CONFLICT (derived_name, depends_on) DO NOTHING;

-- ---------------------------------------------------------------------------
-- The derivation
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_patient_corrected(event jsonb)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, pg_catalog
AS $$
DECLARE
  v_seq     bigint := (event ->> 'global_seq')::bigint;
  v_patient uuid   := (event ->> 'patient_id')::uuid;
  v_change  jsonb;
BEGIN
  IF v_seq IS NULL OR v_patient IS NULL THEN
    RAISE EXCEPTION 'read.apply_patient_corrected: the event is missing global_seq or patient_id';
  END IF;

  -- The history, one row per changed field.
  FOR v_change IN SELECT * FROM jsonb_array_elements(coalesce(event -> 'changes', '[]'::jsonb))
  LOOP
    INSERT INTO read.patient_correction (
      patient_id, facility_id, field, previous, current, reason, high_impact,
      corrected_by, corrected_by_code, corrected_at, event_id, global_seq)
    VALUES (
      v_patient, (event ->> 'facility_id')::uuid,
      v_change ->> 'field', coalesce(v_change ->> 'previous', ''), coalesce(v_change ->> 'current', ''),
      event ->> 'reason', coalesce((event ->> 'high_impact')::boolean, false),
      (event ->> 'corrected_by')::uuid, coalesce(event ->> 'corrected_by_code', ''),
      (event ->> 'corrected_at')::timestamptz, (event ->> 'event_id')::uuid, v_seq)
    ON CONFLICT (event_id, field) DO NOTHING;
  END LOOP;

  -- The read model itself, field by field. Only what the event says changed, so a
  -- correction of one field cannot silently rewrite another.
  UPDATE read.patient SET
    name_en       = coalesce(event ->> 'name_en', name_en),
    name_bn       = coalesce(event ->> 'name_bn', name_bn),
    name_key_en   = coalesce(event ->> 'name_key_en', name_key_en),
    sex           = coalesce(event ->> 'sex', sex),
    birth_date    = coalesce((event ->> 'birth_date')::date, birth_date),
    dob_precision = coalesce(event ->> 'dob_precision', dob_precision),
    dob_source    = coalesce(event ->> 'dob_source', dob_source),
    phone_primary   = coalesce(event ->> 'phone_primary', phone_primary),
    phone_secondary = coalesce(event ->> 'phone_secondary', phone_secondary),
    division      = coalesce(event ->> 'division', division),
    district      = coalesce(event ->> 'district', district),
    upazila       = coalesce(event ->> 'upazila', upazila),
    address_line  = coalesce(event ->> 'address_line', address_line),
    postcode      = coalesce(event ->> 'postcode', postcode),
    event_id      = (event ->> 'event_id')::uuid,
    global_seq    = v_seq
  WHERE patient_id = v_patient AND global_seq < v_seq;

  -- The anonymised cohort row follows the birth year and the sex, or a research query runs
  -- against a value the clinical record no longer holds.
  UPDATE research.research_subject rs
     SET birth_year = coalesce(extract(year from (event ->> 'birth_date')::date)::smallint, rs.birth_year),
         sex        = coalesce(event ->> 'sex', rs.sex)
    FROM identity_link.research_subject link
   WHERE link.patient_id = v_patient AND rs.research_id = link.research_id;

  UPDATE read.projection_state
     SET checkpoint = GREATEST(checkpoint, v_seq),
         applied_at = GREATEST(coalesce(applied_at, '-infinity'::timestamptz),
                               (event ->> 'recorded_at')::timestamptz),
         updated_at = now()
   WHERE name = 'patient';
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION read.apply_patient_corrected(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.apply_patient_corrected(jsonb) TO dthcms_app, dthcms_projector;

-- The rebuild has to empty this too, or a replay doubles the history.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.reset_patient()
RETURNS void
LANGUAGE plpgsql
SET search_path = read, pg_catalog
AS $$
BEGIN
  DELETE FROM read.patient_correction;
  DELETE FROM read.patient;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION read.reset_patient() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.reset_patient() TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariant
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_corrections_are_explained() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  SELECT count(*) INTO offending
  FROM read.patient_correction
  WHERE length(btrim(reason)) < 10 OR previous = current;

  IF offending > 0 THEN
    RAISE EXCEPTION 'demographic corrections with no reason or no change: % row(s)', offending
      USING HINT = 'A correction records who, when and why. A no-op correction is a history '
                   'entry that tells a reader nothing (CP35).';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_corrections_are_explained() IS
  'Raises if a demographic correction has no usable reason or changed nothing (CP35).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_corrections_are_explained', 'every demographic correction says why and changed something', 34)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('ops', 'derived_dependency',
   'A register of which derived values depend on which fields. It describes the software, not a facility''s data.')
ON CONFLICT DO NOTHING;

-- +goose Down

DELETE FROM core.facility_scope_exemption WHERE schema_name = 'ops' AND table_name = 'derived_dependency';
DELETE FROM ops.invariant WHERE function_name = 'assert_corrections_are_explained';
DROP FUNCTION IF EXISTS core.assert_corrections_are_explained();
DROP FUNCTION IF EXISTS read.apply_patient_corrected(jsonb);
DROP TABLE IF EXISTS ops.derived_dependency;
DROP TABLE IF EXISTS read.patient_correction;

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
