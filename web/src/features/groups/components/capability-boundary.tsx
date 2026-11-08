import type { ReactNode } from 'react';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { ErrorState, Loading, NoData } from '@/components/feedback';

export function CapabilityBoundary({
  groupId,
  permission,
  children,
}: {
  groupId: string;
  permission: string;
  children: (allowed: boolean) => ReactNode;
}) {
  const query = useGroupPermissions(groupId);
  if (!query.data)
    return query.isPending ? (
      <Loading />
    ) : (
      <ErrorState
        error={query.error}
        retry={() => {
          void query.refetch();
        }}
      />
    );
  if (!query.data.permissions.includes(permission))
    return (
      <NoData
        title="没有访问此页面的权限"
        description="请联系空间负责人分配权限，或切换工作空间。"
      />
    );
  return (
    <div className="stack spacious">
      {query.isError && (
        <ErrorState
          error={query.error}
          retry={() => {
            void query.refetch();
          }}
        />
      )}
      {children(query.can(permission))}
    </div>
  );
}
