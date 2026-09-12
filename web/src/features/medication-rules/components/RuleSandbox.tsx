'use client';

import { useMutation } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Button, Card, Icon, Input, Select } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import { runSandbox, type RuleDraft, type RuleType, type SandboxResult } from '../api/rules';
import { plainText, toList } from './rulesText';

/**
 * The rule-testing sandbox (CP77, acceptance criterion 4).
 *
 * # The test patient is typed in, and that is the design rather than a shortcut
 *
 * A physician testing a renal rule needs eGFR 29, and eGFR 31, and no eGFR at all — and no
 * patient in the register is all three. Picking a real patient would also put a clinical picture
 * through a screen that does not otherwise hold one, and this is precisely where somebody would
 * later add a log line.
 *
 * So the patient is four numbers and three lists, and nothing about them leaves this request.
 *
 * # It says out loud that nothing here is happening
 *
 * The banner above the result is not decoration. A physician who has just watched a rule block a
 * prescription needs to be told, in the same glance, that it blocked a prescription that does not
 * exist — and that until he publishes it, it will go on blocking nothing.
 *
 * # It shows the working
 *
 * Each test, its answer, and why, in his own language. A verdict with no working is a verdict he
 * has to take on trust, and the rules he does not trust are the ones he will not publish — which
 * is the way this whole checkpoint fails.
 */
export interface RuleSandboxProps {
  type: RuleType;
  versionId: string;
  /** True while the editor holds changes the server has not got. */
  dirty: boolean;
  draft: RuleDraft;
}

interface TestDrug {
  label: string;
  generic: string;
  class: string;
  daily_dose?: string;
  dose_unit?: string;
}

