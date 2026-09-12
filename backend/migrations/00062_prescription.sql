-- The prescription aggregate, its status machine, and the rule that a signed prescription is
-- never edited (CP80, §3 step 9, §7, §9.3).
--
-- This is the most medico-legally significant object in the system. Everything else here derives
-- from that one sentence.
--
-- # The ledger is the truth and these two tables are not
--
-- Both tables are in `read`. Every fact in them arrived as an event: `PRESCRIPTION_CREATED`,
-- `_ITEM_ADDED`, `_ITEM_MODIFIED`, `_ITEM_REMOVED`, `_CORRECTED`, `_CANCELLED`, and the four
-- transition events later checkpoints own. Drop both tables and a rebuild reconstructs them
-- exactly, which is the property that makes "who changed this prescription, when, and to what"
-- answerable from an append-only ledger rather than from a table anybody with an UPDATE could
-- have quietly fixed.
--
-- # Signed immutability, in the database, four ways
--
-- A handler check is not enough for this object, so:
--
--  1. **`dthcms_app` holds SELECT and nothing else** on both tables. The application cannot
--     write a prescription row by any statement, correct or malicious. This is the grant, and it
--     is the one that stops the application-level attack.
--  2. **`prescription_is_frozen()` refuses content changes for everyone**, the projector
--     included. Only the status column, the stamp columns belonging to the status being entered,
--     the correction link, and the bookkeeping columns may ever change. Patient, visit, facility,
--     who created it, what it corrects — immutable from INSERT, in every status including DRAFT.
--  3. **`prescription_transition_is_legal()` checks the status change against
--     `core.prescription_transition`**, which holds the twelve legal edges as rows. There is no
--     CASE statement in a trigger that somebody can extend by accident; adding an edge is an
--     INSERT in a migration, and the whole matrix is `SELECT * FROM core.prescription_transition`.
--  4. **`prescription_item_is_frozen()` allows an item to be inserted, changed or soft-removed
--     only while its prescription is DRAFT.** Not "not signed" — DRAFT. A prescription in
--     QA_REVIEW whose items changed underneath the reviewer is a prescription that was reviewed
--     and then altered, which is the same defect as editing a signed one, one step earlier.
--
-- A row is never deleted by any of this. `removed_at` marks an item the physician took off the
-- sheet, and the row stays, because "what was on this prescription at 14:05" has to have an
-- answer after the item was removed at 14:06.
--
-- # The one hole, named
--
-- The projector may DELETE, and only under `SET LOCAL dthcms.rebuilding = 'on'`. A rebuild has
-- to be able to empty the table; refusing that would mean the read model could never be
-- reconstructed from the ledger, which is the guarantee the whole design rests on. It is
-- deliberately not a security boundary — a role that can set a GUC can set that one — and it
-- does not need to be, because what it protects is the *projection*. The ledger behind it is
-- append-only by grant, rule and trigger (CP23), so a projection somebody emptied rebuilds into
-- exactly what it was.
--
-- # The price, captured by value
--
-- `price_poisha` is an amount on the item row, not a join to `core.medication_price`. It is
-- written from the event payload, and the event payload carries the number itself — so a price
-- that changes in March cannot change what a prescription written in February says the medicine
-- cost, and **a rebuild of this table from the ledger reproduces February's price**, because the
-- number is in the ledger and nothing at projection time looks it up.
--
-- `price_id`, `price_effective_from` and `price_verification` travel with it, by value too, so
-- the row is traceable to the price history without depending on that history's current shape.
-- CP75's own words: a price is never edited, only superseded. This makes a prescription
-- independent of even that.
--
-- # Correction after dispensing
--
-- The plan left this open. The decision, recorded here and flagged for Dr. Nahid:
--
--   * A correction is **always permitted**, at any status from SIGNED onward.
--   * It creates a **new prescription** that references the one it corrects
--     (`corrects_prescription_id`), and the original moves to CORRECTED and is linked forward
--     (`corrected_by_prescription_id`). The original is never edited and never removed from the
--     record.
--   * If the original was already DISPENSED, the correction records that fact **on itself**
--     (`corrects_dispensed_original`), so that anybody reading the correction knows medicine has
--     already left the counter without having to go and look at the original.
--
-- **What the pharmacy then does is a clinical and operational question, not ours.** The system
-- records that a dispensed prescription was corrected and makes that visible; it does not decide
-- whether the patient is called back.
--
-- # Bilingual
--
-- Every status, every transition and both permissions carry English and Bengali here rather than
-- in a Go map, because a status a screen cannot render in Bengali is a status the screen will
-- render in English.

-- +goose Up

-- ---------------------------------------------------------------------------
-- 1. The statuses, as rows
-- ---------------------------------------------------------------------------

CREATE TABLE core.prescription_status (
  status text PRIMARY KEY,
  name_en text NOT NULL,
  name_bn text NOT NULL,
  -- What this status means for the artefact, in words a physician would use.
  meaning_en text NOT NULL,
  meaning_bn text NOT NULL,
  -- Frozen statuses are the ones in which no item may be touched. Everything except DRAFT.
  is_frozen boolean NOT NULL,
  -- Terminal statuses have no outgoing edge at all.
  is_terminal boolean NOT NULL,
  ordering int NOT NULL
);

COMMENT ON TABLE core.prescription_status IS
  'The seven states a prescription can be in (CP80). Rows rather than an enum so the machine is readable with a SELECT.';

