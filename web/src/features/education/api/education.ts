import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * Station 11, typed against the contract (CP88, CP92).
 *
 * # Three calls, and the one that is not here
 *
 * There is no `recordImprovementScore`. The score travels inside the assessment, and adding a
 * second way to send it would be a second thing to guard — the whole of CP88's first decision is
 * that exactly one role may write it, and a rule enforced in two places is a rule enforced in
 * whichever of them somebody remembers.
 *
 * # The reference data is fetched once and cached hard
 *
 * It has no patient in it, it changes when a physician edits a checklist, and the officer needs
 * all of it before the patient sits down. A station on a clinic connection that re-fetched four
 * checklists per patient would spend its morning waiting.
 *
 * # Nothing here computes the re-education flag
 *
 * The server computes it from the item states and the threshold, and returns what it stored. A
 * client-side copy of that arithmetic is the obvious optimisation and would produce exactly the
 * defect worth avoiding: a flag the officer saw and the physician never did.
 */

export type EducationReference = components['schemas']['EducationReference'];
export type EducationSession = components['schemas']['EducationSession'];
export type EducationChecklist = components['schemas']['EducationChecklist'];
export type EducationChecklistItem = components['schemas']['EducationChecklistItem'];
export type EducationState = components['schemas']['EducationState'];
export type EducationCompetency = components['schemas']['EducationCompetency'];
export type ImprovementScale = components['schemas']['ImprovementScale'];
export type ImprovementScaleAnchor = components['schemas']['ImprovementScaleAnchor'];
export type ImprovementAnswer = components['schemas']['ImprovementAnswer'];
export type EducationAssessmentRequest = components['schemas']['EducationAssessmentRequest'];
export type EducationAssessmentResult = components['schemas']['EducationAssessmentResult'];

export const EDUCATION_REFERENCE_KEY = ['education', 'reference'] as const;

export function educationSessionKey(patientId: string, visitId: string) {
  return ['education', 'session', patientId, visitId] as const;
}

/** The checklists, the scale and the vocabularies. No patient in any of it. */
export async function readEducationReference(): Promise<EducationReference> {
  return unwrap(api.GET('/v1/education/reference', {}));
}

/** What this patient's prescription brings up, and what they could do last time. */
export async function readEducationSession(
  patientId: string,
  visitId: string,
): Promise<EducationSession> {
  return unwrap(
    api.GET('/v1/patients/{id}/education', {
      params: { path: { id: patientId }, query: { visit_id: visitId } },
    }),
  );
}

/** Record the assessment. One call, one transaction, one act. */
export async function recordAssessment(
  patientId: string,
  body: EducationAssessmentRequest,
): Promise<EducationAssessmentResult> {
  return unwrap(
    api.POST('/v1/patients/{id}/education', {
      // `writing()` mints the idempotency key. A station tablet that lost the reply and pressed
      // save again must record the assessment once: the key absorbs the retry at the edge, and
      // the body's own `event_id` absorbs it again at the ledger. Two layers, because the first
      // is a cache with a lifetime and the second is a primary key.
      params: { ...writing(), path: { id: patientId } },
      body,
    }),
  );
}

/**
 * The band a score falls in, or `null`.
 *
 * `null` rather than a guess. The server's invariant guarantees every value on a live scale has
 * exactly one band, so a null here means the value is off the scale — which is a data problem
 * and must look like one rather than like the nearest face.
 */
export function anchorFor(scale: ImprovementScale, value: number): ImprovementScaleAnchor | null {
  return scale.anchors.find((band) => value >= band.from_value && value <= band.to_value) ?? null;
}

/**
 * Every value on the scale, in order, so a selector can draw one control per point.
 *
 * Derived from the scale's own bounds rather than from a literal 1..10, because [R-11]'s 1–10
 * may become a symmetric 0–10 and spec §3 promises that is a data change. A hard-coded range
 * here would be the one place that did not move.
 */
export function scaleValues(scale: ImprovementScale): number[] {
  const out: number[] = [];
  for (let value = scale.min_value; value <= scale.max_value; value += 1) out.push(value);
  return out;
}

/**
 * Whether an answer is a score, without a type assertion at every call site.
 *
 * Three states and they are three: a score, a recorded not-applicable, and `null` — nobody has
 * asked. The last is why these are two predicates rather than one boolean: a caller asking "is
 * this not applicable" must not get `true` for a visit nobody has got to.
 */
export function scoreOf(answer: ImprovementAnswer | null | undefined): number | null {
  if (answer && typeof answer === 'object' && 'score' in answer && typeof answer.score === 'number') {
    return answer.score;
  }
  return null;
}

export function notApplicableReasonOf(answer: ImprovementAnswer | null | undefined): string | null {
  if (
    answer &&
    typeof answer === 'object' &&
    'not_applicable_reason' in answer &&
    typeof answer.not_applicable_reason === 'string'
  ) {
    return answer.not_applicable_reason;
  }
  return null;
}

/**
 * How many of each state an assessment holds, for the officer's running tally.
 *
 * It does **not** decide whether the flag fires. That is the server's, and this is the count it
 * will decide from — shown so the officer can see how close they are, never so the screen can
 * pre-empt the record.
 */
export function tally(answers: Record<string, EducationState>): {
  demonstrated: number;
  correctedToday: number;
  unable: number;
} {
  const counts = { demonstrated: 0, correctedToday: 0, unable: 0 };
  for (const state of Object.values(answers)) {
    if (state === 'demonstrated') counts.demonstrated += 1;
    else if (state === 'corrected_today') counts.correctedToday += 1;
    else if (state === 'unable') counts.unable += 1;
  }
  return counts;
}
