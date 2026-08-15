import assert from 'node:assert/strict';
import test from 'node:test';

import { OwnerReadError } from '../dist/application/index.js';
import {
  createEnergyAnalyticsOwnerReader,
  createGatewayToolAuthorizationReader,
  createRegistryOwnerReader,
} from '../dist/tools/index.js';

const tenantId = '0198a36e-4c9d-7b5a-8f2d-4c5e6f708192';
const siteId = '0198a36e-4c9d-7b5a-8f2d-4c5e6f708193';
const equipmentId = '0198a36e-4c9d-7b5a-8f2d-4c5e6f708194';

const scope = Object.freeze({
  tenantId,
  siteId,
  equipmentId: null,
  deviceId: null,
});

const context = Object.freeze({
  investigationId: 'investigation-owner-read-001',
  runId: 'run-owner-read-001',
  scope,
  correlationId: 'request-owner-read-001',
  authorization: {
    decision: 'ALLOW',
    decisionId: 'decision-owner-read-001',
    delegationGrant: 'fallback-delegation-grant-value',
    toolDelegationGrants: {
      'registry.getSite': 'registry-site-grant-value',
      'registry.listSiteEquipment': 'registry-equipment-grant-value',
    },
    policyRevision: 'registry-policy-42',
    traceparent: '00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01',
  },
});

const energyContext = Object.freeze({
  ...context,
  authorization: {
    decision: 'ALLOW',
    decisionId: 'decision-owner-read-energy',
    delegationGrant: 'fallback-energy-grant-value',
    toolDelegationGrants: {
      'analytics.getEnergySeries': 'energy-delegation-grant-value',
    },
    traceparent: context.authorization.traceparent,
  },
});

const siteDto = Object.freeze({
  id: siteId,
  tenantId: tenantId,
  code: 'TOKYO-HQ',
  displayName: 'Tokyo Headquarters',
  timezone: 'Asia/Tokyo',
  status: 'ACTIVE',
  revision: 17,
  createdAt: '2026-07-01T00:00:00.000Z',
  updatedAt: '2026-07-30T00:00:00.000Z',
});

const equipmentDto = Object.freeze({
  id: equipmentId,
  tenantId: tenantId,
  siteId,
  code: 'CH-01',
  displayName: 'Chiller 01',
  equipmentType: 'CHILLER',
  status: 'ACTIVE',
  revision: 29,
  createdAt: '2026-07-01T00:00:00.000Z',
  updatedAt: '2026-07-30T00:00:00.000Z',
});

const energyRequest = Object.freeze({
  requestId: 'read-energy-series-001',
  tool: 'analytics.getEnergySeries',
  input: {
    tenantId,
    siteId,
    energyType: 'electricity',
    granularity: 'hour',
    timezone: 'Asia/Tokyo',
    from: '2026-07-28T15:00:00.000Z',
    to: '2026-07-29T15:00:00.000Z',
    qualityPolicy: 'VALID_AND_SUSPECT',
  },
});

const energyResponse = Object.freeze({
  schemaVersion: 1,
  points: [{
    periodStart: '2026-07-28T15:00:00.000Z',
    periodEnd: '2026-07-28T16:00:00.000Z',
    energyKWh: 128.5,
  }],
  metadata: {
    requestedGranularity: 'hour',
    actualGranularity: 'hour',
    dataWatermark: '2026-07-29T15:00:00.000Z',
    aggregateWatermark: '2026-07-29T15:00:00.000Z',
    datasetRevision: 'energy-interval:v1:1842',
    partial: false,
    qualitySummary: { valid: 24, suspect: 1, invalid: 0 },
  },
});

const jsonResponse = (value, init = {}) => new Response(JSON.stringify(value), {
  status: init.status ?? 200,
  headers: { 'Content-Type': 'application/json', ...(init.headers ?? {}) },
});

const expectOwnerError = async (run, code) => {
  await assert.rejects(run, (error) => (
    error instanceof OwnerReadError && error.code === code
  ));
};

