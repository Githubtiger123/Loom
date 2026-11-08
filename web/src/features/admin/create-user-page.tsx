import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { Alert, Button, Card, Checkbox, Input, Select } from '@atomus';
import { trustedHeaderMode } from '@/lib/api';
import { useMounted } from '@/lib/use-mounted';
import { useCommandKey } from '@/lib/use-command-key';
import { useGlobalPermissions } from '@/lib/use-global-permissions';
import { ErrorState, PageHeading } from '@/components/feedback';
import { UnsavedChangesGuard } from '@/components/unsaved-changes-guard';
import { adminApi } from './api';
import { refreshAdministration } from './cache';
import { createUserSchema, type CreateUserValues } from './form-schema';
import { GlobalCapability } from './components/global-capability';
export function CreateUserPage() {
  const [params] = useSearchParams();
  const local = !trustedHeaderMode && params.get('mode') !== 'external';
  return (
    <GlobalCapability permission="user.manage">
      {(allowed) => <CreateUserForm key={String(local)} local={local} allowed={allowed} />}
    </GlobalCapability>
  );
}
function CreateUserForm({ local, allowed }: { local: boolean; allowed: boolean }) {
  const me = useGlobalPermissions();
  const navigate = useNavigate();
  const mounted = useMounted();
  const client = useQueryClient();
  const key = useCommandKey();
  const form = useForm<CreateUserValues>({
    resolver: zodResolver(createUserSchema),
    defaultValues: {
      username: '',
      displayName: '',
      email: '',
      issuer: '',
      subject: '',
      externalIdentityConfirmed: false,
    },
  });
  const mutation = useMutation({
    mutationFn: (v: CreateUserValues) => {
      if (!allowed) throw new Error('没有用户管理权限');
      if (!local && !v.issuer.trim()) throw new Error('请填写身份提供方 issuer');
      if (!local && v.externalIdentityConfirmed && !v.subject.trim())
        throw new Error('确认身份开通必须提供稳定 subject');
      const base = {
        username: v.username,
        displayName: v.displayName,
        ...(v.email ? { email: v.email } : {}),
      };
      const body = local
        ? base
        : {
            ...base,
            issuer: v.issuer,
            subject: v.subject || null,
            externalIdentityConfirmed: v.externalIdentityConfirmed,
          };
      return adminApi.create(local, body, key({ local, body }));
    },
    onSuccess: (op) => {
      refreshAdministration(client);
      if (!mounted.current) return;
      const result = op.result;
      const id = result && typeof result === 'object' && 'id' in result ? result.id : undefined;
      const valid = typeof id === 'number' && Number.isSafeInteger(id) && id > 0;
      // user.manage 不隐含 user.read；只有可读用户才能进入详情。
      void navigate(
        valid && me.can('user.read')
          ? `/admin/users/${id}`
          : valid
            ? `/admin?createdUserId=${id}`
            : '/admin',
        { replace: true, state: { operationId: op.operationId } },
      );
    },
  });
  return (
    <>
      <Link className="text-link" to="/admin">
        ← 返回平台管理
      </Link>
      <PageHeading
        eyebrow="REGISTER USER"
        title="登记平台用户"
        description="登记不自动加入空间，也不代建 Dex / LDAP / OIDC 账号。"
      />
      <Select
        label="身份类型"
        value={local ? 'local' : 'external'}
        disabled={mutation.isPending}
        onChange={(mode) => {
          void navigate(`/admin/users/new?mode=${mode}`);
        }}
        options={[
          ...(!trustedHeaderMode ? [{ value: 'local', label: '本地账号' }] : []),
          { value: 'external', label: '外部稳定身份' },
        ]}
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
            watchSearch
          />
          <Alert color="info" title={local ? '创建后还需设置密码' : '身份必须由管理员核验'}>
            {local
              ? '平台生成稳定主体并启用业务账号，但不会生成密码。创建后到用户详情设置初始密码；用户首次登录必须改密。'
              : 'issuer + subject 才是稳定身份，不按邮箱合并；未确认时登记为待开通。若当前平台使用本地认证，外部身份不能直接通过本地密码登录。'}
          </Alert>
          <fieldset className="form-section" disabled={mutation.isPending}>
            <Input
              label="登录名"
              hint="与实际登录身份完全一致，注意大小写；不是展示名称"
              {...form.register('username')}
              error={form.formState.errors.username?.message}
            />
            <Input
              label="展示名称"
              {...form.register('displayName')}
              error={form.formState.errors.displayName?.message}
            />
            <Input
              label="联系邮箱（可选）"
              {...form.register('email')}
              error={form.formState.errors.email?.message}
            />
            {!local && (
              <>
                <Input
                  label="身份提供方 issuer"
                  {...form.register('issuer')}
                  error={form.formState.errors.issuer?.message}
                />
                <Input
                  label="稳定 subject"
                  hint="不是邮箱推算值；未开通时可留空，启用时必须补齐"
                  {...form.register('subject')}
                  error={form.formState.errors.subject?.message}
                />
                <Checkbox
                  label="已核验外部身份，可立即开通"
                  {...form.register('externalIdentityConfirmed')}
                />
              </>
            )}
          </fieldset>
          {mutation.isError && <ErrorState error={mutation.error} />}
          <div className="actions end">
            <Button
              hierarchy="primary"
              type="submit"
              loading={mutation.isPending}
              disabled={!allowed}
            >
              登记用户
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}
