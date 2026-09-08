-- Two grants the role every service actually connects as was missing.
--
-- # How both were missed, which matters more than either of them
--
-- Every database test in this repository connects as the schema **owner**, which locally is a
-- superuser. The API connects as `dthcms_app`. So the whole suite can be green against a
-- database in which the application cannot register a patient or record a blood pressure, and
-- it was: neither of the defects below is subtle, and both survived sixty-five checkpoints,
-- because nothing in CI has ever executed a query as the role the deployment uses.
--
-- That is the same shape of gap `cmd/devseed` was written for — a database that is right, and
-- a person (here, a role) that cannot use it — and it is worth naming rather than fixing
-- quietly, because the class will produce another one.
--
-- Both were found by cmd/synthload, which is the first thing that has ever driven the domain
-- services against a database as `dthcms_app`.

-- +goose Up

-- ---------------------------------------------------------------------------
-- 1. The extensions the application was given but could not reach (from 00002)
-- ---------------------------------------------------------------------------
--
-- 00001 installs pg_trgm because patient search and duplicate detection are built on it:
-- `name_en % $1` is how a registration desk finds "Md. Abdur Rahim" after typing "abdul
-- rohim", and the same operator is what CP30's matcher scores a probable duplicate with.
--
-- 00002 then ends with `REVOKE ALL ON SCHEMA public FROM PUBLIC`, which is right — PostgreSQL
-- is generous with that schema by default and nothing should be able to create objects in it.
-- But `ALL` includes USAGE, and pg_trgm's operators live in `public`, so `dthcms_app` could
-- not resolve `%` or `similarity()` at all. Both paths that need them failed outright with
-- `operator does not exist: text % text`:
--
--   POST /v1/patients   every registration, because the duplicate check runs on all of them
--   GET  /v1/patients   every search
--
-- USAGE and nothing more. CREATE stays revoked, from `dthcms_app` as from PUBLIC, so the
-- application still cannot put anything in `public` — which was the point of 00002's revoke
-- and is untouched here.
--
-- Only `dthcms_app`. `dthcms_projector` writes read models and never evaluates a trigram
-- operator itself — index maintenance happens inside the server and is not privilege-checked
-- against the writing role — and `dthcms_research` reads the anonymised schema, where there is
-- no such index and no such query. A grant given to a role with no use for it is a grant
-- nobody can later argue about removing.

GRANT USAGE ON SCHEMA public TO dthcms_app;

COMMENT ON SCHEMA public IS
  'Extensions only. No application object lives here; dthcms_app holds USAGE so it can reach pg_trgm, and CREATE is revoked from everyone.';

-- ---------------------------------------------------------------------------
-- 2. The synchronous projection the application had to call and could not (from 00026)
-- ---------------------------------------------------------------------------
--
-- 00015 states the rule this depends on: `dthcms_app` may not write anything in `read`, and a
-- *synchronous* projection therefore runs as a SECURITY DEFINER `read.apply_…` function that
-- the application is allowed to **call** and nothing else. Six such functions were granted
-- that way — `apply_visit_vital`, `apply_patient_registered`, `apply_timeline` and the rest —
-- each with the same two lines: revoke from PUBLIC, grant to `dthcms_app, dthcms_projector`.
--
-- 00026 wrote the revoke and then granted to `dthcms_projector` alone. `Observation` is a
-- synchronous projection (see internal/projection/observation.go), so every single
-- OBSERVATION_RECORDED append by the API failed inside its own transaction with
-- `permission denied for function apply_observation` — which is to say that no vital sign, no
-- laboratory result and no derived value could be recorded at all.
--
-- It is one grant rather than a family of them because `apply_observation` is the only one
-- with the defect: it is also the only function after 00022 whose migration remembered to
-- revoke PUBLIC's default EXECUTE, and the others are reachable by the application only
-- because they are still reachable by everybody. That is a separate and much smaller problem
-- — `dthcms_research` can call `read.apply_history_item_recorded` today — and it is left
-- alone here deliberately: closing it means revoking and re-granting twenty functions whose
-- callers this change is not in a position to re-test. It is written down so that it is
-- somebody's next task rather than nobody's.

GRANT EXECUTE ON FUNCTION read.apply_observation(jsonb) TO dthcms_app;

-- +goose Down

REVOKE EXECUTE ON FUNCTION read.apply_observation(jsonb) FROM dthcms_app;
REVOKE USAGE ON SCHEMA public FROM dthcms_app;
COMMENT ON SCHEMA public IS 'standard public schema';
