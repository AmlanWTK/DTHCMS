-- The clinical event ledger (CP23, blueprint §7): the single write path and the source
-- of truth for everything clinical. Every observation, diagnosis, prescription and
-- correction is a row here first; every screen is a projection of these rows (CP25).
--
-- Four structural properties, each enforced by the database rather than by discipline:
--
--   * append-only three ways over: the application role has INSERT and SELECT only; a
--     rule on the parent turns UPDATE and DELETE into nothing; a row trigger on every
--     partition raises on either, so a statement aimed at a partition directly is refused
--     too. core.assert_event_store_immutable() checks all three on every start;
--   * every event has a gapless, server-assigned sequence within its aggregate and a
--     monotonic global sequence for replay (§7.6). Uniqueness of event_id (idempotency,
--     §7.5) and of (aggregate, sequence) lives in ledger.event_key, a plain table beside
--     the partitioned one, because a unique constraint on a partitioned table must include
--     the partition key and these two must not;
--   * every event is a link in its aggregate's hash chain, and every day's events are
--     folded into a global anchor that links to the day before (§7.2). Tamper-evident by
--     mathematics, not by policy;
--   * partitioned by month from the first row (§9.4), with a default partition so a
--     forgotten monthly chore loses nothing and the verifier can say how many rows it
--     caught.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The ledger
-- ---------------------------------------------------------------------------

CREATE SEQUENCE ledger.event_global_seq AS bigint;

