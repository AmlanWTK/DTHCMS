-- Renal dosing: the recency window, and which drugs need a kidney function before they are
-- prescribed (CP79, §7.2, §6.4).
--
-- CP43 derives eGFR. CP77 wrote eight banded renal rules. CP78 built the engine that runs them.
-- What was missing between them is the two facts this migration adds, and neither is a rule:
--
--   1. **How old an eGFR may be and still be current renal function.** The plan's own words:
--      *an eGFR from two years ago is not current renal function.* Nothing in the system said
--      how old is too old, so `MET-RENAL-30` would have blocked metformin on a creatinine from
--      2023 with the same confidence as one from this morning.
--   2. **Which drugs cannot be prescribed safely without knowing it at all.** CP78 fails closed
--      when a *rule* meets an absent eGFR, which is the right behaviour and is not enough: a
--      drug with no renal rule written about it produced silence, and silence is what §7.2 says
--      must never happen.
--
-- # Why the window is a table and not a constant
--
-- Because it is a clinical judgement, it is Dr. Nahid's, and he has not made it yet. The plan
-- lists it as an open decision and proposes six months. Six months is what ships here, and it
-- ships as a **row he can change without a release**, per facility, with a citation and a place
-- for his name.
--
-- **Months, not days.** `egfr_recency_months`, and the expiry is `as_of + N months` on the
-- calendar rather than `as_of + 180 days`. This matters at the boundary and it is the kind of
-- thing that is wrong for two years before anybody notices: six calendar months before 12
-- September is 12 March, which is 184 days, so a 180-day window would call a result taken
-- exactly six months ago *stale* while a physician saying "within six months" means it is not.
-- The window is expressed in the unit the clinical sentence is written in.
--
-- # Why an unapproved window is applied anyway, unlike an unapproved rule
--
-- This is the one place in the medication-safety stack where CP77's "a seeded thing is inert
-- until the physician reads it" is deliberately not the behaviour, and the reason is that there
-- is no inert value for a window. A rule that does not fire simply does not fire. A staleness
-- window that does not apply means **every eGFR is treated as current**, which is the exact
-- failure the checkpoint exists to prevent — the seed would have made the system less safe by
-- being unapproved rather than more.
--
-- So the window applies, and `is_approved` travels with every renal status the API returns, and
-- the indicator says *provisional* in both languages until somebody's name is on it. The honest
-- position is "we are using six months and nobody has agreed to it", not "we are using nothing".
--
-- # Why `renal_dependence` and not `is_renally_cleared`
--
-- The plan says *fail closed when creatinine is absent for a renally-cleared drug*. Implemented
-- literally, that flag is wrong in both directions for drugs this clinic prescribes daily:
--
--   * **Empagliflozin is not renally cleared** — it is glucuronidated — and you still must not
--     start it without an eGFR, because below 25 it no longer lowers glucose and below 20 it is
--     not indicated. A clearance flag would have let it through with no kidney function on file.
--   * **Linagliptin is renally cleared by almost nobody's definition** (it is biliary) and that
--     is precisely why it is the answer every seeded renal rule offers as the alternative. A
--     flag keyed to clearance would have fired on it and taught the physician to ignore the
--     column.
--
-- So the column records the question the prescriber actually has to answer — *does prescribing
-- this drug depend on knowing the kidney function* — in two values, `EGFR_REQUIRED` and
-- `EGFR_NOT_REQUIRED`. **A molecule with no row is neither**, and the engine reports it as
-- unclassified rather than as safe. Three states, like coverage, for the same reason.
--
-- **This is a widening of the plan's scope and it is listed for Dr. Nahid.**
--
-- # Dialysis is out of scope, and that is a decision rather than an omission
--
-- Nothing in this migration, and nothing in `medsafety/renal.go`, models dialysis. A patient on
-- haemodialysis has an eGFR that is meaningless as a dosing input — the number describes
-- residual function, the dosing is driven by the dialysis schedule and the drug's dialysability,
-- and applying a band to it would produce confident wrong answers rather than no answer. The
-- plan lists dialysis scope as an open decision; until Dr. Nahid says otherwise there is no
-- dialysis field, no dialysis band, and no half-built dialysis path to mistake for one.
-- `docs/medication-rules.md` says so in as many words.
--
-- # Nothing here is patient data
--
-- Both tables are reference data: a facility's window and a molecule's property. No patient
-- identifier appears in this migration.

-- +goose Up

-- ---------------------------------------------------------------------------
-- 1. The recency window, per facility
-- ---------------------------------------------------------------------------

CREATE TABLE core.facility_renal_policy (
  facility_id uuid PRIMARY KEY REFERENCES core.facility(id),

  -- How old an eGFR may be and still be treated as current renal function.
  --
  -- Bounded rather than free. One month is the tightest window that is not simply a refusal to
  -- use any result; sixty months is five years, past which "recency window" has stopped meaning
  -- anything. The bound is here so that a typo in an admin form is refused by the database
  -- rather than silently disarming the staleness warning for a decade.
  egfr_recency_months int NOT NULL DEFAULT 6
    CHECK (egfr_recency_months BETWEEN 1 AND 60),

  -- Where the number came from. Required, like a rule's citation: a clinical threshold with no
  -- source is a number somebody typed.
  source_citation text NOT NULL,

  origin text NOT NULL DEFAULT 'SEED' CHECK (origin IN ('SEED', 'AUTHORED')),

  -- Null on the seeded row, and that is the whole point. The window applies either way (see the
  -- header); this is what makes the indicator say "provisional" until a physician agrees.
  approved_by uuid REFERENCES core.app_user(id),
  approved_at timestamptz,

  notes_en text NOT NULL DEFAULT '',
  notes_bn text NOT NULL DEFAULT '',

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT renal_policy_citation_present CHECK (btrim(source_citation) <> ''),
  -- An approval is a person and an instant, or it is neither.
  CONSTRAINT renal_policy_approval_is_whole
    CHECK ((approved_by IS NULL) = (approved_at IS NULL)),
  -- A seeded row cannot arrive pre-approved. Same mechanism as a seeded price and a seeded rule.
  CONSTRAINT renal_policy_seed_is_unapproved
    CHECK (origin <> 'SEED' OR approved_by IS NULL)
);

