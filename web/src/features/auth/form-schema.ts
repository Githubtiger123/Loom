import { z } from 'zod';

export const passwordSchema = z
  .object({
    currentPassword: z.string().min(1, '请输入当前密码'),
    newPassword: z.string().refine((value) => {
      const length = new TextEncoder().encode(value).length;
      return length >= 12 && length <= 1024;
    }, '新密码须为 12–1024 字节'),
    confirm: z.string(),
  })
  .refine((value) => value.newPassword === value.confirm, {
    path: ['confirm'],
    message: '两次输入的密码不一致',
  });
export type PasswordValues = z.infer<typeof passwordSchema>;
