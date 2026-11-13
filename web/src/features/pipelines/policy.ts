import { useGroupPermissions } from '@/lib/use-group-permissions';
import type { Identity, Run } from './types';

export function usePipelineAccess(groupId: string) {
  const permissions = useGroupPermissions(groupId);
  return {
    ...permissions,
    allows: (code: string, row?: Pick<Identity, 'createdBy'>) =>
      permissions.can(code) &&
      (!row ||
        permissions.can('pipeline.read.any') ||
        (row.createdBy !== null && row.createdBy === permissions.data?.userId)),
  };
}
export function canStop(run: Run) {
  return (
    !run.deletedAt &&
    !run.executionCleaned &&
    run.desiredState !== 'STOPPED' &&
    !['SUCCEEDED', 'FAILED', 'ERROR', 'STOPPED', 'DELETED', 'DELETING'].includes(run.status)
  );
}
export function canManageRun(run: Run) {
  return (
    !run.deletedAt &&
    run.desiredState !== 'DELETED' &&
    !['DELETED', 'DELETING'].includes(run.status)
  );
}
export function canStart(run: Run) {
  return (
    canManageRun(run) &&
    run.executionCleaned &&
    !run.pendingSpec &&
    run.desiredState === 'STOPPED' &&
    run.status === 'STOPPED' &&
    run.observedState === 'STOPPED'
  );
}
export function formatTime(value?: string | null) {
  if (!value) return '—';
  const time = new Date(value);
  return Number.isNaN(time.getTime()) ? value : time.toLocaleString();
}
