import { Link, Navigate } from 'react-router-dom';
import { Card } from '@atomus';
import { trustedHeaderMode } from '@/lib/api';
import { useSession } from '@/lib/use-session';
import { PasswordForm } from './components/password-form';

// Keep the mandatory first-login flow outside the ordinary personal center.
export function PasswordPage() {
  const session = useSession();
  if (trustedHeaderMode || !session.passwordChangeRequired)
    return <Navigate to="/account/password" replace />;
  return (
    <main className="standalone narrow">
      <Link className="wordmark" to="/">
        <span className="logo-mark">L</span>loom
      </Link>
      <Card
        title="首次登录，请设置新密码"
        supportingText="完成改密后才能进入平台。修改后将撤销已有会话，请使用新密码重新登录。"
        padding="lg"
      >
        <PasswordForm />
      </Card>
    </main>
  );
}
