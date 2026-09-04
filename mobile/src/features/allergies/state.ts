import type { components } from '@dthcms/api-client';

import type { Coding } from '@/features/terminology';

/**
 * Station 4's allergy hard stop, as data (CP54, §3 step 4, [R-01]).
 *
 * # Why every decision is in here and none of it is in the screen
 *
 * A React Native component cannot be rendered outside a device, so anything it decides is a
 * decision nobody checks. What this step decides is not layout. It decides whether an empty
 * list is drawn as "no allergies" — which is a lie in the safe-looking direction, and the
 * exact failure this checkpoint exists to prevent — and it decides what an officer's tap
 * asserts, in that officer's name, in a clinical record a prescriber will rely on. Both of
 * those live in pure functions with tests beside them.
 *
 * # Four statuses, and an empty list means two opposite things
 *
 * `NONE_RECORDED` and `NO_KNOWN_ALLERGY` both come back with no allergies on them, and they
 * are opposite facts: nobody has asked, and somebody asked and was told there are none. There
 * is therefore no function in this file that reads a list and decides what it means. The
 * status is the server's answer, `readingOf` turns it into words, and the sentence for an
 * empty list is chosen by the **status** — `empty.NONE_RECORDED` and `empty.NO_KNOWN_ALLERGY`
 * are two different sentences, and neither can be reached from a list length.
 *
 * # `UNABLE_TO_ASSESS` is allergy status, and it is not reassurance
 *
 * Somebody looked, somebody is named, and the answer could not be got — with a reason, which
 * is mandatory here for the same purpose it is mandatory in the contract: the third state is
 * worth having only because it is reviewable, and "we could not ask" with no reason is a
 * silent gap wearing a label. `reassuring` is true for exactly one of the four statuses, and
 * it is not this one.
 *
 * # There is no override in this file, and there must never be one
 *
 * The gate is a trigger on the queue table. Nothing here enforces it and nothing here can get
 * past it: `mayAdvance` returns the server's own `satisfied`, unmodified, and there is no
 * function in this feature that constructs an `AllergyState`, that computes a status from a
 * list, or that produces a request body which is not one of the three real answers. The plan's
 * own risk note is that operators learn the shape of whatever clears the gate fastest, and a
 * client-side helper would be the easiest place in the system to give them a fourth shape.
 *
 * # Recording is made cheap on purpose
 *
 * If "no known allergies" is one tap and recording is twenty, the incentive points at the
 * answer that is a claim rather than the one that is a finding — and reflexive NKA is the risk
 * the plan names. So the substance comes from the picker's favourites at no keystrokes, the
 * reaction vocabulary is eight chips, and severity and certainty are one tap each.
 * `missingFrom` is what the screen counts down, so an officer can see the whole cost of the
 * honest answer before they start it.
 */

// --- what the contract gives us ---

export type AllergyReaction = components['schemas']['AllergyReaction'];
export type Allergy = components['schemas']['Allergy'];
export type AllergyAssertion = components['schemas']['AllergyAssertion'];
export type AllergyState = components['schemas']['AllergyState'];
export type AllergyChange = components['schemas']['AllergyChange'];
export type RecordAllergyRequest = components['schemas']['RecordAllergyRequest'];

export type AllergyStatus = AllergyState['status'];
export type AssertionKind = AllergyAssertion['kind'];
export type Severity = Allergy['severity'];
export type Certainty = Allergy['certainty'];
export type ReactionName = AllergyReaction['reaction'];

/** The interface language. Local rather than imported: this file must stay renderer-free. */
export type Locale = 'en' | 'bn';

/**
 * The four answers the server can give, in the order the table in the contract lists them.
 *
 * A list rather than a bare union because the message files are checked against it: a status
 * added to the contract without a sentence written for it is a test failure here rather than a
 * raw identifier where the explanation belongs.
 */
export const ALLERGY_STATUSES: readonly AllergyStatus[] = [
  'NONE_RECORDED',
  'ALLERGIES_RECORDED',
  'NO_KNOWN_ALLERGY',
  'UNABLE_TO_ASSESS',
];

/**
 * The two things an officer can assert, and there is no third.
 *
 * `UNABLE_TO_ASSESS` is the reason there is no override: the unconscious patient and the child
 * with no attendant are real, and this is the answer for them — somebody looked, somebody is
 * named, and the record says what was found, which is emphatically not that there are none.
 */
