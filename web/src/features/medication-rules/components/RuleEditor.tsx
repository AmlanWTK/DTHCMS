'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useEffect, useState } from 'react';

import { Button, Card, Icon, Input, Select } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import {
  getVocabulary,
  previewRule,
  VOCABULARY_KEY,
  type RuleDraft,
  type RulePlain,
  type RulePredicate,
  type RuleType,
} from '../api/rules';
import { blankPredicate, fromList, plainNeeds, plainText, toList } from './rulesText';

/**
 * The authoring form (CP77, acceptance criterion 1).
 *
 * # Written for a physician, not for a programmer
 *
 * There is no JSON on this screen, no expression editor, and no field whose label is a type name.
 * A rule is: which medicine, what has to be true, what should happen, and what the physician will
 * read. Those are the four sections, in that order, because that is the order the thought arrives
 * in.
 *
 * The tests a rule may use are fetched from the server rather than listed here, and the server
 * builds that list from **the same table its validator uses**. A form that offered a pregnancy
 * test on a renal rule would be a form that produces rules the server refuses, with no way for
 * the author to know why.
 *
 * # The preview is the check, and it comes from the server
 *
 * Every pause in typing sends the rule to `/preview`, which renders it back as a sentence from
 * the canonical condition it would evaluate. That is the physician's check that what the system
 * understood is what he meant. A sentence composed here from the form's own state could only ever
 * agree with the form.
 *
 * It also names what the rule cannot answer without — "a recent eGFR" — because a rule that needs
 * one will say "cannot verify" on every patient who has not had one, and that is worth knowing
 * before publishing rather than after.
 *
 * # Both languages are side by side rather than behind a tab
 *
 * A tab is how one of them ships empty. The server refuses a rule with a message in one language,
 * and the form makes that visible rather than making it a surprise at the end.
 */
export interface RuleEditorProps {
  type: RuleType;
  draft: RuleDraft;
  editable: boolean;
  onChange: (next: RuleDraft) => void;
}

