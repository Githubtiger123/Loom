import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Button, Modal } from '@atomus';
import { ErrorState } from '@/components/feedback';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { pipelineApi, pipelineKeys, pipelinePath, runPath, noAccess } from '../api';
import { canManageRun, canStart, usePipelineAccess } from '../policy';
import type { Pipeline, Run } from '../types';

type Target = { kind: 'archive'; row: Pipeline } | { kind: 'delete' | 'start'; row: Run };
const messages = {
  archive: {
    title: '归档流水线定义？',
    label: '确认归档',
    description:
      '需先删除定义下全部运行，并等待外部资源清理完成。版本和修订历史保留；不会删除存储文件，没有自动恢复入口。',
  },
  delete: {
    title: '删除运行并清理执行资源？',
    label: '请求删除与清理',
    description:
      '运行中的任务会被停止，后台核验终态并清理 Workflow／Pod。业务记录在清理确认后标记删除；保留历史证据，不删除已有对象存储产物。',
  },
  start: {
    title: '启动已编辑的运行？',
    label: '确认启动新实例',
    description:
      '使用已保存的参数与版本快照创建新实例，不会替换为版本的最新修订；旧执行历史保留。受理不代表执行成功。',
  },
};

export function LifecycleDialog({
  groupId,
  target,
  active,
  onClose,
  onAccepted,
}: {
  groupId: string;
  target: Target;
  active: boolean;
  onClose: () => void;
  onAccepted: (id: string) => void;
}) {
  const [frozen] = useState({
    revision: target.row.revision,
    instance: target.kind === 'archive' ? undefined : target.row.instanceId,
  });
  const access = usePipelineAccess(groupId);
  const key = useCommandKey();
  const mounted = useMounted();
  const client = useQueryClient();
  const message = messages[target.kind];
  const unchanged =
    frozen.revision === target.row.revision &&
    (target.kind === 'archive' || frozen.instance === target.row.instanceId);
  const allowed = access.allows(
    target.kind === 'start' ? 'pipeline.run' : 'pipeline.delete',
    target.row,
  );
  const eligible =
    target.kind === 'archive'
      ? !target.row.deletedAt
      : target.kind === 'start'
        ? canStart(target.row)
        : canManageRun(target.row);
  const enabled = active && unchanged && allowed && eligible;
  const mutation = useMutation({
    retry: false,
    mutationFn: () => {
      if (!enabled) throw noAccess();
      const path =
        target.kind === 'archive'
          ? pipelinePath(groupId, target.row.id)
          : `${runPath(groupId, target.row.id)}${target.kind === 'start' ? '/start' : ''}`;
      const method = target.kind === 'start' ? 'POST' : 'DELETE';
      const body = { revision: frozen.revision };
      return pipelineApi.command(path, method, body, key({ path, method, body }));
    },
    onSuccess: (operation) => {
      void client.invalidateQueries({ queryKey: pipelineKeys.all(groupId) });
      if (mounted.current) onAccepted(operation.operationId);
    },
  });
  return (
    <Modal
      open
      type={target.kind === 'start' ? 'default' : 'destructive'}
      title={message.title}
      description={message.description}
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
            disabled={!enabled || mutation.isSuccess}
            onClick={() => {
              if (enabled && !mutation.isPending && !mutation.isSuccess) mutation.mutate();
            }}
          >
            {message.label}
          </Button>
        </>
      }
    >
      <p>
        {target.row.name} · r{frozen.revision}
      </p>
      {!unchanged && (
        <ErrorState
          error={new Error('修订或实例已变化，请关闭弹窗并重新核对；不会自动覆盖新版本')}
        />
      )}
      {(!allowed || access.isError) && <ErrorState error={access.error ?? noAccess()} />}
      {mutation.isError && <ErrorState error={mutation.error} />}
    </Modal>
  );
}
