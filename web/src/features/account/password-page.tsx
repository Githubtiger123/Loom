import { Card } from '@atomus';
import { trustedHeaderMode } from '@/lib/api';
import { NoData, PageHeading } from '@/components/feedback';
import { PasswordForm } from '@/features/auth/components/password-form';

export function AccountPasswordPage() {
  return (
    <div className="stack spacious">
      <PageHeading
        eyebrow="ACCOUNT SECURITY"
        title="修改密码"
        description="修改后将撤销已有本地会话，请使用新密码重新登录。"
      />
      {trustedHeaderMode ? (
        <NoData
          title="密码由组织身份提供方管理"
          description="请通过组织统一认证入口修改密码，本平台不修改外部身份密码。"
        />
      ) : (
        <div className="account-form">
          <Card padding="lg">
            <PasswordForm />
          </Card>
        </div>
      )}
    </div>
  );
}
