import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Button, Modal } from '@atomus';
import { clearSession } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { useMounted } from '@/lib/use-mounted';
import { ErrorState } from '@/components/feedback';
import { sessionApi, sessionKeys } from '../api';
import type { DeviceSession } from '../types';
export function RevokeSessionDialog({
  session,
  target,
  allowed,
  onClose,
  onRevoked,
}: {
  session: DeviceSession;
  target?: string;
  allowed: boolean;
  onClose: () => void;
  onRevoked: () => void;
}) {
  const mounted = useMounted();
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: () => {
      if (!allowed) throw new Error('当前无权管理会话');
      return sessionApi.revoke(session.familyId, target);
    },
    onSuccess: (result) => {
      if (session.current || result.loginRequired) {
        clearSession();
        queryClient.clear();
        return;
      }
      void client.invalidateQueries({ queryKey: sessionKeys(target) });
      if (mounted.current) onRevoked();
    },
  });
  return (
    <Modal
      open
      title="撤销设备会话？"
      type="destructive"
      description={
        session.current
          ? '这是当前设备，撤销后本页面将退出登录。'
          : '此会话对应的设备将不能继续刷新或使用旧访问凭据，需要重新登录。'
      }
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
            disabled={!allowed}
            loading={mutation.isPending}
            onClick={() => {
              if (!mutation.isPending) mutation.mutate();
            }}
          >
            确认撤销
          </Button>
        </>
      }
    >
      <p className="mono break-word">{session.familyId}</p>
      {mutation.isError && <ErrorState error={mutation.error} />}
    </Modal>
  );
}
