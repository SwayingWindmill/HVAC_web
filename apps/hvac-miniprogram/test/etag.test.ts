import assert from 'node:assert/strict';
import test from 'node:test';
import { createAssetModelStore, type AssetModelStorage, type RawSiteAssetModel } from '../miniprogram/lib/asset-model-cache.ts';
import { createGateway, type ChunkedTransport, type Transport, type TransportRequest } from '../miniprogram/lib/gateway.ts';
import { clearAssetModelCache } from '../miniprogram/lib/plant.ts';

const noStreams: ChunkedTransport = () => { throw new Error('not used'); };

const memorySession = (token: string | null) => {
  let current = token;
  return { get: () => current, set: (value: string) => { current = value; }, clear: () => { current = null; } };
};

const memoryStorage = (): AssetModelStorage & { readonly backing: Map<string, unknown> } => {
  const backing = new Map<string, unknown>();
  return {
    backing,
    get: (key) => backing.get(key),
    set: (key, value) => { backing.set(key, structuredClone(value)); },
    remove: (key) => { backing.delete(key); },
    listKeys: () => [...backing.keys()],
  };
};

const sampleModel = (assetName: string): RawSiteAssetModel => ({
  assets: [{ id: 'asset-1', displayName: assetName, assetType: 'CHILLER', status: 'ACTIVE' }],
  devices: [{ id: 'device-1', displayName: '1 号冷机控制器', deviceType: 'PLC', status: 'ACTIVE' }],
  telemetryPoints: [],
  relationships: [],
});

test('gateway.getConditional sends If-None-Match and exposes 200 ETag or 304 Not Modified without losing 401 sign-out', async () => {
  const seen: TransportRequest[] = [];
  let signedOut = 0;
  let step = 0;
  const session = memorySession('token-1');
  const transport: Transport = async (request) => {
    seen.push(request);
    step += 1;
    if (step === 1) {
      return { statusCode: 200, data: sampleModel('一号冷机'), header: { etag: '"abc123"' } };
    }
    if (step === 2) {
      return { statusCode: 304, data: '', header: { ETag: '"abc123"' } };
    }
    return { statusCode: 401, data: { code: 'SESSION_INVALID' } };
  };
  const gateway = createGateway({
    origin: 'https://hvac.example.test',
    transport,
    chunked: noStreams,
    session,
    onSignedOut: () => { signedOut += 1; },
  });

  const first = await gateway.getConditional<RawSiteAssetModel>('/api/v1/sites/site-1/asset-model');
  assert.equal(first.status, 200);
  assert.equal(first.etag, '"abc123"');
  assert.equal(seen[0].header['If-None-Match'], undefined);

  const second = await gateway.getConditional<RawSiteAssetModel>('/api/v1/sites/site-1/asset-model', first.etag);
  assert.equal(second.status, 304);
  assert.equal(second.etag, '"abc123"');
  assert.equal(seen[1].header['If-None-Match'], '"abc123"');

  await assert.rejects(gateway.getConditional('/api/v1/sites/site-1/asset-model', first.etag), {
    code: 'SESSION_INVALID',
    status: 401,
  });
  assert.equal(signedOut, 1);
  assert.equal(session.get(), null);
});

test('asset model store reuses cached model on 304 across cold restarts and replaces it when ETag updates', async () => {
  const sentHeaders: (string | undefined)[] = [];
  let etag = '"v1-hash"';
  let assetName = '一号离心冷机';
  let forceNotModified = false;

  const gateway = createGateway({
    origin: 'https://hvac.example.test',
    chunked: noStreams,
    session: memorySession('token-1'),
    onSignedOut: () => {},
    transport: async (request) => {
      sentHeaders.push(request.header['If-None-Match']);
      if (forceNotModified && request.header['If-None-Match'] === etag) {
        return { statusCode: 304, data: '', header: { ETag: etag } };
      }
      return { statusCode: 200, data: sampleModel(assetName), header: { ETag: etag } };
    },
  });

  const storage = memoryStorage();
  const coldStore = createAssetModelStore({ client: gateway, storage });

  // 1. First load fetches 200 and stores ETag + model in storage.
  const first = await coldStore.read('tenant-a', 'site-1', false);
  assert.equal(first.assets[0].displayName, '一号离心冷机');
  assert.equal(sentHeaders[0], undefined);
  assert.equal(coldStore.peekCachedEtag('tenant-a', 'site-1'), '"v1-hash"');

  // 2. Subsequent non-fresh read hits memory with zero network calls.
  const warm = await coldStore.read('tenant-a', 'site-1', false);
  assert.equal(warm.assets[0].displayName, '一号离心冷机');
  assert.equal(sentHeaders.length, 1);

  // 3. Simulate mini program cold restart sharing persisted storage: sends stored ETag -> 304 returns cached model.
  forceNotModified = true;
  const restartedStore = createAssetModelStore({ client: gateway, storage });
  const fromStorage304 = await restartedStore.read('tenant-a', 'site-1', false);
  assert.equal(fromStorage304.assets[0].displayName, '一号离心冷机');
  assert.equal(sentHeaders[1], '"v1-hash"');

  // 4. Server model mutates with a new ETag -> pull-down refresh replaces cached model and ETag.
  forceNotModified = false;
  etag = '"v2-hash"';
  assetName = '一号离心冷机（扩容）';
  const refreshed = await restartedStore.read('tenant-a', 'site-1', true);
  assert.equal(refreshed.assets[0].displayName, '一号离心冷机（扩容）');
  assert.equal(sentHeaders[2], '"v1-hash"');
  assert.equal(restartedStore.peekCachedEtag('tenant-a', 'site-1'), '"v2-hash"');

  // 5. Follow-up refresh sends new ETag and hits 304.
  forceNotModified = true;
  const revalidated = await restartedStore.read('tenant-a', 'site-1', true);
  assert.equal(revalidated.assets[0].displayName, '一号离心冷机（扩容）');
  assert.equal(sentHeaders[3], '"v2-hash"');
});

