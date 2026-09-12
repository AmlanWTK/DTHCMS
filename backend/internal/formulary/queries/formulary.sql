-- The medicine formulary and its price history (CP75, §10, §16.1).
--
-- The one query that matters most is `PriceAsOf`. Everything else in this file is a catalogue
-- read or a write; that one is what §12.3's affordability research rests on, and it is written
-- so that its correctness is a property of the WHERE clause rather than of the caller's care.

-- name: MedicationClasses :many
SELECT code, name_en, name_bn, atc_code, ordering
  FROM core.medication_class
 WHERE retired_at IS NULL
 ORDER BY ordering, code;

-- name: MedicationForms :many
SELECT code, name_en, name_bn FROM core.medication_form
 WHERE retired_at IS NULL ORDER BY name_en;

-- name: DispenseUnits :many
SELECT code, name_en, name_bn FROM core.dispense_unit
 WHERE retired_at IS NULL ORDER BY name_en;

-- name: GenericByName :one
-- Case-insensitive, because an import will arrive with whatever case the spreadsheet had and a
-- generic is the key every CP77 rule and every CP78 duplicate-therapy check hangs off. Two
-- spellings of one molecule is two rule sets, silently.
SELECT g.id, g.name, g.class_code, g.atc_code, g.is_active,
       g.components_status,
       c.name_en AS class_name_en, c.name_bn AS class_name_bn
  FROM core.generic g
  JOIN core.medication_class c ON c.code = g.class_code
 WHERE lower(g.name) = lower(@name::text);

-- name: Generics :many
SELECT g.id, g.name, g.class_code, g.atc_code, g.is_active,
       g.components_status, g.components_source, g.components_determined_at,
       c.name_en AS class_name_en, c.name_bn AS class_name_bn,
       (SELECT count(*) FROM core.medication_product p
         WHERE p.generic_id = g.id AND p.facility_id = @facility_id::uuid)::bigint AS product_count
  FROM core.generic g
  JOIN core.medication_class c ON c.code = g.class_code
 ORDER BY c.ordering, g.name;

-- name: InsertGeneric :one
INSERT INTO core.generic (name, class_code, atc_code, notes, created_by, updated_by)
VALUES (@name, @class_code, sqlc.narg('atc_code'), sqlc.narg('notes'), @actor_id, @actor_id)
RETURNING id;

-- name: ProductByID :one
-- The product and its vocabularies in both languages. **No price columns.**
--
-- The price is fetched separately, by `PriceAsOf`, which is the same statement the history view
-- and the as-of route use. A second, differently-written "current price" join here would be a
-- second answer to the question this whole module exists to answer once.
SELECT p.id, p.facility_id, p.generic_id, p.trade_name, p.strength, p.form_code, p.manufacturer,
       p.dispense_unit, p.dgda_registration, p.notes, p.source_url, p.is_active,
       p.withdrawn_at, p.withdrawn_reason,
       g.name AS generic_name, g.class_code,
       c.name_en AS class_name_en, c.name_bn AS class_name_bn,
       f.name_en AS form_name_en, f.name_bn AS form_name_bn,
       u.name_en AS unit_name_en, u.name_bn AS unit_name_bn
  FROM core.medication_product p
  JOIN core.generic g ON g.id = p.generic_id
  JOIN core.medication_class c ON c.code = g.class_code
  JOIN core.medication_form f ON f.code = p.form_code
  JOIN core.dispense_unit u ON u.code = p.dispense_unit
 WHERE p.id = @id::uuid AND p.facility_id = @facility_id::uuid;

