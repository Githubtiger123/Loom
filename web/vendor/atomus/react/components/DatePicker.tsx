import { useCallback, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { cx, useFieldId, useOutsideClick } from '../utils';
import { Icon } from './Icon';

/** Dates are ISO strings 'YYYY-MM-DD' (no time zones). */
export type ISODate = string;
export interface DateRange { start: ISODate | null; end: ISODate | null }

const pad = (n: number) => String(n).padStart(2, '0');
const iso = (y: number, m: number, d: number) => `${y}-${pad(m + 1)}-${pad(d)}`;
const parts = (s: ISODate) => { const [y, m, d] = s.split('-').map(Number); return { y, m: m - 1, d }; };
const today = () => { const t = new Date(); return iso(t.getFullYear(), t.getMonth(), t.getDate()); };
const addDays = (s: ISODate, n: number) => { const { y, m, d } = parts(s); const t = new Date(Date.UTC(y, m, d + n)); return iso(t.getUTCFullYear(), t.getUTCMonth(), t.getUTCDate()); };

export interface CalendarProps {
  /** Figma: Date picker Type */
  type?: 'single' | 'range';
  value?: ISODate | null;
  range?: DateRange;
  onChange?: (value: ISODate) => void;
  onRangeChange?: (range: DateRange) => void;
  min?: ISODate;
  max?: ISODate;
  /** Monday-first by default */
  weekStartsOn?: 0 | 1;
  locale?: string;
  className?: string;
}

/** Figma: Date picker body with Calendar day cells (Default, Today, Selected, In range, Outside, Disabled). Arrow keys move days, PageUp/PageDown move months. */
export function Calendar({ type = 'single', value, range, onChange, onRangeChange, min, max, weekStartsOn = 1, locale, className }: CalendarProps) {
  const anchor = (type === 'range' ? range?.start : value) ?? today();
  const [view, setView] = useState(() => { const p = parts(anchor); return { y: p.y, m: p.m }; });
  const [focus, setFocus] = useState<ISODate>(anchor);
  const grid = useRef<HTMLDivElement>(null);
  const t = today();

  const days = useMemo(() => {
    const first = new Date(Date.UTC(view.y, view.m, 1)).getUTCDay();
    const lead = (first - weekStartsOn + 7) % 7;
    const start = addDays(iso(view.y, view.m, 1), -lead);
    return Array.from({ length: 42 }, (_, i) => addDays(start, i));
  }, [view, weekStartsOn]);
  const weekdays = useMemo(() => Array.from({ length: 7 }, (_, i) => new Date(Date.UTC(2024, 0, 1 + ((i + weekStartsOn + 6) % 7))).toLocaleDateString(locale, { weekday: 'short', timeZone: 'UTC' }).slice(0, 2)), [weekStartsOn, locale]);
  const monthLabel = new Date(Date.UTC(view.y, view.m, 1)).toLocaleDateString(locale, { month: 'long', year: 'numeric', timeZone: 'UTC' });

  const disabled = (d: ISODate) => (min !== undefined && d < min) || (max !== undefined && d > max);
  const pick = (d: ISODate) => {
    if (disabled(d)) return;
    setFocus(d);
    if (type === 'single') { onChange?.(d); return; }
    const r = range ?? { start: null, end: null };
    if (!r.start || r.end) onRangeChange?.({ start: d, end: null });
    else if (d < r.start) onRangeChange?.({ start: d, end: r.start });
    else onRangeChange?.({ start: r.start, end: d });
  };
  const move = (d: ISODate) => {
    setFocus(d);
    const p = parts(d);
    if (p.y !== view.y || p.m !== view.m) setView({ y: p.y, m: p.m });
    requestAnimationFrame(() => grid.current?.querySelector<HTMLButtonElement>(`[data-date="${d}"]`)?.focus());
  };
  const shiftMonth = (n: number) => { const dt = new Date(Date.UTC(view.y, view.m + n, 1)); setView({ y: dt.getUTCFullYear(), m: dt.getUTCMonth() }); };
  const onKey = (e: KeyboardEvent) => {
    const map: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 };
    if (map[e.key] !== undefined) { e.preventDefault(); move(addDays(focus, map[e.key])); }
    else if (e.key === 'PageUp' || e.key === 'PageDown') {
      e.preventDefault();
      const p = parts(focus); const dt = new Date(Date.UTC(p.y, p.m + (e.key === 'PageUp' ? -1 : 1), p.d));
      move(iso(dt.getUTCFullYear(), dt.getUTCMonth(), dt.getUTCDate()));
    }
  };

  return (
    <div className={cx('at-calendar', className)}>
      <div className="at-calendar__head">
        <button type="button" className="at-calendar__nav" aria-label="Previous month" onClick={() => shiftMonth(-1)}><Icon name="chevronLeft" size={20} /></button>
        <span className="at-calendar__month" aria-live="polite">{monthLabel}</span>
        <button type="button" className="at-calendar__nav" aria-label="Next month" onClick={() => shiftMonth(1)}><Icon name="chevronRight" size={20} /></button>
      </div>
      <div ref={grid} role="grid" aria-label={monthLabel} className="at-calendar__grid" onKeyDown={onKey}>
        <div role="row" className="at-calendar__row">
          {weekdays.map((w, i) => <span key={i} role="columnheader" className="at-calendar__weekday">{w}</span>)}
        </div>
        {Array.from({ length: 6 }, (_, w) => (
          <div role="row" className="at-calendar__row" key={w}>
            {days.slice(w * 7, w * 7 + 7).map((d) => {
              const p = parts(d);
              const outside = p.m !== view.m;
              const isSel = type === 'single' ? d === value : d === range?.start || d === range?.end;
              const inRange = type === 'range' && !!range?.start && !!range?.end && d > range.start && d < range.end;
              return (
                <span role="gridcell" key={d} aria-selected={isSel || undefined}>
                  <button
                    type="button"
                    data-date={d}
                    tabIndex={d === focus ? 0 : -1}
                    disabled={disabled(d)}
                    aria-current={d === t ? 'date' : undefined}
                    className={cx('at-day', outside && 'is-outside', d === t && 'is-today', isSel && 'is-selected', inRange && 'is-in-range')}
                    onClick={() => pick(d)}
                  >
                    {p.d}
                  </button>
                </span>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}

export interface DatePickerProps extends Omit<CalendarProps, 'className'> {
  /** Figma: Date input — Label */
  label?: string;
  hint?: string;
  error?: string;
  placeholder?: string;
  size?: 'sm' | 'md' | 'lg';
  disabled?: boolean;
  id?: string;
  /** Start open (docs and tests) */
  defaultOpen?: boolean;
  className?: string;
}

/** Figma: Date input + Date picker. A field that opens the calendar in a popover. */
export function DatePicker({ label, hint, error, placeholder, size = 'md', disabled, id, defaultOpen, className, ...cal }: DatePickerProps) {
  const fieldId = useFieldId(id);
  const [open, setOpen] = useState(!!defaultOpen);
  const wrap = useRef<HTMLDivElement>(null);
  const close = useCallback(() => setOpen(false), []);
  useOutsideClick([wrap], close, open);
  const fmt = (d: ISODate | null | undefined) => (d ? new Date(`${d}T00:00:00Z`).toLocaleDateString(cal.locale, { day: 'numeric', month: 'short', year: 'numeric', timeZone: 'UTC' }) : '');
  const text = cal.type === 'range' ? (cal.range?.start ? `${fmt(cal.range.start)} – ${fmt(cal.range.end) || '…'}` : '') : fmt(cal.value);
  const message = error || hint;
  return (
    <div ref={wrap} className={cx('at-field', 'at-datepicker', className)} onKeyDown={(e) => { if (e.key === 'Escape') close(); }}>
      {label ? <label htmlFor={fieldId} className="at-field__label">{label}</label> : null}
      <div className="at-anchor">
      <button
        type="button"
        id={fieldId}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-describedby={message ? `${fieldId}-msg` : undefined}
        disabled={disabled}
        className={cx('at-input', `at-input--${size}`, 'at-select__trigger', error && 'at-input--error', disabled && 'at-input--disabled')}
        onClick={() => setOpen(!open)}
      >
        <Icon name="calendar" size={20} />
        <span className={cx('at-select__value', !text && 'is-placeholder')}>{text || placeholder || (cal.type === 'range' ? 'Select dates' : 'Select date')}</span>
      </button>
      {open ? (
        <div role="dialog" aria-label={label ?? 'Choose date'} className="at-datepicker__pop">
          <Calendar
            {...cal}
            onChange={(d) => { cal.onChange?.(d); close(); }}
            onRangeChange={(r) => { cal.onRangeChange?.(r); if (r.start && r.end) close(); }}
          />
        </div>
      ) : null}
      </div>
      {message ? <p id={`${fieldId}-msg`} className={cx('at-field__hint', error && 'at-field__hint--error')}>{message}</p> : null}
    </div>
  );
}
