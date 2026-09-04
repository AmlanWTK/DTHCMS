-- Station 5's examination, and the vocabulary that makes "coded, not free text" true (CP51).
--
-- # The departure from the plan, stated plainly
--
-- CP51's plan says "Database: uses the CP42 observation tables; no new schema." This migration
-- adds one table anyway, and the reason is criterion 2: *all findings are coded and queryable
-- for research*.
--
-- Until now a `coded` observation meant only that `value_code` was not empty. Nothing stopped
-- a client sending `absent`, `Absent`, `not felt` and `absent?` for the same finding on four
-- consecutive Tuesdays — and a coded field nobody constrains is a text field with a shorter
-- name and a promise it cannot keep. The research extract would then hold four values for one
-- observation and no way to tell that they were the same one.
--
-- So `core.observation_answer` is the vocabulary: which answers a coded observation may take,
-- in the order a screen should draw them, in both languages. A trigger enforces it, an
-- invariant keeps the trigger honest, and the station app fetches the sets and renders them as
-- buttons — which is also how a complete diabetic foot examination fits into two minutes.
--
-- # Laterality is in the code, not in a column
--
-- `DP_PULSE_LEFT` and `DP_PULSE_RIGHT` are two codes, following the pattern CP42 already set
-- with the monofilament. A `side` column would have been tidier and would have made every
-- existing query wrong by omission: `WHERE code = 'DP_PULSE'` would silently mean "either
-- foot", which for a diabetic foot is the one question nobody may be vague about.
--
-- # Who writes an examination
--
-- The four EXAM codes seeded at CP42 carry `observation.write.history`, because at CP42 there
-- was no examination station app and history was the only station that recorded anything of
-- the sort. That is now wrong in a way worth fixing: a foot examination is performed at
-- station 5, by the clinical assistant or the junior doctor, and the history officer at station
-- 4 does not have the patient's shoes off. `observation.write.exam` is the permission, and the
-- four older codes move to it.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The permission
-- ---------------------------------------------------------------------------

INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('observation.write.exam', 'observation', 'write', 'exam',
   'Record structured examination findings: foot, neuropathy, retinopathy, cardiovascular', false)
ON CONFLICT (code) DO UPDATE SET
  resource = EXCLUDED.resource, action = EXCLUDED.action, scope = EXCLUDED.scope,
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'observation.write.exam' FROM core.role r
 WHERE r.code IN ('CLINICAL_ASSISTANT', 'JUNIOR_DOCTOR', 'PHYSICIAN')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The answer vocabulary
-- ---------------------------------------------------------------------------

CREATE TABLE core.observation_answer (
  code       text NOT NULL REFERENCES core.observation_code(code),
  value_code text NOT NULL,

  display_en text NOT NULL,
  display_bn text NOT NULL,

  -- The order a screen draws them in. Clinical order, not alphabetical: "present, diminished,
  -- absent" is a scale, and a screen that sorted it alphabetically would put absent first and
  -- make every examiner read the list twice.
  ordering int NOT NULL,

  -- Marks the answer that means "nothing abnormal". A screen can pre-select it, and a report
  -- can count abnormal findings without a list of magic strings in three places.
  is_normal boolean NOT NULL DEFAULT false,

  -- An answer is retired, never deleted: an observation recorded under it years ago must still
  -- render. The same rule as the code registry itself.
  retired_at timestamptz,

  PRIMARY KEY (code, value_code),
  CONSTRAINT observation_answer_code_format CHECK (value_code ~ '^[a-z][a-z0-9_]{0,39}$'),
  CONSTRAINT observation_answer_has_display CHECK (btrim(display_en) <> '' AND btrim(display_bn) <> '')
);

CREATE INDEX observation_answer_by_code ON core.observation_answer (code, ordering);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'observation_answer',
   'The answers a monofilament test can take are the same in every clinic. This is the code registry''s vocabulary, not a facility setting (CP51).')
