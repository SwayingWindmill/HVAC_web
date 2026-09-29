import assert from 'node:assert/strict';
import test from 'node:test';

import {
  AgentContractError,
  parseInvestigationComplete,
  parseInvestigationRequestInput,
  transitionAgentSession,
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
    summary: 'Overnight electrical demand remained elevated after the normal shutdown window.',
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
    summary: 'The available energy interval is incomplete.',
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

  assert.throws(
    () => parseInvestigationComplete({
      outcome: 'SUPPORTED_FINDING',
      summary: '',
      evidenceRefs: [],
      limitations: [],
      recommendedNext: [],
    }),
    (error) => error instanceof AgentContractError && error.code === 'TERMINAL_ARTIFACT_INVALID',
  );
});
