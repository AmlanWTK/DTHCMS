-- The medicine formulary, its price history, and the monthly price review (CP75, §10, §16.1, D-56).
--
-- # The one thing this migration exists to get right
--
-- §12.3's affordability research needs **the price at the time of prescribing**, not today's
-- price. A schema in which a price is a column on a product cannot answer that question, and
-- every attempt to bolt history onto one afterwards ends with a history that starts on the day
-- somebody noticed. So a price is a **row with a date range** from the first day, a product has
-- no price column at all, and there is no UPDATE path that changes what a price was: correcting
-- a price is a new row that supersedes the old one from a date.
--
-- `core.medication_price` carries an EXCLUDE constraint over `daterange(effective_from,
-- effective_to)`. That is what makes "what did this cost on 4 March 2024" a question with exactly
-- one answer — not by the Go layer being careful, but because PostgreSQL will not hold two
-- overlapping prices for one product. Half-open `[from, to)`: a price effective from the 4th *is*
-- the price on the 4th, and its successor effective from the 1st of June takes over **on** the
-- 1st of June.
--
-- # Money is integer poisha, not a float and not a numeric
--
-- `bigint`, in poisha (1 BDT = 100 poisha), the same shape as CP70's `cost_micro_usd`. Three
-- reasons, in order of how much they matter:
--
--  1. A float cannot hold 0.34 and a research extract that adds up a year of them is wrong by an
--     amount nobody can predict.
--  2. The value space is exactly the space of prices that can be charged at a counter in
--     Faridpur, so "is this the same price" is integer equality rather than a tolerance somebody
--     picks. An import that arrives with more precision than a poisha is **refused with a named
--     error**, not silently rounded — a rounded price is one nobody can reconcile against its
--     source.
--  3. `numeric` would be exact too, and it arrives in Go as `pgtype.Numeric`, which every caller
--     has to remember to check and some caller eventually will not.
--
-- The limit of this choice, stated plainly: a price *per IU* or *per mL* can be finer than a
-- poisha. Every price this clinic quotes is per tablet, per vial, per pen or per cartridge, and
-- those are all whole poisha. The day somebody wants per-IU insulin pricing, that is a new
-- column with its own scale, not a migration of this one.
--
-- # Why every seeded price is PROVISIONAL, and why that is a constraint rather than a convention
--
-- The 250 seeded prices are published MRP scraped from medex.com.bd on 8 September 2026. They
-- are **not what this clinic charges**, and Dr. Nahid has not reviewed them. A price nobody has
-- checked that looks like a price somebody approved is the single most dangerous thing this
-- module could ship, so:
--
--   * `verification` is PROVISIONAL or VERIFIED, and the API and the screens carry it;
--   * a seeded price has no `recorded_by`, because no person recorded it — and the check
--     constraint `price_seed_is_never_verified` makes it impossible for such a row to be VERIFIED.
--     Clearing a provisional price therefore *requires* a human to write a new row with their
--     name on it, which is exactly what the monthly review does.
--
-- Every other price names the person who recorded it. `price_names_who_recorded_it` is the
-- constraint, and `assert_every_verified_price_names_a_person()` re-checks the whole table after
-- every migration, because a constraint added later does not validate what is already there.
--
-- # DGDA registration numbers
--
-- Modelled, and null on all 250. Not one of them was available from the source, and a fabricated
-- regulatory identifier in a clinical system is worse than an absent one.
--
-- # Nothing is deleted
--
-- A withdrawn product is deactivated (`is_active = false`, with `withdrawn_at` and a reason) and
-- its price history stays. `dthcms_app` holds no DELETE on any table here.

-- +goose Up

-- btree_gist is what lets an EXCLUDE constraint mix an equality on product_id with an overlap on
-- a daterange. Without it the non-overlap guarantee would have to be a trigger, and a trigger is
-- a thing somebody can turn off.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- ---------------------------------------------------------------------------
-- 1. The vocabularies
-- ---------------------------------------------------------------------------

-- Three small bilingual lists: the therapeutic class, the dosage form, and the unit a price is
-- per. Tables rather than strings in the application, for the reason every vocabulary in this
-- system is a table — the Bengali has to live somewhere a physician can correct it without a
-- deploy, and a CSV import needs something to validate against so that "Tablte" is a named
-- error on row 14 rather than a fourteenth dosage form.

CREATE TABLE core.medication_class (
  code text PRIMARY KEY,

  name_en text NOT NULL,
  name_bn text NOT NULL,

  -- The ATC code, where this clinic has decided one. Null on all 33 seeded classes: D-56 chose a
  -- curated formulary over the national database, and the seed's classes are the ones a
  -- Bangladeshi endocrinologist uses in conversation, which are not always an ATC level.
  atc_code text,

  ordering   integer NOT NULL,
  retired_at timestamptz,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT medication_class_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,59}$'),
  CONSTRAINT medication_class_bilingual
    CHECK (btrim(name_en) <> '' AND btrim(name_bn) <> ''),
  CONSTRAINT medication_class_atc_format
    CHECK (atc_code IS NULL OR atc_code ~ '^[A-Z][0-9]{2}[A-Z]{0,2}[0-9]{0,2}$')
);

SELECT core.attach_updated_at('core.medication_class');
GRANT SELECT, INSERT, UPDATE ON core.medication_class TO dthcms_app;

CREATE TABLE core.medication_form (
  code text PRIMARY KEY,
  name_en text NOT NULL,
  name_bn text NOT NULL,
  retired_at timestamptz,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT medication_form_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,59}$'),
  CONSTRAINT medication_form_bilingual CHECK (btrim(name_en) <> '' AND btrim(name_bn) <> '')
);

SELECT core.attach_updated_at('core.medication_form');
GRANT SELECT, INSERT, UPDATE ON core.medication_form TO dthcms_app;

-- What a price is per. `unit_price_bdt` in the source data is a price per tablet, per vial, per
-- pen or per cartridge, and which of those it is changes the number by two orders of magnitude —
-- so it is part of a product's identity, not a note on it.
CREATE TABLE core.dispense_unit (
  code text PRIMARY KEY,
  name_en text NOT NULL,
  name_bn text NOT NULL,
  retired_at timestamptz,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT dispense_unit_code_format CHECK (code ~ '^[a-z][a-z0-9_]{2,29}$'),
  CONSTRAINT dispense_unit_bilingual CHECK (btrim(name_en) <> '' AND btrim(name_bn) <> '')
);

SELECT core.attach_updated_at('core.dispense_unit');
GRANT SELECT, INSERT, UPDATE ON core.dispense_unit TO dthcms_app;

-- ---------------------------------------------------------------------------
-- 2. The generic
-- ---------------------------------------------------------------------------

-- The molecule, which is what a rule (CP77), a duplicate-therapy check (CP78) and an allergy
-- cross-reactivity map all key off. Latin script and no Bengali column, deliberately: a generic
-- name is an international non-proprietary name, and transliterating it would create a second
-- spelling for the thing every safety rule matches on.
CREATE TABLE core.generic (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  name text NOT NULL,
  class_code text NOT NULL REFERENCES core.medication_class(code),

  -- The molecule's own ATC code, where known. Null on all 59 seeded generics for the reason
  -- above: D-56 did not buy the national database, and an ATC code guessed from a class is a
  -- fabricated regulatory identifier.
  atc_code text,

  notes text,
  is_active boolean NOT NULL DEFAULT true,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT generic_name_present CHECK (btrim(name) <> ''),
  CONSTRAINT generic_atc_format
    CHECK (atc_code IS NULL OR atc_code ~ '^[A-Z][0-9]{2}[A-Z]{2}[0-9]{2}$')
);

-- Case-insensitive uniqueness: "Metformin hydrochloride" and "metformin hydrochloride" are one
-- molecule, and a CSV import that created both would split every rule written against it.
CREATE UNIQUE INDEX generic_name_key ON core.generic (lower(name));
CREATE INDEX generic_class_idx ON core.generic (class_code);

SELECT core.attach_updated_at('core.generic');
GRANT SELECT, INSERT, UPDATE ON core.generic TO dthcms_app;

-- ---------------------------------------------------------------------------
-- 3. The product
-- ---------------------------------------------------------------------------

-- What is actually written on a prescription: a trade name, a strength, a form, a manufacturer,
-- and the unit it is dispensed in.
--
-- **Facility-scoped**, unlike the generic and the class above. Which brands a clinic stocks and
-- what it charges for them are that clinic's business (D-61, §15.3 Phase 4); which molecules
-- exist and what class they belong to is not.
CREATE TABLE core.medication_product (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  generic_id uuid NOT NULL REFERENCES core.generic(id),

  -- Latin script, always. A trade name is what is printed on the box the patient is handed, and
  -- a transliterated one is a different medicine as far as the pharmacy counter is concerned.
  trade_name text NOT NULL,
  strength   text NOT NULL,
  form_code  text NOT NULL REFERENCES core.medication_form(code),
  manufacturer text NOT NULL,
  dispense_unit text NOT NULL REFERENCES core.dispense_unit(code),

  -- The DGDA registration number. Null on every seeded row; see the header. Format unenforced
  -- beyond non-emptiness, because this clinic has not yet seen enough of them to know the shape.
  dgda_registration text,

  notes text,
  -- Where the row came from, so a price nobody trusts can be traced back to what was read.
  source_url text,

  is_active boolean NOT NULL DEFAULT true,
  withdrawn_at timestamptz,
  withdrawn_reason text,
  withdrawn_by uuid REFERENCES core.app_user(id),

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT product_trade_name_present CHECK (btrim(trade_name) <> ''),
  CONSTRAINT product_strength_present CHECK (btrim(strength) <> ''),
  CONSTRAINT product_manufacturer_present CHECK (btrim(manufacturer) <> ''),
  CONSTRAINT product_dgda_not_blank
    CHECK (dgda_registration IS NULL OR btrim(dgda_registration) <> ''),
  -- Withdrawal is attributed or it did not happen. A product that went inactive with nobody's
  -- name and no reason against it is the formulary equivalent of the unattributed price change
  -- this whole module exists to prevent.
  CONSTRAINT product_withdrawal_is_attributed
    CHECK ((withdrawn_at IS NULL) = (withdrawn_by IS NULL)
           AND (withdrawn_at IS NULL OR btrim(coalesce(withdrawn_reason, '')) <> '')),
  CONSTRAINT product_withdrawn_is_inactive
    CHECK (withdrawn_at IS NULL OR NOT is_active)
);

-- The natural key. `dispense_unit` is in it because Ansulin R 100 IU/mL comes as a vial at 415
-- BDT and as a cartridge at 220 BDT — same brand, same strength, same form, same maker, two
-- products. A key without it collapses fifteen pairs in the seed into seven and a half rows.
CREATE UNIQUE INDEX medication_product_identity_key ON core.medication_product
  (facility_id, lower(trade_name), lower(strength), form_code, lower(manufacturer), dispense_unit);
CREATE INDEX medication_product_generic_idx ON core.medication_product (generic_id);
CREATE INDEX medication_product_active_idx ON core.medication_product (facility_id) WHERE is_active;

SELECT core.attach_updated_at('core.medication_product');
GRANT SELECT, INSERT, UPDATE ON core.medication_product TO dthcms_app;

-- ---------------------------------------------------------------------------
-- 4. The price history
-- ---------------------------------------------------------------------------

CREATE TABLE core.medication_price (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid NOT NULL REFERENCES core.facility(id),
  product_id uuid NOT NULL REFERENCES core.medication_product(id),

  unit_price_poisha bigint NOT NULL,

  -- Valid time, on the clinic's calendar. `date` rather than `timestamptz` because a price is a
  -- commercial fact that holds for a day: nobody in this clinic can say what a medicine cost at
  -- 14:05 as distinct from 09:00, and a timestamp would invite a precision the source does not
  -- have. Rendered in Asia/Dhaka; the conversion happens once, in the Go layer.
  effective_from date NOT NULL,
  -- Exclusive. Null while this is the current price.
  effective_to date,

  verification text NOT NULL DEFAULT 'PROVISIONAL'
    CHECK (verification IN ('PROVISIONAL', 'VERIFIED')),

  -- How this row got here. SEED is the only one that may have no person against it.
  origin text NOT NULL CHECK (origin IN ('SEED', 'MANUAL', 'IMPORT', 'REVIEW')),
  source_note text,
  source_url text,

  -- Transaction time: when the clinic learned this, as distinct from when it took effect. Kept
  -- so that "what did we believe the price was last March" stays answerable even though the
  -- queries below answer the valid-time question.
  recorded_at timestamptz NOT NULL DEFAULT now(),
  recorded_by uuid REFERENCES core.app_user(id),

  -- A price is never edited, so this table has no updated_at and no updated_by. The only column
  -- that ever changes is effective_to, and it changes exactly once, when a successor arrives.
  created_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT price_is_positive CHECK (unit_price_poisha > 0),
  -- One hundred thousand taka for one tablet is not a price, it is a decimal point in the wrong
  -- place. The most expensive thing in the seed is a 14,259 BDT semaglutide pen.
  CONSTRAINT price_is_plausible CHECK (unit_price_poisha <= 10000000),
  CONSTRAINT price_period_is_forwards
    CHECK (effective_to IS NULL OR effective_to > effective_from),

  -- The two constraints that carry the checkpoint's security criterion.
  CONSTRAINT price_names_who_recorded_it
    CHECK ((origin = 'SEED') = (recorded_by IS NULL)),
  CONSTRAINT price_seed_is_never_verified
    CHECK (origin <> 'SEED' OR verification = 'PROVISIONAL'),

  -- No two prices for one product may cover the same day. This is what makes "the price on
  -- 4 March 2024" a question with exactly one answer, enforced by PostgreSQL rather than by the
  -- application remembering to close the previous row first.
  CONSTRAINT price_periods_do_not_overlap EXCLUDE USING gist (
    product_id WITH =,
    daterange(effective_from, effective_to, '[)') WITH &&
  )
);

-- The index the as-of query runs on: product, then the day.
CREATE INDEX medication_price_asof_idx
  ON core.medication_price (product_id, effective_from DESC);
CREATE INDEX medication_price_current_idx
  ON core.medication_price (facility_id, product_id) WHERE effective_to IS NULL;
CREATE INDEX medication_price_provisional_idx
  ON core.medication_price (facility_id) WHERE effective_to IS NULL AND verification = 'PROVISIONAL';

