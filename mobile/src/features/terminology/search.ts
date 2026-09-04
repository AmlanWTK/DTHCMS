import type { components } from '@dthcms/api-client';

/**
 * The coded terminology picker, as data (CP52, §3 step 6).
 *
 * # Why the whole picker is in here and none of it is in the screen
 *
 * A React Native component cannot be rendered outside a device, so anything it decides is a
 * decision nobody checks. What this picker decides is not layout. It decides which of two
 * answers is on screen when both arrive out of order, and it decides what the clinician's tap
 * actually means — and the second of those is criterion 2: **a coding is a system, a version
 * and a code, together.** A code on its own is a string, and a string is what somebody
 * discovers, four years later, cannot be read back.
 *
 * # The staleness rule is structural, not a race that usually loses
 *
 * Every request leaves with a sequence number, every answer carries the number of the request
 * that produced it, and there is exactly **one** function in this file that can replace what
 * is on screen — `apply` — whose first act is to drop anything not newer than what is already
 * shown. There is no other door. A slow answer for "dia" landing after a fast one for
 * "diabetes" is therefore not a race that a debounce usually wins; it is arithmetic that
 * cannot come out the other way.
 *
 * That matters more here than in an ordinary autocomplete. The clinic's link drops for
 * seconds at a time (ADR-0004), so out-of-order answers are the normal case rather than the
 * pathological one, and the failure they cause is silent: the list under the clinician's
 * finger looks like an answer to what they typed.
 *
 * # The server ranks; this never re-sorts
 *
 * `ORDER BY tier, favourite_rank, score DESC, …` happens in one SQL statement, and the tier
 * and the score come back on every row so a clinician can be told *why* something is third.
 * Re-sorting any of that here — even to group by heading — would silently disagree with the
 * ranking the clinic can actually tune. What this file does instead is note where a heading
 * changes as it walks the list in the server's order, which gives the grouping without moving
 * a single result.
 */

export type CodeSystem = components['schemas']['CodeSystem'];
export type Concept = components['schemas']['Concept'];
export type ConceptMapping = components['schemas']['ConceptMapping'];

/** The interface language. Local rather than imported: this file must stay renderer-free. */
export type Locale = 'en' | 'bn';

/**
 * The server's own ceiling, mirrored.
 *
 * The server caps at 25 and this trims to 25 again on the way out. Not distrust of the
 * backend: the display list is also what a merge or a future cache could grow, and a picker
 * that silently became scrollable to fifty rows is a picker where the twenty-first result is
 * chosen by whoever scrolls furthest. The cap is a decision about reading, so it is enforced
 * where the reading happens.
 */
export const MAX_RESULTS = 25;

/**
 * How still the box must be before a request goes.
 *
 * Criterion 1 is twenty diagnoses in three keystrokes, so the budget is small: a debounce
 * long enough to be felt would spend more than the typing does. 180 ms sits above the gap
 * between two keystrokes of ordinary typing — so a three-letter burst is one request rather
 * than three on a link that may be a shared 3G connection — and below the ~250 ms at which a
 * person starts to experience a list as lagging behind their fingers.
 */
export const DEBOUNCE_MS = 180;

// --- what a coding is ---

/**
 * The three halves of a coding. All three, always.
 *
 * There is no constructor here that can produce two of them, and nothing in this file returns
 * a bare code. That is the whole of criterion 2 expressed as a type: the only way to obtain
 * one of these is from a concept that carries all three, and `selectable` below is the gate.
 */
export interface Coding {
  system: string;
  version: string;
  code: string;
}

/** A concept's coding: its own system and version, never the picker's idea of them. */
export function codingOf(concept: Concept): Coding {
  return { system: concept.system, version: concept.version, code: concept.code };
}

/**
 * Whether this concept can become a coding at all.
 *
 * A row the server sent without a version cannot be recorded, and the honest thing is to say
 * so on that row rather than to store two-thirds of a coding and find out years later. It
 * should never happen — the contract makes all three required — which is exactly why it is
 * checked here rather than trusted: the cost of the check is nothing and the cost of being
 * wrong is a record nobody can read back.
 */