-- name: SearchProducts :many
-- The admin list. Ordered by generic then trade name so that the four brands of metformin sit
-- together — a pharmacist checking prices reads down a molecule, not down an alphabet.
--
-- `p_query` matches trade name, generic and manufacturer. Not a ranked search: CP76 owns the
-- two-letter autocomplete and its cache, and a second, differently-ranked search in the admin
-- screen would be two answers to one question.
SELECT p.id, p.trade_name, p.strength, p.form_code, p.manufacturer, p.dispense_unit,
       p.dgda_registration, p.is_active, p.withdrawn_at,
       g.id AS generic_id, g.name AS generic_name, g.class_code,
       c.name_en AS class_name_en, c.name_bn AS class_name_bn, c.ordering AS class_ordering,
       f.name_en AS form_name_en, f.name_bn AS form_name_bn,
       u.name_en AS unit_name_en, u.name_bn AS unit_name_bn,
       count(*) OVER ()::bigint AS total_count
  FROM core.medication_product p
  JOIN core.generic g ON g.id = p.generic_id
  JOIN core.medication_class c ON c.code = g.class_code
  JOIN core.medication_form f ON f.code = p.form_code
  JOIN core.dispense_unit u ON u.code = p.dispense_unit
 WHERE p.facility_id = @facility_id::uuid
   AND (@p_query::text = '' OR p.trade_name ILIKE '%' || @p_query::text || '%'
        OR g.name ILIKE '%' || @p_query::text || '%'
        OR p.manufacturer ILIKE '%' || @p_query::text || '%')
   AND (@p_class::text = '' OR g.class_code = @p_class::text)
   AND (NOT @p_active_only::boolean OR p.is_active)
   -- "Show me what nobody has checked" is the monthly review's working list, so it is a filter
   -- on the same endpoint rather than a separate report that could disagree with it.
   AND (NOT @p_provisional_only::boolean
        OR NOT EXISTS (SELECT 1 FROM core.medication_price mp
                        WHERE mp.product_id = p.id AND mp.effective_to IS NULL
                          AND mp.verification = 'VERIFIED'))
 ORDER BY c.ordering, g.name, p.trade_name, p.strength, p.dispense_unit, p.id
 LIMIT @p_limit::int OFFSET @p_offset::int;

-- name: PriceAsOf :many
-- **Criterion 1.** The price that was in force on a given day, and no other.
--
-- `effective_from <= day` is what stops a price that had not taken effect yet from being
-- returned — the failure this checkpoint is most likely to have, because the obvious query
-- ("the latest price for this product") returns a price recorded last week for a prescription
-- written last year and looks perfectly correct while doing it.
--
-- `effective_to > day` rather than `>=`, because the range is half-open: the successor's first
-- day is the predecessor's last-day-plus-one.
--
-- **`:many`, not `:one`, and there is no LIMIT.** The EXCLUDE constraint means at most one row
-- can satisfy both conditions — so a second row coming back is not a tie to be broken, it is
-- proof that this WHERE clause is wrong, and the Go layer refuses rather than picking one. That
-- is the difference between a filter bug that is caught and one that silently prices every
-- prescription at whichever row the planner returned first. A `LIMIT 1` here would hide exactly
-- the defect this query exists to avoid.
SELECT mp.id, mp.product_id, mp.unit_price_poisha, mp.effective_from, mp.effective_to,
       mp.verification, mp.origin, mp.source_note, mp.source_url,
       mp.recorded_at, mp.recorded_by,
       who.name_en AS recorded_by_name_en, who.name_bn AS recorded_by_name_bn,
       who.employee_code AS recorded_by_code
  FROM core.medication_price mp
  LEFT JOIN core.app_user who ON who.id = mp.recorded_by
 WHERE mp.product_id = @product_id::uuid
   AND mp.facility_id = @facility_id::uuid
   AND mp.effective_from <= @on_date::date
   AND (mp.effective_to IS NULL OR mp.effective_to > @on_date::date);

-- name: PriceHistory :many
-- Newest first: the question a person opens this on is "what is it now and what was it before",
-- in that order.
SELECT mp.id, mp.product_id, mp.unit_price_poisha, mp.effective_from, mp.effective_to,
       mp.verification, mp.origin, mp.source_note, mp.source_url,
       mp.recorded_at, mp.recorded_by,
       who.name_en AS recorded_by_name_en, who.name_bn AS recorded_by_name_bn,
       who.employee_code AS recorded_by_code
  FROM core.medication_price mp
  LEFT JOIN core.app_user who ON who.id = mp.recorded_by
 WHERE mp.product_id = @product_id::uuid AND mp.facility_id = @facility_id::uuid
 ORDER BY mp.effective_from DESC, mp.recorded_at DESC;

-- name: ClosePriceAt :exec
-- Closing the open period so a successor can start. Only the open row is touched — a period that
-- has already been closed is refused by `core.medication_price_is_immutable`, so a backdated
-- insert cannot quietly rewrite a range somebody has already reported on.
UPDATE core.medication_price
   SET effective_to = @effective_to::date
 WHERE product_id = @product_id::uuid AND effective_to IS NULL
   AND effective_from < @effective_to::date;