-- INSERT and UPDATE, no DELETE. The UPDATE is for closing `effective_to` and for nothing else;
-- `core.medication_price_is_immutable` below refuses every other column change, so the grant
-- cannot be turned into a way to rewrite what a price was.
GRANT SELECT, INSERT, UPDATE ON core.medication_price TO dthcms_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.medication_price_is_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id <> OLD.id
     OR NEW.product_id <> OLD.product_id
     OR NEW.facility_id <> OLD.facility_id
     OR NEW.unit_price_poisha <> OLD.unit_price_poisha
     OR NEW.effective_from <> OLD.effective_from
     OR NEW.verification IS DISTINCT FROM OLD.verification
     OR NEW.origin <> OLD.origin
     OR NEW.recorded_by IS DISTINCT FROM OLD.recorded_by
     OR NEW.recorded_at <> OLD.recorded_at THEN
    RAISE EXCEPTION
      'a price is never edited: supersede it with a new row instead (product %)', OLD.product_id;
  END IF;
  -- A closed period may not be reopened or moved either. Otherwise "what did this cost in March"
  -- becomes a question whose answer depends on when you ask it.
  IF OLD.effective_to IS NOT NULL AND NEW.effective_to IS DISTINCT FROM OLD.effective_to THEN
    RAISE EXCEPTION 'a price period that has been closed is not reopened (price %)', OLD.id;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER medication_price_immutable
  BEFORE UPDATE ON core.medication_price
  FOR EACH ROW EXECUTE FUNCTION core.medication_price_is_immutable();

-- ---------------------------------------------------------------------------
-- 5. The monthly price review
-- ---------------------------------------------------------------------------

-- §16.1 asks explicitly *who owns monthly price review*. The default is the PHARMACIST role, and
-- the answer is a row so that it can be changed without a deploy.
--
-- **Both shapes are modelled**, and that is deliberate rather than indecisive: `owner_role` is
-- always set, `owner_user_id` is optional and narrows it to one person. A role-only owner
-- survives the pharmacist leaving; a named owner is the thing that actually makes somebody feel
-- responsible. The recommendation in the checkpoint report is role as the floor and a named
-- deputy on top, which is exactly this shape.
CREATE TABLE core.formulary_review_owner (
  facility_id uuid PRIMARY KEY REFERENCES core.facility(id),

  owner_role text NOT NULL REFERENCES core.role(code),
  owner_user_id uuid REFERENCES core.app_user(id),

  -- Which day of the month the review is due. The 1st by default; a clinic that does its
  -- stocktake mid-month can move it.
  due_day_of_month integer NOT NULL DEFAULT 1 CHECK (due_day_of_month BETWEEN 1 AND 28),

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id)
);

SELECT core.attach_updated_at('core.formulary_review_owner');
GRANT SELECT, INSERT, UPDATE ON core.formulary_review_owner TO dthcms_app;

INSERT INTO core.formulary_review_owner (facility_id, owner_role)
SELECT f.id, 'PHARMACIST' FROM core.facility f
ON CONFLICT (facility_id) DO NOTHING;

-- One row per month per facility. The owner is **copied** onto the cycle when it opens rather
-- than joined at read time: who was asked to do March's review is a fact about March, and it
-- does not change because the pharmacist left in June.
CREATE TABLE core.formulary_price_review (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  -- The first day of the month under review.
  period_month date NOT NULL,

  owner_role text NOT NULL REFERENCES core.role(code),
  owner_user_id uuid REFERENCES core.app_user(id),

  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'COMPLETE')),

  opened_at timestamptz NOT NULL DEFAULT now(),
  due_on date NOT NULL,

  -- When the reminder actually went out, and the alert it went out as. Null means the cycle
  -- exists but nobody has been told — which is a state the screen must be able to show, because
  -- an unreminded review is the failure criterion 4 is about.
  reminded_at timestamptz,
  alert_id uuid REFERENCES core.admin_alert(id),

  -- What was true when the cycle opened, so a completed review can be read back without
  -- recomputing it against a formulary that has since changed.
  products_at_open integer NOT NULL DEFAULT 0,
  provisional_at_open integer NOT NULL DEFAULT 0,

  completed_at timestamptz,
  completed_by uuid REFERENCES core.app_user(id),
  completion_note text,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT price_review_period_is_a_month CHECK (period_month = date_trunc('month', period_month)),
  CONSTRAINT price_review_completion_is_attributed
    CHECK ((completed_at IS NULL) = (completed_by IS NULL)),
  CONSTRAINT price_review_complete_means_completed
    CHECK ((status = 'COMPLETE') = (completed_at IS NOT NULL))
);

CREATE UNIQUE INDEX formulary_price_review_period_key
  ON core.formulary_price_review (facility_id, period_month);

SELECT core.attach_updated_at('core.formulary_price_review');
GRANT SELECT, INSERT, UPDATE ON core.formulary_price_review TO dthcms_app;

-- ---------------------------------------------------------------------------
-- 6. Bulk import
-- ---------------------------------------------------------------------------

-- Criterion 2 is *"bulk import validates and reports errors per row"*, and per-row errors that
-- exist only in one HTTP response are per-row errors nobody can act on the next morning. So the
-- outcome of every row of every import is a row here, bilingual, naming the line number in the
-- file the person uploaded.
CREATE TABLE core.formulary_import (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  filename text NOT NULL,
  -- Whether the person asked for a dry run. A dry run writes the import and its rows and changes
  -- no product and no price — which is the only honest way to let somebody see the errors in a
  -- 250-line file before committing to it.
  mode text NOT NULL CHECK (mode IN ('DRY_RUN', 'APPLY')),

  rows_total integer NOT NULL DEFAULT 0,
  rows_accepted integer NOT NULL DEFAULT 0,
  rows_rejected integer NOT NULL DEFAULT 0,
  products_created integer NOT NULL DEFAULT 0,
  products_updated integer NOT NULL DEFAULT 0,
  prices_recorded integer NOT NULL DEFAULT 0,

  imported_at timestamptz NOT NULL DEFAULT now(),
  imported_by uuid NOT NULL REFERENCES core.app_user(id),

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id),

  CONSTRAINT formulary_import_counts_add_up
    CHECK (rows_accepted + rows_rejected = rows_total)
);

SELECT core.attach_updated_at('core.formulary_import');
GRANT SELECT, INSERT, UPDATE ON core.formulary_import TO dthcms_app;

CREATE TABLE core.formulary_import_row (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  import_id uuid NOT NULL REFERENCES core.formulary_import(id),
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  -- The line number **in the uploaded file**, header included. A person looking at their
  -- spreadsheet needs the number their spreadsheet shows, not the index of the row among the
  -- ones that parsed.
  line_number integer NOT NULL CHECK (line_number >= 1),

  outcome text NOT NULL CHECK (outcome IN ('CREATED', 'UPDATED', 'UNCHANGED', 'REJECTED')),

  -- Which column the problem is in, where there is one column to blame.
  field text,
  message_en text,
  message_bn text,

  -- What the line said, kept so the error can be read without the original file. A formulary row
  -- holds no patient data, which is why keeping it is safe.
  raw_line text,

  product_id uuid REFERENCES core.medication_product(id),

  created_at timestamptz NOT NULL DEFAULT now(),

  -- A rejection with no message is the defect this table exists to prevent, and it must be a
  -- message in **both** languages: half the clinic's staff work in Bangla, and the moment a
  -- person needs their own language is the moment something they did was refused.
  CONSTRAINT import_row_rejection_says_why
    CHECK (outcome <> 'REJECTED'
           OR (btrim(coalesce(message_en, '')) <> '' AND btrim(coalesce(message_bn, '')) <> ''))
);

CREATE INDEX formulary_import_row_import_idx
  ON core.formulary_import_row (import_id, line_number);

GRANT SELECT, INSERT ON core.formulary_import_row TO dthcms_app;

-- ---------------------------------------------------------------------------
-- 7. The seed, staged
-- ---------------------------------------------------------------------------

-- The 250 curated rows land in a staging table first, and a function projects them into the live
-- tables. Two reasons, and the second is the one that matters:
--
--  1. The seed is then **data**, reviewable as a table, and Dr. Nahid correcting a price is an
--     UPDATE on one row rather than an edit to a migration nobody may edit.
--  2. The projection is **re-runnable**. `core.apply_formulary_seed()` can be called again at any
--     time — after a restore, after a correction, from a test — and it will not clobber a price a
--     human has approved, because it only inserts a price for a product that has none.
CREATE TABLE core.formulary_seed_row (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  generic_name text NOT NULL,
  class_code text NOT NULL REFERENCES core.medication_class(code),
  trade_name text NOT NULL,
  strength text NOT NULL,
  form_code text NOT NULL REFERENCES core.medication_form(code),
  manufacturer text NOT NULL,
  unit_price_poisha bigint NOT NULL CHECK (unit_price_poisha > 0),
  dispense_unit text NOT NULL REFERENCES core.dispense_unit(code),
  dgda_registration text,
  is_active boolean NOT NULL DEFAULT true,
  notes text,
  source_url text,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.app_user(id),
  updated_by uuid REFERENCES core.app_user(id)
);

CREATE UNIQUE INDEX formulary_seed_row_identity_key ON core.formulary_seed_row
  (trade_name, strength, form_code, manufacturer, dispense_unit);

SELECT core.attach_updated_at('core.formulary_seed_row');
GRANT SELECT ON core.formulary_seed_row TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'medication_class',
   'A therapeutic class is pharmacological knowledge, not a clinic''s data.'),
  ('core', 'medication_form',
   'A dosage form is pharmacological knowledge, not a clinic''s data.'),
  ('core', 'dispense_unit',
   'What a medicine is dispensed in is not a clinic''s data.'),
  ('core', 'generic',
   'Which molecules exist is pharmacological knowledge; which brands this clinic stocks is on core.medication_product, which is scoped.'),
  ('core', 'formulary_seed_row',
   'The published MRP catalogue the seed is built from is a source document, not a clinic''s data. What each clinic makes of it is on core.medication_product.')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- 8. The 250 curated rows (generated from /home/claude/formulary/rows.json)
-- ---------------------------------------------------------------------------
--
-- Published MRP from medex.com.bd, read on 8 September 2026, with sources recorded in
-- `formulary/sources.md`. **Not what this clinic charges.** Every price this produces is
-- PROVISIONAL and carries no person's name, and the constraints above make it impossible for
-- one to become VERIFIED without a human writing a new row.

INSERT INTO core.medication_class (code, name_en, name_bn, ordering) VALUES
  ('BIGUANIDE', 'Biguanide', 'বাইগুয়ানাইড', 10),
  ('SULPHONYLUREA', 'Sulphonylurea', 'সালফোনাইলইউরিয়া', 20),
  ('DPP_4_INHIBITOR', 'DPP-4 inhibitor', 'ডিপিপি-৪ ইনহিবিটর', 30),
  ('SGLT2_INHIBITOR', 'SGLT2 inhibitor', 'এসজিএলটি-২ ইনহিবিটর', 40),
  ('GLP_1_RECEPTOR_AGONIST', 'GLP-1 receptor agonist', 'জিএলপি-১ রিসেপ্টর অ্যাগোনিস্ট', 50),
  ('THIAZOLIDINEDIONE', 'Thiazolidinedione', 'থায়াজোলিডিনডায়োন', 60),
  ('ALPHA_GLUCOSIDASE_INHIBITOR', 'Alpha-glucosidase inhibitor', 'আলফা-গ্লুকোসাইডেজ ইনহিবিটর', 70),
  ('DPP_4_INHIBITOR_BIGUANIDE_FDC', 'DPP-4 inhibitor + biguanide (FDC)', 'ডিপিপি-৪ ইনহিবিটর + বাইগুয়ানাইড (কম্বিনেশন)', 80),
  ('SGLT2_INHIBITOR_BIGUANIDE_FDC', 'SGLT2 inhibitor + biguanide (FDC)', 'এসজিএলটি-২ ইনহিবিটর + বাইগুয়ানাইড (কম্বিনেশন)', 90),
  ('SULPHONYLUREA_BIGUANIDE_FDC', 'Sulphonylurea + biguanide (FDC)', 'সালফোনাইলইউরিয়া + বাইগুয়ানাইড (কম্বিনেশন)', 100),
  ('THIAZOLIDINEDIONE_BIGUANIDE_FDC', 'Thiazolidinedione + biguanide (FDC)', 'থায়াজোলিডিনডায়োন + বাইগুয়ানাইড (কম্বিনেশন)', 110),
  ('THIAZOLIDINEDIONE_SULPHONYLUREA_FDC', 'Thiazolidinedione + sulphonylurea (FDC)', 'থায়াজোলিডিনডায়োন + সালফোনাইলইউরিয়া (কম্বিনেশন)', 120),
  ('INSULIN_SHORT_ACTING_HUMAN', 'Insulin - short acting (human)', 'ইনসুলিন — স্বল্পমেয়াদি (হিউম্যান)', 130),
  ('INSULIN_INTERMEDIATE_ACTING_HUMAN', 'Insulin - intermediate acting (human)', 'ইনসুলিন — মধ্যমেয়াদি (হিউম্যান)', 140),
  ('INSULIN_PREMIXED_HUMAN', 'Insulin - premixed (human)', 'ইনসুলিন — প্রিমিক্সড (হিউম্যান)', 150),
  ('INSULIN_RAPID_ACTING_ANALOGUE', 'Insulin - rapid acting analogue', 'ইনসুলিন — দ্রুতক্রিয় অ্যানালগ', 160),
  ('INSULIN_LONG_ACTING_ANALOGUE', 'Insulin - long acting analogue', 'ইনসুলিন — দীর্ঘমেয়াদি অ্যানালগ', 170),
  ('INSULIN_ULTRA_LONG_ACTING_ANALOGUE', 'Insulin - ultra-long acting analogue', 'ইনসুলিন — অতি-দীর্ঘমেয়াদি অ্যানালগ', 180),
  ('INSULIN_PREMIXED_ANALOGUE', 'Insulin - premixed analogue', 'ইনসুলিন — প্রিমিক্সড অ্যানালগ', 190),
  ('THYROID_HORMONE', 'Thyroid hormone', 'থাইরয়েড হরমোন', 200),
  ('ANTITHYROID', 'Antithyroid', 'অ্যান্টিথাইরয়েড', 210),
  ('STATIN', 'Statin', 'স্ট্যাটিন', 220),
  ('FIBRATE', 'Fibrate', 'ফাইব্রেট', 230),
  ('CHOLESTEROL_ABSORPTION_INHIBITOR', 'Cholesterol absorption inhibitor', 'কোলেস্টেরল শোষণ প্রতিরোধক', 240),
  ('ACE_INHIBITOR', 'ACE inhibitor', 'এসিই ইনহিবিটর', 250),
  ('ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Angiotensin II receptor blocker', 'অ্যানজিওটেনসিন-২ রিসেপ্টর ব্লকার', 260),
  ('CALCIUM_CHANNEL_BLOCKER', 'Calcium channel blocker', 'ক্যালসিয়াম চ্যানেল ব্লকার', 270),
  ('ARB_CCB_FDC', 'ARB + CCB (FDC)', 'এআরবি + সিসিবি (কম্বিনেশন)', 280),
  ('ARB_THIAZIDE_FDC', 'ARB + thiazide (FDC)', 'এআরবি + থায়াজাইড (কম্বিনেশন)', 290),
  ('ANTIPLATELET', 'Antiplatelet', 'অ্যান্টিপ্লেটলেট', 300),
  ('NEUROPATHIC_PAIN_DIABETIC_NEUROPATHY', 'Neuropathic pain (diabetic neuropathy)', 'স্নায়ুজনিত ব্যথা (ডায়াবেটিক নিউরোপ্যাথি)', 310),
  ('VITAMIN_SUPPLEMENT', 'Vitamin / supplement', 'ভিটামিন / সাপ্লিমেন্ট', 320),
  ('CALCIUM_VITAMIN_D_SUPPLEMENT', 'Calcium / vitamin D supplement', 'ক্যালসিয়াম / ভিটামিন ডি সাপ্লিমেন্ট', 330)
