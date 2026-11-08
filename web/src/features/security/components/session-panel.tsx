import { useState } from 'react';
import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Table } from '@atomus';
import { ErrorState, Loading, NoData } from '@/components/feedback';
import { formatDate } from '@/lib/format';
import { sessionApi, sessionKeys } from '../api';
import type { DeviceSession } from '../types';
import { RevokeSessionDialog } from './revoke-session-dialog';
export function SessionPanel({ target, allowed = true }: { target?: string; allowed?: boolean }) {
  const client = useQueryClient();
  const [selected, setSelected] = useState<DeviceSession>();
  const query = useInfiniteQuery({
    queryKey: sessionKeys(target),
    initialPageParam: '',
    queryFn: ({ pageParam, signal }) => sessionApi.list(pageParam, target, signal),
    getNextPageParam: (page) => page.nextAfter || undefined,
    enabled: allowed,
    gcTime: 0,
    refetchInterval: (q) => (q.state.error ? false : 30_000),
  });
  return (
    <div className="stack">
      <Alert color="info" title="只展示有效的本地设备会话">
        会话编号不是 Token，不包含密码或凭据摘要。不展示后端未采集的
        IP、浏览器或设备名称。撤销后该设备需要重新登录，已建立连接的清理取决于访问组件。
      </Alert>
      <div className="actions">
        <Button
          hierarchy="outline"
          disabled={!allowed}
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新会话
        </Button>
      </div>
      {!allowed ? (
        <p className="muted">权限尚未确认，暂不展示会话数据。</p>
      ) : query.isPending ? (
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
          <Table<DeviceSession>
            rows={query.data.pages.flatMap((p) => p.items)}
            rowKey={(s) => s.familyId}
            caption="有效设备会话"
            empty={<NoData title="暂无有效会话" />}
            columns={[
              {
                key: 'familyId',
                header: '会话编号',
                render: (s) => (
                  <span className="mono break-word">
                    {s.familyId}
                    {s.current ? ' · 当前设备' : ''}
                  </span>
                ),
              },
              { key: 'createdAt', header: '建立时间', render: (s) => formatDate(s.createdAt) },
              {
                key: 'lastRefreshedAt',
                header: '最近刷新',
                render: (s) => formatDate(s.lastRefreshedAt),
              },
              { key: 'expiresAt', header: '过期时间', render: (s) => formatDate(s.expiresAt) },
              {
                key: 'actions',
                header: '操作',
                render: (s) => (
                  <Button hierarchy="tertiary" onClick={() => setSelected(s)}>
                    撤销{s.current ? '当前设备' : '会话'}
                  </Button>
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
              加载更多会话
            </Button>
          )}
        </>
      )}
      {selected && (
        <RevokeSessionDialog
          session={selected}
          target={target}
          allowed={allowed}
          onClose={() => setSelected(undefined)}
          onRevoked={() => {
            setSelected(undefined);
            void client.invalidateQueries({ queryKey: sessionKeys(target) });
          }}
        />
      )}
    </div>
  );
}
