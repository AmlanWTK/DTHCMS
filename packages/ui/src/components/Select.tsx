import { forwardRef, type ReactNode, type SelectHTMLAttributes } from 'react';

import { cx } from '../lib/cx.js';
import { useLanguage } from '../lib/language.js';
import { Field, type FieldOwnProps } from './Field.js';
import { Icon } from './Icon.js';

export interface SelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}

export interface SelectProps
  extends
    FieldOwnProps,
    Omit<SelectHTMLAttributes<HTMLSelectElement>, 'className' | 'required' | 'disabled'> {
  options: SelectOption[];
  /** Shown as a non-selectable first entry when the value is empty. */
  placeholder?: string;
  children?: ReactNode;
}

/**
 * A select, built on the native element.
 *
 * This is a deliberate departure from the implementation plan, which names Radix. On an
 * Android tablet the native control opens the platform picker: a large, scrollable,
 * touch-sized list that the operator already knows, that works with the system's own
 * accessibility services, and that costs nothing to render. A custom listbox on the same
 * device is a smaller target with hand-rolled keyboard handling and a bundle cost.
 *
 * Radix earns its place when options need rich content — a drug name with a strength and
 * a form on separate lines, say. That is a domain component, and it can wrap Radix then
 * without this primitive having done so first.
 */
export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select(
  {
    label,
    description,
    error,
    warning,
    required,
    disabled,
    labelHidden,
    className,
    options,
    placeholder,
    value,
    ...rest
  },
  ref,
) {
  const { t } = useLanguage();
  const resolvedPlaceholder = placeholder ?? t({ en: 'Select…', bn: 'নির্বাচন করুন…' });

  return (
    <Field
      label={label}
      description={description}
      error={error}
      warning={warning}
      required={required}
      disabled={disabled}
      labelHidden={labelHidden}
      className={className}
    >
      {({ id, describedBy, invalid }) => (
        <div className={cx('dthc-select', disabled && 'is-disabled')}>
          <select
            ref={ref}
            id={id}
            className="dthc-select__control"
            aria-describedby={describedBy}
            aria-invalid={invalid || undefined}
            required={required}
            disabled={disabled}
            value={value}
            {...rest}
          >
            <option value="" disabled={required}>
              {resolvedPlaceholder}
            </option>
            {options.map((option) => (
              <option key={option.value} value={option.value} disabled={option.disabled}>
                {option.label}
              </option>
            ))}
          </select>
          <Icon name="chevron-down" size={20} className="dthc-select__chevron" />
        </div>
      )}
    </Field>
  );
});