ON CONFLICT (code) DO UPDATE SET
  name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn, ordering = EXCLUDED.ordering;

INSERT INTO core.medication_form (code, name_en, name_bn) VALUES
  ('CAPSULE', 'Capsule', 'ক্যাপসুল'),
  ('CHEWABLE_TABLET', 'Chewable Tablet', 'চিবানো ট্যাবলেট'),
  ('EFFERVESCENT_TABLET', 'Effervescent Tablet', 'ইফারভেসেন্ট ট্যাবলেট'),
  ('INJECTION', 'Injection', 'ইনজেকশন'),
  ('LONG_ACTING_TABLET', 'Long Acting Tablet', 'লং অ্যাক্টিং ট্যাবলেট'),
  ('ORAL_SOLUTION', 'Oral Solution', 'খাওয়ার তরল ওষুধ'),
  ('SC_INJECTION', 'SC injection', 'ত্বকের নিচে ইনজেকশন'),
  ('TABLET', 'Tablet', 'ট্যাবলেট'),
  ('TABLET_CONTROLLED_RELEASE', 'Tablet (Controlled Release)', 'ট্যাবলেট (কন্ট্রোলড রিলিজ)'),
  ('TABLET_ENTERIC_COATED', 'Tablet (Enteric Coated)', 'ট্যাবলেট (এন্টেরিক কোটেড)'),
  ('TABLET_EXTENDED_RELEASE', 'Tablet (Extended Release)', 'ট্যাবলেট (এক্সটেন্ডেড রিলিজ)'),
  ('TABLET_MODIFIED_RELEASE', 'Tablet (Modified Release)', 'ট্যাবলেট (মডিফায়েড রিলিজ)'),
  ('TABLET_SUSTAINED_RELEASE', 'Tablet (Sustained Release)', 'ট্যাবলেট (সাসটেইনড রিলিজ)')
ON CONFLICT (code) DO UPDATE SET name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn;

INSERT INTO core.dispense_unit (code, name_en, name_bn) VALUES
  ('ampoule', 'ampoule', 'অ্যাম্পুল'),
  ('bottle', 'bottle', 'বোতল'),
  ('capsule', 'capsule', 'ক্যাপসুল'),
  ('cartridge', 'cartridge', 'কার্ট্রিজ'),
  ('pack', 'pack', 'প্যাক'),
  ('pen', 'pen', 'পেন'),
  ('syringe', 'syringe', 'সিরিঞ্জ'),
  ('tablet', 'tablet', 'ট্যাবলেট'),
  ('vial', 'vial', 'ভায়াল')
ON CONFLICT (code) DO UPDATE SET name_en = EXCLUDED.name_en, name_bn = EXCLUDED.name_bn;

