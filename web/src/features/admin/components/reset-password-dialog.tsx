import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Input, Modal } from '@atomus';
import { clearSession } from '@/lib/api';
import { useGlobalPermissions } from '@/lib/use-global-permissions';
import { useMounted } from '@/lib/use-mounted';
import { ErrorState } from '@/components/feedback';
import { adminApi } from '../api';
import { refreshAdministration } from '../cache';
import { resetPasswordSchema, type ResetPasswordValues } from '../form-schema';
import type { ManagedUser } from '../types';
export function ResetPasswordDialog({
  user,
  allowed,
  onClose,
  onReset,
}: {
  user: ManagedUser;
  allowed: boolean;
  onClose: () => void;
  onReset: () => void;
}) {
  const client = useQueryClient();
  const me = useGlobalPermissions();
  const mounted = useMounted();
  const form = useForm<ResetPasswordValues>({
    resolver: zodResolver(resetPasswordSchema),
    defaultValues: { password: '', confirm: '' },
  });
  // 不将密码作为 mutation variables 或幂等指纹保留，只在请求时读取表单。
  const mutation = useMutation({
    mutationFn: () => {
      if (!allowed) throw new Error('没有重置密码权限');
      return adminApi.resetPassword(user.id, form.getValues('password'));
    },
    onSuccess: () => {
      form.reset();
      if (user.id === me.data?.user.id) {
        clearSession();
        client.clear();
        return;
      }
      refreshAdministration(client);
      if (mounted.current) onReset();
    },
  });
  return (
    <Modal
      open
      title={`设置 / 重置 ${user.username} 的本地密码`}
      description="提交后旧会话全部失效，用户下次登录强制改密。不会更改外部 SSO 密码。"
      onClose={() => {
        if (!mutation.isPending) {
          form.reset();
          onClose();
        }
      }}
      actions={
        <>
          <Button
            hierarchy="outline"
            disabled={mutation.isPending}
            onClick={() => {
              form.reset();
              onClose();
            }}
          >
            取消
          </Button>
          <Button
            hierarchy="primary"
            type="submit"
            form="reset-local-password"
            disabled={!allowed}
            loading={mutation.isPending}
          >
            确认设置密码
          </Button>
        </>
      }
    >
      <form
        id="reset-local-password"
        className="stack"
        onSubmit={form.handleSubmit(() => {
          if (!mutation.isPending) mutation.mutate();
        })}
      >
        <Alert color="warning" title="敏感操作">
          请先核实目标用户，通过安全渠道交付临时密码；网络失败可能意味着结果未确认，不会自动重试。
        </Alert>
        <fieldset className="form-section" disabled={mutation.isPending}>
          <Input
            label="新临时密码"
            type="password"
            autoComplete="new-password"
            {...form.register('password')}
            error={form.formState.errors.password?.message}
          />
          <Input
            label="确认临时密码"
            type="password"
            autoComplete="new-password"
            {...form.register('confirm')}
            error={form.formState.errors.confirm?.message}
          />
        </fieldset>
        {mutation.isError && <ErrorState error={mutation.error} />}
      </form>
    </Modal>
  );
}
