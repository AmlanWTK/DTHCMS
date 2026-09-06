-- Counseling templates, authored by a physician rather than by a release (CP55, §5.1, [R-07]).
--
-- # Why versioning is the whole checkpoint
--
-- §5.1 asks that templates be physician-configurable "without code changes". That is the easy
-- half. The hard half is acceptance criterion 2: **completed sessions retain the template
-- version used.**
--
-- Without it, editing the diabetes template next March silently rewrites what every counsellor
-- was asked to cover last October — and a record that says "all seven items ticked" would then
-- be a claim about a checklist that did not exist at the time. Six months of counselling audit
-- would quietly become unreadable, and nobody would notice, because nothing would look wrong.
--
-- So a version is **frozen the moment it is published**. Its items cannot be edited, added to
-- or removed, and that is a trigger rather than a convention: the edit that breaks this is a
-- well-meaning `UPDATE` fixing a typo, at which point every session that ever referenced it is
-- describing something else.
--
-- Editing a published template means drafting a new version. That is more work for the author
-- and it is the right trade: a typo fixed in a new version is honest, and a typo fixed in place
-- is a small lie told to every past session.
--
-- # Why the diagnosis link is a coding
--
-- CP52 exists, so "the diabetes template" is not a string: it is a rule matching a concept in a
-- named terminology at a named version. A template keyed on the word "diabetes" would miss
-- E11.65 and match a complaint of "diabetes insipidus", and the mistake would show up as a
-- counsellor asked the wrong seven questions.
--
-- # What is not here
--
-- The content. **D-53 is open**: the seven diabetes items below are transcribed from §5.1 and
-- are the launch minimum, not a clinical author's list. Their Bengali is mine. `approved_at` is
-- null on the seeded version and the API reports it, so nothing presents them as settled.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------

-- Reading a template is not clinical: it is the list of things a counsellor is about to be
-- asked to cover, and every station that touches counselling needs it. Publishing is a
-- physician's act and needs a step-up as well, because a published version is immediately what
-- every phone on the floor starts asking patients.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('counseling.template.read', 'counseling', 'template', 'read',
   'Read counselling templates and their items', false),
  ('counseling.template.publish', 'counseling', 'template', 'publish',
   'Publish a counselling template version, making it live on the floor', false)
ON CONFLICT (code) DO UPDATE SET
  resource = EXCLUDED.resource, action = EXCLUDED.action, scope = EXCLUDED.scope,
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'counseling.template.read' FROM core.role r
 WHERE r.code IN ('COUNSELOR', 'NUTRITIONIST', 'EXERCISE', 'RX_EDUCATOR',
                  'CLINICAL_ASSISTANT', 'JUNIOR_DOCTOR', 'PHYSICIAN', 'QA', 'ADMIN')
ON CONFLICT DO NOTHING;

-- Authoring and publishing are the same permission deliberately: a draft nobody may publish is
-- a draft nobody will write, and splitting them would produce an approval queue this clinic has
-- no one to staff.
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'counseling.template.publish' FROM core.role r
 WHERE r.code IN ('PHYSICIAN')
ON CONFLICT DO NOTHING;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'counseling.template.write' FROM core.role r
 WHERE r.code IN ('PHYSICIAN')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The rooms counselling walks through
-- ---------------------------------------------------------------------------

-- §5.2: Counseling Room, then Nutrition Room, then Insulin Corner, "sequence configurable".
-- Configurable means rows, and the sequence lives here rather than in the item ordering so that
-- moving the insulin corner does not mean re-authoring every template that mentions it.
CREATE TABLE core.counseling_room (
  room text PRIMARY KEY,

  display_en text NOT NULL,
  display_bn text NOT NULL,

  -- Which station's queue this room belongs to, where it has one. The insulin corner is part of
  -- the counselling station rather than a station of its own, and a room that claimed to be a
  -- station would appear on the traffic board as a queue nobody is called to.
  station_code text NOT NULL DEFAULT '',

  ordering int NOT NULL,

  CONSTRAINT counseling_room_format CHECK (room ~ '^[A-Z][A-Z_]{1,31}$')
);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'counseling_room', 'The rooms counselling walks through. One clinic today; a catalogue either way.')
ON CONFLICT DO NOTHING;

