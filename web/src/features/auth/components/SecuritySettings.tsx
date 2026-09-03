'use client';

import { useLocale, useTranslations } from 'next-intl';
import { useEffect, useState } from 'react';
import QRCode from 'qrcode';

import { ApiError, NetworkError } from '@dthcms/api-client';
import { AlertBanner, Badge, Button, Card } from '@dthcms/ui';

import { useSessionStore, type Proof } from '@/stores/session';

import {
  beginEnrolment,
  confirmEnrolment,
  disableSecondFactor,
  regenerateRecoveryCodes,
} from '../api/secondFactor';
import { ProofInput } from './ProofInput';
import { StepUpCancelled } from './StepUpDialog';
import { useStepUp } from './StepUpProvider';

/**
 * The security page: the authenticator, from nothing to enrolled and back.
 *
 * Three states, three panels. Not enrolled: a button that starts. Enrolling: the QR code,
 * the key for typing by hand, and the field for the first code. Enrolled: the status, the
 * recovery codes remaining, and the two things that need a step-up — a new sheet of codes,
 * and turning the factor off.
 *
 * Recovery codes appear once, right after confirmation, behind a button that says the
 * person has kept them. There is no "show again". That is the whole point of them.
 */

type Stage =
  | { kind: 'idle' }
  | { kind: 'enrolling'; secret: string; uri: string; qr: string | null; refusal: string | null }
  | { kind: 'codes'; codes: string[]; reason: 'enrolled' | 'regenerated' };

