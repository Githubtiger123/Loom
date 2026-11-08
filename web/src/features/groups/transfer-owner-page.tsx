import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useOutletContext, useParams } from 'react-router-dom';
import { Alert, Button, Card, Checkbox, Input } from '@atomus';
import type { Group } from '@/lib/types';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { ErrorState, PageHeading } from '@/components/feedback';
import { UnsavedChangesGuard } from '@/components/unsaved-changes-guard';
import { groupApi } from './api';
import { refreshGroup } from './cache';
import { transferSchema, type TransferValues } from './form-schema';
import { canManageMembers } from './policy';
import { CapabilityBoundary } from './components/capability-boundary';

export function TransferOwnerPage() {
  const { groupId = '' } = useParams();
  return (
    <CapabilityBoundary groupId={groupId} permission="group.owner.transfer">
      {(allowed) => <TransferForm groupId={groupId} allowed={allowed} />}
    </CapabilityBoundary>
  );
}
function TransferForm({ groupId, allowed }: { groupId: string; allowed: boolean }) {
  const group = useOutletContext<Group>();
  const permissions = useGroupPermissions(groupId);
  const form = useForm<TransferValues>({
    resolver: zodResolver(transferSchema),
    defaultValues: { userId: '', keepCurrent: true, confirmName: '' },
  });
  const navigate = useNavigate();
  const mounted = useMounted();
  const client = useQueryClient();
  const key = useCommandKey();
  const mutation = useMutation({
    mutationFn: (v: TransferValues) => {
      if (!allowed || !canManageMembers(group.status))
        throw new Error('当前空间状态或权限不允许交接');
      if (v.confirmName !== group.name) throw new Error('确认空间名不匹配');
      if (Number(v.userId) === permissions.data?.userId) throw new Error('请选择其他用户');
      const body = { userId: Number(v.userId), keepCurrent: v.keepCurrent };
      return groupApi.transfer(groupId, body, key(body));
    },
    onSuccess: (op) => {
      refreshGroup(client, groupId);
      if (mounted.current)
        void navigate('/', { replace: true, state: { operationId: op.operationId } });
    },
  });
  return (
    <>
      <Link className="text-link" to={`/workspaces/${groupId}/members`}>
        ← 返回空间成员
      </Link>
      <PageHeading
        eyebrow="OWNER HANDOVER"
        title="交接业务负责人"
        description="为另一位启用用户赋予负责人角色；不改变底层 Profile 的管理身份。"
      />
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
          <Alert color="warning" title="请核对目标用户">
            若不保留自己的负责人角色，只会撤销你自己的
            group_owner，其他角色仍保留；你可能失去空间管理或读取权限。此操作不转移其他成员或
            Notebook 的归属。
          </Alert>
          <fieldset className="form-section" disabled={mutation.isPending}>
            <Input
              label="新负责人用户 ID"
              hint="目标必须是已存在的启用用户，可以尚未加入本空间"
              inputMode="numeric"
              {...form.register('userId')}
              error={form.formState.errors.userId?.message}
            />
            <Checkbox
              label="保留我自己的负责人角色"
              description="默认保留，空间可有多位业务负责人"
              {...form.register('keepCurrent')}
            />
            <Input
              label={`输入空间名 ${group.name} 确认`}
              {...form.register('confirmName')}
              error={form.formState.errors.confirmName?.message}
              autoComplete="off"
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
                !canManageMembers(group.status) ||
                form.watch('confirmName') !== group.name
              }
            >
              确认交接
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}
