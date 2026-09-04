import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import type { ConceptSelection } from '@/features/terminology';
import { api, unwrap } from '@/lib/api';

/**
 * The allergy hard stop, typed against the contract (CP54, §3 step 4, [R-01]).
 *
 * Eight calls and a handful of pure rules. Four of them are worth reading the note for,
 * because each is a place where the obvious convenience would break the thing the
 * checkpoint exists for.
 *
 * **There is no function here that satisfies the gate without an answer.** No `skip`, no
 * `proceed`, no `markAsked`. Three acts satisfy it — one or more allergies recorded, "no
 * known allergies" asserted, or "unable to assess" with its reason — and every one of them
 * is a request with a person's name on it. The gate itself is a trigger on the queue table,
 * so nothing written here could bypass it anyway; what a helper *could* do is teach the
 * floor that there is a fourth way, and within a month that way is the normal path. The
 * plan names that risk by hand. `allergies-api.test.ts` has a named test that fails if such
 * an export ever appears.
 *
 * **`status` has four values and two of them have no allergies.** `NONE_RECORDED` means
 * nobody has asked; `NO_KNOWN_ALLERGY` means somebody asked and was told there are none.
 * Both come back with an empty list, they are opposite facts, and a screen that read the
 * list rather than the status would draw the dangerous one as the safe one. Nothing in
 * this module derives a status from `allergies.length`, and `statusOf` exists so that no
 * caller is tempted to.
 *
 * **`UNABLE_TO_ASSESS` is allergy status, not an absence of allergies and not an
 * override.** Somebody looked, somebody is named, and the reason is mandatory. It satisfies
 * the gate precisely so that there is no fourth answer for the unconscious patient — and
 * it must never be rendered as reassurance. `isReassuring` is the predicate that keeps
 * that honest, and it answers `true` for exactly one status.
 *
 * **A coding is three fields or none.** `recordRequestFrom` is the only place `code_system`,
 * `code_version` and `code` are written, and it writes all three or none of them. The
 * server refuses two of the three — a code with no version is a string — and here the
 * escape hatch matters more than anywhere else in the system: an allergy the catalogue has
 * no word for is far more dangerous in a note field than it is on the header, marked
 * uncoded.
 */

export type Allergy = components['schemas']['Allergy'];
export type AllergyAssertion = components['schemas']['AllergyAssertion'];
export type AllergyState = components['schemas']['AllergyState'];
export type AllergyReaction = components['schemas']['AllergyReaction'];
export type AllergyChange = components['schemas']['AllergyChange'];
export type AllergyAssertionRate = components['schemas']['AllergyAssertionRate'];
export type RecordAllergyRequest = components['schemas']['RecordAllergyRequest'];

/** The four answers `GET /v1/patients/{id}/allergies` can give. */
export type AllergyStatus = AllergyState['status'];
/** The two things a person can assert. Neither is a default and neither is an override. */
export type AssertionKind = AllergyAssertion['kind'];
export type AllergySeverity = Allergy['severity'];
export type AllergyCertainty = Allergy['certainty'];

/**
 * The severities the contract allows, worst last.
 *
 * `life_threatening` is here and not in the history feature's list: a complaint can be
 * severe, and only an allergy can be the thing that kills somebody on the way home.
 */
export const SEVERITIES: readonly AllergySeverity[] = [
  'mild',
  'moderate',
  'severe',
  'life_threatening',
];

/** Suspected or confirmed. A suspected reaction thirty years ago is still worth recording. */
export const CERTAINTIES: readonly AllergyCertainty[] = ['suspected', 'confirmed'];

/**
 * The two assertions, in the order a screen offers them.
 *
 * Two, not three. There is deliberately no third entry meaning "asked later" or "not
 * applicable" — see the note at the top of the file.
 */
export const ASSERTION_KINDS: readonly AssertionKind[] = ['NO_KNOWN_ALLERGY', 'UNABLE_TO_ASSESS'];

/**
 * The cache keys, held beside the calls.
 *
 * The state key is read by the patient header on every screen and written by every act on
 * the station panel, and the two live in different files — which is exactly the situation
 * where two spellings of one key produces a header that keeps showing "nobody has asked"
 * after somebody just asked.
 */
