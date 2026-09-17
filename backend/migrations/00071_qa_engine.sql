-- Step 10's quality station, and the gate that makes it real (CP83, §3 step 10, §5.5).
--
-- # The one sentence this migration exists for
--
-- **An empty rule table means every prescription clears, not that clearance is skipped.**
-- `docs/qa-rules.md` §5 says it, and the difference between those two readings is the whole
-- checkpoint. The first is a clinic whose QA officer has nothing to check today; the second is
-- a clinic whose QA station is a screen nobody has to open. Only one of them is safe, and the
-- only way to tell them apart from outside the code is to put the requirement somewhere the
-- rule table cannot reach.
--
-- So: the rules are rows, and **clearance being required is not a row**. There is no column
-- anywhere in this file that turns the gate off, no facility setting, no `enabled` flag on the
-- gate itself. `core.qa_clearance_stands()` consults `read.qa_review` and nothing else — it
-- never reads `core.qa_rule`, so an empty rule table cannot make it answer differently. What an
-- empty rule table changes is what the *officer* sees: no findings, a clean screen, one click.
--
-- # Where the gate is
--
-- A trigger on `read.prescription`, refusing entry to SIGNED, PRINTED or DISPENSED without a
-- standing clearance. CP57's counselling gate is the precedent and the argument is the same: the
-- honest reading of "enforced server-side" is the path nobody remembers — the support script, the
-- second client, the integration written after everybody who read the plan has left. A check in
-- a Go handler is a check one refactor removes.
--
-- **Why the gate is on entry to SIGNED and not only on entry to PRINTED.** PRINTED's only
-- in-edge is from SIGNED and SIGNED's only in-edge is from QA_REVIEW, so gating SIGNED would be
-- enough today. It is not enough tomorrow: a thirteenth row in `core.prescription_transition`
-- is a migration away, and a gate that depended on the shape of the matrix would be a gate that
-- a future edge silently opens. So the trigger names the three statuses a prescription must not
-- reach uncleared, and an invariant asserts that every in-edge to those statuses is covered.
--
-- # What a standing clearance is
--
-- A CLEARED row in `read.qa_review` decided **after the prescription was last bounced**. Without
-- that clause the sequence clear -> bounce -> edit -> resubmit would leave the first clearance
-- standing over a sheet whose drugs have changed since, which is CP80's "reviewed and then
-- altered" defect wearing a different hat. `bounced_at` is overwritten by every bounce
-- (`read.apply_prescription_transition`), so the comparison is against the latest one.
--
-- # Rules are reference data, and the line this migration draws
--
-- Criterion 5 is "the rule set is configurable without a code release", and the way that
-- criterion gets faked is a table nobody can actually change. So `core.qa_rule` is writable by
-- the application behind `qa.rule.write`, and severity, bounce station, recency window, enabled
-- and the rule's parameters are all columns on it.
--
-- What is *not* writable is `kind`. A kind is a question shape — "is this observation recent
-- enough", "did the education station see this patient" — and each one is a Go predicate. A row
-- naming a kind nothing implements is a rule that silently never fires, which is worse than no
-- rule, so `kind` references `core.qa_rule_kind`, that table is read-only to the application,
-- and a trigger refuses to move an existing rule to a different kind.
--
-- The line, stated plainly: **a new rule of an existing shape is a row; a genuinely new question
-- is a release.** "No urine ACR in 12 months" is a row. "Block if the patient's brother was
-- treated here" is a release.

-- +goose Up

-- ---------------------------------------------------------------------------
-- 0. Reference data the rules need and the registry did not have
-- ---------------------------------------------------------------------------

-- Rules 11 and 13 of `docs/qa-rules.md` are about TSH and a baseline full blood count, and
-- neither had an observation code. A rule whose code does not exist is a rule that cannot fire;
-- adding them here rather than leaving the rules unimplementable is the honest fix, and it is
-- reference data, which is what this file is mostly made of.
INSERT INTO core.unit (code, dimension, is_canonical, factor, "offset", display_en, display_bn, decimals) VALUES
  ('m[IU]/L', 'thyrotropin_concentration', true, 1, 0, 'mIU/L', 'মিআইইউ/লি', 2),
  ('10*9/L',  'leukocyte_concentration',   true, 1, 0, '10⁹/L', '১০⁹/লি',    2)
ON CONFLICT (code) DO NOTHING;

INSERT INTO core.observation_code
  (code, category, value_type, dimension, loinc, display_en, display_bn,
   min_canonical, max_canonical, write_permission) VALUES
  ('TSH', 'LAB', 'numeric', 'thyrotropin_concentration', '3016-3',
   'TSH', 'টিএসএইচ', 0, 500, 'observation.write.history'),
  -- The white cell count stands for the baseline full blood count. Rule 13 is about
  -- agranulocytosis, and the number that answers it is the neutrophil count inside an FBC; the
  -- clinic's lab reports the FBC as a panel, and CP103's panel extraction is what will split it.
  -- Until then the white cell count is the field a person actually types, and asking for it is
  -- asking for the FBC.
  ('WBC', 'LAB', 'numeric', 'leukocyte_concentration', '6690-2',
   'White cell count', 'শ্বেত রক্তকণিকা', 0, 200, 'observation.write.history'),
  -- Rule 14 needs somewhere for the answer to go, and there was nowhere.
  --
  -- `cmd/api/medsafety_facts_bridge.go` says it plainly: pregnancy status is not a field
  -- anywhere in DTHCMS, and the only thing in the record that implies it is the ICD-10 code
  -- O24.4. A QA rule that blocks on "no pregnancy status recorded this visit" against a record
  -- with no way to record one is a **block with no remediation**: the officer bounces the
  -- patient to the history station, the history officer has no field to fill in, and the patient
  -- comes back with the same file. That is the worst kind of gate, because the way out of it is
  -- the override, and an override that is the only exit stops being a valve and becomes the
  -- process -- which is exactly the failure `docs/qa-rules.md` 1 warns about.
  --
  -- So the field exists. Coded rather than boolean, because "not asked", "not pregnant",
  -- "pregnant", "unsure" and "declined to say" are five different clinical situations and a
  -- boolean collapses the first into the second.
  ('PREGNANCY_STATUS', 'SCREENING', 'coded', NULL, '82810-3',
   'Pregnancy status', 'গর্ভাবস্থার অবস্থা', NULL, NULL, 'observation.write.history')
ON CONFLICT (code) DO NOTHING;

INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal) VALUES
  ('PREGNANCY_STATUS', 'not_pregnant', 'Not pregnant', 'গর্ভবতী নন', 1, true),
  ('PREGNANCY_STATUS', 'pregnant', 'Pregnant', 'গর্ভবতী', 2, false),
  ('PREGNANCY_STATUS', 'possible', 'Possible, not confirmed', 'সম্ভাবনা আছে, নিশ্চিত নয়', 3, false),
  ('PREGNANCY_STATUS', 'breastfeeding', 'Breastfeeding', 'স্তন্যদান করছেন', 4, false),
  -- A patient who will not answer has answered. The rule is satisfied and the record says why,
  -- which is a different fact from nobody having asked.
  ('PREGNANCY_STATUS', 'declined', 'Declined to say', 'বলতে চাননি', 5, false),
  ('PREGNANCY_STATUS', 'not_applicable', 'Not applicable', 'প্রযোজ্য নয়', 6, true)
