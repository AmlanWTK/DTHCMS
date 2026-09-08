import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { AppState, type AppStateStatus } from 'react-native';

import { useConnectivity } from '@/lib/connectivity';
import { deviceId } from '@/lib/device';
import { localStore, lockLocalStore, unlockLocalStore } from '@/lib/local-store';
import { useSession } from '@/stores/session';

import {
  createSyncEngine,
  httpTransport,
  lifecycleFor,
  noteSessionLost,
  readMetrics,
  resumeAfterSignIn,
  wipeAfterSignOut,
  type SessionPhase,
  type SyncEngine,
  type SyncMetrics,
} from '@/lib/sync';

/**
 * When the engine runs (CP66).
 *
 * Three triggers, which are the checkpoint's own scope: **connectivity regained**, **the app
 * coming to the foreground**, and an interval. None of them needs the operator to do anything,
 * which is acceptance criterion 3.
 *
 * Everything this component decides is decided somewhere testable: `lifecycleFor` says what a
 * change of session means for the database, `resumeAfterSignIn` says which halts a sign-in
 * clears, `createSyncEngine` owns the single-flight loop, and `wipeAfterSignOut` owns what a
 * sign-out does and does not destroy. What is left here is the wiring to NetInfo, AppState and
 * the session store — which cannot run outside a device, and which is exactly why there is
 * nothing here to get wrong.
 */

export function SyncProvider({ children }: { children: ReactNode }) {
  const status = useSession((state) => state.status);
  const { online } = useConnectivity();
  const previous = useRef<SessionPhase>('unknown');
  const engineRef = useRef<SyncEngine | null>(null);
  const [engine, setEngine] = useState<SyncEngine | null>(null);
  const [metrics, setMetrics] = useState<SyncMetrics | null>(null);

  /**
   * What the pill and every screen read (CP67).
   *
   * Read once here rather than by each screen that wants it. Two screens polling the same tables
   * on two timers is not only waste on a tablet: the header pill and the sync panel would be
   * looking at the database a second or two apart, and an operator watching one number change
   * while the other did not is an operator who has just learned that the indicator is unreliable —
   * which is §13.9's failure arriving through the least interesting door.
   */
  const refresh = useCallback(async () => {
    const store = localStore();
    setMetrics(store === null ? null : await readMetrics(store));
  }, []);

  const start = useCallback(async () => {
    // The key is released here and nowhere else: after authentication, as §13.8 requires.
    const store = await unlockLocalStore();
    await resumeAfterSignIn(store);
    const next = createSyncEngine({
      store,
      transport: httpTransport(),
      deviceId: await deviceId(),
      // Every attempt repaints the pill, so what an operator sees is never older than the last
      // thing that happened — rather than up to a poll interval behind it.
      onReport: () => void refresh(),
    });
    next.start();
    engineRef.current = next;
    setEngine(next);
    void refresh();
    void next.sync('foreground');
  }, [refresh]);

  const stop = useCallback(async () => {
    engineRef.current?.stop();
    engineRef.current = null;
    setEngine(null);
    setMetrics(null);
    const store = localStore();
    if (store) {
      // Said out loud rather than left implicit: sync has stopped because nobody is signed in,
      // and the queue is waiting rather than lost.
      await noteSessionLost(store);
      // Wipes what can be read and keeps what has not been delivered. See `lifecycle.ts` for why
      // those are not the same instruction.
      await wipeAfterSignOut(store);
    }
    await lockLocalStore();
  }, []);

  useEffect(() => {
    const action = lifecycleFor(previous.current, status as SessionPhase);
    previous.current = status as SessionPhase;
    if (action === 'unlock') void start();
    if (action === 'lock') void stop();
  }, [status, start, stop]);

  // The signal came back. This is the trigger that matters most in a clinic: a tablet carried out
  // of the corridor should be delivering before the operator has looked at it.
  useEffect(() => {
    if (online && engine) void engine.sync('connectivity');
  }, [online, engine]);

  useEffect(() => {
    const subscription = AppState.addEventListener('change', (next: AppStateStatus) => {
      if (next === 'active' && engine) void engine.sync('foreground');
    });
    return () => subscription.remove();
  }, [engine]);

  /*
   * Polled as well as pushed, and both are needed.
   *
   * `onReport` covers everything the engine does; nothing covers what a *person* does — a
   * correction queued or a refusal escalated on the sync screen changes the counts without any
   * attempt happening. The local database has no change feed, so the alternative to a timer is
   * every screen remembering to tell the provider, which is the sort of thing that is remembered
   * for four screens and forgotten on the fifth.
   */
  useEffect(() => {
    if (engine === null) return;
    void refresh();
    const timer = setInterval(() => void refresh(), 5_000);
    return () => clearInterval(timer);
  }, [engine, refresh]);

  return (
    <SyncContext.Provider value={engine}>
      <SyncMetricsContext.Provider value={{ metrics, refresh }}>
        {children}
      </SyncMetricsContext.Provider>
    </SyncContext.Provider>
  );
}

const SyncContext = createContext<SyncEngine | null>(null);

export interface SyncMetricsView {
  /** Null before the database is open, which is every screen shown to a signed-out operator. */
  metrics: SyncMetrics | null;
  /** Re-read now. Called by whatever has just changed the queue. */
  refresh: () => Promise<void>;
}

const SyncMetricsContext = createContext<SyncMetricsView>({
  metrics: null,
  refresh: async () => {},
});

/** The running engine, or null when nobody is signed in. A screen uses it to ask for a sync. */
export function useSyncEngine(): SyncEngine | null {
  return useContext(SyncContext);
}

/** What is undelivered, shared by every screen that shows it (CP67). */
export function useSyncMetrics(): SyncMetricsView {
  return useContext(SyncMetricsContext);
}
