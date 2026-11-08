import { useEffect, useRef } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Button, Input } from '@atomus';
import { clearSession } from '@/lib/api';
import { ErrorState } from '@/components/feedback';
import { authApi } from '../api';
import { passwordSchema, type PasswordValues } from '../form-schema';

export function PasswordForm() {
  const client = useQueryClient();
  const pending = useRef<{ currentPassword: string; newPassword: string } | undefined>(undefined);
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<PasswordValues>({
    resolver: zodResolver(passwordSchema),
    defaultValues: { currentPassword: '', newPassword: '', confirm: '' },
  });
  useEffect(
    () => () => {
      pending.current = undefined;
    },
    [],
  );
  const mutation = useMutation({
    mutationFn: () => {
      if (!pending.current) throw new Error('请重新填写密码后提交');
      return authApi.changePassword(pending.current);
    },
    onSuccess: () => {
      reset();
      client.clear();
      // SessionGuard is the sole owner of the post-revocation redirect.
      clearSession();
    },
    onSettled: () => {
      pending.current = undefined;
    },
  });
  return (
    <form
      className="stack"
      onSubmit={handleSubmit((values) => {
        if (pending.current || mutation.isPending || mutation.isSuccess) return;
        pending.current = {
          currentPassword: values.currentPassword,
          newPassword: values.newPassword,
        };
        mutation.mutate();
      })}
    >
      <fieldset className="form-fields" disabled={mutation.isPending || mutation.isSuccess}>
        <Input
          label="当前密码"
          type="password"
          autoComplete="current-password"
          {...register('currentPassword')}
          error={errors.currentPassword?.message}
        />
        <Input
          label="新密码"
          type="password"
          autoComplete="new-password"
          hint="12–1024 字节"
          {...register('newPassword')}
          error={errors.newPassword?.message}
        />
        <Input
          label="确认新密码"
          type="password"
          autoComplete="new-password"
          {...register('confirm')}
          error={errors.confirm?.message}
        />
      </fieldset>
      {mutation.isError && <ErrorState error={mutation.error} />}
      <p className="small muted">
        若提交时网络中断，请先尝试使用新密码重新登录，不会自动重试改密请求。
      </p>
      <Button
        hierarchy="primary"
        type="submit"
        loading={mutation.isPending}
        disabled={mutation.isSuccess}
      >
        保存并重新登录
      </Button>
    </form>
  );
}