export const ASSERTION_KINDS: readonly AssertionKind[] = ['NO_KNOWN_ALLERGY', 'UNABLE_TO_ASSESS'];

/**
 * The three ways to satisfy the gate, and there is no fourth.
 *
 * Named as a list because the whole checkpoint rests on the list being exactly this long. A
 * screen draws one control per entry, the message files are checked against it, and a fourth
 * answer cannot be added by a screen — it would have to be added here, in front of a reviewer,
 * next to the sentence saying why there is not one.
 */
export const ANSWERS = ['ALLERGY', 'NO_KNOWN_ALLERGY', 'UNABLE_TO_ASSESS'] as const;
export type Answer = (typeof ANSWERS)[number];

/** Worst last, as the contract enumerates them. Nothing is pre-selected from this list. */
export const SEVERITIES: readonly Severity[] = ['mild', 'moderate', 'severe', 'life_threatening'];

/**
 * Suspected, and confirmed.
 *
 * A suspected reaction thirty years ago and a confirmed anaphylaxis are both worth recording
 * and they are not the same warning, so the form asks rather than assuming — an unasked
 * certainty defaulted to "confirmed" would put a claim in the record nobody made, and one
 * defaulted to "suspected" would quietly soften a reaction somebody watched happen.
 */
export const CERTAINTIES: readonly Certainty[] = ['suspected', 'confirmed'];

/** Everything one recorded allergy carries, in the order the question is actually asked. */
export const ALLERGY_FIELDS = ['substance', 'reaction', 'severity', 'certainty', 'note'] as const;
export type AllergyField = (typeof ALLERGY_FIELDS)[number];

/**
 * The catalogue an allergen is coded from: the clinic's own dictionary.
 *
 * ICD codes diseases, not substances, and there is no allergen axis in it that a history
 * officer could search. `DTHC` is where the clinic keeps what ICD has no code for (CP52), and
 * an allergen it does not yet hold is recorded in words and counted — which is how the
 * dictionary gets the missing entry rather than the officer getting the blame.
 */
export const ALLERGEN_SYSTEM = 'DTHC';

// --- reading the status ---

/**
 * How loud the status is, in the design tokens' own names.
 *
 * `NONE_RECORDED` is critical rather than merely unknown, because it is not a gap in a form:
 * it is a patient who cannot be sent to the next station, and a screen that drew it in the
 * grey it draws "not measured" in would be a screen where the refusal arrives at the queue
 * instead. `ALLERGIES_RECORDED` is critical whatever the severity — a "mild rash to
 * penicillin" is still a standing warning to whoever hands over the medicine, and softening
 * it here is the same quiet reassurance in a smaller font.
 *
 * Nothing is carried by the tone alone. Every state on this screen also says what it is in
 * words, because roughly one man in twelve who will work here cannot rely on the colour.
 */
export type Tone = 'normal' | 'borderline' | 'critical';

export function toneFor(status: string): Tone {
  switch (status) {
    case 'NO_KNOWN_ALLERGY':
      return 'normal';
    case 'UNABLE_TO_ASSESS':
      return 'borderline';
    default:
      // Including a status this build has never heard of. A tablet a version behind the
      // server must not render a fifth answer as calm.
      return 'critical';
  }
}

/** Whether this build knows what a status means at all. */
export function statusKnown(status: string): boolean {
  return (ALLERGY_STATUSES as readonly string[]).includes(status);
}

