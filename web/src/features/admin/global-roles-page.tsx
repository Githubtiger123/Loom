import { useQuery } from '@tanstack/react-query';
import { useParams } from 'react-router-dom';
import { ErrorState, Loading, NoData } from '@/components/feedback';
import { adminApi, adminKeys } from './api';
import { validId } from './form-schema';
import { isAccessDenied } from './policy';
import { GlobalCapability } from './components/global-capability';
import { GlobalRolesForm } from './components/global-roles-form';
export function GlobalRolesPage() {
  const { userId = '' } = useParams();
  if (!validId(userId)) return <NoData title="用户 ID 无效" />;
  return (
    <GlobalCapability permission="role.manage">
      {(allowed) => <Editor id={userId} allowed={allowed} />}
    </GlobalCapability>
  );
}
function Editor({ id, allowed }: { id: string; allowed: boolean }) {
  const bindings = useQuery({
    queryKey: adminKeys.bindings(id),
    queryFn: ({ signal }) => adminApi.bindings(id, signal),
    enabled: allowed,
    refetchInterval: (q) => (q.state.error ? false : 15_000),
    gcTime: 0,
  });
  const roles = useQuery({
    queryKey: adminKeys.roles,
    queryFn: ({ signal }) => adminApi.roles(signal),
    enabled: allowed,
    gcTime: 0,
  });
  const denied = [roles.error, bindings.error].find(isAccessDenied);
  if (denied) return <ErrorState error={denied} />;
  return (
    <>
      {bindings.isError && (
        <ErrorState
          error={bindings.error}
          retry={() => {
            void bindings.refetch();
          }}
        />
      )}
      {roles.isError && (
        <ErrorState
          error={roles.error}
          retry={() => {
            void roles.refetch();
          }}
        />
      )}
      {!bindings.data || !roles.data ? (
        (bindings.isPending || roles.isPending) && <Loading />
      ) : (
        <GlobalRolesForm
          key={id}
          id={id}
          bindings={bindings.data}
          roles={roles.data.filter((r) => r.scope === 'global' && r.enabled)}
          allowed={allowed && bindings.isSuccess && roles.isSuccess}
        />
      )}
    </>
  );
}
