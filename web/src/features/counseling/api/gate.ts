import { ApiError, writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

import { itemsInOrder, type CounselingItem } from './counseling';

/**
 * The counselling checkpoint and what a session actually covered (CP57, §5.5, §5.4).
 *
 * # Why this is its own file rather than more of `counseling.ts`
 *
 * `counseling.ts` is the authoring surface: templates, versions, items, the rules about
 * what may be edited and what must never be. This is the *floor's* surface — one visit, one
 * gate, the sessions walked against it — and it is read by a different person, under a
 * different permission (`counseling.session.read` rather than `counseling.template.read`),
 * with a different failure mode. Keeping them apart also makes the rule below structural
 * instead of remembered.
 *
 * # Nothing in this module ticks anything, and that is the point
 *
 * There is no `tickItem`, no `untickItem`, no `completeSession` and no `startSession` here.
 * The physician's panel is a read surface plus exactly one write — the override — and a tick
 * function within reach of it is how a physician ends up covering an item on a counsellor's
 * behalf, from a screen, without having said a word to the patient. That is precisely the
 * attribution CP56's criterion 1 pays a request per item to protect, and §5.4's method — ask
 * the patient what they were told about injection sites, then look at who told them — is
 * worthless the moment the answer can be somebody who was not in the room.
 *
 * `counseling-panel.test.tsx` has a named test that walks this module's exports and fails if
 * one is ever added whose name starts with tick, untick, complete or start.
 *
 * # `overridden` is not `covered`, and the types keep them apart
 *
 * `blocked` is what the queue will do; `overridden` is why it will not. A screen that
 * reduced the pair to one boolean would tell a physician the counselling was done. So
 * `gateState` answers with **three** values and there is no predicate here that collapses
 * them into two.
 *
 * # Time per item is elapsed time between ticks
 *
 * `itemProgress` derives a duration for each covered item from consecutive tick timestamps
 * within the session. It is **not** attention, and nothing here or on screen may present it
 * as such: a counsellor who talks for ten minutes and ticks four items at the end produces
 * three items of a second each and one of ten minutes. See the note on the function.
 */

export type CounselingGate = components['schemas']['CounselingGate'];
export type CounselingMissingItem = components['schemas']['CounselingMissingItem'];
export type CounselingGateOverride = components['schemas']['CounselingGateOverride'];
export type CounselingSession = components['schemas']['CounselingSession'];
export type CounselingTick = components['schemas']['CounselingTick'];

/**
 * The cache keys, held beside the calls.
 *
 * Granting an override changes two of them at once — the gate, and nothing else, because an
 * override covers no item and must not make a session look finished. The keys are here
 * rather than in the components for the reason CP54's are: the panel that reads the gate and
 * the card that overrides it are different files, and two spellings of one key is a
 * checkpoint that still says "holding" after somebody has just been let past.
 */
export function counselingGateKey(visitId: string) {
  return ['counseling', 'gate', visitId] as const;
}

export function counselingVisitSessionsKey(visitId: string) {
  return ['counseling', 'sessions', 'visit', visitId] as const;
}

export function counselingSessionKey(sessionId: string) {
  return ['counseling', 'session', sessionId] as const;
}

export function counselingOverridesKey(window: { from?: string; to?: string } = {}) {
  return ['counseling', 'overrides', window.from ?? null, window.to ?? null] as const;
}

/** The idempotency key the override carries. A browser has no offline queue; it still sends one. */
export function newEventId(): string {
  return crypto.randomUUID();
}

/**
 * What the checkpoint says about one visit, and what is missing.
 *
 * The whole gate comes back rather than a boolean, and callers keep it whole. `blocked`,
 * `overridden` and `missing` are three different answers and no two of them can be
 * reconstructed from the third.
 */
export async function getCounselingGate(visitId: string): Promise<CounselingGate> {
  const body = await unwrap(
    api.GET('/v1/counseling/visits/{visitId}/gate', { params: { path: { visitId } } }),
  );
  return body.gate;
}

/**
 * Every checklist opened for one visit, oldest first as the server orders them.
 *
 * The index carries no items and no ticks — `outstanding` is on it, always — so a panel
 * opens each session it means to show. A patient with two conditions gets one session per
 * matching rule, and the physician sees them together.
 */
export async function listCounselingSessionsForVisit(
  visitId: string,
): Promise<CounselingSession[]> {
  const body = await unwrap(
    api.GET('/v1/counseling/visits/{visitId}/sessions', { params: { path: { visitId } } }),
  );
  return body.sessions;
}

/**
 * One session with the frozen list it walked, every tick made on it, and what is outstanding.
 *
 * Withdrawn ticks come back too. "Ticked at 11:02 and taken back at 11:04" is an answer to
 * "what was covered", not the absence of one, and this module never filters them out.
 */
export async function getCounselingSession(sessionId: string): Promise<CounselingSession> {
  const body = await unwrap(
    api.GET('/v1/counseling/sessions/{sessionId}', { params: { path: { sessionId } } }),
  );
  return body.session;
}

/**
 * Send a patient past the checkpoint, in your own name, with a reason.
 *
 * The reason is a required positional parameter rather than an optional field, so this
 * cannot be called without one by leaving something out. The server refuses an empty one
 * with `422` against `reason`, a database constraint refuses it again, and a form should
 * refuse it a third time — an override with no reason is indistinguishable from a habit,
 * and telling those two apart is the entire value of recording it.
 *
 * Answers `422` against `reason` when nothing is outstanding, and `409` when an override
 * already stands on the visit. Neither is swallowed: both are facts the screen has to say,
 * because both mean the gate is not in the state the screen was drawn from.
 */
export async function overrideCounselingGate(
  visitId: string,
  reason: string,
): Promise<CounselingGate> {
  const body = await unwrap(
    api.POST('/v1/counseling/visits/{visitId}/gate/override', {
      params: { ...writing(), path: { visitId } },
      body: { event_id: newEventId(), reason: reason.trim() },
    }),
  );
  return body.gate;
}

/**
 * How often the valve is being used, and why. Quality's permission, not a physician's.
 *
 * The plan's mitigation for "a rigid gate will be worked around" is the override *plus*
 * rate monitoring, and monitoring nobody can read is a plan on paper. Whole days, half-open:
 * a window ending "now" would exclude the override granted a minute ago, which is the one
 * somebody is asking about.
 */
export async function listCounselingGateOverrides(
  window: { from?: string; to?: string } = {},
): Promise<{ from: string; to: string; overrides: CounselingGateOverride[] }> {
  return unwrap(
    api.GET('/v1/counseling/gate/overrides', {
      params: {
        query: {
          ...(window.from === undefined ? {} : { from: window.from }),
          ...(window.to === undefined ? {} : { to: window.to }),
        },
      },
    }),
  );
}

/**
 * The conflict code the override answers when one already stands.
 *
 * Branched on by `code` rather than by status. `409` is not one fact: the counselling
 * endpoints answer it for an item somebody has already covered, for a session somebody has
 * closed, and for an idempotency key reused with a different body — and a screen that read
 * the number would put "somebody has already sent this patient past" in front of a physician
 * whose request failed for one of the others. The contract names the code precisely so a
 * client can tell them apart.
 */
export const OVERRIDE_CONFLICT_CODE = 'COUNSELING_GATE_ALREADY_OVERRIDDEN';

/** Whether this refusal means somebody has already let this patient through. */
export function overrideAlreadyStands(error: unknown): boolean {
  return error instanceof ApiError && error.code === OVERRIDE_CONFLICT_CODE;
}

/** The floor the server puts under an override reason, mirrored so a form can say so first. */
export const OVERRIDE_REASON_MIN = 1;

export function overrideReasonAcceptable(reason: string): boolean {
  return reason.trim().length >= OVERRIDE_REASON_MIN;
}

/**
 * The three states the checkpoint can be in, and never two.
 *
 * The tempting reduction is `blocked ? 'held' : 'fine'`, and it is wrong in exactly the case
 * this checkpoint exists for: a visit that is not blocked *because somebody overrode it* has
 * an unfinished checklist and a patient walking to the next station. Drawing that the same
 * way as a completed one tells a physician the counselling was done, which is the single
 * failure the `overridden` field was added to the contract to prevent.
 *
 * `overridden` is checked before `blocked` because the server already sets `blocked` to
 * false when an override stands; asking about `blocked` first would report `clear`.
 */
export type GateState = 'blocked' | 'overridden' | 'clear';

export function gateState(gate: CounselingGate): GateState {
  if (gate.overridden) return 'overridden';
  return gate.blocked ? 'blocked' : 'clear';
}

/**
 * The two remediation paths, told apart by whether anybody opened the checklist.
 *
 * `session_id` present means a counsellor started this checklist and these items are still
 * outstanding: there is a session to go back to, and a room to send the patient to.
 * `session_id` absent means **nobody opened this checklist at all** — the patient's record
 * calls for it and no counsellor has begun. Those are different rooms, different people and
 * different sentences, and a single "missing items" list would send half the patients to the
 * wrong one.
 */
export interface MissingSplit {
  unfinished: CounselingMissingItem[];
  notStarted: CounselingMissingItem[];
}

export function missingByRemediation(gate: CounselingGate): MissingSplit {
  return {
    unfinished: gate.missing.filter((item) => item.session_id !== undefined),
    notStarted: gate.missing.filter((item) => item.session_id === undefined),
  };
}

/*
 * There is no `missingInOrder` here any more, and its absence is deliberate.
 *
 * It used to sort the gate's missing items — by checklist, then by the room's place in §5.2's
 * walk, then by item code — because `core.counseling_gate_missing` was a `UNION ALL` with no
 * `ORDER BY` and the rows arrived in whatever order the planner produced. The function now
 * carries exactly that `ORDER BY`, so the sort would be a second opinion about the order a
 * refusal reads in, held in a place nobody would think to look when the two disagreed. The
 * screens render `gate.missing` as it arrives.
 *
 * The one thing that changed with it: an item in a room the vocabulary does not list used to
 * sort last here and now sorts first, because the server coalesces an unknown room's ordering
 * to 0. That is the server's decision to make and it is visible in one place.
 */

/**
 * What was outstanding when an override was granted, with the text where it is still known.
 *
 * `missing_at_grant` is an array of **item codes and nothing else** — the server records no
 * text with it. `INSULIN_TECHNIQUE` on a physician's screen is a database identifier being
 * shown to a clinician, so the codes are matched against the gate's current `missing` list,
 * which does carry both languages, and an item still outstanding is named in words.
 *
 * An item covered *after* the override was granted has no match and keeps its code. That is
 * the honest answer rather than a blank: the record is of what was skipped at that moment,
 * and covering it afterwards does not make the override retrospectively unnecessary.
 */
export interface SkippedAtGrant {
  itemCode: string;
  item: CounselingMissingItem | undefined;
}

export function skippedAtGrant(
  override: CounselingGateOverride,
  missing: readonly CounselingMissingItem[],
): SkippedAtGrant[] {
  return override.missing_at_grant.map((itemCode) => ({
    itemCode,
    item: missing.find((candidate) => candidate.item_code === itemCode),
  }));
}

/** Whether this tick has been taken back. The row is kept, so an absence would be a lie. */
export function isWithdrawn(tick: CounselingTick): boolean {
  return typeof tick.undone_at === 'string' && tick.undone_at.trim() !== '';
}

/** The tick on one item, withdrawn or not. One row per item per session, by design. */
export function tickFor(session: CounselingSession, itemCode: string): CounselingTick | undefined {
  return (session.ticks ?? []).find((tick) => tick.item_code === itemCode);
}

/**
 * The ticks in the order they were made, which is the order the clock ran in.
 *
 * Withdrawn ticks are **included**. They happened, they took time on the floor, and dropping
 * them would silently add their share of the clock to whatever was ticked next — inflating
 * one item's elapsed time by the duration of a tick that was later taken back. Ties are
 * broken by item code so the sequence is the same on two renders of the same data.
 */
export function ticksInOrder(session: CounselingSession): CounselingTick[] {
  return [...(session.ticks ?? [])].sort(
    (a, b) =>
      Date.parse(a.ticked_at) - Date.parse(b.ticked_at) || a.item_code.localeCompare(b.item_code),
  );
}

/**
 * One item's line on the physician's panel: who covered it, when, and how long since the
 * item before it.
 *
 * # `elapsedMs` is time between ticks, not attention
 *
 * The duration is the gap between this tick and the one before it in the session — and for
 * the first tick, the gap from the session starting. That is all it can be: the system
 * records the moment a counsellor pressed a control, not the moment they started talking.
 *
 * A counsellor who works through four items in one conversation and ticks all four at the
 * end produces three items of a second each and one of eleven minutes. **The panel must not
 * present that as a fact about the teaching**, and the screen says so in words beside the
 * column rather than leaving the reader to assume the obvious wrong thing. It is still worth
 * showing: a whole session of one-second gaps is a checklist somebody ticked through in the
 * corridor, and that is exactly what §5.4's spot-questioning is there to catch.
 *
 * `null` when there is nothing honest to report — no tick, an unparseable timestamp, or a
 * tick that claims to precede the session. The last one is real: a phone that queued ticks
 * offline carries its own clock, and a negative duration on screen would read as a fault in
 * the record rather than in the clock.
 *
 * # Why the items are the spine and the ticks hang off them
 *
 * The checklist is what the patient was owed. Walking the ticks instead would draw a panel
 * that is complete when nothing was covered, which is the one shape this surface must never
 * take. An item with no tick is a row that says so.
 */
export interface ItemProgress {
  item: CounselingItem;
  tick: CounselingTick | undefined;
  /** A tick that stands. Withdrawn ticks are history, not coverage. */
  covered: boolean;
  /** A tick that was taken back. Visible, with its reason, never an absence. */
  withdrawn: boolean;
  /** Mandatory and on the session's own outstanding list — the server's answer, not ours. */
  outstanding: boolean;
  /** Milliseconds since the previous tick, or since the session started. Never attention. */
  elapsedMs: number | null;
  /** True when the duration is measured from the session's start rather than another tick. */
  fromSessionStart: boolean;
}

export function itemProgress(session: CounselingSession): ItemProgress[] {
  const chronological = ticksInOrder(session);
  const started = Date.parse(session.started_at);

  const elapsed = new Map<string, { ms: number | null; fromStart: boolean }>();
  chronological.forEach((tick, index) => {
    const previous = index === 0 ? started : Date.parse(chronological[index - 1]!.ticked_at);
    const at = Date.parse(tick.ticked_at);
    const ms = at - previous;
    elapsed.set(tick.item_code, {
      // A negative or unreadable gap is reported as no answer rather than as a number. See
      // the note above: the offline phone's clock is the usual cause and it is not the
      // record that is wrong.
      ms: Number.isFinite(ms) && ms >= 0 ? ms : null,
      fromStart: index === 0,
    });
  });

  // The server's own list of what is still missing, never recomputed from `mandatory` and
  // the ticks. `core.counseling_outstanding` is what the gate reads, and a second opinion
  // here is how a panel shows a green tick while the gate refuses the patient in front of it.
  const outstanding = new Set(session.outstanding);

  return itemsInOrder(session.items ?? []).map((item) => {
    const tick = tickFor(session, item.item_code);
    const withdrawn = tick !== undefined && isWithdrawn(tick);
    const timing = elapsed.get(item.item_code);
    return {
      item,
      tick,
      covered: tick !== undefined && !withdrawn,
      withdrawn,
      outstanding: outstanding.has(item.item_code),
      elapsedMs: timing?.ms ?? null,
      fromSessionStart: timing?.fromStart ?? false,
    };
  });
}

/** How many of the session's items have a tick that stands. Withdrawn ones do not count. */
export function coveredCount(session: CounselingSession): number {
  return itemProgress(session).filter((row) => row.covered).length;
}

/** Whether a counsellor has closed this session. Never inferred from what is outstanding. */
export function isFinished(session: CounselingSession): boolean {
  return typeof session.completed_at === 'string' && session.completed_at.trim() !== '';
}

/**
 * Minutes and seconds, for a screen that has to say a duration in two languages.
 *
 * Split rather than formatted, because the numerals and the words are the message file's
 * business: Bengali reads its own digits in running text, and a string built here would
 * carry ASCII into a Bangla sentence.
 */
export interface ElapsedParts {
  minutes: number;
  seconds: number;
}

export function elapsedParts(ms: number): ElapsedParts {
  const total = Math.max(0, Math.round(ms / 1000));
  return { minutes: Math.floor(total / 60), seconds: total % 60 };
}
