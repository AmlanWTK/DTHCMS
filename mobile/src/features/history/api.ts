import { ApiError, NetworkError, fieldMessages } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

import type {
  AmendHistoryItemRequest,
  FamilyRelation,
  HistoryItem,
  HistoryKind,
  Locale,
  RecordHistoryItemRequest,
  Removal,
} from './form';

/**
 * The eight history calls, from station 4 (CP53).
 *
 * # A thin binding, and one call that is deliberately not a convenience
 *
 * Most of what follows is what it looks like: the contract's endpoints, unwrapped into values
 * or thrown errors like every other call this app makes.
 *
 * `confirmItem` is the exception, and the exception is that it stays inconvenient. **It takes
 * one item id.** There is no overload that takes a list, no `confirmAll`, and nothing in this
 * feature loops over one — because the endpoint it calls is acceptance criterion 3, and every
 * design that satisfies the words while making the assertion on somebody's behalf is the
 * thing the criterion forbids. A "confirm all" button turns one action by a person into
 * twenty claims in a clinical record, each carrying that person's name and none of them made
 * by them. Twenty carried-forward items is twenty presses.
 *
 * A client-side helper would be the easiest place in the whole system to reintroduce that, so
 * it is not here, and the test file asserts by name that it is not.
 *
 * # Every write carries its own event id
 *
 * The clinic's link drops for seconds at a time (ADR-0004). A confirmation sent twice over a
 * bad connection must be one confirmation in the ledger, not two, so the caller supplies the
 * event id in the body and a retry re-sends the same one. It is the body rather than a header
 * on these four endpoints because that is where the contract puts a history item's key.
 */

export interface HistoryKinds {
  kinds: HistoryKind[];
  relations: FamilyRelation[];
  /** Observation codes station 4 displays and never re-enters. */
  from_lifestyle_station: string[];
}

/** The six kinds, the relations, and what belongs to the lifestyle station. Fetched once. */
export async function listKinds(): Promise<HistoryKinds> {
  return unwrap(api.GET('/v1/history/kinds'));
}

/**
 * How much of this clinic's record could not be coded, by kind.
 *
 * The number that keeps the escape hatch honest. If it grows, the catalogue is wrong rather
 * than the officers, and it is the list of concepts somebody should add.
 */
export async function countUncoded(): Promise<Record<string, number>> {
  const body = await unwrap(api.GET('/v1/history/uncoded'));
  return body.uncoded;
}

/**
 * Everything currently believed about this patient, in station order.
 *
 * Removed items are absent and resolved ones are present, both by the server's decision. The
 * list arrives carrying `confirmed_at` — or not carrying it, which is the state station 4
 * works through one item at a time.
 */
export async function listMedicalHistory(patientId: string): Promise<HistoryItem[]> {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/medical-history', { params: { path: { id: patientId } } }),
  );
  return body.items;
}

/** One item, one request, one event. Never a whole history in one call. */
export async function recordItem(
  patientId: string,
  body: RecordHistoryItemRequest,
): Promise<HistoryItem> {
  const written = await unwrap(
    api.POST('/v1/patients/{id}/medical-history', {
      params: { path: { id: patientId }, header: guard(body.event_id) },
      body,
    }),
  );
  return written.item;
}

/** One item, removed ones included — which is how a removal can be looked at afterwards. */
export async function getItem(itemId: string): Promise<HistoryItem> {
  const body = await unwrap(
    api.GET('/v1/history/items/{itemId}', { params: { path: { itemId } } }),
  );
  return body.item;
}

/**
 * Change what is known about an item.
 *
 * Not what it **is**: the kind and the coding are absent from the request on purpose, because
 * changing those means removing one item and recording another. An amendment confirms as it
 * changes, which is the server's rule and the right one — somebody has just made a fresh
 * assertion about this item.
 */
export async function amendItem(
  itemId: string,
  body: AmendHistoryItemRequest,
): Promise<HistoryItem> {
  const amended = await unwrap(
    api.PATCH('/v1/history/items/{itemId}', {
      params: { path: { itemId }, header: guard(body.event_id) },
      body,
    }),
  );
  return amended.item;
}

/**
 * Say that **one** carried-forward item is still true.
 *
 * One id, by design and not by omission. See this module's own note: there is no batch
 * endpoint, and the reason there is none is that a confirmation is an assertion with an actor
 * behind it. A helper here that took a list would put the auto-acceptance back, wearing the
 * name of whoever pressed once.
 */
