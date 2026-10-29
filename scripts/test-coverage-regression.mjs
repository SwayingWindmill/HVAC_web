import assert from 'node:assert/strict';
import test from 'node:test';

import { regressed, statementPercent } from './check-coverage-regression.mjs';

test('statement percent counts covered blocks over the profile', () => {
  const profile = [
    'mode: set',
    'file.go:1.1,2.1 2 1',
    'file.go:3.1,4.1 2 0',
  ].join('\n');
  assert.equal(statementPercent(profile), 50);
});

test('a rise or a tiny drop stays inside the tolerance', () => {
  assert.equal(regressed(50.1, 50), false);
  assert.equal(regressed(49.95, 50), false);
});

test('a real drop fails the gate', () => {
  assert.equal(regressed(49.8, 50), true);
});
