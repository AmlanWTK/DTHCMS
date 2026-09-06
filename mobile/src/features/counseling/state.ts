import type { components } from '@dthcms/api-client';

/**
 * Counselling ticking, as data (CP56, §5.3, [R-07]).
 *
 * # Why every decision is in here and none of it is in the screen
 *
 * A React Native component cannot be rendered outside a device, so anything it decides is a
 * decision nobody checks. What this station decides is not layout. It decides what one tap
 * asserts — that *this* counsellor covered *this* item with *this* patient, at a moment a
 * physician will later ask the patient about (§5.4) — and it decides how a half-walked
 * checklist reads to the person holding the phone. Both of those live in pure functions with
 * tests beside them.
 *
 * # One tap is one act, and there is nothing in this file that ticks a list
 *
 * Criterion 1 is that every tick carries its own attribution and timestamp, and it is only as
 * strong as the smallest thing a client can send. `toTick` builds a body for exactly one item
 * code; `oneItemCode` refuses anything that is not a single code, which is what stops
 * `"DIET,EXERCISE"` slipping through a field typed as a string; and there is no function here
 * or in `api.ts` that takes an array of codes. Seven items covered is seven requests, on
 * purpose — the cost is paid where it is cheapest, and the alternative is a record that says
 * "the counselling was done by X" when two counsellors and an insulin corner were involved.
 *
 * # Seven items is seven taps and a completion, and `tapsToComplete` is how that stays true
 *
 * The optional per-item note is behind a tap and never blocks the tick; there is no per-item
 * confirmation; and the rooms are headings on one list rather than screens to navigate
 * between. None of that is visible in a screenshot a year from now, so it is a number here
 * instead: `tapsToComplete` of a seven-item checklist is eight, and a redesign that adds a
 * confirm step fails a test rather than a stopwatch.
 *
 * # Progress is two numbers, and this file cannot produce a percentage
 *
 * "Five of seven" is what a counsellor says out loud. A percentage rounds away the difference
 * between finished and nearly finished, which on a checklist whose last item is insulin
 * technique is the difference that matters. `Progress` carries integers and there is no
 * ratio, no fraction and no rounding anywhere in this feature.
 *
 * The covered figure is `mandatory − outstanding`, and `outstanding` is the server's own list,
 * from the same database function CP57's gate reads. A second implementation of "what is still
 * missing" is how a phone shows a green tick while a gate refuses the patient standing in
 * front of it.
 *
 * # Finishing with items outstanding is a record, not a failure
 *
 * The patient left; the interpreter did not arrive; the insulin corner was closed. The button
 * says so and finishes anyway — `finishRefused` is true for exactly one reason, a session
 * somebody already closed, and never because something is uncovered. `Tone` has no failure
 * value at all, so no row and no banner on this screen can be drawn as an alarm. Whether such
 * a visit reaches the physician is CP57's gate to decide, and it can only decide it because
 * this screen did not make the situation unrecordable.
 *
 * # The list is frozen, and nothing here can thaw it
 *
 * `rowsOf` reads `session.items` and takes no template, no version and no catalogue. A
 * checklist republished while a counsellor is halfway down it must not change what this
 * patient was asked about (CP55 criterion 2), and the way that stays true is that there is no
 * argument through which a fresher list could arrive. When the server refuses an item code the
 * frozen version does not contain, `adviceFor` says **reload** — because the phone is the one
 * holding the stale list.
 *
 * # Which checklist a patient gets is answered by the server, and offered here
 *
 * `choicesOf` turns the server's answer to "which checklists does this visit call for" into a
 * row the counsellor can act on, and the only act it offers is **starting one the server
 * already named**. There is no template picker and there is no function here that takes a list
 * of every checklist in the clinic: assignment is a rule keyed on a coded condition (§5.1), and
 * a counsellor choosing from a catalogue would be that clinical assignment made by hand, by the
 * person least placed to make it. Where the server named an open session, `resumeOf` returns it
 * — a second session on a list a colleague is halfway down is how two people each cover half of
 * it and each believe the other did the rest.
 *
 * # A tick refused because somebody covered it first says so in a code
 *
 * The server refuses a tick on an item that already has a live tick rather than re-attributing
 * it, because overwriting `ticked_by` on a mis-tap is the one way this record loses the answer
 * §5.4 exists to ask for. Three different facts arrive as `409` and the contract separates them
 * by `code`, so `alreadyCovered` reads the code rather than guessing from which request met the
 * conflict. That refusal is not the counsellor doing something wrong, so it gets its own
 * sentence — and `readsBack` sends the screen for the session again, because the name of the
 * colleague who covered it is in the record rather than on this phone.
 *
 * # A retry re-sends the id the attempt failed with
 *
 * The same 409 is answered *successfully* when the request carries the event id the stored tick
 * was written with — that is what an offline replay looks like, and the clinic's link drops for
 * seconds at a time (ADR-0004). So an attempt whose fate is unknown keeps its event id
 * (`eventFor`, `keepsItsEvent`) and a fresh one is minted only once the last one is settled.
 * A retry that invented a new id would turn its own successful write into a 409 against itself.
 *
 * # The gate is read, never decided (CP57, §5.5)
 *
 * The last section of this file is the checkpoint that holds a patient back from the physician,
 * and every value in it comes off the server's answer unchanged. `gateStateOf` reads two
 * booleans the contract sets; `remedyFor` reads whether the server sent a `session_id`; nothing
 * here counts ticks, compares a progress figure against a total, or produces a `blocked` of its
 * own. The enforcement is a trigger on the queue table — a phone cannot get past it and must not
 * try to anticipate it, because a client-side "looks finished to me" is exactly how a green tick
 * appears on a screen while the queue refuses the patient standing in front of it.
 *
 * What this file *does* decide is how the answer reads: which items, grouped into which rooms,
 * and which of the two ways back each one is. A checklist somebody left half-walked is resumed;
 * a checklist nobody opened is started, and that is a different room. Sending half the patients
 * to the wrong one is the failure `remedyFor` exists to prevent, and the server draws the line
 * for it by leaving `session_id` off.
 *
 * There is no override anywhere in this feature and there must never be one. Overriding needs
 * `counseling.gate.override`, which a station operator does not hold; a control for it would be
 * a button that answers 403 in front of a patient, so the screen names who *can* instead.
 */

// --- what the contract gives us ---

export type CounselingSession = components['schemas']['CounselingSession'];
export type CounselingItem = components['schemas']['CounselingItem'];
export type CounselingTick = components['schemas']['CounselingTick'];
/** One checklist this visit calls for, and whether somebody is already walking it. */
export type CounselingChecklist = components['schemas']['CounselingChecklist'];

/** What the counselling checkpoint says about one visit, missing items and all (CP57, §5.5). */
export type CounselingGate = components['schemas']['CounselingGate'];
/** One mandatory item nobody has covered, with the room it is covered in. */
export type CounselingMissingItem = components['schemas']['CounselingMissingItem'];
/** A patient let past the gate, by a named person, with a reason. Read here; never written. */
export type CounselingGateOverride = components['schemas']['CounselingGateOverride'];

/** The interface language. Local rather than imported: this file must stay renderer-free. */
export type Locale = 'en' | 'bn';

/** Who is reading, in which language. `me` is what keeps somebody else's tick from reading as yours. */
export interface Reader {
  locale: Locale;
  /** The operator's own id. Empty when the session store has not answered yet. */
  me: string;
}

/**
 * The permission a counsellor holds, named once.
 *
 * Read as a courtesy so the screen can say, before a tap, that this hat reads the checklist
 * but does not tick it. It is **not** the enforcement — the server decides, and a client that
 * treated its own copy of a grant as the answer would be a second, staler account of a rule it
 * does not own. `mayTick` therefore only ever chooses a sentence, or which of two rows is drawn
 * for the same checklist; it decides no request. It used to gate the checklist question itself,
 * and that was this file's copy of a grant answering for the server — a reader who may ask was
 * shown a blank page because the phone had already decided for them.
 *
 * It is held by the nutritionist and the exercise and prescription-education officers as well as
 * the counsellor. §5.2 walks three rooms, and only the counsellor holding it would have meant
 * every other room's items ticked by proxy.
 */
export const PERM_TICK = 'counseling.tick';

export function mayTick(permissions: readonly string[]): boolean {
  return permissions.includes(PERM_TICK);
}

// --- what a single item code is, and what it is not ---

