'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useEffect, useState } from 'react';

import { ApiError, fieldMessages } from '@dthcms/api-client';
import { AlertBanner, Button, Icon, Skeleton } from '@dthcms/ui';

import { StepUpCancelled, useStepUp } from '@/features/auth';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import {
  draftNewVersion,
  getRule,
  publishVersion,
  ruleKey,
  RULES_KEY,
  saveDraft,
  withdrawRule,
  type RuleDraft,
  type RuleVersion,
} from '../api/rules';
import { RuleEditor } from './RuleEditor';
import { RuleSandbox } from './RuleSandbox';
import { plainText } from './rulesText';

/**
 * One rule: its versions, the draft being edited, the sandbox, and publishing (CP77).
 *
 * # Three separate acts, not one
 *
 * Saving a draft is cheap and reversible. Publishing makes the rule check every prescription from
 * that second, freezes the version forever, and retires whatever was live. Reaching it takes
 * three distinct decisions — press *Publish*, read what it will do and confirm, prove it is you
 * with a fresh second factor — and the first press calls nothing. The failure this guards against
 * is not malice; it is a physician fixing a typo at the end of a clinic day who hits the wrong
 * control.
 *
 * # Why unsaved work blocks publishing and a validation problem does not
 *
 * **Unsaved work blocks.** That request would *succeed* and publish something other than what the
 * author is looking at. No server can catch it — the server does not know what is on this screen.
 *
 * **A rule the server might refuse does not block.** That request would *fail*, and the server is
 * the authority on whether it fails. A button greyed out by a stale local judgement is a
 * physician who cannot publish a rule that is actually fine, with nothing on screen explaining
 * why.
 */
