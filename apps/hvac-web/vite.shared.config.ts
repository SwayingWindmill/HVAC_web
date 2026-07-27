import react from '@vitejs/plugin-react';
import { readFileSync } from 'node:fs';
import { fileURLToPath, URL } from 'node:url';
import type { UserConfig } from 'vite';

export type HvacWebBuildTarget = 'demo' | 'real';

const aiRuntimeTarget = process.env.AI_RUNTIME_PROXY_TARGET || 'http://127.0.0.1:3001';
const platformGatewayTarget = process.env.PLATFORM_GATEWAY_PROXY_TARGET || 'http://127.0.0.1:8080';
const legacyApiTarget = process.env.LEGACY_API_PROXY_TARGET || 'http://localhost:3000';
const viteTLSCert = process.env.VITE_TLS_CERT;
const viteTLSKey = process.env.VITE_TLS_KEY;
const https = viteTLSCert && viteTLSKey ? { cert: readFileSync(viteTLSCert), key: readFileSync(viteTLSKey) } : undefined;

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

function entryGraphPlugin(target: HvacWebBuildTarget) {
  const entry = target === 'real' ? '/src/real/main.tsx' : '/src/demo/main.tsx';

  return {
    name: `hvac-web-${target}-entry-graph`,
    transformIndexHtml: {
      order: 'pre' as const,
      handler(html: string) {
        if (!html.includes('/src/main.tsx')) {
          throw new Error('HVAC Web index.html no longer exposes the expected compatibility entry.');
        }
        return html.replace('/src/main.tsx', entry);
      },
    },
  };
}

function demoProxy() {
  return {
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
}

function realProxy() {
  return {
    '/api/v1': { target: platformGatewayTarget, changeOrigin: true },
  };
}

export function createHvacWebConfig(target: HvacWebBuildTarget): UserConfig {
  const isReal = target === 'real';
  const buildId = process.env.HVAC_WEB_BUILD_ID?.trim() || `${target}-local`;
  const gatewayBasePath = process.env.HVAC_WEB_GATEWAY_BASE_PATH?.trim() || '';
  const realtimeProtocol = process.env.HVAC_WEB_REALTIME_PROTOCOL?.trim() || '';

  return {
    plugins: [react(), entryGraphPlugin(target)],
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url)),
        ...(isReal ? {} : { 'node-fetch': fileURLToPath(new URL('./src/ai/nodeFetchBrowserShim.ts', import.meta.url)) }),
      },
    },
    define: {
      __HVAC_WEB_BUILD_TARGET__: JSON.stringify(target),
      __HVAC_WEB_BUILD_ID__: JSON.stringify(buildId),
      __HVAC_WEB_GATEWAY_BASE_PATH__: JSON.stringify(gatewayBasePath),
      __HVAC_WEB_REALTIME_PROTOCOL__: JSON.stringify(realtimeProtocol),
    },
    server: {
      port: isReal ? 5174 : 5173,
      https,
      proxy: isReal ? realProxy() : demoProxy(),
    },
    build: {
      outDir: `dist/${target}`,
      emptyOutDir: true,
      manifest: true,
      chunkSizeWarningLimit: 1200,
      rollupOptions: {
        output: {
          manualChunks,
        },
      },
    },
  };
}
