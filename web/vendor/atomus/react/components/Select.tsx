import { useCallback, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { cx, useFieldId, useOutsideClick } from '../utils';
import { Icon } from './Icon';
import { MenuItem } from './Menu';

export interface SelectOption {
  value: string;
  label: string;
  icon?: ReactNode;
  disabled?: boolean;
}

export interface SelectProps {
  options: SelectOption[];
  value?: string;
  defaultValue?: string;
  onChange?: (value: string) => void;
  /** Figma: Label (+ Show label) */
  label?: string;
  /** Figma: Show hint */
  hint?: string;
  /** Figma: State=Error */
  error?: string;
  placeholder?: string;
  /** Figma: Size — sm 32 · md 40 · lg 48 */
  size?: 'sm' | 'md' | 'lg';
  disabled?: boolean;
  /** Form field name; a hidden input carries the value */
  name?: string;
  id?: string;
  /** Start open (docs and tests) */
  defaultOpen?: boolean;
  className?: string;
}

/** Figma: Select (trigger) + Dropdown menu (list). A WAI-ARIA select-only combobox. */
export function Select({ options, value, defaultValue, onChange, label, hint, error, placeholder = 'Select an option', size = 'md', disabled, name, id, defaultOpen, className }: SelectProps) {
  const fieldId = useFieldId(id);
  const controlled = value !== undefined;
  const [inner, setInner] = useState(defaultValue);
  const current = controlled ? value : inner;
  const [open, setOpen] = useState(!!defaultOpen);
  const [active, setActive] = useState(-1);
  const wrap = useRef<HTMLDivElement>(null);
  const typed = useRef({ text: '', at: 0 });
  const selected = options.find((o) => o.value === current);
  const enabled = options.map((o, i) => (o.disabled ? -1 : i)).filter((i) => i >= 0);
  const message = error || hint;

  const close = useCallback(() => setOpen(false), []);
  useOutsideClick([wrap], close, open);

  const pick = (i: number) => {
    const o = options[i];
    if (!o || o.disabled) return;
    if (!controlled) setInner(o.value);
    onChange?.(o.value);
    setOpen(false);
  };
  const show = (start?: number) => {
    if (disabled) return;
    const sel = options.findIndex((o) => o.value === current);
    setActive(start ?? (sel >= 0 ? sel : enabled[0] ?? -1));
    setOpen(true);
  };
  const onKey = (e: KeyboardEvent) => {
    const pos = enabled.indexOf(active);
    if (!open) {
      if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(e.key)) { e.preventDefault(); show(); }
      return;
    }
    if (e.key === 'ArrowDown') { e.preventDefault(); setActive(enabled[Math.min(pos + 1, enabled.length - 1)]); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActive(enabled[Math.max(pos - 1, 0)]); }
    else if (e.key === 'Home') { e.preventDefault(); setActive(enabled[0]); }
    else if (e.key === 'End') { e.preventDefault(); setActive(enabled[enabled.length - 1]); }
    else if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); pick(active); }
    else if (e.key === 'Escape') { e.preventDefault(); close(); }
    else if (e.key === 'Tab') { close(); }
    else if (e.key.length === 1) {
      const now = Date.now();
      typed.current.text = (now - typed.current.at > 600 ? '' : typed.current.text) + e.key.toLowerCase();
      typed.current.at = now;
      const hit = enabled.find((i) => options[i].label.toLowerCase().startsWith(typed.current.text));
      if (hit !== undefined) setActive(hit);
    }
  };

  return (
    <div ref={wrap} className={cx('at-field', 'at-select', className)}>
      {label ? <span id={`${fieldId}-label`} className="at-field__label">{label}</span> : null}
      <div className="at-anchor">
      <button
        type="button"
        id={fieldId}
        role="combobox"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={`${fieldId}-list`}
        aria-labelledby={label ? `${fieldId}-label ${fieldId}` : undefined}
        aria-activedescendant={open && active >= 0 ? `${fieldId}-o${active}` : undefined}
        aria-invalid={error ? true : undefined}
        aria-describedby={message ? `${fieldId}-msg` : undefined}
        disabled={disabled}
        className={cx('at-input', `at-input--${size}`, 'at-select__trigger', error && 'at-input--error', disabled && 'at-input--disabled', open && 'is-open')}
        onClick={() => (open ? close() : show())}
        onKeyDown={onKey}
      >
        {selected?.icon}
        <span className={cx('at-select__value', !selected && 'is-placeholder')}>{selected ? selected.label : placeholder}</span>
        <Icon name="chevronDown" size={20} className="at-select__chevron" />
      </button>
      {open ? (
        <div id={`${fieldId}-list`} role="listbox" aria-labelledby={label ? `${fieldId}-label` : undefined} className="at-menu at-menu--start at-select__list">
          {options.map((o, i) => (
            <MenuItem
              key={o.value}
              id={`${fieldId}-o${i}`}
              role="option"
              label={o.label}
              icon={o.icon}
              disabled={o.disabled}
              selected={o.value === current}
              active={i === active}
              onSelect={() => pick(i)}
            />
          ))}
        </div>
      ) : null}
      </div>
      {name ? <input type="hidden" name={name} value={current ?? ''} /> : null}
      {message ? <p id={`${fieldId}-msg`} className={cx('at-field__hint', error && 'at-field__hint--error')}>{message}</p> : null}
    </div>
  );
}
