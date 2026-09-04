import { NextResponse, type NextRequest } from 'next/server';

import { apiOrigin } from '@/lib/env';

/**
 * Content Security Policy, with a per-response nonce.
 *
 * This is Next 16's `proxy` convention — what earlier versions called `middleware`. The
 * name changed; the position in the request path did not.
 *
 * A nonce cannot live in a static header: it has to be new on every response or it is
 * not a nonce. So the policy is built here, the nonce goes into a request header that
 * the root layout reads, and Next uses the same nonce for the scripts it injects.
 *
 * `'strict-dynamic'` is what makes this worth doing. Without it, a policy that lists
 * origins is only as strong as the weakest thing on the list, and any script on an
 * allowed origin can load anything else. With it, only a script carrying the nonce runs,
 * and scripts it loads inherit that trust — so an injected `<script>` tag in a patient
 * name, the actual attack, does not execute.
 *
 * Development relaxes two things. Turbopack's HMR client evaluates code, and the dev
 * overlay uses inline styles. Both are stripped in production, which is what CP16 will
 * be reviewed against.
 */
export default function proxy(request: NextRequest) {
  const nonce = Buffer.from(crypto.randomUUID()).toString('base64');
  const isDev = process.env.NODE_ENV === 'development';

  const policy = [
    `default-src 'self'`,
    `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'${isDev ? " 'unsafe-eval'" : ''}`,
    // Fonts and token variables are bundled; styles come from our own stylesheets. The
    // inline allowance covers Next's injected critical CSS, which carries no nonce.
    `style-src 'self' 'unsafe-inline'`,
    `img-src 'self' blob: data:`,
    `font-src 'self'`,
    /*
     * The API is same-origin in a real deployment — CP03 puts both behind one hostname,
     * which ADR-0010 needs anyway so the session cookie travels — and `'self'` also
     * covers the WebSocket at CP27, since ws: to the same host is 'self'.
     *
     * In local development the Go service is on its own port, so the policy has to name
     * it or every request the browser makes is refused. Shipping without this line was a
     * real defect: nothing in the unit suite runs a browser, so the one feature CP10 has
     * would have failed on a developer's machine and passed every test.
     */
    `connect-src ${connectSources(request)}`,
    // Nothing embeds this application, and it embeds nothing.
    `frame-ancestors 'none'`,
    `frame-src 'none'`,
    `object-src 'none'`,
    `base-uri 'self'`,
    // A form that posts anywhere but here is a credential-harvesting form.
    `form-action 'self'`,
    `upgrade-insecure-requests`,
  ].join('; ');

  const headers = new Headers(request.headers);
  headers.set('x-nonce', nonce);

  const response = NextResponse.next({ request: { headers } });
  response.headers.set('Content-Security-Policy', policy);
  return response;
}

/**
 * `'self'`, the API's origin when it is somewhere else, and the realtime gateway.
 *
 * The WebSocket needs naming explicitly: `connect-src 'self'` does **not** cover a
 * `ws://` or `wss://` URL even on the same host, because the scheme differs. Leaving it
 * out blocks the realtime connection in the browser and nowhere else — which is exactly
 * the defect this policy's own e2e test was written to catch at CP10, and exactly the one
 * it caught again at CP27.
 */
function connectSources(request: NextRequest): string {
  const sources = new Set([`'self'`]);
  const origin = apiOrigin() ?? request.nextUrl.origin;
  if (origin !== request.nextUrl.origin) sources.add(origin);
  sources.add(origin.replace(/^http/, 'ws'));
  return [...sources].join(' ');
}

export const config = {
  /*
   * Everything except Next's own static output and the favicon. Those are immutable
   * assets with no script surface, and putting a per-request header on them would defeat
   * caching for no security gain.
   */
  matcher: [
    {
      source: '/((?!_next/static|_next/image|favicon.ico).*)',
      missing: [
        { type: 'header', key: 'next-router-prefetch' },
        { type: 'header', key: 'purpose', value: 'prefetch' },
      ],
    },
  ],
};
