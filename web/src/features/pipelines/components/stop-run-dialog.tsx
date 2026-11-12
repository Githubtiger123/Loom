import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Button, Modal } from '@atomus';
import { ErrorState } from '@/components/feedback';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { pipelineApi, pipelineKeys, runPath } from '../api';
import type { Run } from '../types';
import { canStop, usePipelineAccess } from '../policy';

export function StopRunDialog({
  groupId,
  run,
  onClose,
  onAccepted,
}: {
  groupId: string;
  run: Run;
  onClose: () => void;
  onAccepted: (id: string) => void;
}) {
  const [revision] = useState(run.revision);
  const key = useCommandKey();
  const mounted = useMounted();
  const client = useQueryClient();
  const access = usePipelineAccess(groupId);
  const enabled = access.allows('pipeline.stop', run) && canStop(run) && revision === run.revision;
  const mutation = useMutation({
    retry: false,
    mutationFn: () => {
      const path = `${runPath(groupId, run.id)}/stop`;
      const body = { revision };
      return pipelineApi.create(path, body, key({ path, body }));
    },
    onSuccess: (operation) => {
      void client.invalidateQueries({ queryKey: pipelineKeys.all(groupId) });
      if (mounted.current) onAccepted(operation.operationId);
    },
  });
  return (
    <Modal
      open
      type="destructive"
      title="停止此次运行？"
      description="后台将向执行引擎请求停止。实际停止时间以观测状态为准，不会删除已有产物。"
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
              if (enabled) mutation.mutate();
            }}
          >
            请求停止
          </Button>
        </>
      }
    >
      <p>
        {run.name} · r{revision}
      </p>
      {revision !== run.revision && (
        <ErrorState error={new Error('运行修订已变化，请关闭弹窗并重新确认')} />
      )}
      {access.isError && <ErrorState error={access.error} />}
      {mutation.isError && <ErrorState error={mutation.error} />}
    </Modal>
  );
}
