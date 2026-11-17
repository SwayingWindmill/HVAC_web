import assert from 'node:assert/strict';
import test from 'node:test';

import {
  normalizeAssistantMarkdown,
  withMissingTableDividers,
} from '../apps/hvac-web/src/features/assistant/answer-markdown.ts';

test('a pipe table written without its GFM divider row gets a matching divider inserted (#669)', () => {
  const raw = [
    '**今天（10 月 21 日）到 10:41 为止的电费是 230.09 元，比昨天全天的 1423.48 元少，不过今天还没过完，不能直接和昨天比。**',
    '',
    '| 日期 | 用电量 | 估算电费 |',
    '| 10 月 20 日（全天） | 1691 kWh | 1423.48 元 |',
    '| 10 月 21 日（到 10:41） | 403 kWh | 230.09 元 |',
  ].join('\n');

  const expected = [
    '**今天（10 月 21 日）到 10:41 为止的电费是 230.09 元，比昨天全天的 1423.48 元少，不过今天还没过完，不能直接和昨天比。**',
    '',
    '| 日期 | 用电量 | 估算电费 |',
    '| --- | --- | --- |',
    '| 10 月 20 日（全天） | 1691 kWh | 1423.48 元 |',
    '| 10 月 21 日（到 10:41） | 403 kWh | 230.09 元 |',
  ].join('\n');

  assert.equal(withMissingTableDividers(raw), expected);
  assert.equal(normalizeAssistantMarkdown(raw), expected);
});

test('valid GFM tables with alignment colons are preserved unchanged', () => {
  const valid = [
    '**近两天对比如下。**',
    '',
    '| 日期 | 用电量 | 估算电费 | 备注 |',
    '| :--- | ---: | :---: | --- |',
    '| 10 月 20 日 | 1,691 kWh | 1,423.48 元 | 全天 |',
    '| 10 月 21 日 | 403 kWh | 230.09 元 | 截至 10:41 |',
  ].join('\n');

  assert.equal(withMissingTableDividers(valid), valid);
  assert.equal(normalizeAssistantMarkdown(valid), valid);
});

test('pipe rows inside backtick and tilde code fences are never modified', () => {
  const fenced = [
    '```markdown',
    '| 日期 | 用电量 |',
    '| 10 月 20 日 | 1691 kWh |',
    '```',
    '',
    '~~~',
    '| 设备 | 功率 |',
    '| 1 号冷机 | 210 kW |',
    '~~~',
    '',
    '````markdown',
    '```',
    '| 嵌套 | 示例 |',
    '| a | b |',
    '```',
    '````',
  ].join('\n');

  assert.equal(withMissingTableDividers(fenced), fenced);
});

test('in-flight streaming delimiter rows do not receive a duplicate divider', () => {
  const partialDash = '| 日期 | 电费 |\n| --';
  assert.equal(withMissingTableDividers(partialDash), partialDash);

  const partialColon = '| 日期 | 电费 |\n| :';
  assert.equal(withMissingTableDividers(partialColon), partialColon);

  const partialFirstCell = '| 日期 | 电费 |\n| --- |';
  assert.equal(withMissingTableDividers(partialFirstCell), partialFirstCell);

  const streamingBodyRow = '| 日期 | 电费 |\n| 10 月 20 日';
  assert.equal(withMissingTableDividers(streamingBodyRow), '| 日期 | 电费 |\n| --- | --- |\n| 10 月 20 日');
});

test('multiple tables, escaped pipes, and tables immediately after prose normalize cleanly', () => {
  const multi = [
    '用电对比如下：',
    '| 指标 \\| 说明 | 昨天 | 今天 |',
    '| 电费 \\| 估算 | 1423.48 元 | 230.09 元 |',
    '分设备如下：',
    '| 设备 | 电量 |',
    '| :--- | ---: |',
    '| 1 号冷机 | 800 kWh |',
  ].join('\n');

  assert.equal(withMissingTableDividers(multi), [
    '用电对比如下：',
    '',
    '| 指标 \\| 说明 | 昨天 | 今天 |',
    '| --- | --- | --- |',
    '| 电费 \\| 估算 | 1423.48 元 | 230.09 元 |',
    '分设备如下：',
    '',
    '| 设备 | 电量 |',
    '| :--- | ---: |',
    '| 1 号冷机 | 800 kWh |',
  ].join('\n'));

  // An escaped backslash before a real separator still splits two columns.
  assert.equal(
    withMissingTableDividers('| 路径\\\\ | 状态 |\n| C:\\\\ | 正常 |'),
    '| 路径\\\\ | 状态 |\n| --- | --- |\n| C:\\\\ | 正常 |',
  );

  // Single-column pipe lines are not multi-column tables.
  const singleCol = '| 只有一列 |\n| 数据行 |';
  assert.equal(withMissingTableDividers(singleCol), singleCol);

  // Bold lead followed immediately by a table separates both the paragraph and the table header.
  assert.equal(
    normalizeAssistantMarkdown('**电费已核对。**| 日期 | 电费 |\n| 10 月 20 日 | 1423.48 元 |'),
    '**电费已核对。**\n\n| 日期 | 电费 |\n| --- | --- |\n| 10 月 20 日 | 1423.48 元 |',
  );
});
