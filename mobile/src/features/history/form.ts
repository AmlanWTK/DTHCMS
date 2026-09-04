import type { components } from '@dthcms/api-client';

import type { Coding } from '@/features/terminology';

/**
 * Station 4's medical history, as data (CP53, §3 station 4).
 *
 * # Why every decision is in here and none of it is in the screen
 *
 * The screen cannot be rendered outside a device, so anything it decides is a decision
 * nobody checks. What this station decides is not layout. It decides whether a list somebody
 * carried forward from March gets written as a fresh set of assertions about today, whether a
 * code reaches the record without the version that makes it readable in 2032, and whether
 * "she no longer has this" and "this was never true" end up as the same row. Each of those is
 * a wrong claim in somebody's clinical record, so each lives in a pure function with a test
 * beside it.
 *
 * # The kind's rules come from the server; there is no switch on kind anywhere
 *
 * `/v1/history/kinds` returns `requires_relation`, `requires_duration`, `allows_severity`,
 * `allows_onset`, `is_medication` and `code_system` on every kind, and `fieldsFor` is
 * literally those booleans read in order. A screen that remembered which kind needed what
 * would eventually ask for a relative's name on a presenting complaint — and, worse, would
 * keep asking correctly for a year after the clinic changed the rule in the database.
 *
 * # Carry-forward is confirmation, and confirmation is one item at a time
 *
 * `confirmed_at` is the whole of it. An item with none has not been confirmed by anybody
 * since it was recorded, which is exactly what a returning patient's list looks like. This
 * file tells the screen **which** items are in that state and **why**, and nothing in this
 * feature confirms more than one of them: there is no batch endpoint, and a "confirm all"
 * tap would turn one action by a person into twenty assertions in a clinical record with
 * that person's name on each. Twenty carried-forward items is twenty presses and twenty
 * requests, on purpose.
 *
 * # A coding is three fields, and two of them is not a coding
 *
 * `codingFrom` returns a whole coding or nothing at all, and `partialCoding` is what the
 * screen refuses on. An item may legitimately carry none of the three — the catalogue does
 * not have everything a history officer meets — and then `said` carries what the patient
 * described and the item is **visibly** uncoded, because the uncoded count is the list of
 * concepts somebody should add.
 *
 * # Resolving is not removing
 *
 * Two words and two paths, never one control. `RESOLVED` is a clinical fact — she had this
 * and no longer does — and it stays on the list, because a record that hid it would make
 * every follow-up look like a first visit. Removal is a correction, it takes a reason, and
 * `removalRefused` is why an empty one never leaves the tablet.
 */

// --- what the contract gives us ---

export type HistoryKind = components['schemas']['HistoryKind'];
export type FamilyRelation = components['schemas']['FamilyRelation'];
export type HistoryItem = components['schemas']['HistoryItem'];
export type RecordHistoryItemRequest = components['schemas']['RecordHistoryItemRequest'];
export type AmendHistoryItemRequest = components['schemas']['AmendHistoryItemRequest'];

export type KindName = HistoryKind['kind'];
export type Severity = NonNullable<HistoryItem['severity']>;
export type OnsetPrecision = NonNullable<HistoryItem['onset_precision']>;
export type ItemStatus = HistoryItem['status'];

/** The interface language. Local rather than imported: this file must stay renderer-free. */
export type Locale = 'en' | 'bn';

export const SEVERITIES: readonly Severity[] = ['mild', 'moderate', 'severe'];

/**
 * How exact a start date is.
 *
 * A patient who says "about two years ago" has given a real answer, and storing it as an
 * exact date makes a guess look like a measurement. The precision travels with the date and
 * is refused without it, which is why they are set together and never apart.
 */
export const ONSET_PRECISIONS: readonly OnsetPrecision[] = ['day', 'month', 'year'];

// --- which fields a kind asks for ---

/**
 * Everything a history item can carry beyond its coding, in the order the conversation goes.
 *
 * The words first, because what the patient said is what the officer has before anything
 * else; then who it is about, how long, when it started, how bad, and — for a medicine — how
 * much and how often.
 */
export const HISTORY_FIELDS = [
  'said',
  'relation',
  'duration',
  'onset',
  'severity',
  'dose',
  'frequency',
] as const;
export type HistoryField = (typeof HISTORY_FIELDS)[number];

