import { useQuery } from '@tanstack/react-query';
import { api } from './api';

type GroupPermissions = {
  userId: number;
  groupId: number;
  permissions: string[];
  evaluatedAt: string;
};

export function useGroupPermissions(groupId: string) {
  const query = useQuery({
    queryKey: ['group-permissions', groupId],
    queryFn: ({ signal }) => api<GroupPermissions>(`/groups/${groupId}/me/permissions`, { signal }),
    staleTime: 15_000,
    refetchInterval: 30_000,
    enabled: /^\d+$/.test(groupId) && Number.isSafeInteger(Number(groupId)) && Number(groupId) > 0,
  });
  // Fail closed even when a background refresh fails with cached data present.
  return {
    ...query,
    can: (code: string) => query.isSuccess && query.data.permissions.includes(code),
  };
}
