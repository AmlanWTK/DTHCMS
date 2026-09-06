import { ScreenShell } from '@/components/ScreenShell';
import { SyncPanel, useSyncEngine } from '@/features/sync';

/**
 * What this tablet is still holding (CP64, CP66, §13.9).
 *
 * The operator's answer to one question — *is my work safe?* — and CP67 is where it becomes the
 * whole failure ladder. What it must already be is honest: the panel says "everything is with the
 * clinic" only over an empty queue.
 */
export default function Screen() {
  const engine = useSyncEngine();
  return (
    <ScreenShell titleKey="screen.sync">
      {/* The attempt itself, not a fire-and-forget: the panel waits for it and then repaints. */}
      <SyncPanel onRetry={engine ? () => engine.sync('manual') : undefined} />
    </ScreenShell>
  );
}