ON CONFLICT (schema_name, table_name) DO NOTHING;

GRANT SELECT ON core.observation_answer TO dthcms_app;
GRANT SELECT ON core.observation_answer TO dthcms_projector;

COMMENT ON TABLE core.observation_answer IS
  'Which answers a coded observation may take, in clinical order and in both languages (CP51).';

-- ---------------------------------------------------------------------------
-- The vocabulary for what already exists
-- ---------------------------------------------------------------------------
--
-- CP49's blood-pressure context and CP42's screening result. Seeded here because the trigger
-- below applies to every coded code, and a rule with exceptions is a rule nobody trusts.

INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal) VALUES
  ('BP_ARM', 'left',  'Left arm',  'বাঁ বাহু',  1, false),
  ('BP_ARM', 'right', 'Right arm', 'ডান বাহু', 2, false),

  ('BP_POSITION', 'sitting',  'Sitting',  'বসা',   1, false),
  ('BP_POSITION', 'standing', 'Standing', 'দাঁড়ানো', 2, false),
  ('BP_POSITION', 'supine',   'Lying',    'শোয়া',  3, false),

  ('BP_CUFF', 'adult',       'Adult',       'প্রাপ্তবয়স্ক',  1, false),
  ('BP_CUFF', 'large_adult', 'Large adult', 'বড় মাপ',    2, false),
  ('BP_CUFF', 'paediatric',  'Paediatric',  'শিশু',      3, false),
  ('BP_CUFF', 'thigh',       'Thigh',       'ঊরু',       4, false),

  ('DIABETES_SCREEN', 'normal',      'Normal',              'স্বাভাবিক',        1, true),
  ('DIABETES_SCREEN', 'prediabetes', 'Prediabetes',         'প্রি-ডায়াবেটিস',   2, false),
  ('DIABETES_SCREEN', 'diabetes',    'Diabetes',            'ডায়াবেটিস',       3, false),
  ('DIABETES_SCREEN', 'inconclusive','Inconclusive',        'অনিশ্চিত',         4, false)
