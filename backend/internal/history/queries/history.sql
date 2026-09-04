-- Medical history (CP53).

-- name: HistoryKinds :many
-- The six kinds and what each one needs. Reference data a station app fetches once and then
-- renders the right fields from — which is what stops a screen asking for a family relation on
-- a complaint, or forgetting to ask for one on a family history.
SELECT kind, display_en, display_bn, code_system,
       requires_relation, requires_duration, allows_severity, allows_onset,
       is_medication, ordering
  FROM core.history_kind
 ORDER BY ordering;

-- name: FamilyRelations :many
-- Who a family history is about, with the degree. First-degree family history is a risk
-- factor with a number attached; second-degree is context, and a query that had to enumerate
-- which is which would be a clinical rule living in a WHERE clause somebody copies wrong.
SELECT relation, display_en, display_bn, degree, ordering
  FROM core.family_relation
 ORDER BY ordering;

-- name: HistoryForPatient :many
-- Everything currently believed about this patient, in the order station 4 asks it.
--
-- Removed items are absent: an item somebody removed is one somebody said should not have
-- been recorded, and it belongs in the ledger rather than on the screen. RESOLVED items are
-- present, because "she had this and no longer does" is a clinical fact and a list that hid
-- it would make every follow-up look like a first visit.
--
-- The concept's displays are joined rather than copied onto the item. A catalogue title that
-- was corrected — a spelling, a better Bangla term — should read correctly on every item
-- coded with it, and a copy would leave the old words on every row recorded before the fix.
-- What is *not* joined is `said`: that is what the patient told this officer on this day, and
-- it is the item's own.
SELECT i.id, i.patient_id, i.kind,
       i.code_system, i.code_version, i.code,
       coalesce(c.display_en, '') AS display_en,
       coalesce(c.display_bn, '') AS display_bn,
       coalesce(c.heading, '')    AS heading,
       coalesce(c.heading_bn, '') AS heading_bn,
       i.said,
       i.relation, i.duration_days, i.severity, i.onset_on, i.onset_precision,
       i.dose, i.frequency, i.formulary_product_id, i.reconciliation,
       i.status,
       i.recorded_at, i.recorded_by, i.recorded_role, i.recorded_visit,
       i.confirmed_at, i.confirmed_by, i.confirmed_visit,
       i.amended_at, i.amended_by
  FROM read.history_item i
  LEFT JOIN core.terminology_concept c
    ON c.system = i.code_system AND c.version = i.code_version AND c.code = i.code
  JOIN core.history_kind k ON k.kind = i.kind
 WHERE i.patient_id = $1 AND i.removed_at IS NULL
 ORDER BY k.ordering, i.recorded_at, i.id;

-- name: HistoryItem :one
SELECT i.id, i.patient_id, i.facility_id, i.kind,
       i.code_system, i.code_version, i.code,
       coalesce(c.display_en, '') AS display_en,
       coalesce(c.display_bn, '') AS display_bn,
       coalesce(c.heading, '')    AS heading,
       coalesce(c.heading_bn, '') AS heading_bn,
       i.said,
       i.relation, i.duration_days, i.severity, i.onset_on, i.onset_precision,
       i.dose, i.frequency, i.formulary_product_id, i.reconciliation,
       i.status,
       i.recorded_at, i.recorded_by, i.recorded_role, i.recorded_visit,
       i.confirmed_at, i.confirmed_by, i.confirmed_visit,
       i.amended_at, i.amended_by,
       i.removed_at, i.removed_by, i.removed_reason
  FROM read.history_item i
  LEFT JOIN core.terminology_concept c
    ON c.system = i.code_system AND c.version = i.code_version AND c.code = i.code
 WHERE i.id = $1;

-- name: HistoryKind :one
SELECT kind, display_en, display_bn, code_system,
       requires_relation, requires_duration, allows_severity, allows_onset,
       is_medication, ordering
  FROM core.history_kind WHERE kind = $1;

-- name: UncodedHistoryCount :many
-- How much of the history has no code, by kind.
--
-- Criterion 1 says complaints and comorbidities are coded rather than free text, and the
-- escape hatch for a concept the catalogue lacks is what keeps that rule from being one people
-- work around. This is the number that keeps the hatch honest: it is the list of concepts
-- somebody should add, and if it grows the catalogue is wrong rather than the officers.
SELECT kind, count(*) AS uncoded
  FROM read.history_item
 WHERE facility_id = $1 AND removed_at IS NULL AND code IS NULL
 GROUP BY kind
 ORDER BY kind;