export function RuleSandbox({ type, versionId, dirty, draft }: RuleSandboxProps) {
  const t = useTranslations('medicationRules');
  const locale = useLocale() as Locale;

  const [age, setAge] = useState('');
  const [egfr, setEgfr] = useState('');
  const [pregnancy, setPregnancy] = useState<
    '' | 'PREGNANT' | 'BREASTFEEDING' | 'PLANNING' | 'NOT_PREGNANT'
  >('');
  const [hepatic, setHepatic] = useState<'' | 'NONE' | 'MILD' | 'MODERATE' | 'SEVERE'>('');
  const [diagnoses, setDiagnoses] = useState('');
  const [allergies, setAllergies] = useState('');
  const [drugs, setDrugs] = useState<TestDrug[]>([
    { label: '', generic: '', class: '', daily_dose: '', dose_unit: '' },
  ]);
  const [result, setResult] = useState<SandboxResult | null>(null);

  const run = useMutation({
    mutationFn: () =>
      runSandbox({
        // **The unsaved rule when there is one, the saved version otherwise.** A sandbox that
        // silently ran the saved version while the author was looking at his edits would be
        // showing him the behaviour of a rule he is no longer writing.
        ...(dirty ? { type, rule: draft } : { versionId }),
        patient: {
          age_years: age === '' ? null : Number(age),
          egfr: egfr === '' ? null : Number(egfr),
          pregnancy,
          hepatic,
          // An empty string means "not asked" and travels as null, which fails closed. A
          // non-empty list means it was asked. The two are different clinical facts and the
          // form keeps them different.
          diagnoses: diagnoses.trim() === '' ? null : toList(diagnoses),
          allergies: allergies.trim() === '' ? null : toList(allergies),
          proposed: drugs
            .filter((d) => d.generic.trim() !== '')
            .map((d) => ({
              label: d.label.trim() || d.generic.trim(),
              generic: d.generic.trim(),
              class: d.class.trim(),
              daily_dose: d.daily_dose ? Number(d.daily_dose) : null,
              dose_unit: d.dose_unit ?? '',
            })),
          current: null,
        },
      }),
    onSuccess: setResult,
  });

  return (
    <Card className="rules-sandbox">
      <h4>{t('sandbox.title')}</h4>
      <p className="app-page__description">{t('sandbox.lede')}</p>

      <div className="rules-sandbox-grid">
        <Input
          label={t('sandbox.age')}
          inputMode="decimal"
          value={age}
          onChange={(e) => setAge(e.target.value)}
        />
        <Input
          label={t('sandbox.egfr')}
          inputMode="decimal"
          value={egfr}
          onChange={(e) => setEgfr(e.target.value)}
        />
        <Select
          label={t('sandbox.pregnancy')}
          value={pregnancy}
          onChange={(e) => setPregnancy(e.target.value as typeof pregnancy)}
          placeholder={t('sandbox.unknown')}
          options={[
            { value: 'NOT_PREGNANT', label: t('state_value.NOT_PREGNANT') },
            { value: 'PREGNANT', label: t('state_value.PREGNANT') },
            { value: 'BREASTFEEDING', label: t('state_value.BREASTFEEDING') },
            { value: 'PLANNING', label: t('state_value.PLANNING') },
          ]}
        />
        <Select
          label={t('sandbox.hepatic')}
          value={hepatic}
          onChange={(e) => setHepatic(e.target.value as typeof hepatic)}
          placeholder={t('sandbox.unknown')}
          options={[
            { value: 'NONE', label: t('state_value.NONE') },
            { value: 'MILD', label: t('state_value.MILD') },
            { value: 'MODERATE', label: t('state_value.MODERATE') },
            { value: 'SEVERE', label: t('state_value.SEVERE') },
          ]}
        />
        <Input
          label={t('sandbox.diagnoses')}
          value={diagnoses}
          onChange={(e) => setDiagnoses(e.target.value)}
        />
        <Input
          label={t('sandbox.allergies')}
          value={allergies}
          onChange={(e) => setAllergies(e.target.value)}
        />
      </div>

      <h5>{t('sandbox.drugs')}</h5>
      {drugs.map((drug, i) => (
        <div className="rules-sandbox-drug" key={i}>
          <Input
            label={t('sandbox.drugLabel')}
            value={drug.label}
            onChange={(e) => patch(setDrugs, drugs, i, { label: e.target.value })}
          />
          <Input
            label={t('sandbox.drugGeneric')}
            value={drug.generic}
            onChange={(e) => patch(setDrugs, drugs, i, { generic: e.target.value })}
          />
          <Input
            label={t('sandbox.drugClass')}
            value={drug.class}
            onChange={(e) => patch(setDrugs, drugs, i, { class: e.target.value })}
          />
          <Input
            label={t('sandbox.dose')}
            inputMode="decimal"
            value={drug.daily_dose ?? ''}
            onChange={(e) => patch(setDrugs, drugs, i, { daily_dose: e.target.value })}
          />
          <Input
            label={t('form.unit')}
            value={drug.dose_unit ?? ''}
            onChange={(e) => patch(setDrugs, drugs, i, { dose_unit: e.target.value })}
          />
        </div>
      ))}
      <Button
        variant="secondary"
        size="sm"
        onClick={() =>
          setDrugs([...drugs, { label: '', generic: '', class: '', daily_dose: '', dose_unit: '' }])
        }
      >
        {t('sandbox.addDrug')}
      </Button>

      <div className="rules-actions">
        <Button onClick={() => run.mutate()} disabled={run.isPending}>
          {run.isPending ? t('sandbox.running') : t('sandbox.run')}
        </Button>
      </div>

      {result ? (
        <section className="rules-result" data-testid="sandbox-result">
          {/* The sentence the whole panel exists to make unmissable. */}
          <AlertBanner
            tone={result.live ? 'info' : 'borderline'}
            title={result.live ? t('sandbox.isLive') : t('sandbox.notLive')}
          />

          <p className="rules-outcome" data-outcome={result.finding.outcome}>
            <Icon
              name={
                result.finding.outcome === 'FIRES'
                  ? 'octagon-alert'
                  : result.finding.outcome === 'CANNOT_VERIFY'
                    ? 'help-circle'
                    : 'check'
              }
              size={18}
            />
            <strong>{t(`sandbox.outcome.${result.finding.outcome}`)}</strong>
            {result.finding.outcome === 'FIRES' ? (
              <span className="rules-severity" data-severity={result.finding.severity}>
                {t(`severity.${result.finding.severity}`)}
              </span>
            ) : null}
          </p>

          {result.finding.outcome === 'FIRES' ? (
            <blockquote className="rules-plain" lang={locale}>
              {locale === 'bn' ? result.finding.message_bn : result.finding.message_en}
            </blockquote>
          ) : null}

          {result.finding.missing && result.finding.missing.length > 0 ? (
            <p className="rules-needs">
              {t('preview.needs')}: {result.finding.missing.join(', ')}
            </p>
          ) : null}

          <h5>{t('sandbox.working')}</h5>
          <ul className="rules-steps">
            {(result.finding.steps ?? []).map((step, i) => (
              <li key={i} data-truth={step.truth}>
                <Icon
                  name={
                    step.truth === 'HOLDS'
                      ? 'check'
                      : step.truth === 'CANNOT_TELL'
                        ? 'help-circle'
                        : 'x'
                  }
                  size={14}
                />
                <span>{locale === 'bn' ? step.because_bn : step.because_en}</span>
              </li>
            ))}
          </ul>

          <p className="rules-needs">{plainText(result.plain, locale)}</p>
        </section>
      ) : null}
    </Card>
  );
}

function patch(
  set: (next: TestDrug[]) => void,
  drugs: TestDrug[],
  index: number,
  change: Partial<TestDrug>,
) {
  const existing = drugs[index];
  if (!existing) return;
  const next = [...drugs];
  next[index] = { ...existing, ...change };
  set(next);
}
