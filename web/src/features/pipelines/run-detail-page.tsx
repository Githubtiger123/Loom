import { useState } from 'react';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Tabs } from '@atomus';
import { ErrorState, Loading, PageHeading, StatusBadge } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { pipelineApi, pipelineKeys, pollPipeline, noAccess } from './api';
import { canStop, formatTime, usePipelineAccess } from './policy';
import type { Run } from './types';
import { QueryState } from './components/query-state';
import { DagPanel } from './components/dag-panel';
import { VisualizationsPanel } from './components/visualizations-panel';
import { LogsPanel } from './components/logs-panel';
import { StopRunDialog } from './components/stop-run-dialog';
import { RunActions } from './components/run-actions';
import { RuntimePanel } from './components/runtime-panel';
import './pipelines.css';

export function PipelineRunDetailPage() {
  const { groupId = '', runId = '' } = useParams();
  const access = usePipelineAccess(groupId);
  const { operation, setOperation } = useOperationNavigation();
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'run', runId),
    queryFn: ({ signal }) => pipelineApi.run(groupId, runId, signal),
    enabled: access.can('pipeline.read'),
    refetchInterval: pollPipeline,
    retry: false,
  });
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
    <>
      <QueryState query={query}>
        {(run) =>
          access.allows('pipeline.read', run) ? (
            <RunContent
              key={`${groupId}/${run.id}/${run.instanceId}`}
              groupId={groupId}
              run={run}
              onOperation={setOperation}
              canLogs={access.allows('pipeline.logs', run)}
              canStopRun={access.allows('pipeline.stop', run)}
              onRefresh={() => {
                void query.refetch();
              }}
              refreshing={query.isFetching}
            />
          ) : (
            <ErrorState error={noAccess()} />
          )
        }
      </QueryState>
      {operation && (
        <OperationDialog id={operation} groupId={groupId} onClose={() => setOperation(undefined)} />
      )}
    </>
  );
}

function RunContent({
  groupId,
  run,
  onOperation,
  canLogs,
  canStopRun,
  onRefresh,
  refreshing,
}: {
  groupId: string;
  run: Run;
  onOperation: (id: string) => void;
  canLogs: boolean;
  canStopRun: boolean;
  onRefresh: () => void;
  refreshing: boolean;
}) {
  const [search, setSearch] = useSearchParams();
  const [stopping, setStopping] = useState(false);
  const tabs = [
    { value: 'overview', label: '概览' },
    { value: 'dag', label: 'DAG 与输入输出' },
    { value: 'artifacts', label: '指标与产物' },
    { value: 'diagnostics', label: '运行诊断' },
    ...(canLogs ? [{ value: 'logs', label: '运行日志' }] : []),
  ];
  const requested = search.get('tab') || 'overview';
  const tab = tabs.some((item) => item.value === requested) ? requested : 'overview';
  const panelKey = `${run.instanceId}/${run.revision}/${tab}`;
  return (
    <div className="stack spacious">
      <Link className="text-link" to={`/workspaces/${groupId}/pipelines/${run.pipelineId}`}>
        ← 返回流水线
      </Link>
      <PageHeading
        eyebrow="PIPELINE RUN"
        title={run.name}
        description={`${run.engine} · ${run.namespace || '尚未分配执行空间'}`}
        actions={
          <>
            <Button hierarchy="outline" loading={refreshing} onClick={onRefresh}>
              刷新状态
            </Button>
            {canStopRun && canStop(run) && (
              <Button hierarchy="outline" onClick={() => setStopping(true)}>
                停止运行
              </Button>
            )}
            <RunActions groupId={groupId} run={run} onAccepted={onOperation} />
          </>
        }
      />
      <div className="actions">
        <StatusBadge status={run.status} />
        <span className="muted small">
          观测：{run.observedState} · {formatTime(run.observedAt)}
        </span>
        {run.operationId && (
          <Button hierarchy="link" size="sm" onClick={() => onOperation(run.operationId!)}>
            查看操作进度
          </Button>
        )}
      </div>
      {run.statusReason && (
        <Alert color="warning" title="运行状态说明">
          {run.statusReason}
        </Alert>
      )}
      {!!run.pendingSpec && (
        <Alert color="warning" title="正在准备新配置">
          后台需要先停止并清理旧执行资源，完成后才能手动启动。请查看操作进度，不能将已受理当成清理完成。
        </Alert>
      )}
      <Tabs
        aria-label="运行详情"
        items={tabs}
        value={tab}
        onChange={(value) => setSearch({ tab: value })}
      />
      {tab === 'overview' && <RunOverview run={run} groupId={groupId} />}
      {tab === 'dag' && <DagPanel key={panelKey} groupId={groupId} run={run} />}
      {tab === 'artifacts' && <VisualizationsPanel key={panelKey} groupId={groupId} run={run} />}
      {tab === 'diagnostics' && (
        <RuntimePanel key={panelKey} groupId={groupId} run={run} onOperation={onOperation} />
      )}
      {tab === 'logs' && canLogs && <LogsPanel key={panelKey} groupId={groupId} run={run} />}
      {stopping && canStopRun && (
        <StopRunDialog
          groupId={groupId}
          run={run}
          onClose={() => setStopping(false)}
          onAccepted={(id) => {
            setStopping(false);
            onOperation(id);
          }}
        />
      )}
    </div>
  );
}

function RunOverview({ run, groupId }: { run: Run; groupId: string }) {
  return (
    <div className="stack">
      <Card title="执行快照">
        <dl className="details-grid">
          <dt>版本</dt>
          <dd>
            <Link
              className="text-link"
              to={`/workspaces/${groupId}/pipelines/${run.pipelineId}/versions/${run.versionId}`}
            >
              #{run.versionId}
            </Link>{' '}
            · 固定修订 r{run.versionRevision ?? run.spec.versionRevision ?? '—'}
          </dd>
          <dt>实例 ID</dt>
          <dd className="mono break-word">{run.instanceId}</dd>
          <dt>集群 / 命名空间</dt>
          <dd>
            {run.cluster} / {run.namespace || '—'}
          </dd>
          <dt>包 SHA256</dt>
          <dd className="mono break-word">{run.spec.packageDigest}</dd>
          <dt>业务修订</dt>
          <dd>r{run.revision}</dd>
          <dt>期望 / 观测</dt>
          <dd>
            {run.desiredState} / {run.observedState}
          </dd>
          <dt>创建时间</dt>
          <dd>{formatTime(run.createdAt)}</dd>
          <dt>执行清理</dt>
          <dd>{run.executionCleaned ? '已清理（可查已归档日志）' : '未清理'}</dd>
        </dl>
      </Card>
      <Card title="运行输入参数">
        <div className="stack">
          {run.spec.values?.length ? (
            run.spec.values.map((value) => (
              <div key={value.name}>
                <strong>{value.name}</strong>
                <pre className="pipeline-code">
                  {value.credentialRef ? '[受管凭据引用，不展示内容]' : (value.value ?? '未提供')}
                </pre>
              </div>
            ))
          ) : (
            <p className="muted">未声明或未提供参数。</p>
          )}
        </div>
      </Card>
    </div>
  );
}
