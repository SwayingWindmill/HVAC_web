import assert from 'node:assert/strict';
import test from 'node:test';
import { createGateway, type ChunkedTransport, type Transport } from '../miniprogram/lib/gateway.ts';
import {
  askAssistantLater,
  chooseConversation,
  chosenConversation,
  clearAssistantChoice,
  takePendingQuestion,
} from '../miniprogram/lib/assistant-choice.ts';
import { clearAssetModelCache, readAssetModel } from '../miniprogram/lib/plant.ts';
import { readTenantState, switchActiveTenant } from '../miniprogram/lib/tenant.ts';

const noStreams: ChunkedTransport = () => { throw new Error('not used'); };

function gatewayAnswering(
  answers: Record<string, { statusCode: number; data?: unknown }>,
  sent: { method: string; url: string; data?: unknown }[] = [],
) {
  const transport: Transport = async (request) => {
    const url = request.url.replace('https://hvac.example.test', '');
    sent.push({ method: request.method, url, data: request.data });
    const answer = answers[`${request.method} ${url}`];
    if (answer === undefined) throw new Error(`unexpected ${request.method} ${url}`);
    return { statusCode: answer.statusCode, data: answer.data ?? '' };
  };
  return createGateway({
    origin: 'https://hvac.example.test',
    transport,
    chunked: noStreams,
    session: { get: () => 'bearer-token', set: () => {}, clear: () => {} },
    onSignedOut: () => {},
  });
}

test('readTenantState disables switching when the account belongs to zero or one tenant', () => {
  assert.deepEqual(readTenantState({ context: { tenantId: 't1' } }), {
    activeTenantId: 't1',
    activeTenantName: '',
    activeIndex: 0,
    tenants: [],
    canSwitch: false,
  });

  assert.deepEqual(readTenantState({
    context: { tenantId: 't1' },
    availableTenants: [{ tenantId: 't1', code: 'alpha', displayName: '松雅轩一期' }],
  }), {
    activeTenantId: 't1',
    activeTenantName: '松雅轩一期',
    activeIndex: 0,
    tenants: [{ tenantId: 't1', code: 'alpha', displayName: '松雅轩一期' }],
    canSwitch: false,
  });
});

test('readTenantState locates active tenant and enables switching for multi-tenant accounts', () => {
  const state = readTenantState({
    context: { tenantId: 't2' },
    availableTenants: [
      { tenantId: 't1', code: 'alpha', displayName: '松雅轩一期' },
      { tenantId: 't2', code: 'beta', displayName: '麓谷冷站' },
    ],
  });
  assert.equal(state.activeTenantId, 't2');
  assert.equal(state.activeTenantName, '麓谷冷站');
  assert.equal(state.activeIndex, 1);
  assert.equal(state.canSwitch, true);
});

test('switchActiveTenant posts tenantId, clears cached site and plant state, and reports errors cleanly', async () => {
  const sent: { method: string; url: string; data?: unknown }[] = [];
  let cacheCleared = 0;
  const gateway = gatewayAnswering({
    'POST /api/v1/auth/session/tenant': {
      statusCode: 200,
      data: {
        context: { tenantId: 't2' },
        availableTenants: [
          { tenantId: 't1', code: 'alpha', displayName: '松雅轩一期' },
          { tenantId: 't2', code: 'beta', displayName: '麓谷冷站' },
        ],
      },
    },
  }, sent);

  const ok = await switchActiveTenant(gateway, 't2', () => { cacheCleared += 1; });
  assert.equal(ok.switched, true);
  assert.equal(ok.state?.activeTenantId, 't2');
  assert.equal(ok.state?.activeTenantName, '麓谷冷站');
  assert.equal(ok.message, '已切换到 麓谷冷站');
  assert.equal(cacheCleared, 1);
  assert.deepEqual(sent, [{ method: 'POST', url: '/api/v1/auth/session/tenant', data: { tenantId: 't2' } }]);

  let failedClearCount = 0;
  const forbidden = gatewayAnswering({
    'POST /api/v1/auth/session/tenant': { statusCode: 403, data: { code: 'TENANT_SWITCH_FORBIDDEN' } },
  });
  const denied = await switchActiveTenant(forbidden, 't3', () => { failedClearCount += 1; });
  assert.deepEqual(denied, { switched: false, message: '你当前没有这个组织的访问权限。' });
  assert.equal(failedClearCount, 0);

  const unavailable = gatewayAnswering({
    'POST /api/v1/auth/session/tenant': { statusCode: 503, data: { code: 'IDENTITY_UNAVAILABLE' } },
  });
  assert.deepEqual(await switchActiveTenant(unavailable, 't3', () => { failedClearCount += 1; }), {
    switched: false,
    message: '暂时切换不了组织，请稍后再试。',
  });
  assert.equal(failedClearCount, 0);
});

test('clearAssetModelCache and clearAssistantChoice drop stale site and conversation state on tenant switch', async () => {
  const sent: { method: string; url: string; data?: unknown }[] = [];
  const gateway = gatewayAnswering({
    'GET /api/v1/sites/site-1/asset-model': {
      statusCode: 200,
      data: { spaces: [], assets: [], devices: [], relationships: [], counts: { physicalSensors: 0 } },
    },
  }, sent);
  await readAssetModel(gateway, 'site-1', false);
  await readAssetModel(gateway, 'site-1', false);
  assert.equal(sent.length, 1);
  clearAssetModelCache();
  await readAssetModel(gateway, 'site-1', false);
  assert.equal(sent.length, 2);

  chooseConversation('session-tenant-a');
  askAssistantLater('昨天电费多少');
  clearAssistantChoice();
  assert.equal(chosenConversation(), undefined);
  assert.equal(takePendingQuestion(), null);
});
