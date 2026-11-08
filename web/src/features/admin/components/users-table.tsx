import { Link } from 'react-router-dom';
import { Table } from '@atomus';
import { NoData, StatusBadge } from '@/components/feedback';
import { formatDate } from '@/lib/format';
import type { UserRecord } from '../types';
export function UsersTable({ rows }: { rows: UserRecord[] }) {
  return (
    <Table<UserRecord>
      rows={rows}
      rowKey={(u) => String(u.id)}
      caption="平台用户"
      empty={<NoData title="没有匹配的用户" />}
      columns={[
        {
          key: 'username',
          header: '用户',
          render: (u) => (
            <Link className="text-link" to={`/admin/users/${u.id}`}>
              {u.displayName || u.username}
            </Link>
          ),
          supporting: (u) => `${u.username} · ID ${u.id}`,
        },
        { key: 'status', header: '业务状态', render: (u) => <StatusBadge status={u.status} /> },
        {
          key: 'issuer',
          header: '身份来源',
          render: (u) => <span className="break-word">{u.issuer}</span>,
        },
        { key: 'createdAt', header: '创建时间', render: (u) => formatDate(u.createdAt) },
        { key: 'lastLoginAt', header: '最近账号活动', render: (u) => formatDate(u.lastLoginAt) },
      ]}
    />
  );
}