INSERT INTO core.prescription_status
  (status, name_en, name_bn, meaning_en, meaning_bn, is_frozen, is_terminal, ordering) VALUES
  ('DRAFT', 'Draft', 'খসড়া',
   'Being written. The only status in which items may be added, changed or removed.',
   'লেখা হচ্ছে। একমাত্র এই অবস্থাতেই ওষুধ যোগ, বদল বা বাদ দেওয়া যায়।', false, false, 1),
  ('QA_REVIEW', 'With QA', 'কিউএ পর্যালোচনায়',
   'Submitted for the quality check. Items are fixed; a bounce returns it to draft.',
   'মান-যাচাইয়ের জন্য জমা দেওয়া হয়েছে। ওষুধ আর বদলানো যাবে না; ফেরত পাঠালে আবার খসড়া হবে।', true, false, 2),
  ('SIGNED', 'Signed', 'স্বাক্ষরিত',
   'Carries the physician''s signature. Nothing in it can be changed by any path; a change is a correction.',
   'চিকিৎসকের স্বাক্ষর রয়েছে। এর কিছুই আর কোনোভাবে বদলানো যাবে না; বদল মানে সংশোধনী।', true, false, 3),
  ('PRINTED', 'Printed', 'ছাপা হয়েছে',
   'The paper is in the patient''s hand.',
   'কাগজটি রোগীর হাতে পৌঁছেছে।', true, false, 4),
  ('DISPENSED', 'Dispensed', 'ওষুধ দেওয়া হয়েছে',
   'The medicine has left the counter.',
   'কাউন্টার থেকে ওষুধ চলে গেছে।', true, false, 5),
  ('CANCELLED', 'Cancelled', 'বাতিল',
   'Withdrawn before the paper left the clinic. It stays in the record with its reason.',
   'কাগজ ক্লিনিক ছাড়ার আগেই প্রত্যাহার করা হয়েছে। কারণসহ নথিতে থেকে যাবে।', true, true, 6),
  ('CORRECTED', 'Corrected', 'সংশোধিত',
   'Superseded by a correction. This prescription stays exactly as it was written; the correction is a separate one.',
   'একটি সংশোধনী এটির জায়গা নিয়েছে। এটি যেমন লেখা হয়েছিল তেমনই থেকে যাবে; সংশোধনীটি আলাদা ব্যবস্থাপত্র।', true, true, 7);

GRANT SELECT ON core.prescription_status TO dthcms_app, dthcms_projector;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'prescription_status',
   'The states a prescription can be in are the same in every clinic. The prescriptions themselves are on read.prescription, which is scoped.')
ON CONFLICT (schema_name, table_name) DO UPDATE SET reason = EXCLUDED.reason;

-- ---------------------------------------------------------------------------
-- 2. The transition matrix, as rows
-- ---------------------------------------------------------------------------

-- Twelve edges. Everything not here is refused, which is thirty-seven of the forty-nine cells of
-- the matrix once the seven self-transitions are counted out.
--
-- Rows rather than a CASE in a trigger for two reasons. The matrix is then readable — a person
-- auditing the state machine runs one SELECT and sees all of it — and a new edge is a migration
-- somebody reviews rather than a branch somebody adds while fixing something else.
CREATE TABLE core.prescription_transition (
  from_status text NOT NULL REFERENCES core.prescription_status(status),
  to_status   text NOT NULL REFERENCES core.prescription_status(status),

  -- The ledger event that carries this transition. One event type per edge: a status that could
  -- be reached by two different events is a status whose history has two meanings.
  event_type text NOT NULL,

  -- Why this edge exists, for the person reading the matrix.
  note_en text NOT NULL,
  note_bn text NOT NULL,

  -- Which checkpoint owns the workflow behind it. CP80 built the machine; four of these edges
  -- are driven by screens that do not exist yet, and saying so here is more honest than a
  -- comment that goes stale.
  owned_by text NOT NULL,

  PRIMARY KEY (from_status, to_status),
  CONSTRAINT transition_is_not_a_loop CHECK (from_status <> to_status)
);

COMMENT ON TABLE core.prescription_transition IS
  'Every legal status change for a prescription (CP80). Anything not in this table is refused by a trigger.';

INSERT INTO core.prescription_transition
  (from_status, to_status, event_type, note_en, note_bn, owned_by) VALUES
  ('DRAFT', 'QA_REVIEW', 'PRESCRIPTION_SUBMITTED_FOR_QA',
   'The prescriber has finished writing and sends it for the quality check.',
   'চিকিৎসক লেখা শেষ করে মান-যাচাইয়ের জন্য পাঠালেন।', 'CP80'),
  ('DRAFT', 'CANCELLED', 'PRESCRIPTION_CANCELLED',
   'Abandoned before it was ever submitted.',
   'জমা দেওয়ার আগেই পরিত্যক্ত।', 'CP80'),

  ('QA_REVIEW', 'DRAFT', 'PRESCRIPTION_QA_BOUNCED',
   'The quality check found something and returned it to the prescriber. Items become editable again.',
   'মান-যাচাইয়ে সমস্যা ধরা পড়ায় চিকিৎসকের কাছে ফেরত। ওষুধ আবার বদলানো যাবে।', 'CP83'),
  ('QA_REVIEW', 'SIGNED', 'PRESCRIPTION_SIGNED',
   'Cleared, and the physician has signed it.',
   'ছাড়পত্র পেয়েছে এবং চিকিৎসক স্বাক্ষর করেছেন।', 'CP84'),
  ('QA_REVIEW', 'CANCELLED', 'PRESCRIPTION_CANCELLED',
   'Withdrawn while with QA.',
   'কিউএ-তে থাকা অবস্থাতেই প্রত্যাহার।', 'CP80'),

  ('SIGNED', 'PRINTED', 'PRESCRIPTION_PRINTED',
   'The paper was produced and handed over.',
   'কাগজ ছাপা হয়ে হস্তান্তর হয়েছে।', 'CP89'),
  ('SIGNED', 'DISPENSED', 'PRESCRIPTION_DISPENSED',
   'Dispensed without a printed sheet — the pharmacy queue reads it directly.',
   'ছাপা কাগজ ছাড়াই ওষুধ দেওয়া হয়েছে — ফার্মেসি সরাসরি পর্দায় দেখেছে।', 'CP118'),
  ('SIGNED', 'CANCELLED', 'PRESCRIPTION_CANCELLED',
   'Withdrawn after signing but before the paper left the clinic. This is the last status from which cancelling is possible.',
   'স্বাক্ষরের পর, কিন্তু কাগজ ক্লিনিক ছাড়ার আগে প্রত্যাহার। এর পরে আর বাতিল করা যাবে না।', 'CP80'),
  ('SIGNED', 'CORRECTED', 'PRESCRIPTION_CORRECTED',
   'Superseded by a correction.',
   'সংশোধনী এটির জায়গা নিয়েছে।', 'CP80'),

  ('PRINTED', 'DISPENSED', 'PRESCRIPTION_DISPENSED',
   'The pharmacy dispensed against the printed sheet.',
   'ছাপা কাগজ দেখে ফার্মেসি ওষুধ দিয়েছে।', 'CP118'),
  ('PRINTED', 'CORRECTED', 'PRESCRIPTION_CORRECTED',
   'Superseded by a correction. Cancelling is no longer possible: the paper is out of the clinic and the pharmacy may act on it, so the record has to show a replacement rather than an absence.',
   'সংশোধনী এটির জায়গা নিয়েছে। এখন আর বাতিল করা যাবে না: কাগজ ক্লিনিকের বাইরে চলে গেছে এবং ফার্মেসি তাতে কাজ করতে পারে, তাই নথিতে "নেই" নয়, "বদলে গেছে" দেখাতে হবে।', 'CP80'),

  ('DISPENSED', 'CORRECTED', 'PRESCRIPTION_CORRECTED',
   'Superseded after the medicine was dispensed. The correction records that fact on itself, so that anybody reading it knows medicine has already left the counter.',
   'ওষুধ দেওয়ার পরে সংশোধনী। সংশোধনীটি নিজের গায়েই লিখে রাখে যে ওষুধ ইতিমধ্যে কাউন্টার ছেড়ে গেছে।', 'CP80');

