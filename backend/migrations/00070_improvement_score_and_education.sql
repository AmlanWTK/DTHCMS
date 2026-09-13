-- The patient-reported improvement score (CP88) and the education station (CP92).
--
-- Both checkpoints record clinical values, and neither adds a clinical table. The values are
-- CP42 observations — one write path, one event type, one timeline, one research extract —
-- exactly as both plan entries say. What is new here is *reference data*: the question and its
-- anchors, the four device checklists, the vocabulary of answers, the rule that maps a
-- prescribed product to a device, and the threshold that raises a re-education flag. Every one
-- of those is a thing a clinician changes their mind about, and a clinician should not need a
-- release to do it.
--
-- # The one decision in this file that is not an implementation detail
--
-- **The improvement score is captured at the education station and not at the consultation.**
-- docs/patient-reported-score-and-education.md §1 is the argument: a patient asked by the
-- consultant who has just changed their treatment how much better they feel is being asked by
-- the person whose work they are grading, and the answer drifts upward — most for the patients
-- who most want to please.
--
-- The plan treated the capture station as logistics. Treating it as logistics would mean
-- documenting it and hoping. So it is enforced, in three places that fail independently:
--
--   1. **A permission of its own.** `observation.write.pro` is held by RX_EDUCATOR and by no
--      other role.
--   2. **The registry.** `core.observation_code.write_permission` for the score names it, so
--      `clinical.Service.Record` refuses any other role at the point of the write, whichever
--      route it arrived on — including the generic `POST /v1/observations` the physician can
--      reach with the permissions they do hold.
--   3. **A trigger on the read model**, below. A PRO observation recorded by a role that does
--      not hold the code's write permission cannot be stored — not by a projection rebuild, not
--      by the projector, not by a hand-written INSERT at three in the morning. That is the same
--      division migration 00026 draws for the unit rule: a check in Go protects the person
--      typing, a constraint in the database protects the record.
--
-- Station reach (ADR-0036 §1) is a fourth layer and a genuinely different one: it says *which
-- patient*, not *which role*. `observation.write.pro` carries the `observation.` prefix, so
-- `rbac.scopeFor` gives RX_EDUCATOR own-station reach for it, and the officer can record a
-- score only for the patient they are holding at station 11 right now. It is not what stops the
-- physician, because the physician's reach is facility-wide — the permission is.
--
-- # The state this migration found, stated plainly
--
-- **`IMPROVEMENT_SCORE` has existed since migration 00026 and was wired to the wrong station for
-- its whole life.** It declared `observation.write.history`, which the medical history officer at
-- station 4 holds and the prescription education officer at station 11 does not. So the station
-- the requirement names could never have recorded the score, and the station that could was the
-- one whose consultation the score is supposed to be independent of one desk further on. Nothing
-- had noticed because nothing had ever written the code: there was no screen, no route that
-- named it, and no test. A registry row is not a feature, and a permission nobody has exercised
-- is a permission nobody has checked.
--
-- Its display string was in the same condition. It read *"How much better do you feel? (1–10)"* —
-- which is spec §2's leading question verbatim, the one that presupposes improvement and offers
-- the patient a scale on which to agree. Both are corrected below.

-- +goose Up

-- ===========================================================================
-- PART ONE · The improvement score (CP88)
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- 1. The permission that decides who may ask
-- ---------------------------------------------------------------------------

-- Not sensitive. The score is one number about how a person feels, and marking it sensitive
-- would blind registration and the pharmacist to it through the §4.4 deny rules — which is not
-- the property wanted here. What is wanted is that exactly one role may *write* it, and that is
-- a grant, not a sensitivity flag.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('observation.write.pro', 'observation', 'write', 'pro',
   'Record a patient-reported outcome. Held by the prescription education officer alone, because the answer depends on who asks (CP88).',
   false)
ON CONFLICT (code) DO UPDATE SET
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'observation.write.pro' FROM core.role r WHERE r.code = 'RX_EDUCATOR'
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- 2. The question, the anchors and the banding — as data
-- ---------------------------------------------------------------------------

-- §3's last paragraph is the reason this is a table and not a Go constant. The scale leans
-- positive by construction: with "the same" at 5, a patient has four points to say they are
-- worse and five to say they are better. [R-11] names 1–10, so 1–10 is what is built — but if
-- Dr Nahid prefers the symmetric 0–10, that must be a change to reference data and a scale
-- bound, not a code release. Inserting a second row here and moving `active_from` is the whole
-- of that change.
CREATE TABLE core.pro_scale (
  code text PRIMARY KEY,

  -- The observation code this scale is the input surface for. One scale draws one code.
  observation_code text NOT NULL REFERENCES core.observation_code(code),

  -- §2's wording, and the two things about it that are deliberate: "compared with your last
  -- visit" anchors the comparison to a fixed point, and "how do you feel" rather than "how much
  -- better do you feel" lets the answer be worse. A screen renders this string; it does not
  -- carry its own copy.
  question_en text NOT NULL,
  question_bn text NOT NULL,

  min_value int NOT NULL,
  max_value int NOT NULL,

  -- Which value means "no change". Stored rather than computed as the midpoint, because on a
  -- 1–10 scale there is no midpoint and 5 is a judgement — which is precisely the weakness §3
  -- states rather than hides.
  neutral_value int NOT NULL,

  -- Exactly one scale per observation code is live at a time. A retired scale stays, because
  -- scores recorded against it are still facts and a screen rendering an old score needs the
  -- anchors it was given.
  retired_at timestamptz,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT pro_scale_range_is_ordered CHECK (min_value < max_value),
  CONSTRAINT pro_scale_neutral_is_inside CHECK (neutral_value BETWEEN min_value AND max_value),
  CONSTRAINT pro_scale_bilingual CHECK (btrim(question_en) <> '' AND btrim(question_bn) <> '')
);

CREATE UNIQUE INDEX pro_scale_one_live_per_code
  ON core.pro_scale (observation_code) WHERE retired_at IS NULL;

SELECT core.attach_updated_at('core.pro_scale');

-- One band of the scale, with what it means in both languages and what it looks like.
--
-- The faces and the colour ramp are here rather than in the stylesheet for the same reason the
-- wording is: the symmetric variant has a different number of bands, and a screen that held its
-- own five faces would draw five of them over a six-band scale and nobody would notice until a
-- patient pointed at the wrong one.
CREATE TABLE core.pro_scale_anchor (
  scale_code text NOT NULL REFERENCES core.pro_scale(code) ON DELETE CASCADE,

  -- Inclusive. A band is a range because §3's table is a range: 1–2 is "much worse" and there is
  -- no separate meaning for 1 as against 2.
  from_value int NOT NULL,
  to_value   int NOT NULL,

  label_en text NOT NULL,
  label_bn text NOT NULL,

  -- The face, as a rank from 1 (worst) to n (best) rather than as a glyph. A glyph in the
  -- database would be a rendering decision taken by a DBA; a rank is the clinical fact, and the
  -- screen owns how a rank looks. It is also what lets the same data drive a printed sheet, a
  -- tablet and a screen reader.
  face_rank int NOT NULL,

  ordering int NOT NULL,

  PRIMARY KEY (scale_code, from_value),
  CONSTRAINT pro_scale_anchor_band_is_ordered CHECK (from_value <= to_value),
  CONSTRAINT pro_scale_anchor_bilingual CHECK (btrim(label_en) <> '' AND btrim(label_bn) <> '')
);

CREATE INDEX pro_scale_anchor_by_scale ON core.pro_scale_anchor (scale_code, ordering);

-- Why a first visit has no score, as a closed vocabulary rather than a flag.
--
-- §2: for a first visit there is no last visit, so the question is *not asked*, and its absence
-- is recorded as not-applicable rather than as a missing value. Those are different facts and
-- the difference is the same one CP82 draws between "he said no" and "he did not look": the
-- absence of a row means nobody got to it, and a row means somebody decided. A researcher
-- counting first visits as low scorers, or as non-responders, would be wrong in opposite
-- directions.
CREATE TABLE core.pro_not_applicable_reason (
  code text PRIMARY KEY,
  display_en text NOT NULL,
  display_bn text NOT NULL,
  ordering int NOT NULL,
  retired_at timestamptz,

  CONSTRAINT pro_na_reason_code_format CHECK (code ~ '^[a-z][a-z0-9_]{0,39}$'),
  CONSTRAINT pro_na_reason_bilingual CHECK (btrim(display_en) <> '' AND btrim(display_bn) <> '')
);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'pro_scale',
   'The wording of a patient-reported question is a clinical instrument, not a clinic setting. Two facilities asking it differently would make the numbers incomparable, which is the one thing this score is for (CP88).'),
  ('core', 'pro_scale_anchor',
   'The bands of an instrument belong to the instrument (CP88).'),
  ('core', 'pro_not_applicable_reason',
   'Why a question was not asked is a property of the question (CP88).')
ON CONFLICT (schema_name, table_name) DO NOTHING;

GRANT SELECT ON core.pro_scale TO dthcms_app;
GRANT SELECT ON core.pro_scale_anchor TO dthcms_app;
GRANT SELECT ON core.pro_not_applicable_reason TO dthcms_app;
GRANT SELECT ON core.pro_scale TO dthcms_projector;
GRANT SELECT ON core.pro_scale_anchor TO dthcms_projector;
GRANT SELECT ON core.pro_not_applicable_reason TO dthcms_projector;

COMMENT ON TABLE core.pro_scale IS
  'The question, its bounds and where "no change" sits. Data so the symmetric 0-10 variant is a data change (CP88 spec §3).';
COMMENT ON TABLE core.pro_scale_anchor IS
  'What each band of the scale means, in both languages, with the rank of its face (CP88 spec §3).';

-- ---------------------------------------------------------------------------
-- 3. The registry rows the score needs
-- ---------------------------------------------------------------------------

-- The score's wording moves onto the spec's, and its write permission moves to the education
-- station. The old display asked "How much better do you feel?", which is §2's leading question
-- verbatim — it presupposes improvement and offers the patient a scale on which to agree.
UPDATE core.observation_code
   SET display_en = 'Improvement since last visit (1-10)',
       display_bn = 'গতবারের তুলনায় এখনকার অবস্থা (১–১০)',
       write_permission = 'observation.write.pro'
 WHERE code = 'IMPROVEMENT_SCORE';

