import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Button, Card } from '@atomus';
import { ArrowUpRight, Layers3 } from 'lucide-react';
import { useGroups } from './use-groups';
import { ErrorState, Loading, NoData, PageHeading, StatusBadge } from '@/components/feedback';
import { api } from '@/lib/api';
import type { Me } from '@/lib/types';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { OperationDialog } from '@/components/operation-dialog';
import { AccountLinks } from '@/components/account-links';

export function WorkspacesPage() {
  const query = useGroups();
  const me = useQuery({
    queryKey: ['me'],
    queryFn: ({ signal }) => api<Me>('/me', { signal }),
    staleTime: 0,
    refetchInterval: 30_000,
  });
  const { operation, setOperation } = useOperationNavigation();
  const groups = query.data?.pages.flat() ?? [];
  return (
    <main className="workspace-picker">
      <div className="list-meta">
        <span className="wordmark">
          <span className="logo-mark">L</span>loom
        </span>
        <AccountLinks />
      </div>
      <PageHeading
        eyebrow="YOUR WORKSPACES"
        title="选择工作空间"
        description="每个空间独立管理成员、开发环境、训练流程和模型服务。"
        actions={
          me.isSuccess && me.data.permissions.includes('group.create') ? (
            <Link className="action-link" to="/spaces/new">
              创建工作空间
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
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorState
          error={query.error}
          retry={() => {
            void query.refetch();
          }}
        />
      ) : groups.length === 0 ? (
        <NoData title="你还没有工作空间" description="请联系管理员创建空间或邀请你加入。" />
      ) : (
        <div className="workspace-grid">
          {groups.map((group) => (
            <Link className="workspace-link" key={group.id} to={`/g/${group.id}`}>
              <Card
                title={
                  <>
                    <Layers3 size={20} /> {group.displayName || group.name}
                  </>
                }
                supportingText={group.description || group.name}
                footer={
                  <>
                    <StatusBadge status={group.status} />
                    <ArrowUpRight size={18} />
                  </>
                }
              />
            </Link>
          ))}
        </div>
      )}
      {query.hasNextPage && (
        <Button
          loading={query.isFetchingNextPage}
          onClick={() => {
            void query.fetchNextPage();
          }}
        >
          加载更多空间
        </Button>
      )}
      {operation && (
        <OperationDialog id={operation} groupId="" onClose={() => setOperation(undefined)} />
      )}
    </main>
  );
}
