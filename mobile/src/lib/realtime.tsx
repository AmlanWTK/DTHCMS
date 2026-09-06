import { useQueryClient } from '@tanstack/react-query';
import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { AppState, type AppStateStatus } from 'react-native';

import {
  createRealtimeClient,
  gapInvalidations,
  realtimeInvalidations,
  type RealtimeClient,
  type RealtimeMessage,
  type RealtimeSocket,
  type RealtimeState,
} from '@dthcms/api-client';

import { connectionAction, handshakeHeaders, realtimeUrl } from '@/lib/realtime-handshake';
import { useSession } from '@/stores/session';

export { realtimeUrl };

/**
 * The station app's realtime connection (CP27).
 *
 * The same client the web uses, with two differences the platform forces:
 *
 *   - **Credentials go in headers.** React Native's WebSocket takes them; a browser's does
 *     not. So the station app signs the handshake exactly as it signs every other request
 *     — bearer token plus the CP18 device signature — and the gateway checks it with the
 *     same middleware. Nothing goes in the query string.
 *   - **The app has a background.** An Android tablet in a drawer should not hold a socket
 *     open, and one taken back out should be live before the operator has finished
 *     unlocking it. Both are `AppState`.
 *
 * What arrives becomes a query invalidation and nothing else, for the reason recorded in
 * `@dthcms/api-client`'s realtime-keys: a value written into the cache from a socket is a
 * value no endpoint returned.
 */

interface RealtimeContextValue {
  state: RealtimeState;
  client: RealtimeClient | null;
}

/**
 * Screens that need to see a message the shared invalidation map has no key for.
 *
 * `realtimeInvalidations` lives in `@dthcms/api-client` so that web and mobile invalidate the
 * same things, and it maps a message to *query keys* — which is the whole discipline: a value
 * written into the cache from a socket is a value no endpoint returned. This registry does not
 * weaken that. A listener is handed the message and is expected to invalidate; it is the
 * escape hatch for a kind the shared map has not been taught yet, and CP62's
 * `correction.requested` is the first: it arrives on the operator's own `user:{id}` topic,
 * which that map turns into the audit-alerts key and nothing else, so the correction queue
 * would sit stale on the device the request was routed to.
 *
 * A registry rather than a context value, mirroring `lib/api`'s `onSessionLost`: the provider
 * is mounted once for the process, and a context value that changed identity would re-run
 * every subscriber's effect on each state tick.
 */
const messageListeners = new Set<(message: RealtimeMessage) => void>();

/** Listens until the returned function is called. */
export function onRealtimeMessage(listener: (message: RealtimeMessage) => void): () => void {
  messageListeners.add(listener);
  return () => {
    messageListeners.delete(listener);
  };
}

/**
 * The same thing for a screen: listens while it is mounted.
 *
 * The listener must be stable — `useCallback` at the call site — or this unsubscribes and
 * resubscribes on every render, which is harmless and wasteful and looks like a leak.
 */
export function useRealtimeMessages(listener: (message: RealtimeMessage) => void): void {
  useEffect(() => onRealtimeMessage(listener), [listener]);
}

const initial: RealtimeState = {
  status: 'idle',
  cursor: 0,
  attempts: 0,
  retryAt: null,
  missed: false,
};

const RealtimeContext = createContext<RealtimeContextValue>({ state: initial, client: null });

export function RealtimeProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const status = useSession((store) => store.status);
  const [state, setState] = useState<RealtimeState>(initial);
  const clientRef = useRef<RealtimeClient | null>(null);
  const [client, setClient] = useState<RealtimeClient | null>(null);

  useEffect(() => {
    if (status !== 'authenticated') {
      clientRef.current?.disconnect();
      clientRef.current = null;
      setClient(null);
      setState(initial);
      return;
    }

    let disposed = false;
    let headers: Record<string, string> = {};

    const realtime = createRealtimeClient({
      url: realtimeUrl(),
      socketFactory: (url) => nativeSocket(url, headers),
      onState: setState,
      onMessage: (message) => {
        for (const queryKey of realtimeInvalidations(message)) {
          void queryClient.invalidateQueries({ queryKey });
        }
        // After the shared map, never instead of it. A listener adds the keys that map has no
        // entry for yet; it must not be able to stop the ones it does.
        for (const listener of messageListeners) listener(message);
      },
      onGap: () => {
        for (const queryKey of gapInvalidations(realtime.topics())) {
          void queryClient.invalidateQueries({ queryKey });
        }
        realtime.acknowledgeGap();
      },
    });

    // The signature carries a timestamp and a nonce, so it is minted immediately before
    // the connection and again before every reconnect. A stale one is refused, correctly.
    void handshakeHeaders().then((signed) => {
      if (disposed) return;
      headers = signed;
      clientRef.current = realtime;
      setClient(realtime);
      realtime.connect();
    });

    return () => {
      disposed = true;
      realtime.disconnect();
      clientRef.current = null;
    };
  }, [status, queryClient]);

  // Background and foreground.
  //
  // Going to the background closes the socket: an OS that suspends the process will drop
  // it anyway, and a socket the app believes is open while the OS has killed it is how a
  // screen goes quietly stale. Coming back re-signs the handshake and reconnects at once,
  // and the client reports a gap so the screens refetch.
  useEffect(() => {
    let previous: string = AppState.currentState;
    const subscription = AppState.addEventListener('change', (next: AppStateStatus) => {
      const action = connectionAction(previous, next);
      previous = next;
      if (action === 'resume') {
        // The signature carries a timestamp and a nonce, so it is re-minted before the
        // connection rather than reused from whenever the tablet was last awake.
        void handshakeHeaders().then(() => clientRef.current?.resume());
      } else if (action === 'disconnect') {
        clientRef.current?.disconnect();
      }
    });
    return () => subscription.remove();
  }, []);

  const value = useMemo(() => ({ state, client }), [state, client]);
  return <RealtimeContext.Provider value={value}>{children}</RealtimeContext.Provider>;
}

export function useRealtime(): RealtimeState {
  return useContext(RealtimeContext).state;
}

/** Subscribes while the screen is mounted. Expo Router unmounts a screen on navigation. */
export function useRealtimeTopics(topics: readonly string[]): void {
  const { client } = useContext(RealtimeContext);
  const key = [...topics].sort().join('|');

  useEffect(() => {
    if (!client || key === '') return;
    return client.subscribe(key.split('|'));
  }, [client, key]);
}

/**
 * React Native's WebSocket, which takes headers where the browser's does not.
 *
 * The cast is honest about what is happening: the DOM type has two parameters and the
 * React Native implementation has three, and there is no shared type that admits both.
 */
function nativeSocket(url: string, headers: Record<string, string>): RealtimeSocket {
  const Native = (
    globalThis as unknown as {
      WebSocket: new (
        url: string,
        protocols?: string | string[] | null,
        options?: { headers?: Record<string, string> },
      ) => RealtimeSocket;
    }
  ).WebSocket;
  return new Native(url, null, { headers });
}
