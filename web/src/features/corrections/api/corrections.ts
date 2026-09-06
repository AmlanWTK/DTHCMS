import { ApiError, writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import {
  enteredUnitOf,
  enteredValueOf,
  isReplaced,
  type Observation,
} from '@/features/observations';
import { api, unwrap } from '@/lib/api';

/**
 * The correction workflow, typed against the contract (CP62, §4.3, [R-04]).
 *
 * # The 140/150 case, which is what every rule below is for
 *
 * An operator records a height of 150 cm. The physician is sure it is 140 and flags it. The
 * request is routed to **the operator who typed it** — not to a supervisor and not to a
 * queue — because an operator who never learns they mistyped will mistype again. They
 * correct it; a new value replaces the old one and the old one keeps its row; everything
 * derived from it recomputes; and both numbers stay in the record with both names against
 * them.
 *
 * # Nothing in this module edits a value, and there is no function that could
 *
 * There is no `updateObservation` here and there must never be one. Correcting is *writing a
 * new value that replaces the old one*, which is a different act with a different row, and a
 * helper that edited in place would destroy criterion 1 — the original is never altered and
 * remains visible — in the one place a reviewer would never think to look. The server has no
 * such endpoint either; this is the client half of the same decision.
 *
 * # `APPLIED` and `OVERRIDDEN` are never collapsed
 *
 * A supervisor's fix is a different status from the operator's own, because an operator's
 * quality record must not read a supervisor's fix as though they had put it right themselves
 * — and CP63 counts these statuses. There is deliberately **no** predicate here answering
 * "was it corrected", because the only honest answer to that question is which of the two
 * happened. `corrections.test.tsx` has a named test that walks this module's exports and
 * fails if one ever appears whose name suggests it.
 *
 * # A reason is a code *and* free text, and neither substitutes for the other
 *
 * A code alone cannot say "the tape was against the wall, not the patient". Free text alone
 * cannot be counted, and counting is the whole of CP63's repeated-transcription detection.
 * So the vocabulary is rows fetched from the server — `transcription` marks the ones that get
 * counted — and the note sits beside the code rather than instead of it.
 *
 * # Nothing here reads as blame
 *
 * A correction is a fact about a number. There is no word for wrong in any identifier in this
 * file, no predicate that grades an operator, and no helper that counts somebody's mistakes.
 * The plan's own risk note says a metric that feels punitive makes staff hide errors instead
 * of correcting them, and the first place that goes wrong is the vocabulary.
 */

export type CorrectionReason = components['schemas']['CorrectionReason'];
export type CorrectionRequest = components['schemas']['CorrectionRequest'];

/**
 * The four states a request can be in.
 *
 * `OVERRIDDEN` is not a variant of `APPLIED` and this type does not let a caller pretend it
 * is: every screen that switches on status has to name all four, so adding a fifth server-side
 * fails the build here rather than rendering as a blank on a physician's screen.
 */
export type CorrectionStatus = CorrectionRequest['status'];

/**
 * The cache keys, held beside the calls.
 *
 * Answering a request moves four things at once: the request itself, the operator's queue,
 * the patient's correction list, and — on an apply — the observation history the chain view
 * is drawing. Those are read in four different files, which is exactly the situation where
 * two spellings of one key produce a queue that still lists a request somebody has just
 * answered.
 */
export const CORRECTION_REASONS_KEY = ['corrections', 'reasons'] as const;

export function myCorrectionsKey(includeAnswered: boolean) {
  return ['corrections', 'mine', includeAnswered ? 'all' : 'open'] as const;
}

export function correctionKey(requestId: string) {
  return ['corrections', 'request', requestId] as const;
}

export function patientCorrectionsKey(patientId: string) {
  return ['corrections', 'patient', patientId] as const;
}

/** The idempotency key a write carries. A browser has no offline queue; it still sends one. */
export function newEventId(): string {
  return crypto.randomUUID();
}

/**
 * The ways a value can be wrong, in the order a screen offers them.
 *
 * Reference data with no patient in it. Rows rather than an enum because the taxonomy is a
 * proposal the plan lists as needing clinical confirmation, and changing it should be a
 * decision rather than a release.
 */
export async function listCorrectionReasons(): Promise<CorrectionReason[]> {
  const body = await unwrap(api.GET('/v1/corrections/reasons'));
  return body.reasons;
}

/**
 * What I am being asked to fix — the caller's own queue, oldest first.
 *
 * Oldest first is the server's order and nothing here re-sorts it: a queue answered
 * newest-first is a queue where the oldest request is never answered.
 *
 * `includeAnswered` asks for the history as well. Two different questions on one screen — what
 * is waiting, and what I have already answered — and the second must never be mixed into the
 * first by default, or a queue of two open requests reads as a queue of forty.
 */
export async function listMyCorrections(includeAnswered = false): Promise<CorrectionRequest[]> {
  const body = await unwrap(
    api.GET('/v1/corrections/mine', {
      // The server takes the *presence* of `all`, not a value. Sending `all=false` would ask
      // for the history, which is the opposite of what the caller said.
      params: { query: includeAnswered ? { all: 'true' } : {} },
    }),
  );
  return body.requests;
}

/** One request, by id. What an answer screen reads before it offers to answer anything. */
export async function getCorrectionRequest(requestId: string): Promise<CorrectionRequest> {
  const body = await unwrap(
    api.GET('/v1/corrections/{id}', { params: { path: { id: requestId } } }),
  );
  return body.request;
}

/**
 * Every flag ever raised on one patient's values, newest first.
 *
 * The half of criterion 5 the observation rows cannot carry. An observation says who recorded
 * it; only the request says who said it was wrong, why, who was asked, and what they
 * answered. A chain view built on the observations alone would show two numbers and no
 * conversation.
 */
export async function listCorrectionsForPatient(patientId: string): Promise<CorrectionRequest[]> {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/corrections', { params: { path: { id: patientId } } }),
  );
  return body.requests;
}