COMMENT ON TABLE core.facility_renal_policy IS
  'How old an eGFR may be and still count as current renal function, per facility (CP79). '
  'Six months is the plan''s proposal and nobody has approved it yet.';

SELECT core.attach_updated_at('core.facility_renal_policy');
GRANT SELECT, INSERT, UPDATE ON core.facility_renal_policy TO dthcms_app;

-- Every facility gets the proposed window. Re-runnable, and it never overwrites a window
-- somebody has changed: ON CONFLICT DO NOTHING rather than DO UPDATE, for the same reason the
-- rule seed refuses to touch an existing rule.
INSERT INTO core.facility_renal_policy (facility_id, egfr_recency_months, source_citation, notes_en, notes_bn)
SELECT f.id, 6,
  'DTHCMS implementation plan CP79, proposed window pending clinical approval; KDIGO 2024 CKD Evaluation and Management s1.4 (a CKD diagnosis requires markers present for >3 months, and monitoring frequency by GFR/albuminuria category ranges from annual to four-monthly).',
  'Six months is a proposal, not an approved clinical threshold. Until a physician approves it, every renal status returned by this system is marked provisional.',
  'ছয় মাস একটি প্রস্তাব, অনুমোদিত চিকিৎসা-সীমা নয়। কোনো চিকিৎসক অনুমোদন না করা পর্যন্ত এই সিস্টেমের প্রতিটি কিডনি-অবস্থা "প্রাথমিক" হিসেবে দেখানো হবে।'
  FROM core.facility f
ON CONFLICT (facility_id) DO NOTHING;

-- +goose StatementBegin
-- A facility created after this migration gets the window too.
--
-- Without this, the invariant below is a claim the database cannot keep: goose does not re-run a
-- migration, so a satellite clinic added next year would have no policy row and every start would
-- fail. `TestMigrationsRunOverExistingData` inserts exactly such a facility and found this within
-- minutes of the migration being written.
--
-- The alternative — a COALESCE to six months in the resolver — is the constant this table exists
-- to replace, and it would have hidden the missing row rather than preventing it.
CREATE OR REPLACE FUNCTION core.facility_gets_a_renal_window() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO core.facility_renal_policy
    (facility_id, egfr_recency_months, source_citation, notes_en, notes_bn)
  VALUES (NEW.id, 6,
    'DTHCMS implementation plan CP79, proposed window pending clinical approval; KDIGO 2024 CKD Evaluation and Management s1.4.',
    'Six months is a proposal, not an approved clinical threshold. Until a physician approves it, every renal status returned by this system is marked provisional.',
    'ছয় মাস একটি প্রস্তাব, অনুমোদিত চিকিৎসা-সীমা নয়। কোনো চিকিৎসক অনুমোদন না করা পর্যন্ত এই সিস্টেমের প্রতিটি কিডনি-অবস্থা "প্রাথমিক" হিসেবে দেখানো হবে।')
  ON CONFLICT (facility_id) DO NOTHING;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER facility_renal_window
  AFTER INSERT ON core.facility
  FOR EACH ROW EXECUTE FUNCTION core.facility_gets_a_renal_window();

-- +goose StatementBegin
-- Every facility has exactly one window, including one created after this migration ran.
--
-- Without this, a facility added next year would have no policy row, and the resolver would have
-- to choose between a hardcoded fallback — which is the constant this table exists to replace —
-- and refusing to evaluate renal safety at all. The invariant makes the third option, noticing,
-- the one that happens.
CREATE OR REPLACE FUNCTION core.assert_every_facility_has_a_renal_window() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE missing bigint;
BEGIN
  SELECT count(*) INTO missing
    FROM core.facility f
    LEFT JOIN core.facility_renal_policy p ON p.facility_id = f.id
   WHERE p.facility_id IS NULL;
  IF missing > 0 THEN
    RAISE EXCEPTION '% facilities have no eGFR recency window', missing
      USING HINT = 'Insert a core.facility_renal_policy row. There is no hardcoded fallback, on purpose.';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_every_facility_has_a_renal_window() IS
  'CP79: a facility with no recency window would have to fall back to a constant, which is what the table replaces.';

-- ---------------------------------------------------------------------------
-- 2. Which molecules need a kidney function before they are prescribed
-- ---------------------------------------------------------------------------

