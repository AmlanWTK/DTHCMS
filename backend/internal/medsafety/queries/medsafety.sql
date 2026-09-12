-- The medication safety rule library (CP77).
--
-- Two statements carry the checkpoint, and both are about time rather than about rules.
--
-- `RulesetAt` is criterion 3: a check run in March must be reproducible in December. It asks
-- which version of each rule was live at an instant, from the version periods, which is the same
-- shape as CP75's price-as-of query and for the same reason — "the current version" is a query
-- that cannot be made to answer it however carefully it is written.
--
-- `PublishRuleVersion` and `CloseRuleVersion` are the pair that maintains those periods. They run
-- in one transaction: the predecessor's period closes at the instant the successor's opens, so
-- there is no instant with two live versions and none with zero. The EXCLUDE constraint is what
-- makes that a guarantee rather than an intention.

-- name: AllergenGroups :many
SELECT g.code, g.name_en, g.name_bn, g.notes_en, g.notes_bn, g.source_citation,
       g.origin, g.approved_at, g.approved_by, g.is_active,
       (SELECT count(*) FROM core.allergen_group_member m
         WHERE m.group_code = g.code)::bigint AS member_count
  FROM core.allergen_group g
 ORDER BY g.code;

-- name: AllergenGroupMembers :many
SELECT group_code, match_kind, match_value, source_citation
  FROM core.allergen_group_member
 ORDER BY group_code, match_kind, match_value;

-- name: AllergenCrossReactions :many
SELECT id, from_group, to_group, risk, note_en, note_bn, source_citation,
       origin, approved_at, approved_by, is_active
  FROM core.allergen_cross_reaction
 ORDER BY from_group, to_group;

-- name: ApproveAllergenCrossReaction :exec
-- A seeded cross-reaction becoming the clinic's own. Same shape as approving a rule: a person
-- and an instant, together or not at all.
UPDATE core.allergen_cross_reaction
   SET approved_by = @approved_by::uuid, approved_at = @approved_at::timestamptz,
       updated_by = @approved_by::uuid
 WHERE id = @id::uuid AND approved_at IS NULL;

-- name: ApproveAllergenGroup :exec
UPDATE core.allergen_group
   SET approved_by = @approved_by::uuid, approved_at = @approved_at::timestamptz,
       updated_by = @approved_by::uuid
 WHERE code = @code::text AND approved_at IS NULL;

-- name: ListMedicationRules :many
-- The library screen, in one statement.
--
-- Each row carries the highest version number and the published one, because the list has to say
-- in a glance whether a rule is live and whether there is unpublished work on it — and a screen
-- that fetched the versions per row would be 40 round trips for the seeded set alone.
SELECT r.id, r.code, r.rule_type, r.is_active, r.withdrawn_at, r.withdrawn_reason, r.created_at,
       -- Coalesced, all of them. `assert_every_medication_rule_has_its_versions` makes a rule
       -- with no versions impossible, but a scalar subquery that can return NULL generates a
       -- non-nullable Go type here and fails to scan at the moment the invariant is ever
       -- breached — which is the worst possible moment to discover it. `published_version` 0
       -- means nothing is live, which is a real and common state.
       coalesce((SELECT max(v.version) FROM core.medication_rule_version v
                  WHERE v.rule_id = r.id), 0)::int AS latest_version,
       coalesce((SELECT v.version FROM core.medication_rule_version v
                  WHERE v.rule_id = r.id AND v.status = 'PUBLISHED'), 0)::int AS published_version,
       coalesce((SELECT v.name_en FROM core.medication_rule_version v
                  WHERE v.rule_id = r.id ORDER BY v.version DESC LIMIT 1), '')::text AS latest_name_en,
       coalesce((SELECT v.name_bn FROM core.medication_rule_version v
                  WHERE v.rule_id = r.id ORDER BY v.version DESC LIMIT 1), '')::text AS latest_name_bn,
       coalesce((SELECT v.severity FROM core.medication_rule_version v
                  WHERE v.rule_id = r.id ORDER BY v.version DESC LIMIT 1), '')::text AS latest_severity,
       coalesce((SELECT v.origin FROM core.medication_rule_version v
                  WHERE v.rule_id = r.id ORDER BY v.version DESC LIMIT 1), '')::text AS latest_origin,
       count(*) OVER ()::bigint AS total_count
  FROM core.medication_rule r
 WHERE r.facility_id = @facility_id::uuid
   AND (@p_type::text = '' OR r.rule_type = @p_type::text)
   -- "Show me what nobody has agreed to yet" is the working list this checkpoint creates, so it
   -- is a filter on the same endpoint rather than a separate report that could disagree with it.
   AND (NOT @p_unapproved_only::boolean
        OR NOT EXISTS (SELECT 1 FROM core.medication_rule_version v
                        WHERE v.rule_id = r.id AND v.approved_at IS NOT NULL))
   AND (NOT @p_active_only::boolean OR r.is_active)
 ORDER BY r.rule_type, r.code
 LIMIT @p_limit::int OFFSET @p_offset::int;

