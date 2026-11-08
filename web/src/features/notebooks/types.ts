import type { Notebook } from '@/lib/types';

export type NotebookPanelScope = { groupId: string; id: string; identity: string };

export type NotebookDetail = Notebook & {
  env: { name: string; hasValue: boolean; secretName: string; secretKey: string }[];
  labels: Record<string, string>;
};
export type NotebookAccess = {
  ready: boolean;
  reason: string;
  url?: string;
  serverType: string;
  observedAt: string | null;
};
export type NotebookEvent = {
  kind: string;
  name: string;
  uid: string;
  type: string;
  reason: string;
  message: string;
  count: number;
  at: string;
};
export type NotebookEvents = { items: NotebookEvent[]; truncated: boolean; observedAt: string };
export type NotebookLogs = {
  pod: string;
  podUID: string;
  container: string;
  text: string;
  truncated: boolean;
  observedAt: string;
};
export type NotebookDiagnostics = {
  state: string;
  reason?: string;
  observedAt: string;
  pods: {
    name: string;
    uid: string;
    phase: string;
    ready: boolean;
    reason?: string;
    restarts: number;
  }[];
  idleReclamationAvailable: boolean;
  idleReclamationReason: string;
};
export type NotebookUsage = {
  available: boolean;
  reason?: string;
  cpuMilli?: number;
  memoryBytes?: number;
  gpuAvailable: boolean;
  gpuReason: string;
  timestamp?: string;
  window?: string;
  observedAt: string;
};
export type NotebookAction = 'start' | 'stop' | 'delete';
export type NotebookListFilters = {
  search: string;
  scope: string;
  status: string;
  serverType: string;
};
export type NotebookCreateInput = {
  name: string;
  displayName: string;
  description: string;
  imageId: string;
  serverType: string;
  cpu: string;
  memory: string;
  gpu: number;
  idleStopMinutes: number;
  workspaceSize: string;
  workspaceId?: number;
  workspaceRevision?: number;
};
export type NotebookUpdateInput = {
  revision: number;
  displayName?: string;
  description?: string;
  idleStopMinutes?: number;
  cpu?: string;
  memory?: string;
  gpu?: number;
};
