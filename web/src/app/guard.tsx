import { useEffect } from 'react';
import { Navigate, Outlet, useLocation } from 'react-router-dom';
import { Button } from '@atomus';
import { ensureSession } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { useSession } from '@/lib/use-session';
import { ErrorState, Loading } from '@/components/feedback';

export function SessionGuard() {
  const session = useSession();
  const location = useLocation();
  useEffect(() => {
    void ensureSession();
  }, []);
  useEffect(() => {
    if (session.state === 'anonymous') queryClient.clear();
  }, [session.state]);
  if (session.state === 'checking') return <Loading label="正在恢复会话" />;
  if (session.state === 'unavailable')
    return (
      <div className="standalone">
        <ErrorState error={new Error(session.error)} />
        <Button onClick={() => window.location.reload()}>重新连接</Button>
      </div>
    );
  if (session.state === 'anonymous')
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  if (session.passwordChangeRequired && location.pathname !== '/password')
    return <Navigate to="/password" replace />;
  return <Outlet />;
}