/**
 * One item code, and there is no way through this function to obtain two.
 *
 * The contract types `item_code` as a string with a maximum length, which is exactly the shape
 * a batch would arrive in: `"DIET,EXERCISE"` is a valid string and it is seven acts wearing
 * one press's attribution. The pattern is the database's own — an upper-case identifier — so a
 * comma, a space, a newline or a semicolon is refused here, before it is a request, and the
 * only thing that can leave this tablet is a single item.
 *
 * Trimmed and upper-cased because the server does the same. A check that disagreed with what
 * actually goes on the wire would refuse a request the server would accept, or — worse the
 * other way round — pass one it will not.
 */
export const ITEM_CODE = /^[A-Z][A-Z0-9_]{1,59}$/;

export function oneItemCode(raw: string): string | null {
  const code = raw.trim().toUpperCase();
  return ITEM_CODE.test(code) ? code : null;
}

/**
 * The lengths the contract sets on the two free-text fields.
 *
 * Mirrored here so a counsellor who has written too much is told while the patient is in front
 * of them, rather than losing the note to a 422 in a corridor. The server remains the
 * authority; this only moves the sentence earlier.
 */
export const NOTE_MAX = 500;
export const REASON_MAX = 500;

// --- the two acts, and which of them asks for words ---

/**
 * The two things a counsellor does to an item, and the one rule that separates them.
 *
 * A tick takes an **optional** note: §5.3's note is what this counsellor wants the physician to
 * know about this item for this patient, and most items have nothing to add. A required note
 * is a note people fill with a full stop, so it is behind a tap and it never blocks the tick.
 *
 * An un-tick **requires** a reason. An un-tick with no reason is indistinguishable from a
 * mis-tap, and telling those two apart is the entire value of recording it (criterion 3). The
 * server refuses it too, by validation and by a database constraint — this is not the
 * enforcement, it is the sentence a person reads while they can still act on it.
 */
export const ACTS = ['tick', 'untick'] as const;
export type Act = (typeof ACTS)[number];

export function needsReason(act: Act): boolean {
  return act === 'untick';
}

/**
 * Every refusal this screen can produce, each one mirroring a rule the server enforces.
 *
 * A list rather than a bare union for the same reason station 4's history keeps one: the
 * message files are checked against it, so a refusal added without a sentence written for it is
 * a test failure rather than a raw identifier under somebody's finger.
 */
export const PROBLEMS = [
  'needsReason',
  'reasonTooLong',
  'noteTooLong',
  'badItemCode',
  'notOnThisChecklist',
  'sessionClosed',
] as const;
export type Problem = (typeof PROBLEMS)[number];

/**
 * An un-tick with nothing said, refused before it leaves the tablet.
 *
 * Whitespace counts as nothing. A reason of three spaces satisfies a "not empty" check and
 * answers no question at all six months later, when somebody is trying to tell a correction
 * from a mis-tap.
 */
export function untickRefused(reason: string): boolean {
  return untickProblem(reason) !== null;
}

export function untickProblem(reason: string): Problem | null {
  if (reason.trim() === '') return 'needsReason';
  if (reason.trim().length > REASON_MAX) return 'reasonTooLong';
  return null;
}

/**
 * What is wrong with a tick, which is almost never anything.
 *
 * There is no `needsNote` here and there must never be one: the note is optional, and the only
 * thing that can be wrong with it is that it is longer than the column.
 */
export function tickProblem(
  session: CounselingSession,
  code: string,
  note: string,
): Problem | null {
  if (!sessionOpen(session)) return 'sessionClosed';
  const item = oneItemCode(code);
  if (item === null) return 'badItemCode';
  if (!onTheList(session, item)) return 'notOnThisChecklist';
  if (note.trim().length > NOTE_MAX) return 'noteTooLong';
  return null;
}

// --- the session itself ---

/** Whether anybody may still write on this session. Closed is closed; nothing reopens it here. */
export function sessionOpen(session: CounselingSession): boolean {
  return (session.completed_at ?? '').trim() === '';
}

/**
 * Whether an item code is on the list this session is walking.
 *
 * The same check the service makes, moved one layer earlier so a counsellor sees a sentence
 * rather than a refusal. It reads `session.items` and nothing else — the frozen version, as it
 * arrived — because the only client that sends a stale item code is one holding a list from
 * before a republish, and asking a fresher source would be that client asking itself.
 */
export function onTheList(session: CounselingSession, code: string): boolean {
  return (session.items ?? []).some((item) => item.item_code === code);
}

/**
 * A checklist's name in the reader's language, or its stable code before nothing at all.
 *
 * One implementation, used by both the session and the server's answer to "what does this visit
 * call for", because the two are the same checklist seen from two endpoints — and a chip row
 * that named one of them "DIABETES" and the other "Diabetes counselling" would read as two
 * different lists.
 */
function titleOf(english: string, bengali: string, code: string, locale: Locale): string {
  const own = (locale === 'bn' ? bengali : english).trim();
  if (own !== '') return own;
  const other = (locale === 'bn' ? english : bengali).trim();
  if (other !== '') return other;
  return code.trim();
}

export function sessionTitle(session: CounselingSession, locale: Locale): string {
  return titleOf(
    session.title_en ?? '',
    session.title_bn ?? '',
    session.template_code ?? '',
    locale,
  );
}

export function checklistTitle(checklist: CounselingChecklist, locale: Locale): string {
  return titleOf(checklist.title_en, checklist.title_bn, checklist.template_code, locale);
}

// --- which checklists this visit calls for ---

/**
 * One checklist this visit calls for, as something a counsellor can act on.
 *
 * Three states and no fourth: somebody is walking it, somebody finished it, or nobody has
 * opened it. The last one is the only one that offers a control, and the control opens the
 * checklist the **server** named — never one picked from a catalogue. Which checklist a patient
 * gets is an assignment rule keyed on a coded condition (§5.1), and a counsellor choosing from a
 * list of every checklist in the clinic would be making that clinical assignment by hand.
 */
export interface ChecklistChoice {
  templateId: string;
  /** The session already open for it, or empty when nobody has started it. */
  sessionId: string;
  /** The checklist's name in the reader's language, or its code before nothing at all. */
  title: string;
  /**
   * The coded condition that called for it, as one readable string — `ICD10 E11.9`.
   *
   * On screen because "why am I being asked to do this" deserves an answer where the question
   * is asked. Empty for a session the server could no longer match to a rule, which happens
   * legitimately: a rule retired at lunchtime does not make a half-ticked session disappear.
   */
  matched: string;
  /** True when somebody has opened this checklist for this visit. */
  started: boolean;
  finished: boolean;
  /** Called for, and nobody has opened it. The one state that offers a control. */
  startable: boolean;
}

/**
 * The visit's checklists, merged from the two answers a phone can get.
 *
 * The server's checklist answer is the better one — it names the open session *and* the
 * checklists nothing has been started for — and it now reads with either `counseling.tick` or
 * `counseling.session.read`. So a reviewer or a physician's panel sees what this visit *should*
 * have been walked through and not only what was, which is the half a gate refusal has to be
 * explained by; it used to be a 403 and an empty screen.
 *
 * The visit's own session index is still merged in behind it, for a narrower reason than the 403
 * it used to cover. The checklist answer carries one row per checklist, so a list that was
 * walked, finished and opened a second time names only the later session — and the two answers
 * are two requests, either of which can be the one that has not arrived. What the index adds is
 * a session the checklist answer did not name; it never replaces one.
 *
 * Order is the server's — open sessions first, then the clinic's own priority — and there is no
 * comparator here. Sessions the checklist answer did not name follow, in index order.
 */
export function choicesOf(
  checklists: readonly CounselingChecklist[],
  sessions: readonly CounselingSession[],
  locale: Locale,
): ChecklistChoice[] {
  const out: ChecklistChoice[] = [];
  const named = new Set<string>();

  for (const checklist of checklists) {
    const sessionId = (checklist.session_id ?? '').trim();
    if (sessionId !== '') named.add(sessionId);
    out.push({
      templateId: checklist.template_id,
      sessionId,
      title: checklistTitle(checklist, locale),
      matched: matchedCoding(checklist),
      started: sessionId !== '',
      finished: checklist.complete,
      startable: sessionId === '',
    });
  }

  for (const one of sessions) {
    if (named.has(one.id)) continue;
    out.push({
      templateId: one.template_id,
      sessionId: one.id,
      title: sessionTitle(one, locale),
      // The index says nothing about which rule matched. An invented coding would be this
      // screen claiming to know why a colleague opened a checklist.
      matched: '',
      started: true,
      finished: !sessionOpen(one),
      startable: false,
    });
  }

  return out;
}

