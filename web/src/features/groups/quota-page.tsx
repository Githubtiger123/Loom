import { useQuery } from '@tanstack/react-query';
import { Link, useOutletContext, useParams } from 'react-router-dom';
import { Alert, Button } from '@atomus';
import { api } from '@/lib/api';
import type { Group, Me } from '@/lib/types';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { ErrorState, Loading, PageHeading } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { groupApi, groupKeys } from './api';
import { canChangeQuota } from './policy';
import { CapabilityBoundary } from './components/capability-boundary';
import { QuotaSummary } from './components/quota-summary';

export function QuotaPage() {
  const { groupId = '' } = useParams();
  return (
    <CapabilityBoundary groupId={groupId} permission="group.quota.read">
      {(allowed) => <QuotaContent groupId={groupId} allowed={allowed} />}
    </CapabilityBoundary>
  );
}
function QuotaContent({ groupId, allowed }: { groupId: string; allowed: boolean }) {
  const group = useOutletContext<Group>();
  const { operation, setOperation } = useOperationNavigation();
  const query = useQuery({
    queryKey: [...groupKeys.quota(groupId), group.revision],
    queryFn: ({ signal }) => groupApi.quota(groupId, signal),
    enabled: allowed,
    refetchInterval: (q) => (q.state.error ? false : 15_000),
  });
  const me = useQuery({
    queryKey: ['me'],
    queryFn: ({ signal }) => api<Me>('/me', { signal }),
    staleTime: 0,
    refetchInterval: 30_000,
  });
  return (
    <>
      <PageHeading
        eyebrow="WORKSPACE QUOTA"
        title="资源配额"
        description="区分集群实际额度、已分配请求、待执行预留和可用余额；不是 CPU / GPU 实时利用率。"
        actions={
          me.isSuccess &&
          me.data.permissions.includes('group.quota.update') &&
          canChangeQuota(group.status) ? (
            <Link className="action-link" to={`/workspaces/${groupId}/quota/edit`}>
              调整配额
            </Link>
          ) : undefined
        }
      />
      {me.isError && (
        <ErrorState
          error={me.error}
          retry={() => {
            void me.refetch();
          }}
        />
      )}
      <Alert color="info" title="剩余额度的计算范围">
        剩余 = 集群 hard − 已分配 used − 尚未反映到集群的业务预留，最低为
        0。降额请求可能立即限制新受理；提额要等集群落实后才可使用。最终能否受理以创建或启动时的后端预算检查为准。
      </Alert>
      <div className="actions">
        <Button
          hierarchy="outline"
          disabled={!allowed}
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新配额
        </Button>
      </div>
      {!allowed ? (
        <p className="muted">权限尚未确认，暂不展示配额数据。</p>
      ) : query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorState
          error={query.error}
          retry={() => {
            void query.refetch();
          }}
        />
      ) : (
        <QuotaSummary view={query.data} />
      )}
      {operation && (
        <OperationDialog id={operation} groupId={groupId} onClose={() => setOperation(undefined)} />
      )}
    </>
  );
}
