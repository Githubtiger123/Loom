import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card } from '@atomus';
import { formatDate } from '@/lib/format';
import { ErrorState, Loading, NoData, StatusBadge } from '@/components/feedback';
import { notebookApi, notebookKeys } from '../api';
import type { NotebookPanelScope } from '../types';

export function NotebookDiagnosticsPanel({ groupId, id, identity }: NotebookPanelScope) {
  const query = useQuery({
    queryKey: notebookKeys.panel(groupId, id, 'diagnostics', identity),
    queryFn: ({ signal }) => notebookApi.diagnostics(groupId, id, signal),
    refetchInterval: (current) => (current.state.error ? false : 15_000),
    gcTime: 0,
  });
  return (
    <Card
      title="运行诊断"
      headerAction={
        <Button
          hierarchy="outline"
          size="sm"
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新诊断
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
          <div className="actions">
            <StatusBadge status={query.data.state} />
            <span className="small muted">{formatDate(query.data.observedAt)}</span>
          </div>
          {query.data.reason && (
            <Alert color="warning" title="诊断说明">
              {query.data.reason}
            </Alert>
          )}
          {!query.data.idleReclamationAvailable && (
            <Alert color="brand" title="空闲回收暂不可用">
              {query.data.idleReclamationReason}
            </Alert>
          )}
          {query.data.pods.length === 0 ? (
            <NoData title="当前没有 Pod" description="环境停止或尚未调度时可能没有 Pod。" />
          ) : (
            <div className="diagnostic-list">
              {query.data.pods.map((pod) => (
                <article key={pod.uid} className="diagnostic-row">
                  <div className="event-heading">
                    <strong className="mono">{pod.name}</strong>
                    <span>
                      {pod.ready ? '已就绪' : '未就绪'} · {pod.phase}
                    </span>
                  </div>
                  <p className="small muted">重启 {pod.restarts} 次</p>
                  {pod.reason && <p className="break-word">{pod.reason}</p>}
                </article>
              ))}
            </div>
          )}
        </div>
      )}
    </Card>
  );
}
