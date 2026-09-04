-- Observations and the registry that gives them meaning (CP42).

-- name: ObservationCodes :many
-- The whole registry, with each code's canonical unit. One query rather than three, because
-- a station app fetches this once on start-up and then validates every entry against it,
-- offline, for the rest of the clinic session.
SELECT c.code, c.category, c.value_type, c.dimension, c.loinc,
       c.display_en, c.display_bn, c.min_canonical, c.max_canonical, c.write_permission,
       -- COALESCE, because a unitless code has no canonical unit and sqlc types this
       -- column from the cast: without it the driver is handed a NULL it cannot scan into
       -- a string, and every read of the registry is a 500.
       COALESCE((SELECT u.code FROM core.unit u
                  WHERE u.dimension = c.dimension AND u.is_canonical), '')::text AS canonical_unit
  FROM core.observation_code c
 WHERE c.retired_at IS NULL
 ORDER BY c.category, c.code;

-- name: ObservationCodeByCode :one
SELECT c.code, c.category, c.value_type, c.dimension, c.loinc,
       c.display_en, c.display_bn, c.min_canonical, c.max_canonical, c.write_permission,
       c.retired_at,
       -- COALESCE, because a unitless code has no canonical unit and sqlc types this
       -- column from the cast: without it the driver is handed a NULL it cannot scan into
       -- a string, and every read of the registry is a 500.
       COALESCE((SELECT u.code FROM core.unit u
                  WHERE u.dimension = c.dimension AND u.is_canonical), '')::text AS canonical_unit
  FROM core.observation_code c
 WHERE c.code = $1;

-- name: Units :many
SELECT * FROM core.unit ORDER BY dimension, is_canonical DESC, code;

-- name: UnitByCode :one
SELECT * FROM core.unit WHERE code = $1;

-- name: ObservationByID :one
SELECT * FROM read.observation WHERE id = $1 AND facility_id = $2;

-- name: ObservationsForPatient :many
-- The current value of everything, newest first. Corrected and superseded rows are history,
-- and history is read through ObservationHistoryForCode.
--
-- The tie-break is the ledger's own sequence, not the code. Two values of the same code can
-- share an effective time — a re-measurement in the same minute, a whole station form saved
-- at once — and a caller that takes "the first row for this code" as the current value would
-- otherwise get an arbitrary one of them. A BMI derived from the wrong one of two heights is
-- a plausible-looking wrong number, which is the worst kind (CP45).
SELECT * FROM read.observation
 WHERE patient_id = $1 AND facility_id = $2 AND status = 'ACTIVE'
   AND ($3::text = '' OR category = $3::text)
 ORDER BY effective_at DESC, global_seq DESC
 LIMIT $4;

-- name: ObservationsForVisit :many
SELECT * FROM read.observation
 WHERE visit_id = $1 AND facility_id = $2 AND status = 'ACTIVE'
 ORDER BY effective_at, code;

-- name: ObservationHistoryForCode :many
-- Every value ever recorded for one code on one patient, the replaced ones included. This is
-- what a trend line reads, and what somebody asking "what did it say before" reads.
SELECT * FROM read.observation
 WHERE patient_id = $1 AND facility_id = $2 AND code = $3
 ORDER BY effective_at DESC, global_seq DESC
 LIMIT $4;

-- name: PlausibilityRules :many
-- Every rule, for the station app to evaluate locally. Whole rather than per code, for the
-- same reason the registry is: a tablet fetches it once and warns offline for the rest of
-- the clinic session.
--
-- **Ordered most specific first, in exactly the order core.plausibility_for resolves.** That
-- is the whole trick: the client's rule is "the first one in this list whose predicate
-- matches", which cannot drift from the server's resolution because the server computed the
-- order. A client reimplementing the specificity ranking is a client that one day shows an
-- operator one band and is refused by another.
SELECT * FROM core.plausibility_rule
 ORDER BY code,
          num_nonnulls(sex, min_age_years, max_age_years) DESC,
          coalesce(max_age_years, 200) - coalesce(min_age_years, 0),
          updated_at;

-- name: PlausibilityRuleFor :one
-- The most specific rule for one patient and one code. Resolved by the database so that the
-- client's copy of the resolution rule and the server's cannot disagree about which rule
-- applies — which would show an operator one band and refuse them with another.
SELECT * FROM core.plausibility_for(@p_code::text, @p_sex::text, @p_age_years::numeric);

-- name: ReferenceRanges :many
-- Every normal range, for a station app to flag against locally. Ordered most specific
-- first, in exactly the order core.reference_range_for resolves — so the client takes the
-- first match and never ranks anything itself (the same rule as the plausibility rules).
SELECT * FROM core.reference_range
 ORDER BY code,
          num_nonnulls(sex, min_age_years, max_age_years) DESC,
          coalesce(max_age_years, 200) - coalesce(min_age_years, 0),
          updated_at;
