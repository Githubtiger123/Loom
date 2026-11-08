import { api } from '@/lib/api';
export const authApi = {
  changePassword: (body: { currentPassword: string; newPassword: string }) =>
    api('/auth/password', { method: 'PUT', body }),
};
