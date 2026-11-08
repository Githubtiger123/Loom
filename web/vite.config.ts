import { fileURLToPath, URL } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig, loadEnv } from 'vite';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
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
      // Keep the browser Origin/Host intact: Loom validates PublicOrigin.
      proxy: env.LOOM_API_PROXY_TARGET
        ? { '/api': { target: env.LOOM_API_PROXY_TARGET, changeOrigin: false } }
        : undefined,
    },
    build: { sourcemap: false, target: 'es2022' },
  };
});
