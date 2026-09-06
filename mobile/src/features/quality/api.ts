import { useQuery, type UseQueryResult } from '@tanstack/react-query';

import { ApiError, NetworkError, queryKeys } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

import { WINDOW_DAYS, windowDaysFor } from './state';
import type { Locale, QualityRecord, QualityRule, Trouble } from './state';

/**
 * The quality calls, from the operator's own end (CP63, §4.3, ADR-0029).
 *
 * # Two reads, and no permission argument anywhere
 *
 * `/v1/quality/me` needs a session and nothing else. That is a decision the ADR argues at
 * length and it is the reason this file has no permission check, no `can(...)` guard and no
 * hat to put on before calling: an operator who has to be granted something before they may
 * see their own record is an operator who will assume the record is being kept from them. A
 * client-side gate would be a locked control in front of the one person entitled to press it,
 * and `quality.test.ts` asserts the absence rather than trusting this paragraph.
 *
 * # The four calls that are deliberately missing
 *
 * `/v1/quality/operators`, `/v1/quality/operators/{id}`, `/v1/quality/flags` and
 * `/v1/quality/flags/{id}/resolve` are not bound here. Three of them need `quality.read.team`
 * and the fourth needs `quality.flag.resolve`; a station operator holds neither, so a control
 * wired to any of them would be a button that answers 403 with a patient waiting.
 *
 * But the reason is not only the permission. A supervisor reading somebody's month is at a
 * desk with the correction chain open and the instrument log to hand — that is what makes the
 * numbers mean anything, and it is why ADR-0029 keeps this out of HR's hands. A phone in a
 * queue is not that. **The supervisor's view is web-only, and a call in this file is what a
 * screen would need before it could be otherwise.** This is the sentence somebody would have
 * to delete first.
 *
 * # Nothing here writes, and nothing here logs
 *
 * There is no mutation in this feature at all, so no event id, no idempotency key and no
 * `X-Requested-With` guard: those exist on the writes, and this surface has none. And a
 * refusal on this endpoint names a member of staff and a count about their work, which does
 * not belong in a log line any more than a patient's measurement does.
 */

/**
 * Everything under this prefix is re-read when a note arrives on the socket.
 *
 * It is `queryKeys.quality()` from the shared package rather than a literal of this feature's
 * own, and that is now load-bearing: `realtimeInvalidations` maps every `quality.*` message —
 * and a reconnect gap on a `user:` topic — onto that exact key. Two spellings of the same
 * prefix would produce the failure the shared map exists to prevent, and it would look like
 * "the tablet updates and the dashboard does not".
 */
export const QUALITY_QUERY_PREFIX = queryKeys.quality();

/**
 * One key for the caller's own record. The window is part of it: thirty days and fourteen days
 * are two different answers, and a cache that confused them would show a figure from one window
 * under the heading of the other.
 */
export function myQualityQueryKey(days: number): readonly unknown[] {
  return [...QUALITY_QUERY_PREFIX, 'me', windowDaysFor(days)] as const;
}

export const QUALITY_THRESHOLDS_QUERY_KEY = [...QUALITY_QUERY_PREFIX, 'thresholds'] as const;

/**
 * My own record.
 *
 * The caller's id comes off the session on the server, so there is no argument here that could
 * name anybody else and no way for this function to grow one without changing its signature.
 * The response carries `mine`, which is always true on this path; it is dropped, because a
 * screen that branched on it would be a screen half-written for a reader it cannot have.
 *
 * The window goes through `windowDaysFor` on the way out. `?days` outside 1–365 is refused with
 * a 422 now rather than silently becoming thirty — which is the right server behaviour and
 * makes it this client's job never to ask. One funnel rather than a check at each call site:
 * the key and the query string are built from the same guarded number, so a cache entry and
 * the request that filled it cannot disagree about which window they are.
 */
export async function getMyQualityRecord(days: number = WINDOW_DAYS): Promise<QualityRecord> {
  const body = await unwrap(
    api.GET('/v1/quality/me', { params: { query: { days: windowDaysFor(days) } } }),
  );
  return body.record;
}

