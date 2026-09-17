'use client';

import { useTranslations } from 'next-intl';
import { useEffect, useState } from 'react';
import QRCode from 'qrcode';

import { AlertBanner, Card } from '@dthcms/ui';

/**
 * The QR code the printed sheet carries, shown once (CP85).
 *
 * # Once, and the screen says so
 *
 * The verification token is returned by the signing call and never again: what the database holds
 * is its digest and a sealed copy that only the print model opens. So this block appears in the one
 * render that follows a signature and nowhere else, and the panel that would otherwise draw it says
 * in words that the code is not retrievable rather than leaving a blank space somebody reads as a
 * loading failure.
 *
 * Nothing here writes the token anywhere. It is a prop, it is drawn, and it goes when the component
 * does — no `localStorage`, no query cache key, no URL.
 *
 * # The QR encodes a path, and the path is the server's
 *
 * `verification_path` is composed by the Go side so that the printed sheet and the public page
 * cannot disagree about what a token URL looks like. This component renders the origin in front of
 * it only for the human-readable line, and puts the same absolute URL in the code — a phone camera
 * needs a URL, and a relative path in a QR is a code that opens nothing.
 *
 * # It is not a verification
 *
 * The code on the paper says *here is how to check this*, never *this is checked*. A claim of
 * verification printed on a sheet is worth nothing, because the sheet says whatever it was printed
 * with. That is the whole reason the public page exists, and the caption says so.
 */
export function VerificationCode({ token, path }: { token: string; path: string }) {
  const t = useTranslations('signing');
  const [image, setImage] = useState<string | null>(null);

  const url =
    typeof window === 'undefined' ? path : new URL(path, window.location.origin).toString();

  useEffect(() => {
    let cancelled = false;
    QRCode.toDataURL(url, { errorCorrectionLevel: 'M', margin: 1, width: 224 })
      .then((data) => {
        if (!cancelled) setImage(data);
      })
      .catch(() => {
        // The printed address beside it still works. The QR is a convenience for a camera, and a
        // failure to render one must not look like a failure to sign.
      });
    return () => {
      cancelled = true;
    };
  }, [url]);

  return (
    <Card compact header={<strong>{t('code.title')}</strong>}>
      <div className="app-sign__code" data-testid="verification-code">
        {image ? (
          // A plain `img` and not `next/image`: the source is a data URL generated in this
          // browser a moment ago, and the image loader optimises URLs it can fetch.
          <img src={image} alt={t('code.alt')} width={224} height={224} />
        ) : (
          <div className="app-sign__code-pending">{t('code.rendering')}</div>
        )}
        <div>
          <p className="app-sign__code-url dthc-mono" data-testid="verification-url">
            {url}
          </p>
          <p className="app-sign__note">{t('code.caption')}</p>
        </div>
      </div>
      <AlertBanner tone="high" title={t('code.onceTitle')}>
        <p style={{ margin: 0 }}>{t('code.onceBody')}</p>
      </AlertBanner>
      {/* The token in clear, small, for the case the camera cannot read the code — a pharmacist
          can type it. Deliberately below the code and not beside it: the code is what a phone
          uses, and the string is a fallback. */}
      <p className="app-sign__token dthc-mono" data-testid="verification-token">
        {token}
      </p>
      <p className="app-sign__note">{t('code.typeIt')}</p>
    </Card>
  );
}
