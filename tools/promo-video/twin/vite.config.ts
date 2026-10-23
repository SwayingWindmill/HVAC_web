import react from '@vitejs/plugin-react';
import { fileURLToPath, URL } from 'node:url';
import { defineConfig } from 'vite';

// The digital twin's own scene code, bundled as one script the film page loads. Imports resolve into
// apps/hvac-web, so the film draws exactly the models, materials and pipework the product draws.

export default defineConfig({
  root: fileURLToPath(new URL('.', import.meta.url)),
  plugins: [react()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('../../../apps/hvac-web/src', import.meta.url)) },
  },
  define: { 'process.env.NODE_ENV': JSON.stringify('production') },
  build: {
    outDir: fileURLToPath(new URL('../film/assets/twin', import.meta.url)),
    emptyOutDir: true,
    lib: { entry: 'film.tsx', name: 'HVACTwin', formats: ['iife'], fileName: () => 'twin.js' },
    minify: true,
    sourcemap: false,
  },
});
