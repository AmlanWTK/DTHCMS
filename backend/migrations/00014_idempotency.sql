-- Idempotency records (CP24, blueprint §7.5 layer 2): the response a retried request gets
-- back instead of a second write.
--
-- Three layers protect a retry (§7.5). The first is `event_id`, which the ledger already
-- enforces: replaying an event writes nothing and returns the original (CP23). This is the
-- second — the HTTP layer, so that a client that retried after a timeout receives the same
-- response body rather than a duplicate-key error it has to interpret. The third is
-- job-level keying, which arrives with the job runner.
--
-- **Why `ops` and not `ledger`.** The plan's §9.1 table lists `idempotency_records` under
-- `ledger`. It cannot live there: the record is written before the handler runs and
-- completed after it, and it is deleted when it expires — an UPDATE and a DELETE that the
-- ledger's append-only grant forbids by design (ADR-0008), and that
-- core.assert_ledger_append_only() would refuse on every start. A cache of HTTP responses
-- is operational data with a TTL, not a fact of history, so it belongs in `ops` with the
-- other operational tables. Nothing is lost: what must be immutable is the event, and the
-- event is in the ledger.

-- +goose Up

CREATE TABLE ops.idempotency_record (
  facility_id  uuid        NOT NULL REFERENCES core.facility(id),
  -- Scoped to the person, so one operator's key can never hand another operator's response
  -- back. A key is the client's to choose and carries no meaning here.
  user_id      uuid        NOT NULL REFERENCES core.app_user(id),
  key          text        NOT NULL,

  -- SHA-256 of method, path and body. The same key with a different request is a client
  -- bug (or an attack), and answering it with the first request's response would be worse
  -- than refusing: 409, and the fingerprint is what detects it.
  fingerprint  bytea       NOT NULL,

  -- in_progress: claimed, the handler is running. complete: the response is stored.
  state        text        NOT NULL DEFAULT 'in_progress',

  status       integer,
  headers      jsonb       NOT NULL DEFAULT '{}'::jsonb,
  body         bytea,

  claimed_at   timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  expires_at   timestamptz NOT NULL,

  PRIMARY KEY (user_id, key),
  CONSTRAINT idempotency_key_shape CHECK (length(btrim(key)) BETWEEN 8 AND 200),
  CONSTRAINT idempotency_state_known CHECK (state IN ('in_progress', 'complete')),
  CONSTRAINT idempotency_fingerprint_length CHECK (length(fingerprint) = 32),
  CONSTRAINT idempotency_complete_has_response CHECK (
    state <> 'complete' OR (status IS NOT NULL AND completed_at IS NOT NULL)),
  CONSTRAINT idempotency_expiry_after_claim CHECK (expires_at > claimed_at)
);

COMMENT ON TABLE ops.idempotency_record IS
  'Cached responses for retried mutating requests, keyed per user (CP24, §7.5 layer 2). Operational, with a TTL — not a fact of history.';

-- The cleanup job's query, and nothing else scans this table.
CREATE INDEX idempotency_expiry ON ops.idempotency_record (expires_at);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ops.purge_expired_idempotency(cutoff timestamptz DEFAULT now())
RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
  removed integer;
BEGIN
  DELETE FROM ops.idempotency_record WHERE expires_at <= cutoff;
  GET DIAGNOSTICS removed = ROW_COUNT;
  RETURN removed;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION ops.purge_expired_idempotency(timestamptz) IS
  'Deletes idempotency records past their expiry. Run by the cleanup job; safe to run at any time.';

-- +goose Down

DROP FUNCTION IF EXISTS ops.purge_expired_idempotency(timestamptz);
DROP TABLE IF EXISTS ops.idempotency_record;
