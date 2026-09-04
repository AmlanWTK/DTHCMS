-- Paediatric growth reference (CP47, D-21).

-- name: GrowthStandardForAge :one
-- Which standard covers this age for this indicator. D-21's protocol, read as data — so
-- changing it is an UPDATE rather than a release, which is what the decision promised.
SELECT standard_code FROM core.growth_band
 WHERE indicator = @indicator::text
   AND @age_months::numeric >= min_age_months
   AND @age_months::numeric <  max_age_months
 ORDER BY min_age_months DESC
 LIMIT 1;

-- name: GrowthStandardVersion :one
-- Stored with every computed percentile, so a value from 2026 stays interpretable if the
-- reference data is ever replaced.
SELECT version FROM core.growth_standard WHERE code = $1;

-- name: GrowthLMS :many
-- One table: an indicator, a sex and a standard, in age order.
SELECT age_months, l, m, s FROM core.growth_lms
 WHERE standard_code = @standard_code::text
   AND indicator = @indicator::text
   AND sex = @sex::text
 ORDER BY age_months;

-- name: GrowthBands :many
-- The protocol for one indicator, oldest band first, with each standard's identity so a
-- chart can label where the reference changes rather than pretending one curve runs the
-- whole way (D-21).
SELECT b.min_age_months, b.max_age_months, b.standard_code, s.version, s.name_en, s.name_bn
  FROM core.growth_band b
  JOIN core.growth_standard s ON s.code = b.standard_code
 WHERE b.indicator = $1
 ORDER BY b.min_age_months;