/** The status as the screen reads it — words, tone, and what it means for the gate. */
export interface Reading {
  status: AllergyStatus | string;
  /** False for a status this build has never heard of; `keyed` then reads `unknown`. */
  known: boolean;
  /**
   * The server's own answer to the gate's question, mirrored and never recomputed.
   *
   * The database trigger is the enforcement. This exists so a screen can say why the next
   * button is not available yet instead of discovering the refusal on submit.
   */
  satisfied: boolean;
  /** The patient cannot be sent on. The one thing on this screen that is a refusal. */
  blocked: boolean;
  /** True once somebody has answered the question, whatever the answer was. */
  asked: boolean;
  /**
   * True for exactly one of the four statuses.
   *
   * Not for `UNABLE_TO_ASSESS`, which satisfies the gate and is not a claim that there are
   * none; and not for `NONE_RECORDED`, whose empty list is the absence of a question rather
   * than the answer to one.
   */
  reassuring: boolean;
  /** Message key for the status in words. */
  headline: string;
  /** Message key for what the status means. */
  meaning: string;
  /**
   * Message key for the sentence drawn where the list would be when there are no allergies.
   *
   * Chosen by the **status**, never by the length of the list. Four statuses, four sentences,
   * and no path through this file that renders an empty list as "no allergies".
   */
  empty: string;
  tone: Tone;
  /** The allergies exactly as the server sent them: worst first, never re-sorted here. */
  allergies: readonly Allergy[];
  /** True when any recorded reaction is an emergency, for the word above the list. */
  emergency: boolean;
  /** The standing assertion, when there is one. */
  assertion: AllergyAssertion | null;
}

/**
 * Whether this patient may be sent past the history station.
 *
 * The server's own `satisfied`, and nothing else. It is deliberately not derived from the
 * status or the list: the answer belongs to the trigger on the queue table, and a client that
 * worked it out for itself would be a second, staler account of a rule it does not own — one
 * that would eventually disagree, and always in whichever direction the bug happened to fall.
 */
export function mayAdvance(state: AllergyState): boolean {
  return state.satisfied === true;
}

/** Whether anybody has answered the question yet. */
export function asked(state: AllergyState): boolean {
  return state.status !== 'NONE_RECORDED';
}

/**
 * Whether an emergency reaction is on file.
 *
 * The reaction's own property, from the vocabulary, rather than the severity somebody ticked
 * beside it: anaphylaxis is an emergency whatever was chosen from the list.
 */
export function anyEmergency(
  allergies: readonly Allergy[],
  reactions: readonly AllergyReaction[] = [],
): boolean {
  return allergies.some((allergy) => emergencyOf(allergy, reactions));
}

export function emergencyOf(allergy: Allergy, reactions: readonly AllergyReaction[] = []): boolean {
  if (allergy.is_emergency === true) return true;
  return reactionNamed(reactions, allergy.reaction)?.is_emergency === true;
}

/**
 * The whole status, in the words and the tone the screen draws.
 *
 * The one function a header or a station calls. Everything it returns is decided by the
 * server's `status` and `satisfied`; the list is passed through untouched, in the order it
 * arrived, because the server already leads with the reaction that stops a heart rather than
 * the rash from 1998.
 */
export function readingOf(
  state: AllergyState,
  reactions: readonly AllergyReaction[] = [],
): Reading {
  const known = statusKnown(state.status);
  const key = known ? state.status : 'unknown';
  return {
    status: state.status,
    known,
    satisfied: mayAdvance(state),
    blocked: !mayAdvance(state),
    asked: asked(state),
    // One status, and it is not the one whose list is empty because nobody asked.
    reassuring: state.status === 'NO_KNOWN_ALLERGY',
    headline: `status.${key}`,
    meaning: `meaning.${key}`,
    empty: `empty.${key}`,
    tone: toneFor(state.status),
    allergies: state.allergies,
    emergency: anyEmergency(state.allergies, reactions),
    assertion: state.assertion ?? null,
  };
}

// --- the list, in the order it arrived ---

export interface AllergyRow {
  allergy: Allergy;
  /** The substance in the reader's language, the patient's words, or the bare code. */
  label: string;
  /** False when the catalogue had nothing for this substance. Said out loud on the row. */
  coded: boolean;
  coding: Coding | null;
  /** What the patient called it. Kept on coded rows too, and the only field on uncoded ones. */
  said: string;
  /** The reaction in the reader's language, or the bare code if this build does not know it. */
  reaction: string;
  emergency: boolean;
  severity: Severity;
  certainty: Certainty;
}

/**
 * The recorded allergies, as rows, **in the order the server sent them**.
 *
 * Never re-sorted, and there is no comparator in this file. The server orders by emergency
 * reaction and then by severity in one statement, and a screen that sorted again — even by
 * severity, even innocently — would eventually put a mild rash above an anaphylaxis because
 * the two orderings disagreed about `life_threatening`. Walking the list once cannot reorder
 * anything, because it never holds two rows at the same time.
 */
