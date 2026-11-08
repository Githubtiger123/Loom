import { Input } from '@atomus';
import { useFormContext } from 'react-hook-form';
import type { QuotaValues } from '../form-schema';

export function QuotaFields() {
  const {
    register,
    formState: { errors },
  } = useFormContext<QuotaValues>();
  return (
    <div className="form-grid">
      <Input
        label="CPU 额度"
        hint="例如 8 或 500m；Kubernetes 数量格式"
        {...register('cpu')}
        error={errors.cpu?.message}
      />
      <Input
        label="内存额度"
        hint="例如 16Gi；不是磁盘容量"
        {...register('memory')}
        error={errors.memory?.message}
      />
      <Input
        label="GPU 数量"
        type="text"
        inputMode="numeric"
        {...register('gpu')}
        error={errors.gpu?.message}
      />
    </div>
  );
}
