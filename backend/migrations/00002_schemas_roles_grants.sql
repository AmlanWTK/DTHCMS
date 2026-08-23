-- Schemas, roles and grants.
--
-- This is the most consequential migration in DTHCMS, and it runs before any clinical
-- table exists — deliberately. Blueprint section 4.1 states that the event log, not the
-- current-state table, is the source of truth, and section 4.3 that a correction never
-- overwrites the original. Those are guarantees about what the software may do.
--
-- Guarantees enforced only by application code hold until someone writes a quick fix at
-- the end of a long day. So they are enforced here instead: the application's database
-- role is granted INSERT and SELECT on the ledger and nothing else. An UPDATE against
-- the event log does not fail code review — it fails at the database, in every
-- environment, including the one where somebody is debugging in a psql session.

-- +goose Up

-- +goose StatementBegin
DO $$
BEGIN
  RAISE NOTICE 'Creating DTHCMS schemas, roles and grants';
END
$$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- Schemas
-- ---------------------------------------------------------------------------

-- Reference and identity data: users, roles, devices, facilities, templates,
-- the formulary, clinical rule content. Ordinary versioned CRUD.
CREATE SCHEMA IF NOT EXISTS core;

-- The append-only event ledger. The clinical source of truth.
CREATE SCHEMA IF NOT EXISTS ledger;

-- Projections built from the ledger. Everything here is derived and rebuildable;
-- nothing here is authoritative.
CREATE SCHEMA IF NOT EXISTS read;

-- Operational data: background jobs, metrics rollups, throughput, cost.
CREATE SCHEMA IF NOT EXISTS ops;

-- Metadata about stored documents. The bytes live in object storage.
CREATE SCHEMA IF NOT EXISTS docs;

-- Anonymised research marts. No join path back to core (implementation plan 9.8).
CREATE SCHEMA IF NOT EXISTS research;

COMMENT ON SCHEMA core     IS 'Reference and identity data. Versioned CRUD with audit columns.';
COMMENT ON SCHEMA ledger   IS 'Append-only clinical event ledger. The source of truth. No UPDATE, no DELETE.';
COMMENT ON SCHEMA read     IS 'Projections derived from the ledger. Rebuildable; never authoritative.';
COMMENT ON SCHEMA ops      IS 'Operational data: jobs, metrics, rollups.';
COMMENT ON SCHEMA docs     IS 'Document metadata. Bytes live in object storage.';
COMMENT ON SCHEMA research IS 'Anonymised research data. No identifiers, no join path to core.';

-- ---------------------------------------------------------------------------
-- Roles
--
-- Group roles, holding privileges only: NOLOGIN, no passwords. Login users are granted
-- membership — by the platform in production, and by the migrate binary's dev-roles step
-- locally. Keeping privileges separate from credentials means this migration is
-- identical in every environment and contains no secret.
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dthcms_app') THEN
    CREATE ROLE dthcms_app NOLOGIN;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dthcms_projector') THEN
    CREATE ROLE dthcms_projector NOLOGIN;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dthcms_research') THEN
    CREATE ROLE dthcms_research NOLOGIN;
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON ROLE dthcms_app       IS 'The application. Appends to the ledger; may never modify it.';
COMMENT ON ROLE dthcms_projector IS 'The projection engine. Reads the ledger; owns the read models.';
COMMENT ON ROLE dthcms_research  IS 'Research and analytics. Sees anonymised data only.';

-- ---------------------------------------------------------------------------
-- Grants: the application role
-- ---------------------------------------------------------------------------

GRANT USAGE ON SCHEMA core, ledger, read, ops, docs TO dthcms_app;

-- Ordinary read/write where ordinary CRUD is correct.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA core, ops, docs TO dthcms_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA core, ops, docs
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO dthcms_app;

-- The ledger: append and read. Nothing else, now or in future.
GRANT SELECT, INSERT ON ALL TABLES IN SCHEMA ledger TO dthcms_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA ledger
  GRANT SELECT, INSERT ON TABLES TO dthcms_app;

-- Read models are derived. The application reads them; only the projector writes them.
-- This is what stops a "quick fix" from correcting a projection directly and leaving it
-- silently inconsistent with the events it is supposed to be derived from.
GRANT SELECT ON ALL TABLES IN SCHEMA read TO dthcms_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA read
  GRANT SELECT ON TABLES TO dthcms_app;

GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA core, ledger, ops, docs TO dthcms_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA core, ledger, ops, docs
  GRANT USAGE, SELECT ON SEQUENCES TO dthcms_app;

-- ---------------------------------------------------------------------------
-- Grants: the projection engine
-- ---------------------------------------------------------------------------

GRANT USAGE ON SCHEMA core, ledger, read, ops TO dthcms_projector;

-- Read-only on core. A projection almost always needs reference data the event does not
-- carry — the display name of a template, the facility's timezone, the label of a
-- station — and denying it would only mean the same data being copied into the ledger
-- to get around the restriction. Read-only, because core is the application's to write.
GRANT SELECT ON ALL TABLES IN SCHEMA core TO dthcms_projector;
ALTER DEFAULT PRIVILEGES IN SCHEMA core
  GRANT SELECT ON TABLES TO dthcms_projector;

GRANT SELECT ON ALL TABLES IN SCHEMA ledger TO dthcms_projector;
ALTER DEFAULT PRIVILEGES IN SCHEMA ledger
  GRANT SELECT ON TABLES TO dthcms_projector;

GRANT SELECT, INSERT, UPDATE, DELETE, TRUNCATE ON ALL TABLES IN SCHEMA read TO dthcms_projector;
ALTER DEFAULT PRIVILEGES IN SCHEMA read
  GRANT SELECT, INSERT, UPDATE, DELETE, TRUNCATE ON TABLES TO dthcms_projector;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA ops TO dthcms_projector;
ALTER DEFAULT PRIVILEGES IN SCHEMA ops
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO dthcms_projector;

GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA read, ops TO dthcms_projector;
ALTER DEFAULT PRIVILEGES IN SCHEMA read, ops
  GRANT USAGE, SELECT ON SEQUENCES TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- Grants: research
--
-- Research sees the anonymised marts and nothing else. Not a policy, not a code review
-- convention — an absence of privilege. A notebook connected with this role cannot read
-- a patient's name because the rows are not reachable from it at all.
-- ---------------------------------------------------------------------------

GRANT USAGE ON SCHEMA research TO dthcms_research;

GRANT SELECT ON ALL TABLES IN SCHEMA research TO dthcms_research;
ALTER DEFAULT PRIVILEGES IN SCHEMA research
  GRANT SELECT ON TABLES TO dthcms_research;

-- ---------------------------------------------------------------------------
-- Revoke the defaults PostgreSQL is generous with
-- ---------------------------------------------------------------------------

REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;

-- +goose Down

DROP SCHEMA IF EXISTS research CASCADE;
DROP SCHEMA IF EXISTS docs CASCADE;
DROP SCHEMA IF EXISTS ops CASCADE;
DROP SCHEMA IF EXISTS read CASCADE;
DROP SCHEMA IF EXISTS ledger CASCADE;
DROP SCHEMA IF EXISTS core CASCADE;

-- Roles are cluster-wide and may be shared; dropping them is a deliberate manual act.
