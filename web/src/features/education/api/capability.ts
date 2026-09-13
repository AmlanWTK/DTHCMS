'use client';

import { usePermission } from '@/lib/use-permission';

/**
 * The right to record at station 11, as a value the type system can require (CP92).
 *
 * # The defect this exists to make unrepeatable
 *
 * The first version of this feature had no permission check anywhere in it. The nav entry is on
 * `education.view` — deliberately, because CP92's second acceptance criterion is that the
 * physician sees what the patient could do at the next consultation — so a consultant opened the
 * station and was handed the *whole form*: ten technique items with live state buttons, the
 * missed-dose row, and the improvement score selector.
 *
 * Every one of those controls answers 403. The server was never in doubt: the score is guarded by
 * the observation code's own write permission and `TestThePhysicianCannotRecordAnImprovementScore`
 * proves it through the real router. What was wrong was the screen, which is exactly what
 * `lib/permissions.ts` opens by saying it exists to prevent — and it was wrong inside the
 * checkpoint whose entire argument is about who may ask the question.
 *
 * The score is the part that matters most. CP88 §1 moves the question away from the consultation
 * because a patient asked by the person whose treatment it grades answers upward. A screen that
 * lets that person hover over an 8 is a screen inviting him to think of the number as adjustable,
 * which is the same failure arriving through the interface instead of through the ledger.
 *
 * # Why a token and not a boolean
 *
 * A `mayRecord` prop is a rule somebody can forget to pass, pass inverted, or default to true —
 * and the failure is silent, because a control that renders is a control that looks like it
 * works. What is wanted is the property `eventstore.Reader` and `eventstore.Actor` have on the
 * server: *the wrong call does not compile*.
 *
 * So this is a branded type with no exported constructor and no literal. `useRecordingCapability`
 * is the only thing in the application that can produce one, it asks `usePermission` to do so, and
 * every writable component in this feature takes one as a **required** prop. A caller that has not
 * gone through the hook and narrowed away `null` cannot render a control that writes — not because
 * a reviewer noticed, but because `tsc` did.
 *
 * # What it is not
 *
 * It is not authorisation. It decides what to *draw*; the server decides what may *happen*, for
 * the same role, which the client sends with every request. A token forged by a determined caller
 * buys them a form that answers 403 — which is the arrangement `lib/permissions.ts` describes and
 * the only one that is honest about where the decision lives.
 */

declare const recordingBrand: unique symbol;

/**
 * Proof that this reader holds `education.record`.
 *
 * The brand field does not exist at runtime and has no literal, so the only way to hold one of
 * these is to have been given it by the hook below.
 */
export interface RecordingCapability {
  readonly [recordingBrand]: 'education.record';
}

/** The single instance. Identity is meaningless; the type is the whole of it. */
const GRANTED = {} as RecordingCapability;

/**
 * The one place that decides whether this station's controls are drawn.
 *
 * Returns `null` for a reader who may look and not record — the physician and the junior doctor,
 * who hold `education.read` and not `education.record`. A component that wants to draw a control
 * must narrow this, and narrowing it is the only way to obtain the prop it needs.
 */
export function useRecordingCapability(): RecordingCapability | null {
  return usePermission('education.record') ? GRANTED : null;
}