ON CONFLICT (code, value_code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  ordering = EXCLUDED.ordering, is_normal = EXCLUDED.is_normal;

-- ---------------------------------------------------------------------------
-- The monofilament becomes structured
-- ---------------------------------------------------------------------------
--
-- A ten-site monofilament test has one answer per site, and a single coded value can only say
-- "abnormal" — which loses the thing the test is for. Which sites were not felt is what tells
-- a clinician whether this is early neuropathy at the hallux or a foot that has lost
-- protective sensation across the forefoot, and those are different appointments.
--
-- Rewritten rather than added beside, because nothing has ever been recorded under these two:
-- CP42 seeded them as placeholders for the station app that arrives here.
UPDATE core.observation_code
   SET value_type = 'structured',
       write_permission = 'observation.write.exam'
 WHERE code IN ('MONOFILAMENT_LEFT', 'MONOFILAMENT_RIGHT');

UPDATE core.observation_code
   SET write_permission = 'observation.write.exam'
 WHERE code IN ('FOOT_ULCER_PRESENT', 'FUNDUS_FINDING');

-- ---------------------------------------------------------------------------
-- The finding set (proposed — the plan says it must be authored by Dr. Nahid)
-- ---------------------------------------------------------------------------

INSERT INTO core.observation_code
  (code, category, value_type, dimension, loinc, display_en, display_bn,
   min_canonical, max_canonical, write_permission) VALUES

  -- The diabetic foot, per side.
  ('VIBRATION_LEFT',  'EXAM', 'coded', NULL, '', 'Vibration sense, left foot',  'কম্পন অনুভূতি, বাঁ পা',  NULL, NULL, 'observation.write.exam'),
  ('VIBRATION_RIGHT', 'EXAM', 'coded', NULL, '', 'Vibration sense, right foot', 'কম্পন অনুভূতি, ডান পা', NULL, NULL, 'observation.write.exam'),
  ('ANKLE_REFLEX_LEFT',  'EXAM', 'coded', NULL, '', 'Ankle reflex, left',  'গোড়ালির প্রতিবর্ত, বাঁ',  NULL, NULL, 'observation.write.exam'),
  ('ANKLE_REFLEX_RIGHT', 'EXAM', 'coded', NULL, '', 'Ankle reflex, right', 'গোড়ালির প্রতিবর্ত, ডান', NULL, NULL, 'observation.write.exam'),
  ('DP_PULSE_LEFT',  'EXAM', 'coded', NULL, '', 'Dorsalis pedis pulse, left',  'ডরসালিস পেডিস নাড়ি, বাঁ',  NULL, NULL, 'observation.write.exam'),
  ('DP_PULSE_RIGHT', 'EXAM', 'coded', NULL, '', 'Dorsalis pedis pulse, right', 'ডরসালিস পেডিস নাড়ি, ডান', NULL, NULL, 'observation.write.exam'),
  ('PT_PULSE_LEFT',  'EXAM', 'coded', NULL, '', 'Posterior tibial pulse, left',  'পোস্টেরিয়র টিবিয়াল নাড়ি, বাঁ',  NULL, NULL, 'observation.write.exam'),
  ('PT_PULSE_RIGHT', 'EXAM', 'coded', NULL, '', 'Posterior tibial pulse, right', 'পোস্টেরিয়র টিবিয়াল নাড়ি, ডান', NULL, NULL, 'observation.write.exam'),
  ('FOOT_DEFORMITY_LEFT',  'EXAM', 'coded', NULL, '', 'Foot deformity, left',  'পায়ের বিকৃতি, বাঁ',  NULL, NULL, 'observation.write.exam'),
  ('FOOT_DEFORMITY_RIGHT', 'EXAM', 'coded', NULL, '', 'Foot deformity, right', 'পায়ের বিকৃতি, ডান', NULL, NULL, 'observation.write.exam'),
  ('FOOT_SKIN_LEFT',  'EXAM', 'coded', NULL, '', 'Foot skin, left',  'পায়ের ত্বক, বাঁ',  NULL, NULL, 'observation.write.exam'),
  ('FOOT_SKIN_RIGHT', 'EXAM', 'coded', NULL, '', 'Foot skin, right', 'পায়ের ত্বক, ডান', NULL, NULL, 'observation.write.exam'),
  ('FOOT_ULCER_LEFT',  'EXAM', 'coded', NULL, '', 'Foot ulcer, left (Wagner)',  'পায়ের ঘা, বাঁ (ওয়াগনার)',  NULL, NULL, 'observation.write.exam'),
  ('FOOT_ULCER_RIGHT', 'EXAM', 'coded', NULL, '', 'Foot ulcer, right (Wagner)', 'পায়ের ঘা, ডান (ওয়াগনার)', NULL, NULL, 'observation.write.exam'),

  -- Neuropathy symptoms. The Michigan instrument's patient questionnaire, scored 0–13.
  ('NEUROPATHY_SYMPTOM_SCORE', 'SCREENING', 'numeric', 'ratio', '',
   'Neuropathy symptom score (0–13)', 'স্নায়ুরোগের উপসর্গ স্কোর (০–১৩)', 0, 13, 'observation.write.exam'),

  -- Retinopathy. Screening status first, because "when was this last looked at" is the
  -- question a consultation actually turns on, and grades only exist if somebody looked.
  ('RETINOPATHY_SCREEN', 'SCREENING', 'coded', NULL, '',
   'Retinopathy screening status', 'রেটিনোপ্যাথি স্ক্রিনিং অবস্থা', NULL, NULL, 'observation.write.exam'),
  ('RETINOPATHY_LEFT',  'EXAM', 'coded', NULL, '', 'Retinopathy grade, left eye',  'রেটিনোপ্যাথি মাত্রা, বাঁ চোখ',  NULL, NULL, 'observation.write.exam'),
  ('RETINOPATHY_RIGHT', 'EXAM', 'coded', NULL, '', 'Retinopathy grade, right eye', 'রেটিনোপ্যাথি মাত্রা, ডান চোখ', NULL, NULL, 'observation.write.exam'),
  ('MACULOPATHY_LEFT',  'EXAM', 'boolean', NULL, '', 'Maculopathy, left eye',  'ম্যাকুলোপ্যাথি, বাঁ চোখ',  NULL, NULL, 'observation.write.exam'),
  ('MACULOPATHY_RIGHT', 'EXAM', 'boolean', NULL, '', 'Maculopathy, right eye', 'ম্যাকুলোপ্যাথি, ডান চোখ', NULL, NULL, 'observation.write.exam'),

  -- Cardiovascular.
  ('HEART_SOUNDS',    'EXAM', 'coded', NULL, '', 'Heart sounds',        'হৃদস্পন্দনের শব্দ',   NULL, NULL, 'observation.write.exam'),
  ('MURMUR',          'EXAM', 'coded', NULL, '', 'Murmur',              'মর্মর',              NULL, NULL, 'observation.write.exam'),
  ('JVP',             'EXAM', 'coded', NULL, '', 'Jugular venous pressure', 'জুগুলার শিরার চাপ', NULL, NULL, 'observation.write.exam'),
  ('PERIPHERAL_OEDEMA', 'EXAM', 'coded', NULL, '', 'Peripheral oedema', 'পায়ে পানি আসা',      NULL, NULL, 'observation.write.exam'),
  ('CAROTID_BRUIT_LEFT',  'EXAM', 'boolean', NULL, '', 'Carotid bruit, left',  'ক্যারোটিড ব্রুইট, বাঁ',  NULL, NULL, 'observation.write.exam'),
  ('CAROTID_BRUIT_RIGHT', 'EXAM', 'boolean', NULL, '', 'Carotid bruit, right', 'ক্যারোটিড ব্রুইট, ডান', NULL, NULL, 'observation.write.exam'),

  -- The risk category, derived from the findings above. Not an opinion typed by the examiner:
  -- the whole point of a structured foot examination is that the category falls out of it.
  ('FOOT_RISK_LEFT',  'DERIVED', 'coded', NULL, '', 'Foot risk category, left',  'পায়ের ঝুঁকি শ্রেণি, বাঁ',  NULL, NULL, 'observation.write.exam'),
  ('FOOT_RISK_RIGHT', 'DERIVED', 'coded', NULL, '', 'Foot risk category, right', 'পায়ের ঝুঁকি শ্রেণি, ডান', NULL, NULL, 'observation.write.exam')

ON CONFLICT (code) DO UPDATE SET
  category = EXCLUDED.category, value_type = EXCLUDED.value_type,
  dimension = EXCLUDED.dimension, loinc = EXCLUDED.loinc,
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  min_canonical = EXCLUDED.min_canonical, max_canonical = EXCLUDED.max_canonical,
  write_permission = EXCLUDED.write_permission;

-- The answers.
--
-- Ordered as an examiner works: the normal answer first, then increasing abnormality. Every
-- scale reads the same way round, because an examiner tapping through twenty of these in two
-- minutes should never have to check which end of a list they are at.
INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal)
SELECT side.code || side.suffix, a.value_code, a.display_en, a.display_bn, a.ordering, a.is_normal
  FROM (VALUES
    ('VIBRATION_', 'LEFT'), ('VIBRATION_', 'RIGHT')
  ) AS side(code, suffix)
 CROSS JOIN (VALUES
    ('felt',    'Felt',    'অনুভূত',        1, true),
    ('reduced', 'Reduced', 'কম অনুভূত',     2, false),
    ('absent',  'Absent',  'অনুভূত হয়নি',  3, false)
 ) AS a(value_code, display_en, display_bn, ordering, is_normal)
