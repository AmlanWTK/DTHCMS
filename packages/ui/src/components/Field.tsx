'use client';

import { useId, type ReactNode } from 'react';

import { cx } from '../lib/cx';
import { useLanguage } from '../lib/language';
import { Icon } from './Icon';

/**
 * The scaffolding every form control shares: a label, an optional description, and an
 * error message wired together with the right ARIA relationships.
 *
 * Extracted because getting this wrong is invisible. A control whose description is not
 * referenced by `aria-describedby` still looks correct, still passes a glance, and simply
 * never reaches a screen reader — and "the units field said mmol/L but it was never
 * announced" is not a defect anyone notices until it matters.
 */

export interface FieldOwnProps {
  /**
   * Required, and not optional in the type.
   *
   * A placeholder is not a label: it disappears on focus, exactly when someone hesitating
   * over what to type most needs it, and it is not announced as a name. Making the prop
   * mandatory is the cheapest way to prevent an unlabelled input reaching a clinic.
   */
  label: ReactNode;
  /** Help text. Rendered below the control and referenced by aria-describedby. */
  description?: ReactNode;
  /** An error message. Its presence is what marks the control invalid. */
  error?: ReactNode;
  /**
   * A warning that does not block.
   *
   * Distinct from `error` on purpose. A blood pressure of 190/110 is not invalid input —
   * it is a real reading that needs attention. Treating it as an error would tell the
   * operator they typed it wrongly, and the correct response is to record it and escalate.
   */
  warning?: ReactNode;
  required?: boolean;
  disabled?: boolean;
  /** Visually hides the label while keeping it available to assistive technology. */
  labelHidden?: boolean;
  className?: string;
}

export interface FieldRenderArgs {
  id: string;
  describedBy: string | undefined;
  invalid: boolean;
}

interface FieldProps extends FieldOwnProps {
  /** The control's id, when the caller names it. Otherwise one is generated. */
  controlId?: string;
  children: (args: FieldRenderArgs) => ReactNode;
  /** Rendered under the control, above the description. For a unit or a character count. */
  meta?: ReactNode;
}

export function Field({
  label,
  description,
  error,
  warning,
  required = false,
  disabled = false,
  labelHidden = false,
  className,
  meta,
  children,
  controlId,
}: FieldProps) {
  const { t } = useLanguage();
  // A caller may name the control itself — a form that scrolls to a field by id, a
  // label somewhere else pointing at it. When one does, the label's htmlFor and the
  // control's id must be THAT id, not the generated one. Before this, Input spread the
  // caller's props after its own `id={id}`, so the caller's id landed on the input while
  // htmlFor kept pointing at the generated one: a control that looks labelled, is not,
  // and whose label click does nothing. Nothing visible changes, which is why it lasted.
  const generated = useId();
  const id = controlId ?? generated;
  const descriptionId = `${id}-description`;
  const messageId = `${id}-message`;

  const invalid = Boolean(error);

  // Order matters: the message is announced before the static description, because when
  // something is wrong that is the part the person needs first.
  const describedBy =
    cx(error || warning ? messageId : undefined, description ? descriptionId : undefined) ||
    undefined;

  return (
    <div
      className={cx(
        'dthc-field',
        invalid && 'dthc-field--invalid',
        Boolean(warning) && !invalid && 'dthc-field--warning',
        disabled && 'dthc-field--disabled',
        className,
      )}
    >
      <label
        htmlFor={id}
        className={cx('dthc-field__label', labelHidden && 'dthc-visually-hidden')}
      >
        {label}
        {required && (
          <span className="dthc-field__required">
            <span aria-hidden="true">*</span>
            <span className="dthc-visually-hidden">{t({ en: '(required)', bn: '(আবশ্যক)' })}</span>
          </span>
        )}
      </label>

      {children({ id, describedBy, invalid })}

      {meta && <div className="dthc-field__meta">{meta}</div>}

      {(error || warning) && (
        <p
          id={messageId}
          className={cx('dthc-field__message', error ? 'is-error' : 'is-warning')}
          // Errors are assertive because the person has usually already moved on; a
          // warning is polite because they are still reading the value it refers to.
          role={error ? 'alert' : 'status'}
        >
          <Icon name={error ? 'octagon-alert' : 'alert-triangle'} size={16} />
          <span>{error ?? warning}</span>
        </p>
      )}

      {description && (
        <p id={descriptionId} className="dthc-field__description">
          {description}
        </p>
      )}
    </div>
  );
}
