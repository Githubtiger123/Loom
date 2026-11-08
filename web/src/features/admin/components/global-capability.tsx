import type { ReactNode } from 'react';
import { useGlobalPermissions } from '@/lib/use-global-permissions';
import { ApiError } from '@/lib/api';
import { ErrorState, Loading, NoData } from '@/components/feedback';
export function GlobalCapability({
  permission,
  children,
}: {
  permission: string;
  children: (allowed: boolean) => ReactNode;
}) {
  const query = useGlobalPermissions();
  if (query.error instanceof ApiError && [401, 403].includes(query.error.status))
    return <ErrorState error={query.error} />;
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
        title="没有访问此功能的全局权限"
        description="空间角色不代表平台管理权限，请联系平台管理员。"
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
