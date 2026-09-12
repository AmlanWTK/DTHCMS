-- Who may run a medication safety check (CP78).
--
-- Its own permission rather than a reuse of `medication.rule.read`, because the two are different
-- acts on different objects. Reading the rule library is reading a drug label: "pioglitazone is
-- contraindicated in heart failure" is a sentence about pioglitazone and names nobody. Running a
-- check is reading **this patient's** kidney function, coded diagnoses and allergies, and
-- multiplying them by what is about to be prescribed. A grant that covered both would mean
-- anybody who may read the library may read a clinical picture, which is not what CP77's grant
-- was argued for.
--
-- # Who gets it
--
-- The prescribers, and QA.
--
--   * `PHYSICIAN` and `JUNIOR_DOCTOR` — this runs on every prescription they write (CP81).
--   * `QA` — CP83's clearance re-runs the interaction and duplicate checks before a prescription
--     may be printed, which is the whole point of a fail-closed gate: the officer's screen shows
--     the same findings the prescriber saw, computed again rather than carried forward.
--
-- **Not the pharmacist**, and not the administrator. The pharmacist for §4.4's reason — a
-- contraindication finding is a sentence containing a diagnosis — and the administrator because
-- an administrative account has no clinical reason to read a patient's renal function. Dr. Nahid
-- may reasonably want the pharmacist to have it; that is recorded in docs/medication-rules.md
-- beside the same question about the rule library itself, and it is his.
--
-- # is_sensitive
--
-- True, unlike the three rule-library permissions. The object of this permission is a patient's
-- clinical picture, which is exactly what the sensitive flag is for.

-- +goose Up

INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('medication.safety.check', 'medication', 'safety', 'check',
   'Run the deterministic medication safety engine against a patient and a draft prescription',
   true)
ON CONFLICT (code) DO UPDATE SET
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'medication.safety.check'
  FROM core.role r WHERE r.code IN ('PHYSICIAN', 'JUNIOR_DOCTOR', 'QA')
ON CONFLICT DO NOTHING;

-- +goose Down

DELETE FROM core.role_permission WHERE permission_code = 'medication.safety.check';
DELETE FROM core.permission WHERE code = 'medication.safety.check';
