-- Consent (CP36, blueprint §15.1, §11.2, §12; decision D-02).
--
-- The engine, not the wording. D-02 is deferred to Dr. Nahid and counsel, and the wording is
-- theirs; what is built here is everything that has to be true whatever the words turn out to
-- be, arranged so that loading the approved text later is an INSERT and not a migration.
--
-- Four decisions are structural rather than conventional, and each is here because the
-- alternative fails silently:
--
--   **Layered, not blanket.** Five consent types, each independently grantable and
--   revocable (D-02 option ii). A patient who wants treatment but not an SMS at seven in the
--   morning is expressing a preference a single "I consent" box cannot record.
--
--   **Versioned.** Every consent record stores the template version, the language it was
--   shown in and the digest of the exact text. "The patient consented to research" is not an
--   answer anybody can act on in 2031; "was shown research consent v3 in Bangla on 14
--   September 2026 and gave a thumbprint, witnessed by REG-04" is.
--
--   **Enforced at the point of use.** §15.1's whole point. Research is the boundary that
--   exists today, and it is enforced by *privilege*: `dthcms_research` loses SELECT on
--   `research.research_subject` and is given a view that filters on live consent. A
--   researcher cannot query a non-consenting subject even by writing the query themselves,
--   which is a different guarantee from an ETL that remembers to filter.
--
--   **Revocation is an event.** Never an UPDATE of the grant. Both are needed to answer
--   "was this message lawful when it was sent", which is the question that actually gets
--   asked.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The templates
-- ---------------------------------------------------------------------------

CREATE TABLE core.consent_template (
  id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  consent_type text        NOT NULL CHECK (consent_type IN
                 ('care', 'communication', 'research', 'ai_processing', 'outreach')),
  version      integer     NOT NULL CHECK (version >= 1),
  language     text        NOT NULL CHECK (language IN ('en', 'bn')),

  title        text        NOT NULL CHECK (btrim(title) <> ''),
  -- The text as it is shown, in full. Not a key into a translation file: a translation file
  -- is edited, and what a patient was shown must be retrievable exactly.
  body         text        NOT NULL CHECK (btrim(body) <> ''),
  -- SHA-256 of `body`, in hex. Carried into the event, so a row replaced by somebody with
  -- database access is detectable from the ledger rather than only from this table.
  body_digest  text        NOT NULL CHECK (body_digest ~ '^[0-9a-f]{64}$'),

  -- draft   — being written; may be edited and may not be consented against.
  -- active  — in use. Immutable from here on, enforced by trigger.
  -- retired — superseded. Still readable forever, because consents point at it.
  status       text        NOT NULL DEFAULT 'draft'
                 CHECK (status IN ('draft', 'active', 'retired')),

  effective_from timestamptz,
  retired_at     timestamptz,

  created_at   timestamptz NOT NULL DEFAULT now(),
  created_by   uuid        REFERENCES core.app_user(id),

  CONSTRAINT consent_template_version_per_language UNIQUE (consent_type, version, language),
  CONSTRAINT consent_template_active_has_a_date
    CHECK (status = 'draft' OR effective_from IS NOT NULL),
  CONSTRAINT consent_template_retired_has_a_date
    CHECK (status <> 'retired' OR retired_at IS NOT NULL)
);

COMMENT ON TABLE core.consent_template IS
  'The bilingual, versioned wording a consent is taken against. Immutable once active (CP36, D-02).';
COMMENT ON COLUMN core.consent_template.body_digest IS
  'SHA-256 of the body. Travels into CONSENT_GRANTED so a later edit of this row is detectable.';

-- Only one active version per type and language at a time. Two active versions means two
-- patients on the same morning consented to different words with no way to tell which.
CREATE UNIQUE INDEX consent_template_one_active
  ON core.consent_template (consent_type, language)
  WHERE status = 'active';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.consent_template_immutable()
RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'draft' THEN
    IF NEW.body <> OLD.body OR NEW.title <> OLD.title
       OR NEW.body_digest <> OLD.body_digest OR NEW.version <> OLD.version
       OR NEW.consent_type <> OLD.consent_type OR NEW.language <> OLD.language THEN
      RAISE EXCEPTION 'consent template %/% v% is % and cannot be edited',
        OLD.consent_type, OLD.language, OLD.version, OLD.status
        USING HINT = 'A patient consented to these exact words. Publish a new version '
                     'instead; the old one stays readable forever (CP36).';
    END IF;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER consent_template_immutable
  BEFORE UPDATE ON core.consent_template
  FOR EACH ROW EXECUTE FUNCTION core.consent_template_immutable();

-- A template is never deleted. A consent points at a version, and a version that vanishes
-- turns a recorded consent into a consent to nothing.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.consent_template_undeletable()
RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'consent templates are never deleted; retire them'
    USING HINT = 'Consents recorded against this version would stop being readable (CP36).';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER consent_template_undeletable
  BEFORE DELETE ON core.consent_template
  FOR EACH ROW EXECUTE FUNCTION core.consent_template_undeletable();

GRANT SELECT ON core.consent_template TO dthcms_app;
-- Publishing wording is an administrative act done by the owner, not by a request handler.
-- The application reads templates and never writes them.

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'consent_template',
   'The wording is legal text for the deployment, not a facility''s data. One set of words, every site.')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The current state, per patient per type
-- ---------------------------------------------------------------------------

CREATE TABLE read.patient_consent (
  patient_id   uuid        NOT NULL,
  facility_id  uuid        NOT NULL,
  consent_type text        NOT NULL CHECK (consent_type IN
                 ('care', 'communication', 'research', 'ai_processing', 'outreach')),

  status       text        NOT NULL CHECK (status IN ('granted', 'revoked')),

  template_version integer NOT NULL,
  language         text    NOT NULL,
  template_digest  text    NOT NULL,

  capture_method   text    NOT NULL,
  evidence_key     text    NOT NULL DEFAULT '',
  evidence_sha256  text    NOT NULL DEFAULT '',
  paper_reference  text    NOT NULL DEFAULT '',

  witnessed_by      uuid,
  witnessed_by_code text   NOT NULL DEFAULT '',

  granted_for_relation text NOT NULL DEFAULT '',
  granted_for_name     text NOT NULL DEFAULT '',

  granted_at      timestamptz NOT NULL,
  granted_by      uuid        NOT NULL,
  granted_by_code text        NOT NULL DEFAULT '',

  revoked_at      timestamptz,
  revoked_by      uuid,
  revoked_by_code text        NOT NULL DEFAULT '',
  revoke_reason   text        NOT NULL DEFAULT '',
  revoke_requested_by text    NOT NULL DEFAULT '',

  event_id   uuid   NOT NULL,
  global_seq bigint NOT NULL,

  PRIMARY KEY (patient_id, consent_type),

  CONSTRAINT patient_consent_revoked_has_a_time
    CHECK (status = 'granted' OR revoked_at IS NOT NULL)
);

CREATE INDEX read_patient_consent_by_type
  ON read.patient_consent (facility_id, consent_type, status);

COMMENT ON TABLE read.patient_consent IS
  'What is true now, per patient per consent type. Derived from CONSENT_GRANTED/CONSENT_REVOKED (CP36).';

-- Every grant and every revocation, in order. The current state above answers "may I send
-- this now"; this answers "was that send lawful in March", which is the question a
-- complaint actually asks.
CREATE TABLE read.patient_consent_event (
  id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  patient_id   uuid        NOT NULL,
  facility_id  uuid        NOT NULL,
  consent_type text        NOT NULL,
  action       text        NOT NULL CHECK (action IN ('granted', 'revoked')),

  template_version integer,
  language         text    NOT NULL DEFAULT '',
  capture_method   text    NOT NULL DEFAULT '',
  reason           text    NOT NULL DEFAULT '',
  requested_by     text    NOT NULL DEFAULT '',

  actor_id   uuid        NOT NULL,
  actor_code text        NOT NULL DEFAULT '',
  occurred_at timestamptz NOT NULL,

  event_id   uuid   NOT NULL,
  global_seq bigint NOT NULL,

  CONSTRAINT patient_consent_event_once UNIQUE (event_id)
);

CREATE INDEX read_patient_consent_event_by_patient
  ON read.patient_consent_event (patient_id, occurred_at DESC);

COMMENT ON TABLE read.patient_consent_event IS
  'Every grant and revocation in order, so "was this lawful at the time" is answerable (CP36).';

