-- AI prescribing suggestions and the physician's per-item decision (CP82, D-28,
-- docs/ai-prescribing-suggestions.md).
--
-- # The one sentence this file exists to make true
--
-- **A suggestion is a different kind of object from a prescription line, and nothing copies one
-- into the other except an explicit physician action that records who did it and when.**
--
-- The specification says that is "not a policy expressed in validation … it is expressed in the
-- schema". So it is expressed here, three times, in three places that fail independently:
--
--  1. `core.ai_prescribing_suggestion` and `read.prescription_item` are different tables with
--     different owners. The application role holds INSERT on the first and **SELECT only** on the
--     second — as it has since CP80 — so no statement this process can issue turns a suggestion
--     into a line. A line arrives the way every other line arrives: as an event.
--  2. `read.prescription_item.ai_suggestion_id` marks a line that came from a suggestion, and the
--     deferred constraint trigger `core.ai_origin_item_has_a_decision()` refuses to let a
--     transaction **commit** if such a line exists without an ACCEPTED or EDITED decision naming
--     that exact line. Deferred rather than immediate because the decision row and the item row
--     are written in the same transaction and each wants the other to exist first; deferring means
--     the order does not matter and the guarantee is about the commit, which is the only moment
--     that matters.
--  3. Invariant 130 asks the same question of the whole table, in both directions, so a row
--     written with triggers disabled, or a decision whose line never materialised, is found by the
--     verifier rather than by a physician.
--
-- Between them they hold the criterion the checkpoint is judged on. What they cannot hold is
-- stated plainly rather than glossed: a physician who reads a suggestion off the screen and types
-- the same drug into the ordinary search box has written a hand-typed line, and the system records
-- it as one. That is correct — §1 says an accepted suggestion gets no easier passage and a typed
-- line no harder one — but it means the guarantee is about *provenance that was recorded*, not
-- about a thought somebody had. The Go side is what stops the recording being skipped: `Addition`
-- has no field for a suggestion id, and the only function that sets one is the decision path.
--
-- # Unactioned is a state, and it is not "rejected"
--
-- §1: *"A suggestion that is never acted on is not a rejection … Conflating 'he said no' with 'he
-- did not look' would poison the learning signal and, worse, would let silence be read as a
-- decision."*
--
-- So there is **no `UNACTIONED` value anywhere in this schema**. `core.ai_prescribing_decision`
-- has one row per decision and none for a suggestion nobody touched, and its `decision` column's
-- CHECK lists exactly the three §4 decisions. A query that wants unactioned suggestions writes a
-- LEFT JOIN and tests for NULL; a query that forgets gets no rejections it did not earn. The
-- alternative — a fourth enum value, defaulted — is one `DEFAULT` clause away from recording that
-- a physician rejected every suggestion he never saw.
--
-- # The suggestion is stored as offered, and is never mutated
--
-- §4: *"the suggestion is stored as offered, by value, and is never mutated by the edit."* The
-- entire value — product, dose, frequency, duration, route, the reasoning and the facts it rests
-- on — is columns on the suggestion row, and `core.ai_prescribing_suggestion_is_as_offered()`
-- refuses every UPDATE and every DELETE for every role. An edit writes a decision row and a
-- prescription line; it does not touch the suggestion, and it could not.
--
-- That is why the product's words are copied here rather than joined: the reason CP80 gives for
-- `read.prescription_item` applies with more force to a record whose whole purpose is to say what
-- was proposed on one afternoon.
--
-- # The rejection vocabulary is reference data
--
-- §5, and the same rule as the counselling templates and the formulary: Dr Nahid changes the
-- wording or adds a reason without a code release. It is a table with bilingual labels and an
-- ordering, not a Go enum — `core.ai_prescribing_decision.reject_reason_code` is a foreign key to
-- it, so a reason that is retired keeps every decision that used it readable.
--
-- It is served under `reference.read` (00067) rather than under a permission of its own. The test
-- that permission's own migration states is "is there a patient in this data": there is not — it
-- is twelve bilingual labels — and the nine station roles reach `prescription.*` only for the
-- station they are working, which is precisely the defect 00067 exists to have fixed.
--
-- # Controlled drugs
--
-- §2 forbids proposing a controlled or scheduled drug. CP75's formulary has no column saying which
-- those are, and adding a boolean to `core.generic` would make the answer a property of the
-- molecule that somebody has to remember to set for every molecule added afterwards — defaulting,
-- inevitably, to "not controlled", which is the wrong way round for this rule.
--
-- So it is a register, `core.controlled_molecule`, listing the **molecules** the AI may not
-- propose, with the schedule and a bilingual note. It is reference data for the same reason the
-- rejection vocabulary is: the Bangladesh schedule is not ours to compile into a binary.
--
-- It is keyed on the molecule and **not** on a row in this clinic's formulary, which is the whole
-- of §2 below and is worth stating here as well: "the AI may not propose testosterone" has to be
-- true before this clinic stocks testosterone, not from the moment somebody adds it. The formulary
-- is consulted at read time by `core.generic_is_controlled()`, which the shortlist subtraction and
-- the answer check both call — two copies of that predicate would agree until one was edited.
--
-- The register is consulted **twice** — once to leave the drug out of the shortlist the model is
-- given, and once on the model's answer — and the second is the one that counts, because the first
-- is a prompt and prompts are suggestions.

-- +goose Up

-- ---------------------------------------------------------------------------
-- 1. The rejection vocabulary (§5)
-- ---------------------------------------------------------------------------

CREATE TABLE core.ai_suggestion_reject_reason (
  code text PRIMARY KEY,

  label_en text NOT NULL,
  label_bn text NOT NULL,

  -- The order they are offered in. A physician mid-clinic reads a list top down, and the order
  -- is a clinical judgement about which reasons are commonest — which is why it is a column he
  -- can change rather than the alphabet.
  ordering integer NOT NULL,

  -- Retired rather than deleted, so a decision recorded against it stays readable. Nothing in
  -- this system is deleted; a vocabulary is not the place to start.
  retired_at timestamptz,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT ai_reject_reason_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,39}$'),
  -- Both languages or neither. A reason list half-translated is a list the Bengali screen shows
  -- English in, at the moment a physician is choosing what to record.
  CONSTRAINT ai_reject_reason_is_bilingual
    CHECK (btrim(label_en) <> '' AND btrim(label_bn) <> '')
);

SELECT core.attach_updated_at('core.ai_suggestion_reject_reason');

-- INSERT and UPDATE as well as SELECT: "editable without a code release" is a property of the
-- grant, not of a comment. The screen that does the editing is a later checkpoint; the permission
-- to do it is here so that adding the screen is not also a migration.
GRANT SELECT, INSERT, UPDATE ON core.ai_suggestion_reject_reason TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'ai_suggestion_reject_reason',
   'A vocabulary of reasons a physician may decline a suggestion. No patient in it, and it is the same list at every site.')
ON CONFLICT DO NOTHING;

COMMENT ON TABLE core.ai_suggestion_reject_reason IS
  'Why a physician declined an AI prescribing suggestion (CP82 §5). Reference data, bilingual, editable without a release.';