/**
 * The condition that called for a checklist, as one string.
 *
 * The system travels with the code because a bare `E11.9` is not a coding (CP52) — and on a
 * screen that will one day show the clinic's own dictionary beside ICD-10, a code with no
 * system beside it is the sort of thing somebody reads as the wrong disease.
 */
function matchedCoding(checklist: CounselingChecklist): string {
  const code = (checklist.matched_code ?? '').trim();
  if (code === '') return '';
  const system = (checklist.matched_system ?? '').trim();
  return system === '' ? code : `${system} ${code}`;
}

/**
 * The session the phone opens on.
 *
 * The one still being walked, first. Resuming is the whole reason for asking the server which
 * checklists this visit calls for: a phone that opened a second session on a list a colleague is
 * halfway down is how two counsellors each cover half of it and each believe the other did the
 * rest, and the server names the open session precisely so that cannot happen.
 *
 * With nothing open, the last one that was started. Which finished checklist a reader lands on
 * is arbitrary, and it is left arbitrary rather than dressed up as a decision — every one of
 * them is a single tap away in the row above, and nothing is hidden by the choice.
 *
 * With nothing started at all, the empty string. There is no session, and the screen offers to
 * start one rather than inventing an id for a session that does not exist.
 */
export function resumeOf(choices: readonly ChecklistChoice[]): string {
  const walking = choices.find((choice) => choice.started && !choice.finished);
  if (walking !== undefined) return walking.sessionId;
  const started = choices.filter((choice) => choice.started);
  return started[started.length - 1]?.sessionId ?? '';
}

/**
 * The checklists this visit calls for that nobody has opened.
 *
 * What the start control offers a counsellor — and what a hat that may only read is shown as a
 * plain sentence, because since the checklist answer began accepting `counseling.session.read`
 * this is the only place a visit's *un*-walked counselling is visible at all. The list is drawn
 * for both; only the control is behind the permission. A reviewer shown nothing here would
 * conclude the visit called for nothing, which is the opposite of what they are looking at.
 */
export function startableOf(choices: readonly ChecklistChoice[]): ChecklistChoice[] {
  return choices.filter((choice) => choice.startable);
}

/**
 * The tick row for one item, live one first.
 *
 * Re-ticking an item that was taken back lands on the same row, so in practice there is one
 * row per code. Written to prefer a live tick anyway: if a future response ever carried two,
 * showing the withdrawn one as the current state would tell a counsellor an item is uncovered
 * when the server says it is covered.
 */
export function tickFor(ticks: readonly CounselingTick[], code: string): CounselingTick | null {
  const own = ticks.filter((tick) => tick.item_code === code);
  return own.find((tick) => (tick.undone_at ?? '') === '') ?? own[own.length - 1] ?? null;
}

/** Whether a tick counts right now. A withdrawn one is history, not progress. */
export function tickIsLive(tick: CounselingTick | null): boolean {
  return tick !== null && (tick.undone_at ?? '').trim() === '';
}

// --- the clock, in the clinic's own time ---

/**
 * Bangladesh Standard Time, as minutes ahead of UTC. UTC+06:00, all year.
 *
 * This is the app's *second* statement of the clinic's clock, and it is written down as one
 * named number rather than spelled `+06:00` inside a format string precisely because of that.
 * The first is `CLINIC_TIME_ZONE` in `lib/i18n.tsx` — `Asia/Dhaka`, handed to `use-intl` so that
 * every date a component formats is the clinic's rather than the tablet's. There is no pure
 * helper behind it to reuse: `use-intl` formats inside a React context, and this file must stay
 * renderer-free so its decisions can be tested without a device.
 *
 * A fixed offset rather than `Intl.DateTimeFormat` with the zone name, for two reasons. Hermes
 * ships a cut-down ICU on some builds, and a timestamp that silently renders in the tablet's own
 * zone because a locale database was missing is one record two people read as two different
 * times. And Bangladesh has kept a single offset with no daylight saving since 2009, so the
 * arithmetic here is exact rather than approximate — which the general case would not be.
 *
 * **What breaks if that ever changes.** If Bangladesh reintroduces daylight saving, or this
 * software is deployed anywhere that observes it, every time on this screen is wrong by an hour
 * for part of the year: a tick made at 11:02 reads as 10:02, and §5.4's physician comparing the
 * counsellor's account against the record is comparing against a wrong clock. Nothing throws and
 * nothing looks broken, which is what makes it worth naming here. The fix at that point is a
 * real zone conversion, in one place, and `counseling.test.ts` checks this number against
 * `Asia/Dhaka` so the two statements of the clinic's clock cannot drift apart quietly.
 */
export const CLINIC_UTC_OFFSET_MINUTES = 360;

/**
 * A timestamp as a clock time, in the clinic's hours.
 *
 * Criterion 7 is that an item ticked at 11:02 and taken back at 11:04 says so, and it says so
 * in the time the counsellor's own watch shows. An ISO string in a row would be read by nobody
 * and copied wrongly by somebody.
 *
 * An unparseable timestamp returns an empty string rather than "Invalid Date". A clinical row
 * that says a thing happened at Invalid Date is worse than one that leaves the time out: the
 * first is read as data.
 */
export function clockTime(iso: string): string {
  const parsed = Date.parse(iso);
  if (Number.isNaN(parsed)) return '';
  const local = new Date(parsed + CLINIC_UTC_OFFSET_MINUTES * 60_000);
  const hours = String(local.getUTCHours()).padStart(2, '0');
  const minutes = String(local.getUTCMinutes()).padStart(2, '0');
  return `${hours}:${minutes}`;
}

// --- who did it, and when ---

/**
 * What one tick says about the person who made it (criterion 6).
 *
 * Two counsellors and an insulin corner are involved in one session, so the screen has to say
 * whose act each tick was. `mine` is false whenever the reader's own id is unknown — an empty
 * `me` means the session store has not answered yet, and an unknown reader defaulting to "you"
 * is exactly the sentence that must never appear against somebody else's work.
 */
export interface Attribution {
  by: string;
  /** The role the tick was made under, where the server named one. */
  role: string;
  at: string;
  mine: boolean;
}

export function attributionOf(by: string, role: string, at: string, me: string): Attribution {
  const actor = (by ?? '').trim();
  const reader = (me ?? '').trim();
  return {
    by: actor,
    role: (role ?? '').trim(),
    at: (at ?? '').trim(),
    // Both non-empty, both equal. An unknown reader is never "you".
    mine: reader !== '' && actor !== '' && actor === reader,
  };
}

/**
 * The message key for whose tick this is.
 *
 * Two keys and no third, and the one that says "you" is reachable only when the two ids match.
 * A screen that interpolated a name would read as "you did this" the moment the name was
 * missing; a key cannot.
 */
export function attributionKey(attribution: Attribution): string {
  return attribution.mine ? 'byYou' : 'bySomebodyElse';
}

/**
 * Everything one item's tick has been through (criterion 7).
 *
 * A withdrawn tick is visible history, not an absence: an item ticked at 11:02 and taken back
 * at 11:04 says both, with both names and the reason. A screen that drew a withdrawn tick as
 * an empty checkbox would leave the next reader unable to tell a correction from a thing that
 * never happened — and `undoCount` is there because "ticked and un-ticked three times" is
 * exactly what a quality review is looking for.
 */
export interface TickHistory {
  ticked: Attribution;
  /** Null while the tick stands. Present, with its reason, once somebody takes it back. */
  withdrawn: (Attribution & { reason: string }) | null;
  /** Remembered across re-ticks by the server, so a re-ticked item still shows its past. */
  undoCount: number;
  live: boolean;
}

export function historyOf(tick: CounselingTick, me: string): TickHistory {
  const undoneAt = (tick.undone_at ?? '').trim();
  return {
    ticked: attributionOf(tick.ticked_by, tick.ticked_role ?? '', tick.ticked_at, me),
    withdrawn:
      undoneAt === ''
        ? null
        : {
            ...attributionOf(tick.undone_by ?? '', '', undoneAt, me),
            reason: (tick.undone_reason ?? '').trim(),
          },
    undoCount: tick.undo_count,
    live: undoneAt === '',
  };
}

// --- the words on an item, in the reader's language ---

/**
 * One piece of an item's text, and which language it turned out to be in.
 *
 * Publishing already refuses a version with an item missing either language
 * (`counseling_publishes_only_in_both_languages`), so a missing translation on a session's
 * frozen list is an edge case rather than a shape to design around. It is still worth one
 * honest line: a Bangla-reading counsellor handed English with no explanation is a counsellor
 * who thinks the app switched languages on them, and — on an item like injection technique —
 * one who may read it aloud wrongly rather than admit they cannot read it.
 *
 * So the fallback happens **and says so**. `ownLanguage` false is what the screen labels.
 */
