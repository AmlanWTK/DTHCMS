-- Station reach (CP84, ADR-0036).
--
-- Two things, and they are two halves of one decision.
--
--   1. The indexes the reach query runs on. Authorisation now costs a query per request on
--      the station-scoped routes, which ADR-0036 accepts knowingly and on the condition
--      that it is indexed and measured rather than assumed.
--
--   2. Invariant 127, which holds blueprint §4.4's statement about the registration and
--      records desks so that the scope correction in ADR-0036 §2 cannot silently drift
--      back into a station rule.
--
-- Nothing here is destructive and nothing here is data. Both indexes are additions and the
-- invariant is a function; the Down section removes exactly what the Up section added.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The indexes the reach query runs on
-- ---------------------------------------------------------------------------

-- The current visit is already an index lookup: `visit_one_open_per_patient` is a partial
-- unique index on (patient_id) WHERE status = 'open', added by CP38 for a different reason
-- and exactly the right shape for this one. Nothing is added for it.
--
-- What the queue needs is (visit_id, station_code) with the status alongside, because both
-- reach queries ask the same first two columns and then differ only in what they do with
-- the third. `queue_by_visit` is (visit_id, position) and `queue_live` is
-- (facility_id, station_code, status) partial on the live statuses — neither answers "this
-- visit, this station, any status", which is the read reach, and the read reach is the one
-- on the hot path because reads outnumber writes at every station.
--
-- status is in the index rather than only in the filter so the write reach is answered
-- without touching the heap: an index-only scan on three columns, for a question asked in
-- front of every station-scoped request.
CREATE INDEX queue_entry_station_reach
  ON core.queue_entry (visit_id, station_code, status);

COMMENT ON INDEX core.queue_entry_station_reach IS
  'The reach query of ADR-0036 §1: does this station have this patient in this visit, and at what status.';

-- The same question of the encounter table. `encounter_by_visit` is (visit_id, started_at)
-- and `encounter_one_open_per_station` is partial on in_progress only — which answers the
-- write reach and not the read one.
CREATE INDEX encounter_station_reach
  ON core.encounter (visit_id, station_code, status);

COMMENT ON INDEX core.encounter_station_reach IS
  'The reach query of ADR-0036 §1, encounter half: an open encounter is a write reach, any encounter is a read reach.';

-- ---------------------------------------------------------------------------
-- Invariant 127: the two desks whose job is the facility hold it facility-wide
-- ---------------------------------------------------------------------------

-- Blueprint §4.4 gives the registration desk the patient register and the records office
-- the facility's records. The first draft of the RBAC engine's scope table applied one rule
-- to everything with a `patient.` prefix and swept both desks into "your own station", which
-- is not strictness — for registration it is incoherent, because at the moment
-- `patient.write.demographics` is exercised there is no patient yet to be at a station.
--
-- The correction lives in Go (rbac.scopeFor's deskWide), because scope is a rule and not a
-- row: there is no scope column in this database to correct. So what this invariant can
-- assert is the *grant* half of §4.4 — that these two roles still hold the permissions the
-- correction is about, and that the desks whose reach was widened are still blinded to
-- everything §4.4 blinds them from.
--
-- That is a narrower assertion than "the scope is any", and saying so is the point: an
-- invariant that claimed to check a Go function would be an invariant that checked nothing.
-- What it catches is the drift that would make the correction dangerous rather than merely
-- wrong — a migration that quietly gives REGISTRATION a sensitive permission, at which
-- point a facility-wide reach stops being the front desk's job and starts being a hole.
-- TestRegistrationAndRecordsReachTheFacility in internal/rbac holds the Go half.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_the_facility_wide_desks_stay_blinded() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  missing   text;
  offenders text;
BEGIN
  -- The grants the widening is about. If one of these disappears the correction in
  -- rbac.scopeFor is describing a permission nobody holds, which is dead text pretending
  -- to be policy.
  SELECT string_agg(expected.role_code || ' ' || expected.permission_code, ', '
                    ORDER BY expected.role_code, expected.permission_code)
    INTO missing
    FROM (VALUES
      ('REGISTRATION', 'patient.write.demographics'),
      ('REGISTRATION', 'patient.read.demographics'),
      ('REGISTRATION', 'patient.consent.record'),
      ('REGISTRATION', 'patient.consent.revoke'),
      ('RECORDS',      'patient.read.demographics'),
      ('RECORDS',      'records.read'),
      ('RECORDS',      'records.upload'),
      ('RECORDS',      'records.verify')
    ) AS expected(role_code, permission_code)
   WHERE NOT EXISTS (
     SELECT 1 FROM core.role_permission rp
       JOIN core.role r ON r.id = rp.role_id
      WHERE r.code = expected.role_code AND rp.permission_code = expected.permission_code);

  IF missing IS NOT NULL THEN
    RAISE EXCEPTION 'a facility-wide desk has lost a permission its reach was widened for: %', missing
      USING HINT =
        'ADR-0036 §2 widened these two roles from "your own station" to the whole facility, '
        'on the argument that registering a patient has no station to be at and that the '
        'records office''s job is the facility''s records. A grant that has gone means the '
        'widening now describes nothing. Restore the grant, or narrow rbac.scopeFor''s '
        'deskWide to match — but do not leave the two disagreeing.';
  END IF;

  -- The other half, and the one that matters if somebody is careless: a role with a
  -- facility-wide reach over patients must still be blind to the clinical interpretation.
  -- §4.4 blinds registration; the widening did not touch that and must not come to.
  SELECT string_agg(r.code || ' ' || rp.permission_code, ', ' ORDER BY r.code, rp.permission_code)
    INTO offenders
    FROM core.role_permission rp
    JOIN core.role r ON r.id = rp.role_id
    JOIN core.permission p ON p.code = rp.permission_code
   WHERE r.code = 'REGISTRATION' AND p.is_sensitive;

  IF offenders IS NOT NULL THEN
    RAISE EXCEPTION 'the registration desk holds a sensitive permission: %', offenders
      USING HINT =
        'Blueprint §4.4 blinds registration to diagnoses and clinical interpretation, and '
        'ADR-0036 §2 widened that desk''s reach over the patient *register* to the whole '
        'facility on the explicit basis that this blinding still holds. A sensitive '
        'permission here turns a front desk into a reader of every diagnosis in the clinic.';
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION core.assert_the_facility_wide_desks_stay_blinded() FROM PUBLIC;

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_the_facility_wide_desks_stay_blinded',
   'the registration and records desks hold the permissions their facility-wide reach was granted for, and registration is still blind to every sensitive one',
   127)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant
 WHERE function_name = 'assert_the_facility_wide_desks_stay_blinded';

DROP FUNCTION IF EXISTS core.assert_the_facility_wide_desks_stay_blinded();

DROP INDEX IF EXISTS core.encounter_station_reach;
DROP INDEX IF EXISTS core.queue_entry_station_reach;
