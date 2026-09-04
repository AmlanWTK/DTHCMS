-- Duplicate detection and merging (CP30, blueprint §3 Step 1).
--
-- Duplicate patients destroy longitudinal history, corrupt research cohorts, and are the
-- most common data-quality failure in clinic systems. They are also close to unfixable
-- after the fact, which is why the schema is built for detection *before* creation and for
-- merging that keeps everything.
--
-- Two decisions are here rather than in the code, because they must hold whatever a future
-- handler does:
--
-- **A merge is a redirect, never a delete.** The losing record stays, its status becomes
-- 'merged', and `merged_into_id` points at the survivor. Every event ever written against
-- it still names it, with its original attribution; `read.surviving_patient()` is how a
-- query follows the chain. A merge that deleted anything would take a decade of somebody's
-- clinical history with it.
--
-- **A merge is reversible in evidence if not in effect.** `core.patient_merge` records who
-- merged what, when, on what score, with what justification, and — because the plan asks
-- for full reversibility of the decision trail — the complete candidate list that was on
-- screen at the moment of the decision. Six months later, "why did we merge these two"
-- has an answer.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The search keys the matcher blocks on
-- ---------------------------------------------------------------------------

-- The phonetic key of each name, computed in Go (internal/platform/textmatch) and carried
-- in the event payload to the projection. Not computed in SQL: the rules are Bengali
-- transliteration habits, they will be tuned against real spellings during the pilot, and
-- a plpgsql copy of them would drift from the Go one within a month.
-- Only the English name gets a key. A Bangla name has essentially one spelling — the
-- ambiguity this key exists to absorb is a romanisation ambiguity — so Bangla names are
-- compared as text, where trigram similarity already works well.
ALTER TABLE read.patient
  ADD COLUMN name_key_en text NOT NULL DEFAULT '',
  -- Where a merged record now points. NULL for a live patient.
  ADD COLUMN merged_into_id uuid;

CREATE INDEX read_patient_name_key_en ON read.patient (facility_id, name_key_en)
  WHERE name_key_en <> '';
CREATE INDEX read_patient_name_key_trgm ON read.patient USING gin (name_key_en gin_trgm_ops);
-- The cheap first cut of the probabilistic pass: same facility, same date of birth.
CREATE INDEX read_patient_live ON read.patient (facility_id, birth_date)
  WHERE status <> 'merged';

-- The number a person reads off a card, without the prefix: "001000". Matching it with
-- `LIKE '%-001000'` is a leading wildcard and therefore a scan of the whole register, which
-- at 50,000 patients costs about 140 ms — most of the search budget, spent on the one route
-- that should be a lookup (CP31).
CREATE INDEX read_patient_clinical_serial
  ON read.patient (facility_id, right(clinical_id, 6));

COMMENT ON COLUMN read.patient.name_key_en IS
  'The phonetic key of name_en, for blocking candidates. Computed in Go; see internal/platform/textmatch (CP30).';

