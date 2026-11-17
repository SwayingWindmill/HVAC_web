import assert from 'node:assert/strict';
import test from 'node:test';
import { assistantThreadFrom } from '../apps/hvac-web/src/features/assistant/thread-model.ts';
import {
  normalizeAssistantMarkdown,
  withBoldLeadOnItsOwnLine,
  withMissingTableDividers,
} from '../apps/hvac-web/src/features/assistant/answer-markdown.ts';

const session = (status, activeRunId, revision) => ({
  id: 'session-1', tenantId: 'tenant-1', siteId: 'site-1', agentDefinitionId: 'operations-investigation.v1',
  createdBy: 'principal-1', revision, createdAt: 1_000, updatedAt: 1_000, status, activeRunId,
});
const run = (id, status, startedAt) => ({
  id, sessionId: 'session-1', modelRef: { provider: 'deepseek', model: 'deepseek-flash' }, status, startedAt,
  finishedAt: status === 'RUNNING' ? null : startedAt + 9_000,
  usage: { inputTokens: 0, outputTokens: 0, modelCalls: 0, toolCalls: 0 }, failureCode: null,
});
const message = (id, role, content, createdAt, runId = null) => ({ id, sessionId: 'session-1', runId, role, content, createdAt });
const tool = (id, runId, toolName, status, startedAt) => ({
  id, sessionId: 'session-1', runId, toolName, argumentsDigest: 'sha256:x', status, startedAt,
  finishedAt: status === 'RUNNING' ? null : startedAt + 500, resultSummary: status === 'COMPLETED' ? 'Tool completed.' : null,
  provenance: [], failureCode: status === 'FAILED' ? 'TOOL_OWNER_UNAVAILABLE' : null,
});
const finding = {
  outcome: 'SUPPORTED_FINDING',
  summary: '多用的电主要来自负荷增加。',
  keyFigures: [{ label: '多用电', value: '+762', unit: 'kWh' }],
  evidenceRefs: [{ owner: 'ENERGY', resourceType: 'period-comparison', resourceId: 'power', toolExecutionId: 'tool-1' }],
  limitations: ['没有室外温度数据。'],
  recommendedNext: ['B 机组那天为什么开机？', '晚市要不要再开一台？'],
};
const event = (type, sequence, payload, runId = 'run-1') => ({
  version: 'hvac.agent.event/v1', type, sessionId: 'session-1', runId, sequence, at: 2_000 + sequence, payload,
});

test('a finished run reads as the question, the steps it took, its final answer and the finding, with follow-ups offered', () => {
  const snapshot = {
    session: session('COMPLETED', null, 4),
    runs: [run('run-1', 'COMPLETED', 1_100)],
    messages: [
      message('m-1', 'OPERATOR', '10/15 为什么比上周同日多用电？', 1_050),
      message('m-0', 'ASSISTANT', '先读站点信息。', 1_150, 'run-1'),
      message('m-2', 'ASSISTANT', '10/15 多用电 762 kWh。', 9_000, 'run-1'),
    ],
    toolExecutions: [tool('tool-1', 'run-1', 'energy_compare_periods', 'COMPLETED', 1_200), tool('tool-2', 'run-1', 'energy_by_equipment', 'FAILED', 1_300), tool('tool-3', 'run-1', 'investigation_complete', 'COMPLETED', 8_900)],
    artifacts: [{ id: 'a-1', sessionId: 'session-1', runId: 'run-1', createdAt: 9_100, kind: 'FINDING', finding }],
  };

  const thread = assistantThreadFrom(snapshot, []);

  assert.equal(thread.isRunning, false);
  assert.equal(thread.revision, 4);
  assert.deepEqual(thread.messages, [
    { id: 'm-1', role: 'user', content: [{ type: 'text', text: '10/15 为什么比上周同日多用电？' }] },
    {
      id: 'run-1',
      role: 'assistant',
      status: { type: 'complete', reason: 'stop' },
      metadata: { runId: 'run-1' },
      content: [
        { type: 'tool-call', toolCallId: 'tool-1', toolName: 'energy_compare_periods', args: {}, result: 'Tool completed.', isError: false },
        { type: 'tool-call', toolCallId: 'tool-2', toolName: 'energy_by_equipment', args: {}, result: '没有读到数据', isError: true },
        { type: 'text', text: '10/15 多用电 762 kWh。' },
        { type: 'data-finding', data: { finding, evidenceTools: ['energy_compare_periods'] } },
      ],
    },
  ]);
  assert.deepEqual(thread.suggestions, [{ prompt: 'B 机组那天为什么开机？' }, { prompt: '晚市要不要再开一台？' }]);
  assert.equal(thread.pendingInput, null);
});

