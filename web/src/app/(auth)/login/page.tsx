import { useTranslations } from 'next-intl';
import { Suspense } from 'react';

import { Card } from '@dthcms/ui';

import { LanguageToggle } from '@/components/LanguageToggle';
import { LoginForm } from '@/features/auth';

/**
 * The sign-in page.
 *
 * The language switch is here rather than only in the shell because the first thing a
 * new member of staff needs is an interface they can read, and at this point they have no
 * account to hold a preference.
 *
 * The form reads `?next=` and so is a client component under Suspense; the frame around
 * it is static and renders on the server.
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

        <Suspense fallback={null}>
          <LoginForm />
        </Suspense>

        <LanguageToggle />
      </div>
    </Card>
  );
}