INSERT INTO core.counseling_room (room, display_en, display_bn, station_code, ordering) VALUES
  ('COUNSELING_ROOM', 'Counseling room', 'কাউন্সেলিং রুম', 'STN_COUNSELING', 1),
  ('NUTRITION_ROOM',  'Nutrition room',  'পুষ্টি রুম',      'STN_NUTRITION',  2),
  ('INSULIN_CORNER',  'Insulin corner',  'ইনসুলিন কর্নার',  'STN_COUNSELING', 3)
ON CONFLICT (room) DO UPDATE SET
  display_en = EXCLUDED.display_en, display_bn = EXCLUDED.display_bn,
  station_code = EXCLUDED.station_code, ordering = EXCLUDED.ordering;

GRANT SELECT ON core.counseling_room TO dthcms_app, dthcms_projector;

-- ---------------------------------------------------------------------------
-- The template, and its versions
-- ---------------------------------------------------------------------------

CREATE TABLE core.counseling_template (
  id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code text NOT NULL UNIQUE,

  title_en text NOT NULL,
  title_bn text NOT NULL,

  -- Retired rather than deleted, like everything else here: a template a session referenced
  -- must stay readable forever, and a clinic that stopped using a checklist did not stop having
  -- used it.
  retired_at timestamptz,

  created_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid,

  CONSTRAINT counseling_template_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{1,47}$')
);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'counseling_template', 'A clinical checklist. Shared across facilities by design.')
ON CONFLICT DO NOTHING;

CREATE TABLE core.counseling_template_version (
  template_id uuid NOT NULL REFERENCES core.counseling_template(id),
  version     int  NOT NULL CHECK (version >= 1),

  -- DRAFT     → editable, invisible to the floor
  -- PUBLISHED → frozen, and what a new session gets
  -- RETIRED   → was published, is no longer offered, still readable by sessions that used it
  status text NOT NULL DEFAULT 'DRAFT'
    CHECK (status IN ('DRAFT', 'PUBLISHED', 'RETIRED')),

  notes text NOT NULL DEFAULT '',

  created_at   timestamptz NOT NULL DEFAULT now(),
  created_by   uuid,
  published_at timestamptz,
  published_by uuid,

  -- Who did the publishing, in the sense that matters: a person, or a migration.
  --
  -- The seeded diabetes template is published by this migration, because the alternative is a
  -- clinic whose first counselling session cannot start until somebody remembers to press a
  -- button. Its `published_by` is therefore null -- and rather than relax the rule that a
  -- publication names its publisher, the row says plainly that no person published it. An
  -- invented user id here would be the only attribution in the system naming somebody who did
  -- not do the thing.
  published_source text NOT NULL DEFAULT 'USER'
    CHECK (published_source IN ('USER', 'MIGRATION')),

  retired_at   timestamptz,

  -- D-53. The seeded diabetes version is transcribed from §5.1 and is a launch minimum, not a
  -- clinical author's list; its Bengali is the engineer's. Reported by the API so nothing
  -- presents it as settled.
  approved_at timestamptz,
  approved_by uuid,

  PRIMARY KEY (template_id, version),

  -- A publication by a person names them. One by a migration says so instead.
  CONSTRAINT counseling_version_publication_is_attributed
    CHECK (published_at IS NULL
           OR published_source = 'MIGRATION'
           OR published_by IS NOT NULL),
  CONSTRAINT counseling_version_published_when_it_says_so
    CHECK ((status = 'DRAFT') = (published_at IS NULL))
);

-- One live version per template. Two would make "which checklist is a new session given" a
-- question with two answers, and the wrong one would be whichever the query happened to sort
-- first.
CREATE UNIQUE INDEX counseling_one_published_version
  ON core.counseling_template_version (template_id) WHERE status = 'PUBLISHED';

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'counseling_template_version', 'Versions of a shared clinical checklist.')
ON CONFLICT DO NOTHING;

