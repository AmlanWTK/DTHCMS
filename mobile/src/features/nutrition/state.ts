import { sha1 } from '@noble/hashes/sha1';

import type { components } from '@dthcms/api-client';

/**
 * Station 7's 24-hour recall, as data (CP59, blueprint §5.2, §12.1).
 *
 * # Why every decision is in here and none of it is in the screen
 *
 * The same rule stations 2, 3, 4 and 5 follow: a React Native component cannot be rendered
 * outside a device, so anything it decides is a decision nobody checks. What this station
 * decides is not layout. It decides which of two answers is on screen when both come back out
 * of order, which measures an operator may be offered for a food, what a client may send, and
 * what it may never compute — and all four are pure functions with tests beside them.
 *
 * # Nothing in this file adds up a calorie
 *
 * `read.diet_entry` carries the energy per entry and `DietTotals` carries the day's, both
 * computed by the server from the food composition table. §12.1's diet–outcome analysis reads
 * those. A client that could produce a total could produce **any** total, and a second
 * implementation of the arithmetic is how a tablet shows one figure while the research extract
 * holds another.
 *
 * So: `toEntry` sends the food, the measure and the count and never a weight or an energy;
 * `totalsOf` copies `recall.totals` across and does not touch it; there is no `reduce` in this
 * feature and no multiplication of a portion by a composition figure anywhere in it.
 *
 * # A measure the table cannot weigh is never offered
 *
 * `core.food_grams` returns nothing for a measure a food has no portion row for, and the write
 * is refused with a 422 naming `measure_code` rather than guessing — a guessed weight becomes a
 * calorie count somebody acts on. `measuresFor` is the other end of that rule: the only measures
 * it returns are the ones on the food's own `portions` array, plus every measure the server has
 * marked `universal`.
 *
 * **Grams is the escape hatch and it is found by the flag, never by the code.** No food in the
 * table has a grams portion row, so grams can only come from the measures list — and it is
 * picked out by `universal`, which is the column the migration added for exactly this ("the flag
 * exists so a screen can tell an operator which measures it can offer for a food it has no
 * portion row for"). A screen that matched on the literal `GRAM` would be a screen holding its
 * own copy of which measure is the universal one.
 *
 * # The day being recalled comes from the server and goes back on every write
 *
 * `recall_date` defaults to yesterday **server-side**, and this file never computes that
 * default. What it does is keep the date the server answered with and send it explicitly on
 * every entry, so a recall that a clinic session started before midnight cannot split itself
 * across two days halfway through, and so two operators on two tablets cannot be filing the
 * same conversation under two dates.
 *
 * `dayRelation` exists only to *check* that date against the clinic's own calendar, because the
 * server's "yesterday" is computed in UTC and the clinic is six hours ahead of it. A recall
 * dated today is the mistake worth shouting about: it makes every figure a day out.
 *
 * # Two operators, and nothing to merge
 *
 * Each entry is its own row, written once by one named person and never edited, so two people
 * adding food to one recall cannot collide. The one real collision is the **duplicate**, and it
 * is prevented by being visible: every write returns the whole day, `mayWithdraw` is true on
 * every standing entry whoever recorded it, and `totalsOf().shared` says out loud that two
 * people built this recall.
 */

// --- what the contract gives us ---

export type Food = components['schemas']['Food'];
export type FoodPortion = components['schemas']['FoodPortion'];
export type FoodMeasure = components['schemas']['FoodMeasure'];
export type DietEntry = components['schemas']['DietEntry'];
export type DietRecall = components['schemas']['DietRecall'];
export type DietTotals = components['schemas']['DietTotals'];

/**
 * One of the day's meals, **with its name**, from `core.meal`.
 *
 * It was a bare enum code for one checkpoint, which meant every client had to invent the Bangla
 * for MID_MORNING and BEDTIME — and web and mobile would have invented different words for the
 * same meal. It is a bilingual reference row now, like the household measures beside it, and this
 * file holds no meal name in either language.
 */
export type Meal = components['schemas']['Meal'];

/** A meal as the record spells it. The code, never the words. */
export type MealCode = Meal['code'];

/** One day this patient has a recall for, and how much is on it. */
export type RecallDay = { date: string; entries: number };

/**
 * Everything `GET /v1/foods/measures` answers with: the vocabulary and the clinic's own calendar.
 *
 * Fetched once per clinic session and worked from for the rest of the morning (ADR-0004). Three of
 * these four exist because the first version of this screen had to invent them:
 *
 *  - `meals` carried no names, so a client wrote its own Bangla;
 *  - `recallDateDefault` and `clinicToday` were computed from the tablet's clock, and the server's
 *    own default was a **UTC** yesterday, which between midnight and six in the morning local is a
 *    different day from the clinic's.
 *
 * **`clinicToday` here is only good for the first render.** This payload is held for a whole
 * clinic session, so a session crossing midnight would be checking a date against yesterday's idea
 * of today and would quietly stop warning. Every recall answer carries its own, and that is the
 * one to keep.
 *
 * Nothing in this file decides any of the four, and `emptyReference` is what a screen holds before
 * the answer arrives.
 */
export interface Reference {
  measures: FoodMeasure[];
  meals: Meal[];
  /** The day this clinic thinks a recall taken now is about, on its own calendar. */
  recallDateDefault: string;
  /** The clinic's own today at the moment this was fetched. Superseded by every recall answer. */
  clinicToday: string;
}

export function emptyReference(): Reference {
  return { measures: [], meals: [], recallDateDefault: '', clinicToday: '' };
}