GRANT SELECT ON core.prescription_transition TO dthcms_app, dthcms_projector;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'prescription_transition',
   'The legal transitions of a prescription are the same in every clinic.')
ON CONFLICT (schema_name, table_name) DO UPDATE SET reason = EXCLUDED.reason;

-- ---------------------------------------------------------------------------
-- 3. The prescription
-- ---------------------------------------------------------------------------

CREATE TABLE read.prescription (
  id uuid PRIMARY KEY,
  facility_id uuid NOT NULL REFERENCES core.facility(id),
  patient_id  uuid NOT NULL,
  visit_id    uuid NOT NULL,

  status text NOT NULL REFERENCES core.prescription_status(status),

  -- Who wrote it. From the envelope, like every other attribution in this system: a client that
  -- could name the prescriber could put a colleague's name on a controlled drug.
  created_at   timestamptz NOT NULL,
  created_by   uuid NOT NULL,
  created_role text NOT NULL DEFAULT '',

  -- One stamp pair per status that can be entered. Nullable, because a prescription that was
  -- never signed has no signing instant, and a default would invent one.
  submitted_at timestamptz, submitted_by uuid,
  bounced_at   timestamptz, bounced_by   uuid,
  signed_at    timestamptz, signed_by    uuid,
  printed_at   timestamptz, printed_by   uuid,
  dispensed_at timestamptz, dispensed_by uuid,
  cancelled_at timestamptz, cancelled_by uuid,
  cancelled_reason text NOT NULL DEFAULT '',
  corrected_at timestamptz, corrected_by uuid,

  -- The correction link, both ways. `corrects_prescription_id` is set at INSERT and never
  -- changes; `corrected_by_prescription_id` is set on the original when the correction is
  -- created, and is the one exception to "nothing on a signed prescription changes".
  corrects_prescription_id     uuid REFERENCES read.prescription(id),
  correction_reason            text NOT NULL DEFAULT '',
  corrected_by_prescription_id uuid REFERENCES read.prescription(id),

  -- Set on the *correction*, at the moment it is created, when the prescription it corrects had
  -- already been dispensed. Recorded here rather than derived from the original's status,
  -- because the original's status changes to CORRECTED the same instant and the fact that
  -- medicine had already left the counter would otherwise be recoverable only from the ledger.
  corrects_dispensed_original boolean NOT NULL DEFAULT false,

  -- Where the items came from, when they were carried forward from a previous prescription.
  -- Nullable; a fresh prescription has none.
  carried_forward_from uuid REFERENCES read.prescription(id),

  created_event_id uuid NOT NULL UNIQUE,
  last_event_id    uuid NOT NULL,
  last_global_seq  bigint NOT NULL,
  updated_at       timestamptz NOT NULL DEFAULT now(),

  -- Every stamp is a person and an instant, or neither. Eight constraints rather than one,
  -- because a violated constraint should name the status it is about.
  CONSTRAINT prescription_submission_is_whole CHECK ((submitted_at IS NULL) = (submitted_by IS NULL)),
  CONSTRAINT prescription_bounce_is_whole     CHECK ((bounced_at   IS NULL) = (bounced_by   IS NULL)),
  CONSTRAINT prescription_signature_is_whole  CHECK ((signed_at    IS NULL) = (signed_by    IS NULL)),
  CONSTRAINT prescription_printing_is_whole   CHECK ((printed_at   IS NULL) = (printed_by   IS NULL)),
  CONSTRAINT prescription_dispensing_is_whole CHECK ((dispensed_at IS NULL) = (dispensed_by IS NULL)),
  CONSTRAINT prescription_correction_is_whole CHECK ((corrected_at IS NULL) = (corrected_by IS NULL)),
  -- A cancellation without a reason is a row nobody can account for later.
  CONSTRAINT prescription_cancellation_is_whole CHECK (
    (cancelled_at IS NULL) = (cancelled_by IS NULL)
    AND (cancelled_at IS NULL OR btrim(cancelled_reason) <> '')),

  -- The status and its stamp agree. A prescription in SIGNED with no signing instant is a row
  -- that says a physician signed it and cannot say when.
  CONSTRAINT prescription_status_has_its_stamp CHECK (
    (status <> 'QA_REVIEW' OR submitted_at IS NOT NULL)
    AND (status NOT IN ('SIGNED', 'PRINTED', 'DISPENSED') OR signed_at IS NOT NULL)
    AND (status <> 'PRINTED'   OR printed_at   IS NOT NULL)
    AND (status <> 'DISPENSED' OR dispensed_at IS NOT NULL)
    AND (status <> 'CANCELLED' OR cancelled_at IS NOT NULL)
    AND (status <> 'CORRECTED' OR corrected_at IS NOT NULL)),

  -- A correction says what it corrects and why, or it is not a correction.
  CONSTRAINT prescription_correction_names_its_original CHECK (
    (corrects_prescription_id IS NULL AND btrim(correction_reason) = ''
     AND NOT corrects_dispensed_original)
    OR (corrects_prescription_id IS NOT NULL AND btrim(correction_reason) <> '')),

  -- Nothing corrects itself.
  CONSTRAINT prescription_does_not_correct_itself CHECK (
    corrects_prescription_id IS DISTINCT FROM id
    AND corrected_by_prescription_id IS DISTINCT FROM id
    AND carried_forward_from IS DISTINCT FROM id),

  -- A prescription linked forward to a correction is in CORRECTED and nothing else.
  CONSTRAINT prescription_superseded_is_corrected CHECK (
    corrected_by_prescription_id IS NULL OR status = 'CORRECTED')
);

