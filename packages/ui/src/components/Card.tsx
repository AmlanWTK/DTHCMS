import type { ElementType, ReactNode } from 'react';

import { cx } from '../lib/cx';

export type CardElevation = 'flat' | 'raised' | 'floating';

export interface CardProps {
  children: ReactNode;
  elevation?: CardElevation;
  /** Reduces the padding. For a dense list of readings on a station screen. */
  compact?: boolean;
  header?: ReactNode;
  footer?: ReactNode;
  /** Renders as something other than a div — `section`, `article`, `li`. */
  as?: ElementType;
  className?: string;
}

export function Card({
  children,
  elevation = 'raised',
  compact = false,
  header,
  footer,
  as: Component = 'div',
  className,
}: CardProps) {
  return (
    <Component
      className={cx(
        'dthc-card',
        `dthc-card--${elevation}`,
        compact && 'dthc-card--compact',
        className,
      )}
    >
      {header && <div className="dthc-card__header">{header}</div>}
      <div className="dthc-card__body">{children}</div>
      {footer && <div className="dthc-card__footer">{footer}</div>}
    </Component>
  );
}
