import { formatPartialDate } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import type { FamilyRelation, HistoryItem, HistoryKind } from '../api/history';

/**
 * The words a history item is read in (CP53).
 *
 * Separated from the components for the same reason `alertText.ts` is: these are decisions
 * about what a fact is *called*, several screens ask them, and a second copy is how one of
 * them ends up showing a raw enum to a clinician.
 *
 * Every fallback here goes one way. Bangla when there is Bangla and the interface is in
 * Bangla; English otherwise; the stored code last. A reader of Bangla is better served by an
 * English word than by a blank space where the diagnosis should be — and the code, ugly as
 * it is, is at least something a person can look up.
 */

/** What this kind of history is called. */
export function kindLabel(kind: HistoryKind, locale: Locale): string {
  if (locale === 'bn' && kind.display_bn) return kind.display_bn;
  return kind.display_en || kind.display_bn || kind.kind;
}

/**
 * Who a family history is about.
 *
 * The relation is a code — `MOTHER` — and the server sends the words for it, so a screen
 * that could not find the row shows the code rather than nothing. That is a visible defect
 * somebody will report, which is what it should be; an empty cell reads as "no relative",
 * and a family history with no relative is not one.
 */
export function relationLabel(
  relations: readonly FamilyRelation[],
  code: string,
  locale: Locale,
): string {
  const relation = relations.find((one) => one.relation === code);
  if (!relation) return code;
  if (locale === 'bn' && relation.display_bn) return relation.display_bn;
  return relation.display_en || relation.display_bn || code;
}

/**
 * When it started, said no more precisely than the patient said it.
 *
 * `null` when there is no onset at all, so a caller renders nothing rather than a row
 * reading "Started —". The precision defaults to `day` only when the server sent a date
 * without one, which the contract allows and which is the least surprising reading of a
 * bare date.
 */
export function onsetText(item: HistoryItem, locale: Locale): string | null {
  if (!item.onset_on) return null;
  const at = Date.parse(item.onset_on);
  if (Number.isNaN(at)) return null;
  return formatPartialDate(at, locale, item.onset_precision ?? 'day');
}

/**
 * What to call this item in a sentence — a button's accessible name, a dialog's title.
 *
 * The catalogue's words first, because they are the ones that mean the same thing to
 * everybody; what the patient said when there is no coding; the code as a last resort. A
 * screen with six items otherwise offers a screen reader six controls all called "Confirm",
 * which is the same as offering none.
 */
export function itemName(item: HistoryItem, locale: Locale): string {
  if (locale === 'bn' && item.display_bn) return item.display_bn;
  if (item.display_en) return item.display_en;
  if (item.display_bn) return item.display_bn;
  if (item.said && item.said.trim() !== '') return item.said;
  return item.code ?? '';
}