CREATE TABLE core.counseling_item (
  template_id uuid NOT NULL,
  version     int  NOT NULL,
  item_code   text NOT NULL,

  ordering int NOT NULL,

  text_en text NOT NULL,
  text_bn text NOT NULL,

  -- What the counsellor should actually say, where the item needs it. Optional, and the reason
  -- it exists is §5.4: the physician spot-questions the patient afterwards, and two counsellors
  -- who covered "injection sites" differently make that check useless.
  guidance_en text NOT NULL DEFAULT '',
  guidance_bn text NOT NULL DEFAULT '',

  -- §5.5's gate reads this column. Which items are mandatory is a clinical decision and is
  -- open; what is not open is that the gate reads a column rather than a hardcoded list.
  is_mandatory boolean NOT NULL DEFAULT false,

  room text NOT NULL REFERENCES core.counseling_room(room),

  PRIMARY KEY (template_id, version, item_code),
  FOREIGN KEY (template_id, version)
    REFERENCES core.counseling_template_version(template_id, version) ON DELETE CASCADE,

  CONSTRAINT counseling_item_code_format CHECK (item_code ~ '^[A-Z][A-Z0-9_]{1,47}$'),
  CONSTRAINT counseling_item_says_something CHECK (btrim(text_en) <> '')
);

CREATE INDEX counseling_item_in_order
  ON core.counseling_item (template_id, version, ordering);

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'counseling_item', 'The items of a shared clinical checklist.')
ON CONFLICT DO NOTHING;

GRANT SELECT ON core.counseling_template, core.counseling_template_version,
                core.counseling_item TO dthcms_app, dthcms_projector;
GRANT INSERT, UPDATE ON core.counseling_template, core.counseling_template_version,
                        core.counseling_item TO dthcms_app;
GRANT DELETE ON core.counseling_item TO dthcms_app;

-- ---------------------------------------------------------------------------
-- Which template a patient gets
-- ---------------------------------------------------------------------------

-- Keyed on a CP52 coding rather than on a word. A rule saying "diabetes" would miss E11.65 and
-- match a complaint of diabetes insipidus, and the mistake would surface as a counsellor asked
-- the wrong seven questions.
--
-- `prefix` is why this is not a plain foreign key to one concept: ICD-10 groups a family under
-- E11, and a rule per member would be sixteen rows that drift apart. A prefix rule says what a
-- clinician means -- "type 2 diabetes, any of it".
CREATE TABLE core.counseling_assignment (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  template_id uuid NOT NULL REFERENCES core.counseling_template(id),

  code_system  text NOT NULL,
  code_version text NOT NULL,
  -- Matched as a prefix against a recorded diagnosis code. 'E11' catches the whole family.
  code_prefix  text NOT NULL,

  -- Higher wins where two rules match. A patient with type 2 diabetes and hypothyroidism gets
  -- one checklist per matching rule, and the order they are offered in is not arbitrary.
  priority int NOT NULL DEFAULT 0,

  retired_at timestamptz,

  FOREIGN KEY (code_system, code_version)
    REFERENCES core.code_system_version(system, version),

  CONSTRAINT counseling_assignment_prefix_is_real CHECK (btrim(code_prefix) <> '')
);

CREATE INDEX counseling_assignment_by_code
  ON core.counseling_assignment (code_system, code_version, code_prefix)
  WHERE retired_at IS NULL;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'counseling_assignment', 'Which checklist a diagnosis calls for. Clinical, not local.')
ON CONFLICT DO NOTHING;

GRANT SELECT ON core.counseling_assignment TO dthcms_app, dthcms_projector;
GRANT INSERT, UPDATE ON core.counseling_assignment TO dthcms_app;

-- ---------------------------------------------------------------------------
-- A published version is frozen
-- ---------------------------------------------------------------------------

-- Acceptance criterion 2, as something the database refuses rather than something the authoring
-- UI remembers. The change this stops is not malicious: it is somebody fixing a typo in a live
-- template, after which every session that ever referenced it is describing a checklist that
-- never existed.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.counseling_published_versions_are_frozen() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  state text;
  affected_template uuid;
  affected_version int;
