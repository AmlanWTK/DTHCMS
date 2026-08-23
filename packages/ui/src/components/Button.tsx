import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from 'react';

import { cx } from '../lib/cx.js';
import { Icon, type IconName } from './Icon.js';

export type ButtonVariant = 'primary' | 'secondary' | 'quiet' | 'danger';
export type ButtonSize = 'sm' | 'md' | 'lg';

export interface ButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'className'> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  /**
   * Shows a spinner and blocks interaction.
   *
   * Loading also disables, which is deliberate: a save button that stays pressable while
   * the save is in flight is how a clinical observation gets written twice. The disabled
   * state here is a consequence of being busy, not a separate decision.
   */
  loading?: boolean;
  /** Replaces the label while loading. Falls back to the label, which is usually right. */
  loadingLabel?: string;
  iconStart?: IconName;
  iconEnd?: IconName;
  /** Fills the width of its container. Used for the primary action on a station form. */
  block?: boolean;
  children?: ReactNode;
  className?: string;
}

/**
 * The button.
 *
 * Two things here are not stylistic. `type` defaults to "button" rather than the HTML
 * default of "submit", because a button inside a clinical form that submits it by
 * accident is a recorded observation nobody meant to record. And `md` and `lg` are both
 * at least the 48px touch target, tested — `sm` is not, and is documented as being for
 * pointer-driven density only.
 */
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  {
    variant = 'secondary',
    size = 'md',
    loading = false,
    loadingLabel,
    iconStart,
    iconEnd,
    block = false,
    disabled,
    children,
    className,
    type = 'button',
    ...rest
  },
  ref,
) {
  const isDisabled = disabled === true || loading;

  return (
    <button
      ref={ref}
      type={type}
      className={cx(
        'dthc-button',
        `dthc-button--${variant}`,
        `dthc-button--${size}`,
        block && 'dthc-button--block',
        loading && 'is-loading',
        className,
      )}
      disabled={isDisabled}
      aria-busy={loading || undefined}
      data-variant={variant}
      {...rest}
    >
      {loading ? (
        <Icon name="loader-circle" size={size === 'sm' ? 16 : 20} className="dthc-spin" />
      ) : (
        iconStart && <Icon name={iconStart} size={size === 'sm' ? 16 : 20} />
      )}

      {children !== undefined && (
        <span className="dthc-button__label">
          {loading && loadingLabel ? loadingLabel : children}
        </span>
      )}

      {!loading && iconEnd && <Icon name={iconEnd} size={size === 'sm' ? 16 : 20} />}
    </button>
  );
});
