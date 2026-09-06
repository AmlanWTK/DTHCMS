import { ApiError, NetworkError, fieldMessages } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

import {
  MAX_RESULTS,
  type Attempt,
  type DietEntryBody,
  type Food,
  type FoodAnswer,
  type FoodRequest,
  type Locale,
  type RecallAnswer,
  type RecallDay,
  type Reference,
  type Trouble,
  type WithdrawalBody,
} from './state';

/**
 * Station 7's six calls, from the floor (CP59).
 *
 * # A thin binding, and one seam that is not thin
 *
 * Four of these are what they look like: the contract's endpoints, unwrapped into values or
 * thrown errors like every other call this app makes. `runSearch` is the one that earns its
 * keep, and it **returns failures rather than throwing them**, carrying the sequence number of
 * the request that failed. That is not squeamishness about exceptions. A failure with no
 * sequence number cannot be aged, and an old request timing out would then be free to wipe the
 * list a newer one had already landed.
 *
 * # What is deliberately not here
 *
 * **There is no call that asks what a measure weighs.** `GET /v1/foods` returns each food with
 * its portions attached, in one query, precisely so that a picker does not have to ask a second
 * question after the operator has chosen — the whole recall has four minutes and a round trip
 * per food would spend a noticeable share of it.
 *
 * **There is no call that computes a total.** The day's energy and macros come back on
 * `recall.totals`, computed by the server from the composition table, and nothing here assembles
 * one. This file must never grow a function that does: §12.1's diet–outcome analysis reads the
 * stored `ENERGY_INTAKE`, and a screen with its own copy of the arithmetic is a second answer to
 * the question the research asks.
 *
 * **There is no call that starts a recall.** There is no recall object to start. A 24-hour
 * recall is a set of entries against a patient and a date, which is what lets two assistants
 * work on one from two tablets with nothing to lock, merge or reconcile.
 *
 * # Every write carries its own event id, and the same id is the idempotency key
 *
 * An entry sent twice over a bad connection must be one row in the ledger — one actor, one
 * moment — so the caller supplies the event id in the body, the same value goes on
 * `Idempotency-Key`, and a retry re-sends both unchanged. Recording the same food *again*, on
 * purpose, is a new act with a new id: the patient who says "and another ruti" has eaten a
 * second ruti.
 */

/**
 * The picker.
 *
 * An empty query is a real query and is sent as no `q` parameter at all: the endpoint answers it
 * with the table from the top, so the picker has food in it before anybody types. `limit` is
 * asked for explicitly at the same 25 the display trims to, so the tablet is not handed rows it
 * has already decided not to draw.
 *
 * **The `group` filter the contract offers is deliberately not sent, and there is no parameter
 * here for it.** The picker has one box and it searches everything; a group filter would be a
 * control an operator has to set correctly before they can find a food, and a food filed under
 * SNACK that the operator was looking for under GRAIN would simply not be there. If browsing by
 * group ever earns its place it should arrive as a screen somebody designed, not as an argument
 * a caller can pass by mistake.
 */
export async function searchFoods(query: string): Promise<Food[]> {
  const typed = query.trim();
  const q: { q?: string; limit: number } = { limit: MAX_RESULTS };
  if (typed !== '') q.q = typed;
  const body = await unwrap(api.GET('/v1/foods', { params: { query: q } }));
  return body.foods;
}

/**
 * The station's whole vocabulary, and the clinic's calendar, in one call.
 *
 * Reference data with no patient in it, which is why a station tablet fetches it on arrival and
 * then works from it for the rest of the morning (ADR-0004). Six things arrive, and four of them
 * are here because the first version of this screen had to invent them:
 *
 *  - the household measures, and **the meals with their names** — bilingual, so no client writes
 *    its own Bangla for MID_MORNING and no two clients disagree about what a patient was asked;
 *  - `recall_date_default` and `clinic_today`, **on the clinic's own calendar**. Nothing on this
 *    side computes either. The tablet's date is whatever somebody set it to, and the server's own
 *    default was a UTC yesterday until this checkpoint — which between midnight and six in the
 *    morning in Faridpur is a different day.
 *
 * The quantity ceilings are not here: they are on each measure's own row, which is where the
 * server reads them from.
 *
 * **This payload's `clinic_today` is only good for the first render.** A tablet holds it for a
 * whole clinic session, so a session crossing midnight would be checking against yesterday's idea
 * of today. Every recall answer below carries a fresh one.
 */
export async function listReference(): Promise<Reference> {
  const body = await unwrap(api.GET('/v1/foods/measures'));
  return {
    measures: body.measures,
    meals: body.meals,
    recallDateDefault: body.recall_date_default,
    clinicToday: body.clinic_today,
  };
}

/**
 * One day's eating, withdrawn entries included.
 *
 * Called on arrival, on a refresh, and whenever the operator moves the day. `date` is omitted
 * the first time, so the **server** decides which day is being recalled — it defaults to
 * yesterday, and a client that computed that default would be a client asserting the clinic's
 * calendar. Every write afterwards sends the date this answer carried.
 */
export async function getRecall(patientId: string, date?: string): Promise<RecallAnswer> {
  const day = (date ?? '').trim();
  const body = await unwrap(
    api.GET('/v1/patients/{id}/diet', {
      params: { path: { id: patientId }, query: day === '' ? {} : { date: day } },
    }),
  );
  return { recall: body.recall, clinicToday: body.clinic_today };
}

