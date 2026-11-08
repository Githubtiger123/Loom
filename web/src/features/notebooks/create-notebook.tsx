import { useEffect, useId, useRef } from 'react';
import { FormProvider, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQuery, useQueryClient, type InfiniteData } from '@tanstack/react-query';
import { useNavigate, useOutletContext, useParams } from 'react-router-dom';
import { Alert, Button, Modal } from '@atomus';
import type { Group, Page } from '@/lib/types';
import { ApiError } from '@/lib/api';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { useUnsavedChangesBlocker } from '@/lib/use-unsaved-changes-blocker';
import { ErrorState, Loading, NoData } from '@/components/feedback';
import { storageKeys } from '@/features/storage/api';
import type { Workspace } from '@/features/storage/types';
import { readyWorkspace } from '@/features/storage/policy';
import { notebookApi, notebookKeys } from './api';
import { notebookCreateSchema, type NotebookCreateValues } from './form-schema';
import { NotebookCreateFields } from './components/notebook-create-fields';

export function CreateNotebookDialog() {
  const { groupId = '' } = useParams();
  const group = useOutletContext<Group>();
  const permissions = useGroupPermissions(groupId);
  const navigate = useNavigate();
  const client = useQueryClient();
  const commandKey = useCommandKey();
  const submittedFingerprint = useRef<string | undefined>(undefined);
  const mounted = useMounted();
  const formId = useId();
  const cancelButton = useRef<HTMLButtonElement>(null);
  const continueButton = useRef<HTMLButtonElement>(null);
  const wasConfirming = useRef(false);
  const images = useQuery({
    queryKey: notebookKeys.images(groupId),
    queryFn: ({ signal }) => notebookApi.images(groupId, signal),
    enabled: permissions.can('notebook.create'),
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
  const mutation = useMutation({
    mutationFn: (values: NotebookCreateValues) => {
      if (!permissions.can('notebook.create') || group.status !== 'ACTIVE')
        throw new Error('当前权限或空间状态不允许创建，请刷新后确认');
      if (!images.isSuccess) throw new Error('镜像目录尚未确认，请重新读取');
      const image = images.data.items.find((entry) => entry.id === values.imageId);
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
      // A lost success response may already have bound this directory. Preserve
      // unchanged idempotent replays instead of rejecting the changed list state.
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
      if (mounted.current)
        void navigate(`/workspaces/${groupId}/notebooks`, {
          replace: true,
          state: { operationId: operation.operationId },
        });
    },
  });
  const blocker = useUnsavedChangesBlocker(
    form.formState.isDirty && !mutation.isPending && !mutation.isSuccess,
  );
  const confirming = blocker.state === 'blocked';
  useEffect(() => {
    if (confirming) continueButton.current?.focus();
    else if (wasConfirming.current) cancelButton.current?.focus();
    wasConfirming.current = confirming;
  }, [confirming]);
  const denied =
    permissions.error instanceof ApiError && [401, 403].includes(permissions.error.status);
  const hasPermission = !denied && !!permissions.data?.permissions.includes('notebook.create');
  const enabled =
    hasPermission &&
    permissions.can('notebook.create') &&
    group.status === 'ACTIVE' &&
    images.isSuccess &&
    !!images.data.items.length;
  const close = () => {
    if (mutation.isPending) return;
    if (blocker.state === 'blocked') {
      blocker.reset();
      return;
    }
    void navigate(`/workspaces/${groupId}/notebooks`, { replace: true });
  };
  return (
    <Modal
      open
      size="lg"
      className="resource-create-dialog"
      title={confirming ? '放弃未提交的开发环境？' : '新建开发环境'}
      description={
        confirming
          ? '当前填写内容尚未提交，离开后将丢弃。'
          : '选择受管镜像、计算规格与工作目录，提交后由平台异步创建并启动。'
      }
      closeButton={!mutation.isPending}
      onClose={close}
      actions={
        confirming ? (
          <>
            <Button
              ref={continueButton}
              hierarchy="outline"
              onClick={() => {
                if (blocker.state === 'blocked') blocker.reset();
              }}
            >
              继续填写
            </Button>
            <Button
              hierarchy="primary"
              onClick={() => {
                if (blocker.state === 'blocked') blocker.proceed();
              }}
            >
              放弃并离开
            </Button>
          </>
        ) : (
          <>
            <Button
              ref={cancelButton}
              hierarchy="outline"
              disabled={mutation.isPending}
              onClick={close}
            >
              取消
            </Button>
            {hasPermission && (
              <Button
                hierarchy="primary"
                type="submit"
                form={formId}
                loading={mutation.isPending}
                disabled={!enabled || mutation.isSuccess}
              >
                创建并启动
              </Button>
            )}
          </>
        )
      }
    >
      {confirming && (
        <Alert color="warning" title="将丢弃本地填写内容">
          继续填写可保留当前输入。离开不会撤销已发出的请求；若提交曾遇到网络异常，请返回列表核对结果。
        </Alert>
      )}
      <div hidden={confirming}>
        {!permissions.data && permissions.isPending ? (
          <Loading label="正在获取空间权限" />
        ) : permissions.isError && !permissions.data ? (
          <ErrorState
            error={permissions.error}
            retry={() => {
              void permissions.refetch();
            }}
          />
        ) : !hasPermission ? (
          <NoData title="没有创建开发环境的权限" description="请联系空间负责人分配权限。" />
        ) : (
          <FormProvider {...form}>
            <form
              id={formId}
              className="stack spacious"
              onSubmit={form.handleSubmit((values) => {
                if (!mutation.isPending && !mutation.isSuccess) mutation.mutate(values);
              })}
            >
              {permissions.isError && (
                <ErrorState
                  error={permissions.error}
                  retry={() => {
                    void permissions.refetch();
                  }}
                />
              )}
              {group.status !== 'ACTIVE' && (
                <Alert color="warning" title="空间尚不可用">
                  可查看当前表单，空间恢复可用后才能提交创建。
                </Alert>
              )}
              <NotebookCreateFields
                groupId={groupId}
                userId={permissions.data?.userId}
                allowReuse={
                  permissions.can('workspace.read') && permissions.can('workspace.attach')
                }
                disabled={mutation.isPending || mutation.isSuccess}
                images={images}
              />
              {mutation.isError && <ErrorState error={mutation.error} />}
              <p className="small muted">
                请求受理不代表环境已就绪，创建后可在列表查看操作进度。删除环境默认保留工作目录。
              </p>
            </form>
          </FormProvider>
        )}
      </div>
    </Modal>
  );
}
