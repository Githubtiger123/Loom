import { useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Select, Tabs, Toggle } from '@atomus';
import { NoData } from '@/components/feedback';
import { ApiError } from '@/lib/api';
import { pipelineApi, pipelineKeys } from '../api';
import type { ArchiveEntry, Manifest, Run } from '../types';
import { formatTime } from '../policy';
import { QueryState } from './query-state';

export function LogsPanel({ groupId, run }: { groupId: string; run: Run }) {
  const [search] = useSearchParams();
  const target = {
    instance: search.get('logInstance') || run.instanceId,
    task: search.get('logTask') || '',
    podUID: search.get('logPodUID') || '',
  };
  const [tab, setTab] = useState(search.get('logSource') === 'archive' ? 'archive' : 'live');
  return (
    <div className="stack">
      <Tabs
        aria-label="日志来源"
        value={tab}
        onChange={setTab}
        items={[
          { value: 'live', label: '在线日志' },
          { value: 'archive', label: '离线归档' },
        ]}
      />
      {tab === 'live' ? (
        run.executionCleaned || run.deletedAt ? (
          <NoData title="执行已清理" description="请切换到离线归档查看历史日志。" />
        ) : (
          <LiveLogs
            groupId={groupId}
            run={run}
            targetTask={target.task}
            targetInstance={target.instance}
          />
        )
      ) : (
        <ArchiveAttempts groupId={groupId} run={run} target={target} />
      )}
    </div>
  );
}

function LiveLogs({
  groupId,
  run,
  targetTask,
  targetInstance,
}: {
  groupId: string;
  run: Run;
  targetTask: string;
  targetInstance: string;
}) {
  const [task, setTask] = useState(targetTask);
  const dag = useQuery({
    queryKey: pipelineKeys.query(groupId, 'log-tasks', run.id, run.instanceId, run.revision),
    queryFn: async ({ signal }) => {
      const value = await pipelineApi.dag(groupId, run, signal);
      if (value.instanceId !== run.instanceId || value.revision !== run.revision)
        throw new ApiError(409, 'INSTANCE_CHANGED', '运行实例已变化，请刷新详情');
      return value;
    },
    retry: false,
    gcTime: 0,
  });
  return (
    <Card
      title="在线容器日志"
      supportingText="最近 500 行、最多 256 KiB 的快照，可每 5 秒刷新；不是持久流。"
      headerAction={
        <Button
          hierarchy="outline"
          size="sm"
          loading={dag.isFetching}
          onClick={() => {
            void dag.refetch();
          }}
        >
          刷新节点
        </Button>
      }
    >
      <QueryState query={dag}>
        {(value) => {
          if (targetInstance !== run.instanceId)
            return (
              <NoData
                title="链接指向历史执行实例"
                description="在线日志只读取当前实例；请切换离线归档查看该历史实例，不自动切换到当前任务。"
              />
            );
          const nodes = (value.instances ?? []).filter((node) => node.taskId && node.podName);
          const selected = task ? nodes.find((node) => node.taskId === task) : nodes[0];
          if (!selected && task)
            return (
              <div className="stack">
                <NoData
                  title="指定任务没有当前在线 Pod"
                  description="不会自动切换到其他任务的日志。可切换离线归档查询精确历史 Pod；未发布的日志可能尚不可读。"
                />
                <Button hierarchy="outline" onClick={() => setTask('')}>
                  清除任务定位
                </Button>
              </div>
            );
          return nodes.length && selected?.taskId ? (
            <div className="stack">
              <Select
                label="实际任务实例"
                value={selected.taskId}
                onChange={setTask}
                options={nodes.map((node) => ({
                  value: node.taskId!,
                  label: `${node.name || node.nodeId} · ${node.podName}`,
                }))}
              />
              <LiveLogContent
                key={selected.taskId}
                groupId={groupId}
                run={run}
                task={selected.taskId}
              />
            </div>
          ) : (
            <NoData
              title="暂无可读取日志的 Pod"
              description="缓存命中、跳过节点和 DAG 分组不会创建工作 Pod。可以刷新节点或查看离线归档。"
            />
          );
        }}
      </QueryState>
    </Card>
  );
}