/**
 * Which days this patient has a recall for, newest first, with how much is on each.
 *
 * The count is why this is worth a call at all: "3 September · 12 items" is a day worth opening
 * and "3 September · 1 item" is a recall somebody abandoned, and a list of bare dates could not
 * tell them apart without a request each.
 *
 * It is how an operator reaches a day that is not the default one — the two step controls move by
 * one, and this jumps. Read again after every write, because the day the operator is on may not
 * have been on the list a moment ago.
 */
export async function listRecallDays(patientId: string): Promise<RecallDay[]> {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/diet/days', { params: { path: { id: patientId } } }),
  );
  return body.days;
}

/**
 * One thing the patient said they ate, and the whole day back.
 *
 * The response is the entire recall, not the entry — including everything the other assistant
 * added while this one was typing. That is not a convenience: it is the duplicate-prevention
 * mechanism, and it is why this returns the recall rather than the row it just wrote.
 */
export async function recordDietEntry(body: DietEntryBody): Promise<RecallAnswer> {
  const written = await unwrap(
    api.POST('/v1/diet', {
      params: { header: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': body.event_id } },
      body,
    }),
  );
  return { recall: written.recall, clinicToday: written.clinic_today };
}

/**
 * Take one entry back, with a reason and a name, and get the whole day back.
 *
 * **Any operator may withdraw any entry**, including one somebody else recorded. That is the
 * contract's rule, not a permission this client checks: the commonest withdrawal here is the
 * duplicate two assistants produced, and requiring the original recorder would leave it standing
 * until they return from the next patient.
 */
export async function withdrawDietEntry(
  entryId: string,
  body: WithdrawalBody,
): Promise<RecallAnswer> {
  const written = await unwrap(
    api.POST('/v1/diet/{id}/withdraw', {
      params: {
        path: { id: entryId },
        header: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': body.event_id },
      },
      body,
    }),
  );
  return { recall: written.recall, clinicToday: written.clinic_today };
}

/**
 * One search, answered — successfully or not, but always with its sequence number.
 *
 * This never throws. Everything a caller has to decide about a failure is already decided in
 * `troubleOf`, and everything it has to decide about ordering is decided by the number this
 * carries back through `applyAnswer`'s single door.
 */
export async function runSearch(request: FoodRequest, locale: Locale): Promise<FoodAnswer> {
  try {
    return { seq: request.seq, ok: true, foods: await searchFoods(request.q) };
  } catch (error) {
    return { seq: request.seq, ok: false, trouble: troubleOf(error, locale, 'foods') };
  }
}

/**
 * The fields the server can name on a 422, in the order an operator can act on them.
 *
 * `measure_code` first, and that ordering is load-bearing: it is the one refusal this screen
 * changes its behaviour for, and a future response naming both the measure and the quantity must
 * be reported by the half that has a way forward — record it in grams — rather than by whichever
 * key happened to serialise first.
 */
const FIELD_ORDER = ['measure_code', 'food_code', 'quantity', 'meal', 'recall_date', 'reason'];

/**
 * What went wrong, in the three shapes it can arrive in.
 *
 * The same translation stations 3, 4 and 5 use. The server's `code` and the field it named are
 * both kept, because they are the only parts of a refusal a client may branch on and this
 * station branches on both: `measure_code` means the table cannot weigh that measure, and
 * `DIET_ENTRY_ALREADY_WITHDRAWN` means the other operator got there first. Neither is a mistake
 * the person at the tablet made.
 */
export function troubleOf(error: unknown, locale: Locale, attempt: Attempt): Trouble {
  if (error instanceof NetworkError) {
    return { kind: 'unreachable', attempt, status: 0, code: '', field: '', message: '' };
  }

  if (error instanceof ApiError) {
    if (error.status === 422) {
      const refusal = refusalOf(fieldMessages(error, locale));
      return {
        kind: 'refused',
        attempt,
        status: error.status,
        code: error.code,
        field: refusal.field,
        // A 422 that named no field at all is still a refusal, and the server still wrote a
        // sentence for it. Showing nothing would be the one outcome worse than either.
        message: refusal.message === '' ? messageOf(error, locale) : refusal.message,
      };
    }
    return {
      kind:
        error.status === 403 || error.status === 404 || error.status === 409 ? 'refused' : 'failed',
      attempt,
      status: error.status,
      code: error.code,
      field: '',
      message: messageOf(error, locale),
    };
  }

  // Something that is not an error this app throws. There is no code to carry and the screen
  // supplies the sentence.
  return { kind: 'failed', attempt, status: 0, code: '', field: '', message: '' };
}

/**
 * Which field a refusal is about, and what the server said about it.
 *
 * Known fields in the order above; anything else by name, sorted, so two operators meeting the
 * same refusal read the same sentence rather than whichever key serialised first.
 */
function refusalOf(named: Record<string, string>): { field: string; message: string } {
  for (const field of FIELD_ORDER) {
    const message = named[field];
    if (message !== undefined) return { field, message };
  }
  for (const [field, message] of Object.entries(named).sort((a, b) => a[0].localeCompare(b[0]))) {
    return { field, message };
  }
  return { field: '', message: '' };
}

function messageOf(error: InstanceType<typeof ApiError>, locale: Locale): string {
  const bengali = error.messageBN.trim();
  if (locale === 'bn' && bengali !== '') return bengali;
  return error.messageEN.trim();
}
