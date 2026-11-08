import { Link, useNavigate, useOutletContext, useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert } from '@atomus';
import type { Group } from '@/lib/types';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { OwnerTransferForm } from '@/features/ownership/owner-transfer-form';
import { accessLost } from '@/features/storage/policy';
import { notebookApi, notebookKeys } from './api';
import { canChangeRuntime, canTransferNotebook } from './policy';

export function TransferNotebookOwnerPage() {
  const { groupId = '', notebookId = '' } = useParams();
  const group = useOutletContext<Group>();
  const permissions = useGroupPermissions(groupId);
  const client = useQueryClient();
  const navigate = useNavigate();
  const query = useQuery({
    queryKey: notebookKeys.detail(groupId, notebookId),
    queryFn: ({ signal }) => notebookApi.detail(groupId, notebookId, signal),
    enabled: permissions.can('notebook.read'),
    staleTime: 0,
  });
  if (permissions.isPending) return <Loading />;
  if (accessLost(query.error) || accessLost(permissions.error))
    return <ErrorState error={query.error || permissions.error} />;
  if (!permissions.data || !permissions.data.permissions.includes('notebook.read'))
    return permissions.isError ? (
      <ErrorState
        error={permissions.error}
        retry={() => {
          void permissions.refetch();
        }}
      />
    ) : (
      <NoData title="没有读取开发环境的权限" />
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
    !permissions.data.permissions.includes('notebook.transfer') ||
    !(
      row.ownerUserId === permissions.data.userId ||
      permissions.data.permissions.includes('notebook.transfer.any')
    )
  )
    return <NoData title="没有交接此开发环境的权限" />;
  const allowed =
    !query.isError &&
    permissions.can('notebook.transfer') &&
    canTransferNotebook(row, permissions.data.userId, permissions.can('notebook.transfer.any')) &&
    group.status === 'ACTIVE' &&
    canChangeRuntime(row);
  return (
    <div className="stack spacious">
      <Link className="text-link" to={`/g/${groupId}/notebooks/${notebookId}`}>
        ← 返回环境详情
      </Link>
      <PageHeading
        eyebrow="NOTEBOOK HANDOVER"
        title={`交接 ${row.displayName || row.name}`}
        description="使用 ownerRevision 保护负责人变更，不替换运行配置 revision。"
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
      {!canChangeRuntime(row) && (
        <Alert color="warning" title="请先停止环境">
          需期望、观测状态均已停止且修订一致，不能只提交停止请求后立即交接。后端还会核对观测时效和待执行任务。
        </Alert>
      )}
      <OwnerTransferForm
        key={`${groupId}/${notebookId}`}
        name={row.name}
        ownerUserId={row.ownerUserId}
        revision={row.ownerRevision}
        allowed={allowed}
        notice="此操作不更改历史创建人、不移动空间或 PVC，也不交接目录数据。应先停止并交接 Notebook，再到工作目录页交接数据；完成前新负责人可能无法启动。交接后返回列表，避免继续读取旧权限下的内容。"
        submit={(body, key) => notebookApi.transfer(groupId, row.id, body, key)}
        onAccepted={(op, mounted) => {
          void client.invalidateQueries({ queryKey: notebookKeys.all(groupId) });
          if (mounted)
            void navigate(`/g/${groupId}/notebooks`, {
              replace: true,
              state: { operationId: op.operationId },
            });
        }}
      />
    </div>
  );
}
