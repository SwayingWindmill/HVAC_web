import { createServer as createViteServer } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const fixtureRoot = resolve(root, 'scripts/fixtures/remaining-pages-review');
const port = Number(process.env.REMAINING_REVIEW_PORT ?? 5210);

const server = await createViteServer({
  root: fixtureRoot,
  publicDir: false,
  configFile: false,
  logLevel: 'error',
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': resolve(root, 'apps/hvac-web/src') } },
  server: { host: '127.0.0.1', port, strictPort: true },
});

await server.listen();
console.log(JSON.stringify({ mode: 'serve', url: `http://127.0.0.1:${port}` }));
await new Promise(() => {});
