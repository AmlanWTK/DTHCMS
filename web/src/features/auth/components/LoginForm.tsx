'use client';

import { useRouter, useSearchParams } from 'next/navigation';
import { useLocale, useTranslations } from 'next-intl';
import { useEffect, useId, useState, type FormEvent } from 'react';

import { ApiError, NetworkError } from '@dthcms/api-client';
import { AlertBanner, Button, Input } from '@dthcms/ui';

import { needsEnrolment, useSessionStore, type Proof } from '@/stores/session';

import { ProofInput } from './ProofInput';

/**
 * The sign-in form.
 *
 * Deliberately plain. It asks for two things, says one of three sentences when they are
 * refused, and takes the person where they were going. There is no "forgot password" —
 * passwords are set by an administrator in person at CP21, because a reset link is a
 * second sign-in flow to secure and the clinic has a front desk.
 *
 * Every refusal from the server is the same 401 with the same message, whatever the
 * cause (docs/identity.md §7.3). The form shows that message and nothing more: it does not
 * know whether the code exists, and it must not look as if it does.
 */

/** Where to go after signing in. Only ever a path on this site — see `safeNext`. */
const DEFAULT_DESTINATION = '/dashboard';

/**
 * The `next` parameter is user-controlled input arriving in a URL. An absolute URL, a
 * protocol-relative `//evil`, or a path that is not a path would turn the sign-in page into
 * an open redirect: a phishing link that really does sign the victim into DTHCMS, then sends
 * them somewhere that looks like it.
 */
export function safeNext(candidate: string | null): string {
  if (!candidate || !candidate.startsWith('/') || candidate.startsWith('//')) {
    return DEFAULT_DESTINATION;
  }
  if (candidate.startsWith('/login')) return DEFAULT_DESTINATION;
  // A backslash is treated as a slash by some browsers when resolving `/\evil.com`.
  if (candidate.includes('\\')) return DEFAULT_DESTINATION;
  return candidate;
}

type Refusal =
  | { kind: 'credentials'; message: string }
  | { kind: 'offline' }
  | { kind: 'server'; message: string; correlationID: string }
  | null;

export function LoginForm() {
  const t = useTranslations('login');
  const tSecondFactor = useTranslations('secondFactor');
  const locale = useLocale();
  const router = useRouter();
  const params = useSearchParams();
  const destination = safeNext(params.get('next'));

  const status = useSessionStore((state) => state.status);
  const user = useSessionStore((state) => state.user);
  const hydrate = useSessionStore((state) => state.hydrate);
  const signIn = useSessionStore((state) => state.signIn);
  const completeSecondFactor = useSessionStore((state) => state.completeSecondFactor);

  const [employeeCode, setEmployeeCode] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<Refusal>(null);
  // The second step. Set when the password was right and a code is owed.
  const [challenge, setChallenge] = useState<string | null>(null);
  const [proofRefusal, setProofRefusal] = useState<string | null>(null);
  const errorId = useId();

  // Somebody who is already signed in has no business here — unless their roles require
  // an authenticator they have not set up, in which case that comes before anything.
  useEffect(() => {
    if (status === 'unknown') void hydrate();
    if (status === 'authenticated') {
      router.replace(needsEnrolment(user) ? '/account/security' : destination);
    }
  }, [status, user, hydrate, router, destination]);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) return;

    setBusy(true);
    setRefusal(null);
    try {
      const result = await signIn(employeeCode.trim(), password);
      if (result.kind === 'second-factor') {
        // The password is not kept around for the second step; the challenge stands in
        // for it.
        setPassword('');
        setChallenge(result.challenge);
        return;
      }
      // Redirect is handled by the effect above, which knows about enrolment.
    } catch (error) {
      setPassword('');
      if (error instanceof NetworkError) {
        setRefusal({ kind: 'offline' });
      } else if (error instanceof ApiError && error.status === 401) {
        setRefusal({
          kind: 'credentials',
          message: locale === 'bn' ? error.messageBN : error.messageEN,
        });
      } else if (error instanceof ApiError) {
        setRefusal({
          kind: 'server',
          message: locale === 'bn' ? error.messageBN : error.messageEN,
          correlationID: error.correlationID,
        });
      } else {
        setRefusal({ kind: 'server', message: t('unexpected'), correlationID: '' });
      }
    } finally {
      setBusy(false);
    }
  }

  async function handleProof(proof: Proof) {
    if (!challenge) return;
    setBusy(true);
    setProofRefusal(null);
    try {
      await completeSecondFactor(challenge, proof);
      // Redirect is handled by the effect above.
    } catch (error) {
      if (error instanceof NetworkError) {
        setProofRefusal(tSecondFactor('offline'));
      } else if (error instanceof ApiError && error.status === 401) {
        setProofRefusal(tSecondFactor('refused'));
      } else if (error instanceof ApiError) {
        setProofRefusal(locale === 'bn' ? error.messageBN : error.messageEN);
      } else {
        setProofRefusal(tSecondFactor('unexpected'));
      }
    } finally {
      setBusy(false);
    }
  }

  const canSubmit = employeeCode.trim() !== '' && password !== '' && !busy;

  if (challenge) {
    return (
      <div className="app-stack">
        <div>
          <h2 className="app-card__title">{tSecondFactor('loginTitle')}</h2>
          <p className="app-page__description">{tSecondFactor('loginBody')}</p>
        </div>
        <ProofInput
          onSubmit={handleProof}
          submitLabel={tSecondFactor('continue')}
          refusal={proofRefusal}
          busy={busy}
          autoFocus
        />
        <Button
          type="button"
          variant="quiet"
          size="sm"
          onClick={() => {
            setChallenge(null);
            setProofRefusal(null);
          }}
          disabled={busy}
        >
          {tSecondFactor('startOver')}
        </Button>
      </div>
    );
  }

  return (
    <form
      className="app-stack"
      onSubmit={handleSubmit}
      noValidate
      aria-describedby={refusal ? errorId : undefined}
    >
      {refusal && (
        <div id={errorId}>
          {refusal.kind === 'credentials' && (
            <AlertBanner tone="critical" title={refusal.message} />
          )}
          {refusal.kind === 'offline' && (
            <AlertBanner tone="stale" title={t('offlineTitle')}>
              {t('offlineBody')}
            </AlertBanner>
          )}
          {refusal.kind === 'server' && (
            <AlertBanner tone="critical" title={refusal.message}>
              {refusal.correlationID && t('reference', { id: refusal.correlationID })}
            </AlertBanner>
          )}
        </div>
      )}

      <Input
        label={t('employeeCode')}
        name="employee_code"
        autoComplete="username"
        autoCapitalize="characters"
        autoCorrect="off"
        spellCheck={false}
        inputMode="text"
        value={employeeCode}
        onChange={(event) => setEmployeeCode(event.target.value)}
        disabled={busy}
        required
      />
      <Input
        label={t('password')}
        name="password"
        type="password"
        autoComplete="current-password"
        value={password}
        onChange={(event) => setPassword(event.target.value)}
        disabled={busy}
        required
      />

      <Button
        type="submit"
        variant="primary"
        block
        loading={busy}
        loadingLabel={t('signingIn')}
        disabled={!canSubmit}
      >
        {t('submit')}
      </Button>
    </form>
  );
}
