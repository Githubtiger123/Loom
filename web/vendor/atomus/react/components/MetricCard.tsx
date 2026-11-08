import type { HTMLAttributes, ReactNode } from 'react';
import { cx } from '../utils';
import { Icon } from './Icon';

export interface MetricCardProps extends HTMLAttributes<HTMLDivElement> {
  /** Figma: Label */
  label: ReactNode;
  /** Figma: Value — pre-formatted ("$48.2k") */
  value: ReactNode;
  /** Figma: Type — simple · trend · chart */
  type?: 'simple' | 'trend' | 'chart';
  /** Change vs the comparison period, e.g. "12%" or "-0.4%" */
  change?: string;
  /** Inferred from a leading "-" in change when omitted */
  trend?: 'up' | 'down';
  /** Comparison period, e.g. "vs last month" */
  caption?: ReactNode;
  /** Sparkline values for type="chart" */
  data?: number[];
  /** Header action, e.g. a tertiary icon button */
  action?: ReactNode;
}

function Sparkline({ data, trend }: { data: number[]; trend: 'up' | 'down' }) {
  // Drawn in a fixed coordinate space and stretched to the space the card has left
  // (preserveAspectRatio="none" + non-scaling stroke), so it never overflows a narrow card.
  const w = 112;
  const h = 56;
  const pad = 3;
  const min = Math.min(...data);
  const max = Math.max(...data);
  const span = max - min || 1;
  const pts = data.map((d, i) => [pad + (i / Math.max(1, data.length - 1)) * (w - pad * 2), h - pad - ((d - min) / span) * (h - pad * 2)] as const);
  const line = pts.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join(' ');
  const area = `M${pts[0][0].toFixed(1)},${h} L${line.replace(/ /g, ' L')} L${pts[pts.length - 1][0].toFixed(1)},${h} Z`;
  return (
    <svg className={cx('at-metric__chart', `at-metric__chart--${trend}`)} viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" aria-hidden="true" focusable="false">
      <path className="at-metric__chart-area" d={area} />
      <polyline points={line} fill="none" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

export function MetricCard({ label, value, type = 'trend', change, trend, caption, data, action, className, ...rest }: MetricCardProps) {
  const dir = trend ?? (change?.trim().startsWith('-') ? 'down' : 'up');
  return (
    <div className={cx('at-metric', className)} {...rest}>
      <div className="at-metric__header">
        <p className="at-metric__label">{label}</p>
        {action}
      </div>
      <div className="at-metric__row">
        <div className="at-metric__main">
          <p className="at-metric__value">{value}</p>
          {type !== 'simple' && change ? (
            <p className="at-metric__trend">
              <span className={cx('at-metric__change', `at-metric__change--${dir}`)}>
                <Icon name={dir === 'down' ? 'arrowDown' : 'arrowUp'} size={16} />
                {change}
              </span>
              {caption ? <span className="at-metric__caption">{caption}</span> : null}
            </p>
          ) : null}
        </div>
        {type === 'chart' && data && data.length > 1 ? <Sparkline data={data} trend={dir} /> : null}
      </div>
    </div>
  );
}
