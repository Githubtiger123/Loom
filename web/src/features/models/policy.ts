import { useGroupPermissions } from '@/lib/use-group-permissions';
import type { Artifact, Model, Version } from './types';
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