test('a live run streams the text of its latest turn and shows the step in progress until the run completes', () => {
  const snapshot = {
    session: session('ACTIVE', 'run-1', 2),
    runs: [run('run-1', 'RUNNING', 1_100)],
    messages: [message('m-1', 'OPERATOR', '昨天用电正常吗？', 1_050)],
    toolExecutions: [],
    artifacts: [],
  };
  const live = [
    event('session.snapshot', 0, { snapshot }),
    event('tool.started', 1, { toolExecutionId: 'tool-1', toolName: 'energy_query_series' }),
    event('assistant.delta', 2, { messageId: 'm-1', delta: '先查一下。' }),
    event('assistant.delta', 2, { messageId: 'm-2', delta: '昨天用电' }),
    event('assistant.delta', 3, { messageId: 'm-2', delta: '偏高。' }),
  ];

  const running = assistantThreadFrom(snapshot, live);

  assert.equal(running.isRunning, true);
  assert.equal(running.activeRunId, 'run-1');
  assert.deepEqual(running.messages[1], {
    id: 'run-1',
    role: 'assistant',
    status: { type: 'running' },
    metadata: { runId: 'run-1' },
    content: [
      { type: 'tool-call', toolCallId: 'tool-1', toolName: 'energy_query_series', args: {} },
      { type: 'text', text: '昨天用电偏高。' },
    ],
  });
  assert.deepEqual(running.suggestions, []);

  const done = assistantThreadFrom(snapshot, [
    ...live,
    event('tool.completed', 4, { toolExecution: tool('tool-1', 'run-1', 'energy_query_series', 'COMPLETED', 1_200) }),
    event('run.completed', 5, { run: run('run-1', 'COMPLETED', 1_100) }),
  ]);
  assert.equal(done.isRunning, false);
  assert.equal(done.messages[1].status.type, 'complete');
  assert.equal(done.messages[1].content[0].result, 'Tool completed.');
});

test('a question from the assistant waits for an answer, and the answer reads as the operator turn', () => {
  const request = {
    id: 'a-1', sessionId: 'session-1', runId: 'run-1', createdAt: 5_000, kind: 'INPUT_REQUEST',
    request: { prompt: '按哪种排班比较？', response: { kind: 'SINGLE_SELECT', choices: [{ value: 'weekday', label: '工作日排班' }, { value: 'weekend', label: '周末排班' }] } },
  };
  const waiting = {
    session: session('WAITING_FOR_INPUT', null, 3),
    runs: [run('run-1', 'COMPLETED', 1_100)],
    messages: [message('m-1', 'OPERATOR', '夜里为什么还在用电？', 1_050)],
    toolExecutions: [],
    artifacts: [request],
  };

  const asked = assistantThreadFrom(waiting, []);
  assert.deepEqual(asked.pendingInput, request);
  assert.deepEqual(asked.messages[1].content, [{ type: 'data-input-request', data: request.request }]);

  const answered = assistantThreadFrom({
    ...waiting,
    session: session('ACTIVE', 'run-2', 5),
    runs: [...waiting.runs, run('run-2', 'RUNNING', 7_000)],
    artifacts: [request, { id: 'a-2', sessionId: 'session-1', runId: 'run-1', createdAt: 6_900, kind: 'INPUT_RESPONSE', requestArtifactId: 'a-1', value: 'weekday', submittedBy: 'principal-1' }],
  }, []);
  assert.equal(answered.pendingInput, null);
  assert.deepEqual(answered.messages.map(({ role, content }) => [role, content[0]?.type === 'text' ? content[0].text : content[0]?.type]), [
    ['user', '夜里为什么还在用电？'],
    ['assistant', 'data-input-request'],
    ['user', '工作日排班'],
    ['assistant', undefined],
  ]);
});

// #607: words streamed before the Run reads data are process talk ("I'll start by reading…"); once
// the read starts they are put away, and only the answer that follows is shown.
test('process talk streamed before a read is put away when the read starts', () => {
  const snapshot = {
    session: session('ACTIVE', 'run-1', 2),
    runs: [run('run-1', 'RUNNING', 1_100)],
    messages: [message('m-0', 'OPERATOR', '昨天用电正常吗？', 1_050)],
    toolExecutions: [],
    artifacts: [],
  };
  const talk = [
    event('session.snapshot', 0, { snapshot }),
    event('assistant.delta', 1, { messageId: 'm-1', delta: "I'll start by reading the site." }),
    event('tool.started', 2, { toolExecutionId: 'tool-1', toolName: 'site_get_context' }),
  ];
  const reading = assistantThreadFrom(snapshot, talk);
  assert.deepEqual(reading.messages[1].content, [{ type: 'tool-call', toolCallId: 'tool-1', toolName: 'site_get_context', args: {} }]);

  const answering = assistantThreadFrom(snapshot, [...talk, event('assistant.delta', 3, { messageId: 'm-2', delta: '昨天用电偏高。' })]);
  assert.deepEqual(answering.messages[1].content.at(-1), { type: 'text', text: '昨天用电偏高。' });
});

test('the bold conclusion an answer opens with renders bold even when text follows it on the same line', () => {
  // CommonMark leaves 「**…。**」 open when a character follows it directly, and the operator sees the stars (#663).
  assert.equal(withBoldLeadOnItsOwnLine('**16 点能效最低，只有 3.75。**此外，7 点也偏低。'), '**16 点能效最低，只有 3.75。**\n\n此外，7 点也偏低。');
  assert.equal(withBoldLeadOnItsOwnLine('**16 点能效最低，只有 3.75。** 此外，7 点也偏低。'), '**16 点能效最低，只有 3.75。**\n\n此外，7 点也偏低。');
  for (const fine of ['**16 点能效最低，只有 3.75。**\n\n此外。', '**16 点能效最低。**', '16 点能效 **3.75**，最低。', '**16 点']) {
    assert.equal(withBoldLeadOnItsOwnLine(fine), fine);
  }
});

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
