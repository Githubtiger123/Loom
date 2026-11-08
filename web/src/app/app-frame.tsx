import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { Link, NavLink } from 'react-router-dom';
import { Button } from '@atomus';
import { ChevronLeft, Menu, X, type LucideIcon } from 'lucide-react';
import { AccountControls } from '@/components/account-controls';

export type AppNavItem = {
  to: string;
  label: string;
  icon: LucideIcon;
  end?: boolean;
  active?: boolean;
};

// Shared presentation only: callers independently evaluate global/space access.
export function AppFrame({
  context,
  caption,
  items,
  navigationPending,
  breadcrumb,
  children,
}: {
  context: { label: string; title: string; icon: ReactNode; backLabel: string };
  caption: string;
  items: AppNavItem[];
  navigationPending?: boolean;
  breadcrumb: ReactNode;
  children: ReactNode;
}) {
  const [mobileOpen, setMobileOpen] = useState(false);
  const sidebarId = useId();
  const toggle = useRef<HTMLButtonElement>(null);
  const closeNavigation = () => setMobileOpen(false);
  useEffect(() => {
    if (!mobileOpen) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setMobileOpen(false);
        toggle.current?.focus();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [mobileOpen]);
  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        跳转到主要内容
      </a>
      {mobileOpen && (
        <button
          className="shell-backdrop"
          type="button"
          aria-label="关闭侧栏导航"
          onClick={closeNavigation}
        />
      )}
      <aside
        id={sidebarId}
        className={`sidebar ${mobileOpen ? 'is-open' : ''}`}
        aria-label={`${context.title}导航`}
      >
        <Link className="wordmark" to="/" onClick={closeNavigation}>
          <span className="logo-mark">L</span>loom
        </Link>
        <div className="workspace-label">
          {context.icon}
          <div>
            <span className="small muted">{context.label}</span>
            <strong>{context.title}</strong>
          </div>
        </div>
        <Link className="switch-workspace" to="/" onClick={closeNavigation}>
          <ChevronLeft size={14} />
          {context.backLabel}
        </Link>
        <p className="nav-caption">{caption}</p>
        <nav aria-label={context.label}>
          {navigationPending && (
            <p className="small muted" role="status">
              正在读取可用模块…
            </p>
          )}
          {items.map(({ to, label, icon: Icon, end, active }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              onClick={closeNavigation}
              className={({ isActive }) => ((active ?? isActive) ? 'active' : undefined)}
              aria-current={active === false ? false : undefined}
            >
              <Icon size={18} />
              <span>{label}</span>
            </NavLink>
          ))}
        </nav>
        <div className="sidebar-footer">
          <span className="small muted">Loom Platform</span>
          <span className="small muted">开发 · 编排 · 部署</span>
        </div>
      </aside>
      <div className="app-main">
        <header className="topbar">
          <Button
            ref={toggle}
            hierarchy="tertiary"
            iconOnly
            iconLeading={mobileOpen ? <X size={20} /> : <Menu size={20} />}
            aria-label={mobileOpen ? '关闭导航' : '打开导航'}
            aria-expanded={mobileOpen}
            aria-controls={sidebarId}
            className="mobile-toggle"
            onClick={() => setMobileOpen(!mobileOpen)}
          />
          <div className="breadcrumb">{breadcrumb}</div>
          <AccountControls />
        </header>
        <main id="main-content" className="content" tabIndex={-1}>
          {children}
        </main>
      </div>
    </div>
  );
}
