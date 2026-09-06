'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Button, Card, EmptyState, Skeleton } from '@dthcms/ui';

import { PersonLine } from '@/features/corrections';
import type { Locale } from '@/lib/i18n/config';

import { listOperators, operatorsInOrder, operatorsKey, type OperatorOrder } from '../api/quality';

import { CorrectionRate } from './CorrectionRate';
import { qualityDate } from './qualityText';

/**
 * Everybody who recorded anything, with what came back (CP63).
 *
 * # The decision this file exists to record: what order the rows go in
 *
 * The endpoint used to sort by correction count, descending, and rendering that unchanged under
 * names would have been the single most damaging thing this checkpoint could ship. **Position
 * in a list is itself a claim.** A named list ordered by a count of somebody's corrections
 * reads top-to-bottom as a ranking of the worst, whatever the column headings say and however
 * many denominators are printed beside the numbers.
 *
 * The server now orders by employee code, which asserts nothing. This screen sorts by **name**
 * instead — the same absence of a claim, in the thing the reader is actually looking at: a list
 * ordered by a code nobody sees reads as unordered, and a reader who cannot find the row they
 * came for will reach for the one control that does order it.
 *
 * Sorting by rate was considered and rejected. It is the better statistic and it is not a
 * neutral order: it is the same ranking with fairer arithmetic, and it inverts in the wrong
 * direction — three corrections out of twenty-five sorts above eight out of nine hundred, so a
 * new operator lands at the top of a list a supervisor reads as a priority order. It is also
 * null below the entry floor, and nulls in a sort have to go somewhere, which is one more claim
 * nobody meant to make.
 *
 * A supervisor who wants the busiest correction record first can ask for it, with a control
 * that says in words what it is about to do. Choosing to look at it that way is a legitimate
 * act; being handed it without asking is what turns a record into a league table.
 *
 * # The roster is the other half of the same argument
 *
 * The list now includes everybody who recorded anything in the window, the operators with four
 * hundred entries and nothing corrected among them. Those rows are not padding: a list every
 * row of which has at least one correction is structurally an accusation, whatever it is titled
 * and however carefully each line is annotated. `only_corrected` narrows it, offered here as a
 * control that says what it does — because "show me only the people something came back on" is
 * a reasonable question and a different act from being handed that list by default.
 *
 * # Every line carries the whole outcome, not just the count
 *
 * `entries` and `rate` beside `corrections`, and beneath them the split: put right by the
 * operator, put right by a supervisor, stood, still waiting. The rate is the same arithmetic
 * the record uses, so a supervisor can compare a line here with the record they open next
 * without either number needing a footnote.
 */
export interface OperatorQualityListProps {
  days: number;
  /** Opens one person's record. Named for what it does, not for what it might be used to do. */
  onOpen: (operatorId: string) => void;
}