function LiveLogContent({ groupId, run, task }: { groupId: string; run: Run; task: string }) {
  const [previous, setPrevious] = useState(false);
  const [polling, setPolling] = useState(false);
  const query = useQuery({
    queryKey: pipelineKeys.query(
      groupId,
      'live-log',
      run.id,
      run.instanceId,
      run.revision,
      task,
      previous,
    ),
    queryFn: ({ signal }) => pipelineApi.logs(groupId, run, task, previous, signal),
    retry: false,
    gcTime: 0,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchInterval: (current) => (polling && !current.state.error ? 5000 : false),
  });
  return (
    <div className="stack">
      <div className="actions">
        <Toggle
          label="上一次容器日志"
          checked={previous}
          onChange={(event) => setPrevious(event.target.checked)}
        />
        <Toggle
          label="每 5 秒刷新"
          checked={polling}
          onChange={(event) => setPolling(event.target.checked)}
        />
        <Button
          hierarchy="outline"
          size="sm"
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          读取日志
        </Button>
      </div>
      <QueryState query={query}>
        {(log) => (
          <>
            <p className="muted small">
              {log.pod} / {log.container} · {formatTime(log.observedAt)}
            </p>
            {log.truncated && (
              <Alert color="warning" title="日志快照已截断">
                可查看归档获取其他日志区段。
              </Alert>
            )}
            {log.text ? (
              <pre className="pipeline-code pipeline-logs" tabIndex={0} aria-label="在线容器日志">
                {log.text}
              </pre>
            ) : (
              <NoData title="当前范围暂无日志" />
            )}
          </>
        )}
      </QueryState>
    </div>
  );
}

type LogTarget = { instance: string; task: string; podUID: string };
function ArchiveAttempts({
  groupId,
  run,
  target,
}: {
  groupId: string;
  run: Run;
  target: LogTarget;
}) {
  const [after, setAfter] = useState(0);
  const [instance, setInstance] = useState(target.instance);
  const attempts = useQuery({
    queryKey: pipelineKeys.query(groupId, 'attempts', run.id, run.instanceId, run.revision, after),
    queryFn: ({ signal }) => pipelineApi.attempts(groupId, run, after, signal),
    retry: false,
    gcTime: 0,
  });
  return (
    <Card
      title="离线日志归档"
      supportingText="从后端归档存储分块读取；Pod 被删除后仍可查看已成功归档的内容。"
    >
      <QueryState query={attempts}>
        {(items) => {
          const options = [
            { value: run.instanceId, label: `当前实例 · ${run.instanceId}` },
            ...items
              .filter((item) => item.instanceId !== run.instanceId)
              .map((item) => ({
                value: item.instanceId,
                label: `r${item.revision} · ${formatTime(item.createdAt)} · ${item.phase ?? '历史执行'}`,
              })),
          ];
          if (
            target.instance !== run.instanceId &&
            !options.some((option) => option.value === target.instance)
          )
            options.push({
              value: target.instance,
              label: `链接指定的历史实例 · ${target.instance}`,
            });
          const selected = options.some((option) => option.value === instance)
            ? instance
            : run.instanceId;
          return (
            <div className="stack">
              <Select label="执行实例" value={selected} onChange={setInstance} options={options} />
              <div className="actions">
                {after > 0 && (
                  <Button hierarchy="outline" size="sm" onClick={() => setAfter(0)}>
                    历史首页
                  </Button>
                )}
                {items.length === 50 && (
                  <Button
                    hierarchy="outline"
                    size="sm"
                    onClick={() => setAfter(items[items.length - 1].revision)}
                  >
                    下一页执行记录
                  </Button>
                )}
              </div>
              <ArchiveManifest
                key={selected}
                groupId={groupId}
                run={run}
                instance={selected}
                target={selected === target.instance ? target : undefined}
              />
            </div>
          );
        }}
      </QueryState>
    </Card>
  );
}

function ArchiveManifest({
  groupId,
  run,
  instance,
  target,
}: {
  groupId: string;
  run: Run;
  instance: string;
  target?: LogTarget;
}) {
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'log-manifest', run.id, run.revision, instance),
    queryFn: async ({ signal }) => {
      const manifest = await pipelineApi.archives(groupId, run, instance, signal);
      if (manifest.instanceId !== instance)
        throw new ApiError(409, 'ARCHIVE_CHANGED', '归档实例不一致，请重新加载');
      return manifest;
    },
    gcTime: 0,
    retry: false,
  });
  return (
    <div className="stack">
      <div className="actions">
        <Button
          hierarchy="outline"
          size="sm"
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新归档清单
        </Button>
      </div>
      <QueryState query={query}>
        {(manifest) => (
          <ArchiveFiles
            key={`${target?.task}/${target?.podUID}`}
            groupId={groupId}
            run={run}
            manifest={manifest}
            target={target}
          />
        )}
      </QueryState>
    </div>
  );
}

