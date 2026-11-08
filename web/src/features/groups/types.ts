export type Quota = { cpu: string; memory: string; gpu: number };
export type Budget = { cpuMilli: number; memoryBytes: number; gpu: number };
export type QuotaView = {
  requested: Quota;
  observed: unknown;
  snapshot: {
    groupId: number;
    groupRevision: number;
    namespaceUID: string;
    hard: Budget;
    used: Budget;
    readStartedAt: string;
  } | null;
  reserved: Budget;
  remaining: Budget;
};
export type Role = {
  id: number;
  code: string;
  name: string;
  description: string;
  accessLevel: string;
  permissions: string[];
};
export type RoleGrant = { roleId: number; expiresAt?: string };
export type BindingVersion = { id: number; version: number };
export type Binding = BindingVersion & {
  roleId: number;
  roleCode: string;
  expiresAt: string | null;
};
export type Member = {
  userId: number;
  username: string;
  displayName: string;
  roles: Binding[];
  desiredLevel: string;
  observedLevel: string;
  accessStatus: string;
  lastError: string | null;
};
export type AccessState = {
  userId: number;
  username: string;
  userStatus: string;
  desiredLevel: string;
  observedLevel: string;
  desiredVersion: number;
  appliedVersion: number;
  status: string;
  observedAt: string | null;
  lastError: string | null;
  operationId: string | null;
};
export type GroupAction = 'disable' | 'restore' | 'delete';
export type LifecycleBody = { revision: number; confirmName: string; reason: string };
export type Metadata = { displayName: string; description: string };
