import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { Alert, Button, Card, Tabs } from '@atomus';
import { clearSession, trustedHeaderMode } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { useGlobalPermissions } from '@/lib/use-global-permissions';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { formatDate } from '@/lib/format';
import { ErrorState, Loading, NoData, PageHeading, StatusBadge } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { SessionPanel } from '@/features/security/components/session-panel';
import { adminApi, adminKeys } from './api';
import type { ManagedUser } from './types';
import { validId } from './form-schema';
import { GlobalCapability } from './components/global-capability';
import { UserStateDialog } from './components/user-state-dialog';
import { ResetPasswordDialog } from './components/reset-password-dialog';
export function UserDetailPage() {
  const { userId = '' } = useParams();
  if (!validId(userId)) return <NoData title="用户 ID 无效" />;
  return (
    <GlobalCapability permission="user.read">
      {(allowed) => <UserDetail id={userId} allowed={allowed} />}
    </GlobalCapability>
  );
}
function UserDetail({ id, allowed }: { id: string; allowed: boolean }) {
  const query = useQuery({
    queryKey: adminKeys.user(id),
    queryFn: ({ signal }) => adminApi.user(id, signal),
    enabled: allowed,
    refetchInterval: (q) => (q.state.error ? false : 15_000),
    gcTime: 0,
  });
  if (!allowed) return <p className="muted">权限尚未确认，暂不展示用户详情。</p>;
  if (query.isPending) return <Loading />;
  if (query.isError)
    return (
      <ErrorState
        error={query.error}
        retry={() => {
          void query.refetch();
        }}
      />
    );
  return <UserContent key={id} user={query.data} />;
}
function UserContent({ user }: { user: ManagedUser }) {
  const me = useGlobalPermissions();
  const [intent, setIntent] = useState<{
    kind: 'enable' | 'disable' | 'password';
    user: ManagedUser;
  }>();
  const [passwordSet, setPasswordSet] = useState(false);
  const [params, setParams] = useSearchParams();
  const { operation, setOperation } = useOperationNavigation();
  const canSessions = !trustedHeaderMode && user.localAccount && me.can('user.manage');
  const tab = params.get('tab') === 'sessions' && canSessions ? 'sessions' : 'overview';
  return (
    <>
      <Link className="text-link" to="/admin/users">
        ← 返回用户列表
      </Link>
      <PageHeading
        eyebrow="USER IDENTITY"
        title={user.displayName || user.username}
        description={`${user.username} · ID ${user.id}`}
        actions={
          me.can('role.manage') ? (
            <Link className="action-link" to={`/admin/users/${user.id}/roles`}>
              编辑全局角色
            </Link>
          ) : undefined
        }
      />
      {user.localAccount && (
        <Alert color="info" title="本地账号的业务状态与密码分开管理">
          ACTIVE
          只表示业务账号启用；新登记账号还需设置初始密码才能登录。平台不会在这里返回现有密码。
        </Alert>
      )}
      <div className="detail-action-bar">
        <StatusBadge status={user.status} />
        <div className="actions">
          {me.can('user.manage') && (
            <>
              {['ACTIVE', 'DISABLED', 'PENDING'].includes(user.status) && (
                <Button
                  hierarchy="outline"
                  onClick={() =>
                    setIntent({ kind: user.status === 'ACTIVE' ? 'disable' : 'enable', user })
                  }
                >
                  {user.status === 'ACTIVE' ? '禁用用户' : '启用用户'}
                </Button>
              )}
              {!trustedHeaderMode && user.localAccount && user.status === 'ACTIVE' && (
                <Button hierarchy="outline" onClick={() => setIntent({ kind: 'password', user })}>
                  设置 / 重置密码
                </Button>
              )}
            </>
          )}
        </div>
      </div>
      {passwordSet && (
        <Alert color="success" title="密码已设置">
          目标用户旧会话已失效，下次登录必须修改密码。请通过安全渠道交付临时密码。
        </Alert>
      )}
      <Tabs
        aria-label="用户信息"
        value={tab}
        onChange={(v) => setParams(v === 'sessions' ? { tab: v } : {})}
        items={[
          { value: 'overview', label: '身份与状态' },
          ...(canSessions ? [{ value: 'sessions', label: '有效会话' }] : []),
        ]}
      />
      {tab === 'sessions' ? (
        <SessionPanel target={String(user.id)} allowed={canSessions} />
      ) : (
        <Card title="稳定身份">
          <dl className="details-grid">
            <dt>用户 ID</dt>
            <dd>{user.id}</dd>
            <dt>登录名</dt>
            <dd>{user.username}</dd>
            <dt>身份类型</dt>
            <dd>{user.localAccount ? '当前认证域本地账号' : '外部身份'}</dd>
            <dt>issuer</dt>
            <dd className="mono break-word">{user.issuer}</dd>
            <dt>subject</dt>
            <dd className="mono break-word">{user.subject || '未绑定，启用前需核验'}</dd>
            <dt>联系邮箱</dt>
            <dd>{user.email || '—'}</dd>
            <dt>授权版本</dt>
            <dd>{user.authorizationVersion}</dd>
            <dt>登记时间</dt>
            <dd>{formatDate(user.createdAt)}</dd>
            <dt>最近账号活动</dt>
            <dd>{formatDate(user.lastLoginAt)}</dd>
          </dl>
        </Card>
      )}
      {intent && intent.kind !== 'password' && (
        <UserStateDialog
          user={intent.user}
          action={intent.kind}
          allowed={me.can('user.manage')}
          onClose={() => setIntent(undefined)}
          onAccepted={(id) => {
            const selfDisabled = intent.kind === 'disable' && intent.user.id === me.data?.user.id;
            setIntent(undefined);
            if (selfDisabled) {
              clearSession();
              queryClient.clear();
            } else setOperation(id);
          }}
        />
      )}
      {intent?.kind === 'password' && (
        <ResetPasswordDialog
          user={intent.user}
          allowed={me.can('user.manage')}
          onClose={() => setIntent(undefined)}
          onReset={() => {
            setIntent(undefined);
            setPasswordSet(true);
          }}
        />
      )}
      {operation && (
        <OperationDialog id={operation} groupId="" onClose={() => setOperation(undefined)} />
      )}
    </>
  );
}
