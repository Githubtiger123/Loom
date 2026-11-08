import { useState } from 'react';
import { Link, useOutletContext, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button } from '@atomus';
import type { Group } from '@/lib/types';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { storageApi, storageKeys } from './api';
import { accessLost, canDisposeWorkspace, ownsWorkspace, readyWorkspace } from './policy';
import type { Workspace } from './types';
import { WorkspaceOverview } from './components/workspace-overview';
import { WorkspaceReferences } from './components/workspace-references';
import { WorkspaceDeleteDialog } from './components/workspace-delete-dialog';
import { WorkspaceLinkDialog } from './components/workspace-link-dialog';

type Intent =
  | { row: Workspace; action: 'attach' | 'detach'; notebookId?: number }
  | { row: Workspace; action: 'delete' };
export function WorkspaceDetailPage() {
  const { groupId = '', workspaceId = '' } = useParams();
  const permissions = useGroupPermissions(groupId);
  const query = useQuery({
    queryKey: storageKeys.detail(groupId, workspaceId),
    queryFn: ({ signal }) => storageApi.detail(groupId, workspaceId, signal),
    enabled: permissions.can('workspace.read'),
    refetchInterval: (q) => (q.state.error ? false : 10000),
  });
  if (permissions.isPending) return <Loading />;
  if ((!permissions.data && permissions.isError) || accessLost(permissions.error))
    return (
      <ErrorState
        error={permissions.error}
        retry={() => {
          void permissions.refetch();
        }}
      />
    );
  if (!permissions.data?.permissions.includes('workspace.read'))
    return <NoData title="没有工作目录读取权限" />;
  if (!query.data && query.isPending) return <Loading />;
  if ((!query.data && query.isError) || accessLost(query.error))
    return (
      <ErrorState
        error={query.error}
        retry={() => {
          void query.refetch();
        }}
      />
    );
  if (!query.data) return <Loading />;
  return (
    <WorkspaceDetailContent
      key={`${groupId}/${workspaceId}`}
      groupId={groupId}
      row={query.data}
      available={!query.isError && !permissions.isError}
      error={query.error || permissions.error}
      refreshing={query.isFetching}
      onRefresh={() => {
        void query.refetch();
        void permissions.refetch();
      }}
    />
  );
}
function WorkspaceDetailContent({
  groupId,
  row,
  available,
  error,
  refreshing,
  onRefresh,
}: {
  groupId: string;
  row: Workspace;
  available: boolean;
  error: unknown;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const group = useOutletContext<Group>();
  const permissions = useGroupPermissions(groupId);
  const { operation, setOperation } = useOperationNavigation();
  const [intent, setIntent] = useState<Intent>();
  const can = (permission: string) =>
    available &&
    permissions.can(permission) &&
    ownsWorkspace(row, permissions.data?.userId, permissions.can('workspace.manage.any'));
  const mutable = !row.deletedAt && !['DELETING', 'DELETED'].includes(row.status);
  const canLink =
    can('workspace.attach') &&
    permissions.can('notebook.read') &&
    permissions.can('notebook.update') &&
    mutable;
  const accepted = (id: string) => {
    setIntent(undefined);
    setOperation(id);
  };
  return (
    <div className="stack spacious">
      <Link className="text-link" to={`/workspaces/${groupId}/storage`}>
        ← 返回工作目录
      </Link>
      <PageHeading
        eyebrow="WORKSPACE ASSET"
        title={row.pvcName}
        description="目录资产的归属、数据保护和实例引用独立于 Notebook 生命周期。"
      />
      {!!error && <ErrorState error={error} retry={onRefresh} />}
      {!available && (
        <p className="small muted">当前无法确认最新状态，暂时关闭写操作；未提交的确认内容保留。</p>
      )}
      <div className="actions">
        {canLink && readyWorkspace(row) && !row.notebookIds.length && group.status === 'ACTIVE' && (
          <Button hierarchy="primary" onClick={() => setIntent({ row, action: 'attach' })}>
            关联已有环境
          </Button>
        )}
        {can('workspace.transfer') && readyWorkspace(row) && group.status === 'ACTIVE' && (
          <Link className="text-link" to={`/workspaces/${groupId}/storage/${row.id}/transfer`}>
            交接数据负责人
          </Link>
        )}
        {can('workspace.delete') && readyWorkspace(row) && canDisposeWorkspace(group.status) && (
          <Button
            hierarchy="outline"
            disabled={!!row.notebookIds.length}
            onClick={() => setIntent({ row, action: 'delete' })}
          >
            删除目录数据
          </Button>
        )}
        <Button hierarchy="outline" loading={refreshing} onClick={onRefresh}>
          刷新状态
        </Button>
      </div>
      {!readyWorkspace(row) && (
        <Alert color="warning" title="目录暂不可交接或删除">
          仅支持可信观测确认的独占目录；未知、共享、删除中的目录不开放此类操作。
        </Alert>
      )}
      <WorkspaceOverview row={row} />
      <WorkspaceReferences
        ids={row.notebookIds}
        groupId={groupId}
        canReadNotebook={permissions.can('notebook.read')}
        canDetach={canLink && canDisposeWorkspace(group.status)}
        onDetach={(notebookId) => setIntent({ row, action: 'detach', notebookId })}
      />
      {intent?.action === 'delete' ? (
        <WorkspaceDeleteDialog
          key={`${intent.row.id}/${intent.row.revision}/delete`}
          groupId={groupId}
          row={intent.row}
          available={available}
          onClose={() => setIntent(undefined)}
          onAccepted={accepted}
        />
      ) : (
        intent && (
          <WorkspaceLinkDialog
            key={`${intent.row.id}/${intent.row.revision}/${intent.action}/${intent.notebookId ?? ''}`}
            groupId={groupId}
            row={intent.row}
            available={available}
            action={intent.action}
            notebookId={intent.notebookId}
            onClose={() => setIntent(undefined)}
            onAccepted={accepted}
          />
        )
      )}
      {operation && (
        <OperationDialog id={operation} groupId={groupId} onClose={() => setOperation(undefined)} />
      )}
    </div>
  );
}
