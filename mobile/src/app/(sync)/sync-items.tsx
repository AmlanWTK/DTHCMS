import { ScreenShell } from '@/components/ScreenShell';
import { SyncItems, useSyncEngine } from '@/features/sync';

/**
 * Every entry this tablet is holding, one at a time (CP67, §13.5).
 *
 * # Why it is a second screen rather than more of the first
 *
 * `/sync` answers "is my work safe" in a paragraph an operator reads in five seconds. This answers
 * "which ones, and what do I do about them", which is a list that can be forty rows long on a
 * tablet that has been in a corridor all morning. Putting the list on the summary would mean an
 * operator checking a normal queue scrolls past forty rows of nothing wrong — and CP67's named
 * risk is exactly that: an indicator prominent enough to be ignored.
 *
 * # It is reached from the two places somebody is already worried
 *
 * The pill in the shell's header, which is on every screen, and the panel on `/sync`. Nothing else
 * links here. It is not a place to work; it is where a person goes when the pill has told them
 * something, and a screen in a menu is a screen nobody opens on the morning it matters.
 */
export default function Screen() {
  const engine = useSyncEngine();
  return (
    <ScreenShell titleKey="screen.syncItems">
      {/* A correction or an escalation changes the queue, so the engine is asked for an attempt:
          the corrected measurement should be on its way before the operator has put the tablet
          down, rather than at the next five-minute tick. */}
      <SyncItems onChanged={engine ? () => engine.sync('command') : undefined} />
    </ScreenShell>
  );
}