INSERT INTO core.formulary_seed_row
  (generic_name, class_code, trade_name, strength, form_code, manufacturer,
   unit_price_poisha, dispense_unit, dgda_registration, is_active, notes, source_url) VALUES
  ('Metformin hydrochloride', 'BIGUANIDE', 'Comet', '500 mg', 'TABLET', 'Square Pharmaceuticals PLC', 500, 'tablet', NULL, true, '10 x 10 pack', 'https://medex.com.bd/generics/734/metformin-hydrochloride/brand-names'),
  ('Metformin hydrochloride', 'BIGUANIDE', 'Comet', '850 mg', 'TABLET', 'Square Pharmaceuticals PLC', 602, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/734/metformin-hydrochloride/brand-names'),
  ('Metformin hydrochloride', 'BIGUANIDE', 'Comet XR', '500 mg', 'TABLET_EXTENDED_RELEASE', 'Square Pharmaceuticals PLC', 602, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/734/metformin-hydrochloride/brand-names'),
  ('Metformin hydrochloride', 'BIGUANIDE', 'Comet XR', '1000 mg', 'TABLET_EXTENDED_RELEASE', 'Square Pharmaceuticals PLC', 903, 'tablet', NULL, true, '5 x 6 pack', 'https://medex.com.bd/generics/734/metformin-hydrochloride/brand-names'),
  ('Metformin hydrochloride', 'BIGUANIDE', 'Bigmet', '500 mg', 'TABLET', 'Renata PLC', 400, 'tablet', NULL, true, '10 x 10 pack', 'https://medex.com.bd/generics/734/metformin-hydrochloride/brand-names'),
  ('Metformin hydrochloride', 'BIGUANIDE', 'Bigmet', '850 mg', 'TABLET', 'Renata PLC', 600, 'tablet', NULL, true, '10 x 10 pack', 'https://medex.com.bd/generics/734/metformin-hydrochloride/brand-names'),
  ('Metformin hydrochloride', 'BIGUANIDE', 'Bigmet XR', '500 mg', 'TABLET_EXTENDED_RELEASE', 'Renata PLC', 600, 'tablet', NULL, true, '5 x 6 pack', 'https://medex.com.bd/generics/734/metformin-hydrochloride/brand-names'),
  ('Metformin hydrochloride', 'BIGUANIDE', 'Informet XR', '750 mg', 'TABLET_EXTENDED_RELEASE', 'Beximco Pharmaceuticals Ltd.', 900, 'tablet', NULL, true, '1 x 30 pack', 'https://medex.com.bd/generics/734/metformin-hydrochloride/brand-names'),
  ('Metformin hydrochloride', 'BIGUANIDE', 'Informet LA', '1000 mg', 'LONG_ACTING_TABLET', 'Beximco Pharmaceuticals Ltd.', 800, 'tablet', NULL, true, '6 x 10 pack', 'https://medex.com.bd/generics/734/metformin-hydrochloride/brand-names'),
  ('Metformin hydrochloride', 'BIGUANIDE', 'Daomin XR', '1000 mg', 'TABLET_EXTENDED_RELEASE', 'ACME Laboratories Ltd.', 900, 'tablet', NULL, true, '5 x 6 pack', 'https://medex.com.bd/generics/734/metformin-hydrochloride/brand-names'),
  ('Gliclazide', 'SULPHONYLUREA', 'Comprid', '80 mg', 'TABLET', 'Square Pharmaceuticals PLC', 800, 'tablet', NULL, true, '6 x 10 pack', 'https://medex.com.bd/generics/523/gliclazide/brand-names'),
  ('Gliclazide', 'SULPHONYLUREA', 'Comprid XR', '30 mg', 'TABLET_EXTENDED_RELEASE', 'Square Pharmaceuticals PLC', 700, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/523/gliclazide/brand-names'),
  ('Gliclazide', 'SULPHONYLUREA', 'Comprid XR', '60 mg', 'TABLET_EXTENDED_RELEASE', 'Square Pharmaceuticals PLC', 1200, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/523/gliclazide/brand-names'),
  ('Gliclazide', 'SULPHONYLUREA', 'Consucon', '80 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 800, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/523/gliclazide/brand-names'),
  ('Gliclazide', 'SULPHONYLUREA', 'Consucon MR', '30 mg', 'TABLET_MODIFIED_RELEASE', 'Incepta Pharmaceuticals Ltd.', 690, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/523/gliclazide/brand-names'),
  ('Glimepiride', 'SULPHONYLUREA', 'Secrin', '1 mg', 'TABLET', 'Square Pharmaceuticals PLC', 600, 'tablet', NULL, true, '6 x 10 pack', 'https://medex.com.bd/generics/524/glimepiride/brand-names'),
  ('Glimepiride', 'SULPHONYLUREA', 'Secrin', '2 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1000, 'tablet', NULL, true, '6 x 10 pack', 'https://medex.com.bd/generics/524/glimepiride/brand-names'),
  ('Glimepiride', 'SULPHONYLUREA', 'Secrin', '3 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1200, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/524/glimepiride/brand-names'),
  ('Glimepiride', 'SULPHONYLUREA', 'Secrin', '4 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1500, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/524/glimepiride/brand-names'),
  ('Glimepiride', 'SULPHONYLUREA', 'Losucon', '1 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 600, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/524/glimepiride/brand-names'),
  ('Glimepiride', 'SULPHONYLUREA', 'Losucon', '2 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 900, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/524/glimepiride/brand-names'),
  ('Glibenclamide', 'SULPHONYLUREA', 'Dibenol', '5 mg', 'TABLET', 'Square Pharmaceuticals PLC', 35, 'tablet', NULL, true, '20 x 15 pack', 'https://medex.com.bd/generics/522/glibenclamide/brand-names'),
  ('Glibenclamide', 'SULPHONYLUREA', 'Glucon', '5 mg', 'TABLET', 'Opsonin Pharma Ltd.', 34, 'tablet', NULL, true, '10 x 10 pack', 'https://medex.com.bd/generics/522/glibenclamide/brand-names'),
  ('Sitagliptin', 'DPP_4_INHIBITOR', 'Siglita', '50 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1300, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/990/sitagliptin/brand-names'),
  ('Sitagliptin', 'DPP_4_INHIBITOR', 'Sitagil', '25 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 800, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/990/sitagliptin/brand-names'),
  ('Sitagliptin', 'DPP_4_INHIBITOR', 'Sitagil', '50 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1500, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/990/sitagliptin/brand-names'),
  ('Sitagliptin', 'DPP_4_INHIBITOR', 'Sitagil', '100 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 2800, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/990/sitagliptin/brand-names'),
  ('Sitagliptin', 'DPP_4_INHIBITOR', 'Glipita', '100 mg', 'TABLET', 'Beximco Pharmaceuticals Ltd.', 2500, 'tablet', NULL, true, '1 x 15 pack', 'https://medex.com.bd/generics/990/sitagliptin/brand-names'),
  ('Vildagliptin', 'DPP_4_INHIBITOR', 'Viglita', '50 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1600, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/1125/vildagliptin/brand-names'),
  ('Vildagliptin', 'DPP_4_INHIBITOR', 'Vildapin', '50 mg', 'TABLET', 'ACME Laboratories Ltd.', 1504, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1125/vildagliptin/brand-names'),
  ('Linagliptin', 'DPP_4_INHIBITOR', 'Linita', '5 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1800, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/687/linagliptin/brand-names'),
  ('Linagliptin', 'DPP_4_INHIBITOR', 'Linatab', '5 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 2000, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/687/linagliptin/brand-names'),
  ('Saxagliptin', 'DPP_4_INHIBITOR', 'Glyza', '2.5 mg', 'TABLET', 'The IBN SINA Pharmaceutical PLC', 2000, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/978/saxagliptin/brand-names'),
  ('Saxagliptin', 'DPP_4_INHIBITOR', 'Glyza', '5 mg', 'TABLET', 'The IBN SINA Pharmaceutical PLC', 3500, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/978/saxagliptin/brand-names'),
  ('Saxagliptin', 'DPP_4_INHIBITOR', 'Sixtin', '2.5 mg', 'TABLET', 'Drug International Ltd.', 1600, 'tablet', NULL, true, '1 x 14 pack', 'https://medex.com.bd/generics/978/saxagliptin/brand-names'),
  ('Saxagliptin', 'DPP_4_INHIBITOR', 'Sixtin', '5 mg', 'TABLET', 'Drug International Ltd.', 3000, 'tablet', NULL, true, '1 x 14 pack', 'https://medex.com.bd/generics/978/saxagliptin/brand-names'),
  ('Sitagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Siglimet', '50 mg + 500 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1600, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/737/sitagliptin-metformin-hydrochloride/brand-names'),
  ('Sitagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Siglimet', '50 mg + 1000 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1800, 'tablet', NULL, true, '5 x 6 pack', 'https://medex.com.bd/generics/737/sitagliptin-metformin-hydrochloride/brand-names'),
  ('Sitagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Siglimet XR', '50 mg + 1000 mg', 'TABLET_EXTENDED_RELEASE', 'Square Pharmaceuticals PLC', 1800, 'tablet', NULL, true, '3 x 6 pack', 'https://medex.com.bd/generics/737/sitagliptin-metformin-hydrochloride/brand-names'),
  ('Sitagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Siglimet XR', '100 mg + 1000 mg', 'TABLET_EXTENDED_RELEASE', 'Square Pharmaceuticals PLC', 3000, 'tablet', NULL, true, '2 x 6 pack', 'https://medex.com.bd/generics/737/sitagliptin-metformin-hydrochloride/brand-names'),
  ('Sitagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Sitagil M ER', '50 mg + 500 mg', 'TABLET_EXTENDED_RELEASE', 'Incepta Pharmaceuticals Ltd.', 1600, 'tablet', NULL, true, '5 x 4 pack', 'https://medex.com.bd/generics/737/sitagliptin-metformin-hydrochloride/brand-names'),
  ('Sitagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Sitagil M ER', '50 mg + 1000 mg', 'TABLET_EXTENDED_RELEASE', 'Incepta Pharmaceuticals Ltd.', 1800, 'tablet', NULL, true, '3 x 6 pack', 'https://medex.com.bd/generics/737/sitagliptin-metformin-hydrochloride/brand-names'),
  ('Vildagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Viglimet', '50 mg + 500 mg', 'TABLET', 'Square Pharmaceuticals PLC', 2000, 'tablet', NULL, true, '5 x 6 pack', 'https://medex.com.bd/generics/738/vildagliptin-metformin-hydrochloride/brand-names'),
  ('Vildagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Viglimet', '50 mg + 850 mg', 'TABLET', 'Square Pharmaceuticals PLC', 2200, 'tablet', NULL, true, '5 x 6 pack', 'https://medex.com.bd/generics/738/vildagliptin-metformin-hydrochloride/brand-names'),
  ('Vildagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Vildapin Plus', '50 mg + 500 mg', 'TABLET', 'ACME Laboratories Ltd.', 1900, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/738/vildagliptin-metformin-hydrochloride/brand-names'),
  ('Vildagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Vildapin Plus', '50 mg + 850 mg', 'TABLET', 'ACME Laboratories Ltd.', 2100, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/738/vildagliptin-metformin-hydrochloride/brand-names'),
  ('Linagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Liglimet', '2.5 mg + 500 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1300, 'tablet', NULL, true, '5 x 6 pack', 'https://medex.com.bd/generics/1285/linagliptin-metformin-hydrochloride/brand-names'),
  ('Linagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Liglimet', '2.5 mg + 850 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1500, 'tablet', NULL, true, '3 x 6 pack', 'https://medex.com.bd/generics/1285/linagliptin-metformin-hydrochloride/brand-names'),
  ('Linagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Liglimet XR', '5 mg + 1000 mg', 'TABLET_EXTENDED_RELEASE', 'Square Pharmaceuticals PLC', 2000, 'tablet', NULL, true, '5 x 4 pack', 'https://medex.com.bd/generics/1285/linagliptin-metformin-hydrochloride/brand-names'),
  ('Linagliptin + Metformin hydrochloride', 'DPP_4_INHIBITOR_BIGUANIDE_FDC', 'Linatab M', '2.5 mg + 500 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1200, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1285/linagliptin-metformin-hydrochloride/brand-names'),
  ('Empagliflozin', 'SGLT2_INHIBITOR', 'Emjard', '10 mg', 'TABLET', 'Square Pharmaceuticals PLC', 2500, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/1275/empagliflozin/brand-names'),
  ('Empagliflozin', 'SGLT2_INHIBITOR', 'Emjard', '25 mg', 'TABLET', 'Square Pharmaceuticals PLC', 4000, 'tablet', NULL, true, '1 x 10 pack', 'https://medex.com.bd/generics/1275/empagliflozin/brand-names'),
  ('Empagliflozin', 'SGLT2_INHIBITOR', 'Empatab', '10 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 2500, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1275/empagliflozin/brand-names'),
  ('Empagliflozin', 'SGLT2_INHIBITOR', 'Empatab', '25 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 4000, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/1275/empagliflozin/brand-names'),
  ('Dapagliflozin propanediol', 'SGLT2_INHIBITOR', 'Dapaglip', '5 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1600, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/339/dapagliflozin-propanediol/brand-names'),
  ('Dapagliflozin propanediol', 'SGLT2_INHIBITOR', 'Dapaglip', '10 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 3000, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/339/dapagliflozin-propanediol/brand-names'),
  ('Dapagliflozin propanediol', 'SGLT2_INHIBITOR', 'Glycema', '5 mg', 'TABLET', 'ACI Limited', 2000, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/339/dapagliflozin-propanediol/brand-names'),
  ('Dapagliflozin propanediol', 'SGLT2_INHIBITOR', 'Glycema', '10 mg', 'TABLET', 'ACI Limited', 3000, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/339/dapagliflozin-propanediol/brand-names'),
  ('Empagliflozin + Metformin hydrochloride', 'SGLT2_INHIBITOR_BIGUANIDE_FDC', 'Emjard M', '5 mg + 500 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1800, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1580/empagliflozin-metformin-hydrochloride/brand-names'),
  ('Empagliflozin + Metformin hydrochloride', 'SGLT2_INHIBITOR_BIGUANIDE_FDC', 'Emjard M', '12.5 mg + 500 mg', 'TABLET', 'Square Pharmaceuticals PLC', 2000, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1580/empagliflozin-metformin-hydrochloride/brand-names'),
  ('Empagliflozin + Metformin hydrochloride', 'SGLT2_INHIBITOR_BIGUANIDE_FDC', 'Emjard M XR', '10 mg + 1000 mg', 'TABLET_EXTENDED_RELEASE', 'Square Pharmaceuticals PLC', 3000, 'tablet', NULL, true, '5 x 4 pack', 'https://medex.com.bd/generics/1580/empagliflozin-metformin-hydrochloride/brand-names'),
  ('Empagliflozin + Metformin hydrochloride', 'SGLT2_INHIBITOR_BIGUANIDE_FDC', 'Emjard M XR', '25 mg + 1000 mg', 'TABLET_EXTENDED_RELEASE', 'Square Pharmaceuticals PLC', 5000, 'tablet', NULL, true, '5 x 4 pack', 'https://medex.com.bd/generics/1580/empagliflozin-metformin-hydrochloride/brand-names'),
  ('Liraglutide', 'GLP_1_RECEPTOR_AGONIST', 'Victoza', '6 mg/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd', 496000, 'pen', NULL, true, '3 mL pre-filled pen; cold chain, store 2-8 C', 'https://medex.com.bd/generics/690/liraglutide/brand-names'),
  ('Semaglutide', 'GLP_1_RECEPTOR_AGONIST', 'Ozempic', '1.34 mg/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd', 1425900, 'pen', NULL, true, 'originator brand; listed as 1.5 mL or 3 mL cartridge with device - medex does not separate the two presentations at this price; cold chain', 'https://medex.com.bd/generics/1969/semaglutide/brand-names'),
  ('Semaglutide', 'GLP_1_RECEPTOR_AGONIST', 'Semazic', '0.25 mg/0.188 mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 35000, 'syringe', NULL, true, '1 pre-filled syringe; cold chain', 'https://medex.com.bd/generics/1969/semaglutide/brand-names'),
  ('Semaglutide', 'GLP_1_RECEPTOR_AGONIST', 'Semazic', '0.5 mg/0.375 mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 60000, 'syringe', NULL, true, '1 pre-filled syringe; cold chain', 'https://medex.com.bd/generics/1969/semaglutide/brand-names'),
  ('Semaglutide', 'GLP_1_RECEPTOR_AGONIST', 'Fitaro', '0.25 mg/0.5 mL', 'SC_INJECTION', 'Incepta Pharmaceuticals Ltd.', 45000, 'syringe', NULL, true, '1 pre-filled syringe; cold chain', 'https://medex.com.bd/generics/1969/semaglutide/brand-names'),
  ('Semaglutide', 'GLP_1_RECEPTOR_AGONIST', 'Fitaro', '0.5 mg/0.5 mL', 'SC_INJECTION', 'Incepta Pharmaceuticals Ltd.', 70000, 'syringe', NULL, true, '1 pre-filled syringe; cold chain', 'https://medex.com.bd/generics/1969/semaglutide/brand-names'),
  ('Semaglutide', 'GLP_1_RECEPTOR_AGONIST', 'Fitaro', '1 mg/0.5 mL', 'SC_INJECTION', 'Incepta Pharmaceuticals Ltd.', 100000, 'syringe', NULL, true, '1 pre-filled syringe; cold chain', 'https://medex.com.bd/generics/1969/semaglutide/brand-names'),
  ('Semaglutide', 'GLP_1_RECEPTOR_AGONIST', 'Fitaro', '2.4 mg/0.75 mL', 'SC_INJECTION', 'Incepta Pharmaceuticals Ltd.', 150000, 'syringe', NULL, true, '1 pre-filled syringe; cold chain', 'https://medex.com.bd/generics/1969/semaglutide/brand-names'),
  ('Semaglutide', 'GLP_1_RECEPTOR_AGONIST', 'Orsema', '0.5 mg/0.375 mL', 'SC_INJECTION', 'Incepta Pharmaceuticals Ltd.', 60000, 'syringe', NULL, true, '1 pre-filled syringe; cold chain', 'https://medex.com.bd/generics/1969/semaglutide/brand-names'),
  ('Dulaglutide', 'GLP_1_RECEPTOR_AGONIST', 'Trulicity', '0.75 mg/0.5 mL', 'SC_INJECTION', 'Healthcare Pharmaceuticals Ltd. (Mfg. by Eli Lilly and Company)', 400000, 'syringe', NULL, true, '4''s pack BDT 16,000.00; cold chain, store 2-8 C', 'https://medex.com.bd/generics/1599/dulaglutide/brand-names'),
  ('Dulaglutide', 'GLP_1_RECEPTOR_AGONIST', 'Trulicity', '1.5 mg/0.5 mL', 'SC_INJECTION', 'Healthcare Pharmaceuticals Ltd. (Mfg. by Eli Lilly and Company)', 400000, 'syringe', NULL, true, '4''s pack BDT 16,000.00; cold chain, store 2-8 C', 'https://medex.com.bd/generics/1599/dulaglutide/brand-names'),
  ('Insulin human (rDNA) - soluble/regular', 'INSULIN_SHORT_ACTING_HUMAN', 'Ansulin R', '100 IU/mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 41500, 'vial', NULL, true, '10 mL vial; cold chain', 'https://medex.com.bd/generics/1225/insulin-human-rdna/brand-names'),
  ('Insulin human (rDNA) - soluble/regular', 'INSULIN_SHORT_ACTING_HUMAN', 'Ansulin R', '100 IU/mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 22000, 'cartridge', NULL, true, '3 mL pen cartridge; cold chain', 'https://medex.com.bd/generics/1225/insulin-human-rdna/brand-names'),
  ('Insulin human (rDNA) - soluble/regular', 'INSULIN_SHORT_ACTING_HUMAN', 'Ansulin R', '40 IU/mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 19500, 'vial', NULL, true, '10 mL vial; cold chain', 'https://medex.com.bd/generics/1225/insulin-human-rdna/brand-names'),
  ('Insulin human (rDNA) - soluble/regular', 'INSULIN_SHORT_ACTING_HUMAN', 'Actrapid', '100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd (mktd. Eskayef)', 41500, 'vial', NULL, true, '10 mL vial; cold chain', 'https://medex.com.bd/generics/1225/insulin-human-rdna/brand-names'),
  ('Insulin human (rDNA) - soluble/regular', 'INSULIN_SHORT_ACTING_HUMAN', 'Actrapid FlexPen', '100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd (mktd. Eskayef)', 275000, 'pack', NULL, true, 'price shown is for 5''s pack of 3 mL FlexPen; cold chain', 'https://medex.com.bd/generics/1225/insulin-human-rdna/brand-names'),
  ('Insulin human (rDNA) - soluble/regular', 'INSULIN_SHORT_ACTING_HUMAN', 'Actrapid PenFill', '100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd (mktd. Eskayef)', 255000, 'pack', NULL, true, 'price shown is for 5''s pack of 3 mL PenFill; cold chain', 'https://medex.com.bd/generics/1225/insulin-human-rdna/brand-names'),
  ('Insulin human (rDNA) - soluble/regular', 'INSULIN_SHORT_ACTING_HUMAN', 'Humulin R', '100 IU/mL', 'SC_INJECTION', 'International Agencies (Bd.) Ltd. (Mfg. by Eli Lilly)', 100400, 'vial', NULL, true, '10 mL vial; cold chain', 'https://medex.com.bd/generics/1225/insulin-human-rdna/brand-names'),
  ('Isophane insulin human (NPH)', 'INSULIN_INTERMEDIATE_ACTING_HUMAN', 'Ansulin N', '100 IU/mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 41500, 'vial', NULL, true, '10 mL vial; cold chain', 'https://medex.com.bd/generics/1225/insulin-human-rdna/brand-names'),
  ('Isophane insulin human (NPH)', 'INSULIN_INTERMEDIATE_ACTING_HUMAN', 'Insulatard HM', '100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd (mktd. Eskayef)', 41500, 'vial', NULL, true, '10 mL vial; cold chain', 'https://medex.com.bd/generics/1225/insulin-human-rdna/brand-names'),
  ('Isophane insulin human (NPH)', 'INSULIN_INTERMEDIATE_ACTING_HUMAN', 'Insulatard FlexPen', '100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd (mktd. Eskayef)', 210500, 'pack', NULL, true, 'price shown is for 5''s pack of 3 mL FlexPen; cold chain', 'https://medex.com.bd/generics/1225/insulin-human-rdna/brand-names'),
  ('Isophane insulin human (NPH)', 'INSULIN_INTERMEDIATE_ACTING_HUMAN', 'Insulatard PenFill', '100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd (mktd. Eskayef)', 230000, 'pack', NULL, true, 'price shown is for 5''s pack of 3 mL PenFill; cold chain', 'https://medex.com.bd/generics/1225/insulin-human-rdna/brand-names'),
  ('Isophane insulin human (NPH)', 'INSULIN_INTERMEDIATE_ACTING_HUMAN', 'Humulin N', '100 IU/mL', 'SC_INJECTION', 'International Agencies (Bd.) Ltd. (Mfg. by Eli Lilly)', 99600, 'vial', NULL, true, '10 mL vial; cold chain', 'https://medex.com.bd/generics/1225/insulin-human-rdna/brand-names'),
  ('Regular insulin human + Isophane insulin human (premix)', 'INSULIN_PREMIXED_HUMAN', 'Mixtard 30', '30% + 70% in 100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd', 41500, 'vial', NULL, true, '10 mL vial; cold chain', 'https://medex.com.bd/generics/2364/regular-insulin-human-isophane-insulin-human/brand-names'),
  ('Regular insulin human + Isophane insulin human (premix)', 'INSULIN_PREMIXED_HUMAN', 'Mixtard 30', '30% + 70% in 100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd', 61500, 'pen', NULL, true, '3 mL FlexPen; cold chain', 'https://medex.com.bd/generics/2364/regular-insulin-human-isophane-insulin-human/brand-names'),
  ('Regular insulin human + Isophane insulin human (premix)', 'INSULIN_PREMIXED_HUMAN', 'Mixtard 30', '30% + 70% in 100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd', 51000, 'cartridge', NULL, true, '3 mL PenFill; cold chain', 'https://medex.com.bd/generics/2364/regular-insulin-human-isophane-insulin-human/brand-names'),
  ('Regular insulin human + Isophane insulin human (premix)', 'INSULIN_PREMIXED_HUMAN', 'Mixtard 50', '50% + 50% in 100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd', 51000, 'cartridge', NULL, true, '3 mL PenFill; cold chain', 'https://medex.com.bd/generics/2364/regular-insulin-human-isophane-insulin-human/brand-names'),
  ('Regular insulin human + Isophane insulin human (premix)', 'INSULIN_PREMIXED_HUMAN', 'Ansulin', '30% + 70% in 100 IU/mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 41500, 'vial', NULL, true, '10 mL vial; cold chain', 'https://medex.com.bd/generics/2364/regular-insulin-human-isophane-insulin-human/brand-names'),
  ('Regular insulin human + Isophane insulin human (premix)', 'INSULIN_PREMIXED_HUMAN', 'Ansulin', '30% + 70% in 100 IU/mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 22200, 'cartridge', NULL, true, '3 mL pen cartridge; cold chain', 'https://medex.com.bd/generics/2364/regular-insulin-human-isophane-insulin-human/brand-names'),
  ('Regular insulin human + Isophane insulin human (premix)', 'INSULIN_PREMIXED_HUMAN', 'Ansulin', '30% + 70% in 40 IU/mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 19500, 'vial', NULL, true, '10 mL vial; cold chain', 'https://medex.com.bd/generics/2364/regular-insulin-human-isophane-insulin-human/brand-names'),
  ('Regular insulin human + Isophane insulin human (premix)', 'INSULIN_PREMIXED_HUMAN', 'Ansulin', '50% + 50% in 100 IU/mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 41500, 'vial', NULL, true, '10 mL vial; cold chain', 'https://medex.com.bd/generics/2364/regular-insulin-human-isophane-insulin-human/brand-names'),
  ('Regular insulin human + Isophane insulin human (premix)', 'INSULIN_PREMIXED_HUMAN', 'Gensulin M30', '30% + 70% in 100 IU/mL', 'SC_INJECTION', 'Beximco Pharmaceuticals Ltd.', 41500, 'vial', NULL, true, '10 mL vial; cold chain', 'https://medex.com.bd/generics/2364/regular-insulin-human-isophane-insulin-human/brand-names'),
  ('Regular insulin human + Isophane insulin human (premix)', 'INSULIN_PREMIXED_HUMAN', 'Gensulin M30', '30% + 70% in 100 IU/mL', 'SC_INJECTION', 'Beximco Pharmaceuticals Ltd.', 22200, 'cartridge', NULL, true, '3 mL cartridge; cold chain', 'https://medex.com.bd/generics/2364/regular-insulin-human-isophane-insulin-human/brand-names'),
  ('Insulin glargine', 'INSULIN_LONG_ACTING_ANALOGUE', 'Larsulin', '100 IU/mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 60000, 'vial', NULL, true, '3 mL vial; cold chain', 'https://medex.com.bd/generics/614/insulin-glargine/brand-names'),
  ('Insulin glargine', 'INSULIN_LONG_ACTING_ANALOGUE', 'Larsulin', '100 IU/mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 60000, 'cartridge', NULL, true, '3 mL pen cartridge; cold chain', 'https://medex.com.bd/generics/614/insulin-glargine/brand-names'),
  ('Insulin glargine', 'INSULIN_LONG_ACTING_ANALOGUE', 'Vibrenta', '100 IU/mL', 'SC_INJECTION', 'Incepta Pharmaceuticals Ltd.', 60000, 'vial', NULL, true, '3 mL vial; cold chain', 'https://medex.com.bd/generics/614/insulin-glargine/brand-names'),
  ('Insulin glargine', 'INSULIN_LONG_ACTING_ANALOGUE', 'Vibrenta', '100 IU/mL', 'SC_INJECTION', 'Incepta Pharmaceuticals Ltd.', 60000, 'pen', NULL, true, '3 mL PenSet; cold chain', 'https://medex.com.bd/generics/614/insulin-glargine/brand-names'),
  ('Insulin glargine', 'INSULIN_LONG_ACTING_ANALOGUE', 'Semglee', '100 IU/mL', 'SC_INJECTION', 'Beximco Pharmaceuticals Ltd.', 101600, 'cartridge', NULL, true, '3 mL cartridge; cold chain', 'https://medex.com.bd/generics/614/insulin-glargine/brand-names'),
  ('Insulin glargine', 'INSULIN_LONG_ACTING_ANALOGUE', 'Glarine', '100 IU/mL', 'SC_INJECTION', 'ACI Limited', 60000, 'vial', NULL, true, '3 mL vial; cold chain', 'https://medex.com.bd/generics/614/insulin-glargine/brand-names'),
  ('Insulin glargine', 'INSULIN_LONG_ACTING_ANALOGUE', 'Glarine', '100 IU/mL', 'SC_INJECTION', 'ACI Limited', 80000, 'cartridge', NULL, true, '3 mL cartridge; cold chain', 'https://medex.com.bd/generics/614/insulin-glargine/brand-names'),
  ('Insulin glargine', 'INSULIN_LONG_ACTING_ANALOGUE', 'Glarine', '100 IU/mL', 'SC_INJECTION', 'ACI Limited', 95000, 'pen', NULL, true, '3 mL biopen; cold chain', 'https://medex.com.bd/generics/614/insulin-glargine/brand-names'),
  ('Insulin glargine', 'INSULIN_LONG_ACTING_ANALOGUE', 'Mytus', '100 IU/mL', 'SC_INJECTION', 'Drug International Ltd.', 60000, 'pen', NULL, true, '3 mL PenSet; cold chain', 'https://medex.com.bd/generics/614/insulin-glargine/brand-names'),
  ('Insulin glargine', 'INSULIN_LONG_ACTING_ANALOGUE', 'Toujeo SoloStar', '300 IU/mL', 'SC_INJECTION', 'Synovia Pharma PLC.', 230500, 'pen', NULL, true, '1.5 mL cartridge (concentrated U300); cold chain', 'https://medex.com.bd/generics/614/insulin-glargine/brand-names'),
  ('Insulin detemir', 'INSULIN_LONG_ACTING_ANALOGUE', 'Levemir FlexPen', '100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd', 160000, 'pen', NULL, true, '3 mL FlexPen; 5''s pack BDT 8,000.00; cold chain', 'https://medex.com.bd/generics/613/insulin-detemir/brand-names'),
  ('Insulin degludec', 'INSULIN_ULTRA_LONG_ACTING_ANALOGUE', 'Tresiba FlexTouch', '100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd', 275000, 'pen', NULL, true, '3 mL FlexTouch; 5''s pack BDT 13,750.00; cold chain', 'https://medex.com.bd/generics/612/insulin-degludec/brand-names'),
  ('Insulin aspart', 'INSULIN_RAPID_ACTING_ANALOGUE', 'Fiasp FlexTouch', '100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd', 95000, 'pen', NULL, true, '3 mL pen; 5''s pack BDT 4,750.00; cold chain', 'https://medex.com.bd/generics/610/insulin-aspart/brand-names'),
  ('Insulin aspart', 'INSULIN_RAPID_ACTING_ANALOGUE', 'Rapilog', '100 IU/mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 46000, 'vial', NULL, true, '3 mL vial; cold chain', 'https://medex.com.bd/generics/610/insulin-aspart/brand-names'),
  ('Insulin aspart', 'INSULIN_RAPID_ACTING_ANALOGUE', 'Rapilog', '100 IU/mL', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 56000, 'pen', NULL, true, '3 mL pen; cold chain', 'https://medex.com.bd/generics/610/insulin-aspart/brand-names'),
  ('Insulin aspart', 'INSULIN_RAPID_ACTING_ANALOGUE', 'Glyset Mix', '100 IU/mL', 'SC_INJECTION', 'Incepta Pharmaceuticals Ltd.', 45000, 'vial', NULL, true, '3 mL vial; cold chain. Listed by medex under the plain insulin aspart generic despite the ''Mix'' brand name - verify formulation before dispensing', 'https://medex.com.bd/generics/610/insulin-aspart/brand-names'),
  ('Insulin aspart', 'INSULIN_RAPID_ACTING_ANALOGUE', 'Glyset Mix', '100 IU/mL', 'SC_INJECTION', 'Incepta Pharmaceuticals Ltd.', 55000, 'pen', NULL, true, '3 mL PenSet; cold chain. Listed by medex under the plain insulin aspart generic despite the ''Mix'' brand name - verify formulation before dispensing', 'https://medex.com.bd/generics/610/insulin-aspart/brand-names'),
  ('Insulin lispro', 'INSULIN_RAPID_ACTING_ANALOGUE', 'Humalog 100', '100 unit/mL', 'SC_INJECTION', 'Healthcare Pharmaceuticals Ltd. (Mfg. by Eli Lilly)', 98000, 'cartridge', NULL, true, '3 mL cartridge; 5''s pack BDT 4,900.00; cold chain', 'https://medex.com.bd/generics/616/insulin-lispro/brand-names'),
  ('Insulin lispro', 'INSULIN_RAPID_ACTING_ANALOGUE', 'Humalog 100', '100 unit/mL', 'SC_INJECTION', 'Healthcare Pharmaceuticals Ltd. (Mfg. by Eli Lilly)', 115000, 'pen', NULL, true, '3 mL KwikPen; 5''s pack BDT 5,750.00; cold chain', 'https://medex.com.bd/generics/616/insulin-lispro/brand-names'),
  ('Insulin lispro', 'INSULIN_RAPID_ACTING_ANALOGUE', 'Humalog 200', '200 unit/mL', 'SC_INJECTION', 'Healthcare Pharmaceuticals Ltd. (Mfg. by Eli Lilly)', 160000, 'pen', NULL, true, '3 mL KwikPen; 5''s pack BDT 8,000.00; cold chain', 'https://medex.com.bd/generics/616/insulin-lispro/brand-names'),
  ('Insulin glulisine', 'INSULIN_RAPID_ACTING_ANALOGUE', 'Apidra', '100 unit/mL', 'SC_INJECTION', 'Synovia Pharma PLC.', 94800, 'cartridge', NULL, true, '3 mL cartridge; 1 x 5 pack BDT 4,740.00; cold chain', 'https://medex.com.bd/generics/615/insulin-glulisine/brand-names'),
  ('Insulin glulisine', 'INSULIN_RAPID_ACTING_ANALOGUE', 'Apidra SoloStar', '100 unit/mL', 'SC_INJECTION', 'Synovia Pharma PLC.', 140000, 'pen', NULL, true, '3 mL pen; 1 x 5 pack BDT 7,000.00; cold chain', 'https://medex.com.bd/generics/615/insulin-glulisine/brand-names'),
  ('Insulin lispro protamine + Insulin lispro', 'INSULIN_PREMIXED_ANALOGUE', 'Humalog Mix', '75% + 25%', 'SC_INJECTION', 'Healthcare Pharmaceuticals Ltd. (Mfg. by Eli Lilly)', 98000, 'cartridge', NULL, true, '3 mL cartridge; cold chain', 'https://medex.com.bd/generics/617/insulin-lispro-protamine-insulin-lispro/brand-names'),
  ('Insulin lispro protamine + Insulin lispro', 'INSULIN_PREMIXED_ANALOGUE', 'Humalog Mix', '75% + 25%', 'SC_INJECTION', 'Healthcare Pharmaceuticals Ltd. (Mfg. by Eli Lilly)', 108000, 'pen', NULL, true, '3 mL KwikPen; cold chain', 'https://medex.com.bd/generics/617/insulin-lispro-protamine-insulin-lispro/brand-names'),
  ('Insulin lispro protamine + Insulin lispro', 'INSULIN_PREMIXED_ANALOGUE', 'Humalog Mix', '50% + 50%', 'SC_INJECTION', 'Healthcare Pharmaceuticals Ltd. (Mfg. by Eli Lilly)', 98000, 'cartridge', NULL, true, '3 mL cartridge; cold chain', 'https://medex.com.bd/generics/617/insulin-lispro-protamine-insulin-lispro/brand-names'),
  ('Insulin lispro protamine + Insulin lispro', 'INSULIN_PREMIXED_ANALOGUE', 'Humalog Mix', '50% + 50%', 'SC_INJECTION', 'Healthcare Pharmaceuticals Ltd. (Mfg. by Eli Lilly)', 140000, 'pen', NULL, true, '3 mL KwikPen; cold chain', 'https://medex.com.bd/generics/617/insulin-lispro-protamine-insulin-lispro/brand-names'),
  ('Insulin aspart + Insulin aspart protamine', 'INSULIN_PREMIXED_ANALOGUE', 'RapiMix', '30% + 70%', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 45000, 'vial', NULL, true, '3 mL vial; cold chain', 'https://medex.com.bd/generics/2066/insulin-aspart-insulin-aspart-protamine/brand-names'),
  ('Insulin aspart + Insulin aspart protamine', 'INSULIN_PREMIXED_ANALOGUE', 'RapiMix', '30% + 70%', 'SC_INJECTION', 'Square Pharmaceuticals PLC', 55000, 'cartridge', NULL, true, '3 mL Pen Cartridge; cold chain', 'https://medex.com.bd/generics/2066/insulin-aspart-insulin-aspart-protamine/brand-names'),
  ('Insulin aspart + Insulin aspart protamine', 'INSULIN_PREMIXED_ANALOGUE', 'Mypart Mix', '30% + 70%', 'SC_INJECTION', 'Drug International Ltd.', 45000, 'vial', NULL, true, '3 mL vial; cold chain', 'https://medex.com.bd/generics/2066/insulin-aspart-insulin-aspart-protamine/brand-names'),
  ('Insulin aspart + Insulin aspart protamine', 'INSULIN_PREMIXED_ANALOGUE', 'Mypart Mix', '30% + 70%', 'SC_INJECTION', 'Drug International Ltd.', 55000, 'pen', NULL, true, '3 mL PenSet; cold chain', 'https://medex.com.bd/generics/2066/insulin-aspart-insulin-aspart-protamine/brand-names'),
  ('Insulin degludec + Insulin aspart (premixed)', 'INSULIN_PREMIXED_ANALOGUE', 'Ryzodeg', '100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd', 150000, 'cartridge', NULL, true, '3 mL PenFill; 5''s pack BDT 7,500.00; cold chain', 'https://medex.com.bd/generics/618/insulin-degludec-insulin-aspart-premixed/brand-names'),
  ('Insulin degludec + Insulin aspart (premixed)', 'INSULIN_PREMIXED_ANALOGUE', 'Ryzodeg', '100 IU/mL', 'SC_INJECTION', 'Novo Nordisk Pharma (Pvt.) Ltd', 229500, 'pen', NULL, true, '3 mL FlexTouch; 5''s pack BDT 11,475.00; cold chain', 'https://medex.com.bd/generics/618/insulin-degludec-insulin-aspart-premixed/brand-names'),
  ('Pioglitazone', 'THIAZOLIDINEDIONE', 'Piodar', '15 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 800, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/888/pioglitazone/brand-names'),
  ('Pioglitazone', 'THIAZOLIDINEDIONE', 'Pidus', '15 mg', 'TABLET', 'ACME Laboratories Ltd.', 807, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/888/pioglitazone/brand-names'),
  ('Pioglitazone', 'THIAZOLIDINEDIONE', 'Piozena', '30 mg', 'TABLET', 'Drug International Ltd.', 1500, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/888/pioglitazone/brand-names'),
  ('Pioglitazone + Metformin hydrochloride', 'THIAZOLIDINEDIONE_BIGUANIDE_FDC', 'Compimet', '15 mg + 500 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1000, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/735/pioglitazone-metformin-hydrochloride/brand-names'),
  ('Pioglitazone + Metformin hydrochloride', 'THIAZOLIDINEDIONE_BIGUANIDE_FDC', 'Compimet', '15 mg + 850 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1100, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/735/pioglitazone-metformin-hydrochloride/brand-names'),
  ('Pioglitazone + Metformin hydrochloride', 'THIAZOLIDINEDIONE_BIGUANIDE_FDC', 'Piozena Plus', '15 mg + 500 mg', 'TABLET', 'Drug International Ltd.', 1000, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/735/pioglitazone-metformin-hydrochloride/brand-names'),
  ('Pioglitazone + Metformin hydrochloride', 'THIAZOLIDINEDIONE_BIGUANIDE_FDC', 'Piozena Plus', '15 mg + 850 mg', 'TABLET', 'Drug International Ltd.', 1100, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/735/pioglitazone-metformin-hydrochloride/brand-names'),
  ('Pioglitazone + Glimepiride', 'THIAZOLIDINEDIONE_SULPHONYLUREA_FDC', 'Dieta Plus', '30 mg + 2 mg', 'TABLET', 'Pacific Pharmaceuticals Ltd.', 1250, 'tablet', NULL, true, '20''s pack', 'https://medex.com.bd/generics/525/pioglitazone-glimepiride/brand-names'),
  ('Pioglitazone + Glimepiride', 'THIAZOLIDINEDIONE_SULPHONYLUREA_FDC', 'Dieta Plus', '30 mg + 4 mg', 'TABLET', 'Pacific Pharmaceuticals Ltd.', 1500, 'tablet', NULL, true, '20''s pack', 'https://medex.com.bd/generics/525/pioglitazone-glimepiride/brand-names'),
  ('Glimepiride + Metformin hydrochloride', 'SULPHONYLUREA_BIGUANIDE_FDC', 'Secrin M', '1 mg + 500 mg', 'TABLET_EXTENDED_RELEASE', 'Square Pharmaceuticals PLC', 900, 'tablet', NULL, true, '7 x 4 pack', 'https://medex.com.bd/generics/1471/glimepiride-metformin/brand-names'),
  ('Glimepiride + Metformin hydrochloride', 'SULPHONYLUREA_BIGUANIDE_FDC', 'Secrin M', '2 mg + 500 mg', 'TABLET_EXTENDED_RELEASE', 'Square Pharmaceuticals PLC', 1200, 'tablet', NULL, true, '7 x 4 pack', 'https://medex.com.bd/generics/1471/glimepiride-metformin/brand-names'),
  ('Glimepiride + Metformin hydrochloride', 'SULPHONYLUREA_BIGUANIDE_FDC', 'Losucon M', '1 mg + 500 mg', 'TABLET_SUSTAINED_RELEASE', 'Incepta Pharmaceuticals Ltd.', 900, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1471/glimepiride-metformin/brand-names'),
  ('Glimepiride + Metformin hydrochloride', 'SULPHONYLUREA_BIGUANIDE_FDC', 'Losucon M', '2 mg + 500 mg', 'TABLET_SUSTAINED_RELEASE', 'Incepta Pharmaceuticals Ltd.', 1200, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/1471/glimepiride-metformin/brand-names'),
  ('Acarbose', 'ALPHA_GLUCOSIDASE_INHIBITOR', 'Gluco-A', '50 mg', 'TABLET', 'ACME Laboratories Ltd.', 1104, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/27/acarbose/brand-names'),
  ('Acarbose', 'ALPHA_GLUCOSIDASE_INHIBITOR', 'Gluco-A', '100 mg', 'TABLET', 'ACME Laboratories Ltd.', 2007, 'tablet', NULL, true, '1 x 10 pack', 'https://medex.com.bd/generics/27/acarbose/brand-names'),
  ('Acarbose', 'ALPHA_GLUCOSIDASE_INHIBITOR', 'Sugatrol', '50 mg', 'TABLET', 'Pacific Pharmaceuticals Ltd.', 1500, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/27/acarbose/brand-names'),
  ('Acarbose', 'ALPHA_GLUCOSIDASE_INHIBITOR', 'Sugatrol', '100 mg', 'TABLET', 'Pacific Pharmaceuticals Ltd.', 2500, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/27/acarbose/brand-names'),
  ('Voglibose', 'ALPHA_GLUCOSIDASE_INHIBITOR', 'Vibose', '0.2 mg', 'TABLET', 'Beximco Pharmaceuticals Ltd.', 600, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1266/voglibose/brand-names'),
  ('Voglibose', 'ALPHA_GLUCOSIDASE_INHIBITOR', 'Vibose', '0.3 mg', 'TABLET', 'Beximco Pharmaceuticals Ltd.', 800, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1266/voglibose/brand-names'),
  ('Voglibose', 'ALPHA_GLUCOSIDASE_INHIBITOR', 'Volidus', '0.2 mg', 'TABLET', 'Opsonin Pharma Ltd.', 600, 'tablet', NULL, true, '3 x 14 pack', 'https://medex.com.bd/generics/1266/voglibose/brand-names'),
  ('Voglibose', 'ALPHA_GLUCOSIDASE_INHIBITOR', 'Volidus', '0.3 mg', 'TABLET', 'Opsonin Pharma Ltd.', 800, 'tablet', NULL, true, '3 x 14 pack', 'https://medex.com.bd/generics/1266/voglibose/brand-names'),
  ('Levothyroxine sodium', 'THYROID_HORMONE', 'Thyrin', '25 mcg', 'TABLET', 'Square Pharmaceuticals PLC', 111, 'tablet', NULL, true, '6 x 15 pack', 'https://medex.com.bd/generics/1174/levothyroxine-sodium/brand-names'),
  ('Levothyroxine sodium', 'THYROID_HORMONE', 'Thyrin', '50 mcg', 'TABLET', 'Square Pharmaceuticals PLC', 225, 'tablet', NULL, true, '6 x 15 pack', 'https://medex.com.bd/generics/1174/levothyroxine-sodium/brand-names'),
  ('Levothyroxine sodium', 'THYROID_HORMONE', 'Thyrin', '75 mcg', 'TABLET', 'Square Pharmaceuticals PLC', 310, 'tablet', NULL, true, '4 x 15 pack', 'https://medex.com.bd/generics/1174/levothyroxine-sodium/brand-names'),
  ('Levothyroxine sodium', 'THYROID_HORMONE', 'Thyrin', '100 mcg', 'TABLET', 'Square Pharmaceuticals PLC', 310, 'tablet', NULL, true, '4 x 15 pack', 'https://medex.com.bd/generics/1174/levothyroxine-sodium/brand-names'),
  ('Levothyroxine sodium', 'THYROID_HORMONE', 'Thyrox', '12.5 mcg', 'TABLET', 'Renata PLC', 70, 'tablet', NULL, true, '1 x 100 pack. medex prints this strength as ''12.5 mg'' - treated here as 12.5 mcg; confirm against the pack before dispensing', 'https://medex.com.bd/generics/1174/levothyroxine-sodium/brand-names'),
  ('Levothyroxine sodium', 'THYROID_HORMONE', 'Thyrox', '25 mcg', 'TABLET', 'Renata PLC', 111, 'tablet', NULL, true, '1 x 100 or 3 x 30 pack', 'https://medex.com.bd/generics/1174/levothyroxine-sodium/brand-names'),
  ('Levothyroxine sodium', 'THYROID_HORMONE', 'Thyrox', '50 mcg', 'TABLET', 'Renata PLC', 220, 'tablet', NULL, true, '3 x 30 pack', 'https://medex.com.bd/generics/1174/levothyroxine-sodium/brand-names'),
  ('Levothyroxine sodium', 'THYROID_HORMONE', 'Thyrox', '75 mcg', 'TABLET', 'Renata PLC', 310, 'tablet', NULL, true, '1 x 60 pack', 'https://medex.com.bd/generics/1174/levothyroxine-sodium/brand-names'),
  ('Levothyroxine sodium', 'THYROID_HORMONE', 'Thyrox', '100 mcg', 'TABLET', 'Renata PLC', 310, 'tablet', NULL, true, '1 x 60 pack', 'https://medex.com.bd/generics/1174/levothyroxine-sodium/brand-names'),
  ('Levothyroxine sodium', 'THYROID_HORMONE', 'Thyronor', '12.5 mcg', 'TABLET', 'Nuvista Pharma Ltd.', 70, 'tablet', NULL, true, '3 x 35 pack', 'https://medex.com.bd/generics/1174/levothyroxine-sodium/brand-names'),
  ('Levothyroxine sodium', 'THYROID_HORMONE', 'Leroid', '25 mcg', 'CAPSULE', 'Drug International Ltd.', 250, 'capsule', NULL, true, '4 x 15 pack', 'https://medex.com.bd/generics/1174/levothyroxine-sodium/brand-names'),
  ('Carbimazole', 'ANTITHYROID', 'Carbizol', '5 mg', 'TABLET', 'Square Pharmaceuticals PLC', 400, 'tablet', NULL, true, '10 x 10 pack', 'https://medex.com.bd/generics/200/carbimazole/brand-names'),
  ('Carbimazole', 'ANTITHYROID', 'Carbizol', '10 mg', 'TABLET', 'Square Pharmaceuticals PLC', 500, 'tablet', NULL, true, '6 x 10 pack', 'https://medex.com.bd/generics/200/carbimazole/brand-names'),
  ('Carbimazole', 'ANTITHYROID', 'Carbiroid', '5 mg', 'TABLET', 'The White Horse Pharmaceuticals Ltd.', 480, 'tablet', NULL, true, '5 x 20 pack', 'https://medex.com.bd/generics/200/carbimazole/brand-names'),
  ('Carbimazole', 'ANTITHYROID', 'Carbiroid', '10 mg', 'TABLET', 'The White Horse Pharmaceuticals Ltd.', 500, 'tablet', NULL, true, '3 x 20 pack', 'https://medex.com.bd/generics/200/carbimazole/brand-names'),
  ('Methimazole', 'ANTITHYROID', 'Tapazol', '5 mg', 'TABLET', 'Renata PLC', 600, 'tablet', NULL, true, '3 x 30 pack', 'https://medex.com.bd/generics/1870/methimazole/brand-names'),
  ('Methimazole', 'ANTITHYROID', 'Tapazol', '10 mg', 'TABLET', 'Renata PLC', 1100, 'tablet', NULL, true, '5 x 14 pack', 'https://medex.com.bd/generics/1870/methimazole/brand-names'),
  ('Methimazole', 'ANTITHYROID', 'Thyzol', '5 mg', 'TABLET', 'Nuvista Pharma Ltd.', 600, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1870/methimazole/brand-names'),
  ('Methimazole', 'ANTITHYROID', 'Thyzol', '10 mg', 'TABLET', 'Nuvista Pharma Ltd.', 1100, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1870/methimazole/brand-names'),
  ('Propylthiouracil', 'ANTITHYROID', 'PTU', '50 mg', 'TABLET', 'Renata PLC', 1000, 'tablet', NULL, true, '4 x 14 pack BDT 560.00', 'https://medex.com.bd/generics/1826/propylthiouracil/brand-names'),
  ('Propylthiouracil', 'ANTITHYROID', 'Thiocil', '50 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1000, 'tablet', NULL, true, '5 x 10 pack BDT 500.00', 'https://medex.com.bd/generics/1826/propylthiouracil/brand-names'),
  ('Atorvastatin calcium', 'STATIN', 'Anzitor', '10 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1200, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/92/atorvastatin-calcium/brand-names'),
  ('Atorvastatin calcium', 'STATIN', 'Anzitor', '20 mg', 'TABLET', 'Square Pharmaceuticals PLC', 2000, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/92/atorvastatin-calcium/brand-names'),
  ('Atorvastatin calcium', 'STATIN', 'Anzitor', '40 mg', 'TABLET', 'Square Pharmaceuticals PLC', 2800, 'tablet', NULL, true, '1 x 10 pack', 'https://medex.com.bd/generics/92/atorvastatin-calcium/brand-names'),
  ('Atorvastatin calcium', 'STATIN', 'Tiginor', '10 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1150, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/92/atorvastatin-calcium/brand-names'),
  ('Atorvastatin calcium', 'STATIN', 'Tiginor', '20 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 2000, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/92/atorvastatin-calcium/brand-names'),
  ('Rosuvastatin calcium', 'STATIN', 'Rosuva', '5 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1200, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/970/rosuvastatin-calcium/brand-names'),
  ('Rosuvastatin calcium', 'STATIN', 'Rosuva', '10 mg', 'TABLET', 'Square Pharmaceuticals PLC', 2200, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/970/rosuvastatin-calcium/brand-names'),
  ('Rosuvastatin calcium', 'STATIN', 'Rosuva', '20 mg', 'TABLET', 'Square Pharmaceuticals PLC', 3200, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/970/rosuvastatin-calcium/brand-names'),
  ('Rosuvastatin calcium', 'STATIN', 'Rocovas', '5 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1000, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/970/rosuvastatin-calcium/brand-names'),
  ('Rosuvastatin calcium', 'STATIN', 'Rocovas', '10 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 2000, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/970/rosuvastatin-calcium/brand-names'),
  ('Rosuvastatin calcium', 'STATIN', 'Rocovas', '20 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 3000, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/970/rosuvastatin-calcium/brand-names'),
  ('Simvastatin', 'STATIN', 'Vastocor', '10 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1200, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/989/simvastatin/brand-names'),
  ('Simvastatin', 'STATIN', 'Simvatin', '10 mg', 'TABLET', 'ACME Laboratories Ltd.', 1104, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/989/simvastatin/brand-names'),
  ('Simvastatin', 'STATIN', 'Simvatin', '20 mg', 'TABLET', 'ACME Laboratories Ltd.', 1811, 'tablet', NULL, true, '1 x 10 pack', 'https://medex.com.bd/generics/989/simvastatin/brand-names'),
  ('Fenofibrate', 'FIBRATE', 'Lipired', '200 mg', 'CAPSULE', 'Square Pharmaceuticals PLC', 704, 'capsule', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/454/fenofibrate/brand-names'),
  ('Fenofibrate', 'FIBRATE', 'Nofiate', '200 mg', 'CAPSULE', 'Incepta Pharmaceuticals Ltd.', 700, 'capsule', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/454/fenofibrate/brand-names'),
  ('Fenofibrate', 'FIBRATE', 'Fenatrol', '145 mg', 'TABLET', 'Drug International Ltd.', 800, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/454/fenofibrate/brand-names'),
  ('Ezetimibe', 'CHOLESTEROL_ABSORPTION_INHIBITOR', 'Ezetim', '10 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1000, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/449/ezetimibe/brand-names'),
  ('Ezetimibe', 'CHOLESTEROL_ABSORPTION_INHIBITOR', 'Ezeta', '10 mg', 'TABLET', 'Beximco Pharmaceuticals Ltd.', 800, 'tablet', NULL, true, '2 x 10 pack', 'https://medex.com.bd/generics/449/ezetimibe/brand-names'),
  ('Ramipril', 'ACE_INHIBITOR', 'Ripril', '2.5 mg', 'TABLET', 'Square Pharmaceuticals PLC', 500, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/947/ramipril/brand-names'),
  ('Ramipril', 'ACE_INHIBITOR', 'Ripril', '5 mg', 'TABLET', 'Square Pharmaceuticals PLC', 800, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/947/ramipril/brand-names'),
  ('Ramipril', 'ACE_INHIBITOR', 'Ramoril', '1.25 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 250, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/947/ramipril/brand-names'),
  ('Ramipril', 'ACE_INHIBITOR', 'Ramoril', '2.5 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 500, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/947/ramipril/brand-names'),
  ('Ramipril', 'ACE_INHIBITOR', 'Ramoril', '5 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 800, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/947/ramipril/brand-names'),
  ('Enalapril maleate', 'ACE_INHIBITOR', 'Anapril', '5 mg', 'TABLET', 'Eskayef Pharmaceuticals Ltd.', 151, 'tablet', NULL, true, '10 x 10 pack', 'https://medex.com.bd/generics/409/enalapril-maleate/brand-names'),
  ('Enalapril maleate', 'ACE_INHIBITOR', 'Anapril', '10 mg', 'TABLET', 'Eskayef Pharmaceuticals Ltd.', 270, 'tablet', NULL, true, '10 x 10 pack', 'https://medex.com.bd/generics/409/enalapril-maleate/brand-names'),
  ('Enalapril maleate', 'ACE_INHIBITOR', 'Enaril', '5 mg', 'TABLET', 'Beximco Pharmaceuticals Ltd.', 100, 'tablet', NULL, true, '10 x 10 pack', 'https://medex.com.bd/generics/409/enalapril-maleate/brand-names'),
  ('Lisinopril', 'ACE_INHIBITOR', 'Lipril', '5 mg', 'TABLET', 'ACME Laboratories Ltd.', 300, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/691/lisinopril/brand-names'),
  ('Lisinopril', 'ACE_INHIBITOR', 'Lipril', '10 mg', 'TABLET', 'ACME Laboratories Ltd.', 553, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/691/lisinopril/brand-names'),
  ('Lisinopril', 'ACE_INHIBITOR', 'Acepril', '5 mg', 'TABLET', 'Drug International Ltd.', 405, 'tablet', NULL, true, '4 x 14 pack', 'https://medex.com.bd/generics/691/lisinopril/brand-names'),
  ('Lisinopril', 'ACE_INHIBITOR', 'Acepril', '10 mg', 'TABLET', 'Drug International Ltd.', 705, 'tablet', NULL, true, '2 x 14 pack', 'https://medex.com.bd/generics/691/lisinopril/brand-names'),
  ('Losartan potassium', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Angilock', '25 mg', 'TABLET', 'Square Pharmaceuticals PLC', 500, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/699/losartan-potassium/brand-names'),
  ('Losartan potassium', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Angilock', '50 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1000, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/699/losartan-potassium/brand-names'),
  ('Losartan potassium', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Angilock', '100 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1203, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/699/losartan-potassium/brand-names'),
  ('Losartan potassium', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Osartil', '25 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 500, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/699/losartan-potassium/brand-names'),
  ('Losartan potassium', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Osartil', '50 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1000, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/699/losartan-potassium/brand-names'),
  ('Losartan potassium + Hydrochlorothiazide', 'ARB_THIAZIDE_FDC', 'Angilock Plus', '50 mg + 12.5 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1000, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/550/losartan-potassium-hydrochlorothiazide/brand-names'),
  ('Losartan potassium + Hydrochlorothiazide', 'ARB_THIAZIDE_FDC', 'Angilock Plus', '100 mg + 25 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1203, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/550/losartan-potassium-hydrochlorothiazide/brand-names'),
  ('Losartan potassium + Hydrochlorothiazide', 'ARB_THIAZIDE_FDC', 'Osartil Plus', '50 mg + 12.5 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1000, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/550/losartan-potassium-hydrochlorothiazide/brand-names'),
  ('Telmisartan', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Telmilok', '40 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1250, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1038/telmisartan/brand-names'),
  ('Telmisartan', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Telmilok', '80 mg', 'TABLET', 'Square Pharmaceuticals PLC', 2000, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1038/telmisartan/brand-names'),
  ('Telmisartan', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Telmipres', '20 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 600, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1038/telmisartan/brand-names'),
  ('Telmisartan', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Telmipres', '40 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 900, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1038/telmisartan/brand-names'),
  ('Telmisartan', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Telmipres', '80 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1100, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/1038/telmisartan/brand-names'),
  ('Telmisartan + Amlodipine besilate', 'ARB_CCB_FDC', 'Camlotel', '5 mg + 40 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1250, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/66/amlodipine-besilate-telmisartan/brand-names'),
  ('Telmisartan + Amlodipine besilate', 'ARB_CCB_FDC', 'Camlotel', '5 mg + 80 mg', 'TABLET', 'Square Pharmaceuticals PLC', 2000, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/66/amlodipine-besilate-telmisartan/brand-names'),
  ('Telmisartan + Amlodipine besilate', 'ARB_CCB_FDC', 'Telmidip', '5 mg + 40 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1000, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/66/amlodipine-besilate-telmisartan/brand-names'),
  ('Telmisartan + Amlodipine besilate', 'ARB_CCB_FDC', 'Telmidip', '5 mg + 80 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1200, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/66/amlodipine-besilate-telmisartan/brand-names'),
  ('Olmesartan medoxomil', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Olmecar', '10 mg', 'TABLET', 'Square Pharmaceuticals PLC', 600, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/822/olmesartan-medoxomil/brand-names'),
  ('Olmesartan medoxomil', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Olmecar', '20 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1000, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/822/olmesartan-medoxomil/brand-names'),
  ('Olmesartan medoxomil', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Olmecar', '40 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1800, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/822/olmesartan-medoxomil/brand-names'),
  ('Olmesartan medoxomil', 'ANGIOTENSIN_II_RECEPTOR_BLOCKER', 'Xyotil', '20 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 800, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/822/olmesartan-medoxomil/brand-names'),
  ('Amlodipine besilate', 'CALCIUM_CHANNEL_BLOCKER', 'Camlodin', '5 mg', 'TABLET', 'Square Pharmaceuticals PLC', 502, 'tablet', NULL, true, '4 x 15 pack', 'https://medex.com.bd/generics/62/amlodipine-besilate/brand-names'),
  ('Amlodipine besilate', 'CALCIUM_CHANNEL_BLOCKER', 'Amlotab', '5 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 500, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/62/amlodipine-besilate/brand-names'),
  ('Amlodipine besilate', 'CALCIUM_CHANNEL_BLOCKER', 'Amlotab', '10 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 700, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/62/amlodipine-besilate/brand-names'),
  ('Amlodipine besilate', 'CALCIUM_CHANNEL_BLOCKER', 'Amdocal', '10 mg', 'TABLET', 'Beximco Pharmaceuticals Ltd.', 800, 'tablet', NULL, true, '4 x 15 pack', 'https://medex.com.bd/generics/62/amlodipine-besilate/brand-names'),
  ('Aspirin (low dose)', 'ANTIPLATELET', 'Carva', '75 mg', 'TABLET_ENTERIC_COATED', 'Square Pharmaceuticals PLC', 80, 'tablet', NULL, true, '13 x 15 pack', 'https://medex.com.bd/generics/85/aspirin/brand-names'),
  ('Aspirin (low dose)', 'ANTIPLATELET', 'Cardoprin', '75 mg', 'TABLET_ENTERIC_COATED', 'Beximco Pharmaceuticals Ltd.', 80, 'tablet', NULL, true, '7 x 14 pack', 'https://medex.com.bd/generics/85/aspirin/brand-names'),
  ('Aspirin (low dose)', 'ANTIPLATELET', 'Ecosprin', '81 mg', 'TABLET_ENTERIC_COATED', 'ACME Laboratories Ltd.', 68, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/85/aspirin/brand-names'),
  ('Aspirin (low dose)', 'ANTIPLATELET', 'Erasprin', '81 mg', 'TABLET_ENTERIC_COATED', 'UniMed UniHealth Pharmaceuticals Ltd.', 86, 'tablet', NULL, true, '6 x 10 pack', 'https://medex.com.bd/generics/85/aspirin/brand-names'),
  ('Cholecalciferol (Vitamin D3)', 'VITAMIN_SUPPLEMENT', 'D-Balance', '2000 IU', 'CAPSULE', 'Square Pharmaceuticals PLC', 400, 'capsule', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/252/cholecalciferol-vitamin-d3/brand-names'),
  ('Cholecalciferol (Vitamin D3)', 'VITAMIN_SUPPLEMENT', 'D-Balance', '20000 IU', 'CAPSULE', 'Square Pharmaceuticals PLC', 2000, 'capsule', NULL, true, '1 x 10 pack', 'https://medex.com.bd/generics/252/cholecalciferol-vitamin-d3/brand-names'),
  ('Cholecalciferol (Vitamin D3)', 'VITAMIN_SUPPLEMENT', 'D-Balance', '40000 IU', 'CAPSULE', 'Square Pharmaceuticals PLC', 3500, 'capsule', NULL, true, '1 x 10 pack; weekly dosing', 'https://medex.com.bd/generics/252/cholecalciferol-vitamin-d3/brand-names'),
  ('Cholecalciferol (Vitamin D3)', 'VITAMIN_SUPPLEMENT', 'D-Balance', '200000 IU/mL', 'INJECTION', 'Square Pharmaceuticals PLC', 12000, 'ampoule', NULL, true, '1 mL injectable', 'https://medex.com.bd/generics/252/cholecalciferol-vitamin-d3/brand-names'),
  ('Cholecalciferol (Vitamin D3)', 'VITAMIN_SUPPLEMENT', 'Osteo-D', '1000 IU', 'CHEWABLE_TABLET', 'Incepta Pharmaceuticals Ltd.', 600, 'tablet', NULL, true, '5 x 14 pack', 'https://medex.com.bd/generics/252/cholecalciferol-vitamin-d3/brand-names'),
  ('Cholecalciferol (Vitamin D3)', 'VITAMIN_SUPPLEMENT', 'Osteo-D', '20000 IU', 'CAPSULE', 'Incepta Pharmaceuticals Ltd.', 2000, 'capsule', NULL, true, '1 x 10 pack', 'https://medex.com.bd/generics/252/cholecalciferol-vitamin-d3/brand-names'),
  ('Calcium lactate gluconate + Calcium carbonate + Vitamin D3', 'CALCIUM_VITAMIN_D_SUPPLEMENT', 'Calbo-D Vita', '1358.196 mg + 600 mg (elemental calcium) + 400 IU', 'EFFERVESCENT_TABLET', 'Square Pharmaceuticals PLC', 1505, 'tablet', NULL, true, '1 x 10 pack', 'https://medex.com.bd/generics/1201/calcium-lactate-gluconate-calcium-carbonate-vitamin-d3/brand-names'),
  ('Calcium lactate gluconate + Calcium carbonate + Vitamin D3', 'CALCIUM_VITAMIN_D_SUPPLEMENT', 'FizyCal-D', '1358.196 mg + 600 mg (elemental calcium) + 400 IU', 'EFFERVESCENT_TABLET', 'Incepta Pharmaceuticals Ltd.', 1500, 'tablet', NULL, true, '1 x 10 pack', 'https://medex.com.bd/generics/1201/calcium-lactate-gluconate-calcium-carbonate-vitamin-d3/brand-names'),
  ('Calcium lactate gluconate + Calcium carbonate + Vitamin D3', 'CALCIUM_VITAMIN_D_SUPPLEMENT', 'Ostocal Vita', '1358.196 mg + 600 mg (coral calcium) + 400 IU', 'EFFERVESCENT_TABLET', 'Eskayef Pharmaceuticals Ltd.', 1700, 'tablet', NULL, true, '1 x 20 pack', 'https://medex.com.bd/generics/1201/calcium-lactate-gluconate-calcium-carbonate-vitamin-d3/brand-names'),
  ('Calcium lactate gluconate + Calcium carbonate + Vitamin D3', 'CALCIUM_VITAMIN_D_SUPPLEMENT', 'Cora-DX Vita', '1358.196 mg + 600 mg (coral calcium) + 400 IU', 'EFFERVESCENT_TABLET', 'ACI Limited', 1700, 'tablet', NULL, true, '1 x 14 pack', 'https://medex.com.bd/generics/1201/calcium-lactate-gluconate-calcium-carbonate-vitamin-d3/brand-names'),
  ('Folic acid', 'VITAMIN_SUPPLEMENT', 'B-9', '2.5 mg/5 mL', 'ORAL_SOLUTION', 'Square Pharmaceuticals PLC', 5000, 'bottle', NULL, true, '100 mL bottle. medex lists only oral solutions under the plain folic acid generic; folic acid tablets appear on medex only inside combination generics', 'https://medex.com.bd/generics/495/folic-acid/brand-names'),
  ('Folic acid', 'VITAMIN_SUPPLEMENT', 'Frutifol', '2.5 mg/5 mL', 'ORAL_SOLUTION', 'Incepta Pharmaceuticals Ltd.', 5000, 'bottle', NULL, true, '100 mL bottle. medex lists only oral solutions under the plain folic acid generic; folic acid tablets appear on medex only inside combination generics', 'https://medex.com.bd/generics/495/folic-acid/brand-names'),
  ('Pregabalin', 'NEUROPATHIC_PAIN_DIABETIC_NEUROPATHY', 'Neurolin', '25 mg', 'CAPSULE', 'Square Pharmaceuticals PLC', 1100, 'capsule', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/919/pregabalin/brand-names'),
  ('Pregabalin', 'NEUROPATHIC_PAIN_DIABETIC_NEUROPATHY', 'Neurolin', '50 mg', 'CAPSULE', 'Square Pharmaceuticals PLC', 1500, 'capsule', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/919/pregabalin/brand-names'),
  ('Pregabalin', 'NEUROPATHIC_PAIN_DIABETIC_NEUROPATHY', 'Neurolin', '75 mg', 'CAPSULE', 'Square Pharmaceuticals PLC', 1900, 'capsule', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/919/pregabalin/brand-names'),
  ('Pregabalin', 'NEUROPATHIC_PAIN_DIABETIC_NEUROPATHY', 'Neurolin CR', '82.5 mg', 'TABLET_CONTROLLED_RELEASE', 'Square Pharmaceuticals PLC', 2500, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/919/pregabalin/brand-names'),
  ('Pregabalin', 'NEUROPATHIC_PAIN_DIABETIC_NEUROPATHY', 'Pregaben', '25 mg', 'CAPSULE', 'Incepta Pharmaceuticals Ltd.', 900, 'capsule', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/919/pregabalin/brand-names'),
  ('Gabapentin', 'NEUROPATHIC_PAIN_DIABETIC_NEUROPATHY', 'Gabastar', '100 mg', 'TABLET', 'Square Pharmaceuticals PLC', 604, 'tablet', NULL, true, '5 x 10 pack', 'https://medex.com.bd/generics/510/gabapentin/brand-names'),
  ('Gabapentin', 'NEUROPATHIC_PAIN_DIABETIC_NEUROPATHY', 'Gabastar', '300 mg', 'TABLET', 'Square Pharmaceuticals PLC', 1611, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/510/gabapentin/brand-names'),
  ('Gabapentin', 'NEUROPATHIC_PAIN_DIABETIC_NEUROPATHY', 'Gabapen', '100 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 600, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/510/gabapentin/brand-names'),
  ('Gabapentin', 'NEUROPATHIC_PAIN_DIABETIC_NEUROPATHY', 'Gabapen', '300 mg', 'TABLET', 'Incepta Pharmaceuticals Ltd.', 1600, 'tablet', NULL, true, '3 x 10 pack', 'https://medex.com.bd/generics/510/gabapentin/brand-names')
ON CONFLICT (trade_name, strength, form_code, manufacturer, dispense_unit) DO UPDATE SET
  generic_name = EXCLUDED.generic_name, class_code = EXCLUDED.class_code,
  unit_price_poisha = EXCLUDED.unit_price_poisha,
  dgda_registration = EXCLUDED.dgda_registration, is_active = EXCLUDED.is_active,
  notes = EXCLUDED.notes, source_url = EXCLUDED.source_url;

-- ---------------------------------------------------------------------------
-- 8a. Nothing here is deleted
-- ---------------------------------------------------------------------------

-- `ALTER DEFAULT PRIVILEGES` in `core` hands the application role full CRUD on every table the
-- migrator creates, DELETE included. That is right for a patient's address and wrong for a price
-- history, so it is taken back explicitly — the same treatment `core.admin_alert` gets, for the
-- same reason. A withdrawn product is deactivated; it is not removed, and neither are the prices
-- that were true while it was stocked or the record of an import that went wrong.
--
-- `assert_formulary_history_is_kept()` below re-checks this after every migration, because a
-- later migration that adds a grant would otherwise undo it silently.
REVOKE DELETE, TRUNCATE ON core.medication_product    FROM dthcms_app;
REVOKE DELETE, TRUNCATE ON core.medication_price      FROM dthcms_app;
REVOKE DELETE, TRUNCATE ON core.formulary_import_row  FROM dthcms_app;
REVOKE DELETE, TRUNCATE ON core.formulary_import      FROM dthcms_app;
REVOKE DELETE, TRUNCATE ON core.formulary_price_review FROM dthcms_app;
REVOKE DELETE, TRUNCATE ON core.generic               FROM dthcms_app;
REVOKE DELETE, TRUNCATE ON core.medication_class      FROM dthcms_app;
REVOKE DELETE, TRUNCATE ON core.medication_form       FROM dthcms_app;
REVOKE DELETE, TRUNCATE ON core.dispense_unit         FROM dthcms_app;
REVOKE DELETE, TRUNCATE ON core.formulary_seed_row    FROM dthcms_app;

-- ---------------------------------------------------------------------------
-- 9. Projecting the seed into the live tables, re-runnably
-- ---------------------------------------------------------------------------

-- The three rules this function is built around, in the order they matter:
--
--  1. **It never clobbers a price a human has approved.** A seed price is inserted only for a
--     product that has *no price row at all*. Once anybody — the pharmacist, an import, the
--     monthly review — has recorded a price, this function is done with that product forever.
--  2. **It never reactivates a product a human deactivated.** `is_active` is set on insert and
--     left alone on conflict. A product somebody withdrew stays withdrawn across a re-run.
--  3. **It is safe to call twice.** Every statement is an upsert or a conditional insert, and
--     the test calls it twice with a verified price in between and asserts nothing moved.
--
-- It returns what it did, because a seeding function that reports nothing is one whose failures
-- look exactly like its successes.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.apply_formulary_seed(p_facility uuid, p_effective_from date)
RETURNS TABLE (generics_touched integer, products_touched integer, prices_inserted integer)
LANGUAGE plpgsql AS $$
DECLARE
  v_generics integer;
  v_products integer;
  v_prices integer;
BEGIN
  -- The molecules. One class per generic in the curated set; a generic that already exists keeps
  -- whatever class a human has since filed it under.
  WITH seeded AS (
    SELECT DISTINCT ON (lower(s.generic_name)) s.generic_name, s.class_code
      FROM core.formulary_seed_row s
     ORDER BY lower(s.generic_name), s.class_code
  ), upserted AS (
    INSERT INTO core.generic (name, class_code)
    SELECT seeded.generic_name, seeded.class_code FROM seeded
    ON CONFLICT (lower(name)) DO NOTHING
    RETURNING 1
  )
  SELECT count(*)::integer INTO v_generics FROM upserted;

  -- The products. The descriptive columns are refreshed from the source; `is_active`, the
  -- withdrawal columns and everything a person may have edited are not touched on conflict.
  WITH upserted AS (
    INSERT INTO core.medication_product
      (facility_id, generic_id, trade_name, strength, form_code, manufacturer,
       dispense_unit, dgda_registration, notes, source_url, is_active)
    SELECT p_facility, g.id, s.trade_name, s.strength, s.form_code, s.manufacturer,
           s.dispense_unit, s.dgda_registration, s.notes, s.source_url, s.is_active
      FROM core.formulary_seed_row s
      JOIN core.generic g ON lower(g.name) = lower(s.generic_name)
    ON CONFLICT (facility_id, lower(trade_name), lower(strength), form_code,
                 lower(manufacturer), dispense_unit)
    DO UPDATE SET notes = EXCLUDED.notes, source_url = EXCLUDED.source_url
    RETURNING 1
  )
  SELECT count(*)::integer INTO v_products FROM upserted;

  -- The prices. `NOT EXISTS (any price for this product)` is rule 1, and it is deliberately
  -- "any price" rather than "any seed price": once a pharmacist has priced something, the seed
  -- has no further opinion about it, even about a period the pharmacist left uncovered.
  WITH inserted AS (
    INSERT INTO core.medication_price
      (facility_id, product_id, unit_price_poisha, effective_from, verification, origin,
       source_note, source_url)
    SELECT p_facility, pr.id, s.unit_price_poisha, p_effective_from, 'PROVISIONAL', 'SEED',
           'Published MRP, medex.com.bd, read 8 September 2026. Not reviewed by this clinic.',
           s.source_url
      FROM core.formulary_seed_row s
      JOIN core.generic g ON lower(g.name) = lower(s.generic_name)
      JOIN core.medication_product pr
        ON pr.facility_id = p_facility
       AND lower(pr.trade_name) = lower(s.trade_name)
       AND lower(pr.strength) = lower(s.strength)
       AND pr.form_code = s.form_code
       AND lower(pr.manufacturer) = lower(s.manufacturer)
       AND pr.dispense_unit = s.dispense_unit
     WHERE NOT EXISTS (
       SELECT 1 FROM core.medication_price mp WHERE mp.product_id = pr.id
     )
    RETURNING 1
  )
  SELECT count(*)::integer INTO v_prices FROM inserted;

  RETURN QUERY SELECT v_generics, v_products, v_prices;
END
$$;
-- +goose StatementEnd

-- Seeded for every active facility, effective from the day the prices were read rather than from
-- today: the row says what was true on 8 September 2026, which is the only date these numbers can
-- honestly claim.
-- +goose StatementBegin
DO $$
DECLARE f record;
BEGIN
  FOR f IN SELECT id FROM core.facility WHERE is_active LOOP
    PERFORM core.apply_formulary_seed(f.id, DATE '2026-09-08');
  END LOOP;
END
$$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- 10. Who may do what
-- ---------------------------------------------------------------------------

-- All three names were reserved in `core.permission` at CP06, before this module existed; this
-- migration gives them their descriptions and grants them to roles for the first time.
--
-- **None of these is sensitive.** §4.4 blinds registration and the pharmacist from diagnoses and
-- clinical interpretations; a formulary holds neither. It holds trade names, strengths and
-- prices, and the pharmacist is the person §16.1 puts in charge of them — marking these
-- sensitive would be the access model contradicting itself.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('formulary.read', 'formulary', 'read', '',
   'Read the medicine formulary: products, strengths, forms and prices', false),
  ('formulary.write', 'formulary', 'write', '',
   'Add and correct formulary products, record prices, and import a price list', false),
  ('formulary.price.review', 'formulary', 'price', 'review',
   'Own the monthly price review: confirm prices and close the review cycle', false)
ON CONFLICT (code) DO UPDATE SET
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

-- Reading is wide: a physician prescribing, a pharmacist dispensing, the education officer
-- explaining a cost to a patient, and QA all need to see what a medicine is and what it costs.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'formulary.read'
  FROM core.role r
 -- Not RESEARCHER. D-48 holds that the research role reaches the de-identified marts and
 -- nothing else, and `assert_rbac_constraints()` enforces it — a formulary holds no patient
 -- data, but the rule is about the role's reach rather than about any one table, and CP127's
 -- affordability lens gets its prices through `research` rather than through `core`.
 WHERE r.code IN ('PHYSICIAN', 'JUNIOR_DOCTOR', 'PHARMACIST', 'RX_EDUCATOR', 'QA',
                  'NUTRITIONIST', 'ADMIN')
ON CONFLICT DO NOTHING;

-- Writing is narrow. The pharmacist because §16.1 says so, the administrator because somebody
-- has to be able to when the pharmacist is not in, and the physician because D-56 made the
-- formulary's *content* a clinical decision and he is the person who makes it.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'formulary.write'
  FROM core.role r WHERE r.code IN ('PHARMACIST', 'ADMIN', 'PHYSICIAN')
ON CONFLICT DO NOTHING;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'formulary.price.review'
  FROM core.role r WHERE r.code IN ('PHARMACIST', 'ADMIN', 'PHYSICIAN')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- 11. The monthly reminder, on CP69's queue
-- ---------------------------------------------------------------------------

-- `ops.job_schedule.every_seconds` tops out at one day (ADR-0031: intervals, not cron
-- expressions, because the three periodic jobs this system had were all "every N"). A monthly
-- review does not fit that, and the answer is not a cron parser — it is that **"is a review due"
-- is a domain question, not a scheduling one**. The job runs daily and asks it: if the current
-- month has no review cycle and the due day has arrived, open one and raise the reminder.
-- Otherwise it does nothing and says so.
--
-- That makes the reminder idempotent by construction. The unique index on
-- (facility_id, period_month) is the backstop: a worker that runs this twice in one day, or two
-- workers that both claim it, produce one review cycle and one alert.
INSERT INTO ops.job_kind
  (kind, job_class, queue, priority, max_attempts, backoff_seconds, backoff_cap_seconds,
   sla_seconds, description_en, description_bn) VALUES
  ('maintenance.formulary_price_review', 'MAINTENANCE', 'maintenance', 100, 3, 60, 3600, NULL,
   'Open the month''s medicine price review and remind whoever owns it',
   'মাসের ওষুধের দাম পর্যালোচনা শুরু করা এবং যাঁর দায়িত্ব তাঁকে মনে করিয়ে দেওয়া')
ON CONFLICT (kind) DO UPDATE SET
  description_en = EXCLUDED.description_en, description_bn = EXCLUDED.description_bn;

INSERT INTO ops.job_schedule (kind, every_seconds) VALUES
  ('maintenance.formulary_price_review', 86400)
ON CONFLICT (kind) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 12. The invariants
-- ---------------------------------------------------------------------------

-- Criterion: *"price changes audited with the actor"*. The check constraint refuses such a row
-- from here on; this notices one that arrived some other way — a restore from a dump taken
-- before this migration, a hand edit during an incident, a future code path.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_verified_price_names_a_person() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.medication_price
   WHERE recorded_by IS NULL AND origin <> 'SEED';
  IF offenders > 0 THEN
    RAISE EXCEPTION '% medicine prices were recorded with nobody''s name against them', offenders;
  END IF;

  SELECT count(*) INTO offenders
    FROM core.medication_price
   WHERE verification = 'VERIFIED' AND recorded_by IS NULL;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% medicine prices are marked verified but name nobody who verified them',
      offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- Criterion 1: *"price as of any past date is retrievable"* — which is only true if the answer is
-- unique. The EXCLUDE constraint refuses an overlap from here on; this re-checks the whole table,
-- because a constraint added later does not validate what is already there. A day with two prices
-- would make every affordability figure in §12.3 depend on which row the planner happened to
-- return first.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_medicine_has_two_prices_on_one_day() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders FROM (
    SELECT a.id
      FROM core.medication_price a
      JOIN core.medication_price b
        ON b.product_id = a.product_id AND b.id <> a.id
       AND daterange(a.effective_from, a.effective_to, '[)')
        && daterange(b.effective_from, b.effective_to, '[)')
  ) overlapping;
  IF offenders > 0 THEN
    RAISE EXCEPTION
      '% medicine price rows overlap another price for the same product', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- Nothing in the formulary is deleted. A withdrawn product is deactivated and its price history
-- stays, which is only a guarantee if the application cannot remove either.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_formulary_history_is_kept() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  SELECT t.table_name INTO offender
    FROM unnest(ARRAY['medication_product', 'medication_price', 'formulary_import_row'])
      AS t(table_name)
   WHERE has_table_privilege('dthcms_app', 'core.' || t.table_name, 'DELETE')
      OR has_table_privilege('dthcms_app', 'core.' || t.table_name, 'TRUNCATE')
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION
      'the application role can delete from core.%, and the formulary keeps its history', offender;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_verified_price_names_a_person',
   'every medicine price that a person recorded names that person, and no price is verified without one', 110),
  ('assert_no_medicine_has_two_prices_on_one_day',
   'no medicine has two prices covering the same day, so the price as of a date is unique', 111),
  ('assert_formulary_history_is_kept',
   'the application role cannot delete a formulary product, a price, or an import outcome', 112)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_every_verified_price_names_a_person',
  'assert_no_medicine_has_two_prices_on_one_day',
  'assert_formulary_history_is_kept');
DROP FUNCTION IF EXISTS core.assert_every_verified_price_names_a_person();
DROP FUNCTION IF EXISTS core.assert_no_medicine_has_two_prices_on_one_day();
DROP FUNCTION IF EXISTS core.assert_formulary_history_is_kept();

DELETE FROM ops.job_schedule WHERE kind = 'maintenance.formulary_price_review';
DELETE FROM ops.job WHERE kind = 'maintenance.formulary_price_review';
DELETE FROM ops.job_kind WHERE kind = 'maintenance.formulary_price_review';

-- The three permission rows were reserved at CP06 and are not this migration's to remove; only
-- the grants it made are taken back.
DELETE FROM core.role_permission
 WHERE permission_code IN ('formulary.read', 'formulary.write', 'formulary.price.review');

DROP FUNCTION IF EXISTS core.apply_formulary_seed(uuid, date);

DROP TABLE IF EXISTS core.formulary_seed_row;
DROP TABLE IF EXISTS core.formulary_import_row;
DROP TABLE IF EXISTS core.formulary_import;
DROP TABLE IF EXISTS core.formulary_price_review;
DROP TABLE IF EXISTS core.formulary_review_owner;
DROP TRIGGER IF EXISTS medication_price_immutable ON core.medication_price;
DROP FUNCTION IF EXISTS core.medication_price_is_immutable();
DROP TABLE IF EXISTS core.medication_price;
DROP TABLE IF EXISTS core.medication_product;
DROP TABLE IF EXISTS core.generic;
DROP TABLE IF EXISTS core.dispense_unit;
DROP TABLE IF EXISTS core.medication_form;
DROP TABLE IF EXISTS core.medication_class;

DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core'
   AND table_name IN ('medication_class', 'medication_form', 'dispense_unit', 'generic',
                      'formulary_seed_row');
