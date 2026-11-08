import { useState } from 'react';
import { useParams } from 'react-router-dom';
import { useInfiniteQuery } from '@tanstack/react-query';
import { Button, Input } from '@atomus';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { storageApi, storageKeys } from './api';
import { WorkspaceTable } from './components/workspace-table';

export function StoragePage() {
  const { groupId = '' } = useParams();
  const permissions = useGroupPermissions(groupId);
  const [search, setSearch] = useState('');
  const { operation, setOperation } = useOperationNavigation();
  const query = useInfiniteQuery({
    queryKey: storageKeys.list(groupId),
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) => storageApi.list(groupId, pageParam, signal),
    getNextPageParam: (page) => page.nextAfter ?? undefined,
    enabled: permissions.can('workspace.read'),
    refetchInterval: (q) => (q.state.error ? false : 10000),
  });
  const rows = query.data?.pages.flatMap((page) => page.items) ?? [];
  const filtered = rows.filter((row) =>
    `${row.pvcName} ${row.id} ${row.ownerUserId ?? ''}`
      .toLowerCase()
      .includes(search.trim().toLowerCase()),
  );
  return (
    <div className="stack spacious">
      <PageHeading
        eyebrow="NOTEBOOK STORAGE"
        title="工作目录"
        description="独立管理 Notebook 数据资产；停止或删除实例不会自动删除这里的数据。"
      />
      {permissions.isPending ? (
        <Loading />
      ) : permissions.isError ? (
        <ErrorState
          error={permissions.error}
          retry={() => {
            void permissions.refetch();
          }}
        />
      ) : !permissions.can('workspace.read') ? (
        <NoData title="没有工作目录读取权限" />
      ) : (
        <>
          <div className="toolbar">
            <Input
              label="筛选已加载目录"
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="PVC 名称、目录 ID 或负责人 ID"
            />
            <Button
              hierarchy="outline"
              loading={query.isFetching}
              onClick={() => {
                void query.refetch();
              }}
            >
              刷新
            </Button>
          </div>
          <p className="small muted">
            已加载 {rows.length} 个目录，当前匹配 {filtered.length}{' '}
            个。筛选仅覆盖已加载记录；可见范围由后端权限决定。
          </p>
          {query.isPending ? (
            <Loading />
          ) : query.isError ? (
            <ErrorState
              error={query.error}
              retry={() => {
                void query.refetch();
              }}
            />
          ) : search.trim() && !filtered.length ? (
            <NoData
              title="已加载目录中没有匹配项"
              description="可继续加载后续记录，或调整筛选条件。"
            />
          ) : (
            <WorkspaceTable rows={filtered} groupId={groupId} />
          )}
          {query.hasNextPage && !query.isError && (
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
        </>
      )}
      {operation && (
        <OperationDialog groupId={groupId} id={operation} onClose={() => setOperation(undefined)} />
      )}
    </div>
  );
}
