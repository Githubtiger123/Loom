import { useQuery } from '@tanstack/react-query';
import { Link, useOutletContext, useParams } from 'react-router-dom';
import { api } from '@/lib/api';
import type { Group, Me } from '@/lib/types';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { groupApi, groupKeys } from './api';
import { CapabilityBoundary } from './components/capability-boundary';
import { QuotaForm } from './components/quota-form';

export function EditQuotaPage() {
  const { groupId = '' } = useParams();
  return (
    <CapabilityBoundary groupId={groupId} permission="group.quota.read">
      {(allowed) => <EditContent groupId={groupId} allowed={allowed} />}
    </CapabilityBoundary>
  );
}
function EditContent({ groupId, allowed }: { groupId: string; allowed: boolean }) {
  const group = useOutletContext<Group>();
  const me = useQuery({
    queryKey: ['me'],
    queryFn: ({ signal }) => api<Me>('/me', { signal }),
    staleTime: 0,
    refetchInterval: 30_000,
  });
  const quota = useQuery({
    queryKey: groupKeys.quota(groupId),
    queryFn: ({ signal }) => groupApi.quota(groupId, signal),
    enabled: allowed,
    staleTime: 0,
  });
  return (
    <>
      <Link className="text-link" to={`/workspaces/${groupId}/quota`}>
        ← 返回资源配额
      </Link>
      <PageHeading
        eyebrow="QUOTA CHANGE"
        title="调整空间配额"
        description="需要平台级配额管理权限，提交原因并以固定空间修订执行。"
      />
      {me.isError && (
        <ErrorState
          error={me.error}
          retry={() => {
            void me.refetch();
          }}
        />
      )}
      {quota.isError && (
        <ErrorState
          error={quota.error}
          retry={() => {
            void quota.refetch();
          }}
        />
      )}
      {!me.data || !quota.data ? (
        (me.isPending || quota.isPending) && <Loading />
      ) : !me.data.permissions.includes('group.quota.update') ? (
        <NoData title="没有平台级配额调整权限" description="空间负责人并不自动拥有此权限。" />
      ) : (
        <QuotaForm
          groupId={groupId}
          group={group}
          view={quota.data}
          allowed={allowed && me.isSuccess && quota.isSuccess}
        />
      )}
    </>
  );
}