-- The marker that says the question was not asked and why.
--
-- A second code rather than a nullable column or a magic number on the first. A magic number is
-- the design that fails: 0 is outside the 1-10 band so the database would refuse it, and any
-- number inside the band is a score somebody will average. A second code cannot be averaged by
-- accident, and `read.observation` already refuses a coded value that is not in the vocabulary.
INSERT INTO core.observation_code
  (code, category, value_type, dimension, loinc, display_en, display_bn,
   min_canonical, max_canonical, write_permission) VALUES
  ('IMPROVEMENT_SCORE_NA', 'PRO', 'coded', NULL, '',
   'Improvement score not applicable', 'অগ্রগতির স্কোর প্রযোজ্য নয়',
   NULL, NULL, 'observation.write.pro')
ON CONFLICT (code) DO UPDATE SET
  category = EXCLUDED.category, value_type = EXCLUDED.value_type,
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  write_permission = EXCLUDED.write_permission;

INSERT INTO core.pro_not_applicable_reason (code, display_en, display_bn, ordering) VALUES
  ('first_visit', 'First visit - there is no last visit to compare with',
   'প্রথম আসা — তুলনা করার মতো আগের কোনো দিন নেই', 10)
ON CONFLICT (code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn, ordering = EXCLUDED.ordering;

INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal)
SELECT 'IMPROVEMENT_SCORE_NA', r.code, r.display_en, r.display_bn, r.ordering, false
  FROM core.pro_not_applicable_reason r
ON CONFLICT (code, value_code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn, ordering = EXCLUDED.ordering;

-- The scale itself. §2's wording and §3's five bands, verbatim.
INSERT INTO core.pro_scale
  (code, observation_code, question_en, question_bn, min_value, max_value, neutral_value) VALUES
  ('IMPROVEMENT_1_10', 'IMPROVEMENT_SCORE',
   'Compared with your last visit, how do you feel now?',
   'গতবারের তুলনায় এখন আপনার কেমন লাগছে?',
   1, 10, 5)
ON CONFLICT (code) DO UPDATE SET
  question_en = EXCLUDED.question_en, question_bn = EXCLUDED.question_bn,
  min_value = EXCLUDED.min_value, max_value = EXCLUDED.max_value,
  neutral_value = EXCLUDED.neutral_value;

INSERT INTO core.pro_scale_anchor
  (scale_code, from_value, to_value, label_en, label_bn, face_rank, ordering) VALUES
  ('IMPROVEMENT_1_10',  1,  2, 'Much worse',     'অনেক খারাপ', 1, 10),
  ('IMPROVEMENT_1_10',  3,  4, 'A little worse', 'একটু খারাপ', 2, 20),
  ('IMPROVEMENT_1_10',  5,  5, 'About the same', 'আগের মতোই',  3, 30),
  ('IMPROVEMENT_1_10',  6,  7, 'A little better','একটু ভালো',  4, 40),
  ('IMPROVEMENT_1_10',  8, 10, 'Much better',    'অনেক ভালো',  5, 50)
ON CONFLICT (scale_code, from_value) DO UPDATE SET
  to_value = EXCLUDED.to_value, label_en = EXCLUDED.label_en, label_bn = EXCLUDED.label_bn,
  face_rank = EXCLUDED.face_rank, ordering = EXCLUDED.ordering;

-- ---------------------------------------------------------------------------
-- 4. The trigger: a patient-reported value names the role that is allowed to ask
-- ---------------------------------------------------------------------------

-- Narrow on purpose, and it is worth saying why it is not the obvious general rule.
--
-- The general rule — "no observation may be recorded by a role that does not hold its code's
-- write permission" — is the right sentence and the wrong constraint to add here. DERIVED values
-- are written by the server under whichever role recorded the inputs, historical rows predate
-- several permission renames, and a blanket trigger would refuse a projection rebuild of data
-- that was recorded lawfully under an older catalogue. Refusing to replay history is a worse
-- failure than the one being prevented.
--
-- PRO is a category of two codes, both introduced or re-homed by this migration, and its whole
-- reason for existing is that the answer depends on who asked. So the rule is held exactly where
-- it is load-bearing, and invariant 132 then asserts that no row already violates it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.patient_reported_value_names_its_asker() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  required text;
BEGIN
  IF NEW.category <> 'PRO' THEN
    RETURN NEW;
  END IF;

  SELECT write_permission INTO required FROM core.observation_code WHERE code = NEW.code;

  IF NOT EXISTS (
    SELECT 1 FROM core.role_permission rp
      JOIN core.role r ON r.id = rp.role_id
     WHERE r.code = NEW.recorded_role AND rp.permission_code = required)
  THEN
    RAISE EXCEPTION '% was recorded by % , which does not hold %', NEW.code, NEW.recorded_role, required
      USING HINT =
        'A patient-reported outcome is evidence about how a person feels, and it drifts toward '
        'whatever the asker wants to hear (CP88 spec §1). Which role asked is therefore part of '
        'whether the number means anything. Grant the role the permission if the clinic has '
        'genuinely changed who asks -- and understand that doing so changes what every earlier '
        'score is comparable with.';
  END IF;

  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- AFTER the well-formedness trigger, because that one sets NEW.category from the registry and
-- this one reads it. Trigger order within a timing class is alphabetical in PostgreSQL, and
-- `observation_is_well_formed` sorts before `observation_pro_names_its_asker` — which is a fact
-- about two strings and therefore worth a name that keeps it true rather than a comment hoping
-- it stays that way. The invariant below does not depend on the ordering.
CREATE TRIGGER observation_pro_names_its_asker
  BEFORE INSERT OR UPDATE ON read.observation
  FOR EACH ROW EXECUTE FUNCTION core.patient_reported_value_names_its_asker();

-- ===========================================================================
-- PART TWO · The education station (CP92)
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- 5. Reading back what was demonstrated
-- ---------------------------------------------------------------------------

-- Criterion 2: competency is visible to the physician at the next visit. `education.record` is
-- the write and has been in the catalogue since 00006; there was no permission for the read, so
-- the physician had no lawful way to see it.
--
-- Deliberately not folded into `observation.read.values`. That permission is the whole clinical
-- record; this is one station's output, and the three roles that need it are the officer who
-- wrote it and the two who consult with it in front of them.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('education.read', 'education', 'read', '',
   'Read what a patient was able to demonstrate at the education station, and what they reported about missed doses (CP92).',
   false)
ON CONFLICT (code) DO UPDATE SET
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'education.read' FROM core.role r
 WHERE r.code IN ('RX_EDUCATOR', 'PHYSICIAN', 'JUNIOR_DOCTOR')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- 6. Device types, checklists and their items
-- ---------------------------------------------------------------------------

CREATE TABLE core.education_device_type (
  code text PRIMARY KEY,
  name_en text NOT NULL,
  name_bn text NOT NULL,
  ordering int NOT NULL,
  retired_at timestamptz,

  CONSTRAINT education_device_type_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,39}$'),
  CONSTRAINT education_device_type_bilingual CHECK (btrim(name_en) <> '' AND btrim(name_bn) <> '')
);

CREATE TABLE core.education_checklist (
  code text PRIMARY KEY,
  device_type text NOT NULL REFERENCES core.education_device_type(code),

  title_en text NOT NULL,
  title_bn text NOT NULL,

  -- Retired rather than deleted, like every vocabulary in this schema: an assessment recorded
  -- against a checklist that has since been rewritten must still render as the officer saw it.
  retired_at timestamptz,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT education_checklist_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,39}$'),
  CONSTRAINT education_checklist_bilingual CHECK (btrim(title_en) <> '' AND btrim(title_bn) <> '')
);

CREATE UNIQUE INDEX education_checklist_one_live_per_device
  ON core.education_checklist (device_type) WHERE retired_at IS NULL;

SELECT core.attach_updated_at('core.education_checklist');

-- One line of one checklist.
--
-- Each item names an observation code, and that is the join that makes "no new clinical schema"
-- true: the *result* of this line is an ordinary CP42 observation, and this table is only the
-- question. It also means an item's history is queryable the way every other clinical value is —
-- "how many patients failed the air-shot this quarter" is a query against `read.observation`
-- with no knowledge of this table at all.
CREATE TABLE core.education_checklist_item (
  checklist_code text NOT NULL REFERENCES core.education_checklist(code),
  ordinal int NOT NULL,

  observation_code text NOT NULL UNIQUE REFERENCES core.observation_code(code),

  text_en text NOT NULL,
  text_bn text NOT NULL,

  -- §6.1's note: items 2, 4 and 8 are the three that silently cost a patient their dose, and all
  -- three are invisible in the record unless somebody watches. Marked so a screen can weight them
  -- and a report can count them, not so the scoring changes — §8 forbids rolling these into a
  -- percentage and this column must never become a weight.
  is_critical boolean NOT NULL DEFAULT false,

  retired_at timestamptz,

  PRIMARY KEY (checklist_code, ordinal),
  CONSTRAINT education_checklist_item_bilingual CHECK (btrim(text_en) <> '' AND btrim(text_bn) <> '')
);

CREATE INDEX education_checklist_item_by_checklist
  ON core.education_checklist_item (checklist_code, ordinal);

-- ---------------------------------------------------------------------------
-- 7. The mapping from a prescribed product to a device
-- ---------------------------------------------------------------------------

