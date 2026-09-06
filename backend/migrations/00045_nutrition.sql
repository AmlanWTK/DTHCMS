-- The nutrition assessment (CP59, station 7, §12.1).
--
-- # Two people, one recall, and no conflict to resolve
--
-- Criterion 2 and [R-01]/[R-02]: *"a second assistant may enter food habits from their own device"*,
-- concurrently, each attributed. The obvious implementation is a document — an assessment row two
-- devices edit — and then a lock, or a merge, or last-write-wins. All three are wrong here, and the
-- third is wrong in the way that loses a patient's breakfast without telling anybody.
--
-- **A 24-hour recall is not a document. It is a set of things a patient said they ate.** Each is its
-- own row, written once, by one named person, and never edited. Two operators adding items to the
-- same visit's recall cannot conflict because they never write the same row — the conflict is
-- designed out rather than resolved, which is the only version of concurrent editing that is
-- correct at four in the afternoon with a queue waiting.
--
-- The one real collision — the same food entered twice by two people who did not see each other —
-- is a *duplicate*, not a conflict, and it is handled where duplicates belong: visibly, on the
-- screen, by showing who already recorded what. The database refuses only the exact repeat of one
-- event.
--
-- # Household measures, because that is how people describe food
--
-- The plan says it plainly: portion sizes in cups, pieces and spoons. A patient does not say "one
-- hundred and twenty grams of rice"; they say "two cups". A screen that asks for grams asks the
-- operator to do a conversion in their head in front of a patient, and the number that reaches the
-- record is then the operator's arithmetic rather than the patient's answer.
--
-- So the operator records **the measure the patient used and how many of them**, and the grams are
-- the table's business. Both are stored: the answer as given, and the weight it converts to.
--
-- # The food table is a content dependency, and it says so
--
-- The plan calls it out: this needs a national nutrition institute table or equivalent, sourced or
-- authored, and it is not a coding problem. What is seeded here is a **starter list of what this
-- clinic actually sees**, marked unapproved, with its source named on every row. `approved_at` is
-- null on all of it and the API reports that, the same way the critical-value table and the
-- counselling templates do.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Household measures
-- ---------------------------------------------------------------------------

CREATE TABLE core.food_measure (
  code text PRIMARY KEY,

  name_en text NOT NULL,
  name_bn text NOT NULL,

  -- Whether this measure means the same thing for every food. A teaspoon is a teaspoon; a "piece"
  -- is not — a piece of ruti and a piece of fish are different weights, so the grams live on the
  -- food's own portion row rather than here. The flag exists so a screen can tell an operator
  -- which measures it can offer for a food it has no portion row for.
  universal boolean NOT NULL DEFAULT false,

  ordering integer NOT NULL DEFAULT 100,

  CONSTRAINT food_measure_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,31}$'),
  CONSTRAINT food_measure_bilingual   CHECK (btrim(name_en) <> '' AND btrim(name_bn) <> '')
);

GRANT SELECT ON core.food_measure TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'food_measure', 'A cup is a cup in every clinic.')
ON CONFLICT DO NOTHING;

INSERT INTO core.food_measure (code, name_en, name_bn, universal, ordering) VALUES
  ('PIECE',      'piece',        'টুকরা',        false, 10),
  ('CUP',        'cup',          'কাপ',          false, 20),
  ('BOWL',       'bowl',         'বাটি',          false, 30),
  ('PLATE',      'plate',        'প্লেট',         false, 40),
  ('TABLESPOON', 'tablespoon',   'টেবিল চামচ',   false, 50),
  ('TEASPOON',   'teaspoon',     'চা চামচ',      false, 60),
  ('GLASS',      'glass',        'গ্লাস',         false, 70),
  ('HANDFUL',    'handful',      'এক মুঠো',       false, 80),
  -- The escape hatch, and the only universal one. An operator who knows the weight should be able
  -- to say so rather than translating it into cups.
  ('GRAM',       'gram',         'গ্রাম',         true,  99)
ON CONFLICT (code) DO UPDATE SET
  name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn,
  universal = EXCLUDED.universal, ordering = EXCLUDED.ordering;

-- ---------------------------------------------------------------------------
-- The food table
-- ---------------------------------------------------------------------------

