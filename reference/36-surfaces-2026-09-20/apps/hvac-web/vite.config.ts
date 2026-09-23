import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { tanstackRouter } from '@tanstack/router-plugin/vite';
import { readFileSync } from 'node:fs';
import { fileURLToPath, URL } from 'node:url';
import { defineConfig } from 'vite';

const platformGatewayTarget = process.env.PLATFORM_GATEWAY_PROXY_TARGET || 'http://127.0.0.1:8080';
const auditDisableHMR = process.env.HVAC_WEB_AUDIT_DISABLE_HMR === 'true';
const viteTLSCert = process.env.VITE_TLS_CERT;
const viteTLSKey = process.env.VITE_TLS_KEY;
const https = viteTLSCert && viteTLSKey
  ? { cert: readFileSync(viteTLSCert), key: readFileSync(viteTLSKey) }
  : undefined;

function manualChunks(id: string) {
  if (!id.includes('node_modules')) return undefined;

  const normalized = id.replace(/\\/g, '/');

  if (/node_modules\/@copilotkit\//.test(normalized)) return 'vendor-copilotkit';
  if (/node_modules\/@antv\/g6\//.test(normalized)) return 'vendor-g6';
  if (/node_modules\/@antv\/(?:x6|x6-react-shape)\//.test(normalized)) return 'vendor-x6';
  if (/node_modules\/(react|react-dom|scheduler)\//.test(normalized)) return 'vendor-react';
  if (/node_modules\/@tanstack\/(?:react-router|router-core|history)\//.test(normalized)) return 'vendor-router';
  if (/node_modules\/@tanstack\//.test(normalized)) return 'vendor-query';
  if (/node_modules\/zustand\//.test(normalized)) return 'vendor-state';

  return undefined;
}

export default defineConfig(() => {
  const buildId = process.env.HVAC_WEB_BUILD_ID?.trim() || 'local';
  const gatewayBasePath = process.env.HVAC_WEB_GATEWAY_BASE_PATH?.trim() || '/api/v1';
  const realtimeProtocol = process.env.HVAC_WEB_REALTIME_PROTOCOL?.trim() || 'centrifugo-v1';

  return {
    plugins: [
      tanstackRouter({
        target: 'react',
        autoCodeSplitting: true,
      }),
      react(),
      tailwindcss(),
    ],
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url)),
      },
    },
    define: {
      __HVAC_WEB_BUILD_ID__: JSON.stringify(buildId),
      __HVAC_WEB_GATEWAY_BASE_PATH__: JSON.stringify(gatewayBasePath),
      __HVAC_WEB_REALTIME_PROTOCOL__: JSON.stringify(realtimeProtocol),
    },
    server: {
      port: 5174,
      https,
      hmr: auditDisableHMR ? false : undefined,
      proxy: {
        '/api/v1': { target: platformGatewayTarget, changeOrigin: true },
      },
    },
    build: {
      outDir: 'dist',
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
});
