import { useQuery } from '@tanstack/react-query';
import { Link, useOutletContext, useParams } from 'react-router-dom';
import type { Group } from '@/lib/types';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { groupApi, groupKeys } from './api';
import { userIdSchema } from './form-schema';
import { canManageMembers } from './policy';
import { CapabilityBoundary } from './components/capability-boundary';
import { MemberForm } from './components/member-form';

export function MemberEditorPage() {
  const { groupId = '', userId } = useParams();
  if (userId && !userIdSchema.safeParse(userId).success) return <NoData title="成员 ID 无效" />;
  return (
    <CapabilityBoundary groupId={groupId} permission="group.member.manage">
      {(allowed) => <EditorContent groupId={groupId} userId={userId} allowed={allowed} />}
    </CapabilityBoundary>
  );
}
function EditorContent({
  groupId,
  userId,
  allowed,
}: {
  groupId: string;
  userId?: string;
  allowed: boolean;
}) {
  const group = useOutletContext<Group>();
  const roles = useQuery({
    queryKey: groupKeys.roles(groupId),
    queryFn: ({ signal }) => groupApi.roles(groupId, signal),
    enabled: allowed,
    refetchInterval: (q) => (q.state.error ? false : 30_000),
    gcTime: 0,
  });
  const members = useQuery({
    queryKey: groupKeys.members(groupId),
    queryFn: ({ signal }) => groupApi.members(groupId, signal),
    enabled: allowed,
    refetchInterval: (q) => (q.state.error ? false : 5000),
    gcTime: 0,
  });
  const member = members.data?.find((m) => m.userId === Number(userId));
  return (
    <>
      <Link className="text-link" to={`/workspaces/${groupId}/members`}>
        ← 返回空间成员
      </Link>
      <PageHeading
        eyebrow="MEMBER ROLES"
        title={userId ? '修改成员角色' : '添加已有用户'}
        description="保存时完整替换该用户的空间角色；业务授权与集群授权落实分别处理。"
      />
      {roles.isError && (
        <ErrorState
          error={roles.error}
          retry={() => {
            void roles.refetch();
          }}
        />
      )}
      {members.isError && (
        <ErrorState
          error={members.error}
          retry={() => {
            void members.refetch();
          }}
        />
      )}
      {!roles.data || !members.data ? (
        (roles.isPending || members.isPending) && <Loading />
      ) : userId && !member ? (
        <NoData title="该成员已不存在" description="返回成员列表刷新后重试。" />
      ) : roles.data.length === 0 ? (
        <NoData title="没有可分配的组内角色" />
      ) : (
        <MemberForm
          key={userId ?? 'new'}
          groupId={groupId}
          member={member}
          members={members.data}
          roles={roles.data}
          allowed={allowed && !roles.isError && !members.isError && canManageMembers(group.status)}
        />
      )}
    </>
  );
}
