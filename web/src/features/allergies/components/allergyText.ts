import type { Locale } from '@/lib/i18n/config';

import type { Allergy, AllergyChange, AllergyReaction } from '../api/allergies';

/**
 * The words an allergy is read in (CP54).
 *
 * Separated from the components for the same reason `historyText.ts` and `alertText.ts`
 * are: these are decisions about what a fact is *called*, three surfaces ask them — the
 * header strip, the station panel and the change history — and a second copy is how one of
 * them ends up showing a raw enum to a pharmacist.
 *
 * Every fallback goes one way. Bangla when there is Bangla and the interface is in Bangla;
 * English otherwise; what the patient said next; the stored code last. A reader of Bangla is
 * better served by an English word than by a blank space where the allergen should be — and
 * on this screen a blank space is the worst of all outcomes, because a header row with
 * nothing in it reads as "checked, nothing found".
 */

/**
 * What the patient is allergic to.
 *
 * The catalogue's words first, because they mean the same thing to everybody; the patient's
 * own words when there is no coding, which is the normal state of the uncoded escape hatch;
 * the code only when there is nothing else. Never an empty string — a control named "" is a
 * control a screen reader cannot announce, and a row with no substance is not an allergy.
 */
export function substanceName(allergy: Allergy, locale: Locale): string {
  if (locale === 'bn' && allergy.display_bn) return allergy.display_bn;
  if (allergy.display_en) return allergy.display_en;
  if (allergy.display_bn) return allergy.display_bn;
  if (allergy.said && allergy.said.trim() !== '') return allergy.said.trim();
  return allergy.code ?? '';
}

/**
 * What it did to them.
 *
 * The server sends the words for the reaction beside its code, so a row whose vocabulary
 * entry this build has never heard of shows `ANAPHYLAXIS` rather than nothing. That is a
 * visible defect somebody will report, which is what it should be; an empty cell in this
 * column reads as a reaction nobody recorded.
 */
export function reactionName(allergy: Allergy, locale: Locale): string {
  if (locale === 'bn' && allergy.reaction_bn) return allergy.reaction_bn;
  if (allergy.reaction_en) return allergy.reaction_en;
  if (allergy.reaction_bn) return allergy.reaction_bn;
  return allergy.reaction;
}

/** One entry of the reaction vocabulary, as a form offers it. */
export function reactionLabel(reaction: AllergyReaction, locale: Locale): string {
  if (locale === 'bn' && reaction.display_bn) return reaction.display_bn;
  return reaction.display_en || reaction.display_bn || reaction.reaction;
}

/**
 * What one line of the change history was about.
 *
 * A withdrawn allergy is often the interesting row, and it may carry only a code or only
 * what the patient said. An assertion carries neither, and is named by its kind — the
 * caller supplies that word, because it is the same word the banner uses and it belongs in
 * the message file rather than here.
 */
/**
 * A reaction code rendered in the reader's language, given the vocabulary.
 *
 * The change history carries the reaction as a **code**, because the endpoint answers about
 * withdrawn rows too and a withdrawn row's display would be a join nobody needs on the write
 * path. So the words are looked up here, from the vocabulary the panel already has — and the
 * fallback is the code itself, because "ITCHING" is at least readable, whereas a blank line
 * in an allergy history is a row that reads as though nothing happened.
 */
export function reactionText(
  code: string,
  reactions: readonly AllergyReaction[],
  locale: Locale,
): string {
  for (const reaction of reactions) {
    if (reaction.reaction === code) return reactionLabel(reaction, locale);
  }
  return code;
}

export function changeSubject(change: AllergyChange, fallback: string): string {
  if (change.said && change.said.trim() !== '') return change.said.trim();
  if (change.code) return change.code;
  return fallback;
}
