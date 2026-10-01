import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';
import { loadConfigFromFile } from 'vite';

async function loadConfig() {
  const result = await loadConfigFromFile(
    { command: 'serve', mode: 'test' },
    path.resolve('apps/hvac-web/vite.config.ts'),
  );
  assert.ok(result, 'apps/hvac-web/vite.config.ts');
  return result.config;
}

test('uses one authoritative Web config with Gateway-only proxy and production dist output', async () => {
  const previous = process.env.HVAC_WEB_FRONTEND_REVIEW;
  delete process.env.HVAC_WEB_FRONTEND_REVIEW;
  try {
    const config = await loadConfig();
    assert.equal(config.build?.outDir, 'dist');
    assert.deepEqual(Object.keys(config.server?.proxy ?? {}), ['/api/v1']);
    assert.equal(config.server?.proxy?.['/api/v1']?.target, 'http://127.0.0.1:8080');
  } finally {
    if (previous === undefined) delete process.env.HVAC_WEB_FRONTEND_REVIEW;
    else process.env.HVAC_WEB_FRONTEND_REVIEW = previous;
  }
});

test('keeps frontend review artifacts isolated from the production dist directory', async () => {
  const previous = process.env.HVAC_WEB_FRONTEND_REVIEW;
  process.env.HVAC_WEB_FRONTEND_REVIEW = 'true';
  try {
    const config = await loadConfig();
    assert.equal(config.build?.outDir, 'dist-frontend-review');
  } finally {
    if (previous === undefined) delete process.env.HVAC_WEB_FRONTEND_REVIEW;
    else process.env.HVAC_WEB_FRONTEND_REVIEW = previous;
  }
});

test('can disable HMR only for bounded browser certification servers', async () => {
  const previous = process.env.HVAC_WEB_AUDIT_DISABLE_HMR;
  process.env.HVAC_WEB_AUDIT_DISABLE_HMR = 'true';
  try {
    const config = await loadConfig();
    assert.equal(config.server?.hmr, false);
  } finally {
    if (previous === undefined) delete process.env.HVAC_WEB_AUDIT_DISABLE_HMR;
    else process.env.HVAC_WEB_AUDIT_DISABLE_HMR = previous;
  }
});