CREATE TABLE ledger.event (
  -- Server-assigned, monotonic, for replay and change capture. Carries no causal meaning
  -- across aggregates (§7.6) and the code must never pretend it does.
  global_seq       bigint      NOT NULL DEFAULT nextval('ledger.event_global_seq'),
  -- Client-generated UUIDv7: the idempotency key (§7.5). Unique through ledger.event_key.
  event_id         uuid        NOT NULL,

  aggregate_type   text        NOT NULL,
  aggregate_id     uuid        NOT NULL,
  -- Gapless per aggregate; the only ordering the domain relies on.
  sequence         bigint      NOT NULL,

  -- Denormalised: every clinical event is patient-scoped; a visit is optional.
  patient_id       uuid,
  visit_id         uuid,

  event_type       text        NOT NULL,
  event_version    smallint    NOT NULL DEFAULT 1,

  -- When it happened (the client's clock, clinically meaningful) and when the server
  -- accepted it (authoritative). Both kept, on purpose (§7.2).
  occurred_at      timestamptz NOT NULL,
  recorded_at      timestamptz NOT NULL DEFAULT now(),

  -- The attribution envelope [R-03]: who, on which device, wearing which hat, at which
  -- station, at which facility. Every one required; an append without any of them is
  -- refused before it reaches this table and by this table.
  actor_user_id    uuid        NOT NULL,
  actor_device_id  uuid        NOT NULL,
  actor_role       text        NOT NULL,
  actor_station    text,
  facility_id      uuid        NOT NULL,

  source           text        NOT NULL,

  payload          jsonb       NOT NULL,
  previous         jsonb,
  correction       jsonb,
  metadata         jsonb       NOT NULL DEFAULT '{}'::jsonb,

  -- The chain: prev_hash is the hash of the previous event in this aggregate (32 zero
  -- bytes for sequence 1); hash covers every field above and prev_hash, in the canonical
  -- form internal/eventstore hashes.
  prev_hash        bytea       NOT NULL,
  hash             bytea       NOT NULL,

  PRIMARY KEY (global_seq, recorded_at),
  CONSTRAINT event_sequence_positive CHECK (sequence >= 1),
  CONSTRAINT event_version_positive  CHECK (event_version >= 1),
  CONSTRAINT event_type_shape        CHECK (event_type ~ '^[A-Z][A-Z0-9_]+$'),
  CONSTRAINT event_aggregate_shape   CHECK (aggregate_type ~ '^[A-Z][A-Z_]+$'),
  CONSTRAINT event_role_present      CHECK (length(actor_role) > 0),
  CONSTRAINT event_source_known      CHECK (source IN ('MOBILE_ONLINE', 'MOBILE_OFFLINE_SYNC', 'WEB', 'OCR', 'FIELD', 'SYSTEM')),
  CONSTRAINT event_payload_object    CHECK (jsonb_typeof(payload) = 'object'),
  CONSTRAINT event_metadata_object   CHECK (jsonb_typeof(metadata) = 'object'),
  CONSTRAINT event_hash_length       CHECK (length(hash) = 32 AND length(prev_hash) = 32),
  -- A correction names what it corrects and why; an event that carries one must carry all
  -- of it (§7.7).
  CONSTRAINT event_correction_complete CHECK (
    correction IS NULL OR (
      correction ? 'corrects_event_id' AND correction ? 'reason_code' AND correction ? 'reason_text'))
) PARTITION BY RANGE (recorded_at);

COMMENT ON TABLE ledger.event IS
  'The clinical event ledger: the source of truth. Append-only by grant, rule and trigger; hash-chained per aggregate; partitioned by month.';

-- The queries of §9.3, and the verifier's walk.
CREATE INDEX event_patient_time   ON ledger.event (patient_id, occurred_at DESC) WHERE patient_id IS NOT NULL;
CREATE INDEX event_visit          ON ledger.event (visit_id) WHERE visit_id IS NOT NULL;
CREATE INDEX event_type_time      ON ledger.event (event_type, recorded_at DESC);
CREATE INDEX event_actor_time     ON ledger.event (actor_user_id, recorded_at DESC);
CREATE INDEX event_aggregate_seq  ON ledger.event (aggregate_type, aggregate_id, sequence);
CREATE INDEX event_payload_paths  ON ledger.event USING GIN (payload jsonb_path_ops);

-- Uniqueness that a partitioned table cannot promise on its own: one row per event_id
-- (idempotency) and one row per (aggregate, sequence) (gapless, no duplicates). Written
-- in the same transaction as the event; the chain head for an aggregate is read here.
CREATE TABLE ledger.event_key (
  event_id       uuid        PRIMARY KEY,
  aggregate_type text        NOT NULL,
  aggregate_id   uuid        NOT NULL,
  sequence       bigint      NOT NULL,
  global_seq     bigint      NOT NULL,
  recorded_at    timestamptz NOT NULL,
  hash           bytea       NOT NULL,
  facility_id    uuid        NOT NULL,
  CONSTRAINT event_key_one_per_sequence UNIQUE (aggregate_type, aggregate_id, sequence),
  CONSTRAINT event_key_one_per_global   UNIQUE (global_seq)
);

COMMENT ON TABLE ledger.event_key IS
  'One row per event: enforces event_id and (aggregate, sequence) uniqueness across partitions and holds each aggregate''s chain head.';

CREATE INDEX event_key_aggregate_head ON ledger.event_key (aggregate_type, aggregate_id, sequence DESC);

-- ---------------------------------------------------------------------------
-- Append-only, three ways
-- ---------------------------------------------------------------------------

-- The rules of §7.4: an UPDATE or DELETE against the parent does nothing at all.
CREATE RULE event_no_update AS ON UPDATE TO ledger.event DO INSTEAD NOTHING;
CREATE RULE event_no_delete AS ON DELETE TO ledger.event DO INSTEAD NOTHING;
CREATE RULE event_key_no_update AS ON UPDATE TO ledger.event_key DO INSTEAD NOTHING;
CREATE RULE event_key_no_delete AS ON DELETE TO ledger.event_key DO INSTEAD NOTHING;

-- A rule on the parent does not see a statement aimed at a partition by name. A row
-- trigger on the parent is cloned to every partition, present and future, and raises.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger.refuse_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'the clinical ledger is append-only: % on % is refused', TG_OP, TG_TABLE_NAME
    USING HINT = 'Corrections are new events (CORRECTION_APPLIED, *_CORRECTED); nothing is ever rewritten.';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER event_immutable
  BEFORE UPDATE OR DELETE ON ledger.event
  FOR EACH ROW EXECUTE FUNCTION ledger.refuse_mutation();

CREATE TRIGGER event_key_immutable
  BEFORE UPDATE OR DELETE ON ledger.event_key
  FOR EACH ROW EXECUTE FUNCTION ledger.refuse_mutation();

-- ---------------------------------------------------------------------------
-- Partitions
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger.ensure_event_partitions(months_ahead integer DEFAULT 12)
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
    name := format('event_%s', to_char(m, 'YYYY_MM'));
    IF to_regclass(format('ledger.%I', name)) IS NULL THEN
      EXECUTE format(
        'CREATE TABLE ledger.%I PARTITION OF ledger.event FOR VALUES FROM (%L) TO (%L)',
        name, m, (m + interval '1 month')::date);
      created := created + 1;
    END IF;
  END LOOP;
  RETURN created;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION ledger.ensure_event_partitions(integer) IS
  'Creates the monthly event partitions for the next n months. Idempotent; run by the migrator.';

CREATE TABLE ledger.event_default PARTITION OF ledger.event DEFAULT;
SELECT ledger.ensure_event_partitions(15);

-- ---------------------------------------------------------------------------
-- The daily anchor
-- ---------------------------------------------------------------------------

-- One row per clinic day: the fold of every event hash of that day, in global order,
-- chained to the previous day's anchor. Tampering inside a day breaks that aggregate's
-- chain; removing a whole aggregate breaks the day's anchor; the two together make the
-- ledger a single tamper-evident object rather than many. Written by the anchor job,
-- never rewritten: a day whose events change would need a different anchor, and that is
-- the point.
CREATE TABLE ledger.chain_anchor (
  day              date        PRIMARY KEY,
  facility_id      uuid        NOT NULL,
  event_count      bigint      NOT NULL,
  first_global_seq bigint,
  last_global_seq  bigint,
  prev_anchor      bytea       NOT NULL,
  anchor           bytea       NOT NULL,
  computed_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chain_anchor_hash_length CHECK (length(anchor) = 32 AND length(prev_anchor) = 32),
  CONSTRAINT chain_anchor_count_nonnegative CHECK (event_count >= 0)
);

COMMENT ON TABLE ledger.chain_anchor IS
  'Daily global anchor over the event ledger: SHA-256 fold of the day''s event hashes, chained to the previous day.';

CREATE RULE chain_anchor_no_update AS ON UPDATE TO ledger.chain_anchor DO INSTEAD NOTHING;
CREATE RULE chain_anchor_no_delete AS ON DELETE TO ledger.chain_anchor DO INSTEAD NOTHING;

-- ---------------------------------------------------------------------------
-- Snapshots — created, unused (§7.9)
-- ---------------------------------------------------------------------------

CREATE TABLE ledger.aggregate_snapshot (
  aggregate_type text        NOT NULL,
  aggregate_id   uuid        NOT NULL,
  -- The sequence the state is as of. Replay resumes from sequence + 1.
  sequence       bigint      NOT NULL,
  facility_id    uuid        NOT NULL,
  state          jsonb       NOT NULL,
  taken_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (aggregate_type, aggregate_id, sequence)
);

COMMENT ON TABLE ledger.aggregate_snapshot IS
  'Aggregate state as of a sequence, for replay to resume from. Created at CP23 and unused until a measured need appears (§7.9).';

-- ---------------------------------------------------------------------------
-- Privileges and the invariant
-- ---------------------------------------------------------------------------

-- The schema's default privileges already give the application INSERT and SELECT only,
-- and core.assert_ledger_append_only() checks every table in ledger. The sequence needs
-- saying: the default for ledger sequences is USAGE, SELECT, which is what nextval needs.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_event_store_immutable() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  missing text;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'ledger' AND c.relname = 'event' AND c.relkind = 'p') THEN
    RAISE EXCEPTION 'ledger.event is missing or is not partitioned';
  END IF;

  -- The rules of §7.4, by name.
  SELECT string_agg(r, ', ') INTO missing
  FROM unnest(ARRAY['event_no_update', 'event_no_delete', 'event_key_no_update', 'event_key_no_delete',
                    'chain_anchor_no_update', 'chain_anchor_no_delete']) AS r
  WHERE NOT EXISTS (SELECT 1 FROM pg_rules WHERE schemaname = 'ledger' AND rulename = r);
  IF missing IS NOT NULL THEN
    RAISE EXCEPTION 'the ledger''s append-only rules are missing: %', missing;
  END IF;

  -- The trigger, on the parent and therefore on every partition.
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'ledger' AND c.relname = 'event' AND t.tgname = 'event_immutable' AND NOT t.tgisinternal) THEN
    RAISE EXCEPTION 'ledger.event has lost its immutability trigger';
  END IF;

  -- And the grant, which assert_ledger_append_only also checks; said here so this
  -- function is a complete statement of the guarantee.
  IF has_table_privilege('dthcms_app', 'ledger.event', 'UPDATE')
     OR has_table_privilege('dthcms_app', 'ledger.event', 'DELETE')
     OR has_table_privilege('dthcms_app', 'ledger.event_key', 'UPDATE')
     OR has_table_privilege('dthcms_app', 'ledger.event_key', 'DELETE') THEN
    RAISE EXCEPTION 'the application role may rewrite the clinical ledger';
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_event_store_immutable() IS
  'Raises unless the event ledger is partitioned, carries its append-only rules and trigger, and is unwritable except by INSERT for the application role.';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_event_store_immutable', 'the clinical event ledger cannot be rewritten: grant, rule and trigger all hold', 12)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name = 'assert_event_store_immutable';
DROP FUNCTION IF EXISTS core.assert_event_store_immutable();
DROP TABLE IF EXISTS ledger.aggregate_snapshot;
DROP TABLE IF EXISTS ledger.chain_anchor;
DROP FUNCTION IF EXISTS ledger.ensure_event_partitions(integer);
DROP TABLE IF EXISTS ledger.event_key;
DROP TABLE IF EXISTS ledger.event;
DROP FUNCTION IF EXISTS ledger.refuse_mutation();
DROP SEQUENCE IF EXISTS ledger.event_global_seq;
