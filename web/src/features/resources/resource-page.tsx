import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { Button, Input, Modal, Select, Table } from '@atomus';
import { Plus, RefreshCw } from 'lucide-react';
import { api } from '@/lib/api';
import type { Operation, Page, Resource } from '@/lib/types';
import { ErrorState, Loading, NoData, PageHeading, StatusBadge } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';

import { resourceModules, type ResourceKind } from './resource-config';
export function ResourcePage({ kind }: { kind: ResourceKind }) {
  const { groupId = '' } = useParams();
  const config = resourceModules[kind];
  const [scope, setScope] = useState('mine');
  const [creating, setCreating] = useState(false);
  const [operation, setOperation] = useState<string>();
  const query = useInfiniteQuery({
    queryKey: ['resources', groupId, kind, scope],
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) =>
      api<Page<Resource>>(`/groups/${groupId}/${kind}?scope=${scope}&limit=50&after=${pageParam}`, {
        signal,
      }),
    getNextPageParam: (page) => page.nextAfter ?? undefined,
    refetchInterval: kind === 'kserve-services' ? 10000 : false,
  });
  const rows = query.data?.pages.flatMap((page) => page.items) ?? [];
  return (
    <div className="stack spacious">
      <PageHeading
        eyebrow={config.eyebrow}
        title={config.title}
        description={config.description}
        actions={
          config.create && (
            <Button
              hierarchy="primary"
              iconLeading={<Plus size={16} />}
              onClick={() => setCreating(true)}
            >
              {config.create}
            </Button>
          )
        }
      />
      <div className="toolbar">
        <Select
          label="资源范围"
          size="sm"
          value={scope}
          onChange={setScope}
          options={[
            { value: 'mine', label: '我的资源' },
            { value: 'all', label: '空间全部资源' },
          ]}
        />
        <Button
          hierarchy="outline"
          size="sm"
          loading={query.isFetching}
          iconLeading={<RefreshCw size={15} />}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新
        </Button>
      </div>
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
        <Table
          rows={rows}
          rowKey={(row) => String(row.id)}
          caption={config.title}
          columns={[
            {
              key: 'name',
              header: '名称',
              render: (row) => (
                <Link className="text-link" to={`/g/${groupId}/${kind}/${row.id}`}>
                  {row.displayName || row.name}
                </Link>
              ),
              supporting: (row) => row.name,
            },
            {
              key: 'status',
              header: '状态',
              render: (row) =>
                kind === 'pipelines' ? '流程定义' : <StatusBadge status={row.status} />,
            },
            { key: 'revision', header: '修订', render: (row) => `r${row.revision}` },
            {
              key: 'description',
              header: '描述',
              render: (row) => <span className="muted">{row.description || '—'}</span>,
            },
            {
              key: 'createdAt',
              header: '创建时间',
              render: (row) => new Date(row.createdAt).toLocaleString('zh-CN'),
            },
          ]}
          empty={
            <NoData
              title={`还没有${config.title}`}
              description="创建后将在这里显示，也可以切换资源范围。"
            />
          }
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
          加载更多
        </Button>
      )}
      {creating && (
        <CreateDefinition
          kind={kind}
          groupId={groupId}
          onClose={() => setCreating(false)}
          onCreated={(id) => {
            setCreating(false);
            setOperation(id);
          }}
        />
      )}
      {operation && (
        <OperationDialog id={operation} groupId={groupId} onClose={() => setOperation(undefined)} />
      )}
    </div>
  );
}
const createSchema = z.object({
  name: z.string().min(1, '请输入名称').max(63, '名称最长 63 字符'),
  displayName: z.string().max(128),
  description: z.string().max(1000),
});
type CreateValues = z.infer<typeof createSchema>;
function CreateDefinition({
  kind,
  groupId,
  onClose,
  onCreated,
}: {
  kind: ResourceKind;
  groupId: string;
  onClose: () => void;
  onCreated: (id: string) => void;
}) {
  const client = useQueryClient();
  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<CreateValues>({
    resolver: zodResolver(createSchema),
    defaultValues: { name: '', displayName: '', description: '' },
  });
  const mutation = useMutation({
    mutationFn: (values: CreateValues) =>
      api<Operation>(`/groups/${groupId}/${kind}`, { method: 'POST', body: values }),
    onSuccess: (result) => {
      void client.invalidateQueries({ queryKey: ['resources', groupId, kind] });
      onCreated(result.operationId);
    },
  });
  return (
    <Modal
      open
      title={resourceModules[kind].create}
      onClose={() => {
        if (!mutation.isPending) onClose();
      }}
    >
      <form className="stack" onSubmit={handleSubmit((values) => mutation.mutate(values))}>
        <Input
          label="名称"
          hint={
            kind === 'pipelines'
              ? '小写字母、数字和连字符，最长 63 字符'
              : '字母、数字、点、下划线或连字符'
          }
          {...register('name')}
          error={errors.name?.message}
        />
        <Input label="展示名称" {...register('displayName')} error={errors.displayName?.message} />
        <Input label="描述" {...register('description')} error={errors.description?.message} />
        {mutation.isError && <ErrorState error={mutation.error} />}
        <div className="actions end">
          <Button hierarchy="outline" disabled={mutation.isPending} onClick={onClose}>
            取消
          </Button>
          <Button hierarchy="primary" type="submit" loading={mutation.isPending}>
            创建
          </Button>
        </div>
      </form>
    </Modal>
  );
}