export function allergyRows(
  allergies: readonly Allergy[],
  reactions: readonly AllergyReaction[],
  locale: Locale,
): AllergyRow[] {
  return allergies.map((allergy) => ({
    allergy,
    label: allergyLabel(allergy, locale),
    coded: coded(allergy),
    coding: codingFrom(allergy),
    said: (allergy.said ?? '').trim(),
    reaction: reactionTextOf(allergy, reactions, locale),
    emergency: emergencyOf(allergy, reactions),
    severity: allergy.severity,
    certainty: allergy.certainty,
  }));
}

/**
 * What a recorded allergy reads as, in the reader's language.
 *
 * The catalogue's title where there is one, the patient's own words where there is not — which
 * is the whole point of allowing an uncoded allergy — and the bare code before nothing at all.
 * A blank line in an allergy list is the worst row this app can draw: it reads as "checked,
 * nothing found".
 */
export function allergyLabel(allergy: Allergy, locale: Locale): string {
  if (locale === 'bn') {
    const bengali = (allergy.display_bn ?? '').trim();
    if (bengali !== '') return bengali;
  }
  const english = (allergy.display_en ?? '').trim();
  if (english !== '') return english;
  const said = (allergy.said ?? '').trim();
  if (said !== '') return said;
  return (allergy.code ?? '').trim();
}

/**
 * The reaction in words.
 *
 * The row's own `reaction_en`/`reaction_bn` first — the server joined them from the
 * vocabulary, so a wording corrected next year reads correctly on every row already recorded
 * with it — then this build's own copy of the vocabulary, and the bare code before a blank.
 */
export function reactionTextOf(
  allergy: Allergy,
  reactions: readonly AllergyReaction[],
  locale: Locale,
): string {
  const own = (locale === 'bn' ? allergy.reaction_bn : allergy.reaction_en) ?? '';
  if (own.trim() !== '') return own.trim();
  const known = reactionNamed(reactions, allergy.reaction);
  if (known !== null) return reactionLabel(known, locale);
  return allergy.reaction;
}

// --- the reaction vocabulary ---

/**
 * The reactions, in the clinic's order.
 *
 * Sorted by the vocabulary's own `ordering`, which is the column that exists to be sorted by.
 * The server already returns them ordered; doing it again cannot move anything and means the
 * chips still read emergency-first if a future response forgets to.
 */
export function reactionsInOrder(
  reactions: readonly AllergyReaction[],
): readonly AllergyReaction[] {
  return [...reactions].sort((a, b) => a.ordering - b.ordering);
}

export function reactionNamed(
  reactions: readonly AllergyReaction[],
  name: string,
): AllergyReaction | null {
  return reactions.find((reaction) => reaction.reaction === name) ?? null;
}

/**
 * Whether the vocabulary has this reaction.
 *
 * Checked here as well as at the server, because a row a header cannot render is worse than an
 * allergy nobody recorded: the blank line reads as "checked, nothing found". An empty
 * vocabulary — the tablet has not fetched it yet — refuses everything rather than waving it
 * through, so a reaction is never written against a list nobody has.
 */
export function knownReaction(reactions: readonly AllergyReaction[], name: string): boolean {
  // Trimmed, because `toRecording` trims what it sends: a check that disagreed with what
  // actually goes on the wire would refuse a request the server would have accepted, or —
  // worse the other way round — pass one it will not.
  return reactionNamed(reactions, name.trim()) !== null;
}

export function reactionLabel(reaction: AllergyReaction, locale: Locale): string {
  if (locale === 'bn') {
    const bengali = reaction.display_bn.trim();
    if (bengali !== '') return bengali;
  }
  const english = reaction.display_en.trim();
  return english === '' ? reaction.reaction : english;
}

// --- what a coding is ---

/** The three parts as they arrive on an allergy or a request: each present, or absent. */
export interface CodingParts {
  code_system?: string;
  code_version?: string;
  code?: string;
}

function codingPartsPresent(parts: CodingParts): number {
  return [parts.code_system, parts.code_version, parts.code].filter(
    (part) => (part ?? '').trim() !== '',
  ).length;
}

