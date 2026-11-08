import { Link, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Card } from '@atomus';
import { api } from '@/lib/api';
import type { Page, Resource } from '@/lib/types';
import { ErrorState, Loading, NoData, PageHeading, StatusBadge } from '@/components/feedback';
import { resourceModules, type ResourceKind } from './resource-config';

export function ResourceDetailPage({ kind }: { kind: ResourceKind }) {
  const { groupId = '', resourceId = '' } = useParams();
  const query = useQuery({
    queryKey: ['resources', groupId, kind, resourceId],
    queryFn: ({ signal }) => api<Resource>(`/groups/${groupId}/${kind}/${resourceId}`, { signal }),
    refetchInterval: kind === 'kserve-services' ? 5000 : false,
  });
  if (query.isPending) return <Loading />;
  if (query.isError)
    return (
      <ErrorState
        error={query.error}
        retry={() => {
          void query.refetch();
        }}
      />
    );
  const row = query.data;
  return (
    <div className="stack spacious">
      <Link className="text-link" to={`/g/${groupId}/${kind}`}>
        ← 返回{resourceModules[kind].title}
      </Link>
      <PageHeading title={row.displayName || row.name} description={row.description || row.name} />
      <Card title="基本信息">
        <dl className="details-grid">
          <dt>名称</dt>
          <dd>{row.name}</dd>
          <dt>修订</dt>
          <dd>r{row.revision}</dd>
          <dt>创建时间</dt>
          <dd>{new Date(row.createdAt).toLocaleString('zh-CN')}</dd>
          {row.status && (
            <>
              <dt>状态</dt>
              <dd>
                <StatusBadge status={row.status} />
              </dd>
            </>
          )}
          {kind === 'kserve-services' && (
            <>
              <dt>服务地址</dt>
              <dd className="break-word">
                {typeof row.kserveUrl === 'string' ? row.kserveUrl : '尚未就绪'}
              </dd>
              <dt>状态说明</dt>
              <dd>{typeof row.kserveReason === 'string' ? row.kserveReason : '—'}</dd>
              <dt>最近同步</dt>
              <dd>
                {typeof row.kserveSyncedAt === 'string'
                  ? new Date(row.kserveSyncedAt).toLocaleString('zh-CN')
                  : '尚未同步'}
              </dd>
            </>
          )}
        </dl>
      </Card>
      {kind !== 'kserve-services' && (
        <Versions
          key={`${groupId}/${kind}/${resourceId}`}
          groupId={groupId}
          kind={kind}
          id={resourceId}
        />
      )}
    </div>
  );
}
function Versions({ groupId, kind, id }: { groupId: string; kind: ResourceKind; id: string }) {
  const query = useQuery({
    queryKey: ['resources', groupId, kind, id, 'versions'],
    queryFn: ({ signal }) =>
      api<Page<Resource>>(`/groups/${groupId}/${kind}/${id}/versions?limit=200`, { signal }),
  });
  return (
    <Card title="版本" supportingText="版本按创建顺序展示，最多显示前 200 个。">
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorState
          error={query.error}
          retry={() => {
            void query.refetch();
          }}
        />
      ) : query.data.items.length === 0 ? (
        <NoData title="还没有版本" description="登记或导入版本后将在这里显示。" />
      ) : (
        <div className="version-list">
          {query.data.items.map((version) => (
            <article key={version.id} className="version-row">
              <strong>{version.name}</strong>
              <span className="muted">r{version.revision ?? 1}</span>
              {version.status && <StatusBadge status={version.status} />}
            </article>
          ))}
        </div>
      )}
    </Card>
  );
}
