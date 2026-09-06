-- The nutrition assessment (CP59, station 7).

-- name: SearchFoods :many
-- The picker. Criterion 1's four minutes is mostly this query: an operator types three letters and
-- expects the list to narrow while they are still typing.
--
-- Trigram similarity over the names *and the synonyms*, because "roti", "ruti" and "রুটি" are one
-- food and a picker that only matched the formal name is a picker somebody gives up on. A prefix
-- match sorts first — somebody typing "ru" means the food beginning with it — and similarity
-- breaks the ties.
--
-- **`word_similarity`, not `similarity`.** The first version compared a four-letter query against a
-- forty-five-character concatenation of names and synonyms; the score landed near 0.11, well under
-- the default threshold, so the trigram arm never fired at all and every match came from the
-- `LIKE`. The documented misspelling tolerance did not exist — "roti" worked only because it is a
-- literal synonym, and "rutii" found nothing.
--
-- It also fixes the ranking, which was backwards for exactly the foods the synonyms were added
-- for: dividing by the length of the whole string meant a food with six synonyms scored *lower*
-- than one with none for the same query. `word_similarity` compares the query against the closest
-- word, so a long synonym list can only help.
--
-- The `LIKE` arm stays, with its wildcards escaped: a substring match is not a fuzzy match, and an
-- operator typing "ilish" should find "Ilish fish" whatever the trigram thinks. Unescaped, a typed
-- `%` returned the whole table and `_` matched any character — not injection, just a wrong answer.
SELECT code, name_en, name_bn, group_code,
       kcal_per_100g, protein_per_100g, carb_per_100g, fat_per_100g,
       source, approved_at, staple_rank
  FROM core.food
 WHERE retired_at IS NULL
   AND (sqlc.arg(term)::text = ''
        OR lower(sqlc.arg(term)::text) <% searchable
        OR searchable LIKE '%' || replace(replace(replace(
             lower(sqlc.arg(term)::text), '\', '\\'), '%', '\%'), '_', '\_') || '%' ESCAPE '\')
   AND (sqlc.narg(group_code)::text IS NULL OR group_code = sqlc.narg(group_code)::text)
 ORDER BY
   -- A prefix match is what somebody typing two letters means.
   (searchable LIKE lower(sqlc.arg(term)::text) || '%') DESC,
   word_similarity(lower(sqlc.arg(term)::text), searchable) DESC,
   -- And with nothing typed, what this clinic actually records — which is most of what the
   -- four-minute target costs. Null ranks sort last, alphabetically.
   staple_rank NULLS LAST,
   name_en
 LIMIT sqlc.arg(row_limit);

-- name: FoodByCode :one
SELECT code, name_en, name_bn, group_code,
       kcal_per_100g, protein_per_100g, carb_per_100g, fat_per_100g,
       source, approved_at, staple_rank
  FROM core.food
 WHERE code = $1 AND retired_at IS NULL;

-- name: PortionsFor :many
-- What each household measure of one food weighs, with the note that says how big a "piece" is.
-- A measure without a size is a measure two operators use differently.
SELECT p.food_code, p.measure_code, p.grams, p.note_en, p.note_bn,
       m.name_en AS measure_en, m.name_bn AS measure_bn, m.universal, m.max_quantity
  FROM core.food_portion p
  JOIN core.food_measure m ON m.code = p.measure_code
 WHERE p.food_code = ANY(sqlc.arg(food_codes)::text[])
 ORDER BY p.food_code, p.ordering, p.measure_code;

-- name: FoodMeasures :many
SELECT code, name_en, name_bn, universal, max_quantity, ordering
  FROM core.food_measure
 ORDER BY ordering, code;

-- name: Meals :many
-- The day's meals with their names. They were bare enum codes, so every client invented the Bangla
-- for MID_MORNING and BEDTIME — and web and mobile would have invented different words.
SELECT code, name_en, name_bn, ordering
  FROM core.meal
 ORDER BY ordering, code;

-- name: DietEntries :many
-- One day's recall, in the order the day happened. Withdrawn entries come back too — a recall
-- somebody corrected is still a record of what was said and by whom.
SELECT e.id, e.patient_id, e.visit_id, e.recall_date, e.meal, e.eaten_at_hour,
       e.food_code, e.measure_code, e.quantity, e.grams,
       e.kcal, e.protein, e.carb, e.fat, e.note,
       e.recorded_at, e.recorded_by, e.recorded_role, e.station_code, e.device_id, e.source,
       e.withdrawn_at, e.withdrawn_by, e.withdrawn_reason,
       coalesce(f.name_en, e.food_code) AS food_en,
       coalesce(f.name_bn, e.food_code) AS food_bn,
       coalesce(m.name_en, e.measure_code) AS measure_en,
       coalesce(m.name_bn, e.measure_code) AS measure_bn,
       coalesce(u.employee_code, '') AS recorded_by_code,
       coalesce(u.name_en, '')       AS recorded_by_name_en,
       coalesce(u.name_bn, '')       AS recorded_by_name_bn,
       coalesce(w.employee_code, '') AS withdrawn_by_code,
       coalesce(w.name_en, '')       AS withdrawn_by_name_en,
       coalesce(w.name_bn, '')       AS withdrawn_by_name_bn
  FROM read.diet_entry e
  LEFT JOIN core.food f ON f.code = e.food_code
  LEFT JOIN core.food_measure m ON m.code = e.measure_code
  LEFT JOIN core.app_user u ON u.id = e.recorded_by
  LEFT JOIN core.app_user w ON w.id = e.withdrawn_by
 WHERE e.patient_id = sqlc.arg(patient_id)
   AND e.facility_id = sqlc.arg(facility_id)
   AND e.recall_date = sqlc.arg(recall_date)
 ORDER BY
   CASE e.meal WHEN 'BREAKFAST' THEN 1 WHEN 'MID_MORNING' THEN 2 WHEN 'LUNCH' THEN 3
               WHEN 'AFTERNOON' THEN 4 WHEN 'DINNER' THEN 5 WHEN 'BEDTIME' THEN 6
               ELSE 7 END,
   e.eaten_at_hour NULLS LAST,
   e.recorded_at;

-- name: DietTotals :one
-- The day's totals, summed over the entries that still stand. Computed here rather than stored:
-- an entry withdrawn a minute later must not leave its calories behind.
SELECT coalesce(sum(kcal), 0)::numeric    AS kcal,
       coalesce(sum(protein), 0)::numeric AS protein,
       coalesce(sum(carb), 0)::numeric    AS carb,
       coalesce(sum(fat), 0)::numeric     AS fat,
       count(*)::bigint                   AS entries,
       count(DISTINCT recorded_by)::bigint AS contributors
  FROM read.diet_entry
 WHERE patient_id = $1 AND facility_id = $2 AND recall_date = $3
   AND withdrawn_at IS NULL;

-- name: DietEntryByID :one
SELECT id, patient_id, visit_id, recall_date, meal, food_code, measure_code,
       recorded_by, withdrawn_at
  FROM read.diet_entry
 WHERE id = $1 AND facility_id = $2;

-- name: RecallDates :many
-- Which days this patient has a recall for, newest first.
SELECT recall_date, count(*)::bigint AS entries
  FROM read.diet_entry
 WHERE patient_id = $1 AND facility_id = $2 AND withdrawn_at IS NULL
 GROUP BY recall_date
 ORDER BY recall_date DESC
 LIMIT $3;
