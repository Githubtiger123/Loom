import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Button, Table } from '@atomus';
import { ErrorState, Loading, NoData, PageHeading, StatusBadge } from '@/components/feedback';
import { adminApi, adminKeys } from './api';
import type { Role } from './types';
import { levelLabels } from './policy';
import { GlobalCapability } from './components/global-capability';
export function RolesPage() {
  return (
    <GlobalCapability permission="role.manage">
      {(allowed) => <RolesContent allowed={allowed} />}
    </GlobalCapability>
  );
}
function RolesContent({ allowed }: { allowed: boolean }) {
  const query = useQuery({
    queryKey: adminKeys.roles,
    queryFn: ({ signal }) => adminApi.roles(signal),
    enabled: allowed,
    gcTime: 0,
  });
  return (
    <>
      <PageHeading
        eyebrow="ROLES & PERMISSIONS"
        title="角色与权限"
        description="全局角色与空间角色分开配置。内置角色只读，自定义角色可编辑或停用。"
        actions={
          allowed ? (
            <Link className="action-link" to="/admin/roles/new">
              创建自定义角色
            </Link>
          ) : undefined
        }
      />
      <div className="actions">
        <Button
          hierarchy="outline"
          disabled={!allowed}
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新角色
        </Button>
      </div>
      {!allowed ? (
        <p className="muted">权限尚未确认，暂不展示角色数据。</p>
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
        <Table<Role>
          rows={query.data}
          rowKey={(r) => String(r.id)}
          caption="平台及空间角色定义"
          empty={<NoData title="暂无角色" />}
          columns={[
            {
              key: 'name',
              header: '角色',
              render: (r) => <strong>{r.name}</strong>,
              supporting: (r) => `${r.code} · v${r.version}${r.builtin ? ' · 内置' : ''}`,
            },
            {
              key: 'scope',
              header: '范围',
              render: (r) => (r.scope === 'global' ? '平台全局' : '指定空间'),
            },
            { key: 'accessLevel', header: '原生访问', render: (r) => levelLabels[r.accessLevel] },
            {
              key: 'enabled',
              header: '状态',
              render: (r) => <StatusBadge status={r.enabled ? 'ACTIVE' : 'DISABLED'} />,
            },
            {
              key: 'permissions',
              header: '权限',
              render: (r) => (
                <details>
                  <summary className="text-link">{r.permissions.length} 个权限点</summary>
                  <p className="small muted">{r.description || '没有描述'}</p>
                  {r.permissions.map((p) => (
                    <span className="config-chip mono" key={p}>
                      {p}
                    </span>
                  ))}
                </details>
              ),
            },
            {
              key: 'actions',
              header: '操作',
              render: (r) =>
                r.builtin ? (
                  <span className="small muted">版本维护，只读</span>
                ) : (
                  <Link className="text-link" to={`/admin/roles/${r.id}/edit`}>
                    编辑
                  </Link>
                ),
            },
          ]}
        />
      )}
    </>
  );
}
