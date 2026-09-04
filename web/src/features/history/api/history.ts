import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import type { ConceptSelection } from '@/features/terminology';
import { api, unwrap } from '@/lib/api';

/**
 * Medical history, typed against the contract (CP53, §4.7).
 *
 * Eight calls, and the shape of the set is the design. Three of them are worth reading the
 * note for, because each is a place where an obvious convenience would break something the
 * record depends on.
 *
 * **`confirmHistoryItem` takes one id.** There is no batch endpoint and this layer does not
 * invent one — no `confirmAll`, no helper that maps over a list. A confirmation is a person
 * saying "yes, she is still on metformin", and a function that turned one click into twenty
 * of those would put twenty assertions in the ledger with one person's name on them and
 * nobody who actually made them. That is precisely the auto-acceptance acceptance criterion
 * 3 forbids, wearing a person's name. If a screen wants to confirm two items it calls this
 * twice, because a clinician answered twice.
 *
 * **`amendHistoryItem` and `removeHistoryItem` are not two spellings of one act.** A
 * `PATCH` with `status: 'RESOLVED'` says the patient had this and no longer does, which is
 * a clinical fact worth keeping and reading years later. `POST /remove` says this should
 * never have been recorded, and the server requires a reason because the disagreement is
 * the interesting part. Collapsing them would make "she stopped taking it in March"
 * indistinguishable from "that was somebody else's chart".
 *
 * **The coding is assembled in exactly one place.** `recordRequestFrom` is the only
 * function here that writes `code_system`, `code_version` and `code`, and it writes all
 * three or none of them. The server refuses two of the three — a code with no version is a
 * string — and a screen that built the body itself would eventually send a code stamped
 * with whatever version its picker was configured with rather than the one the catalogue
 * resolved.
 */

export type HistoryKind = components['schemas']['HistoryKind'];
export type FamilyRelation = components['schemas']['FamilyRelation'];
export type HistoryItem = components['schemas']['HistoryItem'];
export type RecordHistoryItemRequest = components['schemas']['RecordHistoryItemRequest'];
export type AmendHistoryItemRequest = components['schemas']['AmendHistoryItemRequest'];

/** The six kinds, as the contract names them. */
export type KindName = HistoryKind['kind'];
export type Severity = NonNullable<HistoryItem['severity']>;
export type OnsetPrecision = NonNullable<HistoryItem['onset_precision']>;

/**
 * What `/v1/history/kinds` answers with: the rules, the relations, and what belongs to
 * another station.
 *
 * `from_lifestyle_station` is the odd one and it is the useful one. Smoking and alcohol are
 * lifestyle observations recorded at station 6, and a history screen that offered them
 * again would produce two answers to one question with no way to tell which is current. The
 * list is what station 4 must *not* ask for, so it is displayed as a note rather than
 * silently dropped — a screen that just omitted them would look like a screen that forgot.
 */
export interface HistoryReference {
  kinds: HistoryKind[];
  relations: FamilyRelation[];
  from_lifestyle_station: string[];
}

/** The severities the contract allows, in the order a form should offer them. */
export const SEVERITIES: readonly Severity[] = ['mild', 'moderate', 'severe'];

/** How exact a start date is. Coarsest last, because "about two years ago" is the common case. */
export const ONSET_PRECISIONS: readonly OnsetPrecision[] = ['day', 'month', 'year'];

/**
 * The cache keys, held here rather than in the panel.
 *
 * Every write in this feature invalidates the patient's list, and the writes live on the
 * cards rather than on the panel that fetched it. Keeping the keys beside the calls is what
 * stops a card importing the panel it is rendered by — and two spellings of one key is a
 * confirmation that lands in the record and never appears on screen.
 */
export const HISTORY_KINDS_KEY = ['history', 'kinds'] as const;

export function historyItemsKey(patientId: string) {
  return ['history', 'items', patientId] as const;
}

/** The idempotency key a write carries. A browser has no offline queue; it still sends one. */
export function newEventId(): string {
  return crypto.randomUUID();
}

/** Reference data: the kinds and their rules, the relations, and the lifestyle codes. */
export async function listHistoryKinds(): Promise<HistoryReference> {
  return unwrap(api.GET('/v1/history/kinds'));
}

/**
 * How much of this facility's history could not be coded, by kind.
 *
 * Not a fault report. It is the list of concepts somebody should add to the catalogue — if
 * it grows, the dictionary is wrong rather than the officers.
 */
export async function countUncoded(): Promise<Record<string, number>> {
  const body = await unwrap(api.GET('/v1/history/uncoded'));
  return body.uncoded;
}

/**
 * Everything currently believed about this patient, in station order.
 *
 * Removed items are absent and resolved ones are present, both by the server's decision.
 * Nothing is re-sorted here: the order is the order station 4 asks in.
 */
export async function listMedicalHistory(patientId: string): Promise<HistoryItem[]> {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/medical-history', { params: { path: { id: patientId } } }),
  );
  return body.items;
}

