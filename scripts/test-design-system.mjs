import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { resolve } from 'node:path';
import test from 'node:test';

const root = resolve(import.meta.dirname, '..');

test('design system gate validates the authoritative guide, references, previews and implementation tokens', () => {
  const result = spawnSync(process.execPath, ['scripts/check-design-system.mjs'], {
    cwd: root,
    encoding: 'utf8',
  });

  assert.equal(result.status, 0, result.stderr || result.stdout);
  assert.match(result.stdout, /Design system check passed/);
});

test('design system gate rejects radius drift beyond the committed baseline', async () => {
  const { compareRadiusBaseline } = await import('./check-design-system.mjs');
  const baseline = {
    'apps/hvac-web/src/example.css': {
      '10px': 1,
    },
  };
  const current = {
    'apps/hvac-web/src/example.css': {
      '10px': 2,
    },
  };

  assert.deepEqual(compareRadiusBaseline(current, baseline), [
    'apps/hvac-web/src/example.css uses 10px 2 times; baseline allows 1',
  ]);
});
