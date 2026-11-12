import { useState } from 'react';
import { useOutletContext } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Select, Table, Tabs } from '@atomus';
import { NoData } from '@/components/feedback';
import type { Group } from '@/lib/types';
import { registryApi, servingKeys } from '../api';
import type { ServingDiagnostics as Diagnostics, ServingService } from '../types';
import { QueryState } from './query-state';
import { ServingLogPanel } from './serving-logs';

export function ServingDiagnostics({
  g,
  row,
  userId,
}: {
  g: string;
  row: ServingService;
  userId?: number;
}) {
  const group = useOutletContext<Group>();
  const query = useQuery({
    queryKey: servingKeys.query(
      g,
      'diagnostics',
      group.revision,
      group.infrastructureOwner,
      row.id,
      row.instanceId,
      row.revision,
      row.externalId,
      row.appliedRevision,
      userId,
    ),
    queryFn: async ({ signal }) => {
      const result = await registryApi.diagnostics(g, row.id, signal);
      if (
        result.serviceId !== row.id ||
        result.instanceId !== row.instanceId ||
        result.revision !== row.revision ||
        result.uid !== row.externalId
      )
        throw new Error('服务实例或修订已变化，请刷新服务状态后重试');
      return result;
    },
    retry: false,
    gcTime: 0,
    refetchInterval: (q) => (q.state.error ? false : 10000),
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  return (
    <div className="stack">
      <div className="actions">
        <Button
          hierarchy="outline"
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新诊断与事件
        </Button>
        <span className="muted small">此面板可见时每 10 秒刷新；失败后停止自动刷新。</span>
      </div>
      <QueryState query={query}>
        {(data) => <DiagnosticContent g={g} row={row} data={data} userId={userId} />}
      </QueryState>
    </div>
  );
}

function DiagnosticContent({
  g,
  row,
  data,
  userId,
}: {
  g: string;
  row: ServingService;
  data: Diagnostics;
  userId?: number;
}) {
  const [tab, setTab] = useState('pods');
  const [source, setSource] = useState<{ podUID: string; container: string }>();
  const pod = data.pods.find((p) => p.uid === source?.podUID);
  const container = pod?.containers.find((c) => c.name === source?.container);
  const allowedContainers = (p: Diagnostics['pods'][number]) =>
    p.containers.filter((c) => ['kserve-container', 'storage-initializer'].includes(c.name));
  const options = data.pods.flatMap((p) =>
    allowedContainers(p).map((c) => ({
      value: `${p.uid}/${c.name}`,
      label: `${p.name} / ${c.name}${c.init ? '（模型存储初始化）' : '（模型加载／推理）'}`,
    })),
  );
  const selected = source ? `${source.podUID}/${source.container}` : '';
  if (selected && !options.some((o) => o.value === selected))
    options.push({ value: selected, label: '原日志目标已消失，请重新选择' });
  return (
    <div className="stack">
      <Card title="模型加载状态">
        <dl className="details-grid">
          <dt>观测状态 / Ready</dt>
          <dd>
            {data.state} / {data.ready ? 'True' : 'False'}
          </dd>
          <dt>业务修订 / CR 观测修订</dt>
          <dd>
            r{data.revision} / r{data.appliedRevision}（不代表 rollout 已完成）
          </dd>
          <dt>最近采集</dt>
          <dd>{new Date(data.observedAt).toLocaleString()}</dd>
        </dl>
        {data.reason && (
          <Alert color="warning" title="状态原因">
            {data.reason}
          </Alert>
        )}
        <p className="muted small">
          storage-initializer 负责下载模型；kserve-container 负责模型加载与推理。Completed
          的初始化容器属于正常情况；Ready 不代替真实推理验收。
        </p>
      </Card>
      {data.truncated && (
        <Alert color="warning" title="诊断结果不完整">
          最多返回 20 个 Pod 和 50 条事件；超过读取上限的内容已截断。
        </Alert>
      )}
      <Tabs
        value={tab}
        onChange={setTab}
        items={[
          { value: 'pods', label: 'Pod／模型容器' },
          { value: 'events', label: '事件' },
          { value: 'logs', label: '容器日志' },
        ]}
      />
      {tab === 'pods' &&
        (data.pods.length ? (
          data.pods.map((p) => (
            <Card
              key={p.uid}
              title={p.name}
              supportingText={`${p.phase} · Ready=${p.ready} · UID ${p.uid}`}
            >
              {p.reason && (
                <Alert color="warning" title="Pod 提示">
                  {p.reason}
                </Alert>
              )}
              <Table
                caption="初始化与模型容器"
                rows={p.containers}
                rowKey={(c) => c.name}
                empty={
                  <NoData
                    title="暂未产生容器状态"
                    description="请查看调度事件；容器尚未启动时暂无日志。"
                  />
                }
                columns={[
                  {
                    key: 'name',
                    header: '容器',
                    render: (c) => `${c.name}${c.init ? '（初始化）' : ''}`,
                  },
                  {
                    key: 'state',
                    header: '状态',
                    render: (c) =>
                      `${c.state} · Ready=${c.ready}${c.exitCode !== undefined ? ` · 退出码 ${c.exitCode}` : ''}`,
                  },
                  { key: 'restarts', header: '重启次数', render: (c) => c.restarts },
                  {
                    key: 'reason',
                    header: '原因',
                    render: (c) => <span className="break-word">{c.reason || '—'}</span>,
                  },
                  {
                    key: 'logs',
                    header: '日志',
                    render: (c) =>
                      ['storage-initializer', 'kserve-container'].includes(c.name) && (
                        <Button
                          hierarchy="link"
                          onClick={() => {
                            setSource({ podUID: p.uid, container: c.name });
                            setTab('logs');
                          }}
                        >
                          查看日志
                        </Button>
                      ),
                  },
                ]}
              />
            </Card>
          ))
        ) : (
          <NoData
            title="暂无 predictor Pod"
            description="服务可能仍在创建，或尚未通过 Runtime、准入和调度检查；请查看事件。"
          />
        ))}
      {tab === 'events' && (
        <Table
          caption="受控资源最近事件（按时间倒序）"
          rows={data.events}
          rowKey={(e) => JSON.stringify([e.uid, e.type, e.reason, e.message, e.at])}
          empty={
            <NoData title="暂无事件" description="事件可能尚未产生或已过保留期，不代表发布成功。" />
          }
          columns={[
            { key: 'at', header: '时间', render: (e) => new Date(e.at).toLocaleString() },
            {
              key: 'name',
              header: '对象',
              render: (e) => (
                <span className="break-word">
                  {e.kind} / {e.name}
                </span>
              ),
            },
            {
              key: 'reason',
              header: '类型 / 原因',
              render: (e) => `${e.type} / ${e.reason}（${e.count} 次）`,
            },
            {
              key: 'message',
              header: '消息',
              render: (e) => <span className="break-word">{e.message}</span>,
            },
          ]}
        />
      )}
      {tab === 'logs' && (
        <div className="stack">
          <Select
            label="选择 Pod 与模型容器"
            value={selected}
            onChange={(value) => {
              const target = data.pods
                .flatMap((p) =>
                  allowedContainers(p).map((c) => ({ podUID: p.uid, container: c.name })),
                )
                .find((s) => `${s.podUID}/${s.container}` === value);
              setSource(target);
            }}
            options={[{ value: '', label: '请选择日志目标' }, ...options]}
          />
          {pod && container ? (
            <ServingLogPanel
              key={`${pod.uid}/${container.name}/${container.restarts}`}
              g={g}
              row={row}
              pod={pod}
              container={container}
              userId={userId}
            />
          ) : (
            <NoData
              title={source ? '原日志目标已消失' : '尚未选择日志目标'}
              description="请选择具体 Pod 和容器；不会自动切换到其他副本或读取所有容器日志。"
            />
          )}
        </div>
      )}
    </div>
  );
}
