-- Station 8's exercise assessment and plan (CP60, §3 step 8, §12.1).
--
-- # The rule this whole checkpoint is built around
--
-- Acceptance criterion 1: *"contraindicated exercises are excluded, not warned"*, and the plan's
-- manual verification says it again in bold — a patient with severe neuropathy must find the
-- contraindicated options **absent**, not flagged.
--
-- That decides where the filter lives. A screen that received the whole library and hid part of it
-- would be a warning wearing a different colour: the data is on the device, one bug or one
-- "show all" affordance away from being offered, and the operator's tap is the only thing between
-- a patient with an insensate foot and a jumping routine. **So there is no endpoint that returns
-- the whole library for a patient.** The permitted list is computed on the server from the
-- contraindications actually recorded, and the excluded exercises are never sent.
--
-- What *is* sent is a count and the reasons: "three options are not shown because of severe
-- neuropathy". That is transparency about the filter rather than an offer, and it matters — a
-- physician looking at a short list needs to know it is short on purpose.
--
-- # Why the mapping is rows
--
-- Criterion 4 asks that the library be editable without a code release, and the plan lists both
-- the content and the contraindication mapping as open clinical decisions. Both are therefore
-- tables, seeded with a starter set that says plainly it is unapproved.
--
-- The mapping is a join table rather than a tag list on the exercise, because the interesting
-- question is per pair — *why* is this exercise excluded for this condition — and a reason a
-- clinician can read is what makes the mapping arguable rather than magic.

-- +goose Up

-- ---------------------------------------------------------------------------
-- What can stop somebody exercising
-- ---------------------------------------------------------------------------

CREATE TABLE core.contraindication (
  code text PRIMARY KEY,

  name_en text NOT NULL,
  name_bn text NOT NULL,
  -- What the operator is actually being asked. A checkbox saying "neuropathy" gets ticked for
  -- tingling toes; one saying "loss of protective sensation on the monofilament test" does not.
  question_en text NOT NULL,
  question_bn text NOT NULL,

  -- Where the record may already know the answer, so the station can pre-fill rather than ask
  -- again. Null when only a person can say.
  from_observation text REFERENCES core.observation_code(code),

  ordering   integer NOT NULL,
  retired_at timestamptz,

  CONSTRAINT contraindication_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,39}$'),
  CONSTRAINT contraindication_bilingual
    CHECK (btrim(name_en) <> '' AND btrim(name_bn) <> ''
           AND btrim(question_en) <> '' AND btrim(question_bn) <> '')
);

GRANT SELECT ON core.contraindication TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'contraindication', 'What stops somebody exercising is clinical knowledge, not a clinic''s data.')
ON CONFLICT DO NOTHING;

-- §3 step 8 names three; two more are here because a foot ulcer and an uncontrolled blood pressure
-- are the two an operator at this station will meet first, and a list that omitted them would be
-- one an examiner quietly works around.
INSERT INTO core.contraindication
  (code, name_en, name_bn, question_en, question_bn, from_observation, ordering) VALUES

  ('SEVERE_NEUROPATHY', 'Severe peripheral neuropathy', 'তীব্র পেরিফেরাল নিউরোপ্যাথি',
   'Has the patient lost protective sensation — no feeling at one or more monofilament sites?',
   'রোগী কি সুরক্ষামূলক অনুভূতি হারিয়েছেন — মনোফিলামেন্টের এক বা একাধিক জায়গায় অনুভূতি নেই?',
   NULL, 10),

  ('PROLIFERATIVE_RETINOPATHY', 'Proliferative retinopathy', 'প্রলিফারেটিভ রেটিনোপ্যাথি',
   'Has an eye examination found new vessels, or has the patient had laser treatment?',
   'চোখের পরীক্ষায় কি নতুন রক্তনালি পাওয়া গেছে, বা লেজার চিকিৎসা হয়েছে?',
   NULL, 20),

  ('CARDIAC_LIMITATION', 'Cardiac limitation', 'হৃদযন্ত্রের সীমাবদ্ধতা',
   'Does the patient get chest pain or unusual breathlessness on exertion?',
   'পরিশ্রম করলে কি বুকে ব্যথা বা অস্বাভাবিক শ্বাসকষ্ট হয়?',
   NULL, 30),

  ('ACTIVE_FOOT_ULCER', 'Active foot ulcer', 'পায়ে সক্রিয় ঘা',
   'Is there an open ulcer or a wound on either foot today?',
   'আজ কোনও পায়ে কি খোলা ঘা বা ক্ষত আছে?',
   NULL, 40),

  ('UNCONTROLLED_HYPERTENSION', 'Uncontrolled blood pressure', 'অনিয়ন্ত্রিত রক্তচাপ',
   'Is the blood pressure above 180/110 today?',
   'আজ রক্তচাপ কি ১৮০/১১০-এর বেশি?',
   NULL, 50)
