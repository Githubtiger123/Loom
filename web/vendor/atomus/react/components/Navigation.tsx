import type { AnchorHTMLAttributes, HTMLAttributes, ReactNode } from 'react';
import { cx } from '../utils';
import { Icon } from './Icon';

export interface NavItemProps extends AnchorHTMLAttributes<HTMLAnchorElement> {
  /** Figma: Label */
  label: string;
  /** Figma: Icon swap */
  icon?: ReactNode;
  /** Figma: Badge — a count or short label after the text */
  badge?: ReactNode;
  /** Figma: Chevron — marks an item with children */
  chevron?: boolean;
  /** Figma: State=Active */
  active?: boolean;
  /** Figma: Collapsed — icon only, label becomes the tooltip and accessible name */
  collapsed?: boolean;
}

/** Figma: Nav item. Renders a link; pass href (or onClick). */
export function NavItem({ label, icon, badge, chevron, active, collapsed, className, ...rest }: NavItemProps) {
  return (
    <a
      className={cx('at-nav-item', active && 'is-active', collapsed && 'is-collapsed', className)}
      aria-current={active ? 'page' : undefined}
      aria-label={collapsed ? label : undefined}
      title={collapsed ? label : undefined}
      {...rest}
    >
      {icon ? <span className="at-nav-item__icon">{icon}</span> : null}
      {collapsed ? null : <span className="at-nav-item__label">{label}</span>}
      {!collapsed && badge != null ? <span className="at-nav-item__badge">{badge}</span> : null}
      {!collapsed && chevron ? <Icon name="chevronDown" size={16} className="at-nav-item__chevron" /> : null}
    </a>
  );
}

export interface SidebarNavigationProps extends HTMLAttributes<HTMLElement> {
  /** Logo or workspace switcher at the top */
  header?: ReactNode;
  /** NavItems */
  children: ReactNode;
  /** Pinned at the bottom: secondary links, user card */
  footer?: ReactNode;
  /** Figma: Collapsed — 280px or a 72px rail */
  collapsed?: boolean;
  'aria-label'?: string;
}

/** Figma: Sidebar navigation (280px, or a 72px rail when collapsed). */
export function SidebarNavigation({ header, children, footer, collapsed, className, ...rest }: SidebarNavigationProps) {
  return (
    <nav aria-label={rest['aria-label'] ?? 'Main'} className={cx('at-sidebar', collapsed && 'is-collapsed', className)} {...rest}>
      {header ? <div className="at-sidebar__header">{header}</div> : null}
      <div className="at-sidebar__items">{children}</div>
      {footer ? <div className="at-sidebar__footer">{footer}</div> : null}
    </nav>
  );
}

export interface AppHeaderProps extends HTMLAttributes<HTMLElement> {
  /** Logo or product name */
  brand?: ReactNode;
  /** Primary links (NavItem or anchors) */
  nav?: ReactNode;
  /** Right side: search, notifications, avatar */
  actions?: ReactNode;
}

/** Figma: App header — top navigation bar for apps. */
export function AppHeader({ brand, nav, actions, className, ...rest }: AppHeaderProps) {
  return (
    <header className={cx('at-app-header', className)} {...rest}>
      {brand ? <div className="at-app-header__brand">{brand}</div> : null}
      {nav ? <nav aria-label="Main" className="at-app-header__nav">{nav}</nav> : null}
      {actions ? <div className="at-app-header__actions">{actions}</div> : null}
    </header>
  );
}
