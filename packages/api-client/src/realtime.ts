import { idempotencyKey } from '@dthcms/shared-schemas';

/**
 * The realtime client (CP27), shared by web and mobile.
 *
 * The gateway (CP26) is a notification channel, not a second read path: a message says
 * *something changed*, and the client fetches the change through the API it already has.
 * That single fact decides the shape of everything here.
 *
 *   - **Invalidate, never mutate.** A message is turned into a set of query keys to
 *     invalidate. Writing a value from a socket message into the cache means two paths
 *     produce the screen's state, and the day they disagree the screen shows something no
 *     endpoint would ever have returned. `realtimeInvalidations` is the whole mapping and
 *     it returns keys, not data.
 *   - **A gap is not a failure.** The gateway does not replay. When a reconnect finds the
 *     cursor has moved on, the client is told there was a gap and refetches everything it
 *     is watching. That is criterion 2 and criterion 4 being one mechanism.
 *   - **Subscriptions belong to screens.** A topic is subscribed while a component that
 *     needs it is mounted and dropped when it unmounts, so a clinic's socket carries what
 *     is on the screens in front of people and nothing else.
 *
 * The socket is injectable: `WebSocket` is a browser global, React Native's is a different
 * implementation, and the tests need neither.
 */

// --- the wire, as the gateway defines it ---

export type RealtimeTopic = string;

export interface RealtimeMessage {
  seq: number;
  topic: RealtimeTopic;
  kind: string;
  event_id?: string;
  patient_id?: string;
  visit_id?: string;
  summary?: Record<string, unknown>;
  at: string;
}

export interface RealtimeEnvelope {
  type:
    | 'welcome'
    | 'subscribed'
    | 'unsubscribed'
    | 'refused'
    | 'message'
    | 'resumed'
    | 'pong'
    | 'error'
    | 'closing';
  message?: RealtimeMessage;
  topics?: RealtimeTopic[];
  cursor?: number;
  error?: string;
  detail?: string;
  dropped?: number;
  at?: string;
}

/**
 * What the user is told, and what a screen decides by.
 *
 * `reconnecting` and `offline` are deliberately separate. Reconnecting is the normal state
 * of a clinic's wifi and deserves a quiet indicator; offline is "we have given up for the
 * moment", and the operator should know that what they are looking at is a snapshot.
 */
export type RealtimeStatus = 'idle' | 'connecting' | 'live' | 'reconnecting' | 'offline';

export interface RealtimeState {
  status: RealtimeStatus;
  /** The highest sequence this client has seen. Survives a reconnect. */
  cursor: number;
  /** Consecutive failed attempts. Zero while live. */
  attempts: number;
  /** When the next attempt is due, as epoch milliseconds. Null when not waiting. */
  retryAt: number | null;
  /**
   * True when the client knows it missed messages — a reconnect after a gap, or the
   * gateway reporting that this connection was too slow. The application's response is to
   * refetch what it is watching; it is cleared once it has.
   */
  missed: boolean;
}

// --- backoff ---

export interface BackoffOptions {
  /** First delay. */
  initialMs?: number;
  /** Ceiling. A clinic's connection comes back in seconds or in minutes, not in hours. */
  maxMs?: number;
  /** Multiplier per attempt. */
  factor?: number;
  /**
   * Jitter as a fraction of the delay, 0–1. Without it, thirty tablets that lost the same
   * access point reconnect in the same millisecond and knock the gateway over on the way
   * back up.
   */
  jitter?: number;
}

const DEFAULT_BACKOFF: Required<BackoffOptions> = {
  initialMs: 1_000,
  maxMs: 30_000,
  factor: 2,
  jitter: 0.3,
};

/** The delay before attempt `n` (1-based), with jitter applied. */
export function backoffDelay(
  attempt: number,
  options: BackoffOptions = {},
  random: () => number = Math.random,
): number {
  const { initialMs, maxMs, factor, jitter } = { ...DEFAULT_BACKOFF, ...options };
  const base = Math.min(initialMs * factor ** Math.max(0, attempt - 1), maxMs);
  // Full jitter downward only: never longer than the ceiling, never instant.
  const spread = base * jitter;
  return Math.round(base - spread * random());
}

// --- the client ---

