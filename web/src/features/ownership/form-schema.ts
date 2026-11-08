import { z } from 'zod';

export const ownerTransferSchema = z.object({
  userId: z
    .string()
    .refine(
      (v) => /^\d+$/.test(v) && Number.isSafeInteger(Number(v)) && Number(v) > 0,
      '请输入有效用户 ID',
    ),
  reason: z
    .string()
    .trim()
    .min(1, '请填写交接原因')
    .refine((v) => [...v].length <= 1000, '最多 1000 字'),
  confirmName: z.string().min(1, '请填写确认名称'),
});
export type OwnerTransferValues = z.infer<typeof ownerTransferSchema>;
