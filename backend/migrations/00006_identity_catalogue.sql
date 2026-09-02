-- The identity catalogue: twelve stations, eighteen roles, and the permissions between them.
--
-- Separate from 00005 because the two change for different reasons. The schema is settled;
-- this file grows a row every time a checkpoint introduces an action someone must be
-- allowed to take, and a diff of a catalogue is readable in a way a diff of a schema plus
-- a catalogue is not.
--
-- Everything here is idempotent. `migrate up` on an existing database must be a no-op, and
-- a seed that is only correct the first time is a seed that will be wrong in staging.
--
-- The last section is the important one. Blueprint §4.4 states three access rules in
-- prose — a nutritionist has no access to prescriptions, a pharmacist sees dosing but not
-- diagnoses, registration is blinded to sensitive clinical data. Prose cannot fail a build.
-- core.assert_rbac_constraints() can, and does, on every migration in every environment.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Stations (blueprint §3)
--
-- is_staffed defaults to false: a station exists in the design from today, but the queue
-- must not route a patient to a room with nobody in it. Turning one on is an operational
-- act, recorded, not a consequence of a seed running.
-- ---------------------------------------------------------------------------

INSERT INTO core.station (facility_id, code, name_en, name_bn, sequence_hint) VALUES
  (core.default_facility(), 'STN_REGISTRATION', 'Registration', 'নিবন্ধন', 1),
  (core.default_facility(), 'STN_ANTHROPOMETRY', 'Anthropometry & Screening', 'দেহমাপ ও স্ক্রিনিং', 2),
  (core.default_facility(), 'STN_COUNSELING', 'Counseling & Lifestyle', 'কাউন্সেলিং ও জীবনযাত্রা', 3),
  (core.default_facility(), 'STN_HISTORY', 'Medical History', 'রোগের ইতিহাস', 4),
  (core.default_facility(), 'STN_EXAMINATION', 'Clinical Examination & Vitals', 'শারীরিক পরীক্ষা ও ভাইটাল', 5),
  (core.default_facility(), 'STN_RECORDS', 'Medical Records Import', 'পুরোনো রেকর্ড সংযোজন', 6),
  (core.default_facility(), 'STN_NUTRITION', 'Nutrition Assessment', 'পুষ্টি মূল্যায়ন', 7),
  (core.default_facility(), 'STN_EXERCISE', 'Exercise Assessment', 'ব্যায়াম মূল্যায়ন', 8),
  (core.default_facility(), 'STN_CONSULTATION', 'Physician Consultation', 'চিকিৎসকের পরামর্শ', 9),
  (core.default_facility(), 'STN_QA', 'Quality Assurance Review', 'মান যাচাই', 10),
  (core.default_facility(), 'STN_RX_EDUCATION', 'Prescription Education', 'ব্যবস্থাপত্র শিক্ষা', 11),
  (core.default_facility(), 'STN_FOLLOWUP', 'Long-Term Monitoring & Follow-Up', 'দীর্ঘমেয়াদি ফলোআপ', 12)
ON CONFLICT (facility_id, code) DO UPDATE SET
  name_en = EXCLUDED.name_en,
  name_bn = EXCLUDED.name_bn,
  sequence_hint = EXCLUDED.sequence_hint;

-- ---------------------------------------------------------------------------
-- Roles (blueprint §6.3, eighteen)
--
-- CLINICAL_ASSISTANT and JUNIOR_DOCTOR both work the examination station; a station has
-- one place in the sequence but need not have one role. PHARMACIST, RESEARCHER, HR, ADMIN
-- and FIELD_WORKER work no station in the patient journey at all.
--
-- The Bangla names are the engineer's translations and are flagged for Dr. Nahid's review,
-- like every other Bengali clinical label in the system. A CHECK in 00005 cannot catch a
-- translation that is merely wrong; only he can.
-- ---------------------------------------------------------------------------