CREATE TABLE core.generic_renal_dependence (
  generic_id uuid PRIMARY KEY REFERENCES core.generic(id),

  -- EGFR_REQUIRED     — this drug cannot be prescribed safely without a current eGFR, whether
  --                     because it is renally cleared, because its efficacy has an eGFR floor,
  --                     or because its risk profile changes with kidney function.
  -- EGFR_NOT_REQUIRED — prescribing this does not turn on the kidney function.
  --
  -- **A molecule with no row is neither**, and the engine says so. See the header.
  dependence text NOT NULL CHECK (dependence IN ('EGFR_REQUIRED', 'EGFR_NOT_REQUIRED')),

  -- Why, in one sentence, in both languages. This is rendered beside the finding, so it is
  -- written for a physician reading a screen rather than for a reviewer reading a table.
  reason_en text NOT NULL,
  reason_bn text NOT NULL,

  source_citation text NOT NULL,
  origin text NOT NULL DEFAULT 'SEED' CHECK (origin IN ('SEED', 'AUTHORED')),

  approved_by uuid REFERENCES core.app_user(id),
  approved_at timestamptz,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT renal_dependence_reason_present
    CHECK (btrim(reason_en) <> '' AND btrim(reason_bn) <> ''),
  CONSTRAINT renal_dependence_citation_present CHECK (btrim(source_citation) <> ''),
  CONSTRAINT renal_dependence_approval_is_whole
    CHECK ((approved_by IS NULL) = (approved_at IS NULL)),
  CONSTRAINT renal_dependence_seed_is_unapproved
    CHECK (origin <> 'SEED' OR approved_by IS NULL)
);

COMMENT ON TABLE core.generic_renal_dependence IS
  'Whether prescribing this molecule depends on knowing the kidney function (CP79). '
  'Absence of a row means unclassified, which the engine reports and never reads as safe.';

-- Global, like `core.generic` itself and for the same reason: whether gabapentin is cleared by
-- the kidney is pharmacology, not this clinic's data. The *window* on the other hand is
-- facility-scoped, because how old a result may be is a clinical judgement a clinic makes.
INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'generic_renal_dependence',
   'Whether a molecule''s dosing depends on kidney function is pharmacology, not a clinic''s data. The recency window, which is a clinical judgement, is on core.facility_renal_policy and is scoped.')
ON CONFLICT (schema_name, table_name) DO UPDATE SET reason = EXCLUDED.reason;

SELECT core.attach_updated_at('core.generic_renal_dependence');
GRANT SELECT, INSERT, UPDATE ON core.generic_renal_dependence TO dthcms_app;

-- +goose StatementBegin
-- Classifies one molecule by name, and does nothing at all if somebody already classified it.
--
-- By name rather than by id because the names are what a clinician reads and what every seeded
-- rule's condition already names. A name this formulary does not hold is **skipped silently**:
-- these rows are content, this function runs on every migration in every environment, and a
-- facility that has not imported the full formulary must not fail to migrate over a molecule it
-- does not stock.
CREATE OR REPLACE FUNCTION core.seed_renal_dependence(
  p_generic text, p_dependence text, p_reason_en text, p_reason_bn text, p_source text)
RETURNS void
LANGUAGE plpgsql AS $$
DECLARE g uuid;
BEGIN
  SELECT id INTO g FROM core.generic WHERE lower(name) = lower(btrim(p_generic));
  IF g IS NULL THEN
    RETURN;
  END IF;
  INSERT INTO core.generic_renal_dependence
    (generic_id, dependence, reason_en, reason_bn, source_citation, origin)
  VALUES (g, p_dependence, p_reason_en, p_reason_bn, p_source, 'SEED')
  ON CONFLICT (generic_id) DO NOTHING;
END
$$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- 3. The classification
-- ---------------------------------------------------------------------------
--
-- Sixty-three of this formulary's sixty-five molecules, each with a reason and a citation, each
-- seeded unapproved. **Two are deliberately left unclassified** — see the end of the block — so
-- that the unclassified path is exercised by real data rather than only by a test fixture.
--
-- Dr. Nahid should expect to disagree with some of these. They are listed for him separately.

-- Biguanide and every combination containing it. The single most important row here: metformin
-- is this clinic's first-line drug and the one whose renal threshold is a hard contraindication.
SELECT core.seed_renal_dependence('Metformin hydrochloride', 'EGFR_REQUIRED',
  'Metformin is cleared unchanged by the kidney and is contraindicated below an eGFR of 30. It cannot be started without knowing the kidney function.',
  'মেটফরমিন অপরিবর্তিত অবস্থায় কিডনি দিয়ে বের হয় এবং eGFR 30-এর নিচে দেওয়া যায় না। কিডনির কার্যকারিতা না জেনে এটি শুরু করা যায় না।',
  'US FDA metformin labelling, 2016 revision; KDIGO 2022 Diabetes in CKD.');
SELECT core.seed_renal_dependence('Linagliptin + Metformin hydrochloride', 'EGFR_REQUIRED',
  'The metformin in this combination is contraindicated below an eGFR of 30, whatever the other component does.',
  'এই সংমিশ্রণের মেটফরমিন eGFR 30-এর নিচে দেওয়া যায় না, অন্য উপাদান যাই হোক।',
  'US FDA metformin labelling, 2016 revision.');
SELECT core.seed_renal_dependence('Sitagliptin + Metformin hydrochloride', 'EGFR_REQUIRED',
  'Both components depend on kidney function: metformin is contraindicated below 30 and sitagliptin needs dose reduction below 45.',
  'দুটি উপাদানই কিডনির উপর নির্ভরশীল: মেটফরমিন 30-এর নিচে নিষিদ্ধ, সিটাগ্লিপটিনের মাত্রা 45-এর নিচে কমাতে হয়।',
  'US FDA metformin and sitagliptin labelling.');