export function RuleEditor({ type, draft, editable, onChange }: RuleEditorProps) {
  const t = useTranslations('medicationRules');
  const locale = useLocale() as Locale;

  const vocabulary = useQuery({
    queryKey: VOCABULARY_KEY,
    queryFn: getVocabulary,
    staleTime: 300_000,
  });
  const [plain, setPlain] = useState<RulePlain | undefined>();
  const [problem, setProblem] = useState<string | undefined>();

  useEffect(() => {
    let cancelled = false;
    const timer = setTimeout(() => {
      previewRule(type, draft)
        .then((res) => {
          if (cancelled) return;
          setPlain(res.plain);
          setProblem(res.valid ? undefined : res.problem);
        })
        .catch(() => {
          /* A preview that could not be fetched is not an error worth a red box: the author is
             mid-sentence and the next keystroke will ask again. */
        });
    }, 250);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
    // The dependency is the rule's *content*, serialised: `draft` is a fresh object on every
    // keystroke, so depending on the reference would re-request on every render and depending on
    // its fields would be a list that goes stale the next time the model grows a field.
  }, [type, JSON.stringify(draft)]);

  const typeChoice = vocabulary.data?.types.find((c) => c.type === type);
  const set = (patch: Partial<RuleDraft>) => onChange({ ...draft, ...patch });

  return (
    <div className="rules-editor">
      {/* 1. What will happen. First, because it is the decision the whole rule is in service of,
             and because it changes what the preview's last clause says. */}
      <Card>
        <h4>{t('form.severity')}</h4>
        <div className="rules-severities">
          {(['BLOCK', 'WARN', 'INFO'] as const).map((s) => (
            <label key={s} className="rules-radio" data-checked={draft.severity === s}>
              <input
                type="radio"
                name="severity"
                checked={draft.severity === s}
                disabled={!editable}
                onChange={() => set({ severity: s })}
              />
              <span>{t(`severity.${s}`)}</span>
            </label>
          ))}
        </div>
      </Card>

      {/* 2. Which medicine. */}
      <Card>
        <h4>{t('form.subject')}</h4>
        <TargetFields
          target={draft.condition.subject}
          generics={vocabulary.data?.generics ?? []}
          classes={vocabulary.data?.classes ?? []}
          editable={editable}
          allowAny={type === 'DUPLICATE_THERAPY'}
          onChange={(subject) => set({ condition: { ...draft.condition, subject } })}
        />
      </Card>

      {/* 3. What has to be true. */}
      <Card>
        <h4>{t('form.when')}</h4>
        {draft.condition.when.map((predicate, i) => (
          <PredicateFields
            key={`${predicate.kind}-${i}`}
            predicate={predicate}
            editable={editable}
            vocabulary={vocabulary.data}
            onChange={(next) => {
              const when = [...draft.condition.when];
              when[i] = next;
              set({ condition: { ...draft.condition, when } });
            }}
            onRemove={() => {
              const when = draft.condition.when.filter((_, j) => j !== i);
              set({ condition: { ...draft.condition, when } });
            }}
          />
        ))}
        {editable && typeChoice ? (
          <>
            <p className="rules-needs">{t('form.addCondition')}</p>
            <div className="rules-add">
              {typeChoice.predicates.map((choice) => (
                <Button
                  key={choice.kind}
                  variant="secondary"
                  size="sm"
                  onClick={() =>
                    set({
                      condition: {
                        ...draft.condition,
                        when: [
                          ...draft.condition.when,
                          blankPredicate(choice.kind as RulePredicate['kind']),
                        ],
                      },
                    })
                  }
                >
                  {locale === 'bn' ? choice.name_bn : choice.name_en}
                </Button>
              ))}
            </div>
          </>
        ) : null}
      </Card>

      {/* 4. What the physician will read. Both languages side by side. */}
      <Card>
        <div className="rules-bilingual">
          <Input
            label={t('form.nameEn')}
            value={draft.name_en}
            disabled={!editable}
            onChange={(e) => set({ name_en: e.target.value })}
          />
          <Input
            label={t('form.nameBn')}
            lang="bn"
            value={draft.name_bn}
            disabled={!editable}
            onChange={(e) => set({ name_bn: e.target.value })}
          />
          <Input
            label={t('form.messageEn')}
            value={draft.message_en}
            disabled={!editable}
            onChange={(e) => set({ message_en: e.target.value })}
          />
          <Input
            label={t('form.messageBn')}
            lang="bn"
            value={draft.message_bn}
            disabled={!editable}
            onChange={(e) => set({ message_bn: e.target.value })}
          />
          <Input
            label={t('form.adviceEn')}
            value={draft.advice_en ?? ''}
            disabled={!editable}
            onChange={(e) => set({ advice_en: e.target.value })}
          />
          <Input
            label={t('form.adviceBn')}
            lang="bn"
            value={draft.advice_bn ?? ''}
            disabled={!editable}
            onChange={(e) => set({ advice_bn: e.target.value })}
          />
        </div>
        <Input
          label={t('form.source')}
          description={t('form.sourceHelp')}
          value={draft.source}
          disabled={!editable}
          onChange={(e) => set({ source: e.target.value })}
        />
      </Card>

      {/* The preview. Last on the page and first in importance. */}
      <section className="rules-preview" data-testid="rule-preview">
        <h4>{t('preview.title')}</h4>
        <blockquote className="rules-plain" lang={locale}>
          {plainText(plain, locale)}
        </blockquote>
        {plainNeeds(plain, locale).length > 0 ? (
          <p className="rules-needs">
            {t('preview.needs')}: {plainNeeds(plain, locale).join(', ')}
          </p>
        ) : null}
        {problem ? (
          <p className="rules-problem">
            <Icon name="alert-triangle" size={14} /> {t('preview.invalid')} — {problem}
          </p>
        ) : null}
      </section>
    </div>
  );
}

