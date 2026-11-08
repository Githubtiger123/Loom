import { useRef } from 'react';
import { Controller, FormProvider, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQuery, useQueryClient, type InfiniteData } from '@tanstack/react-query';
import { Link, useNavigate, useOutletContext, useParams } from 'react-router-dom';
import { Alert, Button, Card, Input, Select } from '@atomus';
import type { Group, Page } from '@/lib/types';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { UnsavedChangesGuard } from '@/components/unsaved-changes-guard';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { notebookApi, notebookKeys } from './api';
import { notebookCreateSchema, type NotebookCreateValues } from './form-schema';
import { NotebookSettingsFields } from './components/notebook-settings-fields';
import { NotebookWorkspaceFields } from './components/notebook-workspace-fields';
import { storageKeys } from '@/features/storage/api';
import type { Workspace } from '@/features/storage/types';
import { readyWorkspace } from '@/features/storage/policy';

export function CreateNotebookPage() {
  const { groupId = '' } = useParams();
  const permissions = useGroupPermissions(groupId);
  if (!permissions.data && permissions.isPending) return <Loading />;
  if (!permissions.data && permissions.isError)
    return (
      <ErrorState
        error={permissions.error}
        retry={() => {
          void permissions.refetch();
        }}
      />
    );
  if (!permissions.data?.permissions.includes('notebook.create'))
    return <NoData title="没有创建开发环境的权限" description="请联系空间负责人分配权限。" />;
  return (
    <div className="stack spacious">
      {permissions.isError && (
        <ErrorState
          error={permissions.error}
          retry={() => {
            void permissions.refetch();
          }}
        />
      )}
      <CreateNotebookForm groupId={groupId} canSubmit={permissions.can('notebook.create')} />
    </div>
  );
}

