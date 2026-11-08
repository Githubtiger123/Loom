import { useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { Alert, Button, Card, Input } from '@atomus';
import { ApiError } from '@/lib/api';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { ErrorState } from '@/components/feedback';
import { UnsavedChangesGuard } from '@/components/unsaved-changes-guard';
import { groupApi } from '../api';
import { refreshGroup } from '../cache';
import { memberSchema, type MemberValues } from '../form-schema';
import type { Member, Role } from '../types';
import { RoleFields } from './role-fields';

const localDate = (iso: string | null) => {
  if (!iso) return '';
  const d = new Date(iso);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60_000).toISOString().slice(0, 23);
};
const bindingSnapshot = (m?: Member) =>
  m?.roles.map((b) => ({ id: b.id, version: b.version })).sort((a, b) => a.id - b.id) ?? [];
export function MemberForm({
  groupId,
  member,
  members,
  roles,
  allowed,
}: {
  groupId: string;
  member?: Member;
  members: Member[];
  roles: Role[];
  allowed: boolean;
}) {
  const [baseline] = useState(member);
  const expected = bindingSnapshot(baseline);
  const form = useForm<MemberValues>({
    resolver: zodResolver(memberSchema),
    defaultValues: {
      userId: baseline ? String(baseline.userId) : '',
      roles:
        baseline?.roles.map((b) => ({ roleId: b.roleId, expiresAt: localDate(b.expiresAt) })) ?? [],
    },
  });
  const permissions = useGroupPermissions(groupId);
  const navigate = useNavigate();
  const mounted = useMounted();
  const client = useQueryClient();
  const key = useCommandKey();
  const userId = form.watch('userId');
  const current = members.find((m) => m.userId === Number(userId));
  const existing = !baseline && !!current?.roles.length;
  const stale = !!baseline && JSON.stringify(bindingSnapshot(current)) !== JSON.stringify(expected);
  const mutation = useMutation({
    mutationFn: (v: MemberValues) => {
      if (!allowed) throw new Error('当前空间状态或权限不允许修改成员');
      const grants = v.roles
        .map((grant) => {
          const role = roles.find((r) => r.id === grant.roleId);
          if (!role) throw new Error('角色目录已变化，请重新确认');
          if (role.code === 'group_owner' && grant.expiresAt)
            throw new Error('负责人角色不允许设置有效期');
          return {
            roleId: grant.roleId,
            ...(grant.expiresAt ? { expiresAt: new Date(grant.expiresAt).toISOString() } : {}),
          };
        })
        .sort((a, b) => a.roleId - b.roleId);
      const body = { userId: Number(v.userId), roles: grants, expectedBindings: expected };
      return groupApi.replaceRoles(groupId, body.userId, grants, expected, key(body));
    },
    onSuccess: (op, v) => {
      refreshGroup(client, groupId);
      if (!mounted.current) return;
      void navigate(Number(v.userId) === permissions.data?.userId ? '/' : `/g/${groupId}/members`, {
        replace: true,
        state: { operationId: op.operationId },
      });
    },
  });
  const conflict = mutation.error instanceof ApiError && [403, 409].includes(mutation.error.status);
  return (
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
        <Alert color="info" title="只分配本空间的角色">
          不修改平台账号或全局角色。到期角色自动失效；长期授权可留空到期时间。自己降权可能立即失去当前页面权限。
        </Alert>
        {stale && (
          <Alert color="warning" title="成员授权已变化">
            草稿仍保留。保存时会比较旧绑定集合，拒绝覆盖其他人的修改；请记录草稿并返回列表重新编辑。
          </Alert>
        )}
        <fieldset className="form-section" disabled={mutation.isPending}>
          <Input
            label="用户 ID"
            readOnly={!!baseline}
            hint={
              baseline
                ? `${baseline.displayName || baseline.username} · ${baseline.username}`
                : '填写已存在且启用的用户 ID；此入口不创建账号，也不发送邮件邀请'
            }
            inputMode="numeric"
            {...form.register('userId')}
            error={form.formState.errors.userId?.message}
          />
          {existing && (
            <p>
              该用户已有空间角色，
              <Link className="text-link" to={`/g/${groupId}/members/${userId}/edit`}>
                请在编辑页修改
              </Link>
              。
            </p>
          )}
          <Controller
            control={form.control}
            name="roles"
            render={({ field }) => (
              <RoleFields
                roles={roles}
                value={field.value}
                onChange={field.onChange}
                errors={form.formState.errors.roles}
              />
            )}
          />
          {form.formState.errors.roles && (
            <p className="field-error" role="alert">
              {form.formState.errors.roles.message || '请检查角色的有效期，必须晚于当前时间。'}
            </p>
          )}
        </fieldset>
        {mutation.isError && <ErrorState error={mutation.error} />}
        <div className="actions end">
          <Button
            type="submit"
            hierarchy="primary"
            loading={mutation.isPending}
            disabled={
              !allowed || conflict || (existing && !mutation.isError) || !form.formState.isDirty
            }
          >
            保存角色
          </Button>
        </div>
      </form>
    </Card>
  );
}
