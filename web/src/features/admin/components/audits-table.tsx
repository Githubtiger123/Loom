import { Button, Table } from '@atomus';
import { NoData, StatusBadge } from '@/components/feedback';
import { formatDate } from '@/lib/format';
import { validOperationId } from '../policy';
import type { Audit } from '../types';
export function AuditsTable({
  rows,
  onOperation,
}: {
  rows: Audit[];
  onOperation: (id: string) => void;
}) {
  return (
    <Table<Audit>
      rows={rows}
      rowKey={(a) => String(a.id)}
      caption="平台业务与安全审计"
      empty={<NoData title="没有匹配的记录" />}
      columns={[
        {
          key: 'createdAt',
          header: '时间',
          render: (a) => formatDate(a.createdAt),
          supporting: (a) => `记录 #${a.id}`,
        },
        {
          key: 'actorName',
          header: '操作者',
          render: (a) => a.actorName,
          supporting: (a) => (a.actorId ? `用户 ID ${a.actorId}` : '系统操作'),
        },
        {
          key: 'action',
          header: '动作',
          render: (a) => <span className="mono break-word">{a.action}</span>,
        },
        {
          key: 'targetId',
          header: '目标',
          render: (a) => `${a.targetType} / ${a.targetId}`,
          supporting: (a) => (a.groupId ? `空间 ID ${a.groupId}` : '平台全局'),
        },
        { key: 'outcome', header: '结果', render: (a) => <StatusBadge status={a.outcome} /> },
        {
          key: 'operation',
          header: '关联操作',
          render: (a) =>
            a.operationId && validOperationId(a.operationId) ? (
              <Button hierarchy="tertiary" onClick={() => onOperation(a.operationId!)}>
                查看操作单
              </Button>
            ) : (
              '—'
            ),
        },
      ]}
    />
  );
}