-- Acceptance criterion 1: an insulin pen on the sheet brings up the pen checklist with no manual
-- selection. The mapping has to be data and not a `strings.Contains(label, "pen")`, for a reason
-- that has already bitten this codebase once (invariant 131's note): a match on a product name
-- silently matches nothing the day a manufacturer renames a brand, and a checklist that silently
-- fails to appear looks exactly like a patient who is not on a device.
--
-- So a rule matches on what the formulary *means* rather than on what it is called: the molecule,
-- the medication class, the dispensing unit and the form. A NULL is "any" — the rules below are
-- deliberately several columns wide, because insulin in a pen and insulin in a vial are the same
-- class and different devices, and the dispensing unit is the column that knows.
--
-- # Why there is a molecule column as well as a class column (spec §6.4)
--
-- The first draft of this table had only the class, and the GLP-1 rule was written against
-- `GLP_1_RECEPTOR_AGONIST`. That class holds semaglutide and dulaglutide, which are **weekly**,
-- and liraglutide, which is **once daily**. A class-level rule would have handed a Victoza
-- patient a checklist asking them to name the day of the week they inject.
--
-- That is not a cosmetic mismatch. §6.4: the commonest dosing error in this class is a patient
-- moving between a daily and a weekly agent and carrying the old rhythm across — so a checklist
-- that asks the wrong question about timing does not merely fail to teach, it *teaches the wrong
-- rhythm*, in the officer's voice, with the patient's own pen in their hand.
--
-- The dosing interval is a property of the molecule and the class cannot carry it. So the
-- timing rule is keyed on `core.generic`, and a molecule-level rule outranks a class-level one
-- for the same product — the precedence is in the query (internal/education/queries), because it
-- is a property of *matching* rather than of any one row.
CREATE TABLE core.education_device_rule (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  checklist_code text NOT NULL REFERENCES core.education_checklist(code),

  -- NULL means "any". At least one must be set: a rule matching everything would put every
  -- checklist in front of every patient, which is the failure mode that looks like working
  -- software.
  --
  -- `match_generic_id` is the molecule, and it is the most specific of the four. It exists for
  -- the case above: two agents in one class that need two different checklists.
  match_generic_id    uuid REFERENCES core.generic(id),
  match_class_code    text REFERENCES core.medication_class(code),
  match_dispense_unit text REFERENCES core.dispense_unit(code),
  match_form_code     text REFERENCES core.medication_form(code),

  -- Why this rule exists, for the person reading it in two years. Not decoration: a mapping
  -- table with no prose is a table nobody dares change.
  note text NOT NULL DEFAULT '',

  retired_at timestamptz,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT education_device_rule_matches_something CHECK (
    match_generic_id IS NOT NULL
    OR match_class_code IS NOT NULL
    OR match_dispense_unit IS NOT NULL
    OR match_form_code IS NOT NULL)
);

CREATE INDEX education_device_rule_by_class
  ON core.education_device_rule (match_class_code) WHERE retired_at IS NULL;
CREATE INDEX education_device_rule_by_generic
  ON core.education_device_rule (match_generic_id) WHERE retired_at IS NULL;

SELECT core.attach_updated_at('core.education_device_rule');

-- ---------------------------------------------------------------------------
-- 8. When technique is poor enough to say so
-- ---------------------------------------------------------------------------

-- §5: any "Unable", or three or more "Corrected today", raises a re-education flag. *"That
-- threshold is a judgement and is a configurable number, not a constant."*
--
-- One row, keyed, rather than a settings blob: a threshold nobody can find is a threshold nobody
-- revises, and a blob makes the CHECK below impossible.
CREATE TABLE core.education_reeducation_policy (
  code text PRIMARY KEY,

  -- Whether a single "Unable" is enough on its own. True per §5, and a column rather than an
  -- assumption because the clinic may one day decide that being unable to state a storage rule
  -- is not the same as being unable to inject.
  unable_raises_flag boolean NOT NULL DEFAULT true,

  -- Three, per §5. At least one, because a threshold of zero would raise the flag for every
  -- patient who was corrected about anything, which is every patient.
  corrected_today_threshold int NOT NULL,

  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT education_reeducation_threshold_is_meaningful
    CHECK (corrected_today_threshold >= 1)
);

SELECT core.attach_updated_at('core.education_reeducation_policy');

INSERT INTO core.education_reeducation_policy (code, unable_raises_flag, corrected_today_threshold)
VALUES ('DEFAULT', true, 3)
ON CONFLICT (code) DO UPDATE SET
  unable_raises_flag = EXCLUDED.unable_raises_flag,
  corrected_today_threshold = EXCLUDED.corrected_today_threshold;

-- ---------------------------------------------------------------------------
-- 9. Why a dose was missed
-- ---------------------------------------------------------------------------

-- §7: *"the fix for each is different and the distinction is invisible in a single adherence
-- percentage."* Cost is a formulary conversation, forgetting is a reminder, side effects are a
-- prescribing decision, and "felt well enough to stop" is the one that kills people.
CREATE TABLE core.medication_miss_reason (
  code text PRIMARY KEY,
  display_en text NOT NULL,
  display_bn text NOT NULL,
  ordering int NOT NULL,
  retired_at timestamptz,

  CONSTRAINT medication_miss_reason_code_format CHECK (code ~ '^[a-z][a-z0-9_]{0,39}$'),
  CONSTRAINT medication_miss_reason_bilingual
    CHECK (btrim(display_en) <> '' AND btrim(display_bn) <> '')
);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'education_device_type',
   'A blood glucose meter is a blood glucose meter in every clinic (CP92).'),
  ('core', 'education_checklist',
   'Physician-authored clinical content, not a clinic setting. Two facilities teaching different insulin technique is the failure this station exists to prevent (CP92).'),
  ('core', 'education_checklist_item',
   'Belongs to its checklist (CP92).'),
  ('core', 'education_device_rule',
   'Which products are pens is a property of the formulary, which is itself the same everywhere above the product row (CP92).'),
  ('core', 'education_reeducation_policy',
   'The threshold at which technique is called poor is a clinical judgement of the service, not of a building (CP92 spec §5).'),
  ('core', 'medication_miss_reason',
   'The reasons people miss doses are the same in Faridpur as anywhere; the proportions are the research question (CP92 spec §7).')
ON CONFLICT (schema_name, table_name) DO NOTHING;

GRANT SELECT ON core.education_device_type TO dthcms_app;
GRANT SELECT ON core.education_checklist TO dthcms_app;
GRANT SELECT ON core.education_checklist_item TO dthcms_app;
GRANT SELECT ON core.education_device_rule TO dthcms_app;
GRANT SELECT ON core.education_reeducation_policy TO dthcms_app;
GRANT SELECT ON core.medication_miss_reason TO dthcms_app;
GRANT SELECT ON core.education_device_type TO dthcms_projector;
GRANT SELECT ON core.education_checklist TO dthcms_projector;
GRANT SELECT ON core.education_checklist_item TO dthcms_projector;
GRANT SELECT ON core.education_device_rule TO dthcms_projector;
GRANT SELECT ON core.education_reeducation_policy TO dthcms_projector;
GRANT SELECT ON core.medication_miss_reason TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- 10. The content
-- ---------------------------------------------------------------------------

INSERT INTO core.education_device_type (code, name_en, name_bn, ordering) VALUES
  ('INSULIN_PEN',        'Insulin pen',              'ইনসুলিন পেন',                 10),
  ('INSULIN_VIAL',       'Insulin vial and syringe', 'ইনসুলিন ভায়াল ও সিরিঞ্জ',      20),
  ('GLUCOSE_METER',      'Blood glucose meter',      'রক্তের গ্লুকোজ মাপার মিটার',   30),
  ('GLP1_WEEKLY_PEN',    'Weekly GLP-1 pen',         'সাপ্তাহিক জিএলপি-১ পেন',      40),
  -- Two GLP-1 device types and not one, because the dosing rhythm is what the checklist
  -- teaches and the two rhythms are taught differently (spec §6.4).
  ('GLP1_DAILY_PEN',     'Daily GLP-1 pen',          'দৈনিক জিএলপি-১ পেন',          50)
ON CONFLICT (code) DO UPDATE SET
  name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn, ordering = EXCLUDED.ordering;

INSERT INTO core.education_checklist (code, device_type, title_en, title_bn) VALUES
  ('PEN_TECHNIQUE',   'INSULIN_PEN',     'Insulin pen technique',
   'ইনসুলিন পেন ব্যবহারের কৌশল'),
  ('VIAL_TECHNIQUE',  'INSULIN_VIAL',    'Insulin vial and syringe technique',
   'ইনসুলিন ভায়াল ও সিরিঞ্জ ব্যবহারের কৌশল'),
  ('METER_TECHNIQUE', 'GLUCOSE_METER',   'Blood glucose meter technique',
   'রক্তের গ্লুকোজ মাপার কৌশল'),
  ('GLP1_TECHNIQUE',  'GLP1_WEEKLY_PEN', 'Weekly GLP-1 pen technique',
   'সাপ্তাহিক জিএলপি-১ পেন ব্যবহারের কৌশল'),
  ('GLP1_DAILY_TECHNIQUE', 'GLP1_DAILY_PEN', 'Daily GLP-1 pen technique',
   'দৈনিক জিএলপি-১ পেন ব্যবহারের কৌশল')
ON CONFLICT (code) DO UPDATE SET
  device_type = EXCLUDED.device_type, title_en = EXCLUDED.title_en, title_bn = EXCLUDED.title_bn;

-- The observation codes, one per item, category EXAM.
--
-- EXAM and not PRO: what this records is what somebody watched the patient do, which is a
-- clinical observation with a witness. A patient saying they inject correctly would be PRO and
-- would be a different — and, per §5, much weaker — fact.
--
-- `coded`, so the three states live in `core.observation_answer` and the database refuses a
-- fourth. A boolean column would have thrown away "Corrected today", which §5 says is the most
-- clinically useful of the three.
INSERT INTO core.observation_code
  (code, category, value_type, dimension, loinc, display_en, display_bn,
   min_canonical, max_canonical, write_permission)
