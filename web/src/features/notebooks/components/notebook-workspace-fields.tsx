import { Controller, useFormContext } from 'react-hook-form';
import { useInfiniteQuery } from '@tanstack/react-query';
import { Alert, Button, Input, Select } from '@atomus';
import { ErrorState, Loading, NoData } from '@/components/feedback';
import { formatBytes } from '@/lib/format';
import { storageApi, storageKeys } from '@/features/storage/api';
import { readyWorkspace } from '@/features/storage/policy';
import type { NotebookCreateValues } from '../form-schema';

export function NotebookWorkspaceFields({
  groupId,
  userId,
  allowReuse,
}: {
  groupId: string;
  userId?: number;
  allowReuse: boolean;
}) {
  const form = useFormContext<NotebookCreateValues>();
  const reuse = form.watch('workspaceMode') === 'reuse';
  const query = useInfiniteQuery({
    queryKey: storageKeys.list(groupId),
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) => storageApi.list(groupId, pageParam, signal),
    getNextPageParam: (page) => page.nextAfter ?? undefined,
    enabled: reuse && allowReuse,
  });
  const eligible = (query.data?.pages.flatMap((page) => page.items) ?? []).filter(
    (w) => readyWorkspace(w) && w.ownerUserId === userId && !w.notebookIds.length,
  );
  return (
    <fieldset className="form-section">
      <legend>工作目录</legend>
      <Controller
        control={form.control}
        name="workspaceMode"
        render={({ field }) => (
          <Select
            label="目录来源"
            value={field.value}
            onChange={field.onChange}
            options={[
              { value: 'new', label: '新建独立目录' },
              ...(allowReuse || reuse ? [{ value: 'reuse', label: '复用我的已有目录' }] : []),
            ]}
          />
        )}
      />
      {!reuse ? (
        <Input
          label="新建目录容量"
          hint="例如 20Gi；删除实例默认保留数据"
          {...form.register('workspaceSize')}
          error={form.formState.errors.workspaceSize?.message}
        />
      ) : !allowReuse ? (
        <NoData title="复用目录需要 workspace.read 与 workspace.attach 权限" />
      ) : (
        <>
          <Alert color="brand" title="只显示属于你的空闲独占目录">
            历史实例引用须先解除，复用不会复制或迁移数据。选择时固定目录版本，状态变化由后端拒绝，不会自动覆盖。
          </Alert>
          {query.isPending ? (
            <Loading />
          ) : query.isError ? (
            <ErrorState
              error={query.error}
              retry={() => {
                void query.refetch();
              }}
            />
          ) : (
            <>
              <Controller
                control={form.control}
                name="workspaceSelection"
                render={({ field }) => (
                  <Select
                    label="已有目录"
                    placeholder="选择目录与修订"
                    value={field.value}
                    error={form.formState.errors.workspaceSelection?.message}
                    onChange={(value) => {
                      field.onChange(value);
                    }}
                    options={eligible.map((w) => ({
                      value: `${w.id}/${w.revision}`,
                      label: `${w.pvcName} · ${formatBytes(w.capacityBytes)} · r${w.revision}`,
                    }))}
                  />
                )}
              />
              {!eligible.length && (
                <NoData
                  title="已加载记录中暂无可复用目录"
                  description="可以继续加载，或先解除历史实例引用。"
                />
              )}
              {query.hasNextPage && (
                <Button
                  hierarchy="outline"
                  loading={query.isFetchingNextPage}
                  onClick={() => {
                    void query.fetchNextPage();
                  }}
                >
                  加载更多目录
                </Button>
              )}
              <Button
                hierarchy="outline"
                loading={query.isFetching}
                onClick={() => {
                  void query.refetch();
                }}
              >
                刷新目录
              </Button>
            </>
          )}
          <Input
            label="所需最小容量"
            hint="例如 20Gi；已有目录容量必须不小于此值，不会因此扩容或缩容"
            {...form.register('workspaceSize')}
            error={form.formState.errors.workspaceSize?.message}
          />
          {form.watch('workspaceSelection') && (
            <p className="small muted">
              已选基线：目录 / 修订 {form.watch('workspaceSelection')}
              。刷新不会自动更改此版本；冲突后需重新选择。
            </p>
          )}
        </>
      )}
    </fieldset>
  );
}
