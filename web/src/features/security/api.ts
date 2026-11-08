import { api } from '@/lib/api';
import type { SessionPage } from './types';
const base = (target?: string) => (target ? `/users/${target}/auth/sessions` : '/auth/sessions');
export const sessionKeys = (target?: string) => ['security-sessions', target ?? 'self'] as const;
export const sessionApi = {
  list: (after: string, target?: string, signal?: AbortSignal) =>
    api<SessionPage>(`${base(target)}?limit=50&after=${encodeURIComponent(after)}`, { signal }),
  revoke: (familyId: string, target?: string) =>
    api<{ revoked: boolean; loginRequired?: boolean }>(
      `${base(target)}/${encodeURIComponent(familyId)}`,
      { method: 'DELETE' },
    ),
  logoutAll: () => api('/auth/logout-all', { method: 'POST' }),
};
