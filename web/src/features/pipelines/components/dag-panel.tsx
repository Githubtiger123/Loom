import { useId, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Button, Card, Select, Table } from '@atomus';
import { NoData, StatusBadge } from '@/components/feedback';
import { ApiError } from '@/lib/api';
import { pipelineApi, pipelineKeys, pollPipeline } from '../api';
import type { DAG, Definition, Edge, NodeIO, Run } from '../types';
import { QueryState } from './query-state';
import { ArtifactAccess } from './visualizations-panel';
import { formatTime } from '../policy';

export function DagPanel({ groupId, run }: { groupId: string; run: Run }) {
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'dag', run.id, run.instanceId, run.revision),
    queryFn: async ({ signal }) => {
      const dag = await pipelineApi.dag(groupId, run, signal);
      if (dag.instanceId !== run.instanceId || dag.revision !== run.revision)
        throw new ApiError(409, 'INSTANCE_CHANGED', '运行已变化，请刷新运行详情');
      return dag;
    },
    retry: false,
    gcTime: 0,
    refetchInterval: pollPipeline,
  });
  return (
    <Card
      title="执行 DAG"
      supportingText="定义依赖与实际执行实例分开展示；循环和重试可对应多个实例。"
      headerAction={
        <Button
          hierarchy="outline"
          size="sm"
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新
        </Button>
      }
    >
      <QueryState query={query}>
        {(dag) => <DagContent groupId={groupId} run={run} dag={dag} />}
      </QueryState>
    </Card>
  );
}

function DagContent({ groupId, run, dag }: { groupId: string; run: Run; dag: DAG }) {
  const [scope, setScope] = useState('root');
  const [node, setNode] = useState('');
  const [definition, setDefinition] = useState('');
  const definitions = dag.definitions ?? [];
  const scopes = definitions.filter((d) =>
    definitions.some((child) => child.parentNodeId === d.nodeId),
  );
  const currentScope = scopes.some((d) => d.nodeId === scope) ? scope : (scopes[0]?.nodeId ?? '');
  const visible = definitions.filter((d) => d.parentNodeId === currentScope);
  const edges = (dag.edges ?? []).filter((e) => e.scopeNodeId === currentScope);
  const instances = (dag.instances ?? []).filter(
    (item) => !definition || item.definitionNodeId === definition,
  );
  const activeNode =
    node && [...definitions, ...(dag.instances ?? [])].some((item) => item.nodeId === node)
      ? node
      : '';
  return (
    <div className="stack">
      <p className="small muted">
        运行数据：{dag.runtime.status} · 元数据：{dag.metadata.status} · 读取于{' '}
        {formatTime(dag.readAt)}
      </p>
      {scopes.length > 0 && (
        <Select
          label="DAG 层级"
          value={currentScope}
          onChange={(value) => {
            setScope(value);
            setDefinition('');
            setNode('');
          }}
          options={scopes.map((d) => ({ value: d.nodeId, label: d.displayName || d.taskName }))}
        />
      )}
      {visible.length ? (
        <DefinitionGraph
          definitions={visible}
          edges={edges}
          selected={definition}
          onSelect={(id) => {
            setDefinition(id);
            setNode(id);
          }}
        />
      ) : (
        <NoData title="此层级暂无定义节点" />
      )}
      <details>
        <summary>依赖与边界端口（{edges.length}）</summary>
        <ul className="pipeline-edges">
          {edges.map((e, i) => (
            <li key={i}>
              <code>
                {e.sourceNodeId}
                {e.sourcePort ? `:${e.sourcePort}` : ''}
              </code>{' '}
              →{' '}
              <code>
                {e.targetNodeId}
                {e.targetPort ? `:${e.targetPort}` : ''}
              </code>{' '}
              · {e.kind}
              {e.boundary ? ` · ${e.boundary}` : ''}
            </li>
          ))}
        </ul>
      </details>
      <div className="actions">
        <h3>实际执行实例</h3>
        {definition && (
          <Button hierarchy="link" size="sm" onClick={() => setDefinition('')}>
            显示全部节点实例
          </Button>
        )}
      </div>
      <Table
        rows={instances}
        rowKey={(row) => row.nodeId}
        caption="实际节点、循环迭代和重试实例"
        empty={
          <NoData title="还没有执行实例" description="任务未被调度、被跳过或运行状态尚未同步。" />
        }
        columns={[
          {
            key: 'name',
            header: '节点',
            render: (row) => (
              <Button hierarchy="link" size="sm" onClick={() => setNode(row.nodeId)}>
                {row.name || row.nodeId}
              </Button>
            ),
            supporting: (row) => row.nodeId,
          },
          { key: 'state', header: '状态', render: (row) => <StatusBadge status={row.state} /> },
          {
            key: 'iteration',
            header: '迭代',
            render: (row) =>
              row.iterationIndex === undefined ? '—' : JSON.stringify(row.iterationIndex),
          },
          {
            key: 'mapping',
            header: '定义映射',
            render: (row) => (row.mapping === 'EXACT' ? '精确匹配' : '尚未解析'),
          },
          {
            key: 'podName',
            header: 'Pod',
            render: (row) =>
              row.podName || (row.cachedExecutionId ? '缓存命中（不创建 Pod）' : '—'),
          },
        ]}
      />
      {activeNode && <NodePanel key={activeNode} groupId={groupId} run={run} node={activeNode} />}
    </div>
  );
}