/** What a flag says: one of the server's reason codes, and what the flagger actually saw. */
export interface Flagging {
  reasonCode: string;
  note?: string;
}

/**
 * Say a value looks wrong. The request goes to whoever typed it.
 *
 * The reason code is a required positional field rather than an optional one, so this cannot
 * be called without a reason by leaving something out. The server refuses an unknown code
 * with `422` against `reason_code`; a form should refuse a blank one before the round trip,
 * because the round trip costs a physician's attention with a patient in the chair.
 *
 * Answers `422` against `observation_id` on your own value — correct it rather than flagging
 * it — and `409` `CORRECTION_ALREADY_OPEN` when somebody has already flagged it. Neither is
 * swallowed: both mean the screen was drawn from a state that is no longer true.
 */
export async function flagObservation(
  observationId: string,
  flagging: Flagging,
): Promise<CorrectionRequest> {
  const note = (flagging.note ?? '').trim();
  const body = await unwrap(
    api.POST('/v1/observations/{id}/flag', {
      params: { ...writing(), path: { id: observationId } },
      body: {
        event_id: newEventId(),
        reason_code: flagging.reasonCode,
        ...(note === '' ? {} : { note }),
      },
    }),
  );
  return body.request;
}

/**
 * The corrected value, in whichever of the contract's five shapes this code uses.
 *
 * All optional, because a code is numeric or text or boolean or coded and never two of them,
 * and a shape that required a number would make correcting a text value impossible. The form
 * sends the one field that matches the original's `value_type`; sending two would be a
 * request the server has to decide between, and it should never have to.
 */
export interface Correcting {
  value?: number;
  /** The unit the corrected value was **entered** in, not the canonical one. */
  unit?: string;
  valueText?: string;
  valueBool?: boolean;
  valueCode?: string;
  /** What the corrector wants the person who flagged it to know. Read by them, not counted. */
  note?: string;
}

/**
 * Correct the value: a new observation replaces the flagged one and everything derived from
 * it recomputes.
 *
 * Who may call it, and why the client does not decide: the person the request was routed to
 * needs no permission at all — asking somebody to hold a permission to fix their own mistake
 * is how mistakes stay — and anybody else needs `observation.correct.approve`, which makes the
 * result `OVERRIDDEN` rather than `APPLIED`. The **server** reads the caller's own permissions
 * to tell those apart; there is no flag in this body saying which act it is, and there must
 * not be, because a client that could nominate itself as the author would be able to write a
 * supervisor's fix onto an operator's quality record.
 *
 * Answers `403` when the request is somebody else's to answer, and `409`
 * `CORRECTION_ALREADY_ANSWERED` when it has already been answered.
 */
