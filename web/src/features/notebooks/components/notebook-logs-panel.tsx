import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Toggle, Select } from '@atomus';
import { formatDate } from '@/lib/format';
import { ErrorState, Loading, NoData } from '@/components/feedback';
import { notebookApi, notebookKeys } from '../api';
import type { NotebookPanelScope } from '../types';

export function NotebookLogsPanel({ groupId, id, identity }: NotebookPanelScope) {
  const [tailLines, setTailLines] = useState('200');
  const [previous, setPrevious] = useState(false);
  const [polling, setPolling] = useState(false);
  const query = useQuery({
    queryKey: notebookKeys.panel(groupId, id, 'logs', { tailLines, previous, identity }),
    queryFn: ({ signal }) => notebookApi.logs(groupId, id, { tailLines, previous }, signal),
    staleTime: 0,
    gcTime: 0,
    refetchOnWindowFocus: false,
    refetchInterval: (current) => (polling && !current.state.error ? 5000 : false),
  });
  return (
    <Card
      title="容器日志"
      supportingText="主容器最近一小时的有界日志快照，单次最多 256 KiB；不是流式日志。"
      headerAction={
        <Button
          hierarchy="outline"
          size="sm"
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          读取日志
        </Button>
      }
    >
      <div className="stack">
        <div className="actions">
          <Select
            label="最近行数"
            size="sm"
            value={tailLines}
            onChange={setTailLines}
            options={['100', '200', '500', '1000'].map((value) => ({
              value,
              label: `${value} 行`,
            }))}
          />
          <Toggle
            label="上一次容器日志"
            checked={previous}
            onChange={(event) => setPrevious(event.target.checked)}
          />
          <Toggle
            label="每 5 秒刷新"
            checked={polling}
            onChange={(event) => setPolling(event.target.checked)}
          />
        </div>
        {query.isPending ? (
          <Loading label="正在读取日志" />
        ) : query.isError ? (
          <ErrorState
            error={query.error}
            retry={() => {
              void query.refetch();
            }}
          />
        ) : (
          <>
            <span className="small muted">
              {query.data.pod} / {query.data.container} · {formatDate(query.data.observedAt)}
            </span>
            {query.data.truncated && (
              <Alert color="warning" title="日志已截断">
                已达到本次快照大小或行数限制。
              </Alert>
            )}
            {query.data.text ? (
              <pre className="log-viewer" tabIndex={0} aria-label="Notebook 容器日志">
                {query.data.text}
              </pre>
            ) : (
              <NoData title="当前范围内暂无日志" description="可以切换行数或查看上一次容器日志。" />
            )}
          </>
        )}
      </div>
    </Card>
  );
}