export const ALLERGY_REACTIONS_KEY = ['allergies', 'reactions'] as const;

export function allergyStateKey(patientId: string) {
  return ['allergies', 'state', patientId] as const;
}

export function allergyChangesKey(patientId: string) {
  return ['allergies', 'changes', patientId] as const;
}

/** The idempotency key a write carries. A browser has no offline queue; it still sends one. */
export function newEventId(): string {
  return crypto.randomUUID();
}

/** The reaction vocabulary, short on purpose and fetched once. */
export async function listAllergyReactions(): Promise<AllergyReaction[]> {
  const body = await unwrap(api.GET('/v1/allergies/reactions'));
  return body.reactions;
}

/**
 * This patient's allergy status, and what is recorded.
 *
 * The whole state comes back rather than the list, and callers keep it whole: `status` and
 * `satisfied` are answers the list cannot be asked for.
 */
export function getAllergyState(patientId: string): Promise<AllergyState> {
  return unwrap(api.GET('/v1/patients/{id}/allergies', { params: { path: { id: patientId } } }));
}

/** Everything ever said about this patient's allergies, withdrawn entries included. */
export async function listAllergyChanges(patientId: string): Promise<AllergyChange[]> {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/allergies/history', { params: { path: { id: patientId } } }),
  );
  return body.changes;
}

/** One allergy, one request. The response is the whole new state, because the gate moved. */
export function recordAllergy(
  patientId: string,
  request: RecordAllergyRequest,
): Promise<AllergyState> {
  return unwrap(
    api.POST('/v1/patients/{id}/allergies', {
      params: { ...writing(), path: { id: patientId } },
      body: { event_id: newEventId(), ...request },
    }),
  );
}

/** What an assertion is allowed to carry. Built by `assertionRequestFrom`, never by hand. */
export interface AssertionRequest {
  kind: AssertionKind;
  reason?: string;
  visit_id?: string;
}

/**
 * State, in your own name, what the allergy answer is.
 *
 * Criterion 2: "no known allergies" is never a default and never an empty field, which is
 * only structural if making it takes a positive act with an actor behind it. This is that
 * act, and it is the same call for both kinds — because a screen with one function for the
 * safe-sounding assertion and another for the awkward one is a screen that will grow a
 * default for the first.
 */
export function assertAllergyStatus(
  patientId: string,
  request: AssertionRequest,
): Promise<AllergyState> {
  return unwrap(
    api.POST('/v1/patients/{id}/allergies/assert', {
      params: { ...writing(), path: { id: patientId } },
      body: { event_id: newEventId(), ...request },
    }),
  );
}

/**
 * Take back an allergy that should not have been recorded. Never a deletion.
 *
 * The reason is required by the server and is the point of the call: the next clinician
 * needs to know a colleague once believed it. The response carries the resulting state
 * because withdrawing the last allergy can **re-close the gate**, and a caller left to
 * guess would show a satisfied header over a patient who can no longer be queued.
 */
export function withdrawAllergy(allergyId: string, reason: string): Promise<AllergyState> {
  return unwrap(
    api.POST('/v1/allergies/{allergyId}/withdraw', {
      params: { ...writing(), path: { allergyId } },
      body: { event_id: newEventId(), reason },
    }),
  );
}

/**
 * Take back a "no known allergies" or an "unable to assess".
 *
 * The one that matters is the first: an officer who tapped it on the wrong patient has put
 * a claim into a record a prescriber will rely on.
 */
export function withdrawAllergyAssertion(
  assertionId: string,
  reason: string,
): Promise<AllergyState> {
  return unwrap(
    api.POST('/v1/allergies/assertions/{assertionId}/withdraw', {
      params: { ...writing(), path: { assertionId } },
      body: { event_id: newEventId(), reason },
    }),
  );
}

