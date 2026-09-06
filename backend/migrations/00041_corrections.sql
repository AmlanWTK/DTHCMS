-- The correction workflow (CP62, §4.3, [R-04]).
--
-- # The 140/150 case, which is the whole checkpoint
--
-- §4.3 describes it concretely: an operator records a height of 150 cm; the physician sees it, is
-- sure it is 140, and flags it; the request reaches the operator who typed it; they correct it;
-- everything derived from it recomputes; both values stay in the record with both names against
-- them. Every design decision here is that scenario read closely.
--
-- **The original value is never altered.** Correcting is writing a *new* observation that
-- replaces the old one — the mechanism CP42 already built — and this workflow is the routing
-- around it. Nothing in this migration updates a value; the ledger is append-only and the read
-- model's corrected row simply stops being ACTIVE.
--
-- **The request is routed to the person who typed it.** §4.3 says so and the reason is the point
-- of the whole feature: an operator who never learns they mistyped will mistype again, and the
-- correction rate is a training signal rather than bookkeeping (CP63). A supervisor may correct
-- it instead — a patient in front of a physician cannot wait for somebody who has gone home — and
-- that is a *different event*, because a supervisor's correction must not land on the operator's
-- record as though they had put it right themselves.
--
-- **A reason is required, and it is a code plus free text.** Free text alone cannot be counted,
-- and a code alone cannot say what actually happened. The taxonomy below is a proposal: the plan
-- lists it as needing clinical and operational confirmation, and it is rows so that changing it
-- is a decision rather than a release.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Why a value was wrong
-- ---------------------------------------------------------------------------

CREATE TABLE core.correction_reason (
  code       text PRIMARY KEY,
  display_en text NOT NULL,
  display_bn text NOT NULL,

  -- Whether this reason means the operator typed something other than what they read. CP63's
  -- pattern detection is specifically about *repeated transcription errors*, and a flag on the
  -- reason is what makes "three transcription errors in thirty days" a query rather than a
  -- guess at what somebody meant by their free text.
  is_transcription boolean NOT NULL DEFAULT false,

  ordering   integer NOT NULL,
  retired_at timestamptz,

  CONSTRAINT correction_reason_code_format CHECK (code ~ '^[A-Z][A-Z_]{2,31}$')
);

GRANT SELECT ON core.correction_reason TO dthcms_app, dthcms_projector;

INSERT INTO core.correction_reason (code, display_en, display_bn, is_transcription, ordering) VALUES
  ('TRANSCRIPTION', 'Typed a different number from the one read',
   'যা পড়া হয়েছিল তার চেয়ে অন্য সংখ্যা লেখা হয়েছে', true, 1),
  ('WRONG_UNIT', 'Entered in the wrong unit',
   'ভুল এককে লেখা হয়েছে', true, 2),
  ('MISREAD_INSTRUMENT', 'Misread the instrument',
   'যন্ত্রের পাঠ ভুল বোঝা হয়েছে', false, 3),
  ('WRONG_PATIENT', 'Recorded against the wrong patient',
   'ভুল রোগীর নামে লেখা হয়েছে', false, 4),
  ('REMEASURED', 'Measured again and the first reading was wrong',
   'আবার মাপা হয়েছে, প্রথম পাঠটি ভুল ছিল', false, 5),
  ('OTHER', 'Something else — described in the note',
   'অন্য কিছু — নোটে লেখা আছে', false, 9)
ON CONFLICT (code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  is_transcription = EXCLUDED.is_transcription, ordering = EXCLUDED.ordering;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'correction_reason', 'Why a value was wrong is a vocabulary, not a clinic''s data.')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The request
-- ---------------------------------------------------------------------------

