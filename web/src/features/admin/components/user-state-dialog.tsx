import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Checkbox, Input, Modal } from '@atomus';
import { ApiError } from '@/lib/api';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { ErrorState } from '@/components/feedback';
import { adminApi } from '../api';
import { refreshAdministration } from '../cache';
import type { ManagedUser } from '../types';
export function UserStateDialog({
  user,
  action,
  allowed,
  onClose,
  onAccepted,
}: {
  user: ManagedUser;
  action: 'enable' | 'disable';
  allowed: boolean;
  onClose: () => void;
  onAccepted: (id: string) => void;
}) {
  const [confirm, setConfirm] = useState('');
  const [subject, setSubject] = useState(user.subject ?? '');
  const [verified, setVerified] = useState(false);
  const client = useQueryClient();
  const key = useCommandKey();
  const mounted = useMounted();
  const label = action === 'enable' ? '启用' : '禁用';
  const externalEnable = action === 'enable' && !user.localAccount;
  const mutation = useMutation({
    mutationFn: () => {
      if (!allowed) throw new Error('没有用户管理权限');
      const body = {
        subject,
        externalIdentityConfirmed: verified,
        expectedAuthorizationVersion: user.authorizationVersion,
      };
      return adminApi.state(user.id, action, body, key({ id: user.id, action, body }));
    },
    onSuccess: (op) => {
      refreshAdministration(client);
      if (mounted.current) onAccepted(op.operationId);
    },
  });
  const conflict = mutation.error instanceof ApiError && [403, 409].includes(mutation.error.status);
  return (
    <Modal
      open
      title={`${label}用户 ${user.username}`}
      type={action === 'disable' ? 'destructive' : 'default'}
      description="只修改 Loom 业务账号状态，不修改外部身份提供方账号。"
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
              conflict ||
              confirm !== user.username ||
              (externalEnable && (!verified || !subject.trim()))
            }
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
        <Alert color="warning" title="请确认影响">
          {action === 'disable'
            ? '账号将不能继续访问平台，旧本地会话失效，并申请停止 Notebook、清理原生访问。活跃 ML 任务、最后管理员或最后负责人限制可能阻止禁用，数据仍保留。'
            : '恢复业务账号后，原有有效授权可能重新生效，但不会自动启动 Notebook。外部身份首次开通需要绑定已核验的稳定主体。'}
        </Alert>
        <fieldset className="form-section" disabled={mutation.isPending}>
          {externalEnable && (
            <>
              <Input
                label="稳定 subject"
                value={subject}
                readOnly={!!user.subject}
                maxLength={255}
                onChange={(e) => setSubject(e.target.value)}
              />
              <Checkbox
                label="我已确认该外部身份可用且属于此用户"
                checked={verified}
                onChange={(e) => setVerified(e.target.checked)}
              />
            </>
          )}
          <Input
            label={`输入 ${user.username} 确认`}
            value={confirm}
            autoComplete="off"
            onChange={(e) => setConfirm(e.target.value)}
          />
        </fieldset>
        {mutation.isError && <ErrorState error={mutation.error} />}
        {conflict && <p className="small muted">请关闭后刷新用户状态与权限，再确认操作。</p>}
      </div>
    </Modal>
  );
}
