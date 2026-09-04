-- The allergy hard stop (CP54).

-- name: AllergyReactions :many
-- The reaction vocabulary. Short on purpose: a list nobody can hold in their head is one
-- people pick the first item from. `is_emergency` is stored rather than inferred from the
-- severity somebody ticked — anaphylaxis is severe whatever the operator chose.
SELECT reaction, display_en, display_bn, is_emergency, ordering
  FROM core.allergy_reaction ORDER BY ordering;

-- name: AllergyStatus :one
-- One function, one answer. The gate on the queue calls the same one, so "does this patient
-- have allergy status" cannot be answered two ways.
SELECT core.allergy_status($1)::text AS status;

-- name: AllergiesForPatient :many
-- What this patient reacts to. Withdrawn ones are absent: an allergy somebody took back is in
-- the ledger, and a header that showed it would be a warning nobody can act on.
--
-- The substance's words are joined from the catalogue rather than copied onto the row, so a
-- corrected term reads correctly on every allergy coded with it. `is_emergency` comes with the
-- reaction because a header has to be able to lead with the ones that stop a heart.
SELECT a.id, a.patient_id,
       a.code_system, a.code_version, a.code,
       coalesce(c.display_en, '') AS display_en,
       coalesce(c.display_bn, '') AS display_bn,
       a.said,
       a.reaction, r.display_en AS reaction_en, r.display_bn AS reaction_bn, r.is_emergency,
       a.severity, a.certainty, a.note,
       a.recorded_at, a.recorded_by, a.recorded_role, a.recorded_visit
  FROM read.allergy a
  JOIN core.allergy_reaction r ON r.reaction = a.reaction
  LEFT JOIN core.terminology_concept c
    ON c.system = a.code_system AND c.version = a.code_version AND c.code = a.code
 WHERE a.patient_id = $1 AND a.removed_at IS NULL
 -- Worst first. A header showing three allergies shows the one that stops a heart at the top,
 -- because a list read in recording order buries it under a rash from 1998.
 ORDER BY r.is_emergency DESC,
          CASE a.severity WHEN 'life_threatening' THEN 0 WHEN 'severe' THEN 1
                          WHEN 'moderate' THEN 2 ELSE 3 END,
          a.recorded_at;

-- name: AllergyByID :one
SELECT id, patient_id, facility_id, removed_at FROM read.allergy WHERE id = $1;

-- name: LiveAssertionForPatient :one
-- The current assertion, if there is one. At most one is live at a time — a new one supersedes
-- the old in the same statement that writes it.
SELECT id, patient_id, kind, reason, asserted_at, asserted_by, asserted_role, asserted_visit
  FROM read.allergy_assertion
 WHERE patient_id = $1 AND withdrawn_at IS NULL
 ORDER BY asserted_at DESC, global_seq DESC
 LIMIT 1;

-- name: AllergyAssertionByID :one
SELECT id, patient_id, facility_id, withdrawn_at FROM read.allergy_assertion WHERE id = $1;

-- name: AllergyHistoryForPatient :many
-- Everything ever said about this patient's allergies, withdrawn entries included, newest
-- first. The plan asks for "allergy change history", and the reason it needs one is that an
-- allergy that was recorded and then taken back is a clinical event: somebody believed it,
-- and somebody else disagreed, and both are worth reading before prescribing.
SELECT 'ALLERGY'::text AS kind, a.id, a.said, a.code, a.reaction, a.severity, a.certainty,
       a.recorded_at AS at, a.recorded_by AS by_user, a.recorded_role AS by_role,
       a.removed_at AS undone_at, a.removed_by AS undone_by, a.removed_reason AS undone_reason
  FROM read.allergy a WHERE a.patient_id = $1
UNION ALL
SELECT s.kind, s.id, ''::text, NULL::text, ''::text, ''::text, s.reason,
       s.asserted_at, s.asserted_by, s.asserted_role,
       s.withdrawn_at, s.withdrawn_by, s.withdrawn_reason
  FROM read.allergy_assertion s WHERE s.patient_id = $1
 ORDER BY at DESC;

-- name: NoKnownAllergyRateByOperator :many
-- The plan's own mitigation for the risk it names: operators asserting NKA reflexively to
-- clear the gate. It is a query rather than a project because the index is there.
--
-- Not a judgement. A registration officer whose patients genuinely have no allergies will sit
-- near the top, and so will one who taps the button without asking — which is exactly why this
-- belongs in front of a QA officer rather than in an automatic rule.
SELECT asserted_by,
       count(*) FILTER (WHERE kind = 'NO_KNOWN_ALLERGY') AS no_known,
       count(*) FILTER (WHERE kind = 'UNABLE_TO_ASSESS') AS unable,
       count(*) AS asserted
  FROM read.allergy_assertion
 WHERE facility_id = $1 AND asserted_at >= $2 AND asserted_at < $3
 GROUP BY asserted_by
 ORDER BY no_known DESC, asserted_by;
