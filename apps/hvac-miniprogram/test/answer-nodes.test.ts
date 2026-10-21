import assert from 'node:assert/strict';
import test from 'node:test';
import { answerNodes } from '../miniprogram/lib/answer-nodes.ts';

const text = (value: string) => ({ type: 'text', text: value });

test('an answer reads as the assistant wrote it: the bold conclusion, paragraphs and a small table', () => {
  const answer = [
    '**10 月 15 日比 10 月 8 日多用电 262 kWh，主要是负荷变大了。**',
    '',
    '两天的能效差不多，多出来的电基本都换成了冷量。',
    '',
    '| 日期 | 用电（kWh） | 供冷（kWh） |',
    '| --- | ---: | ---: |',
    '| 10 月 8 日 | 1,372 | 6,120 |',
    '| 10 月 15 日 | 1,634 | 7,402 |',
  ].join('\n');
  assert.deepEqual(answerNodes(answer), [
    { name: 'p', attrs: { class: 'answer-p' }, children: [{ name: 'strong', children: [text('10 月 15 日比 10 月 8 日多用电 262 kWh，主要是负荷变大了。')] }] },
    { name: 'p', attrs: { class: 'answer-p' }, children: [text('两天的能效差不多，多出来的电基本都换成了冷量。')] },
    { name: 'table', attrs: { class: 'answer-table' }, children: [
      { name: 'tr', children: ['日期', '用电（kWh）', '供冷（kWh）'].map((cell) => ({ name: 'th', children: [text(cell)] })) },
      { name: 'tr', children: ['10 月 8 日', '1,372', '6,120'].map((cell) => ({ name: 'td', children: [text(cell)] })) },
      { name: 'tr', children: ['10 月 15 日', '1,634', '7,402'].map((cell) => ({ name: 'td', children: [text(cell)] })) },
    ] },
  ]);
});

test('a bold conclusion written straight into the next sentence still shows as bold, and lists keep their items', () => {
  assert.deepEqual(answerNodes('**多用电 18 kWh。**主要来自 2 号机组：\n- 2 号机组多开了 **3** 小时\n- 1 号机组持平\n\n1. 先看负荷\n2. 再看能效'), [
    { name: 'p', attrs: { class: 'answer-p' }, children: [{ name: 'strong', children: [text('多用电 18 kWh。')] }] },
    { name: 'p', attrs: { class: 'answer-p' }, children: [text('主要来自 2 号机组：')] },
    { name: 'ul', attrs: { class: 'answer-list' }, children: [
      { name: 'li', children: [text('2 号机组多开了 '), { name: 'strong', children: [text('3')] }, text(' 小时')] },
      { name: 'li', children: [text('1 号机组持平')] },
    ] },
    { name: 'ol', attrs: { class: 'answer-list' }, children: [
      { name: 'li', children: [text('先看负荷')] },
      { name: 'li', children: [text('再看能效')] },
    ] },
  ]);
});

test('text that looks like markup stays text', () => {
  assert.deepEqual(answerNodes('<img src=x onerror=alert(1)> 用电 `18` kWh'), [
    { name: 'p', attrs: { class: 'answer-p' }, children: [text('<img src=x onerror=alert(1)> 用电 18 kWh')] },
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
  const [, table] = answerNodes(answer);
  assert.deepEqual(table, { name: 'table', attrs: { class: 'answer-table' }, children: [
    { name: 'tr', children: ['日期', '用电量', '估算电费'].map((cell) => ({ name: 'th', children: [text(cell)] })) },
    { name: 'tr', children: ['10 月 20 日（全天）', '1691 kWh', '1423.48 元'].map((cell) => ({ name: 'td', children: [text(cell)] })) },
    { name: 'tr', children: ['10 月 21 日（到 10:41）', '403 kWh', '230.09 元'].map((cell) => ({ name: 'td', children: [text(cell)] })) },
  ] });
  // A divider of single dashes is still a divider (GitHub tables need only one).
  const [divided] = answerNodes(['| a | b |', '|-|-|', '| 1 | 2 |'].join('\n'));
  assert.equal('children' in divided ? divided.children.length : 0, 2);
  assert.equal(answerNodes('|').length, 1);
});
