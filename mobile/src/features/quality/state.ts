import type { components, operations } from '@dthcms/api-client';

/**
 * An operator's own quality record, as data (CP63, §4.3, ADR-0029).
 *
 * # Why every decision is in here and none of it is in the screen
 *
 * The same split as every other feature on this surface: a React Native component cannot be
 * rendered in the container the tests run in, so anything it decided would be a decision
 * nobody checks. What this one decides is not layout. It decides **how a number about a
 * colleague's work reads to the colleague it is about**, at the end of a shift, on a
 * five-inch screen. The plan states the risk in its own words — *a metric that feels punitive
 * damages data honesty; staff hide errors instead of correcting them* — and on a screen that
 * small, tone is almost entirely typography and ordering. Ordering is data, so it is here.
 *
 * # A count cannot be produced without its denominator, and now every count has its own
 *
 * `Counted` is the only shape this module will hand a screen for anything that was counted
 * about a person, and the only way to obtain one is `counted(count, outOf)`, which answers
 * `null` when there is no denominator to put beside it.
 *
 * The denominators are no longer all the same number, and that is the substance of this
 * revision. `by_code` and `by_hour` each arrive with an `entries` of their own: how many of
 * *that* measurement the operator took, how many values they entered in *that* hour. Against
 * the shared correction total, "three corrections at four in the afternoon" was a fact about
 * the rota wearing a person's name — an operator who works only the late shift will always
 * cluster late. Against forty-seven values entered at four in the afternoon it becomes a
 * question somebody can answer. `PatternRow.basis` carries which denominator a row is against,
 * so the screen cannot label an hour's figure as though it were a share of the corrections.
 *
 * `by_reason` keeps the correction total, because a reason is not something anybody does a
 * number of: "transcription" has no denominator of its own, and its honest one is the
 * questions asked.
 *
 * # A missing rate says how many more values it needs
 *
 * The floor arrives on the payload as `rate_floor`, so `rateOf` reports the shortfall and the
 * screen can say *"eight more values this month and a rate will appear"*. That is arithmetic;
 * "too few" is a judgement about the same fact. Nothing here computes a rate — `rate` is
 * always a key now, `number | null`, and a genuine `0` is a number and is shown as one.
 *
 * # Four answers, and the operator's own two are adjacent
 *
 * `upheld` is a correction this operator applied. `overridden` is one a supervisor applied
 * instead. CP62 made those different events precisely so that a record would not read a
 * supervisor's fix as though the operator had put it right themselves, and until this revision
 * the count threw that distinction away again. They are now drawn apart.
 *
 * `OUTCOMES` puts `rejected` immediately after `upheld` — the operator's own two answers,
 * side by side, before anybody else's act — and `quality.test.ts` asserts the adjacency. An
 * operator who looked again and said the value stands did the job; folding that into a single
 * "corrections" total teaches them to accept every flag without looking, and the moment that
 * happens the physician's flag has stopped meaning anything.
 *
 * # An answered note stays on the record, and says what was decided
 *
 * `/v1/quality/me` returns the open notes — all of them, whatever window was asked for and
 * however long ago they were raised — and the ones answered recently. A note that appeared on
 * somebody's device and then silently vanished when a supervisor closed it would teach them
 * that things are decided about them out of sight — the same ambush the raise-time message
 * exists to prevent, arriving from the other end. So `notesOf` keeps both,
 * `FlagReading` carries who decided, when, and in their words why, and the screen draws the
 * two differently.
 *
 * # Nothing here counts anything the server did not count
 *
 * There is no arithmetic in this file that produces a figure about a person. `rateOf` reads
 * the server's rate or reports its absence; it never divides. `answeredOf` re-uses the
 * server's own counts. A second implementation of "how well is this operator doing" is exactly
 * the disagreement that ends with two numbers on two screens and a conversation about which
 * one is real.
 *
 * # No name in this module grades anybody
 *
 * There is no `errors`, no `mistakes`, no `score`, no `accuracy` and no `performance` — not as
 * an identifier, not as a message key, not as a `testID`. `quality.test.ts` walks this
 * module's exports, the screen's test identifiers and the message files and fails on such a
 * name, because the vocabulary is the feature: a screen that calls a corrected value an error
 * has already decided what it means, and it has decided it about a person.
 *
 * # The supervisor's half of CP63 is not here and must not be
 *
 * `/v1/quality/operators`, `/v1/quality/flags` and the resolve call all need
 * `quality.read.team` or `quality.flag.resolve`, which a station operator does not hold. There
 * is no function in this feature that takes an operator id, so a control for somebody else's
 * record has nothing to be wired to — and reviewing a colleague's month is done at a desk with
 * the correction chain open, not at a station between patients. This sentence is what somebody
 * would have to delete first.
 */