CREATE TABLE core.food (
  code text PRIMARY KEY,

  name_en text NOT NULL,
  name_bn text NOT NULL,
  -- What an operator would actually type looking for it. A picker that only matches the formal
  -- name is a picker somebody gives up on: "roti", "ruti" and "রুটি" are one food.
  synonyms text[] NOT NULL DEFAULT ARRAY[]::text[],

  -- What it is, for grouping a picker and for the diet advice CP132 will give.
  group_code text NOT NULL CHECK (group_code IN ('GRAIN', 'PULSE', 'VEGETABLE', 'FRUIT',
                                                  'FISH', 'MEAT', 'EGG', 'DAIRY',
                                                  'OIL', 'SWEET', 'DRINK', 'SNACK', 'OTHER')),

  -- Per hundred grams, as every food composition table in the world reports it.
  kcal_per_100g    numeric NOT NULL CHECK (kcal_per_100g >= 0 AND kcal_per_100g <= 900),
  protein_per_100g numeric NOT NULL DEFAULT 0 CHECK (protein_per_100g >= 0),
  carb_per_100g    numeric NOT NULL DEFAULT 0 CHECK (carb_per_100g >= 0),
  fat_per_100g     numeric NOT NULL DEFAULT 0 CHECK (fat_per_100g >= 0),

  -- Where the numbers came from. On every row, because a food composition table assembled from
  -- three sources and remembered as one is a table nobody can check.
  source text NOT NULL,

  -- Null on everything seeded here. The plan lists the data source as an open decision and this
  -- is what stops a starter list being mistaken for a clinic's agreed table.
  approved_at timestamptz,
  approved_by uuid REFERENCES core.app_user(id),

  retired_at timestamptz,

  CONSTRAINT food_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,47}$'),
  CONSTRAINT food_bilingual   CHECK (btrim(name_en) <> '' AND btrim(name_bn) <> ''),
  CONSTRAINT food_says_its_source CHECK (btrim(source) <> ''),
  CONSTRAINT food_approval CHECK ((approved_at IS NULL) = (approved_by IS NULL))
);

GRANT SELECT ON core.food TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'food', 'A food composition table is reference data, not a clinic''s records.')
ON CONFLICT DO NOTHING;

-- The picker's index. Trigram over the names and the synonyms, because an operator types three
-- letters and expects the list to narrow while they are still typing (criterion 1's four minutes
-- is mostly this).
--
-- The searchable text is a real column kept by a trigger, not an index expression and not a
-- generated column: `array_to_string` is only STABLE, and Postgres will neither index nor generate
-- from a call it cannot promise is immutable. A column is the honest place for it anyway — what a
-- search matches against is a property of the food, and a reader can see it.
ALTER TABLE core.food ADD COLUMN searchable text NOT NULL DEFAULT '';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.food_searchable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.searchable := lower(NEW.name_en || ' ' || NEW.name_bn || ' ' ||
                          array_to_string(NEW.synonyms, ' '));
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER food_searchable
  BEFORE INSERT OR UPDATE ON core.food
  FOR EACH ROW EXECUTE FUNCTION core.food_searchable();

CREATE INDEX food_search ON core.food USING gin (searchable gin_trgm_ops)
  WHERE retired_at IS NULL;

CREATE INDEX food_by_group ON core.food (group_code) WHERE retired_at IS NULL;

-- What one household measure of one food weighs.
CREATE TABLE core.food_portion (
  food_code    text NOT NULL REFERENCES core.food(code),
  measure_code text NOT NULL REFERENCES core.food_measure(code),

  grams numeric NOT NULL CHECK (grams > 0 AND grams <= 5000),
  -- What the operator sees beside the measure: "one medium ruti", "a small teacup". A measure
  -- without a size is a measure two operators use differently.
  note_en text NOT NULL DEFAULT '',
  note_bn text NOT NULL DEFAULT '',

  ordering integer NOT NULL DEFAULT 100,

  PRIMARY KEY (food_code, measure_code)
);

GRANT SELECT ON core.food_portion TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'food_portion', 'What a cup of rice weighs is not a clinic''s data.')
ON CONFLICT DO NOTHING;

