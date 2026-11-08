import { useState } from 'react';
import { Link, useOutletContext, useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Tabs } from '@atomus';
import { RefreshCw } from 'lucide-react';
import type { Group } from '@/lib/types';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { ApiError } from '@/lib/api';
import { ErrorState, Loading, PageHeading } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { notebookApi, notebookKeys } from './api';
import type { NotebookAction, NotebookDetail } from './types';
import { canChangeRuntime, canTransferNotebook, ownsNotebook } from './policy';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { NotebookActionDialog } from './components/notebook-action-dialog';
import { NotebookOpenButton } from './components/notebook-open-button';
import { NotebookRowActions } from './components/notebook-row-actions';
import { NotebookOverview } from './components/notebook-overview';
import { NotebookEventsPanel } from './components/notebook-events-panel';
import { NotebookDiagnosticsPanel } from './components/notebook-diagnostics-panel';
import { NotebookUsagePanel } from './components/notebook-usage-panel';
import { NotebookLogsPanel } from './components/notebook-logs-panel';
import { NotebookOperationCard } from './components/notebook-operation-card';

export function NotebookDetailPage() {
  const { groupId = '', notebookId = '' } = useParams();
  const query = useQuery({
    queryKey: notebookKeys.detail(groupId, notebookId),
    queryFn: ({ signal }) => notebookApi.detail(groupId, notebookId, signal),
    refetchInterval: (current) =>
      current.state.error instanceof ApiError &&
      [401, 403, 404].includes(current.state.error.status)
        ? false
        : 5000,
  });
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
  return (
    <NotebookDetailContent
      key={`${groupId}/${notebookId}`}
      groupId={groupId}
      row={query.data}
      refreshing={query.isFetching}
      onRefresh={() => {
        void query.refetch();
      }}
    />
  );
}

function NotebookDetailContent({
  groupId,
  row,
  refreshing,
  onRefresh,
}: {
  groupId: string;
  row: NotebookDetail;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const group = useOutletContext<Group>();
  const permissions = useGroupPermissions(groupId);
  const { operation, setOperation } = useOperationNavigation();
  const [intent, setIntent] = useState<{ row: NotebookDetail; action: NotebookAction }>();
  const [searchParams, setSearchParams] = useSearchParams();
  const can = (code: string) =>
    permissions.can(code) &&
    ownsNotebook(row, permissions.data?.userId, permissions.can('notebook.read.any'));
  const items = [
    { value: 'overview', label: '概览' },
    { value: 'usage', label: '资源用量' },
    ...(can('notebook.connect')
      ? [
          { value: 'events', label: '事件' },
          { value: 'diagnostics', label: '运行诊断' },
          { value: 'logs', label: '容器日志' },
        ]
      : []),
    { value: 'operation', label: '操作记录' },
  ];
  const requestedTab = searchParams.get('tab') || 'overview';
  const tab = items.some((item) => item.value === requestedTab) ? requestedTab : 'overview';
  const panelKey = `${row.id}/${row.revision}/${row.ownerRevision}/${tab}`;
  const panelScope = {
    groupId,
    id: String(row.id),
    identity: `${row.revision}/${row.ownerRevision}`,
  };
  return (
    <div className="stack spacious">
      <Link className="text-link" to={`/workspaces/${groupId}/notebooks`}>
        ← 返回开发环境
      </Link>
      <PageHeading
        eyebrow="DEVELOPMENT ENVIRONMENT"
        title={row.displayName || row.name}
        description={row.description || row.name}
        actions={
          can('notebook.connect') && (
            <NotebookOpenButton
              key={`${row.id}/${row.ownerRevision}`}
              groupId={groupId}
              notebookId={row.id}
              primary
              disabled={row.status !== 'RUNNING' || group.status !== 'ACTIVE'}
            />
          )
        }
      />
      {permissions.isError && (
        <ErrorState
          error={permissions.error}
          retry={() => {
            void permissions.refetch();
          }}
        />
      )}
      <div className="detail-action-bar">
        <NotebookRowActions
          row={row}
          groupId={groupId}
          activeGroup={group.status === 'ACTIVE'}
          can={can}
          onAction={(action) => setIntent({ row, action })}
          onOperation={setOperation}
        />
        {permissions.can('notebook.transfer') &&
          canTransferNotebook(
            row,
            permissions.data?.userId,
            permissions.can('notebook.transfer.any'),
          ) &&
          canChangeRuntime(row) &&
          group.status === 'ACTIVE' && (
            <Link className="text-link" to={`/workspaces/${groupId}/notebooks/${row.id}/transfer`}>
              交接实例负责人
            </Link>
          )}
        {permissions.can('workspace.read') && row.workspaceId > 0 && (
          <Link className="text-link" to={`/workspaces/${groupId}/storage/${row.workspaceId}`}>
            管理工作目录
          </Link>
        )}
        <Button
          hierarchy="outline"
          size="sm"
          loading={refreshing}
          iconLeading={<RefreshCw size={15} />}
          onClick={onRefresh}
        >
          刷新状态
        </Button>
      </div>
      {row.revision !== row.appliedRevision && (
        <Alert color="brand" title="配置正在落实">
          期望修订 r{row.revision}，已落实 r{row.appliedRevision}。请等待集群同步，暂不要重复提交。
        </Alert>
      )}
      <Tabs
        aria-label="开发环境详情"
        items={items}
        value={tab}
        onChange={(value) => {
          const next = new URLSearchParams(searchParams);
          next.set('tab', value);
          setSearchParams(next, { replace: true });
        }}
      />
      <section
        role="tabpanel"
        aria-label={items.find((item) => item.value === tab)?.label}
        key={panelKey}
      >
        {tab === 'overview' && <NotebookOverview row={row} />}
        {tab === 'usage' && <NotebookUsagePanel {...panelScope} />}
        {tab === 'events' && <NotebookEventsPanel {...panelScope} />}
        {tab === 'diagnostics' && <NotebookDiagnosticsPanel {...panelScope} />}
        {tab === 'logs' && <NotebookLogsPanel {...panelScope} />}
        {tab === 'operation' &&
          (row.operationId ? (
            <NotebookOperationCard groupId={groupId} row={row} can={can} />
          ) : (
            <Alert color="gray" title="暂无操作记录">
              此环境尚未关联操作单。
            </Alert>
          ))}
      </section>
      {intent && (
        <NotebookActionDialog
          key={`${intent.row.id}/${intent.row.revision}/${intent.action}`}
          groupId={groupId}
          row={intent.row}
          action={intent.action}
          onClose={() => setIntent(undefined)}
          onAccepted={(id) => {
            setIntent(undefined);
            setOperation(id);
          }}
        />
      )}
      {operation && (
        <OperationDialog groupId={groupId} id={operation} onClose={() => setOperation(undefined)} />
      )}
    </div>
  );
}