ON CONFLICT (code) DO UPDATE SET
  name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn,
  question_en = EXCLUDED.question_en, question_bn = EXCLUDED.question_bn,
  ordering = EXCLUDED.ordering;

-- ---------------------------------------------------------------------------
-- The library
-- ---------------------------------------------------------------------------

CREATE TABLE core.exercise (
  code text PRIMARY KEY,

  name_en text NOT NULL,
  name_bn text NOT NULL,
  -- What the patient is told to do, in their own language, because this is what gets printed and
  -- handed to them (criterion 3). Long enough to be an instruction rather than a label.
  how_en text NOT NULL,
  how_bn text NOT NULL,

  kind text NOT NULL CHECK (kind IN ('AEROBIC', 'RESISTANCE', 'FLEXIBILITY', 'BALANCE')),
  intensity text NOT NULL CHECK (intensity IN ('LOW', 'MODERATE', 'VIGOROUS')),
  -- Whether it jars the feet. The single property §3 step 8's example turns on — "no high-impact
  -- cardio in severe neuropathy" — and the one a mapping most often needs.
  impact text NOT NULL CHECK (impact IN ('NONE', 'LOW', 'HIGH')),

  -- What it needs, so a plan is not built on a treadmill this clinic's patients do not have.
  needs_equipment boolean NOT NULL DEFAULT false,
  can_do_at_home  boolean NOT NULL DEFAULT true,

  -- Null on everything seeded. The plan lists the library content as an open clinical decision.
  approved_at timestamptz,
  approved_by uuid REFERENCES core.app_user(id),

  ordering   integer NOT NULL DEFAULT 100,
  retired_at timestamptz,

  CONSTRAINT exercise_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,39}$'),
  CONSTRAINT exercise_bilingual
    CHECK (btrim(name_en) <> '' AND btrim(name_bn) <> ''
           AND btrim(how_en) <> '' AND btrim(how_bn) <> ''),
  CONSTRAINT exercise_approval CHECK ((approved_at IS NULL) = (approved_by IS NULL))
);

GRANT SELECT ON core.exercise TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'exercise', 'An exercise library is clinical content, not a clinic''s records.')
ON CONFLICT DO NOTHING;

-- The mapping, and the reason for each pair.
--
-- A join table rather than a tag list on the exercise, because the interesting question is per
-- pair — *why* is this excluded for this condition — and a reason a clinician can read is what
-- makes the mapping arguable rather than magic. When somebody disagrees with an exclusion, the
-- sentence is what they argue with.
CREATE TABLE core.exercise_contraindication (
  exercise_code        text NOT NULL REFERENCES core.exercise(code) ON DELETE CASCADE,
  contraindication_code text NOT NULL REFERENCES core.contraindication(code) ON DELETE CASCADE,

  reason_en text NOT NULL,
  reason_bn text NOT NULL,

  PRIMARY KEY (exercise_code, contraindication_code),
  CONSTRAINT exercise_contraindication_says_why
    CHECK (btrim(reason_en) <> '' AND btrim(reason_bn) <> '')
);

GRANT SELECT ON core.exercise_contraindication TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'exercise_contraindication', 'Which exercise is unsafe with which condition is clinical knowledge.')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- A starter library
-- ---------------------------------------------------------------------------