export interface FieldAsk {
  field: HistoryField;
  /**
   * Required by the kind's own rule, whatever has been typed. Only the two the server names
   * unconditionally: a family history is about a relative, and a complaint says how long.
   *
   * `said` is not marked here because its requirement is conditional — it is required when
   * there is no coding — and a label that said "required" over a box the officer may
   * legitimately leave empty is a label that teaches people to ignore the word.
   */
  required: boolean;
}

/**
 * The fields this kind asks for, read straight off the server's rules.
 *
 * There is deliberately no `switch (kind)` in this file. Every branch below is one of the
 * booleans `/v1/history/kinds` returns, so a clinic that changes which kinds carry a severity
 * changes this screen by changing a row — and a test that changes the rules changes the
 * fields, which is the only way to know the screen is reading them at all.
 */
export function fieldsFor(kind: HistoryKind): readonly FieldAsk[] {
  const asks: FieldAsk[] = [
    // Always. An uncoded item must say what was meant, and a coded one is better for having
    // the patient's own words beside the catalogue's.
    { field: 'said', required: false },
  ];
  if (kind.requires_relation) asks.push({ field: 'relation', required: true });
  if (kind.requires_duration) asks.push({ field: 'duration', required: true });
  if (kind.allows_onset) asks.push({ field: 'onset', required: false });
  if (kind.allows_severity) asks.push({ field: 'severity', required: false });
  if (kind.is_medication) {
    asks.push({ field: 'dose', required: false });
    asks.push({ field: 'frequency', required: false });
  }
  return asks;
}

/** Whether this kind asks for this field at all. */
export function asks(kind: HistoryKind, field: HistoryField): boolean {
  return fieldsFor(kind).some((ask) => ask.field === field);
}

/**
 * The six kinds, in the order station 4 asks.
 *
 * Sorted by the kind's own `ordering`, which is the column that exists to be sorted by — not
 * by name, and not by the order they happened to arrive in. The server already returns them
 * ordered; doing it again here cannot move anything and means a screen still asks in the
 * clinic's order if a future response forgets to.
 */
export function kindsInOrder(kinds: readonly HistoryKind[]): readonly HistoryKind[] {
  return [...kinds].sort((a, b) => a.ordering - b.ordering);
}

/** The relations, in the order the clinic lists them. Same rule, same reason. */
export function relationsInOrder(relations: readonly FamilyRelation[]): readonly FamilyRelation[] {
  return [...relations].sort((a, b) => a.ordering - b.ordering);
}

export function kindNamed(kinds: readonly HistoryKind[], name: string): HistoryKind | null {
  return kinds.find((kind) => kind.kind === name) ?? null;
}

// --- what a coding is ---

/** The three parts as they arrive on an item or a request: each present, or absent. */
export interface CodingParts {
  code_system?: string;
  code_version?: string;
  code?: string;
}

/** How many of the three are actually there. Nought or three; anything else is a mistake. */
function codingPartsPresent(parts: CodingParts): number {
  return [parts.code_system, parts.code_version, parts.code].filter(
    (part) => (part ?? '').trim() !== '',
  ).length;
}

/**
 * The three fields as one coding, or nothing.
 *
 * The `codingOf` of this station, and the same guarantee as the picker's: there is no way
 * through this function to obtain two-thirds of a coding. A code with no version is a string,
 * and a string is what somebody discovers, four years later, cannot be read back. Two of the
 * three returns null rather than a repaired coding — guessing the missing third is how a
 * coding acquires a version nobody searched.
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

/** Whether an item carries a whole coding. The number criterion 1 is measured by. */
export function coded(item: HistoryItem): boolean {
  return codingFrom(item) !== null;
}

/**
 * Whether this coding may be filed under this kind.
 *
 * A complaint comes from the clinic's own dictionary and a comorbidity from ICD, and an item
 * coded from the wrong catalogue would make the record assert that a patient *presented
 * with* type 2 diabetes — a claim nobody made. Compared case-insensitively because the
 * server compares it that way.
 */
export function fromKindsCatalogue(kind: HistoryKind, coding: Coding | null): boolean {
  if (coding === null) return true;
  return coding.system.trim().toLowerCase() === kind.code_system.trim().toLowerCase();
}

// --- the draft ---

