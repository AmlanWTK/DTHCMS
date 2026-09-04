import { NextRequest } from 'next/server';
import { afterEach, describe, expect, it, vi } from 'vitest';

import proxy from '@/proxy';

/**
 * The Content Security Policy.
 *
 * This file had no test until CP13, and it is the one place in the web application where
 * being wrong is silent: a policy that omits the API origin produces a feature that works
 * in every test and is refused by the browser. That is not hypothetical — it happened at
 * CP10, and Lighthouse caught it rather than the suite.
 *
 * Playwright covers whether a real browser accepts the policy. These cover the parts a
 * browser cannot tell you about: that the nonce is fresh per response, and that the
 * production policy does not carry the development relaxations.
 */

function parse(header: string): Record<string, string> {
  return Object.fromEntries(
    header.split('; ').map((directive) => {
      const [name, ...rest] = directive.split(' ');
      return [name ?? '', rest.join(' ')];
    }),
  );
}

function run(url = 'http://localhost:3100/queue') {
  const response = proxy(new NextRequest(new URL(url)));
  const header = response.headers.get('Content-Security-Policy') ?? '';
  return { response, header, directives: parse(header) };
}

afterEach(() => {
  vi.unstubAllEnvs();
  vi.resetModules();
});

describe('the nonce', () => {
  it('is different on every response', () => {
    // A nonce reused across responses is not a nonce; it is a password an attacker can
    // read off one page and use on the next.
    const first = run().directives['script-src'];
    const second = run().directives['script-src'];
    expect(first).not.toBe(second);
  });

  it('reaches the layout through a request header, since the page needs the same value', () => {
    const { response } = run();
    expect(response.headers.get('x-middleware-request-x-nonce')).toBeTruthy();
  });

  it('is the one in the policy', () => {
    const { response, directives } = run();
    const forwarded = response.headers.get('x-middleware-request-x-nonce');
    expect(directives['script-src']).toContain(`'nonce-${forwarded}'`);
  });
});

describe('what the policy forbids', () => {
  it('runs only nonce-carrying scripts, and what they load', () => {
    // 'strict-dynamic' is what makes this worth doing. Without it an origin allowlist is
    // only as strong as its weakest entry; with it, an injected <script> in a patient
    // name does not execute.
    expect(run().directives['script-src']).toContain(`'strict-dynamic'`);
  });

  it.each([
    ['frame-ancestors', `'none'`],
    ['frame-src', `'none'`],
    ['object-src', `'none'`],
    ['base-uri', `'self'`],
    // A form that posts anywhere but here is a credential-harvesting form.
    ['form-action', `'self'`],
  ])('pins %s to %s', (directive, value) => {
    expect(run().directives[directive]).toBe(value);
  });

  it('upgrades insecure requests', () => {
    expect(run().header).toContain('upgrade-insecure-requests');
  });
});

describe('the development relaxations', () => {
  it('are absent in production', () => {
    /*
     * The one that matters for CP16's review. Turbopack's HMR client evaluates code, so
     * development needs 'unsafe-eval' — and a production build that shipped it would
     * quietly undo most of what 'strict-dynamic' buys.
     */
    vi.stubEnv('NODE_ENV', 'production');
    expect(run().directives['script-src']).not.toContain('unsafe-eval');
  });

  it('are present in development, or the dev server cannot hot-reload', () => {
    vi.stubEnv('NODE_ENV', 'development');
    expect(run().directives['script-src']).toContain(`'unsafe-eval'`);
  });
});

describe('connect-src', () => {
  it('is self plus the gateway when the API shares the origin', () => {
    // The real deployment shape: CP03 puts both behind one hostname, which ADR-0010
    // needs anyway so the session cookie travels.
    const { directives } = run('http://localhost:8080/queue');
    expect(directives['connect-src']).toContain(`'self'`);
    expect(directives['connect-src']).toContain('ws://localhost:8080');
  });

  it('names the API origin when it is elsewhere', () => {
    // Local development, where the Go service is on its own port. Omitting this line is
    // exactly the CP10 defect: every call refused by the browser, every test still green.
    const { directives } = run('http://localhost:3100/queue');
    expect(directives['connect-src']).toContain('http://localhost:8080');
  });

  it('names the realtime gateway, because a ws:// URL is not covered by self', () => {
    // The CP27 repeat of the same defect. `connect-src 'self'` looks like it covers a
    // socket to the same host and does not: the scheme differs, so the browser refuses
    // the handshake — silently, in production, and nowhere a unit test would see it.
    const { directives } = run('http://localhost:3100/queue');
    expect(directives['connect-src']).toContain('ws://localhost:8080');
  });
});