/**
 * A recall, and the clinic's own today at the moment it was read.
 *
 * The date travels with **every** answer — the read, the write and the withdrawal — rather than
 * only on the reference payload, because that one is fetched once and a clinic session outlives
 * it. This is the pair every call in `api.ts` hands back for that reason.
 */
export interface RecallAnswer {
  recall: DietRecall;
  clinicToday: string;
}

/** The interface language. Local rather than imported: this file must stay renderer-free. */
export type Locale = 'en' | 'bn';

// --- the words on a row, in the reader's language ---

/**
 * One piece of the server's text, and which language it turned out to be in.
 *
 * The database refuses a food, a measure or a portion whose text is missing either language
 * (`food_bilingual`, `food_measure_bilingual`), so a missing translation is an edge case rather
 * than a shape to design around. It is still worth one honest line: a Bangla-reading
 * nutritionist handed English with no explanation is one who thinks the app has switched
 * languages on them, and who may then read a food name aloud wrongly rather than admit they
 * could not read it.
 *
 * Deliberately its own copy rather than an import from the lifestyle feature. A feature that
 * reached into another's internals for a ten-line helper would couple two stations so that
 * neither could change its mind, and the boundary rule exists for exactly that.
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

  return { text: '', language: null, ownLanguage: false };
}

// --- what went wrong, and what the operator can do about it ---

/** Which call a trouble belongs to. Context for the sentence, not a branch. */
export const ATTEMPTS = ['foods', 'measures', 'recall', 'entry', 'withdrawal'] as const;
export type Attempt = (typeof ATTEMPTS)[number];

/**
 * A refusal, an unreachable server, and a server that answered with something else.
 *
 * The same three shapes stations 3, 4 and 5 use, with the status, the server's own code and the
 * field it named kept: what an operator should do next depends on which refusal it was, and a
 * message alone cannot say.
 */
export interface Trouble {
  kind: 'refused' | 'unreachable' | 'failed';
  attempt: Attempt;
  /** The HTTP status, or 0 when there was no answer at all. */
  status: number;
  /**
   * The server's own code for the refusal, or empty.
   *
   * The only part of an error a client may branch on. A message is written for a person: it
   * gets translated, shortened and improved, and a screen that matched on its words would
   * change behaviour the day somebody rewrote a sentence.
   */
  code: string;
  /** The field the server named on a 422, or empty. */
  field: string;
  message: string;
}

/**
 * The refusal this station exists to handle gracefully.
 *
 * `POST /v1/diet` answers 422 on `measure_code` when the table has no portion row for that
 * measure of that food. It should be unreachable from this screen — `measuresFor` offers only
 * measures the table can weigh, and `toEntry` refuses to build a body for any other — so
 * meeting it means the tablet's copy of the food is older than the table, and the answer is
 * always the same one: record it in grams, which is the measure that always works.
 *
 * Matched on the field the server named rather than on a code, because the handler answers with
 * the generic validation code and names `measure_code`. Matching on the sentence would break
 * the day somebody improved the wording.
 */
export function cannotWeigh(trouble: Trouble): boolean {
  return trouble.status === 422 && trouble.field === 'measure_code';
}

/**
 * The other operator got there first.
 *
 * Two assistants who both notice the same duplicate rice will both press "take back", and the
 * second one is answered 409. That is not a failure worth a red banner — the recall is now
 * exactly what both of them wanted — so the advice is to read the day again rather than to try
 * again, which would fail identically for ever.
 */
export const CODE_ALREADY_WITHDRAWN = 'DIET_ENTRY_ALREADY_WITHDRAWN';

export function alreadyWithdrawn(trouble: Trouble): boolean {
  return trouble.code === CODE_ALREADY_WITHDRAWN || trouble.status === 409;
}

/**
 * What the operator can actually do about it.
 *
 * Four answers and no fifth, because a screen that offered "try again" for every failure would
 * train people to press it at the failures where pressing it cannot help.
 */
export const ADVICE = ['refresh', 'retry', 'grams', 'none'] as const;
export type Advice = (typeof ADVICE)[number];

export function adviceFor(trouble: Trouble): Advice {
  if (trouble.kind === 'unreachable') return 'retry';

  // The table cannot weigh that measure. Retrying sends the same measure; the way on is the
  // escape hatch, and the screen puts it under the operator's thumb.
  if (cannotWeigh(trouble)) return 'grams';

  // Somebody else has already taken that entry back, or the entry is not there at all. Both are
  // answered by reading the day again — which is the same act that shows what they did.
  if (alreadyWithdrawn(trouble)) return 'refresh';
  if (trouble.status === 404) return 'refresh';

  // The hat being worn does not hold `observation.write.nutrition`. A different hat or a
  // different person; nothing on this screen.
  if (trouble.status === 403) return 'none';

  // Any other refusal is about what was sent, and the server has said which field. Retrying an
  // unchanged request would fail identically.
  if (trouble.status === 422) return 'none';

  return 'retry';
}

/** The sentence above the server's own words. */
export function troubleKey(trouble: Trouble): string {
  // Each of these gets its own sentence, because the generic one — "the server would not accept
  // that" — reads as the operator having done something wrong, and neither of them is that.
  if (cannotWeigh(trouble)) return 'trouble.cannotWeigh';
  if (alreadyWithdrawn(trouble)) return 'trouble.alreadyWithdrawn';
  return `trouble.${trouble.kind}`;
}

// --- the food picker ---

