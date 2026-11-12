import { useQuery } from '@tanstack/react-query';
import { useOutletContext } from 'react-router-dom';
import { Alert, Button, Select } from '@atomus';
import { ErrorState, Loading } from '@/components/feedback';
import type { Group } from '@/lib/types';
import { registryApi, servingKeys } from '../api';
import { runtimeMismatch } from '../policy';
import type { Artifact } from '../types';

export function RuntimeSelect({
  g,
  userId,
  artifact,
  protocol,
  value,
  onChange,
}: {
  g: string;
  userId?: number;
  artifact: Artifact;
  protocol: string;
  value: string;
  onChange: (value: string) => void;
}) {
  const group = useOutletContext<Group>();
  const query = useQuery({
    queryKey: servingKeys.query(g, 'runtimes', group.revision, group.infrastructureOwner, userId),
    queryFn: ({ signal }) => registryApi.runtimes(g, signal),
    retry: false,
    gcTime: 0,
  });
  if (query.isError)
    return (
      <ErrorState
        error={query.error}
        retry={() => {
          void query.refetch();
        }}
      />
    );
  if (!query.isSuccess) return <Loading label="发现 ServingRuntime…" />;
  const items = query.data.items;
  // Namespace runtimes shadow cluster runtimes for explicit name selection.
  const effective = items.filter(
    (r) =>
      r.scope === 'NAMESPACE' || !items.some((n) => n.scope === 'NAMESPACE' && n.name === r.name),
  );
  const mismatch = (r: (typeof items)[number]) =>
    runtimeMismatch(r, artifact.modelFormat, artifact.modelFormatVersion, protocol);
  const selected = effective.find((r) => r.name === value);
  const problem = value ? (selected ? mismatch(selected) : '当前 Runtime 已不存在或不可见') : '';
  const automatic = items.filter(
    (r) =>
      !mismatch(r) &&
      r.formats.some(
        (f) =>
          f.name === artifact.modelFormat &&
          (!artifact.modelFormatVersion || f.version === artifact.modelFormatVersion) &&
          f.autoSelect,
      ),
  );
  const options = [
    { value: '', label: '自动选择（由 KServe 控制器决定）' },
    ...effective.map((r) => ({
      value: r.name,
      label: `${r.name} · ${r.scope === 'NAMESPACE' ? '空间' : '集群'}${mismatch(r) ? ` · ${mismatch(r)}` : ''}`,
      disabled: !!mismatch(r),
    })),
  ];
  if (value && !selected) options.push({ value, label: `${value} · 不可用`, disabled: true });
  return (
    <div className="stack">
      <Select
        label="ServingRuntime"
        value={value}
        onChange={onChange}
        options={options}
        hint="按产物格式、版本和协议匹配；禁用、多模型、多节点 Runtime 不可选。"
        error={problem || undefined}
      />
      {problem && (
        <Alert color="warning" title="原 Runtime 选择已保留">
          {problem}。请显式重新选择，不会自动替换已有配置。
        </Alert>
      )}
      {!value && !automatic.length && (
        <Alert color="warning" title="未发现可自动匹配的 Runtime">
          可选择支持此产物的显式 Runtime，或联系管理员安装；最终以发布前检查和官方控制器为准。
        </Alert>
      )}
      <div className="actions">
        <Button
          type="button"
          hierarchy="outline"
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新 Runtime
        </Button>
        <span className="muted small">
          {query.data.namespace} · {new Date(query.data.observedAt).toLocaleString()}
        </span>
      </div>
    </div>
  );
}