/**
 * One item being taken down, exactly as it sits on screen.
 *
 * Numbers and dates are held as text for the same reason every other station holds them as
 * text: a half-typed date is a real state a person can be in, and a form that parsed on every
 * keystroke would delete the officer's second digit.
 */
export interface HistoryDraft {
  kind: string;
  coding: Coding | null;
  said: string;
  relation: string;
  duration: string;
  onsetOn: string;
  onsetPrecision: OnsetPrecision | '';
  severity: Severity | '';
  dose: string;
  frequency: string;
}

export function emptyDraft(kind = ''): HistoryDraft {
  return {
    kind,
    coding: null,
    said: '',
    relation: '',
    duration: '',
    onsetOn: '',
    onsetPrecision: '',
    severity: '',
    dose: '',
    frequency: '',
  };
}

/** A change to one or more of the draft's boxes. Nothing is decided here. */
export function edit(draft: HistoryDraft, patch: Partial<HistoryDraft>): HistoryDraft {
  return { ...draft, ...patch };
}

/**
 * Changing what is being recorded.
 *
 * Everything the new kind does not ask for goes, including the coding. Not tidiness: the
 * server refuses a severity on a kind that carries none and a coding from another kind's
 * catalogue, so a field left behind from the previous kind is a 422 the officer meets after
 * the patient has finished speaking. Clearing it is also the honest reading of the act —
 * "actually this is a medicine, not a complaint" is a different item, not an edited one.
 */
export function setKind(draft: HistoryDraft, kind: HistoryKind): HistoryDraft {
  const keep = (field: HistoryField, value: string) => (asks(kind, field) ? value : '');
  return {
    ...draft,
    kind: kind.kind,
    // The coding always goes: each kind draws on its own catalogue, and a code kept across a
    // change of kind is a code from the wrong one.
    coding: null,
    said: draft.said,
    relation: keep('relation', draft.relation),
    duration: keep('duration', draft.duration),
    onsetOn: keep('onset', draft.onsetOn),
    onsetPrecision: asks(kind, 'onset') ? draft.onsetPrecision : '',
    severity: asks(kind, 'severity') ? draft.severity : '',
    dose: keep('dose', draft.dose),
    frequency: keep('frequency', draft.frequency),
  };
}

/**
 * The clinician's coding, if it belongs to this kind's catalogue.
 *
 * A coding from another catalogue is not stored at all rather than stored and refused later,
 * for the same reason the picker will not store two-thirds of a coding: the guarantee is
 * structural, so there is no path through this file that puts an ICD diagnosis on a
 * presenting complaint.
 */
export function setCoding(
  draft: HistoryDraft,
  kind: HistoryKind,
  coding: Coding | null,
): HistoryDraft {
  if (!fromKindsCatalogue(kind, coding)) return draft;
  return { ...draft, coding };
}

/**
 * An onset date and its precision, together.
 *
 * One setter rather than two, because the server refuses either without the other and two
 * setters is two moments at which they can be apart.
 */
export function setOnset(
  draft: HistoryDraft,
  onsetOn: string,
  precision: OnsetPrecision | '',
): HistoryDraft {
  return { ...draft, onsetOn, onsetPrecision: precision };
}

// --- how long it has been going on ---

/**
 * The durations a complaint usually turns out to be, in days.
 *
 * Criterion 1 wants coded complaints taken quickly, and "how long" is asked of every one of
 * them. A number pad for "two weeks" is four taps and a unit conversion done in somebody's
 * head; a chip is one. The box stays, because plenty of complaints are not one of six
 * numbers.
 */
export const DURATION_PRESETS: readonly { key: string; days: number }[] = Object.freeze([
  { key: 'day1', days: 1 },
  { key: 'week1', days: 7 },
  { key: 'week2', days: 14 },
  { key: 'month1', days: 30 },
  { key: 'month3', days: 90 },
  { key: 'year1', days: 365 },
]);

/**
 * The duration as typed, in whole days.
 *
 * Zero is a real answer — it started today — so this cannot reuse anthropometry's rule that
 * treats a non-positive number as "not measured". What it refuses is a duration that is not a
 * count of days: a fraction, a negative, a word.
 */
export function parsedDuration(text: string): number | null {
  const trimmed = text.trim();
  if (trimmed === '') return null;
  const value = Number(trimmed);
  if (!Number.isInteger(value) || value < 0) return null;
  return value;
}