/**
 * The server's own ceiling, mirrored.
 *
 * `GET /v1/foods` defaults to 25 and caps at 100. This asks for 25 and trims to 25 again on the
 * way out, for the reason station 4's picker does: the displayed list is a decision about
 * reading, and a picker that silently became scrollable to a hundred rows is a picker where the
 * twenty-first food is chosen by whoever scrolls furthest.
 */
export const MAX_RESULTS = 25;

/**
 * How still the box must be before a request goes.
 *
 * The whole recall has four minutes and most of that is this picker, so the budget is small: a
 * debounce long enough to be felt would spend more than the typing does. 180 ms sits above the
 * gap between two keystrokes of ordinary typing — so "rut" is one request rather than three on
 * a link that may be a shared 3G connection — and below the ~250 ms at which a person starts to
 * experience a list as lagging behind their fingers.
 *
 * The same number station 4's terminology picker uses, and deliberately its own constant rather
 * than an import: the two pickers search different things over different indexes, and the day
 * one of them needs to change its mind it must be able to without moving the other.
 */
export const DEBOUNCE_MS = 180;

/** What one request needs. Exactly the arguments `api.ts` takes, so nothing is assembled twice. */
export interface FoodRequest {
  /** Increases by one per request, for ever. The whole staleness rule rests on this. */
  seq: number;
  q: string;
}

/** An answer to one request, carrying the sequence number of the request that produced it. */
export type FoodAnswer =
  { seq: number; ok: true; foods: readonly Food[] } | { seq: number; ok: false; trouble: Trouble };

export interface PickerState {
  /** Exactly what is in the box. Empty is a real query: it means the list from the top. */
  query: string;
  /** When the box last changed, on whatever clock `now` is read from. */
  changedAt: number;
  /** The query the newest request went out for. `null` before the first request. */
  issuedQuery: string | null;
  /** The sequence number of the newest request issued. Only ever increases. */
  issued: number;
  /** The sequence number of the newest answer applied. Only ever increases. */
  applied: number;
  foods: readonly Food[];
  trouble: Trouble | null;
}

/**
 * A picker as it opens.
 *
 * `issuedQuery` is null and the query is empty, which together mean "fetch the list now,
 * without waiting". `GET /v1/foods` with no `q` answers with the table from the top, so the
 * picker has food in it before anybody has typed — which is the difference between a station
 * that opens ready and one that opens as a blank box somebody has to guess at.
 */
export function openPicker(): PickerState {
  return {
    query: '',
    changedAt: 0,
    issuedQuery: null,
    issued: 0,
    applied: 0,
    foods: [],
    trouble: null,
  };
}

/** A keystroke. Nothing is fetched here; `searchDue` decides when the box has been still enough. */
export function typedQuery(state: PickerState, text: string, now: number): PickerState {
  if (text === state.query) return state;
  return { ...state, query: text, changedAt: now };
}

/**
 * Whether a request should go out at this instant.
 *
 * Three conditions, and the first is the one worth stating: a query identical to the one
 * already in flight or already answered is not re-sent. Deleting a letter and typing it back
 * costs nothing, which is what makes the debounce short enough to be invisible.
 *
 * The very first request — `issuedQuery` still null — skips the wait entirely. Opening the
 * picker is not typing, and 180 ms of blank list on open is 180 ms of an operator wondering
 * whether the tap registered.
 */
export function searchDue(state: PickerState, now: number): boolean {
  if (state.issuedQuery === null) return true;
  if (state.query === state.issuedQuery) return false;
  return now - state.changedAt >= DEBOUNCE_MS;
}

/**
 * Take the next sequence number and the request that carries it.
 *
 * Returns the state as well as the request because the two must not be able to come apart: a
 * caller cannot issue a request without recording that it did.
 */
export function issueSearch(
  state: PickerState,
  now: number,
): { state: PickerState; request: FoodRequest } {
  const seq = state.issued + 1;
  return {
    state: { ...state, issued: seq, issuedQuery: state.query, changedAt: now },
    request: { seq, q: state.query },
  };
}

/**
 * The one door.
 *
 * Every replacement of what is on screen goes through here, and the guard is the first line. An
 * answer no newer than the one already shown is dropped whole — foods, trouble and all — so a
 * slow answer for "ru" landing after a fast one for "ruti" cannot put the wrong list under the
 * operator's finger, and cannot half-undo the newer one by landing its error over it.
 *
 * That matters more here than in an ordinary autocomplete. The clinic's link drops for seconds
 * at a time (ADR-0004), so out-of-order answers are the normal case rather than the
 * pathological one, and the failure they cause is silent: the list looks like an answer to what
 * was typed.
 */
export function applyAnswer(state: PickerState, answer: FoodAnswer): PickerState {
  if (answer.seq <= state.applied) return state;
  if (!answer.ok) {
    return { ...state, applied: answer.seq, foods: [], trouble: answer.trouble };
  }
  return { ...state, applied: answer.seq, foods: answer.foods, trouble: null };
}

/** True while an answer is still owed. Derived, so it cannot disagree with the sequences. */
export function searching(state: PickerState): boolean {
  return state.issued > state.applied;
}

/** Ask again after a failure. Forgetting the issued query is what makes `searchDue` fire again. */
export function retrySearch(state: PickerState, now: number): PickerState {
  return { ...state, issuedQuery: null, changedAt: now, trouble: null };
}

// --- the measures a food may actually be recorded in ---

/**
 * One measure the operator may choose for the food in hand.
 *
 * `grams` is what **one** of them weighs, from the food's own portion row, and it is null for a
 * universal measure — grams weighs what it says, and a number here would be the tautology 1.
 */