export interface Wording {
  text: string;
  /** The language the text actually is, or null when there is none in either. */
  language: Locale | null;
  ownLanguage: boolean;
}

export function wordingOf(english: string, bengali: string, locale: Locale): Wording {
  const own = (locale === 'bn' ? bengali : english).trim();
  if (own !== '') return { text: own, language: locale, ownLanguage: true };

  const other: Locale = locale === 'bn' ? 'en' : 'bn';
  const fallback = (locale === 'bn' ? english : bengali).trim();
  if (fallback !== '') return { text: fallback, language: other, ownLanguage: false };

  // Neither. For guidance this is ordinary — most items have none — and the screen simply
  // draws nothing. For an item's own text it is a row nobody should guess at, and the screen
  // says that instead of showing a blank line where the question belongs.
  return { text: '', language: null, ownLanguage: false };
}

// --- one row of the checklist ---

/**
 * How loud a row is, and there is deliberately no fourth value.
 *
 * `Tone` has no failure state. An item nobody has covered yet is *quiet* — it is the normal
 * condition of a checklist somebody is halfway down, and drawing it red would make a screen
 * that is red for most of every session, which is a screen people stop reading. The one thing
 * worth a second look is a tick somebody took back, and that is `borderline`.
 *
 * Nothing on this screen is carried by tone alone. Every row also says what it is in words,
 * because roughly one man in twelve who will work here cannot rely on the colour.
 */
export const TONES = ['quiet', 'normal', 'borderline'] as const;
export type Tone = (typeof TONES)[number];

/** One item as the screen draws it. Everything decided; nothing left for the component. */
export interface ItemRow {
  item: CounselingItem;
  code: string;
  /** The item's own words, in the reader's language where there are any. */
  text: Wording;
  /** What to actually say, where the item carries it. Empty text means the item has none. */
  guidance: Wording;
  mandatory: boolean;
  room: string;
  /** The live tick, or the withdrawn one, or null. */
  tick: CounselingTick | null;
  /** True only for a tick that stands right now. */
  covered: boolean;
  history: TickHistory | null;
  /** §5.3's note, as it was written. Empty when there is none. */
  note: string;
  /**
   * Mandatory, and named by the server's own outstanding list.
   *
   * Never computed from the ticks: `core.counseling_outstanding` is what CP57's gate reads,
   * and two answers to "what is still missing" is how a phone and a gate come to disagree
   * about the patient standing between them.
   */
  outstanding: boolean;
  tone: Tone;
}

export function toneFor(covered: boolean, history: TickHistory | null): Tone {
  if (covered) return 'normal';
  // Ticked, then taken back. The one row on this screen worth reading twice.
  if (history !== null) return 'borderline';
  return 'quiet';
}

/**
 * The checklist, as rows, **in the order the session sent them**.
 *
 * Never re-sorted and there is no comparator in this file. The order is the order the
 * physician authored, which is the order §5.4's spot-questioning assumes; a screen that sorted
 * by room, or by whether something was covered, would move insulin technique above glucometer
 * use for one counsellor and not another.
 *
 * It reads `session.items` and takes no template, no version and no room catalogue. That
 * absence is criterion 8: there is no argument through which a freshly-fetched list could
 * arrive, so a checklist republished mid-session cannot change what this patient was asked.
 */
export function rowsOf(session: CounselingSession, reader: Reader): ItemRow[] {
  const ticks = session.ticks ?? [];
  // Read straight off the session, with no `?? []` behind it. The field is required by the
  // contract and the server fills it on the index as well as on a single session, so an empty
  // list means "nothing is missing" everywhere — where a coalesced default would have meant
  // that *and* "this row was never asked", and no reader could tell which.
  const outstanding = new Set(session.outstanding);

  return (session.items ?? []).map((item) => {
    const tick = tickFor(ticks, item.item_code);
    const covered = tickIsLive(tick);
    const history = tick === null ? null : historyOf(tick, reader.me);
    return {
      item,
      code: item.item_code,
      text: wordingOf(item.text_en, item.text_bn, reader.locale),
      guidance: wordingOf(item.guidance_en ?? '', item.guidance_bn ?? '', reader.locale),
      mandatory: item.mandatory,
      room: item.room,
      tick,
      covered,
      history,
      note: covered ? (tick?.note ?? '').trim() : '',
      outstanding: outstanding.has(item.item_code),
      tone: toneFor(covered, history),
    };
  });
}

/**
 * Whether the optional note may still be written for this item.
 *
 * Before the tick, and only before it. The note travels **in** the tick request, and the
 * server's projection lands a second tick on the same row: re-ticking a covered item to attach
 * an afterthought would overwrite `ticked_by` and `ticked_at` with the person who wrote the
 * note and the moment they wrote it. §5.4's question — who told this patient about injection
 * sites — is about the first fact, and a screen that offered "add a note" on a covered item
 * would quietly rewrite the answer.
 *
 * So the note is offered before covering, it never blocks the tick, and afterwards it is shown
 * rather than edited. Correcting one means taking the tick back with a reason, which is the
 * honest path and is already built.
 */
export function noteOpenable(row: ItemRow): boolean {
  return !row.covered;
}

// --- the rooms, as headings ---

/**
 * One room's worth of the list.
 *
 * A heading, not a screen. §5.2 walks three rooms and the temptation is to make each a page
 * with a "next room" button — which turns a seven-tap session into seven taps plus four
 * navigations, and hides from the counsellor in the nutrition room what the counselling room
 * already covered. They are headings on one list, so the whole checklist is always one scroll
 * away and the taps stay at one per item.
 */
export interface RoomGroup {
  room: string;
  /** The room's name in the reader's language, or its code before nothing at all. */
  heading: Wording;
  /**
   * True when this room belongs to the station the operator is working.
   *
   * A marking, never a filter. The nutritionist sees the whole checklist — what the counselling
   * room covered is exactly what stops them repeating it — and this only says which part of it
   * is theirs. Where the item does not say which queue its room belongs to, nothing is marked
   * and nothing is hidden, which is the correct way for that to be missing.
   */
  yours: boolean;
  rows: readonly ItemRow[];
}

/**
 * The rows grouped under their rooms, in the order the rooms first appear.
 *
 * First appearance rather than the catalogue's `ordering`, and that is a deliberate choice
 * between two orderings that can disagree: the items come back sorted by the author's own
 * `ordering`, and the rooms carry a configured sequence of their own. Following the items
 * means the counsellor walks the list in the order it was written, and it means this function
 * has exactly one ordering rule rather than two that will one day contradict each other.
 *
 * Within a group the rows keep the order they arrived in. Grouping walks the list once and
 * never holds two rows at a time, so it cannot reorder anything inside a room.
 */
export function roomsOf(rows: readonly ItemRow[], locale: Locale, station = ''): RoomGroup[] {
  const groups: RoomGroup[] = [];
  const byRoom = new Map<string, ItemRow[]>();

  for (const row of rows) {
    const existing = byRoom.get(row.room);
    if (existing !== undefined) {
      existing.push(row);
      continue;
    }
    const collected: ItemRow[] = [row];
    byRoom.set(row.room, collected);
    groups.push({
      room: row.room,
      heading: roomHeading(row, locale),
      yours: roomIsYours(row, station),
      rows: collected,
    });
  }
  return groups;
}

/**
 * The room's name, from the session's own copy.
 *
 * The item carries `room_en`/`room_bn` because the server joins the room when it reads the
 * session's frozen list, so no second fetch is needed to draw a heading — and a heading that
 * needed one would be a heading that is blank for as long as the clinic's link takes.
 */
function roomHeading(row: ItemRow, locale: Locale): Wording {
  const own = wordingOf(row.item.room_en ?? '', row.item.room_bn ?? '', locale);
  if (own.text !== '') return own;
  // The bare code. A blank heading would silently merge two rooms into one list.
  return { text: row.room, language: null, ownLanguage: false };
}

/**
 * Whether this room is worked from the station the operator is standing in.
 *
 * The mapping is `room_station` on the item — the insulin corner belongs to the counselling
 * station rather than being one of its own — and it arrives with the session, so this screen
 * asks the server one question rather than two. It used to mean a second fetch of the room
 * catalogue, which bought exactly this one boolean and left the marking missing for as long as
 * a second request took on a clinic link that drops for seconds at a time.
 *
 * It decides a marking and nothing else: an item that carries no station leaves its room
 * unmarked, and every row is still drawn.
 */
