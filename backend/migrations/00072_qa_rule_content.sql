-- The clinical content rules 12 and 14 were waiting for (CP83, docs/qa-rules.md Appendix).
--
-- # What this file is and is not
--
-- Migration 00071 built station 10 and reported two of its eighteen rules firing for nothing:
-- rule 12 named a counselling item no template defined, and rule 14 read a teratogenic flag no
-- molecule carried. Neither was an engine defect. Both were content, and the content is now
-- written — `docs/qa-rules.md` A1 and A2. This file is that content and nothing else: no new
-- shape, no new predicate, no change to the gate.
--
-- Invariant 137 reported both states on every `migrate verify` before this file, and reports
-- neither after it. That is the proof, and it is why the invariant reports rather than raises:
-- "inert for want of content somebody owes" is an honest state that must be visible and must not
-- stop a deploy.
--
-- # The part of A2 this clinic cannot yet honour, said out loud
--
-- Three of A2's ten rows name medicines the formulary does not hold. **They are seeded anyway**,
-- because a flag is a statement about a molecule rather than about stock — but the molecules had
-- to be added to the dictionary first, because invariant 119 refuses a rule keyed to a molecule
-- this clinic does not have, and it is right to: *"a rule keyed to a molecule nobody can
-- prescribe never fires, and a physician approving it believes it protects somebody."*
--
-- So spironolactone and testosterone join `core.generic`, and `BISPHOSPHONATE` joins
-- `core.medication_class`, each **with no product**. That is CP92's precedent one level down
-- (`SELF_MONITORING_GLUCOSE` is a class migration 00070 added with nothing in it), and the state
-- is reported by invariant 137 rather than left to be discovered: the flag is live, it reaches no
-- stocked product, and it starts working the day the pharmacist adds one — with no code change.
--
-- Spironolactone is the row that matters most there. A2: *"This clinic prescribes it for PCOS, to
-- exactly the women rule 14 exists for."* A silent nothing on that row would have been the
-- checkpoint's worst outcome.
--
-- # The resubmission defect, restated because a reader will meet it here first
--
-- Migration 00071 fixed a CP80 defect that only CP83 could reach: `submitted_at` was a write-once
-- stamp and `read.apply_prescription_transition` rewrites it on every entry to QA_REVIEW, so a
-- bounced prescription could never be resubmitted, signed or printed. CP80 built the
-- `QA_REVIEW -> DRAFT` edge and left the workflow to this checkpoint; its own tests drove
-- DRAFT -> QA_REVIEW -> SIGNED and never went round the loop, so nothing in the system could
-- reach the defect until a bounce existed to send a prescription back. See 00071 section 6b.

-- +goose Up

-- ---------------------------------------------------------------------------
-- A1. The agranulocytosis counselling item (unblocks rule 12)
-- ---------------------------------------------------------------------------

-- # Why a new template rather than an item on the diabetes one
--
-- `core.counseling_template_version` freezes a published version — CP55's whole second criterion
-- — so an item cannot be added to DIABETES v1, and a v2 would re-ask every diabetic in the clinic
-- a thyroid question. A thyroid checklist is its own thing.
--
-- # Why the code is ANTITHYROID and not THYROID
--
-- Because the warning is about the *drug*, not the gland. A patient on levothyroxine has a thyroid
-- condition and no reason to be told about agranulocytosis, and a checklist called THYROID invites
-- exactly that item creeping onto it. `THYROID` is also the code CP55's own authoring test uses
-- when it proves a physician can write a template, and taking a name another checkpoint's test
-- reaches for is a collision somebody has to untangle later.
--
-- # Why the item is not mandatory, which looks wrong and is not
--
-- CP57's gate counts **mandatory** items and holds the patient at the consultation. The
-- counselling rooms are station 3; the consultation is station 9. So at the moment CP57's gate
-- fires, **the carbimazole has not been prescribed yet** — nobody has decided anything. A
-- mandatory item here would hold every thyrotoxic patient in the building before the drug that
-- makes the warning necessary had been written.
--
-- What enforces it instead is QA rule 12, at station 10, where the prescription exists and the
-- rule can see the antithyroid drug on it. A bounce sends the patient back to counselling with
-- the reason. That is the sequence the spec asks for — *"the prescription does not leave without
-- it"* — and it is the reason `counseling.Store.CoveredItems` exists rather than QA inverting
-- CP57's `Missing`: a rule built on the inverse of the gate's answer would silently pass every
-- non-mandatory item, which is this one.
INSERT INTO core.counseling_template (id, code, title_en, title_bn) VALUES
  ('0190c000-0000-7000-8000-000000000002', 'ANTITHYROID',
   'Antithyroid medicine counseling', 'অ্যান্টিথাইরয়েড ওষুধের পরামর্শ')
