import { Outlet, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import {
  BookOpen,
  Boxes,
  Database,
  GitBranch,
  LayoutDashboard,
  Radio,
  Users,
  Settings,
  Gauge,
} from 'lucide-react';
import { api, ApiError } from '@/lib/api';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import type { Group } from '@/lib/types';
import { ErrorState, Loading, StatusBadge } from '@/components/feedback';
import { AppFrame } from './app-frame';

export function Shell() {
  const { groupId = '' } = useParams();
  const permissions = useGroupPermissions(groupId);
  const valid =
    /^\d+$/.test(groupId) && Number.isSafeInteger(Number(groupId)) && Number(groupId) > 0;
  const group = useQuery({
    queryKey: ['group', groupId],
    queryFn: ({ signal }) => api<Group>(`/groups/${groupId}`, { signal }),
    enabled: valid,
    refetchInterval: (query) => (query.state.error ? false : 5000),
  });
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
  const missing = group.error instanceof ApiError && [401, 403, 404].includes(group.error.status);
  return (
    <AppFrame
      context={{
        label: '当前工作空间',
        title: group.data?.displayName || group.data?.name || '工作空间',
        icon: <Database size={18} />,
        backLabel: '切换工作空间',
      }}
      caption="WORKSPACE"
      items={links
        .filter((link) => permissions.can(link.permission))
        .map(({ path, label, icon }) => ({
          to: `/workspaces/${groupId}${path}`,
          label,
          icon,
          end: path === '',
        }))}
      navigationPending={permissions.isPending}
      breadcrumb={
        <>
          <span>工作空间</span>
          <span>/</span>
          <strong>{group.data?.name ?? '…'}</strong>
        </>
      }
    >
      {valid && permissions.isError && (
        <ErrorState
          error={permissions.error}
          retry={() => {
            void permissions.refetch();
          }}
        />
      )}
      {group.isError && group.data && !missing && (
        <ErrorState
          error={group.error}
          retry={() => {
            void group.refetch();
          }}
        />
      )}
      {!valid ? (
        <ErrorState error={new Error('工作空间 ID 无效，请重新选择空间')} />
      ) : group.isPending ? (
        <Loading />
      ) : group.isError && (!group.data || missing) ? (
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
    </AppFrame>
  );
}
