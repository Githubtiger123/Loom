import { api, ApiError } from '@/lib/api';
import type { Operation, Page } from '@/lib/types';
import type {
  Artifact,
  Model,
  Prediction,
  ServingDiagnostics,
  ServingLogQuery,
  ServingLogs,
  ServingPreflight,
  ServingRequest,
  ServingRuntimeList,
  ServingHistory,
  ServingRevision,
  ServingService,
  Version,
} from './types';

export function modelPath(g: string, m?: string | number, v?: string | number) {
  for (const id of [g, m, v].filter((value) => value !== undefined)) {
    if (!/^\d+$/.test(String(id)) || !Number.isSafeInteger(Number(id)) || Number(id) <= 0)
      throw new Error('模型或空间 ID 无效');
  }
  return `/groups/${g}/models${m === undefined ? '' : `/${m}`}${v === undefined ? '' : `/versions/${v}`}`;
}
export function servingPath(g: string, id?: string | number) {
  modelPath(g, id);
  return `/groups/${g}/kserve-services${id === undefined ? '' : `/${id}`}`;
}
export const modelKeys = {
  all: (g: string) => ['resources', g, 'models'] as const,
  query: (g: string, ...parts: unknown[]) => [...modelKeys.all(g), ...parts] as const,
};
export const servingKeys = {
  all: (g: string) => ['resources', g, 'kserve-services'] as const,
  query: (g: string, ...parts: unknown[]) => [...servingKeys.all(g), ...parts] as const,
};
export const noAccess = () => new ApiError(403, 'FORBIDDEN', '需要当前空间权限及对象访问权限');
export const registryApi = {
  models: (g: string, scope: string, archived: boolean, after: number, signal: AbortSignal) =>
    api<Page<Model>>(
      `${modelPath(g)}?scope=${scope}&includeDeleted=${archived}&after=${after}&limit=50`,
      { signal },
    ),
  model: (g: string, m: string, signal: AbortSignal) => api<Model>(modelPath(g, m), { signal }),
  versions: (g: string, m: string, archived: boolean, after: number, signal: AbortSignal) =>
    api<Page<Version>>(
      `${modelPath(g, m)}/versions?includeDeleted=${archived}&after=${after}&limit=50`,
      { signal },
    ),
  version: (g: string, m: string, v: string, signal: AbortSignal) =>
    api<Version>(modelPath(g, m, v), { signal }),
  artifacts: (
    g: string,
    m: string,
    v: string,
    archived: boolean,
    after: number,
    signal: AbortSignal,
  ) =>
    api<Page<Artifact>>(
      `${modelPath(g, m, v)}/artifacts?includeDeleted=${archived}&after=${after}&limit=50`,
      { signal },
    ),
  artifact: (g: string, m: string, v: string, a: string, signal: AbortSignal) =>
    api<Artifact>(`${modelPath(g, m, v)}/artifacts/${encodeURIComponent(a)}`, { signal }),
  command: (path: string, method: string, body: unknown, key: string) =>
    api<Operation>(path, { method, body, key }),
  services: (g: string, scope: string, after: number, signal: AbortSignal) =>
    api<Page<ServingService>>(`${servingPath(g)}?scope=${scope}&after=${after}&limit=50`, {
      signal,
    }),
  service: (g: string, id: string, signal: AbortSignal) =>
    api<ServingService>(servingPath(g, id), { signal }),
  runtimes: (g: string, signal: AbortSignal) => {
    modelPath(g);
    return api<ServingRuntimeList>(`/groups/${g}/kserve-runtimes`, { signal });
  },
  preflight: (g: string, body: ServingRequest, signal: AbortSignal) => {
    modelPath(g);
    return api<ServingPreflight>(`/groups/${g}/kserve-preflight`, { method: 'POST', body, signal });
  },
  diagnostics: (g: string, id: number, signal: AbortSignal) =>
    api<ServingDiagnostics>(`${servingPath(g, id)}/diagnostics`, { signal }),
  logs: (g: string, id: number, query: ServingLogQuery, signal: AbortSignal) => {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(query)) params.set(key, String(value));
    return api<ServingLogs>(`${servingPath(g, id)}/logs?${params}`, { signal });
  },
  revisions: (g: string, id: string, after: number, signal: AbortSignal) =>
    api<Page<ServingRevision>>(`${servingPath(g, id)}/revisions?after=${after}&limit=50`, {
      signal,
    }),
  history: (g: string, id: string, after: number, signal: AbortSignal) =>
    api<Page<ServingHistory>>(`${servingPath(g, id)}/history?after=${after}&limit=50`, { signal }),
  predict: (g: string, row: ServingService, payload: unknown, key: string) =>
    api<Prediction>(`${servingPath(g, row.id)}/predict`, {
      method: 'POST',
      key,
      body: { revision: row.revision, instanceId: row.instanceId, payload },
    }),
};
