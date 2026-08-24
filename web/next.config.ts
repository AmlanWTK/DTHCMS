import createNextIntlPlugin from 'next-intl/plugin';
import type { NextConfig } from 'next';

/**
 * The locale is not in the URL.
 *
 * next-intl's documented default puts it there — /bn/patients/123. For an authenticated
 * clinical application that is the wrong trade. There is no SEO to win, and a physician
 * sending a colleague a link to a patient would impose their own interface language on
 * whoever opens it. Language belongs to the person, not to the resource.
 *
 * The one exception is the public prescription-verification page, which a patient reaches
 * by scanning a printed QR code with no session at all. That page takes the language
 * explicitly — /verify/{token}?lang=bn — so the printed code carries the language the
 * prescription was printed in.
 */
const withNextIntl = createNextIntlPlugin('./src/lib/i18n/request.ts');

/**
 * Headers that are not CSP.
 *
 * CSP is set per-request in middleware.ts, because a strict policy needs a fresh nonce on
 * every response and a static header cannot have one. Everything here is constant.
 */
const securityHeaders = [
  // A clinical record must not be sniffed into executing.
  { key: 'X-Content-Type-Options', value: 'nosniff' },
  // A patient identifier must never leave in a Referer header to a third party.
  { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
  // Nothing in this application needs a camera, a microphone or a location. Station
  // capture on Android is the mobile app's job, not this one's.
  {
    key: 'Permissions-Policy',
    value: 'camera=(), microphone=(), geolocation=(), interest-cohort=()',
  },
  // Belt and braces alongside frame-ancestors, for anything that predates CSP level 2.
  { key: 'X-Frame-Options', value: 'DENY' },
];

const nextConfig: NextConfig = {
  reactStrictMode: true,

  // The shell is an application, not a document set. Its output is served behind
  // authentication from CP16, so nothing here should be statically cacheable by a proxy.
  poweredByHeader: false,

  // @dthcms/ui and @dthcms/design-tokens ship TypeScript source rather than a build step,
  // which is deliberate: one fewer artefact to go stale. Next compiles them itself.
  transpilePackages: ['@dthcms/ui', '@dthcms/design-tokens'],

  async headers() {
    return [{ source: '/:path*', headers: securityHeaders }];
  },
};

export default withNextIntl(nextConfig);
