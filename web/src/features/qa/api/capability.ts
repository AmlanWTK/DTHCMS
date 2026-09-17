'use client';

import { usePermission } from '@/lib/use-permission';

/**
 * The three acts of station 10, as values the type system can require (CP83).
 *
 * # Why this shape, and the defect it is copied from
 *
 * CP92 shipped its station with no permission check anywhere in it, and the screenshot is what
 * found it: a consultant opened the education station and was handed the whole form — ten live
 * state buttons and an improvement score selector, every one of which answers 403. The server was
 * never in doubt. What was wrong was the screen.
 *
 * This station has **four** readers with different relationships to it and so the same mistake is
 * four times as easy:
 *
 *   - the **QA officer**, who reviews, clears and bounces, and may not override;
 *   - the **consultant**, who may override and — deliberately — may not read the override rate;
 *   - a role holding only `qa.clear` and not `qa.bounce`, or the other way round, because the
 *     server grants them separately;
 *   - anybody with `qa.review` alone, who may look and may do nothing.
 *
 * # Why tokens and not booleans
 *
 * A `mayClear` prop is a rule somebody can forget to pass, pass inverted, or default to true, and
 * the failure is silent because a control that renders is a control that looks like it works.
 * These are branded types with no exported constructor and no literal: the hooks below are the
 * only things in the application that can produce one, and every component that draws a control
 * takes the matching token as a **required** prop. A caller who has not narrowed away `null`
 * cannot render the control — not because a reviewer noticed, but because `tsc` did.
 *
 * # What they are not
 *
 * Not authorisation. They decide what to *draw*; the server decides what may *happen*. A token
 * forged by a determined caller buys them a button that answers 403 — and, for the clearance, a
 * database trigger behind that.
 */

declare const clearBrand: unique symbol;
declare const bounceBrand: unique symbol;
declare const overrideBrand: unique symbol;

/** Proof that this reader holds `qa.clear`. */
export interface ClearCapability {
  readonly [clearBrand]: 'qa.clear';
}

/** Proof that this reader holds `qa.bounce`. */
export interface BounceCapability {
  readonly [bounceBrand]: 'qa.bounce';
}

/** Proof that this reader holds `qa.override`. Consultant-level, and not the officer's. */
export interface OverrideCapability {
  readonly [overrideBrand]: 'qa.override';
}

const GRANTED_CLEAR = {} as ClearCapability;
const GRANTED_BOUNCE = {} as BounceCapability;
const GRANTED_OVERRIDE = {} as OverrideCapability;

export function useClearCapability(): ClearCapability | null {
  return usePermission('qa.clear') ? GRANTED_CLEAR : null;
}

export function useBounceCapability(): BounceCapability | null {
  return usePermission('qa.bounce') ? GRANTED_BOUNCE : null;
}

/**
 * The valve.
 *
 * Returns `null` for the QA officer, which is the whole point rather than an oversight: the
 * person watching the override rate must not be the person granting them, and a screen that drew
 * the button for them would be teaching the opposite.
 */
export function useOverrideCapability(): OverrideCapability | null {
  return usePermission('qa.override') ? GRANTED_OVERRIDE : null;
}
