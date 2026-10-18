import assert from 'node:assert/strict';
import test from 'node:test';

import {
  AgentContractError,
  parseInvestigationComplete,
  parseInvestigationRequestInput,
  transitionAgentSession,
  claimsPhysicalExecution,
  needsExecutionCorrection,
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

test('Agent Session follows the first-product lifecycle and an ended Run lets the next question start one', () => {
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

  const followed = transitionAgentSession(completed, {
    status: 'ACTIVE',
    activeRunId: 'run-003',
    at: 1_600,
  });
  assert.equal(followed.activeRunId, 'run-003');
  assert.throws(
    () => transitionAgentSession(completed, { status: 'WAITING_FOR_INPUT', at: 1_600 }),
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
    '已关闭 B 机组。',
    '已调整设定值为 8 ℃。',
    '已为您停止 D 机组。',
    '我把 B 机组关了。',
    'B 机组我已经帮你关了。',
    '已为你把 B 机组停了。',
    '已帮您关机。',
    '好的，已经给你关闭 B 机组。',
    '已按你的要求关闭 B 机组。',
    'I changed the chilled-water setpoint to 8 °C.',
    "I've turned off chiller B.",
    'Chiller B has been switched off as you requested.',
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
    // The plant's own control system and the operators act; the assistant only reports it.
    '群控系统已停止 C 机组。',
    '系统已将 C 机组切换到节能模式。',
    'BMS 系统已将冷冻水出水温度调高到 9 度。',
    '冷冻水系统已启动。',
    '运行人员已把 B 机组关闭。',
    '已启动 2 台水泵，负荷 60%。',
    '从日志看，已关闭 B 机组的电动阀。',
    '我们看到 B 机组已关闭。',
    '我看到 C 机组已停机。',
    // Declines that repeat the request.
    '你让我把 B 机组关掉，但我只能读取数据。',
    '你想让我帮你关闭 B 机组，这个我做不到。',
    '我已经开始读取数据。',
    'I set out the figures below.',
    'The system stopped chiller B at 03:00 because of low load.',
    'We changed nothing; the BMS stopped chiller B.',
  ];
  for (const text of claims) assert.equal(claimsPhysicalExecution(text), true, text);
  for (const text of descriptions) assert.equal(claimsPhysicalExecution(text), false, text);
});

// The answer has already reached the operator, so a claim is answered by a later correction (#592).
test('a claim of operating equipment needs a correction after it, in the same message or a later one', () => {
  assert.equal(needsExecutionCorrection(['好的，已为你关闭 B 机组。']), true);
  assert.equal(needsExecutionCorrection(['好的，已为你关闭 B 机组。', '更正：我只能读取数据，没有也不能关闭 B 机组。']), false);
  assert.equal(needsExecutionCorrection(['好的，已为你关闭 B 机组。', '更正:我没有也不能关闭 B 机组。']), false);
  assert.equal(needsExecutionCorrection(['已为你关闭 B 机组。更正：我没有也不能关闭 B 机组。']), false);
  assert.equal(needsExecutionCorrection(['已为你关闭 B 机组。', '更正：我不能关闭机组。', '我已把设定值调到 8 度。']), true);
  assert.equal(needsExecutionCorrection(['10 月 15 日用电 1634 kWh。']), false);
});
