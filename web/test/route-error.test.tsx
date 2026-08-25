import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { RouteError } from '@/components/RouteError';
import { isClientMinted, newCorrelationID } from '@/lib/correlation';
import { ApiError } from '@/lib/api';

import { renderWithProviders } from './render';

/**
 * The error boundary, and the reason it exists rather than a plain message.
 *
 * An operator standing in front of a patient needs something to quote down the phone, and
 * the three sources of that reference are genuinely different — the server's own ID, a
 * server-render digest, or nothing at all. The third is the one usually left as "something
 * went wrong", and it is the one where the operator most needs to be told that reporting
 * it is still worthwhile.
 */

afterEach(() => {
  vi.restoreAllMocks();
});

function silenceConsole() {
  return vi.spyOn(console, 'error').mockImplementation(() => {});
}

describe('which reference is shown', () => {
  it('quotes the server’s correlation ID when the API supplied one', () => {
    // The value written into the log line an engineer will actually search for.
    silenceConsole();
    const error = new ApiError({
      status: 500,
      code: 'INTERNAL',
      kind: 'technical',
      messageEN: 'Something went wrong.',
      messageBN: 'কিছু একটা সমস্যা হয়েছে।',
      correlationID: 'srv-abc-123',
    });

    renderWithProviders(<RouteError error={error} reset={() => {}} />);
    expect(screen.getByText(/srv-abc-123/)).toBeInTheDocument();
  });

  it('falls back to Next’s digest for a server-render failure', () => {
    // The token that appears in the server's own log, which is the only handle that
    // exists when the failure happened before the browser got anything.
    silenceConsole();
    const error = Object.assign(new Error('render blew up'), { digest: 'digest-xyz' });

    renderWithProviders(<RouteError error={error} reset={() => {}} />);
    expect(screen.getByText(/digest-xyz/)).toBeInTheDocument();
  });

  it('mints one in the browser when nothing recorded the error anywhere', () => {
    silenceConsole();
    renderWithProviders(<RouteError error={new Error('client blew up')} reset={() => {}} />);

    const reference = screen.getByText(/web-/);
    expect(reference).toBeInTheDocument();
  });

  it('says plainly that a minted reference will not appear in the clinic’s records', () => {
    /*
     * The honest half. An ID that appears in a report and in no log is itself the useful
     * signal — it means the request never arrived — but only if the person reporting it
     * has been told so.
     */
    silenceConsole();
    const { container } = renderWithProviders(
      <RouteError error={new Error('offline')} reset={() => {}} />,
    );
    const serverSide = renderWithProviders(
      <RouteError error={Object.assign(new Error('x'), { digest: 'd-1' })} reset={() => {}} />,
    );

    expect(container.textContent).not.toBe(serverSide.container.textContent);
  });
});

describe('what the operator can do', () => {
  it('offers a retry that calls back into the route', async () => {
    silenceConsole();
    const reset = vi.fn();
    renderWithProviders(<RouteError error={new Error('boom')} reset={reset} />);

    // Named rather than `getByRole('button')`: ErrorState also renders a
    // show-technical-detail toggle, and a positional query would silently start
    // clicking the wrong one the day a control is added.
    await userEvent.click(screen.getByRole('button', { name: /try again/i }));
    expect(reset).toHaveBeenCalledOnce();
  });

  it('records the failure with its reference, so the two can be tied together later', () => {
    // Console for now; CP07's browser telemetry replaces this one call site when there
    // is an endpoint to send it to.
    const logged = silenceConsole();
    renderWithProviders(<RouteError error={new Error('boom')} reset={() => {}} />);

    const payload = logged.mock.calls[0]?.[1] as { correlationId: string; message: string };
    expect(payload.message).toBe('boom');
    expect(payload.correlationId).toBeTruthy();
  });
});

describe('minted references', () => {
  it('are recognisable as client-side', () => {
    expect(isClientMinted(newCorrelationID())).toBe(true);
  });

  it('do not mistake a server ID for one of theirs', () => {
    expect(isClientMinted('0198c4e2-7f3a-7000-8c1d-2b4e6a8f0c3d')).toBe(false);
  });

  it('are unique per call', () => {
    expect(newCorrelationID()).not.toBe(newCorrelationID());
  });
});
