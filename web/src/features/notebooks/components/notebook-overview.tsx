import { Alert, Card } from '@atomus';
import { formatBytes, formatDate } from '@/lib/format';
import { StatusBadge } from '@/components/feedback';
import type { NotebookDetail } from '../types';

export function NotebookOverview({ row }: { row: NotebookDetail }) {
  return (
    <div className="stack spacious">
      {row.statusReason && (
        <Alert color="warning" title="状态说明">
          {row.statusReason}
        </Alert>
      )}
      <div className="details-panels">
        <Card title="运行状态">
          <dl className="details-grid">
            <dt>当前状态</dt>
            <dd>
              <StatusBadge status={row.status} />
            </dd>
            <dt>期望 / 观测</dt>
            <dd>
              {row.desiredState} / {row.observedState}
            </dd>
            <dt>配置落实</dt>
            <dd>
              期望 r{row.revision} / 已落实 r{row.appliedRevision}
            </dd>
            <dt>最近同步</dt>
            <dd>{formatDate(row.observedAt)}</dd>
            <dt>最近启动</dt>
            <dd>{formatDate(row.startedAt)}</dd>
            <dt>最近停止</dt>
            <dd>{formatDate(row.stoppedAt)}</dd>
          </dl>
        </Card>
        <Card title="计算与存储">
          <dl className="details-grid">
            <dt>CPU / 内存</dt>
            <dd>
              {row.cpuMilli / 1000} CPU / {formatBytes(row.memoryBytes)}
            </dd>
            <dt>GPU</dt>
            <dd>{row.gpu} 张</dd>
            <dt>工作目录容量</dt>
            <dd>{formatBytes(row.workspaceSizeBytes)}</dd>
            <dt>目录归属</dt>
            <dd>{row.workspaceOwnership === 'EXCLUSIVE' ? '独占目录' : row.workspaceOwnership}</dd>
            <dt>空闲停止</dt>
            <dd>{row.idleStopMinutes === 0 ? '不自动停止' : `${row.idleStopMinutes} 分钟`}</dd>
            <dt>负责人 ID</dt>
            <dd>{row.ownerUserId ?? '未分配'}</dd>
          </dl>
        </Card>
      </div>
      <Card title="环境配置">
        <dl className="details-grid">
          <dt>实例名称</dt>
          <dd>{row.name}</dd>
          <dt>镜像 ID</dt>
          <dd>{row.imageId}</dd>
          <dt>运行类型</dt>
          <dd>{row.serverType}</dd>
          <dt>创建时间</dt>
          <dd>{formatDate(row.createdAt)}</dd>
          <dt>环境变量</dt>
          <dd>
            {row.env.length
              ? row.env.map((entry) => (
                  <span className="config-chip mono" key={entry.name}>
                    {entry.name} ·{' '}
                    {entry.hasValue
                      ? '已配置（隐藏值）'
                      : entry.secretName
                        ? 'Secret 引用'
                        : '空值'}
                  </span>
                ))
              : '未配置'}
          </dd>
          <dt>标签</dt>
          <dd>
            {Object.keys(row.labels).length
              ? Object.entries(row.labels).map(([key, value]) => (
                  <span className="config-chip mono" key={key}>
                    {key}={value}
                  </span>
                ))
              : '未配置'}
          </dd>
        </dl>
      </Card>
    </div>
  );
}