-- name: InsertPrice :one
INSERT INTO core.medication_price
  (facility_id, product_id, unit_price_poisha, effective_from, effective_to,
   verification, origin, source_note, source_url, recorded_by)
VALUES (@facility_id, @product_id, @unit_price_poisha, @effective_from,
        sqlc.narg('effective_to'), @verification, @origin,
        sqlc.narg('source_note'), sqlc.narg('source_url'), sqlc.narg('recorded_by'))
RETURNING id, recorded_at;

-- name: InsertProduct :one
INSERT INTO core.medication_product
  (facility_id, generic_id, trade_name, strength, form_code, manufacturer, dispense_unit,
   dgda_registration, notes, source_url, created_by, updated_by)
VALUES (@facility_id, @generic_id, @trade_name, @strength, @form_code, @manufacturer,
        @dispense_unit, sqlc.narg('dgda_registration'), sqlc.narg('notes'),
        sqlc.narg('source_url'), @actor_id, @actor_id)
RETURNING id;

-- name: UpdateProductDetails :one
-- The descriptive columns only. `is_active` is not here: deactivating is `WithdrawProduct`, an
-- act with a reason and a name against it, and folding it into a general update is how a product
-- ends up withdrawn by a screen that sent the whole form back.
UPDATE core.medication_product
   SET dgda_registration = sqlc.narg('dgda_registration'),
       notes = sqlc.narg('notes'),
       source_url = sqlc.narg('source_url'),
       generic_id = @generic_id,
       updated_by = @actor_id
 WHERE id = @id::uuid AND facility_id = @facility_id::uuid
RETURNING id;

-- name: WithdrawProduct :one
UPDATE core.medication_product
   SET is_active = false, withdrawn_at = now(),
       withdrawn_reason = @reason, withdrawn_by = @actor_id, updated_by = @actor_id
 WHERE id = @id::uuid AND facility_id = @facility_id::uuid AND is_active
RETURNING id;

-- name: ReinstateProduct :one
UPDATE core.medication_product
   SET is_active = true, withdrawn_at = NULL, withdrawn_reason = NULL, withdrawn_by = NULL,
       updated_by = @actor_id
 WHERE id = @id::uuid AND facility_id = @facility_id::uuid AND NOT is_active
RETURNING id;

-- name: ProductIdentity :one
-- The natural key lookup an import uses to decide create-or-update. Every comparison is the one
-- the unique index uses, so a row this returns nothing for is a row INSERT will accept.
SELECT p.id, p.generic_id, p.is_active
  FROM core.medication_product p
 WHERE p.facility_id = @facility_id::uuid
   AND lower(p.trade_name) = lower(@trade_name::text)
   AND lower(p.strength) = lower(@strength::text)
   AND p.form_code = @form_code::text
   AND lower(p.manufacturer) = lower(@manufacturer::text)
   AND p.dispense_unit = @dispense_unit::text;

-- name: InsertImport :one
INSERT INTO core.formulary_import (facility_id, filename, mode, imported_by, created_by, updated_by)
VALUES (@facility_id, @filename, @mode, @imported_by, @actor_id, @actor_id)
RETURNING id, imported_at;

-- name: FinishImport :exec
UPDATE core.formulary_import
   SET rows_total = @rows_total, rows_accepted = @rows_accepted, rows_rejected = @rows_rejected,
       products_created = @products_created, products_updated = @products_updated,
       prices_recorded = @prices_recorded, updated_by = @actor_id
 WHERE id = @id::uuid;

-- name: InsertImportRow :exec
INSERT INTO core.formulary_import_row
  (import_id, facility_id, line_number, outcome, field, message_en, message_bn, raw_line, product_id)
VALUES (@import_id, @facility_id, @line_number, @outcome, sqlc.narg('field'),
        sqlc.narg('message_en'), sqlc.narg('message_bn'), sqlc.narg('raw_line'),
        sqlc.narg('product_id'));

