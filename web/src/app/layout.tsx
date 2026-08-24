import type { Metadata, Viewport } from 'next';
import { NextIntlClientProvider } from 'next-intl';
import { getLocale, getMessages } from 'next-intl/server';

/*
 * Fonts, self-hosted from npm.
 *
 * Not next/font/google. That fetches from fonts.googleapis.com at build time, which
 * makes every build — including CI, and including a build run at the clinic — depend on
 * reaching Google. It is also a hard build failure when that fetch fails, which is a
 * strange way for a font to break a deployment.
 *
 * Fontsource ships the same faces, under the same licence, as npm packages. The family
 * names it registers — "Inter", "Noto Sans Bengali" — are exactly the first entries in
 * the token font stacks, so nothing has to be overridden here and the stack in
 * typography.json stays the single source.
 *
 * Only the weights the design system uses are imported. Each carries a unicode-range, so
 * a browser rendering an English screen never downloads the Bengali file.
 */
import '@fontsource/inter/400.css';
import '@fontsource/inter/500.css';
import '@fontsource/inter/600.css';
import '@fontsource/inter/700.css';
import '@fontsource/noto-sans-bengali/400.css';
import '@fontsource/noto-sans-bengali/500.css';
import '@fontsource/noto-sans-bengali/600.css';
import '@fontsource/noto-sans-bengali/700.css';

import '@dthcms/design-tokens/css';
import '@dthcms/design-tokens/print';
import '@dthcms/ui/styles.css';
import '@/styles/globals.css';

import { Providers } from '@/components/providers';
import type { Locale } from '@/lib/i18n/config';

export const metadata: Metadata = {
  title: 'DTHCMS',
  description: 'Digital Diabetes, Thyroid & Hormone Clinic Management System',
  // Nothing in this application should ever be indexed. It is behind authentication from
  // CP16, and the one public page states its own rules.
  robots: { index: false, follow: false },
};

export const viewport: Viewport = {
  // No maximum-scale and no user-scalable=no. A physician reading a dose on a phone in a
  // bright clinic will pinch to zoom, and an application that forbids it is an
  // application they will misread.
  width: 'device-width',
  initialScale: 1,
};

export default async function RootLayout({ children }: { children: React.ReactNode }) {
  const locale = (await getLocale()) as Locale;
  const messages = await getMessages();

  // The nonce is not read here. Middleware puts it in the Content-Security-Policy header
  // and Next applies it to the scripts it injects; a component only needs to read
  // `x-nonce` if it renders a script tag of its own, and none of these do.

  return (
    // Both languages are written left to right, so `dir` is constant. It is stated rather
    // than omitted so that the day a right-to-left language is added, this is the line
    // somebody finds.
    <html lang={locale} dir="ltr">
      <body>
        <NextIntlClientProvider locale={locale} messages={messages}>
          <Providers locale={locale}>{children}</Providers>
        </NextIntlClientProvider>
      </body>
    </html>
  );
}
