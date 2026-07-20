import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { readFileSync } from 'node:fs';
import { fileURLToPath, URL } from 'node:url';

const aiRuntimeTarget = process.env.AI_RUNTIME_PROXY_TARGET || 'http://127.0.0.1:3001';
const platformGatewayTarget = process.env.PLATFORM_GATEWAY_PROXY_TARGET || 'http://127.0.0.1:8080';
const legacyApiTarget = process.env.LEGACY_API_PROXY_TARGET || 'http://localhost:3000';
const s0GatewayOnly = process.env.S0_GATEWAY_ONLY === 'true';
const viteTLSCert = process.env.VITE_TLS_CERT;
const viteTLSKey = process.env.VITE_TLS_KEY;
const https = viteTLSCert && viteTLSKey ? { cert: readFileSync(viteTLSCert), key: readFileSync(viteTLSKey) } : undefined;

const gatewayOnlyProxy = {
  '/api/v1': { target: platformGatewayTarget, changeOrigin: true },
};

const standardDevelopmentProxy = {
  '/api/v1/health': { target: platformGatewayTarget, changeOrigin: true },
  '/api/v1/version': { target: platformGatewayTarget, changeOrigin: true },
  '/api/v1/copilotkit': {
    target: aiRuntimeTarget,
    changeOrigin: true,
    timeout: 0,
    proxyTimeout: 0,
  },
  '/api/v1': { target: legacyApiTarget, changeOrigin: true },
  '/ws': { target: 'ws://localhost:3000', ws: true, changeOrigin: true },
};

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

// S0 platform contracts are served only by platform-gateway. The dedicated
// S0 mode registers no Legacy, Copilot, or WebSocket proxy routes.
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
    https,
    proxy: s0GatewayOnly ? gatewayOnlyProxy : standardDevelopmentProxy,
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