/** One operator's assertions over a window. QA's own mitigation, and never a rule (CP83). */
export async function allergyAssertionRates(window: { from?: string; to?: string } = {}): Promise<{
  from: string;
  to: string;
  operators: AllergyAssertionRate[];
}> {
  return unwrap(
    api.GET('/v1/allergies/assertion-rates', {
      params: {
        query: {
          ...(window.from === undefined ? {} : { from: window.from }),
          ...(window.to === undefined ? {} : { to: window.to }),
        },
      },
    }),
  );
}

/** The floor the server puts under a withdrawal reason, mirrored so a form can say so first. */
export const REASON_MIN = 1;

export function reasonAcceptable(reason: string): boolean {
  return reason.trim().length >= REASON_MIN;
}

/**
 * The status, read off the state and never inferred from the list.
 *
 * A one-line function that exists to be the only answer to "what does this patient's
 * allergy status say". `state.allergies.length === 0` is true of both `NONE_RECORDED` and
 * `NO_KNOWN_ALLERGY`, and the difference between them is the difference between "nobody
 * asked" and "asked, and there are none" — which is the whole of acceptance criterion 3's
 * failure mode.
 */
export function statusOf(state: AllergyState): AllergyStatus {
  return state.status;
}

/**
 * Whether the patient may be put in the queue for a station after the history station.
 *
 * The server's own answer, carried rather than recomputed. The enforcement is a trigger on
 * the queue table; this is only what lets a screen explain itself before somebody tries.
 */
export function gateSatisfied(state: AllergyState): boolean {
  return state.satisfied;
}

/**
 * Whether this status is the one that means "asked, and there are none".
 *
 * Exactly one status may be drawn as reassurance. `NONE_RECORDED` is a gap, and
 * `UNABLE_TO_ASSESS` is somebody having looked and failed — a patient who cannot answer is
 * not a patient with no allergies, and the medication safety engine will treat the two very
 * differently. A banner that tinted either of them green would be lying in the direction
 * that gets somebody hurt.
 */
export function isReassuring(status: AllergyStatus): boolean {
  return status === 'NO_KNOWN_ALLERGY';
}

/**
 * Whether this reaction is an emergency, or the severity says it could kill.
 *
 * `is_emergency` is a property of the *reaction* — anaphylaxis is an emergency whatever
 * somebody ticked beside it — and `life_threatening` is a property of what happened. Either
 * is enough, and the word for it goes on screen; a header that carried this only as a
 * colour would say nothing on a monochrome printer, in sunlight, or to the roughly one man
 * in twelve who cannot use hue.
 */
export function isEmergency(allergy: Allergy): boolean {
  return allergy.is_emergency === true || allergy.severity === 'life_threatening';
}

/** Whether anything on this patient's list is an emergency. Used to decide what leads. */
export function hasEmergency(state: AllergyState): boolean {
  return state.allergies.some(isEmergency);
}

/**
 * Whether the catalogue had nothing for the substance.
 *
 * All three coding fields or none, so any missing one means uncoded — read as three checks
 * rather than one, because a half-coding is the state the server refuses and the one this
 * predicate must never quietly round up to "coded".
 */
export function isUncoded(allergy: Allergy): boolean {
  return !allergy.code_system || !allergy.code_version || !allergy.code;
}

/**
 * The allergy's coding in the shape `ConceptChip` shows, or nothing.
 *
 * `null` rather than a partial object: an uncoded allergy is a real state with its own
 * display, and a chip built from a coding with an empty version is the single failure CP52
 * exists to prevent.
 */
export function allergyCoding(allergy: Allergy): ConceptSelection | null {
  if (isUncoded(allergy)) return null;
  return {
    system: allergy.code_system as string,
    version: allergy.code_version as string,
    code: allergy.code as string,
    display_en: allergy.display_en ?? (allergy.code as string),
    ...(allergy.display_bn === undefined ? {} : { display_bn: allergy.display_bn }),
  };
}

/**
 * The reactions in the order the server draws them, emergencies first within that.
 *
 * The list is short and a station operator picks from it in seconds while a queue waits, so
 * the reaction that changes the answer sits where the thumb already is. This orders a
 * *vocabulary*, not a patient's allergies — those arrive worst first from the server and
 * are never re-sorted here.
 */
