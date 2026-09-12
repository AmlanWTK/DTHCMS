'use client';

import { useTranslations } from 'next-intl';

import { Card } from '@dthcms/ui';

import { MedicineCombobox } from './MedicineCombobox';

/**
 * The page around the combobox (CP76).
 *
 * Thin on purpose. Everything of substance is in `MedicineCombobox`, because CP81 embeds that
 * component in the prescription editor and anything that lived here would have to be written
 * twice — which is how the lookup screen and the prescribing screen come to disagree about what
 * the clinic stocks.
 *
 * The keyboard legend is not decoration. The checkpoint's requirement is keyboard-first entry,
 * and a keyboard interface nobody is told about is a mouse interface.
 */
export function MedicineLookup() {
  const t = useTranslations('formulary');
  return (
    <Card>
      <MedicineCombobox autoFocus />
      <p className="combobox-keys">{t('search.keys')}</p>
    </Card>
  );
}