CREATE INDEX prescription_by_patient ON read.prescription (patient_id, created_at DESC);
CREATE INDEX prescription_by_visit   ON read.prescription (visit_id);
CREATE INDEX prescription_by_status  ON read.prescription (facility_id, status);
CREATE UNIQUE INDEX prescription_one_correction_per_original
  ON read.prescription (corrects_prescription_id) WHERE corrects_prescription_id IS NOT NULL;

COMMENT ON TABLE read.prescription IS
  'One prescription, projected from the ledger (CP80). The ledger is the truth; this is its current shape.';

-- **SELECT and nothing else for the application.** The whole checkpoint's security criterion is
-- this line plus the triggers below.
GRANT SELECT ON read.prescription TO dthcms_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON read.prescription TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- 4. The items
-- ---------------------------------------------------------------------------

CREATE TABLE read.prescription_item (
  id uuid PRIMARY KEY,
  prescription_id uuid NOT NULL REFERENCES read.prescription(id),
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  -- Where on the sheet. Reordering is a modification of this column, which is why it is here
  -- and not implied by insertion order.
  line_no int NOT NULL,

  product_id uuid REFERENCES core.medication_product(id),
  -- Copied from the product at the moment of prescribing, not joined. A trade name corrected
  -- next year must not change what this sheet said, and a product withdrawn from the formulary
  -- must not make a historical prescription unreadable.
  product_label text NOT NULL,
  generic_name  text NOT NULL DEFAULT '',
  strength      text NOT NULL DEFAULT '',
  form_code     text NOT NULL DEFAULT '',

  dose       text NOT NULL,
  -- The numeric daily dose, where it can be stated, so the max-dose rules have something to
  -- compare against. Null where the instruction is not reducible to a number ("as directed").
  daily_dose numeric(12,3),
  dose_unit  text NOT NULL DEFAULT '',
  frequency  text NOT NULL,
  duration_days int,
  route      text NOT NULL DEFAULT '',
  quantity   numeric(12,3),

  -- Bilingual, and both required. A Bengali instruction is what the patient reads.
  instructions_en text NOT NULL DEFAULT '',
  instructions_bn text NOT NULL DEFAULT '',

  -- ---- the price at the time of prescribing --------------------------
  --
  -- By value. `price_poisha` is the amount, and it came from the event payload rather than from
  -- a join, so a price change in March cannot rewrite what a February prescription cost — not
  -- even through a rebuild, because the number is in the ledger.
  --
  -- Null when the product had no price on the day. That is a real and common state (CP75:
  -- every product's price history starts somewhere) and it is emphatically not zero, which
  -- would tell a patient a medicine is free.
  price_poisha bigint,
  price_id uuid,
  price_effective_from date,
  price_verification text,
  -- The instant the price was read. Not the same as the item's own recorded_at in principle,
  -- and keeping them separate is what makes "which day's price list was this" answerable.
  price_captured_at timestamptz,

  -- Where this line came from, when it was carried forward.
  carried_forward_from_item uuid,

  recorded_at timestamptz NOT NULL,
  recorded_by uuid NOT NULL,
  modified_at timestamptz, modified_by uuid,
  -- Soft. The row stays, because "what was on this sheet at 14:05" must stay answerable after
  -- the item came off it at 14:06.
  removed_at timestamptz, removed_by uuid,
  removed_reason text NOT NULL DEFAULT '',

  event_id uuid NOT NULL UNIQUE,
  global_seq bigint NOT NULL,

  CONSTRAINT prescription_item_line_is_positive CHECK (line_no > 0),
  CONSTRAINT prescription_item_says_what_it_is CHECK (btrim(product_label) <> ''),
  CONSTRAINT prescription_item_has_a_dose CHECK (btrim(dose) <> '' AND btrim(frequency) <> ''),
  CONSTRAINT prescription_item_duration_is_sane CHECK (duration_days IS NULL OR duration_days BETWEEN 1 AND 3650),
  CONSTRAINT prescription_item_quantity_is_positive CHECK (quantity IS NULL OR quantity > 0),
  CONSTRAINT prescription_item_daily_dose_is_positive CHECK (daily_dose IS NULL OR daily_dose > 0),
  -- A dose with a number needs a unit for that number, or a max-dose rule cannot compare it and
  -- must answer "cannot verify" — which is correct behaviour for data that should not exist.
  CONSTRAINT prescription_item_numeric_dose_has_a_unit CHECK (
    daily_dose IS NULL OR btrim(dose_unit) <> ''),
  -- A price is the amount, the row it came from, the day it took effect and when it was read,
  -- or it is none of those. A partially captured price is a number nobody can trace.
  CONSTRAINT prescription_item_price_is_whole CHECK (
    (price_poisha IS NULL AND price_id IS NULL AND price_effective_from IS NULL
     AND price_verification IS NULL AND price_captured_at IS NULL)
    OR (price_poisha IS NOT NULL AND price_id IS NOT NULL AND price_effective_from IS NOT NULL
        AND price_verification IS NOT NULL AND price_captured_at IS NOT NULL)),
  CONSTRAINT prescription_item_price_is_positive CHECK (price_poisha IS NULL OR price_poisha > 0),
  CONSTRAINT prescription_item_removal_is_whole CHECK (
    (removed_at IS NULL) = (removed_by IS NULL)),
  CONSTRAINT prescription_item_modification_is_whole CHECK (
    (modified_at IS NULL) = (modified_by IS NULL)),
  -- Bengali is not optional on a line the patient is handed.
  CONSTRAINT prescription_item_instructions_are_bilingual CHECK (
    (btrim(instructions_en) = '') = (btrim(instructions_bn) = ''))
);

CREATE INDEX prescription_item_by_prescription
  ON read.prescription_item (prescription_id, line_no) WHERE removed_at IS NULL;
CREATE INDEX prescription_item_all_lines
  ON read.prescription_item (prescription_id, line_no);

COMMENT ON TABLE read.prescription_item IS
  'One line of a prescription, with the price as it was on the day it was written (CP80).';

GRANT SELECT ON read.prescription_item TO dthcms_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON read.prescription_item TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- 5. The triggers that make "nobody edits a signed prescription" a fact
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
-- Every column that is not the status, its stamps, the correction link or bookkeeping is
-- immutable from INSERT — in every status, not only once signed.
--
-- Written as an explicit list of what may change rather than a list of what may not, because the
-- failure mode of the second shape is a column added next year that nobody remembers to forbid.
-- A column added to this table is immutable by default, which is the right default for this
-- object.
CREATE OR REPLACE FUNCTION core.prescription_is_frozen() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    -- The rebuild's escape hatch, and it is deliberately narrow. See the migration header:
    -- what this protects is the projection, and the ledger behind it cannot be rewritten at
    -- all. A rebuild that could not empty the table would make the read model unreconstructable,
    -- which would cost more than it bought.
    IF coalesce(current_setting('dthcms.rebuilding', true), '') = 'on' THEN
      RETURN OLD;
    END IF;
    RAISE EXCEPTION 'a prescription is never deleted (id %)', OLD.id
      USING HINT = 'Cancel it, or correct it. Both keep the record.';
  END IF;

  IF NEW.id <> OLD.id
     OR NEW.facility_id <> OLD.facility_id
     OR NEW.patient_id <> OLD.patient_id
     OR NEW.visit_id <> OLD.visit_id
     OR NEW.created_at <> OLD.created_at
     OR NEW.created_by <> OLD.created_by
     OR NEW.created_role <> OLD.created_role
     OR NEW.created_event_id <> OLD.created_event_id
     OR NEW.corrects_prescription_id IS DISTINCT FROM OLD.corrects_prescription_id
     OR NEW.correction_reason <> OLD.correction_reason
     OR NEW.corrects_dispensed_original <> OLD.corrects_dispensed_original
     OR NEW.carried_forward_from IS DISTINCT FROM OLD.carried_forward_from THEN
    RAISE EXCEPTION 'prescription % is written once: who it is for, who wrote it, and what it corrects cannot change', OLD.id
      USING HINT = 'The ledger is the truth. A change to a prescription is a correction that supersedes it.';
  END IF;

  -- A stamp, once set, is set. A signing instant that could be moved is a signing instant that
  -- proves nothing.
  IF (OLD.submitted_at IS NOT NULL AND NEW.submitted_at IS DISTINCT FROM OLD.submitted_at)
     OR (OLD.signed_at    IS NOT NULL AND NEW.signed_at    IS DISTINCT FROM OLD.signed_at)
     OR (OLD.signed_by    IS NOT NULL AND NEW.signed_by    IS DISTINCT FROM OLD.signed_by)
     OR (OLD.printed_at   IS NOT NULL AND NEW.printed_at   IS DISTINCT FROM OLD.printed_at)
     OR (OLD.dispensed_at IS NOT NULL AND NEW.dispensed_at IS DISTINCT FROM OLD.dispensed_at)
     OR (OLD.cancelled_at IS NOT NULL AND NEW.cancelled_at IS DISTINCT FROM OLD.cancelled_at)
     OR (OLD.corrected_at IS NOT NULL AND NEW.corrected_at IS DISTINCT FROM OLD.corrected_at)
     OR (OLD.corrected_by_prescription_id IS NOT NULL
         AND NEW.corrected_by_prescription_id IS DISTINCT FROM OLD.corrected_by_prescription_id) THEN
    RAISE EXCEPTION 'prescription %: a stamp that has been set cannot be changed', OLD.id;
  END IF;

  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER prescription_frozen
  BEFORE UPDATE OR DELETE ON read.prescription
  FOR EACH ROW EXECUTE FUNCTION core.prescription_is_frozen();

-- +goose StatementBegin
-- A status change must be an edge in core.prescription_transition.
CREATE OR REPLACE FUNCTION core.prescription_transition_is_legal() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = OLD.status THEN
    RETURN NEW;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM core.prescription_transition
                  WHERE from_status = OLD.status AND to_status = NEW.status) THEN
    RAISE EXCEPTION 'a prescription cannot go from % to % (id %)', OLD.status, NEW.status, OLD.id
      USING HINT = 'The legal transitions are the rows of core.prescription_transition.';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER prescription_transitions_legally
  BEFORE UPDATE ON read.prescription
  FOR EACH ROW EXECUTE FUNCTION core.prescription_transition_is_legal();

