import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { SystemStatusCard } from '@/features/system-status/components/SystemStatusCard';
import { systemStatusKeys } from '@/features/system-status/api/useSystemStatus';

import { renderWithProviders } from './render';

/**
 * The operations panel, rendered (CP10).
 *
 * This is the one thing an operator looks at when somebody says "the system is down", and
 * the sentence it produces is the instruction the clinic acts on. Three of them are
 * genuinely different, and a panel that blurs any two sends the wrong one:
 *
 *   ready            start entering visits;
 *   live-not-ready   the process is up and cannot serve — wait, do not restart anything;
 *   unreachable      nothing answered — check the server, and quote this reference.
 *
 * So what is asserted here is what a person reads and can act on: that an unready server
 * is not shown as broken and not shown as fine, that the failing dependency is named
 * rather than left for somebody to guess, that a failure carries the correlation ID the
 * operator will read down a phone line, and that "no connection" is distinguished from
 * "the clinic server refused this" — different problems, different people to call.
 *
 * The `/readyz`-fails-but-`/healthz`-answers case is the one that matters most and is the
 * easiest to lose: it is the only state whose whole value is in *not* being an error.
 */

const CHECKED_AT = Date.parse('2026-09-14T04:42:00Z');

function respond(body: unknown, init: { status?: number } = {}) {
  return new Response(JSON.stringify(body), {
    status: init.status ?? 200,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_status_9' },
  });
}

type Handler = () => Response | Promise<Response>;

/** A scripted pair of health endpoints. Returns the paths asked for, in order. */
function server(routes: Record<string, Handler>): string[] {
  const asked: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = new Request(input, init);
      const path = new URL(request.url).pathname;
      asked.push(path);
      const handler = routes[path];
      if (!handler) throw new TypeError('Failed to fetch');
      return handler();
    }),
  );
  return asked;
}

const live = { status: 'ok', service: 'api', version: '0.1.0-dev' };
const ready = { ...live, checks: { postgres: 'ok', redis: 'ok' } };

const unavailable = {
  error: {
    code: 'UNAVAILABLE',
    kind: 'unavailable',
    message: 'The clinic server is not answering.',
    message_bn: 'ক্লিনিক সার্ভার সাড়া দিচ্ছে না।',
  },
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('while the panel is asking', () => {
  it('says what it is checking rather than showing a silent grey block', async () => {
    // A skeleton is decorative to a sighted operator and silence to everyone else. The
    // label is the only thing that distinguishes "still asking" from "finished, empty".
    server({ '/healthz': () => respond(live), '/readyz': () => respond(ready) });

    renderWithProviders(<SystemStatusCard />);

    expect(screen.getByText('Checking')).toBeInTheDocument();
    await screen.findByText('Ready to serve');
  });
});

describe('when the server is ready', () => {
  it('names the state, the service and the version an operator would quote', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(CHECKED_AT);
    server({ '/healthz': () => respond(live), '/readyz': () => respond(ready) });

    renderWithProviders(<SystemStatusCard />);

    const state = await screen.findByText('Ready to serve');
    // Brand-toned rather than a clinical green: this is an operational fact, and a
    // clinical pill next to it would be a vocabulary the value does not belong to.
    expect(state).toHaveClass('dthc-badge--brand');
    expect(screen.getByText('api')).toBeInTheDocument();
    expect(screen.getByText('0.1.0-dev')).toBeInTheDocument();
    // Ready means ready: no "not ready" notice sitting alongside the ready badge.
    expect(screen.queryByText('Reachable, but not ready')).not.toBeInTheDocument();
  });

  it('lists every dependency it was told about, by name and state', async () => {
    server({
      '/healthz': () => respond(live),
      '/readyz': () => respond({ ...live, checks: { postgres: 'ok', blobstore: 'ok' } }),
    });

    renderWithProviders(<SystemStatusCard />);

    expect(await screen.findByText('postgres: ok')).toBeInTheDocument();
    expect(screen.getByText('blobstore: ok')).toBeInTheDocument();
  });

  it('reports the time on the clinic’s clock, not the browser’s', async () => {
    // Asia/Dhaka. An operator in Dhaka comparing this against a wall clock and finding it
    // six hours out concludes the panel is stale, which is the opposite of what it says.
    vi.spyOn(Date, 'now').mockReturnValue(CHECKED_AT);
    server({ '/healthz': () => respond(live), '/readyz': () => respond(ready) });

    renderWithProviders(<SystemStatusCard />);

    const checked = await screen.findByText(/Checked at/);
    expect(checked).toHaveTextContent('10:42');
    expect(checked).not.toHaveTextContent('04:42');
  });

  it('asks again when the operator asks it to', async () => {
    const user = userEvent.setup();
    const asked = server({ '/healthz': () => respond(live), '/readyz': () => respond(ready) });
    renderWithProviders(<SystemStatusCard />);
    await screen.findByText('Ready to serve');
    expect(asked).toEqual(['/healthz', '/readyz']);

    await user.click(screen.getByRole('button', { name: 'Check again' }));

    // Both probes again. Asking only /healthz would leave a stale readiness on screen
    // beside a fresh timestamp, which is worse than not refreshing at all.
    await waitFor(() => expect(asked).toEqual(['/healthz', '/readyz', '/healthz', '/readyz']));
  });

  it('reads in Bangla, with the timestamp still in ASCII digits', async () => {
    // A date is a number somebody reads back over a phone or copies onto a paper log, so
    // it stays in ASCII whatever the interface language is (lib/formatters).
    vi.spyOn(Date, 'now').mockReturnValue(CHECKED_AT);
    server({ '/healthz': () => respond(live), '/readyz': () => respond(ready) });

    renderWithProviders(<SystemStatusCard />, { locale: 'bn' });

    expect(await screen.findByText('কাজের জন্য প্রস্তুত')).toBeInTheDocument();
    expect(screen.getByText('ক্লিনিক সার্ভার')).toBeInTheDocument();
    expect(screen.getByText(/দেখা হয়েছে/)).toHaveTextContent('10:42');
  });
});