SELECT core.seed_renal_dependence('Vildagliptin + Metformin hydrochloride', 'EGFR_REQUIRED',
  'Both components depend on kidney function: metformin is contraindicated below 30 and vildagliptin is halved below 50.',
  'দুটি উপাদানই কিডনির উপর নির্ভরশীল: মেটফরমিন 30-এর নিচে নিষিদ্ধ, ভিলডাগ্লিপটিন 50-এর নিচে অর্ধেক করতে হয়।',
  'EU SmPC vildagliptin; US FDA metformin labelling.');
SELECT core.seed_renal_dependence('Empagliflozin + Metformin hydrochloride', 'EGFR_REQUIRED',
  'The metformin is contraindicated below an eGFR of 30 and the empagliflozin stops lowering glucose below 25.',
  'মেটফরমিন eGFR 30-এর নিচে নিষিদ্ধ এবং এমপাগ্লিফ্লোজিন 25-এর নিচে আর শর্করা কমায় না।',
  'US FDA metformin labelling; empagliflozin EU SmPC.');
SELECT core.seed_renal_dependence('Glimepiride + Metformin hydrochloride', 'EGFR_REQUIRED',
  'The metformin is contraindicated below 30 and the glimepiride causes prolonged hypoglycaemia as kidney function falls.',
  'মেটফরমিন 30-এর নিচে নিষিদ্ধ এবং কিডনির কার্যকারিতা কমলে গ্লিমেপিরাইডে দীর্ঘস্থায়ী হাইপোগ্লাইসেমিয়া হয়।',
  'US FDA metformin labelling; BNF 88, glimepiride renal impairment.');
SELECT core.seed_renal_dependence('Pioglitazone + Metformin hydrochloride', 'EGFR_REQUIRED',
  'The metformin is contraindicated below an eGFR of 30. Pioglitazone alone is not renally dosed.',
  'মেটফরমিন eGFR 30-এর নিচে নিষিদ্ধ। পায়োগ্লিটাজোন একা কিডনি অনুযায়ী মাত্রা বদলায় না।',
  'US FDA metformin labelling, 2016 revision.');

-- DPP-4 inhibitors. The split inside this class is the clearest example of why the column is
-- `renal_dependence` and not `is_renally_cleared`.
SELECT core.seed_renal_dependence('Sitagliptin', 'EGFR_REQUIRED',
  'Sitagliptin is renally cleared: 50 mg below an eGFR of 45 and 25 mg below 30.',
  'সিটাগ্লিপটিন কিডনি দিয়ে বের হয়: eGFR 45-এর নিচে 50 mg, 30-এর নিচে 25 mg।',
  'US FDA sitagliptin labelling (Januvia), dosage in renal impairment; BNF 88.');
SELECT core.seed_renal_dependence('Vildagliptin', 'EGFR_REQUIRED',
  'Vildagliptin is halved to 50 mg once daily below an eGFR of 50.',
  'eGFR 50-এর নিচে ভিলডাগ্লিপটিন দিনে একবার 50 mg-এ নামাতে হয়।',
  'EU SmPC vildagliptin (Galvus), posology in renal impairment.');
SELECT core.seed_renal_dependence('Saxagliptin', 'EGFR_REQUIRED',
  'Saxagliptin is reduced to 2.5 mg daily below an eGFR of 45.',
  'eGFR 45-এর নিচে স্যাক্সাগ্লিপটিন দিনে 2.5 mg-এ নামাতে হয়।',
  'US FDA saxagliptin labelling (Onglyza), dosage in renal impairment.');
SELECT core.seed_renal_dependence('Linagliptin', 'EGFR_NOT_REQUIRED',
  'Linagliptin is cleared by the biliary route and needs no dose change at any level of kidney function. That is why every renal rule in this library offers it as the alternative.',
  'লিনাগ্লিপটিন পিত্তের পথে বের হয় এবং কিডনির যেকোনো অবস্থায় মাত্রা বদলাতে হয় না। এ কারণেই প্রতিটি কিডনি-নিয়মে এটিকে বিকল্প হিসেবে দেখানো হয়।',
  'US FDA linagliptin labelling (Tradjenta); KDIGO 2022 Diabetes in CKD.');

-- SGLT2 inhibitors. Not renally cleared, and an eGFR is still mandatory before starting one.
SELECT core.seed_renal_dependence('Empagliflozin', 'EGFR_REQUIRED',
  'Not renally cleared, but its glucose-lowering effect depends on filtration: below an eGFR of 25 it no longer lowers glucose meaningfully, and starting one for glycaemic control without an eGFR is prescribing blind.',
  'এটি কিডনি দিয়ে বের হয় না, তবু এর শর্করা কমানোর কাজ ছাঁকনির উপর নির্ভরশীল: eGFR 25-এর নিচে এটি আর কার্যত শর্করা কমায় না। eGFR না জেনে শর্করা নিয়ন্ত্রণের জন্য এটি শুরু করা অন্ধভাবে ব্যবস্থাপত্র দেওয়া।',
  'ADA Standards of Care 2025 s11; empagliflozin EU SmPC; KDIGO 2022 Diabetes in CKD.');
SELECT core.seed_renal_dependence('Dapagliflozin propanediol', 'EGFR_REQUIRED',
  'Same as empagliflozin: the glucose-lowering effect falls away below an eGFR of 25, so the kidney function has to be known before it is started for glycaemic control.',
  'এমপাগ্লিফ্লোজিনের মতোই: eGFR 25-এর নিচে শর্করা কমানোর কাজ কমে যায়, তাই শুরু করার আগে কিডনির কার্যকারিতা জানা দরকার।',
  'ADA Standards of Care 2025 s11; dapagliflozin EU SmPC.');

