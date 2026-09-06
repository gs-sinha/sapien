import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

// Dev-time daemon location. `sapien serve` (or `sapien mcp`, which starts a
// daemon too) writes <workspace>/.sapien/daemon.json with the actual port,
// e.g. { "port": 54213, "token": "..." }. Point SAPIEN_DAEMON_URL at
// http://127.0.0.1:<port> read from that file (or just paste the port in).
// SAPIEN_TOKEN is the same file's "token" field; the dev proxy injects it as
// a Bearer header so requests work without ever going through the cookie
// session flow.
const daemonURL = process.env.SAPIEN_DAEMON_URL || 'http://127.0.0.1:0';
const daemonToken = process.env.SAPIEN_TOKEN || '';

export default defineConfig({
  plugins: [react()],
  base: '/ui/',
  build: {
    outDir: '../internal/ui/dist',
    emptyOutDir: true,
    sourcemap: true,
  },
  server: {
    proxy: {
      '/v1': {
        target: daemonURL,
        changeOrigin: true,
        ws: true,
        headers: daemonToken ? { Authorization: `Bearer ${daemonToken}` } : {},
      },
      '/ui/session': {
        target: daemonURL,
        changeOrigin: true,
        headers: daemonToken ? { Authorization: `Bearer ${daemonToken}` } : {},
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: false,
  },
});
