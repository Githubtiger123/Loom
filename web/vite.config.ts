import { fileURLToPath, URL } from 'node:url';
import { existsSync, readFileSync } from 'node:fs';
import react from '@vitejs/plugin-react';
import { defineConfig, loadEnv } from 'vite';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const certFile = fileURLToPath(new URL('./certs/cert.pem', import.meta.url));
  const keyFile = fileURLToPath(new URL('./certs/key.pem', import.meta.url));
  // 本地登录要求 HTTPS origin；证书存在时自动启用，缺失则退回 HTTP。
  const httpsOptions =
    existsSync(certFile) && existsSync(keyFile)
      ? { cert: readFileSync(certFile), key: readFileSync(keyFile) }
      : undefined;
  return {
    plugins: [react()],
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url)),
        '@atomus': fileURLToPath(new URL('./vendor/atomus/react/index.ts', import.meta.url)),
      },
      dedupe: ['react', 'react-dom'],
    },
    server: {
      port: 5173,
      strictPort: true,
      https: httpsOptions,
      // Keep the browser Origin/Host intact: Loom validates PublicOrigin.
      proxy: env.LOOM_API_PROXY_TARGET
        ? { '/api': { target: env.LOOM_API_PROXY_TARGET, changeOrigin: false } }
        : undefined,
    },
    build: { sourcemap: false, target: 'es2022' },
  };
});