ON CONFLICT (id) DO NOTHING;

-- Drafted first and published at the end of this section, because CP55's freeze trigger refuses
-- an item on a published version — including one published four statements ago. That refusal is
-- right and is the whole of CP55's second criterion: a published checklist is on every phone on
-- the floor and must not change under the counsellor reading it.
INSERT INTO core.counseling_template_version
  (template_id, version, status, published_source, notes) VALUES
  ('0190c000-0000-7000-8000-000000000002', 1, 'DRAFT', 'MIGRATION',
   'Transcribed verbatim from docs/qa-rules.md A1. Physician content, awaiting Dr Nahid''s sign-off; the wording is not a developer''s.')
ON CONFLICT (template_id, version) DO NOTHING;

-- The item, word for word from A1.
--
-- Three properties of the English are load-bearing and the appendix says so. **"Stop the medicine"
-- comes before "get a blood count"**, because a patient who waits for the test while still taking
-- the drug is the patient who dies of this. **"Do not wait to see if it settles"** exists because
-- every instinct says a sore throat is flu. And it **names the three symptoms** — sore throat,
-- mouth ulcers, fever — rather than saying "signs of infection", which is a phrase that means
-- nothing to somebody who is not a clinician.
--
-- Nothing here is paraphrased in either language. The one character that differs from the
-- appendix is the Bangla full stop: A1 ends the sentence with a Latin `.` and this uses the
-- Bengali danda `।`, which is what every other Bangla string in this database uses and what a
-- Bengali reader expects at the end of a sentence.
--
-- Guarded on the version still being a draft, which matters only on the path nobody takes: this
-- file rolled back and re-applied. The down migration cannot remove the item — see its own note —
-- so on a second `up` the version is already PUBLISHED, the guard yields no row, and the freeze
-- trigger is never asked to refuse something that is already true.
INSERT INTO core.counseling_item
  (template_id, version, item_code, ordering, text_en, text_bn, guidance_en, guidance_bn,
   is_mandatory, room)
SELECT '0190c000-0000-7000-8000-000000000002', 1, 'AGRANULOCYTOSIS_WARNING', 1,
   'This medicine can rarely stop your body making the white cells that fight infection. If you get a sore throat, mouth ulcers or fever while taking it, stop the medicine and get a blood count the same day. Do not wait to see if it settles. Bring the result to us, or call the clinic.',
   'এই ওষুধে খুব কম ক্ষেত্রে শরীরে জীবাণুর বিরুদ্ধে লড়াই করা শ্বেত রক্তকণিকা তৈরি বন্ধ হয়ে যেতে পারে। ওষুধ খাওয়ার সময় গলাব্যথা, মুখে ঘা বা জ্বর হলে ওষুধ বন্ধ করে সেদিনই রক্তের সিবিসি পরীক্ষা করান। সেরে যায় কি না দেখার জন্য অপেক্ষা করবেন না। রিপোর্ট আমাদের দেখান বা ক্লিনিকে ফোন করুন।',
   'Say it in this order. A patient who keeps taking the drug while waiting for the test is the one this warning is written for.',
   'এই ক্রমেই বলুন। পরীক্ষার ফলের অপেক্ষায় থেকে যিনি ওষুধ চালিয়ে যান, এই সতর্কবার্তা তাঁর জন্যই লেখা।',
   false, 'COUNSELING_ROOM'
 WHERE EXISTS (SELECT 1 FROM core.counseling_template_version
                WHERE template_id = '0190c000-0000-7000-8000-000000000002'
                  AND version = 1 AND status = 'DRAFT')