CREATE TABLE read.correction_request (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL,
  patient_id  uuid NOT NULL,
  visit_id    uuid,

  -- The value somebody says is wrong. Not a foreign key to `read.observation`: a read model can
  -- be rebuilt, and a request that could not survive a rebuild of the table it points at would
  -- be a workflow destroyed by a maintenance operation.
  observation_id uuid NOT NULL,
  -- The code, denormalised, because CP63 counts corrections *by category* and a join to a table
  -- whose rows are replaced by every correction is a join that answers a moving question.
  code text NOT NULL,

  requested_at   timestamptz NOT NULL,
  requested_by   uuid NOT NULL,
  requested_role text NOT NULL DEFAULT '',

  reason_code text NOT NULL REFERENCES core.correction_reason(code),
  -- What the flagger actually saw. Required alongside the code rather than instead of it: a code
  -- can be counted and cannot say "the tape was against the wall, not the patient".
  note text NOT NULL DEFAULT '',

  -- Who it was routed to: the person who typed the value (§4.3). Kept even after a supervisor
  -- resolves it, because the operator's record is about what *they* were asked to correct.
  assigned_to uuid NOT NULL,

  status text NOT NULL DEFAULT 'OPEN'
         CHECK (status IN ('OPEN', 'APPLIED', 'REJECTED', 'OVERRIDDEN')),

  resolved_at   timestamptz,
  resolved_by   uuid,
  resolved_role text NOT NULL DEFAULT '',
  -- Why it was rejected, or what the corrector wants the flagger to know. Required on a
  -- rejection: "no" with no reason is how a flagging culture dies.
  resolution_note text NOT NULL DEFAULT '',

  -- The observation that replaced the flagged one, once one exists.
  replacement_id uuid,

  -- What else moved because this moved (criterion 3). Codes, because a physician reading
  -- "height corrected" should not have to work out for themselves whether the BMI followed —
  -- and an entry ending `:not-recomputed` says plainly that one did not.
  recomputed text[] NOT NULL DEFAULT ARRAY[]::text[],

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL,

  CONSTRAINT correction_resolution_is_complete
    CHECK ((status = 'OPEN') = (resolved_at IS NULL)),
  CONSTRAINT correction_resolution_is_attributed
    CHECK (resolved_at IS NULL OR resolved_by IS NOT NULL),
  CONSTRAINT correction_rejection_says_why
    CHECK (status <> 'REJECTED' OR btrim(resolution_note) <> ''),
  CONSTRAINT correction_applied_names_the_replacement
    CHECK (status NOT IN ('APPLIED', 'OVERRIDDEN') OR replacement_id IS NOT NULL)
);

-- One open request per value. A second flag on the same value is the same conversation, and two
-- rows would route two corrections at one number.
CREATE UNIQUE INDEX correction_one_open_per_observation
  ON read.correction_request (observation_id) WHERE status = 'OPEN';

-- What an operator's own device asks for: "what am I being asked to fix".
CREATE INDEX correction_open_for_operator
  ON read.correction_request (assigned_to, requested_at DESC) WHERE status = 'OPEN';

-- What CP63's quality record reads.
CREATE INDEX correction_by_operator_and_reason
  ON read.correction_request (facility_id, assigned_to, reason_code, requested_at DESC);

CREATE INDEX correction_by_patient
  ON read.correction_request (patient_id, requested_at DESC);

GRANT SELECT ON read.correction_request TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON read.correction_request TO dthcms_projector;

COMMENT ON TABLE read.correction_request IS
  'One value somebody said was wrong, routed to whoever typed it (CP62, §4.3).';

