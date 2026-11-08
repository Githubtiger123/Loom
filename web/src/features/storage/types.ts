export type Workspace = {
  id: number;
  instanceId: string;
  groupId: number;
  cluster: string;
  namespace: string;
  pvcName: string;
  pvcUID: string | null;
  ownerUserId: number | null;
  capacityBytes: number;
  usedBytes: number | null;
  ownership: string;
  retained: boolean;
  status: string;
  revision: number;
  observedAt: string | null;
  deletedAt: string | null;
  createdAt: string;
  updatedAt: string;
  notebookIds: number[];
};
export type WorkspaceLinkInput = {
  revision: number;
  notebookId: number;
  notebookRevision: number;
};
export type WorkspaceDeleteInput = {
  revision: number;
  confirmName: string;
  reason: string;
  releaseRetention: boolean;
};