describe('when the server is up but cannot serve', () => {
  it('is a notice, not an error — the panel stays, and says wait', async () => {
    // The whole point of the state. Hiding the panel behind an error would tell a clinic
    // the system is broken when the correct instruction is "do not start a visit yet".
    server({
      '/healthz': () => respond(live),
      '/readyz': () => respond(unavailable, { status: 503 }),
    });

    renderWithProviders(<SystemStatusCard />);

    // Said twice on purpose: once as the badge in the row of facts, once as the notice
    // that explains it. Neither is an `alert` — an assertive live region interrupts a
    // screen reader mid-sentence, which this does not warrant.
    const shown = await screen.findAllByText('Reachable, but not ready');
    const badge = shown.find((element) => element.classList.contains('dthc-badge'));
    expect(badge).toHaveClass('dthc-badge--neutral');
    expect(screen.getByRole('status')).toHaveTextContent('Reachable, but not ready');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    // Identity still comes from liveness, so the operator can still say which build.
    expect(screen.getByText('0.1.0-dev')).toBeInTheDocument();
  });

  it('names the dependency that is unhappy', async () => {
    // "Not ready" without a reason is something an operator cannot pass on to anybody.
    server({
      '/healthz': () => respond(live),
      '/readyz': () => respond({ ...live, status: 'unready', checks: { postgres: 'unavailable' } }),
    });

    renderWithProviders(<SystemStatusCard />);

    expect(await screen.findByText('postgres: unavailable')).toBeInTheDocument();
    expect(screen.getAllByText('Reachable, but not ready').length).toBeGreaterThan(0);
  });
});

describe('when nothing answers', () => {
  it('gives the operator the reference to quote down the phone', async () => {
    // Without the correlation ID the support call starts at "roughly when?", and the one
    // request that failed cannot be found in a day of logs.
    server({ '/healthz': () => respond(unavailable, { status: 503 }) });

    renderWithProviders(<SystemStatusCard />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Not reachable');
    expect(alert).toHaveTextContent('The application cannot reach the clinic server.');
    expect(screen.getByText('req_status_9')).toBeInTheDocument();
  });

  it('tells an offline browser it is offline, not that the clinic refused it', async () => {
    // Two different problems and two different people to call. The offline variant says
    // so with its own icon and wording rather than apologising for a server that is fine.
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));

    renderWithProviders(<SystemStatusCard />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveClass('dthc-state--offline');
    // A network failure has no correlation ID, because nothing reached a handler to log
    // one. Showing an empty "Reference" would send an operator hunting for a blank.
    expect(alert).not.toHaveTextContent('Reference');
  });

  it('recovers when the operator retries and the server has come back', async () => {
    const user = userEvent.setup();
    let up = false;
    server({
      '/healthz': () => (up ? respond(live) : respond(unavailable, { status: 503 })),
      '/readyz': () => respond(ready),
    });
    renderWithProviders(<SystemStatusCard />);
    await screen.findByRole('alert');

    up = true;
    await user.click(screen.getByRole('button', { name: 'Try again' }));

    expect(await screen.findByText('Ready to serve')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});

describe('the query key', () => {
  it('is exported under a stable name for CP27 to invalidate', () => {
    // The WebSocket layer invalidates this panel by name. A key spelled out at the call
    // site instead would drift the day this one changes, and the panel would quietly stop
    // refreshing on a push while still looking live.
    expect(systemStatusKeys.all).toEqual(['system-status']);
  });
});