export async function applyCorrection(
  requestId: string,
  correcting: Correcting,
): Promise<CorrectionRequest> {
  const note = (correcting.note ?? '').trim();
  const unit = (correcting.unit ?? '').trim();
  const text = (correcting.valueText ?? '').trim();
  const code = (correcting.valueCode ?? '').trim();

  const body = await unwrap(
    api.POST('/v1/corrections/{id}/apply', {
      params: { ...writing(), path: { id: requestId } },
      body: {
        event_id: newEventId(),
        ...(correcting.value === undefined ? {} : { value: correcting.value }),
        // An empty unit is omitted rather than sent: a unitless code takes no unit, and the
        // server refuses one on a code that has none.
        ...(unit === '' ? {} : { unit }),
        ...(text === '' ? {} : { value_text: text }),
        ...(correcting.valueBool === undefined ? {} : { value_bool: correcting.valueBool }),
        ...(code === '' ? {} : { value_code: code }),
        ...(note === '' ? {} : { note }),
      },
    }),
  );
  return body.request;
}

/**
 * What an answer form holds while it is being filled in.
 *
 * `null` means "the operator has not touched this box", which is not the same as an empty box:
 * an untouched field falls back to what the original said, and a cleared one is a field the
 * form refuses to send. Three fields rather than one because a code is numeric, or text, or
 * boolean, or coded, and never two of them.
 */
export interface AnswerDraft {
  value: string | null;
  unit: string | null;
  text: string | null;
  note: string;
}

/**
 * What an answer form starts with: the value as it was typed, in the unit it was typed in.
 *
 * # Why this is here and not in the component
 *
 * The form has to do this twice — once to fill the boxes, and once to read them back when the
 * button is pressed — and the first version of it did the second half from raw component state,
 * which sent `0` for a number the operator had not retyped and dropped the unit entirely. That
 * is not a bug a test of the form's *rendering* can catch: the boxes looked right. One function,
 * used for both, makes the two halves the same by construction.
 *
 * 154 lb is stored as 69.85 kg and read back as 154 lb, so the number offered is
 * `entered_value` and the unit is `entered_unit`. Pre-filling the canonical pair would ask an
 * operator to check a number they never wrote.
 */
export function answerDefaults(observation: Observation): {
  value: string;
  unit: string;
  text: string;
} {
  const shape = observation.value_type;
  return {
    value: String(enteredValueOf(observation) ?? ''),
    unit: enteredUnitOf(observation) ?? '',
    text:
      shape === 'boolean'
        ? observation.value_bool === undefined
          ? ''
          : String(observation.value_bool)
        : shape === 'coded'
          ? (observation.value_code ?? '')
          : (observation.value_text ?? ''),
  };
}

/**
 * The correction to send, in the one shape this code uses.
 *
 * Reads the draft where the operator has touched it and the original where they have not, so
 * an untouched box sends what is on screen rather than an empty string — see `answerDefaults`.
 */
export function correctingFrom(observation: Observation, draft: AnswerDraft): Correcting {
  const started = answerDefaults(observation);
  const value = draft.value ?? started.value;
  const unit = draft.unit ?? started.unit;
  const text = draft.text ?? started.text;

  switch (observation.value_type) {
    case 'boolean':
      return { valueBool: text === 'true', note: draft.note };
    case 'coded':
      return { valueCode: text, note: draft.note };
    case 'numeric':
      return { value: Number(value), unit, note: draft.note };
    default:
      // `text` and anything the contract adds next. A structured value is refused by the form
      // before it reaches here; sending its text would be a claim about a shape nobody read.
      return { valueText: text, note: draft.note };
  }
}

/**
 * Whether an answer form has something to send.
 *
 * A numeric box that has been cleared, or filled with something that is not a number, is not a
 * correction — and "0" is: a pulse of zero is a finding, and a form that refused it because it
 * looked falsy would be a form that cannot record the one reading that matters most.
 */
export function correctionReady(observation: Observation, draft: AnswerDraft): boolean {
  const started = answerDefaults(observation);
  if (observation.value_type === 'numeric') {
    const value = (draft.value ?? started.value).trim();
    return value !== '' && Number.isFinite(Number(value));
  }
  return (draft.text ?? started.text).trim() !== '';
}

