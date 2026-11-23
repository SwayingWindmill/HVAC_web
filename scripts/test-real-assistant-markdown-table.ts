import assert from 'node:assert/strict';
import test from 'node:test';

import {
  normalizeAssistantMarkdown,
  withMissingTableDividers,
} from '../apps/hvac-web/src/features/assistant/answer-markdown.ts';
import { deduplicateFindingKeyFigures } from '../apps/hvac-web/src/features/assistant/finding-dedup.ts';

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

test('deduplicateFindingKeyFigures suppresses values repeated in markdown tables or matching charts', () => {
  const tableAnswer = [
    '**今天（10 月 21 日）到 10:41 为止的电费是 230.09 元。**',
    '',
    '| 日期 | 用电量 | 估算电费 |',
    '| 10 月 20 日（全天） | 1691 kWh | 1423.48 元 |',
    '| 10 月 21 日（到 10:41） | 403 kWh | 230.09 元 |',
  ].join('\n');

  // 1. Table repeats all keyFigures -> all suppressed, compact card mode enabled.
  const fullTableDedup = deduplicateFindingKeyFigures({
    answerText: tableAnswer,
    finding: {
      keyFigures: [
        { label: '今日电费', value: '230.09', unit: '元' },
        { label: '昨日全天', value: '1423.48', unit: '元' },
      ],
    },
  });
  assert.deepEqual(fullTableDedup, { visibleKeyFigures: [], suppressedCount: 2, compact: true });

  // 2. Table with an extra non-tabulated figure keeps only the non-duplicated one.
  const partialTableDedup = deduplicateFindingKeyFigures({
    answerText: tableAnswer,
    finding: {
      keyFigures: [
        { label: '今日电费', value: '230.09', unit: '元' },
        { label: '昨日全天', value: '1423.48', unit: '元' },
        { label: '平均 COP', value: '4.25' },
      ],
    },
  });
  assert.deepEqual(partialTableDedup, {
    visibleKeyFigures: [{ label: '平均 COP', value: '4.25' }],
    suppressedCount: 2,
    compact: false,
  });

  // 3. Prose + chart keeps signed delta and cross-unit metrics intact (DEFAULT_REPLY shape).
  const proseWithChart = deduplicateFindingKeyFigures({
    answerText: '这一小时比前一天同一时段多用电 18 kWh，主要来自 2 号机组。',
    finding: {
      chart: { unit: 'kWh', series: [{ values: [229, 247] }] },
      keyFigures: [
        { label: '多用电', value: '+18', unit: 'kWh' },
        { label: '综合能效', value: '3.5' },
      ],
    },
  });
  assert.deepEqual(proseWithChart, {
    visibleKeyFigures: [
      { label: '多用电', value: '+18', unit: 'kWh' },
      { label: '综合能效', value: '3.5' },
    ],
    suppressedCount: 0,
    compact: false,
  });

  // 4. Chart answer with non-delta keyFigures matching plotted values & unit suppresses duplicates only.
  const chartDuplicate = deduplicateFindingKeyFigures({
    answerText: '1 号冷机今天用电显著高于 2 号冷机。',
    finding: {
      chart: { unit: 'kWh', series: [{ values: [812, 420] }] },
      keyFigures: [
        { label: '1 号冷机', value: '812', unit: 'kWh' },
        { label: '2 号冷机', value: '420', unit: 'kWh' },
        { label: '平均 COP', value: '4.12' },
      ],
    },
  });
  assert.deepEqual(chartDuplicate, {
    visibleKeyFigures: [{ label: '平均 COP', value: '4.12' }],
    suppressedCount: 2,
    compact: false,
  });

  // 5. Date/time digits inside table cells (e.g. 10月20日, 10:41) never suppress independent KPI figures.
  const dateClockTable = [
    '| 时段 | 负荷 |',
    '| --- | --- |',
    '| 10 月 20 日 10:41 | 310 kW |',
  ].join('\n');
  const dateSafe = deduplicateFindingKeyFigures({
    answerText: dateClockTable,
    finding: {
      keyFigures: [
        { label: '参考负荷', value: '20', unit: 'kW' },
        { label: '采样点', value: '10' },
      ],
    },
  });
  assert.equal(dateSafe.suppressedCount, 0);
  assert.equal(dateSafe.visibleKeyFigures.length, 2);
});