export function SecuritySettings() {
  const t = useTranslations('security');
  const locale = useLocale();
  const user = useSessionStore((state) => state.user);
  const refresh = useSessionStore((state) => state.refresh);
  const requestStepUp = useStepUp();

  const [stage, setStage] = useState<Stage>({ kind: 'idle' });
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<{ tone: 'critical' | 'info'; text: string } | null>(null);

  const status = user?.secondFactor;

  function explain(error: unknown): string {
    if (error instanceof NetworkError) return t('offline');
    if (error instanceof ApiError) return locale === 'bn' ? error.messageBN : error.messageEN;
    return t('unexpected');
  }

  async function start() {
    setBusy(true);
    setNotice(null);
    try {
      const enrolment = await beginEnrolment();
      setStage({
        kind: 'enrolling',
        secret: enrolment.secret,
        uri: enrolment.otpauth_uri,
        qr: null,
        refusal: null,
      });
    } catch (error) {
      setNotice({ tone: 'critical', text: explain(error) });
    } finally {
      setBusy(false);
    }
  }

  // The QR is drawn client-side from the URI. Nothing but this browser ever sees the seed
  // as a picture, and the picture is a data: URL that never leaves the page.
  useEffect(() => {
    if (stage.kind !== 'enrolling' || stage.qr !== null) return;
    let cancelled = false;
    QRCode.toDataURL(stage.uri, { errorCorrectionLevel: 'M', margin: 1, width: 224 })
      .then((qr) => {
        if (!cancelled) setStage((s) => (s.kind === 'enrolling' ? { ...s, qr } : s));
      })
      .catch(() => {
        // The key beside it still works; the QR is a convenience.
      });
    return () => {
      cancelled = true;
    };
  }, [stage]);

  async function confirm(proof: Proof) {
    if (stage.kind !== 'enrolling' || !('code' in proof)) return;
    setBusy(true);
    try {
      const result = await confirmEnrolment(proof.code);
      await refresh();
      setStage({ kind: 'codes', codes: result.recovery_codes, reason: 'enrolled' });
    } catch (error) {
      const message =
        error instanceof ApiError && error.status === 422 ? t('wrongCode') : explain(error);
      setStage({ ...stage, refusal: message });
    } finally {
      setBusy(false);
    }
  }

  async function regenerate() {
    setNotice(null);
    let token: string;
    try {
      token = await requestStepUp('second_factor.recovery_codes', t('regenerateStepUp'));
    } catch (error) {
      if (!(error instanceof StepUpCancelled))
        setNotice({ tone: 'critical', text: explain(error) });
      return;
    }
    setBusy(true);
    try {
      const result = await regenerateRecoveryCodes(token);
      await refresh();
      setStage({ kind: 'codes', codes: result.recovery_codes, reason: 'regenerated' });
    } catch (error) {
      setNotice({ tone: 'critical', text: explain(error) });
    } finally {
      setBusy(false);
    }
  }

  async function disable() {
    setNotice(null);
    let token: string;
    try {
      token = await requestStepUp('second_factor.disable', t('disableStepUp'));
    } catch (error) {
      if (!(error instanceof StepUpCancelled))
        setNotice({ tone: 'critical', text: explain(error) });
      return;
    }
    setBusy(true);
    try {
      await disableSecondFactor(token);
      await refresh();
      setStage({ kind: 'idle' });
      setNotice({ tone: 'info', text: t('disabled') });
    } catch (error) {
      setNotice({ tone: 'critical', text: explain(error) });
    } finally {
      setBusy(false);
    }
  }

  if (!status) return null;

  return (
    <div className="app-stack">
      {notice && (
        <AlertBanner tone={notice.tone} title={notice.text} onDismiss={() => setNotice(null)} />
      )}

      {stage.kind === 'codes' && (
        <Card
          header={
            <h2 className="app-card__title">
              {stage.reason === 'enrolled' ? t('codes.titleEnrolled') : t('codes.titleRegenerated')}
            </h2>
          }
        >
          <div className="app-stack">
            <AlertBanner tone="high" title={t('codes.onceTitle')}>
              {t('codes.onceBody')}
            </AlertBanner>
            <ol className="app-recovery-codes" aria-label={t('codes.listLabel')}>
              {stage.codes.map((code) => (
                <li key={code}>
                  <code>{code}</code>
                </li>
              ))}
            </ol>
            <Button variant="primary" onClick={() => setStage({ kind: 'idle' })}>
              {t('codes.kept')}
            </Button>
          </div>
        </Card>
      )}

      {stage.kind === 'enrolling' && (
        <Card header={<h2 className="app-card__title">{t('enrol.title')}</h2>}>
          <div className="app-enrol">
            <div className="app-enrol__qr">
              {stage.qr ? (
                <img src={stage.qr} alt={t('enrol.qrAlt')} width={224} height={224} />
              ) : (
                <div className="app-enrol__qr-placeholder" aria-hidden="true" />
              )}
            </div>
            <div className="app-stack">
              <ol className="app-enrol__steps">
                <li>{t('enrol.step1')}</li>
                <li>{t('enrol.step2')}</li>
                <li>{t('enrol.step3')}</li>
              </ol>
              <details className="app-enrol__manual">
                <summary>{t('enrol.manualSummary')}</summary>
                <p className="app-page__description">{t('enrol.manualHint')}</p>
                <code className="app-enrol__key" aria-label={t('enrol.keyLabel')}>
                  {stage.secret.match(/.{1,4}/g)?.join(' ')}
                </code>
              </details>
              <ProofInput
                onSubmit={confirm}
                submitLabel={t('enrol.confirm')}
                refusal={stage.refusal}
                busy={busy}
                allowRecovery={false}
                autoFocus
              />
              <Button
                variant="quiet"
                size="sm"
                onClick={() => setStage({ kind: 'idle' })}
                disabled={busy}
              >
                {t('enrol.cancel')}
              </Button>
            </div>
          </div>
        </Card>
      )}

      {stage.kind === 'idle' && (
        <Card
          header={
            <div className="app-card__heading">
              <h2 className="app-card__title">{t('factor.title')}</h2>
              {status.enrolled ? (
                <Badge tone="brand">{t('factor.on')}</Badge>
              ) : (
                <Badge tone="neutral">{t('factor.off')}</Badge>
              )}
            </div>
          }
        >
          <div className="app-stack">
            {status.enrolled ? (
              <>
                <p className="app-page__description">{t('factor.onBody')}</p>
                <p className="app-page__description">
                  {t('factor.codesLeft', { count: status.recoveryCodesLeft })}
                </p>
                {status.recoveryCodesLeft <= 2 && (
                  <AlertBanner tone="borderline" title={t('factor.fewCodesTitle')}>
                    {t('factor.fewCodesBody')}
                  </AlertBanner>
                )}
                <div className="app-actions">
                  <Button variant="secondary" onClick={regenerate} disabled={busy}>
                    {t('factor.regenerate')}
                  </Button>
                  <Button variant="quiet" onClick={disable} disabled={busy}>
                    {t('factor.disable')}
                  </Button>
                </div>
              </>
            ) : (
              <>
                {status.required ? (
                  <AlertBanner tone="high" title={t('factor.requiredTitle')}>
                    {t('factor.requiredBody')}
                  </AlertBanner>
                ) : (
                  <p className="app-page__description">{t('factor.offBody')}</p>
                )}
                <div className="app-actions">
                  <Button variant="primary" onClick={start} loading={busy}>
                    {t('factor.start')}
                  </Button>
                </div>
              </>
            )}
          </div>
        </Card>
      )}
    </div>
  );
}
