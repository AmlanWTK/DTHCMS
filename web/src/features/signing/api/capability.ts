'use client';

import { usePermission } from '@/lib/use-permission';

/**
 * The one act of CP84, as a value the type system can require.
 *
 * # Why a token and not a boolean, again
 *
 * CP83 wrote the argument (`features/qa/api/capability.ts`) and CP92 is the defect it was written
 * against: a station that handed its whole form to somebody who held none of its permissions, every
 * control answering 403, found by a screenshot rather than by a test.
 *
 * Signing is the sharpest case in the system for the same mistake, and it is sharper than QA's for
 * three reasons:
 *
 *   - **`prescription.sign` is PHYSICIAN's alone** (migration 00006). The QA officer, the
 *     pharmacist and the prescription education officer all hold `prescription.read` and reach the
 *     same sheet; not one of them may sign it.
 *   - **`prescription.draft` is not `prescription.sign`.** A junior doctor may write a prescription
 *     and may not sign one. A screen that keyed the sign control off "may prescribe" would hand it
 *     to exactly the person the separation exists for. That is why `prescription.sign` is its own
 *     interface action rather than a fold into `clinical.prescribe`, which `prescription.draft`
 *     alone satisfies.
 *   - **The refusal is expensive to discover.** Signing costs a second factor. A control that
 *     looked available would make somebody find their phone, type a code, and *then* be refused —
 *     which teaches the clinic that the second factor is theatre.
 *
 * So this is a branded type with no exported constructor and no literal spelling: [useSignCapability]
 * is the only thing in the application that can produce one, and the sign control takes one as a
 * **required** prop. A screen that has not narrowed away `null` does not compile.
 *
 * # What it is not
 *
 * Not authorisation. It decides what to *draw*. The server decides what may *happen* — and for
 * signing it decides three times over: the route guard, a step-up token minted for
 * `prescription.sign` and consumed on use, and a database trigger that refuses the transition
 * without a standing QA clearance. A token forged by a determined caller buys them a button that
 * asks for a second factor and is then refused.
 */

declare const signBrand: unique symbol;

/** Proof that this reader holds `prescription.sign`. */
export interface SignCapability {
  readonly [signBrand]: 'prescription.sign';
}

const GRANTED_SIGN = {} as SignCapability;

export function useSignCapability(): SignCapability | null {
  return usePermission('prescription.sign') ? GRANTED_SIGN : null;
}
