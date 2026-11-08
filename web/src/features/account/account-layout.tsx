import { Link, Outlet, useLocation } from 'react-router-dom';
import { KeyRound, Monitor, UserRound } from 'lucide-react';
import { AppFrame } from '@/app/app-frame';
import { trustedHeaderMode } from '@/lib/api';

export function AccountLayout() {
  const { pathname } = useLocation();
  const items = [
    { to: '/account', label: '个人资料', icon: UserRound, end: true },
    ...(!trustedHeaderMode ? [{ to: '/account/password', label: '修改密码', icon: KeyRound }] : []),
    { to: '/account/sessions', label: '设备会话', icon: Monitor },
  ];
  const section = items.find((item) => item.to === pathname.replace(/\/+$/, ''));
  return (
    <AppFrame
      context={{
        label: '我的账号',
        title: '个人中心',
        icon: <UserRound size={18} />,
        backLabel: '返回工作空间',
      }}
      caption="MY ACCOUNT"
      items={items}
      breadcrumb={
        <>
          <Link to="/account">个人中心</Link>
          <span>/</span>
          <strong>{section?.label ?? '账号安全'}</strong>
        </>
      }
    >
      <Outlet />
    </AppFrame>
  );
}