// --- what the contract gives us ---

export type QualityRecord = components['schemas']['QualityRecord'];
export type QualityFlag = components['schemas']['QualityFlag'];
export type QualityThreshold = components['schemas']['QualityThreshold'];
export type QualityWindow = components['schemas']['QualityWindow'];
export type QualityEvidence = components['schemas']['QualityEvidence'];
export type QualityReasonCount = components['schemas']['QualityReasonCount'];
export type QualityCodeCount = components['schemas']['QualityCodeCount'];
export type QualityHourCount = components['schemas']['QualityHourCount'];

/**
 * One threshold and the sentence describing it, as `/v1/quality/thresholds` returns the pair.
 *
 * Taken off the operation rather than declared, because the sentence is not a property of the
 * threshold row: the server renders it from the row's live numbers so that a threshold whose
 * count changes cannot leave a description of the old one behind. A type of this feature's own
 * would be a second opinion about a shape the contract already states.
 */
export type QualityRule =
  operations['listQualityThresholds']['responses'][200]['content']['application/json']['thresholds'][number];

export type Locale = 'en' | 'bn';

// --- the clinic's clock ---

/**
 * Dhaka, as a fixed offset.
 *
 * The fourth copy of this number in the application, and the duplication is deliberate rather
 * than tidy for the reason `features/corrections` records: a feature reaching into another
 * feature's internals for a constant is the coupling the lint rule exists to stop, and the
 * alternative — a shared clock module — is a change to three finished checkpoints.
 * `quality.test.ts` checks this against `Asia/Dhaka` exactly as the other three do, so the
 * four statements of the clinic's clock cannot drift apart quietly.
 */
export const CLINIC_UTC_OFFSET_MINUTES = 360;

/** `YYYY-MM-DD` in clinic time, or '' when the timestamp cannot be read. */
export function clinicDay(iso: string | null | undefined): string {
  const parsed = Date.parse(text(iso));
  if (Number.isNaN(parsed)) return '';
  return new Date(parsed + CLINIC_UTC_OFFSET_MINUTES * 60_000).toISOString().slice(0, 10);
}

/** `HH:00` in clinic time, for an hour the server has already resolved to the clinic's clock. */
export function clockHour(hour: number | null | undefined): string {
  if (typeof hour !== 'number' || !Number.isFinite(hour)) return '';
  const whole = Math.trunc(hour);
  if (whole < 0 || whole > 23) return '';
  return `${String(whole).padStart(2, '0')}:00`;
}

// --- the window this screen may ask for ---

/**
 * The contract's own bounds, and the window this screen asks for.
 *
 * `?days` outside 1–365 is now refused with a 422 rather than silently becoming thirty, which
 * is right — a client asking for a fortnight and being handed a month has two windows
 * pretending to be one. `windowDaysFor` is what makes it impossible for this application to be
 * the client that asks: every call goes through it, and there is no path from a screen to the
 * query string that does not.
 */
export const MIN_WINDOW_DAYS = 1;
export const MAX_WINDOW_DAYS = 365;

/**
 * Thirty, because it is the window the plan's own verification uses — *"three transcription
 * errors within 30 days"* — and because it is the window the seeded thresholds are written
 * against.
 *
 * There is no control to change it. A window picker on this screen would let somebody go
 * looking for the span that makes their figure smallest, or a supervisor go looking for the
 * one that makes it largest, and neither is a question this screen exists to answer.
 */
export const WINDOW_DAYS = 30;

/** A window the contract will accept, from anything a caller might hold. */
export function windowDaysFor(days: number | null | undefined): number {
  if (!whole(days)) return WINDOW_DAYS;
  const rounded = Math.trunc(days);
  if (rounded < MIN_WINDOW_DAYS) return MIN_WINDOW_DAYS;
  if (rounded > MAX_WINDOW_DAYS) return MAX_WINDOW_DAYS;
  return rounded;
}

// --- a count that cannot be shown alone ---

