import { useQuery } from '@tanstack/react-query';
import { useParams } from 'react-router-dom';
import { ErrorState, Loading, NoData } from '@/components/feedback';
import { adminApi, adminKeys } from './api';
import { validId } from './form-schema';
import { isAccessDenied } from './policy';
import { GlobalCapability } from './components/global-capability';
import { RoleForm } from './components/role-form';
export function RoleEditorPage() {
  const { roleId } = useParams();
  if (roleId && !validId(roleId)) return <NoData title="角色 ID 无效" />;
  return (
    <GlobalCapability permission="role.manage">
      {(allowed) => <Editor id={roleId} allowed={allowed} />}
    </GlobalCapability>
  );
}
function Editor({ id, allowed }: { id?: string; allowed: boolean }) {
  const roles = useQuery({
    queryKey: adminKeys.roles,
    queryFn: ({ signal }) => adminApi.roles(signal),
    enabled: allowed,
    gcTime: 0,
  });
  const dictionary = useQuery({
    queryKey: adminKeys.permissions,
    queryFn: ({ signal }) => adminApi.permissions(signal),
    enabled: allowed,
  });
  const role = roles.data?.find((r) => r.id === Number(id));
  const denied = [roles.error, dictionary.error].find(isAccessDenied);
  if (denied) return <ErrorState error={denied} />;
  return (
    <>
      {roles.isError && (
        <ErrorState
          error={roles.error}
          retry={() => {
            void roles.refetch();
          }}
        />
      )}
      {dictionary.isError && (
        <ErrorState
          error={dictionary.error}
          retry={() => {
            void dictionary.refetch();
          }}
        />
      )}
      {!roles.data || !dictionary.data ? (
        (roles.isPending || dictionary.isPending) && <Loading />
      ) : id && !role ? (
        <NoData title="角色不存在" />
      ) : role?.builtin ? (
        <NoData
          title="内置角色不能在此编辑"
          description="请返回角色列表查看权限，内置角色只通过版本升级调整。"
        />
      ) : (
        <RoleForm
          key={id ?? 'new'}
          role={role}
          dictionary={dictionary.data}
          currentVersion={role?.version}
          allowed={allowed && roles.isSuccess && dictionary.isSuccess}
        />
      )}
    </>
  );
}
