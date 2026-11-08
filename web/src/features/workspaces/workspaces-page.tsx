import { Link } from 'react-router-dom';
import { Button, Card } from '@atomus';
import { ArrowUpRight, Layers3 } from 'lucide-react';
import { useGroups } from './use-groups';
import { ErrorState, Loading, NoData, PageHeading, StatusBadge } from '@/components/feedback';
import { useGlobalPermissions } from '@/lib/use-global-permissions';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { OperationDialog } from '@/components/operation-dialog';
import { AccountControls } from '@/components/account-controls';

export function WorkspacesPage() {
  const query = useGroups();
  const me = useGlobalPermissions();
  const { operation, setOperation } = useOperationNavigation();
  const groups = query.data?.pages.flat() ?? [];
  return (
    <div className="home-layout">
      <a className="skip-link" href="#main-content">
        跳转到主要内容
      </a>
      <header className="topbar home-topbar">
        <Link className="wordmark" to="/" aria-label="Loom 首页">
          <span className="logo-mark">L</span>loom
        </Link>
        <AccountControls />
      </header>
      <main id="main-content" className="workspace-picker" tabIndex={-1}>
        <section className="overview-intro home-welcome" aria-label="欢迎回到 Loom">
          <div>
            <p className="eyebrow">WELCOME TO LOOM</p>
            <h1>
              {me.isSuccess
                ? `你好，${me.data.user.displayName || me.data.user.username}`
                : '欢迎回到 Loom'}
            </h1>
            <p className="muted">在工作空间中开始开发与协作，在个人中心管理你的账号安全。</p>
          </div>
          <Link className="text-link" to="/account">
            前往个人中心 <ArrowUpRight size={16} />
          </Link>
        </section>
        <PageHeading
          eyebrow="YOUR WORKSPACES"
          title="选择工作空间"
          description="每个空间独立管理成员、开发环境、训练流程和模型服务。"
          actions={
            me.can('group.create') ? (
              <Link className="action-link" to="/workspaces/new">
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
              <Link className="workspace-link" key={group.id} to={`/workspaces/${group.id}`}>
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
        {query.isSuccess && groups.length > 0 && (
          <p className="small muted">
            已加载 {groups.length} 个可访问空间
            {query.hasNextPage
              ? '，可继续加载；仅展示当前账号可访问的空间。'
              : '；各空间资源与权限独立管理。'}
          </p>
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
    </div>
  );
}
