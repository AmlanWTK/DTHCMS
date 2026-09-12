import type { Locale } from '@/lib/i18n/config';

import type { RuleCondition, RulePlain, RulePredicate, RuleTarget } from '../api/rules';

/**
 * Turning what a rule carries into words (CP77).
 *
 * # No message keys live here
 *
 * Every function takes values and returns values. The literal `t('…')` calls stay in the
 * components, where `useTranslations('medicationRules')` names the namespace — because that is
 * what `i18n.test.ts` reads to check every key exists in both message files. A helper that took
 * `t` and called it with a computed key would be a key nothing checks.
 *
 * # The sentence itself is not built here
 *
 * `plain` comes from the server, rendered from the canonical condition it will evaluate. This
 * file only chooses which of the two the reader gets. Building the sentence in TypeScript would
 * make the preview agree with the form rather than with the rule.
 */

/** The rule in the reader's language, falling back rather than blanking. */
export function plainText(plain: RulePlain | undefined, locale: Locale): string {
  if (!plain) return '';
  const chosen = locale === 'bn' ? plain.bn : plain.en;
  return chosen?.trim() ? chosen : (plain.en ?? '');
}

export function plainNeeds(plain: RulePlain | undefined, locale: Locale): string[] {
  if (!plain) return [];
  const chosen = locale === 'bn' ? plain.needs_bn : plain.needs_en;
  return chosen?.length ? chosen : (plain.needs_en ?? []);
}

/** An empty target, for a form field that has not been filled in yet. */
export function emptyTarget(): RuleTarget {
  return { match: 'GENERIC', generics: [], classes: [] };
}

/**
 * A blank predicate of one kind, with the fields that kind uses.
 *
 * The defaults are chosen to be the common case rather than the neutral one: a renal rule is
 * almost always "below", and an author who has to change the operator every time will eventually
 * forget to.
 */
export function blankPredicate(kind: RulePredicate['kind']): RulePredicate {
  switch (kind) {
    case 'EGFR':
      return { kind, operator: 'LT', value: 30, unit: 'mL/min/1.73m2' };
    case 'AGE':
      return { kind, operator: 'LT', value: 18, unit: 'years' };
    case 'DAILY_DOSE':
      return { kind, operator: 'GT', value: 0, unit: 'mg' };
    case 'PREGNANCY':
      return { kind, states: ['PREGNANT'] };
    case 'HEPATIC':
      return { kind, states: ['SEVERE'] };
    case 'DIAGNOSIS':
      return { kind, diagnosis_codes: [] };
    case 'ALLERGY':
      return { kind, allergen_group: '' };
    case 'DUPLICATE':
      return { kind, with: { match: 'GENERIC' }, current_medications: true };
    case 'CO_PRESCRIBED':
    default:
      return { kind: 'CO_PRESCRIBED', with: emptyTarget(), current_medications: true };
  }
}

/** A blank rule, for the "write a rule" form. */
export function blankCondition(): RuleCondition {
  return { subject: emptyTarget(), when: [] };
}

/** Comma-separated text ⇄ a list, for the fields where a list is faster typed than clicked. */
export function toList(text: string): string[] {
  return text
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
}

export function fromList(list: string[] | undefined): string {
  return (list ?? []).join(', ');
}

/**
 * Which state a rule row is in, as one word.
 *
 * Four, not two. "Nobody has approved this" and "this was withdrawn" are different facts and
 * different amounts of work, and a screen that collapsed them would hide the forty rules waiting
 * to be read among the ones that have been dealt with.
 */
export type RuleState = 'approved' | 'unapproved' | 'withdrawn' | 'draft';

export function ruleState(rule: {
  approved: boolean;
  is_active: boolean;
  published_version?: number | null;
}): RuleState {
  if (!rule.is_active) return 'withdrawn';
  if (rule.published_version) return 'approved';
  if (rule.approved) return 'draft';
  return 'unapproved';
}