ON CONFLICT (template_id, version, item_code) DO NOTHING;

-- Published now that the item is on it.
UPDATE core.counseling_template_version
   SET status = 'PUBLISHED', published_at = now()
 WHERE template_id = '0190c000-0000-7000-8000-000000000002' AND version = 1
   AND status = 'DRAFT';

-- Thyrotoxicosis. E05 is where an antithyroid drug is prescribed from, so it is the code that
-- brings the checklist up on the phone before the patient reaches the consultation.
--
-- **The assignment is a convenience, not the enforcement.** `counseling.SessionService.Start`
-- takes any template with a published version, so a counsellor can open this checklist for a
-- patient whose visit was coded something else — which is what makes rule 12's bounce a
-- remediation rather than a dead end. That mattered enough to check: a QA rule whose only exit is
-- the override is a valve becoming the process.
INSERT INTO core.counseling_assignment (template_id, code_system, code_version, code_prefix, priority)
VALUES ('0190c000-0000-7000-8000-000000000002', 'ICD10', '2019', 'E05', 100)
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- A2. The teratogenic flags (unblocks rule 14)
-- ---------------------------------------------------------------------------

-- # What the flag decides, and what it does not
--
-- A2 is explicit: *"The flag decides one thing: whether the pregnancy question gets asked. It
-- does not refuse the drug, and it must not be read as a contraindication list — several of these
-- are the right drug for the right woman, and the point is that somebody asked first."*
--
-- That sentence is why the flag lives on `core.medication_rule` and is read **independently of
-- whether the rule has been approved for checking**. CP77's seeded rules are all DRAFT: D-22
-- makes Dr Nahid the author of every rule and nothing checks a prescription until he approves it.
-- Approving a rule to *stop a prescription* and flagging a molecule so a *question is asked* are
-- two different acts with two different costs, and gating the second on the first would have kept
-- rule 14 inert for a reason that has nothing to do with rule 14.
--
-- So `medsafety.Store.TeratogenicGenerics` reads the flag and the rule's newest non-withdrawn
-- version, and CP78's own checking is untouched: these rules still do nothing to a prescription
-- until somebody approves them.

-- --- the six A2 rows CP77 already carries a rule for -------------------------
--
-- Each of these is an existing PREGNANCY rule whose subject is already exactly what A2 names.
-- Flagging them is one UPDATE rather than six new rules, which matters: a second rule about
-- carbimazole in pregnancy would be a second thing to approve, a second thing to withdraw, and
-- two answers to "what does this clinic say about carbimazole".
--
-- `ARB-PREG` already names `ARB_CCB_FDC` and `ARB_THIAZIDE_FDC` beside the plain class, which is
-- the half of "ARBs (class)" that is easy to miss: a woman on telmisartan + amlodipine is on an
-- ARB, and a flag that reached only the single-molecule class would not ask her.
UPDATE core.medication_rule SET flags_teratogenic = true
 WHERE rule_type = 'PREGNANCY'
   AND code IN ('CBZ-PREG',        -- carbimazole and methimazole
                'PTU-PREG',        -- propylthiouracil
                'ACE-PREG',        -- ACE inhibitors
                'ARB-PREG',        -- ARBs, including the two fixed-dose combinations
                'STATIN-PREG',     -- statins
                'GLP1-PREG-PLAN'); -- GLP-1 receptor agonists