INSERT INTO core.role (code, name_en, name_bn, is_clinical, station_code) VALUES
  ('REGISTRATION', 'Registration Officer', 'নিবন্ধন কর্মকর্তা', true, 'STN_REGISTRATION'),
  ('ANTHROPOMETRY', 'Anthropometry Officer', 'দেহমাপ কর্মকর্তা', true, 'STN_ANTHROPOMETRY'),
  ('COUNSELOR', 'Clinical Counselor', 'ক্লিনিক্যাল কাউন্সেলর', true, 'STN_COUNSELING'),
  ('HISTORY', 'Medical History Officer', 'রোগ-ইতিহাস কর্মকর্তা', true, 'STN_HISTORY'),
  ('CLINICAL_ASSISTANT', 'Clinical Assistant', 'ক্লিনিক্যাল সহকারী', true, 'STN_EXAMINATION'),
  ('JUNIOR_DOCTOR', 'Junior Doctor', 'সহকারী চিকিৎসক', true, 'STN_EXAMINATION'),
  ('RECORDS', 'Medical Records Officer', 'মেডিকেল রেকর্ড কর্মকর্তা', true, 'STN_RECORDS'),
  ('NUTRITIONIST', 'Clinical Nutritionist', 'পুষ্টিবিদ', true, 'STN_NUTRITION'),
  ('EXERCISE', 'Exercise Specialist', 'ব্যায়াম বিশেষজ্ঞ', true, 'STN_EXERCISE'),
  ('PHYSICIAN', 'Chief Consultant', 'প্রধান পরামর্শক চিকিৎসক', true, 'STN_CONSULTATION'),
  ('QA', 'Quality Assurance Officer', 'মান নিয়ন্ত্রণ কর্মকর্তা', true, 'STN_QA'),
  ('RX_EDUCATOR', 'Prescription Education Officer', 'ব্যবস্থাপত্র শিক্ষা কর্মকর্তা', true, 'STN_RX_EDUCATION'),
  ('PHARMACIST', 'Pharmacist', 'ফার্মাসিস্ট', true, NULL),
  ('CRM', 'Patient Relations Officer', 'রোগী যোগাযোগ কর্মকর্তা', true, 'STN_FOLLOWUP'),
  ('RESEARCHER', 'Researcher', 'গবেষক', false, NULL),
  ('HR', 'Human Resources Officer', 'মানবসম্পদ কর্মকর্তা', false, NULL),
  ('ADMIN', 'System Administrator', 'সিস্টেম প্রশাসক', false, NULL),
  ('FIELD_WORKER', 'Community Field Worker', 'মাঠকর্মী', true, NULL)
ON CONFLICT (code) DO UPDATE SET
  name_en = EXCLUDED.name_en,
  name_bn = EXCLUDED.name_bn,
  is_clinical = EXCLUDED.is_clinical,
  station_code = EXCLUDED.station_code;

-- ---------------------------------------------------------------------------
-- The permission catalogue
--
-- resource.action.scope. is_sensitive marks the ones that reveal a diagnosis or a clinical
-- interpretation, which is what makes §4.4's blinding rules checkable by a query rather
-- than by reading a grant list carefully and hoping.
--
-- patient.read.allergies is deliberately NOT sensitive, and that is a departure worth
-- stating: §4.4 says the pharmacist sees dosing and not diagnoses, but a pharmacist who
-- cannot see an allergy cannot dispense safely. Allergies are held apart from diagnoses so
-- the blinding rule can stand without costing a safety check.
-- ---------------------------------------------------------------------------

INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('patient.read.demographics', 'patient', 'read', 'demographics', 'Read a patient''s name, age, contact details and address', false),
  ('patient.write.demographics', 'patient', 'write', 'demographics', 'Create or amend a patient''s demographic record', false),
  ('patient.read.allergies', 'patient', 'read', 'allergies', 'Read a patient''s recorded allergies', false),
  ('patient.read.clinical', 'patient', 'read', 'clinical', 'Read a patient''s diagnoses and clinical interpretation', true),
  ('patient.merge', 'patient', 'merge', '', 'Merge two records identified as the same person', false),
  ('patient.consent.record', 'patient', 'consent', 'record', 'Record a patient''s consent against a template version', false),
  ('patient.consent.revoke', 'patient', 'consent', 'revoke', 'Record a patient''s revocation of a consent', false),
  ('observation.write.anthro', 'observation', 'write', 'anthro', 'Enter height, weight, waist, hip and body composition', false),
  ('observation.write.vitals', 'observation', 'write', 'vitals', 'Enter blood pressure, pulse, respiration, temperature and SpO2', false),
  ('observation.write.lifestyle', 'observation', 'write', 'lifestyle', 'Enter smoking, alcohol, sleep, stress and activity baseline', false),
  ('observation.write.history', 'observation', 'write', 'history', 'Enter complaints, comorbidities, family history and current drugs', false),
  ('observation.write.nutrition', 'observation', 'write', 'nutrition', 'Enter dietary recall, caloric intake and meal timing', false),
  ('observation.write.exercise', 'observation', 'write', 'exercise', 'Enter mobility, joint findings and baseline fitness', false),
  ('observation.read.values', 'observation', 'read', 'values', 'Read recorded measurements without clinical interpretation', false),
  ('observation.correct.request', 'observation', 'correct', 'request', 'Flag a recorded value as wrong and request its correction', false),
  ('observation.correct.approve', 'observation', 'correct', 'approve', 'Approve a correction to a value entered by someone else', false),
  ('counseling.tick', 'counseling', 'tick', '', 'Tick a counselling checklist item as covered', false),
  ('counseling.template.write', 'counseling', 'template', 'write', 'Create or amend a counselling template', false),
  ('records.upload', 'records', 'upload', '', 'Scan and attach an external medical document', false),
  ('records.read', 'records', 'read', '', 'Read digitised external records and the assembled chronology', true),
  ('records.verify', 'records', 'verify', '', 'Confirm an extracted value against its source document', false),
  ('lab.order', 'lab', 'order', '', 'Order an investigation', false),
  ('lab.result.enter', 'lab', 'result', 'enter', 'Enter a laboratory result', false),
  ('lab.read', 'lab', 'read', '', 'Read laboratory results', false),
  ('diagnosis.read', 'diagnosis', 'read', '', 'Read a patient''s diagnoses', true),
  ('diagnosis.write', 'diagnosis', 'write', '', 'Record or amend a diagnosis', true),
  ('prescription.draft', 'prescription', 'draft', '', 'Prepare a prescription for signature', false),
  ('prescription.sign', 'prescription', 'sign', '', 'Sign a prescription, making it valid for dispensing', false),
  ('prescription.read', 'prescription', 'read', '', 'Read a signed prescription''s drugs and dosing', false),
  ('prescription.dispense', 'prescription', 'dispense', '', 'Record that a prescription has been dispensed', false),
  ('ai.synthesis.read', 'ai', 'synthesis', 'read', 'Read the AI one-page synthesis of a patient''s record', true),
  ('ai.suggestion.approve', 'ai', 'suggestion', 'approve', 'Accept, edit or reject an AI suggestion', false),
  ('qa.review', 'qa', 'review', '', 'Run the quality review on a completed file', false),
  ('qa.clear', 'qa', 'clear', '', 'Clear a file for printing', false),
  ('qa.bounce', 'qa', 'bounce', '', 'Return a file for correction', false),
  ('education.record', 'education', 'record', '', 'Record demonstrated device technique and prior compliance', false),
  ('crm.read', 'crm', 'read', '', 'Read follow-up history and contact preferences', false),
  ('crm.contact', 'crm', 'contact', '', 'Contact a patient by call or message', false),
  ('crm.schedule', 'crm', 'schedule', '', 'Schedule or reschedule a follow-up', false),
  ('research.query', 'research', 'query', '', 'Query the de-identified research schema', false),
  ('research.export', 'research', 'export', '', 'Export a de-identified research dataset', false),
  ('outreach.capture', 'outreach', 'capture', '', 'Capture screening data in the community', false),
  ('outreach.read', 'outreach', 'read', '', 'Read community screening records', false),
  ('formulary.read', 'formulary', 'read', '', 'Read the formulary and current prices', false),
  ('formulary.write', 'formulary', 'write', '', 'Add or amend a formulary entry', false),
  ('formulary.price.review', 'formulary', 'price', 'review', 'Record the monthly price review', false),
  ('stock.movement.record', 'stock', 'movement', 'record', 'Record stock received, issued or adjusted', false),
  ('user.invite', 'user', 'invite', '', 'Invite a new member of staff', false),
  ('user.read', 'user', 'read', '', 'Read staff accounts and their status', false),
  ('user.suspend', 'user', 'suspend', '', 'Suspend a staff account', false),
  ('user.deactivate', 'user', 'deactivate', '', 'Deactivate a staff account', false),
  ('role.grant', 'role', 'grant', '', 'Grant a role to a user', false),
  ('role.revoke', 'role', 'revoke', '', 'Revoke a role from a user', false),
  ('device.enroll', 'device', 'enroll', '', 'Enrol a clinic device', false),
  ('device.revoke', 'device', 'revoke', '', 'Revoke an enrolled device', false),
  ('audit.read', 'audit', 'read', '', 'Read the security and clinical audit trail', false),
  ('station.configure', 'station', 'configure', '', 'Configure stations and their order', false),
  ('facility.configure', 'facility', 'configure', '', 'Configure facility settings', false),
  ('report.read.operational', 'report', 'read', 'operational', 'Read throughput, queue and quality reports', false),
  ('report.read.financial', 'report', 'read', 'financial', 'Read cost and revenue reports', false),
  ('hr.attendance.read', 'hr', 'attendance', 'read', 'Read staff attendance', false),
  ('hr.performance.read', 'hr', 'performance', 'read', 'Read staff quality and throughput records', false)