SELECT item.code, 'EXAM', 'coded', NULL, '', item.display_en, item.display_bn,
       NULL, NULL, 'education.record'
  FROM (VALUES
    ('EDU_PEN_01',  'Pen: expiry and appearance',        'পেন: মেয়াদ ও চেহারা'),
    ('EDU_PEN_02',  'Pen: resuspends cloudy insulin',    'পেন: ঘোলা ইনসুলিন মেশানো'),
    ('EDU_PEN_03',  'Pen: new needle',                   'পেন: নতুন সুচ'),
    ('EDU_PEN_04',  'Pen: air-shot',                     'পেন: এয়ার-শট'),
    ('EDU_PEN_05',  'Pen: dials and states the dose',    'পেন: ডোজ ঘোরানো ও বলা'),
    ('EDU_PEN_06',  'Pen: site choice and rotation',     'পেন: জায়গা বাছাই ও বদল'),
    ('EDU_PEN_07',  'Pen: insertion angle and pinch',    'পেন: সুচের কোণ ও চিমটি'),
    ('EDU_PEN_08',  'Pen: holds and counts to ten',      'পেন: দশ পর্যন্ত গোনা'),
    ('EDU_PEN_09',  'Pen: needle disposal',              'পেন: সুচ ফেলা'),
    ('EDU_PEN_10',  'Pen: storage',                      'পেন: সংরক্ষণ'),
    ('EDU_VIAL_01', 'Vial: expiry and appearance',       'ভায়াল: মেয়াদ ও চেহারা'),
    ('EDU_VIAL_02', 'Vial: resuspends cloudy insulin',   'ভায়াল: ঘোলা ইনসুলিন মেশানো'),
    ('EDU_VIAL_03', 'Vial: wipes the top',               'ভায়াল: মুখ মোছা'),
    ('EDU_VIAL_04', 'Vial: injects air first',           'ভায়াল: আগে বাতাস ঢোকানো'),
    ('EDU_VIAL_05', 'Vial: draws and clears bubbles',    'ভায়াল: ডোজ টানা ও বুদবুদ সরানো'),
    ('EDU_VIAL_06', 'Vial: clear before cloudy',         'ভায়াল: পরিষ্কার আগে, ঘোলা পরে'),
    ('EDU_VIAL_07', 'Vial: site choice and rotation',    'ভায়াল: জায়গা বাছাই ও বদল'),
    ('EDU_VIAL_08', 'Vial: insertion and hold',          'ভায়াল: সুচ ঢোকানো ও ধরে রাখা'),
    ('EDU_VIAL_09', 'Vial: syringe disposal',            'ভায়াল: সিরিঞ্জ ফেলা'),
    ('EDU_VIAL_10', 'Vial: storage',                     'ভায়াল: সংরক্ষণ'),
    ('EDU_MTR_01',  'Meter: washes and dries hands',     'মিটার: হাত ধোয়া ও মোছা'),
    ('EDU_MTR_02',  'Meter: strip date and match',       'মিটার: স্ট্রিপের মেয়াদ ও মিল'),
    ('EDU_MTR_03',  'Meter: lances the side',            'মিটার: আঙুলের পাশে ফোটানো'),
    ('EDU_MTR_04',  'Meter: adequate drop',              'মিটার: যথেষ্ট রক্তের ফোঁটা'),
    ('EDU_MTR_05',  'Meter: applies and waits',          'মিটার: ফোঁটা লাগানো ও অপেক্ষা'),
    ('EDU_MTR_06',  'Meter: records the reading',        'মিটার: ফল লিখে রাখা'),
    ('EDU_MTR_07',  'Meter: states the hypo number',     'মিটার: হাইপোর মাত্রা বলা'),
    ('EDU_MTR_08',  'Meter: states when to test',        'মিটার: কখন পরীক্ষা করবেন'),
    ('EDU_MTR_09',  'Meter: lancet disposal',            'মিটার: ল্যান্সেট ফেলা'),
    ('EDU_GLP_01',  'GLP-1: expiry and appearance',      'জিএলপি-১: মেয়াদ ও চেহারা'),
    ('EDU_GLP_02',  'GLP-1: prepares the device',        'জিএলপি-১: পেন প্রস্তুত করা'),
    ('EDU_GLP_03',  'GLP-1: states the weekly day',      'জিএলপি-১: সপ্তাহের দিন বলা'),
    ('EDU_GLP_04',  'GLP-1: site choice and rotation',   'জিএলপি-১: জায়গা বাছাই ও বদল'),
    ('EDU_GLP_05',  'GLP-1: holds before withdrawing',   'জিএলপি-১: বের করার আগে ধরে রাখা'),
    ('EDU_GLP_06',  'GLP-1: expects and reports nausea', 'জিএলপি-১: বমি ভাব ও জানানো'),
    ('EDU_GLP_07',  'GLP-1: missed dose',                'জিএলপি-১: ডোজ বাদ পড়লে'),
    ('EDU_GLP_08',  'GLP-1: storage',                    'জিএলপি-১: সংরক্ষণ'),
    ('EDU_GLP_09',  'GLP-1: needle disposal',            'জিএলপি-১: সুচ ফেলা'),
    -- The daily GLP-1 checklist (spec §6.4). Its own nine codes rather than a second checklist
    -- pointing at the weekly ones, and the reason is not the UNIQUE constraint on
    -- `education_checklist_item.observation_code` — it is that these are not the same findings.
    -- Item 3 asks a daily patient for a *time of day* and a weekly patient for a *day of the
    -- week*; one code holding both answers would be a column whose meaning depends on which pen
    -- the patient happened to be on, and §12 could not tell the two cohorts apart. The seven
    -- shared items get their own codes for the same reason one step weaker: a patient moved from
    -- Victoza to Ozempic has been taught twice, on two instruments, and the record should say so.
    ('EDU_GLPD_01', 'GLP-1 daily: expiry and appearance',      'দৈনিক জিএলপি-১: মেয়াদ ও চেহারা'),
    ('EDU_GLPD_02', 'GLP-1 daily: prepares the device',        'দৈনিক জিএলপি-১: পেন প্রস্তুত করা'),
    ('EDU_GLPD_03', 'GLP-1 daily: states the time of day',     'দৈনিক জিএলপি-১: দিনের সময় বলা'),
    ('EDU_GLPD_04', 'GLP-1 daily: site choice and rotation',   'দৈনিক জিএলপি-১: জায়গা বাছাই ও বদল'),
    ('EDU_GLPD_05', 'GLP-1 daily: holds before withdrawing',   'দৈনিক জিএলপি-১: বের করার আগে ধরে রাখা'),
    ('EDU_GLPD_06', 'GLP-1 daily: expects and reports nausea', 'দৈনিক জিএলপি-১: বমি ভাব ও জানানো'),
    ('EDU_GLPD_07', 'GLP-1 daily: missed dose',                'দৈনিক জিএলপি-১: ডোজ বাদ পড়লে'),
    ('EDU_GLPD_08', 'GLP-1 daily: storage',                    'দৈনিক জিএলপি-১: সংরক্ষণ'),
    ('EDU_GLPD_09', 'GLP-1 daily: needle disposal',            'দৈনিক জিএলপি-১: সুচ ফেলা')
  ) AS item(code, display_en, display_bn)
ON CONFLICT (code) DO UPDATE SET
  category = EXCLUDED.category, value_type = EXCLUDED.value_type,
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  write_permission = EXCLUDED.write_permission;

-- The flag. A boolean, not a code: it says yes or no, and it is computed by the server from the
-- item states and the threshold rather than chosen by the officer. EXAM, because it is a
-- statement about what somebody watched, and it is the one value this station produces that the
-- physician reads at the *next* visit rather than this one.
INSERT INTO core.observation_code
  (code, category, value_type, dimension, loinc, display_en, display_bn,
   min_canonical, max_canonical, write_permission) VALUES
  ('EDU_REEDUCATION_FLAG', 'EXAM', 'boolean', NULL, '',
   'Re-education needed', 'আবার শেখানো দরকার', NULL, NULL, 'education.record')
ON CONFLICT (code) DO UPDATE SET
  category = EXCLUDED.category, value_type = EXCLUDED.value_type,
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  write_permission = EXCLUDED.write_permission;

-- Why a dose was missed: PRO, because it is what the patient said rather than what anybody
-- watched, and `observation.write.pro` for the same reason the score carries it. §7's preamble
-- makes the true number sayable; it does not make the answer an observation.
INSERT INTO core.observation_code
  (code, category, value_type, dimension, loinc, display_en, display_bn,
   min_canonical, max_canonical, write_permission) VALUES
  ('MEDICATION_MISS_REASON', 'PRO', 'coded', NULL, '',
   'Reason a dose was missed', 'ডোজ বাদ পড়ার কারণ', NULL, NULL, 'observation.write.pro')
ON CONFLICT (code) DO UPDATE SET
  category = EXCLUDED.category, value_type = EXCLUDED.value_type,
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  write_permission = EXCLUDED.write_permission;

-- How many doses were missed in the last week. Dimension `ratio` — the dimensionless dimension,
-- as `IMPROVEMENT_SCORE` and `PACK_YEARS` already use — so that the plausibility band is enforced
-- by the same database trigger as every other number. A unitless code skips that check entirely,
-- and "how many times did you miss" is exactly the field where a mis-keyed 77 would otherwise sit
-- in the record forever.
INSERT INTO core.observation_code
  (code, category, value_type, dimension, loinc, display_en, display_bn,
   min_canonical, max_canonical, write_permission) VALUES
  ('MEDICATION_MISSED_DOSES_7D', 'PRO', 'numeric', 'ratio', '',
   'Doses missed in the last week', 'গত এক সপ্তাহে বাদ পড়া ডোজ',
   0, 50, 'observation.write.pro')
ON CONFLICT (code) DO UPDATE SET
  category = EXCLUDED.category, value_type = EXCLUDED.value_type,
  dimension = EXCLUDED.dimension,
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  min_canonical = EXCLUDED.min_canonical, max_canonical = EXCLUDED.max_canonical,
  write_permission = EXCLUDED.write_permission;

-- The three states, for every item code. §5's table, and there is no fourth.
--
-- `is_normal` marks "Demonstrated" so a report can count what went right without a list of magic
-- strings — and "Corrected today" is deliberately *not* normal, because §5's whole argument is
-- that a patient who was corrected today is a different patient from one who never needed it.
--
-- **The Bangla is a label, not a definition.** The first draft rendered each state as the whole
-- explanatory sentence — *"দেখিয়ে দেওয়ার পরেও ঠিকভাবে করতে পারেননি"* — which is what the state
-- means and is four times the width of the English. On the officer's screen that pushed the
-- three buttons across the row and squeezed the checklist item itself into a column six words
-- wide. A clinician reads a button, not a paragraph; §5's table is the definition and this is the
-- word for it.
INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal)
SELECT c.code, state.value_code, state.display_en, state.display_bn, state.ordering, state.is_normal
  FROM core.observation_code c
 CROSS JOIN (VALUES
    ('demonstrated',    'Demonstrated',    'নিজেই পেরেছেন',        10, true),
    ('corrected_today', 'Corrected today', 'আজ শুধরে দেওয়া হয়েছে', 20, false),
    ('unable',          'Unable',          'পারেননি',              30, false)
  ) AS state(value_code, display_en, display_bn, ordering, is_normal)
 WHERE c.code ~ '^EDU_(PEN|VIAL|MTR|GLP|GLPD)_[0-9]{2}$'
