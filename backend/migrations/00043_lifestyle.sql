-- Lifestyle assessment and the composite risk score (CP58, §3 step 3, §12).
--
-- # Two things are being built here, and only one of them is blocked
--
-- The plan lists **D-26 — some validated instruments are copyrighted** as an open decision and
-- calls it a blocker for the score. It blocks the *content*: which questionnaires this clinic may
-- put in front of a patient, and what the composite formula weighs. It does not block the
-- *mechanism*, and the mechanism is most of the work: an instrument is rows, a response is rows,
-- and a score names the formula version that produced it.
--
-- So this migration builds the framework and seeds only what can be seeded honestly. The
-- copyrighted candidates are **registered by name and marked unusable**, and an invariant refuses
-- their items until somebody records a licence — the same shape CP52 used for SNOMED, and for the
-- same reason: "we remembered not to embed it" is not a control.
--
-- # Why raw responses and not only totals
--
-- Acceptance criterion 1, and it is the criterion that decides the schema. §12's research
-- cohorting is done on behaviour, and a total of 14 on a stress scale cannot be re-analysed,
-- re-scored under a corrected formula, or compared against a study that used different item
-- weights. A stored total is a number somebody has to trust; stored items are data.
--
-- It also decides something less obvious: the total is **derived**, not stored beside the items as
-- an independent column. Two columns that should agree are two columns that will not, and the day
-- they disagree nobody can say which was right.
--
-- # Why the score is an observation and not a column
--
-- CP42 exists so the eleventh station does not invent an eleventh shape. A lifestyle risk score is
-- a number about a patient at a moment with a formula behind it, which is precisely a DERIVED
-- observation — it inherits the correction cascade, the timeline, the research extract and the
-- attribution without a line of new code. What is *not* an observation is the questionnaire
-- itself: an instrument has items, versions and licences, and none of those fit a value.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The instruments
-- ---------------------------------------------------------------------------

CREATE TABLE core.instrument (
  code text PRIMARY KEY,

  name_en text NOT NULL,
  name_bn text NOT NULL,
  -- What it measures, in a sentence an operator can read before deciding to run it.
  purpose_en text NOT NULL DEFAULT '',
  purpose_bn text NOT NULL DEFAULT '',

  -- Who owns it, and whether this clinic may use it. `usable` false is the honest state for a
  -- copyrighted instrument nobody has licensed: the row exists so that the decision is visible
  -- and so that a screen can say "not licensed" rather than silently omitting a questionnaire a
  -- clinician expected to find.
  copyright_holder text NOT NULL DEFAULT '',
  licence_note     text NOT NULL DEFAULT '',
  usable           boolean NOT NULL DEFAULT false,

  -- What kind of thing it asks about, for grouping on a screen and for the composite score to
  -- know which domain a subscale belongs to.
  domain text NOT NULL CHECK (domain IN ('SMOKING', 'ALCOHOL', 'SLEEP', 'STRESS',
                                          'ACTIVITY', 'DIET', 'READINESS')),

  ordering   integer NOT NULL DEFAULT 100,
  retired_at timestamptz,

  CONSTRAINT instrument_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,39}$'),
  -- A usable instrument must say why it may be used. An empty note on a licensed row is how a
  -- licence nobody can produce becomes a licence nobody remembers questioning.
  CONSTRAINT instrument_licence_is_stated CHECK (NOT usable OR btrim(licence_note) <> '')
);

GRANT SELECT ON core.instrument TO dthcms_app;

COMMENT ON TABLE core.instrument IS
  'The questionnaires station 3 may run, and the ones it may not (CP58, D-26).';

-- The instrument tables are exempt from facility scoping, and the answer table is exempt for a
-- different reason worth stating apart: a questionnaire is published literature and belongs to
-- nobody's clinic, while an answer belongs to the response above it, which carries the facility.
-- Putting a facility on the answer would be a second copy of a fact, and the two would disagree
-- the first time a patient record moved.
INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'instrument', 'A questionnaire is published literature, not a clinic''s data.'),
  ('core', 'instrument_version', 'A version of a questionnaire is published literature.'),
  ('core', 'instrument_item', 'A question is published literature.'),
  ('core', 'instrument_option', 'An answer option is published literature.'),
  ('read', 'instrument_answer', 'Scoped by the response it belongs to, which carries the facility.')
