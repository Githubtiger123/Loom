import { useCallback, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { cx, useFieldId, useOutsideClick } from '../../utils';
import { Icon } from '../Icon';
import { useControllable } from './internal';

export interface ModelOption {
  /** Model id sent to your backend */
  value: string;
  /** Display name ("Claude Sonnet 4.5") */
  label: string;
  /** Provider name ("Anthropic"), shown under the label */
  provider?: string;
  /** Figma: Provider icon — the provider's logo (16–20px) */
  icon?: ReactNode;
  /** One line about when to pick it */
  description?: string;
  /** Figma: Capabilities — short badges such as "Vision", "Tools", "Reasoning", "Fast" */
  capabilities?: string[];
  /** Badge after the name ("New", "Beta") */
  badge?: string;
  /** Not selectable (plan, region or availability) */
  disabled?: boolean;
}

export interface ModelSelectorProps {
  /** Figma: Models */
  models: ModelOption[];
  value?: string;
  defaultValue?: string;
  onChange?: (value: string) => void;
  /** Accessible name; shown only when `showLabel` is set */
  label?: string;
  /** Shows `label` above the trigger (forms and settings) */
  showLabel?: boolean;
  /**
   * Figma: Style.
   * `ghost` (default) — borderless trigger for a prompt toolbar.
   * `outline` — bordered like Select, for settings pages.
   */
  variant?: 'ghost' | 'outline';
  /** Figma: Size — sm 32 · md 40 */
  size?: 'sm' | 'md';
  /** Which way the list opens; `up` suits a prompt input at the bottom of the screen */
  placement?: 'down' | 'up';
  disabled?: boolean;
  /** Start open (docs and tests) */
  defaultOpen?: boolean;
  className?: string;
}

/**
 * Figma: Model selector — Select for AI models with provider icon, description and capability badges.
 * The same select-only combobox pattern as Select: ↑ ↓ Home End Enter Esc and type-ahead.
 */
export function ModelSelector({ models, value, defaultValue, onChange, label = 'Model', showLabel = false, variant = 'ghost', size = 'sm', placement = 'up', disabled, defaultOpen, className }: ModelSelectorProps) {
  const id = useFieldId();
  const [current, setCurrent] = useControllable(value, defaultValue ?? models[0]?.value ?? '', onChange);
  const [open, setOpen] = useState(!!defaultOpen);
  const [active, setActive] = useState(-1);
  const wrap = useRef<HTMLDivElement>(null);
  const typed = useRef({ text: '', at: 0 });
  const selected = models.find((m) => m.value === current);
  const enabled = models.map((m, i) => (m.disabled ? -1 : i)).filter((i) => i >= 0);
  const close = useCallback(() => setOpen(false), []);
  useOutsideClick([wrap], close, open);

  const pick = (i: number) => {
    const m = models[i];
    if (!m || m.disabled) return;
    setCurrent(m.value);
    setOpen(false);
  };
  const show = () => {
    if (disabled) return;
    const sel = models.findIndex((m) => m.value === current);
    setActive(sel >= 0 ? sel : enabled[0] ?? -1);
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
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); }
    else if (e.key === 'Tab') close();
    else if (e.key.length === 1) {
      const now = Date.now();
      typed.current.text = (now - typed.current.at > 600 ? '' : typed.current.text) + e.key.toLowerCase();
      typed.current.at = now;
      const hit = enabled.find((i) => models[i].label.toLowerCase().startsWith(typed.current.text));
      if (hit !== undefined) setActive(hit);
    }
  };

  return (
    <div ref={wrap} className={cx('at-model', `at-model--${variant}`, `at-model--${size}`, className)}>
      {showLabel ? <span id={`${id}-label`} className="at-field__label">{label}</span> : null}
      <div className="at-anchor">
        <button
          type="button"
          id={id}
          role="combobox"
          aria-haspopup="listbox"
          aria-expanded={open}
          aria-controls={`${id}-list`}
          aria-label={showLabel ? undefined : label}
          aria-labelledby={showLabel ? `${id}-label ${id}` : undefined}
          aria-activedescendant={open && active >= 0 ? `${id}-o${active}` : undefined}
          disabled={disabled}
          className={cx('at-model__trigger', open && 'is-open')}
          onClick={() => (open ? close() : show())}
          onKeyDown={onKey}
        >
          {selected?.icon ? <span className="at-model__icon" aria-hidden="true">{selected.icon}</span> : null}
          <span className="at-model__value">{selected?.label ?? 'Choose a model'}</span>
          <Icon name="chevronDown" size={16} className="at-model__chev" />
        </button>
        {open ? (
          <div id={`${id}-list`} role="listbox" aria-label={label} className={cx('at-menu', 'at-model__list', `at-model__list--${placement}`)}>
            {models.map((m, i) => (
              <div
                key={m.value}
                id={`${id}-o${i}`}
                role="option"
                aria-selected={m.value === current}
                aria-disabled={m.disabled || undefined}
                className={cx('at-model__option', i === active && 'is-active', m.value === current && 'is-selected', m.disabled && 'is-disabled')}
                onClick={() => pick(i)}
                onMouseMove={() => !m.disabled && setActive(i)}
              >
                <span className="at-model__option-icon" aria-hidden="true">{m.icon ?? <Icon name="sparkle" size={16} />}</span>
                <span className="at-model__option-main">
                  <span className="at-model__option-name">
                    {m.label}
                    {m.badge ? <span className="at-model__badge">{m.badge}</span> : null}
                  </span>
                  {m.provider || m.description ? (
                    <span className="at-model__option-desc">{[m.provider, m.description].filter(Boolean).join(' · ')}</span>
                  ) : null}
                  {m.capabilities?.length ? (
                    <span className="at-model__caps">
                      {m.capabilities.map((c) => <span key={c} className="at-model__cap">{c}</span>)}
                    </span>
                  ) : null}
                </span>
                {m.value === current ? <Icon name="check" size={16} className="at-model__check" /> : null}
              </div>
            ))}
          </div>
        ) : null}
      </div>
    </div>
  );
}
