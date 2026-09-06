'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';

import { AlertBanner, Card, Skeleton } from '@dthcms/ui';

import { bilingual } from '@/lib/bilingual';
import type { Locale } from '@/lib/i18n/config';

import { QUALITY_THRESHOLDS_KEY, listThresholds, thresholdsInOrder } from '../api/quality';

/**
 * What raises a flag, readable by the person a flag would be raised on (CP63, ADR-0029 §3).
 *
 * # Why this is on the operator's own screen
 *
 * The endpoint needs a session and no permission, deliberately: *an operator who can see a
 * flag on their own record should be able to read what raised it without asking a supervisor
 * to explain*. A rule somebody is measured against and cannot read is a rule they will assume
 * is worse than it is, and that assumption is the whole mechanism by which a quality record
 * starts making people hide their corrections.
 *
 * # The sentence is the server's, in both languages, and is not rebuilt here
 *
 * `looks_for_en` / `looks_for_bn` are rendered from the row rather than stored, so numbers that
 * change cannot leave a sentence describing the old ones — and the Bangla carries Bengali
 * numerals, because a Latin numeral inside a Bangla sentence is the thing that makes an
 * interface read as translated rather than written, and the numbers are the part of this
 * sentence a reader is actually looking for.
 *
 * An earlier version of this file rebuilt that sentence from `min_count`, `window_days`,
 * `min_entries` and `after_hour` in the message files, because the endpoint returned English
 * only. It is deleted rather than kept as a fallback: two sources for the sentence describing a
 * rule would mean the screen and the database eventually describe different rules, and this is
 * the screen an operator reads to find out what is being measured about them.
 *
 * The sentence carries the floor under the denominator, which is the humane half of it: nobody
 * is flagged on their first morning, because three corrections out of five entries is a new
 * operator learning the station, and flagging them for retraining then is how a clinic teaches
 * its staff to stop asking for help.
 *
 * # Every one of them is a proposal
 *
 * `approved: false` on everything this system ships with, and it is said once at the top of
 * this list and again on each row. The plan lists the numbers as an open decision requiring
 * clinical approval; until somebody makes it, these are a demonstration of the mechanism.
 *
 */
export function ThresholdList() {
  const t = useTranslations('quality');
  const locale = useLocale() as Locale;

  const thresholds = useQuery({
    queryKey: QUALITY_THRESHOLDS_KEY,
    queryFn: listThresholds,
    // Reference data with no patient and no person in it. Once an hour is generous.
    staleTime: 60 * 60 * 1000,
  });

  if (thresholds.isPending) return <Skeleton height="10rem" />;

  if (thresholds.isError || thresholds.data === undefined) {
    return (
      // Not silence. A screen that quietly omits the rules is a screen that looks as though
      // there are none, on the one surface built to make them readable.
      <AlertBanner tone="borderline" title={t('thresholds.unavailable')}>
        {t('thresholds.unavailableBody')}
      </AlertBanner>
    );
  }

  const rows = thresholdsInOrder(thresholds.data);
  const anyApproved = rows.some((row) => row.threshold.approved);

  return (
    <Card elevation="raised" as="section">
      <h2 className="app-quality__section">{t('thresholds.title')}</h2>
      <p className="app-page__description">{t('thresholds.body')}</p>

      {!anyApproved && rows.length > 0 && (
        <AlertBanner tone="borderline" title={t('thresholds.allProposalsTitle')}>
          {t('thresholds.allProposalsBody')}
        </AlertBanner>
      )}

      <ul className="app-quality__thresholds" data-testid="threshold-list">
        {rows.map(({ threshold, looks_for_en, looks_for_bn }) => {
          const display = bilingual(threshold.display_en, threshold.display_bn, locale);
          const looksFor = bilingual(looks_for_en, looks_for_bn, locale);
          const action = bilingual(threshold.action_en, threshold.action_bn, locale);
          return (
            <li
              key={threshold.code}
              data-testid={`threshold-${threshold.code}`}
              data-approved={String(threshold.approved)}
            >
              <p className="app-quality__threshold-title">
                {display === null ? (
                  <code>{threshold.code}</code>
                ) : (
                  <span lang={display.lang}>{display.text}</span>
                )}
              </p>

              {looksFor !== null && (
                <p
                  className="app-quality__hint"
                  lang={looksFor.lang}
                  data-testid={`threshold-looks-for-${threshold.code}`}
                >
                  {looksFor.text}
                </p>
              )}

              {action !== null && (
                <p className="app-quality__threshold-action" lang={action.lang}>
                  {action.text}
                </p>
              )}

              {!threshold.approved && (
                <p
                  className="app-quality__hint"
                  data-testid={`threshold-proposal-${threshold.code}`}
                >
                  {t('thresholds.proposal')}
                </p>
              )}
            </li>
          );
        })}
      </ul>
    </Card>
  );
}
