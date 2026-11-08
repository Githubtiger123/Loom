import type { User } from '@/lib/types';
export type UserRecord = User & {
  issuer: string;
  subject: string | null;
  email: string | null;
  authorizationVersion: number;
  createdAt: string;
  lastLoginAt: string | null;
};
export type ManagedUser = UserRecord & { localAccount: boolean };
export type Binding = {
  id: number;
  version: number;
  roleId: number;
  roleCode: string;
  expiresAt: string | null;
};
export type Role = {
  id: number;
  code: string;
  name: string;
  scope: 'global' | 'group';
  accessLevel: 'none' | 'view' | 'edit';
  enabled: boolean;
  builtin: boolean;
  description: string;
  version: number;
  permissions: string[];
};
export type Permission = {
  id: number;
  code: string;
  name: string;
  scope: 'global' | 'group';
  module: string;
};
export type RoleBody = Omit<Role, 'id' | 'builtin' | 'version'> & { expectedVersion?: number };
export type Audit = {
  id: number;
  operationId: string | null;
  actorId: number | null;
  actorName: string;
  groupId: number | null;
  action: string;
  targetType: string;
  targetId: string;
  outcome: string;
  createdAt: string;
};
