'use client';

import { useEffect, useState } from 'react';
import { useTranslations } from 'next-intl';

import { AlertBanner } from '@dthcms/ui';

/**
 * The offline banner.
 *
 * `navigator.onLine` is a weak signal — it reports whether the device has a network
 * interface, not whether the clinic server is reachable — so this is deliberately worded
 * as a statement about the device rather than a diagnosis. The stronger signal is a query
 * that failed, and each screen shows that where the data would have been.
 *
 * It starts as online and corrects itself after mount. Rendering "offline" during
 * server-side rendering would flash a warning at everyone on every first paint.
 *
 * The tone is `info` rather than one of the clinical statuses. `stale` would read well
 * here — the data may indeed be out of date — but those seven tones mean something
 * specific about a measurement, and spending one of them on a network condition is how a
 * status vocabulary stops being a vocabulary.
 */
export function OfflineBanner() {
  const t = useTranslations('connection');
  const [online, setOnline] = useState(true);

  useEffect(() => {
    const update = () => setOnline(navigator.onLine);
    update();
    window.addEventListener('online', update);
    window.addEventListener('offline', update);
    return () => {
      window.removeEventListener('online', update);
      window.removeEventListener('offline', update);
    };
  }, []);

  if (online) return null;

  return (
    <AlertBanner tone="info" title={t('offline')}>
      {t('offlineDetail')}
    </AlertBanner>
  );
}