export function selectable(concept: Concept): boolean {
  const coding = codingOf(concept);
  return coding.system.trim() !== '' && coding.version.trim() !== '' && coding.code.trim() !== '';
}

/** Whether two codings are the same one. All three parts, because two of them are not it. */
export function sameCoding(a: Coding | null, b: Coding | null): boolean {
  if (a === null || b === null) return false;
  return a.system === b.system && a.version === b.version && a.code === b.code;
}

/**
 * What a concept is called, in the reader's language.
 *
 * Bangla when there is Bangla, English otherwise, and the code itself if somehow neither —
 * never an empty row. A blank line in a diagnosis picker is worse than an English one: the
 * clinician cannot tell whether it is a bad translation or a bad result, and the row is still
 * tappable either way.
 */
export function conceptLabel(concept: Concept, locale: Locale): string {
  if (locale === 'bn') {
    const bengali = (concept.display_bn ?? '').trim();
    if (bengali !== '') return bengali;
  }
  const english = concept.display_en.trim();
  return english === '' ? concept.code : english;
}

/**
 * The grouping a row is filed under, in the reader's language.
 *
 * This exists because of a screenshot. The first Bangla render of the web picker showed a
 * column of Bengali diagnoses filed under English chapter names, and half-bilingual is its
 * own failure — worse than either language alone, because it reads as an interface somebody
 * translated the easy parts of. A standing database rule now refuses a heading with no Bangla
 * form, so the fallback below should be unreachable for seeded content; it stays because a
 * concept is allowed to have no heading at all.
 */
export function conceptHeading(concept: Concept, locale: Locale): string {
  if (locale === 'bn') {
    const bengali = (concept.heading_bn ?? '').trim();
    if (bengali !== '') return bengali;
  }
  return (concept.heading ?? '').trim();
}

// --- why a result came where it did ---

export type Tier = 1 | 2 | 3 | 4;

/**
 * The words behind a rank.
 *
 * "Why is that third" is the question every search gets asked, and the server answers it on
 * every row rather than hiding it. These are the message keys that answer it out loud —
 * because a ranking a clinician cannot inspect is a ranking they stop trusting, and a picker
 * they do not trust is a picker they scroll past to type free text.
 *
 * A list rather than a bare union, for the same reason `PROMPT_RULES` is a list: the message
 * files are checked against it, so a reason added here without a sentence written for it is a
 * test failure rather than a raw identifier under somebody's finger.
 */
export const REASONS = ['code', 'favourite', 'wordStart', 'similar', 'clinicList'] as const;
export type Reason = (typeof REASONS)[number];

/**
 * The tier, as a reason.
 *
 * Mirrors the four tiers of `SearchTerminology` exactly and adds nothing: 1 is the code typed
 * literally, 2 a clinic favourite whose words start with the query, 3 any word-start match, 4
 * the trigram tier — which is to say, a misspelling. Tier 4 is the one worth naming plainly:
 * the clinician needs to know the catalogue guessed, so that they read the row before tapping
 * rather than after.
 */
export function tierReason(tier: number | undefined): Reason | null {
  switch (tier) {
    case 1:
      return 'code';
    case 2:
      return 'favourite';
    case 3:
      return 'wordStart';
    case 4:
      return 'similar';
    default:
      return null;
  }
}

/**
 * Why this row is here, whether it came from a search or from the clinic's own list.
 *
 * The favourites endpoint returns no tier — it is not a search, and there is nothing to rank
 * against. What it does return is the rank, which is the reason: this is one of the twenty
 * diagnoses DTHC actually makes.
 */
export function reasonFor(concept: Concept): Reason | null {
  const fromTier = tierReason(concept.tier);
  if (fromTier !== null) return fromTier;
  return concept.favourite_rank === undefined ? null : 'clinicList';
}

// --- the list, as it is drawn ---

export interface ConceptRow {
  concept: Concept;
  /**
   * The heading to draw above this row, or `''`. Set only where the heading changes from the
   * row before, so a run of one chapter is captioned once.
   */
  heading: string;
  reason: Reason | null;
  /** False on a row that cannot become a coding. The screen says so rather than hiding it. */
  selectable: boolean;
}