function ArchiveFiles({
  groupId,
  run,
  manifest,
  target,
}: {
  groupId: string;
  run: Run;
  manifest: Manifest;
  target?: LogTarget;
}) {
  const [entryId, setEntryId] = useState('');
  const [focused, setFocused] = useState(!!(target?.task || target?.podUID));
  const entries = (manifest.entries ?? []).filter(
    (entry) =>
      !focused ||
      ((!target?.task || entry.taskId === target.task) &&
        (!target?.podUID || entry.podUID === target.podUID)),
  );
  const entry = entries.find((item) => item.id === entryId) ?? entries[0];
  return (
    <div className="stack">
      {focused && (
        <div className="stack">
          <p className="small break-word">
            精确定位：{target?.task || '任务未指定'}
            {target?.podUID ? ` · Pod UID ${target.podUID}` : ''}
          </p>
          <Button
            hierarchy="link"
            size="sm"
            onClick={() => {
              setFocused(false);
              setEntryId('');
            }}
          >
            取消定位，查看此实例全部归档
          </Button>
        </div>
      )}
      <p className="small muted">
        来源：{manifest.source} ·{' '}
        {manifest.complete ? '清单标记为归档完成' : '归档尚未完成或来源未提供完整性标记'}
      </p>
      {entry ? (
        <>
          <Select
            label="归档文件"
            value={entry.id}
            onChange={setEntryId}
            options={entries.map((item) => ({
              value: item.id,
              label: `${item.pod} / ${item.container} · ${item.sizeBytes.toLocaleString()} B`,
            }))}
          />
          <ArchiveChunk
            key={`${manifest.instanceId}/${entry.id}/${entry.etag}`}
            groupId={groupId}
            run={run}
            instance={manifest.instanceId}
            entry={entry}
          />
        </>
      ) : (
        <NoData
          title={focused ? '指定任务／Pod 暂无已发布归档' : '暂无归档文件'}
          description="可能仍在执行、归档未启用或上传尚未完成，不能据此认定没有日志；不会自动展示其他 Pod 的日志。"
        />
      )}
    </div>
  );
}

function ArchiveChunk({
  groupId,
  run,
  instance,
  entry,
}: {
  groupId: string;
  run: Run;
  instance: string;
  entry: ArchiveEntry;
}) {
  const [offsets, setOffsets] = useState([0]);
  const offset = offsets[offsets.length - 1];
  const query = useQuery({
    queryKey: pipelineKeys.query(
      groupId,
      'log-chunk',
      run.id,
      run.revision,
      instance,
      entry.id,
      entry.etag,
      offset,
    ),
    queryFn: async ({ signal }) => {
      const chunk = await pipelineApi.chunk(
        groupId,
        run,
        instance,
        entry.id,
        entry.etag,
        offset,
        signal,
      );
      if (
        chunk.instanceId !== instance ||
        chunk.entryId !== entry.id ||
        chunk.etag !== entry.etag ||
        chunk.offset !== offset ||
        !Number.isSafeInteger(chunk.nextOffset) ||
        chunk.nextOffset < offset
      )
        throw new ApiError(409, 'ARCHIVE_CHANGED', '日志归档身份或偏移已变化，请刷新清单');
      return chunk;
    },
    retry: false,
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
  return (
    <QueryState query={query}>
      {(chunk) => (
        <div className="stack">
          <p className="small muted">
            字节 {chunk.offset.toLocaleString()}–{chunk.nextOffset.toLocaleString()} /{' '}
            {chunk.sizeBytes.toLocaleString()}
            {chunk.eof ? ' · 已到文件末尾' : ''}
          </p>
          <pre className="pipeline-code pipeline-logs" tabIndex={0} aria-label="离线日志区段">
            {chunk.text || '此区段没有日志内容。'}
          </pre>
          <div className="actions">
            <Button
              hierarchy="outline"
              size="sm"
              disabled={offsets.length <= 1}
              onClick={() => setOffsets((current) => current.slice(0, -1))}
            >
              上一段
            </Button>
            <Button
              hierarchy="outline"
              size="sm"
              disabled={chunk.eof || chunk.nextOffset <= offset}
              onClick={() => setOffsets((current) => [...current, chunk.nextOffset])}
            >
              下一段
            </Button>
          </div>
        </div>
      )}
    </QueryState>
  );
}