// --- what the server would refuse, refused here first ---

/**
 * Every refusal this form can produce, each one mirroring a rule the server enforces.
 *
 * A list rather than a bare union for the same reason the examination's prompt rules are a
 * list: the message files are checked against it, so a refusal added without a sentence
 * written for it is a test failure rather than a raw identifier under somebody's finger.
 *
 * These exist so that the officer sees the problem while the patient is still talking, not
 * as a 422 in a corridor. The server remains the authority — every one of these is also a
 * check in `check()` and a database trigger behind that.
 */
export const PROBLEMS = [
  'partialCoding',
  'wrongCatalogue',
  'nothingSaid',
  'needsRelation',
  'noRelation',
  'needsDuration',
  'notADuration',
  'noSeverity',
  'noOnset',
  'onsetPartial',
  'noDose',
] as const;
export type Problem = (typeof PROBLEMS)[number];

export interface Refusal {
  problem: Problem;
  /** Where to put the sentence: a field of the form, or the coding above it. */
  where: HistoryField | 'coding';
}

/**
 * Everything wrong with this draft, in the order the server checks it.
 *
 * The order matters only in that it is the server's: an officer who fixes the first thing
 * they are told about and presses again should not be told about a second thing the server
 * would have complained about first.
 */
export function problemsWith(draft: HistoryDraft, kind: HistoryKind): Refusal[] {
  const found: Refusal[] = [];
  const coding = draft.coding;

  if (coding !== null && !fromKindsCatalogue(kind, coding)) {
    found.push({ problem: 'wrongCatalogue', where: 'coding' });
  }
  if (coding === null && draft.said.trim() === '') {
    // An item with neither a coding nor words asserts only that the patient has *something*.
    found.push({ problem: 'nothingSaid', where: 'said' });
  }

  if (kind.requires_relation && draft.relation.trim() === '') {
    found.push({ problem: 'needsRelation', where: 'relation' });
  }
  if (!kind.requires_relation && draft.relation.trim() !== '') {
    found.push({ problem: 'noRelation', where: 'relation' });
  }

  if (kind.requires_duration && draft.duration.trim() === '') {
    found.push({ problem: 'needsDuration', where: 'duration' });
  }
  if (draft.duration.trim() !== '' && parsedDuration(draft.duration) === null) {
    found.push({ problem: 'notADuration', where: 'duration' });
  }

  if (!kind.allows_severity && draft.severity !== '') {
    found.push({ problem: 'noSeverity', where: 'severity' });
  }
  if (!kind.allows_onset && draft.onsetOn.trim() !== '') {
    found.push({ problem: 'noOnset', where: 'onset' });
  }
  if ((draft.onsetOn.trim() === '') !== (draft.onsetPrecision === '')) {
    found.push({ problem: 'onsetPartial', where: 'onset' });
  }
  if (!kind.is_medication && (draft.dose.trim() !== '' || draft.frequency.trim() !== '')) {
    found.push({ problem: 'noDose', where: 'dose' });
  }
  return found;
}

/**
 * Whether this draft may be written.
 *
 * An untouched draft is refused like any other — `nothingSaid` covers the empty form — so the
 * button is simply not pressable until there is something to write.
 */
export function canRecord(draft: HistoryDraft, kind: HistoryKind): boolean {
  return problemsWith(draft, kind).length === 0;
}

/**
 * A draft nobody has typed into yet.
 *
 * The refusals are meant to arrive as the officer works, and a form that printed "say what the
 * patient said" in red the instant a kind was chosen would be a form whose warnings are
 * furniture — read past on the way to the first box, and then read past when they matter. A
 * pristine draft is still refused; it is just not corrected at somebody who has not started.
 */
export function isPristine(draft: HistoryDraft): boolean {
  if (draft.coding !== null) return false;
  return [
    draft.said,
    draft.relation,
    draft.duration,
    draft.onsetOn,
    draft.onsetPrecision,
    draft.severity,
    draft.dose,
    draft.frequency,
  ].every((value) => value.trim() === '');
}

/** The refusal, if any, sitting on one field. What the screen puts under the box. */
export function refusalOn(
  refusals: readonly Refusal[],
  where: HistoryField | 'coding',
): Problem | null {
  return refusals.find((refusal) => refusal.where === where)?.problem ?? null;
}