/**
 * The results, in the server's order, with the chapter breaks marked.
 *
 * Deliberately **not** partitioned into groups. Collecting every "Endocrine…" row together
 * would move results past one another, which is a re-ranking however innocent it looks: the
 * exact code match a clinician typed would sink below a favourite two rows down because they
 * happen to share a chapter. Walking the list once and captioning each new run gives the same
 * scannability and cannot reorder anything, because it never holds two rows at the same time.
 */
export function conceptRows(concepts: readonly Concept[], locale: Locale): ConceptRow[] {
  const rows: ConceptRow[] = [];
  let previousHeading = '';
  for (const concept of visible(concepts)) {
    const heading = conceptHeading(concept, locale);
    rows.push({
      concept,
      heading: heading !== '' && heading !== previousHeading ? heading : '',
      reason: reasonFor(concept),
      selectable: selectable(concept),
    });
    previousHeading = heading;
  }
  return rows;
}

/** The rows a clinician will actually read: the first `MAX_RESULTS`, and no more. */
export function visible(concepts: readonly Concept[]): readonly Concept[] {
  return concepts.length <= MAX_RESULTS ? concepts : concepts.slice(0, MAX_RESULTS);
}

/**
 * True when the list came back full.
 *
 * Stated as "at the cap" rather than "there were more", because the picker cannot know: the
 * server stops counting at 25 too. What a clinician needs to be told is that the bottom of
 * this list is not the bottom of the catalogue — the answer to a query with three hundred
 * matches is a better query, and they can only make one if somebody says so.
 */
export function atCap(concepts: readonly Concept[]): boolean {
  return concepts.length >= MAX_RESULTS;
}

// --- what went wrong, in the three ways it can ---

/**
 * A refusal, an unreachable catalogue, and a server that answered with something else.
 *
 * Three rather than one because they are three different sentences and two different
 * instructions. A refusal is about the request — an unknown system, a version this deployment
 * has not loaded, SNOMED pending D-24 — and the server names the field, so the picker shows
 * what the server said rather than a sentence this app invented about somebody else's rules.
 * The other two are about the catalogue being absent, and in a clinic whose link drops that
 * is not an error state at all: it is Tuesday, and the answer is to record the diagnosis in
 * words and let it be coded later.
 */
export type Trouble =
  | { kind: 'refused'; field: string; message: string }
  | { kind: 'unreachable' }
  | { kind: 'failed'; message: string };

/** Whether the catalogue is simply not answering — the case where free text is the way on. */
export function catalogueAbsent(trouble: Trouble | null): boolean {
  return trouble !== null && trouble.kind !== 'refused';
}

// --- the machine ---

/** What one request needs. Exactly the arguments `api.ts` takes, so nothing is assembled twice. */
export interface SearchRequest {
  /** Increases by one per request, for ever. The whole staleness rule rests on this. */
  seq: number;
  system: string;
  /** `''` means "the system's default"; the answer says which that was. */
  version: string;
  q: string;
}

/** An answer to one request, carrying the sequence number of the request that produced it. */
export type SearchAnswer =
  | {
      seq: number;
      ok: true;
      /** The resolved pair. The version here is the one to stamp on a coding. */
      system: string;
      version: string;
      concepts: readonly Concept[];
    }
  | { seq: number; ok: false; trouble: Trouble };

export interface PickerState {
  /** The terminology being searched. */
  system: string;
  /** The version the caller asked for, or `''` for the system's default. */
  askedVersion: string;
  /** The version the catalogue resolved to, once it has told us. `''` until then. */
  version: string;

  /** Exactly what is in the box. Empty is a real query: it means the favourites. */
  query: string;
  /** When the box last changed, on whatever clock `now` is read from. */
  changedAt: number;

  /** The query the newest request went out for. `null` before the first request. */
  issuedQuery: string | null;
  /** The sequence number of the newest request issued. Only ever increases. */
  issued: number;
  /** The sequence number of the newest answer applied. Only ever increases. */
  applied: number;

  concepts: readonly Concept[];
  trouble: Trouble | null;
  /** The clinician's choice, as a whole coding or not at all. */
  selected: Coding | null;
}

