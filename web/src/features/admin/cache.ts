import type { QueryClient } from '@tanstack/react-query';
// 平台角色与账号修改可能影响任意空间。只失效业务缓存，不伪造落实结果。
export function refreshAdministration(client: QueryClient) {
  for (const queryKey of [
    ['admin'],
    ['me'],
    ['groups'],
    ['group-permissions'],
    ['group-management'],
    ['security-sessions'],
  ])
    void client.invalidateQueries({ queryKey });
}