-- The twelve, exactly as docs/ai-prescribing-suggestions.md §5 authors them. The Bengali is
-- clinical register rather than literal translation, which is the author's own note on the table.
INSERT INTO core.ai_suggestion_reject_reason (code, label_en, label_bn, ordering) VALUES
  ('NOT_INDICATED',      'Not indicated for this patient',           'এই রোগীর ক্ষেত্রে প্রযোজ্য নয়',              10),
  ('CONTRAINDICATED',    'Contraindicated — renal, hepatic or cardiac', 'প্রতিনির্দেশিত — কিডনি, লিভার বা হৃদযন্ত্রজনিত', 20),
  ('ALLERGY',            'Allergy or previous adverse reaction',     'অ্যালার্জি বা পূর্বে বিরূপ প্রতিক্রিয়া',      30),
  ('INTERACTION',        'Interacts with current therapy',           'বর্তমান ওষুধের সঙ্গে বিক্রিয়া',              40),
  ('DUPLICATE',          'Duplicates therapy already prescribed',    'ইতিমধ্যে দেওয়া ওষুধেরই পুনরাবৃত্তি',          50),
  ('WRONG_DOSE',         'Dose, frequency or duration wrong',        'মাত্রা, সময় বা মেয়াদ ঠিক নেই',               60),
  ('PREFER_ALTERNATIVE', 'Prefer a different agent in this class',   'এই শ্রেণিতে অন্য ওষুধ পছন্দ',                 70),
  ('COST',               'Patient cannot afford it',                 'রোগীর সাধ্যের বাইরে',                        80),
  ('AVAILABILITY',       'Not reliably available',                   'নিয়মিত পাওয়া যায় না',                        90),
  ('ADHERENCE',          'Adherence or patient preference',          'রোগীর পছন্দ বা নিয়ম মেনে চলার সমস্যা',        100),
  ('TOO_EARLY',          'Defer — reassess at the next visit',       'এখন নয় — পরের বার পুনর্বিবেচনা',              110),
  ('INSUFFICIENT_DATA',  'Not enough information to decide',         'সিদ্ধান্ত নেওয়ার মতো তথ্য নেই',                120)
ON CONFLICT (code) DO UPDATE SET
  label_en = EXCLUDED.label_en, label_bn = EXCLUDED.label_bn, ordering = EXCLUDED.ordering;

-- ---------------------------------------------------------------------------
-- 2. The register of molecules the AI may not propose (§2)
-- ---------------------------------------------------------------------------

-- **Keyed on the molecule, not on a row in this clinic's formulary.**
--
-- The first version of this table had `generic_id uuid PRIMARY KEY REFERENCES core.generic(id)`
-- and was seeded with `INSERT ... SELECT ... FROM core.generic WHERE lower(name) = …`. Two things
-- were wrong with that and the second is the one that matters.
--
-- *It could silently register nothing.* A seed whose `WHERE` matched no row succeeded and inserted
-- nothing, and the register then looked like protection while protecting only whatever happened to
-- match. That is worse than an empty register, because an empty one is obviously empty. It
-- happened: a testosterone row was added and matched zero rows, because there is no testosterone
-- generic in CP75's seed.
--
-- *And it could only protect what the clinic already stocked.* "The AI may not propose
-- testosterone" has to be true **before** testosterone is in the formulary, not from the moment a
-- pharmacist adds it. Keyed on `generic_id`, the rule came into existence at the same instant as
-- the thing it was supposed to forbid, and nothing anywhere said so. A register that is safe until
-- somebody adds a product is a register whose failure is scheduled rather than possible.
--
-- So the row is a molecule name and the formulary is consulted at read time, by
-- `core.generic_is_controlled()`. Registering a molecule this clinic does not stock is now the
-- normal case rather than a no-op.
--
-- # The spelling is CP78's, not a second convention
--
-- `molecule` is written in the **component vocabulary** that `00058_generic_components.sql`
-- defines and argues for: the salt is dropped ("Testosterone", not "Testosterone undecanoate"),
-- the parenthetical synonym is dropped, the dose qualifier is dropped. The unique index is on
-- `lower(molecule)` for the same reason `generic_component_key` is — one molecule is spelled one
-- way, or two spellings are two molecules that never collide.
--
-- Deliberately **not** a foreign key to `core.generic_component.molecule`: that table only holds
-- the molecules of medicines this clinic stocks, which is the dependency this whole change exists
-- to remove.
--
-- # What "controlled" means here, restated
--
-- The membership rule is *the AI may not propose it*. "Scheduled by law" is its main reason and
-- not its only one — see the testosterone row. If the two ever need separating, that is a second
-- table for the pharmacist's legal schedule, not a rewrite of this one.

CREATE TABLE core.controlled_molecule (
  molecule text PRIMARY KEY,

  -- The schedule as the register's author understands it. Free text rather than an enum because
  -- the Bangladesh Narcotics Control Act's classes are not ours to model, and a wrong enum is
  -- worse than an honest string a pharmacist can read. It is also where a row says "classification
  -- to be confirmed" out loud rather than asserting a class nobody checked.
  schedule text NOT NULL,
  note_en text NOT NULL,
  note_bn text NOT NULL,

  recorded_at timestamptz NOT NULL DEFAULT now(),
  recorded_by uuid REFERENCES core.app_user(id),

  CONSTRAINT controlled_molecule_says_which CHECK (btrim(schedule) <> ''),
  CONSTRAINT controlled_molecule_is_bilingual
    CHECK (btrim(note_en) <> '' AND btrim(note_bn) <> ''),
  -- Stored normalised, because a stray space is a row that never matches anything and looks
  -- exactly like a row that does. Invariant 131 checks the whole table for this; the constraint
  -- is what stops one arriving.
  CONSTRAINT controlled_molecule_is_normalised
    CHECK (btrim(molecule) = molecule AND molecule <> '' AND molecule !~ '  ')
);

-- One molecule is spelled one way (00058's rule, and its index shape).
CREATE UNIQUE INDEX controlled_molecule_key ON core.controlled_molecule (lower(molecule));

GRANT SELECT, INSERT, UPDATE ON core.controlled_molecule TO dthcms_app;
REVOKE DELETE, TRUNCATE ON core.controlled_molecule FROM dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'controlled_molecule',
   'Which molecules a machine may not propose is a fact about pharmacology and the law, not about a clinic''s records or its stock.')
ON CONFLICT DO NOTHING;

COMMENT ON TABLE core.controlled_molecule IS
  'Molecules the AI prescribing agent may not propose (CP82 §2). Keyed on the molecule rather than on a formulary row, so the rule holds before this clinic stocks the drug as well as after.';

