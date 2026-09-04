import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import { NextIntlClientProvider } from 'next-intl';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { RealtimeSocket } from '@dthcms/api-client';

import {
  ConnectionIndicator,
  RealtimeProvider,
  realtimeUrl,
  useRealtimeTopics,
} from '@/features/realtime';
import { useSessionStore } from '@/stores/session';

import en from '../messages/en.json';
import bn from '../messages/bn.json';

/**
 * The realtime integration as the shell wires it (CP27).
 *
 * A socket double stands in for the gateway: the protocol is proven against the real
 * gateway on the Go side, and what is worth checking here is the part that is this
 * application's — that a message becomes an invalidation, that the indicator says what the
 * connection is doing, and that a screen's subscription lives and dies with the screen.
 */

const sockets: FakeSocket[] = [];

class FakeSocket implements RealtimeSocket {
  onopen: ((event: unknown) => void) | null = null;
  onclose: ((event: { code?: number; reason?: string }) => void) | null = null;
  onerror: ((event: unknown) => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  readonly sent: string[] = [];
  closed = false;

  constructor(readonly url: string) {
    sockets.push(this);
  }

  send(data: string) {
    this.sent.push(data);
  }

  close() {
    this.closed = true;
    this.onclose?.({ code: 1000 });
  }

  commands() {
    return this.sent.map((raw) => JSON.parse(raw) as Record<string, unknown>);
  }
}

function signIn() {
  // act, because signing in is a store update that re-renders the provider — the same
  // update the real sign-in screen causes.
  act(() => {
    useSessionStore.setState({
      user: {
        id: 'u1',
        employee_code: 'P001',
        name_en: 'Test',
        name_bn: 'টেস্ট',
        roles: [],
        permissions: [],
      } as never,
      activeRole: 'PHYSICIAN' as never,
    });
  });
}

// One client per test, created in beforeEach: a client rebuilt on every render would make
// the provider's effect re-run and open a second socket, which is a property of the test
// and not of the application.
let queryClient: QueryClient;

function Harness({ children, locale = 'en' }: { children: ReactNode; locale?: 'en' | 'bn' }) {
  return (
    <NextIntlClientProvider locale={locale} messages={{ en, bn }[locale]} timeZone="Asia/Dhaka">
      <QueryClientProvider client={queryClient}>
        <RealtimeProvider>{children}</RealtimeProvider>
      </QueryClientProvider>
    </NextIntlClientProvider>
  );
}

beforeEach(() => {
  sockets.length = 0;
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  vi.stubGlobal('WebSocket', FakeSocket);
  useSessionStore.setState({ user: null, activeRole: null } as never);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('realtimeUrl', () => {
  it('turns the API origin into a gateway URL, and carries no credential in it', () => {
    expect(realtimeUrl('http://localhost:8080')).toBe('ws://localhost:8080/v1/realtime');
    expect(realtimeUrl('https://clinic.example')).toBe('wss://clinic.example/v1/realtime');
    // A token in a URL is a token in an access log, a Referer header and the browser's
    // history. The browser's credential is the session cookie, which it sends by itself.
    expect(realtimeUrl('https://clinic.example?token=secret')).not.toContain('token');
  });
});

describe('the connection follows the session', () => {
  it('opens nothing while nobody is signed in', () => {
    render(<Harness>ready</Harness>);
    expect(sockets).toHaveLength(0);
  });

  it('opens when somebody signs in and closes when they sign out', async () => {
    render(<Harness>ready</Harness>);
    signIn();
    await waitFor(() => expect(sockets).toHaveLength(1));

    act(() => {
      useSessionStore.setState({ user: null, activeRole: null } as never);
    });
    await waitFor(() => expect(sockets[0]!.closed).toBe(true));
  });
});

describe('the indicator', () => {
  // Criterion 3: the connection's state is visible to the user.
  it('says nothing while everything is normal and not yet connected', () => {
    render(
      <Harness>
        <ConnectionIndicator />
      </Harness>,
    );
    expect(screen.queryByText(en.realtime.live)).not.toBeInTheDocument();
  });

  it('says live once the socket is open', async () => {
    render(
      <Harness>
        <ConnectionIndicator />
      </Harness>,
    );
    signIn();
    await waitFor(() => expect(sockets).toHaveLength(1));
    act(() => {
      sockets[0]!.onopen?.({});
    });
    expect(await screen.findByText(en.realtime.live)).toBeInTheDocument();
  });

  it('says reconnecting when the socket drops, in words and not only in colour', async () => {
    render(
      <Harness>
        <ConnectionIndicator />
      </Harness>,
    );
    signIn();
    await waitFor(() => expect(sockets).toHaveLength(1));
    act(() => {
      sockets[0]!.onopen?.({});
    });
    act(() => {
      sockets[0]!.onclose?.({ code: 1006 });
    });

    const pill = await screen.findByText(en.realtime.reconnecting);
    expect(pill).toBeInTheDocument();
    // The state is on the element rather than only in a class, so the styling and the
    // assertion agree about what "reconnecting" means.
    expect(pill.closest('[data-status]')).toHaveAttribute('data-status', 'reconnecting');
  });

  it('is in Bangla when the interface is', async () => {
    render(
      <Harness locale="bn">
        <ConnectionIndicator />
      </Harness>,
    );
    signIn();
    await waitFor(() => expect(sockets).toHaveLength(1));
    act(() => {
      sockets[0]!.onopen?.({});
    });
    expect(await screen.findByText(bn.realtime.live)).toBeInTheDocument();
  });
});

describe('subscriptions follow the screen', () => {
  function Screen({ patient }: { patient: string }) {
    useRealtimeTopics([`patient:${patient}`]);
    return <div>watching {patient}</div>;
  }

  // The realistic case: the operator navigates away from a patient. The provider stays;
  // the screen goes.
  it('subscribes when the screen appears and unsubscribes when it goes', async () => {
    const view = render(
      <Harness>
        <Screen patient="p1" />
      </Harness>,
    );
    signIn();
    await waitFor(() => expect(sockets).toHaveLength(1));
    act(() => {
      sockets[0]!.onopen?.({});
    });

    await waitFor(() =>
      expect(sockets[0]!.commands()).toContainEqual({
        type: 'subscribe',
        topics: ['patient:p1'],
      }),
    );

    view.rerender(<Harness>nothing to watch</Harness>);
    await waitFor(() =>
      expect(sockets[0]!.commands()).toContainEqual({
        type: 'unsubscribe',
        topics: ['patient:p1'],
      }),
    );
    // And one socket throughout: navigating between screens does not reconnect.
    expect(sockets).toHaveLength(1);
  });

  it('moves the subscription when the operator opens a different patient', async () => {
    const view = render(
      <Harness>
        <Screen patient="p1" />
      </Harness>,
    );
    signIn();
    await waitFor(() => expect(sockets).toHaveLength(1));
    act(() => {
      sockets[0]!.onopen?.({});
    });
    await waitFor(() =>
      expect(sockets[0]!.commands()).toContainEqual({ type: 'subscribe', topics: ['patient:p1'] }),
    );

    view.rerender(
      <Harness>
        <Screen patient="p2" />
      </Harness>,
    );
    await waitFor(() =>
      expect(sockets[0]!.commands()).toContainEqual({ type: 'subscribe', topics: ['patient:p2'] }),
    );
    expect(sockets[0]!.commands()).toContainEqual({ type: 'unsubscribe', topics: ['patient:p1'] });
  });

  it('does not resubscribe when the same topics are passed as a new array', async () => {
    function Rerendering() {
      // A new array literal on every render, which is what a call site naturally writes.
      useRealtimeTopics(['patient:p1']);
      return <div>ok</div>;
    }
    const view = render(
      <Harness>
        <Rerendering />
      </Harness>,
    );
    signIn();
    await waitFor(() => expect(sockets).toHaveLength(1));
    act(() => {
      sockets[0]!.onopen?.({});
    });
    view.rerender(
      <Harness>
        <Rerendering />
      </Harness>,
    );

    await waitFor(() => {
      const subscribes = sockets[0]!.commands().filter((c) => c.type === 'subscribe');
      expect(subscribes.length).toBeLessThanOrEqual(1);
    });
  });
});

describe('what arrives becomes an invalidation and nothing else', () => {
  function deliver(socket: FakeSocket, message: Record<string, unknown>) {
    act(() => {
      socket.onmessage?.({ data: JSON.stringify({ type: 'message', message }) });
    });
  }

  async function live() {
    render(<Harness>ready</Harness>);
    signIn();
    await waitFor(() => expect(sockets).toHaveLength(1));
    act(() => {
      sockets[0]!.onopen?.({});
    });
    return sockets[0]!;
  }

  it('invalidates the patient a message names', async () => {
    const socket = await live();
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries');

    deliver(socket, {
      seq: 1,
      topic: 'patient:p1',
      kind: 'measurement.recorded',
      patient_id: 'p1',
      visit_id: 'v9',
      at: '2026-09-03T04:42:00Z',
    });

    const keys = invalidate.mock.calls.map(([arg]) => JSON.stringify(arg?.queryKey));
    expect(keys).toContain(JSON.stringify(['patient', 'p1']));
    expect(keys).toContain(JSON.stringify(['visit', 'v9', 'vitals']));
  });

  it('never writes the message into the cache', async () => {
    const socket = await live();
    const setQueryData = vi.spyOn(queryClient, 'setQueryData');

    deliver(socket, {
      seq: 2,
      topic: 'patient:p1',
      kind: 'measurement.recorded',
      patient_id: 'p1',
      summary: { code: 'HEIGHT', value: 150 },
      at: '2026-09-03T04:42:00Z',
    });

    // The whole discipline, as one assertion: a value that reached the screen through the
    // socket rather than through the API is a value no endpoint returned and no log
    // explains. The summary above carries a number precisely so this test would catch it.
    expect(setQueryData).not.toHaveBeenCalled();
  });

  it('refetches what it is watching after a reconnect, because the gateway does not replay', async () => {
    render(
      <Harness>
        <Watching />
      </Harness>,
    );
    signIn();
    await waitFor(() => expect(sockets).toHaveLength(1));
    act(() => {
      sockets[0]!.onopen?.({});
    });
    // A message, so the client has a cursor and the next connection counts as a resume.
    act(() => {
      sockets[0]!.onmessage?.({
        data: JSON.stringify({
          type: 'message',
          message: { seq: 5, topic: 'patient:p1', kind: 'measurement.recorded', at: 'now' },
        }),
      });
    });

    const invalidate = vi.spyOn(queryClient, 'invalidateQueries');
    act(() => {
      sockets[0]!.onclose?.({ code: 1006 });
    });
    // The first backoff is up to a second of real time, and testing-library's default
    // wait is exactly a second — a coin flip on a loaded machine, which is how a suite
    // becomes intermittently red.
    await waitFor(() => expect(sockets.length).toBeGreaterThan(1), { timeout: 5_000 });
    act(() => {
      sockets[1]!.onopen?.({});
    });

    await waitFor(
      () => {
        const keys = invalidate.mock.calls.map(([arg]) => JSON.stringify(arg?.queryKey));
        expect(keys).toContain(JSON.stringify(['patient', 'p1']));
      },
      { timeout: 5_000 },
    );
  });
});

function Watching() {
  useRealtimeTopics(['patient:p1']);
  return <div>watching</div>;
}