-- Physician-authored content, and this is not it. What follows is a working set so that station 8
-- runs on day one and Dr. Nahid has something concrete to correct; `approved_at` is null on every
-- row and the API reports it.
INSERT INTO core.exercise
  (code, name_en, name_bn, how_en, how_bn, kind, intensity, impact,
   needs_equipment, can_do_at_home, ordering) VALUES

  ('WALK_FLAT', 'Walking on level ground', 'সমতল জায়গায় হাঁটা',
   'Walk at a pace where you can still talk but not sing. Wear closed shoes that fit, and check both feet afterwards.',
   'এমন গতিতে হাঁটুন যাতে কথা বলা যায় কিন্তু গান গাওয়া যায় না। মাপমতো ঢাকা জুতা পরুন, আর হাঁটার পরে দুই পা দেখে নিন।',
   'AEROBIC', 'MODERATE', 'LOW', false, true, 10),

  ('WALK_BRISK', 'Brisk walking', 'দ্রুত হাঁটা',
   'Walk fast enough that talking is an effort. Stop if you feel chest tightness or unusual breathlessness.',
   'এত দ্রুত হাঁটুন যাতে কথা বলতে কষ্ট হয়। বুকে চাপ বা অস্বাভাবিক শ্বাসকষ্ট হলে থামুন।',
   'AEROBIC', 'VIGOROUS', 'LOW', false, true, 20),

  ('CYCLE_STATIONARY', 'Stationary cycling', 'স্থির সাইকেল চালানো',
   'Cycle sitting down, with the seat set so your knee is almost straight at the bottom of the turn.',
   'বসে সাইকেল চালান; সিট এমনভাবে রাখুন যাতে প্যাডেল নিচে থাকলে হাঁটু প্রায় সোজা হয়।',
   'AEROBIC', 'MODERATE', 'NONE', true, false, 30),

  ('SWIM', 'Swimming', 'সাঁতার',
   'Swim or walk in chest-deep water. Do not swim if there is any open wound on the feet.',
   'সাঁতার কাটুন বা বুক-সমান পানিতে হাঁটুন। পায়ে খোলা ঘা থাকলে সাঁতার কাটবেন না।',
   'AEROBIC', 'MODERATE', 'NONE', true, false, 40),

  ('JOG', 'Jogging', 'জগিং',
   'Run slowly on even ground. Stop at once if a foot feels numb or if you cannot feel the ground.',
   'সমান জায়গায় ধীরে দৌড়ান। পা অবশ লাগলে বা মাটি অনুভব না করলে সঙ্গে সঙ্গে থামুন।',
   'AEROBIC', 'VIGOROUS', 'HIGH', false, true, 50),

  ('SKIPPING', 'Skipping', 'দড়ি লাফ',
   'Skip with both feet, landing softly. Not for long — two minutes at a time is plenty to begin.',
   'দুই পায়ে হালকাভাবে দড়ি লাফান। বেশিক্ষণ নয় — শুরুতে একবারে দুই মিনিটই যথেষ্ট।',
   'AEROBIC', 'VIGOROUS', 'HIGH', true, true, 60),

  ('STAIRS', 'Stair climbing', 'সিঁড়ি ভাঙা',
   'Go up and down a flight of stairs, holding the rail. Rest whenever you need to.',
   'রেলিং ধরে সিঁড়ি ওঠানামা করুন। যখনই দরকার, বিশ্রাম নিন।',
   'AEROBIC', 'MODERATE', 'LOW', false, true, 70),

  ('CHAIR_STAND', 'Standing up from a chair', 'চেয়ার থেকে ওঠা',
   'Sit on a firm chair and stand up without using your hands, ten times. Rest, and repeat.',
   'শক্ত চেয়ারে বসে হাত না লাগিয়ে দশবার উঠে দাঁড়ান। বিশ্রাম নিয়ে আবার করুন।',
   'RESISTANCE', 'MODERATE', 'LOW', false, true, 80),

  ('BAND_ROW', 'Pulling a resistance band', 'রেজিস্ট্যান্স ব্যান্ড টানা',
   'Sit, loop the band around your feet, and pull the ends towards your waist. Breathe out as you pull.',
   'বসে ব্যান্ডটি পায়ের পাতায় আটকে দুই প্রান্ত কোমরের দিকে টানুন। টানার সময় শ্বাস ছাড়ুন।',
   'RESISTANCE', 'MODERATE', 'NONE', true, true, 90),

  ('HEAVY_LIFT', 'Lifting a heavy weight', 'ভারী ওজন তোলা',
   'Lift a weight you can manage only six to eight times. Never hold your breath while lifting.',
   'এমন ওজন তুলুন যা ছয় থেকে আটবারের বেশি তোলা যায় না। তোলার সময় কখনও শ্বাস আটকে রাখবেন না।',
   'RESISTANCE', 'VIGOROUS', 'LOW', true, false, 100),

  ('SEATED_STRETCH', 'Seated stretches', 'বসে শরীর টানটান করা',
   'Sitting on a chair, reach slowly towards your toes and hold while you count to twenty. Do not bounce.',
   'চেয়ারে বসে ধীরে পায়ের আঙুলের দিকে হাত বাড়ান, কুড়ি গোনা পর্যন্ত ধরে রাখুন। ঝাঁকাবেন না।',
   'FLEXIBILITY', 'LOW', 'NONE', false, true, 110),

  ('STANDING_BALANCE', 'Standing on one leg', 'এক পায়ে দাঁড়ানো',
   'Hold the back of a chair and stand on one leg while you count to ten. Change legs.',
   'চেয়ারের পিঠ ধরে এক পায়ে দশ গোনা পর্যন্ত দাঁড়ান। পা বদলান।',
   'BALANCE', 'LOW', 'LOW', false, true, 120)
ON CONFLICT (code) DO UPDATE SET
  name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn,
  how_en = EXCLUDED.how_en, how_bn = EXCLUDED.how_bn,
  kind = EXCLUDED.kind, intensity = EXCLUDED.intensity, impact = EXCLUDED.impact,
  needs_equipment = EXCLUDED.needs_equipment, can_do_at_home = EXCLUDED.can_do_at_home,
  ordering = EXCLUDED.ordering;