BEGIN
  IF TG_OP = 'DELETE' THEN
    affected_template := OLD.template_id;
    affected_version := OLD.version;
  ELSE
    affected_template := NEW.template_id;
    affected_version := NEW.version;
  END IF;

  SELECT status INTO state FROM core.counseling_template_version
   WHERE template_id = affected_template AND version = affected_version;

  IF state IS NOT NULL AND state <> 'DRAFT' THEN
    RAISE EXCEPTION 'version % of this template is published and cannot be edited', affected_version
      USING HINT = 'Draft a new version. Editing a published one rewrites what past sessions '
                   'were asked to cover, and nothing would look wrong afterwards.';
  END IF;

  IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER counseling_published_versions_are_frozen
  BEFORE INSERT OR UPDATE OR DELETE ON core.counseling_item
  FOR EACH ROW EXECUTE FUNCTION core.counseling_published_versions_are_frozen();

-- A version's own status moves DRAFT -> PUBLISHED -> RETIRED and nowhere else. Un-publishing
-- would leave sessions pointing at a version the floor can no longer see, which is worse than
-- retiring it: retired still reads.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.counseling_version_status_moves_forward() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status = NEW.status THEN RETURN NEW; END IF;
  IF OLD.status = 'DRAFT' AND NEW.status = 'PUBLISHED' THEN RETURN NEW; END IF;
  IF OLD.status = 'PUBLISHED' AND NEW.status = 'RETIRED' THEN RETURN NEW; END IF;
  RAISE EXCEPTION 'a counseling version cannot go from % to %', OLD.status, NEW.status
    USING HINT = 'Draft, publish, retire. A published version is never withdrawn - '
                 'sessions reference it.';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER counseling_version_status_moves_forward
  BEFORE UPDATE ON core.counseling_template_version
  FOR EACH ROW EXECUTE FUNCTION core.counseling_version_status_moves_forward();

-- Acceptance criterion 4: items exist in both languages before publishing is allowed.
--
-- Checked at the transition rather than on every insert, because a draft half-written in
-- English is a normal state for an author mid-sentence -- and a rule that refused it would make
-- the authoring UI fight the person using it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.counseling_publishes_only_in_both_languages() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  offender text;
  item_count int;
BEGIN
  IF NEW.status <> 'PUBLISHED' OR OLD.status = 'PUBLISHED' THEN RETURN NEW; END IF;

  SELECT count(*) INTO item_count FROM core.counseling_item
   WHERE template_id = NEW.template_id AND version = NEW.version;
  IF item_count = 0 THEN
    RAISE EXCEPTION 'a counseling version with no items cannot be published'
      USING HINT = 'An empty checklist on a phone is a checklist that gets ticked.';
  END IF;

  SELECT item_code INTO offender FROM core.counseling_item
   WHERE template_id = NEW.template_id AND version = NEW.version
     AND (btrim(text_en) = '' OR btrim(text_bn) = '')
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'item % is not written in both languages', offender
      USING HINT = 'Half this clinic counsels in Bangla. An item in one language is an item '
                   'half the counsellors cannot read to a patient.';
  END IF;

  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER counseling_publishes_only_in_both_languages
  BEFORE UPDATE ON core.counseling_template_version
  FOR EACH ROW EXECUTE FUNCTION core.counseling_publishes_only_in_both_languages();

