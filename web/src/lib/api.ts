import type { Tokens } from './types';

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

type Session = {
  state: 'checking' | 'authenticated' | 'anonymous' | 'unavailable';
  passwordChangeRequired: boolean;
  error?: string;
};
export const trustedHeaderMode = import.meta.env.VITE_AUTH_MODE === 'trusted-header';
let token = '';
let accessExpiresAt = 0;
let epoch = 0;
let snapshot: Session = { state: 'checking', passwordChangeRequired: false };
const listeners = new Set<() => void>();
let refreshFlight: Promise<void> | undefined;
let bootstrap: Promise<void> | undefined;
export const sessionStore = {
  subscribe(listener: () => void) {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  },
  getSnapshot: () => snapshot,
};
function publish(next: Session) {
  snapshot = next;
  listeners.forEach((listener) => listener());
}
function accept(tokens: Tokens) {
  if (
    !tokens ||
    typeof tokens.accessToken !== 'string' ||
    !tokens.accessToken ||
    !Number.isFinite(tokens.expiresIn) ||
    tokens.expiresIn <= 0 ||
    typeof tokens.passwordChangeRequired !== 'boolean'
  ) {
    throw new ApiError(502, 'INVALID_SESSION', '认证服务返回的会话格式无效');
  }
  token = tokens.accessToken;
  accessExpiresAt = Date.now() + tokens.expiresIn * 1000;
  publish({ state: 'authenticated', passwordChangeRequired: tokens.passwordChangeRequired });
}
export function clearSession() {
  epoch++;
  token = '';
  accessExpiresAt = 0;
  publish({ state: 'anonymous', passwordChangeRequired: false });
}