-- +goose StatementBegin
-- An item may be written only while its prescription is a draft, and its price never changes.
--
-- **DRAFT and not "not signed".** A prescription in QA_REVIEW whose items changed underneath the
-- reviewer is a prescription that was reviewed and then altered, which is the same defect as
-- editing a signed one, one step earlier.
CREATE OR REPLACE FUNCTION core.prescription_item_is_frozen() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  parent read.prescription%ROWTYPE;
  target uuid;
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF coalesce(current_setting('dthcms.rebuilding', true), '') = 'on' THEN
      RETURN OLD;
    END IF;
    RAISE EXCEPTION 'a prescription item is never deleted (id %)', OLD.id
      USING HINT = 'Remove it — the row stays, with who removed it and why.';
  END IF;

  target := CASE WHEN TG_OP = 'INSERT' THEN NEW.prescription_id ELSE OLD.prescription_id END;
  SELECT * INTO parent FROM read.prescription WHERE id = target;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no prescription % to put an item on', target;
  END IF;

  IF parent.status <> 'DRAFT' THEN
    RAISE EXCEPTION 'prescription % is %, so its items cannot be changed', parent.id, parent.status
      USING HINT = 'Only a draft can be edited. A change to anything else is a correction that supersedes it.';
  END IF;

  IF TG_OP = 'UPDATE' THEN
    IF NEW.id <> OLD.id OR NEW.prescription_id <> OLD.prescription_id
       OR NEW.facility_id <> OLD.facility_id
       OR NEW.recorded_at <> OLD.recorded_at OR NEW.recorded_by <> OLD.recorded_by THEN
      RAISE EXCEPTION 'prescription item %: identity and authorship are written once', OLD.id;
    END IF;
    -- **Criterion 3.** The price is what it was on the day this line was written, and no path
    -- through this table can move it.
    IF NEW.price_poisha IS DISTINCT FROM OLD.price_poisha
       OR NEW.price_id IS DISTINCT FROM OLD.price_id
       OR NEW.price_effective_from IS DISTINCT FROM OLD.price_effective_from
       OR NEW.price_captured_at IS DISTINCT FROM OLD.price_captured_at THEN
      RAISE EXCEPTION 'prescription item %: the price at the time of prescribing is never back-updated', OLD.id
        USING HINT = 'CP75 supersedes a price rather than editing it; CP80 captures the amount by value so history cannot move.';
    END IF;
  END IF;

  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER prescription_item_frozen
  BEFORE INSERT OR UPDATE OR DELETE ON read.prescription_item
  FOR EACH ROW EXECUTE FUNCTION core.prescription_item_is_frozen();

