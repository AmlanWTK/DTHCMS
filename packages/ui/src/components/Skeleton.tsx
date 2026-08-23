import { cx } from '../lib/cx.js';
import { useLanguage } from '../lib/language.js';

export interface SkeletonProps {
  /** Number of lines. Ignored when `shape` is not "text". */
  lines?: number;
  shape?: 'text' | 'block' | 'circle';
  width?: string;
  height?: string;
  className?: string;
  /**
   * Announced to assistive technology while loading.
   *
   * A skeleton is decorative for sighted users and invisible to everyone else. Without a
   * live region, a screen reader user hears silence and cannot tell a slow load from a
   * finished empty screen.
   */
  label?: string;
}

export function Skeleton({
  lines = 1,
  shape = 'text',
  width,
  height,
  className,
  label,
}: SkeletonProps) {
  const { t } = useLanguage();
  const announcement = label ?? t({ en: 'Loading…', bn: 'লোড হচ্ছে…' });

  return (
    <div className={cx('dthc-skeleton', `dthc-skeleton--${shape}`, className)} role="status">
      <span className="dthc-visually-hidden">{announcement}</span>
      {shape === 'text' ? (
        Array.from({ length: lines }, (_, index) => (
          <span
            key={index}
            className="dthc-skeleton__line"
            aria-hidden="true"
            // The last line is short, the way a paragraph's last line is. A block of
            // identical bars reads as a table, and the eye waits for a table.
            style={{ width: index === lines - 1 && lines > 1 ? '60%' : width }}
          />
        ))
      ) : (
        <span className="dthc-skeleton__line" aria-hidden="true" style={{ width, height }} />
      )}
    </div>
  );
}
