import { trustedHeaderMode } from '@/lib/api';

export function validatedNotebookURL(raw: string) {
  const configured = import.meta.env.VITE_NOTEBOOK_ORIGIN as string | undefined;
  const url = new URL(raw, window.location.origin);
  const expectedOrigin = configured || (trustedHeaderMode ? window.location.origin : '');
  if (!expectedOrigin) throw new Error('管理员尚未配置 Notebook 访问来源');
  if (
    url.origin !== expectedOrigin ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    !/^\/notebook\/[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\/[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\/$/.test(
      url.pathname,
    )
  ) {
    throw new Error('Notebook 访问地址与受信配置不一致');
  }
  if (url.protocol !== 'https:' && !(trustedHeaderMode && url.origin === window.location.origin))
    throw new Error('Notebook 访问必须使用 HTTPS');
  return url;
}
