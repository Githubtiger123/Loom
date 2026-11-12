import type { Operation } from '@/lib/types';
export function resultID(operation: Operation, key: string, nested?: string) {
  const result = operation.result;
  if (result && typeof result === 'object') {
    let record = result as Record<string, unknown>;
    if (nested) {
      const child = record[nested];
      if (child && typeof child === 'object') record = child as Record<string, unknown>;
    }
    const id = record[key];
    if (typeof id === 'number' && Number.isSafeInteger(id) && id > 0) return id;
  }
  return undefined;
}