export function RuleWorkspace({ ruleId }: { ruleId: string }) {
  const t = useTranslations('medicationRules');
  const locale = useLocale() as Locale;
  const queryClient = useQueryClient();
  const requestStepUp = useStepUp();
  const mayWrite = usePermission('medicationRules.write');
  const mayPublish = usePermission('medicationRules.publish');

  const rule = useQuery({ queryKey: ruleKey(ruleId), queryFn: () => getRule(ruleId) });

  const [draft, setDraft] = useState<RuleDraft | null>(null);
  const [dirty, setDirty] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [asking, setAsking] = useState(false);
  const [withdrawing, setWithdrawing] = useState(false);
  const [reason, setReason] = useState('');

  const versions = rule.data?.versions ?? [];
  // The one being worked on: the newest draft, else the newest version of all.
  const editable = versions.find((v) => v.status === 'DRAFT');
  const shown: RuleVersion | undefined = editable ?? versions[0];

  useEffect(() => {
    if (!shown) return;
    setDraft({
      severity: shown.severity,
      name_en: shown.name_en,
      name_bn: shown.name_bn,
      message_en: shown.message_en,
      message_bn: shown.message_bn,
      advice_en: shown.advice_en ?? '',
      advice_bn: shown.advice_bn ?? '',
      condition: shown.condition,
      source: shown.source,
      notes: shown.notes ?? '',
    });
    setDirty(false);
    // Keyed on the version's id alone. Re-seeding the form whenever `shown` changed identity
    // would throw away what the author is typing every time the query refetched in the
    // background, which is the bug this shape exists to avoid.
  }, [shown?.id]);

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ruleKey(ruleId) });
    void queryClient.invalidateQueries({ queryKey: RULES_KEY });
  };

  const save = useMutation({
    mutationFn: async () => {
      if (!editable || !draft) throw new Error('nothing to save');
      return saveDraft(ruleId, editable.id, draft);
    },
    onSuccess: () => {
      setDirty(false);
      setProblem(null);
      refresh();
    },
    onError: (error) => setProblem(describe(error, locale)),
  });

  const openDraft = useMutation({
    mutationFn: () => draftNewVersion(ruleId),
    onSuccess: refresh,
    onError: (error) => setProblem(describe(error, locale)),
  });

  const publish = useMutation({
    mutationFn: async () => {
      if (!editable) throw new Error('nothing to publish');
      const token = await requestStepUp('medication_rule.publish', t('publish.confirm'));
      return publishVersion(ruleId, editable.id, token);
    },
    onSuccess: () => {
      setAsking(false);
      setProblem(null);
      refresh();
    },
    onError: (error) => {
      if (error instanceof StepUpCancelled) {
        setAsking(false);
        return;
      }
      setProblem(describe(error, locale));
    },
  });

  const withdraw = useMutation({
    mutationFn: async () => {
      const token = await requestStepUp('medication_rule.publish', t('withdraw.confirm'));
      return withdrawRule(ruleId, reason, token);
    },
    onSuccess: () => {
      setWithdrawing(false);
      setProblem(null);
      refresh();
    },
    onError: (error) => {
      if (error instanceof StepUpCancelled) {
        setWithdrawing(false);
        return;
      }
      setProblem(describe(error, locale));
    },
  });

  if (rule.isPending || !shown || !draft) return <Skeleton />;

  const live = versions.find((v) => v.status === 'PUBLISHED');

  return (
    <div className="rules-workspace">
      {problem ? <AlertBanner tone="critical" title={problem} /> : null}

      {!shown.approved_at ? (
        <AlertBanner tone="borderline" title={t('unapprovedNotice')}>
          <p>
            <strong>{t('source')}:</strong> {shown.source}
          </p>
          <p>{t(`origin.${shown.origin}`)}</p>
        </AlertBanner>
      ) : null}

      <RuleEditor
        type={rule.data!.rule.type}
        draft={draft}
        editable={Boolean(editable) && mayWrite}
        onChange={(next) => {
          setDraft(next);
          setDirty(true);
        }}
      />

      <div className="rules-actions">
        {editable && mayWrite ? (
          <Button onClick={() => save.mutate()} disabled={!dirty || save.isPending}>
            {dirty ? t('form.save') : t('form.saved')}
          </Button>
        ) : (
          <Button variant="secondary" onClick={() => openDraft.mutate()} disabled={!mayWrite}>
            {t('form.new')}
          </Button>
        )}

        {editable && mayPublish ? (
          <Button variant="primary" onClick={() => setAsking(true)} disabled={publish.isPending}>
            {t('publish.title')}
          </Button>
        ) : null}

        {live && mayPublish ? (
          <Button variant="secondary" onClick={() => setWithdrawing(true)}>
            {t('withdraw.title')}
          </Button>
        ) : null}
      </div>

      {asking ? (
        <section className="rules-confirm" data-testid="publish-confirm">
          <h4>{t('publish.confirm')}</h4>
          {/* The confirmation says the consequences, not the action. "Are you sure?" tells
              somebody nothing they did not know when they pressed the button. */}
          <p>{t('publish.confirmBody')}</p>
          <blockquote className="rules-plain">{plainText(shown.plain, locale)}</blockquote>
          {live ? <p>{t('publish.supersedes', { version: live.version })}</p> : null}
          {dirty ? <AlertBanner tone="borderline" title={t('publish.blockedUnsaved')} /> : null}
          <div className="rules-actions">
            <Button onClick={() => publish.mutate()} disabled={dirty || publish.isPending}>
              {t('publish.confirm')}
            </Button>
            <Button variant="secondary" onClick={() => setAsking(false)}>
              {t('publish.cancel')}
            </Button>
          </div>
        </section>
      ) : null}

      {withdrawing ? (
        <section className="rules-confirm">
          <h4>{t('withdraw.confirm')}</h4>
          <p>{t('withdraw.body')}</p>
          <label className="rules-field">
            <span>{t('withdraw.reason')}</span>
            <input value={reason} onChange={(e) => setReason(e.target.value)} />
          </label>
          <div className="rules-actions">
            <Button onClick={() => withdraw.mutate()} disabled={!reason.trim()}>
              {t('withdraw.confirm')}
            </Button>
            <Button variant="secondary" onClick={() => setWithdrawing(false)}>
              {t('publish.cancel')}
            </Button>
          </div>
        </section>
      ) : null}

      <RuleSandbox
        type={rule.data!.rule.type}
        versionId={editable ? editable.id : shown.id}
        dirty={dirty}
        draft={draft}
      />

      <section className="rules-versions">
        <h4>{t('list.showing', { shown: versions.length, total: versions.length })}</h4>
        <ul>
          {versions.map((v) => (
            <li key={v.id}>
              <code>v{v.version}</code> <span>{t(`state.${versionState(v)}`)}</span>{' '}
              <span className="rules-period">
                {v.effective_from ? new Date(v.effective_from).toLocaleString(locale) : '—'}
                {v.effective_to ? ` → ${new Date(v.effective_to).toLocaleString(locale)}` : ''}
              </span>
              {v.approved_by_code ? (
                <span className="rules-approver">
                  <Icon name="badge-check" size={14} /> {v.approved_by_code}
                </span>
              ) : null}
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}

function versionState(v: RuleVersion) {
  if (v.status === 'PUBLISHED') return 'approved';
  if (v.status === 'WITHDRAWN') return 'withdrawn';
  if (v.status === 'SUPERSEDED') return 'approved';
  return v.approved_at ? 'draft' : 'unapproved';
}

function describe(error: unknown, locale: Locale): string {
  if (error instanceof ApiError) {
    const named = fieldMessages(error, locale);
    return Object.values(named)[0] ?? (locale === 'bn' ? error.messageBN : error.messageEN);
  }
  return error instanceof Error ? error.message : String(error);
}