ON CONFLICT (code) DO UPDATE SET
  description = EXCLUDED.description,
  is_sensitive = EXCLUDED.is_sensitive;

-- ---------------------------------------------------------------------------
-- Grants
--
-- Written as one statement per role rather than one enormous VALUES list, because the
-- question asked of this file in review is always "what can a nutritionist do", and that
-- should be answerable by reading one paragraph.
-- ---------------------------------------------------------------------------

-- Registration Officer — 5 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'REGISTRATION' AND p.code IN (
       'patient.read.demographics',
       'patient.write.demographics',
       'patient.consent.record',
       'patient.consent.revoke',
       'observation.correct.request'
 ) ON CONFLICT DO NOTHING;

-- Anthropometry Officer — 4 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'ANTHROPOMETRY' AND p.code IN (
       'patient.read.demographics',
       'observation.write.anthro',
       'observation.read.values',
       'observation.correct.request'
 ) ON CONFLICT DO NOTHING;

-- Clinical Counselor — 5 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'COUNSELOR' AND p.code IN (
       'patient.read.demographics',
       'observation.write.lifestyle',
       'observation.read.values',
       'counseling.tick',
       'observation.correct.request'
 ) ON CONFLICT DO NOTHING;

-- Medical History Officer — 7 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'HISTORY' AND p.code IN (
       'patient.read.demographics',
       'patient.read.allergies',
       'patient.read.clinical',
       'observation.write.history',
       'observation.read.values',
       'records.read',
       'observation.correct.request'
 ) ON CONFLICT DO NOTHING;

-- Clinical Assistant — 7 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'CLINICAL_ASSISTANT' AND p.code IN (
       'patient.read.demographics',
       'patient.read.allergies',
       'observation.write.vitals',
       'observation.read.values',
       'lab.read',
       'lab.result.enter',
       'observation.correct.request'
 ) ON CONFLICT DO NOTHING;

-- Junior Doctor — 14 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'JUNIOR_DOCTOR' AND p.code IN (
       'patient.read.demographics',
       'patient.read.allergies',
       'patient.read.clinical',
       'observation.write.vitals',
       'observation.read.values',
       'observation.correct.request',
       'observation.correct.approve',
       'records.read',
       'lab.read',
       'lab.order',
       'lab.result.enter',
       'diagnosis.read',
       'prescription.draft',
       'ai.synthesis.read'
 ) ON CONFLICT DO NOTHING;

-- Medical Records Officer — 5 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'RECORDS' AND p.code IN (
       'patient.read.demographics',
       'patient.merge',
       'records.upload',
       'records.read',
       'records.verify'
 ) ON CONFLICT DO NOTHING;

-- Clinical Nutritionist — 5 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'NUTRITIONIST' AND p.code IN (
       'patient.read.demographics',
       'patient.read.clinical',
       'observation.write.nutrition',
       'observation.read.values',
       'lab.read'
 ) ON CONFLICT DO NOTHING;

-- Exercise Specialist — 4 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'EXERCISE' AND p.code IN (
       'patient.read.demographics',
       'patient.read.clinical',
       'observation.write.exercise',
       'observation.read.values'
 ) ON CONFLICT DO NOTHING;

-- Chief Consultant — 21 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'PHYSICIAN' AND p.code IN (
       'patient.read.demographics',
       'patient.read.allergies',
       'patient.read.clinical',
       'observation.read.values',
       'observation.correct.approve',
       'records.read',
       'lab.read',
       'lab.order',
       'lab.result.enter',
       'diagnosis.read',
       'diagnosis.write',
       'prescription.draft',
       'prescription.sign',
       'prescription.read',
       'ai.synthesis.read',
       'ai.suggestion.approve',
       'counseling.template.write',
       'qa.clear',
       'crm.read',
       'report.read.operational',
       'audit.read'
 ) ON CONFLICT DO NOTHING;

