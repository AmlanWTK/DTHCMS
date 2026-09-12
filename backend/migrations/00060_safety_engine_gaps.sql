-- The rules and molecules the golden suite needed and did not find (CP78).
--
-- # Why a content migration inside an engine checkpoint
--
-- CP78's acceptance criterion 1 is a golden suite of clinical scenarios with **zero false
-- negatives**, and the plan names four of them as non-negotiable:
--
--   * penicillin allergy + amoxicillin → BLOCK
--   * metformin + eGFR 25 → BLOCK
--   * two ACE inhibitors → duplicate therapy flagged
--   * an unknown drug → reports no coverage rather than passing
--
-- Only the second and fourth can be expressed against what CP75 and CP77 shipped. The first
-- needs a molecule this formulary does not hold and a rule nobody has written; the third needs a
-- class-level duplicate rule, because `DUP-GENERIC` compares molecules and enalapril is not
-- ramipril. Writing the suite against rules that do not exist would have produced a suite that
-- passes by asserting nothing, which is the failure mode this checkpoint's verification bar is
-- entirely about.
--
-- So the gaps are filled here, in the checkpoint that found them, under exactly CP77's terms:
--
--   * **every rule below is seeded unapproved and is inert until Dr. Nahid reads it.** Not one
--     of them can fire on a real prescription today. `assert_no_unapproved_rule_is_live()`
--     (invariant 113) still holds, and `TestNoneOfTheSeededRulesCanFire` covers these too.
--   * every rule cites the published guidance it was drafted from;
--   * every rule reads in both languages.
--
-- **These eight rules and six molecules are the largest single piece of clinical content a
-- developer has added to this system, and they are listed for Dr. Nahid separately from
-- everything else in this checkpoint.** He should expect to disagree with some of them.
--
-- # Why the molecules
--
-- Six, and each is here because a named clinical scenario needs it, not because a formulary
-- should be complete.
--
--   * **Amoxicillin** — the penicillin scenario, and genuinely prescribed in this clinic: a
--     diabetic foot infection and dental prophylaxis before an extraction are both ordinary.
--   * **Ibuprofen, Naproxen, Diclofenac** — the "triple whammy" (an NSAID with an ACE inhibitor
--     or ARB and a diuretic, in CKD) is a leading cause of acute-on-chronic kidney injury, and in
--     Bangladesh NSAIDs are bought over a counter without a prescription. A rule that cannot name
--     them is a rule that cannot warn about the commonest way a diabetic kidney is damaged here.
--   * **Hydrochlorothiazide and Furosemide** — the diuretic half of the same rule. HCTZ is
--     already a *component* of a combination this clinic stocks (Losartan + HCTZ), which is
--     precisely the case migration 00058 exists for: without a generic row, a rule about
--     diuretics cannot see the diuretic inside the combination tablet.
--
-- Each is given components in the same breath, because an undetermined generic makes every
-- duplicate check involving it answer "cannot verify" — correct, and not what anybody wants for
-- a molecule added on purpose.
--
-- **Prices: none.** A generic has no price; a *product* does, and no products are added here.
-- These molecules exist so that a rule can name them and a patient's current-medication list can
-- resolve to them. Somebody stocking amoxicillin adds the brand and its price through CP75's
-- admin screen, with their name on it.

-- +goose Up

-- ---------------------------------------------------------------------------
-- 1. The diagnosis codes the new rules are keyed to
-- ---------------------------------------------------------------------------

