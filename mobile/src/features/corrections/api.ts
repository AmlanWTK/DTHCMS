import { keepPreviousData, useQuery, type UseQueryResult } from '@tanstack/react-query';

import { ApiError, NetworkError, fieldMessages } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

import type {
  Act,
  ApplyRequest,
  CorrectionReason,
  CorrectionRequest,
  Locale,
  Observation,
  RejectRequest,
  Trouble,
} from './state';

/**
 * The correction calls, from the operator's end (CP62, §4.3).
 *
 * # A thin binding, and the two things deliberately missing
 *
 * Every function below is one of the contract's endpoints, unwrapped into a value or a thrown
 * error like every other call this app makes. What is worth stating is what is **not** here.
 *
 * **There is no `flagObservation`.** Saying a value looks wrong needs
 * `observation.correct.request`, which the plan gives to the physician, QA and senior
 * operators; the station app's job in §4.3 is the other end of the conversation. A call here
 * is what a control would have to be wired to, so its absence is the reason there is no
 * "flag" button anywhere in this feature, and this sentence is what somebody would have to
 * delete first.
 *
 * **There is no way to answer somebody else's request.** `listMyCorrections` reads `/mine` and
 * nothing in this file takes an arbitrary queue. A supervisor answering on an operator's
 * behalf writes `SUPERVISOR_OVERRIDE_APPLIED` rather than `CORRECTION_APPLIED` — a different
 * event, on purpose, so that an operator's quality record does not read a supervisor's fix as
 * though they had put it right themselves — and that is a review surface, not a station one.
 *
 * # The queue is read oldest-first and never re-sorted
 *
 * `/mine` answers in the order the requests arrived and `state.ts` preserves it. A queue
 * answered newest-first is a queue where the oldest request is never answered.
 *
 * # Every write carries its own event id, and the same id is the idempotency key
 *
 * The clinic's link drops for seconds at a time (ADR-0004). A correction sent twice over a bad
 * connection must be one correction in the ledger — one actor, one moment, one replacement
 * observation — so the caller supplies the event id in the body, the same value goes on
 * `Idempotency-Key`, and a retry re-sends both unchanged. Without that, the second attempt
 * meets `CORRECTION_ALREADY_ANSWERED` and the operator is told somebody beat them to it, when
 * the somebody was themselves.
 *
 * # Nothing here logs
 *
 * A correction refusal names an observation, a patient's measurement and sometimes a
 * colleague's own words about another colleague's work. None of that belongs in a log line.
 */

/** One key for the operator's own queue. `all` is part of it: they are two different answers. */
export function correctionsQueryKey(all: boolean): readonly unknown[] {
  return ['corrections', 'mine', all ? 'all' : 'open'] as const;
}

/** Everything under this prefix is invalidated when a flag arrives on the socket. */
export const CORRECTIONS_QUERY_PREFIX = ['corrections'] as const;

export const CORRECTION_REASONS_QUERY_KEY = ['corrections', 'reasons'] as const;

export function flaggedObservationQueryKey(id: string): readonly unknown[] {
  return ['observation', id] as const;
}

/**
 * What I am being asked to fix.
 *
 * Open requests unless `all`, which adds the answered ones. The second form is not a
 * convenience: an operator who answered on another device, or whose request a supervisor took,
 * would otherwise meet a blank screen where a flag used to be — and the honest reading of that
 * blank is "the thing I was about to fix has vanished".
 */
export async function listMyCorrections(all: boolean): Promise<CorrectionRequest[]> {
  const body = await unwrap(
    api.GET('/v1/corrections/mine', { params: { query: all ? { all: '1' } : {} } }),
  );
  return body.requests;
}

/**
 * The ways a value can be wrong.
 *
 * Reference data for one clinic session: a code and its words, so the screen can say
 * "transcription error" rather than `TRANSCRIPTION`. Rows rather than an enum because the
 * taxonomy is a proposal the plan lists as needing clinical confirmation, which is exactly why
 * this phone must survive a code it has never seen — `reasonDisplay` shows it as itself.
 */
export async function listCorrectionReasons(): Promise<CorrectionReason[]> {
  const body = await unwrap(api.GET('/v1/corrections/reasons'));
  return body.reasons;
}

/**
 * The value that was questioned.
 *
 * A separate read, because the request carries `observation_id` and `code` and not the number.
 * That is the contract's decision and the right one — a queue of ten requests would otherwise
 * put ten patients' clinical values on one screen — so this is asked for the one request the
 * operator has actually opened.
 */
export async function getFlaggedObservation(id: string): Promise<Observation> {
  const body = await unwrap(api.GET('/v1/observations/{id}', { params: { path: { id } } }));
  return body.observation;
}

/**
 * Put the value right.
 *
 * Writes a **new** observation replacing the flagged one and recomputes everything derived
 * from it, all in one transaction on the server. Nothing on this side patches its own copy of
 * anything: the answer is the request as it now stands, and what moved with it is on that.
 *
 * The person the request was routed to needs no permission for this, so there is no permission
 * argument and no guard in front of the call.
 */
export async function applyCorrection(id: string, body: ApplyRequest): Promise<CorrectionRequest> {
  const answer = await unwrap(
    api.POST('/v1/corrections/{id}/apply', {
      params: { path: { id }, header: guard(body.event_id) },
      body,
    }),
  );
  return answer.request;
}

/**
 * Say the value stands.
 *
 * The reason is required by the contract, and `toReject` refuses an empty one before it can
 * become a request. Not because the server would not — it would — but because the sentence is
 * only useful to the physician if the operator writes it while they still remember why.
 */
