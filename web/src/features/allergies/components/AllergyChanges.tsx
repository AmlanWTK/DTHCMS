'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';

import { AlertBanner, Skeleton } from '@dthcms/ui';

import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import {
  allergyChangesKey,
  isWithdrawn,
  listAllergyChanges,
  type AllergyChange,
  type AllergyReaction,
} from '../api/allergies';

import { changeSubject, reactionText } from './allergyText';

/**
 * Everything ever said about this patient's allergies (CP54).
 *
 * Withdrawn entries included, and they are the reason it exists: an allergy that was
 * recorded and then taken back is a clinical event — somebody believed it, somebody else
 * disagreed — and both halves are worth reading before writing a prescription. Nothing is
 * deleted anywhere in this feature, so nothing here filters.
 *
 * A withdrawn row keeps its own text legible rather than being struck through. Struck-out
 * text invites the eye to skip it, and the row that was taken back is often the one worth
 * reading slowly; the word "withdrawn", the reason and the person who wrote it carry the
 * state instead.
 *
 * Newest first, as the server returns it. Nothing is re-sorted.
 */

export interface AllergyChangesProps {
  patientId: string;
  /**
   * The reaction vocabulary, so a withdrawn row reads as "Itching" rather than "ITCHING".
   *
   * Passed in rather than fetched: the panel above already holds it, and a second query for
   * eight rows of reference data would be a round trip to say the same thing twice. Optional,
   * and the fallback is the code — a history that lost its words is worse than one that shows
   * them in shouting case.
   */
  reactions?: readonly AllergyReaction[];
}

export function AllergyChanges({ patientId, reactions = [] }: AllergyChangesProps) {
  const t = useTranslations('allergies');
  const locale = useLocale() as Locale;

  const changes = useQuery({
    queryKey: allergyChangesKey(patientId),
    queryFn: () => listAllergyChanges(patientId),
  });

  if (changes.isPending) return <Skeleton lines={4} />;

  if (changes.isError || !changes.data) {
    return <AlertBanner tone="unknown" title={t('changes.unavailable')} />;
  }

  if (changes.data.length === 0) {
    return (
      <p className="app-allergy-changes__none" data-testid="allergy-changes-empty">
        {t('changes.none')}
      </p>
    );
  }

  return (
    <section
      className="app-allergy-changes"
      aria-label={t('changes.title')}
      data-testid="allergy-changes"
    >
      <h3 className="app-allergy-changes__title">{t('changes.title')}</h3>
      <ul className="app-allergy-changes__list">
        {changes.data.map((change) => (
          <li key={`${change.kind}:${change.id}`}>
            <ChangeLine change={change} locale={locale} reactions={reactions} />
          </li>
        ))}
      </ul>
    </section>
  );
}

function ChangeLine({
  change,
  locale,
  reactions,
}: {
  change: AllergyChange;
  locale: Locale;
  reactions: readonly AllergyReaction[];
}) {
  const t = useTranslations('allergies');
  const undone = isWithdrawn(change);

  return (
    <article
      className="app-allergy-change"
      data-testid={`allergy-change-${change.id}`}
      data-kind={change.kind}
      data-withdrawn={undone}
    >
      <p className="app-allergy-change__what">
        <span className="app-allergy-change__kind">{t(`changes.kind.${change.kind}`)}</span>{' '}
        <span className="app-allergy-change__subject">
          {changeSubject(change, t(`changes.kind.${change.kind}`))}
        </span>
      </p>

      {change.reaction && (
        <p className="app-allergy-change__detail">
          {t('changes.reaction', { reaction: reactionText(change.reaction, reactions, locale) })}
        </p>
      )}

      {/* The certainty on an allergy; the reason on an assertion. The server sends one
          field because a reader wants one sentence, not a schema. */}
      {change.detail && <p className="app-allergy-change__detail">{change.detail}</p>}

      <p className="app-allergy-change__attribution">
        {t('changes.by', { at: formatDateTime(Date.parse(change.at), locale), who: change.by })}
      </p>

      {undone && (
        <p className="app-allergy-change__undone">
          {t('changes.withdrawn', {
            at: formatDateTime(Date.parse(change.undone_at as string), locale),
            who: change.undone_by ?? '',
          })}{' '}
          {change.undone_why ?? ''}
        </p>
      )}
    </article>
  );
}
