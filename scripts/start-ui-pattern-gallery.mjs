import { spawn } from 'node:child_process';
import { readFile, stat } from 'node:fs/promises';
import { createServer } from 'node:http';
import { basename, dirname, extname, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

const scriptPath = fileURLToPath(import.meta.url);
const root = resolve(dirname(scriptPath), '..');
const portArg = process.argv.find((argument) => argument.startsWith('--port='));
const port = Number(portArg?.slice('--port='.length) || process.env.UI_PATTERN_GALLERY_PORT || 5188);
const host = process.env.UI_PATTERN_GALLERY_HOST || '0.0.0.0';
const url = `http://127.0.0.1:${port}/design-system/patterns`;
const publicRoot = resolve(root, 'apps/hvac-web/public');

const html = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>智慧能源 UI Pattern Gallery</title>
  <link rel="stylesheet" href="/app.css" />
</head>
<body>
  <div id="root"></div>
  <script src="/app.js"></script>
</body>
</html>`;

function contentType(path) {
  const extension = extname(path).toLowerCase();
  if (extension === '.css') return 'text/css; charset=utf-8';
  if (extension === '.js') return 'text/javascript; charset=utf-8';
  if (extension === '.svg') return 'image/svg+xml';
  if (extension === '.png') return 'image/png';
  if (extension === '.jpg' || extension === '.jpeg') return 'image/jpeg';
  return 'application/octet-stream';
}

async function buildPreview() {
  const outdir = resolve(root, '.ui-pattern-gallery-preview');
  const result = await build({
    absWorkingDir: root,
    entryPoints: [resolve(root, 'apps/hvac-web/src/features/design-system/preview-main.tsx')],
    bundle: true,
    write: false,
    outdir,
    entryNames: 'app',
    assetNames: 'assets/[name]-[hash]',
    platform: 'browser',
    format: 'iife',
    target: ['es2020'],
    jsx: 'automatic',
    sourcemap: false,
    alias: { '@': resolve(root, 'apps/hvac-web/src') },
    define: { 'process.env.NODE_ENV': '"development"' },
    loader: {
      '.svg': 'dataurl',
      '.png': 'dataurl',
      '.jpg': 'dataurl',
      '.jpeg': 'dataurl',
      '.webp': 'dataurl',
    },
  });

  return new Map(result.outputFiles.map((file) => {
    const path = `/${relative(outdir, file.path).replaceAll('\\', '/')}`;
    return [path, file];
  }));
}

async function servePublic(pathname) {
  const relativePath = pathname.replace(/^\/+/, '');
  const candidate = resolve(publicRoot, relativePath);
  if (!candidate.startsWith(publicRoot)) return undefined;
  try {
    const info = await stat(candidate);
    if (!info.isFile()) return undefined;
    return { contents: await readFile(candidate), path: candidate };
  } catch {
    return undefined;
  }
}

async function runServer() {
  const outputs = await buildPreview();
  const server = createServer(async (request, response) => {
    const pathname = decodeURIComponent(new URL(request.url ?? '/', `http://${request.headers.host ?? 'localhost'}`).pathname);
    if (pathname === '/' || pathname === '/design-system/patterns') {
      response.writeHead(200, { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store' });
      response.end(html);
      return;
    }

    const output = outputs.get(pathname);
    if (output) {
      response.writeHead(200, { 'content-type': contentType(pathname), 'cache-control': 'no-store' });
      response.end(output.contents);
      return;
    }

    const publicFile = await servePublic(pathname);
    if (publicFile) {
      response.writeHead(200, { 'content-type': contentType(publicFile.path), 'cache-control': 'no-store' });
      response.end(publicFile.contents);
      return;
    }

    response.writeHead(404, { 'content-type': 'text/plain; charset=utf-8' });
    response.end(`Not found: ${basename(pathname)}`);
  });

  server.listen(port, host, () => {
    console.log(`UI Pattern Gallery serving at ${url}`);
  });
}

async function startDetached() {
  try {
    const response = await fetch(url);
    if (response.ok) {
      console.log(`UI Pattern Gallery already running at ${url}`);
      return;
    }
  } catch {}

  const child = spawn(process.execPath, [scriptPath, `--port=${port}`], {
    cwd: root,
    detached: true,
    stdio: 'ignore',
    env: { ...process.env, UI_PATTERN_GALLERY_CHILD: '1' },
  });
  child.unref();

  for (let attempt = 0; attempt < 120; attempt += 1) {
    try {
      const response = await fetch(url);
      if (response.ok) {
        console.log(`UI Pattern Gallery running at ${url}`);
        return;
      }
    } catch {}
    await new Promise((resolveWait) => setTimeout(resolveWait, 100));
  }
  throw new Error(`UI Pattern Gallery did not become ready at ${url}`);
}

if (process.env.UI_PATTERN_GALLERY_CHILD === '1') await runServer();
else await startDetached();