/** One item, one request, one event. Never a whole history in one call. */
export async function recordHistoryItem(
  patientId: string,
  request: RecordHistoryItemRequest,
): Promise<HistoryItem> {
  const body = await unwrap(
    api.POST('/v1/patients/{id}/medical-history', {
      params: { ...writing(), path: { id: patientId } },
      body: { event_id: newEventId(), ...request },
    }),
  );
  return body.item;
}

/** One item, whatever its status — removed ones included, which the patient's list omits. */
export async function readHistoryItem(itemId: string): Promise<HistoryItem> {
  const body = await unwrap(
    api.GET('/v1/history/items/{itemId}', { params: { path: { itemId } } }),
  );
  return body.item;
}

/**
 * Change what is known about an item. Never what it *is* — not the kind, not the coding.
 *
 * An amendment confirms as it changes, on the server: somebody has just made a fresh
 * assertion about this item, and leaving it in the unconfirmed list would show an item
 * edited a minute ago as one nobody has looked at since last month.
 */
export async function amendHistoryItem(
  itemId: string,
  changes: AmendHistoryItemRequest,
): Promise<HistoryItem> {
  const body = await unwrap(
    api.PATCH('/v1/history/items/{itemId}', {
      params: { ...writing(), path: { itemId } },
      body: { event_id: newEventId(), ...changes },
    }),
  );
  return body.item;
}

/**
 * Say that **one** carried-forward item is still true.
 *
 * One id, deliberately. See the note at the top of this file before adding anything that
 * takes a list.
 */
export async function confirmHistoryItem(itemId: string, visitId?: string): Promise<HistoryItem> {
  const body = await unwrap(
    api.POST('/v1/history/items/{itemId}/confirm', {
      params: { ...writing(), path: { itemId } },
      body: { event_id: newEventId(), ...(visitId === undefined ? {} : { visit_id: visitId }) },
    }),
  );
  return body.item;
}

/**
 * Mark an item as one that should not have been recorded. Not a deletion, and not resolving.
 *
 * The reason is required by the server and is the point of the call: an item somebody
 * removed is an item somebody disagreed with, and the disagreement is what the next reader
 * needs. 204, so there is no item to return — the row stays and the ledger keeps both events.
 */
export async function removeHistoryItem(itemId: string, reason: string): Promise<void> {
  await unwrap(
    api.POST('/v1/history/items/{itemId}/remove', {
      params: { ...writing(), path: { itemId } },
      body: { event_id: newEventId(), reason },
    }),
  );
}

/**
 * The floor the server puts under a removal reason, mirrored so the form can say so first.
 *
 * A button that submits and then reports "reason is required" has cost the operator a round
 * trip to learn something the form knew before it was pressed.
 */
export const REASON_MIN = 1;

export function reasonAcceptable(reason: string): boolean {
  return reason.trim().length >= REASON_MIN;
}

/**
 * Whether anybody has said this is still true since it was recorded.
 *
 * Absent `confirmed_at` is not "unknown" and not "probably fine". It is the plain fact that
 * no person has asserted this item since the day it was written down, which is exactly what
 * a returning patient's list looks like.
 */
export function isConfirmed(item: HistoryItem): boolean {
  return item.confirmed_at !== undefined && item.confirmed_at !== '';
}

/**
 * Whether station 4 should ask about this item.
 *
 * A **resolved** item is deliberately not asked about, and the reason is what the question
 * means. "Is this still true?" is asked of things the record currently asserts; an item
 * somebody already marked resolved is the record saying she *had* this and no longer does,
 * which is an answer rather than an open question. Prompting for it would ask a clinician to
 * re-confirm a fact about the past every visit for the rest of the patient's life.
 *
 * The server draws the same line — `Item.NeedsConfirmation` refuses anything that is not
 * ACTIVE — and the two must agree, or the count in the banner counts rows the station has no
 * way to clear.
 */
export function needsConfirmation(item: HistoryItem): boolean {
  return item.status === 'ACTIVE' && !isConfirmed(item);
}

/**
 * Items nobody has confirmed, in the order the server returned them.
 *
 * A count, not a control. Nothing in this feature acts on the whole of this list at once.
 */
export function unconfirmedItems(items: readonly HistoryItem[]): HistoryItem[] {
  return items.filter(needsConfirmation);
}

/**
 * Whether the catalogue had nothing for what the patient described.
 *
 * All three coding fields or none, so any missing one means uncoded. Read as three separate
 * checks rather than one, because a half-coding is the state the server refuses and the one
 * this predicate must never quietly round up to "coded".
 */
export function isUncoded(item: HistoryItem): boolean {
  return !item.code_system || !item.code_version || !item.code;
}

/**
 * The item's coding in the shape `ConceptChip` shows, or nothing.
 *
 * `null` rather than a partial object: an uncoded item is a real state with its own display,
 * and a chip rendered from a coding with an empty version is the single failure CP52 exists
 * to prevent.
 */
