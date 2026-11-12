import { z } from 'zod';
import type { ParameterSchema, ParameterValue } from './types';

export const pipelineName = z
  .string()
  .min(1, '请填写名称')
  .max(63, '名称最多 63 字符')
  .regex(/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/, '使用小写字母、数字和中划线');
export const createPipelineSchema = z.object({
  name: pipelineName,
  displayName: z.string().max(128),
  description: z.string().max(1000),
});
export const importVersionSchema = z.object({
  name: pipelineName,
  packageYaml: z
    .string()
    .min(1, '请导入 SDK 编译的 IR YAML')
    .refine((value) => new TextEncoder().encode(value).length <= 512 * 1024, 'IR 不能超过 512 KiB'),
});

export function parameterValues(
  schema: ParameterSchema[],
  values: Record<string, string>,
  supplied: Record<string, boolean>,
): ParameterValue[] {
  return schema.flatMap<ParameterValue>((parameter) => {
    if (!supplied[parameter.name]) {
      if (parameter.required && parameter.default === undefined)
        throw new Error(`请填写参数 ${parameter.name}`);
      return [];
    }
    const value = values[parameter.name] ?? '';
    if (new TextEncoder().encode(value).length > 4096 || value.includes('\0'))
      throw new Error(`${parameter.name} 不能超过 4 KiB`);
    if (parameter.type === 'CREDENTIAL') {
      if (!/^[a-zA-Z0-9._-]{1,64}$/.test(value))
        throw new Error(`${parameter.name} 需要受管凭据引用`);
      return [{ name: parameter.name, credentialRef: { ref: value } }];
    }
    if (parameter.type === 'INTEGER') {
      if (
        !/^[+-]?\d+$/.test(value) ||
        BigInt(value) < -(1n << 63n) ||
        BigInt(value) > (1n << 63n) - 1n
      )
        throw new Error(`${parameter.name} 需要有符号 64 位整数`);
      return [{ name: parameter.name, value }];
    }
    if (parameter.type !== 'STRING') {
      let parsed: unknown;
      try {
        parsed = JSON.parse(value);
      } catch {
        throw new Error(`${parameter.name} 需要有效的 ${parameter.type} JSON 值`);
      }
      const valid =
        parameter.type === 'BOOLEAN'
          ? typeof parsed === 'boolean'
          : parameter.type === 'LIST'
            ? Array.isArray(parsed)
            : parameter.type === 'STRUCT'
              ? parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)
              : typeof parsed === 'number' && Number.isFinite(parsed);
      if (!valid) throw new Error(`${parameter.name} 与 ${parameter.type} 类型不符`);
    }
    return [{ name: parameter.name, value }];
  });
}
