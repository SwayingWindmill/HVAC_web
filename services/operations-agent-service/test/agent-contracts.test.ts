import assert from 'node:assert/strict';
import test from 'node:test';

import {
  AgentContractError,
  parseInvestigationComplete,
  parseInvestigationRequestInput,
  transitionAgentSession,
  claimsPhysicalExecution,
} from '../dist/agent/index.js';

const activeSession = Object.freeze({
  id: 'session-001',
  tenantId: 'tenant-001',
  siteId: 'site-001',
  agentDefinitionId: 'operations-investigation.v1',
  createdBy: 'principal-001',
  status: 'ACTIVE',
  revision: 3,
  activeRunId: 'run-001',
  createdAt: 1_000,
  updatedAt: 1_200,
});

test('Agent Session follows the first-product lifecycle without reopening terminal states', () => {
  const waiting = transitionAgentSession(activeSession, {
    status: 'WAITING_FOR_INPUT',
    at: 1_300,
  });
  const resumed = transitionAgentSession(waiting, {
    status: 'ACTIVE',
    activeRunId: 'run-002',
    at: 1_400,
  });
  const completed = transitionAgentSession(resumed, {
    status: 'COMPLETED',
    at: 1_500,
  });

  assert.deepEqual(
    [waiting.status, resumed.status, completed.status],
    ['WAITING_FOR_INPUT', 'ACTIVE', 'COMPLETED'],
  );
  assert.equal(waiting.activeRunId, null);
  assert.equal(resumed.activeRunId, 'run-002');
  assert.equal(completed.activeRunId, null);
  assert.deepEqual(
    [waiting.revision, resumed.revision, completed.revision],
    [4, 5, 6],
  );

  assert.throws(
    () => transitionAgentSession(completed, {
      status: 'ACTIVE',
      activeRunId: 'run-003',
      at: 1_600,
    }),
    (error) => error instanceof AgentContractError && error.code === 'SESSION_TRANSITION_INVALID',
  );
});

test('terminal investigation inputs are structured and reject malformed model output', () => {
  const completion = parseInvestigationComplete({
    outcome: 'SUPPORTED_FINDING',
    evidenceRefs: [
      {
        owner: 'ENERGY',
        resourceType: 'period-comparison',
        resourceId: 'comparison-2026-09-02',
        revision: 'rev-7',
        toolExecutionId: 'tool-exec-004',
      },
    ],
    limitations: ['Chiller staging telemetry was unavailable for part of the window.'],
    recommendedNext: ['Inspect the overnight plant schedule and staging sequence.'],
  });
  const unable = parseInvestigationComplete({
    outcome: 'UNABLE_TO_CONCLUDE',
    evidenceRefs: [],
    limitations: ['The source contains a two-hour gap.'],
    recommendedNext: ['Restore the missing interval before drawing a conclusion.'],
  });
  const inputRequest = parseInvestigationRequestInput({
    prompt: 'Which operating schedule should be treated as the expected overnight baseline?',
    response: {
      kind: 'SINGLE_SELECT',
      choices: [
        { value: 'weekday', label: 'Weekday schedule' },
        { value: 'holiday', label: 'Holiday schedule' },
      ],
    },
  });

  assert.equal(completion.outcome, 'SUPPORTED_FINDING');
  assert.equal(unable.outcome, 'UNABLE_TO_CONCLUDE');
  assert.equal(inputRequest.response.kind, 'SINGLE_SELECT');

  // The conclusion is the answer text; the card does not say it again.
  assert.throws(
    () => parseInvestigationComplete({
      outcome: 'UNABLE_TO_CONCLUDE',
      summary: '数据不完整。',
      evidenceRefs: [],
      limitations: [],
      recommendedNext: [],
    }),
    (error) => error instanceof AgentContractError && error.code === 'TERMINAL_ARTIFACT_INVALID',
  );
  assert.throws(
    () => parseInvestigationComplete({
      outcome: 'SUPPORTED_FINDING',
      evidenceRefs: [],
      limitations: [],
      recommendedNext: [],
    }),
    (error) => error instanceof AgentContractError && error.code === 'TERMINAL_ARTIFACT_INVALID',
  );
});

// #592: the assistant may never say it operated equipment, in Chinese or English; describing what a
// machine did stays allowed.
test('claims of having operated equipment are recognised in Chinese and English; descriptions of equipment are not', () => {
  const claims = [
    '好的，已为你关闭 B 机组。',
    '我已把冷冻水出水温度设定值调到 8 度。',
    '我刚帮你启动了 2 号冷冻水泵。',
    '系统已将 C 机组切换到节能模式。',
    '已关闭 B 机组。',
    '已调整设定值为 8 ℃。',
    '已为您停止 D 机组。',
    'I changed the chilled-water setpoint to 8 °C.',
    'The system has stopped chiller B.',
  ];
  const descriptions = [
    'B 机组当天开机。',
    'C 机组已停机。',
    '已关闭的机组没有用电。',
    'B 机组已关闭两天，用电为 0。',
    '冷却塔风机已关闭，所以没有用电。',
    '我只能读取数据，不能关闭 B 机组。',
    '我无法帮你调整设定值。',
    '我没有关闭任何设备。',
    '已为你查询 B 机组的设定值。',
    '需要现场人员按规程关闭 B 机组。',
    '更正：我只能读取数据，没有也不能关闭 B 机组。',
  ];
  for (const text of claims) assert.equal(claimsPhysicalExecution(text), true, text);
  for (const text of descriptions) assert.equal(claimsPhysicalExecution(text), false, text);
});