export function itemCoding(item: HistoryItem): ConceptSelection | null {
  if (isUncoded(item)) return null;
  return {
    system: item.code_system as string,
    version: item.code_version as string,
    code: item.code as string,
    display_en: item.display_en ?? (item.code as string),
    ...(item.display_bn === undefined ? {} : { display_bn: item.display_bn }),
  };
}

/** One kind and the items filed under it. Empty groups are kept — see `groupByKind`. */
export interface HistoryGroup {
  kind: HistoryKind;
  items: HistoryItem[];
}

/**
 * The items grouped under the kinds, in the server's order.
 *
 * Two decisions here. The kinds are ordered by the server's own `ordering` — the sequence
 * station 4 asks in — and never by anything this client believes about clinical priority.
 * And a kind with nothing in it is still a group: a list of only what exists cannot show a
 * desk what it has not asked yet, and "no family history recorded" and "family history not
 * asked" are the same row on a screen that hides empty kinds.
 */
export function groupByKind(
  items: readonly HistoryItem[],
  kinds: readonly HistoryKind[],
): HistoryGroup[] {
  return [...kinds]
    .sort((a, b) => a.ordering - b.ordering)
    .map((kind) => ({ kind, items: items.filter((item) => item.kind === kind.kind) }));
}

/** The rules for one kind, or nothing if the server does not know that name. */
export function kindNamed(kinds: readonly HistoryKind[], name: string): HistoryKind | undefined {
  return kinds.find((kind) => kind.kind === name);
}

/**
 * What a form holds while it is being filled in.
 *
 * Every field is a string except the concept, because that is what an input gives back and
 * because "" and "not asked" have to be the same thing until the item is recorded. The
 * conversion to the contract's types — a number for the duration, three coding fields or
 * none — happens once, in `recordRequestFrom`.
 */
export interface HistoryDraft {
  kind: string;
  concept: ConceptSelection | null;
  said: string;
  relation: string;
  durationDays: string;
  severity: string;
  onsetOn: string;
  onsetPrecision: string;
  dose: string;
  frequency: string;
}

export function emptyDraft(kind: string): HistoryDraft {
  return {
    kind,
    concept: null,
    said: '',
    relation: '',
    durationDays: '',
    severity: '',
    onsetOn: '',
    onsetPrecision: 'day',
    dose: '',
    frequency: '',
  };
}

/**
 * The fields the server would refuse this draft for, named as the server names them.
 *
 * The names matter: they are the keys a 422 comes back with, so a client-side complaint and
 * a server-side one land against the same control and the operator never sees the message
 * move. The rules come from the kind rather than from a switch on its name — that is what
 * `/v1/history/kinds` returns them for, and a screen that hardcoded "family history needs a
 * relation" would ask for one on a complaint the first time somebody added a seventh kind.
 *
 * `said` is required only when there is no coding, which is the uncoded escape hatch: the
 * catalogue will not have a code for everything a history officer meets, and an item that
 * says what was meant is worth far more than a refusal.
 */
export function missingFields(kind: HistoryKind, draft: HistoryDraft): string[] {
  const missing: string[] = [];
  if (draft.concept === null && draft.said.trim() === '') missing.push('said');
  if (kind.requires_relation && draft.relation === '') missing.push('relation');
  if (kind.requires_duration && draft.durationDays.trim() === '') missing.push('duration_days');
  return missing;
}

/**
 * The draft as the contract's request body.
 *
 * The coding travels as three fields or as none of them — see the note at the top. Empty
 * strings are dropped rather than sent: on a record, an absent field and an empty one mean
 * the same thing, and sending `severity: ''` for a kind that allows no severity is a
 * validation failure this form would have caused itself.
 */
export function recordRequestFrom(
  kind: HistoryKind,
  draft: HistoryDraft,
): RecordHistoryItemRequest {
  const said = draft.said.trim();
  const duration = Number(draft.durationDays);

  return {
    kind: kind.kind,
    ...(draft.concept === null
      ? {}
      : {
          code_system: draft.concept.system,
          code_version: draft.concept.version,
          code: draft.concept.code,
        }),
    ...(said === '' ? {} : { said }),
    ...(kind.requires_relation && draft.relation !== '' ? { relation: draft.relation } : {}),
    ...(kind.requires_duration && draft.durationDays.trim() !== '' && Number.isFinite(duration)
      ? { duration_days: duration }
      : {}),
    ...(kind.allows_severity && draft.severity !== ''
      ? { severity: draft.severity as Severity }
      : {}),
    ...(kind.allows_onset && draft.onsetOn !== ''
      ? {
          onset_on: draft.onsetOn,
          // The precision travels with the date and never without it. A patient who said
          // "about two years ago" gave a real answer, and storing it as an exact day makes
          // a guess look like a measurement.
          onset_precision: draft.onsetPrecision as OnsetPrecision,
        }
      : {}),
    ...(kind.is_medication && draft.dose.trim() !== '' ? { dose: draft.dose.trim() } : {}),
    ...(kind.is_medication && draft.frequency.trim() !== ''
      ? { frequency: draft.frequency.trim() }
      : {}),
  };
}