-- +goose StatementBegin
-- Does this generic contain a molecule the AI may not propose?
--
-- **One definition, consulted twice**: once to leave the drug out of the shortlist the model is
-- shown, and once on the model's answer. Two copies of this predicate would agree until somebody
-- edited one, and the one that would be wrong is whichever is not the one being read at the time.
--
-- Two paths, because the formulary knows a medicine's molecules in two different strengths:
--
--   (a) `core.generic_component` — CP78's decomposition, exact and in the same vocabulary as this
--       register. This is the good path and it is the one that catches a combination product:
--       a fixed-dose combination containing a registered molecule is refused on the component,
--       not on its trade-facing name.
--   (b) the generic's own name, compared as a **sequence of words**. Needed because
--       `components_state` defaults to UNDETERMINED: a product added tomorrow may have no
--       decomposition recorded, and a register that only read (a) would pass it.
--
-- Path (b) is word-boundary containment rather than equality, so "Testosterone undecanoate" and
-- "Testosterone gel" are both caught by the single word "Testosterone". Both sides are normalised
-- by replacing every run of non-alphanumeric characters with one space and padding with spaces, so
-- the comparison cannot match inside a word and needs no regular expression built from data a
-- person typed.
--
-- **It errs towards refusing, deliberately.** A false positive costs one medicine the AI may not
-- propose — and a physician may still prescribe it by hand, which is the whole shape of this
-- checkpoint. A false negative costs the guarantee. That asymmetry is what makes containment the
-- right comparison here, where it would be the wrong one for a duplicate check.
CREATE OR REPLACE FUNCTION core.generic_is_controlled(p_generic_id uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
  SELECT EXISTS (
    SELECT 1 FROM core.controlled_molecule m
     WHERE EXISTS (
             SELECT 1 FROM core.generic_component c
              WHERE c.generic_id = p_generic_id
                AND lower(c.molecule) = lower(m.molecule))
        OR EXISTS (
             SELECT 1 FROM core.generic g
              WHERE g.id = p_generic_id
                AND strpos(
                      ' ' || regexp_replace(lower(g.name), '[^a-z0-9]+', ' ', 'g') || ' ',
                      ' ' || regexp_replace(lower(m.molecule), '[^a-z0-9]+', ' ', 'g') || ' ') > 0)
  );
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION core.generic_is_controlled(uuid) TO dthcms_app;

COMMENT ON FUNCTION core.generic_is_controlled(uuid) IS
  'True when a formulary generic contains a molecule on core.controlled_molecule, by CP78 component or by word in the generic name. The one definition the shortlist subtraction and the answer check both read (CP82 §2).';

-- The seed. `INSERT ... VALUES`, not `INSERT ... SELECT`: a row here is a statement about a
-- molecule and cannot be made conditional on this clinic stocking it, so it cannot match nothing.
-- That is flaw 1 fixed at the source rather than guarded against; invariant 131 then checks that
-- what the seed intended is actually in the table, and keeps checking afterwards.
--
-- Pregabalin is scheduled in the jurisdictions this clinic's prescribing follows, and it is in
-- CP75's formulary because diabetic neuropathy is a daily problem here. **Gabapentin sits in the
-- same class and is deliberately not on this register** — that distinction is what the register
-- exists to be able to make, because an agent refused a whole therapeutic class would be an agent
-- nobody could use for neuropathy at all.
--
-- Testosterone, added by Amlan on review of the register (13 Sep 2026). It was missing, and its
-- absence is the kind that only shows up if you remember what kind of clinic this is: DTHC is a
-- diabetes, thyroid **and hormone** clinic, the blueprint's own formulary list names testosterone,
-- and it is the one androgen a hormone clinic prescribes regularly. A register of scheduled
-- molecules that omits the scheduled molecule central to one of the clinic's three specialities is
-- a register that looks complete and is not.
--
-- Two reasons it must be here, and only the first is about the law:
--
--   * It is a scheduled anabolic-androgenic steroid in most jurisdictions whose scheduling this
--     clinic's prescribing follows. **Its exact Bangladesh classification needs confirming** — the
--     schedule string below says so rather than asserting a class I am not certain of, which is
--     why this column is free text.
--   * Clinically it should not be machine-proposed regardless of its legal class. Testosterone
--     replacement follows confirmed hypogonadism on repeat morning testosterone with the
--     gonadotrophins interpreted alongside, and it carries erythrocytosis, prostate and fertility
--     consequences that belong in a conversation. A suggestion engine that proposes it is
--     proposing the end of a work-up it cannot see.
--
-- That second reason is doing work this table's name does not quite describe, and it is worth
-- being honest about: the membership rule is "the AI may not propose it", and "scheduled by law"
-- is its main but not its only case.
--
-- **There is no testosterone in CP75's formulary today, and that is now the point rather than the
-- problem.** The row holds; the day a pharmacist adds a testosterone product, it is already
-- excluded, with no change to this table in between.
--
-- **Both rows are clinical content and need Dr Nahid's confirmation**, in the same way §5's
-- vocabulary does. They are rows he can change, which is the point of them being rows.
INSERT INTO core.controlled_molecule (molecule, schedule, note_en, note_bn) VALUES
  ('Pregabalin',
   'Scheduled — psychotropic',
   'Pregabalin is a scheduled substance. The AI may not propose it; a physician may still prescribe it by hand.',
   'প্রিগাবালিন একটি তফসিলভুক্ত ওষুধ। এআই এটি প্রস্তাব করতে পারবে না; চিকিৎসক নিজে হাতে লিখতে পারবেন।'),
  ('Testosterone',
   'Anabolic androgenic steroid — Bangladesh classification to be confirmed',
   'Testosterone is not proposed by the AI. Replacement follows a confirmed work-up and a conversation about fertility, haematocrit and prostate risk; a physician prescribes it by hand.',
   'টেস্টোস্টেরন এআই প্রস্তাব করে না। নিশ্চিত পরীক্ষা এবং প্রজননক্ষমতা, রক্তের ঘনত্ব ও প্রস্টেট ঝুঁকি নিয়ে আলোচনার পরেই এটি দেওয়া হয়; চিকিৎসক নিজে হাতে লিখবেন।')
ON CONFLICT (molecule) DO UPDATE SET
  schedule = EXCLUDED.schedule,
  note_en = EXCLUDED.note_en, note_bn = EXCLUDED.note_bn;


-- ---------------------------------------------------------------------------
-- 3. The run: one ask, and what came of it
-- ---------------------------------------------------------------------------

-- A run exists even when nothing was suggested, and that is the point of it. "The AI proposed
-- nothing" and "nobody has asked the AI" are different facts about a consultation and a screen
-- that could not tell them apart would show the same empty column for both. §2's refusals are the
-- interesting case: a patient with no allergy status produces a run in state REFUSED with a reason
-- and **no model call at all**, which is a thing the physician should be told rather than a
-- silence.
CREATE TABLE core.ai_prescribing_run (
  id uuid PRIMARY KEY,
  facility_id uuid NOT NULL REFERENCES core.facility(id),
  patient_id  uuid NOT NULL REFERENCES core.patient(id),
  visit_id    uuid NOT NULL REFERENCES core.visit(id),
  -- The draft the suggestions were offered against. A suggestion has no meaning without the sheet
  -- it was offered beside: the same patient's next prescription is a different consultation.
  prescription_id uuid NOT NULL REFERENCES read.prescription(id),

  state text NOT NULL CHECK (state IN ('READY', 'REFUSED', 'FAILED')),

  -- Why nothing was asked. Named in a word an operator can group by, and each one is a rule from
  -- §2 rather than a Go error: what a physician needs to be told is which clinical gate stopped
  -- this, not which function returned.
  refusal text CHECK (refusal IS NULL OR refusal IN (
    'NO_ALLERGY_STATUS', 'NOT_A_DRAFT', 'NO_CANDIDATES')),

  -- §10.6's fourth permanent invariant: every AI output is stored with the prompt and the model
  -- version that produced it. Null on a run that never reached a model, which is exactly the
  -- distinction `state` makes.
  ai_interaction_id uuid REFERENCES core.ai_interaction(id),
  prompt_version    text,
  model_version     text,

  offered_count integer NOT NULL DEFAULT 0 CHECK (offered_count >= 0),
  -- How many of the model's items this server threw away, and why they went. §2's refusals are
  -- enforced after the answer comes back as well as before it goes out, and a deployment where
  -- this number is large is one whose prompt needs work — which is a thing somebody has to be able
  -- to see.
  dropped_count integer NOT NULL DEFAULT 0 CHECK (dropped_count >= 0),
  dropped_reasons jsonb NOT NULL DEFAULT '{}'::jsonb,

  failure_detail text NOT NULL DEFAULT '',

  requested_at timestamptz NOT NULL,
  requested_by uuid NOT NULL REFERENCES core.app_user(id),

  created_at timestamptz NOT NULL DEFAULT now(),

  -- A refusal names its rule; anything else does not claim to have been refused.
  CONSTRAINT ai_prescribing_run_refusal_is_named CHECK ((state = 'REFUSED') = (refusal IS NOT NULL)),
  -- A run that reached a model says which one. A READY run with no interaction is a run whose
  -- suggestions nobody can trace to a call, which is the record §10.6 exists to prevent.
  CONSTRAINT ai_prescribing_run_ready_names_its_model CHECK (
    state <> 'READY' OR (ai_interaction_id IS NOT NULL
                         AND btrim(coalesce(prompt_version, '')) <> ''
                         AND btrim(coalesce(model_version, '')) <> '')),
  -- The counts and the reason map are bookkeeping about the model's answer, never its content.
  -- The identifier check is the same one `core.ai_synthesis.context` carries, and it is here for
  -- the same reason: a drop reason is written by us, so a patient's name in one would be our
  -- defect, and this is where it stops.
  CONSTRAINT ai_prescribing_run_reasons_name_nobody CHECK (NOT ops.carries_identifier(dropped_reasons))
);

CREATE INDEX ai_prescribing_run_for_prescription
  ON core.ai_prescribing_run (prescription_id, requested_at DESC);
CREATE INDEX ai_prescribing_run_by_patient
  ON core.ai_prescribing_run (facility_id, patient_id, requested_at DESC);

GRANT SELECT, INSERT ON core.ai_prescribing_run TO dthcms_app;
-- Never updated and never deleted: a run is a thing that happened.
REVOKE UPDATE, DELETE ON core.ai_prescribing_run FROM dthcms_app;

COMMENT ON TABLE core.ai_prescribing_run IS
  'One ask of the AI prescribing agent against one draft: what it was allowed to propose, what it proposed, and what this server threw away (CP82).';

-- ---------------------------------------------------------------------------
-- 4. The suggestion, as offered
-- ---------------------------------------------------------------------------

CREATE TABLE core.ai_prescribing_suggestion (
  id uuid PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES core.ai_prescribing_run(id),
  facility_id uuid NOT NULL REFERENCES core.facility(id),
  -- Denormalised from the run so that "what was suggested on this sheet" is one index rather than
  -- a join, and so that the constraint trigger in §6 can find a suggestion's sheet without one.
  prescription_id uuid NOT NULL REFERENCES read.prescription(id),

  ordinal integer NOT NULL CHECK (ordinal >= 1),

  -- ---- what was proposed, by value ------------------------------------
  --
  -- The product is a foreign key **and** its words are copied. The key is what makes the
  -- formulary rule checkable after the fact; the words are what make the suggestion readable when
  -- the product has been withdrawn, which is the same argument CP80 makes for a prescription line
  -- and is stronger here, because this row's only purpose is to say what was proposed once.
  product_id uuid NOT NULL REFERENCES core.medication_product(id),
  product_label text NOT NULL,
  generic_name  text NOT NULL,
  strength      text NOT NULL DEFAULT '',
  form_code     text NOT NULL DEFAULT '',

  dose          text NOT NULL,
  daily_dose    numeric(12,3),
  dose_unit     text NOT NULL DEFAULT '',
  frequency     text NOT NULL,
  duration_days integer,
  route         text NOT NULL DEFAULT '',

  -- ---- why ------------------------------------------------------------
  --
  -- §2: *"a suggestion a physician cannot audit in five seconds is a suggestion he will either
  -- rubber-stamp or ignore, and both are failures."* So the reasoning and the facts are not
  -- optional columns; they are NOT NULL with a CHECK that they say something.
  rationale_en text NOT NULL,
  rationale_bn text NOT NULL,
  -- The fact references the reasoning rests on, as the model cited them and as CP71's context
  -- defines them. An array of strings; the gateway's grounding check has already refused any
  -- reference that was not in the context it was shown.
  basis jsonb NOT NULL,

  offered_at timestamptz NOT NULL,

  CONSTRAINT ai_suggestion_once_per_run UNIQUE (run_id, ordinal),
  CONSTRAINT ai_suggestion_says_what_it_is CHECK (btrim(product_label) <> ''),
  CONSTRAINT ai_suggestion_has_directions
    CHECK (btrim(dose) <> '' AND btrim(frequency) <> ''),
  CONSTRAINT ai_suggestion_duration_is_sane
    CHECK (duration_days IS NULL OR duration_days BETWEEN 1 AND 3650),
  CONSTRAINT ai_suggestion_daily_dose_is_positive
    CHECK (daily_dose IS NULL OR daily_dose > 0),
  CONSTRAINT ai_suggestion_numeric_dose_has_a_unit
    CHECK (daily_dose IS NULL OR btrim(dose_unit) <> ''),
  -- Bilingual, and both required, for the reason every other clinical string in this schema is:
  -- the physician may be reading either, and a half-translated rationale is the half he cannot
  -- audit.
  CONSTRAINT ai_suggestion_rationale_is_bilingual
    CHECK (btrim(rationale_en) <> '' AND btrim(rationale_bn) <> ''),
  -- A suggestion that rests on nothing is one nobody can check in five seconds.
  CONSTRAINT ai_suggestion_rests_on_something
    CHECK (jsonb_typeof(basis) = 'array' AND jsonb_array_length(basis) >= 1),
  -- The same rule `core.ai_synthesis.context` carries. A model's words reach this table, and a
  -- model that wrote a patient's name into its reasoning must not be recorded as having done so
  -- in a column nobody scrubs.
  CONSTRAINT ai_suggestion_names_nobody
    CHECK (NOT ops.carries_identifier(basis)
           AND NOT ops.text_carries_identifier(rationale_en)
           AND NOT ops.text_carries_identifier(rationale_bn))
);

CREATE INDEX ai_suggestion_for_prescription
  ON core.ai_prescribing_suggestion (prescription_id, ordinal);
CREATE INDEX ai_suggestion_by_run
  ON core.ai_prescribing_suggestion (run_id, ordinal);

GRANT SELECT, INSERT ON core.ai_prescribing_suggestion TO dthcms_app;
REVOKE UPDATE, DELETE ON core.ai_prescribing_suggestion FROM dthcms_app;

COMMENT ON TABLE core.ai_prescribing_suggestion IS
  'One medicine the AI proposed, exactly as it proposed it (CP82 §4). Never updated: an edit writes a decision and a prescription line, and leaves this row saying what was offered.';

-- +goose StatementBegin
-- §4's load-bearing sentence, as a trigger.
--
-- *"If the system records only the final line, the fact that the model said 500 mg and the
-- physician wrote 850 mg is lost — and that difference is the entire training signal."*
--
-- A check in Go would hold for the paths somebody remembered. This holds for the support script,
-- the second client, and the developer with the application's password — every role, including the
-- projector, because a projection rebuild has no business touching this table at all: nothing here
-- arrives from the ledger.
CREATE OR REPLACE FUNCTION core.ai_prescribing_suggestion_is_as_offered() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'an AI prescribing suggestion is never deleted (id %)', OLD.id
      USING HINT = 'A suggestion nobody acted on is recorded as unactioned, which is a different '
                   'fact from a rejection and from never having been offered.';
  END IF;
  RAISE EXCEPTION 'an AI prescribing suggestion is stored as offered and is never changed (id %)', OLD.id
    USING HINT = 'An edit records a decision and writes a prescription line. The difference '
                 'between what was offered and what was issued is the whole record; mutating '
                 'this row would erase it.';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER ai_prescribing_suggestion_as_offered
  BEFORE UPDATE OR DELETE ON core.ai_prescribing_suggestion
  FOR EACH ROW EXECUTE FUNCTION core.ai_prescribing_suggestion_is_as_offered();

-- ---------------------------------------------------------------------------
-- 5. The decision (§4)
-- ---------------------------------------------------------------------------

CREATE TABLE core.ai_prescribing_decision (
  id uuid PRIMARY KEY,
  -- One decision per suggestion, enforced by the primary-key-strength unique. A suggestion
  -- accepted and then rejected is not two decisions about one proposal; it is an accepted line
  -- the physician then removed, which CP80 already records with its own reason.
  suggestion_id uuid NOT NULL UNIQUE REFERENCES core.ai_prescribing_suggestion(id),
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  -- The three of §4's table. **There is no fourth**, and the absence is the design: see the file
  -- header on why "unactioned" is the absence of a row rather than a value here.
  decision text NOT NULL CHECK (decision IN ('ACCEPTED', 'EDITED', 'REJECTED')),

  decided_by uuid NOT NULL REFERENCES core.app_user(id),
  decided_at timestamptz NOT NULL,

  -- The line this decision produced. Deliberately **not** a foreign key to
  -- `read.prescription_item`: that table is a projection and a rebuild empties it, so a foreign
  -- key from a durable core table into it would make the rebuild impossible or the decision
  -- deletable, and both are worse than the join being unenforced. Invariant 130 asserts the join
  -- in both directions instead, which is a check rather than a cascade.
  prescription_item_id uuid,

  -- §5's optional reason. Optional because *"a physician mid-clinic with a patient in front of him
  -- should be able to dismiss a suggestion in one action"*; a foreign key because the vocabulary
  -- is a table he edits.
  reject_reason_code text REFERENCES core.ai_suggestion_reject_reason(code),
  reject_note text NOT NULL DEFAULT '',

  event_id uuid NOT NULL UNIQUE,

  created_at timestamptz NOT NULL DEFAULT now(),

  -- §4's table, as a constraint. An acceptance or an edit produces a line; a rejection produces
  -- nothing. A row that claimed otherwise would be a decision nobody could render.
  CONSTRAINT ai_decision_produces_what_it_says CHECK (
    (decision IN ('ACCEPTED', 'EDITED')) = (prescription_item_id IS NOT NULL)),
  -- Only a rejection carries a reason. An acceptance with a rejection reason on it is a client
  -- that has sent the wrong body, and absorbing it would put a reason nobody meant into the
  -- signal §5 exists to collect.
  CONSTRAINT ai_decision_reason_belongs_to_a_rejection CHECK (
    decision = 'REJECTED'
    OR (reject_reason_code IS NULL AND btrim(reject_note) = '')),
  -- The free text is the physician's own words about a proposal, not about a patient. Held to the
  -- same identifier rule as everything else in this file.
  CONSTRAINT ai_decision_note_names_nobody
    CHECK (NOT ops.text_carries_identifier(reject_note))
);

CREATE INDEX ai_decision_by_item
  ON core.ai_prescribing_decision (prescription_item_id)
  WHERE prescription_item_id IS NOT NULL;
CREATE INDEX ai_decision_rate
  ON core.ai_prescribing_decision (facility_id, decided_at DESC, decision);

GRANT SELECT, INSERT ON core.ai_prescribing_decision TO dthcms_app;
REVOKE UPDATE, DELETE ON core.ai_prescribing_decision FROM dthcms_app;

COMMENT ON TABLE core.ai_prescribing_decision IS
  'What the physician did with one AI prescribing suggestion: accepted, edited or rejected, with who and when (CP82 §4). A suggestion with no row here is unactioned, which is not a rejection.';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.ai_prescribing_decision_is_final() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'a decision on an AI suggestion is never deleted (id %)', OLD.id
      USING HINT = 'The trail is the whole point of CP82. A physician who changed his mind '
                   'removes the prescription line, which CP80 records with its own reason.';
  END IF;
  RAISE EXCEPTION 'a decision on an AI suggestion is written once (id %)', OLD.id;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER ai_prescribing_decision_final
  BEFORE UPDATE OR DELETE ON core.ai_prescribing_decision
  FOR EACH ROW EXECUTE FUNCTION core.ai_prescribing_decision_is_final();

-- ---------------------------------------------------------------------------
-- 6. The boundary: a line that came from a suggestion has a decision behind it
-- ---------------------------------------------------------------------------

ALTER TABLE read.prescription_item
  ADD COLUMN ai_suggestion_id uuid REFERENCES core.ai_prescribing_suggestion(id);

COMMENT ON COLUMN read.prescription_item.ai_suggestion_id IS
  'The AI suggestion this line was accepted or edited from, or NULL for a line the physician wrote (CP82). A line carrying one cannot be committed without a decision naming it.';

CREATE INDEX prescription_item_from_ai
  ON read.prescription_item (ai_suggestion_id)
  WHERE ai_suggestion_id IS NOT NULL;

-- +goose StatementBegin
-- **The criterion, as a constraint.**
--
-- *No AI-suggested item may reach a SIGNED prescription without an explicit accept or edit event.*
--
-- It is checked at commit rather than at insert, and the deferral is not a weakening. The decision
-- row names the item and the item names the suggestion, so at INSERT time each is waiting for the
-- other; an immediate trigger would force one of the two writes to be blind. Deferred, the
-- question asked is the only one worth asking — *may this transaction commit?* — and the answer is
-- no unless both rows exist and agree.
--
-- Checked on UPDATE as well as INSERT, because the projection updates an item when
-- `PRESCRIPTION_ITEM_MODIFIED` arrives, and a modification that moved `ai_suggestion_id` onto a
-- line would be exactly the hole this closes.
--
-- Note what is *not* excused: there is no `dthcms.rebuilding` escape here, unlike the freeze
-- triggers. A rebuild replays the ledger, the ledger carries `ai_suggestion_id` on the item event,
-- and the decision rows are in `core` and untouched by a rebuild — so a rebuild satisfies this
-- check by construction. A rebuild that did *not* satisfy it would be a rebuild producing an
-- AI-origin line nobody decided on, which is precisely the state that must not exist.
CREATE OR REPLACE FUNCTION core.ai_origin_item_has_a_decision() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  decided text;
BEGIN
  IF NEW.ai_suggestion_id IS NULL THEN
    RETURN NULL;
  END IF;

  SELECT d.decision INTO decided
    FROM core.ai_prescribing_decision d
   WHERE d.suggestion_id = NEW.ai_suggestion_id
     AND d.prescription_item_id = NEW.id;

  IF decided IS NULL THEN
    RAISE EXCEPTION
      'prescription item % claims to come from AI suggestion % and no physician decision says so',
      NEW.id, NEW.ai_suggestion_id
      USING HINT = 'A suggestion becomes a line only through an accept or an edit, recorded with '
                   'who did it and when. There is no other path, and this is where the absence of '
                   'one is enforced (CP82 §1).';
  END IF;

  IF decided NOT IN ('ACCEPTED', 'EDITED') THEN
    -- Unreachable while the CHECK above holds — a REJECTED decision cannot carry an item id — and
    -- kept because the two constraints are independent and this one is cheap. If the CHECK is ever
    -- relaxed, this is what stops a rejection quietly producing a line.
    RAISE EXCEPTION
      'prescription item % comes from a suggestion whose decision was %', NEW.id, decided;
  END IF;

  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER prescription_item_ai_origin_is_decided
  AFTER INSERT OR UPDATE ON read.prescription_item
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION core.ai_origin_item_has_a_decision();

-- +goose StatementBegin
-- CP80's freeze list learns about the new column.
--
-- Replaced in full rather than patched, because the function is written as an explicit list of what
-- may change and a reader has to be able to see the whole list in one place. The only difference
-- from 00062's version is the `ai_suggestion_id` clause in the identity check: where a line came
-- from is part of its identity, and a line that could change its origin after the fact is a line
-- whose provenance proves nothing.
CREATE OR REPLACE FUNCTION core.prescription_item_is_frozen() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  parent read.prescription%ROWTYPE;
  target uuid;
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF coalesce(current_setting('dthcms.rebuilding', true), '') = 'on' THEN
      RETURN OLD;
    END IF;
    RAISE EXCEPTION 'a prescription item is never deleted (id %)', OLD.id
      USING HINT = 'Remove it — the row stays, with who removed it and why.';
  END IF;

  target := CASE WHEN TG_OP = 'INSERT' THEN NEW.prescription_id ELSE OLD.prescription_id END;
  SELECT * INTO parent FROM read.prescription WHERE id = target;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no prescription % to put an item on', target;
  END IF;

  IF parent.status <> 'DRAFT' THEN
    RAISE EXCEPTION 'prescription % is %, so its items cannot be changed', parent.id, parent.status
      USING HINT = 'Only a draft can be edited. A change to anything else is a correction that supersedes it.';
  END IF;

  IF TG_OP = 'UPDATE' THEN
    IF NEW.id <> OLD.id OR NEW.prescription_id <> OLD.prescription_id
       OR NEW.facility_id <> OLD.facility_id
       OR NEW.recorded_at <> OLD.recorded_at OR NEW.recorded_by <> OLD.recorded_by
       -- CP82. Where a line came from is written once.
       OR NEW.ai_suggestion_id IS DISTINCT FROM OLD.ai_suggestion_id THEN
      RAISE EXCEPTION 'prescription item %: identity, authorship and origin are written once', OLD.id;
    END IF;
    IF NEW.price_poisha IS DISTINCT FROM OLD.price_poisha
       OR NEW.price_id IS DISTINCT FROM OLD.price_id
       OR NEW.price_effective_from IS DISTINCT FROM OLD.price_effective_from
       OR NEW.price_captured_at IS DISTINCT FROM OLD.price_captured_at THEN
      RAISE EXCEPTION 'prescription item %: the price at the time of prescribing is never back-updated', OLD.id
        USING HINT = 'CP75 supersedes a price rather than editing it; CP80 captures the amount by value so history cannot move.';
    END IF;
  END IF;

  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The projection learns to carry the origin across a rebuild.
--
-- Replaced rather than extended for the same reason as the freeze function: the whole INSERT has
-- to be readable in one place. The only change is `ai_suggestion_id`, read out of the event
-- payload — which is what makes the guarantee survive `make rebuild`: the origin is in the ledger,
-- not only in the projection.
CREATE OR REPLACE FUNCTION read.apply_prescription_item_added(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.prescription_item (
    id, prescription_id, facility_id, line_no,
    product_id, product_label, generic_name, strength, form_code,
    dose, daily_dose, dose_unit, frequency, duration_days, route, quantity,
    instructions_en, instructions_bn,
    price_poisha, price_id, price_effective_from, price_verification, price_captured_at,
    carried_forward_from_item, ai_suggestion_id,
    recorded_at, recorded_by, event_id, global_seq)
  VALUES (
    (p->>'item_id')::uuid,
    (p->>'prescription_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'line_no')::int,
    nullif(p->>'product_id', '')::uuid,
    p->>'product_label',
    coalesce(p->>'generic_name', ''),
    coalesce(p->>'strength', ''),
    coalesce(p->>'form_code', ''),
    p->>'dose',
    nullif(p->>'daily_dose', '')::numeric,
    coalesce(p->>'dose_unit', ''),
    p->>'frequency',
    nullif(p->>'duration_days', '')::int,
    coalesce(p->>'route', ''),
    nullif(p->>'quantity', '')::numeric,
    coalesce(p->>'instructions_en', ''),
    coalesce(p->>'instructions_bn', ''),
    -- The price arrives as a number in the payload. Nothing here reads core.medication_price,
    -- which is what makes a rebuild reproduce the price of the day rather than today's.
    nullif(p->>'price_poisha', '')::bigint,
    nullif(p->>'price_id', '')::uuid,
    nullif(p->>'price_effective_from', '')::date,
    nullif(p->>'price_verification', ''),
    nullif(p->>'price_captured_at', '')::timestamptz,
    nullif(p->>'carried_forward_from_item', '')::uuid,
    -- CP82. The only line that differs from 00062's version of this function.
    nullif(p->>'ai_suggestion_id', '')::uuid,
    (p->>'recorded_at')::timestamptz,
    (p->>'recorded_by')::uuid,
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (id) DO NOTHING;

  UPDATE read.prescription
     SET last_event_id = (p->>'event_id')::uuid,
         last_global_seq = greatest(last_global_seq, (p->>'global_seq')::bigint),
         updated_at = (p->>'recorded_at')::timestamptz
   WHERE id = (p->>'prescription_id')::uuid;
END
$$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- 7. Invariant 130
-- ---------------------------------------------------------------------------

-- **What this asserts that the trigger does not.**
--
-- The constraint trigger refuses a bad *commit*. It cannot speak for a row inserted while triggers
-- were disabled, for a decision whose line was never written, or for the state of the table right
-- now. This does: it is a scan over what is stored, in both directions, and it is the check a
-- verifier runs against a restored backup.
--
-- Both directions, because they fail differently. A line with no decision is the safety boundary
-- broken. A decision naming a line that does not exist is the trail broken — a physician's
-- recorded acceptance pointing at nothing, which is the shape of a decision somebody deleted.
--
-- The third check is the one that would be easy to leave out and is the reason §1 is written the
-- way it is: a **signed** prescription is the state the criterion names, so it is asserted
-- separately and with its own sentence, rather than being folded into "any prescription".

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_ai_lines_were_decided() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
  n integer;
BEGIN
  SELECT count(*), string_agg(i.id::text, ', ' ORDER BY i.id) INTO n, offending
    FROM read.prescription_item i
   WHERE i.ai_suggestion_id IS NOT NULL
     AND NOT EXISTS (
       SELECT 1 FROM core.ai_prescribing_decision d
        WHERE d.suggestion_id = i.ai_suggestion_id
          AND d.prescription_item_id = i.id
          AND d.decision IN ('ACCEPTED', 'EDITED'));
  IF n > 0 THEN
    RAISE EXCEPTION '% prescription line(s) came from an AI suggestion with no physician decision behind them: %',
      n, left(offending, 400)
      USING HINT =
        'CP82 §1: nothing copies a suggestion into a prescription except an explicit physician '
        'action that records who did it and when. A line here means such a path exists. Find it '
        'before anything is signed.';
  END IF;

  SELECT count(*), string_agg(d.id::text, ', ' ORDER BY d.id) INTO n, offending
    FROM core.ai_prescribing_decision d
   WHERE d.prescription_item_id IS NOT NULL
     AND NOT EXISTS (
       SELECT 1 FROM read.prescription_item i
        WHERE i.id = d.prescription_item_id
          AND i.ai_suggestion_id = d.suggestion_id);
  IF n > 0 THEN
    RAISE EXCEPTION '% decision(s) name a prescription line that does not exist or does not name them back: %',
      n, left(offending, 400)
      USING HINT =
        'An accepted suggestion whose line has vanished is a trail with a hole in it. The line is '
        'removed, never deleted, so this state should be unreachable; if the projection was '
        'rebuilt from a ledger that predates CP82, rebuild it from the current one.';
  END IF;

  -- The criterion, named as itself. Redundant with the first check today and worth its own
  -- sentence: this is the one a reviewer will look for, and finding it folded into a more general
  -- check is how a reviewer concludes it was not written.
  SELECT count(*) INTO n
    FROM read.prescription_item i
    JOIN read.prescription p ON p.id = i.prescription_id
   WHERE i.ai_suggestion_id IS NOT NULL
     AND p.status IN ('SIGNED', 'PRINTED', 'DISPENSED')
     AND NOT EXISTS (
       SELECT 1 FROM core.ai_prescribing_decision d
        WHERE d.suggestion_id = i.ai_suggestion_id
          AND d.prescription_item_id = i.id
          AND d.decision IN ('ACCEPTED', 'EDITED'));
  IF n > 0 THEN
    RAISE EXCEPTION
      '% AI-suggested line(s) are on a signed, printed or dispensed prescription with no accept or edit behind them', n
      USING HINT = 'This is CP82''s acceptance criterion 1 failing. Nothing else in this system '
                   'matters more than that it does not.';
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION core.assert_ai_lines_were_decided() FROM PUBLIC;

-- ---------------------------------------------------------------------------
-- Invariant 131: the register of molecules the AI may not propose is not empty
-- ---------------------------------------------------------------------------

-- **What this exists to catch, and it has already happened once.**
--
-- The first version of this register was keyed on `core.generic.id` and seeded with
-- `INSERT ... SELECT ... WHERE lower(name) LIKE 'testosterone%'`. There is no testosterone generic
-- in CP75's seed, so the statement matched nothing, inserted nothing, and **succeeded**. The
-- register looked like protection and protected one molecule.
--
-- Re-keying on the molecule removes the mechanism — the seed is now `INSERT ... VALUES` and cannot
-- match zero rows — and this is the check that keeps asking afterwards, which a `DO` block raising
-- at apply time would not. A migration verifies once; an invariant verifies on every deploy, on a
-- restored backup, and during an incident.
--
-- Three things:
--
--   1. **The register is not empty.** A safety register with no rows is the state that looks
--      exactly like a safe one from every screen in the system.
--   2. **The molecules the seed intended are present, by name.** This is the direct answer to the
--      defect: the migration's intent is asserted against the table's contents rather than
--      inferred from the statement having run. A row deleted, renamed or never inserted fails
--      here.
--   3. **Every row is stored in a form that can match.** A molecule with a leading space, a
--      trailing space or a double space is a row that silently never matches anything —
--      indistinguishable, from a screen, from a row that does. The CHECK constraint stops one
--      arriving; this catches a table that predates the constraint or was written around it.
--
-- What it deliberately does **not** assert is completeness. Nobody can enumerate every scheduled
-- molecule, and a check that pretended to would be the same lie one level up. What stands behind
-- the incompleteness is that a drug the AI proposes still has to be accepted by a person, one line
-- at a time.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_controlled_molecules_are_registered() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  -- The molecules migration 00069 seeds. Named here as well as there on purpose: this list is the
  -- assertion, and reading it from the same statement that wrote it would be asserting that a
  -- literal equals itself.
  seeded text[] := ARRAY['Pregabalin', 'Testosterone'];
  n         integer;
  offending text;
BEGIN
  SELECT count(*) INTO n FROM core.controlled_molecule;
  IF n = 0 THEN
    RAISE EXCEPTION 'the register of molecules the AI may not propose is empty'
      USING HINT =
        'CP82 §2 forbids the prescribing agent proposing a controlled drug, and this table is '
        'what that refusal is enforced against. An empty register refuses nothing and looks '
        'identical, from every screen, to one that is working.';
  END IF;

  SELECT string_agg(want.molecule, ', ' ORDER BY want.molecule) INTO offending
    FROM unnest(seeded) AS want(molecule)
   WHERE NOT EXISTS (
     SELECT 1 FROM core.controlled_molecule m
      WHERE lower(m.molecule) = lower(want.molecule));

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'these molecules are not on the do-not-propose register: %', offending
      USING HINT =
        'Migration 00069 seeds them by name. If one is missing, either the seed did not run or '
        'somebody removed it — and the AI can propose it right now. Restore the row, or write '
        'down here why it no longer belongs.';
  END IF;

  SELECT string_agg(format('%L', m.molecule), ', ' ORDER BY m.molecule) INTO offending
    FROM core.controlled_molecule m
   WHERE btrim(m.molecule) <> m.molecule OR m.molecule ~ '  ' OR m.molecule = '';

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'these registered molecules are stored in a form that will never match: %', offending
      USING HINT =
        'core.generic_is_controlled compares words. A leading, trailing or doubled space makes a '
        'row that matches nothing and looks on every screen like a row that matches.';
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION core.assert_controlled_molecules_are_registered() FROM PUBLIC;

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_controlled_molecules_are_registered',
   'the register of molecules the AI may not propose is not empty, contains the molecules migration 00069 seeds, and holds none whose spelling could never match',
   131)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_ai_lines_were_decided',
   'every prescription line marked as coming from an AI suggestion has an ACCEPTED or EDITED decision naming it, every decision that produced a line has that line, and no signed prescription carries an AI line without one',
   130)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- ---------------------------------------------------------------------------
-- 8. The agent
-- ---------------------------------------------------------------------------

-- §7.2's prescribing agent, registered the way `clinical.synthesis` was in 00053. GENERATIVE,
-- because it puts a language model's proposal in front of a prescriber — which is also why it is
-- the agent in this system with the least latitude: it chooses from a shortlist this server built
-- and cannot name a medicine that is not on it.
--
-- **Grounding is required**, and deliberately not exempted. It is the agent where an invented
-- number is a dose. See internal/prescription/aisuggest.go for the consequence that has: the
-- gateway refuses the whole answer when any claim is ungrounded, so one bad line costs the run
-- rather than costing that line. That is a coarser instrument than §2's "dropped, not repaired"
-- asks for, and it errs in the direction §2 would choose.
INSERT INTO core.ai_agent (agent_code, technology, description_en, description_bn) VALUES
  ('clinical.prescribing', 'GENERATIVE',
   'The prescribing agent: proposes medicines from this clinic''s formulary for the physician to accept, edit or reject one at a time',
   'ব্যবস্থাপত্র এজেন্ট: ক্লিনিকের নিজস্ব ওষুধতালিকা থেকে ওষুধ প্রস্তাব করে, যেগুলো চিকিৎসক একটি একটি করে গ্রহণ, সংশোধন বা বাতিল করেন')
ON CONFLICT (agent_code) DO UPDATE SET
  technology = EXCLUDED.technology,
  description_en = EXCLUDED.description_en, description_bn = EXCLUDED.description_bn;

-- The daily budget, on the same footing as the synthesis agent's. A prescribing suggestion is
-- asked for once per consultation at most, so the budget is small on purpose: a deployment that
-- crosses it is one where something is asking in a loop, and that is worth an alert.
INSERT INTO core.ai_budget (facility_id, agent_code, daily_micro_usd, thresholds)
SELECT f.id, 'clinical.prescribing', 500000, ARRAY[60, 80, 100]
  FROM core.facility f
-- No conflict target: `ai_budget_per_agent` is a partial unique index, so naming the columns
-- would also have to name its predicate, and a repeated predicate is a second copy of the rule.
ON CONFLICT DO NOTHING;

-- +goose StatementBegin
-- The third `INSERT ... SELECT` in this migration, and the only one left.
--
-- The other two were the controlled register, and they are gone: a molecule is registered by name
-- and cannot be made conditional on a row existing. This one is a genuine per-facility fan-out —
-- a budget *is* a property of a facility and there must be exactly one row per facility — so the
-- shape is right. What is not right is that it would succeed having inserted nothing, and a
-- missing budget row is silent in the worst way: `ai.Gateway.meter` finds no budget for the agent,
-- crosses no threshold, raises no alert, and spends unmetered while every screen looks normal.
--
-- So it raises. A `DO` block rather than an invariant because what is being checked is *this
-- statement having done something*, at the moment it ran.
--
-- **The wider hole is not fixed here and is worth writing down.** A facility created *after* this
-- migration gets no `clinical.prescribing` budget, and none for `clinical.synthesis` either —
-- 00053 seeds that one the same way. That is one defect in CP70's shape rather than two in CP82's,
-- and the fix is an invariant asserting every facility has a budget for every generative agent.
-- D-61 says there is one facility today, which is why it has not bitten.
DO $$
DECLARE
  budgets integer;
BEGIN
  SELECT count(*) INTO budgets
    FROM core.ai_budget WHERE agent_code = 'clinical.prescribing';
  IF budgets = 0 THEN
    RAISE EXCEPTION 'no facility received a daily budget for the clinical.prescribing agent'
      USING HINT =
        'The seed above is INSERT ... SELECT FROM core.facility and matched no row. A missing '
        'budget is not a smaller budget: the gateway meters against nothing, crosses no '
        'threshold and raises no alert, and the deployment looks normal while it does it.';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down

DELETE FROM ops.invariant WHERE function_name = 'assert_ai_lines_were_decided';
DROP FUNCTION IF EXISTS core.assert_ai_lines_were_decided();

DROP TRIGGER IF EXISTS prescription_item_ai_origin_is_decided ON read.prescription_item;
DROP FUNCTION IF EXISTS core.ai_origin_item_has_a_decision();
DROP INDEX IF EXISTS read.prescription_item_from_ai;
ALTER TABLE read.prescription_item DROP COLUMN IF EXISTS ai_suggestion_id;

DROP TRIGGER IF EXISTS ai_prescribing_decision_final ON core.ai_prescribing_decision;
DROP FUNCTION IF EXISTS core.ai_prescribing_decision_is_final();
DROP TABLE IF EXISTS core.ai_prescribing_decision;

DROP TRIGGER IF EXISTS ai_prescribing_suggestion_as_offered ON core.ai_prescribing_suggestion;
DROP FUNCTION IF EXISTS core.ai_prescribing_suggestion_is_as_offered();
DROP TABLE IF EXISTS core.ai_prescribing_suggestion;

DROP TABLE IF EXISTS core.ai_prescribing_run;

DELETE FROM core.ai_budget WHERE agent_code = 'clinical.prescribing';
DELETE FROM core.ai_agent WHERE agent_code = 'clinical.prescribing';

DELETE FROM ops.invariant WHERE function_name = 'assert_controlled_molecules_are_registered';
DROP FUNCTION IF EXISTS core.assert_controlled_molecules_are_registered();

DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name IN ('controlled_molecule', 'ai_suggestion_reject_reason');
DROP FUNCTION IF EXISTS core.generic_is_controlled(uuid);
DROP TABLE IF EXISTS core.controlled_molecule;
DROP TABLE IF EXISTS core.ai_suggestion_reject_reason;
