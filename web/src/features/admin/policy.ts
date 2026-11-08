import { ApiError } from '@/lib/api';
export const adminSections = [
  { path: '/admin/users', label: '用户管理', permission: 'user.read' },
  { path: '/admin/users/new', label: '登记用户', permission: 'user.manage' },
  { path: '/admin/roles', label: '角色与权限', permission: 'role.manage' },
  { path: '/admin/audits', label: '审计记录', permission: 'audit.read' },
];
export const validOperationId = (id: string) =>
  /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(id);
export const levelLabels = { none: '无原生访问', view: '只读访问', edit: '读写访问' };
export const isAccessDenied = (error: unknown) =>
  error instanceof ApiError && [401, 403].includes(error.status);