ON CONFLICT DO NOTHING;

-- Versions, because an instrument's wording changes and a response scored under the old wording
-- must keep saying which wording it answered. Same discipline as the counselling templates.
CREATE TABLE core.instrument_version (
  instrument_code text NOT NULL REFERENCES core.instrument(code),
  version         integer NOT NULL CHECK (version >= 1),

  -- How the item scores add up, and what the result means. `sum` is the only one implemented;
  -- the column exists because the second instrument somebody adds will not use it.
  scoring text NOT NULL DEFAULT 'sum' CHECK (scoring IN ('sum', 'none')),

  published_at timestamptz,
  note         text NOT NULL DEFAULT '',

  PRIMARY KEY (instrument_code, version)
);

GRANT SELECT ON core.instrument_version TO dthcms_app;

CREATE TABLE core.instrument_item (
  instrument_code text NOT NULL,
  version         integer NOT NULL,
  -- The item's own code, stable across versions where the question is the same. This is what a
  -- response row points at, so a research extract can compare item 3 across two versions if
  -- somebody decides they are the same question.
  item_code text NOT NULL,

  ordering integer NOT NULL,

  prompt_en text NOT NULL,
  prompt_bn text NOT NULL,

  -- What kind of answer. Coded answers use the option table below; numeric ones carry a range.
  answer_type text NOT NULL CHECK (answer_type IN ('coded', 'numeric', 'boolean')),
  unit        text,
  min_value   numeric,
  max_value   numeric,

  -- Whether an unanswered item is allowed. A questionnaire with a required item somebody cannot
  -- answer is a questionnaire that gets abandoned, so this is per item rather than per
  -- instrument.
  required boolean NOT NULL DEFAULT true,

  PRIMARY KEY (instrument_code, version, item_code),
  FOREIGN KEY (instrument_code, version)
    REFERENCES core.instrument_version(instrument_code, version) ON DELETE CASCADE,

  CONSTRAINT instrument_item_code_format CHECK (item_code ~ '^[A-Z][A-Z0-9_]{0,39}$'),
  CONSTRAINT instrument_item_bilingual   CHECK (btrim(prompt_en) <> '' AND btrim(prompt_bn) <> ''),
  CONSTRAINT instrument_item_range       CHECK (min_value IS NULL OR max_value IS NULL
                                                OR min_value <= max_value),
  CONSTRAINT instrument_item_numeric_only CHECK (answer_type = 'numeric'
                                                 OR (unit IS NULL AND min_value IS NULL AND max_value IS NULL))
);

GRANT SELECT ON core.instrument_item TO dthcms_app;

CREATE TABLE core.instrument_option (
  instrument_code text NOT NULL,
  version         integer NOT NULL,
  item_code       text NOT NULL,
  option_code     text NOT NULL,

  ordering integer NOT NULL,
  label_en text NOT NULL,
  label_bn text NOT NULL,
  -- What this answer contributes to the instrument's own total.
  score integer NOT NULL DEFAULT 0,

  PRIMARY KEY (instrument_code, version, item_code, option_code),
  FOREIGN KEY (instrument_code, version, item_code)
    REFERENCES core.instrument_item(instrument_code, version, item_code) ON DELETE CASCADE,

  CONSTRAINT instrument_option_bilingual CHECK (btrim(label_en) <> '' AND btrim(label_bn) <> '')
);

GRANT SELECT ON core.instrument_option TO dthcms_app;

-- ---------------------------------------------------------------------------
-- What the clinic may and may not run
-- ---------------------------------------------------------------------------