-- name: ImportByID :one
SELECT i.id, i.filename, i.mode, i.rows_total, i.rows_accepted, i.rows_rejected,
       i.products_created, i.products_updated, i.prices_recorded, i.imported_at,
       who.name_en AS imported_by_name_en, who.name_bn AS imported_by_name_bn,
       who.employee_code AS imported_by_code
  FROM core.formulary_import i
  JOIN core.app_user who ON who.id = i.imported_by
 WHERE i.id = @id::uuid AND i.facility_id = @facility_id::uuid;

-- name: Imports :many
SELECT i.id, i.filename, i.mode, i.rows_total, i.rows_accepted, i.rows_rejected,
       i.products_created, i.products_updated, i.prices_recorded, i.imported_at,
       who.name_en AS imported_by_name_en, who.name_bn AS imported_by_name_bn,
       who.employee_code AS imported_by_code
  FROM core.formulary_import i
  JOIN core.app_user who ON who.id = i.imported_by
 WHERE i.facility_id = @facility_id::uuid
 ORDER BY i.imported_at DESC
 LIMIT @p_limit::int;

-- name: ImportRows :many
-- Rejections first, then in file order. A person opening a 250-line import report wants the
-- eleven lines that failed, not to scroll past two hundred successes to find them.
SELECT line_number, outcome, field, message_en, message_bn, raw_line, product_id
  FROM core.formulary_import_row
 WHERE import_id = @import_id::uuid AND facility_id = @facility_id::uuid
   AND (NOT @p_rejected_only::boolean OR outcome = 'REJECTED')
 ORDER BY (outcome = 'REJECTED') DESC, line_number
 LIMIT @p_limit::int;

-- name: ReviewOwner :one
SELECT o.facility_id, o.owner_role, o.owner_user_id, o.due_day_of_month,
       r.name_en AS role_name_en, r.name_bn AS role_name_bn,
       who.name_en AS owner_name_en, who.name_bn AS owner_name_bn,
       who.employee_code AS owner_code
  FROM core.formulary_review_owner o
  JOIN core.role r ON r.code = o.owner_role
  LEFT JOIN core.app_user who ON who.id = o.owner_user_id
 WHERE o.facility_id = @facility_id::uuid;

-- name: SetReviewOwner :exec
INSERT INTO core.formulary_review_owner
  (facility_id, owner_role, owner_user_id, due_day_of_month, created_by, updated_by)
VALUES (@facility_id, @owner_role, sqlc.narg('owner_user_id'), @due_day_of_month,
        @actor_id, @actor_id)
ON CONFLICT (facility_id) DO UPDATE SET
  owner_role = EXCLUDED.owner_role, owner_user_id = EXCLUDED.owner_user_id,
  due_day_of_month = EXCLUDED.due_day_of_month, updated_by = EXCLUDED.updated_by;

-- name: OpenReview :one
-- The month's cycle. The unique index on (facility_id, period_month) is what makes the reminder
-- idempotent: the daily job can run twice, or two workers can both claim it, and this inserts
-- nothing the second time — so the owner is reminded once a month rather than once a day.
INSERT INTO core.formulary_price_review
  (facility_id, period_month, owner_role, owner_user_id, due_on,
   products_at_open, provisional_at_open)
VALUES (@facility_id, @period_month, @owner_role, sqlc.narg('owner_user_id'), @due_on,
        @products_at_open, @provisional_at_open)
ON CONFLICT (facility_id, period_month) DO NOTHING
RETURNING id;

-- name: MarkReviewReminded :exec
UPDATE core.formulary_price_review
   SET reminded_at = now(), alert_id = sqlc.narg('alert_id')
 WHERE id = @id::uuid AND reminded_at IS NULL;

-- name: CurrentReview :one
-- The open cycle, or the most recent one if none is open. A screen that showed nothing between
-- one review closing and the next opening would read as "no review is owed", which is the
-- opposite of true for the twenty-nine days in between.
SELECT v.id, v.period_month, v.owner_role, v.owner_user_id, v.status, v.opened_at, v.due_on,
       v.reminded_at, v.products_at_open, v.provisional_at_open,
       v.completed_at, v.completion_note,
       r.name_en AS role_name_en, r.name_bn AS role_name_bn,
       own.name_en AS owner_name_en, own.name_bn AS owner_name_bn,
       own.employee_code AS owner_code,
       done.name_en AS completed_by_name_en, done.name_bn AS completed_by_name_bn,
       done.employee_code AS completed_by_code
  FROM core.formulary_price_review v
  JOIN core.role r ON r.code = v.owner_role
  LEFT JOIN core.app_user own ON own.id = v.owner_user_id
  LEFT JOIN core.app_user done ON done.id = v.completed_by
 WHERE v.facility_id = @facility_id::uuid
 ORDER BY (v.status = 'OPEN') DESC, v.period_month DESC
 LIMIT 1;

