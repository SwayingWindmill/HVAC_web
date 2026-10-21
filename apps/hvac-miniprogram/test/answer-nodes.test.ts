import assert from 'node:assert/strict';
import test from 'node:test';
import { ANSWER_STYLES, answerBlocks } from '../miniprogram/lib/answer-nodes.ts';

const text = (value: string) => ({ type: 'text', text: value });
const paragraph = (...children: unknown[]) => ({ name: 'p', attrs: { style: ANSWER_STYLES.paragraph }, children });
/** A table cell: its words, whether its column holds figures (read right), and whether it is long enough to wrap. */
const cell = (value: string, align: 'left' | 'right' = 'left', wraps = false) => ({ nodes: [text(value)], align, wraps });

test('an answer reads as the assistant wrote it: the bold conclusion and paragraphs as text, the table as a table of its own', () => {
  const answer = [
    '**10 月 15 日比 10 月 8 日多用电 262 kWh，主要是负荷变大了。**',
    '',
    '两天的能效差不多，多出来的电基本都换成了冷量。',
    '',
    '| 日期 | 用电（kWh） | 供冷（kWh） |',
    '| --- | ---: | ---: |',
    '| 10 月 8 日 | 1,372 | 6,120 |',
    '| 10 月 15 日 | 1,634 | 7,402 |',
    '',
    '多出来的电集中在 14–18 点。',
  ].join('\n');
  assert.deepEqual(answerBlocks(answer), [
    { kind: 'text', nodes: [
      paragraph({ name: 'strong', children: [text('10 月 15 日比 10 月 8 日多用电 262 kWh，主要是负荷变大了。')] }),
      paragraph(text('两天的能效差不多，多出来的电基本都换成了冷量。')),
    ] },
    { kind: 'table',
      head: [cell('日期'), cell('用电（kWh）', 'right'), cell('供冷（kWh）', 'right')],
      rows: [
        [cell('10 月 8 日'), cell('1,372', 'right'), cell('6,120', 'right')],
        [cell('10 月 15 日'), cell('1,634', 'right'), cell('7,402', 'right')],
      ] },
    { kind: 'text', nodes: [paragraph(text('多出来的电集中在 14–18 点。'))] },
  ]);
});

test('a column of words reads left and a long description wraps, so a narrow phone keeps every cell readable', () => {
  // As the assistant answered on 2026-10-21: the first and second columns are not all figures.
  const [table] = answerBlocks([
    '| 读数 | 现在（10 月 21 日 11:45） | 10 月 18–21 日的历史 |',
    '|---|---|---|',
    '| 室外温度 | 20.3 °C | 一直没变化，每天 24 小时都是 20.3 °C |',
    '| 室外温度（通道 14） | 没有读数 | 这段时间完全没有数据 |',
  ].join('\n'));
  assert.deepEqual(table.kind === 'table' && table.rows, [
    [cell('室外温度'), cell('20.3 °C'), cell('一直没变化，每天 24 小时都是 20.3 °C', 'left', true)],
    [cell('室外温度（通道 14）'), cell('没有读数'), cell('这段时间完全没有数据')],
  ]);
});

test('a bold conclusion written straight into the next sentence still shows as bold, and lists keep their items', () => {
  assert.deepEqual(answerBlocks('**多用电 18 kWh。**主要来自 2 号机组：\n- 2 号机组多开了 **3** 小时\n- 1 号机组持平\n\n1. 先看负荷\n2. 再看能效'), [{ kind: 'text', nodes: [
    paragraph({ name: 'strong', children: [text('多用电 18 kWh。')] }),
    paragraph(text('主要来自 2 号机组：')),
    { name: 'ul', attrs: { style: ANSWER_STYLES.list }, children: [
      { name: 'li', children: [text('2 号机组多开了 '), { name: 'strong', children: [text('3')] }, text(' 小时')] },
      { name: 'li', children: [text('1 号机组持平')] },
    ] },
    { name: 'ol', attrs: { style: ANSWER_STYLES.list }, children: [
      { name: 'li', children: [text('先看负荷')] },
      { name: 'li', children: [text('再看能效')] },
    ] },
  ] }]);
});

test('text that looks like markup stays text', () => {
  assert.deepEqual(answerBlocks('<img src=x onerror=alert(1)> 用电 `18` kWh'), [
    { kind: 'text', nodes: [paragraph(text('<img src=x onerror=alert(1)> 用电 18 kWh'))] },
  ]);
});

// Regression (#664): the assistant wrote a table without its divider row on 2026-10-21, and the
// renderer looped forever on its first row, freezing and then crashing the mini program.
test('a table the assistant wrote without its divider row still reads as a table, and every line moves the reading on', { timeout: 2000 }, () => {
  const answer = [
    '**今天（10 月 21 日）到 10:41 为止的电费是 230.09 元，比昨天全天的 1423.48 元少，不过今天还没过完，不能直接和昨天比。**',
    '',
    '| 日期 | 用电量 | 估算电费 |',
    '| 10 月 20 日（全天） | 1691 kWh | 1423.48 元 |',
    '| 10 月 21 日（到 10:41） | 403 kWh | 230.09 元 |',
  ].join('\n');
  const [, table] = answerBlocks(answer);
  assert.deepEqual(table, {
    kind: 'table',
    head: [cell('日期'), cell('用电量', 'right'), cell('估算电费', 'right')],
    rows: [
      [cell('10 月 20 日（全天）'), cell('1691 kWh', 'right'), cell('1423.48 元', 'right')],
      [cell('10 月 21 日（到 10:41）'), cell('403 kWh', 'right'), cell('230.09 元', 'right')],
    ],
  });
  // A divider of single dashes is still a divider (GitHub tables need only one).
  const [divided] = answerBlocks(['| a | b |', '|-|-|', '| 1 | 2 |'].join('\n'));
  assert.equal(divided.kind === 'table' && divided.rows.length, 1);
  assert.equal(answerBlocks('|').length, 1);
});
