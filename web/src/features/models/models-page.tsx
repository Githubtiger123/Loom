import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Select, Table } from '@atomus';
import { NoData, PageHeading, StatusBadge } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { registryApi, modelKeys } from './api';
import { ModelBoundary } from './components/boundary';
import { PageControls, QueryState } from './components/query-state';

export function ModelsPage() {
  return (
    <ModelBoundary>
      {(g, group, access) => (
        <ModelList
          key={g}
          groupId={g}
          create={group.status === 'ACTIVE' && access.can('model.create')}
          readAny={access.can('model.read.any')}
        />
      )}
    </ModelBoundary>
  );
}
function ModelList({
  groupId: g,
  create,
  readAny,
}: {
  groupId: string;
  create: boolean;
  readAny: boolean;
}) {
  const [scope, setScope] = useState('mine'),
    [archived, setArchived] = useState('active');
  const { operation, setOperation } = useOperationNavigation();
  return (
    <div className="stack spacious">
      <PageHeading
        eyebrow="MODEL REGISTRY"
        title="模型管理"
        description="登记模型、管理版本与产物，再发布为推理服务。"
        actions={
          create && (
            <Link className="text-link" to={`/workspaces/${g}/models/new`}>
              登记模型
            </Link>
          )
        }
      />
      <div className="actions">
        {readAny && (
          <Select
            label="可见范围"
            value={scope}
            onChange={setScope}
            options={[
              { value: 'mine', label: '我创建的' },
              { value: 'all', label: '全部可见' },
            ]}
          />
        )}
        <Select
          label="历史记录"
          value={archived}
          onChange={setArchived}
          options={[
            { value: 'active', label: '有效模型' },
            { value: 'all', label: '包括已归档' },
          ]}
        />
      </div>
      <ModelTable
        key={`${g}/${readAny ? scope : 'mine'}/${archived}`}
        groupId={g}
        scope={readAny ? scope : 'mine'}
        archived={archived === 'all'}
      />
      {operation && (
        <OperationDialog id={operation} groupId={g} onClose={() => setOperation(undefined)} />
      )}
    </div>
  );
}
function ModelTable({
  groupId: g,
  scope,
  archived,
}: {
  groupId: string;
  scope: string;
  archived: boolean;
}) {
  const [after, setAfter] = useState(0);
  const query = useQuery({
    queryKey: modelKeys.query(g, 'list', scope, archived, after),
    queryFn: ({ signal }) => registryApi.models(g, scope, archived, after, signal),
    retry: false,
    gcTime: 0,
  });
  return (
    <QueryState query={query}>
      {(page) => (
        <div className="stack">
          <Table
            caption="模型"
            rows={page.items}
            rowKey={(r) => String(r.id)}
            empty={<NoData title="暂无模型" />}
            columns={[
              {
                key: 'name',
                header: '模型',
                render: (r) => (
                  <Link className="text-link" to={`/workspaces/${g}/models/${r.id}`}>
                    {r.displayName || r.name}
                  </Link>
                ),
                supporting: (r) => r.description,
              },
              { key: 'status', header: '状态', render: (r) => <StatusBadge status={r.status} /> },
              { key: 'revision', header: '修订', render: (r) => `r${r.revision}` },
              {
                key: 'createdAt',
                header: '登记时间',
                render: (r) => new Date(r.createdAt).toLocaleString(),
              },
            ]}
          />
          <PageControls after={after} next={page.nextAfter} onChange={setAfter} />
        </div>
      )}
    </QueryState>
  );
}
