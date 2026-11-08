import { useState } from 'react';
import { useInfiniteQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Button, Input } from '@atomus';
import { useGlobalPermissions } from '@/lib/use-global-permissions';
import { ErrorState, Loading, PageHeading } from '@/components/feedback';
import { adminApi, adminKeys } from './api';
import { GlobalCapability } from './components/global-capability';
import { UsersTable } from './components/users-table';
export function UsersPage() {
  return (
    <GlobalCapability permission="user.read">
      {(allowed) => <UsersContent allowed={allowed} />}
    </GlobalCapability>
  );
}
function UsersContent({ allowed }: { allowed: boolean }) {
  const me = useGlobalPermissions();
  const [search, setSearch] = useState('');
  const query = useInfiniteQuery({
    queryKey: adminKeys.users,
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) => adminApi.users(pageParam, signal),
    getNextPageParam: (rows) => (rows.length === 50 ? rows.at(-1)?.id : undefined),
    enabled: allowed,
    gcTime: 0,
  });
  const rows = query.data?.pages.flat() ?? [];
  const filtered = rows.filter((u) =>
    `${u.id} ${u.username} ${u.displayName} ${u.status}`
      .toLowerCase()
      .includes(search.trim().toLowerCase()),
  );
  return (
    <>
      <PageHeading
        eyebrow="PLATFORM USERS"
        title="用户管理"
        description="以稳定身份识别用户，平台状态与外部身份提供方状态分开管理。"
        actions={
          me.can('user.manage') ? (
            <Link className="action-link" to="/admin/users/new">
              登记用户
            </Link>
          ) : undefined
        }
      />
      <div className="toolbar notebook-toolbar">
        <Input
          label="筛选已加载用户"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="用户名、展示名、ID 或状态"
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
        <p className="muted">权限尚未确认，暂不展示用户数据。</p>
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
            已加载 {rows.length} 位，匹配 {filtered.length} 位；筛选仅针对已加载数据。
          </p>
          <UsersTable rows={filtered} />
          {query.hasNextPage && (
            <Button
              hierarchy="outline"
              loading={query.isFetchingNextPage}
              onClick={() => {
                void query.fetchNextPage();
              }}
            >
              加载更多用户
            </Button>
          )}
        </>
      )}
    </>
  );
}