/**
 * The brand that makes `Counted` unforgeable by accident.
 *
 * Not exported. A screen cannot write `{ count: 3, outOf: 0 }` and pass it where a denominated
 * figure is wanted, because the type it would have to satisfy names a symbol it cannot reach.
 * The one cast that mints one is in `counted` below, four lines away from the rule it keeps.
 */
declare const DENOMINATED: unique symbol;

/**
 * A number about a person's work, with the number that makes it legible.
 *
 * There is no constructor for this but `counted`, and no field on it that a screen can read
 * without the other being present. That is the mechanism behind criterion 2 — *a count is
 * never rendered without its denominator* — expressed as a type rather than as a review note,
 * because a review note is what gets skipped on the afternoon somebody adds one more figure.
 */
export interface Counted {
  readonly count: number;
  readonly outOf: number;
  readonly [DENOMINATED]: true;
}

/**
 * One count and the number it came out of, or nothing at all.
 *
 * `null` when either half is missing or nonsensical — a negative count, a denominator smaller
 * than the count, a value the server did not send. The screen renders nothing rather than
 * rendering half of it: a figure that lost its denominator on the way to the glass is the
 * accusation this whole design is arranged to avoid, and showing it with a blank beside it is
 * worse than showing neither.
 *
 * A denominator of zero is allowed and a count of zero with it: "0 of 0 values you entered
 * were questioned" is a true and readable sentence on the morning somebody starts.
 */
export function counted(
  count: number | null | undefined,
  outOf: number | null | undefined,
): Counted | null {
  if (!whole(count) || !whole(outOf)) return null;
  if (count < 0 || outOf < 0) return null;
  if (count > outOf) return null;
  // The one place a Counted is minted. Everything above this line is the guarantee.
  return { count, outOf } as unknown as Counted;
}

function whole(value: number | null | undefined): value is number {
  return typeof value === 'number' && Number.isFinite(value);
}

// --- the work done, which is read first ---

/**
 * What the operator did in the window.
 *
 * A bare number, and the only one on the screen, because it is the denominator everything else
 * is shown against and has nothing of its own to be shown out of. Criterion: the first thing
 * read on this screen is the work, not the corrections — see `SECTIONS`.
 *
 * Derived values are excluded by the server, which is right: a BMI this operator never typed
 * would inflate the denominator of whoever pressed the button that computed it.
 */
export function workDone(record: QualityRecord | undefined): number {
  const entries = record?.entries;
  return whole(entries) && entries >= 0 ? entries : 0;
}

/**
 * How many of those entries a colleague asked about, out of how many there were.
 *
 * `null` when the server sent no denominator, which is the one case where this screen prefers
 * silence to a number.
 */
export function questionsOf(record: QualityRecord | undefined): Counted | null {
  if (record === undefined) return null;
  return counted(record.corrections, record.entries);
}

// --- how the questions were answered ---

/**
 * The four answers, in the order they are drawn.
 *
 * `upheld` and `rejected` are adjacent and first: they are the two answers the **operator**
 * gave, and `quality.test.ts` asserts that `rejected` immediately follows `upheld`. Somebody
 * who read the tape again and said the value stands is doing exactly what the correction
 * workflow needs them to keep doing, and a screen that put a supervisor's act between their
 * two would be separating the pair that belong together.
 *
 * `overridden` is third because it is somebody else's act on this operator's value. Counting
 * it into `upheld` — which is what the server did until this revision — threw away the only
 * distinction CP62 created a separate event for, and told an operator they had put right three
 * values when they had put right one.
 *
 * `open` last because it is the only one of the four that is a thing to go and do, and the
 * corrections queue is where it is done.
 */
export const OUTCOMES = ['upheld', 'rejected', 'overridden', 'open'] as const;
export type Outcome = (typeof OUTCOMES)[number];

export interface Answered {
  outcome: Outcome;
  /** Out of the questions asked, never out of the entries: these four partition that total. */
  figure: Counted;
}

/**
 * The four answers as denominated figures, or `[]` when nothing was asked.
 *
 * Every one is shown out of `corrections` rather than out of `entries`, because that is the
 * total they actually divide. Shown out of the entry count they would each look like a rate,
 * and four rates about one person on one screen is a scorecard.
 *
 * An answer the server could not put a denominator under is dropped rather than shown bare —
 * and `answeredOf` therefore returns a shorter list rather than a padded one, so the screen
 * cannot draw an empty slot where a figure was expected.
 */