export interface MeasureChoice {
  code: string;
  name: Wording;
  /** What one of them weighs, or null for a measure that is its own weight. */
  grams: number | null;
  /** How big — "one medium ruti", "a small teacup". Empty for a universal measure. */
  note: Wording;
  /** True only for a measure that means the same for every food: grams, the escape hatch. */
  universal: boolean;
  /**
   * The most of this measure one entry may carry, from the measure's own row.
   *
   * A hundred cups is nobody's lunch and two hundred grams is an ordinary plate of rice, so the
   * two limits are different — and the difference belongs to the measure. It was briefly a pair
   * of payload-level numbers this file chose between by reading `universal`, which was right only
   * until a second universal measure was seeded. Null when the row does not carry one.
   */
  maxQuantity: number | null;
}

/**
 * What this food can be recorded in — and nothing else.
 *
 * Two sources, in this order and no other:
 *
 *  - the food's own `portions`, in the order the server sent them (the migration's `ordering`,
 *    which puts the measure a patient is likeliest to use first). Never re-sorted here: the
 *    clinic can tune that ordering, and a screen that re-sorted would silently disagree with it;
 *  - then every measure the server marks `universal` that the food has no portion for — which
 *    today is grams, and which is the escape hatch that stops an unweighable measure being a
 *    dead end.
 *
 * A measure that is neither is **not offered**, because `core.food_grams` cannot weigh it and
 * the write would be refused. Offering it and letting the server say no would put a 422 in
 * front of a patient in a four-minute conversation, in place of a control that was simply never
 * there.
 *
 * The universal measures come from the measures list rather than from a literal `GRAM`, so the
 * day the clinic adds a second universal measure — millilitres, say — this screen offers it
 * with no change at all.
 */
export function measuresFor(
  food: Food | null | undefined,
  measures: readonly FoodMeasure[] | undefined,
  locale: Locale,
): MeasureChoice[] {
  if (food === null || food === undefined) return [];

  const out: MeasureChoice[] = [];
  const seen = new Set<string>();

  for (const portion of food.portions ?? []) {
    if (seen.has(portion.measure_code)) continue;
    seen.add(portion.measure_code);
    out.push({
      code: portion.measure_code,
      name: wordingOf(portion.measure_en, portion.measure_bn, locale),
      grams: portion.grams,
      note: wordingOf(portion.note_en ?? '', portion.note_bn ?? '', locale),
      universal: portion.universal,
      // The portion carries the limit where it has one; otherwise the measure's own row does.
      // They are the same column on the server, and the fallback is for a portion payload that
      // predates the field rather than for a disagreement.
      maxQuantity: limitOf(portion.max_quantity, measures, portion.measure_code),
    });
  }

  for (const measure of measures ?? []) {
    if (!measure.universal || seen.has(measure.code)) continue;
    seen.add(measure.code);
    out.push({
      code: measure.code,
      name: wordingOf(measure.name_en, measure.name_bn, locale),
      grams: null,
      note: { text: '', language: null, ownLanguage: false },
      universal: true,
      maxQuantity: limitOf(measure.max_quantity, measures, measure.code),
    });
  }

  return out;
}

/** A stated limit, or the measure row's, or nothing. A limit of nought is nothing stated. */
function limitOf(
  stated: number | undefined,
  measures: readonly FoodMeasure[] | undefined,
  code: string,
): number | null {
  const own = Number.isFinite(stated) && (stated ?? 0) > 0 ? (stated as number) : null;
  if (own !== null) return own;
  const row = (measures ?? []).find((measure) => measure.code === code);
  const fallback = row?.max_quantity;
  return Number.isFinite(fallback) && (fallback ?? 0) > 0 ? (fallback as number) : null;
}

/**
 * Whether the escape hatch is on offer.
 *
 * False only when the measures list has not arrived, which is the one state in which a food
 * with no portion row would have nothing to record it in at all. The screen says so rather than
 * showing an empty row of measures, because an operator who sees no measures assumes the food
 * cannot be recorded and moves on to something close enough.
 */
export function hasEscapeHatch(choices: readonly MeasureChoice[]): boolean {
  return choices.some((choice) => choice.universal);
}

/** One measure out of the offered set by code, or nothing. */
export function measureNamed(
  choices: readonly MeasureChoice[],
  code: string,
): MeasureChoice | null {
  const wanted = code.trim();
  if (wanted === '') return null;
  return choices.find((choice) => choice.code === wanted) ?? null;
}

// --- one food, as the picker draws it ---

export interface FoodRow {
  food: Food;
  code: string;
  name: Wording;
  group: Food['group_code'];
  /** What this food can be recorded in, decided once here rather than again on selection. */
  measures: MeasureChoice[];
  /** False on everything in the table today. Nobody has approved these figures. */
  approved: boolean;
  /** Where the figures came from, on every row, in the server's own words. */
  source: string;
}

/**
 * The results in the server's order, with **nothing removed and nothing re-ranked**.
 *
 * `GET /v1/foods` sorts prefix matches first and breaks ties on trigram similarity, over the
 * names *and* the synonyms — which is what makes "roti", "ruti" and "রুটি" one food. Re-sorting
 * any of that here would silently disagree with a ranking the clinic can tune, and would sink
 * the exact match somebody typed below a food that happens to sort earlier.
 */
