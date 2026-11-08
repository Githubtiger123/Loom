import { Link, useNavigate, useOutletContext, useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert } from '@atomus';
import type { Group } from '@/lib/types';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { OwnerTransferForm } from '@/features/ownership/owner-transfer-form';
import { storageApi, storageKeys } from './api';
import { refreshStorage } from './cache';
import { accessLost, ownsWorkspace, readyWorkspace } from './policy';

export function TransferWorkspaceOwnerPage() {
  const { groupId = '', workspaceId = '' } = useParams();
  const group = useOutletContext<Group>();
  const permissions = useGroupPermissions(groupId);
  const client = useQueryClient();
  const navigate = useNavigate();
  const query = useQuery({
    queryKey: storageKeys.detail(groupId, workspaceId),
    queryFn: ({ signal }) => storageApi.detail(groupId, workspaceId, signal),
    enabled: permissions.can('workspace.read'),
    staleTime: 0,
  });
  if (permissions.isPending) return <Loading />;
  if (accessLost(query.error) || accessLost(permissions.error))
    return <ErrorState error={query.error || permissions.error} />;
  if (!permissions.data || !permissions.data.permissions.includes('workspace.read'))
    return permissions.isError ? (
      <ErrorState
        error={permissions.error}
        retry={() => {
          void permissions.refetch();
        }}
      />
    ) : (
      <NoData title="没有工作目录读取权限" />
    );
  if (!query.isFetchedAfterMount && query.isFetching) return <Loading />;
  if (!query.data)
    return query.isPending ? (
      <Loading />
    ) : (
      <ErrorState
        error={query.error}
        retry={() => {
          void query.refetch();
        }}
      />
    );
  const row = query.data;
  if (
    !permissions.data.permissions.includes('workspace.transfer') ||
    !ownsWorkspace(
      row,
      permissions.data.userId,
      permissions.data.permissions.includes('workspace.manage.any'),
    )
  )
    return <NoData title="没有交接此工作目录的权限" />;
  const allowed =
    !query.isError &&
    permissions.can('workspace.transfer') &&
    ownsWorkspace(row, permissions.data.userId, permissions.can('workspace.manage.any')) &&
    group.status === 'ACTIVE' &&
    readyWorkspace(row);
  return (
    <div className="stack spacious">
      <Link className="text-link" to={`/workspaces/${groupId}/storage/${workspaceId}`}>
        ← 返回目录详情
      </Link>
      <PageHeading
        eyebrow="DATA HANDOVER"
        title={`交接目录 ${row.pvcName}`}
        description="目录数据归属单独变更，使用目录 revision 防止覆盖并发修改。"
      />
      {(query.isError || permissions.isError) && (
        <ErrorState
          error={query.error || permissions.error}
          retry={() => {
            void query.refetch();
            void permissions.refetch();
          }}
        />
      )}
      {!readyWorkspace(row) && (
        <Alert color="warning" title="目录身份尚未确认">
          只有可信观测确认的独占目录可交接，未知或共享目录不可操作。
        </Alert>
      )}
      <OwnerTransferForm
        key={`${groupId}/${workspaceId}`}
        name={row.pvcName}
        ownerUserId={row.ownerUserId}
        revision={row.revision}
        allowed={allowed}
        notice="目录中的文件会交给新负责人。所有关联 Notebook 必须先确认停止并交接给同一个目标用户，或者先安全解除关联。仅有目录管理权限不代表拥有其他人的 Notebook 操作权限；不自动停止、交接或删除关联实例。"
        submit={(body, key) => storageApi.transfer(groupId, row.id, body, key)}
        onAccepted={(op, mounted) => {
          refreshStorage(client, groupId);
          if (mounted)
            void navigate(`/workspaces/${groupId}/storage`, {
              replace: true,
              state: { operationId: op.operationId },
            });
        }}
      />
    </div>
  );
}
