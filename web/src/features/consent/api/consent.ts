import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * Consent, typed against the contract (CP36).
 *
 * Two things here are not obvious from the types.
 *
 * The **template version is never sent**. The server looks up the wording in force and puts
 * the version, the language and the digest of the exact text into the event. A client that
 * could name the version it consented against could record a consent to words the patient
 * never saw.
 *
 * `revoke` is a POST to a sub-resource rather than a DELETE, because nothing is deleted: the
 * grant stays and a revocation is recorded beside it. Both are needed to answer whether a
 * message sent in March was lawful when it was sent.
 */

export type ConsentTemplate = components['schemas']['ConsentTemplate'];
export type PatientConsent = components['schemas']['PatientConsent'];
export type ConsentHistoryEntry = components['schemas']['ConsentHistoryEntry'];
export type ConsentType = ConsentTemplate['consent_type'];
export type CaptureMethod = NonNullable<
  components['schemas']['ConsentGrantRequest']['capture_method']
>;

/** The five, in the order a capture screen should offer them. */
export const CONSENT_TYPES: ConsentType[] = [
  'care',
  'communication',
  'research',
  'ai_processing',
  'outreach',
];

/** The methods that need an image, and the ones that need a witness. */
export const NEEDS_EVIDENCE: CaptureMethod[] = ['signature', 'thumbprint'];
export const NEEDS_WITNESS: CaptureMethod[] = ['thumbprint', 'verbal_attested'];

export function newEventId(): string {
  return crypto.randomUUID();
}

export async function listConsents(patientId: string): Promise<PatientConsent[]> {
  const result = await unwrap(
    api.GET('/v1/patients/{id}/consents', { params: { path: { id: patientId } } }),
  );
  return result.consents;
}

export async function consentHistory(patientId: string): Promise<ConsentHistoryEntry[]> {
  const result = await unwrap(
    api.GET('/v1/patients/{id}/consents/history', { params: { path: { id: patientId } } }),
  );
  return result.entries;
}

export async function consentTemplates(language: 'en' | 'bn'): Promise<ConsentTemplate[]> {
  const result = await unwrap(
    api.GET('/v1/consent-templates', { params: { query: { language } } }),
  );
  return result.templates;
}

export async function grantConsent(
  patientId: string,
  input: {
    consent_type: ConsentType;
    language: 'en' | 'bn';
    capture_method: CaptureMethod;
    evidence_key?: string;
    evidence_sha256?: string;
    paper_reference?: string;
    witnessed_by?: string;
    granted_for_relation?: string;
    granted_for_name?: string;
  },
): Promise<PatientConsent> {
  const result = await unwrap(
    api.POST('/v1/patients/{id}/consents', {
      params: { ...writing(), path: { id: patientId } },
      body: { event_id: newEventId(), ...input },
    }),
  );
  return result.consent;
}

export async function revokeConsent(
  patientId: string,
  consentType: ConsentType,
  input: { reason?: string; requested_by?: 'patient' | 'guardian' | 'clinic' } = {},
): Promise<PatientConsent> {
  const result = await unwrap(
    api.POST('/v1/patients/{id}/consents/{type}/revoke', {
      params: { ...writing(), path: { id: patientId, type: consentType } },
      body: { event_id: newEventId(), requested_by: 'patient', ...input },
    }),
  );
  return result.consent;
}

/** Where to put a signature image. The key is the server's; the bytes never reach the API. */
export async function evidenceUploadURL(
  patientId: string,
): Promise<{ object_key: string; upload_url: string; expires_at: string }> {
  return unwrap(
    api.POST('/v1/patients/{id}/consents/evidence-url', {
      params: { ...writing(), path: { id: patientId } },
      body: { content_type: 'image/png' },
    }),
  );
}