export interface RealtimeClientOptions {
  /** The gateway, ws:// or wss://. */
  url: string;
  /**
   * Opens a socket. Defaults to the platform's WebSocket. Injected by the tests, and by
   * React Native if it ever needs headers the browser cannot set.
   */
  socketFactory?: (url: string) => RealtimeSocket;
  backoff?: BackoffOptions;
  /** Called on every state change. */
  onState?: (state: RealtimeState) => void;
  /** Called for each message that arrives. */
  onMessage?: (message: RealtimeMessage) => void;
  /**
   * Called when the client discovers it missed messages. The application refetches what it
   * is watching; there is nothing to replay.
   */
  onGap?: (reason: 'reconnect' | 'dropped') => void;
  /** Diagnostics. Never a place for message contents: a summary may name a patient's id. */
  onDiagnostic?: (event: string, detail?: Record<string, unknown>) => void;
  setTimeoutFn?: (fn: () => void, ms: number) => unknown;
  clearTimeoutFn?: (handle: unknown) => void;
  now?: () => number;
  random?: () => number;
}

/** The subset of WebSocket this client uses, so a test can supply a double. */
export interface RealtimeSocket {
  send(data: string): void;
  close(code?: number, reason?: string): void;
  onopen: ((event: unknown) => void) | null;
  onclose: ((event: { code?: number; reason?: string }) => void) | null;
  onerror: ((event: unknown) => void) | null;
  onmessage: ((event: { data: unknown }) => void) | null;
}

export interface RealtimeClient {
  /** Opens the connection. Safe to call twice. */
  connect(): void;
  /** Closes it and stops reconnecting. */
  disconnect(): void;
  /**
   * Subscribes while the returned function has not been called. Reference-counted: two
   * screens watching one patient share one subscription, and it is dropped when the second
   * of them unmounts.
   */
  subscribe(topics: RealtimeTopic[]): () => void;
  /** The topics currently subscribed, sorted. */
  topics(): RealtimeTopic[];
  state(): RealtimeState;
  /** Clears the `missed` flag once the application has refetched. */
  acknowledgeGap(): void;
  /**
   * A hint that the app came back to the foreground, or that a token was refreshed.
   * Reconnects immediately rather than waiting out the backoff, since the condition that
   * caused the failure has probably just changed.
   */
  resume(): void;
}

const CLOSE_GOING_AWAY = 1001;

