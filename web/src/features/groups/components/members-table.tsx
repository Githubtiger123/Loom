import { Link } from 'react-router-dom';
import { Button, Table } from '@atomus';
import { NoData, StatusBadge } from '@/components/feedback';
import { formatDate } from '@/lib/format';
import type { Member } from '../types';
import { accessLabel } from '../policy';

export function MembersTable({
  rows,
  groupId,
  canEdit,
  canRemove,
  onRemove,
}: {
  rows: Member[];
  groupId: string;
  canEdit: boolean;
  canRemove: boolean;
  onRemove: (member: Member) => void;
}) {
  return (
    <Table<Member>
      rows={rows}
      rowKey={(m) => String(m.userId)}
      caption="空间成员及原生访问落实状态"
      empty={<NoData title="没有匹配的成员" />}
      columns={[
        {
          key: 'username',
          header: '成员',
          render: (m) => <strong>{m.displayName || m.username}</strong>,
          supporting: (m) => `${m.username} · ID ${m.userId}`,
        },
        {
          key: 'roles',
          header: '有效角色 / 到期时间',
          render: (m) =>
            m.roles.length ? (
              <ul className="plain-list">
                {m.roles.map((b) => (
                  <li key={b.id}>
                    {b.roleCode}{' '}
                    <span className="small muted">
                      {b.expiresAt ? formatDate(b.expiresAt) : '长期'}
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              '无有效角色'
            ),
        },
        {
          key: 'access',
          header: '原生访问：期望 → 观测',
          render: (m) => `${accessLabel(m.desiredLevel)} → ${accessLabel(m.observedLevel)}`,
        },
        {
          key: 'accessStatus',
          header: '授权落实',
          render: (m) => <StatusBadge status={m.accessStatus} />,
          supporting: (m) => m.lastError,
        },
        {
          key: 'actions',
          header: '操作',
          render: (m) => (
            <div className="actions">
              {canEdit && (
                <Link className="text-link" to={`/workspaces/${groupId}/members/${m.userId}/edit`}>
                  修改角色
                </Link>
              )}
              {canRemove && (
                <Button
                  hierarchy="tertiary"
                  aria-label={`移除 ${m.username}`}
                  onClick={() => onRemove(m)}
                >
                  移除
                </Button>
              )}
            </div>
          ),
        },
      ]}
    />
  );
}