-- Quality Assurance Officer — 12 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'QA' AND p.code IN (
       'patient.read.demographics',
       'patient.read.clinical',
       'observation.read.values',
       'diagnosis.read',
       'prescription.read',
       'lab.read',
       'records.read',
       'qa.review',
       'qa.clear',
       'qa.bounce',
       'audit.read',
       'hr.performance.read'
 ) ON CONFLICT DO NOTHING;

-- Prescription Education Officer — 3 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'RX_EDUCATOR' AND p.code IN (
       'patient.read.demographics',
       'prescription.read',
       'education.record'
 ) ON CONFLICT DO NOTHING;

-- Pharmacist — 8 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'PHARMACIST' AND p.code IN (
       'patient.read.demographics',
       'patient.read.allergies',
       'prescription.read',
       'prescription.dispense',
       'formulary.read',
       'formulary.write',
       'formulary.price.review',
       'stock.movement.record'
 ) ON CONFLICT DO NOTHING;

-- Patient Relations Officer — 4 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'CRM' AND p.code IN (
       'patient.read.demographics',
       'crm.read',
       'crm.contact',
       'crm.schedule'
 ) ON CONFLICT DO NOTHING;

-- Researcher — 2 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'RESEARCHER' AND p.code IN (
       'research.query',
       'research.export'
 ) ON CONFLICT DO NOTHING;

-- Human Resources Officer — 4 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'HR' AND p.code IN (
       'user.read',
       'hr.attendance.read',
       'hr.performance.read',
       'report.read.operational'
 ) ON CONFLICT DO NOTHING;

-- System Administrator — 14 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'ADMIN' AND p.code IN (
       'user.invite',
       'user.read',
       'user.suspend',
       'user.deactivate',
       'role.grant',
       'role.revoke',
       'device.enroll',
       'device.revoke',
       'audit.read',
       'station.configure',
       'facility.configure',
       'patient.merge',
       'report.read.operational',
       'report.read.financial'
 ) ON CONFLICT DO NOTHING;

-- Community Field Worker — 6 permissions
INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'FIELD_WORKER' AND p.code IN (
       'patient.read.demographics',
       'patient.write.demographics',
       'observation.write.anthro',
       'observation.write.vitals',
       'outreach.capture',
       'outreach.read'
 ) ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Blueprint §4.4, as an invariant
