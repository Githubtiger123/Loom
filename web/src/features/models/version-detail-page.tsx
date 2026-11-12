import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, Select, Table } from '@atomus';
import type { Operation } from '@/lib/types';
import { api } from '@/lib/api';
import { ErrorState, NoData, PageHeading, StatusBadge } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { registryApi, modelKeys, modelPath, noAccess } from './api';
import { activeModel, activeVersion, publishable } from './policy';
import type { Model, Version } from './types';
import { ModelBoundary, type Access } from './components/boundary';
import { PageControls, QueryState } from './components/query-state';
import { RegistryActionDialog, type RegistryAction } from './components/registry-action';

export function ModelVersionPage() {
  const { modelId = '', versionId = '' } = useParams();
  return (
    <ModelBoundary>
      {(g, group, access) => (
        <VersionDetail
          key={`${g}/${modelId}/${versionId}`}
          g={g}
          m={modelId}
          v={versionId}
          access={access}
          writable={group.status === 'ACTIVE'}
        />
      )}
    </ModelBoundary>
  );
}
function VersionDetail({
  g,
  m,
  v,
  access,
  writable,
}: {
  g: string;
  m: string;
  v: string;
  access: Access;
  writable: boolean;
}) {
  const parent = useQuery({
    queryKey: modelKeys.query(g, 'model', m),
    queryFn: ({ signal }) => registryApi.model(g, m, signal),
    retry: false,
    gcTime: 0,
  });
  const version = useQuery({
    queryKey: modelKeys.query(g, 'version', m, v),
    queryFn: ({ signal }) => registryApi.version(g, m, v, signal),
    retry: false,
    gcTime: 0,
    enabled: parent.isSuccess && access.allows('model.read', parent.data),
  });
  const { operation, setOperation } = useOperationNavigation();
  return (
    <QueryState query={parent}>
      {(model) =>
        !access.allows('model.read', model) ? (
          <ErrorState error={noAccess()} />
        ) : (
          <QueryState query={version}>
            {(row) => (
              <div className="stack spacious">
                <VersionSummary g={g} model={model} row={row} access={access} writable={writable} />
                {row.importOperationId && (
                  <ImportProgress g={g} model={model} row={row} access={access} />
                )}
                <ArtifactList
                  key={`${g}/${m}/${v}`}
                  g={g}
                  model={model}
                  row={row}
                  access={access}
                  writable={writable}
                />
                {operation && (
                  <OperationDialog
                    id={operation}
                    groupId={g}
                    onClose={() => setOperation(undefined)}
                  />
                )}
              </div>
            )}
          </QueryState>
        )
      }
    </QueryState>
  );
}
function VersionSummary({
  g,
  model,
  row,
  access,
  writable,
}: {
  g: string;
  model: Model;
  row: Version;
  access: Access;
  writable: boolean;
}) {
  const [action, setAction] = useState<RegistryAction>();
  const base = `/workspaces/${g}/models/${model.id}/versions/${row.id}`;
  const active = activeModel(model) && activeVersion(row);
  return (
    <>
      <PageHeading
        eyebrow="MODEL VERSION"
        title={`${model.displayName || model.name} / ${row.name}`}
        description={
          row.description || '先登记有效产物，再标记 READY；READY 不是实际加载验收结论。'
        }
        actions={
          <div className="actions">
            <Link className="text-link" to={`/workspaces/${g}/models/${model.id}`}>
              返回模型
            </Link>
            {active && writable && access.allows('model.artifact.create', model) && (
              <Link className="text-link" to={`${base}/artifacts/new`}>
                登记文件地址
              </Link>
            )}
            {active && writable && access.allows('model.version.update', model) && (
              <>
                <Link className="text-link" to={`${base}/edit`}>
                  编辑描述
                </Link>
                <Button
                  hierarchy="outline"
                  onClick={() =>
                    setAction({
                      title: row.status === 'READY' ? '改为草稿' : '标记 READY',
                      description:
                        'READY 要求至少一项有效产物；存储权限和模型兼容性需要发布后验证。',
                      path: modelPath(g, model.id, row.id),
                      method: 'PATCH',
                      body: {
                        revision: row.revision,
                        status: row.status === 'READY' ? 'DRAFT' : 'READY',
                      },
                      destination: base,
                    })
                  }
                >
                  {row.status === 'READY' ? '改为草稿' : '标记 READY'}
                </Button>
              </>
            )}
            {active && access.allows('model.archive', model) && (
              <Button
                hierarchy="outline"
                onClick={() =>
                  setAction({
                    title: '归档版本',
                    description: '版本及其产物元数据将一起归档。',
                    path: `${modelPath(g, model.id, row.id)}/archive`,
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
      <Card title="版本信息">
        <dl className="details-grid">
          <dt>状态</dt>
          <dd>
            <StatusBadge status={row.status} />
          </dd>
          <dt>修订</dt>
          <dd>r{row.revision}</dd>
          <dt>来源运行</dt>
          <dd>
            {row.sourcePipelineRunId ? (
              <Link
                className="text-link"
                to={`/workspaces/${g}/pipeline-runs/${row.sourcePipelineRunId}`}
              >
                Pipeline #{row.sourcePipelineRunId}
              </Link>
            ) : (
              '手动登记'
            )}
          </dd>
          {row.sourcePipelineArtifactId && (
            <>
              <dt>来源产物 / 执行实例</dt>
              <dd className="mono break-word">
                #{row.sourcePipelineArtifactId} / {row.sourcePipelineInstanceId}
              </dd>
            </>
          )}
        </dl>
      </Card>
      {action &&
        access.allows(action.destructive ? 'model.archive' : 'model.version.update', model) && (
          <RegistryActionDialog groupId={g} action={action} close={() => setAction(undefined)} />
        )}
    </>
  );
}
function ImportProgress({
  g,
  model,
  row,
  access,
}: {
  g: string;
  model: Model;
  row: Version;
  access: Access;
}) {
  const client = useQueryClient();
  const [action, setAction] = useState<RegistryAction>();
  const id = row.importOperationId!;
  const query = useQuery({
    queryKey: modelKeys.query(g, 'import-operation', row.id, row.revision, id),
    queryFn: ({ signal }) => api<Operation>(`/operations/${encodeURIComponent(id)}`, { signal }),
    retry: false,
    gcTime: 0,
    refetchInterval: (q) =>
      !q.state.error &&
      ['PENDING', 'RUNNING', 'RETRYING'].includes(q.state.data?.status ?? 'PENDING')
        ? 3000
        : false,
  });
  const status = query.isSuccess ? query.data.status : undefined;
  useEffect(() => {
    if (status === 'SUCCEEDED' && row.revision === 1)
      void client.invalidateQueries({ queryKey: modelKeys.all(g) });
  }, [client, g, status, row.revision]);
  return (
    <QueryState query={query}>
      {(operation) => (
        <Card title="训练产物注册任务">
          <div className="stack">
            <StatusBadge status={operation.status} />
            <p>{operation.step}</p>
            {operation.lastError && <ErrorState error={new Error(operation.lastError)} />}
            {operation.status === 'SUCCEEDED' ? (
              <Alert color="success" title="模型文件已转换并登记">
                若产物列表尚未更新，请刷新页面；版本仍需显式标记 READY。
              </Alert>
            ) : (
              <p className="muted">
                导入由 worker 执行；不会把 tar.gz
                当作模型权重。完成前不要编辑此草稿，否则原导入将拒绝覆盖。
              </p>
            )}
            {operation.status === 'FAILED' &&
              row.revision === 1 &&
              !row.deletedAt &&
              activeModel(model) &&
              access.allows('model.version.create', model) &&
              access.can('model.artifact.create') && (
                <Button
                  hierarchy="outline"
                  onClick={() =>
                    setAction({
                      title: '重试产物注册',
                      description: '将复核当前权限和来源，继续同一导入任务。',
                      path: `/operations/${encodeURIComponent(id)}/retry`,
                      body: {},
                      destination: `/workspaces/${g}/models/${model.id}/versions/${row.id}`,
                    })
                  }
                >
                  重试
                </Button>
              )}
            {action &&
              access.allows('model.version.create', model) &&
              access.can('model.artifact.create') && (
                <RegistryActionDialog
                  groupId={g}
                  action={action}
                  close={() => setAction(undefined)}
                />
              )}
          </div>
        </Card>
      )}
    </QueryState>
  );
}
function ArtifactList({
  g,
  model,
  row,
  access,
  writable,
}: {
  g: string;
  model: Model;
  row: Version;
  access: Access;
  writable: boolean;
}) {
  const [history, setHistory] = useState('active');
  return (
    <section className="stack">
      <h2>文件产物</h2>
      <p className="muted small">
        产物地址和摘要不可原地修改；替换文件时请登记新产物并归档旧记录。
      </p>
      <Select
        label="产物范围"
        value={history}
        onChange={setHistory}
        options={[
          { value: 'active', label: '有效产物' },
          { value: 'all', label: '包括已归档' },
        ]}
      />
      <ArtifactRows
        key={history}
        g={g}
        model={model}
        row={row}
        access={access}
        writable={writable}
        archived={history === 'all'}
      />
    </section>
  );
}
function ArtifactRows({
  g,
  model,
  row,
  access,
  writable,
  archived,
}: {
  g: string;
  model: Model;
  row: Version;
  access: Access;
  writable: boolean;
  archived: boolean;
}) {
  const [after, setAfter] = useState(0),
    [action, setAction] = useState<RegistryAction>();
  const query = useQuery({
    queryKey: modelKeys.query(g, 'artifacts', model.id, row.id, row.revision, archived, after),
    queryFn: ({ signal }) =>
      registryApi.artifacts(g, String(model.id), String(row.id), archived, after, signal),
    retry: false,
    gcTime: 0,
  });
  return (
    <QueryState query={query}>
      {(page) => (
        <div className="stack">
          <Table
            caption="模型产物"
            rows={page.items}
            rowKey={(r) => String(r.id)}
            empty={
              <NoData
                title="暂无产物"
                description="登记模型文件目录，或从 Pipeline 运行的产物面板直接注册。"
              />
            }
            columns={[
              {
                key: 'name',
                header: '产物',
                render: (r) => (
                  <details>
                    <summary>{r.name}</summary>
                    <dl className="stack">
                      <dt>存储地址</dt>
                      <dd className="mono break-word">{r.uri}</dd>
                      <dt>SHA256（可选）</dt>
                      <dd className="mono break-word">{r.digest || '未登记 / 目录产物'}</dd>
                      <dt>大小</dt>
                      <dd>
                        {r.sizeBytes === null ? '未登记' : `${r.sizeBytes.toLocaleString()} 字节`}
                      </dd>
                    </dl>
                  </details>
                ),
              },
              {
                key: 'format',
                header: '格式',
                render: (r) => `${r.modelFormat} ${r.modelFormatVersion}`,
              },
              { key: 'status', header: '状态', render: (r) => <StatusBadge status={r.status} /> },
              {
                key: 'actions',
                header: '操作',
                render: (r) => (
                  <div className="actions">
                    {writable && access.can('kserve.create') && publishable(model, row, r) && (
                      <Link
                        className="text-link"
                        to={`/workspaces/${g}/kserve-services/new?modelId=${model.id}&versionId=${row.id}&artifactId=${r.id}`}
                      >
                        发布服务
                      </Link>
                    )}
                    {activeModel(model) &&
                      activeVersion(row) &&
                      r.status === 'ACTIVE' &&
                      access.allows('model.archive', model) && (
                        <Button
                          hierarchy="outline"
                          size="sm"
                          onClick={() =>
                            setAction({
                              title: '归档产物',
                              description: '若归档最后一个有效产物，READY 版本会自动回到 DRAFT。',
                              path: `${modelPath(g, model.id, row.id)}/artifacts/${r.id}/archive`,
                              body: { revision: r.revision },
                              destination: `/workspaces/${g}/models/${model.id}/versions/${row.id}`,
                              destructive: true,
                            })
                          }
                        >
                          归档
                        </Button>
                      )}
                  </div>
                ),
              },
            ]}
          />
          <PageControls after={after} next={page.nextAfter} onChange={setAfter} />
          {action && access.allows('model.archive', model) && (
            <RegistryActionDialog groupId={g} action={action} close={() => setAction(undefined)} />
          )}
        </div>
      )}
    </QueryState>
  );
}
