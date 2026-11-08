import { useInfiniteQuery } from '@tanstack/react-query';
import { Alert, Button, Table } from '@atomus';
import { ErrorState, Loading, NoData, StatusBadge } from '@/components/feedback';
import { formatDate } from '@/lib/format';
import { groupApi, groupKeys } from '../api';
import { accessLabel } from '../policy';
import type { AccessState } from '../types';

export function AccessPanel({
  groupId,
  onOperation,
}: {
  groupId: string;
  onOperation: (id: string) => void;
}) {
  const query = useInfiniteQuery({
    queryKey: groupKeys.access(groupId),
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) => groupApi.access(groupId, pageParam, signal),
    getNextPageParam: (rows) => (rows.length === 50 ? rows.at(-1)?.userId : undefined),
    refetchInterval: (q) => (q.state.error ? false : 5000),
    gcTime: 0,
  });
  return (
    <div className="stack">
      <Alert color="info" title="含已移除用户的撤权记录">
        业务角色保存不等于集群访问已生效。请结合期望与已落实版本、最近观测、错误及操作单判断；这里不展示底层凭据。
      </Alert>
      <div className="actions">
        <Button
          hierarchy="outline"
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新授权状态
        </Button>
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
        <>
          <Table<AccessState>
            rows={query.data.pages.flat()}
            rowKey={(a) => String(a.userId)}
            caption="集群授权与撤权落实记录"
            empty={<NoData title="暂无授权落实记录" />}
            columns={[
              {
                key: 'username',
                header: '用户',
                render: (a) => a.username,
                supporting: (a) => `ID ${a.userId} · ${a.userStatus}`,
              },
              {
                key: 'levels',
                header: '期望 → 观测',
                render: (a) => `${accessLabel(a.desiredLevel)} → ${accessLabel(a.observedLevel)}`,
              },
              {
                key: 'status',
                header: '状态',
                render: (a) => <StatusBadge status={a.status} />,
                supporting: (a) => a.lastError,
              },
              {
                key: 'versions',
                header: '期望 / 已落实版本',
                render: (a) => `${a.desiredVersion} / ${a.appliedVersion}`,
              },
              { key: 'observedAt', header: '最近观测', render: (a) => formatDate(a.observedAt) },
              {
                key: 'operation',
                header: '操作单',
                render: (a) =>
                  a.operationId ? (
                    <Button hierarchy="tertiary" onClick={() => onOperation(a.operationId!)}>
                      查看进度
                    </Button>
                  ) : (
                    '—'
                  ),
              },
            ]}
          />
          {query.hasNextPage && (
            <Button
              hierarchy="outline"
              loading={query.isFetchingNextPage}
              onClick={() => {
                void query.fetchNextPage();
              }}
            >
              加载更多记录
            </Button>
          )}
        </>
      )}
    </div>
  );
}