export function foodRows(
  foods: readonly Food[] | undefined,
  measures: readonly FoodMeasure[] | undefined,
  locale: Locale,
): FoodRow[] {
  return visible(foods ?? []).map((food) => ({
    food,
    code: food.code,
    name: wordingOf(food.name_en, food.name_bn, locale),
    group: food.group_code,
    measures: measuresFor(food, measures, locale),
    approved: food.approved,
    source: (food.source ?? '').trim(),
  }));
}

/** The rows an operator will actually read: the first `MAX_RESULTS`, and no more. */
export function visible(foods: readonly Food[]): readonly Food[] {
  return foods.length <= MAX_RESULTS ? foods : foods.slice(0, MAX_RESULTS);
}

/**
 * How many of the foods on offer nobody has approved.
 *
 * The plan names the food composition table as a content dependency needing a national
 * institute's table or an authored one; `approved_at` is null on every seeded row and the API
 * reports it. This is what the standing notice counts, and today it counts all of them.
 *
 * Counted rather than assumed, so that the notice goes away by itself on the day a nutritionist
 * approves the table — and so that a half-approved table still says so.
 */
export function unapproved(foods: readonly Food[] | undefined): number {
  return (foods ?? []).filter((food) => !food.approved).length;
}

// --- the day being recalled ---

/**
 * How the day being recalled sits against the clinic's own calendar.
 *
 * Not a computation of what the date *should* be, and not a reading of the tablet's clock either.
 * `today` is `clinic_today` off the reference payload — the server's own calendar day, in the
 * clinic's zone. A tablet's date is whatever somebody set it to, and this check briefly ran
 * against a UTC midnight, which in Faridpur is six in the morning.
 *
 * `ahead` and `today` are the two that matter. A 24-hour recall dated today is a recall of food
 * the patient has not finished eating, and every figure derived from it is a day out.
 */
export const DAY_RELATIONS = ['yesterday', 'today', 'earlier', 'ahead'] as const;
export type DayRelation = (typeof DAY_RELATIONS)[number];

export function dayRelation(recallDate: string, today: string): DayRelation | null {
  const day = recallDate.trim();
  const now = today.trim();
  if (day === '' || now === '') return null;
  if (day === now) return 'today';
  if (day === shiftDay(now, -1)) return 'yesterday';
  return day < now ? 'earlier' : 'ahead';
}

/** True for a recall day an operator should look at twice before recording anything against it. */
export function unusualDay(relation: DayRelation | null): boolean {
  return relation === 'today' || relation === 'ahead';
}

/**
 * A calendar day, moved by one.
 *
 * The only date arithmetic left in this feature, and it is not the kind that was wrong: it never
 * decides *which* day a recall is about — the server says that, on the clinic's own calendar, and
 * `dayRelation` checks it against the server's `clinic_today`. This moves the day already on
 * screen to the one beside it, for the two step controls. There is no endpoint for "the day
 * before this one", and a round trip for a subtraction would be worse than the subtraction.
 *
 * Done through `Date.UTC` so it cannot be dragged by the tablet's own time zone: a station set to
 * UTC and one set to Asia/Dhaka must step from the same day to the same day, and a naive
 * `new Date('2026-03-01')` plus a day does not promise that.
 *
 * An unparseable date is returned unchanged rather than turned into an Invalid Date. A recall
 * headed "NaN-NaN-NaN" is worse than one headed with the string the server sent.
 */
