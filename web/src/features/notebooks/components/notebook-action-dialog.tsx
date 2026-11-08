import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Modal } from '@atomus';
import { ApiError } from '@/lib/api';
import { useCommandKey } from '@/lib/use-command-key';
import type { Notebook } from '@/lib/types';
import { ErrorState } from '@/components/feedback';
import { notebookApi, notebookKeys } from '../api';
import type { NotebookAction } from '../types';

export function NotebookActionDialog({
  groupId,
  row,
  action,
  onClose,
  onAccepted,
}: {
  groupId: string;
  row: Notebook;
  action: NotebookAction;
  onClose: () => void;
  onAccepted: (id: string) => void;
}) {
  const client = useQueryClient();
  const commandKey = useCommandKey();
  const label = { start: '启动', stop: '停止', delete: '删除' }[action];
  const mutation = useMutation({
    mutationFn: () =>
      notebookApi.action(
        groupId,
        row,
        action,
        commandKey({ id: row.id, revision: row.revision, action }),
      ),
    onSuccess: (operation) => {
      void client.invalidateQueries({ queryKey: notebookKeys.all(groupId) });
      onAccepted(operation.operationId);
    },
    onError: (error) => {
      if (error instanceof ApiError && (error.status === 403 || error.status === 409)) {
        void client.invalidateQueries({ queryKey: notebookKeys.all(groupId) });
        void client.invalidateQueries({ queryKey: ['group-permissions', groupId] });
      }
    },
  });
  return (
    <Modal
      open
      title={`${label} ${row.displayName || row.name}`}
      type={action === 'delete' ? 'destructive' : 'default'}
      description={`本次操作基于修订 r${row.revision}。请求受理不代表集群已经完成。`}
      onClose={() => {
        if (!mutation.isPending) onClose();
      }}
      actions={
        <>
          <Button hierarchy="outline" disabled={mutation.isPending} onClick={onClose}>
            取消
          </Button>
          <Button
            hierarchy="primary"
            loading={mutation.isPending}
            disabled={
              mutation.error instanceof ApiError && [403, 409].includes(mutation.error.status)
            }
            onClick={() => mutation.mutate()}
          >
            确认{label}
          </Button>
        </>
      }
    >
      {action === 'delete' ? (
        <Alert color="warning" title="保留工作目录">
          此操作只删除开发环境，工作目录和数据仍保留，不会删除目录资产。
        </Alert>
      ) : (
        <p className="muted">
          {action === 'stop'
            ? '停止后释放运行资源，开发环境及工作目录保留。请先保存尚未写入磁盘的内容。'
            : '将使用当前配置启动环境，实际启动时间取决于调度、镜像和存储状态。'}
        </p>
      )}
      {mutation.isError && <ErrorState error={mutation.error} />}
      {mutation.error instanceof ApiError && mutation.error.status === 409 && (
        <p className="small muted">配置或状态已变化，请关闭弹窗、刷新后重新操作。</p>
      )}
    </Modal>
  );
}