export function answeredOf(record: QualityRecord | undefined): Answered[] {
  if (record === undefined) return [];
  const total = record.corrections;
  const rows: Answered[] = [];
  for (const outcome of OUTCOMES) {
    const figure = counted(record[outcome], total);
    if (figure !== null) rows.push({ outcome, figure });
  }
  return rows;
}

// --- the rate, and the arithmetic that stands where it is missing ---

/**
 * The rate, or how many more values it would take to have one.
 *
 * `known: false` carries the entry count, the server's floor and the shortfall between them,
 * so the screen can say *"eight more values this month and a rate will appear"*. That sentence
 * is arithmetic. "Too few" is a judgement about the identical fact, and a dash or a `0` in the
 * same place is worse than either — a dash reads as nil and a zero reads as a claim.
 */
export type Rate =
  | { known: true; perHundred: number }
  | { known: false; entries: number; floor: number; needed: number };

/**
 * What the server said, and nothing computed here.
 *
 * `rate` is always a key now and is `null` below the floor, so there is one absence to read
 * rather than two. The floor comes off the payload as `rate_floor`, which is what lets the
 * shortfall be named: this file does not know what the floor is and must not, or a clinic that
 * raised it would ship an app quietly promising the old one.
 *
 * `0` is a number and is shown as one. Reading it as "no rate" — the `!rate` this function is
 * written to avoid — would tell an operator with a clean month that their record could not be
 * worked out.
 */
export function rateOf(record: QualityRecord | undefined): Rate {
  const entries = workDone(record);
  const rate = record?.rate;
  if (whole(rate) && rate >= 0) return { known: true, perHundred: rate };
  const floor = whole(record?.rate_floor) && record.rate_floor >= 0 ? record.rate_floor : 0;
  return { known: false, entries, floor, needed: Math.max(0, floor - entries) };
}

// --- the notes on the record ---

/** What a supervisor decided, or that nobody has yet. */
export const NOTE_STATES = ['open', 'acknowledged', 'dismissed'] as const;
export type NoteState = (typeof NOTE_STATES)[number];

export interface EvidenceReading {
  id: string;
  /** Why the value was questioned, in the reader's language; the code itself when unnamed. */
  reason: string;
  /** What was being measured, in the reader's language; never what it measured. */
  measurement: string;
  /** The day the value was recorded, in clinic time. */
  day: string;
  /** `HH:00`, the hour the value was recorded on the clinic's wall clock. */
  hour: string;
}

export interface FlagReading {
  id: string;
  /** The server's own sentence for the pattern, in the reader's language. */
  threshold: string;
  /** What to do about it, in the server's words. A note with no suggested action is a complaint. */
  action: string;
  /** The threshold code, shown when the server sent no words for it, so a note is never wordless. */
  code: string;
  /** What was counted and out of how many, frozen when the note was written. Never one alone. */
  figure: Counted | null;
  /** Whether a clinician has signed off the numbers behind it. False on everything shipped. */
  approved: boolean;
  /** The day it was written, in clinic time, or '' when the record does not say. */
  raised: string;
  /** How many days the frozen window covered, or 0 when the record does not say. */
  windowDays: number;
  /** Open, or what was decided. */
  state: NoteState;
  /** Who decided, in the reader's language, falling back to their staff code, or ''. */
  decidedBy: string;
  /** The day it was decided, in clinic time, or ''. */
  decidedOn: string;
  /** Why, in the supervisor's own words, or ''. */
  reason: string;
  /**
   * The day every row of the evidence was frozen — the moment the note was written.
   *
   * One date for the whole set rather than one per row. `status_as_of` is the raise moment and
   * is identical on every row of a flag; the contract now says so and says to render it once.
   * Repeating it under each line implied it varied, which is the reading that would make
   * somebody check whether one row was fresher than another.
   */
  frozenOn: string;
  /** The corrections it was raised on: no patient and no value in any of them. */
  evidence: EvidenceReading[];
}

