'use client';

import { useTranslations } from 'next-intl';
import { useId, useState } from 'react';

import { Button, Input } from '@dthcms/ui';

import type { Proof } from '@/stores/session';

/**
 * Asks for the second factor: a six-digit code by default, a recovery code on request.
 *
 * One component for the two moments that ask — the second step of sign-in, and a step-up
 * before a privileged action — so that the same affordances, the same wording and the same
 * fallback to a recovery code appear in both, and a person who has learned one has learned
 * the other.
 */
export interface ProofInputProps {
  /** Called with the proof. Resolve to finish; throw to show the failure and keep the form. */
  onSubmit: (proof: Proof) => Promise<void>;
  submitLabel: string;
  /** Shown above the fields when the previous attempt was refused. */
  refusal?: string | null;
  busy?: boolean;
  autoFocus?: boolean;
  /** Off during enrolment, when no recovery codes exist yet to offer. */
  allowRecovery?: boolean;
}

export function ProofInput({
  onSubmit,
  submitLabel,
  refusal,
  busy = false,
  autoFocus,
  allowRecovery = true,
}: ProofInputProps) {
  const t = useTranslations('secondFactor');
  const [mode, setMode] = useState<'code' | 'recovery'>('code');
  const [value, setValue] = useState('');
  const errorId = useId();

  const codeReady =
    mode === 'code' ? /^\d{6}$/.test(value.trim()) : value.replace(/[\s-]/g, '').length >= 16;

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!codeReady || busy) return;
    const proof: Proof = mode === 'code' ? { code: value.trim() } : { recoveryCode: value.trim() };
    try {
      await onSubmit(proof);
    } finally {
      setValue('');
    }
  }

  return (
    <form
      className="app-stack"
      onSubmit={submit}
      noValidate
      aria-describedby={refusal ? errorId : undefined}
    >
      {mode === 'code' ? (
        <Input
          label={t('codeLabel')}
          description={t('codeHint')}
          name="code"
          inputMode="numeric"
          autoComplete="one-time-code"
          pattern="[0-9]*"
          maxLength={6}
          value={value}
          onChange={(event) => setValue(event.target.value.replace(/\D/g, ''))}
          error={refusal ?? undefined}
          disabled={busy}
          autoFocus={autoFocus}
          required
        />
      ) : (
        <Input
          label={t('recoveryLabel')}
          description={t('recoveryHint')}
          name="recovery_code"
          autoComplete="off"
          autoCapitalize="characters"
          spellCheck={false}
          value={value}
          onChange={(event) => setValue(event.target.value)}
          error={refusal ?? undefined}
          disabled={busy}
          autoFocus={autoFocus}
          required
        />
      )}

      <Button type="submit" variant="primary" block loading={busy} disabled={!codeReady || busy}>
        {submitLabel}
      </Button>

      {allowRecovery && (
        <Button
          type="button"
          variant="quiet"
          size="sm"
          onClick={() => {
            setMode(mode === 'code' ? 'recovery' : 'code');
            setValue('');
          }}
          disabled={busy}
        >
          {mode === 'code' ? t('useRecovery') : t('useCode')}
        </Button>
      )}
    </form>
  );
}
