-- Extensions DTHCMS depends on.
--
-- These are also installed by the local container's init script, but a database is not
-- guaranteed to have been built that way — a restored backup, a managed instance, a
-- colleague's machine. Migrations are the one place every environment agrees on.

-- +goose Up

-- Digests and cryptographic helpers. Used for identifier hashing (national ID is stored
-- hashed, never in the clear) and for the event ledger's hash chain.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Trigram matching. Patient search across Bangla and English names, duplicate detection,
-- and the formulary autocomplete, where two typed characters must return ranked results
-- in well under 50 ms.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Exclusion constraints over ranges: scheduling, validity periods, and preventing
-- overlapping records where overlap is clinically meaningless.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- +goose Down

-- Deliberately empty. Dropping an extension would cascade to every object using it.
-- Extensions are additive infrastructure; removing one is a manual, considered act.
SELECT 1;
