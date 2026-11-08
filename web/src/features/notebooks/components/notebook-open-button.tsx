import { useEffect, useRef, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Button } from '@atomus';
import { ExternalLink } from 'lucide-react';
import { establishNotebookSession, trustedHeaderMode } from '@/lib/api';
import { ErrorState } from '@/components/feedback';
import { notebookApi } from '../api';
import { validatedNotebookURL } from '../access';

export function NotebookOpenButton({
  groupId,
  notebookId,
  disabled = false,
  primary = false,
}: {
  groupId: string;
  notebookId: number;
  disabled?: boolean;
  primary?: boolean;
}) {
  const pending = useRef<{ controller: AbortController; popup: Window } | undefined>(undefined);
  const [popupBlocked, setPopupBlocked] = useState(false);
  useEffect(
    () => () => {
      pending.current?.controller.abort();
      pending.current?.popup.close();
    },
    [],
  );
  const mutation = useMutation({
    mutationFn: async ({ popup, controller }: { popup: Window; controller: AbortController }) => {
      const timer = window.setTimeout(
        () => controller.abort(new Error('连接超时，请重试')),
        30_000,
      );
      try {
        const access = await notebookApi.access(groupId, notebookId, controller.signal);
        if (!access.ready || !access.url)
          throw new Error(access.reason || '环境尚未就绪，请稍后重试');
        const url = validatedNotebookURL(access.url);
        if (!trustedHeaderMode) await establishNotebookSession(url.origin, controller.signal);
        controller.signal.throwIfAborted();
        if (popup.closed) throw new Error('访问窗口已关闭，请重新打开');
        popup.location.replace(url.href);
        pending.current = undefined;
      } finally {
        window.clearTimeout(timer);
      }
    },
    onError: () => {
      pending.current?.popup.close();
      pending.current = undefined;
    },
  });
  const open = () => {
    if (pending.current || mutation.isPending) return;
    mutation.reset();
    setPopupBlocked(false);
    // Open synchronously under the user's click to avoid popup blockers. No token
    // enters this page, its URL, local storage, or the destination query string.
    const popup = window.open('about:blank', '_blank');
    if (!popup) {
      setPopupBlocked(true);
      return;
    }
    popup.opener = null;
    popup.document.title = '正在打开开发环境 · Loom';
    popup.document.body.textContent = '正在验证环境权限和访问状态，请稍候…';
    const controller = new AbortController();
    pending.current = { popup, controller };
    mutation.mutate({ popup, controller });
  };
  return (
    <div className="stack compact">
      <Button
        hierarchy={primary ? 'primary' : 'outline'}
        iconLeading={<ExternalLink size={16} />}
        disabled={disabled}
        loading={mutation.isPending}
        onClick={open}
      >
        打开环境
      </Button>
      {mutation.isError && <ErrorState error={mutation.error} />}
      {popupBlocked && (
        <ErrorState error={new Error('浏览器阻止了新窗口，请允许本站弹出窗口后重试')} />
      )}
    </div>
  );
}
