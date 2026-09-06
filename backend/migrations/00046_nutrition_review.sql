-- What building station 7's screen found (CP59, review round).
--
-- Two of these are the same class of bug the counselling and quality reviews turned up: a fact the
-- server knew and did not say, which the client then invented for itself. The rest are the picker's
-- ordering, which is most of what criterion 1's four minutes actually costs.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The day's meals, in both languages
-- ---------------------------------------------------------------------------

-- They were bare enum codes. `core.food_measure` carries a bilingual pair and meals did not, so
-- every client had to invent the Bangla for MID_MORNING and BEDTIME — and web and mobile would have
-- invented different words for the same meal. That is precisely the drift a bilingual reference
-- table exists to prevent.
CREATE TABLE core.meal (
  code text PRIMARY KEY,
  name_en text NOT NULL,
  name_bn text NOT NULL,
  ordering integer NOT NULL,

  CONSTRAINT meal_bilingual CHECK (btrim(name_en) <> '' AND btrim(name_bn) <> '')
);

GRANT SELECT ON core.meal TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'meal', 'Breakfast is breakfast in every clinic.')
ON CONFLICT DO NOTHING;

INSERT INTO core.meal (code, name_en, name_bn, ordering) VALUES
  ('BREAKFAST',   'Breakfast',            'সকালের নাশতা',      1),
  ('MID_MORNING', 'Mid-morning',          'দুপুরের আগে',        2),
  ('LUNCH',       'Lunch',                'দুপুরের খাবার',      3),
  ('AFTERNOON',   'Afternoon',            'বিকেলের খাবার',      4),
  ('DINNER',      'Dinner',               'রাতের খাবার',        5),
  ('BEDTIME',     'Before bed',           'ঘুমানোর আগে',        6),
  ('OTHER',       'Something else',       'অন্য সময়',           7)
ON CONFLICT (code) DO UPDATE SET
  name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn, ordering = EXCLUDED.ordering;

-- ---------------------------------------------------------------------------
-- What the picker shows before anybody types
-- ---------------------------------------------------------------------------

-- The empty query used to fall through to `ORDER BY name_en`, which is alphabetical *by English
-- name* — arbitrary for a Bangla-reading nutritionist, and an arbitrary twenty-five-row slice the
-- moment the table grows past twenty-five.
--
-- CP52 solved the same problem for diagnoses with the clinic's favourites. This is the food
-- equivalent, and it is the single biggest thing standing between this screen and criterion 1's
-- four minutes: the first list an operator sees should be what this clinic actually records, so
-- that most entries need no typing at all.
--
-- The ranks below are a proposal, like every other piece of seeded content here. What should
-- eventually drive them is how often each food is actually recorded — which is a query over
-- `read.diet_entry`, and a decision for after the clinic has a month of data.
ALTER TABLE core.food ADD COLUMN IF NOT EXISTS staple_rank integer;

COMMENT ON COLUMN core.food.staple_rank IS
  'Where this food sits in the list before anybody types. Null sorts last. A proposal until the clinic has recorded a month of recalls (CP59).';

UPDATE core.food SET staple_rank = v.rank
  FROM (VALUES
    ('RICE_BOILED', 1), ('RUTI_ATTA', 2), ('DAL_MASUR', 3), ('SHAK', 4),
    ('POTATO_BHAJI', 5), ('FISH_RUI', 6), ('EGG_BOILED', 7), ('TEA_MILK_SUGAR', 8),
    ('MUDI', 9), ('BANANA', 10), ('CHICKEN_CURRY', 11), ('MILK_COW', 12),
    ('OIL_SOYBEAN', 13), ('PARATHA', 14), ('BEGUN_BHAJI', 15), ('BISCUIT', 16),
    ('DOI', 17), ('MISTI', 18), ('SINGARA', 19), ('BEEF_CURRY', 20),
    ('FISH_ILISH', 21), ('GUAVA', 22), ('MANGO', 23), ('SOFT_DRINK', 24)
  ) AS v(code, rank)
 WHERE core.food.code = v.code;

-- ---------------------------------------------------------------------------
-- The withdrawal projection, facility-scoped
-- ---------------------------------------------------------------------------

-- The handler checked the facility and the projection did not, and the projection is the half that
-- holds under a rebuild. It could not be exploited through the API today; it is fixed because "the
-- handler checks" is the sentence that precedes every one of these that could.
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
     AND facility_id = (p->>'facility_id')::uuid
     AND withdrawn_at IS NULL;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_diet_entry_withdrawn(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- The ceiling belongs on the measure
-- ---------------------------------------------------------------------------

-- It was two numbers on the reference payload — one for household measures and one named for
-- grams — while the only discriminator a client had was `universal`, documented as "true only for
-- grams". Those agree today and diverge the moment a second universal measure is seeded, and the
-- client would then be refusing the wrong quantities against the right flag.
--
-- On the measure, it is simply true.
ALTER TABLE core.food_measure ADD COLUMN IF NOT EXISTS max_quantity integer NOT NULL DEFAULT 100
  CHECK (max_quantity > 0 AND max_quantity <= 100000);

COMMENT ON COLUMN core.food_measure.max_quantity IS
  'The most of this measure one entry may carry. A hundred cups is nobody''s lunch; two hundred grams is an ordinary plate of rice (CP59).';

UPDATE core.food_measure SET max_quantity = 5000 WHERE code = 'GRAM';

-- +goose Down

DELETE FROM core.facility_scope_exemption WHERE schema_name = 'core' AND table_name = 'meal';
ALTER TABLE core.food_measure DROP COLUMN IF EXISTS max_quantity;
ALTER TABLE core.food DROP COLUMN IF EXISTS staple_rank;
DROP TABLE IF EXISTS core.meal;
