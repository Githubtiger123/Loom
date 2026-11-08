import { Link } from 'react-router-dom';
import { useGlobalPermissions } from '@/lib/use-global-permissions';
export function AccountLinks() {
  const query = useGlobalPermissions();
  const admin = ['user.read', 'user.manage', 'role.manage', 'audit.read'].some((code) =>
    query.can(code),
  );
  return (
    <nav className="actions" aria-label="账号和平台管理">
      {admin && (
        <Link className="text-link" to="/admin">
          平台管理
        </Link>
      )}
      <Link className="text-link" to="/account/sessions">
        账号与会话
      </Link>
    </nav>
  );
}