-- The grams for one measure of one food, or nothing when the table does not know.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.food_grams(p_food text, p_measure text, p_quantity numeric)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT CASE
           WHEN p_measure = 'GRAM' THEN p_quantity
           ELSE (SELECT p.grams * p_quantity
                   FROM core.food_portion p
                  WHERE p.food_code = p_food AND p.measure_code = p_measure)
         END;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION core.food_grams(text, text, numeric) TO dthcms_app, dthcms_projector;

-- ---------------------------------------------------------------------------
-- A starter list of what this clinic actually sees
-- ---------------------------------------------------------------------------

-- **None of this is approved and the API says so.** The plan names the food composition table as a
-- content dependency needing a national source; what follows is a working list so that station 7
-- can run on day one and so that the nutritionist has something concrete to correct rather than an
-- empty picker and a blank form.
--
-- The numbers are drawn from published composition tables for South Asian foods and are stated to
-- one or two significant figures, because that is the honest precision for "one cup of cooked
-- rice" and a table quoting 129.4 kcal would be pretending otherwise.
INSERT INTO core.food (code, name_en, name_bn, synonyms, group_code,
                       kcal_per_100g, protein_per_100g, carb_per_100g, fat_per_100g, source) VALUES
  ('RICE_BOILED', 'Boiled rice', 'ভাত', ARRAY['bhat','rice','chal'], 'GRAIN',
   130, 2.7, 28, 0.3, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('RUTI_ATTA', 'Ruti (wholemeal)', 'আটার রুটি', ARRAY['roti','ruti','chapati','atta'], 'GRAIN',
   275, 9.0, 55, 3.0, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('PARATHA', 'Paratha', 'পরোটা', ARRAY['porota','paratha'], 'GRAIN',
   330, 7.0, 45, 14, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('MUDI', 'Puffed rice', 'মুড়ি', ARRAY['muri','mudi','puffed rice'], 'SNACK',
   400, 7.5, 88, 1.0, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('DAL_MASUR', 'Lentil dal', 'মসুর ডাল', ARRAY['dal','daal','masur','lentil'], 'PULSE',
   115, 8.0, 18, 0.8, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('FISH_RUI', 'Rui fish', 'রুই মাছ', ARRAY['rui','rohu','mach','fish'], 'FISH',
   145, 19, 0, 7.0, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('FISH_ILISH', 'Ilish fish', 'ইলিশ মাছ', ARRAY['ilish','hilsa'], 'FISH',
   275, 21, 0, 21, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('CHICKEN_CURRY', 'Chicken curry', 'মুরগির মাংস', ARRAY['murgi','chicken','mangsho'], 'MEAT',
   200, 17, 4.0, 13, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('BEEF_CURRY', 'Beef curry', 'গরুর মাংস', ARRAY['goru','beef','mangsho'], 'MEAT',
   260, 18, 3.0, 20, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('EGG_BOILED', 'Boiled egg', 'সিদ্ধ ডিম', ARRAY['dim','egg'], 'EGG',
   155, 13, 1.1, 11, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('POTATO_BHAJI', 'Potato bhaji', 'আলু ভাজি', ARRAY['alu','aloo','potato','bhaji'], 'VEGETABLE',
   150, 2.0, 20, 7.0, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('SHAK', 'Leafy greens', 'শাক', ARRAY['shak','saag','greens'], 'VEGETABLE',
   60, 3.0, 6.0, 3.0, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('BEGUN_BHAJI', 'Fried aubergine', 'বেগুন ভাজি', ARRAY['begun','brinjal','aubergine','eggplant'], 'VEGETABLE',
   140, 1.2, 9.0, 11, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('BANANA', 'Banana', 'কলা', ARRAY['kola','kola','banana'], 'FRUIT',
   90, 1.1, 23, 0.3, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('MANGO', 'Mango', 'আম', ARRAY['aam','mango'], 'FRUIT',
   60, 0.8, 15, 0.4, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('GUAVA', 'Guava', 'পেয়ারা', ARRAY['peyara','guava'], 'FRUIT',
   68, 2.6, 14, 1.0, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('MILK_COW', 'Milk', 'দুধ', ARRAY['dudh','milk'], 'DAIRY',
   65, 3.3, 4.8, 3.5, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('DOI', 'Yoghurt', 'দই', ARRAY['doi','dahi','yoghurt','curd'], 'DAIRY',
   85, 3.5, 10, 3.5, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('TEA_MILK_SUGAR', 'Tea with milk and sugar', 'দুধ চিনি চা', ARRAY['cha','tea','dudh cha'], 'DRINK',
   50, 1.2, 8.0, 1.5, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('SOFT_DRINK', 'Sweet fizzy drink', 'কোমল পানীয়', ARRAY['cola','soft drink','fizzy'], 'DRINK',
   42, 0, 10.6, 0, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('MISTI', 'Sweet (misti)', 'মিষ্টি', ARRAY['misti','mishti','sweet','roshogolla'], 'SWEET',
   320, 5.0, 55, 9.0, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('SINGARA', 'Singara', 'সিঙ্গাড়া', ARRAY['singara','samosa'], 'SNACK',
   310, 5.0, 34, 17, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('BISCUIT', 'Biscuit', 'বিস্কুট', ARRAY['biscuit','cookie'], 'SNACK',
   460, 7.0, 68, 17, 'Published South Asian composition tables; not yet confirmed against a national source.'),
  ('OIL_SOYBEAN', 'Cooking oil', 'সয়াবিন তেল', ARRAY['tel','oil','soybean'], 'OIL',
   885, 0, 0, 100, 'Published South Asian composition tables; not yet confirmed against a national source.')
ON CONFLICT (code) DO UPDATE SET
  name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn, synonyms = EXCLUDED.synonyms,
  group_code = EXCLUDED.group_code, kcal_per_100g = EXCLUDED.kcal_per_100g,
  protein_per_100g = EXCLUDED.protein_per_100g, carb_per_100g = EXCLUDED.carb_per_100g,
  fat_per_100g = EXCLUDED.fat_per_100g, source = EXCLUDED.source;

-- What one of each measure weighs, per food. These are the numbers an operator's four minutes
-- actually depends on: a picker that knows "two ruti" weighs 90 g is a picker nobody has to do
-- arithmetic in front of.
INSERT INTO core.food_portion (food_code, measure_code, grams, note_en, note_bn, ordering) VALUES
  ('RICE_BOILED',    'CUP',        150, 'a teacup, packed',        'এক কাপ, চেপে',        10),
  ('RICE_BOILED',    'PLATE',      300, 'a full plate',            'ভরা এক প্লেট',        20),
  ('RUTI_ATTA',      'PIECE',      45,  'one medium ruti',         'একটি মাঝারি রুটি',    10),
  ('PARATHA',        'PIECE',      60,  'one medium paratha',      'একটি মাঝারি পরোটা',   10),
  ('MUDI',           'CUP',        20,  'a teacup, loose',         'এক কাপ, আলগা',        10),
  ('MUDI',           'HANDFUL',    10,  'one adult handful',       'একজন বড়র এক মুঠো',    20),
  ('DAL_MASUR',      'BOWL',       200, 'a small serving bowl',    'ছোট এক বাটি',         10),
  ('DAL_MASUR',      'CUP',        150, 'a teacup',                'এক কাপ',              20),
  ('FISH_RUI',       'PIECE',      60,  'one curry-cut piece',     'তরকারির এক টুকরা',    10),
  ('FISH_ILISH',     'PIECE',      50,  'one curry-cut piece',     'তরকারির এক টুকরা',    10),
  ('CHICKEN_CURRY',  'PIECE',      65,  'one curry-cut piece',     'তরকারির এক টুকরা',    10),
  ('BEEF_CURRY',     'PIECE',      50,  'one curry-cut piece',     'তরকারির এক টুকরা',    10),
  ('EGG_BOILED',     'PIECE',      55,  'one hen''s egg',          'একটি মুরগির ডিম',     10),
  ('POTATO_BHAJI',   'CUP',        120, 'a teacup',                'এক কাপ',              10),
  ('SHAK',           'CUP',        100, 'a teacup, cooked',        'এক কাপ, রান্না করা',  10),
  ('BEGUN_BHAJI',    'PIECE',      40,  'one fried slice',         'ভাজা একটি টুকরা',     10),
  ('BANANA',         'PIECE',      100, 'one medium banana',       'একটি মাঝারি কলা',     10),
  ('MANGO',          'PIECE',      200, 'one medium mango',        'একটি মাঝারি আম',      10),
  ('GUAVA',          'PIECE',      120, 'one medium guava',        'একটি মাঝারি পেয়ারা',  10),
  ('MILK_COW',       'GLASS',      200, 'one glass',               'এক গ্লাস',            10),
  ('MILK_COW',       'CUP',        150, 'a teacup',                'এক কাপ',              20),
  ('DOI',            'BOWL',       150, 'a small serving bowl',    'ছোট এক বাটি',         10),
  ('TEA_MILK_SUGAR', 'CUP',        150, 'a teacup',                'এক কাপ',              10),
  ('SOFT_DRINK',     'GLASS',      250, 'one glass',               'এক গ্লাস',            10),
  ('MISTI',          'PIECE',      40,  'one sweet',               'একটি মিষ্টি',         10),
  ('SINGARA',        'PIECE',      50,  'one singara',             'একটি সিঙ্গাড়া',       10),
  ('BISCUIT',        'PIECE',      12,  'one biscuit',             'একটি বিস্কুট',        10),
  ('OIL_SOYBEAN',    'TABLESPOON', 14,  'one tablespoon',          'এক টেবিল চামচ',       10),
  ('OIL_SOYBEAN',    'TEASPOON',   5,   'one teaspoon',            'এক চা চামচ',          20)
ON CONFLICT (food_code, measure_code) DO UPDATE SET
  grams = EXCLUDED.grams, note_en = EXCLUDED.note_en, note_bn = EXCLUDED.note_bn,
  ordering = EXCLUDED.ordering;

-- ---------------------------------------------------------------------------
-- The recall
-- ---------------------------------------------------------------------------

-- One row per thing the patient said they ate. **Never edited, never merged, never locked.**
--
-- This is the whole answer to criterion 2. Two operators contributing to one recall never write
-- the same row, so there is nothing to resolve — the alternative designs (a document with a lock,
-- a merge, last-write-wins) each solve a problem this shape does not have, and the third loses a
-- patient's breakfast without telling anybody.
--
-- A mistake is corrected the way every clinical fact in this system is corrected: the entry is
-- withdrawn, with a reason and a name, and stays in the record.
CREATE TABLE read.diet_entry (
  id          uuid PRIMARY KEY,
  facility_id uuid NOT NULL,
  patient_id  uuid NOT NULL,
  visit_id    uuid,

  -- Which day's eating this is. A 24-hour recall taken on Tuesday is about Monday, and a screen
  -- that recorded it as Tuesday's food would make every recall a day wrong.
  recall_date date NOT NULL,
  meal        text NOT NULL CHECK (meal IN ('BREAKFAST', 'MID_MORNING', 'LUNCH',
                                             'AFTERNOON', 'DINNER', 'BEDTIME', 'OTHER')),
  -- Roughly when, on the clinic's wall clock. Optional: a patient who cannot remember the hour
  -- still remembers the meal, and forcing a time would produce invented ones.
  eaten_at_hour integer CHECK (eaten_at_hour BETWEEN 0 AND 23),

  food_code    text NOT NULL REFERENCES core.food(code),
  measure_code text NOT NULL REFERENCES core.food_measure(code),
  -- What the patient said: "two cups". Kept exactly as given.
  --
  -- The ceiling depends on the measure, and the difference is not pedantry: a hundred cups is
  -- nobody's lunch, while two hundred grams is an ordinary plate of rice. One limit for both would
  -- either refuse grams — the escape hatch that stops an unweighable measure being a dead end — or
  -- accept a hundred bowls of dal.
  quantity numeric NOT NULL CHECK (quantity > 0),
  -- And what the table says that weighs. Both, because the answer as given is the evidence and the
  -- grams are an interpretation of it — a record holding only the grams could never be re-derived
  -- when the portion table is corrected.
  grams numeric NOT NULL CHECK (grams > 0),

  -- The energy and macros for this entry, computed from the food table at write time. Copied
  -- rather than joined so that a recall taken today still adds up to what the operator was shown,
  -- after somebody corrects the composition table next month.
  kcal    numeric NOT NULL DEFAULT 0,
  protein numeric NOT NULL DEFAULT 0,
  carb    numeric NOT NULL DEFAULT 0,
  fat     numeric NOT NULL DEFAULT 0,

  note text NOT NULL DEFAULT '',

  recorded_at   timestamptz NOT NULL,
  recorded_by   uuid NOT NULL,
  recorded_role text NOT NULL DEFAULT '',
  station_code  text NOT NULL DEFAULT '',
  device_id     uuid,
  source        text NOT NULL DEFAULT '',

  -- Withdrawn rather than deleted, with a reason and a name. Two operators working one recall will
  -- occasionally record the same rice twice, and the honest correction is a withdrawal somebody
  -- signed rather than a row that disappears.
  withdrawn_at     timestamptz,
  withdrawn_by     uuid,
  withdrawn_reason text NOT NULL DEFAULT '',

  event_id   uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL,

  CONSTRAINT diet_entry_quantity_is_a_serving
    CHECK (CASE WHEN measure_code = 'GRAM' THEN quantity <= 5000 ELSE quantity <= 100 END),
  CONSTRAINT diet_entry_withdrawal_is_complete
    CHECK ((withdrawn_at IS NULL) = (withdrawn_by IS NULL)),
  CONSTRAINT diet_entry_withdrawal_says_why
    CHECK (withdrawn_at IS NULL OR btrim(withdrawn_reason) <> '')
);

CREATE INDEX diet_entry_by_recall
  ON read.diet_entry (patient_id, recall_date, meal) WHERE withdrawn_at IS NULL;
CREATE INDEX diet_entry_by_visit ON read.diet_entry (visit_id) WHERE visit_id IS NOT NULL;

GRANT SELECT ON read.diet_entry TO dthcms_app;
GRANT SELECT, INSERT, UPDATE ON read.diet_entry TO dthcms_projector;

COMMENT ON TABLE read.diet_entry IS
  'One thing a patient said they ate (CP59). Never edited or merged: two operators cannot collide because they never write the same row.';

-- ---------------------------------------------------------------------------
-- The projections
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_diet_entry_recorded(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  weight numeric;
  food   core.food%ROWTYPE;
BEGIN
  SELECT * INTO food FROM core.food WHERE code = p->>'food_code';
  IF NOT FOUND THEN
    RAISE EXCEPTION '% is not in the food table', p->>'food_code';
  END IF;

  weight := core.food_grams(p->>'food_code', p->>'measure_code', (p->>'quantity')::numeric);
  IF weight IS NULL THEN
    -- A measure the table has no portion row for. Refused here rather than guessed, because a
    -- guessed weight becomes a calorie count somebody acts on.
    RAISE EXCEPTION 'the table does not know what % of % weighs',
      p->>'measure_code', p->>'food_code';
  END IF;

  INSERT INTO read.diet_entry
    (id, facility_id, patient_id, visit_id, recall_date, meal, eaten_at_hour,
     food_code, measure_code, quantity, grams,
     kcal, protein, carb, fat, note,
     recorded_at, recorded_by, recorded_role, station_code, device_id, source,
     event_id, global_seq)
  VALUES
    ((p->>'entry_id')::uuid, (p->>'facility_id')::uuid, (p->>'patient_id')::uuid,
     nullif(p->>'visit_id', '')::uuid, (p->>'recall_date')::date, p->>'meal',
     CASE WHEN p->>'eaten_at_hour' IS NULL THEN NULL ELSE (p->>'eaten_at_hour')::integer END,
     p->>'food_code', p->>'measure_code', (p->>'quantity')::numeric, weight,
     round(food.kcal_per_100g    * weight / 100, 1),
     round(food.protein_per_100g * weight / 100, 1),
     round(food.carb_per_100g    * weight / 100, 1),
     round(food.fat_per_100g     * weight / 100, 1),
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
CREATE OR REPLACE FUNCTION read.apply_diet_entry_withdrawn(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  UPDATE read.diet_entry
     SET withdrawn_at     = (p->>'withdrawn_at')::timestamptz,
         withdrawn_by     = (p->>'withdrawn_by')::uuid,
         withdrawn_reason = coalesce(p->>'reason', '')
   WHERE id = (p->>'entry_id')::uuid
     AND withdrawn_at IS NULL;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_diet_entry_recorded(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_diet_entry_withdrawn(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The day's totals
-- ---------------------------------------------------------------------------

-- The intake codes, DERIVED so they inherit the correction cascade, the timeline, the research
-- extract and the attribution. §12.1's diet–outcome analysis reads these.
INSERT INTO core.observation_code
  (code, category, value_type, dimension, display_en, display_bn,
   min_canonical, max_canonical, write_permission) VALUES
  ('ENERGY_INTAKE', 'DERIVED', 'numeric', 'ratio',
   'Energy eaten in a day', 'দিনে কত ক্যালরি খাওয়া', 0, 10000, 'observation.write.nutrition'),
  ('PROTEIN_INTAKE', 'DERIVED', 'numeric', 'ratio',
   'Protein eaten in a day', 'দিনে কত গ্রাম প্রোটিন', 0, 500, 'observation.write.nutrition'),
  ('CARB_INTAKE', 'DERIVED', 'numeric', 'ratio',
   'Carbohydrate eaten in a day', 'দিনে কত গ্রাম শর্করা', 0, 2000, 'observation.write.nutrition'),
  ('FAT_INTAKE', 'DERIVED', 'numeric', 'ratio',
   'Fat eaten in a day', 'দিনে কত গ্রাম চর্বি', 0, 500, 'observation.write.nutrition')
ON CONFLICT (code) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  min_canonical = EXCLUDED.min_canonical, max_canonical = EXCLUDED.max_canonical,
  write_permission = EXCLUDED.write_permission;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- Criterion 3, standing: an entry's energy is the table's, not a client's.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_diet_entry_matches_the_food_table() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM read.diet_entry e
    JOIN core.food f ON f.code = e.food_code
   WHERE abs(e.kcal - round(f.kcal_per_100g * e.grams / 100, 1)) > 0.15
      OR abs(e.grams - coalesce(core.food_grams(e.food_code, e.measure_code, e.quantity), -1)) > 0.001;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% diet entries do not agree with the food table', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- Criterion 2's other half. Every entry names the person who recorded it, because a recall two
-- people contributed to is only useful if you can tell which half is whose.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_diet_entry_is_attributed() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders FROM read.diet_entry WHERE recorded_by IS NULL;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% diet entries name nobody', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- The content dependency, made visible. It cannot fail today — every seeded row names its source —
-- and it is what stops the first hand-added food arriving with no provenance.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_food_names_its_source() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  SELECT code INTO offender FROM core.food WHERE btrim(source) = '' LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION '% does not say where its composition figures came from', offender;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_diet_entry_matches_the_food_table',
   'every recorded food adds up to what the food table says', 83),
  ('assert_every_diet_entry_is_attributed',
   'every recorded food names the person who recorded it', 84),
  ('assert_every_food_names_its_source',
   'every food in the composition table says where its figures came from', 85)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_every_diet_entry_matches_the_food_table',
  'assert_every_diet_entry_is_attributed',
  'assert_every_food_names_its_source');
DROP FUNCTION IF EXISTS core.assert_every_food_names_its_source();
DROP FUNCTION IF EXISTS core.assert_every_diet_entry_is_attributed();
DROP FUNCTION IF EXISTS core.assert_every_diet_entry_matches_the_food_table();
DELETE FROM core.observation_code WHERE code IN (
  'ENERGY_INTAKE', 'PROTEIN_INTAKE', 'CARB_INTAKE', 'FAT_INTAKE');
DROP FUNCTION IF EXISTS read.apply_diet_entry_withdrawn(jsonb);
DROP FUNCTION IF EXISTS read.apply_diet_entry_recorded(jsonb);
DROP TABLE IF EXISTS read.diet_entry;
DROP FUNCTION IF EXISTS core.food_grams(text, text, numeric);
DROP TABLE IF EXISTS core.food_portion;
DROP TABLE IF EXISTS core.food;
DROP FUNCTION IF EXISTS core.food_searchable();
DROP TABLE IF EXISTS core.food_measure;
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name IN ('food', 'food_portion', 'food_measure');
