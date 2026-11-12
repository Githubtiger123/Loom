import { useState } from 'react';
import { useOutletContext } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Select } from '@atomus';
import { ErrorState, Loading, NoData } from '@/components/feedback';
import type { Group } from '@/lib/types';
import { registryApi, servingKeys } from '../api';
import type { ServingContainer, ServingPod, ServingService } from '../types';

export function ServingLogPanel({
  g,
  row,
  pod,
  container,
  userId,
}: {
  g: string;
  row: ServingService;
  pod: ServingPod;
  container: ServingContainer;
  userId?: number;
}) {
  const [previous, setPrevious] = useState('false');
  const [tail, setTail] = useState('200');
  const [since, setSince] = useState('3600');
  const query = {
    instanceId: row.instanceId,
    revision: row.revision,
    pod: pod.name,
    podUID: pod.uid,
    container: container.name,
    previous: previous === 'true',
    tailLines: Number(tail),
    sinceSeconds: Number(since),
    limitBytes: 256 * 1024,
  };
  return (
    <Card title="容器日志快照">
      <div className="stack">
        <p className="mono small break-word">
          {pod.name} / {container.name} · Pod UID {pod.uid} · 重启 {container.restarts} 次
        </p>
        <div className="model-fields">
          <Select
            label="容器运行记录"
            value={previous}
            onChange={setPrevious}
            options={[
              { value: 'false', label: '当前容器' },
              { value: 'true', label: '上一次退出的容器' },
            ]}
          />
          <Select
            label="末尾行数"
            value={tail}
            onChange={setTail}
            options={['200', '500', '1000'].map((value) => ({ value, label: `${value} 行` }))}
          />
          <Select
            label="时间范围"
            value={since}
            onChange={setSince}
            options={[
              { value: '300', label: '最近 5 分钟' },
              { value: '900', label: '最近 15 分钟' },
              { value: '3600', label: '最近 1 小时' },
            ]}
          />
        </div>
        {previous === 'true' && (
          <p className="muted small">
            仅读取上一轮容器日志；未重启、日志已清理或初始化容器仅运行一次时可能不存在，不会自动改读当前日志。
          </p>
        )}
        <LogSnapshot
          key={JSON.stringify(query)}
          g={g}
          row={row}
          query={query}
          userId={userId}
          restarts={container.restarts}
        />
      </div>
    </Card>
  );
}

function LogSnapshot({
  g,
  row,
  query: target,
  userId,
  restarts,
}: {
  g: string;
  row: ServingService;
  query: Parameters<typeof registryApi.logs>[2];
  userId?: number;
  restarts: number;
}) {
  const group = useOutletContext<Group>();
  const query = useQuery({
    queryKey: servingKeys.query(
      g,
      'logs',
      group.revision,
      group.infrastructureOwner,
      row.id,
      row.externalId,
      row.appliedRevision,
      userId,
      restarts,
      target,
    ),
    queryFn: async ({ signal }) => {
      const result = await registryApi.logs(g, row.id, target, signal);
      if (
        result.podUID !== target.podUID ||
        result.pod !== target.pod ||
        result.container !== target.container
      )
        throw new Error('日志目标已变化，请重新选择 Pod 和容器');
      return result;
    },
    retry: false,
    gcTime: 0,
    staleTime: 0,
    refetchOnMount: 'always',
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
          刷新日志
        </Button>
        <span className="muted small">按需快照，不持续追踪；最多 256 KiB。</span>
      </div>
      {query.isFetching ? (
        <Loading label="读取容器日志…" />
      ) : query.isError ? (
        <ErrorState error={query.error} />
      ) : query.isSuccess ? (
        <>
          {query.data.truncated && (
            <Alert color="warning" title="日志已截断">
              达到快照上限；可缩短时间范围或在集群侧查询更完整日志。
            </Alert>
          )}
          {query.data.text ? (
            <pre className="model-code" tabIndex={0} aria-label="容器日志">
              {query.data.text}
            </pre>
          ) : (
            <NoData title="此范围内没有日志" description="可扩大时间范围，或核对容器是否已启动。" />
          )}
          <p className="muted small">
            采集时间：{new Date(query.data.observedAt).toLocaleString()}
          </p>
        </>
      ) : (
        <Loading />
      )}
    </div>
  );
}
