-- Coded diagnoses and complaints (CP52).

-- name: CodeSystems :many
-- Which terminologies exist, and what may be done with each. The licence note is part of the
-- answer: the next person to add a system will be tempted to add SNOMED, and this is where
-- they find out that they cannot yet (D-24).
SELECT s.code, s.title_en, s.title_bn, s.publisher, s.licence_note, s.usable,
       v.version AS default_version
  FROM core.code_system s
  LEFT JOIN core.code_system_version v ON v.system = s.code AND v.is_default
 ORDER BY s.code;

-- name: SearchTerminology :many
-- One statement, one ranking, one place to explain why a result came first.
--
-- The tiers, in the order a clinician expects:
--
--   1. the code itself, typed. "E11" should be the first thing "E11" finds.
--   2. a favourite whose words start with what was typed. The clinic's twenty, first.
--   3. any title or synonym whose words start with it.
--   4. anything the trigram index thinks is close, for a misspelling.
--
-- `word` matches at the start of any word rather than only the whole string, because a
-- clinician typing "dia" means diabetes — and a plain prefix would answer "Diabetic
-- polyneuropathy" and not "Type 2 diabetes mellitus", which is the diagnosis this clinic
-- makes more often than any other.
--
-- The tier is returned with the row. "Why is that third" is the question every search gets
-- asked, and a ranking nobody can inspect is a ranking nobody can tune.
WITH needle AS (
  SELECT lower(btrim(@p_query::text)) AS q,
         '%[^[:alnum:]]' || lower(btrim(@p_query::text)) || '%' AS word
),
matched AS (
  SELECT c.system, c.version, c.code, c.display_en, c.display_bn,
         c.heading, c.heading_bn, c.favourite_rank,
         CASE
           WHEN lower(c.code) LIKE (SELECT q FROM needle) || '%' THEN 1
           WHEN c.favourite_rank IS NOT NULL AND core.terminology_matches(
                  c.system, c.version, c.code, c.display_en, c.display_bn,
                  (SELECT q FROM needle), (SELECT word FROM needle)) THEN 2
           WHEN core.terminology_matches(
                  c.system, c.version, c.code, c.display_en, c.display_bn,
                  (SELECT q FROM needle), (SELECT word FROM needle)) THEN 3
           ELSE 4
         END AS tier,
         -- Cast to double precision: `similarity` returns `real`, and a generated type of
         -- `interface{}` for a number the caller sorts on is a number nobody can sort on.
         greatest(
           similarity(lower(c.display_en), (SELECT q FROM needle)),
           similarity(lower(c.display_bn), (SELECT q FROM needle)),
           coalesce((SELECT max(similarity(lower(s.term), (SELECT q FROM needle)))
                       FROM core.terminology_synonym s
                      WHERE s.system = c.system AND s.version = c.version AND s.code = c.code), 0)
         )::double precision AS score
    FROM core.terminology_concept c
   WHERE c.system = @p_system::text AND c.version = @p_version::text AND c.retired_at IS NULL
)
SELECT m.system, m.version, m.code, m.display_en, m.display_bn,
       m.heading, m.heading_bn, m.favourite_rank, m.tier, m.score
  FROM matched m
 -- Tier 4 is the misspelling tier and needs a floor, or every query returns the whole
 -- catalogue in an arbitrary order. 0.25 is loose enough for "diabetis" and tight enough that
 -- "e" does not match everything.
 WHERE m.tier < 4 OR m.score >= 0.25
 ORDER BY m.tier, coalesce(m.favourite_rank, 9999), m.score DESC, length(m.display_en), m.code
 LIMIT @p_limit::int;

-- name: TerminologyFavourites :many
-- The clinic's own list, in the order it was ranked. Criterion 1 — twenty diagnoses in three
-- keystrokes — is reached by knowing which twenty, not by a cleverer search.
SELECT * FROM core.terminology_concept
 WHERE system = $1 AND version = $2 AND favourite_rank IS NOT NULL AND retired_at IS NULL
 ORDER BY favourite_rank;

-- name: TerminologyConcept :one
SELECT * FROM core.terminology_concept
 WHERE system = $1 AND version = $2 AND code = $3;

-- name: TerminologyMappings :many
-- Where a concept maps in another system. Empty until D-24 answers whether SNOMED may be used
-- here; the query exists so that the day it does, nothing above this line changes.
SELECT * FROM core.terminology_map
 WHERE from_system = $1 AND from_version = $2 AND from_code = $3
 ORDER BY to_system, to_version, to_code;
