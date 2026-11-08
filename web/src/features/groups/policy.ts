import type { Group } from '@/lib/types';
import type { GroupAction } from './types';
export function canLifecycle(group: Group, action: GroupAction) {
  if (action === 'restore') return group.status === 'DISABLED';
  return (
    action === 'delete' ? ['ACTIVE', 'DEGRADED', 'DISABLED'] : ['ACTIVE', 'DEGRADED']
  ).includes(group.status);
}
export const canManageMembers = (status: string) =>
  ['ACTIVE', 'DEGRADED', 'CREATING'].includes(status);
export const canRemoveMembers = (status: string) =>
  canManageMembers(status) || status === 'DISABLED';
export const canChangeQuota = (status: string) => ['ACTIVE', 'DISABLED'].includes(status);
const accessLabels: Record<string, string> = {
  none: '无原生访问',
  view: '只读',
  edit: '读写',
  unknown: '待确认',
};
export const accessLabel = (level: string) => accessLabels[level] ?? level;