/**
 * One note on this operator's record, in the reader's language.
 *
 * The server's bilingual pairs are rendered rather than re-stated here. `threshold_en` /
 * `threshold_bn`, `action_en` / `action_bn` and now the evidence's `reason_*` and `code_*` all
 * come off rows a clinician or a registry owns, and a copy of those sentences in the mobile
 * message files would be a second, staler account of a vocabulary this application does not
 * own.
 *
 * `approved` is carried out to the screen deliberately and is drawn on the face of the note
 * rather than in a footnote. Every threshold this system ships with is a proposal with
 * `approved_at` null; a note raised on numbers nobody has agreed to, presented as though it
 * were policy, is the single most punitive thing this screen could do.
 *
 * The evidence's `status` is deliberately **not** carried out to a reading. It is what each
 * request was when the note was written — a request that was open then and has since been
 * answered still reads OPEN — which is the honest shape of a frozen record, but it is the one
 * coded value left on this payload with no `_en` / `_bn` pair beside it, and this screen does
 * not invent words for a server's vocabulary. What replaces it is `frozenOn` plus one sentence
 * saying the reading may have been answered since, which is the part an operator needs.
 *
 * `frozenOn` is one date for the whole set: `status_as_of` is the raise moment and is the same
 * on every row, so a copy under each line would imply it varied.
 */
export function flagReadingOf(flag: QualityFlag, locale: Locale): FlagReading {
  return {
    id: text(flag.id),
    threshold: wordingOf(flag.threshold_en, flag.threshold_bn, locale),
    action: wordingOf(flag.action_en, flag.action_bn, locale),
    code: text(flag.threshold_code),
    figure: counted(flag.observed_count, flag.entries_count),
    approved: flag.threshold_approved === true,
    raised: clinicDay(flag.raised_at),
    windowDays: whole(flag.window?.days) ? flag.window.days : 0,
    state: noteStateOf(flag),
    decidedBy:
      wordingOf(flag.resolved_by_name_en, flag.resolved_by_name_bn, locale) ||
      text(flag.resolved_by_code),
    decidedOn: clinicDay(flag.resolved_at),
    reason: text(flag.resolution),
    // The raise moment, taken from the first row because every row carries the same one, and
    // falling back to the raised day — which is what it is — when there is no evidence to read
    // it from.
    frozenOn: clinicDay(flag.evidence?.[0]?.status_as_of) || clinicDay(flag.raised_at),
    evidence: (flag.evidence ?? []).map((item) => ({
      id: text(item.request_id),
      reason: wordingOf(item.reason_en, item.reason_bn, locale) || text(item.reason_code),
      measurement: wordingOf(item.code_en, item.code_bn, locale) || text(item.code),
      day: clinicDay(item.at),
      hour: clockHour(item.hour),
    })),
  };
}

/** Open, acknowledged or dismissed — anything unrecognised reads as still open. */
export function noteStateOf(flag: { status?: string }): NoteState {
  const status = text(flag.status).toUpperCase();
  if (status === 'ACKNOWLEDGED') return 'acknowledged';
  if (status === 'DISMISSED') return 'dismissed';
  return 'open';
}

/**
 * The notes on this record: the ones still waiting first, then the ones somebody answered.
 *
 * Two groups rather than one list, because they are two different things to read. An open note
 * is a conversation that has not happened yet and the oldest is the one that has been waiting
 * longest, so those go oldest first. An answered note is news — what was decided, by whom —
 * and the freshest decision is the one worth reading, so those go newest first.
 *
 * Sorted here rather than left alone because `/v1/quality/me` returns them in the order the
 * supervisor's own query produces, which is right for a list somebody is triaging and wrong
 * for one person's own record.
 */
export function notesOf(record: QualityRecord | undefined, locale: Locale): FlagReading[] {
  const notes = (record?.flags ?? []).map((flag) => flagReadingOf(flag, locale));
  const open = notes
    .filter((note) => note.state === 'open')
    .sort((a, b) => a.raised.localeCompare(b.raised));
  const answered = notes
    .filter((note) => note.state !== 'open')
    .sort((a, b) => b.decidedOn.localeCompare(a.decidedOn));
  return [...open, ...answered];
}

/** Whether anything on this record is still waiting for a supervisor to talk to this operator. */
export function hasOpenNote(record: QualityRecord | undefined): boolean {
  return (record?.flags ?? []).some((flag) => noteStateOf(flag) === 'open');
}

/**
 * How long an answered note keeps appearing, as the server says.
 *
 * `0` when the server sent no number, and the screen then says nothing rather than guessing —
 * a wrong promise about how long somebody's record keeps a decision is worse than no promise.
 */
export function retentionOf(record: QualityRecord | undefined): number {
  const days = record?.answered_flags_kept_days;
  return whole(days) && days > 0 ? Math.trunc(days) : 0;
}

