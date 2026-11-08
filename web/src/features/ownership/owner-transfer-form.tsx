import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation } from '@tanstack/react-query';
import { Alert, Button, Card, Input } from '@atomus';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import type { Operation } from '@/lib/types';
import { ApiError } from '@/lib/api';
import { ErrorState } from '@/components/feedback';
import { FormTextarea } from '@/components/form-textarea';
import { UnsavedChangesGuard } from '@/components/unsaved-changes-guard';
import { ownerTransferSchema, type OwnerTransferValues } from './form-schema';
import type { OwnerTransferInput } from './types';

// Shared only by the two explicit ownership workflows. Endpoints, permissions,
// stopped-state checks and cache invalidation remain in their respective modules.
export function OwnerTransferForm({
  name,
  ownerUserId,
  revision,
  allowed,
  notice,
  submit,
  onAccepted,
}: {
  name: string;
  ownerUserId: number | null;
  revision: number;
  allowed: boolean;
  notice: string;
  submit: (body: OwnerTransferInput, key: string) => Promise<Operation>;
  onAccepted: (operation: Operation, stillMounted: boolean) => void;
}) {
  const [baseline] = useState({ name, ownerUserId, revision });
  const changed = revision !== baseline.revision || ownerUserId !== baseline.ownerUserId;
  const key = useCommandKey();
  const mounted = useMounted();
  const form = useForm<OwnerTransferValues>({
    resolver: zodResolver(ownerTransferSchema),
    defaultValues: { userId: '', reason: '', confirmName: '' },
  });
  const mutation = useMutation({
    mutationFn: (values: OwnerTransferValues) => {
      if (!allowed || changed) throw new Error('当前状态、归属版本或权限已变化，请返回详情确认');
      if (values.confirmName !== baseline.name) throw new Error('确认名称不匹配');
      if (Number(values.userId) === baseline.ownerUserId)
        throw new Error('请选择不同于当前负责人的用户');
      const body = {
        revision: baseline.revision,
        userId: Number(values.userId),
        reason: values.reason,
      };
      return submit(body, key(body));
    },
    onSuccess: (op) => onAccepted(op, mounted.current),
  });
  const conflicted =
    mutation.error instanceof ApiError && [401, 403, 404, 409].includes(mutation.error.status);
  return (
    <Card>
      <form
        className="stack spacious"
        onSubmit={form.handleSubmit((v) => {
          if (!mutation.isPending && !mutation.isSuccess) mutation.mutate(v);
        })}
      >
        <UnsavedChangesGuard
          when={form.formState.isDirty && !mutation.isPending && !mutation.isSuccess}
        />
        <Alert color="warning" title="业务归属与数据归属分别交接">
          {notice}
        </Alert>
        <p className="small muted">
          当前负责人：{baseline.ownerUserId ?? '未分配'}；交接基线版本：{baseline.revision}
          。目标必须是本空间的有效启用成员，由后端重新检查，不读取全站用户目录。
        </p>
        <fieldset className="form-section" disabled={mutation.isPending || mutation.isSuccess}>
          <Input
            label="新负责人用户 ID"
            inputMode="numeric"
            {...form.register('userId')}
            error={form.formState.errors.userId?.message}
          />
          <FormTextarea
            label="交接原因"
            {...form.register('reason')}
            error={form.formState.errors.reason?.message}
          />
          <Input
            label={`输入 ${baseline.name} 确认`}
            autoComplete="off"
            {...form.register('confirmName')}
            error={form.formState.errors.confirmName?.message}
          />
        </fieldset>
        {mutation.isError && <ErrorState error={mutation.error} />}
        {conflicted && (
          <p className="small muted">
            没有覆盖最新归属。请保留需要的输入，返回详情确认最新状态后重新交接。
          </p>
        )}
        {changed && (
          <Alert color="warning" title="交接基线已过期">
            负责人或资源版本已变化。输入仍保留，请返回详情核对后重新进入，不会自动替换为新版本提交。
          </Alert>
        )}
        <div className="actions end">
          <Button
            hierarchy="primary"
            type="submit"
            loading={mutation.isPending}
            disabled={
              !allowed ||
              changed ||
              conflicted ||
              mutation.isSuccess ||
              form.watch('confirmName') !== baseline.name
            }
          >
            确认交接
          </Button>
        </div>
      </form>
    </Card>
  );
}
