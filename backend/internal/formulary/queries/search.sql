-- The two-letter prescribing autocomplete (CP76, §10.1).
--
-- Two statements, and neither of them searches. That is the whole design: §10.1 asks for a p99
-- under 50ms including network, the formulary is a few hundred rows, and the plan's own answer
-- is to hold all of it in the API process. So the database's job here is to hand the process
-- the catalogue and to answer, cheaply and often, "has any of it changed since you looked?".
--
-- A SQL `ILIKE '%xx%'` over 250 rows would also be fast today. It would not stay fast once the
-- ranking has a per-physician recency term in it (CP80), because that term cannot be expressed
-- as an index — and the version of this that ships a query now is the version that is rewritten
-- under time pressure later.

-- name: FormularyCacheRows :many
-- Every product in one facility with its **current** price, in one statement.
--
-- Withdrawn products are included and carry `is_active = false`. The matcher drops them from the
-- results, but the cache holds them so that the decision of whether a withdrawn brand is
-- prescribable stays in one place in Go rather than being half in SQL. It is also what lets a
-- withdrawal be reflected by the ordinary refresh instead of needing its own path.
--
-- **The price joined is the one in force on `@on`, not the one whose period is still open.** The
-- distinction is invisible until somebody records a price that starts next Monday — which the
-- monthly review does routinely — and then `effective_to IS NULL` is next Monday's price, shown
-- today, on the screen a physician quotes a cost to a patient from. This is the same predicate
-- CP75's `PriceAsOf` uses, and it is the same predicate for the same reason.
--
-- The price is a LEFT JOIN and every column of it is read as nullable, because a product nobody
-- has priced is an ordinary state of this table — `POST /formulary/products` creates one, and
-- pricing it is a second act by a second person. The autocomplete must show that product with
-- no price rather than not show it: a physician who cannot find a medicine concludes the clinic
-- does not stock it.
SELECT p.id, p.trade_name, p.strength, p.form_code, p.manufacturer, p.dispense_unit,
       p.is_active,
       g.id AS generic_id, g.name AS generic_name, g.class_code,
       c.name_en AS class_name_en, c.name_bn AS class_name_bn, c.ordering AS class_ordering,
       f.name_en AS form_name_en, f.name_bn AS form_name_bn,
       u.name_en AS unit_name_en, u.name_bn AS unit_name_bn,
       mp.id AS price_id, mp.unit_price_poisha, mp.effective_from AS price_from,
       mp.verification, mp.origin
  FROM core.medication_product p
  JOIN core.generic g ON g.id = p.generic_id
  JOIN core.medication_class c ON c.code = g.class_code
  JOIN core.medication_form f ON f.code = p.form_code
  JOIN core.dispense_unit u ON u.code = p.dispense_unit
  LEFT JOIN core.medication_price mp
         ON mp.product_id = p.id
        AND mp.effective_from <= @as_of::date
        AND (mp.effective_to IS NULL OR mp.effective_to > @as_of::date)
 WHERE p.facility_id = @facility_id::uuid
 ORDER BY p.trade_name, p.strength, p.dispense_unit, p.id;

-- name: FormularyWatermark :one
-- "Has anything changed?", as one cheap row.
--
-- **Counts as well as maxima, and that pairing is the point.** A maximum alone misses a
-- deletion, and although this module deletes nothing through the application, a restore, a hand
-- edit or a `Down` migration can still remove rows — and the failure mode of a watermark that
-- cannot see it is a cache serving a medicine the clinic no longer has, silently, until the
-- process restarts. A count alone misses an edit that changes no row count, which is what a
-- price correction and a trade-name fix both are. Together they catch every change this schema
-- can make.
--
-- `updated_at` is maintained by `core.attach_updated_at` on the product and the generic. The
-- price table has no `updated_at` by design (a price is never edited), so it contributes its
-- row count and the newest `recorded_at`: a new price is an insert, and closing a predecessor's
-- range always accompanies one.
--
-- The vocabularies are in here too. A class renamed in Bengali changes what the autocomplete
-- draws beside every product in that class, and a cache that did not notice would show the old
-- name until the process was restarted.
-- Two columns, not ten: a row count and a newest-change instant, each an explicit cast so
-- that the generated Go is `int64` and `time.Time` rather than `interface{}` — `max()` over a
-- possibly-empty set is nullable, and a watermark that arrives as an untyped nil is one the
-- refresh loop compares by pointer identity and never sees change.
SELECT
  ((SELECT count(*) FROM core.medication_product WHERE facility_id = @facility_id::uuid)
   + (SELECT count(*) FROM core.medication_price WHERE facility_id = @facility_id::uuid)
   + (SELECT count(*) FROM core.generic)
   + (SELECT count(*) FROM core.medication_class)
   + (SELECT count(*) FROM core.medication_form)
   + (SELECT count(*) FROM core.dispense_unit))::bigint AS row_count,
  coalesce(
    greatest(
      (SELECT max(updated_at) FROM core.medication_product WHERE facility_id = @facility_id::uuid),
      (SELECT max(recorded_at) FROM core.medication_price WHERE facility_id = @facility_id::uuid),
      (SELECT max(updated_at) FROM core.generic),
      (SELECT max(updated_at) FROM core.medication_class),
      (SELECT max(updated_at) FROM core.medication_form),
      (SELECT max(updated_at) FROM core.dispense_unit)),
    'epoch'::timestamptz)::timestamptz AS changed_at;
