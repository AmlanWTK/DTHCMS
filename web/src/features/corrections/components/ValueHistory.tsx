'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, EmptyState, Select, Skeleton } from '@dthcms/ui';

import {
  OBSERVATION_CODES_KEY,
  codeEntry,
  listObservationCodes,
  listPatientObservations,
  patientObservationsKey,
} from '@/features/observations';
import { bilingual } from '@/lib/bilingual';
import type { Locale } from '@/lib/i18n/config';

import { DerivedValues } from './DerivedValues';
import { ValueChain } from './ValueChain';

/**
 * One patient's value history, one code at a time (CP62, criterion 5).
 *
 * # Why one code at a time
 *
 * A patient's record holds dozens of codes and hundreds of rows. "The complete chain" is a
 * question somebody asks about *a value* — this height, that glucose — and a screen that drew
 * every chain for every code at once would answer it by burying it. The contract agrees: the
 * history endpoint is per code, because that is what a trend line and a "what did it say
 * before" both read.
 *
 * # Why the list of codes comes from the patient rather than from the registry
 *
 * The registry knows every code the clinic can record, which is a hundred and forty things
 * this patient has never had measured. Offering all of them would make finding the height a
 * scroll, and choosing one that has never been recorded would answer with an empty screen that
 * looks like a failure. So the list is what this patient actually has a current value for, and
 * the registry supplies the words for each of them.
 *
 * # Why the code is named in words and never as its code
 *
 * `BODY_HEIGHT` on a physician's screen is a database identifier being shown to a clinician.
 * The registry carries both languages; where it has neither, the code stands rather than a
 * blank — a poor label beats a screen that has stopped saying what the number is.
 */
export interface ValueHistoryProps {
  patientId: string;
  /** Which code to open on. A caller arriving from a particular value can name it. */
  initialCode?: string;
}

export function ValueHistory({ patientId, initialCode }: ValueHistoryProps) {
  const t = useTranslations('corrections');
  const locale = useLocale() as Locale;

  const [chosen, setChosen] = useState<string | null>(initialCode ?? null);

  const current = useQuery({
    queryKey: patientObservationsKey(patientId),
    queryFn: () => listPatientObservations(patientId),
  });

  const codes = useQuery({
    queryKey: OBSERVATION_CODES_KEY,
    queryFn: listObservationCodes,
    staleTime: 60 * 60 * 1000,
  });

  if (current.isPending) return <Skeleton height="14rem" />;

  if (current.isError || current.data === undefined) {
    return (
      <AlertBanner tone="critical" title={t('history.unavailable')}>
        {t('history.unavailableBody')}
      </AlertBanner>
    );
  }

  function label(code: string): string {
    const entry = codeEntry(codes.data ?? [], code);
    if (entry === undefined) return code;
    return bilingual(entry.display_en, entry.display_bn, locale)?.text ?? code;
  }

  /*
   * One entry per code, in the reader's own alphabet. `Intl.Collator` rather than
   * `localeCompare` on a bare string: Bengali does not sort in Latin order, and a list of
   * measurements sorted by their English names would be unreadable to somebody reading Bangla.
   */
  const collator = new Intl.Collator(locale);
  const available = [...new Set(current.data.map((observation) => observation.code))].sort((a, b) =>
    collator.compare(label(a), label(b)),
  );

  if (available.length === 0) {
    return <EmptyState title={t('history.noValues')}>{t('history.noValuesBody')}</EmptyState>;
  }

  // The first code the reader would see, so the screen opens on something rather than on a
  // prompt. A caller's `initialCode` wins; a code this patient has no value for falls through
  // to the first available one rather than rendering an empty chain that reads as a failure.
  const code = chosen !== null && available.includes(chosen) ? chosen : (available[0] as string);

  return (
    <div className="app-stack" data-testid="value-history">
      <Select
        label={t('history.codeLabel')}
        description={t('history.codeHint')}
        value={code}
        data-testid="history-code"
        options={available.map((candidate) => ({ value: candidate, label: label(candidate) }))}
        onChange={(event) => setChosen(event.target.value)}
      />

      <ValueChain patientId={patientId} code={code} codeLabel={label(code)} />

      <DerivedValues patientId={patientId} code={code} codeLabel={label(code)} />
    </div>
  );
}