// --- what gets sent ---

/**
 * One item, as one request.
 *
 * Never a whole history in one call, and never a partial coding: the three fields are written
 * from `codingFrom`, which cannot produce two of them. Empty boxes are absent rather than
 * empty strings — on this endpoint an empty string is not "unchanged", but sending one for a
 * field the kind does not carry is how a complaint acquires a blank dose.
 *
 * Returns null for a draft the server would refuse, so a caller cannot send one by forgetting
 * to ask. The refusals are `problemsWith`'s, in the server's own order.
 */
export function toRecording(
  draft: HistoryDraft,
  kind: HistoryKind,
  ids: { event: string; visit?: string },
): RecordHistoryItemRequest | null {
  if (!canRecord(draft, kind)) return null;

  const body: RecordHistoryItemRequest = { event_id: ids.event, kind: kind.kind };
  if (ids.visit !== undefined && ids.visit !== '') body.visit_id = ids.visit;

  const coding = draft.coding;
  if (coding !== null) {
    body.code_system = coding.system;
    body.code_version = coding.version;
    body.code = coding.code;
  }

  // Kept on coded items too. The catalogue says "Type 2 diabetes mellitus without
  // complications"; the patient said "sugar since the flood", and the second one is the
  // clinical detail.
  if (draft.said.trim() !== '') body.said = draft.said.trim();

  if (asks(kind, 'relation') && draft.relation.trim() !== '') body.relation = draft.relation.trim();

  const duration = parsedDuration(draft.duration);
  if (asks(kind, 'duration') && duration !== null) body.duration_days = duration;

  if (asks(kind, 'severity') && draft.severity !== '') body.severity = draft.severity;
  if (asks(kind, 'onset') && draft.onsetOn.trim() !== '' && draft.onsetPrecision !== '') {
    body.onset_on = draft.onsetOn.trim();
    body.onset_precision = draft.onsetPrecision;
  }
  if (asks(kind, 'dose')) {
    if (draft.dose.trim() !== '') body.dose = draft.dose.trim();
    if (draft.frequency.trim() !== '') body.frequency = draft.frequency.trim();
  }
  return body;
}

// --- carry-forward ---

/**
 * Why an item is waiting for somebody to say it is still true.
 *
 * Two answers, and they are different sentences. `neverConfirmed` is an item recorded at some
 * earlier visit that nobody has spoken to the patient about since; `notThisVisit` is one that
 * was confirmed, but before today, which is the same question asked again because the answer
 * can have changed. Both need a press; only one of them is news.
 */
export const CARRY_REASONS = ['neverConfirmed', 'notThisVisit'] as const;
export type CarryReason = (typeof CARRY_REASONS)[number];

export interface CarriedItem {
  item: HistoryItem;
  /** True while nobody has said, this visit, that this is still true. */
  needsConfirmation: boolean;
  /** Why, in words the screen has a sentence for. Null once it has been confirmed today. */
  reason: CarryReason | null;
  /** Whether the item carries a whole coding, or is one of the uncoded ones. */
  coded: boolean;
  /** What the patient said, kept whether or not the item is coded. */
  said: string;
  resolved: boolean;
}

/**
 * A moment, as milliseconds, or null when it cannot be read.
 *
 * Unreadable is not the same as absent and both are handled deliberately below: an item whose
 * timestamps this app cannot parse is asked about again, because asking twice costs a
 * sentence and assuming a confirmation nobody made is the failure this whole station exists
 * to prevent.
 */
function momentOf(value: string | undefined): number | null {
  if (value === undefined || value.trim() === '') return null;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? null : parsed;
}

/**
 * Whether this item is waiting on somebody, and why.
 *
 * `since` is the start of this visit. It is the caller's, because "this visit" is a question
 * about the visit and this file does not own visits.
 *
 * A resolved item is not asked about: "she had this and no longer does" is a settled fact,
 * not a claim waiting for support. Everything else with no `confirmed_at`, or one older than
 * this visit, is asked about — which is exactly what a returning patient's list looks like.
 */
