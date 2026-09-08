-- The AI gateway (CP70, §10.3).
--
-- # The query that is deliberately absent
--
-- There is no `UPDATE core.ai_prompt_version`. A prompt version is the provenance of every
-- interaction that names it, and rewriting one silently rewrites what the system was doing eight
-- months ago. The application's grant refuses the update as well, so this is a rule rather than a
-- habit: a changed prompt is a new version, always.

-- name: AIAgents :many
-- The registered agents. §10.1's technology column is here so a reader can see at a glance that
-- not everything the blueprint calls an agent goes near a model.
SELECT agent_code, technology, description_en, description_bn
  FROM core.ai_agent
 ORDER BY agent_code;

-- name: AIModels :many
-- Pinned versions and their prices, in micro-dollars per million tokens (D-13, D-14).
SELECT model, model_version, provider,
       input_micro_usd_per_million, output_micro_usd_per_million,
       description_en, description_bn, retired_at
  FROM core.ai_model
 ORDER BY model_version;

-- name: AIPromptVersions :many
-- Everything deployed, newest version of each agent first. The content is returned with it: a
-- reviewer opening the registry is opening it to read the prompt, and a screen that made them ask
-- again would be a screen built for the shape of the API.
SELECT agent_code, version, major, minor, patch,
       model_version, fallback_model_version,
       content_sha256, content, changelog, deployed_at
  FROM core.ai_prompt_version
 ORDER BY agent_code, major DESC, minor DESC, patch DESC;

-- name: AIPromptVersion :one
SELECT agent_code, version, major, minor, patch,
       model_version, fallback_model_version,
       content_sha256, content, changelog, deployed_at
  FROM core.ai_prompt_version
 WHERE agent_code = $1 AND version = $2;

-- name: DeployAIPromptVersion :one
-- Publish one version of one prompt, once.
--
-- `ON CONFLICT DO NOTHING` rather than an upsert, and the empty result is the interesting case: it
-- means this version number is already deployed. The caller then re-reads the stored row and
-- compares content hashes, because "already deployed with the same text" is start-up succeeding
-- and "already deployed with different text" is the one change that would make every stored
-- interaction unreproducible. An upsert here would have made the second silently become the first.
INSERT INTO core.ai_prompt_version (
  agent_code, version, major, minor, patch,
  model_version, fallback_model_version, content_sha256, content, changelog, deployed_at)
VALUES (
  sqlc.arg(agent_code)::text, sqlc.arg(version)::text,
  sqlc.arg(major)::integer, sqlc.arg(minor)::integer, sqlc.arg(patch)::integer,
  sqlc.arg(model_version)::text, sqlc.narg(fallback_model_version)::text,
  sqlc.arg(content_sha256)::text, sqlc.arg(content)::text, sqlc.arg(changelog)::text,
  sqlc.arg(now)::timestamptz)
ON CONFLICT (agent_code, version) DO NOTHING
RETURNING agent_code, version, content_sha256;

-- name: IsSyntheticSubject :one
-- The whole of criterion 1b's provenance decision, and note what it does *not* take: nothing from
-- the request. A subject is fabricated because somebody entered it in the register, or it is real.
SELECT EXISTS (SELECT 1 FROM core.ai_synthetic_subject WHERE subject_id = $1)::boolean;

-- name: RegisterSyntheticSubject :exec
-- Entering a subject in the register is the act that makes the free tier reachable for it. The
-- reason column is twenty characters minimum by constraint, because the value of the register is
-- that every row is something a reviewer can read.
INSERT INTO core.ai_synthetic_subject (subject_id, facility_id, reason, registered_by, registered_at)
VALUES (sqlc.arg(subject_id)::uuid, sqlc.arg(facility_id)::uuid, sqlc.arg(reason)::text,
        sqlc.narg(registered_by)::uuid, sqlc.arg(now)::timestamptz)
ON CONFLICT (subject_id) DO NOTHING;

-- name: BeginAIInteraction :one
-- The record, written **before** the provider is contacted.
--
-- That order is the point. Criterion 2 says every call is recorded; a row written after the answer
-- comes back records only the calls that came back, which excludes precisely the ones somebody
-- wants to see. And because the outbound column carries a constraint refusing anything that names
-- a person, a payload that cannot be recorded is a payload that is never sent — the check is not
-- merely alongside the call, it is in front of it.
--
-- The same query writes a terminal row: a refusal and a cache hit are finished the moment they are
-- created, so `status` and `finished_at` are arguments rather than a second statement.
INSERT INTO core.ai_interaction (
  id, facility_id, agent_code, prompt_version, model_version, requested_model_version,
  tier, provenance, subject_patient_id, subject_pseudonym, status,
  outbound, response, refusal_detail, input_sha256,
  input_tokens, output_tokens, cost_micro_usd, latency_ms, attempts,
  output_valid, used_fallback, started_at, finished_at)
