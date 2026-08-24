import { useTranslations } from 'next-intl';

import { AlertBanner, Button, Card, Input } from '@dthcms/ui';

import { LanguageToggle } from '@/components/LanguageToggle';

/**
 * The sign-in page — a placeholder, and deliberately an inert one.
 *
 * There is no form action, no state and no request. A login form that looks functional
 * and silently does nothing is worse than one that says it is not built: somebody will
 * type a real password into it.
 *
 * The language switch is here rather than only in the shell because the first thing a
 * new member of staff needs is an interface they can read, and at this point they have no
 * account to hold a preference.
 */
export default function LoginPage() {
  const t = useTranslations('login');

  return (
    <Card className="app-centred__panel">
      <div className="app-stack">
        <div>
          <h1 className="app-page__title">{t('title')}</h1>
          <p className="app-page__description">{t('subtitle')}</p>
        </div>

        <AlertBanner tone="info" title={t('placeholderNotice')} />

        <Input label={t('username')} name="username" autoComplete="off" disabled />
        <Input label={t('password')} name="password" type="password" autoComplete="off" disabled />

        <Button variant="primary" block disabled>
          {t('submit')}
        </Button>

        <LanguageToggle />
      </div>
    </Card>
  );
}