function TargetFields({
  target,
  generics,
  classes,
  editable,
  allowAny,
  onChange,
}: {
  target: { match: string; generics?: string[]; classes?: string[] };
  generics: string[];
  classes: string[];
  editable: boolean;
  allowAny: boolean;
  onChange: (next: {
    match: 'GENERIC' | 'CLASS' | 'ANY';
    generics?: string[];
    classes?: string[];
  }) => void;
}) {
  const t = useTranslations('medicationRules');
  return (
    <div className="rules-target">
      <Select
        label={t('form.subject')}
        value={target.match}
        disabled={!editable}
        onChange={(e) =>
          onChange({
            match: e.target.value as 'GENERIC' | 'CLASS' | 'ANY',
            generics: [],
            classes: [],
          })
        }
        options={[
          { value: 'GENERIC', label: t('form.subjectGeneric') },
          { value: 'CLASS', label: t('form.subjectClass') },
          ...(allowAny ? [{ value: 'ANY', label: t('kind.DUPLICATE_THERAPY') }] : []),
        ]}
      />
      {target.match === 'GENERIC' ? (
        <MultiPicker
          options={generics}
          chosen={target.generics ?? []}
          editable={editable}
          onChange={(next) => onChange({ match: 'GENERIC', generics: next })}
        />
      ) : null}
      {target.match === 'CLASS' ? (
        <MultiPicker
          options={classes}
          chosen={target.classes ?? []}
          editable={editable}
          onChange={(next) => onChange({ match: 'CLASS', classes: next })}
        />
      ) : null}
    </div>
  );
}

/**
 * A list of molecules or classes, chosen by clicking.
 *
 * Not a free-text field. A rule naming "Metformin HCl" instead of "Metformin hydrochloride"
 * validates on the server and never fires, and nothing anywhere says so — so the form only offers
 * names the formulary actually has.
 */
