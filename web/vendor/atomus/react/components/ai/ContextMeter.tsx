import { useCallback, useRef, useState } from 'react';
import { cx, useFieldId } from '../../utils';
import { useDismiss } from './internal';

export interface ContextUsagePart {
  /** "Input", "Output", "Cached", "Tools" … */
  label: string;
  /** Tokens in this part */
  tokens: number;
}

export interface ContextMeterProps {
  /** Figma: Used — tokens in the context window now */
  used: number;
  /** Figma: Limit — the model's context window in tokens */
  limit: number;
  /** Figma: Cost — spend for this conversation, in `currency` */
  cost?: number;
  /** ISO 4217 currency of `cost` */
  currency?: string;
  /** Optional split of `used` (input, output, cached …) shown in the details */
  breakdown?: ContextUsagePart[];
  /** Name of the meter */
  label?: string;
  /**
   * Figma: Style.
   * `compact` (default) — a ring and percentage for a prompt toolbar; details open on click.
   * `bar` — label, bar and numbers inline, for side panels and settings.
   */
  variant?: 'compact' | 'bar';
  /** Percentage at which the meter turns warning; error from 95% */
  warnAt?: number;
  /** BCP 47 locale for numbers */
  locale?: string;
  /** Which way the compact details open; `up` suits a prompt input at the bottom of the screen */
  placement?: 'up' | 'down';
  /** Start with the compact details open (docs and tests) */
  defaultOpen?: boolean;
  className?: string;
}

const fmt = (n: number, locale?: string) => new Intl.NumberFormat(locale, { notation: n >= 1000 ? 'compact' : 'standard', maximumFractionDigits: 1 }).format(n);

/**
 * Figma: Context meter — how much of the model's context window a conversation uses, and what it cost.
 * A WAI-ARIA meter; the compact variant is a button that opens the details.
 */
export function ContextMeter({ used, limit, cost, currency = 'USD', breakdown, label = 'Context window', variant = 'compact', warnAt = 80, locale, placement = 'up', defaultOpen, className }: ContextMeterProps) {
  const id = useFieldId();
  const [open, setOpen] = useState(!!defaultOpen);
  const wrap = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const close = useCallback(() => setOpen(false), []);
  useDismiss(open, close, [wrap], trigger);
  const pct = limit > 0 ? Math.min(100, Math.round((used / limit) * 100)) : 0;
  const tone = pct >= 95 ? 'error' : pct >= warnAt ? 'warning' : 'ok';
  const valueText = `${pct}% used, ${fmt(used, locale)} of ${fmt(limit, locale)} tokens`;
  const money = cost !== undefined ? new Intl.NumberFormat(locale, { style: 'currency', currency, maximumFractionDigits: cost < 1 ? 3 : 2 }).format(cost) : null;

  const meter = (
    <div className="at-context__meter">
      <div className="at-context__row">
        <span id={`${id}-label`} className="at-context__label">{label}</span>
        <span className="at-context__nums">{fmt(used, locale)} / {fmt(limit, locale)}</span>
      </div>
      <div role="meter" aria-labelledby={`${id}-label`} aria-valuemin={0} aria-valuemax={limit} aria-valuenow={Math.min(used, limit)} aria-valuetext={valueText} className="at-context__track">
        <span className="at-context__fill" style={{ width: `${pct}%` }} />
      </div>
      {breakdown?.length ? (
        <dl className="at-context__list">
          {breakdown.map((b) => (
            <div key={b.label} className="at-context__item"><dt>{b.label}</dt><dd>{fmt(b.tokens, locale)}</dd></div>
          ))}
          {money ? <div className="at-context__item at-context__item--total"><dt>Cost</dt><dd>{money}</dd></div> : null}
        </dl>
      ) : money ? (
        <div className="at-context__row at-context__row--cost"><span>Cost</span><span>{money}</span></div>
      ) : null}
      {tone !== 'ok' ? <p className="at-context__hint">{tone === 'error' ? 'Almost full: older messages will be summarised or dropped.' : 'Getting full: start a new chat for a new topic.'}</p> : null}
    </div>
  );

  if (variant === 'bar') return <div className={cx('at-context', 'at-context--bar', `is-${tone}`, className)}>{meter}</div>;

  const r = 7;
  const c = 2 * Math.PI * r;
  return (
    <div ref={wrap} className={cx('at-context', 'at-context--compact', `is-${tone}`, className)}>
      <button ref={trigger} type="button" className="at-context__trigger" aria-expanded={open} aria-controls={`${id}-pop`} aria-label={`${label}: ${valueText}`} onClick={() => setOpen(!open)}>
        <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true" className="at-context__ring">
          <circle cx="9" cy="9" r={r} className="at-context__ring-track" />
          <circle cx="9" cy="9" r={r} className="at-context__ring-fill" strokeDasharray={c} strokeDashoffset={c * (1 - pct / 100)} transform="rotate(-90 9 9)" />
        </svg>
        <span aria-hidden="true">{pct}%</span>
      </button>
      {open ? <div id={`${id}-pop`} role="dialog" aria-label={label} className={cx('at-context__pop', `at-context__pop--${placement}`)}>{meter}</div> : null}
    </div>
  );
}
