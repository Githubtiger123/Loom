import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Input, Modal } from '@atomus';
import { ApiError } from '@/lib/api';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { ErrorState } from '@/components/feedback';
import { groupApi } from '../api';
import { refreshGroup } from '../cache';
import type { Member } from '../types';

export function RemoveMemberDialog({
  groupId,
  member,
  allowed,
  onClose,
  onAccepted,
}: {
  groupId: string;
  member: Member;
  allowed: boolean;
  onClose: () => void;
  onAccepted: (id: string) => void;
}) {
  const [confirm, setConfirm] = useState('');
  const key = useCommandKey();
  const client = useQueryClient();
  const mounted = useMounted();
  const mutation = useMutation({
    mutationFn: () => {
      if (!allowed) throw new Error('当前没有移除成员的权限');
      const expected = member.roles
        .map((b) => ({ id: b.id, version: b.version }))
        .sort((a, b) => a.id - b.id);
      return groupApi.replaceRoles(
        groupId,
        member.userId,
        [],
        expected,
        key({ userId: member.userId, expected, roles: [] }),
      );
    },
    onSuccess: (op) => {
      refreshGroup(client, groupId);
      if (mounted.current) onAccepted(op.operationId);
    },
  });
  return (
    <Modal
      open
      title={`移除成员 ${member.username}`}
      type="destructive"
      description="移除该用户在本空间的全部角色，不会删除平台账号或清理数据资产。"
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
              confirm !== member.username ||
              (mutation.error instanceof ApiError && [403, 409].includes(mutation.error.status))
            }
            onClick={() => {
              if (!mutation.isPending) mutation.mutate();
            }}
          >
            确认移除
          </Button>
        </>
      }
    >
      <div className="stack">
        <Alert color="warning" title="访问和运行会受影响">
          后端会撤销原生访问并申请停止该成员的 Notebook；存在活跃 ML
          工作负载或最后一位负责人限制时可能拒绝。自己被移除后可能失去空间访问权限。
        </Alert>
        <Input
          label={`输入 ${member.username} 确认`}
          value={confirm}
          disabled={mutation.isPending}
          onChange={(e) => setConfirm(e.target.value)}
          autoComplete="off"
        />
        {mutation.isError && <ErrorState error={mutation.error} />}
      </div>
    </Modal>
  );
}
