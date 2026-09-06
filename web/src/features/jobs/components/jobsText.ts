import { bilingual } from '@/lib/bilingual';
import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import { ageParts, ranForParts, type JobKind } from '../api/jobs';

/**
 * Turning what the queue carries into words (CP69).
 *
 * # Why a translator is passed in rather than a formatted string returned from the API layer
 *
 * "Four minutes" is a sentence, and its word order differs between the two languages this
 * clinic runs in. A helper that joined a number to a unit here would bake English word order
 * into the arithmetic, and the Bangla screen would read as translated software — which the
 * endocrinologist who reads Bangla notices before he notices anything else on the page.
 *
 * So the split is: `ageParts` in the API module decides *which unit says something useful*,
 * and the message file decides *what the sentence is*. This file is the join, and it is the
 * only place the two meet.
 *
 * # Nothing here invents a name
 *
 * A kind whose description this build has not seen falls back to its own dotted identifier —
 * `clinical.synthesis` — and never to a blank and never to a guess. The identifier is ugly
 * and it is true, and it is also the string somebody types into a log search, which is what
 * an operator does next.
 */

/** The shape next-intl's `useTranslations` returns, narrowed to what this file needs. */
export type Translate = (key: string, values?: Record<string, string | number>) => string;

/** "waiting 4 minutes", "2 hours" — the largest unit that still says something. */
export function ageLabel(t: Translate, seconds: number): string {
  const { unit, value } = ageParts(seconds);
  return t(`age.${unit}`, { count: value });
}

/** How long one attempt ran before it failed. Milliseconds survive below a second. */
export function ranForLabel(t: Translate, ms: number): string {
  const { unit, value } = ranForParts(ms);
  return t(`ranFor.${unit}`, { value });
}

/**
 * What this kind is for, in the reader's language, falling back to the identifier.
 *
 * The descriptions are bilingual on `JobKind` and absent from `JobQueueHealth`, which is why
 * the screen reads the catalogue beside the health rows. A row whose kind is missing from the
 * catalogue still renders — a health row the catalogue does not describe is a deployment
 * mid-flight, not a reason to drop the row that says the queue is stuck.
 */
export function kindDescription(
  kind: string,
  catalogue: ReadonlyMap<string, JobKind>,
  locale: Locale,
): string {
  const entry = catalogue.get(kind);
  if (!entry) return kind;
  return bilingual(entry.description_en, entry.description_bn, locale)?.text ?? kind;
}

/** The catalogue as a lookup. One place, so no component builds its own and drifts. */
export function catalogueOf(kinds: readonly JobKind[] | undefined): ReadonlyMap<string, JobKind> {
  return new Map((kinds ?? []).map((kind) => [kind.kind, kind]));
}

/**
 * Which sentence names who stopped a kind, and with what to fill into it.
 *
 * A **second** sentence, never a replacement for the first. The row still says what a pause
 * means — nothing of this type is being taken, and what is queued stays queued — because that
 * is what a reader who has never seen a paused row needs. This adds what a reader who has
 * needs: the person to ask before turning it back on.
 *
 * Three shapes, degrading rather than disappearing. With a name and a time it reads *"paused
 * by Dr Nahid Rahman, 3 Sep 2026, 2:40 pm"*; with one of the two it says that one; with
 * neither it answers null and the row simply does not draw the line. Rendering "paused by  at
 * " when the server sent an empty name would look broken, and dropping the whole notice
 * because the attribution was missing would lose the important half to protect the detail.
 *
 * # Why this returns a key rather than a sentence
 *
 * Composing it here would need a translator, and a literal translator call in a file with no
 * `useTranslations` namespace is a key the i18n test's static scan reads as top-level and
 * reports as missing — which is the scan being right about something worth keeping right.
 * Returning the choice and its values leaves the rendering where the namespace is, and makes
 * this function pure: the tests check which sentence is chosen without needing a fake one.
 *
 * The name goes through `bilingual`, which falls back to the other language rather than to a
 * blank: an operator reading Bangla who is shown an English name has still been answered, and
 * a blank where a colleague's name should be has not.
 *
 * A wall-clock time rather than an age, and through `formatDateTime` like every other moment
 * on this page — one clock and one numeral rule per screen. "Paused 2 days ago" invites
 * arithmetic; "3 Sep, 2:40 pm" is what somebody quotes when they go and ask about it.
 */
export interface PausedSentence {
  /** A key under the `jobs.` namespace. */
  key: 'pausedByAt' | 'pausedBy' | 'pausedAt';
  values: Record<string, string>;
}

export function pausedAttribution(
  row: {
    paused_at?: string;
    paused_by_name_en?: string;
    paused_by_name_bn?: string;
  },
  locale: Locale,
): PausedSentence | null {
  const name = bilingual(row.paused_by_name_en ?? '', row.paused_by_name_bn ?? '', locale)?.text;
  const at =
    row.paused_at === undefined ? undefined : formatDateTime(new Date(row.paused_at), locale);

  if (name && at) return { key: 'pausedByAt', values: { name, at } };
  if (name) return { key: 'pausedBy', values: { name } };
  if (at) return { key: 'pausedAt', values: { at } };
  return null;
}