-- A rule keyed to a code nobody can record never fires and nothing says so — CP77's own sentence,
-- and the reason it added five codes with its rules. These are this checkpoint's five.
INSERT INTO core.terminology_concept (system, version, code, display_en, display_bn, heading, heading_bn)
VALUES
  ('ICD10', '2019', 'N18.3', 'Chronic kidney disease, stage 3',
   'দীর্ঘমেয়াদি বৃক্করোগ, পর্যায় 3',
   'Diseases of the genitourinary system', 'মূত্র ও জননতন্ত্রের রোগ'),
  ('ICD10', '2019', 'N18.4', 'Chronic kidney disease, stage 4',
   'দীর্ঘমেয়াদি বৃক্করোগ, পর্যায় 4',
   'Diseases of the genitourinary system', 'মূত্র ও জননতন্ত্রের রোগ'),
  ('ICD10', '2019', 'C73', 'Malignant neoplasm of thyroid gland',
   'থাইরয়েড গ্রন্থির ম্যালিগন্যান্ট টিউমার',
   'Neoplasms', 'নিওপ্লাজম'),
  ('ICD10', '2019', 'B37.3', 'Candidiasis of vulva and vagina',
   'ভালভা ও যোনির ক্যান্ডিডিয়াসিস',
   'Certain infectious and parasitic diseases', 'কিছু সংক্রামক ও পরজীবী রোগ'),
  ('ICD10', '2019', 'B37.4', 'Candidiasis of other urogenital sites',
   'মূত্র ও জননাঙ্গের অন্যান্য স্থানে ক্যান্ডিডিয়াসিস',
   'Certain infectious and parasitic diseases', 'কিছু সংক্রামক ও পরজীবী রোগ'),
  ('ICD10', '2019', 'N39.0', 'Urinary tract infection, site not specified',
   'মূত্রনালির সংক্রমণ, স্থান অনির্দিষ্ট',
   'Diseases of the genitourinary system', 'মূত্র ও জননতন্ত্রের রোগ')
