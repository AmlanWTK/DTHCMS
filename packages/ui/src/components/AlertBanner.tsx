'use client';

import type { ReactNode } from 'react';

import { clinicalStatuses, type ClinicalStatusName } from '@dthcms/design-tokens';

import { cx } from '../lib/cx';
import { useLanguage } from '../lib/language';
import { Icon, type IconName } from './Icon';

export type AlertTone = ClinicalStatusName | 'info';

export interface AlertBannerProps {
  tone?: AlertTone;
  title: ReactNode;
  children?: ReactNode;
  /** A single action. More than one and it is a dialog, not a banner. */
  action?: ReactNode;
  onDismiss?: () => void;
  className?: string;
}

const INFO_ICON: IconName = 'help-circle';

/**
 * An inline message about the state of something on screen.
 *
 * The live-region behaviour is chosen per tone rather than fixed. `critical` is a genuine
 * `alert`, which interrupts a screen reader mid-sentence — correct for a panic value,
 * and wrong for anything else, because a page that interrupts on every notice is a page
 * whose interruptions stop meaning anything. Everything else is a polite `status`.
 */
export function AlertBanner({
  tone = 'info',
  title,
  children,
  action,
  onDismiss,
  className,
}: AlertBannerProps) {
  const { t } = useLanguage();

  const icon: IconName = tone === 'info' ? INFO_ICON : (clinicalStatuses[tone].icon as IconName);

  const assertive = tone === 'critical';

  return (
    <div
      className={cx('dthc-alert', `dthc-alert--${tone}`, className)}
      role={assertive ? 'alert' : 'status'}
      aria-live={assertive ? 'assertive' : 'polite'}
      data-status={tone === 'info' ? undefined : tone}
    >
      <Icon name={icon} size={20} className="dthc-alert__icon" />

      <div className="dthc-alert__content">
        <p className="dthc-alert__title">{title}</p>
        {children && <div className="dthc-alert__body">{children}</div>}
      </div>

      {action && <div className="dthc-alert__action">{action}</div>}

      {onDismiss && (
        <button
          type="button"
          className="dthc-alert__dismiss"
          onClick={onDismiss}
          aria-label={t({ en: 'Dismiss', bn: 'বন্ধ করুন' })}
        >
          <Icon name="x" size={18} />
        </button>
      )}
    </div>
  );
}