ON CONFLICT (code, value_code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  ordering = EXCLUDED.ordering, is_normal = EXCLUDED.is_normal;

INSERT INTO core.medication_miss_reason (code, display_en, display_bn, ordering) VALUES
  ('cost',            'The medicine cost too much',        'ওষুধের দাম বেশি পড়ে যাচ্ছিল',            10),
  ('forgot',          'Forgot',                            'মনে ছিল না',                            20),
  ('side_effects',    'Side effects',                      'ওষুধ খেলে শরীর খারাপ লাগছিল',            30),
  ('ran_out',         'Ran out of the medicine',           'ওষুধ শেষ হয়ে গিয়েছিল',                  40),
  ('felt_well_enough','Felt well enough to stop',          'ভালো লাগছিল, তাই খাওয়া বন্ধ রেখেছিলাম',  50),
  ('could_not_come',  'Could not get to the clinic',       'ক্লিনিকে আসতে পারিনি',                   60)
ON CONFLICT (code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn, ordering = EXCLUDED.ordering;

INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal)
SELECT 'MEDICATION_MISS_REASON', r.code, r.display_en, r.display_bn, r.ordering, false
  FROM core.medication_miss_reason r
ON CONFLICT (code, value_code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn, ordering = EXCLUDED.ordering;

-- The checklists themselves, verbatim from spec §6.
INSERT INTO core.education_checklist_item
  (checklist_code, ordinal, observation_code, text_en, text_bn, is_critical) VALUES
  -- §6.1 Insulin pen. Items 2, 4 and 8 are the critical three.
  ('PEN_TECHNIQUE',  1, 'EDU_PEN_01',
   'Checks the expiry date and that the insulin looks as it should',
   'মেয়াদ শেষের তারিখ দেখে নেন এবং ইনসুলিন দেখতে ঠিক আছে কি না মিলিয়ে নেন', false),
  ('PEN_TECHNIQUE',  2, 'EDU_PEN_02',
   'Rolls a cloudy insulin (NPH or premix) gently between the palms until evenly milky - does not shake it',
   'ঘোলা ইনসুলিন (এনপিএইচ বা প্রিমিক্স) দুই হাতের তালুর মাঝে আস্তে আস্তে গড়িয়ে সমানভাবে দুধের মতো করে নেন — ঝাঁকান না', true),
  ('PEN_TECHNIQUE',  3, 'EDU_PEN_03',
   'Attaches a new needle for this injection',
   'এই ইনজেকশনের জন্য নতুন সুচ লাগান', false),
  ('PEN_TECHNIQUE',  4, 'EDU_PEN_04',
   'Air-shot: dials 2 units, holds the pen upright, presses until a drop appears at the tip',
   'এয়ার-শট: ২ ইউনিট ঘুরিয়ে নিয়ে পেন সোজা উপরের দিকে ধরে চাপ দেন, যতক্ষণ না সুচের মাথায় এক ফোঁটা ওষুধ দেখা যায়', true),
  ('PEN_TECHNIQUE',  5, 'EDU_PEN_05',
   'Dials the prescribed dose and can state what that dose is',
   'নির্ধারিত ডোজ ঘুরিয়ে নেন এবং ডোজটি কত তা বলতে পারেন', false),
  ('PEN_TECHNIQUE',  6, 'EDU_PEN_06',
   'Chooses a site and can name at least two sites they rotate between',
   'ইনজেকশনের জায়গা বেছে নেন এবং অন্তত দুটি জায়গার নাম বলতে পারেন যেগুলো ঘুরিয়ে ফিরিয়ে ব্যবহার করেন', false),
  ('PEN_TECHNIQUE',  7, 'EDU_PEN_07',
   'Inserts at 90 degrees, skin pinched only if they are thin or using a longer needle',
   '৯০ ডিগ্রি কোণে সুচ ঢোকান; শুকনো গড়ন হলে বা লম্বা সুচ হলে তবেই চামড়া চিমটি দিয়ে তোলেন', false),
  ('PEN_TECHNIQUE',  8, 'EDU_PEN_08',
   'Holds the button down and counts to ten before withdrawing',
   'বোতাম চেপে ধরে রেখে দশ পর্যন্ত গোনেন, তারপর সুচ বের করেন', true),
  ('PEN_TECHNIQUE',  9, 'EDU_PEN_09',
   'Removes the needle and disposes of it safely - not loose in household waste',
   'সুচ খুলে নিরাপদে ফেলেন — ঘরের সাধারণ ময়লার সঙ্গে খোলা অবস্থায় নয়', false),
  ('PEN_TECHNIQUE', 10, 'EDU_PEN_10',
   'States how the pen in use and the spare are stored: in use at room temperature, spare in the fridge, never the freezer',
   'চলতি পেন ও বাড়তি পেন কীভাবে রাখতে হয় তা বলতে পারেন: চলতি পেন ঘরের তাপমাত্রায়, বাড়তি পেন ফ্রিজে — কখনোই ডিপ ফ্রিজে নয়', false),

  -- §6.2 Vial and syringe.
  ('VIAL_TECHNIQUE',  1, 'EDU_VIAL_01',
   'Checks expiry and appearance',
   'মেয়াদ ও ইনসুলিনের চেহারা দেখে নেন', false),
  ('VIAL_TECHNIQUE',  2, 'EDU_VIAL_02',
   'Rolls a cloudy insulin - does not shake',
   'ঘোলা ইনসুলিন তালুর মাঝে গড়িয়ে নেন — ঝাঁকান না', true),
  ('VIAL_TECHNIQUE',  3, 'EDU_VIAL_03',
   'Wipes the vial top and lets it dry',
   'ভায়ালের মুখ মুছে নিয়ে শুকাতে দেন', false),
  ('VIAL_TECHNIQUE',  4, 'EDU_VIAL_04',
   'Draws air equal to the dose and injects it into the vial before drawing',
   'ডোজের সমান পরিমাণ বাতাস সিরিঞ্জে নিয়ে ভায়ালের ভেতরে ঢোকান, তারপর ওষুধ টানেন', false),
  ('VIAL_TECHNIQUE',  5, 'EDU_VIAL_05',
   'Draws the dose and expels air bubbles, then re-checks the amount',
   'ডোজ টেনে নিয়ে বাতাসের বুদবুদ বের করে দেন, তারপর পরিমাণ আবার মিলিয়ে নেন', true),
  ('VIAL_TECHNIQUE',  6, 'EDU_VIAL_06',
   'If mixing two insulins: draws the clear before the cloudy, and can say why the order matters',
   'দুই ধরনের ইনসুলিন মেশালে: পরিষ্কারটা আগে, ঘোলাটা পরে টানেন এবং এই ক্রম কেন জরুরি তা বলতে পারেন', true),
  ('VIAL_TECHNIQUE',  7, 'EDU_VIAL_07',
   'Chooses and rotates the site',
   'ইনজেকশনের জায়গা বেছে নেন এবং ঘুরিয়ে ফিরিয়ে ব্যবহার করেন', false),
  ('VIAL_TECHNIQUE',  8, 'EDU_VIAL_08',
   'Inserts at 90 degrees and holds before withdrawing',
   '৯০ ডিগ্রি কোণে সুচ ঢোকান এবং বের করার আগে কিছুক্ষণ ধরে রাখেন', false),
  ('VIAL_TECHNIQUE',  9, 'EDU_VIAL_09',
   'Disposes of the syringe safely, in a puncture-proof container',
   'সিরিঞ্জ শক্ত, ছিদ্র না হওয়া কৌটায় নিরাপদে ফেলেন', false),
  ('VIAL_TECHNIQUE', 10, 'EDU_VIAL_10',
   'States correct storage',
   'সঠিকভাবে সংরক্ষণের নিয়ম বলতে পারেন', false),

  -- §6.3 Blood glucose meter.
  ('METER_TECHNIQUE', 1, 'EDU_MTR_01',
   'Washes and dries hands - water and soap, not alcohol alone',
   'সাবান-পানি দিয়ে হাত ধুয়ে ভালো করে মুছে নেন — শুধু স্পিরিট দিয়ে নয়', true),
  ('METER_TECHNIQUE', 2, 'EDU_MTR_02',
   'Checks the strips are in date and match the meter',
   'স্ট্রিপের মেয়াদ আছে কি না এবং স্ট্রিপ মিটারের সঙ্গে মেলে কি না দেখে নেন', false),
  ('METER_TECHNIQUE', 3, 'EDU_MTR_03',
   'Lances the side of the fingertip, not the pad, and rotates fingers',
   'আঙুলের ডগার পাশে ফোটান, ঠিক মাঝখানে নয়, এবং আঙুল বদলে বদলে ব্যবহার করেন', true),
  ('METER_TECHNIQUE', 4, 'EDU_MTR_04',
   'Gets an adequate drop without squeezing or milking the finger',
   'আঙুল না চেপে, না দুইয়ে যথেষ্ট বড় এক ফোঁটা রক্ত নেন', false),
  ('METER_TECHNIQUE', 5, 'EDU_MTR_05',
   'Applies the drop correctly and waits for the result',
   'রক্তের ফোঁটা ঠিকভাবে স্ট্রিপে লাগান এবং ফলের জন্য অপেক্ষা করেন', false),
  ('METER_TECHNIQUE', 6, 'EDU_MTR_06',
   'Records the reading with the time and whether it was before or after food',
   'ফলাফল সময়সহ লিখে রাখেন, এবং খাওয়ার আগে না পরে তাও লিখে রাখেন', false),
  ('METER_TECHNIQUE', 7, 'EDU_MTR_07',
   'States their own hypo number and what they will do about it',
   'নিজের হাইপোর মাত্রা কত তা বলতে পারেন এবং তখন কী করবেন তা বলতে পারেন', true),
  ('METER_TECHNIQUE', 8, 'EDU_MTR_08',
   'States when they have been asked to test',
   'কখন কখন পরীক্ষা করতে বলা হয়েছে তা বলতে পারেন', false),
  ('METER_TECHNIQUE', 9, 'EDU_MTR_09',
   'Disposes of the lancet safely',
   'ল্যান্সেট নিরাপদে ফেলেন', false),

  -- §6.4 Weekly GLP-1 receptor agonist pen.
  ('GLP1_TECHNIQUE', 1, 'EDU_GLP_01',
   'Checks expiry and that the solution is clear and colourless',
   'মেয়াদ দেখে নেন এবং ওষুধ স্বচ্ছ ও বর্ণহীন আছে কি না মিলিয়ে নেন', false),
  ('GLP1_TECHNIQUE', 2, 'EDU_GLP_02',
   'Prepares the device and attaches a needle where the device needs one',
   'পেন প্রস্তুত করেন এবং যে পেনে সুচ লাগাতে হয় সেখানে সুচ লাগান', false),
  ('GLP1_TECHNIQUE', 3, 'EDU_GLP_03',
   'States the day of the week they will take it, and that it is the same day each week',
   'সপ্তাহের কোন দিন নেবেন তা বলতে পারেন, এবং প্রতি সপ্তাহে সেই একই দিনেই নিতে হবে তা জানেন', true),
  ('GLP1_TECHNIQUE', 4, 'EDU_GLP_04',
   'Chooses and rotates the site',
   'ইনজেকশনের জায়গা বেছে নেন এবং ঘুরিয়ে ফিরিয়ে ব্যবহার করেন', false),
  ('GLP1_TECHNIQUE', 5, 'EDU_GLP_05',
   'Holds for the count the device requires before withdrawing',
   'পেনের নিয়ম অনুযায়ী যতক্ষণ ধরে রাখতে হয় ততক্ষণ ধরে রেখে তারপর সুচ বের করেন', false),
  ('GLP1_TECHNIQUE', 6, 'EDU_GLP_06',
   'States that nausea in the first weeks is expected and usually settles, and that they should not stop without telling us',
   'প্রথম কয়েক সপ্তাহে বমি বমি ভাব হতে পারে এবং তা সাধারণত কমে যায় — এ কথা বলতে পারেন, এবং আমাদের না জানিয়ে ওষুধ বন্ধ করবেন না তা জানেন', true),
  -- §6.4's table gives the weekly missed-dose rule in full, and it is a different instruction
  -- from the daily one rather than a longer phrasing of it: five days is a window, and a weekly
  -- patient who is inside it keeps their usual day.
  ('GLP1_TECHNIQUE', 7, 'EDU_GLP_07',
   'If remembered within 5 days, takes it and keeps the usual day; if longer, skips it and resumes on the usual day. Never two doses to catch up',
   'পাঁচ দিনের মধ্যে মনে পড়লে সেটি নিয়ে নেন এবং নিয়মিত দিনটিই রাখেন; আরও দেরি হলে ওই ডোজটি বাদ দিয়ে নিয়মিত দিনেই ফিরে যান। পুষিয়ে নিতে কখনোই দুই ডোজ নয়', true),
  ('GLP1_TECHNIQUE', 8, 'EDU_GLP_08',
   'States correct storage',
   'সঠিকভাবে সংরক্ষণের নিয়ম বলতে পারেন', false),
  ('GLP1_TECHNIQUE', 9, 'EDU_GLP_09',
   'Disposes of the needle safely',
   'সুচ নিরাপদে ফেলেন', false),

  -- §6.4 daily — liraglutide. Items 1, 2, 4, 5, 6, 8 and 9 are the weekly list word for word;
  -- items 3 and 7 are the two the spec's table replaces, and they are the reason this checklist
  -- exists at all.
  ('GLP1_DAILY_TECHNIQUE', 1, 'EDU_GLPD_01',
   'Checks expiry and that the solution is clear and colourless',
   'মেয়াদ দেখে নেন এবং ওষুধ স্বচ্ছ ও বর্ণহীন আছে কি না মিলিয়ে নেন', false),
  ('GLP1_DAILY_TECHNIQUE', 2, 'EDU_GLPD_02',
   'Prepares the device and attaches a needle where the device needs one',
   'পেন প্রস্তুত করেন এবং যে পেনে সুচ লাগাতে হয় সেখানে সুচ লাগান', false),
  ('GLP1_DAILY_TECHNIQUE', 3, 'EDU_GLPD_03',
   'States the time of day they will take it, and that it is about the same time each day',
   'দিনের কোন সময়ে নেবেন তা বলতে পারেন, এবং প্রতিদিন মোটামুটি সেই একই সময়েই নিতে হবে তা জানেন', true),
  ('GLP1_DAILY_TECHNIQUE', 4, 'EDU_GLPD_04',
   'Chooses and rotates the site',
   'ইনজেকশনের জায়গা বেছে নেন এবং ঘুরিয়ে ফিরিয়ে ব্যবহার করেন', false),
  ('GLP1_DAILY_TECHNIQUE', 5, 'EDU_GLPD_05',
   'Holds for the count the device requires before withdrawing',
   'পেনের নিয়ম অনুযায়ী যতক্ষণ ধরে রাখতে হয় ততক্ষণ ধরে রেখে তারপর সুচ বের করেন', false),
  ('GLP1_DAILY_TECHNIQUE', 6, 'EDU_GLPD_06',
   'States that nausea in the first weeks is expected and usually settles, and that they should not stop without telling us',
   'প্রথম কয়েক সপ্তাহে বমি বমি ভাব হতে পারে এবং তা সাধারণত কমে যায় — এ কথা বলতে পারেন, এবং আমাদের না জানিয়ে ওষুধ বন্ধ করবেন না তা জানেন', true),
  ('GLP1_DAILY_TECHNIQUE', 7, 'EDU_GLPD_07',
   'Takes the next dose at the usual time. Never two doses in one day, and no double dose to catch up',
   'পরের ডোজটি নিয়মিত সময়েই নেন। একদিনে কখনোই দুই ডোজ নয়, আর বাদ পড়া ডোজ পুষিয়ে নিতে দ্বিগুণ ডোজ নয়', true),
  ('GLP1_DAILY_TECHNIQUE', 8, 'EDU_GLPD_08',
   'States correct storage',
   'সঠিকভাবে সংরক্ষণের নিয়ম বলতে পারেন', false),
  ('GLP1_DAILY_TECHNIQUE', 9, 'EDU_GLPD_09',
   'Disposes of the needle safely',
   'সুচ নিরাপদে ফেলেন', false)
ON CONFLICT (checklist_code, ordinal) DO UPDATE SET
  observation_code = EXCLUDED.observation_code,
  text_en = EXCLUDED.text_en, text_bn = EXCLUDED.text_bn,
  is_critical = EXCLUDED.is_critical;

-- The mapping rules.
--
-- Insulin in a pen, a cartridge or a pack of pens is the pen checklist; insulin in a vial or an
-- ampoule is the vial-and-syringe checklist. The class tells you it is insulin and the
-- dispensing unit tells you what it comes in, which is why neither column alone would do.
--
-- `pack` is in the pen list because that is how the formulary carries a five-pack of FlexPens
-- (00056: 'Actrapid FlexPen', dispense_unit 'pack'). It is the one rule here whose reason is a
-- quirk of the price list rather than a clinical fact, and it is written down rather than
-- absorbed so that a formulary tidy-up which splits packs out knows to come here.
INSERT INTO core.education_device_rule
  (checklist_code, match_class_code, match_dispense_unit, note)
SELECT rule.checklist, class.code, rule.unit, rule.note
  FROM (VALUES
    ('PEN_TECHNIQUE',  'pen',
     'Insulin supplied in a pre-filled pen: the commonest device in this clinic and the one where technique errors are most consequential (spec 6.1).'),
    ('PEN_TECHNIQUE',  'cartridge',
     'A PenFill cartridge is loaded into a durable pen and is used with pen technique.'),
    ('PEN_TECHNIQUE',  'pack',
     'The formulary prices multi-pen packs by the pack (migration 00056). A pack of insulin is a pack of pens.'),
    ('VIAL_TECHNIQUE', 'vial',
     'Insulin drawn from a vial with a syringe: still common where cost matters, and it has more steps to get wrong (spec 6.2).'),
    ('VIAL_TECHNIQUE', 'ampoule',
     'Drawn with a syringe like a vial.')
  ) AS rule(checklist, unit, note)
 CROSS JOIN core.medication_class class
 WHERE class.code LIKE 'INSULIN%';

-- The meter, and the gap this migration is closing half of.
--
-- Spec §6.3 authors a blood glucose meter checklist, and §6's rule is that the checklist is
-- chosen from the devices on the prescription. **The formulary carries no meter, no test strips
-- and no lancets** — 00056 seeds medicines only — so before this there was no product a
-- prescriber could put on a sheet that would select the meter checklist, and the content would
-- have shipped unreachable.
--
-- What is added here is the *classification*, which is reference data and the same in every
-- clinic: a class for self-monitoring supplies, the form and the dispensing unit they come in.
-- What is deliberately **not** added is any product or price. Which brands a clinic stocks and
-- what they cost is the pharmacist's, facility-scoped, and inventing a price for a meter in a
-- migration would put a number on a printed sheet that nobody chose.
--
-- So: until the pharmacist adds a strip or a meter to the formulary, the meter checklist is
-- reachable in principle and selected in practice by nothing. That is the honest state, it is
-- visible from this table rather than buried, and invariant 133 can tell the difference between
-- it and a checklist with no rule at all.
INSERT INTO core.medication_class (code, name_en, name_bn, ordering) VALUES
  ('SELF_MONITORING_GLUCOSE', 'Self-monitoring of blood glucose',
   'রক্তের গ্লুকোজ নিজে মাপার সরঞ্জাম', 340)
ON CONFLICT (code) DO UPDATE SET
  name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn, ordering = EXCLUDED.ordering;

INSERT INTO core.medication_form (code, name_en, name_bn) VALUES
  ('TEST_STRIP', 'Test strip', 'টেস্ট স্ট্রিপ'),
  ('DEVICE', 'Device', 'যন্ত্র')
ON CONFLICT (code) DO UPDATE SET name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn;

INSERT INTO core.dispense_unit (code, name_en, name_bn) VALUES
  ('strip', 'strip', 'স্ট্রিপ'),
  ('device', 'device', 'যন্ত্র')
ON CONFLICT (code) DO UPDATE SET name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn;

INSERT INTO core.education_device_rule
  (checklist_code, match_class_code, match_dispense_unit, note) VALUES
  ('METER_TECHNIQUE', 'SELF_MONITORING_GLUCOSE', NULL,
   'Any self-monitoring supply on the sheet - a meter, its strips or its lancets - means the patient is testing at home, and spec 6.3 item 7 (their own hypo number) is the single most important item in this station.');

-- The GLP-1 rules, one per molecule, and **no class-level rule behind them** (spec §6.4).
--
-- # Why a molecule and not the class
--
-- Semaglutide and dulaglutide are weekly; liraglutide is once daily. The class cannot say which,
-- so a class-level rule would teach one of the two rhythms to every patient in it — and §6.4 is
-- explicit that the commonest dosing error here is exactly a patient carrying the wrong rhythm
-- across from a previous agent.
--
-- # Why there is no fallback, which is the more interesting half
--
-- A GLP-1 added to this formulary next year matches none of these rules and brings up **no
-- checklist at all**. That is deliberate, and the alternative was considered and rejected:
-- falling back to the weekly checklist would put a specific dosing rhythm in an officer's mouth
-- on the authority of nobody having checked. A checklist is an instruction, and inheriting an
-- instruction from a sibling molecule is how a daily patient gets told to inject on Fridays.
--
-- Silence is safer and it is not silent. `education.Service.Session` reports a prescribed line
-- whose class the station knows about but whose molecule matches no rule as an **unclassified
-- device**, and the officer sees it on the screen with the patient in front of them. An absent
-- checklist and a checklist that could not be chosen are different facts, and the officer is the
-- only person in the building who can resolve the second one that morning.
INSERT INTO core.education_device_rule
  (checklist_code, match_generic_id, note)
SELECT rule.checklist, g.id, rule.note
  FROM (VALUES
    ('GLP1_TECHNIQUE', 'Semaglutide',
     'Once weekly. Spec 6.4 weekly column.'),
    ('GLP1_TECHNIQUE', 'Dulaglutide',
     'Once weekly. Spec 6.4 weekly column.'),
    ('GLP1_DAILY_TECHNIQUE', 'Liraglutide',
     'Once DAILY, and the reason this table has a molecule column at all. A class-level rule would ask a Victoza patient which day of the week they inject (spec 6.4).')
  ) AS rule(checklist, generic_name, note)
  -- A join, which means this seed can match nothing and succeed — invariant 131's defect
  -- exactly. Invariant 133 is the guard: it asserts these three molecules carry a rule, by name,
  -- so a renamed or absent generic fails a deploy rather than producing a station that silently
  -- stopped recognising Ozempic.
  JOIN core.generic g ON lower(g.name) = lower(rule.generic_name);

-- ---------------------------------------------------------------------------
-- 11. Invariant 132: the scale a screen draws is the scale the database will accept
-- ---------------------------------------------------------------------------

-- Three separate ways this can be quietly wrong, and all three look like working software.
--
--   1. **The bands do not cover the scale.** A hole between 7 and 8 is a score the operator
--      cannot select and the write would have accepted. A patient points at a face and nothing
--      happens.
--   2. **The scale's bounds disagree with the code's plausibility band.** A 0-10 scale against a
--      1-10 code is a selector offering a value the database refuses, discovered by an operator
--      in front of a patient. This is the exact failure the symmetric variant in spec §3 would
--      introduce if somebody changed the scale row and forgot the registry row, which is the
--      most likely way this table ever changes.
--   3. **A patient-reported value already in the record was recorded by a role that may not
--      record it.** The trigger above stops new ones; this is what catches a row that predates
--      the trigger, or a permission that was revoked afterwards, and it is the assertion that
--      makes CP88 §1 a property of the data rather than of the code that was running that day.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_patient_reported_scale_is_coherent() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
  scale     core.pro_scale;
  spec      core.observation_code;
  covered   int;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM core.pro_scale WHERE retired_at IS NULL) THEN
    RAISE EXCEPTION 'no live patient-reported scale'
      USING HINT =
        'CP88 is one question asked the same way every time. Without a live scale the education '
        'station has no question to read aloud, and the screen has nothing to draw -- which '
        'looks, from the floor, like the station simply not asking.';
  END IF;

  FOR scale IN SELECT * FROM core.pro_scale WHERE retired_at IS NULL LOOP
    SELECT * INTO spec FROM core.observation_code WHERE code = scale.observation_code;

    IF spec.min_canonical IS DISTINCT FROM scale.min_value::numeric
       OR spec.max_canonical IS DISTINCT FROM scale.max_value::numeric THEN
      RAISE EXCEPTION 'scale % offers %-% but % accepts %-%',
        scale.code, scale.min_value, scale.max_value,
        spec.code, spec.min_canonical, spec.max_canonical
        USING HINT =
          'The selector and the write would disagree, and the disagreement surfaces as a refusal '
          'in front of a patient. Spec 3 says the symmetric 0-10 variant is a change to reference '
          'data AND a scale bound -- both rows, in one change.';
    END IF;

    -- Every value on the scale falls in exactly one band. `count(*) = 1` catches a hole and an
    -- overlap with the same query, which matters because an overlap is the subtler bug: two
    -- bands claiming 5 draws two faces and the operator picks whichever is on the left.
    SELECT count(*) INTO covered
      FROM generate_series(scale.min_value, scale.max_value) AS v
     WHERE (SELECT count(*) FROM core.pro_scale_anchor a
             WHERE a.scale_code = scale.code AND v BETWEEN a.from_value AND a.to_value) <> 1;

    IF covered > 0 THEN
      RAISE EXCEPTION '% value(s) of scale % are covered by no band or by more than one',
        covered, scale.code
        USING HINT =
          'A patient who cannot read points at a face. A value with no face cannot be selected, '
          'and a value with two faces means two different things depending on which the operator '
          'tapped (CP88 spec 3).';
    END IF;
  END LOOP;

  SELECT string_agg(DISTINCT o.code || ' by ' || o.recorded_role, ', ') INTO offending
    FROM read.observation o
    JOIN core.observation_code c ON c.code = o.code
   WHERE c.category = 'PRO'
     AND NOT EXISTS (
       SELECT 1 FROM core.role_permission rp
         JOIN core.role r ON r.id = rp.role_id
        WHERE r.code = o.recorded_role AND rp.permission_code = c.write_permission);

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'patient-reported values recorded by a role that may not record them: %', offending
      USING HINT =
        'CP88 spec 1: the score is asked by somebody with no stake in the answer. A row recorded '
        'by anybody else is not comparable with the others and the research in 12 cannot tell '
        'which is which. Either the grant was revoked after the row was written, or the row '
        'predates the trigger in migration 00070.';
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION core.assert_patient_reported_scale_is_coherent() FROM PUBLIC;

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_patient_reported_scale_is_coherent',
   'a live patient-reported scale exists, its bands cover its range exactly once, its bounds match the observation code''s plausibility band, and no PRO value was recorded by a role that does not hold its write permission',
   132)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- ---------------------------------------------------------------------------
