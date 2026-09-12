'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Button, Card, Input, Skeleton } from '@dthcms/ui';

import { bilingual } from '@/lib/bilingual';
import { formatCount, formatDate, formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import {
  FORMULARY_KEY,
  REVIEW_KEY,
  completeReview,
  daysOverdue,
  getReview,
} from '../api/formulary';

/**
 * The monthly price review (CP75 criterion 4, §16.1).
 *
 * # The number this screen exists to show
 *
 * **How many prices nobody has checked.** Everything else on the panel is context for it. The
 * clinic opens today with 250 of 250 in that state, because all 250 are published MRP the
 * migration loaded and Dr. Nahid has not returned the reviewed prices — and a screen that led
 * with "250 medicines in the formulary" would read as a healthy, finished system.
 *
 * # Who owns it, drawn as a person and as a role
 *
 * §16.1 asks who owns monthly price review, and the answer this module models is both: a role,
 * which survives the pharmacist leaving, and optionally a named person, which is what actually
 * makes it happen. The panel draws whichever exist, and says plainly when only the role does —
 * "the pharmacists" is a legitimate answer and it is also the shape in which a recurring task
 * quietly stops being done.
 *
 * # An unreminded cycle says so
 *
 * `reminded_at` can be null: the cycle exists and nobody has been told. That is the failure this
 * criterion is about, so it is a banner rather than an absence.
 */
export function ReviewPanel() {
  const t = useTranslations('formulary');
  const locale = useLocale() as Locale;
  const queryClient = useQueryClient();
  const mayReview = usePermission('formulary.review');
  const [note, setNote] = useState('');

  const review = useQuery({ queryKey: REVIEW_KEY, queryFn: getReview });

  const complete = useMutation({
    mutationFn: (id: string) => completeReview(id, note),
    onSuccess: () => {
      setNote('');
      void queryClient.invalidateQueries({ queryKey: FORMULARY_KEY });
    },
  });

  if (review.isPending) return <Skeleton />;
  if (!review.data) return null;

  const state = review.data;
  const current = state.current;
  const overdue = daysOverdue(current, new Date());
  const owner = bilingual(state.owner.owner_name_en ?? '', state.owner.owner_name_bn ?? '', locale);
  const role = bilingual(state.owner.owner_role_name_en, state.owner.owner_role_name_bn, locale);

  return (
    <Card className="formulary-review">
      <h2 className="app-card__title">{t('review.title')}</h2>

      <div className="formulary-review__figures">
        <div className="formulary-review__figure formulary-review__figure--lead">
          <span className="formulary-review__number">{formatCount(state.unverified, locale)}</span>
          <span className="formulary-review__caption">
            {t('review.unverified', { total: formatCount(state.products, locale) })}
          </span>
        </div>
        <div className="formulary-review__figure">
          <span className="formulary-review__number">{formatCount(state.products, locale)}</span>
          <span className="formulary-review__caption">{t('review.products')}</span>
        </div>
        <div className="formulary-review__figure">
          <span className="formulary-review__number">
            {formatCount(state.oldest_price_days, locale)}
          </span>
          <span className="formulary-review__caption">{t('review.oldest')}</span>
        </div>
      </div>

      {/* The seeded-prices warning. Drawn whenever anything is unverified, because the thing a
          reader must not conclude from a full-looking formulary is that it has been checked. */}
      {state.unverified > 0 ? (
        <AlertBanner tone="borderline" title={t('review.provisionalTitle')}>
          {t('review.provisionalBody', { count: formatCount(state.unverified, locale) })}
        </AlertBanner>
      ) : null}

      <dl className="formulary-review__owner">
        <dt>{t('review.owner')}</dt>
        <dd>
          {owner ? (
            <>
              <strong lang={owner.lang}>{owner.text}</strong>{' '}
              <span className="formulary-review__role">({role?.text})</span>
            </>
          ) : (
            // Role only. Said in words rather than left as a bare role name, because "the
            // pharmacists own this" is a real answer and is also the shape in which a monthly
            // task stops having anybody in particular who has failed to do it.
            <>
              <strong>{role?.text}</strong>{' '}
              <span className="formulary-review__role">{t('review.roleOnly')}</span>
            </>
          )}
        </dd>
        <dt>{t('review.due')}</dt>
        <dd>{t('review.dueDay', { day: state.owner.due_day_of_month })}</dd>
      </dl>

      {current ? (
        <div className="formulary-review__cycle">
          <p className="formulary-review__period">
            {t('review.cycle', {
              month: formatDate(new Date(`${current.period_month}T00:00:00Z`), locale),
              status: t(`review.status.${current.status === 'OPEN' ? 'open' : 'complete'}`),
            })}
          </p>
          {overdue !== null ? (
            <AlertBanner tone="high" title={t('review.overdueTitle')}>
              {t('review.overdueBody', { days: formatCount(overdue, locale) })}
            </AlertBanner>
          ) : null}
          {current.status === 'OPEN' && !current.reminded_at ? (
            <AlertBanner tone="unknown" title={t('review.notRemindedTitle')}>
              {t('review.notRemindedBody')}
            </AlertBanner>
          ) : null}
          {current.reminded_at ? (
            <p className="formulary-review__reminded">
              {t('review.reminded', {
                when: formatDateTime(new Date(current.reminded_at), locale),
              })}
            </p>
          ) : null}
          {current.status === 'COMPLETE' && current.completed_at ? (
            <p className="formulary-review__reminded">
              {t('review.completed', {
                who:
                  bilingual(
                    current.completed_by_name_en ?? '',
                    current.completed_by_name_bn ?? '',
                    locale,
                  )?.text ?? '—',
                when: formatDateTime(new Date(current.completed_at), locale),
              })}
            </p>
          ) : null}

          {mayReview && current.status === 'OPEN' ? (
            <form
              className="formulary-review__complete"
              onSubmit={(event) => {
                event.preventDefault();
                complete.mutate(current.id);
              }}
            >
              <Input
                label={t('review.note')}
                description={t('review.noteHelp')}
                value={note}
                onChange={(event) => setNote(event.target.value)}
              />
              <Button type="submit" variant="secondary" disabled={complete.isPending}>
                {complete.isPending ? t('review.completing') : t('review.complete')}
              </Button>
            </form>
          ) : null}
        </div>
      ) : (
        <p className="formulary-review__reminded">{t('review.noCycle')}</p>
      )}
    </Card>
  );
}
