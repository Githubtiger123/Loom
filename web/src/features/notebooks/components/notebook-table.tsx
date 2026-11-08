import { Link } from 'react-router-dom';
import { Table } from '@atomus';
import type { Notebook } from '@/lib/types';
import { formatBytes, formatDate } from '@/lib/format';
import { NoData, StatusBadge } from '@/components/feedback';
import { NotebookRowActions } from './notebook-row-actions';
import { ownsNotebook } from '../policy';
import type { NotebookAction } from '../types';

export function NotebookTable({
  rows,
  groupId,
  activeGroup,
  can,
  userId,
  filtered,
  onAction,
  onOperation,
}: {
  rows: Notebook[];
  groupId: string;
  activeGroup: boolean;
  can: (permission: string) => boolean;
  userId?: number;
  filtered: boolean;
  onAction: (row: Notebook, action: NotebookAction) => void;
  onOperation: (id: string) => void;
}) {
  return (
    <Table
      rows={rows}
      rowKey={(row) => String(row.id)}
      caption="开发环境列表"
      columns={[
        {
          key: 'name',
          header: '环境名称',
          render: (row) => (
            <Link className="text-link" to={`/g/${groupId}/notebooks/${row.id}`}>
              {row.displayName || row.name}
            </Link>
          ),
          supporting: (row) => row.name,
        },
        {
          key: 'status',
          header: '状态',
          render: (row) => <StatusBadge status={row.status} />,
          supporting: (row) =>
            row.revision !== row.appliedRevision
              ? `待落实 r${row.revision} · 已落实 r${row.appliedRevision}`
              : `r${row.revision}`,
        },
        { key: 'imageId', header: '镜像', supporting: (row) => row.serverType },
        {
          key: 'cpuMilli',
          header: '计算规格',
          render: (row) => `${row.cpuMilli / 1000} CPU · ${formatBytes(row.memoryBytes)}`,
          supporting: (row) => `${row.gpu} GPU`,
        },
        { key: 'observedAt', header: '最近同步', render: (row) => formatDate(row.observedAt) },
        {
          key: 'actions',
          header: '操作',
          render: (row) => (
            <NotebookRowActions
              row={row}
              groupId={groupId}
              activeGroup={activeGroup}
              can={(code) => ownsNotebook(row, userId, can('notebook.read.any')) && can(code)}
              onAction={(action) => onAction(row, action)}
              onOperation={onOperation}
            />
          ),
        },
      ]}
      empty={
        <NoData
          title={filtered ? '没有匹配的环境' : '还没有开发环境'}
          description={filtered ? '调整搜索或筛选条件后重试。' : '新建一个环境，开始开发与实验。'}
        />
      }
    />
  );
}