-- 12. Invariant 133: the checklists exist, are answerable, and are reachable
-- ---------------------------------------------------------------------------

-- Invariant 131's lesson, applied to a bigger seed: a seed that matches zero rows inserts
-- nothing and *succeeds*. Every clause below is a way this content could be silently absent
-- while every screen in the system looked fine.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_education_checklists_are_usable() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  -- The four device types spec §6 names, asserted by name rather than by count. A count would
  -- pass on four wrong ones.
  expected text[] := ARRAY['INSULIN_PEN', 'INSULIN_VIAL', 'GLUCOSE_METER',
                           'GLP1_WEEKLY_PEN', 'GLP1_DAILY_PEN'];
  -- The molecules whose dosing rhythm this station teaches, and the checklist each must reach.
  -- Asserted by name rather than by counting rules, because a count passes on three wrong ones —
  -- and the wrong one here is a liraglutide patient being asked which day of the week they
  -- inject (spec §6.4).
  timed text[][] := ARRAY[['Semaglutide', 'GLP1_TECHNIQUE'],
                          ['Dulaglutide', 'GLP1_TECHNIQUE'],
                          ['Liraglutide', 'GLP1_DAILY_TECHNIQUE']];
  offending text;
BEGIN
  SELECT string_agg(want.code, ', ' ORDER BY want.code) INTO offending
    FROM unnest(expected) AS want(code)
   WHERE NOT EXISTS (
     SELECT 1 FROM core.education_checklist c
      WHERE c.device_type = want.code AND c.retired_at IS NULL);

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'these device types have no live checklist: %', offending
      USING HINT =
        'Spec 6 authors four checklists. A device type with none means a patient on that device '
        'reaches the education station and is asked nothing, which is indistinguishable on every '
        'screen from a patient who was educated.';
  END IF;

  SELECT string_agg(c.code, ', ' ORDER BY c.code) INTO offending
    FROM core.education_checklist c
   WHERE c.retired_at IS NULL
     AND NOT EXISTS (
       SELECT 1 FROM core.education_checklist_item i
        WHERE i.checklist_code = c.code AND i.retired_at IS NULL);

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'these live checklists have no items: %', offending
      USING HINT = 'An empty checklist is a station that records nothing and reports success.';
  END IF;

  -- Every item must be answerable in exactly the three states of spec §5. A code with no answers
  -- cannot be written at all (00033''s trigger refuses it); a code with two, or four, is a
  -- checklist whose meaning has drifted from the one the physician authored.
  SELECT string_agg(i.observation_code, ', ' ORDER BY i.observation_code) INTO offending
    FROM core.education_checklist_item i
   WHERE i.retired_at IS NULL
     AND (SELECT count(*) FROM core.observation_answer a
           WHERE a.code = i.observation_code
             AND a.value_code IN ('demonstrated', 'corrected_today', 'unable')
             AND a.retired_at IS NULL) <> 3;

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'these checklist items cannot be answered in the three states: %', offending
      USING HINT =
        'Spec 5: Demonstrated, Corrected today, Unable -- never a boolean. "Corrected today" is '
        'the most clinically useful of the three and the one a simpler design throws away.';
  END IF;

  SELECT string_agg(i.observation_code, ', ' ORDER BY i.observation_code) INTO offending
    FROM core.education_checklist_item i
    JOIN core.observation_code c ON c.code = i.observation_code
   WHERE c.write_permission <> 'education.record' OR c.value_type <> 'coded';

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'these checklist items are not coded values written under education.record: %', offending
      USING HINT =
        'The station''s output is what the patient was able to do in front of somebody. A code '
        'another role may write, or one that is not coded, is an item whose answers nobody can '
        'compare.';
  END IF;

  -- A checklist nothing can select is content nobody will ever see, and it fails silently: the
  -- officer opens the station, sees no checklist, and concludes the patient is on no device.
  SELECT string_agg(c.code, ', ' ORDER BY c.code) INTO offending
    FROM core.education_checklist c
   WHERE c.retired_at IS NULL
     AND NOT EXISTS (
       SELECT 1 FROM core.education_device_rule r
        WHERE r.checklist_code = c.code AND r.retired_at IS NULL);

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'these live checklists are reachable by no device rule: %', offending
      USING HINT =
        'CP92 acceptance criterion 1 is that the checklist appears with no manual selection. A '
        'checklist with no rule can never appear. If the clinic genuinely does not stock the '
        'device yet, retire the checklist rather than leaving it unreachable -- an unreachable '
        'checklist looks identical to a working one from every screen.';
  END IF;

  -- A rule that can never match anything: invariant 131's defect, which is that a register can
  -- look like protection and protect nothing.
  --
  -- The class being *real* is already a foreign key, so that half cannot fail and is not checked
  -- here — a clause that cannot fail is a clause that makes the next reader trust the rest less.
  -- What a foreign key cannot say is that the class has any products in it, and that is the
  -- state this clinic is actually in: `SELF_MONITORING_GLUCOSE` is seeded by migration 00070 with
  -- no products at all, because which meter a clinic stocks and what it costs is the
  -- pharmacist's. So this reports rather than raises, and it reports the honest thing — the meter
  -- checklist exists, is reachable in principle, and will be selected by nothing until somebody
  -- puts a strip on the formulary.
  SELECT string_agg(DISTINCT r.match_class_code, ', ') INTO offending
    FROM core.education_device_rule r
   WHERE r.retired_at IS NULL AND r.match_class_code IS NOT NULL
     AND NOT EXISTS (SELECT 1 FROM core.medication_product p
                       JOIN core.generic g ON g.id = p.generic_id
                      WHERE g.class_code = r.match_class_code AND p.is_active);

  IF offending IS NOT NULL THEN
    RAISE NOTICE 'education device rules whose class has no stocked product: %', offending;
  END IF;

  -- Each timed molecule reaches its own checklist, and reaches no other.
  SELECT string_agg(format('%s -> %s', want.molecule, coalesce(reached.codes, 'nothing')), ', '
                    ORDER BY want.molecule) INTO offending
    FROM (SELECT timed[i][1] AS molecule, timed[i][2] AS checklist
            FROM generate_subscripts(timed, 1) AS i) AS want
    LEFT JOIN LATERAL (
      SELECT string_agg(DISTINCT r.checklist_code, '+' ORDER BY r.checklist_code) AS codes
        FROM core.education_device_rule r
        JOIN core.generic g ON g.id = r.match_generic_id
       WHERE r.retired_at IS NULL AND lower(g.name) = lower(want.molecule)) AS reached ON true
   WHERE reached.codes IS DISTINCT FROM want.checklist;

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'these GLP-1 molecules reach the wrong checklist: %', offending
      USING HINT =
        'Spec 6.4: semaglutide and dulaglutide are weekly, liraglutide is once daily, and the '
        'checklist teaches the rhythm. A molecule reaching "nothing" is a patient taught no '
        'timing at all; one reaching the other checklist is a patient taught the wrong rhythm '
        'in the officer''s own voice. The second is worse.';
  END IF;

  -- Molecules in a class this station knows about that carry no rule of their own.
  --
  -- Reported and not raised. A GLP-1 added to the formulary next year is a real and ordinary
  -- event, and it must not stop a deploy — but it must not pass unremarked either, because the
  -- patient on it reaches station 11 and is taught nothing about their pen. The alarm that
  -- matters is the runtime one (`unclassified_devices` on the session, which the officer sees
  -- with the patient in front of them); this is the same fact reaching whoever runs the deploy.
  SELECT string_agg(DISTINCT g.name, ', ') INTO offending
    FROM core.generic g
   WHERE EXISTS (SELECT 1 FROM core.education_device_rule r
                   JOIN core.generic sibling ON sibling.id = r.match_generic_id
                  WHERE r.retired_at IS NULL AND sibling.class_code = g.class_code)
     AND NOT EXISTS (SELECT 1 FROM core.education_device_rule r
                      WHERE r.retired_at IS NULL AND r.match_generic_id = g.id);

  IF offending IS NOT NULL THEN
    RAISE NOTICE 'molecules in a device class with no education rule of their own: %', offending;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM core.education_reeducation_policy WHERE code = 'DEFAULT') THEN
    RAISE EXCEPTION 'there is no re-education policy'
      USING HINT =
        'Spec 5 makes the threshold a configurable number rather than a constant. With no row '
        'there is no threshold, and the flag either never fires or fires on a default nobody '
        'chose.';
  END IF;

  IF NOT EXISTS (SELECT 1 FROM core.medication_miss_reason WHERE retired_at IS NULL) THEN
    RAISE EXCEPTION 'the list of reasons a dose was missed is empty'
      USING HINT =
        'Spec 7: the fix for each reason is different and the distinction is invisible in a '
        'single adherence percentage. An empty list turns the coded question back into that '
        'percentage.';
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION core.assert_education_checklists_are_usable() FROM PUBLIC;

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_education_checklists_are_usable',
   'each of spec 6''s five device types has a live checklist with items, every item is a coded observation written under education.record and answerable in exactly the three states, every live checklist is reachable by a device rule, each timed GLP-1 molecule reaches its own checklist and no other, and the re-education threshold and miss-reason vocabulary are not empty',
   133)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- ---------------------------------------------------------------------------
