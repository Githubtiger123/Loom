import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card } from '@atomus';
import { formatBytes, formatDate } from '@/lib/format';
import { ErrorState, Loading } from '@/components/feedback';
import { notebookApi, notebookKeys } from '../api';
import type { NotebookPanelScope } from '../types';

export function NotebookUsagePanel({ groupId, id, identity }: NotebookPanelScope) {
  const query = useQuery({
    queryKey: notebookKeys.panel(groupId, id, 'usage', identity),
    queryFn: ({ signal }) => notebookApi.usage(groupId, id, signal),
    refetchInterval: (current) => (current.state.error ? false : 15_000),
  });
  return (
    <Card
      title="实时资源用量"
      supportingText="每 15 秒读取一次指标；配置额度与实际用量不同。"
      headerAction={
        <Button
          hierarchy="outline"
          size="sm"
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新用量
        </Button>
      }
    >
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
        <div className="stack">
          {!query.data.available ? (
            <Alert color="warning" title="暂无可信用量数据">
              {query.data.reason || '指标服务尚未提供有效样本。'} 不会用 0 替代缺失数据。
            </Alert>
          ) : (
            <dl className="details-grid">
              <dt>CPU 实际用量</dt>
              <dd>{query.data.cpuMilli == null ? '—' : `${query.data.cpuMilli / 1000} 核`}</dd>
              <dt>内存实际用量</dt>
              <dd>{formatBytes(query.data.memoryBytes)}</dd>
              <dt>指标时间</dt>
              <dd>{formatDate(query.data.timestamp)}</dd>
              <dt>采样窗口</dt>
              <dd>{query.data.window || '—'}</dd>
            </dl>
          )}
          <p className="small muted">
            GPU 实际用量：{query.data.gpuReason || '当前接口未提供'}；查询时间：
            {formatDate(query.data.observedAt)}
          </p>
        </div>
      )}
    </Card>
  );
}
