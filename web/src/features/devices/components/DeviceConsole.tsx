'use client';

import { useLocale, useTranslations } from 'next-intl';
import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from 'react';

import { ApiError, NetworkError } from '@dthcms/api-client';
import { AlertBanner, Badge, Button, Card, Input, Select, Skeleton } from '@dthcms/ui';

import {
  issueEnrolment,
  listDevices,
  reissueEnrolment,
  transitionDevice,
  transitionsFor,
  type Device,
  type DeviceKind,
  type DeviceStatus,
  type EnrolmentIssued,
  type Transition,
} from '../api/devices';

/**
 * Administration → Devices: the clinic's tablets, and the three things an administrator
 * does to them (CP18).
 *
 * Register one and read out the code; suspend, reinstate, revoke or report one lost, with
 * a reason; issue a new code to one that was reinstalled. The list is the truth about
 * which hardware may write a clinical record, so it shows the two facts that answer
 * "which tablet is that": what it said it was, and when it last spoke.
 *
 * The enrolment code is shown once, large, and is gone when the panel closes — the same
 * discipline as recovery codes.
 */

const KINDS: DeviceKind[] = ['tablet', 'phone', 'desktop'];

export function DeviceConsole() {
  const t = useTranslations('devices');
  const locale = useLocale();

  const [devices, setDevices] = useState<Device[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [notice, setNotice] = useState<{ tone: 'critical' | 'info'; text: string } | null>(null);
  const [issued, setIssued] = useState<EnrolmentIssued | null>(null);
  const [pending, setPending] = useState<{ device: Device; to: Transition } | null>(null);

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
      setDevices(await listDevices());
      setLoadError(null);
    } catch (error) {
      setLoadError(explain(error));
    }
  }, [explain]);

  useEffect(() => {
    void reload();
  }, [reload]);

  async function onIssued(result: EnrolmentIssued) {
    setIssued(result);
    setNotice(null);
    await reload();
  }

  async function reissue(device: Device) {
    setNotice(null);
    try {
      await onIssued(await reissueEnrolment(device.id));
    } catch (error) {
      setNotice({ tone: 'critical', text: explain(error) });
    }
  }

  async function transition(reason: string) {
    if (!pending) return;
    try {
      const updated = await transitionDevice(pending.device.id, pending.to, reason);
      setPending(null);
      setNotice({
        tone: 'info',
        text: t('transitioned', { name: updated.name, status: t(`status.${updated.status}`) }),
      });
      await reload();
    } catch (error) {
      setNotice({ tone: 'critical', text: explain(error) });
      setPending(null);
    }
  }

  return (
    <div className="app-stack">
      {notice && (
        <AlertBanner tone={notice.tone} title={notice.text} onDismiss={() => setNotice(null)} />
      )}

      {issued ? (
        <IssuedCode issued={issued} onDone={() => setIssued(null)} />
      ) : (
        <RegisterForm onIssued={onIssued} explain={explain} />
      )}

      <Card header={<h2 className="app-card__title">{t('list.title')}</h2>}>
        {loadError ? (
          <AlertBanner tone="critical" title={loadError}>
            <Button variant="secondary" size="sm" onClick={() => void reload()}>
              {t('retry')}
            </Button>
          </AlertBanner>
        ) : devices === null ? (
          <div className="app-stack" aria-busy="true">
            <Skeleton />
            <Skeleton />
            <Skeleton />
          </div>
        ) : devices.length === 0 ? (
          <p className="app-page__description">{t('list.empty')}</p>
        ) : (
          <div className="app-table-wrap">
            <table className="app-table">
              <thead>
                <tr>
                  <th scope="col">{t('list.name')}</th>
                  <th scope="col">{t('list.status')}</th>
                  <th scope="col">{t('list.hardware')}</th>
                  <th scope="col">{t('list.appVersion')}</th>
                  <th scope="col">{t('list.lastSeen')}</th>
                  <th scope="col">
                    <span className="dthc-visually-hidden">{t('list.actions')}</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {devices.map((device) => (
                  <DeviceRow
                    key={device.id}
                    device={device}
                    locale={locale}
                    onTransition={(to) => setPending({ device, to })}
                    onReissue={() => void reissue(device)}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <ReasonDialog pending={pending} onCancel={() => setPending(null)} onConfirm={transition} />
    </div>
  );
}

// --- the register form ---

function RegisterForm({
  onIssued,
  explain,
}: {
  onIssued: (issued: EnrolmentIssued) => Promise<void>;
  explain: (error: unknown) => string;
}) {
  const t = useTranslations('devices');
  const [name, setName] = useState('');
  const [kind, setKind] = useState<DeviceKind>('tablet');
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy || name.trim().length < 2) return;
    setBusy(true);
    setRefusal(null);
    try {
      await onIssued(await issueEnrolment(name.trim(), kind));
      setName('');
    } catch (error) {
      if (error instanceof ApiError && error.status === 422 && error.fields?.name) {
        setRefusal(t('register.nameTaken'));
      } else {
        setRefusal(explain(error));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card header={<h2 className="app-card__title">{t('register.title')}</h2>}>
      <form className="app-stack" onSubmit={submit} noValidate>
        <p className="app-page__description">{t('register.body')}</p>
        {refusal && <AlertBanner tone="critical" title={refusal} />}
        <div className="app-form-row">
          <Input
            label={t('register.name')}
            name="name"
            value={name}
            onChange={(event) => setName(event.target.value)}
            disabled={busy}
            required
            description={t('register.nameHint')}
          />
          <Select
            label={t('register.kind')}
            name="kind"
            value={kind}
            onChange={(event) => setKind(event.target.value as DeviceKind)}
            options={KINDS.map((value) => ({ value, label: t(`kind.${value}`) }))}
          />
        </div>
        <div className="app-actions">
          <Button
            type="submit"
            variant="primary"
            loading={busy}
            disabled={name.trim().length < 2 || busy}
          >
            {t('register.submit')}
          </Button>
        </div>
      </form>
    </Card>
  );
}

// --- the code, shown once ---

function IssuedCode({ issued, onDone }: { issued: EnrolmentIssued; onDone: () => void }) {
  const t = useTranslations('devices');
  const locale = useLocale();
  const expires = new Date(issued.expires_at).toLocaleTimeString(
    locale === 'bn' ? 'bn-BD' : 'en-GB',
    {
      hour: '2-digit',
      minute: '2-digit',
    },
  );

  return (
    <Card
      header={<h2 className="app-card__title">{t('code.title', { name: issued.device.name })}</h2>}
    >
      <div className="app-stack">
        <p className="app-page__description">{t('code.body', { time: expires })}</p>
        <output className="app-enrolment-code" aria-label={t('code.label')}>
          {issued.code}
        </output>
        <AlertBanner tone="borderline" title={t('code.onceTitle')}>
          {t('code.onceBody')}
        </AlertBanner>
        <div className="app-actions">
          <Button variant="primary" onClick={onDone}>
            {t('code.done')}
          </Button>
        </div>
      </div>
    </Card>
  );
}

// --- a row ---

const STATUS_TONE: Record<DeviceStatus, 'neutral' | 'brand' | 'info'> = {
  pending: 'info',
  active: 'brand',
  suspended: 'neutral',
  revoked: 'neutral',
  lost: 'neutral',
};

function DeviceRow({
  device,
  locale,
  onTransition,
  onReissue,
}: {
  device: Device;
  locale: string;
  onTransition: (to: Transition) => void;
  onReissue: () => void;
}) {
  const t = useTranslations('devices');
  const transitions = transitionsFor(device.status);
  const terminal = device.status === 'revoked' || device.status === 'lost';
  const hardware = [device.model, device.os_version].filter(Boolean).join(' · ');

  return (
    <tr>
      <th scope="row">
        <div className="app-table__primary">{device.name}</div>
        <div className="app-table__secondary">{t(`kind.${device.kind}`)}</div>
      </th>
      <td>
        <Badge tone={STATUS_TONE[device.status]}>{t(`status.${device.status}`)}</Badge>
        {device.status_reason && <div className="app-table__secondary">{device.status_reason}</div>}
      </td>
      <td>{hardware || <span className="app-table__secondary">{t('list.unknown')}</span>}</td>
      <td>{device.app_version || <span className="app-table__secondary">—</span>}</td>
      <td>
        {device.last_seen_at ? (
          <time dateTime={device.last_seen_at}>{formatSeen(device.last_seen_at, locale)}</time>
        ) : (
          <span className="app-table__secondary">{t('list.never')}</span>
        )}
      </td>
      <td className="app-table__actions">
        <div className="app-actions app-actions--end">
          {!terminal && (
            <Button variant="secondary" size="sm" onClick={onReissue}>
              {t('action.newCode')}
            </Button>
          )}
          {transitions.map((to) => (
            <Button
              key={to}
              variant={to === 'reinstate' ? 'secondary' : 'quiet'}
              size="sm"
              onClick={() => onTransition(to)}
            >
              {t(`action.${to}`)}
            </Button>
          ))}
        </div>
      </td>
    </tr>
  );
}

function formatSeen(iso: string, locale: string): string {
  return new Date(iso).toLocaleString(locale === 'bn' ? 'bn-BD' : 'en-GB', {
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  });
}

// --- the reason dialog ---

function ReasonDialog({
  pending,
  onCancel,
  onConfirm,
}: {
  pending: { device: Device; to: Transition } | null;
  onCancel: () => void;
  onConfirm: (reason: string) => Promise<void>;
}) {
  const t = useTranslations('devices');
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (pending && !dialog.open) {
      setReason('');
      dialog.showModal();
    } else if (!pending && dialog.open) {
      dialog.close();
    }
  }, [pending]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (reason.trim().length < 3 || busy) return;
    setBusy(true);
    try {
      await onConfirm(reason.trim());
    } finally {
      setBusy(false);
    }
  }

  const destructive = pending?.to === 'revoke' || pending?.to === 'lost';

  return (
    <dialog ref={ref} className="app-dialog" onCancel={onCancel} aria-labelledby={titleId}>
      {pending && (
        <form className="app-stack" onSubmit={submit} noValidate>
          <div>
            <h2 className="app-dialog__title" id={titleId}>
              {t(`confirm.${pending.to}.title`, { name: pending.device.name })}
            </h2>
            <p className="app-page__description">{t(`confirm.${pending.to}.body`)}</p>
          </div>
          <Input
            label={t('confirm.reason')}
            name="reason"
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            description={t('confirm.reasonHint')}
            disabled={busy}
            autoFocus
            required
          />
          <div className="app-actions">
            <Button
              type="submit"
              variant={destructive ? 'danger' : 'primary'}
              loading={busy}
              disabled={reason.trim().length < 3 || busy}
            >
              {t(`action.${pending.to}`)}
            </Button>
            <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
              {t('confirm.cancel')}
            </Button>
          </div>
        </form>
      )}
    </dialog>
  );
}
