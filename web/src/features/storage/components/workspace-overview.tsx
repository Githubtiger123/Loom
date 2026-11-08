import { Card } from '@atomus';
import { formatBytes, formatDate } from '@/lib/format';
import { StatusBadge } from '@/components/feedback';
import type { Workspace } from '../types';

export function WorkspaceOverview({ row }: { row: Workspace }) {
  return (
    <Card>
      <dl className="details-grid">
        <dt>目录 ID / 修订</dt>
        <dd>
          #{row.id} / r{row.revision}
        </dd>
        <dt>状态</dt>
        <dd>
          <StatusBadge status={row.status} />
        </dd>
        <dt>PVC</dt>
        <dd className="mono break-word">{row.pvcName}</dd>
        <dt>PVC 身份</dt>
        <dd className="mono break-word">{row.pvcUID || '尚未确认'}</dd>
        <dt>负责人用户 ID</dt>
        <dd>{row.ownerUserId ?? '未分配'}</dd>
        <dt>目录归属</dt>
        <dd>{row.ownership === 'EXCLUSIVE' ? '独占目录' : row.ownership}</dd>
        <dt>容量</dt>
        <dd>{formatBytes(row.capacityBytes)}</dd>
        <dt>实际已用</dt>
        <dd>
          {row.usedBytes === null ? '暂无可信指标，容量不代表已用量' : formatBytes(row.usedBytes)}
        </dd>
        <dt>数据保留保护</dt>
        <dd>{row.retained ? '已开启' : '未开启'}</dd>
        <dt>集群 / 命名空间</dt>
        <dd>
          {row.cluster} / {row.namespace}
        </dd>
        <dt>最近观测</dt>
        <dd>{formatDate(row.observedAt)}</dd>
        <dt>登记时间</dt>
        <dd>{formatDate(row.createdAt)}</dd>
      </dl>
    </Card>
  );
}
