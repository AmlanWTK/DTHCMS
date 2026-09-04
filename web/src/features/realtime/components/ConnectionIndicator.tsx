'use client';

import { useTranslations } from 'next-intl';

import { useRealtime } from '../RealtimeProvider';

/**
 * Whether the screen is live (CP27 criterion 3).
 *
 * It exists to answer one question an operator asks silently, several times a shift: *am I
 * looking at what is happening, or at what was happening?* A clinic screen that has
 * quietly stopped updating is worse than one that is obviously stale, because the operator
 * acts on it with the same confidence either way.
 *
 * Deliberately not a `StatusPill`. Those seven tones mean something specific about a
 * measurement, and spending one on a network condition is how a status vocabulary stops
 * being a vocabulary — the same reasoning the offline banner records.
 *
 * Three states, and the quietest one is `live`: a dot and nothing else, because "working
 * normally" should not compete for attention with a patient's blood pressure. The other
 * two say so in words, because a colour alone is not a signal (§15.2, and the same
 * criterion the clinical statuses are held to).
 */
export function ConnectionIndicator() {
  const t = useTranslations('realtime');
  const { status } = useRealtime();

  if (status === 'idle' || status === 'connecting') return null;

  const label = t(status);
  return (
    <span
      className="app-realtime"
      data-status={status}
      // polite, not assertive: a reconnect is worth knowing about, and not worth
      // interrupting somebody mid-sentence for.
      aria-live="polite"
      title={t(`${status}Detail`)}
    >
      <span className="app-realtime__dot" aria-hidden="true" />
      <span className="app-realtime__label">{label}</span>
    </span>
  );
}
