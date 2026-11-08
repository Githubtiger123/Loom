import { Link, Outlet, useLocation, useSearchParams } from 'react-router-dom';
import { Alert, Card } from '@atomus';
import { LayoutDashboard, ShieldCheck, Users, UserPlus, KeyRound, ScrollText } from 'lucide-react';
import { useGlobalPermissions } from '@/lib/use-global-permissions';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { OperationDialog } from '@/components/operation-dialog';
import { AppFrame } from '@/app/app-frame';
import { adminSections } from './policy';
import { validId } from './form-schema';
export function AdminLayout() {
  const me = useGlobalPermissions();
  const { pathname } = useLocation();
  const { operation, setOperation } = useOperationNavigation();
  const currentPath = pathname.replace(/\/+$/, '');
  const section =
    adminSections.find((s) => currentPath === s.path) ??
    [...adminSections].reverse().find((s) => currentPath.startsWith(`${s.path}/`));
  const icons = {
    'user.read': Users,
    'user.manage': UserPlus,
    'role.manage': KeyRound,
    'audit.read': ScrollText,
  };
  return (
    <AppFrame
      context={{
        label: '管理范围',
        title: '平台管理',
        icon: <ShieldCheck size={18} />,
        backLabel: '返回工作空间',
      }}
      caption="ADMINISTRATION"
      navigationPending={me.isPending}
      items={[
        { to: '/admin', label: '管理概览', icon: LayoutDashboard, end: true },
        ...adminSections
          .filter((s) => me.can(s.permission))
          .map((s) => ({
            to: s.path,
            label: s.label,
            icon: icons[s.permission as keyof typeof icons],
            active: section?.path === s.path,
          })),
      ]}
      breadcrumb={
        <>
          <Link to="/admin">平台管理</Link>
          <span>/</span>
          <strong>{section?.label ?? '管理概览'}</strong>
        </>
      }
    >
      <Outlet />
      {operation && (
        <OperationDialog id={operation} groupId="" onClose={() => setOperation(undefined)} />
      )}
    </AppFrame>
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
      <div className="module-grid">
        {sections.map((section) => (
          <Link key={section.path} className="management-entry" to={section.path}>
            <Card>
              <h2>{section.label}</h2>
              <p className="small muted">进入管理 →</p>
            </Card>
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