-- The mapping. Every row is a clinical claim, stated so a clinician can disagree with it.
INSERT INTO core.exercise_contraindication
  (exercise_code, contraindication_code, reason_en, reason_bn) VALUES

  -- §3 step 8's own example: no high-impact cardio in severe neuropathy. A foot that cannot feel
  -- the ground cannot feel the injury either, and the patient finds out days later.
  ('JOG', 'SEVERE_NEUROPATHY',
   'A foot without protective sensation cannot feel an injury from repeated impact, and the damage is found days later.',
   'সুরক্ষামূলক অনুভূতিহীন পা বারবার আঘাতের ক্ষতি টের পায় না, আর কয়েকদিন পরে তা ধরা পড়ে।'),
  ('SKIPPING', 'SEVERE_NEUROPATHY',
   'Repeated landing on an insensate foot is the commonest way a small injury becomes an ulcer.',
   'অনুভূতিহীন পায়ে বারবার লাফিয়ে নামাই ছোট আঘাতকে ঘায়ে পরিণত করার সবচেয়ে সাধারণ কারণ।'),
  ('STANDING_BALANCE', 'SEVERE_NEUROPATHY',
   'Balance depends on feeling the ground. Without it, a balance exercise is a fall waiting to happen.',
   'ভারসাম্য মাটি অনুভবের উপর নির্ভর করে। তা না থাকলে ভারসাম্যের ব্যায়াম মানে পড়ে যাওয়ার অপেক্ষা।'),

  -- Retinopathy: anything that spikes intrathoracic or intraocular pressure.
  ('HEAVY_LIFT', 'PROLIFERATIVE_RETINOPATHY',
   'Straining against a heavy weight raises pressure inside the eye and can bleed a fragile new vessel.',
   'ভারী ওজনের বিপরীতে জোর দিলে চোখের ভেতরের চাপ বাড়ে, আর ভঙ্গুর নতুন রক্তনালি ফেটে যেতে পারে।'),
  ('JOG', 'PROLIFERATIVE_RETINOPATHY',
   'The jarring of running is enough to bleed a fragile new vessel.',
   'দৌড়ানোর ঝাঁকুনিই ভঙ্গুর নতুন রক্তনালি ফাটানোর জন্য যথেষ্ট।'),
  ('SKIPPING', 'PROLIFERATIVE_RETINOPATHY',
   'The same jarring, harder.',
   'একই ঝাঁকুনি, আরও জোরে।'),

  -- Cardiac limitation: nothing vigorous until somebody has assessed them.
  ('WALK_BRISK', 'CARDIAC_LIMITATION',
   'Vigorous exertion is what brings on the symptom. Walk at a talking pace until a doctor has assessed the heart.',
   'জোর পরিশ্রমেই উপসর্গটি দেখা দেয়। হৃদযন্ত্র পরীক্ষা না হওয়া পর্যন্ত কথা বলার গতিতে হাঁটুন।'),
  ('JOG', 'CARDIAC_LIMITATION',
   'Vigorous exertion is what brings on the symptom.',
   'জোর পরিশ্রমেই উপসর্গটি দেখা দেয়।'),
  ('SKIPPING', 'CARDIAC_LIMITATION',
   'Vigorous exertion is what brings on the symptom.',
   'জোর পরিশ্রমেই উপসর্গটি দেখা দেয়।'),
  ('HEAVY_LIFT', 'CARDIAC_LIMITATION',
   'Straining against a heavy weight loads the heart in the way the symptom is warning about.',
   'ভারী ওজনের বিপরীতে জোর দিলে হৃদযন্ত্রের উপর ঠিক সেই চাপ পড়ে, যার কথা উপসর্গটি বলছে।'),
  ('STAIRS', 'CARDIAC_LIMITATION',
   'Stairs are the everyday exertion that most often brings on the symptom.',
   'সিঁড়িই সেই দৈনন্দিন পরিশ্রম যাতে উপসর্গটি সবচেয়ে বেশি দেখা দেয়।'),

  -- An open ulcer: nothing that puts weight through the foot, and nothing that wets it.
  ('WALK_FLAT', 'ACTIVE_FOOT_ULCER',
   'Weight through an open ulcer is what stops it healing.',
   'খোলা ঘায়ের উপর ওজন পড়লে সেটি শুকায় না।'),
  ('WALK_BRISK', 'ACTIVE_FOOT_ULCER',
   'Weight through an open ulcer is what stops it healing.',
   'খোলা ঘায়ের উপর ওজন পড়লে সেটি শুকায় না।'),
  ('JOG', 'ACTIVE_FOOT_ULCER',
   'Weight through an open ulcer is what stops it healing.',
   'খোলা ঘায়ের উপর ওজন পড়লে সেটি শুকায় না।'),
  ('SKIPPING', 'ACTIVE_FOOT_ULCER',
   'Weight through an open ulcer is what stops it healing.',
   'খোলা ঘায়ের উপর ওজন পড়লে সেটি শুকায় না।'),
  ('STAIRS', 'ACTIVE_FOOT_ULCER',
   'Weight through an open ulcer is what stops it healing.',
   'খোলা ঘায়ের উপর ওজন পড়লে সেটি শুকায় না।'),
  ('STANDING_BALANCE', 'ACTIVE_FOOT_ULCER',
   'Weight through an open ulcer is what stops it healing.',
   'খোলা ঘায়ের উপর ওজন পড়লে সেটি শুকায় না।'),
  ('SWIM', 'ACTIVE_FOOT_ULCER',
   'An open wound must not go into pool or pond water.',
   'খোলা ক্ষত নিয়ে পুকুর বা পুলের পানিতে নামা যাবে না।'),

  -- An uncontrolled pressure: nothing vigorous, and nothing that involves straining.
  ('WALK_BRISK', 'UNCONTROLLED_HYPERTENSION',
   'Vigorous exertion raises an already high pressure further.',
   'জোর পরিশ্রম করলে এমনিতেই বেশি রক্তচাপ আরও বাড়ে।'),
  ('JOG', 'UNCONTROLLED_HYPERTENSION',
   'Vigorous exertion raises an already high pressure further.',
   'জোর পরিশ্রম করলে এমনিতেই বেশি রক্তচাপ আরও বাড়ে।'),
  ('SKIPPING', 'UNCONTROLLED_HYPERTENSION',
   'Vigorous exertion raises an already high pressure further.',
   'জোর পরিশ্রম করলে এমনিতেই বেশি রক্তচাপ আরও বাড়ে।'),
  ('HEAVY_LIFT', 'UNCONTROLLED_HYPERTENSION',
   'Straining against a heavy weight produces the sharpest short-term rise in blood pressure of anything in this list.',
   'ভারী ওজনের বিপরীতে জোর দিলে এই তালিকার যেকোনো কিছুর চেয়ে বেশি হারে সাময়িকভাবে রক্তচাপ বাড়ে।')
