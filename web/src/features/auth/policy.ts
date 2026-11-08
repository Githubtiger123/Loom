// Login may return to a business page, never to an authentication flow. This
// also sanitizes older history entries that already contain /password as `from`.
export function loginReturnPath(from: unknown): string {
  if (
    typeof from !== 'string' ||
    !from.startsWith('/') ||
    from.startsWith('//') ||
    /[\\\u0000-\u0020\u007f]/.test(from)
  )
    return '/';
  try {
    const url = new URL(from, 'https://loom.invalid');
    if (url.origin !== 'https://loom.invalid') return '/';
    const pathname = decodeURIComponent(url.pathname).replace(/\/+$/, '').toLowerCase();
    if (pathname === '/login' || pathname === '/password') return '/';
    if (pathname === '/account/password') return '/account';
    return `${url.pathname}${url.search}${url.hash}`;
  } catch {
    return '/';
  }
}
