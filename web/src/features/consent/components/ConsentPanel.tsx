'use client';

import { useLocale, useTranslations } from 'next-intl';
import { useCallback, useEffect, useState } from 'react';

import { ApiError } from '@dthcms/api-client';
import { AlertBanner, Badge, Button, Card, Skeleton } from '@dthcms/ui';

import {
  CONSENT_TYPES,
  NEEDS_EVIDENCE,
  NEEDS_WITNESS,
  consentTemplates,
  evidenceUploadURL,
  grantConsent,
  listConsents,
  revokeConsent,
  type CaptureMethod,
  type ConsentTemplate,
  type ConsentType,
  type PatientConsent,
} from '../api/consent';
import { digestOf } from '../lib/signature';
import { SignaturePad } from './SignaturePad';

/**
 * What this patient has agreed to (CP36, §15.1).
 *
 * The panel shows **all five consents, always** — including the ones nobody has asked about.
 * A list of only what exists cannot show a desk what it has not done, and "we never asked" is
 * the state that matters at the point of care. `absent` and `revoked` are drawn differently
 * on purpose: one is work outstanding, the other is a decision the patient made, and an
 * interface that conflates them will produce staff who re-ask people who said no.
 *
 * Taking a consent shows the **wording** first and the buttons after. A screen where the
 * consent can be recorded without the text being on it is a screen that records a consent
 * nobody read out.
 */
