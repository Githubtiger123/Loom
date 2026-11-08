import { useState } from 'react';
import { FormProvider, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useOutletContext, useParams } from 'react-router-dom';
import { Alert, Button, Card } from '@atomus';
import type { Group } from '@/lib/types';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { UnsavedChangesGuard } from '@/components/unsaved-changes-guard';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { ApiError } from '@/lib/api';
import { notebookApi, notebookKeys } from './api';
import { notebookSettingsSchema, type NotebookSettingsValues } from './form-schema';
import { canChangeRuntime, isNotebookBusy, ownsNotebook } from './policy';
import type { NotebookDetail, NotebookUpdateInput } from './types';
import { NotebookSettingsFields } from './components/notebook-settings-fields';

export function EditNotebookPage() {
  const { groupId = '', notebookId = '' } = useParams();
  const permissions = useGroupPermissions(groupId);
  const query = useQuery({
    queryKey: notebookKeys.detail(groupId, notebookId),
    queryFn: ({ signal }) => notebookApi.detail(groupId, notebookId, signal),
    staleTime: 0,
    refetchOnWindowFocus: false,
  });
  if ((!query.data && query.isPending) || (!permissions.data && permissions.isPending))
    return <Loading />;
  if ((!query.data && query.isError) || (!permissions.data && permissions.isError))
    return (
      <ErrorState
        error={query.error || permissions.error}
        retry={() => {
          void query.refetch();
          void permissions.refetch();
        }}
      />
    );
  if (!query.data || !permissions.data) return <Loading />;
  if (
    !permissions.data.permissions.includes('notebook.update') ||
    !ownsNotebook(
      query.data,
      permissions.data.userId,
      permissions.data.permissions.includes('notebook.read.any'),
    )
  )
    return <NoData title="没有编辑此环境的权限" />;
  return (
    <div className="stack spacious">
      {(query.isError || permissions.isError) && (
        <ErrorState
          error={query.error || permissions.error}
          retry={() => {
            void query.refetch();
            void permissions.refetch();
          }}
        />
      )}
      <EditNotebookForm
        key={`${groupId}/${notebookId}`}
        groupId={groupId}
        row={query.data}
        canSubmit={!query.isError && permissions.can('notebook.update')}
      />
    </div>
  );
}

function EditNotebookForm({
  groupId,
  row,
  canSubmit,
}: {
  groupId: string;
  row: NotebookDetail;
  canSubmit: boolean;
}) {
  // Pin revision and initial values; never silently rebase an in-progress form.
  const [baseline] = useState(row);
  const group = useOutletContext<Group>();
  const navigate = useNavigate();
  const client = useQueryClient();
  const commandKey = useCommandKey();
  const mounted = useMounted();
  const locked = !canChangeRuntime(baseline);
  const form = useForm<NotebookSettingsValues>({
    resolver: zodResolver(notebookSettingsSchema),
    defaultValues: {
      displayName: baseline.displayName,
      description: baseline.description,
      cpu: `${baseline.cpuMilli}m`,
      memory: String(baseline.memoryBytes),
      gpu: String(baseline.gpu) as NotebookSettingsValues['gpu'],
      idleStopMinutes: String(
        baseline.idleStopMinutes,
      ) as NotebookSettingsValues['idleStopMinutes'],
    },
  });
  const {
    handleSubmit,
    formState: { dirtyFields, isDirty },
  } = form;
  const mutation = useMutation({
    mutationFn: (values: NotebookSettingsValues) => {
      if (!canSubmit) throw new Error('当前权限或数据尚未确认，请刷新后再提交');
      const body: NotebookUpdateInput = { revision: baseline.revision };
      if (dirtyFields.displayName) body.displayName = values.displayName;
      if (dirtyFields.description) body.description = values.description;
      if (dirtyFields.idleStopMinutes) body.idleStopMinutes = Number(values.idleStopMinutes);
      if (!locked) {
        if (dirtyFields.cpu) body.cpu = values.cpu;
        if (dirtyFields.memory) body.memory = values.memory;
        if (dirtyFields.gpu) body.gpu = Number(values.gpu);
      }
      if (Object.keys(body).length === 1) throw new Error('没有需要保存的修改');
      return notebookApi.update(groupId, baseline.id, body, commandKey(body));
    },
    onSuccess: (operation) => {
      void client.invalidateQueries({ queryKey: notebookKeys.all(groupId) });
      if (!mounted.current) return;
      void navigate(`/workspaces/${groupId}/notebooks/${baseline.id}`, {
        replace: true,
        state: { operationId: operation.operationId },
      });
    },
    onError: (error) => {
      if (error instanceof ApiError && error.status === 409)
        void client.invalidateQueries({ queryKey: notebookKeys.all(groupId) });
    },
  });
  return (
    <div className="stack spacious">
      <UnsavedChangesGuard when={isDirty && !mutation.isPending && !mutation.isSuccess} />
      <Link className="text-link" to={`/workspaces/${groupId}/notebooks/${baseline.id}`}>
        ← 返回环境详情
      </Link>
      <PageHeading
        eyebrow="ENVIRONMENT SETTINGS"
        title={`编辑 ${baseline.displayName || baseline.name}`}
        description={`基于修订 r${baseline.revision} 保存；只提交修改字段，不覆盖已有环境变量和标签。`}
      />
      <Alert color="brand" title="编辑规则">
        名称、镜像类型及目录容量不可在这里修改。计算规格需要先停止环境；保存后仍保持停止，由你手动启动。
      </Alert>
      <Card>
        <FormProvider {...form}>
          <form
            className="stack spacious"
            onSubmit={handleSubmit((values) => {
              if (!mutation.isPending) mutation.mutate(values);
            })}
          >
            <fieldset className="form-fields" disabled={mutation.isPending}>
              <NotebookSettingsFields runtimeLocked={locked} />
            </fieldset>
            {mutation.isError && <ErrorState error={mutation.error} />}
            {mutation.error instanceof ApiError && mutation.error.status === 409 && (
              <Alert color="warning" title="未覆盖最新配置">
                请保留需要的修改，返回详情刷新后重新编辑。当前输入不会自动清空或强制覆盖。
              </Alert>
            )}
            <div className="actions end">
              <Button
                hierarchy="outline"
                disabled={mutation.isPending}
                onClick={() => {
                  void navigate(`/workspaces/${groupId}/notebooks/${baseline.id}`);
                }}
              >
                取消
              </Button>
              <Button
                hierarchy="primary"
                type="submit"
                loading={mutation.isPending}
                disabled={
                  !canSubmit || !isDirty || isNotebookBusy(baseline) || group.status !== 'ACTIVE'
                }
              >
                保存修改
              </Button>
            </div>
          </form>
        </FormProvider>
      </Card>
    </div>
  );
}