/**
 * The three fields as one coding, or nothing.
 *
 * The same guarantee as the picker's and station 4's history: there is no way through this
 * function to obtain two-thirds of a coding. A code with no version is a string, and a string
 * is what somebody discovers, four years later, cannot be read back — which for an allergen is
 * a warning nobody can act on.
 */
export function codingFrom(parts: CodingParts): Coding | null {
  if (codingPartsPresent(parts) !== 3) return null;
  return {
    system: (parts.code_system ?? '').trim(),
    version: (parts.code_version ?? '').trim(),
    code: (parts.code ?? '').trim(),
  };
}

/** One or two of the three: the shape the server refuses and this screen never sends. */
export function partialCoding(parts: CodingParts): boolean {
  const present = codingPartsPresent(parts);
  return present !== 0 && present !== 3;
}

/** Whether this allergy carries a whole coding, or is one of the ones kept in words. */
export function coded(allergy: Allergy): boolean {
  return codingFrom(allergy) !== null;
}

/** Whether a coding held on the draft is whole. A hand-built half is refused before sending. */
export function wholeCoding(coding: Coding | null): boolean {
  if (coding === null) return true;
  return !partialCoding({
    code_system: coding.system,
    code_version: coding.version,
    code: coding.code,
  });
}

// --- the draft ---

/**
 * One allergy being taken down, exactly as it sits on screen.
 *
 * Four answers and a note, and every one of the four is a tap: the substance from the picker's
 * favourites, and three chip rows. Nothing is pre-selected — a severity nobody chose is a
 * finding the form invented on the officer's behalf, and on this screen the form's inventions
 * end up in front of a prescriber.
 */
export interface AllergyDraft {
  coding: Coding | null;
  said: string;
  reaction: string;
  severity: Severity | '';
  certainty: Certainty | '';
  note: string;
}

export function emptyDraft(): AllergyDraft {
  return { coding: null, said: '', reaction: '', severity: '', certainty: '', note: '' };
}

/** A change to one or more of the draft's boxes. Nothing is decided here. */
export function edit(draft: AllergyDraft, patch: Partial<AllergyDraft>): AllergyDraft {
  return { ...draft, ...patch };
}

/** The clinician's coding, whole or not at all. There is no path here that stores two parts. */
export function setCoding(draft: AllergyDraft, coding: Coding | null): AllergyDraft {
  if (!wholeCoding(coding)) return draft;
  return { ...draft, coding };
}

/**
 * A draft nobody has touched yet.
 *
 * The refusals are meant to arrive as the officer works. A form that printed "say what the
 * patient reacted to" in red the instant it opened would be a form whose warnings are
 * furniture — read past on the way to the first control, and read past again when they matter.
 */
export function isPristine(draft: AllergyDraft): boolean {
  if (draft.coding !== null) return false;
  return [draft.said, draft.reaction, draft.severity, draft.certainty, draft.note].every(
    (value) => value.trim() === '',
  );
}

/**
 * What this allergy still needs, in the order it is asked for.
 *
 * The countdown the screen shows beside the three answers, so an officer can see the whole
 * cost of recording before they start it rather than discovering it four controls in. It is
 * also the measure of the promise this feature makes: the honest answer is four taps, and the
 * two assertions are one and two, which is the point — if recording were twenty, the incentive
 * would point at the claim rather than the finding.
 */
export function missingFrom(
  draft: AllergyDraft,
  reactions: readonly AllergyReaction[],
): readonly AllergyField[] {
  const missing: AllergyField[] = [];
  if (draft.coding === null && draft.said.trim() === '') missing.push('substance');
  if (!knownReaction(reactions, draft.reaction)) missing.push('reaction');
  if (draft.severity === '') missing.push('severity');
  if (draft.certainty === '') missing.push('certainty');
  return missing;
}

// --- what the server would refuse, refused here first ---

/**
 * Every refusal this step can produce, each one mirroring a rule the server enforces.
 *
 * A list rather than a bare union for the same reason station 4's history keeps one: the
 * message files are checked against it, so a refusal added without a sentence written for it
 * is a test failure rather than a raw identifier under somebody's finger.
 *
 * These exist so the officer sees the problem while the patient is still in front of them, not
 * as a 422 in a corridor. The server remains the authority, and the gate remains the database's.
 */