export function createRealtimeClient(options: RealtimeClientOptions): RealtimeClient {
  const {
    url,
    socketFactory = defaultSocketFactory,
    backoff = {},
    onState,
    onMessage,
    onGap,
    onDiagnostic,
    setTimeoutFn = (fn, ms) => setTimeout(fn, ms),
    clearTimeoutFn = (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
    now = () => Date.now(),
    random = Math.random,
  } = options;

  let socket: RealtimeSocket | null = null;
  let timer: unknown = null;
  let wanted = false;
  /** Topic → how many callers hold it. */
  const counts = new Map<RealtimeTopic, number>();
  let state: RealtimeState = {
    status: 'idle',
    cursor: 0,
    attempts: 0,
    retryAt: null,
    missed: false,
  };

  function setState(next: Partial<RealtimeState>) {
    state = { ...state, ...next };
    onState?.(state);
  }

  function diagnostic(event: string, detail?: Record<string, unknown>) {
    onDiagnostic?.(event, detail);
  }

  function currentTopics(): RealtimeTopic[] {
    return [...counts.keys()].sort();
  }

  function send(command: Record<string, unknown>): boolean {
    if (!socket || state.status !== 'live') return false;
    try {
      socket.send(JSON.stringify(command));
      return true;
    } catch {
      // A send on a socket the platform has already torn down. onclose will follow and
      // the reconnect handles it; throwing here would take a React render down with it.
      return false;
    }
  }

  function open() {
    if (socket) return;
    setState({ status: state.attempts === 0 ? 'connecting' : 'reconnecting', retryAt: null });

    let next: RealtimeSocket;
    try {
      next = socketFactory(url);
    } catch (error) {
      diagnostic('socket_open_failed', { error: String(error) });
      scheduleReconnect();
      return;
    }
    socket = next;

    next.onopen = () => {
      const resumed = state.cursor > 0;
      setState({ status: 'live', attempts: 0, retryAt: null });
      diagnostic('connected', { resumed });

      const topics = currentTopics();
      if (topics.length > 0) send({ type: 'subscribe', topics });
      if (resumed) {
        // The gateway does not replay. `resume` returns where this connection stands, and
        // anything between that and the cursor we held is a gap the application closes by
        // refetching.
        send({ type: 'resume', since: state.cursor });
        setState({ missed: true });
        onGap?.('reconnect');
      }
    };

    next.onmessage = (event) => {
      if (typeof event.data !== 'string') return;
      let envelope: RealtimeEnvelope;
      try {
        envelope = JSON.parse(event.data) as RealtimeEnvelope;
      } catch {
        diagnostic('envelope_not_json');
        return;
      }
      handle(envelope);
    };

    next.onerror = () => {
      // Browsers deliberately give an error event with no detail, to avoid leaking
      // cross-origin information. onclose follows and carries the code.
      diagnostic('socket_error');
    };

    next.onclose = (event) => {
      socket = null;
      diagnostic('disconnected', { code: event?.code, reason: event?.reason });
      if (!wanted) {
        setState({ status: 'idle', attempts: 0, retryAt: null });
        return;
      }
      scheduleReconnect();
    };
  }

  function handle(envelope: RealtimeEnvelope) {
    switch (envelope.type) {
      case 'welcome':
      case 'resumed':
        if (typeof envelope.cursor === 'number' && envelope.cursor > state.cursor) {
          setState({ cursor: envelope.cursor });
        }
        if (envelope.dropped && envelope.dropped > 0) {
          setState({ missed: true });
          onGap?.('dropped');
        }
        break;

      case 'message': {
        const message = envelope.message;
        if (!message) return;
        if (envelope.dropped && envelope.dropped > 0) {
          // The gateway is telling us this connection fell behind and messages were not
          // queued. Nothing is lost — the events are in the ledger — but this client's
          // view is incomplete until it refetches.
          setState({ missed: true });
          onGap?.('dropped');
        }
        if (message.seq > state.cursor) setState({ cursor: message.seq });
        onMessage?.(message);
        break;
      }

      case 'refused':
        // A topic this role may not watch. Named rather than silent, so it is a bug report
        // and not a screen that mysteriously never updates.
        diagnostic('subscription_refused', { topics: envelope.topics ?? [] });
        break;

      case 'error':
        diagnostic('gateway_error', { error: envelope.error });
        if (envelope.error === 'reauthentication_failed') {
          // The session changed under the connection. Reconnecting re-runs the whole
          // handshake, which is where a refreshed token or a new role takes effect.
          socket?.close(CLOSE_GOING_AWAY, 'reauthenticating');
        }
        break;

      case 'closing':
        diagnostic('gateway_closing', { detail: envelope.detail });
        break;

      case 'subscribed':
      case 'unsubscribed':
      case 'pong':
        break;
    }
  }

  function scheduleReconnect() {
    if (!wanted) return;
    const attempts = state.attempts + 1;
    const delay = backoffDelay(attempts, backoff, random);
    // Past a few minutes of failure, say "offline" rather than "reconnecting": the
    // operator should know the screen is a snapshot, not a live view.
    const status: RealtimeStatus = attempts >= 5 ? 'offline' : 'reconnecting';
    setState({ status, attempts, retryAt: now() + delay });
    diagnostic('reconnect_scheduled', { attempts, delay });

    clearTimer();
    timer = setTimeoutFn(() => {
      timer = null;
      if (wanted) open();
    }, delay);
  }

  function clearTimer() {
    if (timer !== null) {
      clearTimeoutFn(timer);
      timer = null;
    }
  }

  return {
    connect() {
      if (wanted) return;
      wanted = true;
      open();
    },

    disconnect() {
      wanted = false;
      clearTimer();
      const closing = socket;
      socket = null;
      closing?.close(1000, 'client closed');
      setState({ status: 'idle', attempts: 0, retryAt: null });
    },

    subscribe(topics) {
      const added: RealtimeTopic[] = [];
      for (const topic of topics) {
        const count = counts.get(topic) ?? 0;
        counts.set(topic, count + 1);
        if (count === 0) added.push(topic);
      }
      if (added.length > 0) send({ type: 'subscribe', topics: added.sort() });

      let released = false;
      return () => {
        if (released) return;
        released = true;
        const dropped: RealtimeTopic[] = [];
        for (const topic of topics) {
          const count = counts.get(topic) ?? 0;
          if (count <= 1) {
            counts.delete(topic);
            dropped.push(topic);
          } else {
            counts.set(topic, count - 1);
          }
        }
        if (dropped.length > 0) send({ type: 'unsubscribe', topics: dropped.sort() });
      };
    },

    topics: currentTopics,
    state: () => state,
    acknowledgeGap() {
      if (state.missed) setState({ missed: false });
    },

    resume() {
      if (!wanted) return;
      clearTimer();
      if (socket) return;
      setState({ attempts: 0 });
      open();
    },
  };
}

function defaultSocketFactory(url: string): RealtimeSocket {
  const Socket = (globalThis as { WebSocket?: new (url: string) => RealtimeSocket }).WebSocket;
  if (!Socket) {
    throw new Error('no WebSocket implementation on this platform');
  }
  return new Socket(url);
}

/**
 * A correlation id for one connection attempt, so a gateway log line and a client log line
 * can be lined up. Not sent yet — the handshake carries no custom headers from a browser —
 * but generated here so the two surfaces do it the same way when it is.
 */
export function connectionId(): string {
  return idempotencyKey();
}
