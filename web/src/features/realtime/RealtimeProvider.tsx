'use client';

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

import {
  createRealtimeClient,
  gapInvalidations,
  realtimeInvalidations,
  type RealtimeClient,
  type RealtimeMessage,
  type RealtimeState,
} from '@dthcms/api-client';

import { API_BASE_URL } from '@/lib/env';
import { useSessionStore } from '@/stores/session';

/**
 * The realtime connection, for the whole application (CP27).
 *
 * One socket per tab, held here, because a clinic screen shows several things at once and
 * a connection per component would mean a dozen sockets per operator — which is what the
 * gateway's per-user limit exists to refuse.
 *
 * What arrives is turned into query invalidations and nothing else. The reasoning is in
 * `@dthcms/api-client`'s `realtime-keys`, and the short version is that a value written
 * into the cache from a socket is a value no endpoint returned and no log explains.
 *
 * The connection follows the session: it opens when somebody is signed in and closes when
 * they are not, so a shared clinic browser at the sign-in screen holds nothing open.
 */

interface RealtimeContextValue {
  state: RealtimeState;
  /** Subscribes while the caller is mounted. Null before the client exists. */
  client: RealtimeClient | null;
}

const RealtimeContext = createContext<RealtimeContextValue>({
  state: { status: 'idle', cursor: 0, attempts: 0, retryAt: null, missed: false },
  client: null,
});

/** The gateway's URL, derived from the API's. */
export function realtimeUrl(base: string = API_BASE_URL): string {
  const url = new URL(
    base,
    typeof window === 'undefined' ? 'http://localhost' : window.location.href,
  );
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.pathname = `${url.pathname.replace(/\/$/, '')}/v1/realtime`;
  // Deliberately no token in the query string: the browser's credential is the session
  // cookie, which it attaches to the handshake by itself (ADR-0010). A token in a URL is a
  // token in an access log, a Referer header and the browser's history.
  url.search = '';
  return url.toString();
}

export function RealtimeProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const signedIn = useSessionStore((store) => store.user !== null);
  const [state, setState] = useState<RealtimeState>({
    status: 'idle',
    cursor: 0,
    attempts: 0,
    retryAt: null,
    missed: false,
  });

  // The client outlives renders; the ref is what stops a re-render opening a second socket.
  const clientRef = useRef<RealtimeClient | null>(null);
  const [client, setClient] = useState<RealtimeClient | null>(null);

  useEffect(() => {
    if (!signedIn) {
      clientRef.current?.disconnect();
      clientRef.current = null;
      setClient(null);
      setState((current) => ({ ...current, status: 'idle', attempts: 0, retryAt: null }));
      return;
    }

    const realtime = createRealtimeClient({
      url: realtimeUrl(),
      onState: setState,
      onMessage: (message: RealtimeMessage) => {
        for (const queryKey of realtimeInvalidations(message)) {
          void queryClient.invalidateQueries({ queryKey });
        }
      },
      onGap: () => {
        // Nothing to replay. Everything this tab is watching is refetched, which is what
        // "recovered on reconnect" means when the channel is a notification channel.
        for (const queryKey of gapInvalidations(realtime.topics())) {
          void queryClient.invalidateQueries({ queryKey });
        }
        realtime.acknowledgeGap();
      },
    });

    clientRef.current = realtime;
    setClient(realtime);
    realtime.connect();

    return () => {
      realtime.disconnect();
      clientRef.current = null;
    };
  }, [signedIn, queryClient]);

  // A tab that comes back to the foreground reconnects at once rather than waiting out a
  // backoff that may have grown to half a minute while nobody was looking.
  useEffect(() => {
    if (typeof document === 'undefined') return;
    const wake = () => {
      if (document.visibilityState === 'visible') clientRef.current?.resume();
    };
    document.addEventListener('visibilitychange', wake);
    window.addEventListener('online', wake);
    return () => {
      document.removeEventListener('visibilitychange', wake);
      window.removeEventListener('online', wake);
    };
  }, []);

  const value = useMemo(() => ({ state, client }), [state, client]);
  return <RealtimeContext.Provider value={value}>{children}</RealtimeContext.Provider>;
}

/** The connection's state, for the indicator and for anything that wants to know. */
export function useRealtime(): RealtimeState {
  return useContext(RealtimeContext).state;
}

/**
 * Subscribes to topics while this component is mounted (CP27: "subscription lifecycle tied
 * to the visible screen").
 *
 * A screen asks for what it is showing. When the operator navigates away the component
 * unmounts and the topic is released, so the clinic's sockets carry what is in front of
 * people and nothing else — which is also what keeps the gateway's per-message RBAC work
 * proportional to what is actually being watched.
 *
 * Pass an empty array to subscribe to nothing; a screen with no patient yet does that.
 */
export function useRealtimeTopics(topics: readonly string[]): void {
  const { client } = useContext(RealtimeContext);
  // Sorted and joined, so a caller passing a new array literal on every render does not
  // resubscribe on every render.
  const key = [...topics].sort().join('|');

  useEffect(() => {
    if (!client || key === '') return;
    return client.subscribe(key.split('|'));
  }, [client, key]);
}
