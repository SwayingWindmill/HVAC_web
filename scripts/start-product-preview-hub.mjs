import { spawn } from 'node:child_process';
import { createServer } from 'node:http';

const hubPort = Number(process.env.PRODUCT_PREVIEW_PORT ?? 5176);
const services = [
  { name: 'overview', port: 5191, script: 'scripts/run-overview-browser-review.mjs', env: { OVERVIEW_REVIEW_PORT: '5191' } },
  { name: 'assets', port: 5192, script: 'scripts/run-assets-workspace-browser-review.mjs', env: { ASSETS_REVIEW_PORT: '5192' } },
  { name: 'alarms', port: 5193, script: 'scripts/run-real-alarms-browser-audit.mjs', env: { ALARMS_REVIEW_PORT: '5193' } },
  { name: 'diagnostics', port: 5194, script: 'scripts/run-diagnostics-browser-review.mjs', env: { DIAGNOSTICS_REVIEW_PORT: '5194' } },
  { name: 'energy', port: 5195, script: 'scripts/run-energy-analysis-browser-review.mjs', env: { ENERGY_REVIEW_PORT: '5195' } },
  { name: 'control', port: 5196, script: 'scripts/run-control-center-browser-review.mjs', env: { CONTROL_REVIEW_PORT: '5196' } },
  { name: 'trends', port: 5197, script: 'scripts/run-trend-analysis-browser-review.mjs', env: { TREND_REVIEW_PORT: '5197' } },
  { name: 'comfort', port: 5198, script: 'scripts/run-comfort-browser-review.mjs', env: { COMFORT_REVIEW_PORT: '5198' } },
];

const pause = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function waitReady(url, name) {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    try {
      const response = await fetch(url, { cache: 'no-store' });
      if (response.ok) return;
    } catch {}
    await pause(100);
  }
  throw new Error(`${name} preview did not become ready at ${url}`);
}

const serviceProcesses = new Map();

for (const service of services) {
  const child = spawn(process.execPath, [service.script, '--serve'], {
    detached: false,
    stdio: 'ignore',
    env: { ...process.env, ...service.env },
  });
  const state = { child, status: 'starting', exitCode: null, signal: null };
  serviceProcesses.set(service.name, state);
  child.on('exit', (code, signal) => {
    state.status = 'stopped';
    state.exitCode = code;
    state.signal = signal;
  });
}

await Promise.all(services.map(async (service) => {
  await waitReady(`http://127.0.0.1:${service.port}`, service.name);
  const state = serviceProcesses.get(service.name);
  if (state) state.status = 'ready';
}));

const pages = [
  { id: 'overview', number: '03', label: '站点概览', url: 'http://127.0.0.1:5191/' },
  { id: 'operations', number: '04', label: '系统运行', url: 'http://127.0.0.1:5191/?page=operations' },
  { id: 'trends', number: '05', label: '趋势分析', url: 'http://127.0.0.1:5197/' },
  { id: 'assets', number: '06', label: '设备中心', url: 'http://127.0.0.1:5192/' },
  { id: 'device-detail', number: '07', label: '设备详情', url: 'http://127.0.0.1:5192/?page=device-detail' },
  { id: 'comfort', number: '08', label: '舒适与室内环境', url: 'http://127.0.0.1:5198/' },
  { id: 'alarms', number: '09', label: '告警中心', url: 'http://127.0.0.1:5193/' },
  { id: 'diagnostics', number: '10', label: '诊断中心', url: 'http://127.0.0.1:5194/' },
  { id: 'energy', number: '14', label: '能源分析', url: 'http://127.0.0.1:5195/' },
  { id: 'control', number: '25', label: '控制中心', url: 'http://127.0.0.1:5196/' },
];

const pageData = JSON.stringify(Object.fromEntries(pages.map((page) => [page.id, page])));
const links = pages.map((page) => `<a href="#${page.id}">${page.number} · ${page.label}</a>`).join('');

const html = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>当前新前端预览</title>
  <style>
    * { box-sizing: border-box; }
    html, body { min-height: 100%; margin: 0; background: #fff; color: #18181b; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    main { width: min(720px, calc(100% - 48px)); margin: 64px auto; }
    h1 { margin: 0 0 8px; font-size: 24px; }
    p { margin: 0 0 24px; color: #71717a; }
    nav { display: grid; gap: 8px; }
    a { display: block; padding: 12px 14px; border: 1px solid #e4e4e7; border-radius: 8px; color: inherit; text-decoration: none; }
    a:hover { background: #f4f4f5; }
  </style>
</head>
<body>
  <main>
    <h1>当前新前端预览</h1>
    <p>选择页面后将直接打开该页面自身的正式 AppShell，不再使用 iframe 嵌套。</p>
    <nav>${links}</nav>
  </main>
  <script>
    const pages = ${pageData};
    function openSelectedPage() {
      const id = location.hash.slice(1);
      if (!id) return;
      const page = pages[id];
      if (page) location.replace(page.url);
    }
    window.addEventListener('hashchange', openSelectedPage);
    openSelectedPage();
  </script>
</body>
</html>`;

const server = createServer((request, response) => {
  if ((request.url ?? '/') === '/health') {
    const serviceStates = services.map((service) => {
      const state = serviceProcesses.get(service.name);
      return {
        name: service.name,
        port: service.port,
        status: state?.status ?? 'unknown',
        exitCode: state?.exitCode ?? null,
        signal: state?.signal ?? null,
      };
    });
    const healthy = serviceStates.every((service) => service.status === 'ready');
    response.writeHead(healthy ? 200 : 503, { 'content-type': 'application/json', 'cache-control': 'no-store' });
    response.end(JSON.stringify({ status: healthy ? 'ok' : 'degraded', services: serviceStates }));
    return;
  }
  response.writeHead(200, { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store' });
  response.end(html);
});

server.listen(hubPort, '0.0.0.0', () => {
  console.log(JSON.stringify({ url: `http://127.0.0.1:${hubPort}`, pages }));
});
