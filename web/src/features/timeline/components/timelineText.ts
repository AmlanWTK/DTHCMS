/**
 * The words this screen needs that come from server data (CP74).
 *
 * Same shape and the same reasoning as CP73's `dashboardText`: everything with a fixed
 * English and Bengali sentence lives in `messages/`, and what lives here is the handful of
 * labels that come from **values the server chose** — a lane key, an observation code, a
 * flag — where the message file has an entry per value and the lookup has to be safe against
 * a value nobody has written a sentence for yet.
 *
 * There is deliberately no helper here for a value's **source**. CP61's attribution panel
 * already owns that vocabulary, in three states rather than two, and a second one on this
 * screen would be a second place for "source not recorded" to drift from what every other
 * screen says.
 *
 * That property is the whole reason this file exists. The lane list is closed *today*; the
 * server's own contract says a deployment that adds a category gets a lane named after it,
 * and a client that rendered `timeline.lane.procurement` on a physician's chart would be
 * worse than one that rendered `procurement`. A missing sentence is a content gap. A key
 * where a clinical label should be is a screen a physician stops trusting.
 */

/**
 * What these helpers need from `next-intl`'s translator: the call, and `has`.
 *
 * `has` is the important half, for the reason CP73 wrote down: asking for a message that is
 * not there *logs a console error* and returns the key, so a chart drawing fourteen lanes
 * with two unnamed ones fills a physician's console with errors about a fallback that is
 * working perfectly — and makes a genuinely broken key indistinguishable from a deliberate
 * one in the only place somebody would look.
 */
type Translate = ((key: string, values?: Record<string, string | number>) => string) & {
  has: (key: string) => boolean;
};

/**
 * The lanes §8 names, in the order the chart stacks them.
 *
 * Interventions above measurements, and medications directly above the value plot, because
 * acceptance criterion 4 is about the eye travelling a short distance between a bar starting
 * and a line bending. This mirrors the server's own order; it is repeated here so that a
 * lane filter can offer every lane — including ones this patient has none of — rather than
 * only the ones that happen to have marks today.
 */
export const TIMELINE_LANES = [
  'diagnoses',
  'medications',
  'procedures',
  'admissions',
  'investigations',
  'lifestyle',
  'visits',
  'observations',
  'alerts',
  'documents',
  'communication',
  'consent',
  'administrative',
  'registration',
] as const;

export type TimelineLaneKey = (typeof TIMELINE_LANES)[number];

/** What a lane is called, in the reader's language, or the key itself. */
export function laneLabel(key: string, t: Translate): string {
  const message = `lane.${key}`;
  return t.has(message) ? t(message) : key;
}

/**
 * What an observation code is called.
 *
 * The registry has bilingual display names and this payload does not carry them, for the
 * reason CP73 gives: `GET /v1/observations/codes` is reference data with no patient in it,
 * fetched once per session, and copying forty display names onto every chart response would
 * be sending the same dictionary with every patient.
 *
 * The raw code is the honest fallback. `WAIST_CIRC` is readable by the person this screen is
 * for; `timeline.code.WAIST_CIRC` is not.
 */
export function codeLabel(code: string, t: Translate): string {
  const message = `code.${code}`;
  return t.has(message) ? t(message) : code;
}

/**
 * How a mark is drawn apart from by colour.
 *
 * The design system's rule, and it is a safety rule rather than a style one: roughly one man
 * in twelve working in this clinic cannot use hue, a tablet held near a window flattens it
 * for everybody, and a photograph of the screen sent to a consultant has no colour worth
 * relying on. So every series additionally carries a dash pattern and a marker shape, and
 * every lane carries a word.
 *
 * Four patterns rather than one per possible series: past four overlays on one time axis the
 * chart is unreadable whatever it is drawn with, and the screen caps the choice at four for
 * that reason rather than inventing a fifth pattern nobody can tell from the fourth.
 */
export const SERIES_DASHES = ['none', '5 3', '1 3', '9 3 2 3'] as const;

export const SERIES_SHAPES = ['circle', 'square', 'triangle', 'diamond'] as const;

export type SeriesShape = (typeof SERIES_SHAPES)[number];

/** Which of the four visual identities a series gets, by its position in the chosen set. */
export function seriesStyle(index: number): { dash: string; shape: SeriesShape; slot: number } {
  const slot = index % SERIES_DASHES.length;
  return {
    dash: SERIES_DASHES[slot] ?? 'none',
    shape: SERIES_SHAPES[slot] ?? 'circle',
    slot,
  };
}

/**
 * A flag, in words.
 *
 * Drawn as a word beside the value and never as a tint alone. `corrected` on a point is the
 * one that matters most: it says the number under the cursor is not the number that stands
 * today, and a physician reading a trend has no other way to know.
 */
export function flagLabel(flag: string, t: Translate): string {
  const message = `flag.${flag}`;
  return t.has(message) ? t(message) : flag;
}
