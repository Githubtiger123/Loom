import type { HTMLAttributes, ReactNode } from 'react';
import { cx } from '../utils';
import { Icon } from './Icon';

export type BadgeColor = 'gray' | 'brand' | 'error' | 'warning' | 'success';

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  /** Figma: Label */
  children: ReactNode;
  /** Figma: Color */
  color?: BadgeColor;
  /** Figma: Style — light (tinted) or solid */
  variant?: 'light' | 'solid';
  /** Figma: Size — sm 20 · md 24 · lg 28 */
  size?: 'sm' | 'md' | 'lg';
  /** Figma: Dot */
  dot?: boolean;
  /** Figma: Leading icon + Leading icon swap */
  icon?: ReactNode;
  /** Figma: Close — shows a remove button */
  onClose?: () => void;
}

export function Badge({ children, color = 'gray', variant = 'light', size = 'md', dot, icon, onClose, className, ...rest }: BadgeProps) {
  return (
    <span className={cx('at-badge', `at-badge--${size}`, `at-badge--${variant}-${color}`, className)} {...rest}>
      {dot ? <span className="at-badge__dot" aria-hidden="true" /> : icon}
      {children}
      {onClose ? (
        <button type="button" className="at-badge__close" aria-label={`Remove ${typeof children === 'string' ? children : 'badge'}`} onClick={onClose}>
          <Icon name="x" size={12} />
        </button>
      ) : null}
    </span>
  );
}