export const PROBLEMS = [
  'partialCoding',
  'nothingSaid',
  'needsReaction',
  'unknownReaction',
  'needsSeverity',
  'needsCertainty',
  'needsReason',
  'reasonRefused',
  'needsWithdrawalReason',
] as const;
export type Problem = (typeof PROBLEMS)[number];

export interface Refusal {
  problem: Problem;
  /** Where the sentence goes: a control of the form, or the reason box beneath an act. */
  where: AllergyField | 'reason';
}

/**
 * Everything wrong with this draft, in the order the server checks it.
 *
 * The reaction is checked against the vocabulary rather than trusted, because the alternative
 * is a row a header cannot render — and an allergy that shows as a blank line is worse than
 * one nobody recorded, since the blank line reads as "checked, nothing found".
 */
export function problemsWith(
  draft: AllergyDraft,
  reactions: readonly AllergyReaction[],
): Refusal[] {
  const found: Refusal[] = [];

  if (!wholeCoding(draft.coding)) {
    found.push({ problem: 'partialCoding', where: 'substance' });
  } else if (draft.coding === null && draft.said.trim() === '') {
    // Neither a coding nor words asserts only that the patient reacts to *something*, which
    // is the one allergy row a prescriber cannot act on.
    found.push({ problem: 'nothingSaid', where: 'substance' });
  }

  if (draft.reaction.trim() === '') {
    found.push({ problem: 'needsReaction', where: 'reaction' });
  } else if (!knownReaction(reactions, draft.reaction)) {
    found.push({ problem: 'unknownReaction', where: 'reaction' });
  }

  if (draft.severity === '') found.push({ problem: 'needsSeverity', where: 'severity' });
  if (draft.certainty === '') found.push({ problem: 'needsCertainty', where: 'certainty' });
  return found;
}

/** Whether this allergy may be written. An untouched draft is refused like any other. */
export function canRecord(draft: AllergyDraft, reactions: readonly AllergyReaction[]): boolean {
  return problemsWith(draft, reactions).length === 0;
}

/** The refusal, if any, sitting on one control. What the screen puts under it. */
export function refusalOn(
  refusals: readonly Refusal[],
  where: AllergyField | 'reason',
): Problem | null {
  return refusals.find((refusal) => refusal.where === where)?.problem ?? null;
}

// --- the two assertions ---

/**
 * What is wrong with an assertion, or nothing.
 *
 * Two rules, and they are mirror images. `UNABLE_TO_ASSESS` **requires** a reason because the
 * whole worth of the third state is that it is reviewable: "we could not ask" with nothing
 * after it is a silent gap wearing a label. `NO_KNOWN_ALLERGY` **refuses** one for the mirror
 * reason — text nobody will ever read, answering a question nobody asked.
 */
export function assertionProblem(kind: AssertionKind, reason: string): Problem | null {
  if (kind === 'UNABLE_TO_ASSESS') return reason.trim() === '' ? 'needsReason' : null;
  return reason.trim() === '' ? null : 'reasonRefused';
}

export function canAssert(kind: AssertionKind, reason: string): boolean {
  return assertionProblem(kind, reason) === null;
}

/** Whether this assertion asks for a reason at all. The screen draws the box from this. */
export function needsReason(kind: AssertionKind): boolean {
  return kind === 'UNABLE_TO_ASSESS';
}

// --- withdrawal ---

/**
 * A withdrawal with no reason, refused before it leaves the tablet.
 *
 * The reason is the point of the endpoint. An officer who tapped "no known allergies" on the
 * wrong patient has put a claim into a clinical record that a prescriber will rely on, and six
 * months later the reason is the only thing that distinguishes that from two clinicians
 * disagreeing.
 */
export function withdrawalRefused(reason: string): boolean {
  return reason.trim() === '';
}

// --- what gets sent ---

/** Every write this feature makes carries its own event id, and it is the idempotency key. */
export interface WriteIDs {
  event: string;
  visit?: string;
}

export type AllergyRecording = RecordAllergyRequest & { event_id: string };

export interface AllergyAssertionRequest {
  event_id: string;
  visit_id?: string;
  kind: AssertionKind;
  reason?: string;
}

export interface AllergyWithdrawal {
  event_id: string;
  visit_id?: string;
  reason: string;
}