-- The registration derivation gains the key. Replaced wholesale rather than patched,
-- because a function that exists in two versions across two migrations is one nobody can
-- read in one place; `sqlc diff` and the migration test hold the two files honest.
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
    patient_id, facility_id, clinical_id, name_en, name_bn, name_key_en, sex,
    birth_date, dob_precision, dob_source, phone_primary, phone_secondary,
    division, district, upazila, address_line, postcode,
    emergency_name, emergency_relation, emergency_phone,
    education_level, occupation_category, income_band, household_size,
    residence_type, medicine_payer, identifier_kinds, consent_reference,
    registered_at, registered_by, registered_role, registered_station,
    event_id, global_seq)
  VALUES (
    v_patient, (event ->> 'facility_id')::uuid, event ->> 'clinical_id',
    event ->> 'name_en', coalesce(event ->> 'name_bn', ''),
    coalesce(event ->> 'name_key_en', ''), event ->> 'sex',
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
  ON CONFLICT (patient_id) DO UPDATE
     SET clinical_id = excluded.clinical_id, name_en = excluded.name_en,
         name_bn = excluded.name_bn, name_key_en = excluded.name_key_en,
         sex = excluded.sex,
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

-- ---------------------------------------------------------------------------
-- The merge record
-- ---------------------------------------------------------------------------

CREATE TABLE core.patient_merge (
  id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id  uuid        NOT NULL REFERENCES core.facility(id),

  -- The record that stays, and the one that now redirects to it.
  survivor_id  uuid        NOT NULL REFERENCES core.patient(id),
  merged_id    uuid        NOT NULL REFERENCES core.patient(id),

  -- What the matcher thought, and what the person decided anyway. Both are needed: a merge
  -- performed against a low score is a person overruling the machine, and that is exactly
  -- the case somebody will want to review.
  score        numeric(5, 4) NOT NULL CHECK (score >= 0 AND score <= 1),
  decision     text        NOT NULL CHECK (decision IN ('blocked_match', 'reviewed_match', 'manual')),
  -- Free text, required. "Duplicate" is not a justification; "same NID, second registration
  -- at outreach camp on 12 Aug" is.
  justification text       NOT NULL,

  -- The candidate list as it stood on screen when the decision was made, so that "why did
  -- we merge these two" is answerable six months later without re-running a matcher whose
  -- weights have since been tuned.
  candidates   jsonb       NOT NULL DEFAULT '[]'::jsonb,

  merged_by    uuid        NOT NULL REFERENCES core.app_user(id),
  merged_at    timestamptz NOT NULL DEFAULT now(),
  event_id     uuid        NOT NULL,

  CONSTRAINT patient_merge_not_itself CHECK (survivor_id <> merged_id),
  CONSTRAINT patient_merge_justified CHECK (length(btrim(justification)) >= 10),
  -- A record is merged away exactly once. A second merge of the same loser would make
  -- "where did this patient's history go" ambiguous.
  CONSTRAINT patient_merge_once UNIQUE (merged_id)
);

CREATE INDEX patient_merge_by_survivor ON core.patient_merge (survivor_id);

COMMENT ON TABLE core.patient_merge IS
  'One row per merge: who, when, on what score, with what justification, and the candidate list that was on screen (CP30).';

-- ---------------------------------------------------------------------------
-- Following the chain
-- ---------------------------------------------------------------------------

-- read.surviving_patient follows merged_into_id to the record that is still live.
--
-- Iterative rather than recursive-with-a-CTE so the depth guard is explicit: a cycle here
-- would be a bug, and a query that spins forever inside a clinical read is worse than one
-- that raises.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.surviving_patient(p_patient uuid)
RETURNS uuid
LANGUAGE plpgsql STABLE
SET search_path = read, pg_catalog
AS $$
DECLARE
  v_current uuid := p_patient;
  v_next    uuid;
  v_hops    integer := 0;
BEGIN
  LOOP
    SELECT merged_into_id INTO v_next FROM read.patient WHERE patient_id = v_current;
    IF v_next IS NULL THEN
      RETURN v_current;
    END IF;
    v_hops := v_hops + 1;
    IF v_hops > 16 THEN
      RAISE EXCEPTION 'merge chain from % is longer than 16 hops or cyclic', p_patient;
    END IF;
    v_current := v_next;
  END LOOP;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION read.surviving_patient(uuid) IS
  'The live record a possibly-merged patient id now refers to (CP30).';

-- ---------------------------------------------------------------------------
-- The merge derivation
-- ---------------------------------------------------------------------------

-- Applies PATIENT_MERGED to both read rows: the loser is marked and redirected, the
-- survivor is left alone. SECURITY DEFINER for the CP03 reason.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_patient_merged(event jsonb)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, pg_catalog
AS $$
DECLARE
  v_seq      bigint := (event ->> 'global_seq')::bigint;
  v_merged   uuid   := (event ->> 'merged_id')::uuid;
  v_survivor uuid   := (event ->> 'survivor_id')::uuid;
BEGIN
  IF v_seq IS NULL OR v_merged IS NULL OR v_survivor IS NULL THEN
    RAISE EXCEPTION 'read.apply_patient_merged: the event is missing global_seq, merged_id or survivor_id';
  END IF;

  UPDATE read.patient
     SET status = 'merged', merged_into_id = v_survivor,
         event_id = (event ->> 'event_id')::uuid, global_seq = v_seq
   WHERE patient_id = v_merged AND global_seq < v_seq;

  UPDATE read.projection_state
     SET checkpoint = GREATEST(checkpoint, v_seq),
         applied_at = GREATEST(coalesce(applied_at, '-infinity'::timestamptz),
                               (event ->> 'recorded_at')::timestamptz),
         updated_at = now()
   WHERE name = 'patient';
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION read.apply_patient_merged(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION read.apply_patient_merged(jsonb) TO dthcms_app, dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariant
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_merges_are_redirects() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  -- A merged patient that points nowhere is a patient whose history has been orphaned:
  -- every event against them still exists and nothing can find it.
  SELECT count(*) INTO offending
  FROM core.patient
  WHERE status = 'merged' AND merged_into_id IS NULL;

  IF offending > 0 THEN
    RAISE EXCEPTION 'merged patients with no survivor: % row(s)', offending
      USING HINT = 'A merge is a redirect. core.patient_merge records the decision; '
                   'merged_into_id is what makes the history findable (CP30).';
  END IF;

  SELECT count(*) INTO offending
  FROM core.patient p
  JOIN core.patient s ON s.id = p.merged_into_id
  WHERE p.status = 'merged' AND s.status = 'merged' AND s.merged_into_id IS NULL;

  IF offending > 0 THEN
    RAISE EXCEPTION 'merge chains ending at a merged record: % row(s)', offending;
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_merges_are_redirects() IS
  'Raises if a merged patient has no survivor to redirect to (CP30).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_merges_are_redirects', 'every merged patient redirects to a live record', 32)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name = 'assert_merges_are_redirects';
DROP FUNCTION IF EXISTS core.assert_merges_are_redirects();
DROP FUNCTION IF EXISTS read.apply_patient_merged(jsonb);
DROP FUNCTION IF EXISTS read.surviving_patient(uuid);
DROP TABLE IF EXISTS core.patient_merge;
DROP INDEX IF EXISTS read.read_patient_clinical_serial;
DROP INDEX IF EXISTS read.read_patient_live;
DROP INDEX IF EXISTS read.read_patient_name_key_trgm;
DROP INDEX IF EXISTS read.read_patient_name_key_en;
ALTER TABLE read.patient
  DROP COLUMN IF EXISTS merged_into_id,
  DROP COLUMN IF EXISTS name_key_en;
