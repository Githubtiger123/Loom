import { Link } from 'react-router-dom';
import { Card } from '@atomus';
import { KeyRound, Monitor, ShieldCheck } from 'lucide-react';
import { trustedHeaderMode } from '@/lib/api';
import { useGlobalPermissions } from '@/lib/use-global-permissions';
import { ErrorState, Loading, PageHeading } from '@/components/feedback';
import { ProfileOverview } from './components/profile-overview';

export function ProfilePage() {
  const me = useGlobalPermissions();
  return (
    <div className="stack spacious">
      <PageHeading
        eyebrow="MY PROFILE"
        title="个人资料"
        description="查看你的平台身份，在个人中心统一管理密码和设备会话。"
      />
      {me.isPending ? (
        <Loading label="正在读取个人资料" />
      ) : me.isError ? (
        <ErrorState
          error={me.error}
          retry={() => {
            void me.refetch();
          }}
        />
      ) : (
        <ProfileOverview user={me.data.user} />
      )}
      <Card
        title={
          <>
            <ShieldCheck size={18} />
            账号安全
          </>
        }
        padding="lg"
      >
        <div className="stack">
          <p className="muted">
            {trustedHeaderMode
              ? '当前使用组织统一认证，密码与统一登录退出由身份提供方管理。'
              : '修改密码会撤销已有本地会话；设备会话支持单独撤销或退出全部设备。'}
          </p>
          <div className="actions">
            {!trustedHeaderMode && (
              <Link className="text-link" to="/account/password">
                <KeyRound size={16} />
                修改密码
              </Link>
            )}
            <Link className="text-link" to="/account/sessions">
              <Monitor size={16} />
              管理设备会话
            </Link>
          </div>
        </div>
      </Card>
    </div>
  );
}