-- **`NONINSULIN-PREG` is deliberately not flagged**, and this is the one judgement in this file
-- that a reader is most likely to reverse by accident.
--
-- It is a basket: DPP-4 inhibitors, sulphonylureas, thiazolidinediones and their combinations
-- alongside the SGLT2 inhibitors A2 does name. It is a good *safety* rule — none of those belongs
-- in pregnancy — and a bad *flag*, because flagging it would ask the pregnancy question of every
-- woman on gliclazide, which A2 does not ask for and which is how a question people are asked
-- eleven times a day stops being answered. The SGLT2 half gets its own flagged rule below.

-- --- the four A2 rows CP77 has no rule for ----------------------------------

-- Three classes the formulary does not have. Added as vocabulary with no products, which is
-- exactly what migration 00070 did with `SELF_MONITORING_GLUCOSE`: the class is real, the clinic
-- does not stock it yet, and the honest state is visible from the table rather than absent.
INSERT INTO core.medication_class (code, name_en, name_bn, ordering) VALUES
  ('ALDOSTERONE_ANTAGONIST', 'Aldosterone antagonist', 'অ্যালডোস্টেরন প্রতিরোধক', 350),
  ('ANDROGEN', 'Androgen', 'অ্যান্ড্রোজেন', 360),
  ('BISPHOSPHONATE', 'Bisphosphonate', 'বিসফসফোনেট', 370)
ON CONFLICT (code) DO UPDATE SET
  name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn, ordering = EXCLUDED.ordering;

-- Two molecules the dictionary did not hold.
--
-- **Molecules, not products.** `core.generic` is what a medicine *is*; `core.medication_product`
-- is what the pharmacy stocks and what it costs, and inventing either would put a price on a
-- printed sheet that nobody chose. Six generics in this database already have no product, so this
-- is an ordinary state rather than a new one.
--
-- Invariant 119 is why they are here at all: it refuses a rule keyed to a molecule the formulary
-- does not hold, because such a rule never fires and nothing says so. Adding the molecule is the
-- minimum that makes A2's row a real statement; invariant 137 then reports that it reaches no
-- stocked product, so the gap is named rather than silent.
INSERT INTO core.generic (name, class_code, atc_code, notes) VALUES
  ('Spironolactone', 'ALDOSTERONE_ANTAGONIST', 'C03DA01',
   'Added by migration 00072 for docs/qa-rules.md A2. No product is stocked; this clinic prescribes it for PCOS, which is exactly the population QA rule 14 exists for.'),
  ('Testosterone', 'ANDROGEN', 'G03BA03',
   'Added by migration 00072 for docs/qa-rules.md A2. No product is stocked. Already on CP82''s do-not-propose register for its own reasons.')
ON CONFLICT (lower(name)) DO NOTHING;

-- SGLT2 inhibitors, on their own, so the flag reaches them without reaching the rest of
-- `NONINSULIN-PREG`'s basket. Both classes, because empagliflozin + metformin is still
-- empagliflozin.
SELECT core.seed_medication_rule(f.id, 'SGLT2-PREG-FLAG', 'PREGNANCY', 'WARN',
  'SGLT2 inhibitor and pregnancy', 'এসজিএলটি২ ইনহিবিটর ও গর্ভাবস্থা',
  'An SGLT2 inhibitor is contraindicated in pregnancy and is to be discontinued. These are not classical teratogens; the reason to ask is that the drug has to stop.',
  'গর্ভাবস্থায় এসজিএলটি২ ইনহিবিটর দেওয়া যায় না, বন্ধ করতে হয়। এগুলি প্রচলিত অর্থে ভ্রূণ-ক্ষতিকর নয়; জিজ্ঞেস করার কারণ হলো ওষুধটি বন্ধ করতেই হবে।',
  'Ask about pregnancy before continuing. Insulin is the agent of pregnancy.',
  'চালিয়ে যাওয়ার আগে গর্ভাবস্থার কথা জিজ্ঞেস করুন। গর্ভাবস্থায় ইনসুলিনই ব্যবহার্য।',
  'docs/qa-rules.md A2; BNF 88, SGLT2 inhibitors in pregnancy.',
  '{"subject":{"match":"CLASS","classes":["SGLT2_INHIBITOR","SGLT2_INHIBITOR_BIGUANIDE_FDC"]},"when":[{"kind":"PREGNANCY","states":["PREGNANT","PLANNING"]}]}'::jsonb)
  FROM core.facility f;