-- **The unusable ones first**, so that the list reads as a decision rather than an omission. Every
-- one of these is a questionnaire a clinician might reasonably expect to find; the row says why it
-- is not there. `usable` false, no items, and an invariant below refuses items until somebody
-- records a licence.
INSERT INTO core.instrument (code, name_en, name_bn, purpose_en, purpose_bn,
                             copyright_holder, licence_note, usable, domain, ordering) VALUES
  ('PSS_10', 'Perceived Stress Scale (10-item)', 'পারসিভড স্ট্রেস স্কেল (১০টি প্রশ্ন)',
   'How unpredictable and overloaded the patient has felt in the last month',
   'গত এক মাসে রোগী কতটা অনিশ্চিত ও চাপে ছিলেন',
   'Sheldon Cohen / Mind Garden',
   'D-26: permission for clinical use in a fee-charging clinic must be confirmed before the items are entered.',
   false, 'STRESS', 30),

  ('PHQ_9', 'Patient Health Questionnaire (9-item)', 'পেশেন্ট হেলথ কোয়েশ্চেনেয়ার (৯টি প্রশ্ন)',
   'Depressive symptoms over the last fortnight',
   'গত দুই সপ্তাহে বিষণ্ণতার লক্ষণ',
   'Pfizer Inc.',
   'D-26: widely stated to be free to reproduce, and that statement has not been verified for this clinic. Not entered until it is.',
   false, 'STRESS', 40),

  ('IPAQ_SHORT', 'International Physical Activity Questionnaire (short form)',
   'ইন্টারন্যাশনাল ফিজিক্যাল অ্যাক্টিভিটি কোয়েশ্চেনেয়ার (সংক্ষিপ্ত)',
   'Activity in the last seven days, as minutes at each intensity',
   'গত সাত দিনে কোন মাত্রায় কত মিনিট শারীরিক পরিশ্রম',
   'IPAQ Group',
   'D-26: free for non-commercial research use; a fee-charging clinic is a different case and needs confirming.',
   false, 'ACTIVITY', 50)
ON CONFLICT (code) DO UPDATE SET
  name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn,
  purpose_en = EXCLUDED.purpose_en, purpose_bn = EXCLUDED.purpose_bn,
  copyright_holder = EXCLUDED.copyright_holder, licence_note = EXCLUDED.licence_note,
  domain = EXCLUDED.domain, ordering = EXCLUDED.ordering;

-- **AUDIT-C**, which the World Health Organization publishes for free use and which is what the
-- clinic can actually run on the day this ships. Three items, scored 0–12.
INSERT INTO core.instrument (code, name_en, name_bn, purpose_en, purpose_bn,
                             copyright_holder, licence_note, usable, domain, ordering) VALUES
  ('AUDIT_C', 'Alcohol Use Disorders Identification Test — Consumption',
   'অ্যালকোহল ব্যবহার শনাক্তকরণ পরীক্ষা — সেবন',
   'Three questions on how much and how often the patient drinks',
   'রোগী কতটা ও কত ঘন ঘন মদ্যপান করেন, তিনটি প্রশ্নে',
   'World Health Organization',
   'Published by the WHO for free use and reproduction, including in clinical settings.',
   true, 'ALCOHOL', 20)
ON CONFLICT (code) DO UPDATE SET
  licence_note = EXCLUDED.licence_note, usable = EXCLUDED.usable,
  domain = EXCLUDED.domain, ordering = EXCLUDED.ordering;

INSERT INTO core.instrument_version (instrument_code, version, scoring, published_at, note) VALUES
  ('AUDIT_C', 1, 'sum', now(), 'WHO wording, Bengali by this clinic and not yet reviewed by a clinician.')
ON CONFLICT DO NOTHING;

INSERT INTO core.instrument_item
  (instrument_code, version, item_code, ordering, prompt_en, prompt_bn, answer_type) VALUES
  ('AUDIT_C', 1, 'FREQUENCY', 1,
   'How often do you have a drink containing alcohol?',
   'কত ঘন ঘন আপনি অ্যালকোহলযুক্ত পানীয় পান করেন?', 'coded'),
  ('AUDIT_C', 1, 'TYPICAL_AMOUNT', 2,
   'How many standard drinks do you have on a typical day when you are drinking?',
   'যেদিন পান করেন, সাধারণত কয়টি স্ট্যান্ডার্ড পানীয় নেন?', 'coded'),
  ('AUDIT_C', 1, 'HEAVY_EPISODES', 3,
   'How often do you have six or more drinks on one occasion?',
   'কত ঘন ঘন এক বসায় ছয় বা তার বেশি পানীয় নেন?', 'coded')
