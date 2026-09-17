-- What a QA rule is looking for, in words a clinician reads (CP83 follow-up).
--
-- # The defect, and why it is a schema change rather than a string fix
--
-- The QA review screen was rendering its own reference data. An officer standing at station 10
-- with a patient waiting read:
--
--   * "no HBA1C in the last 6 months, and none ordered"
--   * "no MONOFILAMENT_LEFT or MONOFILAMENT_RIGHT in the last 1 year"
--   * "no CHOL_LDL or CHOL_TOTAL in the last 1 year, and none ordered"
--
-- Three separate problems in one line, and only the first is a translation bug. `HBA1C` is a
-- database column on a clinical screen. `in the last 1 year` is a machine counting rather than a
-- person speaking. And the third is the one this table has to answer: **`CHOL_LDL` or
-- `CHOL_TOTAL` is not two things, it is a lipid profile** — the rule is looking for one clinical
-- thing and satisfies itself with whichever component the lab happened to report.
--
-- The first two are rendering, and `internal/clinicalterm` now owns them for every module.
-- The third is not. No lookup table can know that those four codes together mean "a lipid
-- profile", because that is a statement about *this rule's intent*, not about the codes — a
-- different rule could list CHOL_LDL alone and mean "an LDL". So the phrase belongs on the rule,
-- beside the codes it is a summary of, and it is editable by the same person and the same
-- permission that edits the codes. A client-side dictionary of "codes that go together" would be
-- reference data living in a place nobody with `qa.rule.write` can reach.
--
-- # The columns say the noun, not the sentence
--
-- `looks_for_en` is "lipid profile", not "a lipid profile" and not "no lipid profile in the last
-- year". The predicate composes the sentence — three shapes compose three different ones from the
-- same noun — and a row that carried an article would read "no a lipid profile". Stated here
-- because the first person to add a rule will type the article.
--
-- # Where the line is drawn, and it is a CHECK rather than a convention
--
-- A rule naming **one** observation code needs no phrase: `core.observation_code.display_en`
-- already says "HbA1c", which is what a clinician calls it, and a second copy of that name on the
-- rule row is a second copy to drift. A rule naming **more than one** code, or naming a
-- counselling item (which has no catalogue of short names at all), has to say what it means —
-- and the constraint refuses the row rather than letting a screen fall back to listing codes.
-- That is the same choice migration 00071 made for `window_days`: the state where a screen looks
-- configured and is not must be unreachable, not merely avoided.

-- +goose Up

ALTER TABLE core.qa_rule
  ADD COLUMN looks_for_en text NOT NULL DEFAULT '',
  ADD COLUMN looks_for_bn text NOT NULL DEFAULT '';

COMMENT ON COLUMN core.qa_rule.looks_for_en IS
  'The one clinical thing this rule is looking for, as a bare noun phrase with no article: '
  '"lipid profile", "foot sensation test". Required when the rule names more than one observation '
  'code or any counselling item; otherwise the observation catalogue answers.';

-- Seeded before the constraint, because the constraint is about rows and there are rows.
UPDATE core.qa_rule SET looks_for_en = v.en, looks_for_bn = v.bn
  FROM (VALUES
    -- Rule 5. Two codes, one cuff, thirty seconds at a station the patient already walked past.
    ('DIABETES_BP',         'blood pressure',      'রক্তচাপ'),
    -- Rule 7. Four codes: two monofilament feet and two risk categories. One examination.
    ('DIABETES_FOOT',       'foot sensation test', 'পায়ের অনুভূতি পরীক্ষা'),
    -- Rule 8. Four codes covering a screen, two eyes and a fundus finding. One screening.
    ('DIABETES_EYE',        'retinopathy screening', 'রেটিনোপ্যাথি স্ক্রিনিং'),
    -- Rule 9. Four codes, and the lab reports whichever it reports.
    ('DIABETES_LIPIDS',     'lipid profile',       'লিপিড প্রোফাইল'),
    -- Rule 13. One code, and the phrase is set anyway: the catalogue calls WBC a "White cell
    -- count", and `docs/qa-rules.md` rule 13 asks for a *baseline full blood count*. The white
    -- cell count is the number that matters and the full blood count is the test that is ordered,
    -- and an officer sending a patient to the lab needs the name of the test.
    ('ANTITHYROID_FBC',     'full blood count',    'সম্পূর্ণ রক্ত পরীক্ষা (সিবিসি)'),
    -- Rule 12. A counselling item has no catalogue of short names — `text_en` is the script the
    -- counsellor reads aloud, four lines of it — so the phrase is the only way this rule can name
    -- itself on a screen.
    ('ANTITHYROID_COUNSEL', 'agranulocytosis warning', 'অ্যাগ্রানুলোসাইটোসিসের সতর্কবার্তা')
  ) AS v(code, en, bn)
 WHERE core.qa_rule.code = v.code;