-- Spironolactone. A2's own emphasis: this clinic prescribes it for PCOS, to exactly the women
-- rule 14 exists for.
SELECT core.seed_medication_rule(f.id, 'SPIRO-PREG', 'PREGNANCY', 'BLOCK',
  'Spironolactone in pregnancy', 'গর্ভাবস্থায় স্পাইরোনোল্যাকটোন',
  'Spironolactone is an anti-androgen and can feminise a male fetus. It is prescribed here for PCOS, to women of exactly the age this question is asked of.',
  'স্পাইরোনোল্যাকটোন একটি অ্যান্টি-অ্যান্ড্রোজেন এবং পুরুষ ভ্রূণের স্ত্রী-বৈশিষ্ট্য আনতে পারে। এখানে এটি পিসিওএস-এর জন্য দেওয়া হয় — ঠিক যে বয়সের নারীদের এই প্রশ্নটি করা হয়, তাঁদেরই।',
  'Ask about pregnancy before prescribing, and make sure she knows to stop it if she conceives.',
  'দেওয়ার আগে গর্ভাবস্থার কথা জিজ্ঞেস করুন, এবং গর্ভধারণ করলে ওষুধটি বন্ধ করতে হবে তা তাঁকে জানিয়ে দিন।',
  'docs/qa-rules.md A2; BNF 88, spironolactone in pregnancy.',
  '{"subject":{"match":"GENERIC","generics":["spironolactone"]},"when":[{"kind":"PREGNANCY","states":["PREGNANT","PLANNING"]}]}'::jsonb)
  FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'TESTO-PREG', 'PREGNANCY', 'BLOCK',
  'Testosterone in pregnancy', 'গর্ভাবস্থায় টেস্টোস্টেরন',
  'Testosterone virilises a female fetus.',
  'টেস্টোস্টেরন স্ত্রী ভ্রূণে পুরুষ-বৈশিষ্ট্য আনে।',
  'Ask about pregnancy before prescribing.',
  'দেওয়ার আগে গর্ভাবস্থার কথা জিজ্ঞেস করুন।',
  'docs/qa-rules.md A2; BNF 88, testosterone contraindications.',
  '{"subject":{"match":"GENERIC","generics":["testosterone"]},"when":[{"kind":"PREGNANCY","states":["PREGNANT","PLANNING"]}]}'::jsonb)
  FROM core.facility f;

-- Bisphosphonates. A2's reason is the one that makes this row different from the others: the
-- skeletal half-life outlasts the prescription, so the question is about a pregnancy that has not
-- happened yet.
SELECT core.seed_medication_rule(f.id, 'BISPHOS-PREG', 'PREGNANCY', 'WARN',
  'Bisphosphonate and pregnancy', 'বিসফসফোনেট ও গর্ভাবস্থা',
  'A bisphosphonate stays in bone for years, so the exposure outlasts the prescription. The question is about a pregnancy that may be some way off.',
  'বিসফসফোনেট বছরের পর বছর হাড়ে থেকে যায়, তাই ওষুধ বন্ধ করার পরেও তার প্রভাব থাকে। প্রশ্নটি এমন গর্ভাবস্থা নিয়ে যা এখনও অনেক দূরে হতে পারে।',
  'Ask about pregnancy and about plans, not only about now.',
  'শুধু এখনকার কথা নয়, ভবিষ্যতের পরিকল্পনার কথাও জিজ্ঞেস করুন।',
  'docs/qa-rules.md A2; BNF 88, bisphosphonates in pregnancy.',
  '{"subject":{"match":"CLASS","classes":["BISPHOSPHONATE"]},"when":[{"kind":"PREGNANCY","states":["PREGNANT","PLANNING"]}]}'::jsonb)
  FROM core.facility f;

