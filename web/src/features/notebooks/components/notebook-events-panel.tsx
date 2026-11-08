import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card } from '@atomus';
import { formatDate } from '@/lib/format';
import { ErrorState, Loading, NoData } from '@/components/feedback';
import { notebookApi, notebookKeys } from '../api';
import type { NotebookPanelScope } from '../types';

export function NotebookEventsPanel({ groupId, id, identity }: NotebookPanelScope) {
  const query = useQuery({
    queryKey: notebookKeys.panel(groupId, id, 'events', identity),
    queryFn: ({ signal }) => notebookApi.events(groupId, id, signal),
    refetchInterval: (current) => (current.state.error ? false : 15_000),
    gcTime: 0,
  });
  return (
    <Card
      title="资源事件"
      supportingText="读取当前环境及其已验证归属资源的事件，每 15 秒刷新。"
      headerAction={
        <Button
          hierarchy="outline"
          size="sm"
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新事件
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
          <span className="small muted">查询时间：{formatDate(query.data.observedAt)}</span>
          {query.data.truncated && (
            <Alert color="warning" title="事件已截断">
              只显示有界事件集合，不保证包含全部最新事件。
            </Alert>
          )}
          {query.data.items.length === 0 ? (
            <NoData title="暂无资源事件" />
          ) : (
            <ol className="event-list">
              {[...query.data.items]
                .sort((a, b) => Date.parse(b.at) - Date.parse(a.at))
                .map((event, index) => (
                  <li key={`${event.uid}/${event.at}/${index}`}>
                    <div className="event-heading">
                      <strong>{event.reason || event.type}</strong>
                      <span className="small muted">
                        {formatDate(event.at)} · {event.count} 次
                      </span>
                    </div>
                    <p className="break-word">{event.message}</p>
                    <span className="small muted">
                      {event.kind} / {event.name} · {event.type}
                    </span>
                  </li>
                ))}
            </ol>
          )}
        </div>
      )}
    </Card>
  );
}
