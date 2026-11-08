import { useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { Alert, Button, Card, Input } from '@atomus';
import { useGlobalPermissions } from '@/lib/use-global-permissions';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { ErrorState, PageHeading } from '@/components/feedback';
import { UnsavedChangesGuard } from '@/components/unsaved-changes-guard';
import { adminApi } from '../api';
import { refreshAdministration } from '../cache';
import { globalGrantsSchema, type GlobalGrantsValues } from '../form-schema';
import type { Binding, Role } from '../types';
import { GlobalGrantFields } from './global-grant-fields';
const snapshot = (rows: Binding[]) =>
  rows.map((b) => ({ id: b.id, version: b.version })).sort((a, b) => a.id - b.id);
const localDate = (value: string | null) => {
  if (!value) return '';
  const date = new Date(value);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 23);
};
export function GlobalRolesForm({
  id,
  bindings,
  roles,
  allowed,
}: {
  id: string;
  bindings: Binding[];
  roles: Role[];
  allowed: boolean;
}) {
  const [baseline] = useState(bindings);
  const form = useForm<GlobalGrantsValues>({
    resolver: zodResolver(globalGrantsSchema),
    defaultValues: {
      roles: baseline.map((b) => ({ roleId: b.roleId, expiresAt: localDate(b.expiresAt) })),
      confirmId: '',
    },
  });
  const me = useGlobalPermissions();
  const client = useQueryClient();
  const navigate = useNavigate();
  const key = useCommandKey();
  const mounted = useMounted();
  const mutation = useMutation({
    mutationFn: (v: GlobalGrantsValues) => {
      if (!allowed) throw new Error('当前没有全局角色管理权限');
      if (v.confirmId !== id) throw new Error('确认用户 ID 不匹配');
      const grants = v.roles
        .map((g) => {
          const role = roles.find((r) => r.id === g.roleId);
          if (!role) throw new Error('角色已停用或不在当前全局目录，请重新确认');
          if (role.code === 'platform_admin' && g.expiresAt)
            throw new Error('平台管理员不允许设置到期时间');
          return {
            roleId: g.roleId,
            ...(g.expiresAt ? { expiresAt: new Date(g.expiresAt).toISOString() } : {}),
          };
        })
        .sort((a, b) => a.roleId - b.roleId);
      const body = { roles: grants, expectedBindings: snapshot(baseline) };
      return adminApi.replaceBindings(id, body, key(body));
    },
    onSuccess: (op) => {
      refreshAdministration(client);
      if (mounted.current)
        void navigate(Number(id) === me.data?.user.id ? '/' : '/admin', {
          replace: true,
          state: { operationId: op.operationId },
        });
    },
  });
  const stale = JSON.stringify(snapshot(bindings)) !== JSON.stringify(snapshot(baseline));
  return (
    <>
      <Link className="text-link" to="/admin">
        ← 返回平台管理
      </Link>
      <PageHeading
        eyebrow="GLOBAL ROLE BINDINGS"
        title={`用户 ${id} 的全局角色`}
        description="完整替换平台级授权，不修改任何组内角色。当前仅展示有效绑定。"
      />
      <Card>
        <form
          className="stack spacious"
          onSubmit={form.handleSubmit((v) => {
            if (!mutation.isPending && !mutation.isSuccess) mutation.mutate(v);
          })}
        >
          <UnsavedChangesGuard
            when={form.formState.isDirty && !mutation.isPending && !mutation.isSuccess}
          />
          <Alert color="warning" title="保存会完整替换全局角色">
            不选择任何角色表示撤销所有全局角色。后端保护最后一个有效平台管理员；修改自己可能立即失去管理权限。管理员角色不允许设置有效期。
          </Alert>
          {stale && (
            <Alert color="warning" title="授权已发生变化">
              保留当前草稿；旧绑定 ID + version 会由后端锁内校验，不自动覆盖新授权。
            </Alert>
          )}
          <fieldset className="form-section" disabled={mutation.isPending}>
            <Controller
              control={form.control}
              name="roles"
              render={({ field }) => (
                <GlobalGrantFields
                  roles={roles}
                  value={field.value}
                  onChange={field.onChange}
                  errors={form.formState.errors.roles}
                />
              )}
            />
            <Input
              label={`输入用户 ID ${id} 确认`}
              {...form.register('confirmId')}
              error={form.formState.errors.confirmId?.message}
            />
          </fieldset>
          {form.formState.errors.roles && (
            <p className="field-error" role="alert">
              请检查角色数量与有效期。
            </p>
          )}
          {mutation.isError && <ErrorState error={mutation.error} />}
          <div className="actions end">
            <Button
              hierarchy="primary"
              type="submit"
              loading={mutation.isPending}
              disabled={!allowed || form.watch('confirmId') !== id}
            >
              保存全局角色
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}
