import { cloneElement, isValidElement, useCallback, useRef, useState, type KeyboardEvent, type ReactElement, type ReactNode } from 'react';
import { cx, useFieldId, useOutsideClick } from '../utils';

export interface MenuItemProps {
  /** Figma: Label */
  label: ReactNode;
  /** Figma: Leading icon + Leading icon swap */
  icon?: ReactNode;
  /** Figma: Shortcut + Shortcut text */
  shortcut?: string;
  /** Figma: Size — sm 32 · md 40 */
  size?: 'sm' | 'md';
  /** Figma: State=Selected — shows a check */
  selected?: boolean;
  disabled?: boolean;
  /** Red label for destructive actions (Context menu) */
  destructive?: boolean;
  onSelect?: () => void;
  role?: 'menuitem' | 'option' | 'menuitemcheckbox';
  id?: string;
  active?: boolean;
}

/** Figma: Menu item — row for dropdowns, selects, context and command menus. */
export function MenuItem({ label, icon, shortcut, size = 'sm', selected, disabled, destructive, onSelect, role = 'menuitem', id, active }: MenuItemProps) {
  return (
    <div
      id={id}
      role={role}
      aria-disabled={disabled || undefined}
      aria-selected={role === 'option' ? !!selected : undefined}
      tabIndex={-1}
      className={cx('at-menu-item', `at-menu-item--${size}`, selected && 'is-selected', active && 'is-active', destructive && 'at-menu-item--destructive', disabled && 'is-disabled')}
      onClick={() => { if (!disabled) onSelect?.(); }}
    >
      {icon ? <span className="at-menu-item__icon">{icon}</span> : null}
      <span className="at-menu-item__label">{label}</span>
      {shortcut ? <kbd className="at-menu-item__shortcut">{shortcut}</kbd> : null}
      {selected && role === 'option' ? (
        <svg className="at-menu-item__check" width="16" height="16" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M4 10.5l3.5 3.5L16 6" /></svg>
      ) : null}
    </div>
  );
}

export type DropdownItem =
  | { type?: 'item'; label: ReactNode; icon?: ReactNode; shortcut?: string; disabled?: boolean; destructive?: boolean; onSelect?: () => void }
  | { type: 'separator' }
  | { type: 'heading'; label: ReactNode };

export interface DropdownMenuProps {
  /** The element that opens the menu, usually a Button */
  trigger: ReactElement;
  /** Figma: Items slot */
  items: DropdownItem[];
  align?: 'start' | 'end';
  size?: 'sm' | 'md';
  /** Accessible name for the menu */
  label?: string;
  /** Start open (docs and tests) */
  defaultOpen?: boolean;
  className?: string;
}

/** Figma: Dropdown menu / Context menu — keyboard: ↑ ↓ Home End Enter Esc. */
export function DropdownMenu({ trigger, items, align = 'start', size = 'sm', label, defaultOpen, className }: DropdownMenuProps) {
  const [open, setOpen] = useState(!!defaultOpen);
  const [active, setActive] = useState(-1);
  const wrap = useRef<HTMLDivElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const id = useFieldId();
  const actionable = items.map((it, i) => ((it.type ?? 'item') === 'item' && !(it as { disabled?: boolean }).disabled ? i : -1)).filter((i) => i >= 0);

  const close = useCallback(() => { setOpen(false); setActive(-1); }, []);
  useOutsideClick([wrap], close, open);

  const openMenu = (first = true) => {
    setOpen(true);
    setActive(first ? actionable[0] ?? -1 : actionable[actionable.length - 1] ?? -1);
    requestAnimationFrame(() => menu.current?.focus());
  };
  const choose = (i: number) => {
    const it = items[i] as { onSelect?: () => void };
    close();
    it.onSelect?.();
  };
  const onKey = (e: KeyboardEvent) => {
    if (!open) return;
    const pos = actionable.indexOf(active);
    if (e.key === 'ArrowDown') { e.preventDefault(); setActive(actionable[(pos + 1) % actionable.length]); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActive(actionable[(pos - 1 + actionable.length) % actionable.length]); }
    else if (e.key === 'Home') { e.preventDefault(); setActive(actionable[0]); }
    else if (e.key === 'End') { e.preventDefault(); setActive(actionable[actionable.length - 1]); }
    else if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); if (active >= 0) choose(active); }
    else if (e.key === 'Escape' || e.key === 'Tab') { close(); }
  };

  const triggerEl = isValidElement(trigger)
    ? cloneElement(trigger as ReactElement<Record<string, unknown>>, {
        'aria-haspopup': 'menu',
        'aria-expanded': open,
        'aria-controls': open ? `${id}-menu` : undefined,
        onClick: () => (open ? close() : openMenu()),
        onKeyDown: (e: KeyboardEvent) => {
          if (e.key === 'ArrowDown') { e.preventDefault(); openMenu(true); }
          if (e.key === 'ArrowUp') { e.preventDefault(); openMenu(false); }
        },
      })
    : trigger;

  return (
    <div ref={wrap} className={cx('at-dropdown', className)} onKeyDown={onKey}>
      {triggerEl}
      {open ? (
        <div
          ref={menu}
          id={`${id}-menu`}
          role="menu"
          aria-label={label}
          tabIndex={-1}
          aria-activedescendant={active >= 0 ? `${id}-i${active}` : undefined}
          className={cx('at-menu', `at-menu--${align}`)}
        >
          {items.map((it, i) => {
            if (it.type === 'separator') return <div key={i} role="separator" className="at-menu__sep" />;
            if (it.type === 'heading') return <div key={i} role="presentation" className="at-menu__heading">{it.label}</div>;
            return (
              <MenuItem
                key={i}
                id={`${id}-i${i}`}
                size={size}
                label={it.label}
                icon={it.icon}
                shortcut={it.shortcut}
                disabled={it.disabled}
                destructive={it.destructive}
                active={active === i}
                onSelect={() => choose(i)}
              />
            );
          })}
        </div>
      ) : null}
    </div>
  );
}