-- 13. The research marts
-- ---------------------------------------------------------------------------

-- CP88 criterion 3 and CP92 criterion 4. Views rather than tables, and owned by the migration
-- owner rather than by the research role, which is what lets them cross `identity_link` — the
-- same arrangement `research.cohort` uses and the only one that keeps `dthcms_research` unable to
-- reach a patient id.
--
-- Built from `research.cohort` and not from `research.research_subject`, per 00021's standing
-- rule: the base table includes people who withdrew.
--
-- `operator_key` is the honest part. §12 asks whether the answer depends on who asked, and that
-- cannot be answered without grouping by asker. It is a digest rather than the user id so that
-- the extract carries no clinic identifier — and it is **not anonymisation**: anybody holding
-- the staff list can reverse an md5 of a uuid in a second. It is a grouping key, and the reason
-- it is safe is that the whole `research` schema is under the same governance, not that this
-- column is clever.
CREATE VIEW research.patient_reported_score AS
  SELECT c.research_id,
         c.facility_code,
         date_trunc('month', o.effective_at)::date AS observed_month,
         o.code,
         o.value_num::int  AS score,
         nullif(o.value_code, '') AS not_applicable_reason,
         o.recorded_role   AS asked_by_role,
         md5(o.recorded_by::text) AS operator_key,
         o.station_code
    FROM read.observation o
    JOIN identity_link.research_subject l ON l.patient_id = o.patient_id
    JOIN research.cohort c ON c.research_id = l.research_id
   WHERE o.status = 'ACTIVE'
     AND o.code IN ('IMPROVEMENT_SCORE', 'IMPROVEMENT_SCORE_NA');

