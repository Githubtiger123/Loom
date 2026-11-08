import { z } from 'zod';
const text = (max: number) => z.string().refine((v) => [...v].length <= max, `最多 ${max} 字`);
export const reasonSchema = text(1000).refine((v) => !!v.trim(), '请填写操作原因');
const quantity = z.string().trim().min(1, '请填写额度').max(32, '额度过长');
export const quotaSchema = z.object({
  cpu: quantity,
  memory: quantity,
  gpu: z
    .string()
    .regex(/^\d+$/, '请输入非负整数')
    .refine((v) => Number.isSafeInteger(Number(v)), '数值过大'),
});
export const metadataSchema = z.object({ displayName: text(128), description: text(1000) });
export const createGroupSchema = quotaSchema.extend({
  name: z
    .string()
    .trim()
    .min(1, '请输入空间名')
    .max(63, '空间名最长 63 字符')
    .regex(/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/, '使用小写字母、数字和连字符，首尾不能为连字符'),
  displayName: text(128),
});
export const quotaUpdateSchema = quotaSchema.extend({ reason: reasonSchema });
export const userIdSchema = z
  .string()
  .regex(/^[1-9]\d*$/, '请输入已有用户的 ID')
  .refine((v) => Number.isSafeInteger(Number(v)), '用户 ID 超出范围');
export const memberSchema = z.object({
  userId: userIdSchema,
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
    .min(1, '至少选择一个角色；移除请使用成员列表中的移除操作')
    .max(32, '最多绑定 32 个角色'),
});
export const transferSchema = z.object({
  userId: userIdSchema,
  keepCurrent: z.boolean(),
  confirmName: z.string().min(1, '请填写空间名确认'),
});
export type QuotaValues = z.infer<typeof quotaSchema>;
export type QuotaUpdateValues = z.infer<typeof quotaUpdateSchema>;
export type CreateGroupValues = z.infer<typeof createGroupSchema>;
export type MetadataValues = z.infer<typeof metadataSchema>;
export type MemberValues = z.infer<typeof memberSchema>;
export type TransferValues = z.infer<typeof transferSchema>;