function MultiPicker({
  options,
  chosen,
  editable,
  onChange,
}: {
  options: string[];
  chosen: string[];
  editable: boolean;
  onChange: (next: string[]) => void;
}) {
  const [filter, setFilter] = useState('');
  const shown = options
    .filter((o) => o.toLowerCase().includes(filter.toLowerCase()))
    .slice(0, filter ? 20 : 0);

  return (
    <div className="rules-picker">
      <ul className="rules-chosen">
        {chosen.map((c) => (
          <li key={c}>
            <span>{c}</span>
            {editable ? (
              <button type="button" onClick={() => onChange(chosen.filter((x) => x !== c))}>
                <Icon name="x" size={12} />
              </button>
            ) : null}
          </li>
        ))}
      </ul>
      {editable ? (
        <>
          <input
            className="rules-picker-input"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="…"
          />
          {shown.length > 0 ? (
            <ul className="rules-options">
              {shown.map((o) => (
                <li key={o}>
                  <button
                    type="button"
                    onClick={() => {
                      if (!chosen.includes(o)) onChange([...chosen, o]);
                      setFilter('');
                    }}
                  >
                    {o}
                  </button>
                </li>
              ))}
            </ul>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

function PredicateFields({
  predicate,
  editable,
  vocabulary,
  onChange,
  onRemove,
}: {
  predicate: RulePredicate;
  editable: boolean;
  vocabulary: ReturnType<typeof useQuery<Awaited<ReturnType<typeof getVocabulary>>>>['data'];
  onChange: (next: RulePredicate) => void;
  onRemove: () => void;
}) {
  const t = useTranslations('medicationRules');
  const numeric =
    predicate.kind === 'EGFR' || predicate.kind === 'AGE' || predicate.kind === 'DAILY_DOSE';

  return (
    <div className="rules-predicate">
      <span className="rules-predicate-kind">{t(`predicate.${predicate.kind}`)}</span>

      {numeric ? (
        <>
          <Select
            label={t('form.operator')}
            value={predicate.operator ?? 'LT'}
            disabled={!editable}
            onChange={(e) => onChange({ ...predicate, operator: e.target.value as 'LT' })}
            options={(vocabulary?.operators ?? ['LT', 'LTE', 'GT', 'GTE']).map((o) => ({
              value: o,
              label: t(`operator.${o}`),
            }))}
          />
          <Input
            label={t('form.value')}
            inputMode="decimal"
            value={String(predicate.value ?? '')}
            disabled={!editable}
            onChange={(e) => onChange({ ...predicate, value: Number(e.target.value) })}
          />
          <Input
            label={t('form.unit')}
            value={predicate.unit ?? ''}
            disabled={!editable}
            onChange={(e) => onChange({ ...predicate, unit: e.target.value })}
          />
        </>
      ) : null}

      {predicate.kind === 'PREGNANCY' || predicate.kind === 'HEPATIC' ? (
        <div className="rules-states">
          {(predicate.kind === 'PREGNANCY'
            ? (vocabulary?.pregnancy_states ?? [])
            : (vocabulary?.hepatic_grades ?? [])
          ).map((state) => (
            <label
              key={state}
              className="rules-radio"
              data-checked={predicate.states?.includes(state)}
            >
              <input
                type="checkbox"
                checked={predicate.states?.includes(state) ?? false}
                disabled={!editable}
                onChange={(e) =>
                  onChange({
                    ...predicate,
                    states: e.target.checked
                      ? [...(predicate.states ?? []), state]
                      : (predicate.states ?? []).filter((s) => s !== state),
                  })
                }
              />
              <span>{t(`state_value.${state}`)}</span>
            </label>
          ))}
        </div>
      ) : null}

      {predicate.kind === 'DIAGNOSIS' ? (
        <Input
          label={t('form.diagnoses')}
          value={fromList(predicate.diagnosis_codes)}
          disabled={!editable}
          onChange={(e) => onChange({ ...predicate, diagnosis_codes: toList(e.target.value) })}
        />
      ) : null}

      {predicate.kind === 'ALLERGY' ? (
        <>
          <Select
            label={t('form.allergen')}
            value={predicate.allergen_group ?? ''}
            disabled={!editable}
            onChange={(e) => onChange({ ...predicate, allergen_group: e.target.value })}
            options={(vocabulary?.allergen_groups ?? []).map((g) => ({ value: g, label: g }))}
          />
          <label className="rules-radio" data-checked={predicate.cross_reactive}>
            <input
              type="checkbox"
              checked={predicate.cross_reactive ?? false}
              disabled={!editable}
              onChange={(e) => onChange({ ...predicate, cross_reactive: e.target.checked })}
            />
            <span>{t('form.crossReactive')}</span>
          </label>
        </>
      ) : null}

      {predicate.kind === 'CO_PRESCRIBED' || predicate.kind === 'DUPLICATE' ? (
        <>
          <Select
            label={predicate.kind === 'DUPLICATE' ? t('form.duplicateLevel') : t('form.subject')}
            value={predicate.with?.match ?? 'GENERIC'}
            disabled={!editable}
            onChange={(e) =>
              onChange({
                ...predicate,
                with: { ...(predicate.with ?? {}), match: e.target.value as 'GENERIC' },
              })
            }
            options={[
              { value: 'GENERIC', label: t('form.subjectGeneric') },
              { value: 'CLASS', label: t('form.subjectClass') },
            ]}
          />
          {predicate.kind === 'CO_PRESCRIBED' ? (
            <MultiPicker
              options={
                predicate.with?.match === 'CLASS'
                  ? (vocabulary?.classes ?? [])
                  : (vocabulary?.generics ?? [])
              }
              chosen={
                (predicate.with?.match === 'CLASS'
                  ? predicate.with?.classes
                  : predicate.with?.generics) ?? []
              }
              editable={editable}
              onChange={(next) =>
                onChange({
                  ...predicate,
                  with:
                    predicate.with?.match === 'CLASS'
                      ? { match: 'CLASS', classes: next }
                      : { match: 'GENERIC', generics: next },
                })
              }
            />
          ) : null}
          <label className="rules-radio" data-checked={predicate.current_medications}>
            <input
              type="checkbox"
              checked={predicate.current_medications ?? false}
              disabled={!editable}
              onChange={(e) => onChange({ ...predicate, current_medications: e.target.checked })}
            />
            <span>{t('form.currentMeds')}</span>
          </label>
        </>
      ) : null}

      {editable ? (
        <Button variant="quiet" size="sm" onClick={onRemove}>
          {t('form.remove')}
        </Button>
      ) : null}
    </div>
  );
}
