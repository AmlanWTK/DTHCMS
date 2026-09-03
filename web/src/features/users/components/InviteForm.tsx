'use client';

import { useTranslations } from 'next-intl';
import { useState, type FormEvent } from 'react';

import { ApiError } from '@dthcms/api-client';
import { AlertBanner, Button, Card, Input } from '@dthcms/ui';

import { StepUpCancelled, useStepUp } from '@/features/auth';

import {
  EMPLOYEE_CODE,
  PASSWORD_MIN,
  inviteUser,
  passwordAcceptable,
  type AdminAccount,
  type RoleDefinition,
} from '../api/users';
import { RolePicker } from './RolePicker';

/**
 * Inviting a colleague: an employee code, two names, the roles, and a first password.
 *
 * The password is set here rather than mailed, because the clinic's staff do not all
 * have e-mail and the person is standing at the desk. It is shown once, after the account
 * exists. A generated one is offered so nobody types "Password1234". (Changing one's own
 * password is D-72, open.)
 */
export function InviteForm({
  catalogue,
  explain,
  onInvited,
  onCancel,
}: {
  catalogue: RoleDefinition[];
  explain: (error: unknown) => string;
  onInvited: (account: AdminAccount, password: string) => void;
  onCancel: () => void;
}) {
  const t = useTranslations('users.invite');
  const requestStepUp = useStepUp();

  const [code, setCode] = useState('');
  const [nameEN, setNameEN] = useState('');
  const [nameBN, setNameBN] = useState('');
  const [phone, setPhone] = useState('');
  const [email, setEmail] = useState('');
  const [roles, setRoles] = useState<string[]>([]);
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});

  const normalisedCode = code.trim().toUpperCase();
  const ready =
    EMPLOYEE_CODE.test(normalisedCode) &&
    nameEN.trim().length >= 2 &&
    nameBN.trim().length >= 1 &&
    roles.length > 0 &&
    passwordAcceptable(password) &&
    !busy;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!ready) return;
    setBusy(true);
    setRefusal(null);
    setFields({});
    try {
      const token = await requestStepUp('user.manage', t('stepUp', { name: nameEN.trim() }));
      const account = await inviteUser(
        {
          employee_code: normalisedCode,
          name_en: nameEN.trim(),
          name_bn: nameBN.trim(),
          phone: phone.trim(),
          email: email.trim(),
          roles,
          password,
        },
        token,
      );
      onInvited(account, password);
    } catch (error) {
      if (error instanceof StepUpCancelled) return;
      if (error instanceof ApiError && error.status === 422 && Object.keys(error.fields).length) {
        setFields(error.fields);
        setRefusal(t('checkFields'));
      } else {
        setRefusal(explain(error));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card header={<h2 className="app-card__title">{t('title')}</h2>}>
      <form className="app-stack" onSubmit={submit} noValidate>
        <p className="app-page__description">{t('body')}</p>
        {refusal && <AlertBanner tone="critical" title={refusal} />}

        <div className="app-form-grid">
          <Input
            label={t('code')}
            name="employee_code"
            value={code}
            onChange={(event) => setCode(event.target.value)}
            description={t('codeHint')}
            error={fields.employee_code}
            autoCapitalize="characters"
            spellCheck={false}
            disabled={busy}
            required
          />
          <Input
            label={t('phone')}
            name="phone"
            type="tel"
            value={phone}
            onChange={(event) => setPhone(event.target.value)}
            error={fields.phone}
            disabled={busy}
          />
          <Input
            label={t('nameEN')}
            name="name_en"
            value={nameEN}
            onChange={(event) => setNameEN(event.target.value)}
            error={fields.name_en}
            disabled={busy}
            required
          />
          <Input
            label={t('nameBN')}
            name="name_bn"
            lang="bn"
            value={nameBN}
            onChange={(event) => setNameBN(event.target.value)}
            error={fields.name_bn}
            disabled={busy}
            required
          />
          <Input
            label={t('email')}
            name="email"
            type="email"
            value={email}
            onChange={(event) => setEmail(event.target.value)}
            error={fields.email}
            disabled={busy}
          />
          <Input
            label={t('password')}
            name="password"
            type="text"
            autoComplete="off"
            spellCheck={false}
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            description={t('passwordHint', { min: PASSWORD_MIN })}
            error={fields.password}
            disabled={busy}
            required
            after={
              <Button
                type="button"
                variant="quiet"
                size="sm"
                onClick={() => setPassword(generatePassword())}
                disabled={busy}
              >
                {t('generate')}
              </Button>
            }
          />
        </div>

        <RolePicker catalogue={catalogue} chosen={roles} onChange={setRoles} disabled={busy} />
        {fields.roles && <AlertBanner tone="critical" title={fields.roles} />}

        <div className="app-actions">
          <Button type="submit" variant="primary" loading={busy} disabled={!ready}>
            {t('submit')}
          </Button>
          <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
            {t('cancel')}
          </Button>
        </div>
      </form>
    </Card>
  );
}

/**
 * Sixteen characters from an alphabet without look-alikes (no 0/O, 1/l/I), grouped in
 * fours so it can be read across a desk. About 80 bits.
 */
export function generatePassword(): string {
  const alphabet = 'abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789';
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  const chars = Array.from(bytes, (b) => alphabet[b % alphabet.length]);
  return [0, 4, 8, 12].map((i) => chars.slice(i, i + 4).join('')).join('-');
}
