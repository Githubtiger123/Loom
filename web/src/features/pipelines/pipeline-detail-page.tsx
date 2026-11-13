import { useState } from 'react';
import { Link, useOutletContext, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Tabs } from '@atomus';
import type { Group } from '@/lib/types';
import { ErrorState, Loading, PageHeading } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { pipelineApi, pipelineKeys, noAccess } from './api';
import { usePipelineAccess } from './policy';
import { QueryState } from './components/query-state';
import { RunsTable, VersionsTable } from './components/catalog-tables';
import { VersionSummary } from './components/version-summary';
import { LifecycleDialog } from './components/lifecycle-dialog';
import './pipelines.css';

export function PipelineDetailPage() {
  const { groupId = '', pipelineId = '' } = useParams();
  const group = useOutletContext<Group>();
  const access = usePipelineAccess(groupId);
  const [tab, setTab] = useState('versions');
  const [archiving, setArchiving] = useState(false);
  const { operation, setOperation } = useOperationNavigation();
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'definition', pipelineId),
    queryFn: ({ signal }) => pipelineApi.detail(groupId, pipelineId, signal),
    enabled: access.can('pipeline.read'),
    retry: false,
  });
  if (access.isPending) return <Loading />;
  if (access.isError) return <ErrorState error={access.error} />;
  if (!access.can('pipeline.read')) return <ErrorState error={noAccess()} />;
  return (
    <QueryState query={query}>
      {(row) =>
        access.allows('pipeline.read', row) ? (
          <div className="stack spacious">
            <Link className="text-link" to={`/workspaces/${groupId}/pipelines`}>
              ← 返回流水线
            </Link>
            <PageHeading
              eyebrow={`PIPELINE · r${row.revision}`}
              title={row.displayName || row.name}
              description={row.description || row.name}
              actions={
                !row.deletedAt &&
                group.status === 'ACTIVE' && (
                  <>
                    {access.allows('pipeline.version.create', row) && (
                      <Link
                        className="text-link"
                        to={`/workspaces/${groupId}/pipelines/${pipelineId}/versions/new`}
                      >
                        导入版本
                      </Link>
                    )}
                    {access.allows('pipeline.run', row) && (
                      <Link
                        className="text-link"
                        to={`/workspaces/${groupId}/pipelines/${pipelineId}/run`}
                      >
                        提交运行
                      </Link>
                    )}
                    {access.allows('pipeline.delete', row) && (
                      <Button hierarchy="outline" onClick={() => setArchiving(true)}>
                        归档定义
                      </Button>
                    )}
                  </>
                )
              }
            />
            {row.deletedAt && (
              <Alert color="warning" title="定义已归档">
                仅保留版本和运行历史，不可再导入、编辑版本或提交运行。
              </Alert>
            )}
            <Tabs
              aria-label="流水线详情"
              value={tab}
              onChange={setTab}
              items={[
                { value: 'versions', label: '版本' },
                { value: 'runs', label: '运行记录' },
              ]}
            />
            {tab === 'versions' ? (
              <VersionsTable
                groupId={groupId}
                pipelineId={pipelineId}
                canRun={
                  !row.deletedAt && group.status === 'ACTIVE' && access.allows('pipeline.run', row)
                }
              />
            ) : (
              <RunsTable
                key={`${pipelineId}/${access.can('pipeline.read.any')}`}
                groupId={groupId}
                pipelineId={pipelineId}
                scope={access.can('pipeline.read.any') ? 'all' : 'mine'}
              />
            )}
            {operation && (
              <OperationDialog
                id={operation}
                groupId={groupId}
                onClose={() => setOperation(undefined)}
              />
            )}
            {archiving && access.allows('pipeline.delete', row) && (
              <LifecycleDialog
                groupId={groupId}
                target={{ kind: 'archive', row }}
                active={group.status === 'ACTIVE'}
                onClose={() => setArchiving(false)}
                onAccepted={(id) => {
                  setArchiving(false);
                  setOperation(id);
                }}
              />
            )}
          </div>
        ) : (
          <ErrorState error={noAccess()} />
        )
      }
    </QueryState>
  );
}

export function PipelineVersionPage() {
  const { groupId = '', pipelineId = '', versionId = '' } = useParams();
  const access = usePipelineAccess(groupId);
  const group = useOutletContext<Group>();
  const { operation, setOperation } = useOperationNavigation();
  const parent = useQuery({
    queryKey: pipelineKeys.query(groupId, 'definition', pipelineId),
    queryFn: ({ signal }) => pipelineApi.detail(groupId, pipelineId, signal),
    enabled: access.can('pipeline.read'),
    retry: false,
  });
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'version', pipelineId, versionId),
    queryFn: ({ signal }) => pipelineApi.version(groupId, pipelineId, versionId, signal),
    enabled: access.can('pipeline.read'),
    retry: false,
  });
  if (access.isPending) return <Loading />;
  if (access.isError) return <ErrorState error={access.error} />;
  if (!access.can('pipeline.read')) return <ErrorState error={noAccess()} />;
  return (
    <QueryState query={query}>
      {(version) => (
        <QueryState query={parent}>
          {(row) =>
            access.allows('pipeline.read', row) ? (
              <div className="stack spacious">
                <Link className="text-link" to={`/workspaces/${groupId}/pipelines/${pipelineId}`}>
                  ← 返回流水线
                </Link>
                <PageHeading
                  title={version.name}
                  eyebrow={`VERSION · r${version.revision}`}
                  description="提交运行会固定此版本及修订，后续版本修改不会改变已有运行。"
                  actions={
                    !row.deletedAt &&
                    group.status === 'ACTIVE' &&
                    access.allows('pipeline.version.update', row) && (
                      <Link
                        className="text-link"
                        to={`/workspaces/${groupId}/pipelines/${pipelineId}/versions/${versionId}/edit`}
                      >
                        编辑版本
                      </Link>
                    )
                  }
                />
                <VersionSummary
                  key={`${version.id}/${version.revision}`}
                  groupId={groupId}
                  pipelineId={pipelineId}
                  version={version}
                />
                <Card title="Pipeline IR">
                  <pre className="pipeline-code">
                    {JSON.stringify(
                      version.spec.pipelineSpec ?? { packageRef: version.spec.packageRef },
                      null,
                      2,
                    )}
                  </pre>
                </Card>
                {operation && (
                  <OperationDialog
                    id={operation}
                    groupId={groupId}
                    onClose={() => setOperation(undefined)}
                  />
                )}
              </div>
            ) : (
              <ErrorState error={noAccess()} />
            )
          }
        </QueryState>
      )}
    </QueryState>
  );
}
