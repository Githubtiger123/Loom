import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Select, Table } from '@atomus';
import { NoData, PageHeading, StatusBadge } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { registryApi, servingKeys } from './api';
import { ModelBoundary } from './components/boundary';
import { PageControls, QueryState } from './components/query-state';
export function ServingPage() {
  return (
    <ModelBoundary permission="kserve.read">
      {(g, group, access) => (
        <ServingList
          key={g}
          g={g}
          canCreate={access.can('kserve.create') && group.status === 'ACTIVE'}
          readAny={access.can('kserve.read.any')}
        />
      )}
    </ModelBoundary>
  );
}
function ServingList({
  g,
  canCreate,
  readAny,
}: {
  g: string;
  canCreate: boolean;
  readAny: boolean;
}) {
  const [scope, setScope] = useState('mine');
  const { operation, setOperation } = useOperationNavigation();
  return (
    <div className="stack spacious">
      <PageHeading
        eyebrow="MODEL SERVING"
        title="推理服务"
        description="KServe 管理运行资源，Loom 展示期望、观测状态与发布历史。"
        actions={
          canCreate && (
            <Link className="text-link" to={`/workspaces/${g}/models`}>
              从模型版本发布
            </Link>
          )
        }
      />
      {readAny && (
        <Select
          label="范围"
          value={scope}
          onChange={setScope}
          options={[
            { value: 'mine', label: '我创建的' },
            { value: 'all', label: '全部可见' },
          ]}
        />
      )}
      <ServiceTable key={readAny ? scope : 'mine'} g={g} scope={readAny ? scope : 'mine'} />
      {operation && (
        <OperationDialog id={operation} groupId={g} onClose={() => setOperation(undefined)} />
      )}
    </div>
  );
}
function ServiceTable({ g, scope }: { g: string; scope: string }) {
  const [after, setAfter] = useState(0);
  const query = useQuery({
    queryKey: servingKeys.query(g, 'list', scope, after),
    queryFn: ({ signal }) => registryApi.services(g, scope, after, signal),
    retry: false,
    gcTime: 0,
    refetchInterval: (q) => (q.state.error ? false : 5000),
  });
  return (
    <QueryState query={query}>
      {(page) => (
        <div className="stack">
          <Table
            caption="推理服务"
            rows={page.items}
            rowKey={(r) => String(r.id)}
            empty={
              <NoData title="尚无推理服务" description="到 READY 模型版本选择有效产物创建发布。" />
            }
            columns={[
              {
                key: 'name',
                header: '名称',
                render: (r) => (
                  <Link className="text-link" to={`/workspaces/${g}/kserve-services/${r.id}`}>
                    {r.name}
                  </Link>
                ),
              },
              {
                key: 'status',
                header: '业务状态',
                render: (r) => <StatusBadge status={r.status} />,
              },
              {
                key: 'observedState',
                header: '观测状态',
                render: (r) => <StatusBadge status={r.observedState} />,
              },
              {
                key: 'ready',
                header: 'KServe 就绪',
                render: (r) =>
                  r.kserveReady && r.desiredState === 'SERVING' && r.appliedRevision > 0
                    ? 'Ready'
                    : '未就绪',
              },
              {
                key: 'revision',
                header: '修订',
                render: (r) => `r${r.revision} / 已应用 r${r.appliedRevision}`,
              },
            ]}
          />
          <PageControls after={after} next={page.nextAfter} onChange={setAfter} />
        </div>
      )}
    </QueryState>
  );
}
