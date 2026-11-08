import { api } from '@/lib/api';
import type { Operation } from '@/lib/types';
import type {
  AccessState,
  BindingVersion,
  GroupAction,
  LifecycleBody,
  Member,
  Metadata,
  Quota,
  QuotaView,
  Role,
  RoleGrant,
} from './types';

const path = (groupId: string) => `/groups/${groupId}`;
export const groupKeys = {
  all: (groupId: string) => ['group-management', groupId] as const,
  members: (groupId: string) => [...groupKeys.all(groupId), 'members'] as const,
  roles: (groupId: string) => [...groupKeys.all(groupId), 'roles'] as const,
  access: (groupId: string) => [...groupKeys.all(groupId), 'access'] as const,
  quota: (groupId: string) => [...groupKeys.all(groupId), 'quota'] as const,
};
export const groupApi = {
  create: (body: { name: string; displayName: string; quota: Quota }, key: string) =>
    api<Operation>('/groups', { method: 'POST', body, key }),
  metadata: (id: string, body: Partial<Metadata> & { expected: Metadata }, key: string) =>
    api<Operation>(path(id), { method: 'PATCH', body, key }),
  lifecycle: (id: string, action: GroupAction, body: LifecycleBody, key: string) =>
    api<Operation>(`${path(id)}${action === 'delete' ? '' : `/${action}`}`, {
      method: action === 'delete' ? 'DELETE' : 'POST',
      body,
      key,
    }),
  members: (id: string, signal?: AbortSignal) => api<Member[]>(`${path(id)}/members`, { signal }),
  roles: (id: string, signal?: AbortSignal) => api<Role[]>(`${path(id)}/roles`, { signal }),
  replaceRoles: (
    id: string,
    userId: number,
    roles: RoleGrant[],
    expectedBindings: BindingVersion[],
    key: string,
  ) =>
    api<Operation>(`${path(id)}/members/${userId}/roles`, {
      method: 'PUT',
      body: { roles, expectedBindings },
      key,
    }),
  transfer: (id: string, body: { userId: number; keepCurrent: boolean }, key: string) =>
    api<Operation>(`${path(id)}/owner-transfer`, { method: 'POST', body, key }),
  access: (id: string, after: number, signal?: AbortSignal) =>
    api<AccessState[]>(`${path(id)}/access?limit=50&after=${after}`, { signal }),
  quota: (id: string, signal?: AbortSignal) => api<QuotaView>(`${path(id)}/quota`, { signal }),
  updateQuota: (
    id: string,
    body: { revision: number; quota: Quota; reason: string },
    key: string,
  ) => api<Operation>(`${path(id)}/quota`, { method: 'PUT', body, key }),
};