/**
 * How recent a decision has to be for the shell to mention it: one day.
 *
 * Long enough that somebody who was off shift when their note was answered still meets the
 * line when they next pick up a tablet. Short enough that it is not a banner they learn to
 * scroll past — and the record itself keeps the answered note either way, so nothing is lost
 * when the line goes.
 */
export const RECENT_DECISION_MS = 24 * 60 * 60_000;

/** What, if anything, the shell's own line should say about this record. */
export const NOTICES = ['raised', 'answered', 'none'] as const;
export type Notice = (typeof NOTICES)[number];

/**
 * Whether there is something on this record the operator has not been standing in front of.
 *
 * An open note always. A decision, for a day after it was taken — and "now" is the record's
 * own `window.to`, which is the server's clock, rather than this tablet's. A station tablet
 * with a wrong clock would otherwise either nag forever or never speak, and neither failure
 * would look like a clock.
 *
 * `raised` wins over `answered` when both are true: a note still waiting is the live one.
 */
export function noticeOf(record: QualityRecord | undefined): Notice {
  if (record === undefined) return 'none';
  if (hasOpenNote(record)) return 'raised';
  const now = Date.parse(text(record.window?.to));
  if (Number.isNaN(now)) return 'none';
  for (const flag of record.flags ?? []) {
    const decided = Date.parse(text(flag.resolved_at));
    if (Number.isNaN(decided)) continue;
    if (now - decided <= RECENT_DECISION_MS) return 'answered';
  }
  return 'none';
}

// --- where the questions fell, each against its own denominator ---

/**
 * Which denominator a row is counted against.
 *
 * On the row rather than known only by the caller, so a screen cannot label an hour's figure
 * as a share of the corrections. The three are genuinely different questions, and the sentence
 * beside the number has to match the one being answered.
 */
export const BASES = ['questions', 'measurements', 'hour'] as const;
export type Basis = (typeof BASES)[number];

export interface PatternRow {
  /** What to draw. Already in the reader's language: every grouping now sends a pair. */
  label: string;
  figure: Counted;
  basis: Basis;
}

/**
 * Why values were questioned, in the server's words.
 *
 * The one grouping counted against the questions asked rather than against a denominator of
 * its own, and rightly: "transcription" is not something anybody does a number of, so its
 * honest denominator is how many questions there were.
 *
 * `transcription` on the row is deliberately **not** drawn. It is a property of a taxonomy
 * rather than a fact about a colleague, and printing "this counts as a transcription error"
 * beside somebody's own record is the metric the plan's risk note warns about, wearing a label.
 */
export function byReasonOf(record: QualityRecord | undefined, locale: Locale): PatternRow[] {
  return rowsOf(record?.by_reason, 'questions', (row) => ({
    label: wordingOf(row.display_en, row.display_bn, locale) || text(row.reason_code),
    value: row.corrections,
    outOf: record?.corrections,
  }));
}

/**
 * Which measurement was questioned, against how many of that measurement the operator took.
 *
 * Three corrections on a weight is a different fact when somebody weighed four hundred people
 * and when they weighed nine, and until the server sent `entries` on this row there was no way
 * to draw the difference. The name now arrives as a pair from the observation registry, so a
 * Bangla reader is no longer handed `BODY_WEIGHT`; the raw code is still the fallback, exactly
 * as CP62 shows a reason code this build has never met.
 */
export function byCodeOf(record: QualityRecord | undefined, locale: Locale): PatternRow[] {
  return rowsOf(record?.by_code, 'measurements', (row) => ({
    label: wordingOf(row.display_en, row.display_bn, locale) || text(row.code),
    value: row.corrections,
    outOf: row.entries,
  }));
}

/**
 * What time of day the questioned values were **recorded** — not when they were questioned.
 *
 * That distinction is the server's and it matters: a physician reviewing yesterday's file at
 * nine in the morning would otherwise make every operator on the rota look like a morning
 * problem. The hour arrives already on the clinic's wall clock.
 *
 * Each hour is counted against its own entries. Without that this row is unfair by
 * construction — somebody who works only the late shift will always cluster late, and the
 * end-of-shift threshold would be reading a rota as a person.
 */
export function byHourOf(record: QualityRecord | undefined): PatternRow[] {
  return rowsOf(record?.by_hour, 'hour', (row) => ({
    label: clockHour(row.hour),
    value: row.corrections,
    outOf: row.entries,
  }));
}

