import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { STEP_UP_HEADER } from '@/features/auth';
import { api, unwrap } from '@/lib/api';

/**
 * Station 10, typed against the contract (CP83).
 *
 * # Two calls for the two decisions, and not one with a flag
 *
 * `clearPrescription` and `bouncePrescription` are separate functions because they are separate
 * routes behind separate permissions. A single `decide(outcome)` would compile for a caller who
 * holds only one of them and answer 403 at runtime — and the interface would have no way to know
 * which button not to draw.
 *
 * # Nothing here computes whether a file may clear
 *
 * `can_clear` and `clearance_stands` both come back from the server and they answer different
 * questions: *would a clearance be accepted* and *has one been given*. A client-side copy of the
 * first would be a second implementation of the rule engine; a client-side copy of the second
 * would be a screen that said "cleared" while a database trigger refused the signature, which is
 * the defect this station exists to be.
 */

export type QAReview = components['schemas']['QAReview'];
export type QAFinding = components['schemas']['QAFinding'];
export type QADecision = components['schemas']['QADecision'];
export type QAOverride = components['schemas']['QAOverride'];
export type QAQueueEntry = components['schemas']['QAQueueEntry'];
export type QARule = components['schemas']['QARule'];
export type QASeverity = components['schemas']['QASeverity'];

/** What the review endpoint returns, summary sentences and gate state included. */
export interface QAReviewPage {
  review: QAReview;
  summary_en: string;
  summary_bn: string;
  can_clear: boolean;
  clearance_stands: boolean;
}

export const QA_QUEUE_KEY = ['qa', 'queue'] as const;

export function qaReviewKey(prescriptionId: string) {
  return ['qa', 'review', prescriptionId] as const;
}

export function qaOverridesKey(from?: string, to?: string) {
  return ['qa', 'overrides', from ?? '', to ?? ''] as const;
}

/** Everybody waiting for clearance, oldest first. */
export async function readQAQueue(): Promise<{ queue: QAQueueEntry[] }> {
  return unwrap(api.GET('/v1/qa/queue', {}));
}

/** What the rules find on one prescription. Writes nothing. */
export async function readQAReview(prescriptionId: string): Promise<QAReviewPage> {
  return unwrap(
    api.GET('/v1/prescriptions/{prescriptionId}/qa', {
      params: { path: { prescriptionId } },
    }),
  ) as Promise<QAReviewPage>;
}

/** Clear the file. `qa.clear`. */
export async function clearPrescription(
  prescriptionId: string,
  body: { event_id: string; acknowledged: string[] },
): Promise<{ decision: QADecision }> {
  return unwrap(
    api.POST('/v1/prescriptions/{prescriptionId}/qa/clearance', {
      // `writing()` mints the idempotency key. A desk tablet that lost the reply and pressed
      // clear again must record one clearance: the key absorbs the retry at the edge and the
      // body's own `event_id` absorbs it again at the ledger.
      params: { ...writing(), path: { prescriptionId } },
      body,
    }),
  ) as Promise<{ decision: QADecision }>;
}

/** Send it back to a named station, with a reason. `qa.bounce`. */
export async function bouncePrescription(
  prescriptionId: string,
  body: {
    event_id: string;
    bounce_station_code?: string;
    reason_en?: string;
    reason_bn?: string;
  },
): Promise<{ decision: QADecision }> {
  return unwrap(
    api.POST('/v1/prescriptions/{prescriptionId}/qa/bounce', {
      params: { ...writing(), path: { prescriptionId } },
      body,
    }),
  ) as Promise<{ decision: QADecision }>;
}

/**
 * The consultant's valve. `qa.override`, **plus a step-up token**.
 *
 * The token is a required argument rather than an optional header, so a caller who has not
 * minted one cannot compile. The server refuses the request without it either — this is the half
 * that makes the omission visible before it is deployed rather than after.
 */
export async function overrideQAGate(
  prescriptionId: string,
  body: { event_id: string; reason: string },
  stepUpToken: string,
): Promise<{ override: QAOverride }> {
  return unwrap(
    api.POST('/v1/prescriptions/{prescriptionId}/qa/override', {
      params: {
        ...writing({ [STEP_UP_HEADER]: stepUpToken }),
        path: { prescriptionId },
      },
      body,
    }),
  ) as Promise<{ override: QAOverride }>;
}

/** The override rate. `qa.review`, and deliberately not `qa.override`. */
export async function readQAOverrides(
  from?: string,
  to?: string,
): Promise<{ overrides: QAOverride[]; count: number; from: string; to: string }> {
  return unwrap(
    api.GET('/v1/qa/overrides', {
      params: { query: { from, to } },
    }),
  ) as Promise<{ overrides: QAOverride[]; count: number; from: string; to: string }>;
}

/** The checklist itself. */
export async function readQARules(): Promise<{ rules: QARule[] }> {
  return unwrap(api.GET('/v1/qa/rules', {})) as Promise<{ rules: QARule[] }>;
}

/**
 * The findings that stop a clearance.
 *
 * Derived here rather than asked for, because it is a filter on a list the server already sent
 * and not a judgement: `can_clear` is the judgement and it comes from the server.
 */
export function blockingOf(review: QAReview): QAFinding[] {
  return (review.findings ?? []).filter((f) => f.severity === 'BLOCK');
}

/** The findings that clear with an acknowledgement. */
export function warningsOf(review: QAReview): QAFinding[] {
  return (review.findings ?? []).filter((f) => f.severity !== 'BLOCK');
}

/**
 * The station a bounce would default to: the strongest blocking finding's, or the first
 * warning's when nothing blocks.
 *
 * The server applies the same default. This copy exists so the officer sees which room the button
 * will send the patient to *before* they press it — and if the two ever disagreed, the server's
 * answer is what the record would say, which is why the form sends the station explicitly.
 */
export function defaultBounceStation(review: QAReview): string {
  const ordered = [...blockingOf(review), ...warningsOf(review)];
  return ordered.find((f) => f.bounce_station_code)?.bounce_station_code ?? '';
}
