import { api } from '@/lib/api';
import type { Operation, Page } from '@/lib/types';
import type { OwnerTransferInput } from '@/features/ownership/types';
import type { Workspace, WorkspaceDeleteInput, WorkspaceLinkInput } from './types';

export const storageKeys = {
  all: (groupId: string) => ['resources', groupId, 'workspaces'] as const,
  list: (groupId: string) => [...storageKeys.all(groupId), 'list'] as const,
  detail: (groupId: string, id: string) => [...storageKeys.all(groupId), 'detail', id] as const,
};
export const storagePath = (groupId: string, id?: string | number) => {
  for (const value of [groupId, ...(id === undefined ? [] : [String(id)])]) {
    if (!/^\d+$/.test(value) || !Number.isSafeInteger(Number(value)) || Number(value) <= 0)
      throw new Error('空间或目录 ID 无效');
  }
  return `/groups/${groupId}/workspaces${id === undefined ? '' : `/${id}`}`;
};
export const storageApi = {
  list: (gid: string, after: number, signal?: AbortSignal) =>
    api<Page<Workspace>>(`${storagePath(gid)}?limit=50&after=${after}`, { signal }),
  detail: (gid: string, id: string, signal?: AbortSignal) =>
    api<Workspace>(storagePath(gid, id), { signal }),
  link: (
    gid: string,
    id: number,
    action: 'attach' | 'detach',
    body: WorkspaceLinkInput,
    key: string,
  ) => api<Operation>(`${storagePath(gid, id)}/${action}`, { method: 'POST', body, key }),
  remove: (gid: string, id: number, body: WorkspaceDeleteInput, key: string) =>
    api<Operation>(storagePath(gid, id), { method: 'DELETE', body, key }),
  transfer: (gid: string, id: number, body: OwnerTransferInput, key: string) =>
    api<Operation>(`${storagePath(gid, id)}/owner-transfer`, { method: 'POST', body, key }),
};