function roomIsYours(row: ItemRow, station: string): boolean {
  const at = station.trim();
  if (at === '') return false;
  return (row.item.room_station ?? '').trim() === at;
}

// --- progress, as two numbers ---

/**
 * How much of the checklist is covered — mandatory items only, as two integers.
 *
 * "Five of seven" is what a counsellor says out loud, and it is what this returns. There is no
 * percentage here and there is nothing to compute one from without doing the division
 * yourself: a percentage rounds away the difference between finished and nearly finished, and
 * on a list whose last item is insulin technique that is the difference that matters.
 *
 * Optional items are drawn on the list and are **not** counted as outstanding, because the
 * gate does not count them. `optional` and `optionalCovered` are reported separately so a
 * screen can show them without folding them into the figure the gate reads.
 */
export interface Progress {
  /**
   * Whether the two figures mean anything, which is a question about the **items**.
   *
   * False when the session arrived without them — the visit index answers that way. It does
   * carry its outstanding list now, so what an index row cannot say is how many items are
   * mandatory: the denominator, not the numerator. A screen drawing "0 of 0" from one would show
   * a finished-looking checklist for a visit nobody has counselled.
   *
   * `outstanding` below is therefore still true when this is false. `mandatory` and `covered`
   * are not, and there is nothing on an index row to make them true from.
   */
  known: boolean;
  mandatory: number;
  covered: number;
  outstanding: number;
  optional: number;
  optionalCovered: number;
  /** Somebody closed the session. Never inferred from the counts. */
  complete: boolean;
}

export function progressOf(session: CounselingSession): Progress {
  const items = session.items ?? [];
  const ticks = session.ticks ?? [];
  const mandatory = items.filter((item) => item.mandatory).length;
  const optional = items.length - mandatory;

  // The server's own list, never recomputed from the ticks. `core.counseling_outstanding` is
  // what CP57's gate reads, and a second answer here is how a phone shows a green tick while
  // a gate refuses the patient in front of it.
  const outstanding = session.outstanding.length;

  return {
    // From the items, because they are the half an index row leaves out. The outstanding list
    // arrives on one, so what is unknown there is the denominator — and "0 of 0" drawn from it
    // would read as a checklist somebody finished.
    known: items.length > 0,
    mandatory,
    // Clamped only because a negative "−1 of 7" would be unreadable. It cannot happen while
    // both figures come from the same frozen version.
    covered: Math.max(0, mandatory - outstanding),
    outstanding,
    optional,
    // Counted here rather than taken from the server because the server deliberately does not
    // count them: `outstanding` is the gate's question, and the gate does not ask about
    // optional items. There is no second answer to disagree with.
    optionalCovered: items.filter(
      (item) => !item.mandatory && tickIsLive(tickFor(ticks, item.item_code)),
    ).length,
    complete: !sessionOpen(session),
  };
}

// --- what finishing costs, and what it means ---

/**
 * One tap covers one item. There is no confirmation step and there must never be one.
 *
 * A dialog asking "cover this item?" doubles the cost of the honest act and teaches people to
 * dismiss dialogs, which is a habit they then carry to the dialog that matters. The un-tick is
 * where the second step lives, and it lives there because taking something back is the rarer
 * and more consequential act.
 */
export const TAPS_PER_ITEM = 1;

/** Finishing is one press. It is not a confirmation of the ticks; it is its own statement. */
export const TAPS_TO_FINISH = 1;

/**
 * Opening the checklist is one press, once, before the eight — and never once per item.
 *
 * It is a separate number rather than folded into `tapsToComplete` because it is a separate
 * promise: criterion 2 counts the walk, and starting happens once per checklist for whichever
 * of the three rooms reaches the patient first. It is a press at all only because the server
 * decides *which* checklist; the counsellor is confirming they have the patient in front of
 * them, not choosing a list.
 */
export const TAPS_TO_START = 1;

/**
 * What a whole session costs, in taps.
 *
 * Eight for the seeded seven-item checklist: one per item, one to finish. The point of the
 * number is that it is checked. None of what keeps it at eight — no per-item dialog, no
 * navigation between rooms, an optional note that is behind a tap and never blocks — is
 * visible in a screenshot a year from now, so a redesign that adds a step fails a test rather
 * than a stopwatch in a busy clinic.
 */
export function tapsToComplete(items: readonly CounselingItem[]): number {
  return items.length * TAPS_PER_ITEM + TAPS_TO_FINISH;
}

/**
 * Finishing the session, and what the button says while doing it.
 *
 * A counsellor may finish with items outstanding. That is not an error path and it is not an
 * escape hatch — the patient left, the interpreter did not arrive, the insulin corner was
 * closed, and the record then says exactly that. What the screen owes them is that the button
 * names the situation before it is pressed, and that the missing items are on screen by name
 * rather than as a count they have to go and reconcile.
 *
 * Nothing here decides whether such a visit reaches the physician. That is CP57's gate, it
 * reads the same outstanding list, and pre-empting it on this screen — by refusing the press,
 * by colouring it as an alarm, by asking "are you sure" — would make the honest record the
 * expensive option.
 */
export interface Completion {
  /** Whether the button does anything. False for a session somebody already closed. */
  open: boolean;
  /** The mandatory rows still uncovered, in list order, by name. */
  missing: readonly ItemRow[];
  /** Message key for the button: it changes with the situation, and it never reads as failure. */
  label: string;
  /** Message key for the sentence under it. */
  hint: string;
}

export function completionOf(session: CounselingSession, rows: readonly ItemRow[]): Completion {
  const missing = rows.filter((row) => row.outstanding);
  const anything = missing.length > 0;
  return {
    open: sessionOpen(session),
    missing,
    label: anything ? 'finishWithOutstanding' : 'finish',
    hint: anything ? 'finishOutstandingHint' : 'finishHint',
  };
}

/**
 * Whether finishing is refused, and there is exactly one reason it can be.
 *
 * A session somebody already closed. **Not** outstanding items: a screen that refused the
 * press until everything was ticked would leave a counsellor whose patient walked out with two
 * options, and the cheaper one is to tick the items they did not cover.
 */
export function finishRefused(session: CounselingSession): boolean {
  return !sessionOpen(session);
}

// --- what gets sent ---

/** Every write this feature makes carries its own event id, and it is the idempotency key. */
export interface WriteIDs {
  event: string;
}

// --- the four writes, and the event id each one keeps until it is settled ---

/**
 * The act a request was made for.
 *
 * Two jobs, and it has just lost the louder one. It still names the write an event id is held
 * against (`attemptKey`), which is what keeps ticking `DIET` and ticking `EXERCISE` two acts.
 * It is no longer what tells the 409s apart: that was true while the server answered one generic
 * conflict for a session somebody closed, an item somebody covered and an id reused with a
 * different body, and `code` is what separates them now.
 *
 * It stays on `Trouble` because the screen holds one trouble at a time for all four writes, and
 * the value would otherwise not say which of them it belongs to — for `start` and `finish` there
 * is not even an item code beside it. Nothing branches on it; it is context, and it is the sort
 * of field to spend on a sentence or delete rather than to leave growing readers by accident.
 */
export const ATTEMPTS = ['start', 'tick', 'untick', 'finish'] as const;
export type Attempt = (typeof ATTEMPTS)[number];

/**
 * The event ids of attempts nobody knows the fate of, by attempt.
 *
 * Held by the screen, decided here. An empty record is the ordinary state: an id is kept only
 * between a write whose answer never arrived and the press that retries it.
 */
export type HeldEvents = Readonly<Record<string, string>>;

/**
 * The name an attempt's event id is held under.
 *
 * Three parts, because all three change what the attempt *is*: the act, the thing it is done to
 * (the session, or the visit for a start), and its subject (the item code, or the template for a
 * start). Ticking `DIET` and ticking `EXERCISE` are two attempts and must never share an id —
 * sharing one would make the second tick a replay of the first, and the ledger would record one
 * item covered where the counsellor covered two.
 */
export function attemptKey(attempt: Attempt, on: string, subject: string): string {
  return `${attempt}:${on.trim()}:${subject.trim()}`;
}

/**
 * The event id an attempt goes out with: the one it was first made with, where there is one.
 *
 * **This is what makes a retry a replay rather than a second act.** Ticking an item that already
 * has a live tick is refused with `409` — unless the request carries the event id the stored
 * tick was written with, in which case the server answers `200` with the session it already
 * produced. That is exactly what a phone whose reply was lost, or an offline queue replaying
 * what it wrote in a room with no signal, looks like. Minting a fresh id on the retry turns this
 * phone's own successful write into a conflict against itself, and puts "somebody has already
 * covered this" in front of the person who covered it.
 *
 * The replay is the contract's own promise — written on the `409` of
 * `POST /v1/counseling/sessions/{sessionId}/ticks` — rather than behaviour this app noticed and
 * decided to lean on. This function is the single place the client relies on it, and there is
 * nowhere else in the feature a uuid is minted.
 */
