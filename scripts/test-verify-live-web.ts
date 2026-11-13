import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { existsSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { chromium } from '@playwright/test';

import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';
import {
  checkOriginHealth,
  DEFAULT_LIVE_ORIGIN,
  DEFAULT_LIVE_ROUTES,
  DEFAULT_LIVE_SLUG,
  parseLiveVerifyArguments,
  runLiveVerification,
  type VerifyBrowserPage,
  type VerifyBrowserSession,
} from './verify-live-web.ts';

function startHttpServer(handler: http.RequestListener): Promise<{ server: http.Server; origin: string }> {
  return new Promise((resolvePromise, rejectPromise) => {
    const server = http.createServer(handler);
    server.once('error', rejectPromise);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') {
        rejectPromise(new Error('Expected TCP address info from http.Server'));
        return;
      }
      resolvePromise({ server, origin: `http://127.0.0.1:${address.port}` });
    });
  });
}

test('parseLiveVerifyArguments validates flags and normalizes routes/origin', () => {
  const defaults = parseLiveVerifyArguments([]);
  assert.equal(defaults.help, false);
  assert.equal(defaults.slug, DEFAULT_LIVE_SLUG);
  assert.deepEqual(defaults.routes, DEFAULT_LIVE_ROUTES);
  assert.equal(defaults.origin, DEFAULT_LIVE_ORIGIN);
  assert.equal(defaults.label, undefined);

  const custom = parseLiveVerifyArguments([
    '--slug=system-audit',
    '--route=system?tab=audit',
    '--routes=/operations/alarms,/energy/reports',
    '--origin=http://127.0.0.1:5194/',
    '--label=after',
    '--timeout-ms=15000',
  ]);
  assert.equal(custom.slug, 'system-audit');
  assert.deepEqual(custom.routes, ['/system?tab=audit', '/operations/alarms', '/energy/reports']);
  assert.equal(custom.origin, 'http://127.0.0.1:5194');
  assert.equal(custom.label, 'after');
  assert.equal(custom.timeoutMs, 15000);

  assert.throws(() => parseLiveVerifyArguments(['--slug=../escape']), /Invalid --slug/);
  assert.throws(() => parseLiveVerifyArguments(['--origin=ftp://localhost']), /only http:\/\/ and https:\/\//);
  assert.throws(() => parseLiveVerifyArguments(['--timeout-ms=0']), /Invalid --timeout-ms/);
  assert.throws(() => parseLiveVerifyArguments(['--unknown']), /Unknown argument/);
});

test('checkOriginHealth distinguishes 200, 503, and unreachable endpoints', async () => {
  let healthy = true;
  const { server, origin } = await startHttpServer((req, res) => {
    if (req.url === '/healthz') {
      res.writeHead(healthy ? 200 : 503, { 'Content-Type': 'text/plain' });
      res.end(healthy ? 'ok' : 'unavailable');
      return;
    }
    res.writeHead(404);
    res.end();
  });

  try {
    const okResult = await checkOriginHealth(origin, 5000);
    assert.equal(okResult.ok, true);
    assert.equal(okResult.status, 200);

    healthy = false;
    const degradedResult = await checkOriginHealth(origin, 5000);
    assert.equal(degradedResult.ok, false);
    assert.equal(degradedResult.status, 503);
  } finally {
    await new Promise<void>((resolveClose) => server.close(() => resolveClose()));
  }

  const downResult = await checkOriginHealth(origin, 5000);
  assert.equal(downResult.ok, false);
  assert.equal(downResult.status, null);
});

test('runLiveVerification returns exitCode 2 PRECONDITION_UNMET without launching browser when /healthz fails', async () => {
  const tempRoot = mkdtempSync(join(tmpdir(), 'hvac-live-verify-down-'));
  let browserOpened = false;
  try {
    const outcome = await runLiveVerification(parseLiveVerifyArguments(['--slug=offline-check']), {
      repoRoot: tempRoot,
      checkHealth: async () => ({ ok: false, status: null, reason: 'connect ECONNREFUSED 127.0.0.1:8443' }),
      openSession: async () => {
        browserOpened = true;
        throw new Error('should not be called');
      },
    });
    assert.equal(outcome.exitCode, 2);
    assert.match(outcome.reason, /PRECONDITION_UNMET/);
    assert.equal(browserOpened, false);
  } finally {
    rmSync(tempRoot, { recursive: true, force: true });
  }
});

test('runLiveVerification closes browser in finally, retains evidence after failure, and checks console/page/server/overflow', async () => {
  const tempRoot = mkdtempSync(join(tmpdir(), 'hvac-live-verify-fake-'));
  try {
    let closedCount = 0;
    let currentUrl = '';
    const pageEvents = new EventEmitter();

    const fakePage: VerifyBrowserPage = {
      async setViewportSize() {},
      on(event, listener) {
        pageEvents.on(event, listener);
      },
      async goto(url: string) {
        currentUrl = url;
        if (url.endsWith('/broken')) {
          pageEvents.emit('console', { type: () => 'error', text: () => 'console boom' });
          pageEvents.emit('response', { status: () => 500, url: () => `${url}/api/fail` });
        }
      },
      async waitForLoadState() {},
      async waitForFunction() {
        if (currentUrl.endsWith('/blank')) {
          throw new Error('Timeout awaiting visible body text');
        }
      },
      async evaluate() {
        if (currentUrl.endsWith('/blank')) {
          return { scrollWidth: 1440, clientWidth: 1440, hasVisibleText: false };
        }
        return currentUrl.endsWith('/broken')
          ? { scrollWidth: 1620, clientWidth: 1440, hasVisibleText: true }
          : { scrollWidth: 1440, clientWidth: 1440, hasVisibleText: true };
      },
      async screenshot({ path }: { path: string }) {
        writeFileSync(path, Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]));
      },
      url() {
        return currentUrl;
      },
    };

    const fakeSession: VerifyBrowserSession = {
      browser: {
        async close() {
          closedCount += 1;
        },
      },
      page: fakePage,
      principal: { principal: { subject: 'admin-sub' } },
    };

    const failed = await runLiveVerification(
      parseLiveVerifyArguments(['--slug=fake-run', '--label=after', '--route=/ok', '--route=/broken', '--route=/blank']),
      {
        repoRoot: tempRoot,
        checkHealth: async () => ({ ok: true, status: 200, reason: 'HTTP 200' }),
        openSession: async () => fakeSession,
      },
    );

    assert.equal(closedCount, 1);
    assert.equal(failed.exitCode, 1);
    assert.ok(failed.reportPath && existsSync(failed.reportPath));
    assert.equal(failed.report?.routes[0]?.passed, true);
    assert.equal(failed.report?.routes[1]?.passed, false);
    assert.equal(failed.report?.routes[1]?.overflowPixels, 180);
    assert.deepEqual(failed.report?.routes[1]?.consoleErrors, ['console boom']);
    assert.equal(failed.report?.routes[1]?.serverErrors[0]?.status, 500);
    assert.equal(failed.report?.routes[2]?.passed, false);
    assert.equal(failed.report?.routes[2]?.hasVisibleText, false);
    for (const r of failed.report?.routes ?? []) {
      const pngPath = join(failed.evidenceDir, r.screenshotFile);
      assert.ok(statSync(pngPath).size > 0);
    }
  } finally {
    rmSync(tempRoot, { recursive: true, force: true });
  }
});

