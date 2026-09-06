import { ApiError, writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * The operator quality record, typed against the contract (CP63, §4.3, ADR-0029).
 *
 * # What this is for, and the one thing that must not go wrong
 *
 * §4.3 states the purpose of the correction workflow: *"recurring patterns per operator
 * surface so targeted retraining happens and the same mistake does not repeat."* CP62 built
 * the routing to the person who typed the value; this is what makes those rows worth
 * keeping.
 *
 * The plan states the risk in its own words, and it reads as a warning about a feature
 * exactly like this one: *"a metric that feels punitive damages data honesty — staff hide
 * errors instead of correcting them."* That sentence is not a caveat to note in a README.
 * It is the design constraint, and four rules below exist only because of it. Each one is
 * arranged so that breaking it would take a deliberate act rather than an oversight.
 *
 * # 1. A count never travels without its denominator
 *
 * `QualityRecord` carries `entries` beside `corrections`, and `QualityOperator` carries it on
 * every line of the supervisor's list. Nothing in this module returns one without the other,
 * there is no helper here that extracts a bare numerator, and the only component that draws
 * either of them takes both as required props. Three corrections against four hundred
 * entries and against forty are different facts, and only one of them is a question.
 *
 * # 2. A rate that cannot be computed is absent, not zero
 *
 * The server answers `rate: null` below its own floor of entries in the window. This module
 * passes the null through untouched — there is deliberately no `rateOr(0)` here and there
 * must never be one. Zero is a claim; null is the absence of one, and rendering the absence
 * as a number invites somebody to act on noise. `rateIsAvailable` is the only predicate, and
 * it exists so a screen has to decide what to say rather than defaulting.
 *
 * # 3. `rejected` is a separate fact and is never folded into a total
 *
 * A request the operator answered by saying the value stands, and was not overruled on, is
 * an operator doing the job. There is no helper here that adds `upheld` and `rejected`
 * together, and `quality.test.tsx` has a named test that walks this module's exports and
 * fails if one appears.
 *
 * `upheld` and `overridden` are separate for the same family of reasons and a sharper one:
 * CP62 made a supervisor's fix a different event *precisely* so that an operator's record
 * would not read it as though they had put it right themselves. A "put right" figure that
 * quietly included the values somebody else corrected would undo that at the last step.
 *
 * # 4. Nothing here grades a person
 *
 * No identifier in this file says error, mistake, score, accuracy, worst or offender. A
 * correction is a fact about a number; a flag is a suggestion that somebody have a
 * conversation. The vocabulary is where a feature like this goes wrong first, before any of
 * its arithmetic does, and the corrections feature's header says the same thing about the
 * screens on the other side of this one.
 *
 * # The thresholds are proposals, and this module says so
 *
 * Every threshold ships with `approved: false`, and every flag carries `threshold_approved`.
 * Neither is defaulted to true anywhere here. A flag raised on numbers no clinician has
 * signed off is a demonstration of the mechanism; drawn as though it were policy it would be
 * a false statement about a colleague, made by software, on the record.
 */

export type QualityRecord = components['schemas']['QualityRecord'];
export type QualityFlag = components['schemas']['QualityFlag'];
export type QualityOperator = components['schemas']['QualityOperator'];
export type QualityThreshold = components['schemas']['QualityThreshold'];
export type QualityWindow = components['schemas']['QualityWindow'];
export type QualityEvidence = components['schemas']['QualityEvidence'];
export type QualityReasonCount = components['schemas']['QualityReasonCount'];
export type QualityCodeCount = components['schemas']['QualityCodeCount'];
export type QualityHourCount = components['schemas']['QualityHourCount'];

/** The three states a flag can be in. Answered is two different answers, never one. */
export type QualityFlagStatus = QualityFlag['status'];

/**
 * One threshold with the sentence the server renders about what it looks for.
 *
 * A pair, in both languages, with Bengali numerals in the Bangla — and rendered from the row
 * rather than stored, so numbers that change cannot leave a sentence describing the old ones.
 * This is the screen an operator reads to understand the rule that measures them, so nothing
 * here is rebuilt in the message files: two sources for that sentence would mean the screen and
 * the database eventually describe different rules.
 */
export interface ThresholdWithDescription {
  threshold: QualityThreshold;
  looks_for_en: string;
  looks_for_bn: string;
}

/**
 * The cache keys, held beside the calls.
 *
 * Everything under one `quality` prefix, which is also the key the realtime gateway's
 * `quality.flag_raised` invalidates (`queryKeys.quality()` in `@dthcms/api-client`). A flag
 * raised on a correction moves four reads at once — the operator's own record, the
 * supervisor's list, the open flags, and that operator's detail if somebody has it open —
 * and the message carries neither an operator nor a window, so a finer key would have to be
 * guessed from a payload that deliberately does not carry the answer.
 *
 * The window is part of every record key. "The last thirty days" and "the last seven" are
 * different answers to different questions, and a cache that treated them as one would show
 * a supervisor last month's numbers under this week's heading.
 */
export const QUALITY_KEY = ['quality'] as const;

export const QUALITY_THRESHOLDS_KEY = ['quality', 'thresholds'] as const;

export function myRecordKey(days: number) {
  return ['quality', 'record', 'mine', days] as const;
}

export function operatorRecordKey(operatorId: string, days: number) {
  return ['quality', 'record', operatorId, days] as const;
}

export function operatorsKey(days: number, onlyCorrected: boolean) {
  return ['quality', 'operators', days, onlyCorrected ? 'corrected' : 'everybody'] as const;
}

export function flagsKey(days: number, includeAnswered: boolean) {
  return ['quality', 'flags', days, includeAnswered ? 'all' : 'open'] as const;
}

/**
 * The windows a screen offers, and the one it opens on.
 *
 * Thirty days by default because that is the window §4.3's own verification uses — "three
 * transcription corrections within 30 days" — and because a month is the unit people
 * actually think in. Seven is there for "is this still happening this week", which is the
 * question a supervisor asks after a conversation; a year is there because a review asks
 * about somebody who left in March.
 *
 * The server clamps anything above 365 and defaults to 30, so these are offers rather than
 * rules; a screen that invented its own would simply disagree with the endpoint quietly.
 */
export const WINDOW_CHOICES = [7, 30, 90, 365] as const;
export const DEFAULT_WINDOW_DAYS = 30;

/**
 * The range the server accepts, mirrored so a control cannot offer something outside it.
 *
 * A window outside it is now **refused with 422** rather than quietly replaced with thirty, and
 * that is the right call: a client asking for fourteen days and being handed thirty has two
 * windows pretending to be one, and every count on the screen is then a numerator over somebody
 * else's denominator. The mirror here is not a second opinion about the rule — the server still
 * decides — it is so that a screen never builds a request it knows will be refused.
 */
export const MIN_WINDOW_DAYS = 1;
export const MAX_WINDOW_DAYS = 365;

export function windowIsAcceptable(days: number): boolean {
  return Number.isInteger(days) && days >= MIN_WINDOW_DAYS && days <= MAX_WINDOW_DAYS;
}

/**
 * My own record. A session and nothing else — see ADR-0029 §3.
 *
 * There is no permission on this call and no place to put one. The server reads the caller's
 * own id from the session, so there is no version of it that returns somebody else's work.
 * The reason is in the ADR and it is worth repeating here, because the tempting change is to
 * gate the screen: an operator who has to be granted something before they may see their own
 * correction count is an operator who will assume the count is being kept from them, and a
 * count somebody believes is being kept from them is the whole failure this checkpoint is
 * arranged to avoid.
 */
export async function getMyRecord(days: number): Promise<QualityRecord> {
  const body = await unwrap(api.GET('/v1/quality/me', { params: { query: { days } } }));
  return body.record;
}

/**
 * Somebody else's record, in the same shape. Needs `quality.read.team`.
 *
 * The same shape on purpose, and one component draws both: a supervisor and the person they
 * supervise should be reading the same screen about the same month, so that a conversation
 * about it starts from one set of words rather than two.
 *
 * `mine` comes back with it, and is passed through rather than recomputed. A supervisor can
 * reach their own record by this route, and whose record a screen is drawing decides whether
 * each flag on it names the person it is about — a sentence that reads as a colleague being
 * discussed when it is somebody else, and as a file being kept when it is you. Comparing the
 * id against the session here would be a second answer to a question the server has already
 * answered.
 */
export async function getOperatorRecord(
  operatorId: string,
  days: number,
): Promise<{ record: QualityRecord; mine: boolean }> {
  const body = await unwrap(
    api.GET('/v1/quality/operators/{id}', {
      params: { path: { id: operatorId }, query: { days } },
    }),
  );
  return { record: body.record, mine: body.mine };
}

/**
 * The supervisor's list, with the window the server actually used.
 *
 * **A roster by default**, which is the whole difference between a list and an accusation: it
 * includes the operators who recorded four hundred values and had nothing corrected, and those
 * rows are what stop the screen reading as "the people with problems, ordered". `onlyCorrected`
 * narrows it to the people who received a correction — a legitimate thing for a supervisor to
 * ask for, and a different act from being handed it.
 *
 * The server orders by employee code. Nothing here re-sorts by count unless somebody asks: see
 * `operatorsInOrder`.
 */
export async function listOperators(
  days: number,
  options: { onlyCorrected?: boolean } = {},
): Promise<{ operators: QualityOperator[]; window: QualityWindow }> {
  const body = await unwrap(
    api.GET('/v1/quality/operators', {
      params: {
        query: {
          days,
          // Presence, not a value. Sending `only_corrected=false` would narrow the list, which
          // is the opposite of what a caller passing `false` means.
          ...(options.onlyCorrected === true ? { only_corrected: 'true' } : {}),
        },
      },
    }),
  );
  return { operators: body.operators, window: body.window };
}

/**
 * The raised patterns. Open ones by default.
 *
 * `includeAnswered` asks for the answered ones too, and it is deliberately not the default:
 * a list of three open flags mixed with everything anybody ever acknowledged reads as a list
 * of forty problems, which is both wrong and the tone this feature cannot afford.
 */
export async function listFlags(
  days: number,
  options: { operatorId?: string; includeAnswered?: boolean } = {},
): Promise<QualityFlag[]> {
  const body = await unwrap(
    api.GET('/v1/quality/flags', {
      params: {
        query: {
          // The same window as the operator list and the records beside it, and it means the
          // same thing in all three: what happened in the period, raised or answered. An open
          // flag is never outside it — "what is still waiting" is not a question about a date
          // range — so nothing a supervisor is looking at can disagree with anything else.
          days,
          // The server takes the *presence* of `all`, not a value. Sending `all=false` would
          // ask for the answered ones, which is the opposite of what the caller said.
          ...(options.includeAnswered === true ? { all: 'true' } : {}),
          ...(options.operatorId === undefined ? {} : { operator_id: options.operatorId }),
        },
      },
    }),
  );
  return body.flags;
}

/**
 * What raises a flag, in the clinic's own vocabulary.
 *
 * Readable with a session and no permission, for the same reason the record above is: an
 * operator who can see a flag on their own record should be able to read what raised it
 * without having to ask a supervisor to explain it to them.
 */
export async function listThresholds(): Promise<ThresholdWithDescription[]> {
  const body = await unwrap(api.GET('/v1/quality/thresholds'));
  return body.thresholds;
}

/**
 * How a supervisor answers a flag. Two answers, and they are not variants of each other.
 *
 * Acknowledging is "I have seen this and I am doing something about it". Dismissing is "this
 * is not a problem", and it **carries the reason as a required field of its own variant** —
 * so a dismissal with nothing said cannot be constructed by leaving a property off. That is
 * the same rule a rejected correction follows (CP62) for the same reason: "no" with no reason
 * teaches nobody anything, and here it would also leave a flag on somebody's record with no
 * record of why it was dropped.
 *
 * The note on an acknowledgement is optional, and that asymmetry is deliberate. Saying what
 * you are doing about it is useful; requiring prose before a supervisor may say "yes, I have
 * this" would slow down the one act the whole mechanism depends on people bothering to do.
 */
export type FlagAnswer =
  { kind: 'acknowledged'; note?: string } | { kind: 'dismissed'; reason: string };

/**
 * Whether an answer has everything the server will ask for.
 *
 * Checked before the request rather than after it. The server refuses a reasonless dismissal
 * with `422` against `resolution`, and that refusal costs a supervisor a round trip and a
 * re-read of a form they have already filled in — on a clinic's connection, on a cheap
 * tablet, usually while somebody is waiting.
 */
export function flagAnswerReady(answer: FlagAnswer): boolean {
  return answer.kind === 'acknowledged' || answer.reason.trim() !== '';
}

/**
 * Answer a flag. Neither answer deletes anything. Needs `quality.flag.resolve`.
 *
 * A flag is never removed, only answered, and both answers are on the record with the name of
 * whoever gave them: a flag that could be made to disappear is a flag a supervisor can be
 * persuaded to make disappear.
 *
 * Answers `409` `QUALITY_FLAG_ANSWERED` when a colleague got there first — see
 * `alreadyAnswered`, which is a different situation from a failure and reads differently on
 * screen.
 */
export async function answerFlag(flagId: string, answer: FlagAnswer): Promise<QualityFlag> {
  const resolution =
    answer.kind === 'dismissed' ? answer.reason.trim() : (answer.note ?? '').trim();

  const body = await unwrap(
    api.POST('/v1/quality/flags/{id}/resolve', {
      params: { ...writing(), path: { id: flagId } },
      body: {
        status: answer.kind === 'dismissed' ? 'DISMISSED' : 'ACKNOWLEDGED',
        ...(resolution === '' ? {} : { resolution }),
      },
    }),
  );
  return body.flag;
}

/** The conflict code, branched on by code rather than by status. */
export const ALREADY_ANSWERED_CODE = 'QUALITY_FLAG_ANSWERED';

/** Whether this refusal means a colleague has already answered the flag. */
export function alreadyAnswered(error: unknown): boolean {
  return error instanceof ApiError && error.code === ALREADY_ANSWERED_CODE;
}

/** Whether a flag is still waiting for somebody. The server's own word for it. */
export function isOpen(flag: QualityFlag): boolean {
  return flag.status === 'OPEN';
}

/**
 * Whether the server was able to put a number on this window at all.
 *
 * The server answers `rate: null` when there are too few entries in the window for a rate to
 * mean anything. This is the only predicate about it, and there is deliberately no companion
 * that substitutes a zero: a rate computed from four entries is noise, and rendering noise as
 * a number invites somebody to act on it. A screen that reads `false` here says so in words.
 *
 * The floor itself is the server's and is not mirrored here. A copy would be a second opinion
 * that goes stale the day somebody changes the row, and the sentence on screen names no
 * number for exactly that reason.
 */
export function rateIsAvailable(subject: { rate?: number | null }): boolean {
  return subject.rate !== null && subject.rate !== undefined;
}

/**
 * How many more entries this window needs before there is a rate at all.
 *
 * The floor is now on the payload, which lets a screen say *"eight more values this month and a
 * rate appears"* where it used to be able to say only *"too few"*. That difference is most of
 * what makes this read as arithmetic rather than as a judgement: a threshold somebody can count
 * towards is a rule, and an unexplained refusal to show a number is a decision being taken about
 * them.
 *
 * Zero when the floor is already met — a caller with a rate in hand should be drawing it, not
 * explaining its absence.
 */
export function entriesUntilARate(subject: { entries: number; rate_floor: number }): number {
  return Math.max(0, subject.rate_floor - subject.entries);
}

/**
 * How the supervisor's list is ordered, and why this is a decision rather than a default.
 *
 * # The problem with the order the server gives
 *
 * `GET /v1/quality/operators` sorts by correction count, descending. That is the right order
 * for the query — it puts the rows a supervisor is looking for near the top — and it is the
 * wrong thing to render unchanged, because **position in a list is itself a claim**. A named
 * list sorted by a count of somebody's corrections, read top to bottom, is a ranking of the
 * worst regardless of what the column headings say, and no amount of denominators printed
 * beside the number undoes the reading. That is precisely the artefact the plan's risk note
 * describes.
 *
 * # And the problem with "just sort by rate instead"
 *
 * Rate is the better statistic and it is not a neutral order. It is the same ranking with a
 * fairer arithmetic, and it inverts in the wrong direction: three corrections out of
 * twenty-five sorts above eight out of nine hundred, which puts a new operator at the top of
 * a list a supervisor will read as a priority order. It is also undefined for anybody below
 * the entry floor, and nulls in a sort have to be put somewhere, which is one more claim
 * nobody meant to make.
 *
 * # What this does instead
 *
 * The default is `by-name`: alphabetical, stable, and carrying no assertion at all. The count
 * is still on every line, never on its own and never first — it is drawn inside the "out of"
 * sentence, in the same weight as the rest of it.
 *
 * A supervisor who *wants* the count order can ask for it, with a control that says in words
 * what it is about to do. Choosing to look at the busiest correction record is a legitimate
 * act and a different one from being handed that order without asking for it; the second is
 * the one that quietly turns a record into a league table.
 *
 * `by-corrections` reproduces the server's own order — count descending, ties broken by
 * staff code so the list does not shuffle between reads.
 */
export type OperatorOrder = 'by-name' | 'by-corrections';

export function operatorsInOrder(
  operators: readonly QualityOperator[],
  order: OperatorOrder,
  locale: string,
): QualityOperator[] {
  const rows = [...operators];
  if (order === 'by-corrections') {
    return rows.sort(
      (a, b) =>
        b.corrections - a.corrections ||
        (a.employee_code ?? '').localeCompare(b.employee_code ?? ''),
    );
  }
  return rows.sort(
    (a, b) =>
      nameFor(a, locale).localeCompare(nameFor(b, locale), locale) ||
      (a.employee_code ?? '').localeCompare(b.employee_code ?? ''),
  );
}

/**
 * The name to sort by — the reader's language, falling back to the other one.
 *
 * Not exported: it answers "which string do I compare" and nothing else. What a screen
 * *shows* goes through `staffLabel`, which is the house's one answer to "what do we call this
 * person", and a second one here would be how one screen comes to name a colleague
 * differently from the next.
 */
function nameFor(operator: QualityOperator, locale: string): string {
  const mine = (locale === 'bn' ? operator.name_bn : operator.name_en) ?? '';
  if (mine.trim() !== '') return mine;
  const other = (locale === 'bn' ? operator.name_en : operator.name_bn) ?? '';
  return other.trim() !== '' ? other : (operator.employee_code ?? '');
}

/**
 * The thresholds in the order the clinic put them in, ties broken by code for a stable list.
 *
 * The ordering column is load-bearing rather than cosmetic: the detector consults thresholds
 * in it, so what a reader sees first is what a flag would be raised under first.
 */
export function thresholdsInOrder(
  thresholds: readonly ThresholdWithDescription[],
): ThresholdWithDescription[] {
  return [...thresholds].sort(
    (a, b) =>
      a.threshold.ordering - b.threshold.ordering ||
      a.threshold.code.localeCompare(b.threshold.code),
  );
}

/**
 * Whether every threshold behind a set of flags is still an unsigned proposal.
 *
 * True on everything this system ships with. A screen uses it to say so once, at the top,
 * rather than repeating the sentence on each flag — but every flag still carries the fact on
 * its own face, because a flag read on its own, quoted in a message or photographed, must
 * carry it too.
 */
export function allThresholdsAreProposals(flags: readonly QualityFlag[]): boolean {
  return flags.length > 0 && flags.every((flag) => !flag.threshold_approved);
}
