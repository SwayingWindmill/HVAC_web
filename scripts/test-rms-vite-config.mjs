import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';
import { loadConfigFromFile } from 'vite';

async function loadConfig(relativePath) {
  const result = await loadConfigFromFile(
    { command: 'serve', mode: 'test' },
    path.resolve(relativePath),
  );
  assert.ok(result, relativePath);
  return result.config;
}

test('preserves the existing S0 Gateway-only Demo development topology', async () => {
  const previous = process.env.S0_GATEWAY_ONLY;
  process.env.S0_GATEWAY_ONLY = 'true';
  try {
    const config = await loadConfig('apps/hvac-web/vite.config.ts');
    assert.deepEqual(Object.keys(config.server?.proxy ?? {}), ['/api/v1']);
    assert.equal(config.server?.proxy?.['/api/v1']?.target, 'http://127.0.0.1:8080');
  } finally {
    if (previous === undefined) delete process.env.S0_GATEWAY_ONLY;
    else process.env.S0_GATEWAY_ONLY = previous;
  }
});

test('uses an isolated Real entry plugin, output directory, and Gateway-only proxy', async () => {
  const config = await loadConfig('apps/hvac-web/vite.real.config.ts');
  assert.ok(config.plugins?.some((plugin) => plugin && 'name' in plugin && plugin.name === 'hvac-web-real-entry-graph'));
  assert.equal(config.build?.outDir, 'dist/real');
  assert.deepEqual(Object.keys(config.server?.proxy ?? {}), ['/api/v1']);
  assert.equal(config.server?.proxy?.['/api/v1']?.target, 'http://127.0.0.1:8080');
});
