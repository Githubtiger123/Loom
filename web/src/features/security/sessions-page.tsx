import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Button, Modal } from '@atomus';
import { clearSession, trustedHeaderMode } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { ErrorState, NoData, PageHeading } from '@/components/feedback';
import { sessionApi } from './api';
import { SessionPanel } from './components/session-panel';
export function SessionsPage() {
  const [all, setAll] = useState(false);
  const mutation = useMutation({
    mutationFn: sessionApi.logoutAll,
    onSuccess: () => {
      clearSession();
      queryClient.clear();
    },
  });
  return (
    <main className="management-layout stack spacious">
      <Link className="text-link" to="/">
        ← 返回工作空间
      </Link>
      <PageHeading
        eyebrow="ACCOUNT SECURITY"
        title="我的登录会话"
        description="管理本地认证的登录设备，不管理外部 SSO 提供方的会话。"
        actions={
          !trustedHeaderMode ? (
            <Button hierarchy="outline" onClick={() => setAll(true)}>
              退出全部设备
            </Button>
          ) : undefined
        }
      />
      {trustedHeaderMode ? (
        <NoData
          title="当前使用外部身份认证"
          description="请到身份提供方管理登录设备，本平台不伪造其会话状态。"
        />
      ) : (
        <>
          <Link className="text-link" to="/password">
            修改密码
          </Link>
          <SessionPanel />
        </>
      )}
      {all && (
        <Modal
          open
          title="退出全部设备？"
          type="destructive"
          description="包括当前设备。所有旧会话会失效，你需要重新登录。"
          onClose={() => {
            if (!mutation.isPending) setAll(false);
          }}
          actions={
            <>
              <Button
                hierarchy="outline"
                disabled={mutation.isPending}
                onClick={() => setAll(false)}
              >
                取消
              </Button>
              <Button
                hierarchy="primary"
                loading={mutation.isPending}
                onClick={() => {
                  if (!mutation.isPending) mutation.mutate();
                }}
              >
                确认退出全部设备
              </Button>
            </>
          }
        >
          {mutation.isError && <ErrorState error={mutation.error} />}
        </Modal>
      )}
    </main>
  );
}
