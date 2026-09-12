'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';

import { AlertBanner, Button, Card, Icon } from '@dthcms/ui';

import { StepUpCancelled, useStepUp } from '@/features/auth';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import { ALLERGENS_KEY, approveCrossReaction, getAllergens } from '../api/rules';

/**
 * Allergen groups and the cross-reactivity map (CP77; CP78 is what will use them).
 *
 * # Why the sulfonamide row is the one to read
 *
 * Most prescribers believe a sulfonamide antibiotic allergy means avoiding sulphonylureas,
 * thiazides and furosemide. The evidence does not support it — the cohort that looked found the
 * raised risk was explained by a general predisposition to allergy rather than by the sulfonamide
 * group — and the cost of the belief is a diabetic patient who is not offered a useful drug.
 *
 * A map that simply omitted the row would leave the belief in place. So the risk is recorded as
 * `NONE`, with the paper, and the row is here to be argued with.
 *
 * # Every row is unapproved, like every rule
 *
 * I drafted these from the literature. Until Dr. Nahid approves one, CP78 will not expand an
 * allergy through it, and the screen says so rather than drawing it as settled.
 */
export function AllergenPanel() {
  const t = useTranslations('medicationRules');
  const locale = useLocale() as Locale;
  const queryClient = useQueryClient();
  const requestStepUp = useStepUp();
  const mayApprove = usePermission('medicationRules.publish');

  const allergens = useQuery({ queryKey: ALLERGENS_KEY, queryFn: getAllergens });

  const approve = useMutation({
    mutationFn: async (id: string) => {
      const token = await requestStepUp('medication_rule.publish', t('allergens.approve'));
      return approveCrossReaction(id, token);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ALLERGENS_KEY });
    },
    onError: (error) => {
      if (error instanceof StepUpCancelled) return;
    },
  });

  if (!allergens.data) return null;
  const unapproved = allergens.data.cross_reactions.filter((c) => !c.approved).length;

  return (
    <Card>
      <h3>{t('allergens.title')}</h3>
      <p className="app-page__description">{t('allergens.lede')}</p>

      {unapproved > 0 ? (
        <AlertBanner tone="borderline" title={t('unapprovedBanner', { count: unapproved })} />
      ) : null}

      <ul className="rules-cross">
        {allergens.data.cross_reactions.map((c) => (
          <li key={c.id} data-approved={c.approved}>
            <p className="rules-cross-head">
              <strong>{c.from_group}</strong> <Icon name="chevron-right" size={14} />{' '}
              <strong>{c.to_group}</strong>
              <span className="rules-risk" data-risk={c.risk}>
                {t(`risk.${c.risk}`)}
              </span>
              <span className="rules-state" data-state={c.approved ? 'approved' : 'unapproved'}>
                <Icon name={c.approved ? 'check' : 'alert-triangle'} size={14} />
                {t(c.approved ? 'state.approved' : 'state.unapproved')}
              </span>
            </p>
            <p lang={locale}>{locale === 'bn' ? c.note_bn : c.note_en}</p>
            <p className="rules-needs">
              {t('source')}: {c.source}
            </p>
            {!c.approved && mayApprove ? (
              <Button variant="secondary" size="sm" onClick={() => approve.mutate(c.id)}>
                {t('allergens.approve')}
              </Button>
            ) : null}
          </li>
        ))}
      </ul>

      <ul className="rules-groups">
        {allergens.data.groups.map((g) => (
          <li key={g.code}>
            <strong>{g.code}</strong> — {locale === 'bn' ? g.name_bn : g.name_en}
            <span className="rules-needs">
              {' '}
              {t('allergens.members')}: {g.members.map((m) => m.value).join(', ')}
            </span>
          </li>
        ))}
      </ul>
    </Card>
  );
}
