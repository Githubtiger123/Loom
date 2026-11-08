import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card } from '@atomus';
import { api, ApiError } from '@/lib/api';
import type { Notebook, Operation } from '@/lib/types';
import { useCommandKey } from '@/lib/use-command-key';
import { ErrorState, Loading, StatusBadge } from '@/components/feedback';
import { notebookApi, notebookKeys } from '../api';

export function NotebookOperationCard({
  groupId,
  row,
  can,
}: {
  groupId: string;
  row: Notebook;
  can: (code: string) => boolean;
}) {
  if (!row.operationId) return null;
  return (
    <LatestOperation
      key={row.operationId}
      groupId={groupId}
      row={row}
      id={row.operationId}
      can={can}
    />
  );
}

function LatestOperation({
  groupId,
  row,
  id,
  can,
}: {
  groupId: string;
  row: Notebook;
  id: string;
  can: (code: string) => boolean;
}) {
  const client = useQueryClient();
  const commandKey = useCommandKey();
  const [retryIntent, setRetryIntent] = useState(0);
  const query = useQuery({
    queryKey: ['operation', groupId, id],
    queryFn: ({ signal }) => api<Operation & { action: string }>(`/operations/${id}`, { signal }),
    refetchInterval: (current) =>
      ['PENDING', 'RUNNING', 'RETRYING'].includes(current.state.data?.status ?? 'PENDING') &&
      !current.state.error
        ? 2000
        : false,
  });
  const retry = useMutation({
    mutationFn: () => {
      const body = { revision: row.revision, operationId: id };
      return notebookApi.retry(groupId, row.id, body, commandKey({ ...body, retryIntent }));
    },
    onSuccess: (operation) => {
      setRetryIntent((value) => value + 1);
      client.setQueryData(['operation', groupId, id], operation);
      void client.invalidateQueries({ queryKey: notebookKeys.all(groupId) });
    },
    onError: (error) => {
      if (error instanceof ApiError && [403, 409].includes(error.status))
        void client.invalidateQueries({ queryKey: notebookKeys.all(groupId) });
    },
  });
  return (
    <Card
      title="最近操作"
      supportingText="操作单记录执行进度；环境是否可访问仍以实时访问检查为准。"
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
            <StatusBadge status={query.data.status} />
            <span className="mono small break-word">{id}</span>
          </div>
          <p className="small muted">处理阶段：{query.data.step || '—'}</p>
          {query.data.lastError && (
            <Alert color="error" title="执行失败">
              {query.data.lastError}
            </Alert>
          )}
          {query.data.status === 'FAILED' &&
            [
              'notebook.create',
              'notebook.start',
              'notebook.stop',
              'notebook.update',
              'notebook.delete',
            ].includes(query.data.action) &&
            can(query.data.action) && (
              <div className="actions">
                <Button
                  hierarchy="outline"
                  loading={retry.isPending}
                  onClick={() => retry.mutate()}
                >
                  重试失败操作
                </Button>
                <span className="small muted">后端会重新核对权限、身份和修订。</span>
              </div>
            )}
          {retry.isError && <ErrorState error={retry.error} />}
        </div>
      )}
    </Card>
  );
}
