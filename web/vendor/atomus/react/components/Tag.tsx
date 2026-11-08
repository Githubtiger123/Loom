import type { HTMLAttributes, ReactNode } from 'react';
import { cx } from '../utils';
import { Icon } from './Icon';

export interface TagProps extends HTMLAttributes<HTMLSpanElement> {
  /** Figma: Label */
  children: ReactNode;
  /** Figma: Size — sm 24 · md 28 · lg 32 */
  size?: 'sm' | 'md' | 'lg';
  /** Figma: Leading icon + Leading icon swap */
  icon?: ReactNode;
  /** Figma: Close — shows the × button */
  onRemove?: () => void;
}

export function Tag({ children, size = 'md', icon, onRemove, className, ...rest }: TagProps) {
  return (
    <span className={cx('at-tag', `at-tag--${size}`, className)} {...rest}>
      {icon}
      {children}
      {onRemove ? (
        <button type="button" className="at-tag__x" aria-label={`Remove ${typeof children === 'string' ? children : 'tag'}`} onClick={onRemove}>
          <Icon name="x" size={12} />
        </button>
      ) : null}
    </span>
  );
}
