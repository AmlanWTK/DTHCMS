-- DTHCMS local database initialisation (CP04).
--
-- Runs once, when the Postgres volume is first created. Its only job is to install the
-- extensions the application needs, so that local and production behave identically —
-- a query that works here must work there.
--
-- Schemas, roles and grants are NOT created here. They belong to the migration
-- framework at CP06, so that the database is built the same way in every environment
-- rather than partly by a container init script.

-- Deterministic UUIDs, digests and crypto helpers.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Trigram matching. Used for patient search across Bangla and English names, for
-- duplicate detection, and for the formulary autocomplete, where two typed characters
-- must return ranked results in well under 50 ms.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Exclusion constraints over ranges — needed later for scheduling and validity periods.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Query statistics, so a slow query can be found rather than guessed at.
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

DO $$
BEGIN
  RAISE NOTICE 'DTHCMS: extensions installed (pgcrypto, pg_trgm, btree_gist, pg_stat_statements)';
  RAISE NOTICE 'DTHCMS: schemas, roles and grants are created by migrations at CP06';
END
$$;
