import type { Metadata } from 'next';
import { getTranslations } from 'next-intl/server';

import { Card } from '@dthcms/ui';

import { PublicVerificationPanel } from '@/features/signing';

/**
 * Public prescription verification (CP85).
 *
 * **The only route in this application that is not behind authentication, besides login**, and the
 * one a stranger reaches by pointing a phone at a piece of paper. Everything about it is decided by
 * that sentence.
 *
 * # What this page does and does not fetch
 *
 * The verification is fetched **in the browser**, by `PublicVerificationPanel`, with a bare `fetch`
 * that carries no credential. Not on the server: a server-side fetch would put the clinic's own
 * infrastructure between a stranger's scan and the API, which means our logs, our rate limiter's
 * blind spot — every request would arrive at the API from one address — and our timing. The
 * endpoint is rate-limited by address precisely because the caller is a stranger, and a page that
 * proxied the call would collapse every stranger into one budget.
 *
 * # The locale, and a gap worth naming
 *
 * The language comes from the application's own locale cookie, like every other screen. **For a
 * stranger that cookie does not exist**, so this page opens in the default — English — for exactly
 * the reader most likely to want Bangla. Both messages are on screen in the verdict banner because
 * the server composes them in both languages, so nobody is stranded; but the page's own furniture
 * follows a preference the reader never set. Carrying the language in the QR's URL is the fix, and
 * it belongs with CP89, which is what prints the code.
 *
 * # The token
 *
 * It is in the path, which means it is in the browser's history and in the address bar. That is
 * unavoidable — it arrives from a camera — and it is why the token is opaque: 160 bits from a
 * cryptographic source, carrying no patient data and no prescription id, so a URL left in a shared
 * phone's history is a URL and not a record.
 *
 * It is **not** echoed back on this page. An earlier draft printed "Reference on the prescription:
 * <token>" under the heading, which turns a shoulder-surfed screen into a working code; the
 * prescription id on a verified response is what a person names when they ring the clinic, and it
 * is already on their paper.
 */

export const metadata: Metadata = {
  // Nothing in this application is indexed, and this page least of all: a search engine that
  // crawled verification URLs would be publishing a list of live tokens.
  robots: { index: false, follow: false },
};

export default async function VerifyPage({ params }: { params: Promise<{ token: string }> }) {
  const { token } = await params;
  const t = await getTranslations('verify');

  return (
    <div className="app-centred app-verify">
      <Card className="app-centred__panel">
        <div className="app-stack">
          <header>
            <h1 className="app-page__title">{t('title')}</h1>
            <p className="app-page__description">{t('subtitle')}</p>
          </header>

          {/*
            No footer restating the limit.
            //
            // An earlier version said it twice — once in the bordered note inside the panel and
            // again in a grey line down here, in a different voice. Saying it twice is what makes
            // a sentence read as boilerplate, and this is the page's strongest claim: it has to
            // land once, hard. The one that stayed is the bordered note, because its second
            // sentence — "A page that shows those is not this one" — is the part that does the
            // work, by telling a reader how to recognise a page pretending to be this one.
          */}
          <PublicVerificationPanel token={token} />
        </div>
      </Card>
    </div>
  );
}
