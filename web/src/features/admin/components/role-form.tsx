import { useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { Alert, Button, Card, Checkbox, Input, Select } from '@atomus';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { ErrorState, PageHeading } from '@/components/feedback';
import { FormTextarea } from '@/components/form-textarea';
import { UnsavedChangesGuard } from '@/components/unsaved-changes-guard';
import { adminApi } from '../api';
import { refreshAdministration } from '../cache';
import { roleSchema, type RoleValues } from '../form-schema';
import type { Permission, Role } from '../types';
import { PermissionPicker } from './permission-picker';
export function RoleForm({
  role,
  currentVersion,
  dictionary,
  allowed,
}: {
  role?: Role;
  currentVersion?: number;
  dictionary: Permission[];
  allowed: boolean;
}) {
  const [baseline] = useState(role);
  const form = useForm<RoleValues>({
    resolver: zodResolver(roleSchema),
    defaultValues: baseline
      ? {
          code: baseline.code,
          name: baseline.name,
          scope: baseline.scope,
          accessLevel: baseline.accessLevel,
          enabled: baseline.enabled,
          description: baseline.description,
          permissions: baseline.permissions,
        }
      : {
          code: '',
          name: '',
          scope: 'group',
          accessLevel: 'view',
          enabled: true,
          description: '',
          permissions: [],
        },
  });
  const scope = form.watch('scope');
  const selected = form.watch('permissions');
  const client = useQueryClient();
  const navigate = useNavigate();
  const key = useCommandKey();
  const mounted = useMounted();
  const mutation = useMutation({
    mutationFn: (v: RoleValues) => {
      if (!allowed) throw new Error('没有角色管理权限');
      const codes = [...v.permissions].sort();
      if (codes.some((c) => !dictionary.some((p) => p.code === c && p.scope === v.scope)))
        throw new Error('权限目录已变化或选择范围不匹配');
      for (const code of codes.filter((c) =>
        /^(notebook|workspace|pipeline|trainer|kserve)\./.test(c),
      )) {
        const read = /\.read(?:\.any)?$/.test(code);
        if ((!read && v.accessLevel !== 'edit') || (read && v.accessLevel === 'none'))
          throw new Error(
            '所选工作负载权限与原生访问级别不匹配：读取至少需要 view，其他操作需要 edit',
          );
      }
      const body = {
        ...v,
        permissions: codes,
        ...(baseline ? { expectedVersion: baseline.version } : {}),
      };
      return adminApi.saveRole(baseline ? String(baseline.id) : undefined, body, key(body));
    },
    onSuccess: (op) => {
      refreshAdministration(client);
      if (mounted.current)
        void navigate('/admin/roles', { replace: true, state: { operationId: op.operationId } });
    },
  });
  return (
    <>
      <Link className="text-link" to="/admin/roles">
        ← 返回角色与权限
      </Link>
      <PageHeading
        eyebrow="ROLE DEFINITION"
        title={baseline ? `编辑 ${baseline.name}` : '创建自定义角色'}
        description="保存完整角色定义；修改会影响所有绑定该角色的用户，集群访问另行异步落实。"
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
          <Alert color="warning" title="授权影响范围">
            停用或改变权限会影响已有绑定，包括你自己的管理权限。全局角色不能授予原生空间访问；空间角色只有绑定到具体空间后生效。
          </Alert>
          {baseline && currentVersion !== baseline.version && (
            <Alert color="warning" title="角色版本已变化">
              当前草稿仍保留，保存时会核对 v{baseline.version}，不会强制覆盖新版本。
            </Alert>
          )}
          <fieldset className="form-section" disabled={mutation.isPending}>
            <Input
              label="角色编码"
              readOnly={!!baseline}
              {...form.register('code')}
              error={form.formState.errors.code?.message}
            />
            <Input
              label="角色名称"
              {...form.register('name')}
              error={form.formState.errors.name?.message}
            />
            <Controller
              control={form.control}
              name="scope"
              render={({ field }) => (
                <Select
                  label="作用范围（创建后固定）"
                  value={field.value}
                  disabled={!!baseline}
                  onChange={(v) => {
                    field.onChange(v);
                    form.setValue('permissions', [], { shouldDirty: true });
                    form.setValue('accessLevel', v === 'global' ? 'none' : 'view', {
                      shouldDirty: true,
                    });
                  }}
                  options={[
                    { value: 'group', label: '空间角色' },
                    { value: 'global', label: '平台全局角色' },
                  ]}
                />
              )}
            />
            <Controller
              control={form.control}
              name="accessLevel"
              render={({ field }) => (
                <Select
                  label="原生访问级别"
                  value={field.value}
                  onChange={field.onChange}
                  disabled={scope === 'global'}
                  options={[
                    { value: 'none', label: 'none · 无原生访问' },
                    { value: 'view', label: 'view · 只读' },
                    { value: 'edit', label: 'edit · 读写' },
                  ]}
                />
              )}
            />
            <Checkbox
              label="启用该角色"
              description="此项随表单保存，不会立即生效"
              {...form.register('enabled')}
            />
            <FormTextarea
              label="描述"
              {...form.register('description')}
              error={form.formState.errors.description?.message}
            />
            <Controller
              control={form.control}
              name="permissions"
              render={({ field }) => (
                <PermissionPicker
                  dictionary={dictionary.filter((p) => p.scope === scope)}
                  value={field.value}
                  onChange={field.onChange}
                />
              )}
            />
          </fieldset>
          <p className="small muted">
            已选择 {selected.length} 个权限点{baseline ? ` · 编辑基线 v${baseline.version}` : ''}。
          </p>
          {mutation.isError && <ErrorState error={mutation.error} />}
          <div className="actions end">
            <Button
              hierarchy="primary"
              type="submit"
              loading={mutation.isPending}
              disabled={!allowed || (!!baseline && !form.formState.isDirty)}
            >
              保存角色定义
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}
