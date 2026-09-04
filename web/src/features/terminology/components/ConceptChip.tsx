'use client';

import { useLocale, useTranslations } from 'next-intl';
import type { ReactNode } from 'react';

import { Button, cx } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import { conceptLabel, type ConceptSelection } from '../api/terminology';

/**
 * One coding, shown whole (CP52, acceptance criterion 2).
 *
 * The reason this is a component rather than three spans at a call site is that it is the
 * only place in the web application that decides what "showing a coding" means — and the
 * decision is that a coding is never shown as a code alone.
 *
 * `E11.9` is not a diagnosis. `E11.9` in ICD-10 2019 is. The same string is a different
 * disease in ICD-11, and in ICD-10 2016 several of the codes this clinic uses meant
 * something narrower. A screen that shows only the code is a screen that will be believed
 * by somebody reading a record in five years, and they will be wrong. So the system and the
 * version are on the chip, quietly but always, and there is no prop to turn them off.
 *
 * Shared with the picker rather than owned by it, because the diagnosis field (CP53) and the
 * prescription screens after it show a chosen coding in exactly this shape, and three
 * near-identical chips is how one of them ends up missing the version.
 */

export interface ConceptChipProps {
  concept: ConceptSelection;
  /** Offered only where removing is a real act. A chip in a printed record has no button. */
  onRemove?: () => void;
  className?: string;
}

export function ConceptChip({ concept, onRemove, className }: ConceptChipProps) {
  const t = useTranslations('terminology');
  const locale = useLocale() as Locale;

  const display = conceptLabel(concept, locale);

  return (
    <span className={cx('app-coding', className)} data-testid="concept-chip">
      <span className="app-coding__display">{display}</span>

      {/* Three facts, each named for a screen reader. "ICD10 2019 E11.9" read as a run of
          characters is what this markup exists to avoid: without the names it is three
          numbers, and a listener cannot tell which of them is the code. */}
      <Part label={t('codeLabel')} className="app-coding__code">
        {concept.code}
      </Part>
      <Part label={t('systemLabel')} className="app-coding__system">
        {concept.system}
      </Part>
      <Part label={t('versionLabel')} className="app-coding__version">
        {concept.version}
      </Part>

      {onRemove && (
        // Full size rather than `sm`, which the design system documents as not meeting the
        // touch target. Removing the wrong diagnosis because a 32px button was the nearest
        // thing to a thumb is not a paper cut.
        <Button
          variant="quiet"
          iconStart="x"
          // Named with the diagnosis, because a form with three chips otherwise offers a
          // screen reader three controls all called "Remove".
          aria-label={t('remove', { display })}
          onClick={onRemove}
          className="app-coding__remove"
        />
      )}
    </span>
  );
}

/**
 * A value with its name spoken but not shown.
 *
 * The value keeps its own element so that the visible text is exactly the value — which is
 * what a test looks for, and what a person copying a code out of the screen selects.
 */
function Part({
  label,
  className,
  children,
}: {
  label: string;
  className: string;
  children: ReactNode;
}) {
  return (
    <span className="app-coding__part">
      <span className="dthc-visually-hidden">{label}</span>
      <span className={className}>{children}</span>
    </span>
  );
}