ON CONFLICT (exercise_code, contraindication_code) DO UPDATE SET
  reason_en = EXCLUDED.reason_en, reason_bn = EXCLUDED.reason_bn;

-- ---------------------------------------------------------------------------
-- The assessment and the plan
-- ---------------------------------------------------------------------------

-- What station 8 found, and which contraindications apply. **The plan's filter reads this row and
-- nothing a client sends**, which is what makes criterion 1 a property of the system rather than
-- of a screen: a client cannot obtain a contraindicated exercise by saying the patient has no
-- contraindications, because the server reads what was recorded.
CREATE TABLE read.exercise_assessment (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL,
  patient_id  uuid NOT NULL,
  visit_id    uuid,

  -- Mobility and joints, in words the station actually uses.
  walks_unaided boolean,
  walk_minutes  integer CHECK (walk_minutes IS NULL OR (walk_minutes >= 0 AND walk_minutes <= 600)),
  joint_pain    text NOT NULL DEFAULT '' ,

  -- Which of core.contraindication applies today.
  contraindications text[] NOT NULL DEFAULT ARRAY[]::text[],
  -- And which were **asked**, which is a different fact and the one the filter's honesty depends
  -- on. Without it, "we asked all five and none apply" and "we asked two and skipped the
  -- neuropathy question" are byte-identical rows, and the filter computes the permitted list as
  -- though the unasked question had been answered no — the exact failure criterion 1 exists to
  -- prevent, arriving through the front door. The mobility fields already carried this
  -- distinction (`walks_unaided` is nullable because a patient nobody asked is a different record
  -- from one who cannot); the five conditions the filter actually turns on had no such state.
  --
  -- Stored rather than assumed. The service refuses an assessment that leaves a live condition
  -- unanswered, so at write time this is always the whole catalogue — its value is **historical**:
  -- when a sixth condition is added next year, every assessment taken before it existed must still
  -- read as complete-for-its-time rather than as five people having skipped a question.
  asked text[] NOT NULL DEFAULT ARRAY[]::text[],

  note text NOT NULL DEFAULT '',

  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUPERSEDED')),

  recorded_at   timestamptz NOT NULL,
  recorded_by   uuid NOT NULL,
  recorded_role text NOT NULL DEFAULT '',
  station_code  text NOT NULL DEFAULT '',
  device_id     uuid,
  source        text NOT NULL DEFAULT '',

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL
);