ON CONFLICT DO NOTHING;

INSERT INTO core.instrument_option
  (instrument_code, version, item_code, option_code, ordering, label_en, label_bn, score) VALUES
  ('AUDIT_C', 1, 'FREQUENCY', 'NEVER',        1, 'Never', 'কখনও নয়', 0),
  ('AUDIT_C', 1, 'FREQUENCY', 'MONTHLY_OR_LESS', 2, 'Monthly or less', 'মাসে একবার বা তার কম', 1),
  ('AUDIT_C', 1, 'FREQUENCY', 'TWO_TO_FOUR_MONTHLY', 3, '2–4 times a month', 'মাসে ২–৪ বার', 2),
  ('AUDIT_C', 1, 'FREQUENCY', 'TWO_TO_THREE_WEEKLY', 4, '2–3 times a week', 'সপ্তাহে ২–৩ বার', 3),
  ('AUDIT_C', 1, 'FREQUENCY', 'FOUR_OR_MORE_WEEKLY', 5, '4 or more times a week', 'সপ্তাহে ৪ বার বা বেশি', 4),

  ('AUDIT_C', 1, 'TYPICAL_AMOUNT', 'ONE_OR_TWO',  1, '1 or 2', '১ বা ২', 0),
  ('AUDIT_C', 1, 'TYPICAL_AMOUNT', 'THREE_OR_FOUR', 2, '3 or 4', '৩ বা ৪', 1),
  ('AUDIT_C', 1, 'TYPICAL_AMOUNT', 'FIVE_OR_SIX', 3, '5 or 6', '৫ বা ৬', 2),
  ('AUDIT_C', 1, 'TYPICAL_AMOUNT', 'SEVEN_TO_NINE', 4, '7 to 9', '৭ থেকে ৯', 3),
  ('AUDIT_C', 1, 'TYPICAL_AMOUNT', 'TEN_OR_MORE', 5, '10 or more', '১০ বা বেশি', 4),

  ('AUDIT_C', 1, 'HEAVY_EPISODES', 'NEVER',      1, 'Never', 'কখনও নয়', 0),
  ('AUDIT_C', 1, 'HEAVY_EPISODES', 'LESS_MONTHLY', 2, 'Less than monthly', 'মাসে একবারের কম', 1),
  ('AUDIT_C', 1, 'HEAVY_EPISODES', 'MONTHLY',    3, 'Monthly', 'মাসে একবার', 2),
  ('AUDIT_C', 1, 'HEAVY_EPISODES', 'WEEKLY',     4, 'Weekly', 'সপ্তাহে একবার', 3),
  ('AUDIT_C', 1, 'HEAVY_EPISODES', 'DAILY',      5, 'Daily or almost daily', 'প্রতিদিন বা প্রায় প্রতিদিন', 4)
ON CONFLICT DO NOTHING;

-- **The clinic's own readiness question.** Not a validated instrument and not pretending to be:
-- one item, authored here, because §3 step 3 asks for readiness-to-change and the validated
-- alternatives are all behind D-26. It is marked as the clinic's own so nobody later cites it as
-- though it were published.
INSERT INTO core.instrument (code, name_en, name_bn, purpose_en, purpose_bn,
                             copyright_holder, licence_note, usable, domain, ordering) VALUES
  ('READINESS_1', 'Readiness to change (single item)', 'পরিবর্তনের প্রস্তুতি (একটি প্রশ্ন)',
   'How ready the patient says they are to change one habit, in their own words',
   'রোগী নিজে বলছেন একটি অভ্যাস বদলাতে তিনি কতটা প্রস্তুত',
   'This clinic',
   'Written here. Not a validated instrument, and must not be reported as one.',
   true, 'READINESS', 60)
ON CONFLICT (code) DO UPDATE SET licence_note = EXCLUDED.licence_note, usable = EXCLUDED.usable;

INSERT INTO core.instrument_version (instrument_code, version, scoring, published_at, note) VALUES
  ('READINESS_1', 1, 'sum', now(), 'One item. The score is the answer, and means nothing on its own.')
ON CONFLICT DO NOTHING;