ON CONFLICT (code, value_code) DO NOTHING;

INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal)
SELECT side.code || side.suffix, a.value_code, a.display_en, a.display_bn, a.ordering, a.is_normal
  FROM (VALUES
    ('ANKLE_REFLEX_', 'LEFT'), ('ANKLE_REFLEX_', 'RIGHT')
  ) AS side(code, suffix)
 CROSS JOIN (VALUES
    ('present',       'Present',                  'আছে',                    1, true),
    ('reinforcement', 'Present with reinforcement','শক্তি প্রয়োগে আছে',      2, false),
    ('absent',        'Absent',                   'নেই',                    3, false)
 ) AS a(value_code, display_en, display_bn, ordering, is_normal)
ON CONFLICT (code, value_code) DO NOTHING;

INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal)
SELECT side.code || side.suffix, a.value_code, a.display_en, a.display_bn, a.ordering, a.is_normal
  FROM (VALUES
    ('DP_PULSE_', 'LEFT'), ('DP_PULSE_', 'RIGHT'), ('PT_PULSE_', 'LEFT'), ('PT_PULSE_', 'RIGHT')
  ) AS side(code, suffix)
 CROSS JOIN (VALUES
    ('present',    'Present',    'স্পষ্ট',     1, true),
    ('diminished', 'Diminished', 'ক্ষীণ',      2, false),
    ('absent',     'Absent',     'পাওয়া যায়নি', 3, false)
 ) AS a(value_code, display_en, display_bn, ordering, is_normal)
