import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath, URL } from 'node:url';

const aiRuntimeTarget = process.env.AI_RUNTIME_PROXY_TARGET || 'http://127.0.0.1:3001';

function manualChunks(id: string) {
  if (!id.includes('node_modules')) return undefined;

  const normalized = id.replace(/\\/g, '/');

  if (/node_modules\/(three|@react-three|@pmndrs|maath|troika-three|meshline)\//.test(normalized)) return 'vendor-three';
  if (/node_modules\/(echarts|zrender|echarts-for-react)\//.test(normalized)) return 'vendor-echarts';
  if (/node_modules\/@copilotkit\//.test(normalized)) return 'vendor-copilotkit';
  if (/node_modules\/(antd|@ant-design|rc-|@rc-component)\//.test(normalized)) return 'vendor-antd';
  if (/node_modules\/(react|react-dom|scheduler)\//.test(normalized)) return 'vendor-react';
  if (/node_modules\/react-router/.test(normalized)) return 'vendor-router';
  if (/node_modules\/@tanstack\//.test(normalized)) return 'vendor-query';
  if (/node_modules\/zustand\//.test(normalized)) return 'vendor-state';
  if (/node_modules\/(axios|socket\.io-client|engine\.io-client)\//.test(normalized)) return 'vendor-network';

  return undefined;
}

// hvac-backend runs on :3000 with global prefix /api/v1 and Socket.IO at /ws/telemetry.
// Proxy is wired now so swapping mock -> real API later is a one-line change in src/api.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      'node-fetch': fileURLToPath(new URL('./src/ai/nodeFetchBrowserShim.ts', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api/v1/copilotkit': {
        target: aiRuntimeTarget,
        changeOrigin: true,
        timeout: 0,
        proxyTimeout: 0,
      },
      '/api/v1': { target: 'http://localhost:3000', changeOrigin: true },
      '/ws': { target: 'ws://localhost:3000', ws: true, changeOrigin: true },
    },
  },
  build: {
    chunkSizeWarningLimit: 1200,
    rollupOptions: {
      output: {
        manualChunks,
      },
    },
  },
});
