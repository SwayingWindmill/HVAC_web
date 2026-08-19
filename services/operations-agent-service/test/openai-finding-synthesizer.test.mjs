import assert from 'node:assert/strict';
import test from 'node:test';

import { createOpenAiFindingSynthesizer } from '../dist/model/index.js';

const model = 'gpt-5.5-2026-07-15';
const input = Object.freeze({
  schemaVersion: 1,
  investigationId: 'investigation-001',
  scope: {
    tenantId: 'organization-001',
    siteId: 'site-001',
    assetId: null,
    deviceId: null,
  },
  expectedClassification: 'INFERENCE',
  deterministicStatement: 'Site night energy was 24% above the comparable baseline.',
  evidence: [{
    id: 'evidence-001',
    classification: 'ALGORITHM_RESULT',
    statement: 'The target period used 124 kWh and the baseline used 100 kWh.',
  }],
  analysisReferences: [{ id: 'analysis-001', outcome: 'SUPPORTED_SITE_FINDING' }],
  promptPolicyVersion: 'finding-synthesis-policy/v1',
  outputSchemaVersion: 'finding-synthesis-output/v1',
  untrustedContentPolicy: 'DATA_ONLY',
});

const output = {
  classification: 'INFERENCE',
  statement: 'Site night energy was 24% above baseline.',
  evidenceIds: ['evidence-001'],
  limitations: [],
};

const createClient = (response) => {
  const calls = [];
  return {
    calls,
    client: {
      responses: {
        async create(request, requestOptions) {
          calls.push({ request, requestOptions });
          if (response instanceof Error) throw response;
          return response;
        },
      },
    },
  };
};

test('OpenAI adapter sends non-stored strict structured requests with stable idempotency', async () => {
  const fake = createClient({
    id: 'response-001',
    _request_id: 'request-001',
    status: 'completed',
    model,
    output_text: JSON.stringify(output),
    usage: {
      input_tokens: 90,
      output_tokens: 30,
    },
  });
  let now = 1_000;
  const synthesizer = createOpenAiFindingSynthesizer({
    model,
    requestTimeoutMs: 5_000,
    maxOutputTokens: 256,
    client: fake.client,
    now: () => {
      now += 20;
      return now;
    },
  });

  const result = await synthesizer.synthesize(input);
  const replay = await synthesizer.synthesize(input);
  assert.deepEqual(result.output, output);
  assert.deepEqual(replay.output, output);
  assert.equal(result.metadata.provider, 'openai');
  assert.equal(result.metadata.model, model);
  assert.equal(result.metadata.configurationDigest.startsWith('sha256:'), true);
  assert.deepEqual(result.metadata.tokenUsage, { inputTokens: 90, outputTokens: 30 });
  assert.equal(result.metadata.traceId, 'request-001');
  assert.equal(result.metadata.latencyMs, 20);

  assert.equal(fake.calls.length, 2);
  const [{ request, requestOptions }, replayCall] = fake.calls;
  assert.equal(request.model, model);
  assert.equal(request.store, false);
  assert.equal(request.max_output_tokens, 256);
  assert.equal(Object.hasOwn(request, 'tools'), false);
  assert.equal(Object.hasOwn(request, 'background'), false);
  assert.equal(Object.hasOwn(request, 'stream'), false);
  assert.equal(request.text.format.type, 'json_schema');
  assert.equal(request.text.format.strict, true);
  assert.deepEqual(request.text.format.schema.properties.classification.enum, ['INFERENCE']);
  assert.deepEqual(request.text.format.schema.properties.evidenceIds.items.enum, ['evidence-001']);
  assert.equal(request.text.format.schema.additionalProperties, false);
  assert.deepEqual(JSON.parse(request.input[0].content[0].text), input);
  assert.match(requestOptions.idempotencyKey, /^operations-finding-[0-9a-f]{64}$/u);
  assert.equal(replayCall.requestOptions.idempotencyKey, requestOptions.idempotencyKey);
});

test('OpenAI adapter rejects model drift, incomplete responses, and invalid structured JSON', async () => {
  const responses = [{
    status: 'completed',
    model: 'another-model',
    output_text: JSON.stringify(output),
    usage: null,
  }, {
    status: 'incomplete',
    model,
    output_text: JSON.stringify(output),
    usage: null,
  }, {
    status: 'completed',
    model,
    output_text: 'not-json',
    usage: null,
  }];

  for (const response of responses) {
    const fake = createClient(response);
    const synthesizer = createOpenAiFindingSynthesizer({
      model,
      requestTimeoutMs: 5_000,
      maxOutputTokens: 256,
      client: fake.client,
    });
    await assert.rejects(() => synthesizer.synthesize(input), {
      name: 'OpenAiFindingSynthesizerError',
    });
  }
});

test('OpenAI adapter hides SDK failure details and validates local bounds before requests', async () => {
  const fake = createClient(new Error('upstream detail must not escape'));
  const synthesizer = createOpenAiFindingSynthesizer({
    model,
    requestTimeoutMs: 5_000,
    maxOutputTokens: 256,
    client: fake.client,
  });
  await assert.rejects(
    () => synthesizer.synthesize(input),
    (error) => error.name === 'OpenAiFindingSynthesizerError'
      && error.message === 'OpenAI request failed.'
      && !error.message.includes('upstream detail'),
  );

  assert.throws(() => createOpenAiFindingSynthesizer({
    model,
    requestTimeoutMs: 99,
    maxOutputTokens: 256,
    client: fake.client,
  }), { name: 'OpenAiFindingSynthesizerError' });
  assert.throws(() => createOpenAiFindingSynthesizer({
    model,
    requestTimeoutMs: 5_000,
    maxOutputTokens: 2_049,
    client: fake.client,
  }), { name: 'OpenAiFindingSynthesizerError' });
  assert.equal(fake.calls.length, 1);
});
