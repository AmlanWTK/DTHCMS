'use client';

import { StatusPill } from '@dthcms/ui';
import type { ClinicalStatusName } from '@dthcms/design-tokens';
import type { ReactNode } from 'react';

/**
 * A clinical status token with this screen's own word beside it (CP73).
 *
 * # Why the design system's word is hidden and another is shown
 *
 * `StatusPill` renders three signals that all say the same thing — a colour, an icon and a
 * word — and it deliberately offers no way to render only the colour, because under
 * deuteranopia several of its hues converge. Its word is the *token's* word: "Normal",
 * "High", "Borderline".
 *
 * On this screen the useful word is usually more specific. "Overweight" says which side of
 * the Asian cut-off a BMI sits; "High" does not. "Counselling complete" says what a physician
 * needs to know; "Normal" is a colour's name read aloud.
 *
 * So the pill keeps its icon and its colour with `labelHidden`, which leaves the token's word
 * in the accessibility tree rather than deleting it, and the specific word is drawn beside it.
 * Two signals remain for a reader who cannot use hue — the icon and the specific word — and a
 * screen-reader user hears both words, which is more information rather than less.
 *
 * The alternative, adding a `children` prop to `StatusPill`, was rejected: it would let any
 * screen replace the status vocabulary with whatever it liked, and the point of that component
 * is that a status looks and reads the same everywhere in the application.
 */
export function StatusWord({
  status,
  children,
  testId,
}: {
  status: ClinicalStatusName;
  children: ReactNode;
  testId?: string;
}) {
  return (
    <span className="dash-status" data-status={status} data-testid={testId}>
      <StatusPill status={status} labelHidden size="sm" />
      <span className="dash-status__word">{children}</span>
    </span>
  );
}