export function ConsentPanel({
  patientId,
  heading = true,
}: {
  patientId: string;
  /** Off when the screen's own header already says "Consent"; on when embedded in a record. */
  heading?: boolean;
}) {
  const t = useTranslations('patients.consent');
  const locale = useLocale();
  const language = locale === 'bn' ? 'bn' : 'en';

  const [consents, setConsents] = useState<PatientConsent[] | null>(null);
  const [templates, setTemplates] = useState<ConsentTemplate[]>([]);
  const [failed, setFailed] = useState<string | null>(null);
  const [taking, setTaking] = useState<ConsentType | null>(null);
  const [revoking, setRevoking] = useState<ConsentType | null>(null);

  const reload = useCallback(async () => {
    const rows = await listConsents(patientId);
    setConsents(rows);
  }, [patientId]);

  useEffect(() => {
    let live = true;
    Promise.all([listConsents(patientId), consentTemplates(language)])
      .then(([rows, wording]) => {
        if (!live) return;
        setConsents(rows);
        setTemplates(wording);
      })
      .catch((error: unknown) => {
        if (live) setFailed(error instanceof Error ? error.message : t('loadFailed'));
      });
    return () => {
      live = false;
    };
  }, [patientId, language, t]);

  if (failed) return <AlertBanner tone="critical" title={failed} />;
  if (!consents) return <Skeleton lines={6} />;

  const wordingFor = (kind: ConsentType) => templates.find((one) => one.consent_type === kind);

  return (
    <section className="app-consent" aria-label={t('title')} data-testid="consent-panel">
      {heading ? (
        <header className="app-consent__head">
          <h2>{t('title')}</h2>
          <p>{t('lede')}</p>
        </header>
      ) : null}

      {templates.length === 0 ? (
        // Not an error and not hidden. Until D-02 is answered there is no approved wording,
        // and a screen that quietly offered the buttons anyway would collect consents to
        // nothing.
        <AlertBanner tone="borderline" title={t('noWordingTitle')}>
          {t('noWordingBody')}
        </AlertBanner>
      ) : null}

      {taking ? (
        <TakeConsent
          patientId={patientId}
          consentType={taking}
          template={wordingFor(taking)}
          language={language}
          onDone={async () => {
            setTaking(null);
            await reload();
          }}
          onCancel={() => setTaking(null)}
        />
      ) : revoking ? (
        <Withdraw
          consentType={revoking}
          onConfirm={async (reason) => {
            await revokeConsent(patientId, revoking, { reason: reason || undefined });
            setRevoking(null);
            await reload();
          }}
          onCancel={() => setRevoking(null)}
        />
      ) : null}

      {/* The list is hidden while one consent is being taken. On a tablet held between an
          operator and a patient, a form below five rows is a form nobody scrolls to, and the
          rows are not the task at that moment. */}
      <ul className="app-consent__list" hidden={taking !== null || revoking !== null}>
        {CONSENT_TYPES.map((kind) => {
          const record = consents.find((one) => one.consent_type === kind);
          const status = record?.status ?? 'absent';
          return (
            <li key={kind} className="app-consent__row" data-status={status}>
              <Card>
                <div className="app-consent__item">
                  <div className="app-consent__what">
                    <h3>{t(`types.${kind}`)}</h3>
                    <p>{t(`explain.${kind}`)}</p>
                  </div>

                  <div className="app-consent__state" data-testid={`consent-${kind}-status`}>
                    <StatusBadge status={status} label={t(`status.${status}`)} />
                    {record && status !== 'absent' ? (
                      <p className="app-consent__detail">
                        {status === 'granted'
                          ? t('grantedLine', {
                              method: t(`methods.${record.capture_method || 'verbal_attested'}`),
                              version: record.template_version ?? 0,
                              language: t(`languages.${record.language || 'bn'}`),
                              when: shortDate(record.granted_at, locale),
                            })
                          : t('revokedLine', { when: shortDate(record.revoked_at, locale) })}
                      </p>
                    ) : null}
                    {record?.revoke_reason ? (
                      <p className="app-consent__detail">{record.revoke_reason}</p>
                    ) : null}
                  </div>

                  <div className="app-consent__actions">
                    {status === 'granted' ? (
                      <Button
                        variant="secondary"
                        onClick={() => setRevoking(kind)}
                        data-testid={`consent-${kind}-revoke`}
                      >
                        {t('withdraw')}
                      </Button>
                    ) : (
                      <Button
                        variant="primary"
                        onClick={() => setTaking(kind)}
                        disabled={!wordingFor(kind)}
                        data-testid={`consent-${kind}-take`}
                      >
                        {status === 'revoked' ? t('takeAgain') : t('take')}
                      </Button>
                    )}
                  </div>
                </div>
              </Card>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function StatusBadge({ status, label }: { status: string; label: string }) {
  // Three states, three tones. "Never asked" is not a refusal and must not look like one.
  const tone = status === 'granted' ? 'brand' : status === 'revoked' ? 'info' : 'neutral';
  return <Badge tone={tone}>{label}</Badge>;
}

function shortDate(value: string | undefined, locale: string): string {
  if (!value) return '';
  return new Date(value).toLocaleDateString(locale);
}

/** Taking one consent: the wording, then how it was taken, then the record. */
function TakeConsent({
  patientId,
  consentType,
  template,
  language,
  onDone,
  onCancel,
}: {
  patientId: string;
  consentType: ConsentType;
  template?: ConsentTemplate;
  language: 'en' | 'bn';
  onDone: () => void | Promise<void>;
  onCancel: () => void;
}) {
  const t = useTranslations('patients.consent');
  const locale = useLocale();
  const [method, setMethod] = useState<CaptureMethod>('signature');
  const [paperReference, setPaperReference] = useState('');
  const [witness, setWitness] = useState('');
  const [relation, setRelation] = useState('');
  const [forName, setForName] = useState('');
  const [png, setPng] = useState<Blob | null>(null);
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);

  const needsEvidence = NEEDS_EVIDENCE.includes(method);
  const needsWitness = NEEDS_WITNESS.includes(method);
  const ready =
    !busy &&
    Boolean(template) &&
    (!needsEvidence || png !== null) &&
    (!needsWitness || witness.trim() !== '') &&
    (method !== 'paper_form' || paperReference.trim() !== '');

  async function record() {
    if (!ready) return;
    setBusy(true);
    setRefusal(null);
    try {
      let evidenceKey: string | undefined;
      let evidenceDigest: string | undefined;
      if (needsEvidence && png) {
        // The bytes go straight to storage; the API is told the key afterwards and reads the
        // object back itself. A signature that never enters the API process cannot end up in
        // a request log (CP34's rule, applied here).
        const ticket = await evidenceUploadURL(patientId);
        const response = await fetch(ticket.upload_url, {
          method: 'PUT',
          headers: { 'Content-Type': 'image/png' },
          body: png,
        });
        if (!response.ok) throw new Error(t('uploadFailed'));
        evidenceKey = ticket.object_key;
        evidenceDigest = await digestOf(png);
      }
      await grantConsent(patientId, {
        consent_type: consentType,
        language,
        capture_method: method,
        evidence_key: evidenceKey,
        evidence_sha256: evidenceDigest,
        paper_reference: paperReference.trim() || undefined,
        witnessed_by: witness.trim() || undefined,
        granted_for_relation: relation.trim() || undefined,
        granted_for_name: forName.trim() || undefined,
      });
      await onDone();
    } catch (error) {
      setRefusal(
        error instanceof ApiError || error instanceof Error ? error.message : t('recordFailed'),
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <div className="app-consent__take" data-testid="consent-take">
        <h3>{t('takingTitle', { what: t(`types.${consentType}`) })}</h3>

        {/* The wording, first and in full. A screen that lets a consent be recorded without
            the text on it records a consent nobody read out. */}
        {template ? (
          <blockquote className="app-consent__wording" data-testid="consent-wording">
            <h4>{template.title}</h4>
            <p>{template.body}</p>
            <footer>
              {t('versionLine', {
                version: template.version,
                language: t(`languages.${template.language}`),
              })}
            </footer>
          </blockquote>
        ) : (
          <AlertBanner tone="borderline" title={t('noWordingTitle')}>
            {t('noWordingBody')}
          </AlertBanner>
        )}

        <fieldset className="app-consent__method">
          <legend>{t('howTaken')}</legend>
          {(['signature', 'thumbprint', 'verbal_attested', 'paper_form'] as CaptureMethod[]).map(
            (option) => (
              <label key={option}>
                <input
                  type="radio"
                  name="capture-method"
                  value={option}
                  checked={method === option}
                  onChange={() => setMethod(option)}
                  data-testid={`method-${option}`}
                />
                {t(`methods.${option}`)}
              </label>
            ),
          )}
        </fieldset>

        {needsEvidence ? <SignaturePad label={t(`padLabel.${method}`)} onChange={setPng} /> : null}

        {needsWitness ? (
          <label className="app-consent__field">
            {t('witness')}
            <input
              type="text"
              value={witness}
              onChange={(event) => setWitness(event.target.value)}
              placeholder={t('witnessPlaceholder')}
              data-testid="consent-witness"
            />
            <span>{t('witnessHint')}</span>
          </label>
        ) : null}

        {method === 'paper_form' ? (
          <label className="app-consent__field">
            {t('paperReference')}
            <input
              type="text"
              value={paperReference}
              onChange={(event) => setPaperReference(event.target.value)}
              data-testid="consent-paper-reference"
            />
            <span>{t('paperHint')}</span>
          </label>
        ) : null}

        <details className="app-consent__guardian">
          <summary>{t('someoneElse')}</summary>
          <label className="app-consent__field">
            {t('guardianName')}
            <input
              type="text"
              value={forName}
              onChange={(event) => setForName(event.target.value)}
              data-testid="consent-guardian-name"
            />
          </label>
          <label className="app-consent__field">
            {t('guardianRelation')}
            <input
              type="text"
              value={relation}
              onChange={(event) => setRelation(event.target.value)}
              data-testid="consent-guardian-relation"
            />
          </label>
        </details>

        {refusal ? <AlertBanner tone="critical" title={refusal} /> : null}

        <div className="app-consent__confirm">
          <Button variant="primary" onClick={record} disabled={!ready} data-testid="consent-record">
            {busy ? t('recording') : t('record')}
          </Button>
          <Button variant="quiet" onClick={onCancel} disabled={busy}>
            {t('cancel')}
          </Button>
        </div>
        <p className="app-consent__note">{t('versionNote', { locale })}</p>
      </div>
    </Card>
  );
}

/** Withdrawing one consent. Deliberately one click and an optional reason. */
function Withdraw({
  consentType,
  onConfirm,
  onCancel,
}: {
  consentType: ConsentType;
  onConfirm: (reason: string) => void | Promise<void>;
  onCancel: () => void;
}) {
  const t = useTranslations('patients.consent');
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);

  return (
    <Card>
      <div className="app-consent__withdraw" data-testid="consent-withdraw">
        <h3>{t('withdrawTitle', { what: t(`types.${consentType}`) })}</h3>
        <p>{t('withdrawBody')}</p>
        <label className="app-consent__field">
          {t('withdrawReason')}
          <input
            type="text"
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            data-testid="consent-revoke-reason"
          />
          {/* Optional, and the hint says so. A patient withdrawing consent does not owe
              anybody an explanation, and a required field here would be filled in with
              "revoked" by an operator standing in front of somebody who wants to leave. */}
          <span>{t('withdrawReasonHint')}</span>
        </label>
        <div className="app-consent__confirm">
          <Button
            variant="danger"
            data-testid="consent-revoke-confirm"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              try {
                await onConfirm(reason.trim());
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? t('withdrawing') : t('withdrawConfirm')}
          </Button>
          <Button variant="quiet" onClick={onCancel} disabled={busy}>
            {t('cancel')}
          </Button>
        </div>
      </div>
    </Card>
  );
}
