import type { ReactNode } from 'react';
import { Alert, Badge, Button, EmptyState } from '@atomus';
import { ApiError, errorMessage } from '@/lib/api';

const statusLabels: Record<string, string> = {
  ACTIVE: '可用',
  READY: '就绪',
  ARCHIVED: '已归档',
  DRAFT: '草稿',
  RUNNING: '运行中',
  SERVING: '服务中',
  SUCCEEDED: '已完成',
  FAILED: '失败',
  ERROR: '异常',
  UNKNOWN: '待确认',
  PENDING: '等待处理',
  CREATING: '创建中',
  STARTING: '启动中',
  STOPPING: '停止中',
  STOPPED: '已停止',
  UPDATING: '更新中',
  DELETING: '删除中',
  DELETED: '已删除',
  DISABLED: '已停用',
  DEGRADED: '待恢复',
  RETRYING: '重试中',
  SUPERSEDED: '已被替代',
  DISABLING: '停用中',
  RESTORING: '恢复中',
  APPLYING: '授权落实中',
  REVOKING: '撤权中',
  DRIFTED: '授权偏移',
  ACCEPTED: '已受理',
  DENIED: '已拒绝',
  AVAILABLE: '可复用',
  BOUND: '已关联',
};
export function StatusBadge({ status }: { status?: string }) {
  const value = status ?? 'UNKNOWN';
  const color = ['ACTIVE', 'READY', 'RUNNING', 'SERVING', 'SUCCEEDED'].includes(value)
    ? 'success'
    : ['ERROR', 'FAILED', 'DRIFTED', 'DENIED'].includes(value)
      ? 'error'
      : [
            'PENDING',
            'ACCEPTED',
            'CREATING',
            'STARTING',
            'STOPPING',
            'UPDATING',
            'RETRYING',
            'DELETING',
            'DISABLING',
            'RESTORING',
            'APPLYING',
            'REVOKING',
          ].includes(value)
        ? 'warning'
        : 'gray';
  return (
    <Badge color={color} size="sm" dot>
      {statusLabels[value] ?? value}
    </Badge>
  );
}
export function Loading({ label = '正在加载' }: { label?: string }) {
  return (
    <div className="loading-state" role="status">
      <span className="at-spinner" />
      {label}
    </div>
  );
}
export function ErrorState({ error, retry }: { error: unknown; retry?: () => void }) {
  const denied = error instanceof ApiError && error.status === 403;
  const conflict = error instanceof ApiError && error.status === 409;
  const missing = error instanceof ApiError && error.status === 404;
  return (
    <Alert
      color={denied || conflict ? 'warning' : 'error'}
      title={
        denied
          ? '当前账号没有访问权限'
          : conflict
            ? '资源状态或配置已变化'
            : missing
              ? '资源不存在或已被删除'
              : '请求未完成'
      }
      actions={
        retry && !denied ? (
          <Button hierarchy="outline" size="sm" onClick={retry}>
            重新加载
          </Button>
        ) : undefined
      }
    >
      {errorMessage(error)}
    </Alert>
  );
}
export function NoData({
  title = '还没有资源',
  description,
  action,
}: {
  title?: string;
  description?: string;
  action?: ReactNode;
}) {
  return <EmptyState title={title} description={description} actions={action} />;
}
export function PageHeading({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow?: string;
  title: string;
  description: string;
  actions?: ReactNode;
}) {
  return (
    <header className="page-heading">
      <div>
        {eyebrow && <p className="eyebrow">{eyebrow}</p>}
        <h1>{title}</h1>
        <p className="muted">{description}</p>
      </div>
      {actions && <div className="actions">{actions}</div>}
    </header>
  );
}