test('Registry reader exposes only Site and Site Equipment reads with authoritative metadata', async () => {
  const calls = [];
  const reader = createRegistryOwnerReader({
    baseUrl: 'https://platform-core.internal',
    fetchImplementation: async (url, init) => {
      calls.push({ url: String(url), init });
      if (String(url).endsWith(`/sites/${siteId}`)) return jsonResponse(siteDto);
      if (String(url).includes(`/sites/${siteId}/equipment`)) {
        return jsonResponse({ items: [equipmentDto], nextCursor: null, hasMore: false });
      }
      throw new Error('unexpected Registry route');
    },
  });

  const site = await reader.read({
    request: { requestId: 'read-site-001', tool: 'registry.getSite', input: { siteId } },
    context,
  });
  const equipment = await reader.read({
    request: {
      requestId: 'read-site-equipment-001',
      tool: 'registry.listSiteEquipment',
      input: { siteId },
    },
    context,
  });

  assert.equal(site.owner, 'registry');
  assert.deepEqual(site.scope, scope);
  assert.equal(site.revision, 'registry-site:17');
  assert.equal(site.provenance, 'platform-core-service:registry-site/v1');
  assert.deepEqual(site.payload, { kind: 'SITE', site: siteDto });
  assert.match(equipment.revision, /^registry-site-equipment:sha256:[0-9a-f]{64}$/);
  assert.deepEqual(equipment.payload, {
    kind: 'SITE_EQUIPMENT',
    siteId,
    equipment: [equipmentDto],
  });

  assert.equal(calls.length, 2);
  assert.equal(calls[0].init.headers['X-Delegation-Grant'], 'registry-site-grant-value');
  assert.equal(calls[1].init.headers['X-Delegation-Grant'], 'registry-equipment-grant-value');
  for (const call of calls) {
    assert.equal(call.init.method, 'GET');
    assert.equal(call.init.headers['X-Route-Policy-Revision'], 'registry-policy-42');
    assert.equal(call.init.headers['X-Request-ID'].startsWith('read-'), true);
    assert.equal(call.init.headers.traceparent, context.authorization.traceparent);
  }
});

test('Energy reader calls only the fixed Energy Series product contract and preserves readiness metadata', async () => {
  let captured;
  const reader = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async (url, init) => {
      captured = { url: String(url), init };
      return jsonResponse(energyResponse);
    },
  });

  const result = await reader.read({ request: energyRequest, context: energyContext });

  assert.equal(captured.url, 'https://telemetry-query.internal/internal/v1/analytics/energy-series');
  assert.equal(captured.init.method, 'POST');
  assert.deepEqual(JSON.parse(captured.init.body), energyRequest.input);
  assert.equal(captured.init.headers['X-Delegation-Grant'], 'energy-delegation-grant-value');
  assert.equal(captured.init.headers['X-Request-ID'], energyRequest.requestId);
  assert.equal('X-Route-Policy-Revision' in captured.init.headers, false);
  assert.equal(result.owner, 'telemetry-query-service');
  assert.deepEqual(result.scope, scope);
  assert.equal(result.revision, energyResponse.metadata.datasetRevision);
  assert.equal(result.quality, 'UNCERTAIN');
  assert.equal(result.provenance, 'telemetry-query-service:energy-series/v1');
  assert.deepEqual(result.payload, energyResponse);
  assert.equal('measures' in JSON.parse(captured.init.body), false);
  assert.equal('dimensions' in JSON.parse(captured.init.body), false);
  assert.equal('sql' in JSON.parse(captured.init.body), false);
});

test('independent Registry and Energy READ adapters can execute concurrently', async () => {
  let active = 0;
  let maximumActive = 0;
  const concurrentFetch = async (_url, init) => {
    active += 1;
    maximumActive = Math.max(maximumActive, active);
    await new Promise((resolve) => setTimeout(resolve, 10));
    active -= 1;
    return init.method === 'GET' ? jsonResponse(siteDto) : jsonResponse(energyResponse);
  };
  const registry = createRegistryOwnerReader({
    baseUrl: 'https://platform-core.internal',
    fetchImplementation: concurrentFetch,
  });
  const energy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: concurrentFetch,
  });

  await Promise.all([
    registry.read({
      request: { requestId: 'read-site-parallel', tool: 'registry.getSite', input: { siteId } },
      context,
    }),
    energy.read({ request: energyRequest, context: energyContext }),
  ]);
  assert.equal(maximumActive, 2);
});

test('Owner readers fail closed for nondiscoverable, malformed, oversized, contradictory and unavailable responses', async () => {
  const registryForbidden = createRegistryOwnerReader({
    baseUrl: 'https://platform-core.internal',
    fetchImplementation: async () => new Response('{}', { status: 403 }),
  });
  await expectOwnerError(() => registryForbidden.read({
    request: { requestId: 'read-site-forbidden', tool: 'registry.getSite', input: { siteId } },
    context,
  }), 'OWNER_RESOURCE_NOT_FOUND');

  const malformedEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async () => jsonResponse({ ...energyResponse, unexpected: true }),
  });
  await expectOwnerError(
    () => malformedEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_RESPONSE_INVALID',
  );

  const contradictoryEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async () => jsonResponse({
      ...energyResponse,
      metadata: { ...energyResponse.metadata, actualGranularity: 'day' },
    }),
  });
  await expectOwnerError(
    () => contradictoryEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_RESPONSE_INVALID',
  );

  const oversizedEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    maximumResponseBytes: 32,
    fetchImplementation: async () => jsonResponse(energyResponse),
  });
  await expectOwnerError(
    () => oversizedEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_RESPONSE_TOO_LARGE',
  );

  const unavailableEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async () => new Response('{}', { status: 503 }),
  });
  await expectOwnerError(
    () => unavailableEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_READ_UNAVAILABLE',
  );
});