-- Rules 4, 11 and 18 name one code each — HBA1C, TSH, BODY_WEIGHT — and are deliberately left
-- empty, so that the catalogue path is the one the seeded clinic actually exercises rather than a
-- fallback nothing reaches. `TestNoFindingShowsAnInternalCodeAsItsSentence` and
-- `TestARuleThatNamesSeveralCodesSaysWhatTheyAre` cover both paths.

ALTER TABLE core.qa_rule
  ADD CONSTRAINT qa_rule_says_what_it_looks_for CHECK (
    -- Bilingual or neither, the same rule the titles follow.
    (btrim(looks_for_en) = '') = (btrim(looks_for_bn) = '')
    AND (
      btrim(looks_for_en) <> ''
      OR (
        -- At most one observation code, so `core.observation_code` can answer...
        (CASE WHEN jsonb_typeof(params -> 'codes') = 'array'
              THEN jsonb_array_length(params -> 'codes') ELSE 0 END) <= 1
        -- ...and no counselling item, because nothing can answer for one of those.
        AND (CASE WHEN jsonb_typeof(params -> 'item_codes') = 'array'
                  THEN jsonb_array_length(params -> 'item_codes') ELSE 0 END) = 0
      )
    )
  );

-- +goose StatementBegin
-- Invariant 138.
--
-- The constraint above holds for rows. This holds for the *pairing* — a rule whose single code is
-- not in `core.observation_code` at all. That row passes the constraint, because one code is one
-- code, and then renders as `Chol ldl`: spelled rather than named, which is what
-- `clinicalterm.Spell` deliberately produces so that the failure is legible instead of looking
-- like a database column. It is a defect in the row and it is invisible from the screen, so it is
-- reported here.
--
-- A NOTICE and not an EXCEPTION, for invariant 133's reason: a code that a later migration adds,
-- or a rule written against a code this deployment has not seeded, is a state somebody must see
-- and not a reason to refuse a deploy.
CREATE OR REPLACE FUNCTION core.assert_every_qa_rule_can_name_itself() RETURNS void
LANGUAGE plpgsql
SET search_path = core, pg_catalog
AS $$
DECLARE
  offending text;
BEGIN
  SELECT string_agg(DISTINCT r.code || ' (' || c.code || ')', ', ') INTO offending
    FROM core.qa_rule r
   CROSS JOIN LATERAL jsonb_array_elements_text(
     CASE WHEN jsonb_typeof(r.params -> 'codes') = 'array'
          THEN r.params -> 'codes' ELSE '[]'::jsonb END) AS c(code)
   WHERE r.retired_at IS NULL AND r.enabled
     AND btrim(r.looks_for_en) = ''
     AND NOT EXISTS (SELECT 1 FROM core.observation_code oc WHERE oc.code = c.code);

  IF offending IS NOT NULL THEN
    RAISE NOTICE 'QA rules that would render an observation code no catalogue names: %. '
                 'The finding will read as spelled-out words rather than the test''s name; set '
                 'looks_for_en/looks_for_bn on the rule, or add the code to '
                 'core.observation_code.', offending;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_qa_rule_can_name_itself',
   'every live QA rule can say what it is looking for in words, without falling back to an observation code', 138)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name = 'assert_every_qa_rule_can_name_itself';
DROP FUNCTION IF EXISTS core.assert_every_qa_rule_can_name_itself();

ALTER TABLE core.qa_rule DROP CONSTRAINT IF EXISTS qa_rule_says_what_it_looks_for;
ALTER TABLE core.qa_rule
  DROP COLUMN IF EXISTS looks_for_en,
  DROP COLUMN IF EXISTS looks_for_bn;