function rowsOf<T>(
  source: readonly T[] | undefined,
  basis: Basis,
  read: (row: T) => { label: string; value: number; outOf: number | undefined },
): PatternRow[] {
  const rows: PatternRow[] = [];
  for (const row of source ?? []) {
    const { label, value, outOf } = read(row);
    const figure = counted(value, outOf);
    // No label and no denominator are each enough to drop the row. A bar with nothing beside
    // it, or a figure with nothing under it, invites the reader to supply the missing half.
    if (label === '' || figure === null) continue;
    rows.push({ label, figure, basis });
  }
  return rows;
}

// --- the window ---

export interface WindowReading {
  days: number;
  from: string;
  to: string;
  known: boolean;
}

/**
 * The period this record covers.
 *
 * Always drawn, always at the top, and never left implicit. "Three corrections" over a
 * fortnight and over a year are different facts in the same way that three out of forty and
 * three out of four hundred are, and the window is the second denominator on this screen.
 *
 * A note's own span is separate and is frozen: these counts follow the window that was asked
 * for, a note's counts follow the threshold's. Both are labelled with their own span rather
 * than one being assumed from the other — see `FlagReading.windowDays`.
 *
 * What this window no longer does is hide anything. An open note is returned whatever span is
 * asked for, so a fortnight's record still carries a note raised two months ago that nobody
 * has answered. That is the behaviour this screen wants and there is nothing here to
 * compensate for: a note disappearing because somebody narrowed a window would be the same
 * silent vanishing the answered ones are kept to prevent.
 */
export function windowOf(record: QualityRecord | undefined): WindowReading {
  const window = record?.window;
  const days = whole(window?.days) ? window.days : 0;
  const from = clinicDay(window?.from);
  const to = clinicDay(window?.to);
  return { days, from, to, known: days > 0 && from !== '' && to !== '' };
}

// --- what puts a note on a record, before one is there ---

export interface ThresholdReading {
  code: string;
  /** The server's sentence for the pattern, in the reader's language. */
  display: string;
  /** What it looks for, in the server's words and the reader's numerals. */
  looksFor: string;
  /** What a supervisor is asked to do about it, in the server's words. */
  action: string;
  /** False on everything shipped. Drawn on the face of the row, never in a footnote. */
  approved: boolean;
}

/**
 * The three patterns, as an operator may read them before one is ever raised about them.
 *
 * `/v1/quality/thresholds` needs a session and no permission, for the reason the contract
 * states: somebody who can see a note on their own record should be able to read what raised
 * it without asking a supervisor to explain. Reading it *before* is better still — a rule
 * somebody knows in advance is not an ambush, and the whole defence against the plan's stated
 * risk is that nothing here arrives as a surprise.
 *
 * `looks_for_en` / `looks_for_bn` is rendered rather than rebuilt from the row's numbers here.
 * The server renders that sentence from the live row precisely so that a threshold whose count
 * changes cannot leave a description of the old one behind, and a client that composed its own
 * would put the staleness straight back — in two languages instead of one.
 */
export function thresholdReadingsOf(
  rules: readonly QualityRule[] | undefined,
  locale: Locale,
): ThresholdReading[] {
  return (rules ?? []).map((rule) => ({
    code: text(rule.threshold?.code),
    display: wordingOf(rule.threshold?.display_en, rule.threshold?.display_bn, locale),
    looksFor: wordingOf(rule.looks_for_en, rule.looks_for_bn, locale),
    action: wordingOf(rule.threshold?.action_en, rule.threshold?.action_bn, locale),
    approved: rule.threshold?.approved === true,
  }));
}

// --- the order the screen is read in ---

/**
 * The sections, top to bottom.
 *
 * This is the whole of criterion "order the screen so the first thing read is the work done".
 * It is a constant with a test on it rather than the order somebody happened to type the JSX
 * in, because the ordering *is* the tone: a screen that opens with "3 corrections" and reaches
 * "412 values entered" halfway down has told the operator what it thinks of them before they
 * have read a word.
 *
 *   `work`      — what they did. The first thing, always, and the only bare number.
 *   `questions` — how many of those a colleague asked about, out of that same total.
 *   `answered`  — the four answers. `rejected` beside `upheld`, never inside a total.
 *   `notes`     — the flags, open ones first, each carrying its denominator and its state.
 *   `patterns`  — where the questions fell: reason, measurement, hour, each against its own.
 *   `rules`     — what would put a note here, readable before one ever is.
 *
 * `notes` sits above `patterns` and below `answered` on purpose. It must not be the first
 * thing read, and it must not be at the bottom either: a note somebody has to scroll to is a
 * note they hear about from their supervisor first, which is the ambush this checkpoint is
 * arranged to prevent. The shell's own line is what makes sure it is never missed.
 */