/**
 * What puts a note on a record.
 *
 * Readable with a session for the same reason the record is, and read here for a reason worth
 * stating: an operator can see the rule **before** it is ever applied to them. A threshold
 * somebody only meets on the day it is crossed is an ambush however gently it is worded, and
 * the plan's stated risk is precisely that a mechanism which feels like one makes people hide
 * their work instead of correcting it.
 *
 * Every threshold ships unapproved, and the flag saying so travels with each row rather than
 * being inferred from a null timestamp — that is the contract's decision, taken so that a
 * client cannot forget to check for the null.
 *
 * The whole row is returned, threshold **and** sentence. `looks_for_en` / `looks_for_bn` is
 * rendered from the live row by the server so that a threshold whose numbers change cannot
 * leave a description of the old ones behind; unwrapping it away here and rebuilding the
 * sentence in the message files — which is what this did while the field was English only —
 * would put that staleness straight back, in two languages instead of one.
 */
export async function listQualityThresholds(): Promise<QualityRule[]> {
  const body = await unwrap(api.GET('/v1/quality/thresholds'));
  return body.thresholds;
}

// --- the same calls, cached, for the screens ---

/**
 * How long the threshold vocabulary is treated as current: a whole clinic session.
 *
 * Reference data that changes by a clinician's deliberate decision rather than during a
 * morning, and a re-fetch on every screen change spends a connection that may not be there.
 * The same figure `features/corrections` uses for the reason taxonomy, and for the same reason.
 */
export const THRESHOLDS_STALE_MS = 8 * 60 * 60_000;

/**
 * The caller's own record.
 *
 * `enabled` on the session having answered, so the sign-in screen — which wears the same shell
 * — makes no request at all. A failure is not thrown into the screen: the panel says what went
 * wrong and what can be done about it, and a thrown error inside the application shell would
 * take every other screen down with it, which for a station tablet mid-clinic means the queue.
 *
 * The shell's own line and this screen share one key, so an operator who opens their record
 * after seeing that line spends no second request on a connection that may not be there.
 */
export function useMyQualityRecord(
  signedIn: boolean,
  days: number = WINDOW_DAYS,
): UseQueryResult<QualityRecord> {
  return useQuery({
    queryKey: myQualityQueryKey(days),
    queryFn: () => getMyQualityRecord(days),
    enabled: signedIn,
    throwOnError: false,
  });
}

/** The rules, once per session, shared by every row that names one. */
export function useQualityThresholds(signedIn: boolean): UseQueryResult<QualityRule[]> {
  return useQuery({
    queryKey: QUALITY_THRESHOLDS_QUERY_KEY,
    queryFn: listQualityThresholds,
    enabled: signedIn,
    staleTime: THRESHOLDS_STALE_MS,
    gcTime: THRESHOLDS_STALE_MS,
    throwOnError: false,
  });
}

// --- what went wrong, in the three ways it can ---

/**
 * A failed read, in the three shapes the screen knows how to say.
 *
 * The server's own sentence is carried through untouched where there is one, for the reason
 * CP62 records: the rules behind a refusal are the server's, and a client that paraphrased
 * them would be keeping a second, staler account of a rule it does not own.
 *
 * A 403 is reported as a plain refusal rather than as "you are wearing the wrong hat". This
 * endpoint takes no permission at all, so there is no hat that would help — and telling an
 * operator to switch roles to see their own record would be exactly the sentence ADR-0029
 * exists to make impossible.
 */
export function troubleOf(error: unknown, locale: Locale): Trouble {
  if (error instanceof NetworkError) {
    return { kind: 'unreachable', status: 0, message: '' };
  }
  if (error instanceof ApiError) {
    // 422 sits with the refusals rather than with the failures. It can only mean this build
    // asked for a window the contract refuses — which `windowDaysFor` exists to make
    // impossible — so it is something to report to whoever looks after the system, not
    // something to ask an operator at a station to do differently.
    const refused =
      error.status === 401 || error.status === 403 || error.status === 404 || error.status === 422;
    return {
      kind: refused ? 'refused' : 'failed',
      status: error.status,
      message: messageOf(error, locale),
    };
  }
  // Something this application does not throw. There is no sentence to carry and the screen
  // supplies its own.
  return { kind: 'failed', status: 0, message: '' };
}

function messageOf(error: InstanceType<typeof ApiError>, locale: Locale): string {
  const bengali = error.messageBN.trim();
  if (locale === 'bn' && bengali !== '') return bengali;
  return error.messageEN.trim();
}
