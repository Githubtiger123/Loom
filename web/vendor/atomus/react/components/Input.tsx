import { forwardRef, type InputHTMLAttributes, type ReactNode } from 'react';
import { cx, useFieldId } from '../utils';

export interface InputProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'size'> {
  /** Figma: Label (+ Show label). Always give one; use aria-label when it is hidden. */
  label?: string;
  /** Figma: Hint (+ Show hint) */
  hint?: string;
  /** Figma: State=Error — replaces the hint and turns the border red */
  error?: string;
  /** Figma: Size — sm 32 · md 40 · lg 48, matching Button */
  size?: 'sm' | 'md' | 'lg';
  iconLeading?: ReactNode;
  iconTrailing?: ReactNode;
}

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { label, hint, error, size = 'md', iconLeading, iconTrailing, className, id, disabled, ...rest },
  ref,
) {
  const fieldId = useFieldId(id);
  const message = error || hint;
  return (
    <div className={cx('at-field', className)}>
      {label ? <label htmlFor={fieldId} className="at-field__label">{label}</label> : null}
      <div className={cx('at-input', `at-input--${size}`, error && 'at-input--error', disabled && 'at-input--disabled')}>
        {iconLeading}
        <input
          ref={ref}
          id={fieldId}
          disabled={disabled}
          aria-invalid={error ? true : undefined}
          aria-describedby={message ? `${fieldId}-msg` : undefined}
          {...rest}
        />
        {iconTrailing}
      </div>
      {message ? <p id={`${fieldId}-msg`} className={cx('at-field__hint', error && 'at-field__hint--error')}>{message}</p> : null}
    </div>
  );
});
