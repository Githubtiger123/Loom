import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useOutletContext } from 'react-router-dom';
import { Alert, Button, Input, Modal } from '@atomus';
import type { Group } from '@/lib/types';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { useCommandKey } from '@/lib/use-command-key';
import { ErrorState, Loading, NoData, StatusBadge } from '@/components/feedback';
import { notebookApi, notebookKeys } from '@/features/notebooks/api';
import { canChangeRuntime, ownsNotebook } from '@/features/notebooks/policy';
import { storageApi } from '../api';
import { refreshStorage } from '../cache';
import { notebookReferenceSchema, type NotebookReferenceValues } from '../form-schema';
import { canDisposeWorkspace, ownsWorkspace, readyWorkspace, staleCommand } from '../policy';
import type { Workspace, WorkspaceLinkInput } from '../types';

export function WorkspaceLinkDialog({
  groupId,
  row,
  available,
  action,
  notebookId,
  onClose,
  onAccepted,
}: {
  groupId: string;
  row: Workspace;
  available: boolean;
  action: 'attach' | 'detach';
  notebookId?: number;
  onClose: () => void;
  onAccepted: (id: string) => void;
}) {
  const group = useOutletContext<Group>();
  const permissions = useGroupPermissions(groupId);
  const [selected, setSelected] = useState(notebookId ? String(notebookId) : '');
  const form = useForm<NotebookReferenceValues>({
    resolver: zodResolver(notebookReferenceSchema),
    defaultValues: { notebookId: selected },
  });
  const client = useQueryClient();
  const key = useCommandKey();
  const query = useQuery({
    queryKey: notebookKeys.panel(groupId, selected, 'workspace-link', {
      id: row.id,
      revision: row.revision,
      action,
    }),
    queryFn: ({ signal }) => notebookApi.detail(groupId, selected, signal),
    enabled: !!selected && permissions.can('notebook.read'),
    staleTime: 0,
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
  const target = query.isSuccess ? query.data : undefined;
  const targetStopped =
    target &&
    (canChangeRuntime(target) ||
      (action === 'detach' &&
        !!target.deletedAt &&
        target.desiredState === 'DELETED' &&
        target.observedState === 'DELETED' &&
        target.appliedRevision === target.revision));
  const targetAllowed =
    target &&
    permissions.can('notebook.read') &&
    permissions.can('notebook.update') &&
    ownsNotebook(target, permissions.data?.userId, permissions.can('notebook.read.any'));
  const allowed =
    available &&
    !!targetAllowed &&
    !!targetStopped &&
    permissions.can('workspace.attach') &&
    ownsWorkspace(row, permissions.data?.userId, permissions.can('workspace.manage.any')) &&
    (action === 'attach'
      ? group.status === 'ACTIVE' &&
        readyWorkspace(row) &&
        row.notebookIds.length === 0 &&
        target?.ownerUserId === row.ownerUserId
      : canDisposeWorkspace(group.status) &&
        row.notebookIds.includes(Number(selected)) &&
        !row.deletedAt &&
        !['DELETING', 'DELETED'].includes(row.status));
  const mutation = useMutation({
    mutationFn: (body: WorkspaceLinkInput) => {
      if (!allowed) throw new Error('当前权限、实例状态或数据归属不允许此操作');
      return storageApi.link(groupId, row.id, action, body, key(body));
    },
    onSuccess: (op) => {
      refreshStorage(client, groupId);
      onAccepted(op.operationId);
    },
    onError: (error) => {
      if (staleCommand(error)) refreshStorage(client, groupId);
    },
  });
  const label = action === 'attach' ? '关联' : '解除关联';
  return (
    <Modal
      open
      title={`${label}工作目录`}
      description={`目录 ${row.pvcName}，固定版本 r${row.revision}。`}
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
              !allowed || staleCommand(mutation.error) || form.watch('notebookId') !== selected
            }
            onClick={() => {
              if (target && !mutation.isPending)
                mutation.mutate(
                  mutation.isError && mutation.variables
                    ? mutation.variables
                    : {
                        revision: row.revision,
                        notebookId: target.id,
                        notebookRevision: target.revision,
                      },
                );
            }}
          >
            确认{label}
          </Button>
        </>
      }
    >
      <div className="stack">
        <Alert
          color="warning"
          title={action === 'attach' ? '不会覆盖原目录' : '仅解除引用，不删除文件'}
        >
          {action === 'attach'
            ? '目标必须已确认停止，负责人须与目录相同且目录无其他实例引用。已有其他目录的实例不能隐式替换，需先安全解除旧目录。关联后由用户手动启动。'
            : '实例需确认停止，或已删除且集群已观测确认消失。解除后目录保持保留保护；下一次启动可能新建工作目录，不会自动迁移文件。'}
        </Alert>
        {action === 'attach' && (
          <form
            className="stack"
            onSubmit={form.handleSubmit((v) => {
              if (!mutation.isPending && !mutation.isError) setSelected(v.notebookId);
            })}
          >
            <Input
              label="开发环境 ID"
              inputMode="numeric"
              disabled={mutation.isPending || mutation.isError}
              {...form.register('notebookId')}
              error={form.formState.errors.notebookId?.message}
            />
            <Button
              hierarchy="outline"
              type="submit"
              disabled={mutation.isPending || mutation.isError}
            >
              读取实例并核对
            </Button>
          </form>
        )}
        {!permissions.can('notebook.read') ? (
          <NoData title="读取关联实例需要 notebook.read 权限" />
        ) : (
          selected &&
          (query.isPending ? (
            <Loading label="正在读取目标实例" />
          ) : query.isError ? (
            <ErrorState
              error={query.error}
              retry={() => {
                void query.refetch();
              }}
            />
          ) : (
            target && (
              <>
                <dl className="details-grid">
                  <dt>实例</dt>
                  <dd>
                    {target.displayName || target.name}（#{target.id}）
                  </dd>
                  <dt>负责人</dt>
                  <dd>{target.ownerUserId ?? '未分配'}</dd>
                  <dt>实例修订</dt>
                  <dd>
                    r{target.revision} / 已落实 r{target.appliedRevision}
                  </dd>
                  <dt>状态</dt>
                  <dd>
                    <StatusBadge status={target.status} />
                  </dd>
                </dl>
                {!targetStopped && (
                  <p className="small muted">
                    实例尚未确认停止或消失，请先处理实例状态后重新打开此确认框。
                  </p>
                )}
                {!targetAllowed && (
                  <p className="small muted">
                    还需要目标实例的读取、更新权限及对象操作范围，目录管理权限不能代替。
                  </p>
                )}
                {action === 'attach' && target.ownerUserId !== row.ownerUserId && (
                  <p className="small muted">实例与目录的负责人不一致，不能关联。</p>
                )}
              </>
            )
          ))
        )}
        {mutation.isError && <ErrorState error={mutation.error} />}
        {mutation.isError && (
          <p className="small muted">
            未改变的请求可原样重试；更换目标或处理版本冲突时，请关闭后重新核对，避免把不确定失败悄悄变成新请求。
          </p>
        )}
      </div>
    </Modal>
  );
}