export function OperatorQualityList({ days, onOpen }: OperatorQualityListProps) {
  const t = useTranslations('quality');
  const tCorrection = useTranslations('corrections');
  const locale = useLocale() as Locale;

  const [order, setOrder] = useState<OperatorOrder>('by-name');
  const [onlyCorrected, setOnlyCorrected] = useState(false);

  const operators = useQuery({
    queryKey: operatorsKey(days, onlyCorrected),
    queryFn: () => listOperators(days, { onlyCorrected }),
  });

  if (operators.isPending) return <Skeleton height="12rem" />;

  if (operators.isError || operators.data === undefined) {
    return (
      <AlertBanner tone="critical" title={t('operators.unavailable')}>
        {t('operators.unavailableBody')}
      </AlertBanner>
    );
  }

  const { operators: rows, window } = operators.data;
  const ordered = operatorsInOrder(rows, order, locale);

  return (
    <section className="app-stack" data-testid="operator-list">
      <h2 className="app-quality__section">{t('operators.title')}</h2>
      <p className="app-page__description">
        {onlyCorrected ? t('operators.narrowed') : t('operators.roster')}
      </p>
      <p className="app-quality__hint" data-testid="operators-window">
        {t('window.covering', {
          from: qualityDate(Date.parse(window.from), locale),
          to: qualityDate(Date.parse(window.to), locale),
        })}
      </p>

      <div
        className="app-quality__order"
        role="group"
        aria-label={t('operators.showLabel')}
        data-testid="operator-scope"
      >
        <Button
          variant={onlyCorrected ? 'quiet' : 'secondary'}
          aria-pressed={!onlyCorrected}
          data-testid="scope-everybody"
          onClick={() => setOnlyCorrected(false)}
        >
          {t('operators.scopeEverybody')}
        </Button>
        <Button
          variant={onlyCorrected ? 'secondary' : 'quiet'}
          aria-pressed={onlyCorrected}
          data-testid="scope-corrected"
          onClick={() => setOnlyCorrected(true)}
        >
          {t('operators.scopeCorrected')}
        </Button>
      </div>

      {rows.length === 0 ? (
        <EmptyState icon="check" title={t('operators.noneTitle')}>
          {t('operators.noneBody')}
        </EmptyState>
      ) : (
        <>
          <div
            className="app-quality__order"
            role="group"
            aria-label={t('operators.orderLabel')}
            data-testid="operator-order"
          >
            <Button
              variant={order === 'by-name' ? 'secondary' : 'quiet'}
              aria-pressed={order === 'by-name'}
              data-testid="order-by-name"
              onClick={() => setOrder('by-name')}
            >
              {t('operators.orderByName')}
            </Button>
            <Button
              variant={order === 'by-corrections' ? 'secondary' : 'quiet'}
              aria-pressed={order === 'by-corrections'}
              data-testid="order-by-corrections"
              onClick={() => setOrder('by-corrections')}
            >
              {t('operators.orderByCorrections')}
            </Button>
          </div>

          <ul className="app-quality__operators" data-order={order} data-testid="operator-rows">
            {ordered.map((operator) => {
              const whole = operator.corrections;
              return (
                <li key={operator.operator_id} data-testid={`operator-${operator.operator_id}`}>
                  <Card elevation="raised" as="article" compact>
                    <p className="app-quality__operator-name">
                      <PersonLine
                        nameEn={operator.name_en}
                        nameBn={operator.name_bn}
                        code={operator.employee_code}
                        id={operator.operator_id}
                        testId={`operator-name-${operator.operator_id}`}
                      />
                    </p>

                    {operator.status !== undefined && operator.status !== 'active' && (
                      // Deactivated staff stay on the list, because much of what a review asks
                      // about is somebody who has since left. Saying so stops a supervisor going
                      // to look for a colleague who is not there.
                      <p className="app-quality__hint">
                        {operator.status === 'deactivated' || operator.status === 'suspended'
                          ? tCorrection(`person.presence.${operator.status}`)
                          : tCorrection('person.presence.other', { status: operator.status })}
                      </p>
                    )}

                    <p className="app-quality__operator-counts">
                      <CorrectionRate
                        corrections={operator.corrections}
                        entries={operator.entries}
                        rate={operator.rate}
                        rateFloor={operator.rate_floor}
                        inline
                        testId={`operator-rate-${operator.operator_id}`}
                      />
                    </p>

                    {operator.corrections > 0 && (
                      // The split, on the line rather than a click away. `rejected` beside
                      // `upheld` is the rule this feature exists to keep, and it is only
                      // keepable here because the row now carries both.
                      <ul
                        className="app-quality__outcomes"
                        data-testid={`operator-outcomes-${operator.operator_id}`}
                      >
                        <li>{t('outcome.upheld', { part: operator.upheld, whole })}</li>
                        <li>{t('outcome.overridden', { part: operator.overridden, whole })}</li>
                        <li>{t('outcome.rejected', { part: operator.rejected, whole })}</li>
                        <li>{t('outcome.open', { part: operator.open, whole })}</li>
                      </ul>
                    )}

                    {operator.open_flags > 0 && (
                      <p className="app-quality__operator-flags">
                        {t('operators.openFlags', { count: operator.open_flags })}
                      </p>
                    )}

                    <Button
                      variant="quiet"
                      data-testid={`open-${operator.operator_id}`}
                      onClick={() => onOpen(operator.operator_id)}
                    >
                      {t('operators.openRecord')}
                    </Button>
                  </Card>
                </li>
              );
            })}
          </ul>
        </>
      )}
    </section>
  );
}
