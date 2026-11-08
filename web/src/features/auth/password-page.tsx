import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { useMutation } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { Button, Card, Input } from '@atomus';
import { api, clearSession } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { ErrorState } from '@/components/feedback';

const schema = z
  .object({
    currentPassword: z.string().min(1, '请输入当前密码'),
    newPassword: z
      .string()
      .refine(
        (value) =>
          new TextEncoder().encode(value).length >= 12 &&
          new TextEncoder().encode(value).length <= 1024,
        '新密码须为 12–1024 字节',
      ),
    confirm: z.string(),
  })
  .refine((value) => value.newPassword === value.confirm, {
    path: ['confirm'],
    message: '两次输入的密码不一致',
  });
type Values = z.infer<typeof schema>;
export function PasswordPage() {
  const navigate = useNavigate();
  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<Values>({ resolver: zodResolver(schema) });
  const mutation = useMutation({
    mutationFn: (values: Values) =>
      api('/auth/password', {
        method: 'PUT',
        body: { currentPassword: values.currentPassword, newPassword: values.newPassword },
      }),
    onSuccess: () => {
      clearSession();
      queryClient.clear();
      void navigate('/login', { replace: true });
    },
  });
  return (
    <main className="standalone narrow">
      <Card
        title="设置新密码"
        supportingText="修改后会撤销已有会话，请使用新密码重新登录。"
        padding="lg"
      >
        <form className="stack" onSubmit={handleSubmit((values) => mutation.mutate(values))}>
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
          {mutation.isError && <ErrorState error={mutation.error} />}
          <Button hierarchy="primary" type="submit" loading={mutation.isPending}>
            保存并重新登录
          </Button>
        </form>
      </Card>
    </main>
  );
}
