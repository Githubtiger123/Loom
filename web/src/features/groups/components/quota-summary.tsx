import { Alert, Card, Table } from '@atomus';
import { formatBytes, formatDate } from '@/lib/format';
import type { Budget, QuotaView } from '../types';

type Row = {
  resource: string;
  requested: string;
  hard: string;
  used: string;
  reserved: string;
  remaining: string;
};
const cpu = (n: number) => `${(n / 1000).toLocaleString('zh-CN', { maximumFractionDigits: 3 })} 核`;
export function QuotaSummary({ view }: { view: QuotaView }) {
  const snapshot = view.snapshot;
  if (!snapshot)
    return (
      <Alert color="warning" title="没有可信的集群配额快照">
        不能把缺失配额显示为零，请检查后台同步或稍后刷新。
      </Alert>
    );
  const cell = (key: keyof Budget, format: (n: number) => string) => ({
    hard: format(snapshot.hard[key]),
    used: format(snapshot.used[key]),
    reserved: format(view.reserved[key]),
    remaining: format(view.remaining[key]),
  });
  const rows: Row[] = [
    { resource: 'CPU', requested: view.requested.cpu, ...cell('cpuMilli', cpu) },
    { resource: '内存', requested: view.requested.memory, ...cell('memoryBytes', formatBytes) },
    { resource: 'GPU', requested: `${view.requested.gpu} 张`, ...cell('gpu', (v) => `${v} 张`) },
  ];
  return (
    <Card
      title="配额与预算"
      supportingText={`读取时间 ${formatDate(snapshot.readStartedAt)} · 空间修订 r${snapshot.groupRevision}`}
    >
      <Table<Row>
        rows={rows}
        rowKey={(row) => row.resource}
        caption="空间资源额度、已分配量与待执行预留"
        columns={[
          { key: 'resource', header: '资源' },
          { key: 'requested', header: '业务请求额度' },
          { key: 'hard', header: '集群实际额度' },
          { key: 'used', header: '集群已分配' },
          { key: 'reserved', header: '未反映预留' },
          { key: 'remaining', header: '剩余预算' },
        ]}
      />
    </Card>
  );
}
