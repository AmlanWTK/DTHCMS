import Link from 'next/link';
import { useTranslations } from 'next-intl';

import { Card } from '@dthcms/ui';

export default function NotFound() {
  const t = useTranslations('error.notFound');

  return (
    <div className="app-centred">
      <Card className="app-centred__panel">
        <div className="app-stack">
          <h1 className="app-page__title">{t('title')}</h1>
          <p className="app-page__description">{t('body')}</p>
          <Link href="/dashboard">{t('action')}</Link>
        </div>
      </Card>
    </div>
  );
}
