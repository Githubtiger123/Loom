import { z } from 'zod';
export const nameSchema = z
  .string()
  .min(1, '请填写名称')
  .max(63)
  .regex(/^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$/, '名称使用字母、数字、点、下划线或连字符');
export const modelSchema = z.object({
  name: nameSchema,
  displayName: z.string().max(128),
  description: z.string().max(2000),
});
export const descriptionSchema = z.string().max(2000, '描述最多 2000 字');
export const formats = [
  'sklearn',
  'xgboost',
  'tensorflow',
  'pytorch',
  'triton',
  'onnx',
  'huggingface',
  'pmml',
  'lightgbm',
  'paddle',
];
export function artifactPayload(value: {
  name: string;
  uri: string;
  storageType: string;
  modelFormat: string;
  modelFormatVersion: string;
  digest: string;
  sizeBytes: string;
}) {
  nameSchema.parse(value.name);
  nameSchema.parse(value.modelFormat);
  let parsed: URL;
  try {
    parsed = new URL(value.uri);
  } catch {
    throw new Error('请填写有效存储地址');
  }
  const schemes: Record<string, string> = { S3: 's3:', GS: 'gs:', PVC: 'pvc:', OCI: 'oci:' };
  if (
    parsed.protocol !== schemes[value.storageType] ||
    !parsed.host ||
    parsed.pathname.length < 2 ||
    parsed.username ||
    parsed.password ||
    parsed.search ||
    parsed.hash ||
    value.uri.includes('..') ||
    /\s|\0/.test(value.uri) ||
    value.uri.length > 2048
  )
    throw new Error('存储地址与存储类型不匹配，不能含凭据、查询串或上级目录');
  if (value.modelFormatVersion.length > 64) throw new Error('格式版本最多 64 字符');
  if (value.digest && !/^[a-f0-9]{64}$/.test(value.digest))
    throw new Error('SHA256 必须是 64 位小写十六进制');
  const size = value.sizeBytes ? Number(value.sizeBytes) : undefined;
  if (size !== undefined && (!/^\d+$/.test(value.sizeBytes) || !Number.isSafeInteger(size)))
    throw new Error('大小必须是非负安全整数');
  return { ...value, sizeBytes: size };
}
export function servingPayload(value: {
  name: string;
  artifactId: number;
  runtime: string;
  protocol: string;
  minReplicas: string;
  maxReplicas: string;
  cpu: string;
  memory: string;
  gpu: string;
}) {
  if (!/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(value.name) || value.name.length > 63)
    throw new Error('服务名使用最长 63 字符的小写字母、数字和中划线');
  if (!Number.isSafeInteger(value.artifactId) || value.artifactId < 1)
    throw new Error('请选择有效产物');
  const min = Number(value.minReplicas),
    max = Number(value.maxReplicas),
    gpu = Number(value.gpu);
  if (
    ![value.minReplicas, value.maxReplicas, value.gpu].every((v) => /^\d+$/.test(v)) ||
    min < 1 ||
    max < min ||
    max > 128 ||
    gpu < 0 ||
    gpu > 128
  )
    throw new Error('副本必须在 1–128 且 max≥min；GPU 为 0–128 整数');
  if (!value.cpu.trim() || !value.memory.trim()) throw new Error('请填写 CPU 与内存');
  if (
    value.runtime &&
    (!/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/.test(value.runtime) || value.runtime.length > 253)
  )
    throw new Error('Runtime 名称无效');
  return {
    name: value.name,
    deploymentType: 'MODEL',
    protocol: value.protocol,
    minReplicas: min,
    maxReplicas: max,
    resources: { cpu: value.cpu, memory: value.memory, gpu },
    model: { artifactId: value.artifactId, ...(value.runtime ? { runtime: value.runtime } : {}) },
  };
}
