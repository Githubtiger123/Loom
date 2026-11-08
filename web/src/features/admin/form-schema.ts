import { z } from 'zod';
const text = (max: number) => z.string().refine((v) => [...v].length <= max, `最多 ${max} 字`);
const required = (max: number) => text(max).refine((v) => !!v.trim(), '此项必填');
export const validId = (id: string) => /^[1-9]\d*$/.test(id) && Number.isSafeInteger(Number(id));
export const createUserSchema = z.object({
  username: required(255),
  displayName: text(128),
  email: z
    .string()
    .refine((v) => new TextEncoder().encode(v).length <= 255, '邮箱最多 255 字节')
    .refine((v) => !v || /^[^\s@]+@[^\s@]+$/.test(v), '邮箱格式无效，具体格式由后端校验'),
  issuer: text(255),
  subject: text(255),
  externalIdentityConfirmed: z.boolean(),
});
export const roleSchema = z.object({
  code: z
    .string()
    .regex(/^[a-z][a-z0-9_]{0,63}$/, '以小写字母开头，使用小写字母、数字、下划线，最长 64 字符'),
  name: required(128),
  scope: z.enum(['global', 'group']),
  accessLevel: z.enum(['none', 'view', 'edit']),
  enabled: z.boolean(),
  description: z
    .string()
    .refine((v) => new TextEncoder().encode(v).length <= 1000, '描述最多 1000 字节'),
  permissions: z.array(z.string()),
});
export const resetPasswordSchema = z
  .object({
    password: z.string().refine((v) => {
      const n = new TextEncoder().encode(v).length;
      return n >= 12 && n <= 1024;
    }, '密码须为 12–1024 字节'),
    confirm: z.string(),
  })
  .refine((v) => v.password === v.confirm, { path: ['confirm'], message: '两次密码不一致' });
export const globalGrantsSchema = z.object({
  roles: z
    .array(
      z.object({
        roleId: z.number().int().positive(),
        expiresAt: z
          .string()
          .refine(
            (v) => !v || (Number.isFinite(Date.parse(v)) && Date.parse(v) > Date.now()),
            '有效期必须在未来',
          ),
      }),
    )
    .max(32, '最多 32 个角色'),
  confirmId: z.string(),
});
export type CreateUserValues = z.infer<typeof createUserSchema>;
export type RoleValues = z.infer<typeof roleSchema>;
export type ResetPasswordValues = z.infer<typeof resetPasswordSchema>;
export type GlobalGrantsValues = z.infer<typeof globalGrantsSchema>;
