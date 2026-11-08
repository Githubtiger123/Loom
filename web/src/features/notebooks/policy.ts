import type { Notebook } from '@/lib/types';

export function canChangeRuntime(row: Notebook) {
  return (
    row.status === 'STOPPED' &&
    row.desiredState === 'STOPPED' &&
    row.observedState === 'STOPPED' &&
    row.appliedRevision === row.revision
  );
}
export function isNotebookBusy(row: Notebook) {
  return ['CREATING', 'STARTING', 'STOPPING', 'UPDATING', 'DELETING', 'PENDING'].includes(
    row.status,
  );
}
export function ownsNotebook(row: Notebook, userId?: number, canReadAny = false) {
  return canReadAny || (row.ownerUserId !== null && row.ownerUserId === userId);
}
export function canTransferNotebook(row: Notebook, userId?: number, transferAny = false) {
  return transferAny || (row.ownerUserId !== null && row.ownerUserId === userId);
}
