-- The security audit log (CP22, blueprint §4.5 and §9.1).
--
-- Two trails, by design: the clinical ledger (CP23) records what happened to patients;
-- this one records what happened to the system — who signed in, who was given which role,
-- who exported what, who broke the glass. Both are append-only. This one is also a chain:
-- every row carries the hash of the row before it, so a row cannot be removed from the
-- middle, or altered, without every later hash disagreeing.
--
-- Three structural properties, in the house style:
--
--   * the application can INSERT and SELECT, and nothing else — the schema's default
--     privileges say so, and core.assert_ledger_append_only() checks it on every start;
--   * the table is partitioned by month from the first day, so retention (D-05) and
--     per-partition verification are ordinary operations later rather than agony;
--   * a break-glass access and the alert it raises are rows that cannot be deleted, because
--     the whole point of them is to be found afterwards.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The chain
-- ---------------------------------------------------------------------------

CREATE TABLE ledger.audit_event (
  -- Gapless, facility-wide. Assigned under an advisory lock by the recorder; the chain
  -- makes a gap or a duplicate detectable regardless.
  seq            bigint      NOT NULL,
  facility_id    uuid        NOT NULL REFERENCES core.facility(id),

  -- What happened, as a dotted name from the registry in internal/audit/sentences.go.
  -- Every kind has a sentence in both languages; the registry test refuses one without.
  kind           text        NOT NULL,

  -- Who did it. The code is copied so the sentence survives a renamed account; the id is
  -- for joins. A failed sign-in for a code that does not exist has neither.
  actor_user_id  uuid        REFERENCES core.app_user(id),
  actor_code     text        NOT NULL DEFAULT '',
  actor_role     text        NOT NULL DEFAULT '',

  -- Whom it was done to, when it was done to a person.
  target_user_id uuid        REFERENCES core.app_user(id),
  target_code    text        NOT NULL DEFAULT '',

  -- What it was done about, when it was about a patient, a device or a session. No foreign
  -- keys: the ledger must stay valid whatever happens to the tables it names (§9.2).
  patient_id     uuid,
  device_id      uuid,
  session_id     uuid,

  reason         text        NOT NULL DEFAULT '',
  -- Everything else the sentence needs: the role granted, the status before and after,
  -- the number of sessions ended. Small, and never a secret.
  details        jsonb       NOT NULL DEFAULT '{}'::jsonb,
  client_digest  bytea,

  recorded_at    timestamptz NOT NULL DEFAULT now(),

  -- The chain. prev_hash is the hash of seq-1 (32 zero bytes for seq 1); hash covers
  -- prev_hash and every field above, in the canonical form internal/audit hashes.
  prev_hash      bytea       NOT NULL,
  hash           bytea       NOT NULL,

  PRIMARY KEY (seq, recorded_at),
  CONSTRAINT audit_event_seq_positive  CHECK (seq >= 1),
  CONSTRAINT audit_event_kind_shape    CHECK (kind ~ '^[a-z_]+(\.[a-z_]+)+$'),
  CONSTRAINT audit_event_hash_length   CHECK (length(hash) = 32 AND length(prev_hash) = 32),
  CONSTRAINT audit_event_details_object CHECK (jsonb_typeof(details) = 'object')
) PARTITION BY RANGE (recorded_at);

COMMENT ON TABLE ledger.audit_event IS
  'Security audit log: sign-ins, role changes, credential resets, exports, break-glass. Append-only, hash-chained, partitioned by month.';

-- The queries the viewer runs: by time (the default), by person, by patient.
CREATE INDEX audit_event_recorded_at ON ledger.audit_event (facility_id, recorded_at DESC);
CREATE INDEX audit_event_actor       ON ledger.audit_event (actor_user_id, recorded_at DESC) WHERE actor_user_id IS NOT NULL;
CREATE INDEX audit_event_target      ON ledger.audit_event (target_user_id, recorded_at DESC) WHERE target_user_id IS NOT NULL;
CREATE INDEX audit_event_patient     ON ledger.audit_event (patient_id, recorded_at DESC) WHERE patient_id IS NOT NULL;
CREATE INDEX audit_event_kind        ON ledger.audit_event (facility_id, kind, recorded_at DESC);

-- Monthly partitions, created ahead. ledger.ensure_audit_partitions(n) adds the next n
-- months and is idempotent; the migrator runs it (the application role may not CREATE
-- in ledger, deliberately). A default partition catches a row that arrives with a date no
-- partition covers, so the write is never refused — the audit log losing an entry because
-- an operator forgot a monthly chore would be the wrong failure mode.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger.ensure_audit_partitions(months_ahead integer DEFAULT 12)
RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
  first date := date_trunc('month', now())::date;
  m date;
  created integer := 0;
  name text;
BEGIN
  FOR i IN 0..months_ahead LOOP
    m := first + (i || ' months')::interval;
    name := format('audit_event_%s', to_char(m, 'YYYY_MM'));
    IF to_regclass(format('ledger.%I', name)) IS NULL THEN
      EXECUTE format(
        'CREATE TABLE ledger.%I PARTITION OF ledger.audit_event FOR VALUES FROM (%L) TO (%L)',
        name, m, (m + interval '1 month')::date);
      created := created + 1;
    END IF;
  END LOOP;
  RETURN created;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION ledger.ensure_audit_partitions(integer) IS
  'Creates the monthly audit_event partitions for the next n months. Idempotent; run by the migrator.';

-- The first year and a bit, plus the safety net. The default partition is listed first
-- because a row that lands in it is a row an operator should notice: the verifier reports
-- how many are there.
CREATE TABLE ledger.audit_event_default PARTITION OF ledger.audit_event DEFAULT;
SELECT ledger.ensure_audit_partitions(15);

