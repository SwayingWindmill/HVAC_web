import assert from 'node:assert/strict';
import test from 'node:test';
import { changeBadge, grouped } from '../miniprogram/lib/format.ts';

test('numbers read the way the web shows them, without relying on the phone locale', () => {
  assert.equal(grouped(1691), '1,691');
  assert.equal(grouped(31280.4), '31,280');
  assert.equal(grouped(4.526, 2), '4.53');
  assert.equal(grouped(-1234567.891, 1), '-1,234,567.9');
  assert.equal(grouped(null), '—');
});

test('a change badge is coloured by good or bad, not by up or down', () => {
  assert.deepEqual(changeBadge(0.156, 'less-is-better'), { text: '▲ 15.6%', tone: 'bad' });
  assert.deepEqual(changeBadge(-0.042, 'less-is-better'), { text: '▼ 4.2%', tone: 'good' });
  assert.deepEqual(changeBadge(-0.016, 'more-is-better'), { text: '▼ 1.6%', tone: 'bad' });
  assert.deepEqual(changeBadge(0.137, 'neutral'), { text: '▲ 13.7%', tone: 'neutral' });
  assert.deepEqual(changeBadge(0, 'less-is-better'), { text: '持平', tone: 'neutral' });
  assert.equal(changeBadge(null, 'neutral'), null);
});
