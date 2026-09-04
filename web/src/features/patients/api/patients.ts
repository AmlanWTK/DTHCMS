import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { STEP_UP_HEADER } from '@/features/auth';
import { api, unwrap } from '@/lib/api';

/**
 * The patient module's calls, typed against the contract (CP29, CP30).
 *
 * Two things here are not obvious from the types.
 *
 * `checkDuplicates` is a POST that reads. The body carries a name, a telephone number and
 * a date of birth, and personal data does not belong in a URL — so it is a POST, and it
 * carries a fresh idempotency key like every other POST under `/v1`. Fresh, because the
 * desk calls it repeatedly as it types and reusing a key across different bodies is what
 * a 409 means.
 *
 * `mergePatients` needs a step-up. Two clinical histories become one, and the change is
 * irreversible in effect however well the decision is recorded.
 */

export type Patient = components['schemas']['Patient'];
export type PatientRegistration = components['schemas']['PatientRegistration'];
export type DuplicateMatch = components['schemas']['DuplicateMatch'];
export type DuplicateCandidate = components['schemas']['DuplicateCandidate'];
export type PatientMergeRecord = components['schemas']['PatientMergeRecord'];

/** What the duplicate check needs, which is a subset of a registration. */
export type DuplicateProbe = Omit<PatientRegistration, 'event_id'>;
export type MergeDecision = components['schemas']['PatientMergeRequest']['decision'];

export function newEventId(): string {
  return crypto.randomUUID();
}

export async function getPatient(id: string): Promise<Patient> {
  const result = await unwrap(api.GET('/v1/patients/{id}', { params: { path: { id } } }));
  return result.patient;
}

export async function registerPatient(
  registration: PatientRegistration,
): Promise<{ patient: Patient; duplicate: boolean }> {
  const result = await unwrap(api.POST('/v1/patients', { params: writing(), body: registration }));
  return { patient: result.patient, duplicate: result.duplicate };
}

export function checkDuplicates(probe: DuplicateProbe): Promise<DuplicateMatch> {
  return unwrap(
    api.POST('/v1/patients/check-duplicates', {
      params: writing(),
      body: { ...probe, event_id: newEventId() },
    }),
  );
}

export async function listMerges(id: string): Promise<PatientMergeRecord[]> {
  const result = await unwrap(api.GET('/v1/patients/{id}/merges', { params: { path: { id } } }));
  return result.merges;
}

export async function mergePatients(
  stepUpToken: string,
  survivorId: string,
  input: { merged_id: string; score: number; decision: MergeDecision; justification: string },
): Promise<Patient> {
  const result = await unwrap(
    api.POST('/v1/patients/{id}/merge', {
      params: { ...writing({ [STEP_UP_HEADER]: stepUpToken }), path: { id: survivorId } },
      body: { ...input, event_id: newEventId() },
    }),
  );
  return result.survivor;
}

/**
 * The shortest justification the server will take.
 *
 * Mirrored here so the button can be disabled rather than the request refused — but the
 * server has the last word, because a rule enforced only in a browser is not a rule.
 */
export const JUSTIFICATION_MIN = 10;

export function justificationAcceptable(text: string): boolean {
  return text.trim().length >= JUSTIFICATION_MIN;
}

// --- photographs (CP34) ---

export type PhotoUploadTicket = components['schemas']['PhotoUploadTicket'];
export type PatientPhotoRecord = components['schemas']['PatientPhoto'];

export function uploadTicket(id: string, contentType: string): Promise<PhotoUploadTicket> {
  return unwrap(
    api.POST('/v1/patients/{id}/photo/upload-url', {
      params: { ...writing(), path: { id } },
      body: { content_type: contentType as 'image/jpeg' },
    }),
  );
}

export async function attachPhoto(
  id: string,
  input: { object_key: string; content_type: string; width: number; height: number },
): Promise<PatientPhotoRecord> {
  const result = await unwrap(
    api.POST('/v1/patients/{id}/photo', {
      params: { ...writing(), path: { id } },
      body: {
        event_id: newEventId(),
        object_key: input.object_key,
        content_type: input.content_type as 'image/jpeg',
        width: input.width,
        height: input.height,
      },
    }),
  );
  return result.photo;
}

export function photoURL(id: string): Promise<{ url: string; expires_at: string }> {
  return unwrap(api.GET('/v1/patients/{id}/photo', { params: { path: { id } } }));
}

// --- corrections (CP35) ---

export type PatientCorrection = components['schemas']['PatientCorrection'];
export type FieldChange = components['schemas']['FieldChange'];
export type DerivedDependency = components['schemas']['DerivedDependency'];
export type CorrectionRequest = components['schemas']['PatientCorrectionRequest'];

/** What one correction did, as the server reports it. */
export type CorrectionApplied = {
  patient: Patient;
  changes: FieldChange[];
  high_impact: boolean;
  invalidated: DerivedDependency[];
  event_id: string;
};

/**
 * The fields a correction to which also needs a step-up.
 *
 * Mirrored from the server so the screen can *ask for the code before submitting* rather
 * than submit, be refused, and ask afterwards — which loses the typing on a tablet. The
 * server still decides; this list only decides what the browser asks for first.
 */
export const HIGH_IMPACT_FIELDS = ['name_en', 'sex', 'birth_date', 'dob_precision'] as const;

export type CorrectableField = keyof Omit<CorrectionRequest, 'event_id' | 'reason'>;

export function isHighImpact(fields: readonly string[]): boolean {
  return fields.some((field) => (HIGH_IMPACT_FIELDS as readonly string[]).includes(field));
}

/** The shortest reason the server will take. `minLength: 10` in the contract. */
export const REASON_MIN = 10;

export function reasonAcceptable(text: string): boolean {
  return text.trim().length >= REASON_MIN;
}

export async function correctPatient(
  id: string,
  body: CorrectionRequest,
  stepUpToken?: string,
): Promise<CorrectionApplied> {
  const headers = stepUpToken ? { [STEP_UP_HEADER]: stepUpToken } : undefined;
  return unwrap(
    api.PATCH('/v1/patients/{id}', {
      params: { ...writing(headers), path: { id } },
      body,
    }),
  ) as Promise<CorrectionApplied>;
}

export async function listCorrections(id: string): Promise<PatientCorrection[]> {
  const result = await unwrap(api.GET('/v1/patients/{id}/history', { params: { path: { id } } }));
  return result.corrections;
}
