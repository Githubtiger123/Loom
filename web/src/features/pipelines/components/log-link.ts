import type { Run } from '../types';

export function pipelineLogLink(groupId: string, run: Run, taskId: string, podUID?: string) {
  const params = new URLSearchParams({
    tab: 'logs',
    logSource: podUID || run.executionCleaned || run.deletedAt ? 'archive' : 'live',
    logInstance: run.instanceId,
    logTask: taskId,
  });
  if (podUID) params.set('logPodUID', podUID);
  return `/workspaces/${groupId}/pipeline-runs/${run.id}?${params}`;
}
