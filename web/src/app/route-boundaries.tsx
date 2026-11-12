import { Link, Navigate, useLocation, useRouteError } from 'react-router-dom';
import { ErrorState } from '@/components/feedback';

// Preserve deep links, filters, anchors and operation-navigation state from old URLs.
export function LegacyWorkspaceRedirect() {
  const location = useLocation();
  const pathname = location.pathname.startsWith('/g/')
    ? location.pathname.replace(/^\/g\//, '/workspaces/')
    : '/workspaces/new';
  return (
    <Navigate
      to={{ pathname, search: location.search, hash: location.hash }}
      state={location.state}
      replace
    />
  );
}

export function RouteError() {
  const error = useRouteError();
  return (
    <main className="standalone">
      <ErrorState error={error instanceof Error ? error : new Error('页面无法加载')} />
      <Link to="/">返回工作空间</Link>
    </main>
  );
}