-- +goose StatementBegin
-- TRUNCATE bypasses row triggers entirely, which would make every guarantee above a guarantee
-- against one statement nobody would think to use.
CREATE OR REPLACE FUNCTION core.prescription_refuses_truncate() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'prescriptions are not truncated'
    USING HINT = 'A rebuild deletes under SET LOCAL dthcms.rebuilding = ''on''.';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER prescription_no_truncate
  BEFORE TRUNCATE ON read.prescription
  FOR EACH STATEMENT EXECUTE FUNCTION core.prescription_refuses_truncate();
CREATE TRIGGER prescription_item_no_truncate
  BEFORE TRUNCATE ON read.prescription_item
  FOR EACH STATEMENT EXECUTE FUNCTION core.prescription_refuses_truncate();

REVOKE TRUNCATE ON read.prescription, read.prescription_item FROM dthcms_projector;

-- ---------------------------------------------------------------------------
-- 6. The projections
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_prescription_created(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.prescription (
    id, facility_id, patient_id, visit_id, status,
    created_at, created_by, created_role,
    corrects_prescription_id, correction_reason, corrects_dispensed_original,
    carried_forward_from,
    created_event_id, last_event_id, last_global_seq, updated_at)
  VALUES (
    (p->>'prescription_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'patient_id')::uuid,
    (p->>'visit_id')::uuid,
    'DRAFT',
    (p->>'created_at')::timestamptz,
    (p->>'created_by')::uuid,
    coalesce(p->>'created_role', ''),
    nullif(p->>'corrects_prescription_id', '')::uuid,
    coalesce(p->>'correction_reason', ''),
    coalesce((p->>'corrects_dispensed_original')::boolean, false),
    nullif(p->>'carried_forward_from', '')::uuid,
    (p->>'event_id')::uuid,
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint,
    (p->>'created_at')::timestamptz)
  ON CONFLICT (id) DO NOTHING;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_prescription_item_added(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  INSERT INTO read.prescription_item (
    id, prescription_id, facility_id, line_no,
    product_id, product_label, generic_name, strength, form_code,
    dose, daily_dose, dose_unit, frequency, duration_days, route, quantity,
    instructions_en, instructions_bn,
    price_poisha, price_id, price_effective_from, price_verification, price_captured_at,
    carried_forward_from_item,
    recorded_at, recorded_by, event_id, global_seq)
  VALUES (
    (p->>'item_id')::uuid,
    (p->>'prescription_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'line_no')::int,
    nullif(p->>'product_id', '')::uuid,
    p->>'product_label',
    coalesce(p->>'generic_name', ''),
    coalesce(p->>'strength', ''),
    coalesce(p->>'form_code', ''),
    p->>'dose',
    nullif(p->>'daily_dose', '')::numeric,
    coalesce(p->>'dose_unit', ''),
    p->>'frequency',
    nullif(p->>'duration_days', '')::int,
    coalesce(p->>'route', ''),
    nullif(p->>'quantity', '')::numeric,
    coalesce(p->>'instructions_en', ''),
    coalesce(p->>'instructions_bn', ''),
    -- The price arrives as a number in the payload. Nothing here reads core.medication_price,
    -- which is what makes a rebuild reproduce the price of the day rather than today's.
    nullif(p->>'price_poisha', '')::bigint,
    nullif(p->>'price_id', '')::uuid,
    nullif(p->>'price_effective_from', '')::date,
    nullif(p->>'price_verification', ''),
    nullif(p->>'price_captured_at', '')::timestamptz,
    nullif(p->>'carried_forward_from_item', '')::uuid,
    (p->>'recorded_at')::timestamptz,
    (p->>'recorded_by')::uuid,
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (id) DO NOTHING;

  UPDATE read.prescription
     SET last_event_id = (p->>'event_id')::uuid,
         last_global_seq = greatest(last_global_seq, (p->>'global_seq')::bigint),
         updated_at = (p->>'recorded_at')::timestamptz
   WHERE id = (p->>'prescription_id')::uuid;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_prescription_item_modified(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  -- Every clinical field of the line, and not one price column. A modification is a change to
  -- how the medicine is taken; re-pricing it would be a change to what it cost on a day that
  -- has already passed.
  UPDATE read.prescription_item
     SET line_no         = (p->>'line_no')::int,
         dose            = p->>'dose',
         daily_dose      = nullif(p->>'daily_dose', '')::numeric,
         dose_unit       = coalesce(p->>'dose_unit', ''),
         frequency       = p->>'frequency',
         duration_days   = nullif(p->>'duration_days', '')::int,
         route           = coalesce(p->>'route', ''),
         quantity        = nullif(p->>'quantity', '')::numeric,
         instructions_en = coalesce(p->>'instructions_en', ''),
         instructions_bn = coalesce(p->>'instructions_bn', ''),
         modified_at     = (p->>'modified_at')::timestamptz,
         modified_by     = (p->>'modified_by')::uuid,
         global_seq      = greatest(global_seq, (p->>'global_seq')::bigint)
   WHERE id = (p->>'item_id')::uuid
     AND removed_at IS NULL;

  UPDATE read.prescription
     SET last_event_id = (p->>'event_id')::uuid,
         last_global_seq = greatest(last_global_seq, (p->>'global_seq')::bigint),
         updated_at = (p->>'modified_at')::timestamptz
   WHERE id = (p->>'prescription_id')::uuid;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION read.apply_prescription_item_removed(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  UPDATE read.prescription_item
     SET removed_at = (p->>'removed_at')::timestamptz,
         removed_by = (p->>'removed_by')::uuid,
         removed_reason = coalesce(p->>'reason', ''),
         global_seq = greatest(global_seq, (p->>'global_seq')::bigint)
   WHERE id = (p->>'item_id')::uuid
     AND removed_at IS NULL;

  UPDATE read.prescription
     SET last_event_id = (p->>'event_id')::uuid,
         last_global_seq = greatest(last_global_seq, (p->>'global_seq')::bigint),
         updated_at = (p->>'removed_at')::timestamptz
   WHERE id = (p->>'prescription_id')::uuid;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- One function for all nine transitions, because there is one rule and it lives in a table.
--
-- Idempotent and order-tolerant, like every projection here: it does nothing when the row is
-- already in the target status, so a replayed event and a rebuild land on the same row. The
-- transition trigger still has the final word, which is what makes an out-of-order replay fail
-- loudly rather than write a state the machine does not allow.
CREATE OR REPLACE FUNCTION read.apply_prescription_transition(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
DECLARE
  target text := p->>'to_status';
  who uuid := (p->>'actor_id')::uuid;
  at timestamptz := (p->>'at')::timestamptz;
BEGIN
  UPDATE read.prescription
     SET status = target,
         submitted_at = CASE WHEN target = 'QA_REVIEW' THEN at ELSE submitted_at END,
         submitted_by = CASE WHEN target = 'QA_REVIEW' THEN who ELSE submitted_by END,
         bounced_at   = CASE WHEN target = 'DRAFT'     THEN at ELSE bounced_at END,
         bounced_by   = CASE WHEN target = 'DRAFT'     THEN who ELSE bounced_by END,
         signed_at    = CASE WHEN target = 'SIGNED'    THEN at ELSE signed_at END,
         signed_by    = CASE WHEN target = 'SIGNED'    THEN who ELSE signed_by END,
         printed_at   = CASE WHEN target = 'PRINTED'   THEN at ELSE printed_at END,
         printed_by   = CASE WHEN target = 'PRINTED'   THEN who ELSE printed_by END,
         dispensed_at = CASE WHEN target = 'DISPENSED' THEN at ELSE dispensed_at END,
         dispensed_by = CASE WHEN target = 'DISPENSED' THEN who ELSE dispensed_by END,
         cancelled_at = CASE WHEN target = 'CANCELLED' THEN at ELSE cancelled_at END,
         cancelled_by = CASE WHEN target = 'CANCELLED' THEN who ELSE cancelled_by END,
         cancelled_reason = CASE WHEN target = 'CANCELLED'
                                 THEN coalesce(p->>'reason', '') ELSE cancelled_reason END,
         corrected_at = CASE WHEN target = 'CORRECTED' THEN at ELSE corrected_at END,
         corrected_by = CASE WHEN target = 'CORRECTED' THEN who ELSE corrected_by END,
         corrected_by_prescription_id = CASE WHEN target = 'CORRECTED'
                                             THEN nullif(p->>'correction_id', '')::uuid
                                             ELSE corrected_by_prescription_id END,
         last_event_id = (p->>'event_id')::uuid,
         last_global_seq = greatest(last_global_seq, (p->>'global_seq')::bigint),
         updated_at = at
   WHERE id = (p->>'prescription_id')::uuid
     AND status <> target;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_prescription_created(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_prescription_item_added(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_prescription_item_modified(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_prescription_item_removed(jsonb) TO dthcms_projector;
GRANT EXECUTE ON FUNCTION read.apply_prescription_transition(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- 7. Permissions
-- ---------------------------------------------------------------------------

-- **No new permission.** CP15's catalogue already holds `prescription.draft`,
-- `prescription.sign`, `prescription.read` and `prescription.dispense`, granted per §4.4's
-- access matrix, and `core.assert_access_matrix_holds()` (invariant 43) enforces that matrix on
-- every start. This checkpoint uses them:
--
--   * **`prescription.draft`** — create, add, modify, remove, submit, cancel, correct.
--     PHYSICIAN and JUNIOR_DOCTOR hold it, which is §4.4's "only prescribers create".
--   * **`prescription.read`** — read a prescription and its items. PHYSICIAN, JUNIOR_DOCTOR,
--     PHARMACIST, QA and RX_EDUCATOR hold it.
--   * `prescription.sign` and `prescription.dispense` belong to CP84 and CP118. The edges they
--     drive are in the matrix above; the routes are theirs to mount.
--
-- The first draft of this migration added `prescription.write` as a sensitive permission and
-- granted it to the prescribers. That was wrong twice, and the database said so: it duplicated
-- `prescription.draft`, and the `ON CONFLICT` clause beside it flipped `prescription.read` to
-- sensitive, which broke invariant 43 — *"diagnoses are not hidden from the pharmacist"* — on
-- the next verify. The assertion caught a permission change nobody meant to make, which is
-- exactly what it is for. The stray `prescription.write` rows it created are removed here.
DELETE FROM core.role_permission WHERE permission_code = 'prescription.write';
DELETE FROM core.permission WHERE code = 'prescription.write';

-- **The pharmacist and this read model.** PHARMACIST holds `prescription.read`, and §4.4 says a
-- pharmacist sees the drug list and dosing with diagnoses hidden. That holds here by
-- construction rather than by filtering: **there is no diagnosis column on either table**, and a
-- prescription in this model cannot carry one. CP118 builds the pharmacy queue and its own
-- redaction golden test; this note records why the grant is not already a leak.

-- ---------------------------------------------------------------------------
-- 8. Invariants
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_prescription_is_in_an_unreachable_state() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  -- A signed prescription names a signer. Belt and braces with the CHECK, because the CHECK is
  -- about the row and this is about the table after a migration that added rows.
  SELECT count(*) INTO offenders FROM read.prescription
   WHERE status IN ('SIGNED', 'PRINTED', 'DISPENSED') AND (signed_at IS NULL OR signed_by IS NULL);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% signed prescriptions do not name who signed them or when', offenders;
  END IF;

  -- Every status a prescription is in is one the machine can reach. A status with no incoming
  -- edge and no row starting in it is a state somebody wrote by hand.
  SELECT count(*) INTO offenders FROM read.prescription p
   WHERE p.status <> 'DRAFT'
     AND NOT EXISTS (SELECT 1 FROM core.prescription_transition t WHERE t.to_status = p.status);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% prescriptions are in a status no transition reaches', offenders;
  END IF;

  -- A correction and its original point at each other, both ways, or neither way.
  SELECT count(*) INTO offenders
    FROM read.prescription c
   WHERE c.corrects_prescription_id IS NOT NULL
     AND NOT EXISTS (SELECT 1 FROM read.prescription o
                      WHERE o.id = c.corrects_prescription_id
                        AND o.corrected_by_prescription_id = c.id
                        AND o.status = 'CORRECTED');
  IF offenders > 0 THEN
    RAISE EXCEPTION '% corrections are not linked back from the prescription they correct', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_no_prescription_is_in_an_unreachable_state() IS
  'CP80: every prescription is in a state the machine can reach, every signed one names its signer, and every correction is linked both ways.';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_prescriptions_cannot_be_rewritten() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  -- The grant. This is the line that stops the application editing a signed prescription, and a
  -- future migration that widened it would otherwise pass every test in the suite.
  SELECT count(*) INTO offenders
    FROM information_schema.role_table_grants
   WHERE grantee = 'dthcms_app'
     AND table_schema = 'read'
     AND table_name IN ('prescription', 'prescription_item')
     AND privilege_type IN ('INSERT', 'UPDATE', 'DELETE', 'TRUNCATE');
  IF offenders > 0 THEN
    RAISE EXCEPTION 'the application role holds % write privileges on the prescription read model', offenders
      USING HINT = 'Prescriptions are derived from the ledger. SELECT is the only grant dthcms_app may hold.';
  END IF;

  -- The triggers. A trigger dropped by a later migration would silently remove the guarantee.
  SELECT count(*) INTO offenders
    FROM (VALUES ('prescription_frozen'), ('prescription_transitions_legally'),
                 ('prescription_item_frozen'), ('prescription_no_truncate'),
                 ('prescription_item_no_truncate')) AS wanted(name)
   WHERE NOT EXISTS (SELECT 1 FROM pg_trigger t WHERE t.tgname = wanted.name AND NOT t.tgisinternal);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% of the triggers that freeze a signed prescription are missing', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_prescriptions_cannot_be_rewritten() IS
  'CP80: the application role cannot write a prescription row, and the freezing triggers are all present.';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_no_prescription_is_in_an_unreachable_state',
   'every prescription is in a state the machine can reach, every signed one names its signer, and every correction is linked both ways', 122),
  ('assert_prescriptions_cannot_be_rewritten',
   'the application cannot write a prescription row, and every trigger that freezes a signed one is present', 123)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_no_prescription_is_in_an_unreachable_state',
  'assert_prescriptions_cannot_be_rewritten');
DROP FUNCTION IF EXISTS core.assert_prescriptions_cannot_be_rewritten();
DROP FUNCTION IF EXISTS core.assert_no_prescription_is_in_an_unreachable_state();

DROP FUNCTION IF EXISTS read.apply_prescription_transition(jsonb);
DROP FUNCTION IF EXISTS read.apply_prescription_item_removed(jsonb);
DROP FUNCTION IF EXISTS read.apply_prescription_item_modified(jsonb);
DROP FUNCTION IF EXISTS read.apply_prescription_item_added(jsonb);
DROP FUNCTION IF EXISTS read.apply_prescription_created(jsonb);

DROP TRIGGER IF EXISTS prescription_item_no_truncate ON read.prescription_item;
DROP TRIGGER IF EXISTS prescription_no_truncate ON read.prescription;
DROP FUNCTION IF EXISTS core.prescription_refuses_truncate();
DROP TRIGGER IF EXISTS prescription_item_frozen ON read.prescription_item;
DROP FUNCTION IF EXISTS core.prescription_item_is_frozen();
DROP TRIGGER IF EXISTS prescription_transitions_legally ON read.prescription;
DROP FUNCTION IF EXISTS core.prescription_transition_is_legal();
DROP TRIGGER IF EXISTS prescription_frozen ON read.prescription;
DROP FUNCTION IF EXISTS core.prescription_is_frozen();

DROP TABLE IF EXISTS read.prescription_item;
DROP TABLE IF EXISTS read.prescription;

DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name IN ('prescription_status', 'prescription_transition');
DROP TABLE IF EXISTS core.prescription_transition;
DROP TABLE IF EXISTS core.prescription_status;