test('asset model ETag cache isolates tenants, purges on clear(), and scrubs entries on 403/404 denial', async () => {
  const sent: TransportRequest[] = [];
  let failSiteForTenantB = false;

  const gateway = createGateway({
    origin: 'https://hvac.example.test',
    chunked: noStreams,
    session: memorySession('token-1'),
    onSignedOut: () => {},
    transport: async (request) => {
      sent.push(request);
      if (failSiteForTenantB) {
        return { statusCode: 404, data: { code: 'RESOURCE_NOT_FOUND' } };
      }
      if (request.header['If-None-Match'] === '"etag-tenant-a"') {
        return { statusCode: 304, data: '', header: { ETag: '"etag-tenant-a"' } };
      }
      const model = sent.length === 1 ? sampleModel('租户A设备') : sampleModel('租户B设备');
      const etag = sent.length === 1 ? '"etag-tenant-a"' : '"etag-tenant-b"';
      return { statusCode: 200, data: model, header: { ETag: etag } };
    },
  });

  const storage = memoryStorage();
  const store = createAssetModelStore({ client: gateway, storage });

  const modelA = await store.read('tenant-a', 'shared-site-1', false);
  assert.equal(modelA.assets[0].displayName, '租户A设备');

  // Tenant B with the same siteId must NOT reuse Tenant A's ETag or cached model.
  const modelB = await store.read('tenant-b', 'shared-site-1', false);
  assert.equal(modelB.assets[0].displayName, '租户B设备');
  assert.equal(sent[1].header['If-None-Match'], undefined);

  // If Tenant B loses access (404), refresh must fail closed and drop Tenant B's persisted ETag.
  failSiteForTenantB = true;
  await assert.rejects(store.read('tenant-b', 'shared-site-1', true), { status: 404 });
  assert.equal(store.peekCachedEtag('tenant-b', 'shared-site-1'), null);
  assert.equal(store.peekCachedEtag('tenant-a', 'shared-site-1'), '"etag-tenant-a"');

  // Calling clear() + clearAssetModelCache() wipes all remaining memory & persisted storage ETags.
  clearAssetModelCache();
  store.clear();
  assert.equal(store.peekCachedEtag('tenant-a', 'shared-site-1'), null);
  assert.equal(storage.backing.size, 0);
});

test('401 sign-out on another endpoint and session token changes purge same-tenant memory and storage caches', async () => {
  const sent: TransportRequest[] = [];
  const session = memorySession('token-admin');
  const storage = memoryStorage();
  let returnUnauthorizedOnCommands = false;
  let returnForbiddenForDowngradedUser = false;

  let store!: ReturnType<typeof createAssetModelStore>;
  const gateway = createGateway({
    origin: 'https://hvac.example.test',
    chunked: noStreams,
    session,
    onSignedOut: () => {
      clearAssetModelCache();
      store.clear();
    },
    transport: async (request) => {
      sent.push(request);
      if (returnUnauthorizedOnCommands && request.url.endsWith('/api/v1/commands')) {
        return { statusCode: 401, data: { code: 'SESSION_INVALID' } };
      }
      if (returnForbiddenForDowngradedUser) {
        return { statusCode: 404, data: { code: 'RESOURCE_NOT_FOUND' } };
      }
      return { statusCode: 200, data: sampleModel('管理员视图设备'), header: { ETag: '"admin-etag"' } };
    },
  });

  store = createAssetModelStore({
    client: gateway,
    storage,
    sessionToken: () => session.get(),
  });

  // 1. Admin populates same-tenant asset-model memory + storage cache.
  const cached = await store.read('same-tenant', 'site-1', false);
  assert.equal(cached.assets[0].displayName, '管理员视图设备');
  assert.equal(store.peekCachedEtag('same-tenant', 'site-1'), '"admin-etag"');

  // 2. A 401 on an unrelated endpoint triggers onSignedOut, clearing same-tenant cache immediately.
  returnUnauthorizedOnCommands = true;
  await assert.rejects(gateway.get('/api/v1/commands'), { status: 401, code: 'SESSION_INVALID' });
  assert.equal(store.peekCachedEtag('same-tenant', 'site-1'), null);
  assert.equal(storage.backing.size, 0);

  // 3. Downgraded user signs in under the SAME tenantId: must NOT reuse stale memory cache and must fail closed on 404.
  returnUnauthorizedOnCommands = false;
  returnForbiddenForDowngradedUser = true;
  session.set('token-downgraded');
  await assert.rejects(store.read('same-tenant', 'site-1', false), { status: 404 });
  assert.equal(sent[sent.length - 1].header['If-None-Match'], undefined);

  // 4. Even if session token changes directly without clear(), store detects token mismatch and refuses stale memory.
  returnForbiddenForDowngradedUser = false;
  await store.read('same-tenant', 'site-1', false);
  const callsBeforeTokenSwap = sent.length;
  session.set('token-another-user');
  await store.read('same-tenant', 'site-1', false);
  assert.equal(sent.length, callsBeforeTokenSwap + 1);
  assert.equal(sent[sent.length - 1].header['If-None-Match'], undefined);
});