ON CONFLICT (system, version, code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn;

-- ---------------------------------------------------------------------------
-- 2. The classes and the molecules
-- ---------------------------------------------------------------------------

INSERT INTO core.medication_class (code, name_en, name_bn, ordering) VALUES
  ('PENICILLIN_ANTIBIOTIC', 'Penicillin antibiotic', 'পেনিসিলিন অ্যান্টিবায়োটিক', 90),
  ('NSAID', 'Anti-inflammatory painkiller (NSAID)', 'প্রদাহনাশক ব্যথার ওষুধ (NSAID)', 91),
  ('THIAZIDE_DIURETIC', 'Thiazide diuretic', 'থায়াজাইড মূত্রবর্ধক', 92),
  ('LOOP_DIURETIC', 'Loop diuretic', 'লুপ মূত্রবর্ধক', 93)
ON CONFLICT (code) DO NOTHING;

INSERT INTO core.generic (name, class_code, notes) VALUES
  ('Amoxicillin', 'PENICILLIN_ANTIBIOTIC',
   'Added by CP78 so that the penicillin-allergy scenario can be expressed, and because a diabetic foot infection and dental prophylaxis are both ordinary here. No product is stocked against it until somebody adds one.'),
  ('Ibuprofen', 'NSAID',
   'Added by CP78 for the NSAID/ACE-inhibitor/diuretic rule. Bought over a counter in Bangladesh, which is exactly why the prescription has to ask about it.'),
  ('Naproxen', 'NSAID', 'Added by CP78 for the NSAID/ACE-inhibitor/diuretic rule.'),
  ('Diclofenac sodium', 'NSAID',
   'Added by CP78 for the NSAID/ACE-inhibitor/diuretic rule. The NSAID most often taken without a prescription here.'),
  ('Hydrochlorothiazide', 'THIAZIDE_DIURETIC',
   'Added by CP78. Already a component of Losartan + Hydrochlorothiazide, which this clinic stocks — the case migration 00058 exists for.'),
  ('Furosemide', 'LOOP_DIURETIC', 'Added by CP78 for the NSAID/ACE-inhibitor/diuretic rule.')
ON CONFLICT DO NOTHING;

SELECT core.determine_generic_components('Amoxicillin', ARRAY['Amoxicillin'], 'WHO INN (CP78).');
SELECT core.determine_generic_components('Ibuprofen', ARRAY['Ibuprofen'], 'WHO INN (CP78).');
SELECT core.determine_generic_components('Naproxen', ARRAY['Naproxen'], 'WHO INN (CP78).');
SELECT core.determine_generic_components('Diclofenac sodium', ARRAY['Diclofenac'],
  'WHO INN; the sodium is a salt and is dropped (CP78).');
SELECT core.determine_generic_components('Hydrochlorothiazide', ARRAY['Hydrochlorothiazide'],
  'WHO INN. Named exactly as the component inside Losartan + Hydrochlorothiazide, which is what makes the combination collide with a separate thiazide (CP78).');
SELECT core.determine_generic_components('Furosemide', ARRAY['Furosemide'], 'WHO INN (CP78).');

-- Amoxicillin joins the penicillin allergen group, which CP77 seeded with it already named as a
-- member. Nothing to do — the membership is by molecule name and does not depend on this
-- clinic stocking it, which is the reason CP77 modelled it that way.

-- ---------------------------------------------------------------------------
-- 3. The eight rules
-- ---------------------------------------------------------------------------

-- Every one seeded through CP77's own function, which cannot produce an approved row: the
-- version goes in with `approved_by`, `approved_at`, `effective_from` and `effective_to` all
-- null, and two check constraints make any other combination impossible for a DRAFT.

SELECT core.seed_medication_rule(f.id, 'PEN-ALLERGY', 'CONTRAINDICATION', 'BLOCK',
  'Penicillin in a penicillin-allergic patient', 'পেনিসিলিন-অ্যালার্জি রোগীতে পেনিসিলিন',
  'This patient has a recorded penicillin allergy. Do not give amoxicillin or any other penicillin.',
  'রোগীর নথিতে পেনিসিলিন অ্যালার্জি আছে। অ্যামোক্সিসিলিন বা অন্য কোনো পেনিসিলিন দেবেন না।',
  'Use a macrolide, or a cephalosporin if the recorded reaction was not anaphylaxis. Most reported penicillin allergy is not allergy — around 90% tolerate it on formal testing — so it is worth having the label checked rather than avoiding the drug for life.',
  'ম্যাক্রোলাইড দিন, অথবা নথিভুক্ত প্রতিক্রিয়া অ্যানাফাইল্যাক্সিস না হলে সেফালোস্পোরিন। বেশিরভাগ ক্ষেত্রেই পেনিসিলিন-অ্যালার্জি প্রকৃত অ্যালার্জি নয় — আনুষ্ঠানিক পরীক্ষায় প্রায় 90% সহ্য করতে পারেন — তাই সারাজীবন এড়িয়ে যাওয়ার বদলে পরীক্ষা করানোই ভালো।',
  'AAAAI/ACAAI Drug Allergy Practice Parameter 2022; Shenoy ES et al., JAMA 2019;321:188; BNF 88, penicillins.',
  '{"subject":{"match":"CLASS","classes":["PENICILLIN_ANTIBIOTIC"]},"when":[{"kind":"ALLERGY","allergen_group":"PENICILLIN","cross_reactive":false}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'CEPH-PEN-ALLERGY', 'CONTRAINDICATION', 'WARN',
  'Cephalosporin in a penicillin-allergic patient', 'পেনিসিলিন-অ্যালার্জি রোগীতে সেফালোস্পোরিন',
  'This patient reports a penicillin allergy. Cross-reaction with a cephalosporin is around 2% overall and close to zero where the side chains differ, but a documented anaphylaxis still warrants caution.',
  'রোগী পেনিসিলিন অ্যালার্জির কথা বলেছেন। সেফালোস্পোরিনের সঙ্গে ক্রস-রিঅ্যাকশন সামগ্রিকভাবে প্রায় 2%, আর সাইড-চেইন আলাদা হলে প্রায় শূন্য; তবে নথিভুক্ত অ্যানাফাইল্যাক্সিস থাকলে সাবধানতা দরকার।',
  'Check what the recorded reaction actually was. A childhood rash is not a reason to withhold a cephalosporin; anaphylaxis is.',
  'নথিভুক্ত প্রতিক্রিয়াটি আসলে কী ছিল দেখুন। ছোটবেলার র‍্যাশ সেফালোস্পোরিন না দেওয়ার কারণ নয়; অ্যানাফাইল্যাক্সিস কারণ।',
  'Zagursky RJ, Pichichero ME, J Allergy Clin Immunol Pract 2018;6:72; AAAAI/ACAAI Drug Allergy Practice Parameter 2022.',
  '{"subject":{"match":"CLASS","classes":["PENICILLIN_ANTIBIOTIC"]},"when":[{"kind":"ALLERGY","allergen_group":"CEPHALOSPORIN","cross_reactive":true}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'DUP-RAAS', 'DUPLICATE_THERAPY', 'WARN',
  'Two drugs acting on the renin-angiotensin system', 'রেনিন-অ্যাঞ্জিওটেনসিন তন্ত্রে কাজ করা দুটি ওষুধ',
  'Two ACE inhibitors, two ARBs, or an ACE inhibitor with an ARB. Dual blockade gives no extra cardiovascular or renal benefit and roughly doubles the rate of hyperkalaemia, acute kidney injury and symptomatic hypotension.',
  'দুটি ACE ইনহিবিটর, দুটি ARB, অথবা একটি ACE ইনহিবিটরের সঙ্গে একটি ARB। দ্বৈত অবরোধে হৃদযন্ত্র বা কিডনির বাড়তি কোনো লাভ হয় না, বরং হাইপারক্যালেমিয়া, তীব্র কিডনি বিকলতা ও রক্তচাপ পড়ে যাওয়ার হার প্রায় দ্বিগুণ হয়।',
  'Keep one. If blood pressure needs more, add a calcium channel blocker or a thiazide.',
  'একটিই রাখুন। রক্তচাপ আরও কমাতে হলে ক্যালসিয়াম চ্যানেল ব্লকার বা থায়াজাইড যোগ করুন।',
  'ONTARGET, N Engl J Med 2008;358:1547; VA NEPHRON-D, N Engl J Med 2013;369:1892; ALTITUDE, N Engl J Med 2012;367:2204; KDIGO 2024 CKD, recommendation 3.5.2.',
  '{"subject":{"match":"CLASS","classes":["ACE_INHIBITOR","ANGIOTENSIN_II_RECEPTOR_BLOCKER","ARB_CCB_FDC","ARB_THIAZIDE_FDC"]},"when":[{"kind":"DUPLICATE","with":{"match":"CLASS"},"current_medications":true}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'RAAS-CROSS-DUP', 'INTERACTION', 'WARN',
  'ACE inhibitor with an ARB', 'ACE ইনহিবিটরের সঙ্গে ARB',
  'An ACE inhibitor and an angiotensin receptor blocker together. The two classes are different, so a duplicate-therapy check by class does not see it; the clinical objection is the same as for two of either.',
  'একটি ACE ইনহিবিটর ও একটি অ্যাঞ্জিওটেনসিন রিসেপ্টর ব্লকার একসঙ্গে। শ্রেণি দুটি আলাদা বলে শ্রেণিভিত্তিক পুনরাবৃত্তি-পরীক্ষায় এটি ধরা পড়ে না; আপত্তিটি একই।',
  'Keep one. Check potassium and creatinine if the patient has already been taking both.',
  'একটিই রাখুন। রোগী আগে থেকেই দুটোই খেয়ে থাকলে পটাশিয়াম ও ক্রিয়েটিনিন দেখুন।',
  'ONTARGET, N Engl J Med 2008;358:1547; VA NEPHRON-D, N Engl J Med 2013;369:1892.',
  '{"subject":{"match":"CLASS","classes":["ACE_INHIBITOR"]},"when":[{"kind":"CO_PRESCRIBED","with":{"match":"CLASS","classes":["ANGIOTENSIN_II_RECEPTOR_BLOCKER","ARB_CCB_FDC","ARB_THIAZIDE_FDC"]},"current_medications":true}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'SGLT2-MYCOTIC', 'CONTRAINDICATION', 'WARN',
  'SGLT2 inhibitor after a genital fungal infection', 'জননাঙ্গের ছত্রাক সংক্রমণের পর SGLT2 ইনহিবিটর',
  'An SGLT2 inhibitor works by putting glucose into the urine, which is what causes the genital mycotic infections these drugs are known for. In a patient who has already had one, the recurrence rate is high — and in this climate, high enough that patients stop the drug themselves and do not say so.',
  'SGLT2 ইনহিবিটর প্রস্রাবে গ্লুকোজ বের করে কাজ করে, আর সেটিই এই ওষুধে জননাঙ্গের ছত্রাক সংক্রমণের কারণ। যাঁর একবার হয়েছে, তাঁর আবার হওয়ার হার বেশি — এবং এই আবহাওয়ায় এত বেশি যে রোগী নিজেই ওষুধ বন্ধ করে দেন, বলেন না।',
  'Explain the risk and how to reduce it — genital hygiene, and telling you early rather than stopping the tablet. Treat the infection first if it is active. A DPP-4 inhibitor is the usual alternative if it recurs.',
  'ঝুঁকিটি এবং কীভাবে কমানো যায় তা বুঝিয়ে বলুন — পরিচ্ছন্নতা, এবং ওষুধ বন্ধ না করে আগেভাগে আপনাকে জানানো। সংক্রমণ চলতে থাকলে আগে তার চিকিৎসা করুন। বারবার হলে সাধারণত DPP-4 ইনহিবিটর বিকল্প।',
  'ADA Standards of Care in Diabetes 2025 s9; dapagliflozin and empagliflozin EU SmPCs, special warnings; McGovern AP et al., Diabetes Ther 2018;9:1755.',
  '{"subject":{"match":"CLASS","classes":["SGLT2_INHIBITOR","SGLT2_INHIBITOR_BIGUANIDE_FDC"]},"when":[{"kind":"DIAGNOSIS","diagnosis_codes":["B37.3","B37.4"]}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'NSAID-CKD-RAAS', 'INTERACTION', 'WARN',
  'NSAID with an ACE inhibitor or ARB', 'ACE ইনহিবিটর বা ARB-এর সঙ্গে NSAID',
  'An NSAID on top of an ACE inhibitor or ARB removes the kidney''s last compensation for a fall in perfusion. With a diuretic as well it is the "triple whammy", and it is a leading cause of acute-on-chronic kidney injury in exactly this clinic''s patients.',
  'ACE ইনহিবিটর বা ARB-এর উপর NSAID যোগ হলে কিডনির শেষ ক্ষতিপূরণের পথটিও বন্ধ হয়ে যায়। সঙ্গে মূত্রবর্ধক থাকলে একে "ট্রিপল হোয়ামি" বলে, আর এই ক্লিনিকের রোগীদের মধ্যে তীব্র কিডনি বিকলতার এটি একটি প্রধান কারণ।',
  'Use paracetamol instead. If an NSAID is unavoidable, keep it short, check creatinine within a week, and tell the patient to stop it if they cannot drink normally.',
  'বদলে প্যারাসিটামল দিন। NSAID অনিবার্য হলে অল্প দিনের জন্য দিন, এক সপ্তাহের মধ্যে ক্রিয়েটিনিন দেখুন, আর রোগীকে বলুন পানি ঠিকমতো খেতে না পারলে ওষুধটি বন্ধ করতে।',
  'Lapi F et al., BMJ 2013;346:e8525 (triple whammy cohort); KDIGO 2024 CKD Evaluation and Management, s3.10; NICE CKD guideline NG203.',
  '{"subject":{"match":"CLASS","classes":["NSAID"]},"when":[{"kind":"CO_PRESCRIBED","with":{"match":"CLASS","classes":["ACE_INHIBITOR","ANGIOTENSIN_II_RECEPTOR_BLOCKER","ARB_CCB_FDC","ARB_THIAZIDE_FDC"]},"current_medications":true}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'NSAID-RENAL-60', 'RENAL', 'WARN',
  'NSAID below eGFR 60', 'eGFR 60-এর নিচে NSAID',
  'An NSAID below an eGFR of 60 mL/min/1.73m2 can cause a further, sometimes permanent, fall in kidney function.',
  'eGFR 60 mL/min/1.73m2-এর নিচে NSAID দিলে কিডনির কার্যকারিতা আরও কমতে পারে, কখনো তা স্থায়ী হয়।',
  'Paracetamol. Below an eGFR of 30 an NSAID should be avoided altogether.',
  'প্যারাসিটামল দিন। eGFR 30-এর নিচে NSAID একেবারেই এড়িয়ে চলা উচিত।',
  'KDIGO 2024 CKD Evaluation and Management, s3.10; BNF 88, NSAIDs in renal impairment.',
  '{"subject":{"match":"CLASS","classes":["NSAID"]},"when":[{"kind":"EGFR","operator":"LT","value":60,"unit":"mL/min/1.73m2"}]}'::jsonb) FROM core.facility f;

SELECT core.seed_medication_rule(f.id, 'MET-CKD-DIAGNOSIS', 'CONTRAINDICATION', 'WARN',
  'Metformin with advanced chronic kidney disease coded', 'উন্নত দীর্ঘমেয়াদি বৃক্করোগে মেটফরমিন',
  'The problem list carries stage 4 or stage 5 chronic kidney disease. Metformin should not be started at that level of function, whatever the last eGFR happened to say — a single reading can be flattering after a day of good hydration.',
  'সমস্যার তালিকায় পর্যায় 4 বা 5 দীর্ঘমেয়াদি বৃক্করোগ রয়েছে। শেষ eGFR যাই বলুক, এই মাত্রার কিডনি কার্যকারিতায় মেটফরমিন শুরু করা উচিত নয় — ভালোভাবে পানি খাওয়ার পরের একটি রিপোর্ট প্রকৃত অবস্থার চেয়ে ভালো দেখাতে পারে।',
  'Confirm with a current creatinine. If the stage is right, stop metformin; linagliptin needs no dose change at any eGFR.',
  'বর্তমান ক্রিয়েটিনিন দিয়ে নিশ্চিত হন। পর্যায় ঠিক থাকলে মেটফরমিন বন্ধ করুন; যেকোনো eGFR-এ লিনাগ্লিপটিনের মাত্রা বদলাতে হয় না।',
  'KDIGO 2022 Diabetes Management in CKD, recommendation 1.3; US FDA metformin labelling, 2016 revision.',
  '{"subject":{"match":"GENERIC","generics":["empagliflozin + metformin hydrochloride","glimepiride + metformin hydrochloride","linagliptin + metformin hydrochloride","metformin hydrochloride","pioglitazone + metformin hydrochloride","sitagliptin + metformin hydrochloride","vildagliptin + metformin hydrochloride"]},"when":[{"kind":"DIAGNOSIS","diagnosis_codes":["N18.4","N18.5"]}]}'::jsonb) FROM core.facility f;

-- ---------------------------------------------------------------------------
-- 4. The invariant CP78 found it needed
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
-- **A rule that names a molecule this clinic does not hold never fires, and nothing says so.**
--
-- CP77 said exactly this about diagnosis codes and added five of them with its rules. The same
-- failure exists one field along, on the subject: `Condition.Validate` checks every generic and
-- every class against the formulary when a rule is *saved through the API*, and the seeds go in
-- as raw SQL and never meet it. So a seeded rule with a typo in a molecule name — or one written
-- against a molecule a later migration renamed — is inert in the most dangerous possible way: it
-- is listed in the library, it is approvable, a physician approves it believing it protects
-- somebody, and it silently matches nothing for ever.
--
-- CP78 is where that stops being theoretical, because CP78 is the thing that would have been
-- believed. This runs over every version of every rule, draft and published alike, after every
-- migration in every environment — which is the only place that catches a rename.
--
-- Written against `jsonb_array_elements_text` rather than a Go pass, for the reason every
-- invariant in this system is: a check that only runs when the application is running is a check
-- that does not run on a restore, a hand edit, or a migration written by somebody who never read
-- this file.
CREATE OR REPLACE FUNCTION core.assert_every_rule_names_a_medicine_this_clinic_holds() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  -- Molecules, from the subject and from every CO_PRESCRIBED or DUPLICATE predicate.
  SELECT string_agg(DISTINCT r.code || ' → ' || named, ', ') INTO offender
    FROM core.medication_rule r
    JOIN core.medication_rule_version v ON v.rule_id = r.id
    CROSS JOIN LATERAL (
      SELECT jsonb_array_elements_text(v.condition -> 'subject' -> 'generics') AS named
      UNION ALL
      SELECT jsonb_array_elements_text(w -> 'with' -> 'generics')
        FROM jsonb_array_elements(v.condition -> 'when') AS w
    ) AS names
   WHERE NOT EXISTS (
     SELECT 1 FROM core.generic g WHERE lower(g.name) = lower(names.named));
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'these rules name a molecule this clinic''s formulary does not hold: %',
      offender
      USING HINT = 'A rule keyed to a molecule nobody can prescribe never fires, and a physician approving it believes it protects somebody.';
  END IF;

  -- Classes, the same two places.
  SELECT string_agg(DISTINCT r.code || ' → ' || named, ', ') INTO offender
    FROM core.medication_rule r
    JOIN core.medication_rule_version v ON v.rule_id = r.id
    CROSS JOIN LATERAL (
      SELECT jsonb_array_elements_text(v.condition -> 'subject' -> 'classes') AS named
      UNION ALL
      SELECT jsonb_array_elements_text(w -> 'with' -> 'classes')
        FROM jsonb_array_elements(v.condition -> 'when') AS w
    ) AS names
   WHERE NOT EXISTS (
     SELECT 1 FROM core.medication_class c WHERE c.code = names.named);
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'these rules name a therapeutic class this clinic''s formulary does not have: %',
      offender;
  END IF;

  -- Allergen groups, from every ALLERGY predicate. Same failure: a rule about a group nobody
  -- can record is a rule that cannot fire.
  SELECT string_agg(DISTINCT r.code || ' → ' || named, ', ') INTO offender
    FROM core.medication_rule r
    JOIN core.medication_rule_version v ON v.rule_id = r.id
    CROSS JOIN LATERAL (
      SELECT w ->> 'allergen_group' AS named
        FROM jsonb_array_elements(v.condition -> 'when') AS w
       WHERE w ->> 'allergen_group' IS NOT NULL
    ) AS names
   WHERE NOT EXISTS (
     SELECT 1 FROM core.allergen_group a WHERE a.code = names.named);
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'these rules name an allergen group that does not exist: %', offender;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_rule_names_a_medicine_this_clinic_holds',
   'no medication rule is keyed to a molecule, class or allergen group this clinic does not have — such a rule never fires and nothing says so', 119)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant
 WHERE function_name = 'assert_every_rule_names_a_medicine_this_clinic_holds';
DROP FUNCTION IF EXISTS core.assert_every_rule_names_a_medicine_this_clinic_holds();


-- The rules go; the molecules, classes and ICD codes stay.
--
-- A generic may already have a product against it, a price history and a prescription written
-- from it, and an ICD-10 code may already be on somebody's problem list. None of that is this
-- migration's to remove on the way out — the same reasoning CP77 gave for leaving its five codes
-- behind.
DELETE FROM core.medication_rule_version v
 USING core.medication_rule r
 WHERE v.rule_id = r.id
   AND r.code IN ('PEN-ALLERGY', 'CEPH-PEN-ALLERGY', 'DUP-RAAS', 'RAAS-CROSS-DUP',
                  'SGLT2-MYCOTIC', 'NSAID-CKD-RAAS', 'NSAID-RENAL-60', 'MET-CKD-DIAGNOSIS');
DELETE FROM core.medication_rule
 WHERE code IN ('PEN-ALLERGY', 'CEPH-PEN-ALLERGY', 'DUP-RAAS', 'RAAS-CROSS-DUP',
                'SGLT2-MYCOTIC', 'NSAID-CKD-RAAS', 'NSAID-RENAL-60', 'MET-CKD-DIAGNOSIS');
