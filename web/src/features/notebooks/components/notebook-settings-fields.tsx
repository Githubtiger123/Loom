import { Controller, useFormContext } from 'react-hook-form';
import { Input, Select } from '@atomus';
import { FormTextarea } from '@/components/form-textarea';
import type { NotebookSettingsValues } from '../form-schema';

export function NotebookSettingsFields({ runtimeLocked = false }: { runtimeLocked?: boolean }) {
  const {
    register,
    control,
    formState: { errors },
  } = useFormContext<NotebookSettingsValues>();
  return (
    <>
      <fieldset className="form-section">
        <legend>基本信息</legend>
        <Input label="展示名称" {...register('displayName')} error={errors.displayName?.message} />
        <FormTextarea
          label="描述"
          rows={3}
          {...register('description')}
          error={errors.description?.message}
          hint="最多 1000 字"
        />
      </fieldset>
      <fieldset className="form-section">
        <legend>计算与回收策略</legend>
        {runtimeLocked && (
          <p className="muted small">
            资源配置仅在停止已生效且修订一致后可编辑，基本信息与空闲策略仍可修改。
          </p>
        )}
        <div className="form-grid">
          <Input
            label="CPU"
            hint="例如 2 或 500m"
            readOnly={runtimeLocked}
            {...register('cpu')}
            error={errors.cpu?.message}
          />
          <Input
            label="内存"
            hint="例如 4Gi 或 4096Mi"
            readOnly={runtimeLocked}
            {...register('memory')}
            error={errors.memory?.message}
          />
          <Controller
            control={control}
            name="gpu"
            render={({ field }) => (
              <Select
                label="GPU 卡数"
                value={field.value}
                onChange={field.onChange}
                disabled={runtimeLocked}
                error={errors.gpu?.message}
                options={['0', '1', '2', '4'].map((value) => ({ value, label: `${value} 张` }))}
              />
            )}
          />
          <Controller
            control={control}
            name="idleStopMinutes"
            render={({ field }) => (
              <Select
                label="空闲自动停止"
                value={field.value}
                onChange={field.onChange}
                error={errors.idleStopMinutes?.message}
                options={[
                  { value: '0', label: '不自动停止' },
                  { value: '60', label: '空闲 1 小时' },
                  { value: '240', label: '空闲 4 小时' },
                  { value: '480', label: '空闲 8 小时' },
                ]}
              />
            )}
          />
        </div>
        <p className="small muted">
          实际可用资源由空间配额和集群调度决定；空闲停止依赖可信活动信号。
        </p>
      </fieldset>
    </>
  );
}