-- name: CompleteReview :one
UPDATE core.formulary_price_review
   SET status = 'COMPLETE', completed_at = now(), completed_by = @actor_id,
       completion_note = sqlc.narg('completion_note'), updated_by = @actor_id
 WHERE id = @id::uuid AND facility_id = @facility_id::uuid AND status = 'OPEN'
RETURNING id, period_month;

-- name: ReviewCounts :one
-- What the review is being asked to look at. `provisional` counts products whose *current* price
-- nobody has checked, and products with no price at all — both are things a person has to decide
-- about, and a count that quietly omitted the second would under-report the work.
SELECT count(*)::bigint AS products,
       count(*) FILTER (
         WHERE NOT EXISTS (SELECT 1 FROM core.medication_price mp
                            WHERE mp.product_id = p.id AND mp.effective_to IS NULL
                              AND mp.verification = 'VERIFIED'))::bigint AS provisional,
       coalesce(max(extract(epoch FROM (now() - (
         SELECT mp.recorded_at FROM core.medication_price mp
          WHERE mp.product_id = p.id AND mp.effective_to IS NULL)))::bigint), 0)::bigint
         AS oldest_price_age_seconds
  FROM core.medication_product p
 WHERE p.facility_id = @facility_id::uuid AND p.is_active;

-- name: FacilitiesForReview :many
-- Every clinic the daily job has to consider. One row today; the job is written for the set
-- because a job that hard-codes "the facility" is one that stops working at §15.3 Phase 4.
SELECT f.id, f.timezone, o.owner_role, o.owner_user_id, o.due_day_of_month
  FROM core.facility f
  JOIN core.formulary_review_owner o ON o.facility_id = f.id
 WHERE f.is_active
 ORDER BY f.id;

-- name: RaiseReviewAlert :one
INSERT INTO core.admin_alert (facility_id, kind, severity, message_en, message_bn, reference)
VALUES (@facility_id, 'formulary.price_review_due', 'normal', @message_en, @message_bn, @reference)
RETURNING id;

-- name: CurrentPricesFor :many
-- The current price of each of a page of products, in one statement.
--
-- A separate query rather than a join on the list above, and deliberately so. sqlc reads the
-- schema to decide what can be null, and it cannot see that a LEFT JOIN or a scalar subquery
-- makes a NOT NULL column nullable — it generates `int64` for a price that may be absent, and a
-- product nobody has priced then fails to scan at the moment somebody adds one through the UI.
-- Two statements whose types are honest beat one whose types are a lie, and this is still one
-- round trip per page rather than one per row.
SELECT mp.product_id, mp.id, mp.unit_price_poisha, mp.effective_from,
       mp.verification, mp.origin, mp.recorded_at
  FROM core.medication_price mp
 WHERE mp.facility_id = @facility_id::uuid
   AND mp.effective_to IS NULL
   AND mp.product_id = ANY(@product_ids::uuid[]);


-- name: GenericComponents :many
-- What every medicine in the formulary is made of (CP78, migration 00058).
--
-- Unfiltered and unpaginated on purpose: the answer is 59 generics and their molecules, which is
-- the whole table. The safety engine needs the complete map to answer "is metformin in this
-- prescription twice", and a query that returned only the molecules of the drugs it was asked
-- about would need the caller to already know what they contained.
--
-- `components_status` travels with each row rather than being inferred from the presence of
-- components, because the two can disagree and the disagreement is exactly the fail-closed case:
-- a generic with no components and no status is *undetermined*, not *composed of nothing*.
SELECT g.id, g.name, g.class_code, g.components_status,
       c.ordinal, c.molecule
  FROM core.generic g
  LEFT JOIN core.generic_component c ON c.generic_id = g.id
 ORDER BY g.name, c.ordinal;
