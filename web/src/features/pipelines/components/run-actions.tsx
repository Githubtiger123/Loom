import { useState } from 'react';
import { Link, useOutletContext } from 'react-router-dom';
import { Button } from '@atomus';
import type { Group } from '@/lib/types';
import type { Run } from '../types';
import { canManageRun, canStart, usePipelineAccess } from '../policy';
import { LifecycleDialog } from './lifecycle-dialog';

export function RunActions({
  groupId,
  run,
  onAccepted,
}: {
  groupId: string;
  run: Run;
  onAccepted: (id: string) => void;
}) {
  const group = useOutletContext<Group>();
  const access = usePipelineAccess(groupId);
  const [action, setAction] = useState<'delete' | 'start'>();
  const active = group.status === 'ACTIVE';
  const editable =
    active &&
    canManageRun(run) &&
    !run.pendingSpec &&
    ['pipeline.update', 'pipeline.stop', 'pipeline.delete'].every((code) =>
      access.allows(code, run),
    );
  const deletable = active && canManageRun(run) && access.allows('pipeline.delete', run);
  const startable = active && canStart(run) && access.allows('pipeline.run', run);
  return (
    <>
      {editable && (
        <Link className="text-link" to={`/workspaces/${groupId}/pipeline-runs/${run.id}/edit`}>
          修改配置
        </Link>
      )}
      {startable && (
        <Button hierarchy="outline" onClick={() => setAction('start')}>
          启动已保存配置
        </Button>
      )}
      {deletable && (
        <Button hierarchy="outline" onClick={() => setAction('delete')}>
          删除与清理
        </Button>
      )}
      {action &&
        (action === 'delete'
          ? access.allows('pipeline.delete', run)
          : access.allows('pipeline.run', run)) && (
          <LifecycleDialog
            groupId={groupId}
            target={{ kind: action, row: run }}
            active={active}
            onClose={() => setAction(undefined)}
            onAccepted={(id) => {
              setAction(undefined);
              onAccepted(id);
            }}
          />
        )}
    </>
  );
}