export function eventFor(held: HeldEvents, key: string, minted: string): string {
  const kept = (held[key] ?? '').trim();
  return kept === '' ? minted : kept;
}

/** The id this attempt went out with, remembered in case the answer never comes. */
export function holdEvent(held: HeldEvents, key: string, event: string): HeldEvents {
  return { ...held, [key]: event };
}

/** Forgotten, so the next press of the same control is a new act rather than a replay. */
export function releaseEvent(held: HeldEvents, key: string): HeldEvents {
  const next = { ...held };
  delete next[key];
  return next;
}

/**
 * Whether a failed attempt keeps the id it was made with.
 *
 * Only when nobody knows whether it landed — the request never left, or the server fell over
 * somewhere after receiving it. Then the next press must be the same attempt, so that a write
 * which did land is recognised rather than refused.
 *
 * A **refusal** is different and must forget its id. Nothing was written, the counsellor is
 * being told why, and whatever they do next is a new act: re-ticking an item after taking it
 * back is a genuinely new tick, by whoever ticks it this time, and reusing the old id would
 * have the server hand back the answer to a request nobody is making any more.
 */
export function keepsItsEvent(trouble: Trouble): boolean {
  return trouble.kind !== 'refused';
}

export interface StartRequest {
  event_id: string;
  patient_id: string;
  visit_id: string;
  template_id: string;
}

export interface TickRequest {
  event_id: string;
  /** One code. Singular in the type, singular in `oneItemCode`, singular on the wire. */
  item_code: string;
  note?: string;
}

export interface UntickRequest {
  event_id: string;
  item_code: string;
  reason: string;
}

export interface CompleteRequest {
  event_id: string;
}

/**
 * One item covered, as one request.
 *
 * The whole of criterion 1 in a function signature: one session, one code, one body. There is
 * no array parameter, no second code, and `oneItemCode` refuses a string that is secretly a
 * list. A caller who wanted to tick seven items would have to call this seven times, which is
 * the point — seven acts, seven event ids, seven actors, seven moments.
 *
 * Returns null for anything the server would refuse, so a caller cannot send one by forgetting
 * to ask: a closed session, a code the frozen list does not contain, a note longer than the
 * column. The empty note is **not** among them — a tick with nothing to add is the ordinary
 * case and it must never be the blocked one.
 */
export function toTick(
  session: CounselingSession,
  code: string,
  note: string,
  ids: WriteIDs,
): TickRequest | null {
  if (tickProblem(session, code, note) !== null) return null;
  const item = oneItemCode(code);
  if (item === null) return null;

  const body: TickRequest = { event_id: ids.event, item_code: item };
  // Omitted rather than sent empty. An empty string in the ledger is a note somebody wrote
  // nothing in, which is not the same as a note nobody wrote.
  if (note.trim() !== '') body.note = note.trim();
  return body;
}

/**
 * A tick taken back, with the reason criterion 3 requires.
 *
 * Null while there is no reason, which is not a state that sends. The tick's row is kept by the
 * server — a delete would satisfy the words and destroy the point, because the interesting
 * record is that somebody covered an item and then somebody decided they had not.
 */
export function toUntick(
  session: CounselingSession,
  code: string,
  reason: string,
  ids: WriteIDs,
): UntickRequest | null {
  if (!sessionOpen(session)) return null;
  const item = oneItemCode(code);
  if (item === null) return null;
  if (!onTheList(session, item)) return null;
  if (untickRefused(reason)) return null;
  return { event_id: ids.event, item_code: item, reason: reason.trim() };
}

/**
 * The counsellor saying they are done.
 *
 * It carries nothing but its event id, and that emptiness is deliberate. A completion that
 * also covered the outstanding items would be the batch attribution criterion 1 forbids,
 * wearing a different name, and it would let a session be closed by somebody who counselled
 * nobody. There is no `and_tick`, no `cover_rest`, and no list parameter to grow one into.
 */
export function toCompletion(session: CounselingSession, ids: WriteIDs): CompleteRequest | null {
  if (finishRefused(session)) return null;
  return { event_id: ids.event };
}

/**
 * Opening a checklist for the patient in the room.
 *
 * Idempotent at the server by design: a phone that lost the reply and pressed start again
 * lands back in the session it already has. Everything the body needs is an identifier
 * somebody else decided — this feature never chooses which checklist a patient gets, because
 * that is an assignment rule keyed on a coded diagnosis (CP55) and not a counsellor's guess.
 */
export function toStart(
  patientId: string,
  visitId: string,
  templateId: string,
  ids: WriteIDs,
): StartRequest | null {
  const parts = [patientId, visitId, templateId].map((part) => part.trim());
  if (parts.some((part) => part === '')) return null;
  return {
    event_id: ids.event,
    patient_id: parts[0] as string,
    visit_id: parts[1] as string,
    template_id: parts[2] as string,
  };
}

// --- what went wrong, and what the counsellor should do about it ---

/**
 * A refusal, an unreachable server, and a server that answered with something else.
 *
 * The same three shapes station 4 uses, with the status kept: what a counsellor should do next
 * depends on which refusal it was, and a message alone cannot say.
 */
export interface Trouble {
  kind: 'refused' | 'unreachable' | 'failed';
  /** Which of the four writes this belongs to. Context, not a branch — see `ATTEMPTS`. */
  attempt: Attempt;
  /** The HTTP status, or 0 when there was no answer at all. */
  status: number;
  /**
   * The server's own code for the refusal, or empty when there was no answer to carry one.
   *
   * The only part of an error a client may branch on. A message is written for a person: it gets
   * translated, shortened and improved, and a screen that matched on its words would change
   * behaviour the day somebody rewrote a sentence. Four of this station's refusals share one
   * status and are told apart here and nowhere else.
   */
  code: string;
  /** The field the server named on a refusal, or empty. */
  field: string;
  /** The server's own words, in the reader's language where there are any. */
  message: string;
}

/**
 * The one conflict code this station changes its behaviour for.
 *
 * `POST .../ticks` answers `409` for three different facts: this one, a session somebody
 * finished (`COUNSELING_SESSION_FINISHED`), and an event id already written with a different
 * body (`IDEMPOTENCY_KEY_REUSED`); the un-tick adds a fourth, an item that is not ticked
 * (`COUNSELING_ITEM_NOT_TICKED`). The other three are named here rather than given constants of
 * their own because nothing branches on them — they take the same **reload** — and a constant
 * nothing reads is one that goes stale without anybody noticing.
 */
export const CODE_ALREADY_COVERED = 'COUNSELING_ITEM_ALREADY_COVERED';

/**
 * What the counsellor can actually do about it.
 *
 * Three answers and no fourth, because a screen that offered "try again" for every failure
 * would train people to press it at the one failure where pressing it cannot help.
 */
export const ADVICE = ['reload', 'retry', 'none'] as const;
export type Advice = (typeof ADVICE)[number];

export function adviceFor(trouble: Trouble): Advice {
  if (trouble.kind === 'unreachable') return 'retry';

  // The frozen list, in the one place it can be felt. A 422 on `item_code` means this phone is
  // holding a checklist from before a republish — criterion 8's failure, arriving exactly where
  // the design said it would — and the only thing that fixes it is reading the session again.
  if (trouble.status === 422 && trouble.field === 'item_code') return 'reload';

  // A colleague covered the item first, the session is closed, or the item is not ticked.
  // Three codes, and the same answer for every one of them: this phone's copy is behind what the
  // server knows, so read it again. Pressing again would send the same refused request — and
  // would keep sending it, because the phone's reason for offering the control has not changed.
  //
  // The fourth conflict, `IDEMPOTENCY_KEY_REUSED`, arrives here too, and it is the one a reload
  // does not fix: it means a retry carried the id of an earlier attempt with a different body,
  // which on this screen is a counsellor who edited the note between two presses. What fixes it
  // is the next press, and that is already arranged — a refusal releases the held id
  // (`keepsItsEvent`), so the press after this one is a new act with a new id. The reload costs
  // one read and misleads nobody, which is why it does not get a branch of its own.
  if (trouble.status === 409) return 'reload';
  if (trouble.status === 404) return 'reload';

  // The hat being worn does not hold `counseling.tick`. Nothing on this screen fixes that and
  // nothing on it should try; the counsellor needs a different hat or a different person.
  if (trouble.status === 403) return 'none';

  // Any other refusal is about what was typed — a reason that is empty or too long — and the
  // server has already said which field. Retrying an unchanged request would fail identically.
  if (trouble.status === 422) return 'none';

  return 'retry';
}

