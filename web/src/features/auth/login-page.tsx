import { useEffect } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { useMutation } from '@tanstack/react-query';
import { Navigate, useLocation } from 'react-router-dom';
import { Button, Card, Input } from '@atomus';
import { ensureSession, login, trustedHeaderMode } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { useSession } from '@/lib/use-session';
import { ErrorState, Loading } from '@/components/feedback';
import { loginReturnPath } from './policy';

const schema = z.object({
  username: z.string().trim().min(1, '请输入账号'),
  password: z.string().min(1, '请输入密码'),
});
type Values = z.infer<typeof schema>;
export function LoginPage() {
  const session = useSession();
  const location = useLocation();
  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<Values>({ resolver: zodResolver(schema) });
  const mutation = useMutation({
    mutationFn: ({ username, password }: Values) => login(username, password),
    onSuccess: () => queryClient.clear(),
  });
  useEffect(() => {
    void ensureSession();
  }, []);
  const target = loginReturnPath((location.state as { from?: unknown } | null)?.from);
  if (session.state === 'checking') return <Loading label="正在恢复会话" />;
  if (session.state === 'authenticated')
    return <Navigate to={session.passwordChangeRequired ? '/password' : target} replace />;
  return (
    <main className="login-layout">
      <section className="login-story">
        <span className="wordmark">
          <span className="logo-mark">L</span>loom
          <span className="wordmark-note">机器学习工作平台</span>
        </span>
        <div>
          <p className="eyebrow">FROM EXPERIMENT TO PRODUCTION</p>
          <h1>
            让每一个想法，
            <br />
            走向真实应用。
          </h1>
          <p className="muted">在统一工作空间中开发、编排、管理模型与发布推理服务。</p>
          <div className="story-modules">
            <span>Notebook</span>
            <span>Pipeline</span>
            <span>Model Registry</span>
            <span>KServe</span>
          </div>
        </div>
        <p className="muted">Loom · Your machine learning workspace</p>
      </section>
      <section className="login-form-wrap">
        <Card title="登录 Loom" supportingText="使用你的平台账号进入工作空间" padding="lg">
          {trustedHeaderMode ? (
            <ErrorState error={new Error('请通过组织统一认证入口访问平台')} />
          ) : (
            <form className="stack" onSubmit={handleSubmit((values) => mutation.mutate(values))}>
              <Input
                label="账号"
                autoComplete="username"
                {...register('username')}
                error={errors.username?.message}
              />
              <Input
                label="密码"
                type="password"
                autoComplete="current-password"
                {...register('password')}
                error={errors.password?.message}
              />
              {mutation.isError && <ErrorState error={mutation.error} />}
              {session.state === 'unavailable' && <ErrorState error={new Error(session.error)} />}
              <Button hierarchy="primary" type="submit" loading={mutation.isPending} fullWidth>
                登录
              </Button>
            </form>
          )}
        </Card>
        <p className="muted small">账号由平台管理员分配；首次登录可能需要修改密码。</p>
      </section>
    </main>
  );
}