-- ---------------------------------------------------------------------------
-- The diabetes template (§5.1's launch minimum; D-53 open)
-- ---------------------------------------------------------------------------

INSERT INTO core.counseling_template (id, code, title_en, title_bn) VALUES
  ('0190c000-0000-7000-8000-000000000001', 'DIABETES',
   'Diabetes counseling', 'ডায়াবেটিস কাউন্সেলিং')
ON CONFLICT (code) DO UPDATE SET
  title_en = EXCLUDED.title_en, title_bn = EXCLUDED.title_bn;

INSERT INTO core.counseling_template_version (template_id, version, status, notes) VALUES
  ('0190c000-0000-7000-8000-000000000001', 1, 'DRAFT',
   'Transcribed from blueprint section 5.1. The launch minimum, not a clinical author''s list (D-53).')
ON CONFLICT (template_id, version) DO NOTHING;

-- The seven, in §5.1's own order. Mandatory-ness is a clinical decision and open: everything is
-- marked mandatory here because a gate that lets a diabetic patient reach the consultant without
-- being told about their complications is a gate that is not doing anything -- and it is easier
-- for a clinician to relax one than to notice a missing one.
INSERT INTO core.counseling_item
  (template_id, version, item_code, ordering, text_en, text_bn,
   guidance_en, guidance_bn, is_mandatory, room) VALUES
  ('0190c000-0000-7000-8000-000000000001', 1, 'DISEASE_UNDERSTANDING', 1,
   'Understanding of the disease: what diabetes is',
   'রোগ সম্পর্কে ধারণা: ডায়াবেটিস কী',
   'In their own words, not yours. Ask them to explain it back.',
   'তাঁর নিজের ভাষায়, আপনার ভাষায় নয়। ফিরিয়ে বলতে বলুন।',
   true, 'COUNSELING_ROOM'),
  ('0190c000-0000-7000-8000-000000000001', 1, 'COMPLICATIONS', 2,
   'Awareness of diabetes complications',
   'ডায়াবেটিসের জটিলতা সম্পর্কে সচেতনতা',
   'Eyes, kidneys, feet, heart. Name all four; a patient who has heard only about the eyes will not check their feet.',
   'চোখ, কিডনি, পা, হৃদযন্ত্র। চারটিই বলুন; যিনি শুধু চোখের কথা শুনেছেন তিনি পা দেখবেন না।',
   true, 'COUNSELING_ROOM'),
  ('0190c000-0000-7000-8000-000000000001', 1, 'DIET', 3,
   'Food habits and diet counseling',
   'খাদ্যাভ্যাস ও ডায়েট কাউন্সেলিং',
   'What they actually eat, before what they should. A plan built on a guess is a plan nobody follows.',
   'তিনি আসলে কী খান, আগে সেটা জানুন। অনুমানের উপর গড়া পরিকল্পনা কেউ মানে না।',
   true, 'NUTRITION_ROOM'),
  ('0190c000-0000-7000-8000-000000000001', 1, 'EXERCISE', 4,
   'Exercise counseling',
   'ব্যায়াম কাউন্সেলিং',
   'Check the foot examination first. Walking advice for a foot with no sensation is dangerous advice.',
   'আগে পায়ের পরীক্ষা দেখুন। অনুভূতিহীন পায়ে হাঁটার পরামর্শ বিপজ্জনক।',
   true, 'COUNSELING_ROOM'),
  ('0190c000-0000-7000-8000-000000000001', 1, 'SELF_CARE', 5,
   'Chronic disease self-care counseling',
   'দীর্ঘমেয়াদি রোগে নিজের যত্ন',
   'Daily foot checks, sick-day rules, and when to come back before the appointment.',
   'প্রতিদিন পা দেখা, অসুস্থ দিনের নিয়ম, আর কখন সময়ের আগেই আসতে হবে।',
   true, 'COUNSELING_ROOM'),
  ('0190c000-0000-7000-8000-000000000001', 1, 'GLUCOMETER', 6,
   'Glucometer use',
   'গ্লুকোমিটার ব্যবহার',
   'Have them do it, not watch you do it.',
   'তাঁকে নিজে করতে দিন, শুধু দেখতে নয়।',
   true, 'COUNSELING_ROOM'),
  ('0190c000-0000-7000-8000-000000000001', 1, 'INSULIN_TECHNIQUE', 7,
   'Insulin injection sites and technique',
   'ইনসুলিন দেওয়ার স্থান ও পদ্ধতি',
   'Rotation matters more than technique. Ask where they injected yesterday.',
   'পদ্ধতির চেয়ে জায়গা বদলানো বেশি জরুরি। গতকাল কোথায় দিয়েছেন জিজ্ঞেস করুন।',
   true, 'INSULIN_CORNER')
ON CONFLICT (template_id, version, item_code) DO UPDATE SET
  ordering = EXCLUDED.ordering, text_en = EXCLUDED.text_en, text_bn = EXCLUDED.text_bn,
  guidance_en = EXCLUDED.guidance_en, guidance_bn = EXCLUDED.guidance_bn,
  is_mandatory = EXCLUDED.is_mandatory, room = EXCLUDED.room;

-- Published in the migration, because the alternative is a clinic whose first counselling
-- session cannot start until somebody remembers to press a button in the admin console.
UPDATE core.counseling_template_version
   SET status = 'PUBLISHED', published_at = now(), published_source = 'MIGRATION'
 WHERE template_id = '0190c000-0000-7000-8000-000000000001' AND version = 1
   AND status = 'DRAFT';

INSERT INTO core.counseling_assignment (template_id, code_system, code_version, code_prefix, priority)
VALUES
  ('0190c000-0000-7000-8000-000000000001', 'ICD10', '2019', 'E11', 100),
  ('0190c000-0000-7000-8000-000000000001', 'ICD10', '2019', 'E10', 100),
  ('0190c000-0000-7000-8000-000000000001', 'ICD10', '2019', 'O24', 90)
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- Criterion 4, standing rather than only checked at the transition. A published version whose
-- items lost their Bengali would be one half the counsellors cannot read from, and the way that
-- happens is a migration, not a form.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_published_counseling_is_bilingual() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender text;
BEGIN
  SELECT i.item_code INTO offender
    FROM core.counseling_item i
    JOIN core.counseling_template_version v
      ON v.template_id = i.template_id AND v.version = i.version
   WHERE v.status <> 'DRAFT'
     AND (btrim(i.text_en) = '' OR btrim(i.text_bn) = '')
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'published counseling item % is not in both languages', offender;
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_published_counseling_is_bilingual() IS
  'Raises if a published counseling item lacks either language (CP55 criterion 4).';

-- Every item names a room that exists and a template that has one live version at most. The
-- second half is an index; this is the first, plus the thing an index cannot say: a published
-- template with no items is a checklist that reads as complete the moment it opens.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_published_template_has_items() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offender text;
BEGIN
  SELECT t.code INTO offender
    FROM core.counseling_template_version v
    JOIN core.counseling_template t ON t.id = v.template_id
   WHERE v.status = 'PUBLISHED'
     AND NOT EXISTS (SELECT 1 FROM core.counseling_item i
                      WHERE i.template_id = v.template_id AND i.version = v.version)
   LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'published counseling template % has no items', offender
      USING HINT = 'An empty checklist reads as complete the moment it opens.';
  END IF;
END;
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_every_published_template_has_items() IS
  'Raises if a published counseling version is empty (CP55).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_published_counseling_is_bilingual',
   'every published counseling item reads in both languages', 68),
  ('assert_every_published_template_has_items',
   'no published counseling template is empty', 69)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_published_counseling_is_bilingual', 'assert_every_published_template_has_items');
