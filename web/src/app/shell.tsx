import { useState } from 'react';
import { Link, NavLink, Outlet, useParams } from 'react-router-dom';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Button } from '@atomus';
import {
  BookOpen,
  Boxes,
  ChevronLeft,
  Database,
  GitBranch,
  LayoutDashboard,
  LogOut,
  Menu,
  Moon,
  Radio,
  Sun,
  Users,
  Settings,
  Gauge,
  X,
} from 'lucide-react';
import { api, ApiError, clearSession, trustedHeaderMode } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import type { Group, Me } from '@/lib/types';
import { ErrorState, Loading, StatusBadge } from '@/components/feedback';
import { AccountLinks } from '@/components/account-links';

export function Shell() {
  const { groupId = '' } = useParams();
  const permissions = useGroupPermissions(groupId);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [dark, setDark] = useState(() => document.documentElement.dataset.theme === 'dark');
  const valid =
    /^\d+$/.test(groupId) && Number.isSafeInteger(Number(groupId)) && Number(groupId) > 0;
  const group = useQuery({
    queryKey: ['group', groupId],
    queryFn: ({ signal }) => api<Group>(`/groups/${groupId}`, { signal }),
    enabled: valid,
    refetchInterval: (query) => (query.state.error ? false : 5000),
  });
  const me = useQuery({ queryKey: ['me'], queryFn: ({ signal }) => api<Me>('/me', { signal }) });
  const logout = useMutation({
    mutationFn: () => api('/auth/logout', { method: 'POST' }),
    onSuccess: () => {
      clearSession();
      queryClient.clear();
    },
  });
  const toggleTheme = () => {
    const next = !dark;
    document.documentElement.dataset.theme = next ? 'dark' : 'light';
    localStorage.setItem('loom.theme', next ? 'dark' : 'light');
    setDark(next);
  };
  const links = [
    { path: '', label: '工作台', icon: LayoutDashboard, permission: 'group.read' },
    { path: '/notebooks', label: '开发环境', icon: BookOpen, permission: 'notebook.read' },
    { path: '/storage', label: '工作目录', icon: Database, permission: 'workspace.read' },
    { path: '/pipelines', label: 'Pipeline', icon: GitBranch, permission: 'pipeline.read' },
    { path: '/models', label: '模型管理', icon: Boxes, permission: 'model.read' },
    { path: '/kserve-services', label: '推理服务', icon: Radio, permission: 'kserve.read' },
    { path: '/members', label: '成员与授权', icon: Users, permission: 'group.read' },
    { path: '/quota', label: '资源配额', icon: Gauge, permission: 'group.quota.read' },
    { path: '/settings', label: '空间设置', icon: Settings, permission: 'group.read' },
  ];
  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        跳转到主要内容
      </a>
      <aside className={`sidebar ${mobileOpen ? 'is-open' : ''}`} aria-label="主导航">
        <Link className="wordmark" to="/">
          <span className="logo-mark">L</span>loom
        </Link>
        <div className="workspace-label">
          <Database size={18} />
          <div>
            <span className="small muted">当前工作空间</span>
            <strong>{group.data?.displayName || group.data?.name || '加载中'}</strong>
          </div>
        </div>
        <Link className="switch-workspace" to="/">
          <ChevronLeft size={14} />
          切换工作空间
        </Link>
        <p className="nav-caption">WORKSPACE</p>
        <nav>
          {permissions.isPending && (
            <p className="small muted" role="status">
              正在读取可用模块…
            </p>
          )}
          {links
            .filter((link) => permissions.can(link.permission))
            .map(({ path, label, icon: Icon }) => (
              <NavLink
                key={path}
                to={`/g/${groupId}${path}`}
                end={path === ''}
                onClick={() => setMobileOpen(false)}
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
            hierarchy="tertiary"
            iconOnly
            iconLeading={mobileOpen ? <X size={20} /> : <Menu size={20} />}
            aria-label="切换导航"
            className="mobile-toggle"
            onClick={() => setMobileOpen(!mobileOpen)}
          />
          <div className="breadcrumb">
            <span>工作空间</span>
            <span>/</span>
            <strong>{group.data?.name ?? '…'}</strong>
          </div>
          <div className="topbar-actions">
            <AccountLinks />
            <Button
              hierarchy="tertiary"
              iconOnly
              iconLeading={dark ? <Sun size={18} /> : <Moon size={18} />}
              aria-label={dark ? '切换浅色主题' : '切换深色主题'}
              onClick={toggleTheme}
            />
            <span className="user-name">
              {me.data?.user.displayName || me.data?.user.username || '当前用户'}
            </span>
            {!trustedHeaderMode && (
              <Button
                hierarchy="tertiary"
                iconOnly
                iconLeading={<LogOut size={18} />}
                aria-label="退出登录"
                loading={logout.isPending}
                onClick={() => logout.mutate()}
              />
            )}
          </div>
        </header>
        <main id="main-content" className="content" tabIndex={-1}>
          {valid && permissions.isError && (
            <ErrorState
              error={permissions.error}
              retry={() => {
                void permissions.refetch();
              }}
            />
          )}
          {logout.isError && <ErrorState error={logout.error} />}
          {group.isError &&
            group.data &&
            !(group.error instanceof ApiError && [403, 404].includes(group.error.status)) && (
              <ErrorState
                error={group.error}
                retry={() => {
                  void group.refetch();
                }}
              />
            )}
          {me.isError && (
            <ErrorState
              error={me.error}
              retry={() => {
                void me.refetch();
              }}
            />
          )}
          {!valid ? (
            <NoGroup />
          ) : group.isPending ? (
            <Loading />
          ) : group.isError &&
            (!group.data ||
              (group.error instanceof ApiError && [403, 404].includes(group.error.status))) ? (
            <ErrorState
              error={group.error}
              retry={() => {
                void group.refetch();
              }}
            />
          ) : group.data ? (
            <>
              <div className="space-state">
                <StatusBadge status={group.data.status} />
                <span className="small muted">{group.data.name}</span>
              </div>
              <Outlet key={groupId} context={group.data} />
            </>
          ) : null}
        </main>
      </div>
    </div>
  );
}
function NoGroup() {
  return <ErrorState error={new Error('工作空间 ID 无效，请重新选择空间')} />;
}