async function send<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api/v1${path}`, {
    ...init,
    credentials: 'same-origin',
    redirect: 'error',
    cache: 'no-store',
  });
  return readResponse<T>(response);
}

async function readResponse<T>(response: Response): Promise<T> {
  const payload = (await response.json().catch(() => null)) as {
    data?: T;
    error?: { code: string; message: string };
  } | null;
  if (!response.ok) {
    throw new ApiError(
      response.status,
      payload?.error?.code ?? 'HTTP_ERROR',
      payload?.error?.message ?? `请求失败（${response.status}）`,
    );
  }
  if (!payload || typeof payload !== 'object' || !('data' in payload))
    throw new ApiError(502, 'INVALID_RESPONSE', '服务返回格式无效');
  return payload.data as T;
}

async function refresh() {
  if (!refreshFlight) {
    const generation = epoch;
    refreshFlight = send<Tokens>('/auth/refresh', { method: 'POST' })
      .then((tokens) => {
        if (generation === epoch) accept(tokens);
      })
      .catch((error: unknown) => {
        if (generation === epoch && error instanceof ApiError && error.status === 401)
          clearSession();
        throw error;
      })
      .finally(() => {
        refreshFlight = undefined;
      });
  }
  await refreshFlight;
}

export function ensureSession() {
  if (!bootstrap) {
    const generation = epoch;
    bootstrap = trustedHeaderMode
      ? Promise.resolve().then(() =>
          publish({ state: 'authenticated', passwordChangeRequired: false }),
        )
      : refresh().catch((error: unknown) => {
          if (generation === epoch && !(error instanceof ApiError && error.status === 401)) {
            publish({
              state: 'unavailable',
              passwordChangeRequired: false,
              error: errorMessage(error),
            });
          }
        });
  }
  return bootstrap;
}

export async function login(username: string, password: string) {
  const generation = ++epoch;
  const tokens = await send<Tokens>('/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
  if (generation === epoch) accept(tokens);
}

type Options = { method?: string; body?: unknown; signal?: AbortSignal; key?: string };
export async function api<T>(path: string, options: Options = {}): Promise<T> {
  if (!path.startsWith('/') || path.startsWith('//')) throw new Error('API 路径必须为站内相对路径');
  const method = options.method ?? 'GET';
  const headers: Record<string, string> = {};
  if (options.body !== undefined) headers['Content-Type'] = 'application/json';
  if (method !== 'GET' && !path.startsWith('/auth/')) {
    headers['Idempotency-Key'] = options.key ?? crypto.randomUUID();
  }
  const request = () =>
    send<T>(path, {
      method,
      signal: options.signal,
      headers: token ? { ...headers, Authorization: `Bearer ${token}` } : headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
    });
  return withSession(request);
}

// Downloads use the same in-memory session and refresh rules as JSON requests.
// Bound the stream before constructing a Blob, including chunked responses.
export async function apiDownload(path: string, signal: AbortSignal) {
  if (!path.startsWith('/') || path.startsWith('//')) throw new Error('下载路径必须为站内相对路径');
  return withSession(async () => {
    const response = await fetch(`/api/v1${path}`, {
      signal,
      credentials: 'same-origin',
      redirect: 'error',
      cache: 'no-store',
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!response.ok) return readResponse<never>(response);
    const reader = response.body?.getReader();
    if (!reader) throw new Error('下载响应为空');
    const chunks: Uint8Array<ArrayBuffer>[] = [];
    let size = 0;
    try {
      for (;;) {
        const part = await reader.read();
        if (part.done) break;
        size += part.value.byteLength;
        if (size > 32 * 1024 * 1024) throw new Error('在线下载超过 32 MiB 上限');
        chunks.push(new Uint8Array(part.value));
      }
    } finally {
      await reader.cancel().catch(() => undefined);
      reader.releaseLock();
    }
    signal.throwIfAborted();
    const disposition = response.headers.get('Content-Disposition') ?? '';
    const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition)?.[1];
    const plain = /filename="([^"]+)"|filename=([^;]+)/i.exec(disposition);
    let name = plain?.[1] ?? plain?.[2] ?? 'artifact';
    if (encoded) {
      try {
        name = decodeURIComponent(encoded);
      } catch {
        /* Use the plain fallback. */
      }
    }
    name = name.replace(/[\\/\u0000-\u001f\u007f]/g, '_').slice(0, 180) || 'artifact';
    return { blob: new Blob(chunks, { type: 'application/octet-stream' }), name };
  });
}

async function withSession<T>(request: () => Promise<T>): Promise<T> {
  const generation = epoch;
  try {
    const result = await request();
    if (generation !== epoch) throw new ApiError(401, 'SESSION_CHANGED', '会话已改变，请重新加载');
    return result;
  } catch (error) {
    if (
      !(error instanceof ApiError) ||
      error.status !== 401 ||
      trustedHeaderMode ||
      generation !== epoch
    )
      throw error;
    await refresh();
    if (generation !== epoch) throw error;
    try {
      const result = await request();
      if (generation !== epoch)
        throw new ApiError(401, 'SESSION_CHANGED', '会话已改变，请重新加载');
      return result;
    } catch (retryError) {
      if (generation === epoch && retryError instanceof ApiError && retryError.status === 401)
        clearSession();
      throw retryError;
    }
  }
}

// The only cross-origin credential dispatch. The destination must be explicitly
// configured by the deployer; never trust an access URL as an authorization host.
export async function establishNotebookSession(origin: string, signal: AbortSignal) {
  const generation = epoch;
  const configured = import.meta.env.VITE_NOTEBOOK_ORIGIN as string | undefined;
  if (trustedHeaderMode) throw new Error('外部身份模式不签发 Notebook 本地会话');
  if (!configured || configured !== origin) throw new Error('Notebook 访问域名与部署配置不一致');
  const target = new URL(configured);
  if (
    target.protocol !== 'https:' ||
    target.origin !== configured ||
    target.hostname === window.location.hostname
  ) {
    throw new Error('Notebook 必须配置独立主机的 HTTPS 来源');
  }
  if (accessExpiresAt - Date.now() < 60_000) await refresh();
  signal.throwIfAborted();
  if (generation !== epoch) throw new Error('会话已变化，请重新打开环境');
  if (!token || snapshot.state !== 'authenticated') throw new Error('会话已失效，请重新登录');
  const request = async () => {
    signal.throwIfAborted();
    const response = await fetch(`${origin}/api/v1/auth/notebook-session`, {
      method: 'POST',
      credentials: 'include',
      redirect: 'error',
      cache: 'no-store',
      referrerPolicy: 'no-referrer',
      signal,
      headers: { Authorization: `Bearer ${token}` },
    });
    const result = await readResponse<{ established: boolean }>(response);
    if (!result.established) throw new Error('Notebook 会话未建立');
    return result;
  };
  await withSession(request);
}
export function errorMessage(error: unknown): string {
  if (error instanceof TypeError) return '无法连接服务，请检查网络或稍后重试';
  return error instanceof Error ? error.message : '请求失败，请稍后重试';
}
