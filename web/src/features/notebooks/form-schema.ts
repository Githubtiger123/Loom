import { z } from 'zod';

export const gpuOptions = ['0', '1', '2', '4'] as const;
export const idleOptions = ['0', '60', '240', '480'] as const;
const text = (length: number) =>
  z.string().refine((value) => [...value].length <= length, `最多 ${length} 字`);
const quantity = (label: string) => z.string().trim().min(1, `请输入${label}`).max(32, '规格过长');
export const notebookSettingsSchema = z.object({
  displayName: text(128),
  description: text(1000),
  cpu: quantity('CPU'),
  memory: quantity('内存'),
  gpu: z.enum(gpuOptions),
  idleStopMinutes: z.enum(idleOptions),
});
export const notebookCreateSchema = notebookSettingsSchema
  .extend({
    name: z
      .string()
      .trim()
      .min(1, '请输入实例名称')
      .max(63, '名称最长 63 字符')
      .regex(/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/, '使用小写字母、数字或连字符，首尾须为字母或数字'),
    imageId: z.string().min(1, '请选择运行镜像'),
    workspaceSize: quantity('工作目录容量'),
    workspaceMode: z.enum(['new', 'reuse']),
    workspaceSelection: z.string(),
  })
  .superRefine((v, ctx) => {
    const parts = v.workspaceSelection.split('/');
    if (
      v.workspaceMode === 'reuse' &&
      (parts.length !== 2 ||
        !parts.every(
          (part) => /^\d+$/.test(part) && Number.isSafeInteger(Number(part)) && Number(part) > 0,
        ))
    )
      ctx.addIssue({
        code: 'custom',
        path: ['workspaceSelection'],
        message: '请选择一个可复用目录',
      });
  });
export type NotebookSettingsValues = z.infer<typeof notebookSettingsSchema>;
export type NotebookCreateValues = z.infer<typeof notebookCreateSchema>;
