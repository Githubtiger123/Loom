import { useState } from 'react';
import { FormProvider, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { Alert, Button, Card, Input } from '@atomus';
import { api } from '@/lib/api';
import type { Me } from '@/lib/types';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { UnsavedChangesGuard } from '@/components/unsaved-changes-guard';
import { groupApi } from './api';
import { createGroupSchema, type CreateGroupValues } from './form-schema';
import { QuotaFields } from './components/quota-fields';

export function CreateGroupPage() {
  const me = useQuery({
    queryKey: ['me'],
    queryFn: ({ signal }) => api<Me>('/me', { signal }),
    staleTime: 0,
  });
  return (
    <main className="standalone stack spacious">
      <Link className="text-link" to="/">
        ← 返回空间列表
      </Link>
      <PageHeading
        eyebrow="NEW WORKSPACE"
        title="创建工作空间"
        description="创建业务空间及对应 Profile，由后台落实隔离环境与授权。"
      />
      {me.isError && (
        <ErrorState
          error={me.error}
          retry={() => {
            void me.refetch();
          }}
        />
      )}
      {!me.data ? (
        me.isPending && <Loading />
      ) : !me.data.permissions.includes('group.create') ? (
        <NoData title="没有创建空间的权限" />
      ) : (
        <CreateForm allowed={!me.isError} />
      )}
    </main>
  );
}
function CreateForm({ allowed }: { allowed: boolean }) {
  const navigate = useNavigate();
  const client = useQueryClient();
  const mounted = useMounted();
  const commandKey = useCommandKey();
  const [operation, setOperation] = useState<string>();
  const form = useForm<CreateGroupValues>({
    resolver: zodResolver(createGroupSchema),
    defaultValues: { name: '', displayName: '', cpu: '8', memory: '16Gi', gpu: '0' },
  });
  const mutation = useMutation({
    mutationFn: (v: CreateGroupValues) => {
      if (!allowed) throw new Error('当前权限未确认，请刷新后提交');
      const body = {
        name: v.name,
        displayName: v.displayName,
        quota: { cpu: v.cpu, memory: v.memory, gpu: Number(v.gpu) },
      };
      return groupApi.create(body, commandKey(body));
    },
    onSuccess: (op) => {
      void client.invalidateQueries({ queryKey: ['groups'] });
      if (!mounted.current) return;
      const result = op.result;
      const id =
        result && typeof result === 'object' && 'groupId' in result ? result.groupId : undefined;
      if (typeof id === 'number' && Number.isSafeInteger(id) && id > 0) {
        void navigate(`/g/${id}/settings`, {
          replace: true,
          state: { operationId: op.operationId },
        });
      } else setOperation(op.operationId);
    },
  });
  return (
    <>
      <UnsavedChangesGuard
        when={form.formState.isDirty && !mutation.isPending && !mutation.isSuccess}
      />
      <Alert color="info" title="负责人由平台自动配置">
        你将成为业务空间负责人；底层 Profile owner 使用平台配置的管理身份，两者相互独立。
      </Alert>
      <Card>
        <FormProvider {...form}>
          <form
            className="stack spacious"
            onSubmit={form.handleSubmit((v) => {
              if (!mutation.isPending && !mutation.isSuccess) mutation.mutate(v);
            })}
          >
            <fieldset className="form-section" disabled={mutation.isPending || mutation.isSuccess}>
              <legend>空间信息</legend>
              <Input
                label="空间名"
                hint="与 namespace / Profile 同名，最长 63 字符，创建后不可修改"
                {...form.register('name')}
                error={form.formState.errors.name?.message}
              />
              <Input
                label="展示名称"
                {...form.register('displayName')}
                error={form.formState.errors.displayName?.message}
              />
              <QuotaFields />
            </fieldset>
            {mutation.isError && <ErrorState error={mutation.error} />}
            {mutation.isSuccess && (
              <Alert color="info" title="创建请求已受理">
                请返回空间列表查看创建进度，不要重复提交。
              </Alert>
            )}
            <div className="actions end">
              <Button
                type="submit"
                hierarchy="primary"
                loading={mutation.isPending}
                disabled={!allowed || mutation.isSuccess}
              >
                创建空间
              </Button>
            </div>
          </form>
        </FormProvider>
      </Card>
      {operation && (
        <OperationDialog id={operation} groupId="" onClose={() => setOperation(undefined)} />
      )}
    </>
  );
}