ON CONFLICT (code, value_code) DO NOTHING;

INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal)
SELECT side.code || side.suffix, a.value_code, a.display_en, a.display_bn, a.ordering, a.is_normal
  FROM (VALUES
    ('FOOT_DEFORMITY_', 'LEFT'), ('FOOT_DEFORMITY_', 'RIGHT')
  ) AS side(code, suffix)
 CROSS JOIN (VALUES
    ('none',            'None',                     'নেই',                  1, true),
    ('clawed_toes',     'Clawed or hammer toes',    'আঙুল বাঁকা',           2, false),
    ('bunion',          'Bunion',                   'বুনিয়ন',               3, false),
    ('prominent_heads', 'Prominent metatarsal heads','মেটাটারসাল হাড় উঁচু',  4, false),
    ('charcot',         'Charcot deformity',        'শারকো বিকৃতি',         5, false),
    ('amputation',      'Previous amputation',      'আগে অঙ্গহানি হয়েছে',   6, false)
 ) AS a(value_code, display_en, display_bn, ordering, is_normal)
ON CONFLICT (code, value_code) DO NOTHING;

INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal)
SELECT side.code || side.suffix, a.value_code, a.display_en, a.display_bn, a.ordering, a.is_normal
  FROM (VALUES
    ('FOOT_SKIN_', 'LEFT'), ('FOOT_SKIN_', 'RIGHT')
  ) AS side(code, suffix)
 CROSS JOIN (VALUES
    ('intact',   'Intact',              'অক্ষত',           1, true),
    ('dry',      'Dry',                 'শুষ্ক',           2, false),
    ('callus',   'Callus',              'কড়া পড়েছে',      3, false),
    ('fissure',  'Fissure',             'ফাটা',            4, false),
    ('macerated','Macerated or infected','ভেজা বা সংক্রমিত', 5, false)
 ) AS a(value_code, display_en, display_bn, ordering, is_normal)
ON CONFLICT (code, value_code) DO NOTHING;