// Topological columns are for definition edges only. Runtime rows are never
// matched by display name or multiplied into invented loop dependencies.
function graphLayout(definitions: Definition[], edges: Edge[]) {
  const ids = new Set(definitions.map((d) => d.nodeId));
  const links = edges.filter(
    (e) => !e.boundary && ids.has(e.sourceNodeId) && ids.has(e.targetNodeId),
  );
  const ranks = new Map<string, number>();
  for (let pass = 0; pass < definitions.length; pass++) {
    let changed = false;
    for (const d of definitions) {
      if (ranks.has(d.nodeId)) continue;
      const parents = links.filter((e) => e.targetNodeId === d.nodeId).map((e) => e.sourceNodeId);
      if (parents.every((id) => ranks.has(id))) {
        ranks.set(
          d.nodeId,
          parents.length ? Math.max(...parents.map((id) => ranks.get(id) ?? 0)) + 1 : 0,
        );
        changed = true;
      }
    }
    if (!changed) break;
  }
  const rows = new Map<number, number>();
  const positions = new Map<string, { x: number; y: number }>();
  for (const d of definitions) {
    const rank = ranks.get(d.nodeId) ?? 0;
    const row = rows.get(rank) ?? 0;
    positions.set(d.nodeId, { x: 20 + rank * 250, y: 20 + row * 110 });
    rows.set(rank, row + 1);
  }
  return {
    positions,
    links,
    width: Math.max(500, ...[...positions.values()].map((p) => p.x + 230)),
    height: Math.max(140, ...[...positions.values()].map((p) => p.y + 100)),
  };
}

