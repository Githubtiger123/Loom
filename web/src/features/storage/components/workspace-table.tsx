import { Link } from 'react-router-dom';
import { Table } from '@atomus';
import { formatBytes, formatDate } from '@/lib/format';
import { NoData, StatusBadge } from '@/components/feedback';
import type { Workspace } from '../types';

export function WorkspaceTable({ rows, groupId }: { rows: Workspace[]; groupId: string }) {
  return (
    <Table
      rows={rows}
      rowKey={(row) => String(row.id)}
      caption="工作目录资产"
      columns={[
        {
          key: 'pvcName',
          header: '工作目录',
          render: (row) => (
            <Link className="text-link" to={`/g/${groupId}/storage/${row.id}`}>
              {row.pvcName}
            </Link>
          ),
          supporting: (row) => `#${row.id} · r${row.revision}`,
        },
        {
          key: 'status',
          header: '状态',
          render: (row) => <StatusBadge status={row.status} />,
          supporting: (row) => (row.ownership === 'EXCLUSIVE' ? '独占目录' : row.ownership),
        },
        {
          key: 'capacityBytes',
          header: '容量 / 已用',
          render: (row) => formatBytes(row.capacityBytes),
          supporting: (row) =>
            row.usedBytes === null ? '已用量暂无可信指标' : `已用 ${formatBytes(row.usedBytes)}`,
        },
        { key: 'ownerUserId', header: '负责人 ID', render: (row) => row.ownerUserId ?? '未分配' },
        {
          key: 'retained',
          header: '数据保护',
          render: (row) => (row.retained ? '保留保护' : '未开启保留保护'),
        },
        { key: 'notebookIds', header: '关联实例', render: (row) => row.notebookIds.length },
        { key: 'observedAt', header: '最近观测', render: (row) => formatDate(row.observedAt) },
      ]}
      empty={
        <NoData
          title="暂无工作目录资产"
          description="Notebook 的独占目录经可信观测后登记；删除实例默认保留目录。"
        />
      }
    />
  );
}
