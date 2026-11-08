import type { HTMLAttributes, ReactNode } from 'react';
import { cx } from '../utils';

export interface CardProps extends Omit<HTMLAttributes<HTMLElement>, 'title'> {
  /** Figma: Title (Header on) */
  title?: ReactNode;
  /** Figma: Supporting text */
  supportingText?: ReactNode;
  /** Figma: Header action — e.g. a Button or icon button, top right */
  headerAction?: ReactNode;
  /** Figma: Content slot */
  children?: ReactNode;
  /** Figma: Footer — buttons aligned right */
  footer?: ReactNode;
  /** Figma: Style */
  variant?: 'outlined' | 'elevated' | 'filled';
  /** Figma: Padding — md 16 · lg 24 */
  padding?: 'md' | 'lg';
}

export function Card({ title, supportingText, headerAction, children, footer, variant = 'outlined', padding = 'md', className, ...rest }: CardProps) {
  const hasHeader = title || supportingText || headerAction;
  return (
    <section className={cx('at-card', `at-card--${variant}`, `at-card--pad-${padding}`, className)} {...rest}>
      {hasHeader ? (
        <header className="at-card__header">
          <div className="at-card__heading">
            {title ? <h3 className="at-card__title">{title}</h3> : null}
            {supportingText ? <p className="at-card__supporting">{supportingText}</p> : null}
          </div>
          {headerAction ? <div className="at-card__action">{headerAction}</div> : null}
        </header>
      ) : null}
      {children ? <div className="at-card__content">{children}</div> : null}
      {footer ? <footer className="at-card__footer">{footer}</footer> : null}
    </section>
  );
}