export function reactionsInOrder(reactions: readonly AllergyReaction[]): AllergyReaction[] {
  return [...reactions].sort((a, b) => {
    if (a.is_emergency !== b.is_emergency) return a.is_emergency ? -1 : 1;
    return a.ordering - b.ordering;
  });
}

/**
 * What a record-an-allergy form holds while it is being filled in.
 *
 * Every field is a string except the concept, because that is what an input gives back and
 * because "" and "not asked" have to be the same thing until it is recorded. Severity and
 * certainty start **empty** rather than at a sensible middle value: a defaulted severity is
 * a clinical claim nobody made, and this is the one record where a claim nobody made is
 * read by somebody about to hand over a medicine.
 */
export interface AllergyDraft {
  concept: ConceptSelection | null;
  said: string;
  reaction: string;
  severity: string;
  certainty: string;
  note: string;
}

export function emptyAllergyDraft(): AllergyDraft {
  return { concept: null, said: '', reaction: '', severity: '', certainty: '', note: '' };
}

/**
 * The fields the server would refuse this draft for, named as the server names them.
 *
 * The names matter: they are the keys a 422 comes back with, so a client-side complaint and
 * a server-side one land against the same control and the operator never sees the message
 * move. `said` is required only when there is no coding — the uncoded escape hatch — and
 * severity and certainty are required always, because the contract requires them and
 * because neither has a safe default.
 */
export function missingAllergyFields(draft: AllergyDraft): string[] {
  const missing: string[] = [];
  if (draft.concept === null && draft.said.trim() === '') missing.push('said');
  if (draft.reaction === '') missing.push('reaction');
  if (draft.severity === '') missing.push('severity');
  if (draft.certainty === '') missing.push('certainty');
  return missing;
}

/**
 * The draft as the contract's request body.
 *
 * The coding travels as three fields or as none of them — see the note at the top. Empty
 * strings are dropped rather than sent: an absent field and an empty one mean the same
 * thing to this endpoint, and `note: ''` is a validation failure this form would have
 * caused itself.
 */
export function recordRequestFrom(draft: AllergyDraft, visitId?: string): RecordAllergyRequest {
  const said = draft.said.trim();
  const note = draft.note.trim();

  return {
    ...(draft.concept === null
      ? {}
      : {
          code_system: draft.concept.system,
          code_version: draft.concept.version,
          code: draft.concept.code,
        }),
    ...(said === '' ? {} : { said }),
    reaction: draft.reaction,
    severity: draft.severity as AllergySeverity,
    certainty: draft.certainty as AllergyCertainty,
    ...(note === '' ? {} : { note }),
    ...(visitId === undefined ? {} : { visit_id: visitId }),
  };
}

/**
 * An assertion as the contract's request body: the reason travels with exactly one kind.
 *
 * Required for `UNABLE_TO_ASSESS`, refused for `NO_KNOWN_ALLERGY`. Both halves are here so
 * that a screen cannot make the refused request by leaving a box filled in — the reason is
 * dropped for the kind that has no room for it rather than sent and bounced, which would
 * cost an operator a round trip to be told something this function already knew.
 */
export function assertionRequestFrom(
  kind: AssertionKind,
  reason: string,
  visitId?: string,
): AssertionRequest {
  const trimmed = reason.trim();
  return {
    kind,
    ...(kind === 'UNABLE_TO_ASSESS' && trimmed !== '' ? { reason: trimmed } : {}),
    ...(visitId === undefined ? {} : { visit_id: visitId }),
  };
}

/**
 * The fields an assertion would be refused for, named as the server names them.
 *
 * One rule, and it is the reason the third state is worth having: "we could not ask" with
 * no reason is a silent gap wearing a label, and a state that is not reviewable is an
 * override with a longer name.
 */
export function missingAssertionFields(kind: AssertionKind, reason: string): string[] {
  return kind === 'UNABLE_TO_ASSESS' && !reasonAcceptable(reason) ? ['reason'] : [];
}

/** Whether a change-history line has been taken back. Withdrawn rows are why the list exists. */
export function isWithdrawn(change: AllergyChange): boolean {
  return change.undone_at !== undefined && change.undone_at !== '';
}