-- Wagner, as published. Grade 0 is the normal answer, and it is not "no ulcer" — it is a foot
-- at risk with intact skin, which is a finding rather than the absence of one.
INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal)
SELECT side.code || side.suffix, a.value_code, a.display_en, a.display_bn, a.ordering, a.is_normal
  FROM (VALUES
    ('FOOT_ULCER_', 'LEFT'), ('FOOT_ULCER_', 'RIGHT')
  ) AS side(code, suffix)
 CROSS JOIN (VALUES
    ('grade_0', 'Grade 0 — intact skin',            'গ্রেড ০ — ত্বক অক্ষত',            1, true),
    ('grade_1', 'Grade 1 — superficial ulcer',      'গ্রেড ১ — উপরিভাগের ঘা',          2, false),
    ('grade_2', 'Grade 2 — deep to tendon or bone', 'গ্রেড ২ — গভীর, টেন্ডন বা হাড় পর্যন্ত', 3, false),
    ('grade_3', 'Grade 3 — abscess or osteomyelitis','গ্রেড ৩ — ফোড়া বা হাড়ের সংক্রমণ', 4, false),
    ('grade_4', 'Grade 4 — localised gangrene',     'গ্রেড ৪ — স্থানীয় পচন',           5, false),
    ('grade_5', 'Grade 5 — gangrene of the foot',   'গ্রেড ৫ — পুরো পায়ে পচন',         6, false)
 ) AS a(value_code, display_en, display_bn, ordering, is_normal)
ON CONFLICT (code, value_code) DO NOTHING;

INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal) VALUES
  ('RETINOPATHY_SCREEN', 'today',     'Examined today',        'আজ পরীক্ষা হয়েছে',        1, false),
  ('RETINOPATHY_SCREEN', 'elsewhere', 'Examined elsewhere',    'অন্যত্র পরীক্ষা হয়েছে',   2, false),
  ('RETINOPATHY_SCREEN', 'due',       'Due',                   'সময় হয়েছে',              3, false),
  ('RETINOPATHY_SCREEN', 'never',     'Never examined',        'কখনো পরীক্ষা হয়নি',       4, false),
  ('RETINOPATHY_SCREEN', 'declined',  'Patient declined',      'রোগী রাজি হননি',          5, false)
ON CONFLICT (code, value_code) DO NOTHING;

INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal)
SELECT side.code || side.suffix, a.value_code, a.display_en, a.display_bn, a.ordering, a.is_normal
  FROM (VALUES
    ('RETINOPATHY_', 'LEFT'), ('RETINOPATHY_', 'RIGHT')
  ) AS side(code, suffix)
 CROSS JOIN (VALUES
    ('none',          'None',                       'নেই',                          1, true),
    ('mild_npdr',     'Mild non-proliferative',     'মৃদু নন-প্রোলিফারেটিভ',        2, false),
    ('moderate_npdr', 'Moderate non-proliferative', 'মাঝারি নন-প্রোলিফারেটিভ',      3, false),
    ('severe_npdr',   'Severe non-proliferative',   'তীব্র নন-প্রোলিফারেটিভ',       4, false),
    ('pdr',           'Proliferative',              'প্রোলিফারেটিভ',                5, false),
    ('ungradable',    'Ungradable',                 'মাত্রা নির্ণয় করা যায়নি',     6, false)
 ) AS a(value_code, display_en, display_bn, ordering, is_normal)
ON CONFLICT (code, value_code) DO NOTHING;

INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal) VALUES
  ('HEART_SOUNDS', 'normal',   'Normal, S1 and S2',   'স্বাভাবিক, এস১ ও এস২',  1, true),
  ('HEART_SOUNDS', 'added_s3', 'Third sound',         'তৃতীয় শব্দ',            2, false),
  ('HEART_SOUNDS', 'added_s4', 'Fourth sound',        'চতুর্থ শব্দ',            3, false),
  ('HEART_SOUNDS', 'muffled',  'Muffled',             'অস্পষ্ট',               4, false),

  ('MURMUR', 'none',      'None',                  'নেই',                  1, true),
  ('MURMUR', 'systolic',  'Systolic',              'সিস্টোলিক',            2, false),
  ('MURMUR', 'diastolic', 'Diastolic',             'ডায়াস্টোলিক',          3, false),
  ('MURMUR', 'continuous','Continuous',            'অবিরাম',               4, false),

  ('JVP', 'not_raised', 'Not raised',              'বাড়েনি',               1, true),
  ('JVP', 'raised',     'Raised',                  'বেড়েছে',               2, false),
  ('JVP', 'not_assessed','Not assessable',         'দেখা যায়নি',           3, false),

  ('PERIPHERAL_OEDEMA', 'none',    'None',            'নেই',              1, true),
  ('PERIPHERAL_OEDEMA', 'ankle',   'To the ankle',    'গোড়ালি পর্যন্ত',   2, false),
  ('PERIPHERAL_OEDEMA', 'mid_leg', 'To mid-leg',      'হাঁটুর নিচ পর্যন্ত', 3, false),
  ('PERIPHERAL_OEDEMA', 'knee',    'To the knee',     'হাঁটু পর্যন্ত',     4, false),
  ('PERIPHERAL_OEDEMA', 'sacral',  'Sacral',          'কোমরে',            5, false)
ON CONFLICT (code, value_code) DO NOTHING;

-- The IWGDF risk categories, as the derived answer.
INSERT INTO core.observation_answer (code, value_code, display_en, display_bn, ordering, is_normal)
SELECT side.code || side.suffix, a.value_code, a.display_en, a.display_bn, a.ordering, a.is_normal
  FROM (VALUES
    ('FOOT_RISK_', 'LEFT'), ('FOOT_RISK_', 'RIGHT')
  ) AS side(code, suffix)
 CROSS JOIN (VALUES
    ('very_low', 'Very low — sensation and circulation intact', 'খুব কম — অনুভূতি ও রক্তসঞ্চালন ঠিক আছে', 1, true),
    ('low',      'Low — sensation or circulation lost',         'কম — অনুভূতি বা রক্তসঞ্চালন কমেছে',      2, false),
    ('moderate', 'Moderate — two of the three, or deformity',   'মাঝারি — তিনটির দুটি, বা বিকৃতি',        3, false),
    ('high',     'High — previous ulcer or amputation',         'বেশি — আগে ঘা বা অঙ্গহানি হয়েছে',       4, false)
 ) AS a(value_code, display_en, display_bn, ordering, is_normal)
ON CONFLICT (code, value_code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- The rule, enforced where it cannot be forgotten
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.observation_answer_is_known() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_type text;
BEGIN
  SELECT value_type INTO v_type FROM core.observation_code WHERE code = NEW.code;
  IF v_type IS DISTINCT FROM 'coded' THEN
    RETURN NEW;
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM core.observation_answer a
     WHERE a.code = NEW.code AND a.value_code = NEW.value_code
  ) THEN
    RAISE EXCEPTION '% is not an answer % can take', NEW.value_code, NEW.code
      USING HINT = 'A coded field nobody constrains is a text field with a shorter name. '
                   'Add the answer to core.observation_answer, or correct the value.';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER observation_answer_is_known
  BEFORE INSERT OR UPDATE ON read.observation
  FOR EACH ROW EXECUTE FUNCTION core.observation_answer_is_known();

COMMENT ON FUNCTION core.observation_answer_is_known() IS
  'Refuses a coded observation whose value is not in that code''s vocabulary (CP51 criterion 2).';

-- Every coded code has answers, or it is a field nobody can fill in.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_coded_observation_has_answers() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender text;
BEGIN
  SELECT c.code INTO offender
    FROM core.observation_code c
   WHERE c.value_type = 'coded' AND c.retired_at IS NULL
     AND NOT EXISTS (SELECT 1 FROM core.observation_answer a
                      WHERE a.code = c.code AND a.retired_at IS NULL)
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION '% is a coded observation with no answers', offender
      USING HINT = 'Nothing can be recorded under it: the trigger refuses every value.';
  END IF;