VALUES (
  sqlc.arg(id)::uuid, sqlc.arg(facility_id)::uuid, sqlc.arg(agent_code)::text,
  sqlc.narg(prompt_version)::text, sqlc.narg(model_version)::text,
  sqlc.narg(requested_model_version)::text,
  sqlc.arg(tier)::text, sqlc.arg(provenance)::text,
  sqlc.narg(subject_patient_id)::uuid, sqlc.arg(subject_pseudonym)::text,
  sqlc.arg(status)::text,
  sqlc.narg(outbound)::jsonb, sqlc.narg(response)::jsonb,
  sqlc.arg(refusal_detail)::text, sqlc.arg(input_sha256)::text,
  sqlc.arg(input_tokens)::integer, sqlc.arg(output_tokens)::integer,
  sqlc.arg(cost_micro_usd)::bigint, sqlc.arg(latency_ms)::integer, sqlc.arg(attempts)::integer,
  sqlc.narg(output_valid)::boolean, sqlc.arg(used_fallback)::boolean,
  sqlc.arg(started_at)::timestamptz, sqlc.narg(finished_at)::timestamptz)
RETURNING id, started_at;

-- name: FinishAIInteraction :exec
-- What the provider said, and what it cost. Only ever applied to a row this process opened.
UPDATE core.ai_interaction
   SET status = sqlc.arg(status)::text,
       model_version = coalesce(sqlc.narg(model_version)::text, model_version),
       prompt_version = coalesce(sqlc.narg(prompt_version)::text, prompt_version),
       response = sqlc.narg(response)::jsonb,
       refusal_detail = sqlc.arg(refusal_detail)::text,
       input_tokens = sqlc.arg(input_tokens)::integer,
       output_tokens = sqlc.arg(output_tokens)::integer,
       cost_micro_usd = sqlc.arg(cost_micro_usd)::bigint,
       latency_ms = sqlc.arg(latency_ms)::integer,
       attempts = sqlc.arg(attempts)::integer,
       output_valid = sqlc.narg(output_valid)::boolean,
       used_fallback = sqlc.arg(used_fallback)::boolean,
       finished_at = sqlc.arg(finished_at)::timestamptz
 WHERE id = sqlc.arg(id)::uuid;

-- name: CachedAIResponse :one
-- §10.3 step 4: *"identical input never re-billed"*.
--
-- Keyed on the hash of the **minimised** payload together with the prompt version — which pins the
-- model version, because changing the model means editing the prompt file and therefore bumping its
-- version. A prompt edit or a model change therefore misses the cache rather than serving an answer
-- produced by something else. It reads the interaction table rather than a cache of its own, which buys two
-- things: the cached answer is the audited answer, and there is no second store that can disagree
-- with the record.
SELECT id, response, prompt_version, model_version, input_tokens, output_tokens, started_at
  FROM core.ai_interaction
 WHERE input_sha256 = sqlc.arg(input_sha256)::text
   AND status = 'SUCCEEDED'
   AND response IS NOT NULL
   AND started_at >= sqlc.arg(not_before)::timestamptz
 ORDER BY started_at DESC
 LIMIT 1;

-- name: AIDailySpend :many
-- What each agent has spent today, and the total. One row per agent plus a NULL-agent row for the
-- deployment, produced by GROUPING SETS so that the two budgets are answered by one query and
-- cannot disagree about what "today" means.
-- `coalesce(agent_code, '')` rather than the bare column: GROUPING SETS produces a NULL agent for
-- the deployment-wide total, and '' is the same spelling `core.ai_budget_alert` uses for that row,
-- so the two halves of the metering agree about how to name "everything together". A real agent
-- code can never be empty — the catalogue's format constraint refuses it — so the two cannot
-- collide.
SELECT coalesce(agent_code, '')::text AS agent_code,
       count(*)::bigint AS calls,
       coalesce(sum(cost_micro_usd), 0)::bigint AS spend_micro_usd,
       coalesce(sum(input_tokens), 0)::bigint AS input_tokens,
       coalesce(sum(output_tokens), 0)::bigint AS output_tokens
  FROM core.ai_interaction
 WHERE facility_id = sqlc.arg(facility_id)::uuid
   AND started_at >= sqlc.arg(day_start)::timestamptz
   AND started_at <  sqlc.arg(day_end)::timestamptz
 GROUP BY GROUPING SETS ((agent_code), ())
 ORDER BY agent_code NULLS FIRST;

