import { forwardRef, useEffect, useImperativeHandle, useRef, type InputHTMLAttributes } from 'react';
import { cx, useFieldId } from '../utils';

interface ChoiceBase extends Omit<InputHTMLAttributes<HTMLInputElement>, 'size' | 'type'> {
  /** Figma: Label (+ Show label) */
  label?: string;
  /** Figma: Description (+ Show description) */
  description?: string;
  /** Figma: Size — sm (16 control, 14px label) · md (20 control, 16px label) */
  size?: 'sm' | 'md';
}

export interface CheckboxProps extends ChoiceBase {
  /** Figma: Checked=Indeterminate */
  indeterminate?: boolean;
}
export type RadioProps = ChoiceBase;
export interface ToggleProps extends ChoiceBase {
  /** Figma: Shape */
  shape?: 'pill' | 'square';
}

function ChoiceText({ label, description }: { label?: string; description?: string }) {
  if (!label && !description) return null;
  return (
    <span className="at-choice__text">
      {label ? <span className="at-choice__label">{label}</span> : null}
      {description ? <span className="at-choice__desc">{description}</span> : null}
    </span>
  );
}

export const Checkbox = forwardRef<HTMLInputElement, CheckboxProps>(function Checkbox(
  { label, description, size = 'sm', indeterminate, className, id, disabled, ...rest },
  ref,
) {
  const fieldId = useFieldId(id);
  const inner = useRef<HTMLInputElement>(null);
  useImperativeHandle(ref, () => inner.current as HTMLInputElement);
  useEffect(() => {
    if (inner.current) inner.current.indeterminate = !!indeterminate;
  }, [indeterminate]);
  return (
    <label htmlFor={fieldId} className={cx('at-choice', `at-choice--${size}`, disabled && 'at-choice--disabled', className)}>
      <input ref={inner} id={fieldId} type="checkbox" className="at-checkbox" disabled={disabled} {...rest} />
      <ChoiceText label={label} description={description} />
    </label>
  );
});

export const Radio = forwardRef<HTMLInputElement, RadioProps>(function Radio(
  { label, description, size = 'sm', className, id, disabled, ...rest },
  ref,
) {
  const fieldId = useFieldId(id);
  return (
    <label htmlFor={fieldId} className={cx('at-choice', `at-choice--${size}`, disabled && 'at-choice--disabled', className)}>
      <input ref={ref} id={fieldId} type="radio" className="at-radio" disabled={disabled} {...rest} />
      <ChoiceText label={label} description={description} />
    </label>
  );
});

export const Toggle = forwardRef<HTMLInputElement, ToggleProps>(function Toggle(
  { label, description, size = 'sm', shape = 'pill', className, id, disabled, ...rest },
  ref,
) {
  const fieldId = useFieldId(id);
  return (
    <label htmlFor={fieldId} className={cx('at-choice', `at-choice--${size}`, disabled && 'at-choice--disabled', className)}>
      <input ref={ref} id={fieldId} type="checkbox" role="switch" className={cx('at-toggle', `at-toggle--${shape}`)} disabled={disabled} {...rest} />
      <ChoiceText label={label} description={description} />
    </label>
  );
});
