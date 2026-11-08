import { useState } from 'react';
import { useNavigate, useOutletContext, useParams } from 'react-router-dom';
import { Button, Card } from '@atomus';
import type { Group } from '@/lib/types';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { ErrorState, PageHeading, StatusBadge } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { formatDate } from '@/lib/format';
import type { GroupAction } from './types';
import { canLifecycle } from './policy';
import { CapabilityBoundary } from './components/capability-boundary';
import { MetadataForm } from './components/metadata-form';
import { LifecycleDialog } from './components/lifecycle-dialog';

export function GroupSettingsPage() {
  const { groupId = '' } = useParams();
  const group = useOutletContext<Group>();
  const permissions = useGroupPermissions(groupId);
  const navigate = useNavigate();
  const { operation, setOperation } = useOperationNavigation();
  const [editing, setEditing] = useState(false);
  const [command, setCommand] = useState<{ action: GroupAction; snapshot: Group }>();
  return (
    <CapabilityBoundary groupId={groupId} permission="group.read">
      {() => (
        <>
          <PageHeading
            eyebrow="WORKSPACE SETTINGS"
            title="空间设置"
            description="业务负责人和基础设施身份独立管理，异步操作以观测结果为准。"
          />
          {group.lastError && <ErrorState error={new Error(group.lastError)} />}
          <Card
            title="基本信息"
            headerAction={
              !editing &&
              permissions.can('group.update') &&
              !['DELETING', 'DELETED'].includes(group.status) ? (
                <Button hierarchy="outline" onClick={() => setEditing(true)}>
                  编辑信息
                </Button>
              ) : undefined
            }
          >
            {editing ? (
              <MetadataForm
                groupId={groupId}
                group={group}
                allowed={permissions.can('group.update')}
                onCancel={() => setEditing(false)}
                onAccepted={(id) => {
                  setEditing(false);
                  setOperation(id);
                }}
              />
            ) : (
              <dl className="details-grid">
                <dt>空间名</dt>
                <dd>{group.name}</dd>
                <dt>展示名称</dt>
                <dd>{group.displayName || '—'}</dd>
                <dt>描述</dt>
                <dd className="preserve-text">{group.description || '—'}</dd>
                <dt>集群</dt>
                <dd>{group.cluster}</dd>
                <dt>Profile owner</dt>
                <dd>{group.infrastructureOwner}</dd>
                <dt>业务状态</dt>
                <dd>
                  <StatusBadge status={group.status} />
                </dd>
                <dt>观测状态</dt>
                <dd>
                  <StatusBadge status={group.observedState} />
                </dd>
                <dt>修订</dt>
                <dd>r{group.revision}</dd>
                <dt>最近观测</dt>
                <dd>{formatDate(group.observedAt)}</dd>
              </dl>
            )}
          </Card>
          <Card
            title="空间生命周期"
            supportingText="停用不是删除。空间必须清空业务依赖后才能删除；数据资产不会随停用自动清理。"
          >
            <div className="stack">
              <p className="muted">
                停用会申请停止 Notebook 并撤销原生访问；存在活跃
                Pipeline、训练或推理任务时，后端会拒绝。恢复后不会自动启动先前停止的 Notebook。
              </p>
              <div className="actions">
                {(['disable', 'restore', 'delete'] as const)
                  .filter((a) => permissions.can(`group.${a}`))
                  .map((action) => (
                    <Button
                      key={action}
                      hierarchy="outline"
                      disabled={editing || !canLifecycle(group, action)}
                      onClick={() => setCommand({ action, snapshot: group })}
                    >
                      {{ disable: '停用空间', restore: '恢复空间', delete: '删除空间' }[action]}
                    </Button>
                  ))}
              </div>
            </div>
          </Card>
          {command && (
            <LifecycleDialog
              groupId={groupId}
              group={command.snapshot}
              action={command.action}
              allowed={permissions.can(`group.${command.action}`)}
              onClose={() => setCommand(undefined)}
              onAccepted={(id) => {
                setCommand(undefined);
                if (command.action === 'delete') void navigate('/', { state: { operationId: id } });
                else setOperation(id);
              }}
            />
          )}
          {operation && (
            <OperationDialog
              id={operation}
              groupId={groupId}
              onClose={() => setOperation(undefined)}
            />
          )}
        </>
      )}
    </CapabilityBoundary>
  );
}