function CreateNotebookForm({ groupId, canSubmit }: { groupId: string; canSubmit: boolean }) {
  const group = useOutletContext<Group>();
  const permissions = useGroupPermissions(groupId);
  const navigate = useNavigate();
  const client = useQueryClient();
  const commandKey = useCommandKey();
  const submittedFingerprint = useRef<string | undefined>(undefined);
  const mounted = useMounted();
  const images = useQuery({
    queryKey: notebookKeys.images(groupId),
    queryFn: ({ signal }) => notebookApi.images(groupId, signal),
  });
  const form = useForm<NotebookCreateValues>({
    resolver: zodResolver(notebookCreateSchema),
    defaultValues: {
      name: '',
      displayName: '',
      description: '',
      imageId: '',
      cpu: '2',
      memory: '4Gi',
      workspaceSize: '20Gi',
      workspaceMode: 'new',
      workspaceSelection: '',
      gpu: '0',
      idleStopMinutes: '0',
    },
  });
  const {
    register,
    control,
    handleSubmit,
    formState: { errors },
  } = form;
  const mutation = useMutation({
    mutationFn: (values: NotebookCreateValues) => {
      if (!canSubmit) throw new Error('当前权限尚未确认，请刷新权限后再提交');
      const image = images.data?.items.find((entry) => entry.id === values.imageId);
      if (!image) throw new Error('所选镜像已不可用，请刷新目录后重新选择');
      const { workspaceMode, workspaceSelection, ...settings } = values;
      if (
        workspaceMode === 'reuse' &&
        !(permissions.can('workspace.read') && permissions.can('workspace.attach'))
      )
        throw new Error('当前权限不允许复用目录');
      const [workspaceId, workspaceRevision] = workspaceSelection.split('/').map(Number);
      const body = {
        ...settings,
        ...(workspaceMode === 'reuse' ? { workspaceId, workspaceRevision } : {}),
        serverType: image.serverType,
        gpu: Number(values.gpu),
        idleStopMinutes: Number(values.idleStopMinutes),
      };
      const fingerprint = JSON.stringify(body);
      // An accepted request may already have bound this directory even when its
      // response was lost. Do not block an unchanged idempotent replay on that
      // changed list state; the server reauthorizes before replaying its result.
      if (workspaceMode === 'reuse' && submittedFingerprint.current !== fingerprint) {
        const state = client.getQueryState<InfiniteData<Page<Workspace>>>(
          storageKeys.list(groupId),
        );
        const selected = state?.data?.pages
          .flatMap((page) => page.items)
          .find((w) => w.id === workspaceId && w.revision === workspaceRevision);
        if (
          state?.status !== 'success' ||
          !selected ||
          !readyWorkspace(selected) ||
          selected.notebookIds.length ||
          selected.ownerUserId !== permissions.data?.userId
        )
          throw new Error('所选目录不可用或版本已变化，请刷新目录并重新核对选择');
      }
      submittedFingerprint.current = fingerprint;
      return notebookApi.create(groupId, body, commandKey(body));
    },
    onSuccess: (operation) => {
      void client.invalidateQueries({ queryKey: notebookKeys.all(groupId) });
      void client.invalidateQueries({ queryKey: storageKeys.all(groupId) });
      if (!mounted.current) return;
      void navigate(`/g/${groupId}/notebooks`, {
        replace: true,
        state: { operationId: operation.operationId },
      });
    },
  });
  return (
    <div className="stack spacious">
      <UnsavedChangesGuard
        when={form.formState.isDirty && !mutation.isPending && !mutation.isSuccess}
      />
      <Link className="text-link" to={`/g/${groupId}/notebooks`}>
        ← 返回开发环境
      </Link>
      <PageHeading
        eyebrow="NEW ENVIRONMENT"
        title="新建开发环境"
        description="选择受管镜像和计算规格，提交后由平台异步创建并启动。"
      />
      {group.status !== 'ACTIVE' && (
        <Alert color="warning" title="空间尚不可用">
          请等待空间恢复后再创建环境。
        </Alert>
      )}
      <Card>
        <FormProvider {...form}>
          <form
            className="stack spacious"
            onSubmit={handleSubmit((values) => {
              if (!mutation.isPending) mutation.mutate(values);
            })}
          >
            <fieldset className="form-section" disabled={mutation.isPending}>
              <legend>环境与镜像</legend>
              <Input
                label="实例名称"
                hint="最长 63 字符，小写字母、数字和连字符；创建后不可修改"
                {...register('name')}
                error={errors.name?.message}
              />
              {images.isPending ? (
                <Loading label="正在读取受管镜像" />
              ) : images.isError ? (
                <ErrorState
                  error={images.error}
                  retry={() => {
                    void images.refetch();
                  }}
                />
              ) : images.data.items.length === 0 ? (
                <NoData title="暂无可用镜像" description="请联系管理员配置镜像目录。" />
              ) : (
                <Controller
                  control={control}
                  name="imageId"
                  render={({ field }) => (
                    <Select
                      label="运行镜像"
                      value={field.value}
                      onChange={field.onChange}
                      placeholder="请选择镜像"
                      error={errors.imageId?.message}
                      options={images.data.items.map((image) => ({
                        value: image.id,
                        label: `${image.name} · ${image.category}`,
                      }))}
                    />
                  )}
                />
              )}
            </fieldset>
            <fieldset className="form-fields" disabled={mutation.isPending}>
              <NotebookSettingsFields />
              <NotebookWorkspaceFields
                groupId={groupId}
                userId={permissions.data?.userId}
                allowReuse={
                  permissions.can('workspace.read') && permissions.can('workspace.attach')
                }
              />
            </fieldset>
            {mutation.isError && <ErrorState error={mutation.error} />}
            <div className="actions end">
              <Button
                hierarchy="outline"
                disabled={mutation.isPending}
                onClick={() => {
                  void navigate(`/g/${groupId}/notebooks`);
                }}
              >
                取消
              </Button>
              <Button
                hierarchy="primary"
                type="submit"
                loading={mutation.isPending}
                disabled={
                  !canSubmit ||
                  group.status !== 'ACTIVE' ||
                  !images.isSuccess ||
                  images.data.items.length === 0
                }
              >
                创建并启动
              </Button>
            </div>
          </form>
        </FormProvider>
      </Card>
    </div>
  );
}