-- Sulphonylureas. The one that kills people at night is glibenclamide.
SELECT core.seed_renal_dependence('Glibenclamide', 'EGFR_REQUIRED',
  'Glibenclamide has active metabolites that accumulate as kidney function falls and cause prolonged overnight hypoglycaemia. Avoid below an eGFR of 60.',
  'গ্লিবেনক্লামাইডের সক্রিয় উপাদান কিডনির কার্যকারিতা কমলে জমে থাকে এবং সারা রাত ধরে হাইপোগ্লাইসেমিয়া ঘটায়। eGFR 60-এর নিচে এড়িয়ে চলুন।',
  'KDIGO 2022 Diabetes in CKD; BNF 88; SIGN 154.');
SELECT core.seed_renal_dependence('Glimepiride', 'EGFR_REQUIRED',
  'Glimepiride carries a high risk of prolonged hypoglycaemia below an eGFR of 30 and is started at 1 mg or avoided.',
  'eGFR 30-এর নিচে গ্লিমেপিরাইডে দীর্ঘস্থায়ী হাইপোগ্লাইসেমিয়ার ঝুঁকি বেশি; 1 mg দিয়ে শুরু করুন বা এড়িয়ে চলুন।',
  'BNF 88, glimepiride renal impairment; KDIGO 2022 Diabetes in CKD.');
SELECT core.seed_renal_dependence('Gliclazide', 'EGFR_NOT_REQUIRED',
  'Gliclazide is metabolised to inactive products and is the sulphonylurea of choice when kidney function is reduced. Caution rather than a threshold.',
  'গ্লিক্লাজাইড নিষ্ক্রিয় উপাদানে ভেঙে যায় এবং কিডনির কার্যকারিতা কমলে এটিই পছন্দের সালফোনাইলইউরিয়া। এখানে নির্দিষ্ট সীমা নয়, সতর্কতা।',
  'KDIGO 2022 Diabetes in CKD; BNF 88, gliclazide renal impairment.');
SELECT core.seed_renal_dependence('Pioglitazone + Glimepiride', 'EGFR_REQUIRED',
  'The glimepiride half carries the hypoglycaemia risk that rises as kidney function falls.',
  'এর গ্লিমেপিরাইড অংশটিই কিডনির কার্যকারিতা কমলে হাইপোগ্লাইসেমিয়ার ঝুঁকি বাড়ায়।',
  'BNF 88, glimepiride renal impairment.');

-- Thiazolidinedione, GLP-1 agonists, insulins: not renally dosed.
SELECT core.seed_renal_dependence('Pioglitazone', 'EGFR_NOT_REQUIRED',
  'Pioglitazone is hepatically metabolised and needs no renal dose adjustment. Its problem is fluid retention in heart failure, which is a different rule.',
  'পায়োগ্লিটাজোন যকৃতে ভাঙে, কিডনি অনুযায়ী মাত্রা বদলাতে হয় না। এর সমস্যা হৃদযন্ত্রের বিকলতায় পানি জমা, যা আলাদা নিয়ম।',
  'US FDA pioglitazone labelling (Actos); BNF 88.');
SELECT core.seed_renal_dependence('Semaglutide', 'EGFR_NOT_REQUIRED',
  'No dose adjustment by kidney function. Dehydration from vomiting can worsen kidney function, which is advice rather than a dosing threshold.',
  'কিডনি অনুযায়ী মাত্রা বদলাতে হয় না। বমির কারণে পানিশূন্যতা কিডনির ক্ষতি করতে পারে — এটি মাত্রার সীমা নয়, পরামর্শ।',
  'US FDA semaglutide labelling (Ozempic); ADA Standards of Care 2025 s11.');
SELECT core.seed_renal_dependence('Liraglutide', 'EGFR_NOT_REQUIRED',
  'No dose adjustment by kidney function.',
  'কিডনি অনুযায়ী মাত্রা বদলাতে হয় না।',
  'US FDA liraglutide labelling (Victoza).');
SELECT core.seed_renal_dependence('Dulaglutide', 'EGFR_NOT_REQUIRED',
  'No dose adjustment by kidney function.',
  'কিডনি অনুযায়ী মাত্রা বদলাতে হয় না।',
  'US FDA dulaglutide labelling (Trulicity).');

-- Every insulin. Insulin requirement genuinely falls as kidney function falls, and that is a
-- titration fact rather than a band: there is no eGFR at which a dose can be computed, and a
-- fail-closed finding on every insulin prescription would be noise that teaches the physician to
-- ignore the column.
SELECT core.seed_renal_dependence(name, 'EGFR_NOT_REQUIRED',
  'Insulin is titrated to the glucose, not to the eGFR. Insulin requirement does fall as kidney function falls — that is a reason to review the dose, not a threshold the software can apply.',
  'ইনসুলিনের মাত্রা রক্তের শর্করা দেখে ঠিক করা হয়, eGFR দেখে নয়। কিডনির কার্যকারিতা কমলে ইনসুলিনের প্রয়োজন কমে — এটি মাত্রা পুনর্বিবেচনার কারণ, সফটওয়্যারের প্রয়োগ করার মতো সীমা নয়।',
  'ADA Standards of Care 2025 s11; KDIGO 2022 Diabetes in CKD.')
  FROM core.generic WHERE class_code LIKE 'INSULIN%';

