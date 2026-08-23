import { forwardRef, type InputHTMLAttributes, type ReactNode } from 'react';

import { Field, type FieldOwnProps } from './Field.js';

export interface InputProps
  extends
    FieldOwnProps,
    Omit<InputHTMLAttributes<HTMLInputElement>, 'className' | 'required' | 'disabled' | 'size'> {
  /**
   * Rendered inside the control, before the text. A search glyph, a currency symbol.
   *
   * Named `before` rather than `prefix` because `prefix` is a real HTML attribute typed
   * as a string, and shadowing it produces a type error at every call site.
   */
  before?: ReactNode;
  /** Rendered inside the control, after the text. A unit, a clear button. */
  after?: ReactNode;
}

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  {
    label,
    description,
    error,
    warning,
    required,
    disabled,
    labelHidden,
    className,
    before,
    after,
    ...rest
  },
  ref,
) {
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
        <div className="dthc-input">
          {before && <span className="dthc-input__affix">{before}</span>}
          <input
            ref={ref}
            id={id}
            className="dthc-input__control"
            aria-describedby={describedBy}
            aria-invalid={invalid || undefined}
            required={required}
            disabled={disabled}
            {...rest}
          />
          {after && <span className="dthc-input__affix">{after}</span>}
        </div>
      )}
    </Field>
  );
});
