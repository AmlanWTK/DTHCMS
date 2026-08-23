import { forwardRef, useId, type InputHTMLAttributes, type ReactNode } from 'react';

import { cx } from '../lib/cx.js';
import { useLanguage } from '../lib/language.js';
import { Field, type FieldOwnProps } from './Field.js';

export interface NumericRange {
  min: number;
  max: number;
}

export interface NumericInputProps
  extends
    FieldOwnProps,
    Omit<
      InputHTMLAttributes<HTMLInputElement>,
      'className' | 'required' | 'disabled' | 'type' | 'value' | 'onChange' | 'min' | 'max'
    > {
  value: string;
  onValueChange: (value: string) => void;
  /** Rendered inside the control, after the number. "mmol/L", "kg", "mmHg". */
  unit?: ReactNode;
  /**
   * Values outside this are impossible — a typo, a slipped decimal point. Rejected.
   *
   * Set these wide. A range that rejects a real reading is worse than one that accepts a
   * wrong one, because the operator's only way forward is to record something false.
   */
  possible?: NumericRange;
  /**
   * Values outside this are unusual but real. Warned about, never blocked.
   *
   * This is the distinction the component exists for. A fasting glucose of 22 mmol/L is
   * not a typing mistake to be corrected; it is a patient who needs attention now, and an
   * interface that refuses to record it is an interface that loses the finding.
   */
  plausible?: NumericRange;
}

/*
 * There was a `decimals` prop here. It was documented as "display guidance" and read by
 * nothing, which is the worst kind of API: a caller sets it, believes something happens,
 * and nothing does.
 *
 * Rounding what someone typed into a clinical field is also the wrong behaviour. If an
 * operator enters 5.45 for a value recorded to one decimal place, silently storing 5.5
 * changes a measurement. Display formatting belongs where a value is displayed - see
 * formatClinicalValue below - and precision guidance belongs in the field's description,
 * where the person can read it.
 */

const NUMERIC_PATTERN = /^-?\d*\.?\d*$/;

/**
 * A number entry built for clinical values.
 *
 * It deliberately does not use `type="number"`. Three reasons, each of which has caused a
 * real incident somewhere:
 *
 *   - A number input silently reports an empty value when its contents are unparseable,
 *     so "12..5" and "nothing entered" are indistinguishable to the application. For a
 *     clinical measurement those must never be the same state.
 *   - The scroll wheel and arrow keys change the value while it has focus. On a tablet in
 *     a clinic, a stray touch on a focused field silently alters a recorded reading.
 *   - Browsers localise the decimal separator inconsistently, so the same keystrokes give
 *     different values on different devices.
 *
 * `inputMode="decimal"` still gets the numeric keypad on Android, which is the only thing
 * `type="number"` was wanted for.
 */
export const NumericInput = forwardRef<HTMLInputElement, NumericInputProps>(function NumericInput(
  {
    label,
    description,
    error,
    warning,
    required,
    disabled,
    labelHidden,
    className,
    value,
    onValueChange,
    unit,
    possible,
    plausible,
    ...rest
  },
  ref,
) {
  const { t } = useLanguage();
  const unitId = useId();

  const numeric = value.trim() === '' ? null : Number(value);
  const parsed = numeric !== null && Number.isFinite(numeric) ? numeric : null;

  const outOfPossible =
    parsed !== null && possible !== undefined && (parsed < possible.min || parsed > possible.max);

  const outOfPlausible =
    parsed !== null &&
    !outOfPossible &&
    plausible !== undefined &&
    (parsed < plausible.min || parsed > plausible.max);

  const malformed = value.trim() !== '' && parsed === null;

  const resolvedError =
    error ??
    (malformed
      ? t({ en: 'Enter a number.', bn: 'একটি সংখ্যা লিখুন।' })
      : outOfPossible && possible
        ? t({
            en: `Enter a value between ${possible.min} and ${possible.max}.`,
            bn: `${possible.min} থেকে ${possible.max} এর মধ্যে একটি মান লিখুন।`,
          })
        : undefined);

  const resolvedWarning =
    warning ??
    (outOfPlausible && plausible
      ? t({
          en: `Outside the usual range of ${plausible.min}–${plausible.max}. Recorded as entered.`,
          bn: `স্বাভাবিক সীমা ${plausible.min}–${plausible.max} এর বাইরে। যেমন লেখা হয়েছে তেমনই সংরক্ষিত।`,
        })
      : undefined);

  return (
    <Field
      label={label}
      description={description}
      error={resolvedError}
      warning={resolvedWarning}
      required={required}
      disabled={disabled}
      labelHidden={labelHidden}
      className={className}
    >
      {({ id, describedBy, invalid }) => (
        <div className={cx('dthc-input', 'dthc-input--numeric', disabled && 'is-disabled')}>
          <input
            ref={ref}
            id={id}
            // Text, not number. See the note above the component.
            type="text"
            inputMode="decimal"
            autoComplete="off"
            className="dthc-input__control dthc-numeric"
            value={value}
            onChange={(event) => {
              const next = event.target.value;
              // Reject characters that cannot be part of a number as they are typed,
              // rather than accepting and complaining later. A partial value like "-" or
              // "12." is allowed through: people type left to right.
              if (next === '' || NUMERIC_PATTERN.test(next)) {
                onValueChange(next);
              }
            }}
            aria-describedby={cx(describedBy, unit ? unitId : undefined) || undefined}
            aria-invalid={invalid || undefined}
            required={required}
            disabled={disabled}
            data-clinical-value=""
            {...rest}
          />
          {unit && (
            <span className="dthc-input__affix dthc-input__unit" id={unitId}>
              {unit}
            </span>
          )}
        </div>
      )}
    </Field>
  );
});

/** Formats a number for display at a fixed number of decimals, in ASCII digits. */
export function formatClinicalValue(value: number, decimals = 1): string {
  return value.toFixed(decimals);
}