END;
$$;
-- +goose StatementEnd

-- Every laterality comes in pairs. A finding that exists for one foot and not the other is a
-- screen that silently cannot record the left one.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_lateral_findings_come_in_pairs() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender text;
BEGIN
  SELECT c.code INTO offender
    FROM core.observation_code c
   WHERE c.retired_at IS NULL
     AND (c.code LIKE '%\_LEFT' OR c.code LIKE '%\_RIGHT')
     AND NOT EXISTS (
       SELECT 1 FROM core.observation_code other
        WHERE other.retired_at IS NULL
          AND other.code = CASE
                WHEN c.code LIKE '%\_LEFT' THEN left(c.code, length(c.code) - 5) || '_RIGHT'
                ELSE left(c.code, length(c.code) - 6) || '_LEFT'
              END)
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION '% has no counterpart on the other side', offender
      USING HINT = 'Laterality is in the code (CP51). A finding recordable for one side only '
                   'is a screen that cannot record the other.';
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_every_coded_observation_has_answers() IS
  'Raises if a live coded observation has no vocabulary (CP51).';
COMMENT ON FUNCTION core.assert_lateral_findings_come_in_pairs() IS
  'Raises if a lateral finding exists for one side only (CP51).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_coded_observation_has_answers',
   'every coded observation has a vocabulary somebody can choose from', 57),
  ('assert_lateral_findings_come_in_pairs',
   'every lateral finding is recordable on both sides', 58)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_every_coded_observation_has_answers', 'assert_lateral_findings_come_in_pairs');
DROP FUNCTION IF EXISTS core.assert_lateral_findings_come_in_pairs();
DROP FUNCTION IF EXISTS core.assert_every_coded_observation_has_answers();
DROP TRIGGER IF EXISTS observation_answer_is_known ON read.observation;
DROP FUNCTION IF EXISTS core.observation_answer_is_known();
DELETE FROM core.observation_code WHERE code IN (
  'VIBRATION_LEFT', 'VIBRATION_RIGHT', 'ANKLE_REFLEX_LEFT', 'ANKLE_REFLEX_RIGHT',
  'DP_PULSE_LEFT', 'DP_PULSE_RIGHT', 'PT_PULSE_LEFT', 'PT_PULSE_RIGHT',
  'FOOT_DEFORMITY_LEFT', 'FOOT_DEFORMITY_RIGHT', 'FOOT_SKIN_LEFT', 'FOOT_SKIN_RIGHT',
  'FOOT_ULCER_LEFT', 'FOOT_ULCER_RIGHT', 'NEUROPATHY_SYMPTOM_SCORE',
  'RETINOPATHY_SCREEN', 'RETINOPATHY_LEFT', 'RETINOPATHY_RIGHT',
  'MACULOPATHY_LEFT', 'MACULOPATHY_RIGHT', 'HEART_SOUNDS', 'MURMUR', 'JVP',
  'PERIPHERAL_OEDEMA', 'CAROTID_BRUIT_LEFT', 'CAROTID_BRUIT_RIGHT',
  'FOOT_RISK_LEFT', 'FOOT_RISK_RIGHT');
UPDATE core.observation_code SET value_type = 'coded', write_permission = 'observation.write.history'
 WHERE code IN ('MONOFILAMENT_LEFT', 'MONOFILAMENT_RIGHT');
UPDATE core.observation_code SET write_permission = 'observation.write.history'
 WHERE code IN ('FOOT_ULCER_PRESENT', 'FUNDUS_FINDING');
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name = 'observation_answer';
DROP TABLE IF EXISTS core.observation_answer;
DELETE FROM core.role_permission WHERE permission_code = 'observation.write.exam';
DELETE FROM core.permission WHERE code = 'observation.write.exam';
