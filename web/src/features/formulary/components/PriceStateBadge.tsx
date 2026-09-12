'use client';

import { useTranslations } from 'next-intl';

import { Icon, type IconName } from '@dthcms/ui';

import type { PriceState } from './formularyText';

/**
 * What is known about a price, in three signals at once (CP75; `docs/design-system.md` §2).
 *
 * Colour, an icon and a word — all three, always. The design system's rule is that colour is
 * never the only signal, and it is load-bearing here rather than decorative: under deuteranopia
 * the amber of "nobody has checked this" and the green of "this is what we charge" converge, and
 * the difference between those two is whether a number on this screen can be quoted to a patient.
 *
 * `StatusPill` was the obvious component and is the wrong one: its labels come from the clinical
 * status vocabulary, so a provisional price would have read "Borderline". A price is not a
 * measurement, and borrowing a measurement's words for it would be the interface saying something
 * it does not mean.
 *
 * **Four states, not two.** "Nobody has checked this" and "nobody has priced this at all" are
 * different facts and different amounts of work, and a screen that collapsed them would tell the
 * pharmacist there was nothing to do about the second.
 */
const ICONS: Record<PriceState, IconName> = {
  confirmed: 'check',
  unchecked: 'alert-triangle',
  seeded: 'alert-triangle',
  none: 'help-circle',
};

export function PriceStateBadge({ state }: { state: PriceState }) {
  const t = useTranslations('formulary');
  return (
    <span className="formulary-state" data-state={state}>
      <Icon name={ICONS[state] ?? 'help-circle'} size={14} />
      <span>{t(`state.${state}`)}</span>
    </span>
  );
}
