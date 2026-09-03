'use client';

import { useTranslations } from 'next-intl';
import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from 'react';

import { AlertBanner, Button, Input } from '@dthcms/ui';

/**
 * "Are you sure, and why" — the dialog every administrative write goes through.
 *
 * A reason is asked for whenever the server will keep one (the status note, the revoke
 * reason, the audit entry). The optional secret field is for setting a password, which
 * is a reason plus one more thing, not a different dialog.
 *
 * The step-up prompt opens *after* this one confirms: the person says what they are
 * doing and why, then proves it is them. The other order would ask for a code before
 * they had said what for.
 */
export interface ConfirmRequest {
  title: ReactNode;
  body?: ReactNode;
  confirmLabel: ReactNode;
  destructive?: boolean;
  /** Whether a reason must be given; when false it is still offered, but may be empty. */
  reasonRequired?: boolean;
  /** Ask for a new secret alongside the reason. Its label and hint come from the caller. */
  secret?: {
    label: ReactNode;
    hint: ReactNode;
    acceptable: (value: string) => boolean;
    /** Offers a generated value, so nobody types a weak one under time pressure. */
    suggest?: () => string;
  };
  onConfirm: (values: { reason: string; secret: string }) => Promise<void>;
}

export function ConfirmDialog({
  request,
  onCancel,
}: {
  request: ConfirmRequest | null;
  onCancel: () => void;
}) {
  const t = useTranslations('users.confirm');
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const [reason, setReason] = useState('');
  const [secret, setSecret] = useState('');
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (request && !dialog.open) {
      setReason('');
      setSecret('');
      setRefusal(null);
      dialog.showModal();
    } else if (!request && dialog.open) {
      dialog.close();
    }
  }, [request]);

  const reasonOk = !request?.reasonRequired || reason.trim().length >= 3;
  const secretOk = !request?.secret || request.secret.acceptable(secret);
  const ready = reasonOk && secretOk && !busy;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!request || !ready) return;
    setBusy(true);
    setRefusal(null);
    try {
      await request.onConfirm({ reason: reason.trim(), secret });
    } catch (error) {
      setRefusal(error instanceof Error ? error.message : t('failed'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <dialog ref={ref} className="app-dialog" onCancel={onCancel} aria-labelledby={titleId}>
      {request && (
        <form className="app-stack" onSubmit={submit} noValidate>
          <div>
            <h2 className="app-dialog__title" id={titleId}>
              {request.title}
            </h2>
            {request.body && <p className="app-page__description">{request.body}</p>}
          </div>
          {refusal && <AlertBanner tone="critical" title={refusal} />}
          {request.secret && (
            <Input
              label={request.secret.label}
              name="secret"
              type="text"
              autoComplete="off"
              spellCheck={false}
              value={secret}
              onChange={(event) => setSecret(event.target.value)}
              description={request.secret.hint}
              disabled={busy}
              autoFocus
              required
              after={
                request.secret.suggest && (
                  <Button
                    type="button"
                    variant="quiet"
                    size="sm"
                    onClick={() => setSecret(request.secret!.suggest!())}
                    disabled={busy}
                  >
                    {t('generate')}
                  </Button>
                )
              }
            />
          )}
          <Input
            label={t('reason')}
            name="reason"
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            description={request.reasonRequired ? t('reasonRequired') : t('reasonOptional')}
            disabled={busy}
            autoFocus={!request.secret}
            required={request.reasonRequired}
          />
          <div className="app-actions">
            <Button
              type="submit"
              variant={request.destructive ? 'danger' : 'primary'}
              loading={busy}
              disabled={!ready}
            >
              {request.confirmLabel}
            </Button>
            <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
              {t('cancel')}
            </Button>
          </div>
        </form>
      )}
    </dialog>
  );
}