-- ---------------------------------------------------------------------------
-- Break-glass
-- ---------------------------------------------------------------------------

-- An emergency access: a clinician says, in writing, why they need a record their role
-- does not reach, and gets it for a bounded time while an administrator is told at once.
-- The row is the evidence; it cannot be deleted, and closing it is an update that keeps
-- everything that was there.
CREATE TABLE core.break_glass_access (
  id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id     uuid        NOT NULL REFERENCES core.facility(id),
  user_id         uuid        NOT NULL REFERENCES core.app_user(id),
  active_role     text        NOT NULL DEFAULT '',

  -- What is being reached: a patient (by id), or something else named in words.
  scope_kind      text        NOT NULL,
  scope_ref       text        NOT NULL,

  -- Typed, and long enough that it cannot be a keystroke. The check is the whole of
  -- acceptance criterion 3's first half.
  justification   text        NOT NULL,

  granted_at      timestamptz NOT NULL DEFAULT now(),
  expires_at      timestamptz NOT NULL,
  ended_at        timestamptz,
  ended_by        uuid        REFERENCES core.app_user(id),
  end_reason      text        NOT NULL DEFAULT '',

  -- The administrator who saw it, and when. Null until somebody does.
  acknowledged_by uuid        REFERENCES core.app_user(id),
  acknowledged_at timestamptz,

  -- The audit row this raised, so the two can be read together.
  audit_seq       bigint,

  CONSTRAINT break_glass_scope_known      CHECK (scope_kind IN ('patient', 'other')),
  CONSTRAINT break_glass_scope_present    CHECK (length(btrim(scope_ref)) >= 1),
  CONSTRAINT break_glass_justified        CHECK (length(btrim(justification)) >= 20),
  CONSTRAINT break_glass_bounded          CHECK (expires_at > granted_at
                                                 AND expires_at <= granted_at + interval '24 hours'),
  CONSTRAINT break_glass_ended_consistent CHECK ((ended_at IS NULL) = (ended_by IS NULL)),
  CONSTRAINT break_glass_ack_consistent   CHECK ((acknowledged_at IS NULL) = (acknowledged_by IS NULL))
);

COMMENT ON TABLE core.break_glass_access IS
  'Emergency access grants: who, what, the typed justification, for how long, and who acknowledged it. Never deleted.';

CREATE INDEX break_glass_open ON core.break_glass_access (facility_id, granted_at DESC)
  WHERE ended_at IS NULL;

-- ---------------------------------------------------------------------------
-- Administrator alerts
-- ---------------------------------------------------------------------------

-- What "notify an administrator immediately" is made of before there is an SMS gateway:
-- a row every administrator's console polls, shown until one of them acknowledges it.
-- Bilingual at the row, because the person reading it may be either.
CREATE TABLE core.admin_alert (
  id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id     uuid        NOT NULL REFERENCES core.facility(id),
  kind            text        NOT NULL,
  severity        text        NOT NULL DEFAULT 'high',
  message_en      text        NOT NULL,
  message_bn      text        NOT NULL,
  -- What the alert is about, for the console to link to.
  reference       jsonb       NOT NULL DEFAULT '{}'::jsonb,
  audit_seq       bigint,
  created_at      timestamptz NOT NULL DEFAULT now(),
  acknowledged_by uuid        REFERENCES core.app_user(id),
  acknowledged_at timestamptz,

  CONSTRAINT admin_alert_severity_known CHECK (severity IN ('high', 'normal')),
  CONSTRAINT admin_alert_messages_present CHECK (length(message_en) >= 5 AND length(message_bn) >= 3),
  CONSTRAINT admin_alert_ack_consistent CHECK ((acknowledged_at IS NULL) = (acknowledged_by IS NULL))
);

COMMENT ON TABLE core.admin_alert IS
  'Things an administrator must see now: break-glass access, chain verification failures. Acknowledged, never deleted.';

CREATE INDEX admin_alert_open ON core.admin_alert (facility_id, created_at DESC)
  WHERE acknowledged_at IS NULL;

-- ---------------------------------------------------------------------------
-- Privileges and the invariant
-- ---------------------------------------------------------------------------

-- ledger.audit_event and its partitions: the schema's default privileges already give the
-- application INSERT and SELECT only, and core.assert_ledger_append_only() checks every
-- table in ledger, partitions included. These two are in core and need saying.
REVOKE DELETE ON core.break_glass_access FROM dthcms_app;
REVOKE DELETE ON core.admin_alert        FROM dthcms_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_audit_trail_kept() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  -- The chain table must exist and be partitioned; a plain table here would mean a
  -- migration replaced it without thinking about retention.
  IF NOT EXISTS (
    SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'ledger' AND c.relname = 'audit_event' AND c.relkind = 'p') THEN
    RAISE EXCEPTION 'ledger.audit_event is missing or is not partitioned';
  END IF;

  SELECT string_agg(t, ', ') INTO offending
  FROM unnest(ARRAY['core.break_glass_access', 'core.admin_alert']) AS t
  WHERE has_table_privilege('dthcms_app', t, 'DELETE');

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'the application may delete %; break-glass accesses and alerts are evidence', offending;
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_audit_trail_kept() IS
  'Raises unless the audit chain is partitioned and break-glass rows and alerts cannot be deleted by the application.';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_audit_trail_kept', 'the security audit trail, break-glass records and alerts are kept', 65)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name = 'assert_audit_trail_kept';
DROP FUNCTION IF EXISTS core.assert_audit_trail_kept();
DROP TABLE IF EXISTS core.admin_alert;
DROP TABLE IF EXISTS core.break_glass_access;
DROP FUNCTION IF EXISTS ledger.ensure_audit_partitions(integer);
DROP TABLE IF EXISTS ledger.audit_event;
