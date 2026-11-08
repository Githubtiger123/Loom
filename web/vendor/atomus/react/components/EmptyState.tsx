import type { HTMLAttributes, ReactNode } from 'react';
import { cx } from '../utils';
import { Icon } from './Icon';

export interface EmptyStateProps extends Omit<HTMLAttributes<HTMLDivElement>, 'title'> {
  /** Figma: Title */
  title: ReactNode;
  /** Figma: Description */
  description?: ReactNode;
  /** Featured icon glyph */
  icon?: ReactNode;
  /** Figma: Actions — usually Outline + Primary buttons */
  actions?: ReactNode;
  /** Figma: Size — sm (panels) · md (pages) */
  size?: 'sm' | 'md';
}

export function EmptyState({ title, description, icon, actions, size = 'md', className, ...rest }: EmptyStateProps) {
  return (
    <div className={cx('at-empty', `at-empty--${size}`, className)} {...rest}>
      <span className="at-empty__icon" aria-hidden="true">{icon ?? <Icon name="search" size={24} />}</span>
      <div className="at-empty__text">
        <h3 className="at-empty__title">{title}</h3>
        {description ? <p className="at-empty__desc">{description}</p> : null}
      </div>
      {actions ? <div className="at-empty__actions">{actions}</div> : null}
    </div>
  );
}