-- ---------------------------------------------------------------------------
-- Research: enforcement by privilege, not by remembering to filter
-- ---------------------------------------------------------------------------

-- The anonymised row learns whether its subject consented to being in it. Written by the
-- consent projection, which crosses `identity_link` — the one place that crossing is
-- allowed, and only because the function is SECURITY DEFINER and no role holds it directly.
ALTER TABLE research.research_subject
  ADD COLUMN research_consent boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN research.research_subject.research_consent IS
  'Opt-in, never assumed. False until a RESEARCH consent is granted, false again on revocation (CP36, D-02).';

-- What a researcher actually queries.
CREATE VIEW research.cohort AS
  SELECT research_id, facility_code, enrolled_month, birth_year, sex,
         education_level, occupation_category, income_band, household_size,
         residence_type, medicine_payer, created_at
    FROM research.research_subject
   WHERE research_consent;

COMMENT ON VIEW research.cohort IS
  'The consenting subjects, and the only thing dthcms_research may read. Consent is enforced by privilege (CP36).';

-- The substitution. Research keeps a table's worth of access and loses the ability to see
-- anybody who did not agree to be seen.
REVOKE SELECT ON research.research_subject FROM dthcms_research;
GRANT SELECT ON research.cohort TO dthcms_research;

-- 00002's default privileges on future tables in `research` are deliberately left alone.
-- They are what lets a mart be added without a grant, and a mart is an aggregate — the
-- per-subject rows live in exactly one table, and that table is the one just revoked.
--
-- Which leaves one rule that privilege cannot enforce and a reviewer has to: **a mart is
-- built from `research.cohort`, never from `research.research_subject`.** The projector
-- and the owner can both reach the base table, so a mart written against it would compile
-- and would quietly include people who withdrew.

GRANT UPDATE (research_consent) ON research.research_subject TO dthcms_app;

-- ---------------------------------------------------------------------------
-- The derivations
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_consent_granted(event jsonb)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, pg_catalog
AS $$
DECLARE
  v_seq     bigint := (event ->> 'global_seq')::bigint;
  v_patient uuid   := (event ->> 'patient_id')::uuid;
  v_type    text   := event ->> 'consent_type';
