-- Consent (CP36). The application reads templates and never writes them: publishing legal
-- wording is an administrative act, not something a request handler does.

-- name: ActiveConsentTemplate :one
SELECT id, consent_type, version, language, title, body, body_digest, status, effective_from
  FROM core.consent_template
 WHERE consent_type = $1 AND language = $2 AND status = 'active';

-- name: ActiveConsentTemplates :many
SELECT id, consent_type, version, language, title, body, body_digest, status, effective_from
  FROM core.consent_template
 WHERE language = $1 AND status = 'active'
 ORDER BY consent_type;

-- name: ConsentTemplateVersion :one
SELECT id, consent_type, version, language, title, body, body_digest, status, effective_from
  FROM core.consent_template
 WHERE consent_type = $1 AND language = $2 AND version = $3;

-- name: PatientConsents :many
SELECT patient_id, consent_type, status, template_version, language, capture_method,
       evidence_key, paper_reference, witnessed_by_code, granted_for_relation, granted_for_name,
       granted_at, granted_by_code, revoked_at, revoked_by_code, revoke_reason
  FROM read.patient_consent
 WHERE patient_id = $1 AND facility_id = $2
 ORDER BY consent_type;

-- name: PatientConsent :one
SELECT patient_id, consent_type, status, template_version, language, capture_method,
       evidence_key, paper_reference, witnessed_by_code, granted_for_relation, granted_for_name,
       granted_at, granted_by_code, revoked_at, revoked_by_code, revoke_reason
  FROM read.patient_consent
 WHERE patient_id = $1 AND facility_id = $2 AND consent_type = $3;

-- name: PatientConsentHistory :many
SELECT consent_type, action, template_version, language, capture_method, reason,
       requested_by, actor_code, occurred_at, event_id
  FROM read.patient_consent_event
 WHERE patient_id = $1 AND facility_id = $2
 ORDER BY occurred_at DESC, id DESC;
