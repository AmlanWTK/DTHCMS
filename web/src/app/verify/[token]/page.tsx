import { useTranslations } from 'next-intl';

import { AlertBanner, Card } from '@dthcms/ui';

/**
 * Public prescription verification.
 *
 * The one route in the application that is not behind authentication, and the reason the
 * locale is a query parameter here rather than a user preference: the person reading this
 * page scanned a QR code on a piece of paper and has no account, no session and no stored
 * language. The printed code carries `?lang=bn` so the page opens in the language the
 * prescription was printed in.
 *
 * What it will never show is the prescription's contents. Verification answers one
 * question — did DTHC issue this? — and a public page that answered any more than that
 * would be a patient record with a URL.
 */
export default async function VerifyPage({ params }: { params: Promise<{ token: string }> }) {
  const { token } = await params;
  return <VerifyPanel token={token} />;
}

function VerifyPanel({ token }: { token: string }) {
  const t = useTranslations('verify');

  return (
    <div className="app-centred">
      <Card className="app-centred__panel">
        <div className="app-stack">
          <div>
            <h1 className="app-page__title">{t('title')}</h1>
            <p className="app-page__description">{t('subtitle')}</p>
          </div>

          <AlertBanner tone="info" title={t('publicNotice')} />

          <p>
            {t('token')}: <code className="dthc-mono">{token}</code>
          </p>

          <p className="app-page__description">{t('placeholderNotice')}</p>
        </div>
      </Card>
    </div>
  );
}
