import assert from 'node:assert/strict';
import test from 'node:test';
import { assistantThreadFrom } from '../apps/hvac-web/src/features/assistant/thread-model.ts';

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
