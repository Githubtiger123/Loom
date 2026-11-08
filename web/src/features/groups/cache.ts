import type { QueryClient } from '@tanstack/react-query';
import { groupKeys } from './api';

// 更新业务库后立即刷新；集群结果另由各页面和操作单继续观测。
export function refreshGroup(client: QueryClient, id: string) {
  for (const queryKey of [
    groupKeys.all(id),
    ['group', id],
    ['groups'],
    ['group-permissions', id],
  ]) {
    void client.invalidateQueries({ queryKey });
  }
}