--
-- Each rule below is a sentence from the blueprint turned into a query that fails the
-- migration. That is the difference between a rule that is documented and a rule that is
-- true: a future checkpoint adding one convenient permission to the pharmacist role finds
-- out at `migrate up`, in every environment, rather than at an audit.
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_rbac_constraints() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  -- §4.4: "Nutritionist: write diet; read labs; no access to prescriptions."
  SELECT string_agg(rp.permission_code, ', ' ORDER BY rp.permission_code) INTO offending
  FROM core.role_permission rp
  JOIN core.role r ON r.id = rp.role_id
  JOIN core.permission p ON p.code = rp.permission_code
  WHERE r.code = 'NUTRITIONIST' AND p.resource = 'prescription';

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'blueprint §4.4: the nutritionist role holds prescription permissions: %', offending
      USING HINT = 'A nutritionist has no access to prescriptions. Remove the grant, or '
                   'amend the blueprint and this assertion in the same change.';
  END IF;

  -- §4.4: "Pharmacist: sees authorized drug list and dosing only; diagnoses hidden."
  SELECT string_agg(rp.permission_code, ', ' ORDER BY rp.permission_code) INTO offending
  FROM core.role_permission rp
  JOIN core.role r ON r.id = rp.role_id
  JOIN core.permission p ON p.code = rp.permission_code
  WHERE r.code = 'PHARMACIST' AND (p.resource = 'diagnosis' OR p.is_sensitive);

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'blueprint §4.4: diagnoses are not hidden from the pharmacist: %', offending
      USING HINT = 'The pharmacist sees the drug list, dosing and allergies. Diagnoses are hidden.';
  END IF;

  -- §4.4: "Registration: read/write demographics; blinded to sensitive clinical diagnoses."
  SELECT string_agg(rp.permission_code, ', ' ORDER BY rp.permission_code) INTO offending
  FROM core.role_permission rp
  JOIN core.role r ON r.id = rp.role_id
  JOIN core.permission p ON p.code = rp.permission_code
  WHERE r.code = 'REGISTRATION' AND p.is_sensitive;

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'blueprint §4.4: registration is not blinded to clinical data: %', offending
      USING HINT = 'Registration reads and writes demographics only.';
  END IF;

  -- D-48: the research role reaches de-identified data and nothing else. The database
  -- already refuses it the identified schemas (00002); this refuses it the permission,
  -- so the two layers say the same thing.
  SELECT string_agg(rp.permission_code, ', ' ORDER BY rp.permission_code) INTO offending
  FROM core.role_permission rp
  JOIN core.role r ON r.id = rp.role_id
  JOIN core.permission p ON p.code = rp.permission_code
  WHERE r.code = 'RESEARCHER' AND p.resource <> 'research';

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'D-48: the researcher role reaches identifiable data: %', offending
      USING HINT = 'A researcher holds research permissions only. Identifiable access is '
                   'a separate role, granted separately and visible as such.';
  END IF;

  -- A permission nobody holds is either a missing grant or a permission that should not
  -- exist. Both are worth an error while the catalogue is small enough to fix.
  SELECT string_agg(p.code, ', ' ORDER BY p.code) INTO offending
  FROM core.permission p
  WHERE NOT EXISTS (SELECT 1 FROM core.role_permission rp WHERE rp.permission_code = p.code);

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'permissions granted to no role: %', offending
      USING HINT = 'Grant it to the role that needs it, or delete it. An ungranted '
                   'permission is a check that can never pass.';
  END IF;

  -- Every station must be reachable by somebody, or a patient can be queued to a room
  -- no role can work.
  SELECT string_agg(s.code, ', ' ORDER BY s.code) INTO offending
  FROM core.station s
  WHERE s.is_active
    AND NOT EXISTS (SELECT 1 FROM core.role r WHERE r.station_code = s.code);

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'active stations no role works: %', offending
      USING HINT = 'Give the station a role, or deactivate the station.';
  END IF;

  -- Blueprint §3 names twelve stations and §6.3 names eighteen roles. A seed that
  -- silently produces a different number is a seed that partly failed.
  IF (SELECT count(*) FROM core.role) < 18 THEN
    RAISE EXCEPTION 'the role catalogue has % roles; the blueprint names 18',
      (SELECT count(*) FROM core.role);
  END IF;

  IF (SELECT count(*) FROM core.station WHERE is_active) < 12 THEN
    RAISE EXCEPTION 'the station list has % active stations; the blueprint names 12',
      (SELECT count(*) FROM core.station WHERE is_active);
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_rbac_constraints() IS
  'Blueprint §4.4 access rules and D-48 research isolation, enforced on every migration run.';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_invariants() RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM core.assert_ledger_append_only();
  PERFORM core.assert_read_models_derived();
  PERFORM core.assert_research_isolated();
  PERFORM core.assert_facility_scoping();
  PERFORM core.assert_users_undeletable();
  PERFORM core.assert_rbac_constraints();
END
$$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_invariants() RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM core.assert_ledger_append_only();
  PERFORM core.assert_read_models_derived();
  PERFORM core.assert_research_isolated();
  PERFORM core.assert_facility_scoping();
END
$$;
-- +goose StatementEnd

DROP FUNCTION IF EXISTS core.assert_rbac_constraints();

-- Rolling the catalogue back on a database where roles have been granted would mean
-- deleting core.user_role rows — which is the one thing this checkpoint exists to prevent.
-- The foreign key already refuses it; this turns an opaque constraint violation into an
-- instruction. Found by running the down migration, which is the only way this is ever
-- found.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM core.user_role) THEN
    RAISE EXCEPTION 'cannot roll back the role catalogue: % role grants exist',
      (SELECT count(*) FROM core.user_role)
      USING HINT = 'Revoking a grant sets revoked_at; the row is never deleted, so the '
                   'catalogue it points at cannot be removed either. Roll back to before '
                   'CP15 only on a database with no staff, or restore from a backup.';
  END IF;
END
$$;

DELETE FROM core.role_permission;
DELETE FROM core.permission;
DELETE FROM core.role;
DELETE FROM core.station WHERE facility_id = core.default_facility();
