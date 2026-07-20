import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import WebSocket from 'ws';

const baseUrl = process.env.HVAC_AUDIT_BASE_URL ?? 'http://127.0.0.1:4173';
const debugPort = Number(process.env.HVAC_BIGSCREEN_AUDIT_DEBUG_PORT ?? 9335);
const profileDir = join(tmpdir(), `hvac-bigscreen-audit-${process.pid}`);
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const candidates = [
  process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
].filter(Boolean);
const edgePath = candidates.find((candidate) => existsSync(candidate));
if (!edgePath) throw new Error('Microsoft Edge executable not found');

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

await mkdir(profileDir, { recursive: true });
const edge = spawn(edgePath, [
  '--headless=new',
  '--disable-gpu',
  '--no-first-run',
  '--no-default-browser-check',
  '--hide-scrollbars',
  `--remote-debugging-port=${debugPort}`,
  `--user-data-dir=${profileDir}`,
  `${baseUrl}/bigscreen`,
], { stdio: 'ignore' });

async function waitForDebugger() {
  for (let index = 0; index < 160; index += 1) {
    try {
      const response = await fetch(`http://127.0.0.1:${debugPort}/json`);
      if (response.ok) return response.json();
    } catch {}
    await pause(100);
  }
  throw new Error('Edge debugger unavailable');
}

function createClient(url) {
  const socket = new WebSocket(url);
  let nextId = 0;
  const pending = new Map();
  const ready = new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true });
    socket.addEventListener('error', reject, { once: true });
  });
  socket.addEventListener('message', ({ data }) => {
    const message = JSON.parse(String(data));
    if (!message.id) return;
    const job = pending.get(message.id);
    if (!job) return;
    pending.delete(message.id);
    if (message.error) job.reject(new Error(message.error.message));
    else job.resolve(message.result);
  });
  return {
    async send(method, params = {}) {
      await ready;
      const id = ++nextId;
      return new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
        socket.send(JSON.stringify({ id, method, params }));
      });
    },
    close() {
      socket.close();
    },
  };
}

async function evaluate(client, expression) {
  const response = await client.send('Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (response.exceptionDetails) {
    throw new Error(response.exceptionDetails.exception?.description ?? 'Evaluation failed');
  }
  return response.result.value;
}

async function waitFor(client, expression, label) {
  for (let index = 0; index < 200; index += 1) {
    try {
      if (await evaluate(client, expression)) return;
    } catch {}
    await pause(100);
  }
  throw new Error(`Timeout: ${label}`);
}

const viewports = [
  { name: 'full-hd', width: 1920, height: 1080, expectedCharts: 3, minChartHeight: 180 },
  { name: 'desktop', width: 1440, height: 900, expectedCharts: 3, minChartHeight: 100 },
  { name: 'laptop', width: 1366, height: 768, expectedCharts: 2, minChartHeight: 180 },
  { name: 'compact', width: 1024, height: 768, expectedCharts: 1, minChartHeight: 200 },
];

