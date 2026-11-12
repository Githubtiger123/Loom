import { useState } from 'react';
import { Link, useOutletContext, useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Select, Table, Tabs } from '@atomus';
import type { Group } from '@/lib/types';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { noAccess, pipelineApi, pipelineKeys } from './api';
import { formatTime, usePipelineAccess } from './policy';
import { QueryState } from './components/query-state';
import { PageControls, RunsTable } from './components/catalog-tables';
import './pipelines.css';

export function PipelinesPage() {
  const { groupId = '' } = useParams();
  const group = useOutletContext<Group>();
  const access = usePipelineAccess(groupId);
  const [search, setSearch] = useSearchParams();
  const [scope, setScope] = useState('mine');
  const { operation, setOperation } = useOperationNavigation();
  const activeScope = scope === 'all' && access.can('pipeline.read.any') ? 'all' : 'mine';
  const tab = search.get('tab') === 'runs' ? 'runs' : 'definitions';
  if (access.isPending) return <Loading />;
  if (access.isError)
    return (
      <ErrorState
        error={access.error}
        retry={() => {
          void access.refetch();
        }}
      />
    );
  if (!access.can('pipeline.read')) return <ErrorState error={noAccess()} />;
  return (
    <div className="stack spacious">
      <PageHeading
        eyebrow="PIPELINES"
        title="流水线"
        description="管理流水线版本，提交运行并查看节点、产物和日志。"
        actions={
          access.can('pipeline.create') &&
          group.status === 'ACTIVE' && (
            <Link className="text-link" to={`/workspaces/${groupId}/pipelines/new`}>
              新建流水线
            </Link>
          )
        }
      />
      <div className="pipeline-toolbar">
        <Tabs
          aria-label="流水线列表"
          items={[
            { value: 'definitions', label: '流水线' },
            { value: 'runs', label: '运行记录' },
          ]}
          value={tab}
          onChange={(value) => setSearch({ tab: value })}
        />
        {access.can('pipeline.read.any') && (
          <Select
            label="可见范围"
            value={activeScope}
            onChange={setScope}
            options={[
              { value: 'mine', label: '我创建的' },
              { value: 'all', label: '空间内全部' },
            ]}
          />
        )}
      </div>
      {tab === 'runs' ? (
        <RunsTable key={`${groupId}/${activeScope}`} groupId={groupId} scope={activeScope} />
      ) : (
        <DefinitionsTable key={`${groupId}/${activeScope}`} groupId={groupId} scope={activeScope} />
      )}
      {operation && (
        <OperationDialog id={operation} groupId={groupId} onClose={() => setOperation(undefined)} />
      )}
    </div>
  );
}

function DefinitionsTable({ groupId, scope }: { groupId: string; scope: string }) {
  const [after, setAfter] = useState(0);
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'list', scope, after),
    queryFn: ({ signal }) => pipelineApi.list(groupId, scope, after, signal),
    retry: false,
  });
  return (
    <QueryState query={query}>
      {(page) => (
        <div className="stack">
          <Table
            rows={page.items ?? []}
            rowKey={(row) => String(row.id)}
            caption="流水线定义"
            empty={<NoData title="还没有流水线" description="先新建流水线，再导入编译好的版本。" />}
            columns={[
              {
                key: 'name',
                header: '流水线',
                render: (row) => (
                  <Link className="text-link" to={`/workspaces/${groupId}/pipelines/${row.id}`}>
                    {row.displayName || row.name}
                  </Link>
                ),
                supporting: (row) => row.description,
              },
              { key: 'revision', header: '修订', render: (row) => `r${row.revision}` },
              { key: 'createdAt', header: '创建时间', render: (row) => formatTime(row.createdAt) },
            ]}
          />
          <PageControls after={after} next={page.nextAfter} onChange={setAfter} />
        </div>
      )}
    </QueryState>
  );
}
