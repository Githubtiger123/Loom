import { Controller, useFormContext } from 'react-hook-form';
import type { UseQueryResult } from '@tanstack/react-query';
import { Input, Select } from '@atomus';
import type { NotebookImage } from '@/lib/types';
import { ErrorState, Loading, NoData } from '@/components/feedback';
import type { NotebookCreateValues } from '../form-schema';
import { NotebookSettingsFields } from './notebook-settings-fields';
import { NotebookWorkspaceFields } from './notebook-workspace-fields';

export function NotebookCreateFields({
  groupId,
  userId,
  allowReuse,
  disabled,
  images,
}: {
  groupId: string;
  userId?: number;
  allowReuse: boolean;
  disabled: boolean;
  images: UseQueryResult<{ items: NotebookImage[] }>;
}) {
  const {
    register,
    control,
    formState: { errors },
  } = useFormContext<NotebookCreateValues>();
  return (
    <fieldset className="form-fields" disabled={disabled}>
      <fieldset className="form-section">
        <legend>环境与镜像</legend>
        <Input
          label="实例名称"
          hint="最长 63 字符，小写字母、数字和连字符；创建后不可修改"
          {...register('name')}
          error={errors.name?.message}
          autoComplete="off"
        />
        {images.isPending ? (
          <Loading label="正在读取受管镜像" />
        ) : images.isError ? (
          <ErrorState
            error={images.error}
            retry={() => {
              void images.refetch();
            }}
          />
        ) : images.data.items.length === 0 ? (
          <NoData title="暂无可用镜像" description="请联系管理员配置镜像目录。" />
        ) : (
          <Controller
            control={control}
            name="imageId"
            render={({ field }) => (
              <Select
                label="运行镜像"
                value={field.value}
                onChange={field.onChange}
                disabled={disabled}
                placeholder="请选择镜像"
                error={errors.imageId?.message}
                options={images.data.items.map((image) => ({
                  value: image.id,
                  label: `${image.name} · ${image.category}`,
                }))}
              />
            )}
          />
        )}
      </fieldset>
      <NotebookSettingsFields disabled={disabled} />
      <NotebookWorkspaceFields
        groupId={groupId}
        userId={userId}
        allowReuse={allowReuse}
        disabled={disabled}
      />
    </fieldset>
  );
}