/**
 * Say the value stands, with a reason.
 *
 * The reason is a required positional parameter rather than an optional field, so this cannot
 * be called without one by leaving something out. "No" with no reason is how a flagging
 * culture dies: the physician who flagged it learns nothing, cannot tell a disagreement from
 * an oversight, and stops flagging — and a system nobody flags anything in has an audit trail
 * and no quality signal. The server refuses an empty one with `422` against `reason`, and a
 * form should refuse it first.
 */
export async function rejectCorrection(
  requestId: string,
  reason: string,
): Promise<CorrectionRequest> {
  const body = await unwrap(
    api.POST('/v1/corrections/{id}/reject', {
      params: { ...writing(), path: { id: requestId } },
      body: { event_id: newEventId(), reason: reason.trim() },
    }),
  );
  return body.request;
}

/**
 * The conflict codes, branched on by code rather than by status.
 *
 * `409` is not one fact on this surface: flagging answers it when somebody has already
 * flagged the value *and* when the value has already been replaced, and answering returns it
 * when the request has already been answered. A screen that read the number would put
 * "somebody has already flagged this" in front of a physician whose request failed for one of
 * the others.
 */
export const ALREADY_FLAGGED_CODE = 'CORRECTION_ALREADY_OPEN';
export const ALREADY_ANSWERED_CODE = 'CORRECTION_ALREADY_ANSWERED';

/** Whether this refusal means somebody has already asked for this value to be looked at. */
export function alreadyFlagged(error: unknown): boolean {
  return error instanceof ApiError && error.code === ALREADY_FLAGGED_CODE;
}

/** Whether this refusal means the request has already been answered by somebody. */
export function alreadyAnswered(error: unknown): boolean {
  return error instanceof ApiError && error.code === ALREADY_ANSWERED_CODE;
}

/**
 * Whether this refusal means the request is somebody else's to answer.
 *
 * `403` rather than `422`, because the request is well-formed and it is simply not this
 * person's. The screen turns it into a sentence naming who it was routed to, which is what an
 * operator staring at a refused button actually needs — who to go and find, rather than what
 * to retype.
 */
export function notYoursToAnswer(error: unknown): boolean {
  return error instanceof ApiError && error.status === 403;
}

/** Whether a request is still waiting for an answer. The server's own word for it. */
export function isOpen(request: CorrectionRequest): boolean {
  return request.status === 'OPEN';
}

/**
 * Whether somebody other than the value's author answered it.
 *
 * Its own predicate, and used only to *say so on screen*. It is never used to decide whether a
 * value was corrected: a supervisor's fix corrected the value just as thoroughly, and the
 * distinction this keeps alive is about whose record it goes on, not about the number.
 */
export function wasOverridden(request: CorrectionRequest): boolean {
  return request.status === 'OVERRIDDEN';
}

/**
 * The requests raised against one value, newest first.
 *
 * A value can carry more than one over its life: flagged, rejected, and flagged again by
 * somebody else a week later is an ordinary sequence and the whole of it is the record. A
 * helper returning only the latest would hide a rejection, which is the one outcome a
 * physician most needs to see before flagging the same number again.
 */
export function requestsOn(
  requests: readonly CorrectionRequest[],
  observationId: string,
): CorrectionRequest[] {
  return requests
    .filter((request) => request.observation_id === observationId)
    .sort((a, b) => Date.parse(b.requested_at) - Date.parse(a.requested_at));
}

/**
 * The open request on one value, where there is one.
 *
 * At most one can be open at a time — the server refuses a second with `409` — so this is a
 * question with a single answer rather than a list. It is what decides whether a flag control
 * is offered: a control that exists in order to be refused teaches an operator that the
 * software is unreliable.
 */
export function openRequestOn(
  requests: readonly CorrectionRequest[],
  observationId: string,
): CorrectionRequest | undefined {
  return requestsOn(requests, observationId).find(isOpen);
}

/**
 * Whether a reason code has been chosen. The floor the server puts under a flag, mirrored.
 *
 * The note is deliberately not required here, matching the contract: a physician who has
 * chosen `TRANSCRIPTION` has already said something a screen can count, and demanding prose
 * before they may say it would slow down the one act this whole workflow depends on people
 * doing. The screen still asks for it, in words, beside the box.
 */
export function flagReady(reasonCode: string): boolean {
  return reasonCode.trim() !== '';
}