DROP FUNCTION IF EXISTS core.assert_every_published_template_has_items();
DROP FUNCTION IF EXISTS core.assert_published_counseling_is_bilingual();
DROP TRIGGER IF EXISTS counseling_publishes_only_in_both_languages ON core.counseling_template_version;
DROP TRIGGER IF EXISTS counseling_version_status_moves_forward ON core.counseling_template_version;
DROP FUNCTION IF EXISTS core.counseling_publishes_only_in_both_languages();
DROP FUNCTION IF EXISTS core.counseling_version_status_moves_forward();
DROP TRIGGER IF EXISTS counseling_published_versions_are_frozen ON core.counseling_item;
DROP FUNCTION IF EXISTS core.counseling_published_versions_are_frozen();
DELETE FROM core.facility_scope_exemption
 WHERE schema_name = 'core' AND table_name IN (
   'counseling_room', 'counseling_template', 'counseling_template_version',
   'counseling_item', 'counseling_assignment');
DROP TABLE IF EXISTS core.counseling_assignment;
DROP TABLE IF EXISTS core.counseling_item;
DROP TABLE IF EXISTS core.counseling_template_version;
DROP TABLE IF EXISTS core.counseling_template;
DROP TABLE IF EXISTS core.counseling_room;
DELETE FROM core.role_permission
 WHERE permission_code IN ('counseling.template.read', 'counseling.template.publish');
DELETE FROM core.permission
 WHERE code IN ('counseling.template.read', 'counseling.template.publish');
