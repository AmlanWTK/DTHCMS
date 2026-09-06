-- What building the station screen found (CP58, review round).
--
-- Every change here came from the client side meeting the contract and reporting what it could not
-- do honestly. They are worth recording as a group because they share a shape: each is a fact the
-- server knew and did not say, which the client then either printed in the wrong language,
-- inferred from prose, or re-implemented.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The licence note, in both languages
-- ---------------------------------------------------------------------------

-- Every other string on an instrument is a bilingual pair. The one sentence that must not be
-- misunderstood — why a counsellor cannot run PHQ-9 today — was English only, so a Bangla-reading
-- counsellor read English about a licensing decision.
ALTER TABLE core.instrument ADD COLUMN IF NOT EXISTS licence_note_bn text NOT NULL DEFAULT '';

-- ---------------------------------------------------------------------------
-- Where a questionnaire came from, as a fact rather than as prose
-- ---------------------------------------------------------------------------

-- `READINESS_1` must never read as a validated instrument, and the only signal a client had was
-- `copyright_holder = 'This clinic'` — a string comparison against seeded English prose, holding
-- up the one rule in the feature that matters most. This makes it a column.
ALTER TABLE core.instrument ADD COLUMN IF NOT EXISTS provenance text NOT NULL DEFAULT 'PUBLISHED';

-- +goose StatementBegin
DO $$ BEGIN
  ALTER TABLE core.instrument ADD CONSTRAINT instrument_provenance_known
    CHECK (provenance IN ('PUBLISHED', 'THIS_CLINIC'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- A questionnaire item's unit is a unit
-- ---------------------------------------------------------------------------

-- It was bare text with no dimension and no conversion, so an item asking "how many minutes" was a
-- string a screen could only print — free to disagree with `ACTIVE_MINUTES_WEEK`, which gets CP42's
-- whole framework. Nothing seeded uses a numeric item yet, so this costs nothing today and stops
-- the first one being wrong.
-- +goose StatementBegin
DO $$ BEGIN
  ALTER TABLE core.instrument_item
    ADD CONSTRAINT instrument_item_unit_is_a_unit FOREIGN KEY (unit) REFERENCES core.unit(code);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- The seed, corrected
-- ---------------------------------------------------------------------------

UPDATE core.instrument SET
  provenance = 'PUBLISHED',
  licence_note_bn = CASE code
    WHEN 'PSS_10' THEN 'D-26: ফি নেওয়া হয় এমন ক্লিনিকে ব্যবহারের অনুমতি আছে কিনা তা নিশ্চিত না হওয়া পর্যন্ত প্রশ্নগুলি তোলা হয়নি।'
    WHEN 'PHQ_9'  THEN 'D-26: বিনামূল্যে ব্যবহারযোগ্য বলে ব্যাপকভাবে বলা হয়, তবে এই ক্লিনিকের জন্য তা যাচাই করা হয়নি। যাচাই না হওয়া পর্যন্ত তোলা হয়নি।'
    WHEN 'IPAQ_SHORT' THEN 'D-26: অবাণিজ্যিক গবেষণায় বিনামূল্যে ব্যবহারযোগ্য; ফি নেওয়া ক্লিনিক আলাদা বিষয় এবং তা নিশ্চিত করা দরকার।'
    WHEN 'AUDIT_C' THEN 'বিশ্ব স্বাস্থ্য সংস্থা এটি বিনামূল্যে ব্যবহার ও পুনরুৎপাদনের জন্য প্রকাশ করেছে, চিকিৎসাক্ষেত্রেও।'
    ELSE licence_note_bn
  END
 WHERE code IN ('PSS_10', 'PHQ_9', 'IPAQ_SHORT', 'AUDIT_C');

UPDATE core.instrument SET
  provenance = 'THIS_CLINIC',
  licence_note_bn = 'এখানে লেখা। এটি কোনও যাচাইকৃত (validated) প্রশ্নপত্র নয় এবং সেভাবে উপস্থাপন করা যাবে না।'
 WHERE code = 'READINESS_1';

-- Every instrument says why it may or may not be used, in both languages. The English half was
-- already required for a usable row; this makes the pair required, because a refusal a counsellor
-- cannot read is a refusal they will work around.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_instrument_states_its_licence() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  SELECT code INTO offender
    FROM core.instrument
   WHERE btrim(licence_note) = '' OR btrim(licence_note_bn) = ''
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION '% does not say in both languages why it may or may not be used', offender;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_instrument_states_its_licence',
   'every questionnaire says in both languages why it may or may not be used', 82)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name = 'assert_every_instrument_states_its_licence';
DROP FUNCTION IF EXISTS core.assert_every_instrument_states_its_licence();
ALTER TABLE core.instrument_item DROP CONSTRAINT IF EXISTS instrument_item_unit_is_a_unit;
ALTER TABLE core.instrument DROP CONSTRAINT IF EXISTS instrument_provenance_known;
ALTER TABLE core.instrument DROP COLUMN IF EXISTS provenance;
ALTER TABLE core.instrument DROP COLUMN IF EXISTS licence_note_bn;