COMMENT ON VIEW research.patient_reported_score IS
  'The improvement score and its not-applicable markers, per consenting subject, with the role and an opaque grouping key for who asked (CP88 spec 1, 12).';

CREATE VIEW research.education_competency AS
  SELECT c.research_id,
         c.facility_code,
         date_trunc('month', o.effective_at)::date AS observed_month,
         i.checklist_code,
         i.ordinal,
         o.code,
         o.value_code AS state,
         i.is_critical
    FROM read.observation o
    JOIN core.education_checklist_item i ON i.observation_code = o.code
    JOIN identity_link.research_subject l ON l.patient_id = o.patient_id
    JOIN research.cohort c ON c.research_id = l.research_id
   WHERE o.status = 'ACTIVE';

COMMENT ON VIEW research.education_competency IS
  'Which specific step which subject got wrong, per item, never rolled into a percentage (CP92 spec 8).';

CREATE VIEW research.medication_adherence_report AS
  SELECT c.research_id,
         c.facility_code,
         date_trunc('month', o.effective_at)::date AS observed_month,
         o.code,
         o.value_num::int AS missed_doses,
         nullif(o.value_code, '') AS reason
    FROM read.observation o
    JOIN identity_link.research_subject l ON l.patient_id = o.patient_id
    JOIN research.cohort c ON c.research_id = l.research_id
   WHERE o.status = 'ACTIVE'
     AND o.code IN ('MEDICATION_MISSED_DOSES_7D', 'MEDICATION_MISS_REASON');

COMMENT ON VIEW research.medication_adherence_report IS
  'What patients said about missed doses and why, asked with spec 7''s preamble (CP92).';

GRANT SELECT ON research.patient_reported_score TO dthcms_research;
GRANT SELECT ON research.education_competency TO dthcms_research;
GRANT SELECT ON research.medication_adherence_report TO dthcms_research;

SELECT core.assert_rbac_constraints();

-- +goose Down

DROP VIEW IF EXISTS research.medication_adherence_report;
DROP VIEW IF EXISTS research.education_competency;
DROP VIEW IF EXISTS research.patient_reported_score;

DELETE FROM ops.invariant
 WHERE function_name IN ('assert_patient_reported_scale_is_coherent',
                         'assert_education_checklists_are_usable');
DROP FUNCTION IF EXISTS core.assert_education_checklists_are_usable();
DROP FUNCTION IF EXISTS core.assert_patient_reported_scale_is_coherent();

DROP TRIGGER IF EXISTS observation_pro_names_its_asker ON read.observation;
DROP FUNCTION IF EXISTS core.patient_reported_value_names_its_asker();

DELETE FROM core.observation_answer
 WHERE code IN ('IMPROVEMENT_SCORE_NA', 'MEDICATION_MISS_REASON')
    OR code ~ '^EDU_(PEN|VIAL|MTR|GLP|GLPD)_[0-9]{2}$';

DROP TABLE IF EXISTS core.education_device_rule;
DROP TABLE IF EXISTS core.education_checklist_item;
DROP TABLE IF EXISTS core.education_checklist;
DROP TABLE IF EXISTS core.education_device_type;
DROP TABLE IF EXISTS core.education_reeducation_policy;
DROP TABLE IF EXISTS core.medication_miss_reason;
DROP TABLE IF EXISTS core.pro_scale_anchor;
DROP TABLE IF EXISTS core.pro_scale;
DROP TABLE IF EXISTS core.pro_not_applicable_reason;

DELETE FROM core.observation_code
 WHERE code IN ('IMPROVEMENT_SCORE_NA', 'MEDICATION_MISSED_DOSES_7D',
                'MEDICATION_MISS_REASON', 'EDU_REEDUCATION_FLAG')
    OR code ~ '^EDU_(PEN|VIAL|MTR|GLP|GLPD)_[0-9]{2}$';

UPDATE core.observation_code
   SET write_permission = 'observation.write.history',
       display_en = 'How much better do you feel? (1–10)',
       display_bn = 'কতটা ভালো বোধ করছেন? (১–১০)'
 WHERE code = 'IMPROVEMENT_SCORE';

DELETE FROM core.role_permission
 WHERE permission_code IN ('observation.write.pro', 'education.read');
DELETE FROM core.permission
 WHERE code IN ('observation.write.pro', 'education.read');

DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core'
   AND table_name IN ('pro_scale', 'pro_scale_anchor', 'pro_not_applicable_reason',
                      'education_device_type', 'education_checklist', 'education_checklist_item',
                      'education_device_rule', 'education_reeducation_policy',
                      'medication_miss_reason');