export function carryReasonFor(item: HistoryItem, since: string): CarryReason | null {
  if (item.status !== 'ACTIVE') return null;
  if ((item.confirmed_at ?? '').trim() === '') return 'neverConfirmed';

  const confirmed = momentOf(item.confirmed_at);
  const start = momentOf(since);
  // Somebody confirmed this at some point but the app cannot tell when — an unreadable
  // timestamp, or an unreadable `since`. It asks again rather than assuming, for the reason
  // in `momentOf`, and says the milder of the two things: confirmed, but not known to be
  // today.
  if (confirmed === null || start === null) return 'notThisVisit';
  return confirmed < start ? 'notThisVisit' : null;
}

export function needsConfirmation(item: HistoryItem, since: string): boolean {
  return carryReasonFor(item, since) !== null;
}

/** One item as the screen reads it. */
export function carriedItem(item: HistoryItem, since: string): CarriedItem {
  const reason = carryReasonFor(item, since);
  return {
    item,
    needsConfirmation: reason !== null,
    reason,
    coded: coded(item),
    said: (item.said ?? '').trim(),
    resolved: item.status === 'RESOLVED',
  };
}

/**
 * The patient's history as the station reads it, in the order the server sent it.
 *
 * Never re-sorted. The server orders by the kind's own `ordering`, then by when each item was
 * recorded, and floating the unconfirmed ones to the top here would move an item away from
 * the one recorded beside it on the same afternoon — which is how the officer remembers them.
 * The count of what is outstanding does the work a re-sort would have done.
 */
export function carryForward(items: readonly HistoryItem[], since: string): CarriedItem[] {
  return items.map((item) => carriedItem(item, since));
}

/** The items still waiting on a person, in the same order, for the working list. */
export function needingConfirmation(items: readonly HistoryItem[], since: string): CarriedItem[] {
  return carryForward(items, since).filter((carried) => carried.needsConfirmation);
}

/**
 * How much of the carried list is still outstanding.
 *
 * A count, and deliberately the only aggregate in this file. It is what tells the officer
 * they are not finished; it is not a control, and there is nothing in this feature it can be
 * wired to that would clear it in one press.
 */
export function outstanding(
  items: readonly HistoryItem[],
  since: string,
): { done: number; total: number } {
  const carried = carryForward(items, since);
  // Resolved items are not waiting on anybody, so they are not part of the denominator: a
  // progress line that could never reach its total is a line people stop reading.
  const asked = carried.filter((entry) => !entry.resolved);
  return {
    done: asked.filter((entry) => !entry.needsConfirmation).length,
    total: asked.length,
  };
}

// --- how the list is grouped ---

export interface KindGroup {
  kind: HistoryKind;
  items: CarriedItem[];
  /** How many of this kind nobody has confirmed this visit. */
  outstanding: number;
}

/**
 * The history under the six headings, in the order station 4 asks.
 *
 * Every kind gets a group, including the empty ones: "no operations recorded" is an answer,
 * and a heading that vanished when there was nothing under it would leave the officer unable
 * to tell "nobody asked" from "asked and there is none".
 */
export function groupByKind(
  items: readonly HistoryItem[],
  kinds: readonly HistoryKind[],
  since: string,
): KindGroup[] {
  const carried = carryForward(items, since);
  return kindsInOrder(kinds).map((kind) => {
    const mine = carried.filter((entry) => entry.item.kind === kind.kind);
    return {
      kind,
      items: mine,
      outstanding: mine.filter((entry) => entry.needsConfirmation).length,
    };
  });
}

/**
 * Items whose kind this build has never heard of.
 *
 * It should not happen — the six are reference data — which is exactly why they are surfaced
 * rather than dropped. A tablet a version behind the server would otherwise silently hide a
 * whole kind of history from the officer working through the list.
 */
export function ofUnknownKind(
  items: readonly HistoryItem[],
  kinds: readonly HistoryKind[],
  since: string,
): CarriedItem[] {
  const known = new Set<string>(kinds.map((kind) => kind.kind));
  return carryForward(items, since).filter((entry) => !known.has(entry.item.kind));
}

// --- what an item is called ---

/**
 * What an item reads as, in the reader's language.
 *
 * The catalogue's title where there is one, because a title corrected next year should read
 * correctly on every item coded with it. What the patient said where there is not — which is
 * the whole point of allowing an uncoded item — and the bare code only if somehow neither,
 * never an empty row. A blank line in a history list is worse than one in the wrong language:
 * the officer cannot tell whether it is a bad translation or a lost record.
 */
