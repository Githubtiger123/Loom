import { api, apiDownload, ApiError } from '@/lib/api';
import type { Operation, Page } from '@/lib/types';
import type {
  ArtifactResult,
  ArtifactFile,
  ArtifactFiles,
  Attempt,
  Compatibility,
  DAG,
  LiveLog,
  LogChunk,
  Manifest,
  NodeDetail,
  Pipeline,
  Run,
  Runtime,
  Version,
  Visualizations,
} from './types';

const segment = (value: string | number) => encodeURIComponent(String(value));
export function pipelinePath(groupId: string, id?: string | number) {
  for (const value of [groupId, ...(id === undefined ? [] : [id])]) {
    if (!/^\d+$/.test(String(value)) || !Number.isSafeInteger(Number(value)) || Number(value) <= 0)
      throw new Error('流水线或空间 ID 无效');
  }
  return `/groups/${groupId}/pipelines${id === undefined ? '' : `/${id}`}`;
}
export function runPath(groupId: string, id: string | number) {
  pipelinePath(groupId, id);
  return `/groups/${groupId}/pipeline-runs/${id}`;
}
export const pipelineKeys = {
  all: (g: string) => ['resources', g, 'pipelines'] as const,
  query: (g: string, ...parts: unknown[]) => [...pipelineKeys.all(g), ...parts] as const,
};
export const pollPipeline = (query: { state: { error: unknown } }) =>
  query.state.error ? (false as const) : 5000;
export const noAccess = () => new ApiError(403, 'FORBIDDEN', '需要流水线权限及对象访问权限');
export const pipelineApi = {
  list: (g: string, scope: string, after: number, signal: AbortSignal, includeDeleted = false) =>
    api<Page<Pipeline>>(
      `${pipelinePath(g)}?scope=${scope}&limit=50&after=${after}&includeDeleted=${includeDeleted}`,
      { signal },
    ),
  detail: (g: string, id: string, signal: AbortSignal) =>
    api<Pipeline>(pipelinePath(g, id), { signal }),
  versions: (g: string, id: string, after: number, signal: AbortSignal) =>
    api<Page<Version>>(`${pipelinePath(g, id)}/versions?limit=50&after=${after}`, { signal }),
  version: (g: string, id: string, vid: string, signal: AbortSignal) =>
    api<Version>(`${pipelinePath(g, id)}/versions/${segment(vid)}`, { signal }),
  compatibility: (g: string, id: string, version: Version, signal: AbortSignal) =>
    api<Compatibility>(
      `${pipelinePath(g, id)}/versions/${version.id}/native-compatibility?revision=${version.revision}`,
      { signal },
    ),
  runs: (
    g: string,
    id: string | undefined,
    scope: string,
    after: number,
    signal: AbortSignal,
    includeDeleted = false,
  ) =>
    api<Page<Run>>(
      `${id ? `${pipelinePath(g, id)}/runs` : `/groups/${g}/pipeline-runs`}?scope=${scope}&limit=50&after=${after}&includeDeleted=${includeDeleted}`,
      { signal },
    ),
  run: (g: string, id: string, signal: AbortSignal) => api<Run>(runPath(g, id), { signal }),
  create: (path: string, body: unknown, key: string) =>
    api<Operation>(path, { method: 'POST', body, key }),
  command: (path: string, method: 'POST' | 'PUT' | 'DELETE', body: unknown, key: string) =>
    api<Operation>(path, { method, body, key }),
  runtime: (g: string, run: Run, signal: AbortSignal) =>
    api<Runtime>(`${runPath(g, run.id)}/runtime`, { signal }),
  dag: (g: string, run: Run, signal: AbortSignal) =>
    api<DAG>(`${runPath(g, run.id)}/dag`, { signal }),
  node: (g: string, run: Run, node: string, signal: AbortSignal) =>
    api<NodeDetail>(
      `${runPath(g, run.id)}/nodes/${segment(node)}?instanceId=${segment(run.instanceId)}`,
      { signal },
    ),
  visualizations: (g: string, run: Run, signal: AbortSignal) =>
    api<Visualizations>(
      `${runPath(g, run.id)}/visualizations?instanceId=${segment(run.instanceId)}`,
      { signal },
    ),
  preview: (g: string, run: Run, artifact: string, signal: AbortSignal) =>
    api<ArtifactResult>(
      `${runPath(g, run.id)}/artifacts/${segment(artifact)}/preview?instanceId=${segment(run.instanceId)}`,
      { signal },
    ),
  files: (g: string, run: Run, artifact: string, signal: AbortSignal) =>
    api<ArtifactFiles>(
      `${runPath(g, run.id)}/artifacts/${segment(artifact)}/files?instanceId=${segment(run.instanceId)}`,
      { signal },
    ),
  filePreview: (
    g: string,
    run: Run,
    listing: ArtifactFiles,
    file: ArtifactFile,
    signal: AbortSignal,
  ) =>
    api<ArtifactResult>(
      `${runPath(g, run.id)}/artifacts/${segment(listing.artifactId)}/files/preview?${new URLSearchParams({ instanceId: run.instanceId, path: file.name, manifestDigest: listing.manifestDigest, etag: file.etag })}`,
      { signal },
    ),
  fileDownload: (
    g: string,
    run: Run,
    listing: ArtifactFiles,
    file: ArtifactFile,
    signal: AbortSignal,
  ) =>
    apiDownload(
      `${runPath(g, run.id)}/artifacts/${segment(listing.artifactId)}/files/download?${new URLSearchParams({ instanceId: run.instanceId, path: file.name, manifestDigest: listing.manifestDigest, etag: file.etag })}`,
      signal,
    ),
  download: (g: string, run: Run, artifact: string, signal: AbortSignal) =>
    apiDownload(
      `${runPath(g, run.id)}/artifacts/${segment(artifact)}/download?instanceId=${segment(run.instanceId)}`,
      signal,
    ),
  logs: (g: string, run: Run, task: string, previous: boolean, signal: AbortSignal) =>
    api<LiveLog>(
      `${runPath(g, run.id)}/tasks/${segment(task)}/logs?${new URLSearchParams({ follow: 'false', previous: String(previous), tailLines: '500', limitBytes: '262144', timestamps: 'true' })}`,
      { signal },
    ),
  attempts: (g: string, run: Run, after: number, signal: AbortSignal) =>
    api<Attempt[]>(`${runPath(g, run.id)}/attempts?limit=50&after=${after}`, { signal }),
  archives: (g: string, run: Run, instance: string, signal: AbortSignal) =>
    api<Manifest>(`${runPath(g, run.id)}/log-archives?instanceId=${segment(instance)}`, { signal }),
  chunk: (
    g: string,
    run: Run,
    instance: string,
    entry: string,
    etag: string,
    offset: number,
    signal: AbortSignal,
  ) =>
    api<LogChunk>(
      `${runPath(g, run.id)}/log-archives/${segment(entry)}?${new URLSearchParams({ instanceId: instance, etag, offset: String(offset), limitBytes: '262144' })}`,
      { signal },
    ),
};