function withVisit<T extends { visit_id?: string }>(body: T, ids: WriteIDs): T {
  if (ids.visit !== undefined && ids.visit !== '') body.visit_id = ids.visit;
  return body;
}

/**
 * One allergy, as one request.
 *
 * Never a partial coding: the three fields are written from `codingFrom`, which cannot produce
 * two of them. Returns null for a draft the server would refuse, so a caller cannot send one
 * by forgetting to ask.
 *
 * Recording does **not** withdraw an earlier "no known allergies", and nothing here tries to:
 * both are true statements about their own moment, and the server works out which one the
 * status is. A client that helpfully retracted the assertion would be deleting somebody's
 * honest answer from March.
 */
export function toRecording(
  draft: AllergyDraft,
  reactions: readonly AllergyReaction[],
  ids: WriteIDs,
): AllergyRecording | null {
  if (!canRecord(draft, reactions)) return null;

  const body: AllergyRecording = {
    event_id: ids.event,
    reaction: draft.reaction.trim(),
    // Both are non-empty here: `canRecord` refuses the draft otherwise.
    severity: draft.severity as Severity,
    certainty: draft.certainty as Certainty,
  };
  withVisit(body, ids);

  const coding = draft.coding;
  if (coding !== null) {
    body.code_system = coding.system;
    body.code_version = coding.version;
    body.code = coding.code;
  }
  // Kept on coded allergies too. The catalogue says "Penicillins"; the patient said "the
  // yellow tablet from the pharmacy near the bridge", and that is sometimes the only thing
  // that identifies what actually happened.
  if (draft.said.trim() !== '') body.said = draft.said.trim();
  if (draft.note.trim() !== '') body.note = draft.note.trim();
  return body;
}

/**
 * One of the two assertions, as one request.
 *
 * There are two kinds and no third, and this will not build a body for anything else: a caller
 * that invented a kind gets null rather than a request the server would have to refuse. Null
 * is also what an `UNABLE_TO_ASSESS` with no reason produces, and what a `NO_KNOWN_ALLERGY`
 * with one produces — the two rules that make the third state reviewable and the first state
 * a bare, unqualified claim with a name on it.
 */
export function toAssertion(
  kind: AssertionKind,
  reason: string,
  ids: WriteIDs,
): AllergyAssertionRequest | null {
  if (!(ASSERTION_KINDS as readonly string[]).includes(kind)) return null;
  if (!canAssert(kind, reason)) return null;

  const body: AllergyAssertionRequest = { event_id: ids.event, kind };
  withVisit(body, ids);
  if (needsReason(kind)) body.reason = reason.trim();
  return body;
}

/** Taking something back. Null while there is no reason, which is not a state that sends. */
export function toWithdrawal(reason: string, ids: WriteIDs): AllergyWithdrawal | null {
  if (withdrawalRefused(reason)) return null;
  const body: AllergyWithdrawal = { event_id: ids.event, reason: reason.trim() };
  return withVisit(body, ids);
}

// --- everything ever said ---

export interface ChangeRow {
  change: AllergyChange;
  kind: AllergyChange['kind'];
  /** What it was about: the substance, in words or in code. Empty on an assertion. */
  label: string;
  /** The certainty on an allergy; the reason on an assertion. */
  detail: string;
  /** True for an entry somebody took back. The reason this list exists at all. */
  withdrawn: boolean;
  /** Why it was taken back, when somebody said. */
  why: string;
}

/**
 * The change history, in the order the server sent it — newest first, withdrawn entries
 * included.
 *
 * Never filtered and never re-sorted. An allergy that was recorded and then taken back is a
 * clinical event: somebody believed it and somebody else disagreed, and both halves are worth
 * reading before writing a prescription. A list that hid the withdrawn half would leave the
 * next clinician unable to tell a correction from a thing that never happened.
 */
export function changeRows(changes: readonly AllergyChange[]): ChangeRow[] {
  return changes.map((change) => ({
    change,
    kind: change.kind,
    label: (change.said ?? '').trim() || (change.code ?? '').trim(),
    detail: (change.detail ?? '').trim(),
    withdrawn: (change.undone_at ?? '').trim() !== '',
    why: (change.undone_why ?? '').trim(),
  }));
}
