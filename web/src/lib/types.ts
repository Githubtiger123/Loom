export type Page<T> = { items: T[]; nextAfter: number | null };
export type User = { id: number; username: string; displayName: string; status: string };
export type Me = {
  user: User & {
    email: string | null;
    issuer: string;
    createdAt: string;
    lastLoginAt: string | null;
  };
  permissions: string[];
};
export type Tokens = {
  accessToken: string;
  expiresIn: number;
  userId: number;
  passwordChangeRequired: boolean;
};
export type Group = {
  id: number;
  name: string;
  displayName: string;
  description: string;
  status: string;
  revision: number;
  cluster: string;
  infrastructureOwner: string;
  observedState: string;
  observedAt: string | null;
  lastError: string | null;
  quotaRequested: { cpu: string; memory: string; gpu: number };
};
export type Operation = {
  operationId: string;
  status: string;
  step: string;
  lastError: string | null;
  result: unknown;
};
export type Resource = {
  id: number;
  name: string;
  displayName?: string;
  description?: string;
  status?: string;
  revision: number;
  createdAt: string;
  [key: string]: unknown;
};
export type Notebook = Resource & {
  name: string;
  displayName: string;
  description: string;
  status: string;
  ownerUserId: number | null;
  ownerRevision: number;
  imageId: string;
  serverType: string;
  cpuMilli: number;
  memoryBytes: number;
  gpu: number;
  desiredState: string;
  observedState: string;
  statusReason: string | null;
  operationId: string | null;
  appliedRevision: number;
  workspaceSizeBytes: number;
  workspaceId: number;
  workspaceOwnership: string;
  idleStopMinutes: number;
  observedAt: string | null;
  startedAt: string | null;
  stoppedAt: string | null;
  deletedAt: string | null;
  updatedAt: string;
};
export type NotebookImage = { id: string; name: string; category: string; serverType: string };