export function itemLabel(item: HistoryItem, locale: Locale): string {
  if (locale === 'bn') {
    const bengali = (item.display_bn ?? '').trim();
    if (bengali !== '') return bengali;
  }
  const english = (item.display_en ?? '').trim();
  if (english !== '') return english;
  const said = (item.said ?? '').trim();
  if (said !== '') return said;
  return (item.code ?? '').trim();
}

/** The item's coding, whole or not at all. What the chip on the row shows. */
export function itemCoding(item: HistoryItem): Coding | null {
  return codingFrom(item);
}

/** How many items of this kind this clinic could not code. The catalogue's to-do list. */
export function uncodedCount(counts: Record<string, number>, kind: string): number {
  return counts[kind] ?? 0;
}

// --- resolving, and removing, which are not the same act ---

/**
 * "She had this and no longer does."
 *
 * A clinical fact, kept: the item stays on the list with its status showing, because a record
 * that hid resolved conditions would make every follow-up look like a first visit. It is a
 * `PATCH`, and the server treats an amendment as a fresh assertion — so this confirms as it
 * changes, which is right, because somebody has just spoken to the patient about it.
 */
export function toResolution(ids: { event: string; visit?: string }): AmendHistoryItemRequest {
  const body: AmendHistoryItemRequest = { event_id: ids.event, status: 'RESOLVED' };
  if (ids.visit !== undefined && ids.visit !== '') body.visit_id = ids.visit;
  return body;
}

/** "She has it again." The other half of the same control, and still not a removal. */
export function toReactivation(ids: { event: string; visit?: string }): AmendHistoryItemRequest {
  const body: AmendHistoryItemRequest = { event_id: ids.event, status: 'ACTIVE' };
  if (ids.visit !== undefined && ids.visit !== '') body.visit_id = ids.visit;
  return body;
}

/**
 * A removal with no reason, refused before it leaves the tablet.
 *
 * The reason is the point of the endpoint. An item somebody removed is an item somebody
 * disagreed with, and what they disagreed with is the interesting part — six months later it
 * is the only thing that distinguishes "recorded on the wrong patient" from a disagreement
 * between two clinicians.
 */
export function removalRefused(reason: string): boolean {
  return reason.trim() === '';
}

export interface Removal {
  event_id: string;
  visit_id?: string;
  reason: string;
}

/** "This was never true." Null while there is no reason, which is not a state that sends. */
export function toRemoval(reason: string, ids: { event: string; visit?: string }): Removal | null {
  if (removalRefused(reason)) return null;
  const body: Removal = { event_id: ids.event, reason: reason.trim() };
  if (ids.visit !== undefined && ids.visit !== '') body.visit_id = ids.visit;
  return body;
}

// --- the lifestyle station's answers, shown and never asked ---

/** One observation as the patient's record holds it, newest first. */
export interface ObservationRow {
  code: string;
  value_code?: string | null;
  value?: number | null;
  unit?: string | null;
  effective_at?: string | null;
}

export interface LifestyleRow {
  code: string;
  /** The coded answer the lifestyle station recorded, or `''` when nobody has yet. */
  valueCode: string;
  /** False when this clinic has not asked yet. Shown as such, never as a blank. */
  known: boolean;
}

/**
 * What the lifestyle station has already answered, for display only.
 *
 * `from_lifestyle_station` is the server's own list of observation codes that belong to
 * another station, and station 4 shows them and never asks for them again — a second copy
 * would be two answers to one question with no way to tell which is current. There is
 * deliberately no setter anywhere in this feature for any of these codes; the only thing this
 * function can produce is a row to read.
 *
 * Rows arrive newest first, so the first sighting of a code is the current answer.
 */
export function lifestyleRows(
  codes: readonly string[],
  observations: readonly ObservationRow[] | undefined,
): LifestyleRow[] {
  const latest = new Map<string, string>();
  for (const row of observations ?? []) {
    if (latest.has(row.code)) continue;
    latest.set(row.code, (row.value_code ?? '').trim());
  }
  return codes.map((code) => {
    const value = latest.get(code);
    return {
      code,
      valueCode: value ?? '',
      known: value !== undefined && value !== '',
    };
  });
}