/**
 * A picker as it opens.
 *
 * `issuedQuery` is null and the query is empty, which together mean "fetch the favourites,
 * now, without waiting". That is criterion 1's first half: the twenty diagnoses this clinic
 * makes are on screen before anybody has typed, so the common case costs no keystrokes at all
 * and the uncommon one costs three.
 */
export function openPicker(system: string, version = ''): PickerState {
  return {
    system,
    askedVersion: version,
    version: '',
    query: '',
    changedAt: 0,
    issuedQuery: null,
    issued: 0,
    applied: 0,
    concepts: [],
    trouble: null,
    selected: null,
  };
}

/** A keystroke. Nothing is fetched here; `due` decides when the box has been still enough. */
export function typed(state: PickerState, text: string, now: number): PickerState {
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
 * picker is not typing, and 180 ms of blank list on open is 180 ms of a clinician wondering
 * whether the tap registered.
 */
export function due(state: PickerState, now: number): boolean {
  if (state.issuedQuery === null) return true;
  if (state.query === state.issuedQuery) return false;
  return now - state.changedAt >= DEBOUNCE_MS;
}

/**
 * The version the next request will name.
 *
 * Once the catalogue has resolved a default, the picker names it explicitly from then on. A
 * deployment that loaded a newer ICD-10 between two keystrokes would otherwise hand one
 * clinician two versions inside one search, and the coding they end up tapping would depend
 * on how fast they type.
 */
export function requestVersion(state: PickerState): string {
  return state.version !== '' ? state.version : state.askedVersion;
}

/**
 * Take the next sequence number and the request that carries it.
 *
 * Returns the state as well as the request because the two must not be able to come apart: a
 * caller cannot issue a request without recording that it did.
 */
export function issue(
  state: PickerState,
  now: number,
): { state: PickerState; request: SearchRequest } {
  const seq = state.issued + 1;
  const request: SearchRequest = {
    seq,
    system: state.system,
    version: requestVersion(state),
    q: state.query,
  };
  return {
    state: { ...state, issued: seq, issuedQuery: state.query, changedAt: now },
    request,
  };
}

/**
 * The one door.
 *
 * Every replacement of what is on screen goes through here, and the guard is the first line.
 * An answer no newer than the one already shown is dropped whole — results, version, trouble
 * and all — so a slow request cannot undo a fast one, and cannot half-undo one either by
 * landing its error over the newer list.
 *
 * Failures clear the results deliberately. A list left standing under a box that no longer
 * matches it is the single way this picker could put the wrong diagnosis under somebody's
 * finger, and "the network went away" is not worth that risk.
 */
export function apply(state: PickerState, answer: SearchAnswer): PickerState {
  if (answer.seq <= state.applied) return state;

  if (!answer.ok) {
    return { ...state, applied: answer.seq, concepts: [], trouble: answer.trouble };
  }

  return {
    ...state,
    applied: answer.seq,
    // The resolved version, from the answer rather than from anything this app assumed.
    system: answer.system,
    version: answer.version,
    concepts: answer.concepts,
    trouble: null,
  };
}

/** True while an answer is still owed. Derived, so it cannot disagree with the sequences. */
export function busy(state: PickerState): boolean {
  return state.issued > state.applied;
}

/**
 * Ask again after a failure.
 *
 * Forgetting which query was issued is what makes `due` fire for the same text a second time.
 * The alternative — a separate "retry wanted" flag — is a second thing that can disagree with
 * the first.
 */
export function retry(state: PickerState, now: number): PickerState {
  return { ...state, issuedQuery: null, changedAt: now, trouble: null };
}

/**
 * The clinician's choice.
 *
 * A concept without all three parts is not stored at all. That is criterion 2's guarantee
 * made structural rather than remembered: there is no path through this file that puts a bare
 * code into `selected`, so there is no path that hands the caller one.
 */
export function select(state: PickerState, concept: Concept): PickerState {
  if (!selectable(concept)) return state;
  return { ...state, selected: codingOf(concept) };
}

export function clearSelection(state: PickerState): PickerState {
  return { ...state, selected: null };
}

/** Whether this row is the one currently chosen. */
export function isSelected(state: PickerState, concept: Concept): boolean {
  return sameCoding(state.selected, codingOf(concept));
}
