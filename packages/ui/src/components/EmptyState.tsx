'use client';

import type { ReactNode } from 'react';

import { cx } from '../lib/cx';
import { useLanguage } from '../lib/language';
import { Icon, type IconName } from './Icon';

export interface EmptyStateProps {
  icon?: IconName;
  title?: ReactNode;
  children?: ReactNode;
  /** What to do about it. An empty state without a next step is a dead end. */
  action?: ReactNode;
  className?: string;
}

/**
 * Nothing here yet — and, importantly, that being the correct state rather than a failure.
 *
 * Kept distinct from ErrorState because the two are constantly confused and the
 * difference matters to the person reading. "No patients in the queue" means the clinic
 * is quiet. "Could not load the queue" means there may be twenty people waiting and the
 * screen cannot see them. Showing the first when the second is true is how a station goes
 * unattended.
 */
export function EmptyState({
  icon = 'inbox',
  title,
  children,
  action,
  className,
}: EmptyStateProps) {
  const { t } = useLanguage();

  return (
    <div className={cx('dthc-state', 'dthc-state--empty', className)}>
      <Icon name={icon} size={32} className="dthc-state__icon" />
      <p className="dthc-state__title">
        {title ?? t({ en: 'Nothing here yet', bn: 'এখনও কিছু নেই' })}
      </p>
      {children && <div className="dthc-state__body">{children}</div>}
      {action && <div className="dthc-state__action">{action}</div>}
    </div>
  );
}
