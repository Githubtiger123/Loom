import { useState } from 'react';
import { Link, useNavigate, useOutletContext, useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Button, Input, Tabs } from '@atomus';
import type { Group } from '@/lib/types';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { ErrorState, Loading, PageHeading } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { groupApi, groupKeys } from './api';
import type { Member } from './types';
import { canManageMembers, canRemoveMembers } from './policy';
import { CapabilityBoundary } from './components/capability-boundary';
import { MembersTable } from './components/members-table';
import { RemoveMemberDialog } from './components/remove-member-dialog';
import { AccessPanel } from './components/access-panel';

export function MembersPage() {
  const { groupId = '' } = useParams();
  return (
    <CapabilityBoundary groupId={groupId} permission="group.read">
      {(allowed) => <MembersContent groupId={groupId} allowed={allowed} />}
    </CapabilityBoundary>
  );
}
function MembersContent({ groupId, allowed }: { groupId: string; allowed: boolean }) {
  const permissions = useGroupPermissions(groupId);
  const group = useOutletContext<Group>();
  const [search, setSearch] = useState('');
  const [removing, setRemoving] = useState<Member>();
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const { operation, setOperation } = useOperationNavigation();
  const manage = permissions.can('group.member.manage');
  const tab = params.get('tab') === 'access' && manage ? 'access' : 'members';
  const query = useQuery({
    queryKey: groupKeys.members(groupId),
    queryFn: ({ signal }) => groupApi.members(groupId, signal),
    enabled: allowed && tab === 'members',
    refetchInterval: (q) => (q.state.error ? false : 5000),
    gcTime: 0,
  });
  const items =
    query.data?.filter((m) =>
      `${m.userId} ${m.username} ${m.displayName}`
        .toLowerCase()
        .includes(search.trim().toLowerCase()),
    ) ?? [];
  return (
    <>
      <PageHeading
        eyebrow="WORKSPACE ACCESS"
        title="成员与授权"
        description="业务角色决定平台操作权限；原生访问单独异步落实，不代表拥有相同的平台权限。"
        actions={
          manage && canManageMembers(group.status) ? (
            <Link className="action-link" to={`/workspaces/${groupId}/members/new`}>
              添加已有用户
            </Link>
          ) : undefined
        }
      />
      <div className="actions">
        <Tabs
          aria-label="成员与集群授权"
          value={tab}
          onChange={(value) => setParams(value === 'members' ? {} : { tab: value })}
          items={[
            { value: 'members', label: '空间成员' },
            ...(manage ? [{ value: 'access', label: '集群授权落实' }] : []),
          ]}
        />
        {permissions.can('group.owner.transfer') && canManageMembers(group.status) && (
          <Link className="text-link" to={`/workspaces/${groupId}/members/transfer`}>
            负责人交接
          </Link>
        )}
      </div>
      {tab === 'access' ? (
        <AccessPanel groupId={groupId} onOperation={setOperation} />
      ) : (
        <div className="stack">
          <div className="toolbar notebook-toolbar">
            <Input
              label="搜索成员"
              placeholder="用户名、展示名或用户 ID"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
            <Button
              hierarchy="outline"
              loading={query.isFetching}
              disabled={!allowed}
              onClick={() => {
                void query.refetch();
              }}
            >
              刷新
            </Button>
          </div>
          {!allowed ? (
            <p className="muted">权限尚未确认，暂不展示成员数据。</p>
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
            <>
              <p className="small muted">
                当前 {query.data.length} 位成员，匹配 {items.length} 位。角色有效期按本地时间显示。
              </p>
              <MembersTable
                rows={items}
                groupId={groupId}
                canEdit={manage && canManageMembers(group.status)}
                canRemove={manage && canRemoveMembers(group.status)}
                onRemove={setRemoving}
              />
            </>
          )}
        </div>
      )}
      {removing && (
        <RemoveMemberDialog
          groupId={groupId}
          member={removing}
          allowed={manage && canRemoveMembers(group.status)}
          onClose={() => setRemoving(undefined)}
          onAccepted={(id) => {
            setRemoving(undefined);
            if (removing.userId === permissions.data?.userId)
              void navigate('/', { state: { operationId: id } });
            else setOperation(id);
          }}
        />
      )}
      {operation && (
        <OperationDialog id={operation} groupId={groupId} onClose={() => setOperation(undefined)} />
      )}
    </>
  );
}