-- RAAS blockers. The ACE inhibitors are renally cleared; the ARBs in this formulary are not, and
-- both carry the same haemodynamic caution, which `RAAS-RENAL-30` covers separately.
SELECT core.seed_renal_dependence(name, 'EGFR_REQUIRED',
  'ACE inhibitors are renally cleared and dose-reduced in impairment, and any dose change needs potassium and creatinine rechecked one to two weeks later.',
  'ACE ইনহিবিটর কিডনি দিয়ে বের হয় এবং কিডনির দুর্বলতায় মাত্রা কমাতে হয়; মাত্রা বদলানোর 1-2 সপ্তাহ পর পটাশিয়াম ও ক্রিয়েটিনিন দেখতে হয়।',
  'BNF 88, renal impairment; KDIGO 2024 CKD Evaluation and Management s3.3.')
  FROM core.generic WHERE class_code = 'ACE_INHIBITOR';
SELECT core.seed_renal_dependence('Losartan potassium', 'EGFR_NOT_REQUIRED',
  'Losartan is hepatically metabolised and is not renally dose-adjusted. The caution in CKD is haemodynamic and is covered by the RAAS rule rather than by a dose band.',
  'লোসারটান যকৃতে ভাঙে, কিডনি অনুযায়ী মাত্রা বদলাতে হয় না। CKD-তে সতর্কতা রক্তপ্রবাহ-সংক্রান্ত, যা RAAS নিয়মে আছে, মাত্রার সীমায় নয়।',
  'US FDA losartan labelling (Cozaar); BNF 88.');
SELECT core.seed_renal_dependence('Telmisartan', 'EGFR_NOT_REQUIRED',
  'Telmisartan is cleared by the biliary route and is not renally dose-adjusted.',
  'টেলমিসারটান পিত্তের পথে বের হয়, কিডনি অনুযায়ী মাত্রা বদলাতে হয় না।',
  'EU SmPC telmisartan (Micardis); BNF 88.');
SELECT core.seed_renal_dependence('Olmesartan medoxomil', 'EGFR_NOT_REQUIRED',
  'Olmesartan needs no renal dose adjustment down to an eGFR of 20; below that there is no established dose.',
  'eGFR 20 পর্যন্ত অলমেসারটানের মাত্রা বদলাতে হয় না; এর নিচে প্রতিষ্ঠিত মাত্রা নেই।',
  'US FDA olmesartan labelling (Benicar); EU SmPC.');
SELECT core.seed_renal_dependence('Telmisartan + Amlodipine besilate', 'EGFR_NOT_REQUIRED',
  'Neither component is renally dose-adjusted.',
  'কোনো উপাদানই কিডনি অনুযায়ী মাত্রা বদলায় না।',
  'EU SmPCs telmisartan and amlodipine.');
SELECT core.seed_renal_dependence('Losartan potassium + Hydrochlorothiazide', 'EGFR_REQUIRED',
  'The hydrochlorothiazide half stops working below an eGFR of about 30, at which point this tablet is a losartan tablet with a useless second ingredient in it.',
  'এর হাইড্রোক্লোরোথায়াজাইড অংশ eGFR প্রায় 30-এর নিচে আর কাজ করে না; তখন এই ট্যাবলেটটি কার্যত শুধু লোসারটান, সঙ্গে অকেজো একটি উপাদান।',
  'BNF 88, thiazide diuretics in renal impairment; KDIGO 2024 CKD s3.');

-- Diuretics.
SELECT core.seed_renal_dependence('Hydrochlorothiazide', 'EGFR_REQUIRED',
  'Thiazides lose their diuretic effect below an eGFR of about 30. Prescribing one at that level is prescribing an electrolyte disturbance with no benefit.',
  'eGFR প্রায় 30-এর নিচে থায়াজাইড আর প্রস্রাব বাড়ায় না। এই অবস্থায় এটি দেওয়া মানে কোনো লাভ ছাড়াই লবণের ভারসাম্য নষ্ট করা।',
  'BNF 88, thiazide diuretics in renal impairment; KDIGO 2024 CKD s3.');
SELECT core.seed_renal_dependence('Furosemide', 'EGFR_REQUIRED',
  'Furosemide works at every level of kidney function but needs progressively higher doses as it falls, because it must be secreted into the tubule to act.',
  'কিডনির যেকোনো অবস্থাতেই ফিউরোসেমাইড কাজ করে, তবে কার্যকারিতা কমার সঙ্গে সঙ্গে মাত্রা বাড়াতে হয়, কারণ কাজ করতে হলে একে নালিকায় পৌঁছাতে হয়।',
  'BNF 88, loop diuretics in renal impairment; KDIGO 2024 CKD.');

-- Neuropathic pain. Both are among the commonest avoidable overdoses in a diabetic CKD clinic.
SELECT core.seed_renal_dependence('Gabapentin', 'EGFR_REQUIRED',
  'Gabapentin is eliminated unchanged by the kidney and accumulates fast. Sedation, ataxia and myoclonus in CKD are usually this drug at an unadjusted dose.',
  'গ্যাবাপেন্টিন অপরিবর্তিত অবস্থায় কিডনি দিয়ে বের হয় এবং দ্রুত জমে। CKD-তে ঝিমুনি, ভারসাম্যহীনতা ও পেশির ঝাঁকুনি সাধারণত মাত্রা না কমানো এই ওষুধেরই ফল।',
  'US FDA gabapentin labelling (Neurontin), dosage in renal impairment; BNF 88.');
