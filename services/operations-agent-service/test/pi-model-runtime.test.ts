import assert from 'node:assert/strict';
import test from 'node:test';

import {
  PiModelConfigurationError,
  createProductionPiAgentRuntimeFromEnvironment,
} from '../dist/runtime-pi/index.js';
import { createScriptedPiAgentEngine } from '../dist/runtime-pi/testing.js';

const validEnvironment = Object.freeze({
  AGENT_MODEL_PROVIDER: 'openai',
  AGENT_MODEL_ID: 'gpt-5-mini',
  AGENT_MODEL_ALLOWLIST: 'gpt-5-mini',
  AGENT_MODEL_THINKING_LEVEL: 'medium',
  AGENT_MODEL_TIMEOUT_MS: '12000',
  AGENT_MODEL_MAX_OUTPUT_TOKENS: '1024',
  OPENAI_API_KEY: '[REDACTED_SECRET]',
});

const expectConfigurationError = async (environment, code) => {
  await assert.rejects(
    () => createProductionPiAgentRuntimeFromEnvironment({ environment }),
    (error) => error instanceof PiModelConfigurationError && error.code === code,
  );
};

test('production Pi composition validates one exact OpenAI model and exposes only non-secret policy', async () => {
  const runtime = await createProductionPiAgentRuntimeFromEnvironment({
    environment: validEnvironment,
  });

  assert.deepEqual(runtime.modelRef, { provider: 'openai', model: 'gpt-5-mini' });
  assert.deepEqual(runtime.policy, {
    modelRef: { provider: 'openai', model: 'gpt-5-mini' },
    thinkingLevel: 'medium',
    timeoutMs: 12000,
    maxOutputTokens: 1024,
  });
  assert.equal(JSON.stringify(runtime).includes(validEnvironment.OPENAI_API_KEY), false);
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
    { ...validEnvironment, AGENT_MODEL_ALLOWLIST: 'gpt-5' },
    'MODEL_NOT_ALLOWLISTED',
  );
});

test('missing production credential fails before any external model work', async () => {
  const { OPENAI_API_KEY: _removed, ...environment } = validEnvironment;
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
      AGENT_MODEL_ID: 'gpt-4o-mini',
      AGENT_MODEL_ALLOWLIST: 'gpt-4o-mini',
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
  });
  assert.equal(runtime.modelRef.provider, 'faux');
});
