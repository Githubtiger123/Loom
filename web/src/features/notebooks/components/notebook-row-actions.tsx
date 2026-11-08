import { Link } from 'react-router-dom';
import { Button } from '@atomus';
import type { Notebook } from '@/lib/types';
import { isNotebookBusy } from '../policy';
import type { NotebookAction } from '../types';

export function NotebookRowActions({
  row,
  groupId,
  activeGroup,
  can,
  onAction,
  onOperation,
}: {
  row: Notebook;
  groupId: string;
  activeGroup: boolean;
  can: (permission: string) => boolean;
  onAction: (action: NotebookAction) => void;
  onOperation?: (id: string) => void;
}) {
  const deleting = row.status === 'DELETING' || row.desiredState === 'DELETED';
  const operationId = row.operationId;
  return (
    <div className="actions">
      {can('notebook.update') && !isNotebookBusy(row) && !deleting && activeGroup && (
        <Link className="text-link small" to={`/workspaces/${groupId}/notebooks/${row.id}/edit`}>
          编辑
        </Link>
      )}
      {can('notebook.start') && row.status === 'STOPPED' && !deleting && activeGroup && (
        <Button hierarchy="link" size="sm" onClick={() => onAction('start')}>
          启动
        </Button>
      )}
      {can('notebook.stop') && row.desiredState === 'RUNNING' && !deleting && (
        <Button hierarchy="link" size="sm" onClick={() => onAction('stop')}>
          停止
        </Button>
      )}
      {can('notebook.delete') && !deleting && (
        <Button hierarchy="tertiary" size="sm" onClick={() => onAction('delete')}>
          删除
        </Button>
      )}
      {operationId && onOperation && (
        <Button hierarchy="tertiary" size="sm" onClick={() => onOperation(operationId)}>
          操作进度
        </Button>
      )}
    </div>
  );
}
