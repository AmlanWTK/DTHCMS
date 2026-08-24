import type { ReactNode } from 'react';

import { cx } from '../lib/cx';

export type BadgeTone = 'neutral' | 'brand' | 'info';

export interface BadgeProps {
  children: ReactNode;
  tone?: BadgeTone;
  className?: string;
}

/**
 * A small count or category marker.
 *
 * Deliberately has no clinical tones. A badge that could be coloured "critical" would be
 * used for a clinical status, and would then be one without an icon or a label — which is
 * exactly what StatusPill exists to make impossible. Anything clinical uses StatusPill.
 */
export function Badge({ children, tone = 'neutral', className }: BadgeProps) {
  return <span className={cx('dthc-badge', `dthc-badge--${tone}`, className)}>{children}</span>;
}
