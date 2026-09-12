'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Card, EmptyState, Icon, Select, Skeleton } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import {
  listRules,
  rulesKey,
  type RuleFilter,
  type RuleSummary,
  type RuleType,
} from '../api/rules';
import { AllergenPanel } from './AllergenPanel';
import { RuleIO } from './RuleIO';
import { RuleWorkspace } from './RuleWorkspace';
import { ruleState } from './rulesText';

/**
 * The rule library (CP77, §6.3, D-22).
 *
 * # The banner at the top is the point of this screen
 *
 * Forty rules ship drafted from published guidance and none of them does anything. That is the
 * honest state of the system and it is also a to-do list forty items long, so the screen says it
 * in a sentence before it says anything else, and "waiting for you" is a filter rather than
 * something to assemble by reading down a column.
 *
 * A library that opened on a full-looking list of rules would let a physician conclude the safety
 * checking was done. Nothing in this system is more dangerous than that conclusion.
 *
 * # Why the whole thing is one screen with panels rather than four routes
 *
 * Authoring, testing and publishing are one sitting. A physician who has to navigate away to test
 * a rule and back to publish it loses what he was looking at, and the acceptance criterion is
 * that he can do all three unaided.
 */
export function RuleLibrary() {
  const t = useTranslations('medicationRules');
  const locale = useLocale() as Locale;

  const [filter, setFilter] = useState<RuleFilter>({ type: '', unapprovedOnly: false });
  const [openRule, setOpenRule] = useState<string | null>(null);

  const rules = useQuery({ queryKey: rulesKey(filter), queryFn: () => listRules(filter) });
  const all = useQuery({
    queryKey: rulesKey({ type: '', unapprovedOnly: true }),
    queryFn: () => listRules({ type: '', unapprovedOnly: true }),
  });

  const waiting = all.data?.total ?? 0;

  return (
    <div className="app-stack">
      {waiting > 0 ? (
        <AlertBanner tone="borderline" title={t('unapprovedBanner', { count: waiting })}>
          <p>{t('unapprovedNotice')}</p>
        </AlertBanner>
      ) : null}

      <Card>
        <div className="rules-filters">
          <Select
            label={t('filter.type')}
            value={filter.type}
            onChange={(e) => setFilter({ ...filter, type: e.target.value as RuleType | '' })}
            placeholder={t('filter.allTypes')}
            options={[
              ...(
                [
                  'INTERACTION',
                  'CONTRAINDICATION',
                  'RENAL',
                  'HEPATIC',
                  'PREGNANCY',
                  'PAEDIATRIC',
                  'DUPLICATE_THERAPY',
                  'MAX_DOSE',
                ] as RuleType[]
              ).map((value) => ({ value, label: t(`kind.${value}`) })),
            ]}
          />
          <Select
            label={t('filter.show')}
            value={filter.unapprovedOnly ? 'unapproved' : 'all'}
            onChange={(e) =>
              setFilter({ ...filter, unapprovedOnly: e.target.value === 'unapproved' })
            }
            options={[
              { value: 'all', label: t('filter.all') },
              { value: 'unapproved', label: t('filter.unapproved') },
            ]}
          />
        </div>

        {rules.isPending ? <Skeleton /> : null}

        {rules.data && rules.data.items.length === 0 ? (
          <EmptyState title={t('list.empty')}>{t('list.emptyBody')}</EmptyState>
        ) : null}

        {rules.data && rules.data.items.length > 0 ? (
          <>
            <p className="rules-count">
              {t('list.showing', { shown: rules.data.items.length, total: rules.data.total })}
            </p>
            <ul className="rules-list">
              {rules.data.items.map((rule) => (
                <RuleRow
                  key={rule.id}
                  rule={rule}
                  locale={locale}
                  open={openRule === rule.id}
                  onToggle={() => setOpenRule(openRule === rule.id ? null : rule.id)}
                />
              ))}
            </ul>
          </>
        ) : null}
      </Card>

      <AllergenPanel />
      <RuleIO />
    </div>
  );
}

function RuleRow({
  rule,
  locale,
  open,
  onToggle,
}: {
  rule: RuleSummary;
  locale: Locale;
  open: boolean;
  onToggle: () => void;
}) {
  const t = useTranslations('medicationRules');
  const state = ruleState(rule);
  const name = locale === 'bn' ? rule.name_bn : rule.name_en;

  return (
    <li className="rules-row" data-state={state}>
      <button type="button" className="rules-row-head" onClick={onToggle} aria-expanded={open}>
        <Icon name={open ? 'chevron-down' : 'chevron-right'} size={16} />
        {/* The code stays in Latin script in both interfaces: it is what a finding quotes and
            what one person says to another across a room. */}
        <code className="rules-code">{rule.code}</code>
        <span className="rules-name">{name}</span>
        <span className="rules-kind">{t(`kind.${rule.type}`)}</span>
        {/* The severity is drawn as what it does — "Stops the prescription" — rather than as
            the word BLOCK. A label somebody has to be taught is a label people skim past. */}
        <span className="rules-severity" data-severity={rule.severity}>
          {t(`severity.${rule.severity}`)}
        </span>
        {/* Colour, an icon and a word — never colour alone, per the design system. The one that
            matters is `unapproved`: a rule nobody has agreed to must not look like one somebody
            has. */}
        <span className="rules-state" data-state={state}>
          <Icon
            name={
              state === 'approved'
                ? 'check'
                : state === 'withdrawn'
                  ? 'x'
                  : state === 'draft'
                    ? 'scroll-text'
                    : 'alert-triangle'
            }
            size={14}
          />
          {t(`state.${state}`)}
        </span>
      </button>

      {open ? <RuleWorkspace ruleId={rule.id} /> : null}
    </li>
  );
}