-- ---------------------------------------------------------------------------
-- The projections
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_correction_requested(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.correction_request (
    id, facility_id, patient_id, visit_id, observation_id, code,
    requested_at, requested_by, requested_role, reason_code, note, assigned_to,
    event_id, global_seq)
  VALUES (
    (p->>'request_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    nullif(p->>'visit_id', '')::uuid,
    (p->>'observation_id')::uuid,
    p->>'code',
    (p->>'requested_at')::timestamptz,
    (p->>'requested_by')::uuid,
    coalesce(p->>'requested_role', ''),
    p->>'reason_code',
    coalesce(p->>'note', ''),
    (p->>'assigned_to')::uuid,
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (id) DO NOTHING;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_correction_resolved(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  -- One function for all three outcomes, because they differ only in the status and in whether
  -- there is a replacement. A row that has already been resolved is left alone: a request is
  -- answered once, and a second answer arriving late is a race rather than a correction.
  UPDATE read.correction_request
     SET status          = p->>'status',
         resolved_at     = (p->>'resolved_at')::timestamptz,
         resolved_by     = (p->>'resolved_by')::uuid,
         resolved_role   = coalesce(p->>'resolved_role', ''),
         resolution_note = coalesce(p->>'note', ''),
         replacement_id  = nullif(p->>'replacement_id', '')::uuid,
         -- A cascade that recomputed nothing sends a nil slice, which marshals to JSON
         -- null rather than to an empty array; `jsonb_array_elements_text` on a scalar
         -- raises rather than returning no rows, so the shape is checked before the
         -- elements are pulled apart. Absent, null and empty all mean the same thing
         -- here: nothing downstream needed recomputing.
         recomputed      = CASE
                             WHEN jsonb_typeof(p->'recomputed') = 'array'
                             THEN coalesce(
                                    (SELECT array_agg(value::text)
                                       FROM jsonb_array_elements_text(p->'recomputed')),
                                    ARRAY[]::text[])
                             ELSE ARRAY[]::text[]
                           END,
         global_seq      = (p->>'global_seq')::bigint
   WHERE id = (p->>'request_id')::uuid
     AND status = 'OPEN';
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_correction_requested(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_correction_resolved(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- Criterion 2, standing: a correction records who, when, why and what it corrects. The CHECK
-- constraints guard rows written from here on; this notices one that arrived some other way.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_correction_says_why() RETURNS void
LANGUAGE plpgsql
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM read.correction_request
   WHERE reason_code IS NULL OR requested_by IS NULL OR assigned_to IS NULL
      OR (status = 'REJECTED' AND btrim(resolution_note) = '');
  IF offenders > 0 THEN
    RAISE EXCEPTION '% correction requests do not say who asked, who was asked, or why', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- Criterion 1: the original value is never altered or deleted and remains queryable. A corrected
-- observation keeps its row and stops being ACTIVE; this notices a request whose flagged value
-- has vanished from the read model altogether, which would mean the record can no longer answer
-- what was corrected.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_corrected_values_are_still_there() RETURNS void
LANGUAGE plpgsql
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM read.correction_request r
   WHERE NOT EXISTS (SELECT 1 FROM read.observation o WHERE o.id = r.observation_id);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% flagged values are no longer in the record', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_correction_says_why',
   'every correction names who asked, who was asked and why', 76),
  ('assert_corrected_values_are_still_there',
   'a corrected value is still in the record, next to its correction', 77)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- ---------------------------------------------------------------------------
-- Who may flag, corrected
-- ---------------------------------------------------------------------------

-- §4.3's protagonist is the physician: *"the physician sees it, is sure it is 140, and flags
-- it"*. CP15's seed gave `observation.correct.request` to the stations that record values and to
-- the junior doctor, and not to the consultant or to QA — so the person the scenario is written
-- about could not raise the request, and the person whose job is reviewing quality could not
-- either. Two grants, and the checkpoint is testable.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'observation.correct.request' FROM core.role r
 WHERE r.code IN ('PHYSICIAN', 'QA')
ON CONFLICT DO NOTHING;

-- And one taken away. Registration holds `observation.correct.request` and does **not** hold
-- `observation.read.values`: a grant to flag values the holder cannot see, which is not a
-- capability but a line in an access review that somebody has to explain every time. Removing it
-- takes nothing away that anybody could do.
DELETE FROM core.role_permission
 WHERE permission_code = 'observation.correct.request'
   AND role_id IN (SELECT id FROM core.role WHERE code = 'REGISTRATION');

-- +goose Down

DELETE FROM core.role_permission
 WHERE permission_code = 'observation.correct.request'
   AND role_id IN (SELECT id FROM core.role WHERE code IN ('PHYSICIAN', 'QA'));
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'observation.correct.request' FROM core.role r WHERE r.code = 'REGISTRATION'
ON CONFLICT DO NOTHING;

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_every_correction_says_why', 'assert_corrected_values_are_still_there');
DROP FUNCTION IF EXISTS core.assert_corrected_values_are_still_there();
DROP FUNCTION IF EXISTS core.assert_every_correction_says_why();
DROP FUNCTION IF EXISTS read.apply_correction_resolved(jsonb);
DROP FUNCTION IF EXISTS read.apply_correction_requested(jsonb);
DROP TABLE IF EXISTS read.correction_request;
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name = 'correction_reason';
DROP TABLE IF EXISTS core.correction_reason;
