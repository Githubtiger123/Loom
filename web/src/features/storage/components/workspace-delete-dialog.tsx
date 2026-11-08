import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Checkbox, Input, Modal } from '@atomus';
import { useOutletContext } from 'react-router-dom';
import type { Group } from '@/lib/types';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { useCommandKey } from '@/lib/use-command-key';
import { ErrorState } from '@/components/feedback';
import { FormTextarea } from '@/components/form-textarea';
import { storageApi } from '../api';
import { refreshStorage } from '../cache';
import { workspaceDeleteSchema, type WorkspaceDeleteValues } from '../form-schema';
import { canDisposeWorkspace, ownsWorkspace, readyWorkspace, staleCommand } from '../policy';
import type { Workspace } from '../types';

export function WorkspaceDeleteDialog({
  groupId,
  row,
  available,
  onClose,
  onAccepted,
}: {
  groupId: string;
  row: Workspace;
  available: boolean;
  onClose: () => void;
  onAccepted: (id: string) => void;
}) {
  const group = useOutletContext<Group>();
  const permissions = useGroupPermissions(groupId);
  const client = useQueryClient();
  const key = useCommandKey();
  const form = useForm<WorkspaceDeleteValues>({
    resolver: zodResolver(workspaceDeleteSchema),
    defaultValues: { confirmName: '', reason: '', releaseRetention: false },
  });
  const allowed =
    available &&
    permissions.can('workspace.delete') &&
    ownsWorkspace(row, permissions.data?.userId, permissions.can('workspace.manage.any')) &&
    canDisposeWorkspace(group.status) &&
    readyWorkspace(row) &&
    !row.notebookIds.length;
  const mutation = useMutation({
    mutationFn: (values: WorkspaceDeleteValues) => {
      if (!allowed) throw new Error('当前状态、权限或引用不允许删除');
      if (values.confirmName !== row.pvcName) throw new Error('PVC 名称不匹配');
      if (row.retained && !values.releaseRetention) throw new Error('请显式确认解除保留保护');
      const body = { ...values, revision: row.revision };
      return storageApi.remove(groupId, row.id, body, key(body));
    },
    onSuccess: (op) => {
      refreshStorage(client, groupId);
      onAccepted(op.operationId);
    },
    onError: (error) => {
      if (staleCommand(error)) refreshStorage(client, groupId);
    },
  });
  return (
    <Modal
      open
      type="destructive"
      title={`删除工作目录 ${row.pvcName}`}
      description={`本次删除基于 r${row.revision}。删除实例与删除数据不是同一操作。`}
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
              !allowed ||
              staleCommand(mutation.error) ||
              form.watch('confirmName') !== row.pvcName ||
              (row.retained && !form.watch('releaseRetention'))
            }
            onClick={() => {
              void form.handleSubmit((v) => {
                if (!mutation.isPending) mutation.mutate(v);
              })();
            }}
          >
            确认删除目录数据
          </Button>
        </>
      }
    >
      <div className="stack">
        <Alert color="error" title="目录数据可能无法恢复">
          这会请求后台删除实际
          PVC，文件可能永久丢失，平台不提供恢复保证。请先完成备份。任何关联或历史 Notebook
          引用都必须先安全解除；不提供强制删除。
        </Alert>
        <fieldset className="form-section" disabled={mutation.isPending}>
          <Input
            label={`输入 PVC 名称 ${row.pvcName}`}
            autoComplete="off"
            {...form.register('confirmName')}
            error={form.formState.errors.confirmName?.message}
          />
          <FormTextarea
            label="处置原因"
            {...form.register('reason')}
            error={form.formState.errors.reason?.message}
          />
          {row.retained && (
            <Checkbox
              label="解除目录保留保护，并确认删除其中数据"
              {...form.register('releaseRetention')}
            />
          )}
        </fieldset>
        {mutation.isError && <ErrorState error={mutation.error} />}
        {staleCommand(mutation.error) && (
          <p className="small muted">
            请关闭确认框，刷新目录状态后重新核对。不会自动换版本强制删除。
          </p>
        )}
      </div>
    </Modal>
  );
}
