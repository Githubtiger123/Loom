import { useQuery } from '@tanstack/react-query';
import { api } from './api';
import type { Me } from './types';

export function useGlobalPermissions() {
  const query = useQuery({
    queryKey: ['me'],
    queryFn: ({ signal }) => api<Me>('/me', { signal }),
    staleTime: 15_000,
    refetchInterval: 30_000,
  });
  return {
    ...query,
    can: (code: string) => query.isSuccess && query.data.permissions.includes(code),
  };
}