SELECT core.seed_renal_dependence('Pregabalin', 'EGFR_REQUIRED',
  'Pregabalin is eliminated unchanged by the kidney and the daily dose is banded by creatinine clearance from 600 mg down to 75 mg.',
  'প্রিগাবালিন অপরিবর্তিত অবস্থায় কিডনি দিয়ে বের হয়; ক্রিয়েটিনিন ক্লিয়ারেন্স অনুযায়ী দৈনিক মাত্রা 600 mg থেকে 75 mg পর্যন্ত নামে।',
  'US FDA pregabalin labelling (Lyrica), dosage in renal impairment; BNF 88.');

-- NSAIDs. Bought over a counter here, and the commonest cause of acute-on-chronic kidney injury.
SELECT core.seed_renal_dependence(name, 'EGFR_REQUIRED',
  'An NSAID reduces renal blood flow. In CKD, and especially alongside an ACE inhibitor or ARB and a diuretic, it is a leading cause of acute-on-chronic kidney injury.',
  'NSAID কিডনিতে রক্তপ্রবাহ কমায়। CKD-তে, বিশেষত ACE ইনহিবিটর বা ARB ও মূত্রবর্ধকের সঙ্গে, এটি হঠাৎ কিডনি বিকল হওয়ার প্রধান কারণগুলোর একটি।',
  'KDIGO 2024 CKD Evaluation and Management s3.5; BNF 88, NSAIDs in renal impairment.')
  FROM core.generic WHERE class_code = 'NSAID';

-- Antibiotic.
SELECT core.seed_renal_dependence('Amoxicillin', 'EGFR_REQUIRED',
  'Amoxicillin is renally cleared and the dosing interval is lengthened below an eGFR of 30.',
  'অ্যামোক্সিসিলিন কিডনি দিয়ে বের হয়; eGFR 30-এর নিচে দুই মাত্রার মধ্যে ব্যবধান বাড়াতে হয়।',
  'BNF 88, amoxicillin renal impairment.');

-- Lipids.
SELECT core.seed_renal_dependence('Rosuvastatin calcium', 'EGFR_REQUIRED',
  'Rosuvastatin is capped at 10 mg daily below an eGFR of 30 and is not started at 40 mg in any degree of renal impairment.',
  'eGFR 30-এর নিচে রোসুভাস্ট্যাটিন দিনে সর্বোচ্চ 10 mg; কিডনির দুর্বলতায় 40 mg দিয়ে শুরু করা যায় না।',
  'US FDA rosuvastatin labelling (Crestor), dosage in renal impairment.');
SELECT core.seed_renal_dependence('Simvastatin', 'EGFR_REQUIRED',
  'Simvastatin above 10 mg daily is used with caution below an eGFR of 30 because of the myopathy risk.',
  'eGFR 30-এর নিচে দিনে 10 mg-এর বেশি সিমভাস্ট্যাটিন সাবধানে, পেশির ক্ষতির ঝুঁকির কারণে।',
  'US FDA simvastatin labelling (Zocor); BNF 88.');
SELECT core.seed_renal_dependence('Atorvastatin calcium', 'EGFR_NOT_REQUIRED',
  'Atorvastatin is cleared hepatically and needs no renal dose adjustment.',
  'অ্যাটরভাস্ট্যাটিন যকৃত দিয়ে বের হয়, কিডনি অনুযায়ী মাত্রা বদলাতে হয় না।',
  'US FDA atorvastatin labelling (Lipitor); BNF 88.');
SELECT core.seed_renal_dependence('Fenofibrate', 'EGFR_REQUIRED',
  'Fenofibrate is renally cleared, dose-reduced below an eGFR of 60 and avoided below 30.',
  'ফেনোফাইব্রেট কিডনি দিয়ে বের হয়; eGFR 60-এর নিচে মাত্রা কমাতে হয়, 30-এর নিচে এড়াতে হয়।',
  'US FDA fenofibrate labelling; BNF 88, fenofibrate renal impairment.');
SELECT core.seed_renal_dependence('Ezetimibe', 'EGFR_NOT_REQUIRED',
  'Ezetimibe needs no renal dose adjustment.',
  'ইজেটিমাইবের মাত্রা কিডনি অনুযায়ী বদলাতে হয় না।',
  'US FDA ezetimibe labelling (Zetia).');

-- Thyroid, cardiovascular, supplements.
SELECT core.seed_renal_dependence('Levothyroxine sodium', 'EGFR_NOT_REQUIRED',
  'Levothyroxine is titrated to TSH and is not renally dosed.',
  'লেভোথাইরক্সিনের মাত্রা TSH দেখে ঠিক হয়, কিডনি অনুযায়ী নয়।',
  'ATA 2014 Guidelines for the Treatment of Hypothyroidism; BNF 88.');
SELECT core.seed_renal_dependence(name, 'EGFR_NOT_REQUIRED',
  'Antithyroid drugs are titrated to thyroid function tests and are not renally dosed.',
  'অ্যান্টিথাইরয়েড ওষুধের মাত্রা থাইরয়েড পরীক্ষা দেখে ঠিক হয়, কিডনি অনুযায়ী নয়।',
  'ATA 2016 Guidelines for Hyperthyroidism; BNF 88.')
  FROM core.generic WHERE class_code = 'ANTITHYROID';
