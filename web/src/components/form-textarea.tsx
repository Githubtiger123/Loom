import { forwardRef, useId, type TextareaHTMLAttributes } from 'react';

// Atomus has no React Textarea export; this accessible field composes its
// existing field classes and semantic tokens without changing vendor code.
export const FormTextarea = forwardRef<
  HTMLTextAreaElement,
  TextareaHTMLAttributes<HTMLTextAreaElement> & { label: string; error?: string; hint?: string }
>(function FormTextarea({ label, error, hint, id, ...props }, ref) {
  const generated = useId();
  const fieldId = id ?? generated;
  const message = error || hint;
  return (
    <div className="at-field">
      <label className="at-field__label" htmlFor={fieldId}>
        {label}
      </label>
      <textarea
        {...props}
        ref={ref}
        id={fieldId}
        className="form-textarea"
        aria-invalid={!!error}
        aria-describedby={message ? `${fieldId}-hint` : undefined}
      />
      {message && (
        <p
          id={`${fieldId}-hint`}
          className={`at-field__hint${error ? ' at-field__hint--error' : ''}`}
        >
          {message}
        </p>
      )}
    </div>
  );
});