-- name: MedicationRuleByID :one
SELECT r.id, r.facility_id, r.code, r.rule_type, r.is_active,
       r.withdrawn_at, r.withdrawn_reason, r.created_at
  FROM core.medication_rule r
 WHERE r.id = @id::uuid AND r.facility_id = @facility_id::uuid;

-- name: MedicationRuleByCode :one
SELECT r.id, r.facility_id, r.code, r.rule_type, r.is_active,
       r.withdrawn_at, r.withdrawn_reason, r.created_at
  FROM core.medication_rule r
 WHERE r.code = @code::text AND r.facility_id = @facility_id::uuid;

-- name: MedicationRuleVersions :many
SELECT v.id, v.rule_id, v.version, v.severity, v.name_en, v.name_bn,
       v.message_en, v.message_bn, v.advice_en, v.advice_bn, v.condition,
       v.source_citation, v.origin, v.status,
       v.authored_by, v.authored_at, v.approved_by, v.approved_at,
       v.effective_from, v.effective_to, v.notes,
       author.employee_code AS authored_code,
       approver.employee_code AS approved_code
  FROM core.medication_rule_version v
  LEFT JOIN core.app_user author ON author.id = v.authored_by
  LEFT JOIN core.app_user approver ON approver.id = v.approved_by
 WHERE v.rule_id = @rule_id::uuid
 ORDER BY v.version DESC;

-- name: MedicationRuleVersion :one
SELECT v.id, v.rule_id, v.version, v.severity, v.name_en, v.name_bn,
       v.message_en, v.message_bn, v.advice_en, v.advice_bn, v.condition,
       v.source_citation, v.origin, v.status,
       v.authored_by, v.authored_at, v.approved_by, v.approved_at,
       v.effective_from, v.effective_to, v.notes,
       author.employee_code AS authored_code,
       approver.employee_code AS approved_code
  FROM core.medication_rule_version v
  LEFT JOIN core.app_user author ON author.id = v.authored_by
  LEFT JOIN core.app_user approver ON approver.id = v.approved_by
  JOIN core.medication_rule r ON r.id = v.rule_id
 WHERE v.id = @id::uuid AND r.facility_id = @facility_id::uuid;

-- name: InsertMedicationRule :one
INSERT INTO core.medication_rule (facility_id, code, rule_type, created_by, updated_by)
VALUES (@facility_id::uuid, @code::text, @rule_type::text,
        @actor_id::uuid, @actor_id::uuid)
RETURNING id;

-- name: NextMedicationRuleVersion :one
SELECT coalesce(max(version), 0)::int + 1 FROM core.medication_rule_version
 WHERE rule_id = @rule_id::uuid;

-- name: InsertMedicationRuleVersion :one
-- Always a DRAFT, and always unapproved. There is no argument to this statement that could
-- produce a live version: publishing is `PublishRuleVersion`, which is a separate act with a
-- separate permission and a step-up in front of it.
INSERT INTO core.medication_rule_version
  (rule_id, version, severity, name_en, name_bn, message_en, message_bn,
   advice_en, advice_bn, condition, source_citation, origin, status,
   authored_by, notes, updated_by)
VALUES
  (@rule_id::uuid, @version::int, @severity::text, @name_en::text, @name_bn::text,
   @message_en::text, @message_bn::text, @advice_en::text, @advice_bn::text,
   @condition::jsonb, @source_citation::text, @origin::text, 'DRAFT',
   @actor_id::uuid, @notes::text, @actor_id::uuid)
RETURNING id;

-- name: UpdateMedicationRuleDraft :exec
-- Edits a draft. The trigger refuses this on a published version, so the WHERE clause is belt
-- rather than braces — but it is here so that an attempt answers "not found" rather than an
-- exception with a database message in it.
UPDATE core.medication_rule_version
   SET severity = @severity::text,
       name_en = @name_en::text, name_bn = @name_bn::text,
       message_en = @message_en::text, message_bn = @message_bn::text,
       advice_en = @advice_en::text, advice_bn = @advice_bn::text,
       condition = @condition::jsonb,
       source_citation = @source_citation::text,
       notes = @notes::text,
       updated_by = @actor_id::uuid
 WHERE id = @id::uuid AND status = 'DRAFT';

