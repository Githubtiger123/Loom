import { useState } from 'react';
import { FormProvider, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { Alert, Button, Card } from '@atomus';
import type { Group } from '@/lib/types';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { ErrorState } from '@/components/feedback';
import { FormTextarea } from '@/components/form-textarea';
import { UnsavedChangesGuard } from '@/components/unsaved-changes-guard';
import { groupApi } from '../api';
import { refreshGroup } from '../cache';
import { quotaUpdateSchema, type QuotaUpdateValues } from '../form-schema';
import { canChangeQuota } from '../policy';
import type { QuotaView } from '../types';
import { QuotaFields } from './quota-fields';

export function QuotaForm({
  groupId,
  group,
  view,
  allowed,
}: {
  groupId: string;
  group: Group;
  view: QuotaView;
  allowed: boolean;
}) {
  const [baseline] = useState(() => ({
    revision: group.revision,
    quota: view.requested,
    snapshotRevision: view.snapshot?.groupRevision,
  }));
  const form = useForm<QuotaUpdateValues>({
    resolver: zodResolver(quotaUpdateSchema),
    defaultValues: {
      cpu: baseline.quota.cpu,
      memory: baseline.quota.memory,
      gpu: String(baseline.quota.gpu),
      reason: '',
    },
  });
  const client = useQueryClient();
  const key = useCommandKey();
  const mounted = useMounted();
  const navigate = useNavigate();
  const mutation = useMutation({
    mutationFn: (v: QuotaUpdateValues) => {
      if (!allowed || !canChangeQuota(group.status))
        throw new Error('当前空间状态或权限不允许调整额度');
      const body = {
        revision: baseline.revision,
        quota: { cpu: v.cpu, memory: v.memory, gpu: Number(v.gpu) },
        reason: v.reason,
      };
      return groupApi.updateQuota(groupId, body, key(body));
    },
    onSuccess: (op) => {
      refreshGroup(client, groupId);
      if (mounted.current)
        void navigate(`/workspaces/${groupId}/quota`, {
          replace: true,
          state: { operationId: op.operationId },
        });
    },
  });
  const stale =
    group.revision !== baseline.revision || baseline.snapshotRevision !== baseline.revision;
  return (
    <Card>
      <FormProvider {...form}>
        <form
          className="stack spacious"
          onSubmit={form.handleSubmit((v) => {
            if (!mutation.isPending && !mutation.isSuccess) mutation.mutate(v);
          })}
        >
          <UnsavedChangesGuard
            when={form.formState.isDirty && !mutation.isPending && !mutation.isSuccess}
          />
          <Alert color="info" title={`基于空间修订 r${baseline.revision}`}>
            配额不会低于集群已用量与业务预留；更新提交后异步落实到 Profile 和 ResourceQuota。CPU /
            内存需要正数，GPU 可以为 0；格式和精度由后端校验。
          </Alert>
          {stale && (
            <Alert color="warning" title="空间修订或快照已经变化">
              保留当前输入，不会自动换成新版本覆盖。请记录草稿后返回配额页刷新并重新编辑；旧修订提交会被后端拒绝。
            </Alert>
          )}
          {!canChangeQuota(group.status) && (
            <Alert color="warning" title="当前不可调整配额">
              仅在空间可用或已停用时允许调整，请等待当前操作完成。
            </Alert>
          )}
          <fieldset className="form-section" disabled={mutation.isPending}>
            <QuotaFields />
            <FormTextarea
              label="调整原因"
              {...form.register('reason')}
              error={form.formState.errors.reason?.message}
              hint="必填，最多 1000 字"
            />
          </fieldset>
          {mutation.isError && <ErrorState error={mutation.error} />}
          <div className="actions end">
            <Button
              hierarchy="primary"
              type="submit"
              loading={mutation.isPending}
              disabled={
                !allowed ||
                !canChangeQuota(group.status) ||
                baseline.snapshotRevision !== baseline.revision
              }
            >
              提交配额调整
            </Button>
          </div>
        </form>
      </FormProvider>
    </Card>
  );
}
