import { useInfiniteQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { Group } from '@/lib/types';

export function useGroups() {
  return useInfiniteQuery({
    queryKey: ['groups'],
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) =>
      api<Group[]>(`/groups?limit=50&after=${pageParam}`, { signal }),
    getNextPageParam: (rows) => (rows.length === 50 ? rows.at(-1)?.id : undefined),
    refetchInterval: (query) => (query.state.error ? false : 10_000),
  });
}