BEGIN
  IF v_seq IS NULL OR v_patient IS NULL OR v_type IS NULL THEN
    RAISE EXCEPTION 'read.apply_consent_granted: the event is missing global_seq, patient_id or consent_type';
  END IF;

  INSERT INTO read.patient_consent (
    patient_id, facility_id, consent_type, status,
    template_version, language, template_digest,
    capture_method, evidence_key, evidence_sha256, paper_reference,
    witnessed_by, witnessed_by_code, granted_for_relation, granted_for_name,
    granted_at, granted_by, granted_by_code, event_id, global_seq)
  VALUES (
    v_patient, (event ->> 'facility_id')::uuid, v_type, 'granted',
    (event ->> 'template_version')::integer, event ->> 'language', event ->> 'template_digest',
    event ->> 'capture_method', coalesce(event ->> 'evidence_key', ''),
    coalesce(event ->> 'evidence_sha256', ''), coalesce(event ->> 'paper_reference', ''),
    nullif(event ->> 'witnessed_by', '')::uuid,
    -- The witness's employee code, resolved here rather than carried in the event. The
    -- durable fact is the user id; the code is a rendering, and a person whose code changes
    -- should not leave an old one printed on a consent from last year.
    coalesce((SELECT employee_code FROM core.app_user
               WHERE id = nullif(event ->> 'witnessed_by', '')::uuid), ''),
    coalesce(event ->> 'granted_for_relation', ''), coalesce(event ->> 'granted_for_name', ''),
    (event ->> 'occurred_at')::timestamptz, (event ->> 'actor_id')::uuid,
    coalesce(event ->> 'actor_code', ''), (event ->> 'event_id')::uuid, v_seq)
  ON CONFLICT (patient_id, consent_type) DO UPDATE SET
    status               = 'granted',
    template_version     = excluded.template_version,
    language             = excluded.language,
    template_digest      = excluded.template_digest,
    capture_method       = excluded.capture_method,
    evidence_key         = excluded.evidence_key,
    evidence_sha256      = excluded.evidence_sha256,
    paper_reference      = excluded.paper_reference,
    witnessed_by         = excluded.witnessed_by,
    witnessed_by_code    = excluded.witnessed_by_code,
    granted_for_relation = excluded.granted_for_relation,
    granted_for_name     = excluded.granted_for_name,
    granted_at           = excluded.granted_at,
    granted_by           = excluded.granted_by,
    granted_by_code      = excluded.granted_by_code,
    -- A re-grant clears the revocation. The history keeps both.
    revoked_at           = NULL,
    revoked_by           = NULL,
    revoked_by_code      = '',
    revoke_reason        = '',
    revoke_requested_by  = '',
    event_id             = excluded.event_id,
    global_seq           = excluded.global_seq
  WHERE read.patient_consent.global_seq < excluded.global_seq;

  INSERT INTO read.patient_consent_event (
    patient_id, facility_id, consent_type, action, template_version, language,
    capture_method, actor_id, actor_code, occurred_at, event_id, global_seq)
  VALUES (
    v_patient, (event ->> 'facility_id')::uuid, v_type, 'granted',
    (event ->> 'template_version')::integer, event ->> 'language', event ->> 'capture_method',
    (event ->> 'actor_id')::uuid, coalesce(event ->> 'actor_code', ''),
    (event ->> 'occurred_at')::timestamptz, (event ->> 'event_id')::uuid, v_seq)
  ON CONFLICT (event_id) DO NOTHING;

  IF v_type = 'research' THEN
    UPDATE research.research_subject rs
       SET research_consent = true
      FROM identity_link.research_subject link
     WHERE link.patient_id = v_patient AND rs.research_id = link.research_id;
  END IF;

  UPDATE read.projection_state
     SET checkpoint = GREATEST(checkpoint, v_seq),
         applied_at = GREATEST(coalesce(applied_at, '-infinity'::timestamptz),
                               (event ->> 'recorded_at')::timestamptz),
         updated_at = now()
   WHERE name = 'patient';
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_consent_revoked(event jsonb)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, pg_catalog
AS $$
DECLARE
  v_seq     bigint := (event ->> 'global_seq')::bigint;
  v_patient uuid   := (event ->> 'patient_id')::uuid;
  v_type    text   := event ->> 'consent_type';