UPDATE core.medication_rule SET flags_teratogenic = true
 WHERE rule_type = 'PREGNANCY'
   AND code IN ('SGLT2-PREG-FLAG', 'SPIRO-PREG', 'TESTO-PREG', 'BISPHOS-PREG');

-- --- what A2 deliberately does not flag, and why --------------------------
--
-- A2 states this as a list because *"a list like this tends to grow by anxiety"*. It is repeated
-- here because a later reader's instinct will be to add these four, and a comment is what stops
-- him.
--
--   * **Metformin** — used in pregnancy and not teratogenic. Flagging it would ask the question of
--     nearly every woman in this clinic, which is how a question stops being answered.
--   * **Insulin** — the safest option in pregnancy and the thing patients are switched *to*. A
--     flag on it would be a warning against the answer.
--   * **Levothyroxine** — essential, and the dose goes *up* in pregnancy. Flagging it would teach
--     the opposite of the right instinct, which is worse than teaching nothing.
--   * **Cabergoline** — stopped at conception in prolactinoma, but that is a management decision
--     rather than a teratogenic one, and this flag is not a stop list.
--
-- None of the four is flagged and none of them should become flagged without a line in
-- `docs/qa-rules.md` saying why the appendix changed its mind.

-- ---------------------------------------------------------------------------
-- Invariant 137, told what to report now
-- ---------------------------------------------------------------------------

-- Two changes, and the second is the new one.
--
-- The "rule 14 is inert" NOTICE stays exactly as it was: it is written as a condition rather than
-- as a statement about today, so it falls silent by itself now that a molecule carries the flag,
-- and it speaks again if every flag is ever withdrawn. A NOTICE somebody deleted because it had
-- stopped being true would be a NOTICE that never came back.
--
-- What is added is the state this migration actually creates: a flag that is live and reaches no
-- stocked product. Spironolactone, testosterone and the bisphosphonates are each a correct
-- clinical statement about a medicine the pharmacy does not hold, and the danger is not that it is
-- wrong — it is that it looks identical, from every screen, to a flag that is doing something.
-- Reported and not raised, for migration 00070's reason: a medicine the clinic does not stock yet
-- is an ordinary state and must not stop a deploy.
-- +goose StatementBegin
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

  -- Rule 14's own condition. True before migration 00072 and false after it; left in place so it
  -- speaks again if every teratogenic flag is ever withdrawn.
  IF NOT EXISTS (SELECT 1 FROM core.medication_rule WHERE flags_teratogenic AND is_active) THEN
    RAISE NOTICE 'QA rule 14 is inert: no molecule is flagged teratogenic on core.medication_rule, '
                 'so no prescription can trigger the pregnancy-status question. This is the '
                 'documented state (docs/qa-rules.md 3.4) and not a defect; it stops being true '
                 'the day Dr Nahid publishes a PREGNANCY rule with flags_teratogenic set.';
  END IF;

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

  -- The state migration 00072 creates: a teratogenic flag reaching no stocked product.
  --
  -- Invariant 119 has already guaranteed that every molecule and class named here *exists*. What
  -- it cannot say is whether the pharmacy holds one, and a flag on a molecule nobody can be
  -- prescribed asks nobody anything. It is still worth having — the day a product arrives, the
  -- question starts being asked with no code change — but it must not look like a flag that is
  -- working.
  SELECT string_agg(DISTINCT r.code, ', ' ORDER BY r.code) INTO offending
    FROM core.medication_rule r
    JOIN LATERAL (
      SELECT v.condition FROM core.medication_rule_version v
       WHERE v.rule_id = r.id AND v.status <> 'WITHDRAWN'
       ORDER BY v.version DESC LIMIT 1) AS live ON true
   WHERE r.flags_teratogenic AND r.is_active
     AND NOT EXISTS (
       SELECT 1
         FROM core.medication_product p
         JOIN core.generic g ON g.id = p.generic_id
        WHERE p.is_active
          AND (g.class_code IN (
                 SELECT jsonb_array_elements_text(
                          coalesce(live.condition -> 'subject' -> 'classes', '[]'::jsonb)))
               OR lower(g.name) IN (
                 SELECT lower(jsonb_array_elements_text(
                          coalesce(live.condition -> 'subject' -> 'generics', '[]'::jsonb))))));

  IF offending IS NOT NULL THEN
    RAISE NOTICE 'teratogenic flags that reach no stocked product: %. The molecule exists and the '
                 'flag is live; the pharmacy holds nothing that matches, so the pregnancy question '
                 'is asked of nobody until it does (docs/qa-rules.md A2).', offending;
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down