let client;
try {
  const pages = await waitForDebugger();
  const page = pages.find((item) => item.type === 'page' && item.url.startsWith(baseUrl))
    ?? pages.find((item) => item.type === 'page');
  assert(page, 'No browser page was available for BigScreen audit');
  client = createClient(page.webSocketDebuggerUrl);
  await client.send('Page.enable');
  await client.send('Runtime.enable');

  const checks = [];
  for (const viewport of viewports) {
    await client.send('Emulation.setDeviceMetricsOverride', {
      width: viewport.width,
      height: viewport.height,
      deviceScaleFactor: 1,
      mobile: false,
    });
    await client.send('Page.navigate', { url: `${baseUrl}/bigscreen` });
    await waitFor(client, `document.readyState === 'complete' && location.pathname === '/bigscreen'`, `${viewport.name} route`);
    await waitFor(client, `Boolean(document.querySelector('.bigscreen-stage') && document.querySelector('[data-testid="bigscreen-system-canvas"]'))`, `${viewport.name} layout`);
    await pause(1800);

    const state = await evaluate(client, `(() => {
      const rect = (element) => {
        const value = element.getBoundingClientRect();
        return { left: value.left, top: value.top, right: value.right, bottom: value.bottom, width: value.width, height: value.height };
      };
      const contains = (outer, inner, tolerance = 3) => inner.left >= outer.left - tolerance
        && inner.top >= outer.top - tolerance
        && inner.right <= outer.right + tolerance
        && inner.bottom <= outer.bottom + tolerance;
      const overlapArea = (first, second) => Math.max(0, Math.min(first.right, second.right) - Math.max(first.left, second.left))
        * Math.max(0, Math.min(first.bottom, second.bottom) - Math.max(first.top, second.top));
      const frames = [...document.querySelectorAll('.bigscreen-chart-frame')].filter((element) => element.offsetParent !== null);
      const chartStates = frames.map((frame) => {
        const frameRect = rect(frame);
        const canvas = frame.querySelector('canvas');
        const canvasRect = canvas ? rect(canvas) : null;
        return {
          height: frameRect.height,
          contained: Boolean(canvasRect && contains(frameRect, canvasRect)),
          canvas: canvasRect,
        };
      });
      const chartOverlaps = [];
      for (let first = 0; first < chartStates.length; first += 1) {
        for (let second = first + 1; second < chartStates.length; second += 1) {
          const area = chartStates[first].canvas && chartStates[second].canvas
            ? overlapArea(chartStates[first].canvas, chartStates[second].canvas)
            : 0;
          if (area > 1) chartOverlaps.push({ first, second, area });
        }
      }
      const systemFrame = document.querySelector('[data-testid="bigscreen-system-canvas"]');
      const systemCanvas = systemFrame?.querySelector('canvas');
      const systemFrameRect = systemFrame ? rect(systemFrame) : null;
      const systemCanvasRect = systemCanvas ? rect(systemCanvas) : null;
      const deviceItems = [...document.querySelectorAll('.bigscreen-device-item')]
        .filter((element) => element.offsetParent !== null)
        .map(rect);
      const deviceOverlaps = [];
      for (let first = 0; first < deviceItems.length; first += 1) {
        for (let second = first + 1; second < deviceItems.length; second += 1) {
          const area = overlapArea(deviceItems[first], deviceItems[second]);
          if (area > 1) deviceOverlaps.push({ first, second, area });
        }
      }
      const absoluteDeviceCards = [...document.querySelectorAll('.bigscreen-system-canvas *')].filter((element) => {
        const label = element.firstElementChild?.textContent?.trim() ?? '';
        return getComputedStyle(element).position === 'absolute'
          && ['冷机', '冷却塔', '冷冻泵', '冷却泵', '末端'].includes(label);
      }).length;
      return {
        chartCount: chartStates.length,
        chartContained: chartStates.every((item) => item.contained),
        minimumChartHeight: chartStates.length > 0 ? Math.min(...chartStates.map((item) => item.height)) : 0,
        chartOverlaps,
        systemCanvasContained: Boolean(systemFrameRect && systemCanvasRect && contains(systemFrameRect, systemCanvasRect)),
        deviceItemCount: deviceItems.length,
        deviceOverlaps,
        absoluteDeviceCards,
        pageOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth + 1
          || document.documentElement.scrollHeight > document.documentElement.clientHeight + 1,
        hasOperationalFooter: document.querySelector('.bigscreen-footer')?.textContent?.includes('数据更新时间') ?? false,
        hasBrand: document.querySelector('.bigscreen-brand h1')?.textContent?.includes('泉来禾智慧能源驾驶舱')
          && document.querySelector('.bigscreen-brand-mark')?.complete
          && document.querySelector('.bigscreen-brand-mark')?.naturalWidth > 0,
        hasControlledViewLabel: document.querySelector('.bigscreen-canvas-mode')?.textContent?.includes('滚轮缩放') ?? false,
        zoomMin: Number(systemFrame?.dataset.zoomMin ?? 0),
        zoomMax: Number(systemFrame?.dataset.zoomMax ?? 0),
      };
    })()`);

    assert(state.chartCount === viewport.expectedCharts, `${viewport.name}: expected ${viewport.expectedCharts} charts, received ${state.chartCount}`);
    assert(state.chartContained, `${viewport.name}: ECharts canvas escaped its chart frame`);
    assert(state.minimumChartHeight >= viewport.minChartHeight, `${viewport.name}: chart height ${state.minimumChartHeight} is below ${viewport.minChartHeight}`);
    assert(state.chartOverlaps.length === 0, `${viewport.name}: ECharts canvases overlap: ${JSON.stringify(state.chartOverlaps)}`);
    assert(state.systemCanvasContained, `${viewport.name}: 3D canvas escaped the system viewport`);
    assert(state.deviceItemCount >= 2, `${viewport.name}: device status rail is missing`);
    assert(state.deviceOverlaps.length === 0, `${viewport.name}: device status items overlap: ${JSON.stringify(state.deviceOverlaps)}`);
    assert(state.absoluteDeviceCards === 0, `${viewport.name}: fixed-position device cards returned to the 3D canvas`);
    assert(!state.pageOverflow, `${viewport.name}: page-level overflow detected`);
    assert(state.hasOperationalFooter, `${viewport.name}: operational footer is missing`);
    assert(state.hasBrand, `${viewport.name}: 泉来禾 brand identity is missing or failed to load`);
    assert(state.hasControlledViewLabel, `${viewport.name}: controlled zoom guidance is missing`);
    assert(state.zoomMin === 10.5 && state.zoomMax === 18.5, `${viewport.name}: zoom range is invalid: ${state.zoomMin}–${state.zoomMax}`);
    checks.push(`${viewport.name} layout remains collision-free`);
  }

  console.log(JSON.stringify({ passed: checks.length, checks }, null, 2));
} finally {
  client?.close();
  if (edge.exitCode === null && edge.signalCode === null) edge.kill('SIGTERM');
  await Promise.race([
    once(edge, 'exit').catch(() => undefined),
    pause(1400),
  ]);
  for (let attempt = 0; attempt < 6; attempt += 1) {
    try {
      await rm(profileDir, { recursive: true, force: true });
      break;
    } catch (error) {
      if (attempt === 5 || !['EBUSY', 'EPERM'].includes(error?.code)) throw error;
      await pause(250 * (attempt + 1));
    }
  }
}
