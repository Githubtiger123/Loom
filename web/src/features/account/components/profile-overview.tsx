import { Avatar, Card } from '@atomus';
import { StatusBadge } from '@/components/feedback';
import { formatDate } from '@/lib/format';
import type { Me } from '@/lib/types';

export function ProfileOverview({ user }: { user: Me['user'] }) {
  return (
    <Card padding="lg">
      <div className="stack spacious">
        <div className="profile-identity">
          <Avatar name={user.displayName || user.username} size="2xl" aria-hidden="true" />
          <div className="stack compact">
            <h2>{user.displayName || user.username}</h2>
            <span className="muted break-word">{user.username}</span>
          </div>
          <StatusBadge status={user.status} />
        </div>
        <dl className="details-grid">
          <dt>用户 ID</dt>
          <dd>{user.id}</dd>
          <dt>登录账号</dt>
          <dd className="break-word">{user.username}</dd>
          <dt>展示名称</dt>
          <dd className="break-word">{user.displayName || '未设置'}</dd>
          <dt>联系邮箱</dt>
          <dd className="break-word">{user.email || '未设置'}</dd>
          <dt>身份来源</dt>
          <dd className="break-word">{user.issuer || '未提供'}</dd>
          <dt>账号创建时间</dt>
          <dd>{formatDate(user.createdAt)}</dd>
        </dl>
        <p className="small muted">
          资料由平台或组织身份源维护；如需更改，请联系管理员。这里不展示密码或登录凭据。
        </p>
      </div>
    </Card>
  );
}
