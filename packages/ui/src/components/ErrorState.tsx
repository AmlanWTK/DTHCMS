'use client';

import { useState, type ReactNode } from 'react';

import { cx } from '../lib/cx';
import { useLanguage } from '../lib/language';
import { Button } from './Button';
import { Icon, type IconName } from './Icon';

export interface ErrorStateProps {
  title?: ReactNode;
  children?: ReactNode;
  onRetry?: () => void;
  retrying?: boolean;
  /**
   * The correlation ID from the failed request.
   *
   * Displayed rather than hidden, and selectable. It is the one thing that lets an
   * operator on the phone and an engineer in a trace viewer talk about the same event —
   * the id the API returns in X-Request-ID and puts on every log line and span (CP05,
   * CP07). Without it the support conversation begins with "roughly when?".
   */
  correlationId?: string;
  /** Technical detail, collapsed. Never shown by default. */
  detail?: ReactNode;
  /** Offline is not an error to apologise for; it is a state with its own icon. */
  variant?: 'error' | 'offline';
  className?: string;
}

export function ErrorState({
  title,
  children,
  onRetry,
  retrying = false,
  correlationId,
  detail,
  variant = 'error',
  className,
}: ErrorStateProps) {
  const { t } = useLanguage();
  const [showDetail, setShowDetail] = useState(false);

  const icon: IconName = variant === 'offline' ? 'wifi-off' : 'octagon-alert';

  const defaultTitle =
    variant === 'offline'
      ? t({ en: 'No connection', bn: 'সংযোগ নেই' })
      : t({ en: 'Something went wrong', bn: 'কিছু একটা ভুল হয়েছে' });

  const defaultBody =
    variant === 'offline'
      ? t({
          en: 'Your work is saved on this device and will sync when the connection returns.',
          bn: 'আপনার কাজ এই ডিভাইসে সংরক্ষিত আছে এবং সংযোগ ফিরলে সিঙ্ক হবে।',
        })
      : t({
          en: 'The information could not be loaded. Nothing you entered has been lost.',
          bn: 'তথ্য লোড করা যায়নি। আপনি যা লিখেছেন তার কিছুই হারায়নি।',
        });

  return (
    <div
      className={cx('dthc-state', 'dthc-state--error', `dthc-state--${variant}`, className)}
      role="alert"
    >
      <Icon name={icon} size={32} className="dthc-state__icon" />
      <p className="dthc-state__title">{title ?? defaultTitle}</p>
      <div className="dthc-state__body">{children ?? defaultBody}</div>

      {onRetry && (
        <div className="dthc-state__action">
          <Button
            variant="secondary"
            iconStart="refresh-cw"
            onClick={onRetry}
            loading={retrying}
            loadingLabel={t({ en: 'Retrying…', bn: 'আবার চেষ্টা করা হচ্ছে…' })}
          >
            {t({ en: 'Try again', bn: 'আবার চেষ্টা করুন' })}
          </Button>
        </div>
      )}

      {correlationId && (
        <p className="dthc-state__reference">
          {t({ en: 'Reference', bn: 'রেফারেন্স' })}{' '}
          <code className="dthc-state__id">{correlationId}</code>
        </p>
      )}

      {detail && (
        <div className="dthc-state__detail">
          <button
            type="button"
            className="dthc-state__toggle"
            onClick={() => setShowDetail((open) => !open)}
            aria-expanded={showDetail}
          >
            {showDetail
              ? t({ en: 'Hide technical detail', bn: 'কারিগরি বিবরণ লুকান' })
              : t({ en: 'Show technical detail', bn: 'কারিগরি বিবরণ দেখুন' })}
          </button>
          {showDetail && <pre className="dthc-state__pre">{detail}</pre>}
        </div>
      )}
    </div>
  );
}
