import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Button, Card, Select, Table } from '@atomus';
import { ErrorState, NoData, PageHeading, StatusBadge } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { registryApi, modelKeys, modelPath, noAccess } from './api';
import { activeModel } from './policy';
import { ModelBoundary, type Access } from './components/boundary';
import { QueryState, PageControls } from './components/query-state';
import { RegistryActionDialog, type RegistryAction } from './components/registry-action';
import type { Model } from './types';

export function ModelDetailPage() {
  const { modelId = '' } = useParams();
  return (
    <ModelBoundary>
      {(g, group, access) => (
        <ModelDetail
          key={`${g}/${modelId}`}
          g={g}
          id={modelId}
          access={access}
          writable={group.status === 'ACTIVE'}
        />
      )}
    </ModelBoundary>
  );
}
function ModelDetail({
  g,
  id,
  access,
  writable,
}: {
  g: string;
  id: string;
  access: Access;
  writable: boolean;
}) {
  const query = useQuery({
    queryKey: modelKeys.query(g, 'model', id),
    queryFn: ({ signal }) => registryApi.model(g, id, signal),
    retry: false,
    gcTime: 0,
  });
  const { operation, setOperation } = useOperationNavigation();
  return (
    <QueryState query={query}>
      {(row) =>
        access.allows('model.read', row) ? (
          <>
            <ModelSummary g={g} row={row} access={access} writable={writable} />
            <VersionsTable g={g} modelId={id} />
            {operation && (
              <OperationDialog id={operation} groupId={g} onClose={() => setOperation(undefined)} />
            )}
          </>
        ) : (
          <ErrorState error={noAccess()} />
        )
      }
    </QueryState>
  );
}
function ModelSummary({
  g,
  row,
  access,
  writable,
}: {
  g: string;
  row: Model;
  access: Access;
  writable: boolean;
}) {
  const [action, setAction] = useState<RegistryAction>();
  const base = `/workspaces/${g}/models/${row.id}`;
  const active = activeModel(row);
  return (
    <div className="stack spacious">
      <PageHeading
        eyebrow="MODEL"
        title={row.displayName || row.name}
        description={row.description || '模型版本和存储产物独立管理。'}
        actions={
          <div className="actions">
            {active && writable && access.allows('model.version.create', row) && (
              <Link className="text-link" to={`${base}/versions/new`}>
                创建版本
              </Link>
            )}
            {active && writable && access.allows('model.update', row) && (
              <Link className="text-link" to={`${base}/edit`}>
                编辑信息
              </Link>
            )}
            {active && access.allows('model.archive', row) && (
              <Button
                hierarchy="outline"
                onClick={() =>
                  setAction({
                    title: '归档模型',
                    description: '模型及其全部版本、产物元数据会一起归档。',
                    path: `${modelPath(g, row.id)}/archive`,
                    body: { revision: row.revision },
                    destination: base,
                    destructive: true,
                  })
                }
              >
                归档
              </Button>
            )}
          </div>
        }
      />
      <Card title="模型信息">
        <dl className="details-grid">
          <dt>标识</dt>
          <dd>{row.name}</dd>
          <dt>状态</dt>
          <dd>
            <StatusBadge status={row.status} />
          </dd>
          <dt>修订</dt>
          <dd>r{row.revision}</dd>
          <dt>归属用户</dt>
          <dd>{row.createdBy ?? '未分配'}</dd>
        </dl>
      </Card>
      {action && access.allows('model.archive', row) && (
        <RegistryActionDialog groupId={g} action={action} close={() => setAction(undefined)} />
      )}
    </div>
  );
}
function VersionsTable({ g, modelId }: { g: string; modelId: string }) {
  const [history, setHistory] = useState('active');
  return (
    <section className="stack">
      <h2>模型版本</h2>
      <Select
        label="版本范围"
        value={history}
        onChange={setHistory}
        options={[
          { value: 'active', label: '有效版本' },
          { value: 'all', label: '包括已归档' },
        ]}
      />
      <VersionRows key={history} g={g} modelId={modelId} archived={history === 'all'} />
    </section>
  );
}
function VersionRows({ g, modelId, archived }: { g: string; modelId: string; archived: boolean }) {
  const [after, setAfter] = useState(0);
  const query = useQuery({
    queryKey: modelKeys.query(g, 'versions', modelId, archived, after),
    queryFn: ({ signal }) => registryApi.versions(g, modelId, archived, after, signal),
    retry: false,
    gcTime: 0,
  });
  return (
    <QueryState query={query}>
      {(page) => (
        <div className="stack">
          <Table
            caption="模型版本"
            rows={page.items}
            rowKey={(r) => String(r.id)}
            empty={<NoData title="尚无版本" />}
            columns={[
              {
                key: 'name',
                header: '版本',
                render: (r) => (
                  <Link
                    className="text-link"
                    to={`/workspaces/${g}/models/${modelId}/versions/${r.id}`}
                  >
                    {r.name}
                  </Link>
                ),
                supporting: (r) => r.description,
              },
              { key: 'status', header: '状态', render: (r) => <StatusBadge status={r.status} /> },
              {
                key: 'source',
                header: '来源',
                render: (r) =>
                  r.sourcePipelineRunId ? `Pipeline #${r.sourcePipelineRunId}` : '手动登记',
              },
              { key: 'revision', header: '修订', render: (r) => `r${r.revision}` },
            ]}
          />
          <PageControls after={after} next={page.nextAfter} onChange={setAfter} />
        </div>
      )}
    </QueryState>
  );
}
