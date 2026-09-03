'use client';

import { useLocale, useTranslations } from 'next-intl';
import { useCallback, useEffect, useState, type FormEvent } from 'react';

import { ApiError, NetworkError } from '@dthcms/api-client';
import { AlertBanner, Badge, Button, Card, Input, Select } from '@dthcms/ui';

import { StepUpCancelled, useStepUp } from '@/features/auth';

import {
  MIN_JUSTIFICATION,
  endBreakGlass,
  justificationAcceptable,
  myBreakGlass,
  openBreakGlass,
  type BreakGlassAccess,
} from '../api/audit';
import { formatWhen } from './AuditViewer';

/**
 * The emergency door (CP22 criterion 3), from the clinician's side.
 *
 * Says plainly what will happen — the justification is kept, every administrator is
 * told at once, the access is on the record — so nobody opens it casually, and nobody who
 * needs it hesitates. What the access unlocks is the clinical screens' business once they
 * exist; this page opens it, shows what is open, and closes it.
 */
export function BreakGlassConsole() {
  const t = useTranslations('breakGlass');
  const locale = useLocale();
  const requestStepUp = useStepUp();

  const [scopeKind, setScopeKind] = useState<'patient' | 'other'>('patient');
  const [scopeRef, setScopeRef] = useState('');
  const [justification, setJustification] = useState('');
  const [hours, setHours] = useState(4);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<{ tone: 'critical' | 'info'; text: string } | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [mine, setMine] = useState<BreakGlassAccess[]>([]);
  const [ending, setEnding] = useState<string | null>(null);

  const explain = useCallback(
    (error: unknown): string => {
      if (error instanceof NetworkError) return t('offline');
      if (error instanceof ApiError) return locale === 'bn' ? error.messageBN : error.messageEN;
      return t('unexpected');
    },
    [t, locale],
  );

  const reload = useCallback(async () => {
    try {
      setMine(await myBreakGlass());
    } catch {
      setMine([]);
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const scopeOk =
    scopeKind === 'patient'
      ? /^[0-9a-f-]{36}$/i.test(scopeRef.trim())
      : scopeRef.trim().length >= 3;
  const ready = scopeOk && justificationAcceptable(justification) && !busy;
  const remaining = Math.max(0, MIN_JUSTIFICATION - [...justification.trim()].length);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!ready) return;
    setBusy(true);
    setNotice(null);
    setFields({});
    try {
      const token = await requestStepUp('break_glass', t('stepUp'));
      const access = await openBreakGlass(
        {
          scope_kind: scopeKind,
          scope_ref: scopeRef.trim(),
          justification: justification.trim(),
          hours,
        },
        token,
      );
      setNotice({
        tone: 'info',
        text: t('opened', { until: formatWhen(access.expires_at, locale) }),
      });
      setScopeRef('');
      setJustification('');
      await reload();
    } catch (error) {
      if (error instanceof StepUpCancelled) return;
      if (error instanceof ApiError && error.status === 422 && Object.keys(error.fields).length) {
        setFields(error.fields);
      } else {
        setNotice({ tone: 'critical', text: explain(error) });
      }
    } finally {
      setBusy(false);
    }
  }

  async function end(access: BreakGlassAccess) {
    setEnding(access.id);
    try {
      await endBreakGlass(access.id, t('endedReason'));
      setNotice({ tone: 'info', text: t('ended') });
      await reload();
    } catch (error) {
      setNotice({ tone: 'critical', text: explain(error) });
    } finally {
      setEnding(null);
    }
  }

  return (
    <div className="app-stack">
      {notice && (
        <AlertBanner tone={notice.tone} title={notice.text} onDismiss={() => setNotice(null)} />
      )}

      <AlertBanner tone="borderline" title={t('warning.title')}>
        {t('warning.body')}
      </AlertBanner>

      <Card header={<h2 className="app-card__title">{t('form.title')}</h2>}>
        <form className="app-stack" onSubmit={submit} noValidate>
          <div className="app-form-row">
            <Input
              label={scopeKind === 'patient' ? t('form.patient') : t('form.other')}
              name="scope_ref"
              value={scopeRef}
              onChange={(e) => setScopeRef(e.target.value)}
              description={scopeKind === 'patient' ? t('form.patientHint') : t('form.otherHint')}
              error={fields.scope}
              spellCheck={false}
              disabled={busy}
              required
            />
            <Select
              label={t('form.scopeKind')}
              name="scope_kind"
              value={scopeKind}
              onChange={(e) => setScopeKind(e.target.value as 'patient' | 'other')}
              options={[
                { value: 'patient', label: t('form.kindPatient') },
                { value: 'other', label: t('form.kindOther') },
              ]}
            />
          </div>
          <Input
            label={t('form.justification')}
            name="justification"
            value={justification}
            onChange={(e) => setJustification(e.target.value)}
            description={
              remaining > 0
                ? t('form.justificationRemaining', { count: remaining })
                : t('form.justificationHint')
            }
            error={fields.justification}
            disabled={busy}
            required
          />
          <Select
            label={t('form.hours')}
            name="hours"
            value={String(hours)}
            onChange={(e) => setHours(Number(e.target.value))}
            options={[1, 2, 4, 8, 12, 24].map((h) => ({
              value: String(h),
              label: t('form.hoursOption', { count: h }),
            }))}
          />
          <div className="app-actions">
            <Button
              type="submit"
              variant="danger"
              iconStart="siren"
              loading={busy}
              disabled={!ready}
            >
              {t('form.submit')}
            </Button>
          </div>
        </form>
      </Card>

      <Card header={<h2 className="app-card__title">{t('mine.title')}</h2>}>
        {mine.length === 0 ? (
          <p className="app-page__description">{t('mine.empty')}</p>
        ) : (
          <ul className="app-role-list">
            {mine.map((access) => (
              <li key={access.id}>
                <div>
                  <div className="app-table__primary">
                    {access.scope_kind === 'patient'
                      ? t('mine.patient', { id: access.scope_ref })
                      : access.scope_ref}
                  </div>
                  <div className="app-table__secondary">
                    {t('mine.until', { when: formatWhen(access.expires_at, locale) })}
                    {' · '}
                    {access.acknowledged_at ? (
                      <Badge tone="brand">{t('mine.acknowledged')}</Badge>
                    ) : (
                      <Badge tone="info">{t('mine.notYetSeen')}</Badge>
                    )}
                  </div>
                  <div className="app-table__secondary">{access.justification}</div>
                </div>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => void end(access)}
                  loading={ending === access.id}
                  disabled={ending !== null}
                >
                  {t('mine.end')}
                </Button>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}
