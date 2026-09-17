import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { STEP_UP_HEADER } from '@/features/auth';
import { API_BASE_URL, api, unwrap } from '@/lib/api';

/**
 * Signing a prescription, and checking one (CP84, CP85).
 *
 * # Nothing here decides whether a prescription may be signed
 *
 * `readiness` comes back from the server, whole, with the reason already in both languages. A
 * client-side copy of the rule would be a third implementation of a gate that already has two —
 * the service's check and CP83's database trigger — and the copy in the browser would be the one
 * that disagreed, which is a screen saying "sign" while a trigger refuses.
 *
 * The prescriber cannot work it out for himself in any case: `GET /v1/prescriptions/{id}/qa` is
 * behind `qa.review`, and PHYSICIAN does not hold it.
 *
 * # The step-up token is an argument, not a header somebody remembers
 *
 * [signPrescription] takes it positionally and required. A caller who has not minted one does not
 * compile. The server refuses without it as well — this is the half that makes the omission visible
 * before it is deployed rather than after.
 *
 * # The verification token is returned once and is not stored here
 *
 * It comes back from the signing call, goes into the QR code on the sheet, and is never asked for
 * again. Nothing in this module puts it in `localStorage`, a query cache key, or a log.
 */

export type PrescriptionSignature = components['schemas']['PrescriptionSignature'];
export type SignatureVerification = components['schemas']['SignatureVerification'];
export type SignatureImageCaveat = components['schemas']['SignatureImageCaveat'];
export type SigningReadiness = components['schemas']['SigningReadiness'];
export type PublicVerification = components['schemas']['PublicVerification'];

/** What `GET /v1/prescriptions/{id}/signature` answers, signed or not. */
export interface SignatureView {
  signed: boolean;
  readiness: SigningReadiness;
  verification?: SignatureVerification;
  signature_image: SignatureImageCaveat;
}

/** What a signing answers. The token is in it exactly once. */
export interface SigningResult {
  signature: PrescriptionSignature;
  verification_token: string;
  verification_path: string;
  signature_image: SignatureImageCaveat;
}

export function signatureKey(prescriptionId: string) {
  return ['prescriptions', prescriptionId, 'signature'] as const;
}

/** The signature and a verification computed on this call. `prescription.read`. */
export async function readSignature(prescriptionId: string): Promise<SignatureView> {
  return unwrap(
    api.GET('/v1/prescriptions/{prescriptionId}/signature', {
      params: { path: { prescriptionId } },
    }),
  ) as Promise<SignatureView>;
}

/**
 * Sign it. `prescription.sign`, **plus a step-up minted for `prescription.sign`**.
 *
 * `writing()` mints the idempotency key. A consultant whose tablet lost the reply and pressed sign
 * again must produce one signature, not two: the key absorbs the retry at the edge and the body's
 * `event_id` absorbs it again at the ledger. Both are sent because they answer at different
 * layers, and the ledger's is the one that survives a proxy nobody configured.
 */
export async function signPrescription(
  prescriptionId: string,
  eventId: string,
  stepUpToken: string,
): Promise<SigningResult> {
  return unwrap(
    api.POST('/v1/prescriptions/{prescriptionId}/signature', {
      params: {
        ...writing({ [STEP_UP_HEADER]: stepUpToken }),
        path: { prescriptionId },
      },
      body: { event_id: eventId },
    }),
  ) as Promise<SigningResult>;
}

/**
 * Ask the public endpoint about a token (CP85).
 *
 * # Why this does not go through `api`
 *
 * `@/lib/api`'s client wraps a *refreshing* fetch: it attaches a bearer token when there is one and,
 * on a 401, spends the refresh cookie and tells the shell the session is gone. Every one of those
 * behaviours is wrong here. The caller is a stranger who scanned a piece of paper; they have no
 * session, and a page that reached for one would be sending a credential — or, worse, attempting a
 * refresh — from the one surface in this system that anybody on the internet can reach.
 *
 * So: a bare `fetch`, `credentials: 'omit'` stated rather than inherited, and no headers at all
 * beyond what the browser sends on its own. A phone's camera reaches the endpoint with exactly this
 * much, and the page must not need more.
 *
 * # Every failure is the same failure
 *
 * The endpoint answers 200 for both verdicts, deliberately — the verdict is in the body so that
 * nothing between the phone and the server can tell the two apart. Anything else, including a
 * network error and a rate-limited 429, is turned into the same unverified answer here, for the
 * same reason: a page that said "could not reach the server" for one failure and "not verified" for
 * another would be teaching a stranger which of their guesses was interesting.
 *
 * The one thing this function will not do is invent a verdict. A malformed body throws, and the
 * page shows its own "we could not check this" state rather than a fabricated `NOT_VERIFIED` that
 * a pharmacist would read as an accusation.
 */
export async function verifyPublicly(token: string): Promise<PublicVerification> {
  const response = await fetch(`${API_BASE_URL}/v1/verify/${encodeURIComponent(token)}`, {
    method: 'GET',
    credentials: 'omit',
    cache: 'no-store',
  });
  const body: unknown = await response.json();
  if (!isPublicVerification(body)) {
    throw new Error('the verification service answered something this page cannot read');
  }
  return body;
}

/**
 * What the page will render, checked at runtime.
 *
 * Types are erased; this is not. The public page draws a verdict a pharmacist acts on, and the one
 * unacceptable outcome is drawing "verified" from a body that did not say so — which is exactly
 * what a cast would allow if anything ever sat between the phone and the API.
 */
function isPublicVerification(value: unknown): value is PublicVerification {
  if (typeof value !== 'object' || value === null) return false;
  const body = value as Record<string, unknown>;
  return (
    (body.verdict === 'VERIFIED' || body.verdict === 'NOT_VERIFIED') &&
    typeof body.message_en === 'string' &&
    typeof body.message_bn === 'string'
  );
}
