import type { HTMLAttributes, ReactNode } from 'react';
import { cx } from '../utils';
import { Icon, type IconName } from './Icon';

export type AlertColor = 'brand' | 'gray' | 'error' | 'warning' | 'success';
/** Aliases accepted for convenience: info → brand, neutral → gray, danger → error. */
export type AlertColorAlias = 'info' | 'neutral' | 'danger';

export interface AlertProps extends Omit<HTMLAttributes<HTMLDivElement>, 'title' | 'color'> {
  /** Figma: Title */
  title?: ReactNode;
  /** Figma: Description (+ Show description) */
  children?: ReactNode;
  /** Figma: Color. Aliases: `info` = brand, `neutral` = gray, `danger` = error. */
  color?: AlertColor | AlertColorAlias;
  /**
   * Figma: Style.
   * `subtle` (default) — tinted fill and hairline border.
   * `outline` — white surface with a coloured border; use on tinted or grey surfaces.
   * `solid` — full-colour fill; only for blocking, page-level issues.
   */
  variant?: 'subtle' | 'outline' | 'solid';
  /** Figma: Size — `md` (default) or the compact `sm` for dense UI, forms and side panels. */
  size?: 'sm' | 'md';
  /** Removes the radius and side borders so the alert can span the full width of a card or page edge. */
  flush?: boolean;
  /** Figma: Actions — up to two small buttons */
  actions?: ReactNode;
  /** Figma: Close */
  onClose?: () => void;
  /** Replaces the default status icon */
  icon?: ReactNode;
}

const ALIASES: Record<AlertColorAlias, AlertColor> = { info: 'brand', neutral: 'gray', danger: 'error' };
const ICONS: Record<AlertColor, IconName> = { brand: 'info', gray: 'info', error: 'alert', warning: 'alert', success: 'success' };

export function Alert({ title, children, color = 'brand', variant = 'subtle', size = 'md', flush = false, actions, onClose, icon, className, ...rest }: AlertProps) {
  const tone: AlertColor = (ALIASES as Record<string, AlertColor>)[color] ?? (color as AlertColor);
  const iconSize = size === 'sm' ? 16 : 20;
  return (
    <div
      role={tone === 'error' ? 'alert' : 'status'}
      className={cx('at-alert', `at-alert--${variant}`, `at-alert--${tone}`, size === 'sm' && 'at-alert--sm', flush && 'at-alert--flush', className)}
      {...rest}
    >
      <span className="at-alert__icon" aria-hidden="true">{icon ?? <Icon name={ICONS[tone]} size={iconSize} />}</span>
      <div className="at-alert__body">
        {title ? <div className="at-alert__title">{title}</div> : null}
        {children ? <div className="at-alert__text">{children}</div> : null}
        {actions ? <div className="at-alert__actions">{actions}</div> : null}
      </div>
      {onClose ? (
        <button type="button" className="at-alert__close" aria-label="Dismiss" onClick={onClose}>
          <Icon name="x" size={iconSize} />
        </button>
      ) : null}
    </div>
  );
}