BEGIN
  IF v_seq IS NULL OR v_patient IS NULL OR v_type IS NULL THEN
    RAISE EXCEPTION 'read.apply_consent_revoked: the event is missing global_seq, patient_id or consent_type';
  END IF;

  UPDATE read.patient_consent SET
    status              = 'revoked',
    revoked_at          = (event ->> 'occurred_at')::timestamptz,
    revoked_by          = (event ->> 'actor_id')::uuid,
    revoked_by_code     = coalesce(event ->> 'actor_code', ''),
    revoke_reason       = coalesce(event ->> 'reason', ''),
    revoke_requested_by = coalesce(event ->> 'requested_by', ''),
    event_id            = (event ->> 'event_id')::uuid,
    global_seq          = v_seq
  WHERE patient_id = v_patient AND consent_type = v_type AND global_seq < v_seq;

  INSERT INTO read.patient_consent_event (
    patient_id, facility_id, consent_type, action, reason, requested_by,
    actor_id, actor_code, occurred_at, event_id, global_seq)
  VALUES (
    v_patient, (event ->> 'facility_id')::uuid, v_type, 'revoked',
    coalesce(event ->> 'reason', ''), coalesce(event ->> 'requested_by', ''),
    (event ->> 'actor_id')::uuid, coalesce(event ->> 'actor_code', ''),
    (event ->> 'occurred_at')::timestamptz, (event ->> 'event_id')::uuid, v_seq)
  ON CONFLICT (event_id) DO NOTHING;

  -- Immediately, in the same transaction as the event. A revocation that propagates on a
  -- schedule is a revocation with a window in which the clinic is still doing the thing.
  IF v_type = 'research' THEN
    UPDATE research.research_subject rs
       SET research_consent = false
      FROM identity_link.research_subject link
     WHERE link.patient_id = v_patient AND rs.research_id = link.research_id;
  END IF;

  UPDATE read.projection_state
     SET checkpoint = GREATEST(checkpoint, v_seq),
         applied_at = GREATEST(coalesce(applied_at, '-infinity'::timestamptz),
                               (event ->> 'recorded_at')::timestamptz),
         updated_at = now()
   WHERE name = 'patient';
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION read.apply_consent_granted(jsonb) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION read.apply_consent_revoked(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.apply_consent_granted(jsonb) TO dthcms_app, dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_consent_revoked(jsonb) TO dthcms_app, dthcms_projector;

-- A rebuild empties consent too, and puts the cohort flag back to its opt-in default.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.reset_patient()
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, pg_catalog
AS $$
BEGIN
  DELETE FROM read.patient_consent_event;
  DELETE FROM read.patient_consent;
  UPDATE research.research_subject SET research_consent = false WHERE research_consent;
  DELETE FROM read.patient_correction;
  DELETE FROM read.patient;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION read.reset_patient() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.reset_patient() TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_research_needs_consent() RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  IF has_table_privilege('dthcms_research', 'research.research_subject', 'SELECT') THEN
    RAISE EXCEPTION 'dthcms_research can read research.research_subject directly'
      USING HINT = 'Research reads research.cohort, which filters on live consent. Direct '
                   'access to the base table is the ability to query somebody who said no '
                   '(CP36, D-02).';
  END IF;

  IF NOT has_table_privilege('dthcms_research', 'research.cohort', 'SELECT') THEN
    RAISE EXCEPTION 'dthcms_research cannot read research.cohort'
      USING HINT = 'Research has to be able to do its work; the filter is the point, not '
                   'the refusal (CP36).';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_research_needs_consent() IS
  'Raises if research can reach a subject who did not consent to being in the cohort (CP36).';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_consents_are_versioned() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  SELECT count(*) INTO offending
  FROM read.patient_consent
  WHERE template_version < 1
     OR template_digest !~ '^[0-9a-f]{64}$'
     OR language NOT IN ('en', 'bn');

  IF offending > 0 THEN
    RAISE EXCEPTION 'consents with no version, digest or language: % row(s)', offending
      USING HINT = 'A consent that does not say which words were shown, in which language, '
                   'is a consent to nothing in particular (CP36, D-02).';
  END IF;

  SELECT count(*) INTO offending
  FROM read.patient_consent
  WHERE capture_method IN ('signature', 'thumbprint')
    AND (evidence_key = '' OR evidence_sha256 = '');

  IF offending > 0 THEN
    RAISE EXCEPTION 'signature or thumbprint consents with no evidence: % row(s)', offending
      USING HINT = 'The image is the evidence. A capture method claiming one that is not '
                   'there is worse than an honest verbal attestation (CP36).';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_consents_are_versioned() IS
  'Raises if a recorded consent does not say which words, in which language, with what evidence (CP36).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_research_needs_consent', 'research can only read subjects who consented to being in the cohort', 35),
  ('assert_consents_are_versioned', 'every consent names its template version, language and evidence', 36)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN
  ('assert_research_needs_consent', 'assert_consents_are_versioned');
DROP FUNCTION IF EXISTS core.assert_consents_are_versioned();
DROP FUNCTION IF EXISTS core.assert_research_needs_consent();
DROP FUNCTION IF EXISTS read.apply_consent_granted(jsonb);
DROP FUNCTION IF EXISTS read.apply_consent_revoked(jsonb);

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

DROP VIEW IF EXISTS research.cohort;
GRANT SELECT ON research.research_subject TO dthcms_research;
ALTER TABLE research.research_subject DROP COLUMN IF EXISTS research_consent;

DROP TABLE IF EXISTS read.patient_consent_event;
DROP TABLE IF EXISTS read.patient_consent;

DROP TRIGGER IF EXISTS consent_template_undeletable ON core.consent_template;
DROP TRIGGER IF EXISTS consent_template_immutable ON core.consent_template;
DROP FUNCTION IF EXISTS core.consent_template_undeletable();
DROP FUNCTION IF EXISTS core.consent_template_immutable();
DELETE FROM core.facility_scope_exemption WHERE schema_name = 'core' AND table_name = 'consent_template';
DROP TABLE IF EXISTS core.consent_template;