-- name: CloseMedicationRuleVersion :exec
-- Retires whatever is live, at the instant its successor takes over. Half-open, so the successor
-- is the live one from that instant and there is no gap and no overlap.
UPDATE core.medication_rule_version
   SET effective_to = @at::timestamptz,
       status = @status::text,
       updated_by = @actor_id::uuid
 WHERE rule_id = @rule_id::uuid AND status = 'PUBLISHED';

-- name: PublishMedicationRuleVersion :one
-- **The approval.** Sets the approver, the instant, and the period's start together, because the
-- constraints refuse any two of the three without the other.
UPDATE core.medication_rule_version
   SET status = 'PUBLISHED',
       approved_by = @actor_id::uuid,
       approved_at = @at::timestamptz,
       effective_from = @at::timestamptz,
       updated_by = @actor_id::uuid
 WHERE id = @id::uuid AND status = 'DRAFT'
RETURNING id, version, effective_from;

-- name: WithdrawMedicationRule :exec
UPDATE core.medication_rule
   SET is_active = false, withdrawn_at = @at::timestamptz,
       withdrawn_reason = @reason::text, withdrawn_by = @actor_id::uuid,
       updated_by = @actor_id::uuid
 WHERE id = @id::uuid AND facility_id = @facility_id::uuid AND is_active;

-- name: RulesetAt :many
-- **Criterion 3.** Which version of each rule was the live one at an instant.
--
-- `effective_from <= at` is the half that carries it. The obvious query — the newest published
-- version of each rule — returns a version published this morning for a check run last March,
-- and looks entirely correct while doing it. The `EXCLUDE` constraint on the table guarantees at
-- most one row per rule satisfies this, which is why there is no DISTINCT ON and no LIMIT: a
-- second answer would mean the constraint had failed, and the Go layer would rather find that
-- out than quietly take the first row.
--
-- A DRAFT cannot appear here at all. Not because it is filtered — because a draft has no
-- `effective_from`, so there is no instant at which it was live. That is the whole mechanism
-- behind "a rule nobody approved cannot fire".
SELECT r.id AS rule_id, r.code, r.rule_type, r.is_active,
       v.id AS version_id, v.version, v.severity,
       v.name_en, v.name_bn, v.message_en, v.message_bn,
       v.advice_en, v.advice_bn, v.condition, v.source_citation,
       v.origin, v.status, v.approved_by, v.approved_at,
       v.effective_from, v.effective_to
  FROM core.medication_rule r
  JOIN core.medication_rule_version v ON v.rule_id = r.id
 WHERE r.facility_id = @facility_id::uuid
   AND v.effective_from IS NOT NULL
   AND v.effective_from <= @at::timestamptz
   AND (v.effective_to IS NULL OR v.effective_to > @at::timestamptz)
 ORDER BY r.rule_type, r.code;

-- ---------------------------------------------------------------------------
-- Renal dosing (CP79)
-- ---------------------------------------------------------------------------

-- name: FacilityRenalPolicy :one
-- The eGFR recency window this facility uses, and whether anybody has approved it.
--
-- One row per facility, guaranteed by `assert_every_facility_has_a_renal_window`. There is no
-- COALESCE to a constant here on purpose: a missing row is a database that failed its own
-- invariant, and answering it with a silent default would hide exactly that.
SELECT facility_id, egfr_recency_months, source_citation, origin,
       approved_at, approved_by, notes_en, notes_bn
  FROM core.facility_renal_policy
 WHERE facility_id = @facility_id::uuid;

-- name: GenericRenalDependence :many
-- Which molecules cannot be prescribed without knowing the kidney function.
--
-- Every classified molecule, keyed by the generic's name, because that is what a rule condition
-- names and what an item resolves to. **Molecules with no row are absent from this result and
-- that is the point** — the engine reports them unclassified rather than reading silence as
-- "does not need one".
SELECT g.name AS generic_name, d.dependence, d.reason_en, d.reason_bn,
       d.source_citation, d.approved_at IS NOT NULL AS is_approved
  FROM core.generic_renal_dependence d
  JOIN core.generic g ON g.id = d.generic_id
 ORDER BY g.name;
