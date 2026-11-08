import { z } from 'zod';
import { validResourceId } from './policy';

export const workspaceDeleteSchema = z.object({
  confirmName: z.string().min(1, '请输入 PVC 名称'),
  reason: z
    .string()
    .trim()
    .min(1, '请填写处置原因')
    .refine((v) => [...v].length <= 1000, '最多 1000 字'),
  releaseRetention: z.boolean(),
});
export const notebookReferenceSchema = z.object({
  notebookId: z.string().refine(validResourceId, '请输入有效的开发环境 ID'),
});
export type WorkspaceDeleteValues = z.infer<typeof workspaceDeleteSchema>;
export type NotebookReferenceValues = z.infer<typeof notebookReferenceSchema>;
