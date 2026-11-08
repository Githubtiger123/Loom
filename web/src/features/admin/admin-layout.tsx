import { Link, NavLink, Outlet, useSearchParams } from 'react-router-dom';
import { Alert } from '@atomus';
import { useGlobalPermissions } from '@/lib/use-global-permissions';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { OperationDialog } from '@/components/operation-dialog';
import { AccountLinks } from '@/components/account-links';
import { adminSections } from './policy';
import { validId } from './form-schema';
export function AdminLayout() {
  const me = useGlobalPermissions();
  const { operation, setOperation } = useOperationNavigation();
  return (
    <main className="management-layout stack spacious">
      <div className="list-meta">
        <Link className="wordmark" to="/">
          <span className="logo-mark">L</span>loom <span className="small muted">平台管理</span>
        </Link>
        <AccountLinks />
      </div>
      <nav className="management-nav" aria-label="平台管理导航">
        {adminSections
          .filter((s) => me.can(s.permission))
          .map((s) => (
            <NavLink key={s.path} to={s.path} end={s.path === '/admin/users'}>
              {s.label}
            </NavLink>
          ))}
      </nav>
      <Outlet />
      {operation && (
        <OperationDialog id={operation} groupId="" onClose={() => setOperation(undefined)} />
      )}
    </main>
  );
}
export function AdminIndex() {
  const query = useGlobalPermissions();
  const [params] = useSearchParams();
  const createdId = params.get('createdUserId') ?? '';
  if (query.isPending) return <Loading />;
  if (query.isError)
    return (
      <ErrorState
        error={query.error}
        retry={() => {
          void query.refetch();
        }}
      />
    );
  const sections = adminSections.filter((s) => query.can(s.permission));
  return sections.length ? (
    <div className="stack spacious">
      {validId(createdId) && (
        <Alert color="info" title={`本次登记的用户 ID：${createdId}`}>
          此处仅作导航提示，请以关联操作单为准确认结果。若是本地账号，还需具备相应权限的管理人员设置初始密码；账号没有自动加入空间。
        </Alert>
      )}
      <PageHeading
        eyebrow="PLATFORM ADMINISTRATION"
        title="平台管理"
        description="用户生命周期、授权定义和安全审计；管理操作始终由后端逐请求授权。"
      />
      <div className="management-nav">
        {sections.map((section) => (
          <Link key={section.path} to={section.path}>
            {section.label}
          </Link>
        ))}
      </div>
      <Link className="text-link" to="/">
        返回工作空间
      </Link>
    </div>
  ) : (
    <NoData title="当前没有平台管理权限" />
  );
}
