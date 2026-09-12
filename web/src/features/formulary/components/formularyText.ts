import { bilingual } from '@/lib/bilingual';
import type { Locale } from '@/lib/i18n/config';

import type { FormularyPrice, FormularyProduct } from '../api/formulary';

/**
 * Turning what the formulary carries into words (CP75).
 *
 * # No message keys live here
 *
 * Every function in this file takes values and returns values. The literal `t('…')` calls stay in
 * the components, where `useTranslations('formulary')` names the namespace — because that is what
 * `i18n.test.ts` reads to check that every key the code asks for exists in both message files. A
 * helper that took `t` and called it with a literal key would be a key nothing checks, in the one
 * file most likely to accumulate them.
 *
 * # Which half of a row is translated and which is not
 *
 * A medicine's **identity stays in Latin script**: the trade name, the strength, the
 * manufacturer, the generic. That is not laziness about Bengali — it is the same rule the design
 * system applies to a glucose reading. "Comet" is what is printed on the box the patient is
 * handed and what the pharmacy counter reads back; a transliterated trade name is a different
 * medicine as far as dispensing is concerned, and an interface that produced one would be
 * introducing a second name for a drug into a system whose whole purpose downstream (CP77, CP78)
 * is that one drug has one name.
 *
 * **Everything around it is translated**: the class, the dosage form, the dispensing unit, every
 * column header, every state, every empty state, every error. The clinic's pharmacist works in
 * Bangla, and a screen that gave them an English word for "tablet" beside a brand name they
 * already knew would be the half-bilingual interface this project keeps refusing to ship.
 *
 * # The amount is never formatted here, or anywhere else in TypeScript
 *
 * `amount_bdt` arrives from the server already rendered, in integer arithmetic. A component draws
 * it through the `currency` message, which attaches ৳ or Tk according to the reader's language.
 * Nothing in this application divides `amount_poisha` by 100 — that is a floating-point division
 * of exactly the values (0.34, 6.02) it gets wrong, and the server has already done it properly.
 */

/** The class, the form or the unit in the reader's language, falling back rather than blanking. */
export function named(en: string, bn: string, locale: Locale): string {
  return bilingual(en, bn, locale)?.text ?? en;
}

/**
 * The one-line description of what a medicine *is*, for the row's second line.
 *
 * Strength first, because that is what distinguishes two rows with the same brand name, and it is
 * what a physician's eye goes to.
 */
export function presentation(product: FormularyProduct, locale: Locale): string {
  const form = named(product.form_name_en, product.form_name_bn, locale);
  const unit = named(product.unit_name_en, product.unit_name_bn, locale);
  return `${product.strength} · ${form} · ${unit}`;
}

/**
 * What a price's state should say, as a message-key suffix.
 *
 * Four states rather than two, and the distinction that matters is the last pair: a price nobody
 * has checked and a medicine nobody has priced are **different things**, and a screen that drew
 * them the same way would tell a pharmacist there was nothing to do about the second.
 *
 * A seeded price and a hand-recorded unconfirmed one are both "not checked" to a reader and are
 * kept apart here anyway, because the badge's own styling and the next checkpoint that wants to
 * count them both need the difference and neither should have to re-derive it from `origin`.
 */
export type PriceState = 'confirmed' | 'unchecked' | 'seeded' | 'none';

export function priceState(price: FormularyPrice | undefined | null): PriceState {
  if (!price) return 'none';
  if (price.verification === 'VERIFIED') return 'confirmed';
  if (price.origin === 'SEED') return 'seeded';
  return 'unchecked';
}

/**
 * Who recorded a price, or the fact that nobody did.
 *
 * Returns null for a seeded price, which the caller renders as the explicit sentence rather than
 * as a blank. A blank in an attribution column reads as "not loaded yet"; this one means "nobody
 * has ever taken responsibility for this number", which is the single most important thing the
 * screen has to say about the 250 rows it opens on.
 */
export function recordedBy(price: FormularyPrice, locale: Locale): string | null {
  const name = bilingual(price.recorded_by_name_en ?? '', price.recorded_by_name_bn ?? '', locale);
  return name?.text ?? null;
}