export function shiftDay(iso: string, days: number): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso.trim());
  if (match === null) return iso;
  const moved = new Date(Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3]) + days));
  if (Number.isNaN(moved.getTime())) return iso;
  const year = String(moved.getUTCFullYear()).padStart(4, '0');
  const month = String(moved.getUTCMonth() + 1).padStart(2, '0');
  const day = String(moved.getUTCDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

/**
 * How often the day on screen is read again while nobody is typing.
 *
 * The whole duplicate-prevention mechanism is that each operator can see what the other one
 * recorded, and every write returns the whole day — but an operator who has not written
 * anything for two minutes has been shown nothing for two minutes, and that is exactly the
 * operator about to record the rice their colleague already entered.
 *
 * Fifteen seconds: shorter than the gap between two foods in an unhurried recall, long enough
 * that a morning's clinic is a few hundred small reads rather than a few thousand. It never
 * touches the half-filled form, only the day beside it.
 */
export const RECALL_REFRESH_MS = 15_000;

// --- the entry form ---

/**
 * The four quantities that cover most of a recall, one tap each.
 *
 * A patient says "one", "two", "three" or "half" far more often than anything else, and each of
 * those as a button is one tap instead of a keyboard, a digit and a dismiss. The field is still
 * there for the rest, because a screen that only offered four numbers would have an operator
 * rounding four ruti down to three.
 *
 * A half rather than a quarter: half a ruti, half a plate and half a banana are things people
 * say, and a quarter of anything is a portion nobody estimates out loud.
 */
export const QUANTITY_STEPS = Object.freeze([0.5, 1, 2, 3]);

/**
 * The entry being built.
 *
 * The quantity is kept as **text**, for the reason station 2's fields are: an operator typing
 * "1" on the way to "1.5" has typed a plausible number, and a field that parsed on every
 * keystroke could not hold "1." at all — the operator would watch their decimal point vanish.
 */
export interface EntryDraft {
  meal: MealCode | '';
  foodCode: string;
  measureCode: string;
  quantity: string;
}

/**
 * A form as it opens, and as it re-opens after each entry.
 *
 * The quantity starts at one. One of a thing is what most of a recall is, and a default of one
 * is a default the operator confirms by not touching it rather than a blank they must fill.
 */
export function emptyDraft(): EntryDraft {
  return { meal: '', foodCode: '', measureCode: '', quantity: '1' };
}

export function chooseMeal(draft: EntryDraft, meal: MealCode): EntryDraft {
  return { ...draft, meal };
}

/**
 * The operator has picked a food, and a measure comes with it.
 *
 * The preselected measure is the food's **first household portion** — the migration's own
 * ordering, which is the measure a patient is likeliest to have used — and grams only when the
 * food has no portion at all. Never grams while a household measure exists: a screen that
 * opened on grams would be asking the operator to convert "two cups" in their head in front of
 * a patient, which is the whole thing this station was built not to do.
 *
 * A food with no offered measures at all leaves the measure empty, and `toEntry` will not build
 * a body from it.
 */
export function chooseFood(draft: EntryDraft, row: FoodRow): EntryDraft {
  const household = row.measures.find((choice) => !choice.universal);
  const fallback = row.measures[0];
  return {
    ...draft,
    foodCode: row.code,
    measureCode: (household ?? fallback)?.code ?? '',
  };
}

export function chooseMeasure(draft: EntryDraft, code: string): EntryDraft {
  return { ...draft, measureCode: code };
}

export function typeQuantity(draft: EntryDraft, text: string): EntryDraft {
  return { ...draft, quantity: text };
}

/**
 * The form after an entry has been recorded.
 *
 * **The meal stays.** A patient describes a meal at a time — "for breakfast, two ruti, an egg
 * and tea" — so clearing the meal after every food would charge one tap per item for a fact
 * that did not change. The food clears, because the next one is a different food, and the
 * quantity goes back to one.
 */
export function afterEntry(draft: EntryDraft): EntryDraft {
  return { ...emptyDraft(), meal: draft.meal };
}

/**
 * The count as a number, or null for empty, half-typed or unparseable.
 *
 * Zero is not a quantity: "no rice" is not a thing a patient ate, and an entry of none would put
 * a row in the record for a food that was not eaten. That is the opposite of station 3's rule
 * for its counts and the same as station 2's for its measurements, and the difference is what
 * the number means — a count of cigarettes can honestly be nought, and a portion cannot.
 *
 * **There is no upper bound checked here.** The server refuses more than a hundred household
 * measures and more than five thousand grams, and those two numbers are nowhere in the contract:
 * copying them into this file would be a second implementation of somebody else's rule, going
 * stale the day either is tuned. A quantity above them is refused with the server's own sentence
 * on `quantity`, which the form shows where the operator is looking.
 */
export function parsedQuantity(draft: EntryDraft): number | null {
  const text = draft.quantity.trim();
  if (text === '') return null;
  const value = Number(text);
  if (!Number.isFinite(value) || value <= 0) return null;
  return value;
}

/**
 * The most of this measure one entry may carry, or null when the server has not said.
 *
 * **Read off the measure's own row**, which is where the server reads it from. It was briefly a
 * pair of payload-level numbers with this function choosing between them by `universal` — right
 * only until a second universal measure was seeded, and the sort of inference that is correct for
 * exactly as long as nobody adds a row.
 *
 * Null is "not known" and not "nothing is allowed". A form that refused every quantity while the
 * measures were still loading would be a form that looked broken for its first second, and the
 * server refuses an excessive quantity either way.
 */
export function ceilingFor(choice: MeasureChoice | null | undefined): number | null {
  if (choice === null || choice === undefined) return null;
  return choice.maxQuantity;
}

/** What the form is still missing, in the order the operator fills it in. */
export const MISSING = ['meal', 'food', 'measure', 'quantity', 'tooMany'] as const;
export type Missing = (typeof MISSING)[number];

export function missingFrom(draft: EntryDraft, choices: readonly MeasureChoice[]): Missing[] {
  const out: Missing[] = [];
  if (draft.meal === '') out.push('meal');
  if (draft.foodCode.trim() === '') out.push('food');
  // A measure that is not among the offered ones counts as missing rather than as chosen: it is
  // one the table cannot weigh, and the operator has to pick again.
  const choice = measureNamed(choices, draft.measureCode);
  if (choice === null) out.push('measure');
  const quantity = parsedQuantity(draft);
  if (quantity === null) {
    out.push('quantity');
    return out;
  }
  // And the measure's own ceiling, so the operator is stopped mid-sentence rather than after
  // they have finished. Only when the server has said what it is.
  const ceiling = ceilingFor(choice);
  if (ceiling !== null && quantity > ceiling) out.push('tooMany');
  return out;
}

/**
 * One entry, as the request body.
 *
 * **There is no `grams` and no `kcal` on this body, at any depth.** The client sends the food,
 * the measure and the count the patient gave; the weight and the energy are computed by the
 * server from the composition table. A client that sent them would be sending its own
 * arithmetic, and a calorie count is a number people act on.
 *
 * `recall_date` is always sent, and it is always the date the server itself answered with. The
 * endpoint would default it to yesterday if it were left out — but "yesterday" moves at
 * midnight, and a recall that a late clinic session started before midnight would then file its
 * last two foods against a different day from its first ten.
 */
export interface DietEntryBody {
  event_id: string;
  patient_id: string;
  visit_id?: string;
  recall_date: string;
  meal: MealCode;
  food_code: string;
  measure_code: string;
  quantity: number;
}

/**
 * The draft as one request, or null when it is not one yet.
 *
 * Null — rather than a body the server will refuse — for the reasons `missingFrom` enumerates,
 * plus the two that are not the operator's doing: no patient, and no day to file it against.
 * Every one of them is a refusal the operator would otherwise meet with a patient mid-sentence.
 *
 * The measure is checked against the offered set rather than against a literal, which is the
 * client half of the 422 rule: a measure the table cannot weigh is never offered, and can
 * therefore never be sent.
 */
export function toEntry(
  draft: EntryDraft,
  choices: readonly MeasureChoice[],
  ids: { event: string; patient: string; visit?: string; recallDate: string },
): DietEntryBody | null {
  const patient = ids.patient.trim();
  const event = ids.event.trim();
  const day = ids.recallDate.trim();
  if (patient === '' || event === '' || day === '') return null;
  if (missingFrom(draft, choices).length > 0) return null;

  const body: DietEntryBody = {
    event_id: event,
    patient_id: patient,
    recall_date: day,
    meal: draft.meal as MealCode,
    food_code: draft.foodCode.trim(),
    measure_code: draft.measureCode.trim(),
    quantity: parsedQuantity(draft) as number,
  };
  const visit = (ids.visit ?? '').trim();
  if (visit !== '') body.visit_id = visit;
  return body;
}

// --- the day, as it stands ---

/** One entry as the screen draws it. Everything decided; nothing left for the component. */
export interface EntryLine {
  entry: DietEntry;
  id: string;
  food: Wording;
  measure: Wording;
  /** True when this entry has been taken back. It stays on the day, struck through. */
  withdrawn: boolean;
  /** Why it was taken back, in the words whoever took it back wrote. */
  reason: string;
  /**
   * Whether this entry can be taken back now.
   *
   * True on **every** standing entry, whoever recorded it. That is the contract's own rule and
   * it is the point: the commonest reason to withdraw is that two assistants recorded the same
   * rice, and requiring the original recorder to remove it would leave the duplicate standing
   * until they come back from the next patient. False only for one already withdrawn.
   */
  mayWithdraw: boolean;
}

/** One meal's worth of the day. */
export interface MealGroup {
  meal: MealCode;
  /**
   * What the meal is called, in the reader's language, **from `core.meal`**.
   *
   * Not a message key. The names are bilingual reference data beside the household measures, for
   * the reason those are: two clients inventing their own Bangla for MID_MORNING is two clients
   * that disagree about what a patient was asked.
   */
  name: Wording;
  /**
   * False for a meal the reference payload does not name.
   *
   * Reachable in one ordinary way — the measures call failed and this tablet has no names at all —
   * and one rare one, a server returning a meal older builds do not know. Both end the same: the
   * entries are still drawn, under the code, with a line saying the name could not be read. A
   * patient's dinner disappearing because a lookup failed is the one outcome worse than a heading
   * an operator has to squint at.
   */
  known: boolean;
  lines: EntryLine[];
}

export function entryLine(entry: DietEntry, locale: Locale): EntryLine {
  const withdrawn = (entry.withdrawn_at ?? '').trim() !== '';
  return {
    entry,
    id: entry.id,
    food: wordingOf(entry.food_en, entry.food_bn, locale),
    measure: wordingOf(entry.measure_en, entry.measure_bn, locale),
    withdrawn,
    reason: (entry.withdrawn_reason ?? '').trim(),
    mayWithdraw: !withdrawn,
  };
}

/**
 * The day grouped by meal, in the order the day happens.
 *
 * Both orderings are the server's. The meals come from `GET /v1/foods/measures`, which returns
 * them "in the order the day happens, so a screen does not hard-code them" — this file holds no
 * list of meals and no idea that lunch follows breakfast. Inside a meal the entries stay in the
 * order the recall returned them, which is by hour where the patient remembered one and by the
 * moment they were recorded otherwise.
 *
 * A meal with nothing in it is not drawn. The day so far is a record of what was said, and seven
 * empty headings above two lines of food is a screen an operator scrolls past.
 *
 * A meal the server has started returning but this build's measures list does not know is drawn
 * last under its own heading rather than dropped. A patient's dinner disappearing because a
 * tablet is a version behind is the one outcome worse than an unfamiliar heading.
 */
export function mealGroups(
  recall: DietRecall | null | undefined,
  meals: readonly Meal[] | undefined,
  locale: Locale,
): MealGroup[] {
  const entries = recall?.entries ?? [];
  const named = meals ?? [];

  // The server's order first — `core.meal.ordering`, which is the order the day happens — and
  // then any meal an entry carries that the payload did not name, last.
  const order: MealCode[] = named.map((meal) => meal.code);
  for (const entry of entries) {
    if (!order.includes(entry.meal)) order.push(entry.meal);
  }

  const groups: MealGroup[] = [];
  for (const code of order) {
    const lines = entries
      .filter((entry) => entry.meal === code)
      .map((entry) => entryLine(entry, locale));
    if (lines.length === 0) continue;
    const meal = named.find((one) => one.code === code) ?? null;
    groups.push({
      meal: code,
      // The code stands in for the name where there is none, so the heading still separates
      // breakfast from dinner. `known` is what lets the screen say why it reads like that.
      name:
        meal === null
          ? wordingOf(code, code, locale)
          : wordingOf(meal.name_en, meal.name_bn, locale),
      known: meal !== null,
      lines,
    });
  }
  return groups;
}

/** True when the day has nothing on it at all — not even a withdrawn entry. */
export function dayIsEmpty(recall: DietRecall | null | undefined): boolean {
  return (recall?.entries ?? []).length === 0;
}

// --- the running totals, which are the server's ---

/**
 * The day's energy and macros, exactly as the server sent them.
 *
 * Every figure here is copied across. Nothing in this function or anywhere else in this feature
 * adds, multiplies or rounds a calorie: `read.diet_entry` carries the per-entry figures computed
 * from the composition table at write time, and `DietTotals` is summed over the entries that
 * still stand — never accumulated, so a withdrawn entry takes its calories with it.
 *
 * `shared` is [R-01] made visible. A recall two assistants built is the thing the requirement
 * asked for, and a screen that could not show it would make the feature invisible to the people
 * using it.
 */
export interface TotalsReading {
  kcal: number;
  protein: number;
  carb: number;
  fat: number;
  entries: number;
  contributors: number;
  /** True when more than one person recorded part of this day. */
  shared: boolean;
}

export function totalsOf(recall: DietRecall | null | undefined): TotalsReading | null {
  const totals = recall?.totals;
  if (totals === undefined) return null;
  return {
    kcal: totals.kcal,
    protein: totals.protein,
    carb: totals.carb,
    fat: totals.fat,
    entries: totals.entries,
    contributors: totals.contributors,
    shared: totals.contributors > 1,
  };
}

// --- which row is the one this tablet just wrote ---

/**
 * The namespace the server derives a diet entry's id in.
 *
 * A fixed uuid whose only job is to keep these ids out of any other namespace's range, copied
 * from `nutrition.dietEntryNamespace`. Copied rather than fetched because it is a constant of the
 * format and not a setting: a deployment cannot change it without changing every id it has ever
 * written, and `entryIdFor`'s test pins a vector computed from the Go implementation.
 */
export const DIET_ENTRY_NAMESPACE = '6f0e5b2a-4c1d-4f6e-9a3b-7d2c8e5f1a90';

/**
 * The entry a given write produces, derived rather than waited for.
 *
 * `POST /v1/diet` answers with the **whole day** and nothing in it marks the row just written —
 * which is right, because the whole day is what stops the same rice being recorded twice. But the
 * day is grouped by meal, so an added breakfast item lands three groups above where the operator
 * is looking, and among two identical rows from two tablets there is otherwise nothing to say
 * which is yours.
 *
 * So the server derives the entry id from the event id (`nutrition.EntryIDFor`, a version-5 uuid
 * over a fixed namespace) and this computes the same value from the event id it just sent. It is
 * a second implementation of an id, which is worth being uncomfortable about — so it is a
 * *format*, not a rule: RFC 4122 §4.3, twenty lines, pinned to a test vector taken from the Go
 * side. It decides nothing clinical. If it were ever wrong the row simply would not highlight.
 */
export function entryIdFor(eventId: string): string {
  const namespace = uuidBytes(DIET_ENTRY_NAMESPACE);
  const event = uuidBytes(eventId);
  if (namespace === null || event === null) return '';

  const message = new Uint8Array(32);
  message.set(namespace, 0);
  message.set(event, 16);

  const digest = sha1(message).slice(0, 16);
  // Version 5 and the RFC 4122 variant, exactly as `uuid.NewHash` sets them.
  digest[6] = (digest[6]! & 0x0f) | 0x50;
  digest[8] = (digest[8]! & 0x3f) | 0x80;

  const hex = Array.from(digest, (byte) => byte.toString(16).padStart(2, '0')).join('');
  return [
    hex.slice(0, 8),
    hex.slice(8, 12),
    hex.slice(12, 16),
    hex.slice(16, 20),
    hex.slice(20, 32),
  ].join('-');
}

/** The sixteen bytes of a uuid, or null when the text is not one. */
function uuidBytes(text: string): Uint8Array | null {
  const hex = text.trim().toLowerCase().replace(/-/g, '');
  if (!/^[0-9a-f]{32}$/.test(hex)) return null;
  const out = new Uint8Array(16);
  for (let i = 0; i < 16; i += 1) out[i] = Number.parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  return out;
}

// --- taking an entry back ---

/**
 * The reasons offered as one tap, as message keys.
 *
 * They **fill the box**; they do not bypass it. The text that reaches the server is whatever is
 * in the field when the operator presses, so a preset can be edited or replaced, and there is
 * exactly one path by which a reason is sent.
 *
 * A list rather than free text alone because the commonest withdrawal at this station is the
 * duplicate two assistants produced, and a reason nobody has to type is a reason that actually
 * gets written rather than "x".
 */
export const REASON_PRESETS = ['duplicate', 'corrected'] as const;
export type ReasonPreset = (typeof REASON_PRESETS)[number];

export interface WithdrawalDraft {
  entryId: string;
  reason: string;
}

export function openWithdrawal(entryId: string): WithdrawalDraft {
  return { entryId, reason: '' };
}

export function typeReason(draft: WithdrawalDraft, text: string): WithdrawalDraft {
  return { ...draft, reason: text };
}

export interface WithdrawalBody {
  event_id: string;
  reason: string;
}

/**
 * A withdrawal as one request, or null when it is not one yet.
 *
 * The reason is required by the contract, by the service and by a table constraint, and it is
 * required for a reason: an entry that vanishes with no explanation is a gap the *other*
 * operator has to account for, and at this station there is always another operator.
 */
export function toWithdrawal(
  draft: WithdrawalDraft,
  eventId: string,
): { entryId: string; body: WithdrawalBody } | null {
  const entryId = draft.entryId.trim();
  const reason = draft.reason.trim();
  const event = eventId.trim();
  if (entryId === '' || reason === '' || event === '') return null;
  return { entryId, body: { event_id: event, reason } };
}