export const SECTIONS = ['work', 'questions', 'answered', 'notes', 'patterns', 'rules'] as const;
export type Section = (typeof SECTIONS)[number];

// --- being told, on the device of the person it is about (ADR-0029) ---

/**
 * The two messages the gateway publishes about somebody's own record.
 *
 * Named here rather than matched on, because the shared invalidation map in
 * `@dthcms/api-client` now turns any `quality.*` message into the `['quality']` key — on the
 * socket and on a gap — so this feature no longer carries a listener of its own. What it keeps
 * is the subscription (`myTopic`) and a test asserting that both of these actually reach that
 * key: the map is in another package, and the failure it would otherwise produce is a record
 * that sits stale on the one device the note is about.
 *
 * **Neither message carries a count.** The bridge publishes the flag id, the threshold code
 * and the resolved status, and deliberately nothing else — no observed count, no denominator,
 * no measurement. The re-read goes back through the endpoint, which is what puts it back
 * through the session check that decides whether this reader may see it at all.
 */
export const QUALITY_FLAG_RAISED = 'quality.flag_raised';
export const QUALITY_FLAG_RESOLVED = 'quality.flag_resolved';
export const QUALITY_KINDS = [QUALITY_FLAG_RAISED, QUALITY_FLAG_RESOLVED] as const;

/** The operator's own channel, or '' when the session has not answered yet. */
export function myTopic(me: string): string {
  const id = text(me);
  return id === '' ? '' : `user:${id}`;
}

// --- what went wrong, when the read does not come back ---

export const PROBLEMS = ['unreachable', 'refused', 'failed'] as const;
export type Problem = (typeof PROBLEMS)[number];

/**
 * A failed read, in the three shapes this screen knows how to say.
 *
 * Narrower than CP62's equivalent because this screen only reads: there is no write on it, no
 * idempotency to preserve and no half-finished act to recover. A refusal is shown in the
 * server's own words, for the reason recorded there — the rules behind it are the server's,
 * and a client that paraphrased them would be inventing a second, staler account of them.
 *
 * There is no `notThisRole` case and there must not be one. `/v1/quality/me` requires a session
 * and no permission at all, deliberately (ADR-0029 §3): an operator who has to be granted
 * something before they may see their own record is an operator who will assume the record is
 * being kept from them. A 403 here is a defect in the server, not a hat the operator is
 * wearing, and it is reported as a plain refusal rather than explained away as one.
 *
 * A 422 lands here too. It can only mean this application asked for a window the contract
 * refuses, which `windowDaysFor` exists to make impossible — so it is a refusal to report to
 * somebody who can fix the build, not something to ask an operator to do differently.
 */
export interface Trouble {
  kind: Problem;
  status: number;
  /** The server's own sentence, in the reader's language, or '' when it sent none. */
  message: string;
}

/**
 * What the operator can do about it. One of these, never a stack trace.
 *
 * `troubleOf` — which turns a thrown error into one of the shapes above — lives in `api.ts`
 * beside the calls that throw, exactly as CP62's does: it is the one function in the feature
 * that has to know what an `ApiError` is.
 */
export const ADVICE = ['waitForLink', 'tellSomebody', 'tryAgain'] as const;
export type Advice = (typeof ADVICE)[number];

export function adviceFor(trouble: Trouble): Advice {
  if (trouble.kind === 'unreachable') return 'waitForLink';
  if (trouble.kind === 'refused') return 'tellSomebody';
  return 'tryAgain';
}

// --- small shared helpers ---

function text(value: string | null | undefined): string {
  return (value ?? '').trim();
}

/** A pair of translations, in the reader's language, falling back to the other. */
function wordingOf(
  en: string | null | undefined,
  bn: string | null | undefined,
  locale: Locale,
): string {
  const own = text(locale === 'bn' ? bn : en);
  if (own !== '') return own;
  return text(locale === 'bn' ? en : bn);
}