CREATE INDEX exercise_assessment_live
  ON read.exercise_assessment (patient_id) WHERE status = 'ACTIVE';
CREATE INDEX exercise_assessment_by_patient
  ON read.exercise_assessment (patient_id, recorded_at DESC);

GRANT SELECT ON read.exercise_assessment TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON read.exercise_assessment TO dthcms_projector;

-- The plan issued, and its targets.
--
-- Criterion 2: adherence targets structured and comparable across visits, because §12.1's
-- exercise–outcome analysis needs them that way from the start. "Walk more" is not a target; three
-- times a week for twenty minutes is.
CREATE TABLE read.exercise_plan (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL,
  patient_id  uuid NOT NULL,
  visit_id    uuid,

  -- The assessment this plan was filtered against. Frozen, so that a plan issued last month can be
  -- read against the contraindications that were true then rather than the ones true now — which
  -- is the only way "why was she given jogging" has an answer.
  assessment_id uuid NOT NULL REFERENCES read.exercise_assessment(id),

  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUPERSEDED')),

  issued_at   timestamptz NOT NULL,
  issued_by   uuid NOT NULL,
  issued_role text NOT NULL DEFAULT '',
  station_code text NOT NULL DEFAULT '',
  device_id   uuid,
  source      text NOT NULL DEFAULT '',

  note text NOT NULL DEFAULT '',

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL
);

CREATE INDEX exercise_plan_live ON read.exercise_plan (patient_id) WHERE status = 'ACTIVE';
CREATE INDEX exercise_plan_by_patient ON read.exercise_plan (patient_id, issued_at DESC);

GRANT SELECT ON read.exercise_plan TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON read.exercise_plan TO dthcms_projector;

CREATE TABLE read.exercise_plan_item (
  plan_id       uuid NOT NULL REFERENCES read.exercise_plan(id) ON DELETE CASCADE,
  exercise_code text NOT NULL REFERENCES core.exercise(code),

  -- The target, in the two numbers §12.1 can compare across visits.
  times_per_week      integer NOT NULL CHECK (times_per_week BETWEEN 1 AND 14),
  minutes_per_session integer NOT NULL CHECK (minutes_per_session BETWEEN 1 AND 240),

  ordering integer NOT NULL DEFAULT 100,
  note     text NOT NULL DEFAULT '',

  PRIMARY KEY (plan_id, exercise_code)
);

GRANT SELECT ON read.exercise_plan_item TO dthcms_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON read.exercise_plan_item TO dthcms_projector;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('read', 'exercise_plan_item', 'Scoped by the plan it belongs to, which carries the facility.')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The filter, in the database
-- ---------------------------------------------------------------------------

-- What this patient may be offered, given what was recorded about them.
--
-- **In the database rather than only in Go**, and the reason is the same one the counselling gate
-- had: a rule that lives only in an application is a rule a second application does not have. The
-- physician dashboard, the research extract and any future screen all read this function, so
-- "contraindicated exercises are excluded" cannot become "excluded on the station app".
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.exercises_permitted(p_patient uuid, p_facility uuid)
RETURNS TABLE (code text) AS $$
  SELECT e.code
    FROM core.exercise e
   WHERE e.retired_at IS NULL
     AND NOT EXISTS (
           SELECT 1
             FROM core.exercise_contraindication x
             -- Retired conditions do not exclude. Retiring one is a clinical decision that it is
             -- no longer a contraindication, and without this join a condition removed from the
             -- catalogue would exclude forever — it would be absent from every new assessment's
             -- `asked` set and so read as "never asked".
             JOIN core.contraindication c
               ON c.code = x.contraindication_code AND c.retired_at IS NULL
             JOIN read.exercise_assessment a
               ON a.patient_id = p_patient
              AND a.facility_id = p_facility
              AND a.status = 'ACTIVE'
            WHERE x.exercise_code = e.code
              -- Applies, **or was never asked**. Absence of evidence is not evidence of absence,
              -- and offering an exercise mapped to a question nobody put is the same "warned, not
              -- excluded" failure one step removed. The practical consequence — adding a condition
              -- to the catalogue narrows every existing patient's list until somebody asks them the
              -- new question — is the correct behaviour, and the API reports the two reasons apart
              -- so it shows on the screen rather than happening silently.
              AND (x.contraindication_code = ANY(a.contraindications)
                   OR NOT (x.contraindication_code = ANY(a.asked)))
         )
   ORDER BY e.ordering, e.code;
$$ LANGUAGE sql STABLE;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION core.exercises_permitted(uuid, uuid) TO dthcms_app, dthcms_projector;

