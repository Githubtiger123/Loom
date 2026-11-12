import { useGroupPermissions } from '@/lib/use-group-permissions';
import type { Artifact, Model, ServingRuntime, Version } from './types';
export function useModelAccess(groupId: string) {
  const query = useGroupPermissions(groupId);
  return {
    ...query,
    allows: (code: string, row?: { createdBy: number | null }, domain = 'model') =>
      query.can(code) &&
      (!row ||
        query.can(`${domain}.read.any`) ||
        (row.createdBy !== null && row.createdBy === query.data?.userId)),
  };
}
export const activeModel = (row: Model) => row.status === 'ACTIVE' && !row.deletedAt;
export const activeVersion = (row: Version) => row.status !== 'ARCHIVED' && !row.deletedAt;
export const publishable = (model: Model, version: Version, artifact: Artifact) =>
  activeModel(model) &&
  version.status === 'READY' &&
  !version.deletedAt &&
  artifact.status === 'ACTIVE' &&
  !artifact.deletedAt;

export function runtimeMismatch(
  runtime: ServingRuntime,
  format: string,
  version: string,
  protocol: string,
) {
  if (runtime.disabled) return '已禁用';
  if (runtime.multiModel || runtime.multiNode) return '首期不支持多模型／多节点 Runtime';
  if (runtime.protocols.length && !runtime.protocols.includes(protocol)) return '协议不匹配';
  if (!runtime.formats.some((f) => f.name === format && (!version || f.version === version)))
    return '模型格式或版本不匹配';
  return '';
}
