import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createAssetModelStore,
  type AssetModelStorage,
} from '../miniprogram/lib/asset-model-cache.ts';
import {
  askAssistantLater,
  chooseConversation,
  chosenConversation,
  clearAssistantChoice,
  takePendingQuestion,
} from '../miniprogram/lib/assistant-choice.ts';
import { createGateway, type ChunkedTransport } from '../miniprogram/lib/gateway.ts';
import { clearAssetModelCache, fetchPlantDevice } from '../miniprogram/lib/plant.ts';
import { registerSignOutHandler, runSignOutHandlers } from '../miniprogram/lib/session-cleanup.ts';
import { clearTabBadge, selectTab, showAttention } from '../miniprogram/lib/tab.ts';
import { resolveSiteTodayStart } from '../miniprogram/lib/today.ts';
import { assetModel } from './recorded.ts';

const noStreams: ChunkedTransport = () => {
  throw new Error('not used');
};

function memoryStorage(): AssetModelStorage & { readonly entries: Map<string, unknown> } {
  const entries = new Map<string, unknown>();
  return {
    entries,
    get: (key) => entries.get(key),
    set: (key, value) => {
      entries.set(key, value);
    },
    remove: (key) => {
      entries.delete(key);
    },
    listKeys: () => [...entries.keys()],
  };
}

function fakePage(route: string) {
  let latestData: Record<string, unknown> | null = null;
  const tabBar = {
    setData(next: Record<string, unknown>) {
      latestData = next;
    },
  };
  return {
    page: {
      route,
      getTabBar: () => tabBar,
    },
    badge: () => latestData?.badge,
  };
}

test('runSignOutHandlers and 401 gateway refusals clear registered caches (assistant choice, tab badge, asset-model store, site selection)', async () => {
  let token: string | null = 'token-1';
  let rememberedSite: string | null = 'site-a';
  let requestMode: 'ok' | '401' = 'ok';
  const storage = memoryStorage();

  const gateway = createGateway({
    origin: 'https://hvac.example.test',
    chunked: noStreams,
    session: {
      get: () => token,
      set: (value) => {
        token = value;
      },
      clear: () => {
        token = null;
      },
    },
    onSignedOut: runSignOutHandlers,
    transport: async (request) => {
      if (requestMode === '401') {
        return { statusCode: 401, data: { code: 'SESSION_INVALID' } };
      }
      if (request.url.endsWith('/asset-model')) {
        return { statusCode: 200, data: assetModel, header: { ETag: '"etag-1"' } };
      }
      return { statusCode: 404, data: { code: 'NOT_FOUND' } };
    },
  });

  const store = createAssetModelStore({
    client: gateway,
    storage,
    sessionToken: () => token,
  });

  registerSignOutHandler(() => {
    rememberedSite = null;
  });
  registerSignOutHandler(() => {
    clearAssetModelCache();
    store.clear();
  });

  try {
    chooseConversation('session-a');
    askAssistantLater('昨天冷站COP是多少？');
    const tab = fakePage('pages/notifications/index');
    showAttention(tab.page, 7);
    assert.equal(tab.badge(), '7');

    const loaded = await store.read('t1', 'site-a', false);
    assert.ok(loaded.devices.length > 0);
    assert.equal(store.peekCachedEtag('t1', 'site-a'), '"etag-1"');

    // 1. Explicit sign-out cleanup drops every session-scoped store & badge.
    runSignOutHandlers();
    assert.equal(rememberedSite, null);
    assert.equal(chosenConversation(), undefined);
    assert.equal(takePendingQuestion(), null);
    assert.equal(store.peekCachedEtag('t1', 'site-a'), null);
    assert.equal(storage.entries.size, 0);
    selectTab(tab.page);
    assert.equal(tab.badge(), '');

    // 2. Gateway 401 response clears session token and fires all registered cleaners.
    token = 'token-2';
    rememberedSite = 'site-b';
    chooseConversation('session-b');
    askAssistantLater('再次提问');
    showAttention(tab.page, 12);
    await store.read('t1', 'site-b', false);
    assert.equal(store.peekCachedEtag('t1', 'site-b'), '"etag-1"');

    requestMode = '401';
    await assert.rejects(() => gateway.get('/api/v1/sites?limit=50'));
    assert.equal(token, null);
    assert.equal(rememberedSite, null);
    assert.equal(chosenConversation(), undefined);
    assert.equal(takePendingQuestion(), null);
    assert.equal(store.peekCachedEtag('t1', 'site-b'), null);
    assert.equal(storage.entries.size, 0);
    selectTab(tab.page);
    assert.equal(tab.badge(), '');
  } finally {
    clearAssistantChoice();
    clearTabBadge();
    clearAssetModelCache();
  }
});

test('fetchPlantDevice reuses preloadedModel without issuing redundant /asset-model GET requests', async () => {
  const chillerId = assetModel.devices[0].id as string;
  const calledPaths: string[] = [];
  const client = {
    async get<T>(path: string): Promise<T> {
      calledPaths.push(path);
      return {
        evaluationAvailability: 'AVAILABLE',
        presence: { applicability: 'APPLICABLE', currentState: 'ONLINE' },
        values: [],
      } as T;
    },
  };

  const unit = await fetchPlantDevice(client, 'site-1', chillerId, false, new Date('2026-10-20T06:05:00.000Z'), assetModel);
  assert.ok(unit !== null);
  assert.equal(calledPaths.some((path) => path.endsWith('/asset-model')), false);
  assert.ok(calledPaths.some((path) => path.includes(`/devices/${chillerId}/observation-snapshot`)));
});

test('resolveSiteTodayStart resolves siteDayBounds locally without querying daily energy-series, falling back only when needed', async () => {
  const now = new Date('2026-10-20T06:05:00.000Z');
  const calls: string[] = [];
  const client = {
    async post<T>(path: string): Promise<T> {
      calls.push(path);
      return {
        points: [
          { periodStart: '2026-10-19T16:00:00.000Z', periodEnd: '2026-10-20T16:00:00.000Z', energyKWh: 1200 },
        ],
      } as T;
    },
  };

  const shanghaiStart = await resolveSiteTodayStart(client, { id: 's1', tenantId: 't1', timezone: 'Asia/Shanghai' }, now);
  assert.equal(shanghaiStart, '2026-10-19T16:00:00.000Z');
  assert.equal(calls.length, 0);

  const fallbackStart = await resolveSiteTodayStart(client, { id: 's2', tenantId: 't1', timezone: 'Mars/Olympus' }, now);
  assert.equal(fallbackStart, '2026-10-19T16:00:00.000Z');
  assert.deepEqual(calls, ['/api/v1/analytics/energy-series']);
});