-- The invariant goes back to migration 00071's body, which is the same function without the
-- last clause. Restoring it here rather than leaving 00072's version behind, because a down
-- migration that leaves a check reporting a state its own schema no longer creates is worse than
-- no down migration.
-- +goose StatementBegin
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
    RAISE EXCEPTION 'these QA rules bounce to a station that is not active: %', offending;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM core.medication_rule WHERE flags_teratogenic AND is_active) THEN
    RAISE NOTICE 'QA rule 14 is inert: no molecule is flagged teratogenic on core.medication_rule.';
  END IF;
END
$$;
-- +goose StatementEnd

UPDATE core.medication_rule SET flags_teratogenic = false
 WHERE code IN ('CBZ-PREG', 'PTU-PREG', 'ACE-PREG', 'ARB-PREG', 'STATIN-PREG',
                'GLP1-PREG-PLAN', 'SGLT2-PREG-FLAG', 'SPIRO-PREG', 'TESTO-PREG', 'BISPHOS-PREG');

DELETE FROM core.medication_rule_version v
 USING core.medication_rule r
 WHERE v.rule_id = r.id
   AND r.code IN ('SGLT2-PREG-FLAG', 'SPIRO-PREG', 'TESTO-PREG', 'BISPHOS-PREG');
DELETE FROM core.medication_rule
 WHERE code IN ('SGLT2-PREG-FLAG', 'SPIRO-PREG', 'TESTO-PREG', 'BISPHOS-PREG');

DELETE FROM core.generic WHERE lower(name) IN ('spironolactone', 'testosterone');
DELETE FROM core.medication_class
 WHERE code IN ('ALDOSTERONE_ANTAGONIST', 'ANDROGEN', 'BISPHOSPHONATE');

-- **The counselling item is not removed, and cannot be.**
--
-- CP55 refuses every edit to a published version and refuses PUBLISHED -> DRAFT outright, for a
-- reason this migration has no business overruling: sessions reference the version, and a
-- checklist that could be withdrawn is a record of what a patient was asked that can be rewritten
-- afterwards. So the down path does what an author would do — it stops the checklist being
-- *offered*, by deleting the assignment that brings it up — and leaves the content in place.
--
-- The consequence, stated rather than hidden: rolling this migration back leaves the antithyroid
-- checklist authored and unassigned. It is reachable by template id and nothing selects it
-- automatically. That is the same state a physician reaches by retiring an assignment, and it is
-- the honest limit of rolling back content somebody may already have used.
DELETE FROM core.counseling_assignment
 WHERE template_id = '0190c000-0000-7000-8000-000000000002';