SELECT core.seed_renal_dependence('Amlodipine besilate', 'EGFR_NOT_REQUIRED',
  'Amlodipine is hepatically metabolised and needs no renal dose adjustment.',
  'অ্যামলোডিপিন যকৃতে ভাঙে, কিডনি অনুযায়ী মাত্রা বদলাতে হয় না।',
  'US FDA amlodipine labelling (Norvasc); BNF 88.');
SELECT core.seed_renal_dependence('Aspirin (low dose)', 'EGFR_NOT_REQUIRED',
  'Low-dose aspirin for cardiovascular protection is not renally dose-adjusted. Analgesic doses are a different drug for this purpose.',
  'হৃদরোগ প্রতিরোধে কম মাত্রার অ্যাসপিরিনের মাত্রা কিডনি অনুযায়ী বদলাতে হয় না। ব্যথার মাত্রা এ ক্ষেত্রে ভিন্ন বিষয়।',
  'BNF 88, aspirin renal impairment; KDIGO 2024 CKD.');
SELECT core.seed_renal_dependence('Acarbose', 'EGFR_REQUIRED',
  'Acarbose is barely absorbed, but plasma concentrations rise sharply in renal impairment and it is not recommended below a creatinine clearance of 25.',
  'অ্যাকারবোজ প্রায় শোষিতই হয় না, তবু কিডনির দুর্বলতায় রক্তে এর মাত্রা অনেক বেড়ে যায়; ক্রিয়েটিনিন ক্লিয়ারেন্স 25-এর নিচে এটি দেওয়ার পরামর্শ নেই।',
  'US FDA acarbose labelling (Precose), renal impairment; BNF 88.');
SELECT core.seed_renal_dependence('Cholecalciferol (Vitamin D3)', 'EGFR_NOT_REQUIRED',
  'Nutritional vitamin D replacement is not renally dose-adjusted. Active vitamin D analogues in CKD-MBD are a different question and this clinic stocks none.',
  'পুষ্টির জন্য ভিটামিন ডি-এর মাত্রা কিডনি অনুযায়ী বদলাতে হয় না। CKD-MBD-তে সক্রিয় ভিটামিন ডি আলাদা বিষয় এবং এই ক্লিনিকে তা নেই।',
  'KDIGO 2017 CKD-MBD update; BNF 88.');
SELECT core.seed_renal_dependence('Folic acid', 'EGFR_NOT_REQUIRED',
  'Folic acid is not renally dose-adjusted.',
  'ফলিক অ্যাসিডের মাত্রা কিডনি অনুযায়ী বদলাতে হয় না।',
  'BNF 88.');

-- Deliberately left unclassified, and this is the honest answer rather than a gap:
--
--   * **Voglibose** — the same class as acarbose, and the published renal data is thinner. I am
--     not confident enough to say either thing about it in a system that will act on the answer.
--   * **Calcium lactate gluconate + Calcium carbonate + Vitamin D3** — calcium supplementation
--     in CKD is not a clearance question at all. It is CKD-MBD, where a calcium load can be
--     actively harmful at low eGFR and the decision belongs to a physician rather than to a
--     dependence flag. Classifying it either way would misrepresent the question.
--
-- Both come back from the engine as "renal handling not classified", which is what CP78's
-- coverage model does with a drug no rule mentions, and for the same reason.

-- +goose StatementBegin
-- Every classification says why and where from, and no seeded one claims an approval.
CREATE OR REPLACE FUNCTION core.assert_renal_content_is_sourced_and_unapproved() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.generic_renal_dependence
   WHERE origin = 'SEED' AND (approved_by IS NOT NULL OR approved_at IS NOT NULL);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% seeded renal classifications claim an approval nobody gave', offenders;
  END IF;

  SELECT count(*) INTO offenders
    FROM core.facility_renal_policy
   WHERE origin = 'SEED' AND (approved_by IS NOT NULL OR approved_at IS NOT NULL);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% seeded eGFR recency windows claim an approval nobody gave', offenders;
  END IF;

  SELECT count(*) INTO offenders
    FROM core.generic_renal_dependence
   WHERE btrim(source_citation) = '' OR btrim(reason_en) = '' OR btrim(reason_bn) = '';
  IF offenders > 0 THEN
    RAISE EXCEPTION '% renal classifications lack a source or a bilingual reason', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_renal_content_is_sourced_and_unapproved() IS
  'CP79: seeded renal content is drafted from published guidance and approved by nobody until a physician reads it.';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_facility_has_a_renal_window',
   'every facility has an eGFR recency window; there is no hardcoded fallback', 120),
  ('assert_renal_content_is_sourced_and_unapproved',
   'every seeded renal classification cites a source, reads in both languages, and claims no approval', 121)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_every_facility_has_a_renal_window',
  'assert_renal_content_is_sourced_and_unapproved');
DROP FUNCTION IF EXISTS core.assert_renal_content_is_sourced_and_unapproved();
DROP FUNCTION IF EXISTS core.assert_every_facility_has_a_renal_window();
DROP TRIGGER IF EXISTS facility_renal_window ON core.facility;
DROP FUNCTION IF EXISTS core.facility_gets_a_renal_window();
DROP FUNCTION IF EXISTS core.seed_renal_dependence(text, text, text, text, text);
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name = 'generic_renal_dependence';
DROP TABLE IF EXISTS core.generic_renal_dependence;
DROP TABLE IF EXISTS core.facility_renal_policy;