-- A plan may not contain an exercise the assessment it was filtered against forbids.
--
-- A trigger rather than a check in the service, for the reason every other gate in this system is:
-- a projection rebuild writes these rows too, and a rebuild that quietly accepted a contraindicated
-- item would produce a read model in which criterion 1 is false.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.exercise_plan_item_is_permitted() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  SELECT x.contraindication_code INTO offending
    FROM read.exercise_plan p
    JOIN read.exercise_assessment a ON a.id = p.assessment_id
    JOIN core.exercise_contraindication x ON x.exercise_code = NEW.exercise_code
    JOIN core.contraindication c ON c.code = x.contraindication_code AND c.retired_at IS NULL
   WHERE p.id = NEW.plan_id
     AND (x.contraindication_code = ANY(a.contraindications)
          OR NOT (x.contraindication_code = ANY(a.asked)))
   LIMIT 1;

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION '% is contraindicated by % for this patient', NEW.exercise_code, offending;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER exercise_plan_item_is_permitted
  BEFORE INSERT OR UPDATE ON read.exercise_plan_item
  FOR EACH ROW EXECUTE FUNCTION read.exercise_plan_item_is_permitted();

-- ---------------------------------------------------------------------------
-- The projections
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_exercise_assessment_recorded(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  -- A second assessment supersedes the first. A patient whose foot ulcer healed between visits has
  -- a new answer, not an edited one, and last month's plan must stay readable against last month's
  -- findings.
  UPDATE read.exercise_assessment
     SET status = 'SUPERSEDED'
   WHERE patient_id = (p->>'patient_id')::uuid AND status = 'ACTIVE';

  INSERT INTO read.exercise_assessment
    (id, facility_id, patient_id, visit_id, walks_unaided, walk_minutes, joint_pain,
     contraindications, asked, note, recorded_at, recorded_by, recorded_role, station_code,
     device_id, source, event_id, global_seq)
  VALUES
    ((p->>'assessment_id')::uuid, (p->>'facility_id')::uuid, (p->>'patient_id')::uuid,
     nullif(p->>'visit_id', '')::uuid,
     CASE WHEN p->>'walks_unaided' IS NULL THEN NULL ELSE (p->>'walks_unaided')::boolean END,
     CASE WHEN p->>'walk_minutes' IS NULL THEN NULL ELSE (p->>'walk_minutes')::integer END,
     coalesce(p->>'joint_pain', ''),
     CASE
       WHEN jsonb_typeof(p->'contraindications') = 'array'
       THEN coalesce((SELECT array_agg(value::text)
                        FROM jsonb_array_elements_text(p->'contraindications')),
                     ARRAY[]::text[])
       ELSE ARRAY[]::text[]
     END,
     CASE
       WHEN jsonb_typeof(p->'asked') = 'array'
       THEN coalesce((SELECT array_agg(value::text)
                        FROM jsonb_array_elements_text(p->'asked')),
                     ARRAY[]::text[])
       ELSE ARRAY[]::text[]
     END,
     coalesce(p->>'note', ''),
     (p->>'recorded_at')::timestamptz, (p->>'recorded_by')::uuid,
     coalesce(p->>'recorded_role', ''), coalesce(p->>'station_code', ''),
     nullif(p->>'device_id', '')::uuid, coalesce(p->>'source', ''),
     (p->>'event_id')::uuid, (p->>'global_seq')::bigint)
  ON CONFLICT (id) DO NOTHING;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_exercise_plan_issued(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  item jsonb;
BEGIN
  UPDATE read.exercise_plan
     SET status = 'SUPERSEDED'
   WHERE patient_id = (p->>'patient_id')::uuid AND status = 'ACTIVE';

  INSERT INTO read.exercise_plan
    (id, facility_id, patient_id, visit_id, assessment_id, issued_at, issued_by, issued_role,
     station_code, device_id, source, note, event_id, global_seq)
  VALUES
    ((p->>'plan_id')::uuid, (p->>'facility_id')::uuid, (p->>'patient_id')::uuid,
     nullif(p->>'visit_id', '')::uuid, (p->>'assessment_id')::uuid,
     (p->>'issued_at')::timestamptz, (p->>'issued_by')::uuid,
     coalesce(p->>'issued_role', ''), coalesce(p->>'station_code', ''),
     nullif(p->>'device_id', '')::uuid, coalesce(p->>'source', ''),
     coalesce(p->>'note', ''),
     (p->>'event_id')::uuid, (p->>'global_seq')::bigint)
  ON CONFLICT (id) DO NOTHING;

  FOR item IN SELECT * FROM jsonb_array_elements(p->'items')
  LOOP
    -- The trigger on this table refuses a contraindicated item, so a rebuild of a plan that
    -- should never have existed fails loudly rather than reproducing it.
    INSERT INTO read.exercise_plan_item
      (plan_id, exercise_code, times_per_week, minutes_per_session, ordering, note)
    VALUES
      ((p->>'plan_id')::uuid, item->>'exercise_code',
       (item->>'times_per_week')::integer, (item->>'minutes_per_session')::integer,
       coalesce((item->>'ordering')::integer, 100), coalesce(item->>'note', ''))
    ON CONFLICT (plan_id, exercise_code) DO NOTHING;
  END LOOP;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_exercise_assessment_recorded(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_exercise_plan_issued(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- Criterion 1, standing. The trigger guards rows written from here on; this notices one that
-- arrived some other way, and it is the check the manual verification is really about.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_plan_offers_a_contraindicated_exercise() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM read.exercise_plan_item i
    JOIN read.exercise_plan p ON p.id = i.plan_id
    JOIN read.exercise_assessment a ON a.id = p.assessment_id
    JOIN core.exercise_contraindication x ON x.exercise_code = i.exercise_code
    JOIN core.contraindication c ON c.code = x.contraindication_code AND c.retired_at IS NULL
   WHERE x.contraindication_code = ANY(a.contraindications)
      OR NOT (x.contraindication_code = ANY(a.asked));
  IF offenders > 0 THEN
    RAISE EXCEPTION '% plan items are contraindicated for the patient they were issued to', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- Criterion 2. A target that is not two numbers is not comparable across visits, and §12.1's
-- analysis needs them comparable from the first patient.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_exercise_target_is_countable() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM read.exercise_plan_item
   WHERE times_per_week IS NULL OR minutes_per_session IS NULL
      OR times_per_week < 1 OR minutes_per_session < 1;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% exercise targets cannot be counted', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- Criterion 3's precondition. A sheet printed in Bangla needs the instruction in Bangla, and an
-- exercise whose only wording is English is one a patient is handed in a language they may not
-- read.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_exercise_reads_in_both_languages() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  SELECT code INTO offender
    FROM core.exercise
   WHERE btrim(name_bn) = '' OR btrim(how_bn) = '' OR btrim(name_en) = '' OR btrim(how_en) = ''
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION '% cannot be printed in both languages', offender;
  END IF;

  SELECT exercise_code INTO offender
    FROM core.exercise_contraindication
   WHERE btrim(reason_bn) = '' OR btrim(reason_en) = ''
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'an exclusion of % says why in only one language', offender;
  END IF;
END
$$;
-- +goose StatementEnd

-- You cannot record a condition as applying that you did not ask about. Unlike "every condition is
-- answered", this one stays true forever — a catalogue addition does not falsify it — so it is an
-- invariant rather than only a write-time check.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_finding_was_asked_about() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender uuid;
BEGIN
  SELECT id INTO offender
    FROM read.exercise_assessment
   WHERE NOT (contraindications <@ asked)
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'assessment % records a condition it did not ask about', offender;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_finding_was_asked_about',
   'no exercise assessment records a condition it did not ask about', 89),
  ('assert_no_plan_offers_a_contraindicated_exercise',
   'no exercise plan contains something contraindicated for the patient it was issued to', 86),
  ('assert_every_exercise_target_is_countable',
   'every exercise target is a number of times a week and a number of minutes', 87),
  ('assert_every_exercise_reads_in_both_languages',
   'every exercise, and every reason one is excluded, reads in both languages', 88)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_every_finding_was_asked_about',
  'assert_no_plan_offers_a_contraindicated_exercise',
  'assert_every_exercise_target_is_countable',
  'assert_every_exercise_reads_in_both_languages');
DROP FUNCTION IF EXISTS core.assert_every_finding_was_asked_about();
DROP FUNCTION IF EXISTS core.assert_every_exercise_reads_in_both_languages();
DROP FUNCTION IF EXISTS core.assert_every_exercise_target_is_countable();
DROP FUNCTION IF EXISTS core.assert_no_plan_offers_a_contraindicated_exercise();
DROP FUNCTION IF EXISTS read.apply_exercise_plan_issued(jsonb);
DROP FUNCTION IF EXISTS read.apply_exercise_assessment_recorded(jsonb);
DROP TABLE IF EXISTS read.exercise_plan_item;
DROP FUNCTION IF EXISTS read.exercise_plan_item_is_permitted();
DROP TABLE IF EXISTS read.exercise_plan;
DROP TABLE IF EXISTS read.exercise_assessment;
DROP FUNCTION IF EXISTS core.exercises_permitted(uuid, uuid);
DROP TABLE IF EXISTS core.exercise_contraindication;
DROP TABLE IF EXISTS core.exercise;
DROP TABLE IF EXISTS core.contraindication;
DELETE FROM core.facility_scope_exemption
 WHERE (schema_name = 'core' AND table_name IN ('contraindication', 'exercise', 'exercise_contraindication'))
    OR (schema_name = 'read' AND table_name = 'exercise_plan_item');
