import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { RealtimeState, RealtimeStatus } from '@dthcms/api-client';

import { renderWithProviders } from './render';

/**
 * The banner that tells a clinician the screen may be behind (CP27).
 *
 * The connection is a notification channel, so a screen that loses it keeps showing the
 * numbers it had. That is fine for the operator glancing at a queue and dangerous for the
 * one about to act on a potassium result — which is what this banner exists for, and why
 * the two things worth testing are *when it appears* and *what it says*.
 *
 * It must not appear while reconnecting. A clinic's wifi drops several times an hour, and a
 * banner that flashes on every blip is a banner people learn to scroll past — at which point
 * it is not there for the one time the connection is genuinely gone.
 *
 * And it must not overstate. The API is a different connection: a write that reaches it is
 * recorded whether or not this socket is up. A banner that said "you cannot save" would stop
 * a nurse recording an observation that would have saved perfectly.
 *
 * The connection state is supplied directly rather than by driving a socket to the fifth
 * failed retry, which is minutes of backoff for a boolean.
 */

const connection = vi.hoisted(() => ({ status: 'live' as RealtimeStatus }));

vi.mock('@/features/realtime/RealtimeProvider', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/features/realtime/RealtimeProvider')>();
  return {
    ...actual,
    useRealtime: (): RealtimeState => ({
      status: connection.status,
      cursor: 0,
      attempts: 0,
      retryAt: null,
      missed: false,
    }),
  };
});

const { StaleDataBanner } = await import('@/features/realtime/components/StaleDataBanner');

beforeEach(() => {
  connection.status = 'live';
});

describe('when the banner appears', () => {
  it.each(['idle', 'connecting', 'live', 'reconnecting'] as const)(
    'stays out of the way while the connection is %s',
    (status) => {
      connection.status = status;
      const { container } = renderWithProviders(<StaleDataBanner />);
      expect(container).toBeEmptyDOMElement();
    },
  );

  it('says nothing while reconnecting, which is the ordinary state of clinic wifi', () => {
    // Split out from the table because it is the decision, not a case of it: `reconnecting`
    // means the client still expects the connection back in a moment. A banner here would
    // fire several times an hour and teach everyone to ignore it.
    connection.status = 'reconnecting';
    renderWithProviders(<StaleDataBanner />);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(screen.queryByText('Not live')).not.toBeInTheDocument();
  });

  it('appears once the client has given up, in words rather than only a colour', () => {
    connection.status = 'offline';
    renderWithProviders(<StaleDataBanner />);
    expect(screen.getByText('Not live')).toBeInTheDocument();
  });
});

describe('what the banner claims', () => {
  beforeEach(() => {
    connection.status = 'offline';
  });

  it('says the screen may be behind', () => {
    renderWithProviders(<StaleDataBanner />);
    expect(screen.getByRole('status')).toHaveTextContent(/may be behind/i);
  });

  it('says saving still works, because it does', () => {
    // The narrowness is the whole design. The API is a different connection; a write that
    // reaches it is recorded whether or not this socket is up. A banner read as "you cannot
    // save" would stop a nurse recording an observation that would have gone through.
    renderWithProviders(<StaleDataBanner />);
    expect(screen.getByRole('status')).toHaveTextContent(/still recorded/i);
  });

  it('is polite, not an interruption', () => {
    // A screen reader mid-sentence is interrupted by role="alert". That is right for a panic
    // potassium and wrong for a dropped socket; a page that interrupts for everything is a
    // page whose interruptions stop meaning anything.
    renderWithProviders(<StaleDataBanner />);
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByRole('status')).toHaveAttribute('aria-live', 'polite');
  });

  it('reads in Bangla for a clinician working in Bangla', () => {
    // The person most likely to be reading a screen that has quietly stopped updating is the
    // one at the desk all day, and they work in Bangla.
    renderWithProviders(<StaleDataBanner />, { locale: 'bn' });
    expect(screen.getByText('লাইভ নয়')).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('স্বয়ংক্রিয় হালনাগাদ বন্ধ আছে');
  });
});