/** The floor the server puts under a rejection reason, mirrored so a form can say so first. */
export const REJECT_REASON_MIN = 1;

export function rejectReasonAcceptable(reason: string): boolean {
  return reason.trim().length >= REJECT_REASON_MIN;
}

/**
 * Whether this reason is one CP63 counts as a transcription error.
 *
 * Exposed so a screen can say which of the reasons it is offering will be counted, rather
 * than counting somebody without telling them. A vocabulary where the consequence of one
 * choice is invisible is a vocabulary people learn to answer strategically.
 */
export function isTranscription(reason: CorrectionReason): boolean {
  return reason.transcription;
}

/** The vocabulary in the order the server gives it, ties broken by code for a stable list. */
export function reasonsInOrder(reasons: readonly CorrectionReason[]): CorrectionReason[] {
  return [...reasons].sort((a, b) => a.ordering - b.ordering || a.code.localeCompare(b.code));
}

/** One reason by code, for a screen naming the reason a request already carries. */
export function reasonByCode(
  reasons: readonly CorrectionReason[],
  code: string,
): CorrectionReason | undefined {
  return reasons.find((reason) => reason.code === code);
}

/**
 * Why a value cannot be flagged, where it cannot.
 *
 * Five answers rather than a boolean, because a screen has to do something different with each
 * of them and only the first is about permission:
 *
 *   - `not-permitted` — the caller does not hold `observation.correct.request`. The control is
 *     not drawn and nothing is said. This is the ordinary permission rule: a control that
 *     exists in order to be refused teaches people that the software is unreliable.
 *   - `viewer-unknown` — the session has not answered yet, so this client cannot tell whether
 *     the value is the reader's own. Its own answer rather than being folded into `your-own`,
 *     which would put a sentence about whose value this is on screen on the strength of a
 *     request that has not come back. The control waits; nothing is said.
 *   - `already-replaced` — a value that is no longer current has nothing to route. The row is
 *     already marked as corrected or superseded, so the screen says nothing further.
 *   - `already-flagged` — somebody has asked already, and a second flag is the same
 *     conversation. The open request is drawn on the row directly above, so again nothing
 *     further is said.
 *   - `your-own` — the server refuses this with `422`, saying "correct it rather than flagging
 *     it". Worth a sentence rather than silence: a physician who sees the control on one row
 *     and not the next would otherwise read the absence as a fault in the screen.
 *
 * `null` means it can be flagged.
 *
 * **The order is the order the sentences are worth saying in.** `your-own` is checked last on
 * purpose: on a value that is both yours and already replaced, "correct it rather than flagging
 * it" is true and useless — there is nothing left to correct — and the fact that matters is
 * that a later value has replaced it.
 */
export type FlagBlocker =
  'not-permitted' | 'viewer-unknown' | 'already-replaced' | 'already-flagged' | 'your-own';

export function whyNotFlaggable(
  observation: Observation,
  options: {
    viewerId: string | undefined;
    mayRequest: boolean;
    openRequest: CorrectionRequest | undefined;
  },
): FlagBlocker | null {
  if (!options.mayRequest) return 'not-permitted';
  if (options.viewerId === undefined) return 'viewer-unknown';
  if (isReplaced(observation)) return 'already-replaced';
  if (options.openRequest !== undefined) return 'already-flagged';
  if (observation.recorded_by === options.viewerId) return 'your-own';
  return null;
}

/**
 * Who may answer this request, from the viewer's point of view.
 *
 * `author` is the person it was routed to, who needs no permission for their own work.
 * `supervisor` is somebody else holding `observation.correct.approve`, whose answer the server
 * records as `OVERRIDDEN` rather than `APPLIED`. `nobody` is everybody else, and the control
 * is not drawn for them.
 *
 * The two are kept apart here so that the *form* can say which it is before the button is
 * pressed. A supervisor who did not know their fix would be recorded as an override is a
 * supervisor who has taken a decision about somebody else's quality record without being told.
 */
export type AnswerRole = 'author' | 'supervisor' | 'nobody';

export function answerRole(
  request: CorrectionRequest,
  options: { viewerId: string | undefined; mayApprove: boolean },
): AnswerRole {
  if (options.viewerId !== undefined && request.assigned_to === options.viewerId) return 'author';
  return options.mayApprove ? 'supervisor' : 'nobody';
}
