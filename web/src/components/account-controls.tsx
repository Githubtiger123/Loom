import { useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Avatar, Button, DropdownMenu, type DropdownItem } from '@atomus';
import { ChevronDown, LogOut, Moon, ShieldCheck, Sun, UserRound } from 'lucide-react';
import { api, clearSession, errorMessage, trustedHeaderMode } from '@/lib/api';
import { useGlobalPermissions } from '@/lib/use-global-permissions';
import { adminSections } from '@/features/admin/policy';

// One account entry across the home page, workspaces, administration and account center.
export function AccountControls() {
  const me = useGlobalPermissions();
  const client = useQueryClient();
  const navigate = useNavigate();
  const accountTrigger = useRef<HTMLButtonElement>(null);
  const [dark, setDark] = useState(() => document.documentElement.dataset.theme === 'dark');
  const logout = useMutation({
    mutationFn: () => api('/auth/logout', { method: 'POST' }),
    onSuccess: () => {
      clearSession();
      client.clear();
    },
  });
  const name = me.isSuccess ? me.data.user.displayName || me.data.user.username : undefined;
  const admin = adminSections.some((section) => me.can(section.permission));
  const items: DropdownItem[] = [
    {
      label: '个人中心',
      icon: <UserRound size={16} aria-hidden="true" />,
      onSelect: () => navigate('/account'),
    },
    ...(admin
      ? [
          {
            label: '平台管理',
            icon: <ShieldCheck size={16} aria-hidden="true" />,
            onSelect: () => navigate('/admin'),
          },
        ]
      : []),
    { type: 'separator' },
    {
      label: (
        <span title={trustedHeaderMode ? '外部统一认证请到组织登录入口退出' : undefined}>
          退出登录
        </span>
      ),
      icon: <LogOut size={16} aria-hidden="true" />,
      destructive: true,
      disabled: logout.isPending || trustedHeaderMode,
      onSelect: () => {
        if (!logout.isPending && !trustedHeaderMode) logout.mutate();
      },
    },
  ];
  return (
    <div className="account-controls">
      <nav className="account-actions" aria-label="个人账号与平台入口">
        <Button
          hierarchy="tertiary"
          iconOnly
          iconLeading={dark ? <Sun size={18} /> : <Moon size={18} />}
          aria-label={dark ? '切换浅色主题' : '切换深色主题'}
          onClick={() => {
            const next = !dark;
            document.documentElement.dataset.theme = next ? 'dark' : 'light';
            localStorage.setItem('loom.theme', next ? 'dark' : 'light');
            setDark(next);
          }}
        />
        <div
          className="account-menu"
          onKeyDown={(event) => {
            if (event.key === 'Escape') accountTrigger.current?.focus();
          }}
        >
          <DropdownMenu
            key={`${me.data?.user.id ?? 'unknown'}:${admin}`}
            align="end"
            size="md"
            label="账号菜单"
            items={items}
            trigger={
              <Button
                ref={accountTrigger}
                hierarchy="tertiary"
                className="account-trigger"
                iconLeading={<Avatar name={name} size="sm" aria-hidden="true" />}
                iconTrailing={<ChevronDown size={16} aria-hidden="true" />}
                loading={logout.isPending}
                aria-label={
                  logout.isPending ? '正在退出登录' : `${name || '我的账号'}，打开账号菜单`
                }
                title={name}
              >
                <span className="account-name">
                  {logout.isPending ? '正在退出…' : name || '我的账号'}
                </span>
              </Button>
            }
          />
        </div>
        {me.isError && (
          <Button
            hierarchy="link"
            size="sm"
            onClick={() => {
              void me.refetch();
            }}
          >
            账号读取失败，重试
          </Button>
        )}
      </nav>
      {logout.isError && (
        <p className="small field-error" role="alert">
          退出未确认：{errorMessage(logout.error)}，请重试。
        </p>
      )}
    </div>
  );
}
