import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Button, Table } from '@atomus';
import { NoData, StatusBadge } from '@/components/feedback';
import { pipelineApi, pipelineKeys, pollPipeline } from '../api';
import { formatTime } from '../policy';
import { QueryState } from './query-state';

export function RunsTable({
  groupId,
  pipelineId,
  scope,
}: {
  groupId: string;
  pipelineId?: string;
  scope: string;
}) {
  const [after, setAfter] = useState(0);
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'runs', pipelineId, scope, after),
    queryFn: ({ signal }) => pipelineApi.runs(groupId, pipelineId, scope, after, signal),
    refetchInterval: pollPipeline,
    retry: false,
  });
  return (
    <QueryState query={query}>
      {(page) => (
        <div className="stack">
          <Table
            rows={page.items ?? []}
            rowKey={(row) => String(row.id)}
            caption="流水线运行记录"
            empty={<NoData title="还没有运行" description="选择流水线版本并提交一次运行。" />}
            columns={[
              {
                key: 'name',
                header: '运行',
                render: (row) => (
                  <Link className="text-link" to={`/workspaces/${groupId}/pipeline-runs/${row.id}`}>
                    {row.name}
                  </Link>
                ),
              },
              { key: 'engine', header: '引擎' },
              {
                key: 'versionId',
                header: '版本',
                render: (row) =>
                  `#${row.versionId} · r${row.versionRevision ?? row.spec.versionRevision ?? '—'}`,
              },
              {
                key: 'status',
                header: '状态',
                render: (row) => <StatusBadge status={row.status} />,
              },
              { key: 'createdAt', header: '创建时间', render: (row) => formatTime(row.createdAt) },
            ]}
          />
          <PageControls after={after} next={page.nextAfter} onChange={setAfter} />
        </div>
      )}
    </QueryState>
  );
}

export function VersionsTable({
  groupId,
  pipelineId,
  canRun,
}: {
  groupId: string;
  pipelineId: string;
  canRun: boolean;
}) {
  const [after, setAfter] = useState(0);
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'versions', pipelineId, after),
    queryFn: ({ signal }) => pipelineApi.versions(groupId, pipelineId, after, signal),
    retry: false,
  });
  return (
    <QueryState query={query}>
      {(page) => (
        <div className="stack">
          <Table
            rows={page.items ?? []}
            rowKey={(row) => String(row.id)}
            caption="流水线版本"
            empty={<NoData title="还没有版本" description="导入 Python SDK 编译生成的 IR YAML。" />}
            columns={[
              {
                key: 'name',
                header: '版本',
                render: (row) => (
                  <Link
                    className="text-link"
                    to={`/workspaces/${groupId}/pipelines/${pipelineId}/versions/${row.id}`}
                  >
                    {row.name}
                  </Link>
                ),
              },
              { key: 'revision', header: '修订', render: (row) => `r${row.revision}` },
              {
                key: 'digest',
                header: '包摘要',
                render: (row) => <span className="mono break-word">{row.spec.packageDigest}</span>,
              },
              { key: 'createdAt', header: '创建时间', render: (row) => formatTime(row.createdAt) },
              {
                key: 'run',
                header: '操作',
                render: (row) =>
                  canRun && (
                    <Link
                      className="text-link"
                      to={`/workspaces/${groupId}/pipelines/${pipelineId}/run?version=${row.id}`}
                    >
                      提交运行
                    </Link>
                  ),
              },
            ]}
          />
          <PageControls after={after} next={page.nextAfter} onChange={setAfter} />
        </div>
      )}
    </QueryState>
  );
}

export function PageControls({
  after,
  next,
  onChange,
}: {
  after: number;
  next: number | null;
  onChange: (value: number) => void;
}) {
  return (
    <div className="actions end">
      {after > 0 && (
        <Button hierarchy="outline" size="sm" onClick={() => onChange(0)}>
          返回首页
        </Button>
      )}
      {next !== null && next !== undefined && next !== after && (
        <Button hierarchy="outline" size="sm" onClick={() => onChange(next)}>
          下一页
        </Button>
      )}
    </div>
  );
}