INSERT INTO core.instrument_item
  (instrument_code, version, item_code, ordering, prompt_en, prompt_bn, answer_type) VALUES
  ('READINESS_1', 1, 'READY', 1,
   'How ready do you feel to change this habit in the next month?',
   'আগামী এক মাসে এই অভ্যাসটি বদলাতে আপনি কতটা প্রস্তুত বোধ করছেন?', 'coded')
ON CONFLICT DO NOTHING;

INSERT INTO core.instrument_option
  (instrument_code, version, item_code, option_code, ordering, label_en, label_bn, score) VALUES
  ('READINESS_1', 1, 'READY', 'NOT_THINKING', 1, 'Not thinking about it', 'ভাবছি না', 0),
  ('READINESS_1', 1, 'READY', 'THINKING',     2, 'Thinking about it', 'ভাবছি', 1),
  ('READINESS_1', 1, 'READY', 'PREPARING',    3, 'Getting ready to start', 'শুরুর প্রস্তুতি নিচ্ছি', 2),
  ('READINESS_1', 1, 'READY', 'STARTED',      4, 'Already started', 'শুরু করে দিয়েছি', 3),
  ('READINESS_1', 1, 'READY', 'KEEPING_UP',   5, 'Keeping it up for a while now', 'বেশ কিছুদিন ধরে চালিয়ে যাচ্ছি', 4)
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The lifestyle observations
-- ---------------------------------------------------------------------------

-- Duration, which this system did not have a dimension for.
--
-- It is worth adding rather than storing "hours" as a bare number, and the reason is CP42's whole
-- argument: an operator who is used to thinking in minutes and one who thinks in hours will
-- otherwise type the same amount of sleep as two different numbers under the same code. Minutes
-- are canonical because the activity guideline is written in them and because an integer minute is
-- exact where a decimal hour is not.
INSERT INTO core.unit (code, dimension, is_canonical, factor, "offset", display_en, display_bn, decimals) VALUES
  ('min', 'duration', true,  1,  0, 'min', 'মিনিট', 0),
  ('h',   'duration', false, 60, 0, 'h',   'ঘণ্টা',  1)
ON CONFLICT (code) DO NOTHING;

-- §3 step 3's plain numbers, which are values rather than questionnaire items and therefore
-- belong in CP42's model where every other value lives. Two of them exist so that PACK_YEARS —
-- declared since CP43 and unreachable ever since — can finally be computed.
INSERT INTO core.observation_code
  (code, category, value_type, dimension, display_en, display_bn,
   min_canonical, max_canonical, write_permission) VALUES
  -- The two counts are dimensionless, in the `ratio` sense PACK_YEARS already uses: "twenty a
  -- day" is a pure number and the period is in the code's name, not in a unit somebody could
  -- convert.
  ('CIGARETTES_PER_DAY', 'SCREENING', 'numeric', 'ratio',
   'Cigarettes a day', 'দিনে কতটি সিগারেট', 0, 100, 'observation.write.lifestyle'),
  ('SMOKING_YEARS', 'SCREENING', 'numeric', 'ratio',
   'Years smoked', 'কত বছর ধরে ধূমপান', 0, 90, 'observation.write.lifestyle'),
  -- The two durations are stored in canonical **minutes**, so an operator may type either.
  ('SLEEP_MINUTES', 'SCREENING', 'numeric', 'duration',
   'Sleep on a usual night', 'সাধারণত রাতে কতক্ষণ ঘুম', 0, 1440, 'observation.write.lifestyle'),
  ('ACTIVE_MINUTES_WEEK', 'SCREENING', 'numeric', 'duration',
   'Activity in a usual week', 'সাধারণ সপ্তাহে কতক্ষণ শারীরিক পরিশ্রম',
   0, 5000, 'observation.write.lifestyle'),
  -- The composite. DERIVED, so it inherits the correction cascade, the timeline and the research
  -- extract without a line of new code — and so that a screen showing it can never show a number
  -- whose formula nobody recorded.
  ('LIFESTYLE_RISK', 'DERIVED', 'numeric', 'ratio',
   'Lifestyle risk score', 'জীবনযাপন ঝুঁকির স্কোর', 0, 100, 'observation.write.lifestyle')