export async function confirmItem(
  itemId: string,
  ids: { event: string; visit?: string },
): Promise<HistoryItem> {
  const body: { event_id: string; visit_id?: string } = { event_id: ids.event };
  if (ids.visit !== undefined && ids.visit !== '') body.visit_id = ids.visit;
  const confirmed = await unwrap(
    api.POST('/v1/history/items/{itemId}/confirm', {
      params: { path: { itemId }, header: guard(body.event_id) },
      body,
    }),
  );
  return confirmed.item;
}

/**
 * "This was never true."
 *
 * Not a deletion and not a resolution — the row stays and the ledger keeps both events. The
 * reason is required by the contract and by `toRemoval`, which will not build a body without
 * one, so an empty reason cannot reach here.
 */
export async function removeItem(itemId: string, body: Removal): Promise<void> {
  await unwrap(
    api.POST('/v1/history/items/{itemId}/remove', {
      params: { path: { itemId }, header: guard(body.event_id) },
      body,
    }),
  );
}

/**
 * The forgery guard and the idempotency key, on every write.
 *
 * One helper rather than four copies: a write that forgot either is a refusal the officer
 * meets mid-sentence. The key is the item's own `event_id` — the same value the body carries,
 * which is where the contract puts a history item's key — so a retry over a stuttering link
 * re-sends one attempt and the ledger keeps one event rather than two.
 *
 * The fallback is only reachable for a caller that supplied no event id at all, which
 * `form.ts` never does: every body it builds carries one. A fresh key is still better than an
 * absent one — the request is answered rather than refused, and a body with no event id had
 * nothing to be deduplicated by in the first place.
 */
function guard(eventId?: string): {
  'X-Requested-With': 'DTHCMS';
  'Idempotency-Key': string;
} {
  return {
    'X-Requested-With': 'DTHCMS',
    'Idempotency-Key': eventId ?? crypto.randomUUID(),
  };
}

// --- what went wrong, in the three ways it can ---

/**
 * A refusal, an unreachable server, and a server that answered with something else.
 *
 * The same three shapes the terminology picker uses, and deliberately a separate copy: the
 * fields a history refusal can name are this station's — a missing relation, a duration that
 * is not one, a removal with no reason — and a shared helper would need the field list passed
 * in, which is one more argument for a caller to get wrong. Worth folding together the day a
 * third station needs it, and not before.
 */
export type Trouble =
  | { kind: 'refused'; field: string; message: string }
  | { kind: 'unreachable' }
  | { kind: 'failed'; message: string };

/**
 * The fields the server can name on a refusal, in the order an officer can act on them.
 *
 * The thing they are being asked about first — what it is, then what was said about it, then
 * the per-kind rules in the order the conversation goes. A refusal naming two fields is
 * reported by the one the person at the tablet can do something about, rather than by
 * whichever key happened to serialise first.
 */
const FIELD_ORDER = [
  'kind',
  'code',
  'code_system',
  'code_version',
  'said',
  'relation',
  'duration_days',
  'severity',
  'onset_on',
  'onset_precision',
  'dose',
  'frequency',
  'status',
  'reason',
];

function refusalOf(named: Record<string, string>): { field: string; message: string } {
  for (const field of FIELD_ORDER) {
    const message = named[field];
    if (message !== undefined) return { field, message };
  }
  // A field this build has never heard of is still shown, with its name, so that a support
  // call can quote it — sorted, so two operators do not read different sentences.
  for (const [field, message] of Object.entries(named).sort((a, b) => a[0].localeCompare(b[0]))) {
    return { field, message };
  }
  return { field: '', message: '' };
}

/**
 * What went wrong, in the three shapes the station knows how to say.
 *
 * A refusal is shown in the server's own words. The per-kind rules are enforced in the
 * database as well as in Go, and a client that paraphrased them would be inventing a second,
 * staler account of a clinical rule it does not own.
 *
 * Everything else is the server being absent, which at a station on this clinic's link is not
 * an error to argue with. What it costs here is worse than at the picker, though, and the
 * screen says so: an item the officer cannot record is a piece of the history that only
 * exists in the room.
 */
export function troubleOf(error: unknown, locale: Locale): Trouble {
  if (error instanceof NetworkError) return { kind: 'unreachable' };

  if (error instanceof ApiError) {
    if (error.status === 422) {
      const refusal = refusalOf(fieldMessages(error, locale));
      return {
        kind: 'refused',
        field: refusal.field,
        message: refusal.message === '' ? messageOf(error, locale) : refusal.message,
      };
    }
    return { kind: 'failed', message: messageOf(error, locale) };
  }

  // Something that is not an error this app throws. The screen supplies the sentence.
  return { kind: 'failed', message: '' };
}

function messageOf(error: InstanceType<typeof ApiError>, locale: Locale): string {
  const bengali = error.messageBN.trim();
  if (locale === 'bn' && bengali !== '') return bengali;
  return error.messageEN.trim();
}
