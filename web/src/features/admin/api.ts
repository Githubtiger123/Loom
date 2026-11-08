import { api } from '@/lib/api';
import type { Operation } from '@/lib/types';
import type { Audit, Binding, ManagedUser, Permission, Role, RoleBody, UserRecord } from './types';
export const adminKeys = {
  users: ['admin', 'users'] as const,
  user: (id: string) => ['admin', 'user', id] as const,
  bindings: (id: string) => ['admin', 'bindings', id] as const,
  roles: ['admin', 'roles'] as const,
  permissions: ['admin', 'permissions'] as const,
  audits: ['admin', 'audits'] as const,
};
export const adminApi = {
  users: (after: number, signal?: AbortSignal) =>
    api<UserRecord[]>(`/users?limit=50&after=${after}`, { signal }),
  user: (id: string, signal?: AbortSignal) => api<ManagedUser>(`/users/${id}`, { signal }),
  create: (local: boolean, body: unknown, key: string) =>
    api<Operation>(local ? '/local-users' : '/users', { method: 'POST', body, key }),
  state: (
    id: number,
    action: 'enable' | 'disable',
    body: {
      subject: string;
      externalIdentityConfirmed: boolean;
      expectedAuthorizationVersion: number;
    },
    key: string,
  ) => api<Operation>(`/users/${id}/${action}`, { method: 'POST', body, key }),
  resetPassword: (id: number, password: string) =>
    api<{ loginRequired: boolean }>(`/users/${id}/password`, { method: 'PUT', body: { password } }),
  bindings: (id: string, signal?: AbortSignal) => api<Binding[]>(`/users/${id}/roles`, { signal }),
  replaceBindings: (
    id: string,
    body: {
      roles: { roleId: number; expiresAt?: string }[];
      expectedBindings: { id: number; version: number }[];
    },
    key: string,
  ) => api<Operation>(`/users/${id}/roles`, { method: 'PUT', body, key }),
  roles: (signal?: AbortSignal) => api<Role[]>('/roles', { signal }),
  permissions: (signal?: AbortSignal) => api<Permission[]>('/permissions', { signal }),
  saveRole: (id: string | undefined, body: RoleBody, key: string) =>
    api<Operation>(id ? `/roles/${id}` : '/roles', { method: id ? 'PUT' : 'POST', body, key }),
  audits: (after: number, signal?: AbortSignal) =>
    api<Audit[]>(`/audits?limit=50&after=${after}`, { signal }),
};