export async function rejectCorrection(
  id: string,
  body: RejectRequest,
): Promise<CorrectionRequest> {
  const answer = await unwrap(
    api.POST('/v1/corrections/{id}/reject', {
      params: { path: { id }, header: guard(body.event_id) },
      body,
    }),
  );
  return answer.request;
}

/**
 * The forgery guard and the idempotency key, on both writes.
 *
 * One helper rather than two copies: a write that forgot either is a refusal the operator
 * meets with a patient in front of them. The key is the event id from the body, so a retry
 * over a stuttering link re-sends the same attempt and the record keeps one correction.
 */
function guard(eventId: string): { 'X-Requested-With': 'DTHCMS'; 'Idempotency-Key': string } {
  return { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': eventId };
}

// --- the same calls, cached, for the screens ---

/**
 * How long the reason vocabulary is treated as current: a whole clinic session.
 *
 * It is reference data that changes by deliberate decision rather than during a morning, and a
 * re-fetch on every screen change spends a connection that may not be there.
 */
export const REASONS_STALE_MS = 8 * 60 * 60_000;

/**
 * The operator's own queue.
 *
 * `enabled` on the session having answered, so the sign-in screen — which wears the same shell
 * — makes no request at all. A failure is not thrown into the screen: the panel says what went
 * wrong and offers the one act that can help, and a thrown error inside the application shell
 * would take every other screen down with it.
 *
 * `keepPreviousData` because `all` is part of the key, and the screen turns it on at the exact
 * moment a request is answered. Without it the list would empty itself for one round trip
 * immediately after the operator pressed the button — which is the one moment on this screen
 * where a blank would be read as "what I just did has gone wrong".
 */
export function useMyCorrections(
  signedIn: boolean,
  all: boolean,
): UseQueryResult<CorrectionRequest[]> {
  return useQuery({
    queryKey: correctionsQueryKey(all),
    queryFn: () => listMyCorrections(all),
    enabled: signedIn,
    placeholderData: keepPreviousData,
    throwOnError: false,
  });
}

/** The vocabulary, once per session, shared by every card on the screen. */
export function useCorrectionReasons(signedIn: boolean): UseQueryResult<CorrectionReason[]> {
  return useQuery({
    queryKey: CORRECTION_REASONS_QUERY_KEY,
    queryFn: listCorrectionReasons,
    enabled: signedIn,
    staleTime: REASONS_STALE_MS,
    gcTime: REASONS_STALE_MS,
    throwOnError: false,
  });
}

/**
 * The questioned value, for the one request that is open on screen.
 *
 * Keyed on the observation rather than on the request, so that a value already read by another
 * screen is not read again — and so that a correction, which replaces the row rather than
 * editing it, cannot leave a stale number behind under the same key.
 */
export function useFlaggedObservation(id: string): UseQueryResult<Observation> {
  return useQuery({
    queryKey: flaggedObservationQueryKey(id),
    queryFn: () => getFlaggedObservation(id),
    enabled: id !== '',
    throwOnError: false,
  });
}

// --- what went wrong, in the three ways it can ---

/**
 * The fields the server can name on a refusal, in the order an operator can act on them.
 *
 * `value` first: it is the one that changes what the person does next. A refusal naming two
 * fields should be reported by that one rather than by whichever key serialised first.
 */
const FIELD_ORDER = ['value', 'unit', 'value_text', 'value_bool', 'reason', 'note'];

function refusalOf(named: Record<string, string>): { field: string; message: string } {
  for (const field of FIELD_ORDER) {
    const message = named[field];
    if (message !== undefined) return { field, message };
  }
  // A field this build has never heard of is still shown, with its name, so a support call can
  // quote it — sorted, so two operators do not read different sentences.
  for (const [field, message] of Object.entries(named).sort((a, b) => a[0].localeCompare(b[0]))) {
    return { field, message };
  }
  return { field: '', message: '' };
}

/**
 * What went wrong, in the three shapes this screen knows how to say.
 *
 * A refusal is shown in the server's own words, because the rules behind it are the database's
 * and a client that paraphrased them would be inventing a second, staler account of a clinical
 * rule it does not own. What this app adds is the status, the act it was refusing, and the
 * server's **code** — carried through untouched, because it is the only part of an error a
 * client may branch on. `adviceFor` reads the status to say whether the answer is to read
 * again, press again, or stop pressing; `alreadyAnswered` reads the code to tell "somebody got
 * there first" from every other conflict, and those two need different sentences.
 */
export function troubleOf(error: unknown, locale: Locale, act: Act): Trouble {
  if (error instanceof NetworkError) {
    return { kind: 'unreachable', act, status: 0, code: '', field: '', message: '' };
  }

  if (error instanceof ApiError) {
    if (error.status === 422) {
      const refusal = refusalOf(fieldMessages(error, locale));
      return {
        kind: 'refused',
        act,
        status: error.status,
        code: error.code,
        field: refusal.field,
        message: refusal.message === '' ? messageOf(error, locale) : refusal.message,
      };
    }
    // 403, 404 and 409 are refusals too, and the status is what tells them apart: somebody
    // else's request, a request that is gone, and one that has already been answered.
    return {
      kind:
        error.status === 403 || error.status === 404 || error.status === 409 ? 'refused' : 'failed',
      act,
      status: error.status,
      code: error.code,
      field: '',
      message: messageOf(error, locale),
    };
  }

  // Something that is not an error this app throws. There is no code to carry and the screen
  // supplies the sentence.
  return { kind: 'failed', act, status: 0, code: '', field: '', message: '' };
}

function messageOf(error: InstanceType<typeof ApiError>, locale: Locale): string {
  const bengali = error.messageBN.trim();
  if (locale === 'bn' && bengali !== '') return bengali;
  return error.messageEN.trim();
}
