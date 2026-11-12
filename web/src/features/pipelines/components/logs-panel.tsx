import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Select, Tabs, Toggle } from '@atomus';
import { NoData } from '@/components/feedback';
import { ApiError } from '@/lib/api';
import { pipelineApi, pipelineKeys } from '../api';
import type { ArchiveEntry, Manifest, Run } from '../types';
import { formatTime } from '../policy';
import { QueryState } from './query-state';

export function LogsPanel({ groupId, run }: { groupId: string; run: Run }) {
  const [tab, setTab] = useState('live');
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
          <LiveLogs groupId={groupId} run={run} />
        )
      ) : (
        <ArchiveAttempts groupId={groupId} run={run} />
      )}
    </div>
  );
}

function LiveLogs({ groupId, run }: { groupId: string; run: Run }) {
  const [task, setTask] = useState('');
  const dag = useQuery({
    queryKey: pipelineKeys.query(groupId, 'log-tasks', run.id, run.instanceId, run.revision),
    queryFn: async ({ signal }) => {
      const value = await pipelineApi.dag(groupId, run, signal);
      if (value.instanceId !== run.instanceId)
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
          const nodes = (value.instances ?? []).filter((node) => node.taskId && node.podName);
          const selected = nodes.find((node) => node.taskId === task) ?? nodes[0];
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

function ArchiveAttempts({ groupId, run }: { groupId: string; run: Run }) {
  const [after, setAfter] = useState(0);
  const [instance, setInstance] = useState(run.instanceId);
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
              <ArchiveManifest key={selected} groupId={groupId} run={run} instance={selected} />
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
}: {
  groupId: string;
  run: Run;
  instance: string;
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
        {(manifest) => <ArchiveFiles groupId={groupId} run={run} manifest={manifest} />}
      </QueryState>
    </div>
  );
}

function ArchiveFiles({
  groupId,
  run,
  manifest,
}: {
  groupId: string;
  run: Run;
  manifest: Manifest;
}) {
  const [entryId, setEntryId] = useState('');
  const entries = manifest.entries ?? [];
  const entry = entries.find((item) => item.id === entryId) ?? entries[0];
  return (
    <div className="stack">
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
          title="暂无归档文件"
          description="可能仍在执行、归档未启用或上传尚未完成，不能据此认定没有日志。"
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
