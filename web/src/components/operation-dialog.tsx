import { useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Modal } from '@atomus';
import { api } from '@/lib/api';
import type { Operation } from '@/lib/types';
import { ErrorState, Loading, StatusBadge } from './feedback';

export function OperationDialog({
  id,
  groupId,
  onClose,
}: {
  id: string;
  groupId: string;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const query = useQuery({
    queryKey: ['operation', groupId, id],
    queryFn: ({ signal }) => api<Operation>(`/operations/${encodeURIComponent(id)}`, { signal }),
    refetchInterval: (current) =>
      ['PENDING', 'RUNNING', 'RETRYING'].includes(current.state.data?.status ?? 'PENDING') &&
      !current.state.error
        ? 2000
        : false,
  });
  const status = query.data?.status;
  useEffect(() => {
    if (status && !['PENDING', 'RUNNING', 'RETRYING'].includes(status)) {
      void client.invalidateQueries({ queryKey: ['groups'] });
      if (groupId) {
        for (const queryKey of [
          ['resources', groupId],
          ['group', groupId],
          ['group-management', groupId],
          ['group-permissions', groupId],
        ]) {
          void client.invalidateQueries({ queryKey });
        }
      }
    }
  }, [client, groupId, status]);
  return (
    <Modal
      open
      onClose={onClose}
      title="操作进度"
      description="操作已受理，资源实际状态以后台观测为准。"
      actions={
        <Button hierarchy="outline" onClick={onClose}>
          关闭
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
          <StatusBadge status={query.data.status} />
          <dl className="details-grid">
            <dt>操作编号</dt>
            <dd className="mono break-word">{query.data.operationId}</dd>
            <dt>处理阶段</dt>
            <dd>{query.data.step}</dd>
          </dl>
          {query.data.lastError && <ErrorState error={new Error(query.data.lastError)} />}
        </div>
      )}
    </Modal>
  );
}
