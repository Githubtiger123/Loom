import { ApiError } from '@/lib/api';
import type { Workspace } from './types';

export const validResourceId = (id: string) =>
  /^\d+$/.test(id) && Number.isSafeInteger(Number(id)) && Number(id) > 0;
export const accessLost = (error: unknown) =>
  error instanceof ApiError && [401, 403, 404].includes(error.status);
export const staleCommand = (error: unknown) =>
  error instanceof ApiError && [401, 403, 404, 409].includes(error.status);
export function readyWorkspace(row: Workspace) {
  return (
    !row.deletedAt &&
    !!row.pvcUID &&
    !!row.observedAt &&
    row.ownership === 'EXCLUSIVE' &&
    ['AVAILABLE', 'BOUND'].includes(row.status)
  );
}
export function ownsWorkspace(row: Workspace, userId?: number, manageAny = false) {
  return manageAny || (row.ownerUserId !== null && row.ownerUserId === userId);
}
export const canDisposeWorkspace = (status: string) =>
  ['ACTIVE', 'DISABLED', 'DEGRADED'].includes(status);
