import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Table } from '@atomus';
import { ApiError } from '@/lib/api';
import { NoData, StatusBadge } from '@/components/feedback';
import { pipelineApi, pipelineKeys, pollPipeline } from '../api';
import { formatTime } from '../policy';
import type { Run } from '../types';
import { QueryState } from './query-state';

export function RuntimePanel({
  groupId,
  run,
  onOperation,
}: {
  groupId: string;
  run: Run;
  onOperation: (id: string) => void;
}) {
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'runtime', run.id, run.instanceId, run.revision),
    queryFn: async ({ signal }) => {
      const runtime = await pipelineApi.runtime(groupId, run, signal);
      if (runtime.instanceId !== run.instanceId || runtime.revision !== run.revision)
        throw new ApiError(409, 'INSTANCE_CHANGED', '执行实例或修订已变化，请刷新运行详情');
      return runtime;
    },
    retry: false,
    gcTime: 0,
    refetchInterval: pollPipeline,
  });
  return (
    <div className="stack">
      <Card
        title="运行诊断"
        supportingText="来自已授权的执行状态与持久记录；不是完整集群健康检查。只有实际发布的输出才可视为产物。"
        headerAction={
          <Button
            hierarchy="outline"
            size="sm"
            loading={query.isFetching}
            onClick={() => {
              void query.refetch();
            }}
          >
            刷新诊断
          </Button>
        }
      >
        <dl className="details-grid">
          <dt>业务 / 期望 / 观测</dt>
          <dd>
            {run.status} / {run.desiredState} / {run.observedState}
          </dd>
          <dt>最近观测</dt>
          <dd>{formatTime(run.observedAt)}</dd>
          <dt>执行资源清理</dt>
          <dd>{run.executionCleaned ? '已确认清理' : '未确认清理'}</dd>
        </dl>
        {run.statusReason && (
          <Alert color="warning" title="控制面状态原因">
            {run.statusReason}
          </Alert>
        )}
        {!!run.pendingSpec && (
          <Alert color="warning" title="新配置等待应用">
            后台尚未完成停止／旧实例清理；此时不能启动新实例。请查看操作进度。
          </Alert>
        )}
        {run.operationId && (
          <Button hierarchy="link" onClick={() => onOperation(run.operationId!)}>
            查看操作与失败原因
          </Button>
        )}
        <QueryState query={query}>
          {(runtime) => (
            <div className="stack">
              <dl className="details-grid">
                <dt>执行引擎</dt>
                <dd>{run.engine}</dd>
                <dt>执行实例</dt>
                <dd className="mono break-word">{runtime.instanceId}</dd>
                <dt>外部执行 ID</dt>
                <dd className="mono break-word">{runtime.externalId || '尚未绑定'}</dd>
                <dt>执行状态</dt>
                <dd>
                  <StatusBadge status={runtime.state} />
                </dd>
                <dt>创建 / 结束</dt>
                <dd>
                  {formatTime(runtime.createdAt)} / {formatTime(runtime.finishedAt)}
                </dd>
                {runtime.diagnostics && (
                  <>
                    <dt>官方引擎版本</dt>
                    <dd className="mono break-word">
                      {runtime.diagnostics.engineRelease || '历史记录未提供'}
                    </dd>
                    <dt>Workflow</dt>
                    <dd className="break-word">{runtime.diagnostics.workflowName || '尚未绑定'}</dd>
                    <dt>Workflow UID</dt>
                    <dd className="mono break-word">
                      {runtime.diagnostics.workflowUID || '尚未绑定'}
                    </dd>
                    <dt>冻结计划摘要</dt>
                    <dd className="mono break-word">{runtime.diagnostics.planDigest || '—'}</dd>
                    <dt>持久观测阶段</dt>
                    <dd>{runtime.diagnostics.phase || '尚未观测'}</dd>
                    <dt>同步尝试 / 成功观测</dt>
                    <dd>
                      {formatTime(runtime.diagnostics.syncAttemptedAt)} /{' '}
                      {formatTime(runtime.diagnostics.observedAt)}
                    </dd>
                    <dt>终态证据归档</dt>
                    <dd>
                      {runtime.diagnostics.archivedAt
                        ? formatTime(runtime.diagnostics.archivedAt)
                        : '尚未归档（不代表日志已丢失或任务失败）'}
                    </dd>
                  </>
                )}
              </dl>
              {runtime.diagnostics?.reason && (
                <Alert color="warning" title="执行观测原因">
                  {runtime.diagnostics.reason}
                </Alert>
              )}
              {runtime.diagnostics?.syncReason && (
                <Alert color="warning" title="同步异常">
                  {runtime.diagnostics.syncReason}
                </Alert>
              )}
              {!runtime.tasks?.length ? (
                <NoData
                  title="尚无任务执行记录"
                  description="可能仍在提交、driver 初始化或状态同步阶段；请结合操作进度判断，不据此认定成功或失败。"
                />
              ) : (
                <Table
                  rows={runtime.tasks}
                  rowKey={(task) => task.taskId}
                  caption="已观测任务与 Pod"
                  columns={[
                    { key: 'name', header: '任务', render: (task) => task.name || task.taskId },
                    {
                      key: 'state',
                      header: '状态',
                      render: (task) => <StatusBadge status={task.state} />,
                    },
                    {
                      key: 'podName',
                      header: 'Pod',
                      render: (task) => task.podName || '—（分组／跳过／缓存可能无 Pod）',
                    },
                    { key: 'executionId', header: '执行 ID' },
                    {
                      key: 'startTime',
                      header: '开始',
                      render: (task) => formatTime(task.startTime),
                    },
                    { key: 'endTime', header: '结束', render: (task) => formatTime(task.endTime) },
                  ]}
                />
              )}
              <p className="muted small">
                组件输出错误可在 DAG 节点详情、运行日志查看。镜像拉取／调度等 Kubernetes
                事件当前未由此接口提供，不会推测具体失败原因。
              </p>
            </div>
          )}
        </QueryState>
      </Card>
    </div>
  );
}