ON CONFLICT (code, value_code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 1. The teratogenic flag, on CP77's rule table
-- ---------------------------------------------------------------------------

-- `docs/qa-rules.md` §3.4: *"The teratogenic flag is a property of the molecule and belongs on
-- the CP77 rule table, not in this document — which drugs carry it is Dr Nahid's list to write,
-- and until he writes it this rule fires for nothing."*
--
-- So it is a column on `core.medication_rule`, meaningful on a PREGNANCY rule and on no other
-- kind, and **no row sets it**. Rule 14 is therefore inert in production — deliberately, and
-- visibly: `core.assert_the_qa_rule_set_is_reachable()` reports it with a NOTICE on every
-- verify, the same way invariant 133 reports the glucose meter nobody stocks.
--
-- Why a flag on the rule rather than a boolean on `core.generic`: the molecule table is the
-- formulary's, and "teratogenic" is not a fact about a product line, it is a clinical statement
-- somebody has to author, approve and cite. CP77 already has authoring, approval, versioning and
-- a citation field, and a rule's subject already names molecules or a whole class — which is the
-- right shape, because the answer for ACE inhibitors is the class and the answer for carbimazole
-- is the molecule.
ALTER TABLE core.medication_rule
  ADD COLUMN IF NOT EXISTS flags_teratogenic boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN core.medication_rule.flags_teratogenic IS
  'This rule''s subject molecules are teratogenic (CP83 rule 14). Meaningful only on a PREGNANCY '
  'rule. No seeded row sets it: the list is Dr Nahid''s to write.';

ALTER TABLE core.medication_rule
  ADD CONSTRAINT medication_rule_teratogenic_is_a_pregnancy_rule
  CHECK (NOT flags_teratogenic OR rule_type = 'PREGNANCY');

-- ---------------------------------------------------------------------------
-- 2. Investigation orders — "recorded **or** ordered"
-- ---------------------------------------------------------------------------

-- Rule 4 is the plan's named rule, and its wording is *"no HbA1c recorded **or ordered**"*. The
-- second half had nowhere to live: no checkpoint in `docs/implementation-plan.md` owns lab
-- ordering, and nothing in the schema records that a test was asked for. Without it rule 4
-- blocks the consultant who did exactly the right thing and is waiting for the lab, which
-- `docs/qa-rules.md` §3.2 explicitly says is blocking the wrong person.
--
-- So the concept is created here, as narrowly as it can be: an order is a patient, a visit, an
-- observation code, an instant and a person. No specimen, no lab, no result — a result is an
-- observation and already has a home. `lab.order` is the permission, seeded by CP15 and held by
-- the prescribers, which is evidence that the concept was always intended.
--
-- **It belongs to `clinical`, not to `qa`.** QA asks a question about orders; it does not own
-- them. When a lab checkpoint arrives it inherits this table rather than migrating away from a
-- second one.
CREATE TABLE read.investigation_order (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL REFERENCES core.facility(id),
  patient_id  uuid NOT NULL,
  visit_id    uuid,

  code text NOT NULL REFERENCES core.observation_code(code),

  ordered_at   timestamptz NOT NULL,
  ordered_by   uuid NOT NULL,
  ordered_role text NOT NULL DEFAULT '',

  -- Why it was asked for. Free text, because the reason a consultant orders an HbA1c today is
  -- not a coded list anybody has written.
  note text NOT NULL DEFAULT '',

  -- An order withdrawn rather than deleted, like everything else in this system.
  cancelled_at     timestamptz,
  cancelled_by     uuid,
  cancelled_reason text NOT NULL DEFAULT '',

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL,

  CONSTRAINT investigation_order_cancellation_is_whole CHECK (
    (cancelled_at IS NULL) = (cancelled_by IS NULL)
    AND (cancelled_at IS NULL OR btrim(cancelled_reason) <> ''))
);

CREATE INDEX investigation_order_by_patient
  ON read.investigation_order (patient_id, code, ordered_at DESC);
CREATE INDEX investigation_order_by_visit ON read.investigation_order (visit_id);

GRANT SELECT ON read.investigation_order TO dthcms_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON read.investigation_order TO dthcms_projector;

COMMENT ON TABLE read.investigation_order IS
  'A test somebody asked for (CP83). The "or ordered" half of QA rule 4; owned by clinical.';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_investigation_ordered(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.investigation_order (
    id, facility_id, patient_id, visit_id, code,
    ordered_at, ordered_by, ordered_role, note, event_id, global_seq)
  VALUES (
    (p->>'order_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    nullif(p->>'visit_id', '')::uuid,
    p->>'code',
    (p->>'ordered_at')::timestamptz,
    (p->>'ordered_by')::uuid,
    coalesce(p->>'ordered_role', ''),
    coalesce(p->>'note', ''),
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (id) DO NOTHING;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_investigation_ordered(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- 3. The rule kinds — the shapes, which are code
-- ---------------------------------------------------------------------------

-- Each kind is one Go predicate in `internal/qa/rules.go`. The table exists so that a rule row
-- cannot name a question nothing answers: a `kind` column of free text would let somebody write
-- `'HBA1C_RECENCY'` where the code expects `'OBSERVATION_RECENT'` and produce a rule that is
-- present, enabled, and permanently silent. That is the worst failure available at this station,
-- because the screen shows a rule and the patient walks out anyway.
--
-- **Read-only to the application**, for invariant 124's reason exactly: a table that says what
-- the application is allowed to ask is a table the application must not be able to write.
CREATE TABLE core.qa_rule_kind (
  kind text PRIMARY KEY,

  -- What question this shape asks, for the person reading the rule table.
  question_en text NOT NULL,
  question_bn text NOT NULL,

  -- Which parameters it reads out of `core.qa_rule.params`, documented here because the params
  -- column is jsonb and jsonb without documentation is a column nobody dares change.
  params_doc text NOT NULL,

  -- Whether `window_days` means anything for this shape. A recency window on a rule that asks
  -- "is there a diagnosis on this visit" is a number somebody will set and nothing will read.
  uses_window boolean NOT NULL DEFAULT false,

  ordering int NOT NULL,

  CONSTRAINT qa_rule_kind_is_upper CHECK (kind ~ '^[A-Z][A-Z0-9_]{2,47}$')
);

COMMENT ON TABLE core.qa_rule_kind IS
  'The question shapes the QA engine can ask (CP83). Reference data: one Go predicate each, so a '
  'new kind is a release. Invariant 134 keeps the application out of it.';

GRANT SELECT ON core.qa_rule_kind TO dthcms_app, dthcms_projector;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON core.qa_rule_kind FROM dthcms_app;

INSERT INTO core.qa_rule_kind (kind, question_en, question_bn, params_doc, uses_window, ordering) VALUES
  ('SAFETY_ENGINE_FINDING',
   'Did CP78''s medication safety engine leave anything unresolved on this sheet?',
   'সিপি৭৮-এর ওষুধ নিরাপত্তা ইঞ্জিন এই ব্যবস্থাপত্রে অমীমাংসিত কিছু রেখেছে কি?',
   '{"min_severity":"BLOCK|WARN","rule_types":["INTERACTION",...] (empty = every type)}', false, 10),
  ('ALLERGY_STATUS_ASSERTED',
   'Has somebody asserted this patient''s allergy status at all?',
   'এই রোগীর অ্যালার্জির অবস্থা কেউ আদৌ নিশ্চিত করেছেন কি?',
   'none', false, 20),
  ('OBSERVATION_RECENT',
   'Is there a recent enough value for one of these observation codes?',
   'এই পর্যবেক্ষণ কোডগুলির কোনোটির যথেষ্ট সাম্প্রতিক মান আছে কি?',
   '{"codes":["HBA1C"],"accept_ordered":true,"when_diagnosis":["E11"],"when_generics":["Levothyroxine"],"when_classes":["..."]}', true, 30),
  ('OBSERVATION_THIS_VISIT',
   'Was one of these observation codes recorded during this visit?',
   'এই পরিদর্শনে এই পর্যবেক্ষণ কোডগুলির কোনোটি নেওয়া হয়েছে কি?',
   '{"codes":["BP_SYSTOLIC"],"when_diagnosis":["E11"],"when_generics":[...],"when_classes":[...]}', false, 40),
  ('RENAL_WINDOW',
   'Is there an eGFR inside the facility''s window for a renally-dosed drug on this sheet?',
   'এই ব্যবস্থাপত্রের কিডনি-নির্ভর ওষুধের জন্য প্রতিষ্ঠানের সময়সীমার ভেতরে ইজিএফআর আছে কি?',
   'none - CP79''s core.facility_renal_policy is the window', false, 50),
  ('EDUCATION_THIS_VISIT',
   'Did the prescription education station see this patient this visit?',
   'এই পরিদর্শনে ওষুধ শিক্ষা কেন্দ্র রোগীকে দেখেছে কি?',
   '{"when_classes":["INSULIN"],"when_generics":["Semaglutide"]}', false, 60),
  ('COUNSELING_ITEM_COVERED',
   'Was a specific counselling item covered for this visit?',
   'এই পরিদর্শনে নির্দিষ্ট পরামর্শ-বিষয়টি বোঝানো হয়েছে কি?',
   '{"item_codes":["AGRANULOCYTOSIS_WARNING"],"when_generics":["Carbimazole"],"when_classes":[...]}', false, 70),
  ('COUNSELING_COMPLETE',
   'Is the whole counselling checklist for this visit finished?',
   'এই পরিদর্শনের পুরো পরামর্শ তালিকা শেষ হয়েছে কি?',
   'none - CP57''s gate is the answer', false, 80),
  ('TERATOGEN_PREGNANCY_STATUS',
   'A teratogenic drug for a woman who could be pregnant, with no pregnancy status this visit.',
   'গর্ভধারণক্ষম নারীকে ভ্রূণ-ক্ষতিকর ওষুধ, অথচ এই পরিদর্শনে গর্ভাবস্থার তথ্য নেই।',
   '{"age_min":15,"age_max":49}', false, 90),
  ('DIAGNOSIS_CODED_THIS_VISIT',
   'Is there a coded diagnosis on this visit?',
   'এই পরিদর্শনে সাংকেতিক রোগনির্ণয় আছে কি?',
   'none', false, 100),
  ('MANDATORY_STATION_DATA',
   'Did every station this visit''s route plans as mandatory actually record something?',
   'এই পরিদর্শনের পথে বাধ্যতামূলক প্রতিটি কেন্দ্র কি সত্যিই কিছু লিখেছে?',
   'none - core.station_sequence is the route', false, 110),
  ('WEIGHT_FOR_WEIGHT_BASED_DOSE',
   'A weight-based dose with no weight recorded this visit.',
   'ওজন-নির্ভর মাত্রা, অথচ এই পরিদর্শনে ওজন নেওয়া হয়নি।',
   '{"codes":["BODY_WEIGHT"]}', false, 120);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'qa_rule_kind',
   'The shapes of question the QA engine can ask are the same in every clinic; which of them are asked is core.qa_rule, which is facility-scoped.')
ON CONFLICT (schema_name, table_name) DO UPDATE SET reason = EXCLUDED.reason;

-- ---------------------------------------------------------------------------
-- 4. The rules themselves — rows Dr Nahid changes without a release
-- ---------------------------------------------------------------------------

CREATE TABLE core.qa_rule (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  -- The handle a finding cites and a person quotes across a room. Immutable once written: a
  -- code that could be reassigned would make every historical review row ambiguous.
  code text NOT NULL,
  kind text NOT NULL REFERENCES core.qa_rule_kind(kind),

  -- `docs/qa-rules.md` §2. Two severities and the difference is the patient's feet.
  severity text NOT NULL CHECK (severity IN ('BLOCK', 'WARN')),

  -- The station the patient walks back to. Nullable only for a WARN whose remediation is not a
  -- room — and the constraint below says a BLOCK always names one, because §3's opening line is
  -- that "incomplete" without "whose" is a rule that stalls.
  bounce_station_code text,

  -- The recency window, in days, for the shapes that have one. Null means the shape does not use
  -- one; `core.qa_rule_uses_its_window()` refuses the combinations that would be silently ignored.
  window_days int CHECK (window_days IS NULL OR window_days > 0),

  -- The shape's parameters. jsonb because each shape reads different keys, documented on
  -- `core.qa_rule_kind.params_doc`, and validated by the Go engine on load rather than by a
  -- constraint per shape that would have to be edited for every new one.
  params jsonb NOT NULL DEFAULT '{}'::jsonb,

  -- Whether it is asked at all. A rule turned off rather than deleted, so the row that used to
  -- fire is still there to explain a review from last year.
  enabled boolean NOT NULL DEFAULT true,

  -- What the officer reads. Bilingual and required in both: a finding that appears in English
  -- for an officer who works in Bangla has not appeared.
  title_en text NOT NULL,
  title_bn text NOT NULL,
  -- The sentence that says what to do about it.
  detail_en text NOT NULL DEFAULT '',
  detail_bn text NOT NULL DEFAULT '',

  ordering int NOT NULL DEFAULT 100,

  retired_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT qa_rule_code_is_a_handle CHECK (code ~ '^[A-Z][A-Z0-9_]{2,47}$'),
  CONSTRAINT qa_rule_is_bilingual CHECK (
    btrim(title_en) <> '' AND btrim(title_bn) <> ''
    AND (btrim(detail_en) = '') = (btrim(detail_bn) = '')),
  -- §3: every rule names the station it bounces to, because a BLOCK that cannot say where the
  -- patient goes is a BLOCK that leaves them standing in the corridor.
  CONSTRAINT qa_rule_block_names_its_station CHECK (
    severity <> 'BLOCK' OR bounce_station_code IS NOT NULL)
);

CREATE UNIQUE INDEX qa_rule_code_key ON core.qa_rule (facility_id, code);
CREATE INDEX qa_rule_live ON core.qa_rule (facility_id, ordering) WHERE retired_at IS NULL;

SELECT core.attach_updated_at('core.qa_rule');

COMMENT ON TABLE core.qa_rule IS
  'The QA checklist, as rows (CP83 criterion 5). Severity, bounce station, recency window, '
  'parameters and enabled are all editable behind qa.rule.write; the kind is not, because a kind '
  'is a Go predicate.';

-- **Writable**, which is criterion 5, and the reason this differs from
-- `core.prescription_transition`: the transition matrix is what the application is judged by,
-- and this is what the *officer* is asked to check. Changing it cannot make a prescription skip
-- clearance, only change what clearance looks at — because `core.qa_clearance_stands()` never
-- reads this table. DELETE is withheld: a retired rule stays, so that a review recorded last
-- year can still name what fired.
GRANT SELECT, INSERT, UPDATE ON core.qa_rule TO dthcms_app;
-- ALTER DEFAULT PRIVILEGES grants DELETE on every new table in `core`, which is the default
-- that migration 00063 was written about. Taken back explicitly, because "retired, not deleted"
-- is a property of the data and not of the trigger that happens to guard it today.
REVOKE DELETE, TRUNCATE ON core.qa_rule FROM dthcms_app;
GRANT SELECT ON core.qa_rule TO dthcms_projector;

-- +goose StatementBegin
-- The two things a rule row may not become.
--
-- Changing `kind` re-points a rule at a different Go predicate while keeping its code, its
-- history and its name on every past review — so a finding recorded in March would now be
-- explained by a rule that asks something else. Changing `code` does the same to the join in the
-- other direction.
--
-- And a window on a shape that does not read one is the quiet failure this whole table is
-- arranged to avoid: somebody sets 180 days, nothing reads it, and the screen says the rule is
-- configured.
CREATE OR REPLACE FUNCTION core.qa_rule_is_configurable_but_not_reshapable() RETURNS trigger
LANGUAGE plpgsql
SET search_path = core, pg_catalog
AS $$
DECLARE
  uses boolean;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF NEW.code <> OLD.code OR NEW.kind <> OLD.kind OR NEW.facility_id <> OLD.facility_id THEN
      RAISE EXCEPTION 'QA rule %: its code and its kind are written once', OLD.code
        USING HINT =
          'A kind is a Go predicate, and a rule that changed kind would explain every past '
          'finding with a question it never asked. Retire this rule and add another.';
    END IF;
  END IF;

  SELECT k.uses_window INTO uses FROM core.qa_rule_kind k WHERE k.kind = NEW.kind;
  IF NEW.window_days IS NOT NULL AND NOT uses THEN
    RAISE EXCEPTION 'QA rule % is a % and does not read a recency window', NEW.code, NEW.kind
      USING HINT =
        'A window nothing reads is a setting that makes a screen say the rule is configured. '
        'core.qa_rule_kind.uses_window says which shapes have one.';
  END IF;
  IF NEW.window_days IS NULL AND uses THEN
    RAISE EXCEPTION 'QA rule % is a % and needs a recency window', NEW.code, NEW.kind
      USING HINT = 'How recent is recent enough is the clinic''s number, not a constant in Go.';
  END IF;

  IF NEW.bounce_station_code IS NOT NULL
     AND NOT EXISTS (SELECT 1 FROM core.station s
                      WHERE s.facility_id = NEW.facility_id
                        AND s.code = NEW.bounce_station_code) THEN
    RAISE EXCEPTION 'QA rule % bounces to %, which is not a station in this facility',
                    NEW.code, NEW.bounce_station_code
      USING HINT =
        'A bounce to a station that does not exist is a patient sent to a room that is not '
        'there. The station list is core.station.';
  END IF;

  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER qa_rule_stays_the_shape_it_was
  BEFORE INSERT OR UPDATE ON core.qa_rule
  FOR EACH ROW EXECUTE FUNCTION core.qa_rule_is_configurable_but_not_reshapable();

-- +goose StatementBegin
-- Nothing is deleted. A rule row is cited by every review that ever fired it.
CREATE OR REPLACE FUNCTION core.qa_rule_is_retired_not_deleted() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'QA rule % is retired, not deleted', OLD.code
    USING HINT = 'Set retired_at. Reviews from last year still name it.';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER qa_rule_no_delete
  BEFORE DELETE ON core.qa_rule
  FOR EACH ROW EXECUTE FUNCTION core.qa_rule_is_retired_not_deleted();

-- ---------------------------------------------------------------------------
-- 5. The review, and the override
-- ---------------------------------------------------------------------------

CREATE TABLE read.qa_review (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  prescription_id uuid NOT NULL REFERENCES read.prescription(id),
  patient_id      uuid NOT NULL,
  visit_id        uuid NOT NULL,

  outcome text NOT NULL CHECK (outcome IN ('CLEARED', 'BOUNCED')),

  decided_at   timestamptz NOT NULL,
  decided_by   uuid NOT NULL,
  decided_role text NOT NULL DEFAULT '',

  -- Where the patient walks back to, and why, in both languages. A bounce is a real clinical
  -- event — somebody with a walking stick goes back up the corridor — and "incomplete" is not a
  -- reason a person at the other end can act on.
  bounce_station_code text,
  reason_en text NOT NULL DEFAULT '',
  reason_bn text NOT NULL DEFAULT '',

  -- Every finding as it stood at the decision, by value. Recomputing it later would answer a
  -- different question: what was missing *then* is the record, and an HbA1c ordered an hour
  -- afterwards would make a recomputed list say the bounce was for nothing.
  findings jsonb NOT NULL DEFAULT '[]'::jsonb,

  -- §2: a WARN clears, and the officer acknowledges it. The codes acknowledged are kept so that
  -- "who decided this was acceptable" has an answer.
  acknowledged text[] NOT NULL DEFAULT ARRAY[]::text[],

  -- The consultant override this clearance stood on, when it stood on one.
  override_id uuid,

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL,

  CONSTRAINT qa_review_bounce_names_a_station_and_a_reason CHECK (
    outcome <> 'BOUNCED'
    OR (bounce_station_code IS NOT NULL
        AND btrim(reason_en) <> '' AND btrim(reason_bn) <> '')),
  -- A clearance does not send anybody anywhere.
  CONSTRAINT qa_review_clearance_bounces_nobody CHECK (
    outcome <> 'CLEARED' OR bounce_station_code IS NULL)
);

CREATE INDEX qa_review_by_prescription
  ON read.qa_review (prescription_id, decided_at DESC);
CREATE INDEX qa_review_by_day ON read.qa_review (facility_id, decided_at DESC);

GRANT SELECT ON read.qa_review TO dthcms_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON read.qa_review TO dthcms_projector;

COMMENT ON TABLE read.qa_review IS
  'One QA decision on one prescription, with the findings as they stood (CP83). A CLEARED row '
  'later than the last bounce is what core.qa_clearance_stands() looks for.';

CREATE TABLE read.qa_override (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  prescription_id uuid NOT NULL REFERENCES read.prescription(id),
  patient_id      uuid NOT NULL,
  visit_id        uuid NOT NULL,

  granted_at   timestamptz NOT NULL,
  granted_by   uuid NOT NULL,
  granted_role text NOT NULL DEFAULT '',

  -- Required, and required to say something. The valve is acceptable only while it is legible,
  -- and the only defence against "override" typed in a reason field is a person reading them.
  reason text NOT NULL CHECK (btrim(reason) <> ''),

  -- What was blocking at the moment it was granted, not what is blocking now.
  blocking_at_grant text[] NOT NULL,

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL
);

-- One per prescription. A second override is not another act.
CREATE UNIQUE INDEX qa_override_once_per_prescription
  ON read.qa_override (prescription_id);
CREATE INDEX qa_override_by_day ON read.qa_override (facility_id, granted_at DESC);

GRANT SELECT ON read.qa_override TO dthcms_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON read.qa_override TO dthcms_projector;

COMMENT ON TABLE read.qa_override IS
  'A consultant letting a blocked prescription past the QA gate, in their own name (CP83). The '
  'rate is Quality''s to watch, which is why the rate route is behind qa.review and not qa.override.';

ALTER TABLE read.qa_review
  ADD CONSTRAINT qa_review_override_exists
  FOREIGN KEY (override_id) REFERENCES read.qa_override(id);

-- ---------------------------------------------------------------------------
-- 6. The gate
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
-- Does a clearance stand on this prescription right now?
--
-- **This function does not read `core.qa_rule`.** That is the §5 distinction made structural: an
-- empty rule table changes what the officer is shown and cannot change whether a decision is
-- required. There is no path from a rule row to this answer.
CREATE OR REPLACE FUNCTION core.qa_clearance_stands(p_prescription uuid) RETURNS boolean
LANGUAGE sql
STABLE
SET search_path = read, core, pg_catalog
AS $$
  SELECT EXISTS (
    SELECT 1
      FROM read.qa_review r
      JOIN read.prescription p ON p.id = r.prescription_id
     WHERE r.prescription_id = p_prescription
       AND r.outcome = 'CLEARED'
       -- Later than the last bounce. Without this, clear -> bounce -> edit -> resubmit leaves
       -- the first clearance standing over a sheet whose drugs have changed since.
       AND r.decided_at >= coalesce(p.bounced_at, '-infinity'::timestamptz));
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION core.qa_clearance_stands(uuid) TO dthcms_app, dthcms_projector;

-- +goose StatementBegin
-- The gate itself.
--
-- Three statuses named rather than one edge, because the guarantee has to survive a thirteenth
-- row in `core.prescription_transition`. A prescription must not be SIGNED, PRINTED or DISPENSED
-- without a clearance behind it, whatever path it took to get there.
CREATE OR REPLACE FUNCTION core.prescription_needs_qa_clearance() RETURNS trigger
LANGUAGE plpgsql
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  IF NEW.status = OLD.status THEN
    RETURN NEW;
  END IF;
  IF NEW.status NOT IN ('SIGNED', 'PRINTED', 'DISPENSED') THEN
    RETURN NEW;
  END IF;
  -- Already past the gate: a printed prescription being dispensed was cleared before it was
  -- signed, and re-asking would refuse the pharmacy over a clearance whose bounce comparison
  -- has nothing to do with them.
  IF OLD.status IN ('SIGNED', 'PRINTED', 'DISPENSED') THEN
    RETURN NEW;
  END IF;

  IF NOT core.qa_clearance_stands(NEW.id) THEN
    RAISE EXCEPTION 'prescription % has no QA clearance', NEW.id
      USING ERRCODE = 'raise_exception',
            HINT =
              'Step 10 is the last point at which this clinic can notice an incomplete file '
              'while the patient is still in the building. A prescription reaches SIGNED only '
              'after a QA officer decides CLEARED. An empty rule table means every prescription '
              'clears -- it does not mean clearance is skipped.';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER prescription_needs_qa_clearance_trigger
  BEFORE UPDATE ON read.prescription
  FOR EACH ROW EXECUTE FUNCTION core.prescription_needs_qa_clearance();

-- ---------------------------------------------------------------------------
-- 6b. A bounced prescription has to be able to come back
-- ---------------------------------------------------------------------------

-- **A defect in CP80 that only CP83 could reach.**
--
-- `core.prescription_is_frozen()` treats `submitted_at` as a stamp: once set, it may never
-- change. That is right for `signed_at` and `printed_at` — a signing instant that can be moved
-- proves nothing — and it is wrong for this one, because `QA_REVIEW -> DRAFT` exists precisely so
-- that a prescriber can fix what QA found and send it back. `read.apply_prescription_transition`
-- writes `submitted_at = at` on every entry to QA_REVIEW, so the second submission raised
-- *"a stamp that has been set cannot be changed"* and **the bounce workflow was a one-way door**:
-- a bounced prescription could be edited and could never be resubmitted, signed or printed.
--
-- Nobody could reach it before this checkpoint. CP80 built the edge and left the workflow to
-- CP83, and its own tests drove DRAFT -> QA_REVIEW -> SIGNED and never round the loop.
--
-- The fix is the narrowest one that restores the loop: `submitted_at` may move **forwards**, and
-- only while the prescription is entering QA_REVIEW. It cannot be moved backwards, cannot be
-- cleared, and cannot be touched in any other transition — so "when was this last sent to QA" is
-- still a fact nobody can rewrite, and it now answers the question a reader actually has.
--
-- The clearance gate is unaffected either way: `core.qa_clearance_stands()` compares against
-- `bounced_at`, which is already overwritten by every bounce, so a resubmitted prescription needs
-- a new clearance whatever `submitted_at` says.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.prescription_is_frozen() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF coalesce(current_setting('dthcms.rebuilding', true), '') = 'on' THEN
      RETURN OLD;
    END IF;
    RAISE EXCEPTION 'a prescription is never deleted (id %)', OLD.id
      USING HINT = 'Cancel it, or correct it. Both keep the record.';
  END IF;

  IF NEW.id <> OLD.id
     OR NEW.facility_id <> OLD.facility_id
     OR NEW.patient_id <> OLD.patient_id
     OR NEW.visit_id <> OLD.visit_id
     OR NEW.created_at <> OLD.created_at
     OR NEW.created_by <> OLD.created_by
     OR NEW.created_role <> OLD.created_role
     OR NEW.created_event_id <> OLD.created_event_id
     OR NEW.corrects_prescription_id IS DISTINCT FROM OLD.corrects_prescription_id
     OR NEW.correction_reason <> OLD.correction_reason
     OR NEW.corrects_dispensed_original <> OLD.corrects_dispensed_original
     OR NEW.carried_forward_from IS DISTINCT FROM OLD.carried_forward_from THEN
    RAISE EXCEPTION 'prescription % is written once: who it is for, who wrote it, and what it corrects cannot change', OLD.id
      USING HINT = 'The ledger is the truth. A change to a prescription is a correction that supersedes it.';
  END IF;

  -- The resubmission, and nothing else. Forwards only, into QA_REVIEW only, never to NULL.
  IF OLD.submitted_at IS NOT NULL AND NEW.submitted_at IS DISTINCT FROM OLD.submitted_at THEN
    IF NOT (NEW.status = 'QA_REVIEW'
            AND NEW.submitted_at IS NOT NULL
            AND NEW.submitted_at >= OLD.submitted_at) THEN
      RAISE EXCEPTION 'prescription %: the submission instant only moves forwards, and only on a resubmission to QA', OLD.id
        USING HINT = 'A prescription bounced by QA is resubmitted; anything else that moved this stamp would be rewriting when the prescriber finished.';
    END IF;
  END IF;

  IF (OLD.signed_at    IS NOT NULL AND NEW.signed_at    IS DISTINCT FROM OLD.signed_at)
     OR (OLD.signed_by    IS NOT NULL AND NEW.signed_by    IS DISTINCT FROM OLD.signed_by)
     OR (OLD.printed_at   IS NOT NULL AND NEW.printed_at   IS DISTINCT FROM OLD.printed_at)
     OR (OLD.dispensed_at IS NOT NULL AND NEW.dispensed_at IS DISTINCT FROM OLD.dispensed_at)
     OR (OLD.cancelled_at IS NOT NULL AND NEW.cancelled_at IS DISTINCT FROM OLD.cancelled_at)
     OR (OLD.corrected_at IS NOT NULL AND NEW.corrected_at IS DISTINCT FROM OLD.corrected_at)
     OR (OLD.corrected_by_prescription_id IS NOT NULL
         AND NEW.corrected_by_prescription_id IS DISTINCT FROM OLD.corrected_by_prescription_id) THEN
    RAISE EXCEPTION 'prescription %: a stamp that has been set cannot be changed', OLD.id;
  END IF;

  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- 7. Permissions
-- ---------------------------------------------------------------------------

-- `qa.review`, `qa.clear` and `qa.bounce` already exist (CP15's catalogue) and are held by QA;
-- `qa.clear` is also held by PHYSICIAN. Two are new.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('qa.override', 'qa', 'override', '',
   'Let a blocked prescription past the QA gate, with a recorded reason', true),
  ('qa.rule.write', 'qa', 'rule', 'write',
   'Change the QA checklist: severity, bounce station, recency window, enabled', true)
ON CONFLICT (code) DO UPDATE SET
  resource = EXCLUDED.resource, action = EXCLUDED.action, scope = EXCLUDED.scope,
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

-- **Consultant-level, and not the QA officer's.** `docs/qa-rules.md` §2: the override rate is
-- Quality's to watch, not the prescriber's, and the person granting an override should not be
-- the person monitoring how often it happens. So QA holds `qa.review` (and therefore the rate
-- view) and does not hold `qa.override`; the consultant holds the override and not the rate.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'qa.override' FROM core.role r WHERE r.code IN ('PHYSICIAN', 'ADMIN')
ON CONFLICT DO NOTHING;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'qa.rule.write' FROM core.role r WHERE r.code IN ('PHYSICIAN', 'ADMIN')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- 8. The projections
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_qa_reviewed(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.qa_review (
    id, facility_id, prescription_id, patient_id, visit_id, outcome,
    decided_at, decided_by, decided_role, bounce_station_code, reason_en, reason_bn,
    findings, acknowledged, override_id, event_id, global_seq)
  VALUES (
    (p->>'review_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'prescription_id')::uuid,
    (p->>'patient_id')::uuid,
    (p->>'visit_id')::uuid,
    p->>'outcome',
    (p->>'decided_at')::timestamptz,
    (p->>'decided_by')::uuid,
    coalesce(p->>'decided_role', ''),
    nullif(p->>'bounce_station_code', ''),
    coalesce(p->>'reason_en', ''),
    coalesce(p->>'reason_bn', ''),
    CASE WHEN jsonb_typeof(p->'findings') = 'array' THEN p->'findings' ELSE '[]'::jsonb END,
    -- `jsonb_typeof` rather than `coalesce`, because an absent key and a JSON null are two
    -- different things here and only the first is SQL NULL. A payload whose `acknowledged` field
    -- marshalled as `null` -- which is what an empty Go slice does -- would make coalesce pass a
    -- scalar to jsonb_array_elements_text and the projection would refuse the event.
    CASE WHEN jsonb_typeof(p->'acknowledged') = 'array'
         THEN coalesce((SELECT array_agg(value::text)
                          FROM jsonb_array_elements_text(p->'acknowledged')), ARRAY[]::text[])
         ELSE ARRAY[]::text[] END,
    nullif(p->>'override_id', '')::uuid,
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (id) DO NOTHING;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_qa_overridden(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.qa_override (
    id, facility_id, prescription_id, patient_id, visit_id,
    granted_at, granted_by, granted_role, reason, blocking_at_grant, event_id, global_seq)
  VALUES (
    (p->>'override_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'prescription_id')::uuid,
    (p->>'patient_id')::uuid,
    (p->>'visit_id')::uuid,
    (p->>'granted_at')::timestamptz,
    (p->>'granted_by')::uuid,
    coalesce(p->>'granted_role', ''),
    p->>'reason',
    CASE WHEN jsonb_typeof(p->'blocking') = 'array'
         THEN coalesce((SELECT array_agg(value::text)
                          FROM jsonb_array_elements_text(p->'blocking')), ARRAY[]::text[])
         ELSE ARRAY[]::text[] END,
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (id) DO NOTHING;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_qa_reviewed(jsonb)    TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_qa_overridden(jsonb)  TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- 9. The rules, seeded from docs/qa-rules.md
-- ---------------------------------------------------------------------------

-- Eighteen rows, one per rule in §3, for every facility. They are rows: Dr Nahid retires rule 7,
-- moves rule 10 from BLOCK to WARN, or widens rule 4's window from 180 days to 270, and none of
-- those is a release.
INSERT INTO core.qa_rule
  (facility_id, code, kind, severity, bounce_station_code, window_days, params,
   title_en, title_bn, detail_en, detail_bn, ordering)
SELECT f.id, r.code, r.kind, r.severity, r.station, r.window_days, r.params::jsonb,
       r.title_en, r.title_bn, r.detail_en, r.detail_bn, r.ordering
  FROM core.facility f
 CROSS JOIN (VALUES
  -- §3.1 Safety, inherited from CP78 rather than reimplemented.
  ('SAFETY_UNRESOLVED', 'SAFETY_ENGINE_FINDING', 'BLOCK', 'STN_CONSULTATION', NULL,
   '{"min_severity":"BLOCK","rule_types":["INTERACTION","DUPLICATE_THERAPY","CONTRAINDICATION","MAX_DOSE","PAEDIATRIC","HEPATIC"]}',
   'Unresolved interaction or duplicate',
   'অমীমাংসিত ওষুধ-সংঘাত বা একই ওষুধ দুইবার',
   'The medication safety engine is still blocking this sheet. Resolve it in the consultation room.',
   'ওষুধ নিরাপত্তা ইঞ্জিন এখনও এই ব্যবস্থাপত্র আটকে রেখেছে। পরামর্শ কক্ষে এটি মীমাংসা করুন।', 10),
  ('ALLERGY_STATUS', 'ALLERGY_STATUS_ASSERTED', 'BLOCK', 'STN_HISTORY', NULL, '{}',
   'Allergy status not asserted',
   'অ্যালার্জির অবস্থা নিশ্চিত করা হয়নি',
   'Nobody has recorded whether this patient has allergies. "No known allergy" is an answer; silence is not.',
   'এই রোগীর অ্যালার্জি আছে কিনা কেউ লেখেননি। "জানা অ্যালার্জি নেই" একটি উত্তর; নীরবতা নয়।', 20),
  ('ALLERGY_CONFLICT', 'SAFETY_ENGINE_FINDING', 'BLOCK', 'STN_CONSULTATION', NULL,
   '{"min_severity":"BLOCK","rule_types":["ALLERGY"]}',
   'A prescribed drug the patient is recorded allergic to',
   'রোগীর নথিভুক্ত অ্যালার্জি আছে এমন ওষুধ লেখা হয়েছে',
   'The safety engine matched a prescribed drug to a recorded allergy.',
   'নিরাপত্তা ইঞ্জিন লেখা ওষুধের সঙ্গে নথিভুক্ত অ্যালার্জির মিল পেয়েছে।', 30),

  -- §3.2 Diabetes.
  ('DIABETES_HBA1C', 'OBSERVATION_RECENT', 'BLOCK', 'STN_CONSULTATION', 180,
   '{"codes":["HBA1C"],"accept_ordered":true,"when_diagnosis":["E10","E11","E12","E13","E14"]}',
   'Diabetic with no HbA1c recorded or ordered in six months',
   'ডায়াবেটিস রোগী, ছয় মাসে এইচবিএ১সি নেওয়া বা লেখা হয়নি',
   'Record the result, or order the test. An ordered test counts: the consultant has done the right thing and the lab''s turnaround is not his to answer for.',
   'ফল লিখুন, অথবা পরীক্ষাটি দিন। পরীক্ষা দেওয়া হলেই যথেষ্ট: চিকিৎসক ঠিক কাজটিই করেছেন, ল্যাবের দেরির জন্য তিনি দায়ী নন।', 40),
  ('DIABETES_BP', 'OBSERVATION_THIS_VISIT', 'BLOCK', 'STN_EXAMINATION', NULL,
   '{"codes":["BP_SYSTOLIC","BP_DIASTOLIC"],"when_diagnosis":["E10","E11","E12","E13","E14"]}',
   'Diabetic with no blood pressure this visit',
   'ডায়াবেটিস রোগী, এই পরিদর্শনে রক্তচাপ নেওয়া হয়নি',
   'Thirty seconds at a station the patient already walked past, and in a diabetic it is the number most likely to be what actually kills them.',
   'রোগী যে কেন্দ্রের পাশ দিয়েই এসেছেন সেখানে ত্রিশ সেকেন্ডের কাজ, আর ডায়াবেটিসে এই সংখ্যাটিই সবচেয়ে বেশি প্রাণঘাতী।', 50),
  ('RENAL_WINDOW', 'RENAL_WINDOW', 'BLOCK', 'STN_CONSULTATION', NULL, '{}',
   'A renally-dosed drug with no eGFR inside the facility''s window',
   'কিডনি-নির্ভর ওষুধ, অথচ প্রতিষ্ঠানের সময়সীমার ভেতরে ইজিএফআর নেই',
   'Metformin''s dose is a function of eGFR. Prescribing against a number from two years ago is prescribing against a number that may no longer exist.',
   'মেটফরমিনের মাত্রা ইজিএফআর-এর উপর নির্ভর করে। দুই বছর আগের সংখ্যা দেখে লেখা মানে এমন সংখ্যা দেখে লেখা যা হয়তো আর নেই।', 60),
  ('DIABETES_FOOT', 'OBSERVATION_RECENT', 'WARN', 'STN_EXAMINATION', 365,
   '{"codes":["MONOFILAMENT_LEFT","MONOFILAMENT_RIGHT","FOOT_RISK_LEFT","FOOT_RISK_RIGHT"],"when_diagnosis":["E10","E11","E12","E13","E14"]}',
   'Diabetic with no foot examination in twelve months',
   'ডায়াবেটিস রোগী, বারো মাসে পায়ের পরীক্ষা হয়নি',
   'The examination station can do it today.',
   'পরীক্ষা কেন্দ্র আজই এটি করতে পারে।', 70),
  ('DIABETES_EYE', 'OBSERVATION_RECENT', 'WARN', 'STN_CONSULTATION', 365,
   '{"codes":["RETINOPATHY_SCREEN","RETINOPATHY_LEFT","RETINOPATHY_RIGHT","FUNDUS_FINDING"],"accept_ordered":true,"when_diagnosis":["E10","E11","E12","E13","E14"]}',
   'Diabetic with no retinopathy screening in twelve months',
   'ডায়াবেটিস রোগী, বারো মাসে রেটিনোপ্যাথি স্ক্রিনিং হয়নি',
   'Refer or record the screening.',
   'রেফার করুন অথবা স্ক্রিনিং লিখুন।', 80),
  ('DIABETES_LIPIDS', 'OBSERVATION_RECENT', 'WARN', 'STN_CONSULTATION', 365,
   '{"codes":["CHOL_LDL","CHOL_TOTAL","CHOL_HDL","TRIGLYCERIDE"],"accept_ordered":true,"when_diagnosis":["E11"]}',
   'Type 2 diabetic with no lipid profile in twelve months',
   'টাইপ ২ ডায়াবেটিস রোগী, বারো মাসে লিপিড প্রোফাইল হয়নি',
   'Order it, or record the result if it is on paper in front of you.',
   'পরীক্ষাটি দিন, অথবা কাগজে ফল সামনে থাকলে সেটি লিখুন।', 90),
  ('INSULIN_EDUCATION', 'EDUCATION_THIS_VISIT', 'BLOCK', 'STN_RX_EDUCATION', NULL,
   '{"when_classes":["INSULIN_RAPID_ACTING_ANALOGUE","INSULIN_SHORT_ACTING_HUMAN","INSULIN_INTERMEDIATE_ACTING_HUMAN","INSULIN_LONG_ACTING_ANALOGUE","INSULIN_ULTRA_LONG_ACTING_ANALOGUE","INSULIN_PREMIXED_HUMAN","INSULIN_PREMIXED_ANALOGUE","GLP_1_RECEPTOR_AGONIST"]}',
   'Insulin or a GLP-1 pen prescribed with no education record this visit',
   'ইনসুলিন বা জিএলপি-১ পেন লেখা হয়েছে, এই পরিদর্শনে শিক্ষা কেন্দ্রের নথি নেই',
   'A first pen handed over with nobody watching the patient use it is the commonest avoidable treatment failure in this clinic''s population.',
   'প্রথম পেন হাতে দিয়ে দেওয়া, অথচ রোগী কীভাবে ব্যবহার করছেন কেউ দেখেননি — এই ক্লিনিকের রোগীদের মধ্যে এটিই সবচেয়ে সাধারণ এড়ানো-যোগ্য চিকিৎসা ব্যর্থতা।', 100),

  -- §3.3 Thyroid.
  ('THYROID_TSH', 'OBSERVATION_RECENT', 'BLOCK', 'STN_CONSULTATION', 180,
   '{"codes":["TSH"],"accept_ordered":true,"when_classes":["THYROID_HORMONE","ANTITHYROID"]}',
   'Thyroid drug prescribed with no TSH in six months',
   'থাইরয়েডের ওষুধ লেখা হয়েছে, ছয় মাসে টিএসএইচ হয়নি',
   'Record the result, or order the test.',
   'ফল লিখুন, অথবা পরীক্ষাটি দিন।', 110),
  ('ANTITHYROID_COUNSEL', 'COUNSELING_ITEM_COVERED', 'BLOCK', 'STN_COUNSELING', NULL,
   '{"item_codes":["AGRANULOCYTOSIS_WARNING"],"when_classes":["ANTITHYROID"]}',
   'Antithyroid drug with the agranulocytosis warning not counselled',
   'অ্যান্টিথাইরয়েড ওষুধ, অ্যাগ্রানুলোসাইটোসিসের সতর্কবার্তা বোঝানো হয়নি',
   'Agranulocytosis is survivable only if the patient knows that a sore throat and fever means stop the drug and get a blood count today.',
   'অ্যাগ্রানুলোসাইটোসিস থেকে বাঁচা যায় কেবল তখনই, যখন রোগী জানেন গলাব্যথা ও জ্বর হলে ওষুধ বন্ধ করে সেদিনই রক্ত পরীক্ষা করাতে হবে।', 120),
  ('ANTITHYROID_FBC', 'OBSERVATION_RECENT', 'WARN', 'STN_CONSULTATION', 180,
   '{"codes":["WBC"],"accept_ordered":true,"when_classes":["ANTITHYROID"]}',
   'Antithyroid drug with no baseline full blood count',
   'অ্যান্টিথাইরয়েড ওষুধ, প্রাথমিক রক্তের সম্পূর্ণ পরীক্ষা নেই',
   'A baseline count is what a later one is compared against.',
   'পরে করা পরীক্ষাটি এই প্রাথমিক ফলের সঙ্গেই মেলানো হয়।', 130),

  -- §3.4 Pregnancy. Inert until the teratogenic list is written.
  ('TERATOGEN_PREGNANCY', 'TERATOGEN_PREGNANCY_STATUS', 'BLOCK', 'STN_HISTORY', NULL,
   '{"age_min":15,"age_max":49}',
   'Teratogenic drug for a woman who could be pregnant, with no pregnancy status this visit',
   'গর্ভধারণক্ষম নারীকে ভ্রূণ-ক্ষতিকর ওষুধ, এই পরিদর্শনে গর্ভাবস্থার তথ্য নেই',
   'The age band is a proxy and a crude one. It will occasionally ask an irrelevant question, and that is the correct direction to be wrong in.',
   'বয়সের সীমাটি একটি স্থূল অনুমান। মাঝে মাঝে অপ্রাসঙ্গিক প্রশ্ন করবে, আর ভুল করার এটিই সঠিক দিক।', 140),

  -- §3.5 Completeness.
  ('DIAGNOSIS_CODED', 'DIAGNOSIS_CODED_THIS_VISIT', 'BLOCK', 'STN_CONSULTATION', NULL, '{}',
   'No diagnosis coded on this visit',
   'এই পরিদর্শনে কোনো সাংকেতিক রোগনির্ণয় নেই',
   'An uncoded visit is invisible to every report this clinic will ever run on itself. A year of them is a year that cannot be analysed.',
   'সাংকেতিক নয় এমন পরিদর্শন ক্লিনিকের কোনো প্রতিবেদনেই ধরা পড়ে না। এক বছরের এমন নথি মানে এক বছরের বিশ্লেষণ অসম্ভব।', 150),
  ('COUNSELING_COMPLETE', 'COUNSELING_COMPLETE', 'BLOCK', 'STN_COUNSELING', NULL, '{}',
   'Counselling checklist for this visit incomplete',
   'এই পরিদর্শনের পরামর্শ তালিকা অসম্পূর্ণ',
   'CP57''s gate already asked. This is the last person who can catch it.',
   'সিপি৫৭-এর গেট আগেই জিজ্ঞেস করেছে। এটিই শেষ ব্যক্তি যিনি ধরতে পারেন।', 160),
  ('MANDATORY_STATIONS', 'MANDATORY_STATION_DATA', 'WARN', NULL, NULL, '{}',
   'A mandatory station on this visit''s route recorded nothing',
   'এই পরিদর্শনের পথে বাধ্যতামূলক একটি কেন্দ্র কিছুই লেখেনি',
   'The bounce is to the station itself, which is why this rule names no single one.',
   'ফেরত পাঠানো হয় সেই কেন্দ্রেই, তাই এই নিয়মটি কোনো একটি কেন্দ্রের নাম বলে না।', 170),
  ('WEIGHT_BASED_DOSE', 'WEIGHT_FOR_WEIGHT_BASED_DOSE', 'BLOCK', 'STN_ANTHROPOMETRY', NULL,
   '{"codes":["BODY_WEIGHT"]}',
   'Weight-based dose with no weight recorded this visit',
   'ওজন-নির্ভর মাত্রা, এই পরিদর্শনে ওজন নেওয়া হয়নি',
   'A dose per kilogram against last year''s weight is a dose against a number that has moved.',
   'গত বছরের ওজন ধরে প্রতি কেজির মাত্রা মানে বদলে যাওয়া সংখ্যা ধরে মাত্রা।', 180)
 ) AS r(code, kind, severity, station, window_days, params,
        title_en, title_bn, detail_en, detail_bn, ordering)
 -- A rule whose bounce station this facility does not have is not seeded at all, rather than
 -- seeded pointing at a room that is not there: core.qa_rule's own trigger would refuse it, and a
 -- clinic that later opens the station adds the row.
 WHERE r.station IS NULL
    OR EXISTS (SELECT 1 FROM core.station s
                WHERE s.facility_id = f.id AND s.code = r.station);

-- ---------------------------------------------------------------------------
-- 10. The invariants
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
-- 134. The shapes are not the application's to invent.
CREATE OR REPLACE FUNCTION core.assert_qa_rule_kinds_are_not_writable_by_the_application()
RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  SELECT string_agg(p.privilege, ', ' ORDER BY p.privilege) INTO offender
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'core'
   CROSS JOIN (VALUES ('INSERT'), ('UPDATE'), ('DELETE'), ('TRUNCATE')) AS p(privilege)
   WHERE c.relname = 'qa_rule_kind'
     AND has_table_privilege('dthcms_app', c.oid, p.privilege);

  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'the application can write the QA rule shapes: %', offender
      USING HINT =
        'Every kind in core.qa_rule_kind is one Go predicate. A row naming a kind nothing '
        'implements is a rule that is present, enabled and permanently silent -- which is worse '
        'than no rule, because the screen shows it and the patient walks out anyway.';
  END IF;

  -- DELETE on core.qa_rule is the same defect one table over: a deleted rule takes the
  -- explanation of every review that cited it with it.
  IF has_table_privilege('dthcms_app', 'core.qa_rule', 'DELETE') THEN
    RAISE EXCEPTION 'the application can delete QA rules'
      USING HINT = 'Rules are retired, not deleted. read.qa_review cites them by code.';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- 135. The gate is wired, and it covers every way into the statuses it guards.
--
-- A migration that dropped the trigger while keeping the function would leave a clinic printing
-- uncleared prescriptions **and no error** — everybody would still believe there was a gate. The
-- second half is the one that will matter later: a thirteenth transition row, added by somebody
-- solving a different problem, must not be a new way into SIGNED that the gate does not see.
CREATE OR REPLACE FUNCTION core.assert_the_qa_gate_is_wired() RETURNS void
LANGUAGE plpgsql
SET search_path = core, pg_catalog
AS $$
DECLARE
  wired boolean;
  uncovered text;
BEGIN
  SELECT EXISTS (
    SELECT 1 FROM pg_trigger t
      JOIN pg_class c ON c.oid = t.tgrelid
      JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'read' AND c.relname = 'prescription'
       AND t.tgname = 'prescription_needs_qa_clearance_trigger'
       AND NOT t.tgisinternal)
    INTO wired;

  IF NOT wired THEN
    RAISE EXCEPTION 'the QA clearance gate is not wired to read.prescription'
      USING HINT =
        'CP83 criterion 1 is that printing is impossible without clearance, enforced '
        'server-side. A gate that is only a function is a gate nothing calls.';
  END IF;

  -- Every edge into a guarded status starts from a status that is itself guarded or from
  -- QA_REVIEW. An edge from DRAFT straight to SIGNED would be a way past the review entirely.
  SELECT string_agg(format('%s -> %s', t.from_status, t.to_status), ', ') INTO uncovered
    FROM core.prescription_transition t
   WHERE t.to_status IN ('SIGNED', 'PRINTED', 'DISPENSED')
     AND t.from_status NOT IN ('QA_REVIEW', 'SIGNED', 'PRINTED', 'DISPENSED');

  IF uncovered IS NOT NULL THEN
    RAISE EXCEPTION 'these transitions reach a signed or printed prescription without passing QA: %', uncovered
      USING HINT =
        'The gate asks for a clearance when a prescription enters SIGNED, PRINTED or DISPENSED '
        'from outside that set. An edge from DRAFT would be checked too -- but a prescription '
        'that never entered QA_REVIEW has nothing to clear it, so the edge itself is the defect.';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- 136. Every review and every override says who, and why.
CREATE OR REPLACE FUNCTION core.assert_every_qa_decision_is_accountable() RETURNS void
LANGUAGE plpgsql
SET search_path = read, core, pg_catalog
AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders FROM read.qa_override
   WHERE btrim(reason) = '' OR granted_by IS NULL;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% QA overrides have no reason or no author', offenders
      USING HINT = 'The valve is acceptable only while it is legible.';
  END IF;

  SELECT count(*) INTO offenders FROM read.qa_review
   WHERE outcome = 'BOUNCED'
     AND (bounce_station_code IS NULL OR btrim(reason_en) = '' OR btrim(reason_bn) = '');
  IF offenders > 0 THEN
    RAISE EXCEPTION '% QA bounces name no station or no reason in both languages', offenders
      USING HINT =
        '"Incomplete" without "whose" is a rule that stalls, and a reason in one language is a '
        'reason half the floor cannot read.';
  END IF;

  -- A prescription past the gate with no clearance behind it. The trigger stops new ones; this
  -- is what catches a row that arrived before the trigger existed, or through a rebuild.
  SELECT count(*) INTO offenders FROM read.prescription p
   WHERE p.status IN ('SIGNED', 'PRINTED', 'DISPENSED')
     AND NOT EXISTS (SELECT 1 FROM read.qa_review r
                      WHERE r.prescription_id = p.id AND r.outcome = 'CLEARED');
  IF offenders > 0 THEN
    RAISE EXCEPTION '% signed or printed prescriptions have no QA clearance', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- 137. Which rules can fire at all, and which are inert for want of reference data.
--
-- `docs/qa-rules.md` §3.4 says rule 14 "fires for nothing" until the teratogenic list is
-- written. That is a correct and deliberate state, and the danger is not that it is wrong — it is
-- that it is **invisible**, and looks exactly like a working rule from every screen. So it is
-- reported on every verify, with the other rules in the same condition, the way invariant 133
-- reports the glucose meter nobody stocks.
--
-- Raised, not reported, for the two conditions that are always defects: a rule naming a kind
-- nothing implements (impossible through the foreign key, so not checked), and a rule whose
-- bounce station has been deactivated under it — a patient sent to a closed room.
CREATE OR REPLACE FUNCTION core.assert_the_qa_rule_set_is_reachable() RETURNS void
LANGUAGE plpgsql
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  offending text;
BEGIN
  SELECT string_agg(DISTINCT r.code || ' -> ' || r.bounce_station_code, ', ') INTO offending
    FROM core.qa_rule r
    JOIN core.station s ON s.facility_id = r.facility_id AND s.code = r.bounce_station_code
   WHERE r.retired_at IS NULL AND r.enabled AND NOT s.is_active;

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'these QA rules bounce to a station that is not active: %', offending
      USING HINT = 'A bounce to a closed room is a patient standing in a corridor.';
  END IF;

  -- Rule 14, and anything else in its condition: enabled, implemented, and about reference data
  -- nobody has written. Reported so that whoever runs the deploy sees the same fact the officer
  -- will never see, because a rule that fires for nothing shows nothing.
  IF NOT EXISTS (SELECT 1 FROM core.medication_rule WHERE flags_teratogenic AND is_active) THEN
    RAISE NOTICE 'QA rule 14 is inert: no molecule is flagged teratogenic on core.medication_rule, '
                 'so no prescription can trigger the pregnancy-status question. This is the '
                 'documented state (docs/qa-rules.md 3.4) and not a defect; it stops being true '
                 'the day Dr Nahid publishes a PREGNANCY rule with flags_teratogenic set.';
  END IF;

  -- The same check for every other rule whose trigger set is empty: a counselling item code
  -- nothing defines, a medication class with no products, an observation code nothing writes.
  SELECT string_agg(DISTINCT r.code, ', ') INTO offending
    FROM core.qa_rule r
   CROSS JOIN LATERAL jsonb_array_elements_text(coalesce(r.params->'when_classes', '[]'::jsonb)) AS c(class)
   WHERE r.retired_at IS NULL AND r.enabled
     AND NOT EXISTS (SELECT 1 FROM core.medication_product p
                       JOIN core.generic g ON g.id = p.generic_id
                      WHERE g.class_code = c.class AND p.is_active);

  IF offending IS NOT NULL THEN
    RAISE NOTICE 'QA rules whose triggering drug class has no stocked product: %', offending;
  END IF;

  SELECT string_agg(DISTINCT r.code, ', ') INTO offending
    FROM core.qa_rule r
   CROSS JOIN LATERAL jsonb_array_elements_text(coalesce(r.params->'item_codes', '[]'::jsonb)) AS i(item)
   WHERE r.retired_at IS NULL AND r.enabled
     AND NOT EXISTS (SELECT 1 FROM core.counseling_item ci WHERE ci.item_code = i.item);

  IF offending IS NOT NULL THEN
    RAISE NOTICE 'QA rules naming a counselling item no template defines: %', offending;
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION core.assert_qa_rule_kinds_are_not_writable_by_the_application() FROM PUBLIC;

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_qa_rule_kinds_are_not_writable_by_the_application',
   'the application cannot invent QA question shapes or delete QA rules', 134),
  ('assert_the_qa_gate_is_wired',
   'the QA clearance gate is a trigger on the prescription, and every edge into a signed prescription passes it', 135),
  ('assert_every_qa_decision_is_accountable',
   'every QA bounce names a station and a bilingual reason, every override names a person and a reason, and nothing is signed uncleared', 136),
  ('assert_the_qa_rule_set_is_reachable',
   'no QA rule bounces to a closed station, and the rules that are inert for want of reference data are named', 137)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_qa_rule_kinds_are_not_writable_by_the_application',
  'assert_the_qa_gate_is_wired',
  'assert_every_qa_decision_is_accountable',
  'assert_the_qa_rule_set_is_reachable');
DROP FUNCTION IF EXISTS core.assert_the_qa_rule_set_is_reachable();
DROP FUNCTION IF EXISTS core.assert_every_qa_decision_is_accountable();
DROP FUNCTION IF EXISTS core.assert_the_qa_gate_is_wired();
DROP FUNCTION IF EXISTS core.assert_qa_rule_kinds_are_not_writable_by_the_application();

DROP TRIGGER IF EXISTS prescription_needs_qa_clearance_trigger ON read.prescription;
DROP FUNCTION IF EXISTS core.prescription_needs_qa_clearance();
DROP FUNCTION IF EXISTS core.qa_clearance_stands(uuid);

DROP FUNCTION IF EXISTS read.apply_qa_overridden(jsonb);
DROP FUNCTION IF EXISTS read.apply_qa_reviewed(jsonb);
DROP FUNCTION IF EXISTS read.apply_investigation_ordered(jsonb);

DROP TABLE IF EXISTS read.qa_review;
DROP TABLE IF EXISTS read.qa_override;
DROP TABLE IF EXISTS read.investigation_order;

DROP TRIGGER IF EXISTS qa_rule_no_delete ON core.qa_rule;
DROP FUNCTION IF EXISTS core.qa_rule_is_retired_not_deleted();
DROP TRIGGER IF EXISTS qa_rule_stays_the_shape_it_was ON core.qa_rule;
DROP FUNCTION IF EXISTS core.qa_rule_is_configurable_but_not_reshapable();
DROP TABLE IF EXISTS core.qa_rule;
DELETE FROM core.facility_scope_exemption WHERE schema_name = 'core' AND table_name = 'qa_rule_kind';
DROP TABLE IF EXISTS core.qa_rule_kind;

DELETE FROM core.role_permission WHERE permission_code IN ('qa.override', 'qa.rule.write');
DELETE FROM core.permission WHERE code IN ('qa.override', 'qa.rule.write');

ALTER TABLE core.medication_rule DROP CONSTRAINT IF EXISTS medication_rule_teratogenic_is_a_pregnancy_rule;
ALTER TABLE core.medication_rule DROP COLUMN IF EXISTS flags_teratogenic;