function DefinitionGraph({
  definitions,
  edges,
  selected,
  onSelect,
}: {
  definitions: Definition[];
  edges: Edge[];
  selected: string;
  onSelect: (id: string) => void;
}) {
  const marker = useId().replace(/:/g, '');
  const layout = graphLayout(definitions, edges);
  return (
    <div className="pipeline-graph-scroll" tabIndex={0} aria-label="可横向滚动的定义 DAG">
      <div className="pipeline-graph" style={{ width: layout.width, height: layout.height }}>
        <svg width={layout.width} height={layout.height} aria-hidden="true">
          <defs>
            <marker
              id={marker}
              viewBox="0 0 10 10"
              refX="9"
              refY="5"
              markerWidth="6"
              markerHeight="6"
              orient="auto-start-reverse"
            >
              <path d="M 0 0 L 10 5 L 0 10 z" />
            </marker>
          </defs>
          {layout.links.map((e, i) => {
            const a = layout.positions.get(e.sourceNodeId)!;
            const b = layout.positions.get(e.targetNodeId)!;
            return (
              <path
                key={i}
                className={`pipeline-edge pipeline-edge-${e.kind.toLowerCase()}`}
                d={`M${a.x + 210},${a.y + 40} C${a.x + 230},${a.y + 40} ${b.x - 20},${b.y + 40} ${b.x},${b.y + 40}`}
                markerEnd={`url(#${marker})`}
              />
            );
          })}
        </svg>
        {definitions.map((d) => (
          <button
            type="button"
            key={d.nodeId}
            className="pipeline-graph-node"
            aria-pressed={selected === d.nodeId}
            style={{
              left: layout.positions.get(d.nodeId)!.x,
              top: layout.positions.get(d.nodeId)!.y,
            }}
            onClick={() => onSelect(d.nodeId)}
          >
            <strong>{d.displayName || d.taskName}</strong>
            <span className="small muted">
              {d.kind}
              {d.parameterIterator ? ' · 循环' : ''}
              {d.triggerPolicy ? ' · 条件' : ''}
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}

function NodePanel({ groupId, run, node }: { groupId: string; run: Run; node: string }) {
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'node', run.id, run.instanceId, run.revision, node),
    queryFn: ({ signal }) => pipelineApi.node(groupId, run, node, signal),
    retry: false,
    gcTime: 0,
    refetchInterval: pollPipeline,
  });
  return (
    <Card title="节点详情">
      <QueryState query={query}>
        {(detail) => (
          <div className="stack">
            <div className="actions">
              <h3>{detail.instance?.name || detail.definition?.displayName || node}</h3>
              {detail.instance && <StatusBadge status={detail.instance.state} />}
            </div>
            {detail.instance && (
              <dl className="details-grid">
                <dt>父实例</dt>
                <dd className="break-word">{detail.instance.parentNodeId || '—'}</dd>
                <dt>缓存来源</dt>
                <dd>{detail.instance.cachedExecutionId || '—'}</dd>
                <dt>开始 / 结束</dt>
                <dd>
                  {formatTime(detail.instance.startTime)} / {formatTime(detail.instance.endTime)}
                </dd>
              </dl>
            )}
            {detail.instance?.error !== undefined && (
              <pre className="pipeline-code">{JSON.stringify(detail.instance.error, null, 2)}</pre>
            )}
            {!!detail.instance?.retries?.length && (
              <details>
                <summary>历史重试 Pod（{detail.instance.retries.length}）</summary>
                <ul>
                  {detail.instance.retries.map((attempt) => (
                    <li key={attempt.podUID} className="break-word">
                      #{attempt.epoch + 1} · {attempt.podName} · {attempt.state}
                      {attempt.publicationReplayed ? ' · 复用已提交结果，未重复运行组件' : ''}
                    </li>
                  ))}
                </ul>
                <p className="muted small">已发布的历史 Pod 日志可在离线日志面板查看。</p>
              </details>
            )}
            <div className="pipeline-columns">
              <NodeIOPanel title="输入" io={detail.inputs} groupId={groupId} run={run} />
              <NodeIOPanel title="输出" io={detail.outputs} groupId={groupId} run={run} />
            </div>
            {detail.definition && (
              <details>
                <summary>条件、循环、重试和缓存配置</summary>
                <pre className="pipeline-code">
                  {JSON.stringify(
                    {
                      triggerPolicy: detail.definition.triggerPolicy,
                      parameterIterator: detail.definition.parameterIterator,
                      retryPolicy: detail.definition.retryPolicy,
                      cachingOptions: detail.definition.cachingOptions,
                    },
                    null,
                    2,
                  )}
                </pre>
              </details>
            )}
          </div>
        )}
      </QueryState>
    </Card>
  );
}

function NodeIOPanel({
  title,
  io,
  groupId,
  run,
}: {
  title: string;
  io: NodeIO;
  groupId: string;
  run: Run;
}) {
  return (
    <section className="stack">
      <h3>{title}</h3>
      <p className="small muted">
        参数：{io.parametersStatus} · 产物：{io.artifactsStatus}
      </p>
      {Object.entries(io.parameters ?? {}).map(([name, value]) => (
        <div key={name}>
          <strong>{name}</strong>
          <span className="small muted"> · {value.source}</span>
          <pre className="pipeline-code">
            {value.available
              ? JSON.stringify(value.value, null, 2)
              : '尚未解析（不是空字符串或零）'}
          </pre>
        </div>
      ))}
      {Object.entries(io.artifacts ?? {}).map(([name, artifacts]) => (
        <div className="stack" key={name}>
          <strong>{name}</strong>
          {artifacts.map((artifact) => (
            <Card
              key={artifact.artifactId}
              title={`产物 ${artifact.artifactId}`}
              supportingText={artifact.status}
            >
              <ArtifactAccess
                groupId={groupId}
                run={run}
                artifact={artifact.artifactId}
                enabled={artifact.status === 'AVAILABLE'}
              />
            </Card>
          ))}
        </div>
      ))}
      {!Object.keys(io.parameters ?? {}).length && !Object.keys(io.artifacts ?? {}).length && (
        <p className="muted">暂无输入输出记录。</p>
      )}
    </section>
  );
}
