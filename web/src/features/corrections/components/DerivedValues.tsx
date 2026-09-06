'use client';

import { useQueries, useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';

import { AlertBanner, Skeleton } from '@dthcms/ui';

import {
  OBSERVATION_CODES_KEY,
  ObservationValue,
  chainsOf,
  codeEntry,
  computedFromCurrent,
  derivedFrom,
  listObservationCodes,
  listObservationHistory,
  listPatientObservations,
  observationHistoryKey,
  patientObservationsKey,
} from '@/features/observations';
import { bilingual } from '@/lib/bilingual';
import type { Locale } from '@/lib/i18n/config';

/**
 * What moved when this value was corrected (CP62, criterion 3 and 4).
 *
 * # The question this answers, and why the chain view cannot answer it
 *
 * A corrected height changes a BMI. The server does that inside the correction's own
 * transaction — a corrected height with a stale BMI beside it is worse than either, because it
 * is two numbers that disagree, both looking equally official — and it does it by writing a
 * **new** derived value that supersedes the old one rather than editing it, so the old answer
 * stays in the record too.
 *
 * None of that is visible on the height's own chain. A physician who has just watched a height
 * change from 150 to 140 is left to wonder whether the BMI beside it recomputed, and the only
 * ways to find out without this panel are to open another screen and compare arithmetic in
 * their head, or to assume. Both are worse than a sentence.
 *
 * # Why this reads `inputs` rather than a table of which formula reads what
 *
 * The server knows that BMI reads height; this client does not, and a copy of that table here
 * would be a second opinion that disagrees the day a formula changes. `inputs` is the record
 * of what this particular derived value **was given**, stored beside it when it was computed.
 * A BMI naming `BODY_HEIGHT` in its inputs was computed from this patient's height, and that
 * is a fact rather than an inference.
 *
 * # Why it compares the input against the value that stands now
 *
 * The cascade names a derivation it could not recompute rather than failing the correction —
 * its other input may have gone, or the formula may refuse the new number — and that failure
 * does not reach the contract in any field. So this compares what the derived value says it saw
 * against what the code now says: agreeing means it moved with the correction, disagreeing
 * means it did not, and that is a statement about two numbers in the record rather than a guess
 * about a transaction.
 *
 * Where either number is missing the answer is "the record does not say", in words. A boolean
 * there would round "we cannot tell" up to "it is fine", which is the one answer a physician
 * must not be given about a value they are about to act on.
 */
export interface DerivedValuesProps {
  patientId: string;
  code: string;
  /** What the corrected code is, in words. Named in every sentence below. */
  codeLabel: string;
}

export function DerivedValues({ patientId, code, codeLabel }: DerivedValuesProps) {
  const t = useTranslations('corrections');
  const locale = useLocale() as Locale;

  const current = useQuery({
    queryKey: patientObservationsKey(patientId),
    queryFn: () => listPatientObservations(patientId),
  });

  const codes = useQuery({
    queryKey: OBSERVATION_CODES_KEY,
    queryFn: listObservationCodes,
    staleTime: 60 * 60 * 1000,
  });

  const dependents = derivedFrom(current.data ?? [], code);
  const standing = (current.data ?? []).find((observation) => observation.code === code);

  /*
   * One history per derived code, so the superseded predecessor can be shown beside the value
   * that replaced it. `useQueries` rather than a query per component because the number of
   * dependents is data — a patient with a waist and a hip has a waist-to-hip ratio and one
   * without has none — and a component per row would make the hook count depend on the
   * response.
   */
  const histories = useQueries({
    queries: dependents.map((dependent) => ({
      queryKey: observationHistoryKey(patientId, dependent.code),
      queryFn: () => listObservationHistory(patientId, dependent.code),
    })),
  });

  if (current.isPending) return <Skeleton height="6rem" />;

  if (current.isError) {
    // Said rather than drawn as an empty section. "Nothing was computed from this value" and
    // "we could not find out" are different facts, and only one of them is safe to imply.
    return (
      <AlertBanner tone="borderline" title={t('derived.unavailable')}>
        {t('derived.unavailableBody')}
      </AlertBanner>
    );
  }

  return (
    <section
      className="app-corrections__derived"
      aria-label={t('derived.title')}
      data-testid="derived-values"
    >
      <h3 className="app-corrections__heading">{t('derived.title')}</h3>
      <p className="app-page__description">{t('derived.body', { what: codeLabel })}</p>

      {dependents.length === 0 ? (
        <p className="app-corrections__hint" data-testid="derived-none">
          {t('derived.none', { what: codeLabel })}
        </p>
      ) : (
        <ul className="app-corrections__derived-list">
          {dependents.map((dependent, index) => {
            const entry = codeEntry(codes.data ?? [], dependent.code);
            const label =
              entry === undefined
                ? dependent.code
                : (bilingual(entry.display_en, entry.display_bn, locale)?.text ?? dependent.code);

            const agrees = computedFromCurrent(dependent, code, standing);
            const history = histories[index];
            const chain = chainsOf(history?.data ?? []).find((candidate) =>
              candidate.some((observation) => observation.id === dependent.id),
            );
            const predecessors = (chain ?? []).filter(
              (observation) => observation.id !== dependent.id,
            );

            return (
              <li
                key={dependent.id}
                className="app-corrections__derived-row"
                data-testid={`derived-${dependent.code}`}
                data-current={agrees === null ? 'unknown' : String(agrees)}
              >
                <div className="app-corrections__version-value">
                  <ObservationValue
                    observation={dependent}
                    label={label}
                    testId={`derived-value-${dependent.code}`}
                  />
                  <span className="app-corrections__derived-name">{label}</span>
                </div>

                <p
                  className="app-corrections__derived-state"
                  data-testid={`derived-state-${dependent.code}`}
                >
                  {agrees === null
                    ? t('derived.unknownInput', { what: codeLabel })
                    : agrees
                      ? t('derived.moved', { what: codeLabel })
                      : t('derived.stale', { what: codeLabel })}
                </p>

                {predecessors.length > 0 && (
                  <div
                    className="app-corrections__derived-previous"
                    data-testid={`derived-previous-${dependent.code}`}
                  >
                    <p className="app-corrections__hint">{t('derived.previousKept')}</p>
                    <ul className="app-corrections__versions">
                      {predecessors.map((previous) => (
                        <li key={previous.id} className="app-corrections__version">
                          <ObservationValue
                            observation={previous}
                            label={label}
                            testId={`derived-previous-value-${previous.id}`}
                          />
                          <span className="app-corrections__version-state">
                            {t('state.SUPERSEDED')}
                          </span>
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
