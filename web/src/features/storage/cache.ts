import type { QueryClient } from '@tanstack/react-query';
import { notebookKeys } from '@/features/notebooks/api';
import { storageKeys } from './api';

export function refreshStorage(client: QueryClient, groupId: string) {
  for (const queryKey of [
    storageKeys.all(groupId),
    notebookKeys.all(groupId),
    ['group-permissions', groupId],
  ])
    void client.invalidateQueries({ queryKey });
}
