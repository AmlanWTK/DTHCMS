'use client';

import { useLocale, useTranslations } from 'next-intl';
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';

import { ApiError, NetworkError } from '@dthcms/api-client';
import { Button } from '@dthcms/ui';

import type { Proof } from '@/stores/session';

import { stepUp, type StepUpPurpose } from '../api/secondFactor';
import { ProofInput } from './ProofInput';

/**
 * The step-up prompt: "confirm with your authenticator to continue".
 *
 * Opened by `useStepUp` when a privileged call needs a fresh second factor. It mints the
 * token and hands it back; what the token is then used for is the caller's business. The
 * dialog never sees the privileged request.
 *
 * A native <dialog>, for the focus trap and the Escape handling the browser already does
 * correctly, and because a modal that a screen reader cannot tell is modal is not one.
 */
export interface StepUpRequest {
  purpose: StepUpPurpose;
  /** What the person is about to do, in their words. Shown in the prompt. */
  description: ReactNode;
  resolve: (token: string) => void;
  reject: (reason: unknown) => void;
}

export function StepUpDialog({
  request,
  onClose,
}: {
  request: StepUpRequest | null;
  onClose: () => void;
}) {
  const t = useTranslations('secondFactor');
  const locale = useLocale();
  const ref = useRef<HTMLDialogElement>(null);
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (request && !dialog.open) {
      setRefusal(null);
      dialog.showModal();
    } else if (!request && dialog.open) {
      dialog.close();
    }
  }, [request]);

  const cancel = useCallback(() => {
    request?.reject(new StepUpCancelled());
    onClose();
  }, [request, onClose]);

  async function submit(proof: Proof) {
    if (!request) return;
    setBusy(true);
    setRefusal(null);
    try {
      const token = await stepUp(request.purpose, proof);
      request.resolve(token);
      onClose();
    } catch (error) {
      if (error instanceof NetworkError) {
        setRefusal(t('offline'));
      } else if (error instanceof ApiError && error.status === 409) {
        // Not enrolled. The prompt cannot help; the security page can.
        setRefusal(t('notEnrolled'));
      } else if (error instanceof ApiError) {
        setRefusal(locale === 'bn' ? error.messageBN : error.messageEN);
      } else {
        setRefusal(t('unexpected'));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <dialog ref={ref} className="app-dialog" onCancel={cancel} aria-labelledby="step-up-title">
      {request && (
        <div className="app-stack">
          <div>
            <h2 className="app-dialog__title" id="step-up-title">
              {t('stepUpTitle')}
            </h2>
            <p className="app-page__description">{request.description}</p>
          </div>
          <ProofInput
            onSubmit={submit}
            submitLabel={t('confirm')}
            refusal={refusal}
            busy={busy}
            autoFocus
          />
          <Button type="button" variant="secondary" onClick={cancel} disabled={busy}>
            {t('cancel')}
          </Button>
        </div>
      )}
    </dialog>
  );
}

/** Thrown to the caller when the person closes the prompt without confirming. */
export class StepUpCancelled extends Error {
  constructor() {
    super('step-up cancelled');
    this.name = 'StepUpCancelled';
  }
}
