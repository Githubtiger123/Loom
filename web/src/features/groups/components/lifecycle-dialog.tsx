import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Input, Modal } from '@atomus';
import type { Group } from '@/lib/types';
import { ApiError } from '@/lib/api';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { ErrorState } from '@/components/feedback';
import { FormTextarea } from '@/components/form-textarea';
import { groupApi } from '../api';
import { refreshGroup } from '../cache';
import { reasonSchema } from '../form-schema';
import type { GroupAction } from '../types';

export function LifecycleDialog({
  groupId,
  group,
  action,
  allowed,
  onClose,
  onAccepted,
}: {
  groupId: string;
  group: Group;
  action: GroupAction;
  allowed: boolean;
  onClose: () => void;
  onAccepted: (id: string) => void;
}) {
  const [reason, setReason] = useState('');
  const [confirmName, setConfirmName] = useState('');
  const key = useCommandKey();
  const client = useQueryClient();
  const mounted = useMounted();
  const validReason = reasonSchema.safeParse(reason);
  const label = { disable: '停用', restore: '恢复', delete: '删除' }[action];
  const mutation = useMutation({
    mutationFn: () => {
      if (!allowed) throw new Error('当前没有执行权限');
      const body = { revision: group.revision, confirmName, reason };
      return groupApi.lifecycle(groupId, action, body, key({ action, ...body }));
    },
    onSuccess: (op) => {
      refreshGroup(client, groupId);
      if (mounted.current) onAccepted(op.operationId);
    },
  });
  const conflict = mutation.error instanceof ApiError && [403, 409].includes(mutation.error.status);
  return (
    <Modal
      open
      title={`${label}空间 ${group.name}`}
      type={action === 'restore' ? 'default' : 'destructive'}
      description={`基于 r${group.revision} 提交。受理后需等待后台执行，不会立即完成。`}
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
            disabled={!allowed || !validReason.success || confirmName !== group.name || conflict}
            onClick={() => {
              if (!mutation.isPending) mutation.mutate();
            }}
          >
            确认{label}
          </Button>
        </>
      }
    >
      <div className="stack">
        <Alert color="warning" title="请确认操作范围">
          {action === 'delete'
            ? '必须先显式处置 Notebook、目录、历史引用、Pipeline、运行、训练、推理及预算占用。空间非空时后端拒绝删除，不提供强制级联删除。'
            : action === 'disable'
              ? '将申请停止本空间 Notebook 并撤销原生访问，保留业务数据。请先结束活跃的 Pipeline、训练和推理任务。'
              : '将重新落实空间及成员访问，不会自动重启 Notebook。'}
        </Alert>
        <fieldset className="form-section" disabled={mutation.isPending}>
          <Input
            label={`输入 ${group.name} 确认`}
            value={confirmName}
            onChange={(e) => setConfirmName(e.target.value)}
            autoComplete="off"
          />
          <FormTextarea
            label="操作原因"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            hint="必填，最多 1000 字，记录在操作单中"
            error={
              reason && !validReason.success ? validReason.error.issues[0]?.message : undefined
            }
          />
        </fieldset>
        {mutation.isError && <ErrorState error={mutation.error} />}
        {conflict && (
          <p className="small muted">
            请记录输入，关闭后刷新空间状态与权限再操作；不会自动覆盖新的修订。
          </p>
        )}
      </div>
    </Modal>
  );
}
