import type { HTMLAttributes } from 'react';
import { cx } from '../utils';

export interface ProgressBarProps extends HTMLAttributes<HTMLDivElement> {
  /** 0–100 */
  value: number;
  /** Figma: Label — where the percentage sits */
  labelPosition?: 'none' | 'right' | 'bottom';
  /** Accessible name, e.g. "Storage used" */
  label?: string;
}

export function ProgressBar({ value, labelPosition = 'right', label = 'Progress', className, ...rest }: ProgressBarProps) {
  const v = Math.max(0, Math.min(100, value));
  return (
    <div className={cx('at-progress', `at-progress--${labelPosition}`, className)} {...rest}>
      <div className="at-progress__track" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={v} aria-label={label}>
        <div className="at-progress__fill" style={{ width: `${v}%` }} />
      </div>
      {labelPosition !== 'none' ? <span className="at-progress__value">{Math.round(v)}%</span> : null}
    </div>
  );
}
