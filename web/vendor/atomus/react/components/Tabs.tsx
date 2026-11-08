import { useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { cx } from '../utils';

export interface TabItem {
  value: string;
  label: ReactNode;
  /** Small count badge after the label */
  count?: number;
  disabled?: boolean;
}

export interface TabsProps {
  items: TabItem[];
  /** Figma: Style */
  variant?: 'underline' | 'pill' | 'segmented';
  value?: string;
  defaultValue?: string;
  onChange?: (value: string) => void;
  /** Accessible name for the tab list */
  'aria-label'?: string;
  className?: string;
}

export function Tabs({ items, variant = 'underline', value, defaultValue, onChange, className, ...aria }: TabsProps) {
  const controlled = value !== undefined;
  const [inner, setInner] = useState(defaultValue ?? items[0]?.value);
  const current = controlled ? value : inner;
  const refs = useRef<Array<HTMLButtonElement | null>>([]);

  const select = (v: string) => {
    if (!controlled) setInner(v);
    onChange?.(v);
  };
  const onKeyDown = (e: KeyboardEvent) => {
    const enabled = items.filter((t) => !t.disabled);
    const i = enabled.findIndex((t) => t.value === current);
    let next: TabItem | undefined;
    if (e.key === 'ArrowRight') next = enabled[(i + 1) % enabled.length];
    if (e.key === 'ArrowLeft') next = enabled[(i - 1 + enabled.length) % enabled.length];
    if (e.key === 'Home') next = enabled[0];
    if (e.key === 'End') next = enabled[enabled.length - 1];
    if (next) {
      e.preventDefault();
      select(next.value);
      refs.current[items.indexOf(next)]?.focus();
    }
  };

  return (
    <div role="tablist" aria-label={aria['aria-label']} className={cx('at-tabs', `at-tabs--${variant}`, className)} onKeyDown={onKeyDown}>
      {items.map((t, i) => {
        const active = t.value === current;
        return (
          <button
            key={t.value}
            ref={(el) => { refs.current[i] = el; }}
            type="button"
            role="tab"
            aria-selected={active}
            tabIndex={active ? 0 : -1}
            disabled={t.disabled}
            className={cx('at-tab', active && 'is-active')}
            onClick={() => select(t.value)}
          >
            {t.label}
            {t.count != null ? <span className="at-tab__count">{t.count}</span> : null}
          </button>
        );
      })}
    </div>
  );
}