ON CONFLICT (code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  min_canonical = EXCLUDED.min_canonical, max_canonical = EXCLUDED.max_canonical,
  write_permission = EXCLUDED.write_permission;

-- Every code an operator types at a station needs a plausibility rule (invariant 27), and these
-- are the bands beyond which a number is a typing error rather than a patient.
--
-- The soft band is what an operator is warned about; the absolute band is what the code itself
-- can hold. Both are proposals, like every other row in this table (CP46), and `approved_at` is
-- null on all four.
INSERT INTO core.plausibility_rule
  (code, absolute_min, absolute_max, plausible_min, plausible_max, note_en, note_bn) VALUES

  ('CIGARETTES_PER_DAY', 0, 100, 0, 60,
   'More than sixty a day is three packs. Check the number before saving it.',
   'দিনে ষাটটির বেশি মানে তিন প্যাকেট। সংরক্ষণের আগে সংখ্যাটি দেখে নিন।'),

  -- The delta rules are the interesting ones here and are deliberately absent: years smoked goes
  -- up by one a year and a patient who quit does not lose them, so "changed a lot since last
  -- time" is not a signal for this code the way it is for a weight.
  ('SMOKING_YEARS', 0, 90, 0, 70,
   'More years than most patients have been alive. Check it against their age.',
   'বেশিরভাগ রোগীর বয়সের চেয়েও বেশি বছর। বয়সের সঙ্গে মিলিয়ে দেখুন।'),

  -- In canonical minutes, like the value: a rule written in hours beside a value stored in
  -- minutes is the unit bug CP42's whole framework exists to prevent.
  ('SLEEP_MINUTES', 0, 1440, 180, 720,
   'Under three hours or over twelve is worth a second question rather than a second look.',
   'তিন ঘণ্টার কম বা বারো ঘণ্টার বেশি হলে আবার দেখা নয়, আবার জিজ্ঞাসা করা দরকার।'),

  ('ACTIVE_MINUTES_WEEK', 0, 5000, 0, 1500,
   'Fifteen hundred minutes is three and a half hours a day, every day.',
   'পনেরোশো মিনিট মানে প্রতিদিন সাড়ে তিন ঘণ্টা।')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The responses
-- ---------------------------------------------------------------------------

-- One row per **item**, which is acceptance criterion 1 and the whole reason this table exists.
--
-- A stored total is a number somebody has to trust; stored items are data. §12's cohorting is
-- done on behaviour, and a total of 14 cannot be re-analysed, re-scored under a corrected formula,
-- or compared against a study that weighted the items differently.
--
-- There is deliberately **no `total` column here**. The total is a function of these rows, and two
-- columns that ought to agree are two columns that will not — on the day they disagree nobody can
-- say which was right.
CREATE TABLE read.instrument_response (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL,
  patient_id  uuid NOT NULL,
  visit_id    uuid,

  instrument_code text NOT NULL,
  -- The version answered, frozen. An instrument whose wording changes next year must not make
  -- last year's answers read as answers to the new question.
  instrument_version integer NOT NULL,

  recorded_at   timestamptz NOT NULL,
  recorded_by   uuid NOT NULL,
  recorded_role text NOT NULL DEFAULT '',
  station_code  text NOT NULL DEFAULT '',
  device_id     uuid,
  source        text NOT NULL DEFAULT '',

  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUPERSEDED')),
  replaced_by uuid,

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL,

  FOREIGN KEY (instrument_code, instrument_version)
    REFERENCES core.instrument_version(instrument_code, version)
);

CREATE INDEX instrument_response_by_patient
  ON read.instrument_response (patient_id, instrument_code, recorded_at DESC);
CREATE INDEX instrument_response_live
  ON read.instrument_response (patient_id, instrument_code) WHERE status = 'ACTIVE';

GRANT SELECT ON read.instrument_response TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON read.instrument_response TO dthcms_projector;

CREATE TABLE read.instrument_answer (
  response_id uuid NOT NULL REFERENCES read.instrument_response(id) ON DELETE CASCADE,
  item_code   text NOT NULL,

  -- Exactly one of these is set, matching the item's answer_type. A trigger checks it against the
  -- item rather than trusting the writer, because a response that answered a coded item with a
  -- number is a response nothing downstream can score.
  option_code  text,
  value_num    numeric,
  value_bool   boolean,

  -- What that answer was worth under the version answered, copied at write time. Copied rather
  -- than joined because a published version's options are frozen; if that ever stops being true,
  -- this column is what keeps last year's totals reproducible.
  score integer NOT NULL DEFAULT 0,

  PRIMARY KEY (response_id, item_code)
);

GRANT SELECT ON read.instrument_answer TO dthcms_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON read.instrument_answer TO dthcms_projector;

COMMENT ON TABLE read.instrument_answer IS
  'One row per item answered. Raw responses, not totals (CP58 criterion 1) — a total cannot be re-scored.';

-- The answer must be the shape the item asks for.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.instrument_answer_matches_its_item() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  wants text;
  code  text;
  ver   integer;
BEGIN
  SELECT r.instrument_code, r.instrument_version INTO code, ver
    FROM read.instrument_response r WHERE r.id = NEW.response_id;

  SELECT i.answer_type INTO wants
    FROM core.instrument_item i
   WHERE i.instrument_code = code AND i.version = ver AND i.item_code = NEW.item_code;

  IF wants IS NULL THEN
    RAISE EXCEPTION '% is not an item of % v%', NEW.item_code, code, ver;
  END IF;

  IF wants = 'coded' AND (NEW.option_code IS NULL OR NEW.value_num IS NOT NULL OR NEW.value_bool IS NOT NULL) THEN
    RAISE EXCEPTION '% expects one of its options', NEW.item_code;
  END IF;
  IF wants = 'numeric' AND (NEW.value_num IS NULL OR NEW.option_code IS NOT NULL OR NEW.value_bool IS NOT NULL) THEN
    RAISE EXCEPTION '% expects a number', NEW.item_code;
  END IF;
  IF wants = 'boolean' AND (NEW.value_bool IS NULL OR NEW.option_code IS NOT NULL OR NEW.value_num IS NOT NULL) THEN
    RAISE EXCEPTION '% expects yes or no', NEW.item_code;
  END IF;

  IF NEW.option_code IS NOT NULL AND NOT EXISTS (
       SELECT 1 FROM core.instrument_option o
        WHERE o.instrument_code = code AND o.version = ver
          AND o.item_code = NEW.item_code AND o.option_code = NEW.option_code) THEN
    RAISE EXCEPTION '% is not an option of % in % v%', NEW.option_code, NEW.item_code, code, ver;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER instrument_answer_matches_its_item
  BEFORE INSERT OR UPDATE ON read.instrument_answer
  FOR EACH ROW EXECUTE FUNCTION read.instrument_answer_matches_its_item();

-- The total, as a function rather than a column. Criterion 1's other half: a screen that wants a
-- total gets one computed from the items every time, so it cannot drift from them.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.instrument_total(p_response uuid) RETURNS integer
LANGUAGE sql STABLE AS $$
  SELECT coalesce(sum(a.score), 0)::integer
    FROM read.instrument_answer a
   WHERE a.response_id = p_response;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION core.instrument_total(uuid) TO dthcms_app, dthcms_projector;

-- ---------------------------------------------------------------------------
-- The projections
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_instrument_response_recorded(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  item jsonb;
BEGIN
  -- A repeat answer supersedes rather than replaces. The old response keeps its row, the same
  -- rule every clinical value in this system follows: an operator who ran the questionnaire twice
  -- because the patient corrected themselves has produced two facts, not one edit.
  UPDATE read.instrument_response
     SET status = 'SUPERSEDED', replaced_by = (p->>'response_id')::uuid
   WHERE patient_id = (p->>'patient_id')::uuid
     AND instrument_code = p->>'instrument_code'
     AND status = 'ACTIVE';

  INSERT INTO read.instrument_response
    (id, facility_id, patient_id, visit_id, instrument_code, instrument_version,
     recorded_at, recorded_by, recorded_role, station_code, device_id, source,
     event_id, global_seq)
  VALUES
    ((p->>'response_id')::uuid, (p->>'facility_id')::uuid, (p->>'patient_id')::uuid,
     nullif(p->>'visit_id', '')::uuid, p->>'instrument_code', (p->>'instrument_version')::integer,
     (p->>'recorded_at')::timestamptz, (p->>'recorded_by')::uuid,
     coalesce(p->>'recorded_role', ''), coalesce(p->>'station_code', ''),
     nullif(p->>'device_id', '')::uuid, coalesce(p->>'source', ''),
     (p->>'event_id')::uuid, (p->>'global_seq')::bigint)
  ON CONFLICT (id) DO NOTHING;

  FOR item IN SELECT * FROM jsonb_array_elements(p->'answers')
  LOOP
    INSERT INTO read.instrument_answer
      (response_id, item_code, option_code, value_num, value_bool, score)
    VALUES
      ((p->>'response_id')::uuid, item->>'item_code',
       nullif(item->>'option_code', ''),
       CASE WHEN item->>'value_num' IS NULL THEN NULL ELSE (item->>'value_num')::numeric END,
       CASE WHEN item->>'value_bool' IS NULL THEN NULL ELSE (item->>'value_bool')::boolean END,
       coalesce((item->>'score')::integer, 0))
    ON CONFLICT (response_id, item_code) DO NOTHING;
  END LOOP;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_instrument_response_recorded(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- D-26, enforced. "We remembered not to type the copyrighted questionnaire in" is not a control.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_unlicensed_instrument_holds_items() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.instrument_item i
    JOIN core.instrument n ON n.code = i.instrument_code
   WHERE NOT n.usable;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% item(s) belong to an instrument this clinic may not use', offenders
      USING HINT = 'D-26: several validated instruments are copyrighted. Record the licence on '
                   'core.instrument before entering any of their wording.';
  END IF;
END
$$;
-- +goose StatementEnd

-- Criterion 4, and criterion 1 from the other side: a response names the version it answered and
-- carries the items, not a total. A response with no answers is a questionnaire nobody filled in,
-- stored as though somebody had.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_response_kept_its_items() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM read.instrument_response r
   WHERE NOT EXISTS (SELECT 1 FROM read.instrument_answer a WHERE a.response_id = r.id);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% instrument response(s) kept no item answers', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_no_unlicensed_instrument_holds_items',
   'no copyrighted questionnaire has been entered without a licence', 80),
  ('assert_every_response_kept_its_items',
   'every questionnaire response kept the answers it was scored from, not just a total', 81)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_no_unlicensed_instrument_holds_items', 'assert_every_response_kept_its_items');
DROP FUNCTION IF EXISTS core.assert_every_response_kept_its_items();
DROP FUNCTION IF EXISTS core.assert_no_unlicensed_instrument_holds_items();
DROP FUNCTION IF EXISTS read.apply_instrument_response_recorded(jsonb);
DROP FUNCTION IF EXISTS core.instrument_total(uuid);
DROP TABLE IF EXISTS read.instrument_answer;
DROP FUNCTION IF EXISTS read.instrument_answer_matches_its_item();
DROP TABLE IF EXISTS read.instrument_response;
DELETE FROM core.plausibility_rule WHERE code IN (
  'CIGARETTES_PER_DAY', 'SMOKING_YEARS', 'SLEEP_MINUTES', 'ACTIVE_MINUTES_WEEK');
DELETE FROM core.observation_code WHERE code IN (
  'CIGARETTES_PER_DAY', 'SMOKING_YEARS', 'SLEEP_MINUTES', 'ACTIVE_MINUTES_WEEK', 'LIFESTYLE_RISK');
DELETE FROM core.unit WHERE dimension = 'duration';
DROP TABLE IF EXISTS core.instrument_option;
DROP TABLE IF EXISTS core.instrument_item;
DROP TABLE IF EXISTS core.instrument_version;
DELETE FROM core.facility_scope_exemption
 WHERE (schema_name = 'core' AND table_name IN ('instrument', 'instrument_version',
                                                'instrument_item', 'instrument_option'))
    OR (schema_name = 'read' AND table_name = 'instrument_answer');
DROP TABLE IF EXISTS core.instrument;