-- name: AIBudgets :many
-- The configured limits. The row whose agent is null is the deployment-wide one — the budget that
-- catches a runaway in an agent nobody was watching.
SELECT facility_id, agent_code, daily_micro_usd, thresholds
  FROM core.ai_budget
 WHERE facility_id = $1
 ORDER BY agent_code NULLS FIRST;

-- name: RaiseAIBudgetAlert :one
-- Fire once, per threshold, per agent, per day.
--
-- The unique key is the mechanism rather than bookkeeping about it: whether a row was inserted is
-- what decides whether anybody is told. Without it the 80% alert fires on every call for the rest
-- of the day, and an alert that fires four hundred times is one somebody turns off — which is how
-- a clinic ends up with no alerting at all on the day it matters.
INSERT INTO core.ai_budget_alert (
  facility_id, agent_code, day, threshold_percent, spend_micro_usd, budget_micro_usd, raised_at)
VALUES (sqlc.arg(facility_id)::uuid, sqlc.arg(agent_code)::text, sqlc.arg(day)::date,
        sqlc.arg(threshold_percent)::integer, sqlc.arg(spend_micro_usd)::bigint,
        sqlc.arg(budget_micro_usd)::bigint, sqlc.arg(now)::timestamptz)
ON CONFLICT (facility_id, agent_code, day, threshold_percent) DO NOTHING
RETURNING facility_id, agent_code, day, threshold_percent, spend_micro_usd, budget_micro_usd, raised_at;

-- name: AIBudgetAlerts :many
-- Which thresholds have been crossed, most recent first. The operator screen's second half.
SELECT facility_id, agent_code, day, threshold_percent, spend_micro_usd, budget_micro_usd, raised_at
  FROM core.ai_budget_alert
 WHERE facility_id = sqlc.arg(facility_id)::uuid
   AND day >= sqlc.arg(since)::date
 ORDER BY raised_at DESC
 LIMIT sqlc.arg(row_limit)::integer;

-- name: AIInteractions :many
-- The human-reviewable outbound log (the plan's own mitigation for its headline risk).
--
-- The payload is **not** in the list. It is in the detail view, one row at a time, because a list
-- endpoint returning two hundred clinical payloads is a bulk export of the clinic's caseload
-- wearing an operational screen's clothes.
SELECT id, agent_code, prompt_version, model_version, requested_model_version,
       tier, provenance, subject_patient_id, subject_pseudonym, status,
       refusal_detail, input_sha256,
       input_tokens, output_tokens, cost_micro_usd, latency_ms, attempts,
       output_valid, used_fallback, started_at, finished_at
  FROM core.ai_interaction
 WHERE facility_id = sqlc.arg(facility_id)::uuid
   AND (sqlc.narg(agent_code)::text IS NULL OR agent_code = sqlc.narg(agent_code)::text)
   AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
   AND started_at >= sqlc.arg(since)::timestamptz
 ORDER BY started_at DESC
 LIMIT sqlc.arg(row_limit)::integer;

-- name: AIInteractionByID :one
-- One call, with what was sent and what came back. This is the screen the manual verification step
-- opens to confirm that no name, national ID, phone number or address is in the payload.
SELECT id, agent_code, prompt_version, model_version, requested_model_version,
       tier, provenance, subject_patient_id, subject_pseudonym, status,
       outbound, response, refusal_detail, input_sha256,
       input_tokens, output_tokens, cost_micro_usd, latency_ms, attempts,
       output_valid, used_fallback, started_at, finished_at
  FROM core.ai_interaction
 WHERE id = sqlc.arg(id)::uuid AND facility_id = sqlc.arg(facility_id)::uuid;

-- name: PIIPatterns :many
-- The database's copy of the free-text scrubber's rules, so a Go test can compare it against the
-- compiled Go copy in both directions — and, more usefully, run the same fixture corpus through
-- both engines and fail when they disagree. Two regular-expression engines agreeing about a list
-- is not something to assume.
SELECT kind, pattern, replacement, description_en, description_bn
  FROM ops.pii_pattern
 ORDER BY kind;

-- name: TextCarriesIdentifier :one
-- The database's answer for one string, so the cross-engine agreement test can ask it directly
-- rather than inferring it from a refused insert.
SELECT ops.text_carries_identifier(sqlc.arg(candidate)::text)::boolean;

-- name: PayloadCarriesIdentifier :one
-- The same, for a whole document. Used by the test that proves the Go minimiser and the database
-- constraint refuse the same payloads: the constraint is the backstop, and a backstop that is
-- narrower than the thing it backs is not one.
SELECT ops.carries_identifier(sqlc.arg(payload)::jsonb)::boolean;