test('Owner readers reject timeout, malformed JSON, missing authorization and cross-Scope identities', async () => {
  const timeoutEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    requestTimeoutMs: 5,
    fetchImplementation: async () => new Promise(() => {}),
  });
  await expectOwnerError(
    () => timeoutEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_READ_TIMEOUT',
  );

  const invalidJsonEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async () => new Response('{', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }),
  });
  await expectOwnerError(
    () => invalidJsonEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_RESPONSE_INVALID',
  );

  const registryMissingPolicy = createRegistryOwnerReader({
    baseUrl: 'https://platform-core.internal',
    fetchImplementation: async () => jsonResponse(siteDto),
  });
  await expectOwnerError(() => registryMissingPolicy.read({
    request: { requestId: 'read-site-no-policy', tool: 'registry.getSite', input: { siteId } },
    context: energyContext,
  }), 'OWNER_REQUEST_INVALID');

  const registryWrongOrganization = createRegistryOwnerReader({
    baseUrl: 'https://platform-core.internal',
    fetchImplementation: async () => jsonResponse({
      ...siteDto,
      tenantId: '0198a36e-4c9d-7b5a-8f2d-4c5e6f708199',
    }),
  });
  await expectOwnerError(() => registryWrongOrganization.read({
    request: {
      requestId: 'read-site-wrong-organization',
      tool: 'registry.getSite',
      input: { siteId },
    },
    context,
  }), 'OWNER_RESPONSE_INVALID');

  const invalidRequestEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async () => new Response('{}', { status: 422 }),
  });
  await expectOwnerError(
    () => invalidRequestEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_REQUEST_INVALID',
  );
  await expectOwnerError(
    () => invalidRequestEnergy.read({
      request: energyRequest,
      context: {
        ...energyContext,
        authorization: {
          ...energyContext.authorization,
          traceparent: '00-' + '0'.repeat(32) + '-' + '0'.repeat(16) + '-01',
        },
      },
    }),
    'OWNER_REQUEST_INVALID',
  );
});

test('Gateway Tool authorization exchanges the service delegation for an exact Owner grant', async () => {
  const calls = [];
  const reader = createGatewayToolAuthorizationReader({
    baseUrl: 'https://platform-gateway.internal',
    fetchImplementation: async (url, init) => {
      calls.push({ url: String(url), init });
      return jsonResponse({
        delegationGrant: 'exact-energy-owner-grant',
        policyRevision: 'analytics-policy-7',
      });
    },
  });
  const authorizationContext = {
    ...energyContext,
    authorization: {
      decision: 'ALLOW',
      decisionId: 'operations-service-delegation',
      delegationGrant: 'operations-service-grant',
      traceparent: context.authorization.traceparent,
    },
  };

  const grant = await reader.authorize({ request: energyRequest, context: authorizationContext });
  assert.deepEqual(grant, {
    delegationGrant: 'exact-energy-owner-grant',
    policyRevision: 'analytics-policy-7',
  });
  assert.equal(calls.length, 1);
  assert.equal(
    calls[0].url,
    'https://platform-gateway.internal/internal/v1/operations/tool-authorization',
  );
  assert.equal(calls[0].init.method, 'POST');
  assert.equal(calls[0].init.headers['X-Delegation-Grant'], 'operations-service-grant');
  assert.equal(calls[0].init.headers['X-Request-ID'], energyRequest.requestId);
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    investigationId: authorizationContext.investigationId,
    runId: authorizationContext.runId,
    request: energyRequest,
  });
});

test('Gateway Tool authorization rejects incomplete context and malformed grant responses', async () => {
  const reader = createGatewayToolAuthorizationReader({
    baseUrl: 'https://platform-gateway.internal',
    fetchImplementation: async () => jsonResponse({
      delegationGrant: 'grant',
      policyRevision: 'policy',
      payload: { forbidden: true },
    }),
  });
  await expectOwnerError(() => reader.authorize({
    request: energyRequest,
    context: {
      ...energyContext,
      authorization: {
        decision: 'ALLOW',
        decisionId: 'missing-service-grant',
      },
    },
  }), 'OWNER_REQUEST_INVALID');
  await expectOwnerError(() => reader.authorize({
    request: energyRequest,
    context: {
      ...energyContext,
      authorization: {
        decision: 'ALLOW',
        decisionId: 'service-grant-present',
        delegationGrant: 'operations-service-grant',
      },
    },
  }), 'OWNER_RESPONSE_INVALID');
});
