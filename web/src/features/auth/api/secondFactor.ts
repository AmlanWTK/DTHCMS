import { ApiError, guarded } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';
import type { Proof } from '@/stores/session';

/**
 * The second-factor calls, typed against the contract.
 *
 * Thin on purpose: every rule — what a code buys, what a step-up is good for, how many
 * recovery codes there are — is the server's, and these functions make no decision the
 * server would not make for them.
 */

export type StepUpPurpose =
  | 'second_factor.disable'
  | 'second_factor.recovery_codes'
  | 'prescription.sign'
  | 'rbac.change'
  | 'research.export'
  | 'clinical.override'
  | 'user.manage'
  | 'credential.reset'
  | 'break_glass'
  // CP30. Merging two patient records is irreversible in effect — two clinical histories
  // become one — so it has its own purpose rather than borrowing another.
  | 'patient_merge'
  // CP35. Correcting a date of birth, a sex or an English name invalidates values that were
  // already computed and acted on. Its own purpose, so a token minted to merge two records
  // cannot be spent rewriting a birth date.
  | 'patient_correct_identity';

export const STEP_UP_HEADER = 'X-Step-Up-Token';

export function secondFactorStatus() {
  return unwrap(api.GET('/v1/auth/second-factor'));
}

export function beginEnrolment() {
  return unwrap(api.POST('/v1/auth/second-factor/enrol', { params: guarded }));
}

export function confirmEnrolment(code: string) {
  return unwrap(api.POST('/v1/auth/second-factor/confirm', { params: guarded, body: { code } }));
}

/** Mint a step-up token for one purpose. Throws the server's refusal. */
export async function stepUp(purpose: StepUpPurpose, proof: Proof): Promise<string> {
  const result = await unwrap(
    api.POST('/v1/auth/step-up', {
      params: guarded,
      body: {
        purpose,
        ...('code' in proof ? { code: proof.code } : { recovery_code: proof.recoveryCode }),
      },
    }),
  );
  return result.step_up_token;
}

export function disableSecondFactor(stepUpToken: string, reason?: string) {
  return unwrap(
    api.POST('/v1/auth/second-factor/disable', {
      params: { header: { ...guarded.header, [STEP_UP_HEADER]: stepUpToken } },
      body: reason ? { reason } : {},
    }),
  );
}

export function regenerateRecoveryCodes(stepUpToken: string) {
  return unwrap(
    api.POST('/v1/auth/second-factor/recovery-codes', {
      params: { header: { ...guarded.header, [STEP_UP_HEADER]: stepUpToken } },
    }),
  );
}

/** Whether an error is the server asking for a step-up rather than saying no. */
export function isStepUpRequired(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'STEP_UP_REQUIRED';
}