test('runLiveVerification drives real headless Chromium at 1440x900 on Linux and preserves PNG proof after close', { skip: process.platform === 'win32' }, async () => {
  const tempRoot = mkdtempSync(join(tmpdir(), 'hvac-live-verify-browser-'));
  const { server, origin } = await startHttpServer((req, res) => {
    if (req.url === '/healthz') {
      res.writeHead(200, { 'Content-Type': 'text/plain' });
      res.end('ok');
      return;
    }
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
    res.end('<!doctype html><html><body style="margin:0"><main style="width:1440px;height:600px"><h1>系统状态正常</h1></main></body></html>');
  });

  let closed = false;
  try {
    const result = await runLiveVerification(
      parseLiveVerifyArguments(['--slug=real-browser', `--origin=${origin}`, '--route=/status']),
      {
        repoRoot: tempRoot,
        openSession: async (targetOrigin: string) => {
          const browser = await chromium.launch({
            headless: true,
            executablePath: resolveLinuxBrowserExecutable(),
          });
          const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
          const page = await context.newPage();
          return {
            browser: {
              close: async () => {
                closed = true;
                await browser.close();
              },
            },
            page,
            principal: { principal: { subject: `live-${targetOrigin}` } },
          };
        },
      },
    );

    assert.equal(closed, true);
    assert.equal(result.exitCode, 0);
    assert.equal(result.report?.routes.length, 1);
    assert.equal(result.report?.routes[0]?.passed, true);
    assert.equal(result.report?.routes[0]?.overflowPixels, 0);
    const firstRoute = result.report?.routes[0];
    assert.ok(firstRoute && result.evidenceDir);
    const pngPath = join(result.evidenceDir, firstRoute.screenshotFile);
    const pngHeader = readFileSync(pngPath).subarray(0, 4);
    assert.deepEqual([...pngHeader], [0x89, 0x50, 0x4e, 0x47]);
  } finally {
    rmSync(tempRoot, { recursive: true, force: true });
    await new Promise<void>((resolveClose) => server.close(() => resolveClose()));
  }
});
