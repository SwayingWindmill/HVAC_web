import assert from 'node:assert/strict';
import test from 'node:test';

import {
  AGENT_TOOL_EXECUTION_MODE_ENV,
  PiModelConfigurationError,
  createProductionPiAgentRuntimeFromEnvironment,
} from '../dist/runtime-pi/index.js';
import { operationsInvestigationSystemPolicy } from '../dist/runtime-pi/internal/system-policy.js';
import { createScriptedPiAgentEngine } from '../dist/runtime-pi/testing.js';

const validEnvironment = Object.freeze({
  AGENT_MODEL_PROVIDER: 'deepseek',
  AGENT_MODEL_ID: 'deepseek-flash',
  AGENT_MODEL_ALLOWLIST: 'deepseek-flash',
  AGENT_MODEL_THINKING_LEVEL: 'high',
  AGENT_MODEL_TIMEOUT_MS: '12000',
  AGENT_MODEL_MAX_OUTPUT_TOKENS: '1024',
  DEEPSEEK_API_KEY: '[REDACTED_SECRET]',
});

const expectConfigurationError = async (environment, code) => {
  await assert.rejects(
    () => createProductionPiAgentRuntimeFromEnvironment({ environment }),
    (error) => error instanceof PiModelConfigurationError && error.code === code,
  );
};

test('production Pi composition validates one exact DeepSeek model and exposes only non-secret policy', async () => {
  const runtime = await createProductionPiAgentRuntimeFromEnvironment({
    environment: validEnvironment,
  });

  assert.deepEqual(runtime.modelRef, { provider: 'deepseek', model: 'deepseek-flash' });
  assert.deepEqual(runtime.policy, {
    modelRef: { provider: 'deepseek', model: 'deepseek-flash' },
    thinkingLevel: 'high',
    timeoutMs: 12000,
    maxOutputTokens: 1024,
    toolExecutionMode: 'direct',
  });
  assert.equal(JSON.stringify(runtime).includes(validEnvironment.DEEPSEEK_API_KEY), false);
  assert.equal('credential' in runtime, false);
  assert.equal('apiKey' in runtime, false);
});

test('unsupported provider, model, and allowlist fail during composition before a Run starts', async () => {
  await expectConfigurationError(
    { ...validEnvironment, AGENT_MODEL_PROVIDER: 'anthropic' },
    'MODEL_PROVIDER_UNSUPPORTED',
  );
  await expectConfigurationError(
    { ...validEnvironment, AGENT_MODEL_ID: 'not-a-real-model', AGENT_MODEL_ALLOWLIST: 'not-a-real-model' },
    'MODEL_NOT_REGISTERED',
  );
  await expectConfigurationError(
    { ...validEnvironment, AGENT_MODEL_ALLOWLIST: 'deepseek-v4-pro' },
    'MODEL_NOT_ALLOWLISTED',
  );
});

test('missing production credential fails before any external model work', async () => {
  const { DEEPSEEK_API_KEY: _removed, ...environment } = validEnvironment;
  await expectConfigurationError(environment, 'MODEL_PROVIDER_UNAVAILABLE');
});

test('model policy rejects malformed limits and thinking unsupported by the selected model', async () => {
  await expectConfigurationError(
    { ...validEnvironment, AGENT_MODEL_TIMEOUT_MS: '0' },
    'MODEL_TIMEOUT_INVALID',
  );
  await expectConfigurationError(
    { ...validEnvironment, AGENT_MODEL_MAX_OUTPUT_TOKENS: '999999' },
    'MODEL_OUTPUT_LIMIT_INVALID',
  );
  await expectConfigurationError(
    {
      ...validEnvironment,
      AGENT_MODEL_THINKING_LEVEL: 'medium',
    },
    'MODEL_THINKING_UNSUPPORTED',
  );
});

test('deterministic faux-provider composition keeps the same normalized policy surface', () => {
  const runtime = createScriptedPiAgentEngine({
    responses: [],
    policy: {
      thinkingLevel: 'off',
      timeoutMs: 9000,
      maxOutputTokens: 768,
    },
  });

  assert.deepEqual(runtime.policy, {
    modelRef: runtime.modelRef,
    thinkingLevel: 'off',
    timeoutMs: 9000,
    maxOutputTokens: 768,
    toolExecutionMode: 'direct',
  });
  assert.equal(runtime.modelRef.provider, 'faux');
});

test('AGENT_TOOL_EXECUTION_MODE accepts direct and codemode and rejects unknown modes', async () => {
  assert.equal(AGENT_TOOL_EXECUTION_MODE_ENV, 'AGENT_TOOL_EXECUTION_MODE');
  const directExplicit = await createProductionPiAgentRuntimeFromEnvironment({
    environment: { ...validEnvironment, AGENT_TOOL_EXECUTION_MODE: 'direct' },
  });
  assert.equal(directExplicit.policy.toolExecutionMode, 'direct');

  const emptyMode = await createProductionPiAgentRuntimeFromEnvironment({
    environment: { ...validEnvironment, AGENT_TOOL_EXECUTION_MODE: '   ' },
  });
  assert.equal(emptyMode.policy.toolExecutionMode, 'direct');

  const codemodeRuntime = await createProductionPiAgentRuntimeFromEnvironment({
    environment: { ...validEnvironment, AGENT_TOOL_EXECUTION_MODE: 'codemode' },
  });
  assert.equal(codemodeRuntime.policy.toolExecutionMode, 'codemode');

  await expectConfigurationError(
    { ...validEnvironment, AGENT_TOOL_EXECUTION_MODE: 'sandbox' },
    'MODEL_TOOL_MODE_INVALID',
  );
});

test('system policy enforces GFM table divider row in direct and codemode prompts (#669)', () => {
  const site = { name: '松雅轩', timezone: 'Asia/Shanghai' };
  const directPrompt = operationsInvestigationSystemPolicy(Date.parse('2026-10-16T02:30:00.000Z'), site);
  assert.match(directPrompt, /表头下一行必须写 `\| --- \|` 分隔线/u);
  assert.ok(directPrompt.includes('| 时段 | 10 月 8 日 kWh | 10 月 15 日 kWh |\n| --- | --- | --- |'));
  assert.equal(directPrompt.includes('codemode'), false);

  const codemodePrompt = operationsInvestigationSystemPolicy(Date.parse('2026-10-16T02:30:00.000Z'), site, 'codemode');
  assert.match(codemodePrompt, /表头下一行必须写 `\| --- \|` 分隔线/u);
  assert.ok(codemodePrompt.includes('| 时段 | 10 月 8 日 kWh | 10 月 15 日 kWh |\n| --- | --- | --- |'));
  assert.match(codemodePrompt, /codemode.*Promise\.all.*evidenceId.*investigation_complete/u);
});