/**
 * A tick refused because the item already has a live tick.
 *
 * The server refuses rather than re-attributing, and it is right to: overwriting `ticked_by` on
 * a mis-tap is the one way this record loses the answer §5.4 exists to ask for. But the
 * counsellor did nothing wrong — somebody in another room got there first, or their own earlier
 * tick landed after all — so this is not drawn as a refusal to be corrected.
 *
 * Recognised from the code the server sent, which is the only part of a refusal a client may
 * read. It used to be inferred from the act and the status together, because `409` was every
 * conflict at once — and that guess put "somebody has already covered this item" in front of a
 * counsellor whose session had simply been closed, and in front of one whose own retry had
 * carried an event id it had already used with a different note. The server names the fact now,
 * and nothing here guesses at it.
 */
export function alreadyCovered(trouble: Trouble): boolean {
  return trouble.code === CODE_ALREADY_COVERED;
}

/**
 * Whether the screen goes back to the server for the session without being asked.
 *
 * True for exactly one refusal: an item somebody else has covered. The server's own sentence
 * tells the counsellor to open the session again to see who — and the phone can do that in the
 * seconds they spend reading the banner, rather than asking a person mid-consultation to press a
 * button in order to obey an instruction the phone was given.
 *
 * What it prevents is a phone that keeps a copy it has just been told is stale: the item still
 * reads as uncovered, the tick control is still live under a thumb, and the next press meets the
 * same refusal. After the read the row says covered, by whom and at what time, and `coveredBy`
 * names the colleague from the record instead of the screen inferring one.
 *
 * Not for the others. A session somebody closed and an item that is not ticked get the
 * **reload** button — the same read, with a person choosing when — because a screen that re-read
 * itself after every refusal is one that quietly retries in a room with no signal.
 */
export function readsBack(trouble: Trouble): boolean {
  return alreadyCovered(trouble);
}

/**
 * Who covered an item, from the session this phone is already holding.
 *
 * Named beside "somebody has already covered this" where this copy knows, and left unsaid where
 * it does not — which is the ordinary state at the moment the refusal lands, because a phone
 * that already knew would not have offered the tick.
 *
 * Nothing is fetched here. `readsBack` is what sends the screen back to the server on the one
 * refusal where the name matters, and this function then reads whatever arrived; a getter that
 * fetched would be doing a write's work, and would do it again on every render.
 */
export function coveredBy(
  session: CounselingSession | null,
  code: string,
  me: string,
): Attribution | null {
  if (session === null) return null;
  const item = oneItemCode(code);
  if (item === null) return null;
  const found = tickFor(session.ticks ?? [], item);
  if (found === null || !tickIsLive(found)) return null;
  return attributionOf(found.ticked_by, found.ticked_role ?? '', found.ticked_at, me);
}

/** The sentence above the server's own words. */
export function troubleKey(trouble: Trouble): string {
  // Its own sentence, because the generic one — "the server would not accept that" — reads as
  // the counsellor having done something wrong, and they have not.
  if (alreadyCovered(trouble)) return 'trouble.alreadyCovered';
  return `trouble.${trouble.kind}`;
}

// --- the gate, and the way back (CP57, §5.5) ---

/**
 * The code the queue refuses with, and the only part of that refusal a client may read.
 *
 * `POST /v1/visits/{id}/queue` answers `409` with this code when the counselling checkpoint is
 * holding the visit. A code rather than the message, for the reason `Trouble.code` exists at
 * all: the message is written for a person, it gets translated, shortened and improved, and a
 * screen that matched on its words would stop recognising the refusal the day somebody rewrote
 * a sentence.
 *
 * What a caller does on seeing it is **go and read the gate**. The server's sentence already
 * names the missing items — criterion 2 is satisfied before this app draws anything — but a
 * sentence cannot say which *room* an item belongs to, and the room is where the patient has to
 * be walked. `GET /v1/counseling/visits/{id}/gate` says both, per item, and says whether anybody
 * has opened the checklist at all, which is a third thing again.
 *
 * It is deliberately not fed to `adviceFor`. That function answers for this feature's own four
 * writes and its three answers are all wrong here: nothing on this phone is stale, pressing
 * again would meet the same refusal, and there *is* something to be done about it.
 */
export const CODE_GATE_BLOCKED = 'VISIT_GATE_BLOCKED';

export function gateRefused(trouble: Trouble): boolean {
  return trouble.code === CODE_GATE_BLOCKED;
}

/**
 * The three things the gate can say, and they are the server's own two booleans in one order.
 *
 * `blocked` is what the queue will do; `overridden` is why it will not. The contract keeps them
 * as separate fields for one stated reason — a screen that drew them the same way would be
 * telling a physician the counselling was done — so an overridden visit is its own state here,
 * with its own sentence, and it is never `clear`.
 *
 * A list rather than a bare union because the message files are checked against it: a state
 * added without a sentence written for it is a test failure rather than a raw identifier where
 * the explanation belongs.
 */
export const GATE_STATES = ['blocked', 'overridden', 'clear'] as const;
export type GateState = (typeof GATE_STATES)[number];

export function gateStateOf(gate: CounselingGate): GateState {
  // `blocked` first, because it is the answer the queue gives. The server already sets it false
  // when an override stands, so this order is what keeps "held" and "let through anyway" from
  // collapsing into one word.
  if (gate.blocked) return 'blocked';
  if (gate.overridden) return 'overridden';
  return 'clear';
}

/**
 * How loud the gate's answer is, and it is a second scale rather than a fourth `Tone`.
 *
 * `Tone` belongs to the checklist and has no failure value on purpose, so that no row and no
 * banner on the counselling list can be drawn as an alarm — an item nobody has covered yet is
 * the ordinary condition of a session somebody is halfway down. The gate is the other kind of
 * fact: a patient who cannot be sent to the next station, which is exactly CP54's reasoning for
 * drawing an unanswered allergy question in the loudest tokens rather than in the grey it draws
 * "not measured" in.
 *
 * These are the same status tokens CP54's gate uses, and that is the point. A refusal here is
 * not a failure — nothing was lost, nothing went wrong, the checkpoint did its job — so it must
 * not borrow the treatment this app gives a crash or an unreachable server. To an operator it is
 * the same event as the other gate, so it is drawn in the same family.
 */
export type GateTone = 'normal' | 'borderline' | 'critical';

export function gateToneFor(state: GateState): GateTone {
  switch (state) {
    case 'blocked':
      return 'critical';
    case 'overridden':
      // Let through, with items still uncovered, by a named person who gave a reason. Not
      // reassuring and not an alarm: the valve worked, and the record says so.
      return 'borderline';
    default:
      return 'normal';
  }
}

/**
 * The two ways back, and there is no third.
 *
 * A checklist somebody opened and left half-walked is **resumed**: the session exists, its ticks
 * are somebody's, and opening a second one is how two people each cover half of a list and each
 * believe the other did the rest. A checklist nobody ever opened is **started** — a different
 * room, a different sentence, and a different person. "Go back and finish it", said to an
 * operator whose colleague never started, is an instruction that cannot be followed.
 *
 * The server draws the line and this only reads it. `session_id` is absent on an item whose
 * checklist nobody opened; there is nothing here that infers the difference from a tick count or
 * from an empty list, because those are the same two answers the gate itself refuses to conflate.
 */
export const REMEDIES = ['resume', 'start'] as const;
export type Remedy = (typeof REMEDIES)[number];

export function remedyFor(item: CounselingMissingItem): Remedy {
  return (item.session_id ?? '').trim() === '' ? 'start' : 'resume';
}

/** One item the patient is being held for, as the screen draws it. */
export interface MissingRow {
  item: CounselingMissingItem;
  code: string;
  /** The item's own words, in the reader's language where there are any. */
  text: Wording;
  room: string;
  templateId: string;
  /** The session to go back into. Empty when nobody opened this checklist — see `remedyFor`. */
  sessionId: string;
  remedy: Remedy;
}

export function missingRowOf(item: CounselingMissingItem, locale: Locale): MissingRow {
  return {
    item,
    code: item.item_code,
    text: wordingOf(item.text_en, item.text_bn, locale),
    room: item.room,
    templateId: item.template_id,
    sessionId: (item.session_id ?? '').trim(),
    remedy: remedyFor(item),
  };
}

