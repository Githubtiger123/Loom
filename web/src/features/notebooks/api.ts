import { api } from '@/lib/api';
import type { Notebook, NotebookImage, Operation, Page } from '@/lib/types';
import type { OwnerTransferInput } from '@/features/ownership/types';
import type {
  NotebookAction,
  NotebookAccess,
  NotebookCreateInput,
  NotebookDetail,
  NotebookDiagnostics,
  NotebookEvents,
  NotebookListFilters,
  NotebookLogs,
  NotebookUsage,
  NotebookUpdateInput,
} from './types';

export const notebookKeys = {
  all: (groupId: string) => ['resources', groupId, 'notebooks'] as const,
  list: (groupId: string, filters: NotebookListFilters) =>
    [...notebookKeys.all(groupId), 'list', filters] as const,
  detail: (groupId: string, id: string) => [...notebookKeys.all(groupId), 'detail', id] as const,
  panel: (groupId: string, id: string, panel: string, options?: unknown) =>
    [...notebookKeys.detail(groupId, id), panel, options] as const,
  images: (groupId: string) => ['images', groupId] as const,
};

export const notebookPath = (groupId: string, id?: string | number) => {
  if (
    !/^\d+$/.test(groupId) ||
    !Number.isSafeInteger(Number(groupId)) ||
    Number(groupId) <= 0 ||
    (id !== undefined &&
      (!/^\d+$/.test(String(id)) || !Number.isSafeInteger(Number(id)) || Number(id) <= 0))
  ) {
    throw new Error('开发环境或空间 ID 无效');
  }
  return `/groups/${groupId}/notebooks${id === undefined ? '' : `/${id}`}`;
};

export const notebookApi = {
  transfer: (groupId: string, id: number, body: OwnerTransferInput, key: string) =>
    api<Operation>(`${notebookPath(groupId, id)}/owner-transfer`, { method: 'POST', body, key }),
  list: (groupId: string, filters: NotebookListFilters, after: number, signal?: AbortSignal) =>
    api<Page<Notebook>>(
      `${notebookPath(groupId)}?${new URLSearchParams({ ...filters, after: String(after), limit: '50' })}`,
      { signal },
    ),
  detail: (groupId: string, id: string, signal?: AbortSignal) =>
    api<NotebookDetail>(notebookPath(groupId, id), { signal }),
  images: (groupId: string, signal?: AbortSignal) =>
    api<{ items: NotebookImage[] }>(`/groups/${groupId}/notebook-images`, { signal }),
  access: (groupId: string, id: number, signal?: AbortSignal) =>
    api<NotebookAccess>(`${notebookPath(groupId, id)}/access`, { signal }),
  events: (groupId: string, id: string, signal?: AbortSignal) =>
    api<NotebookEvents>(`${notebookPath(groupId, id)}/events?limit=100`, { signal }),
  diagnostics: (groupId: string, id: string, signal?: AbortSignal) =>
    api<NotebookDiagnostics>(`${notebookPath(groupId, id)}/diagnostics`, { signal }),
  usage: (groupId: string, id: string, signal?: AbortSignal) =>
    api<NotebookUsage>(`${notebookPath(groupId, id)}/usage`, { signal }),
  logs: (
    groupId: string,
    id: string,
    options: { tailLines: string; previous: boolean },
    signal?: AbortSignal,
  ) =>
    api<NotebookLogs>(
      `${notebookPath(groupId, id)}/logs?${new URLSearchParams({ tailLines: options.tailLines, previous: String(options.previous), sinceSeconds: '3600', limitBytes: '262144' })}`,
      { signal },
    ),
  retry: (
    groupId: string,
    id: number,
    body: { revision: number; operationId: string },
    key: string,
  ) => api<Operation>(`${notebookPath(groupId, id)}/retry`, { method: 'POST', body, key }),
  create: (groupId: string, body: NotebookCreateInput, key: string) =>
    api<Operation>(notebookPath(groupId), { method: 'POST', body, key }),
  update: (groupId: string, id: number, body: NotebookUpdateInput, key: string) =>
    api<Operation>(notebookPath(groupId, id), { method: 'PATCH', body, key }),
  action: (groupId: string, row: Notebook, action: NotebookAction, key: string) =>
    api<Operation>(`${notebookPath(groupId, row.id)}${action === 'delete' ? '' : `/${action}`}`, {
      method: action === 'delete' ? 'DELETE' : 'POST',
      body:
        action === 'delete'
          ? { revision: row.revision, deleteWorkspace: false }
          : { revision: row.revision },
      key,
    }),
};
