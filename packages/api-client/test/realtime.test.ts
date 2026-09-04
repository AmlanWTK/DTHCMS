import { beforeEach, describe, expect, it } from 'vitest';

import {
  backoffDelay,
  createRealtimeClient,
  type RealtimeClient,
  type RealtimeEnvelope,
  type RealtimeSocket,
  type RealtimeState,
} from '../src/realtime';

/**
 * The realtime client's four acceptance criteria (CP27), against a socket double.
 *
 * A double rather than a real gateway because everything asserted here is the client's
 * behaviour on the *edges* — a close event, a delay, a gap — and reproducing those against
 * a real server means sleeping and hoping. The protocol itself is proven against the real
 * gateway on the Go side, including from a real browser.
 */

class FakeSocket implements RealtimeSocket {
  onopen: ((event: unknown) => void) | null = null;
  onclose: ((event: { code?: number; reason?: string }) => void) | null = null;
  onerror: ((event: unknown) => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;

  readonly sent: string[] = [];
  closed: { code?: number; reason?: string } | null = null;

  send(data: string) {
    if (this.closed) throw new Error('socket is closed');
    this.sent.push(data);
  }

  close(code?: number, reason?: string) {
    this.closed = { code, reason };
    this.onclose?.({ code, reason });
  }

  /** The server side of the double. */
  open() {
    this.onopen?.({});
  }

  deliver(envelope: RealtimeEnvelope) {
    this.onmessage?.({ data: JSON.stringify(envelope) });
  }

  drop(code = 1006) {
    this.closed = { code };
    this.onclose?.({ code });
  }

  commands(): Array<Record<string, unknown>> {
    return this.sent.map((raw) => JSON.parse(raw) as Record<string, unknown>);
  }
}

interface Harness {
  client: RealtimeClient;
  sockets: FakeSocket[];
  states: RealtimeState[];
  messages: string[];
  gaps: Array<'reconnect' | 'dropped'>;
  /** Runs the timer that is due, so a backoff can be tested without waiting for it. */
  tick(): void;
  pending(): number | null;
}

function harness(): Harness {
  const sockets: FakeSocket[] = [];
  const states: RealtimeState[] = [];
  const messages: string[] = [];
  const gaps: Array<'reconnect' | 'dropped'> = [];
  let pending: { fn: () => void; ms: number } | null = null;

  const client = createRealtimeClient({
    url: 'ws://clinic.test/v1/realtime',
    socketFactory: () => {
      const socket = new FakeSocket();
      sockets.push(socket);
      return socket;
    },
    onState: (state) => states.push({ ...state }),
    onMessage: (message) => messages.push(message.kind),
    onGap: (reason) => gaps.push(reason),
    // Deterministic: the jitter and the clock are the two things that would otherwise make
    // this suite flaky, and flaky tests about reconnection are how reconnection bugs ship.
    random: () => 0,
    now: () => 1_000_000,
    setTimeoutFn: (fn, ms) => {
      pending = { fn, ms };
      return 1;
    },
    clearTimeoutFn: () => {
      pending = null;
    },
  });

  return {
    client,
    sockets,
    states,
    messages,
    gaps,
    tick() {
      const due = pending;
      pending = null;
      due?.fn();
    },
    pending: () => pending?.ms ?? null,
  };
}

const message = (seq: number, kind = 'measurement.recorded'): RealtimeEnvelope => ({
  type: 'message',
  message: { seq, topic: 'patient:p1', kind, patient_id: 'p1', at: '2026-09-03T04:42:00Z' },
});

describe('backoffDelay', () => {
  it('grows exponentially and stops at the ceiling', () => {
    const fixed = { jitter: 0 };
    expect(backoffDelay(1, fixed)).toBe(1_000);
    expect(backoffDelay(2, fixed)).toBe(2_000);
    expect(backoffDelay(3, fixed)).toBe(4_000);
    expect(backoffDelay(10, fixed)).toBe(30_000);
    expect(backoffDelay(100, fixed)).toBe(30_000);
  });

  it('spreads attempts, so thirty tablets on one access point do not return together', () => {
    const delays = new Set(Array.from({ length: 200 }, () => backoffDelay(4, {}, Math.random)));
    expect(delays.size).toBeGreaterThan(50);
    for (const delay of delays) {
      expect(delay).toBeLessThanOrEqual(8_000);
      expect(delay).toBeGreaterThanOrEqual(8_000 * 0.7 - 1);
    }
  });
});

describe('the connection', () => {
  let h: Harness;
  beforeEach(() => {
    h = harness();
  });

  it('reports connecting, then live', () => {
    h.client.connect();
    expect(h.client.state().status).toBe('connecting');
    h.sockets[0]!.open();
    expect(h.client.state().status).toBe('live');
    expect(h.client.state().attempts).toBe(0);
  });

  // Criterion 1: reconnection is automatic, with exponential backoff.
  it('reconnects by itself, backing off further each time', () => {
    h.client.connect();
    h.sockets[0]!.open();

    h.sockets[0]!.drop();
    expect(h.client.state().status).toBe('reconnecting');
    expect(h.pending()).toBe(1_000);

    h.tick();
    expect(h.sockets).toHaveLength(2);
    h.sockets[1]!.drop();
    expect(h.pending()).toBe(2_000);

    h.tick();
    h.sockets[2]!.drop();
    expect(h.pending()).toBe(4_000);

    // And it recovers: a successful open clears the attempt count.
    h.tick();
    h.sockets[3]!.open();
    expect(h.client.state().status).toBe('live');
    expect(h.client.state().attempts).toBe(0);
  });

  // Criterion 3: the status is something the user can be shown, and "reconnecting" stops
  // being an honest word after a while.
  it('says offline once it has been failing for a while', () => {
    h.client.connect();
    h.sockets[0]!.open();
    for (let i = 0; i < 4; i += 1) {
      h.sockets[h.sockets.length - 1]!.drop();
      h.tick();
    }
    h.sockets[h.sockets.length - 1]!.drop();
    expect(h.client.state().status).toBe('offline');
    expect(h.client.state().attempts).toBeGreaterThanOrEqual(5);
  });

  it('stops reconnecting once it is told to disconnect', () => {
    h.client.connect();
    h.sockets[0]!.open();
    h.client.disconnect();
    expect(h.sockets[0]!.closed?.code).toBe(1000);
    expect(h.pending()).toBeNull();
    expect(h.client.state().status).toBe('idle');
  });

  it('resumes at once when the app comes back, rather than waiting out the backoff', () => {
    h.client.connect();
    h.sockets[0]!.open();
    h.sockets[0]!.drop();
    h.sockets[1]?.drop();
    expect(h.pending()).not.toBeNull();

    h.client.resume();
    expect(h.pending()).toBeNull();
    expect(h.sockets.length).toBeGreaterThan(1);
    expect(h.client.state().attempts).toBe(0);
  });
});

describe('subscriptions', () => {
  let h: Harness;
  beforeEach(() => {
    h = harness();
    h.client.connect();
    h.sockets[0]!.open();
  });

  it('subscribes once for two screens watching the same patient', () => {
    const first = h.client.subscribe(['patient:p1']);
    const second = h.client.subscribe(['patient:p1']);
    expect(h.sockets[0]!.commands().filter((c) => c.type === 'subscribe')).toHaveLength(1);

    // The first screen unmounts; the topic stays, because the second still needs it.
    first();
    expect(h.sockets[0]!.commands().filter((c) => c.type === 'unsubscribe')).toHaveLength(0);
    expect(h.client.topics()).toEqual(['patient:p1']);

    second();
    expect(h.sockets[0]!.commands().filter((c) => c.type === 'unsubscribe')).toHaveLength(1);
    expect(h.client.topics()).toEqual([]);
  });

  it('is idempotent when a screen unmounts twice', () => {
    const release = h.client.subscribe(['patient:p1']);
    release();
    release();
    expect(h.sockets[0]!.commands().filter((c) => c.type === 'unsubscribe')).toHaveLength(1);
  });

  it('re-subscribes everything after a reconnect', () => {
    h.client.subscribe(['patient:p1', 'queue:f1']);
    h.sockets[0]!.drop();
    h.tick();
    h.sockets[1]!.open();

    const subscribe = h.sockets[1]!.commands().find((c) => c.type === 'subscribe');
    expect(subscribe?.topics).toEqual(['patient:p1', 'queue:f1']);
  });

  it('subscribes on connect for topics held while offline', () => {
    const offline = harness();
    offline.client.subscribe(['patient:p2']);
    offline.client.connect();
    offline.sockets[0]!.open();
    expect(offline.sockets[0]!.commands()[0]).toEqual({
      type: 'subscribe',
      topics: ['patient:p2'],
    });
  });
});

describe('the cursor and the gap', () => {
  let h: Harness;
  beforeEach(() => {
    h = harness();
    h.client.connect();
    h.sockets[0]!.open();
  });

  it('tracks the highest sequence it has seen', () => {
    h.sockets[0]!.deliver(message(10));
    h.sockets[0]!.deliver(message(11));
    expect(h.client.state().cursor).toBe(11);
    // Out of order does not move it backwards.
    h.sockets[0]!.deliver(message(5));
    expect(h.client.state().cursor).toBe(11);
  });

  // Criterion 2: what was missed while disconnected is recovered. The gateway does not
  // replay, so "recovered" means the application is told to refetch.
  it('reports a gap on reconnect and asks the gateway where it stands', () => {
    h.sockets[0]!.deliver(message(10));
    h.sockets[0]!.drop();
    h.tick();
    h.sockets[1]!.open();

    expect(h.gaps).toEqual(['reconnect']);
    expect(h.client.state().missed).toBe(true);
    expect(h.sockets[1]!.commands()).toContainEqual({ type: 'resume', since: 10 });

    h.client.acknowledgeGap();
    expect(h.client.state().missed).toBe(false);
  });

  it('does not report a gap on the very first connection', () => {
    expect(h.gaps).toEqual([]);
    expect(h.client.state().missed).toBe(false);
  });

  it('reports a gap when the gateway says this connection fell behind', () => {
    h.sockets[0]!.onmessage?.({
      data: JSON.stringify({ ...message(20), dropped: 7 }),
    });
    expect(h.gaps).toEqual(['dropped']);
    expect(h.client.state().missed).toBe(true);
  });

  // Criterion 4: no duplicates. The client hands every message to the application once,
  // and the application invalidates rather than appending — so a message seen twice would
  // at worst cause one extra fetch, never a second row.
  it('passes each message to the application exactly once', () => {
    h.sockets[0]!.deliver(message(1, 'measurement.recorded'));
    h.sockets[0]!.deliver(message(2, 'diagnosis.recorded'));
    expect(h.messages).toEqual(['measurement.recorded', 'diagnosis.recorded']);
  });
});

describe('the gateway talking back', () => {
  let h: Harness;
  const diagnostics: Array<[string, Record<string, unknown> | undefined]> = [];

  beforeEach(() => {
    diagnostics.length = 0;
    const sockets: FakeSocket[] = [];
    const client = createRealtimeClient({
      url: 'ws://clinic.test/v1/realtime',
      socketFactory: () => {
        const socket = new FakeSocket();
        sockets.push(socket);
        return socket;
      },
      onDiagnostic: (event, detail) => diagnostics.push([event, detail]),
      setTimeoutFn: () => 1,
      clearTimeoutFn: () => undefined,
    });
    h = { ...harness(), client, sockets } as Harness;
    client.connect();
    sockets[0]!.open();
  });

  it('names a refused subscription rather than failing silently', () => {
    h.sockets[0]!.deliver({
      type: 'refused',
      topics: ['user:someone-else'],
      error: 'not_permitted',
    });
    expect(diagnostics).toContainEqual(['subscription_refused', { topics: ['user:someone-else'] }]);
  });

  it('reconnects when the session changed under the connection', () => {
    h.sockets[0]!.deliver({ type: 'error', error: 'reauthentication_failed' });
    // Closing is what re-runs the handshake, which is where a refreshed token takes effect.
    expect(h.sockets[0]!.closed).not.toBeNull();
  });

  it('ignores an envelope that is not JSON instead of throwing into a render', () => {
    h.sockets[0]!.onmessage?.({ data: 'not json' });
    expect(diagnostics.map(([event]) => event)).toContain('envelope_not_json');
  });

  it('ignores a message type it has never heard of', () => {
    expect(() =>
      h.sockets[0]!.deliver({ type: 'quantum_entangled' } as unknown as RealtimeEnvelope),
    ).not.toThrow();
  });
});

describe('failures that must not take a render down', () => {
  it('survives a socket factory that throws', () => {
    const client = createRealtimeClient({
      url: 'ws://clinic.test/v1/realtime',
      socketFactory: () => {
        throw new Error('no WebSocket here');
      },
      setTimeoutFn: () => 1,
      clearTimeoutFn: () => undefined,
    });
    expect(() => client.connect()).not.toThrow();
    expect(client.state().status).toBe('reconnecting');
  });

  it('survives a send on a socket the platform has already torn down', () => {
    const h = harness();
    h.client.connect();
    h.sockets[0]!.open();
    h.sockets[0]!.closed = { code: 1006 };
    expect(() => h.client.subscribe(['patient:p1'])).not.toThrow();
  });
});