/**
 * The checklist's name, from the row itself.
 *
 * `titleOf` is the same three-way fallback the session and the visit's checklist answer use, so
 * the same list cannot read as "Diabetes counselling" in one place and `DIABETES` in another.
 * The gate carries its own `title_en`/`title_bn` now, which is why this takes no second source:
 * merging the visit's checklist answer in bought exactly this string, and a phone holding only a
 * refusal — which is the situation this panel exists for — would not have had one.
 *
 * The code is the fallback and not a blank. Both title fields can come back empty for a template
 * whose words were never filled in, and a nameless checklist on a blocked screen is a route
 * nobody can follow.
 */
function missingChecklistTitle(item: CounselingMissingItem, locale: Locale): string {
  return titleOf(item.title_en ?? '', item.title_bn ?? '', item.template_code, locale);
}

/**
 * One checklist's worth of what is missing, inside one room.
 *
 * The route back belongs **here** and not on the item, and that is the whole reason this layer
 * exists. A checklist nobody opened has every one of its mandatory items outstanding — seven for
 * the seeded diabetes list — and a control per item would be seven identical buttons that all
 * open the same session. One remediation is one control: this is the thing a person does, and
 * the items above it are what they will find when they get there.
 */
export interface MissingChecklistGroup {
  templateId: string;
  templateCode: string;
  /** The checklist's own name in the reader's language, or its code before nothing at all. */
  checklist: string;
  /** The session to go back into. Empty when nobody opened it — see `remedyFor`. */
  sessionId: string;
  remedy: Remedy;
  rows: readonly MissingRow[];
}

/**
 * One room's worth of what is missing.
 *
 * The same shape as the checklist's own `RoomGroup`, minus the marking. `RoomGroup.yours` comes
 * from `room_station` on a session's item, and the gate's missing items do not carry it — so
 * there is nothing here to say which of the rooms holding this patient is the operator's own.
 * Rather than fetch the room catalogue for that one boolean, which is exactly the second request
 * CP56 removed on the grounds that one boolean does not pay for it, the marking is simply absent.
 * Every room is drawn to everybody, which was always the important half.
 */
export interface MissingRoomGroup {
  room: string;
  /** The room's name in the reader's language, or its bare code before nothing at all. */
  heading: Wording;
  lists: readonly MissingChecklistGroup[];
}

/**
 * The room's name, from the row itself.
 *
 * `room_en`/`room_bn` travel with the missing item now — the same arrangement a session's frozen
 * items already have — so no catalogue is fetched to learn what `INSULIN_CORNER` reads as. Both
 * can be empty for a room the vocabulary no longer lists, which is a left join in the server's
 * own query rather than an error, so the bare code is the fallback: a blank heading would
 * silently merge two rooms into one list, and on this screen that is two rooms' worth of patients
 * sent to one of them.
 */
function roomNameOf(item: CounselingMissingItem, locale: Locale): Wording {
  const named = wordingOf(item.room_en ?? '', item.room_bn ?? '', locale);
  if (named.text !== '') return named;
  return { text: item.room, language: null, ownLanguage: false };
}

/**
 * What is missing, grouped under the rooms it is covered in.
 *
 * **There is no ordering in this function at all.** The rooms come out in the order they first
 * appear in the server's list, and the server sorts that list by `template_code`, then the room's
 * own configured sequence, then the item code — so a visit held by one checklist reads in §5.2's
 * corridor order without this file holding an opinion about it. The earlier version walked the
 * room catalogue to impose that order; the catalogue was a second request and the walk was a
 * second opinion, and both are gone.
 *
 * The one shape it does not order perfectly is a visit held by **two** checklists whose rooms
 * differ, because the server's outermost key is the template: a room only the second checklist
 * has follows the first checklist's rooms rather than merging into the corridor. `room_step` is
 * on the wire and this file deliberately does not read it, because reading it would mean sorting
 * — and a comparator here is how the guarantee that a session's items are never re-sorted rots.
 * Two checklists in different rooms read slightly out of walking order; that is the whole cost.
 *
 * Nothing is dropped and nothing is merged. An item whose room the vocabulary no longer lists is
 * still drawn, under its bare code, because a list shorter than its own count is a list somebody
 * stops trusting.
 */
export function missingRoomsOf(gate: CounselingGate, locale: Locale): MissingRoomGroup[] {
  // Built mutable and handed out readonly. The groups are assembled in one walk over the
  // server's list, which is what keeps this function from being able to reorder anything.
  const byRoom = new Map<string, (MissingChecklistGroup & { rows: MissingRow[] })[]>();
  const groups: (MissingRoomGroup & { lists: MissingChecklistGroup[] })[] = [];

  for (const item of gate.missing) {
    const row = missingRowOf(item, locale);
    let lists = byRoom.get(row.room);
    if (lists === undefined) {
      lists = [];
      byRoom.set(row.room, lists);
      groups.push({ room: row.room, heading: roomNameOf(item, locale), lists });
    }
    // Keyed on the session as well as the template, so a checklist that somehow arrived both
    // walked and unopened becomes two groups rather than one wearing whichever remedy came
    // first. The server's own query cannot produce that; a route that quietly picked one would
    // be the failure this layer exists to prevent, so it is made unreachable instead of trusted.
    const list = lists.find(
      (one) => one.templateId === row.templateId && one.sessionId === row.sessionId,
    );
    if (list === undefined) {
      lists.push({
        templateId: row.templateId,
        templateCode: item.template_code,
        checklist: missingChecklistTitle(item, locale),
        sessionId: row.sessionId,
        remedy: row.remedy,
        rows: [row],
      });
      continue;
    }
    list.rows.push(row);
  }

  return groups;
}

/**
 * The gate, as the screen reads it.
 *
 * Everything that decides what happens to the patient is the server's, mirrored and never
 * recomputed: `blocked` and `overridden` come off the answer untouched, and there is no argument
 * to this function through which a session, a tick or a progress figure could arrive to be
 * counted. The enforcement is a trigger on the queue table; this exists so a screen can say why
 * a patient cannot be sent on, and where to take them, instead of the operator discovering it as
 * a refusal after the patient has stood up.
 *
 * `missing` is a count of items still uncovered *now*, and it can be more than zero in the
 * `overridden` state — that is the ordinary shape of an override, and a screen that read a
 * non-empty list as "held" would contradict the server about a patient already sent on.
 */
export interface GateReading {
  state: GateState;
  /** The server's own answer to what the queue will do. Never derived from anything here. */
  blocked: boolean;
  /** The server's own answer to why it will not. Never the same sentence as `clear`. */
  overridden: boolean;
  /** Nothing outstanding, and nobody had to be let through. The one reassuring state. */
  clear: boolean;
  /** How many mandatory items are uncovered right now. */
  missing: number;
  rooms: readonly MissingRoomGroup[];
  /** The override standing on this visit, or null. Read only; nothing here grants one. */
  override: CounselingGateOverride | null;
  /** Message key for what the gate says, in words. */
  headline: string;
  /** Message key for what that means for this patient. */
  meaning: string;
  tone: GateTone;
}

/**
 * Who let this patient through, as one string, in the reader's language.
 *
 * The override's whole worth is that a **named person** took it, so the name comes first now
 * that the contract carries one. The role behind it is the fallback rather than the answer:
 * "let through by PHYSICIAN" is a sentence about a permission, and the question a reviewer asks
 * six months later is which physician.
 *
 * Every field below is optional on the wire, so this can still come back empty — an override
 * projected before the names were joined, or a staff row since removed. Empty is left empty and
 * the screen supplies the honest word for it; a placeholder invented here would be the one
 * attribution in this system naming somebody who did not do the thing.
 */
export function grantedBy(override: CounselingGateOverride, locale: Locale): string {
  const named = wordingOf(
    override.granted_by_name_en ?? '',
    override.granted_by_name_bn ?? '',
    locale,
  );
  if (named.text !== '') return named.text;
  // The staff code before the role: it identifies a person, and the role identifies a hat.
  const code = (override.granted_by_code ?? '').trim();
  if (code !== '') return code;
  return (override.granted_role ?? '').trim();
}

export function gateReadingOf(gate: CounselingGate, locale: Locale): GateReading {
  const state = gateStateOf(gate);
  return {
    state,
    blocked: gate.blocked,
    overridden: gate.overridden,
    clear: state === 'clear',
    missing: gate.missing.length,
    rooms: missingRoomsOf(gate, locale),
    override: gate.override ?? null,
    headline: `gate.headline.${state}`,
    meaning: `gate.meaning.${state}`,
    tone: gateToneFor(state),
  };
}
