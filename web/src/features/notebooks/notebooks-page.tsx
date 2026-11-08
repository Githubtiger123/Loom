import { useEffect, useState } from 'react';
import { Link, useNavigate, useOutletContext, useParams } from 'react-router-dom';
import { useInfiniteQuery } from '@tanstack/react-query';
import { Alert, Button } from '@atomus';
import { Plus } from 'lucide-react';
import type { Group, Notebook } from '@/lib/types';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { ApiError } from '@/lib/api';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { notebookApi, notebookKeys } from './api';
import type { NotebookAction, NotebookListFilters } from './types';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { NotebookActionDialog } from './components/notebook-action-dialog';
import { NotebookTable } from './components/notebook-table';
import { NotebookToolbar } from './components/notebook-toolbar';

export function NotebooksPage() {
  const { groupId = '' } = useParams();
  const group = useOutletContext<Group>();
  const navigate = useNavigate();
  const permissions = useGroupPermissions(groupId);
  const [filters, setFilters] = useState<NotebookListFilters>({
    search: '',
    scope: 'mine',
    status: '',
    serverType: '',
  });
  const [search, setSearch] = useState('');
  const [intent, setIntent] = useState<{ row: Notebook; action: NotebookAction }>();
  const { operation, setOperation } = useOperationNavigation();
  useEffect(() => {
    const timer = window.setTimeout(() => setSearch(filters.search.trim()), 300);
    return () => window.clearTimeout(timer);
  }, [filters.search]);
  const effectiveFilters = {
    ...filters,
    search,
    scope: permissions.can('notebook.read.any') ? filters.scope : 'mine',
  };
  const query = useInfiniteQuery({
    queryKey: notebookKeys.list(groupId, effectiveFilters),
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) =>
      notebookApi.list(groupId, effectiveFilters, pageParam, signal),
    getNextPageParam: (page) => page.nextAfter ?? undefined,
    enabled: permissions.can('notebook.read'),
    refetchInterval: (current) =>
      current.state.error instanceof ApiError &&
      [401, 403, 404].includes(current.state.error.status)
        ? false
        : 5000,
  });
  const rows = query.data?.pages.flatMap((page) => page.items) ?? [];
  return (
    <div className="stack spacious">
      <PageHeading
        eyebrow="DEVELOPMENT"
        title="开发环境"
        description="从受管镜像启动开发空间，独立管理计算资源与工作目录。"
        actions={
          <>
            {permissions.can('workspace.read') && (
              <Link className="text-link" to={`/g/${groupId}/storage`}>
                工作目录资产
              </Link>
            )}
            {permissions.can('notebook.create') && (
              <Button
                hierarchy="primary"
                disabled={group.status !== 'ACTIVE'}
                iconLeading={<Plus size={16} />}
                onClick={() => {
                  void navigate(`/g/${groupId}/notebooks/new`);
                }}
              >
                新建环境
              </Button>
            )}
          </>
        }
      />
      {permissions.isPending ? (
        <Loading label="正在获取空间权限" />
      ) : permissions.isError ? (
        <ErrorState
          error={permissions.error}
          retry={() => {
            void permissions.refetch();
          }}
        />
      ) : !permissions.can('notebook.read') ? (
        <NoData title="没有开发环境访问权限" />
      ) : (
        <>
          <NotebookToolbar
            filters={{ ...filters, scope: effectiveFilters.scope }}
            onChange={setFilters}
            canReadAny={permissions.can('notebook.read.any')}
            refreshing={query.isFetching}
            onRefresh={() => {
              void query.refetch();
            }}
          />
          <div className="list-meta">
            <span>{query.data ? `已加载 ${rows.length} 个环境` : '正在读取环境'}</span>
            <span>每 5 秒同步 · 状态以集群观测为准</span>
          </div>
          {query.isPending ? (
            <Loading />
          ) : query.isError ? (
            <ErrorState
              error={query.error}
              retry={() => {
                void query.refetch();
              }}
            />
          ) : (
            <NotebookTable
              rows={rows}
              groupId={groupId}
              activeGroup={group.status === 'ACTIVE'}
              can={permissions.can}
              userId={permissions.data?.userId}
              filtered={!!(filters.search || filters.status || filters.serverType)}
              onAction={(row, action) => setIntent({ row, action })}
              onOperation={setOperation}
            />
          )}
          {query.hasNextPage && (
            <Button
              hierarchy="outline"
              loading={query.isFetchingNextPage}
              onClick={() => {
                void query.fetchNextPage();
              }}
            >
              加载更多
            </Button>
          )}
          {group.status !== 'ACTIVE' && (
            <Alert color="warning" title="当前空间不可创建或启动环境">
              已有实例仍可按权限停止或删除。
            </Alert>
          )}
        </>
      )}
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
        <OperationDialog id={operation} groupId={groupId} onClose={() => setOperation(undefined)} />
      )}
    </div>
  );
}
