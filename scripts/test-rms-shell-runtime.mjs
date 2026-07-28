import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

function compile(file) {
  const sourcePath = path.resolve(file);
  return {
    sourcePath,
    code: ts.transpileModule(fs.readFileSync(sourcePath, 'utf8'), {
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2020,
        strict: true,
      },
      fileName: sourcePath,
    }).outputText,
  };
}

function evaluateCommonJs(compiled, requireFn = () => { throw new Error('unexpected require'); }) {
  const module = { exports: {} };
  vm.runInNewContext(compiled.code, {
    module,
    exports: module.exports,
    require: requireFn,
    URL,
    AbortController,
    DOMException,
  }, { filename: compiled.sourcePath });
  return module.exports;
}

const policy = evaluateCommonJs(compile('apps/hvac-web/src/real/shell-policy.ts'));
const siteRouting = evaluateCommonJs(compile('apps/hvac-web/src/real/site-routing.ts'));
const runtimeModule = evaluateCommonJs(
  compile('apps/hvac-web/src/real/shell-runtime.ts'),
  (specifier) => {
    if (specifier === './shell-policy') return policy;
    if (specifier === './site-routing') return siteRouting;
    throw new Error(`unexpected require: ${specifier}`);
  },
);

function deferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

function principal(
  expiresAt = '2026-07-28T06:00:00.000Z',
  capabilities = ['site.list', 'site.read'],
) {
  return {
    principal: {
      subject: 'operator-1',
      issuer: 'https://identity.example',
      displayName: 'Operator One',
      email: ['operator-1', 'example.invalid'].join('@'),
      roles: ['descriptive-only'],
    },
    context: {
      initiatingPrincipal: {
        subject: 'operator-1',
        issuer: 'https://identity.example',
        displayName: 'Operator One',
        email: ['operator-1', 'example.invalid'].join('@'),
        roles: ['descriptive-only'],
      },
      executingServicePrincipal: { service: 'platform-gateway', spiffeId: 'spiffe://hvac.local/platform-gateway' },
      actingOrganizationId: '01900000-0000-7000-8000-000000000001',
      audience: 'iam-service',
      policyRevision: 'gateway:1',
      delegationExpiresAt: expiresAt,
    },
    authorization: {
      capabilitySetVersion: 1,
      policyRevision: 'iam:1',
      capabilities,
    },
    session: {
      id: 'session-1',
      expiresAt,
      csrfToken: '[REDACTED_SECRET]',
      revocationObjectiveMs: 1000,
      lastAuditMessageId: 'audit-1',
    },
  };
}

function environment() {
  const navigations = [];
  const timers = new Map();
  let nextTimer = 0;
  return {
    navigations,
    timers,
    value: {
      origin: 'https://hvac.example',
      now: () => Date.parse('2026-07-28T05:00:00.000Z'),
      navigate: (target) => navigations.push(target),
      setTimer: (handler, delay) => {
        const id = ++nextTimer;
        timers.set(id, { handler, delay });
        return id;
      },
      clearTimer: (id) => timers.delete(id),
    },
  };
}

function platformStatus(status = 'ok') {
  return {
    status,
    service: 'platform-status',
    implementation: 'go',
    version: 'rms-04-test',
    checkedAt: '2026-07-28T05:00:00.000Z',
    routePolicyRevision: 7,
    routeRevision: 11,
    compatibilityMode: 'native',
  };
}

function registrySite(
  id = '01900000-0001-7000-8000-000000000001',
  owningOrganizationId = '01900000-0000-7000-8000-000000000001',
) {
  return {
    id,
    owningOrganizationId,
    code: `SITE-${id.slice(-4)}`,
    displayName: `Site ${id.slice(-4)}`,
    timezone: 'Asia/Tokyo',
    status: 'ACTIVE',
    revision: 1,
    createdAt: '2026-07-28T00:00:00.000Z',
    updatedAt: '2026-07-28T00:00:00.000Z',
  };
}

function siteCollection(items = [], nextCursor = null, hasMore = false) {
  return { items, nextCursor, hasMore };
}

function client(overrides = {}) {
  return {
    getCurrentPrincipal: async () => ({ data: principal() }),
    getPlatformStatus: async () => ({ data: platformStatus() }),
    listOrganizationSites: async () => ({ data: siteCollection() }),
    loginUrl: ({ returnTo }) => `/api/v1/auth/login?returnTo=${encodeURIComponent(returnTo)}`,
    logout: async () => ({ data: undefined }),
    ...overrides,
  };
}

test('holds the shell in BOOTSTRAPPING until Principal bootstrap completes', async () => {
  const pending = deferred();
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({ getCurrentPrincipal: () => pending.promise }), env.value);

  const bootstrap = runtime.bootstrap('https://hvac.example/system?tab=overview');
  assert.equal(runtime.current().state, 'BOOTSTRAPPING');
  assert.equal(runtime.current().principal, undefined);

  pending.resolve({ data: principal() });
  await bootstrap;
  assert.equal(runtime.current().state, 'READY');
  assert.equal(runtime.current().principal.principal.displayName, 'Operator One');
});

test('enters LOGIN_REQUIRED and navigates with only a normalized same-origin return path', async () => {
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    getCurrentPrincipal: async () => {
      throw { problem: { status: 401, code: 'SESSION_INVALID', detail: 'invalid', traceId: '0'.repeat(32), retryable: false } };
    },
  }), env.value);

  await runtime.bootstrap('https://attacker.example/steal');
  assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
  assert.equal(runtime.current().principal, undefined);
  assert.deepEqual(env.navigations, ['/api/v1/auth/login?returnTo=%2F']);
});

test('keeps protected memory mounted and exposes a retryable logout failure', async () => {
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    logout: async () => {
      throw { problem: { status: 503, code: 'SESSION_PERSISTENCE_FAILED', detail: 'retry later', traceId: '1'.repeat(32), retryable: true } };
    },
  }), env.value);

  await runtime.bootstrap('/system');
  await runtime.logout();
  assert.equal(runtime.current().state, 'READY');
  assert.equal(runtime.current().principal.session.id, 'session-1');
  assert.equal(runtime.current().logout.status, 'failed');
  assert.equal(runtime.current().logout.retryable, true);
});

test('purges protected memory only after confirmed or already-invalid logout', async () => {
  for (const logout of [
    async () => ({ data: undefined }),
    async () => { throw { problem: { status: 401, code: 'SESSION_INVALID', detail: 'already gone', traceId: '2'.repeat(32), retryable: false } }; },
  ]) {
    const env = environment();
    const runtime = runtimeModule.createShellRuntime(client({ logout }), env.value);
    await runtime.bootstrap('/system');
    const outcome = await runtime.logout();
    assert.equal(outcome, 'completed');
    assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
    assert.equal(runtime.current().principal, undefined);
    assert.equal(env.navigations.length, 0);
  }
});

test('session expiration purges protected memory and starts login again', async () => {
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client(), env.value);
  await runtime.bootstrap('/system');
  assert.equal(env.timers.size, 1);

  const [{ handler }] = env.timers.values();
  handler();
  assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
  assert.equal(runtime.current().principal, undefined);
  assert.deepEqual(env.navigations, ['/api/v1/auth/login?returnTo=%2Fsystem']);
});

test('a server revocation signal purges protected memory and stops route mounting', async () => {
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client(), env.value);
  await runtime.bootstrap('/system');

  runtime.purge('SESSION_REVOKED', true);

  assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
  assert.equal(runtime.current().principal, undefined);
  assert.equal(env.timers.size, 0);
  assert.deepEqual(env.navigations, ['/api/v1/auth/login?returnTo=%2Fsystem']);
});

test('disposing an unmounted shell clears timers and suppresses later navigation', async () => {
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client(), env.value);
  await runtime.bootstrap('/system');
  assert.equal(env.timers.size, 1);

  runtime.dispose();
  runtime.purge('SESSION_EXPIRED', true);

  assert.equal(env.timers.size, 0);
  assert.deepEqual(env.navigations, []);
});

test('publishes available and degraded platform status without interpreting roles', async () => {
  for (const [status, expected] of [['ok', 'available'], ['degraded', 'degraded']]) {
    const env = environment();
    const runtime = runtimeModule.createShellRuntime(client({
      getPlatformStatus: async () => ({ data: platformStatus(status) }),
    }), env.value);

    await runtime.bootstrap('/system');

    assert.equal(runtime.current().state, 'READY');
    assert.equal(runtime.current().platform.state, expected);
    assert.equal(runtime.current().platform.status.status, status);
    assert.deepEqual(runtime.current().principal.principal.roles, ['descriptive-only']);
  }
});

test('keeps the authenticated shell while platform availability is unavailable', async () => {
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    getPlatformStatus: async () => {
      throw { problem: { status: 503, code: 'PLATFORM_STATUS_UNAVAILABLE', detail: 'status unavailable', traceId: '3'.repeat(32), retryable: true } };
    },
  }), env.value);

  await runtime.bootstrap('/system');

  assert.equal(runtime.current().state, 'READY');
  assert.equal(runtime.current().principal.session.id, 'session-1');
  assert.equal(runtime.current().platform.state, 'unavailable');
  assert.equal(runtime.current().platform.failure.code, 'PLATFORM_STATUS_UNAVAILABLE');
  assert.equal(runtime.current().platform.failure.retryable, true);
});

test('an authentication failure from platform status purges protected memory', async () => {
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    getPlatformStatus: async () => {
      throw { problem: { status: 401, code: 'SESSION_INVALID', detail: 'session invalid', traceId: '4'.repeat(32), retryable: false } };
    },
  }), env.value);

  await runtime.bootstrap('/system');

  assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
  assert.equal(runtime.current().principal, undefined);
  assert.deepEqual(env.navigations, ['/api/v1/auth/login?returnTo=%2Fsystem']);
});

test('late platform availability cannot reset an in-flight logout', async () => {
  const pendingPlatform = deferred();
  const pendingLogout = deferred();
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    getPlatformStatus: () => pendingPlatform.promise,
    logout: () => pendingLogout.promise,
  }), env.value);

  const bootstrap = runtime.bootstrap('/system');
  for (let attempt = 0; attempt < 5 && !runtime.current().platform; attempt += 1) {
    await Promise.resolve();
  }
  assert.equal(runtime.current().platform.state, 'checking');

  const logout = runtime.logout();
  assert.equal(runtime.current().logout.status, 'submitting');

  pendingPlatform.resolve({ data: platformStatus() });
  await bootstrap;
  assert.equal(runtime.current().platform.state, 'available');
  assert.equal(runtime.current().logout.status, 'submitting');

  pendingLogout.resolve({ data: undefined });
  await logout;
  assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
});

test('does not request Registry Sites without the effective site.list capability', async () => {
  let listCalls = 0;
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    getCurrentPrincipal: async () => ({ data: principal(undefined, ['site.read']) }),
    listOrganizationSites: async () => {
      listCalls += 1;
      return { data: siteCollection([registrySite()]) };
    },
  }), env.value);

  await runtime.bootstrap('/sites');

  assert.equal(runtime.current().state, 'READY');
  assert.equal(runtime.current().sites.state, 'forbidden');
  assert.equal(listCalls, 0);
});

test('completed Site discovery does not orphan a pending platform request', async () => {
  const pendingPlatform = deferred();
  let platformSignal;
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    getPlatformStatus: ({ signal }) => {
      platformSignal = signal;
      return pendingPlatform.promise;
    },
    listOrganizationSites: async () => ({ data: siteCollection([registrySite()]) }),
  }), env.value);

  void runtime.bootstrap('/sites');
  for (let attempt = 0; attempt < 10 && runtime.current().sites?.state !== 'available'; attempt += 1) {
    await Promise.resolve();
  }

  assert.equal(runtime.current().sites.state, 'available');
  assert.equal(platformSignal.aborted, false);
  runtime.dispose();
  assert.equal(platformSignal.aborted, true);
});

test('Site discovery is not blocked by a pending platform status request', async () => {
  const pendingPlatform = deferred();
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    getPlatformStatus: () => pendingPlatform.promise,
    listOrganizationSites: async () => ({ data: siteCollection([registrySite()]) }),
  }), env.value);

  const bootstrap = runtime.bootstrap('/sites');
  for (let attempt = 0; attempt < 10 && runtime.current().sites?.state !== 'available'; attempt += 1) {
    await Promise.resolve();
  }

  assert.equal(runtime.current().sites.state, 'available');
  assert.equal(runtime.current().platform.state, 'checking');

  pendingPlatform.resolve({ data: platformStatus() });
  await bootstrap;
  assert.equal(runtime.current().platform.state, 'available');
  assert.equal(runtime.current().sites.state, 'available');
});

test('discovers all authorized Sites only under the acting Organization', async () => {
  const calls = [];
  const siteA = registrySite();
  const siteB = registrySite('01900000-0002-7000-8000-000000000002');
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    listOrganizationSites: async (organizationId, params) => {
      calls.push({ organizationId, params });
      if (!params.cursor) {
        return { data: siteCollection([siteA], 'cursor.page-two', true) };
      }
      return { data: siteCollection([siteB], null, false) };
    },
  }), env.value);

  await runtime.bootstrap('/sites');

  assert.equal(calls.length, 2);
  assert.equal(calls[0].organizationId, '01900000-0000-7000-8000-000000000001');
  assert.equal(calls[0].params.limit, 100);
  assert.equal(calls[0].params.cursor, undefined);
  assert.equal(calls[1].organizationId, '01900000-0000-7000-8000-000000000001');
  assert.equal(calls[1].params.limit, 100);
  assert.equal(calls[1].params.cursor, 'cursor.page-two');
  assert.equal(runtime.current().sites.state, 'available');
  assert.equal(runtime.current().sites.items.length, 2);
  assert.equal(runtime.current().sites.items[0].id, siteA.id);
  assert.equal(runtime.current().sites.items[1].id, siteB.id);
});

test('an empty Registry collection becomes an explicit available zero-Site result', async () => {
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client(), env.value);

  await runtime.bootstrap('/sites');

  assert.equal(runtime.current().sites.state, 'available');
  assert.equal(runtime.current().sites.items.length, 0);
});

test('rejects malformed, cross-Organization, and duplicate Site discovery responses', async () => {
  const actingOrganizationId = '01900000-0000-7000-8000-000000000001';
  const invalidCollections = [
    [registrySite('b1')],
    [registrySite('01900000-0001-7000-8000-000000000001', '01900000-0009-7000-8000-000000000009')],
    [registrySite(), registrySite()],
  ];

  for (const items of invalidCollections) {
    const env = environment();
    const runtime = runtimeModule.createShellRuntime(client({
      listOrganizationSites: async (organizationId) => {
        assert.equal(organizationId, actingOrganizationId);
        return { data: siteCollection(items) };
      },
    }), env.value);

    await runtime.bootstrap('/sites');

    assert.equal(runtime.current().state, 'READY');
    assert.equal(runtime.current().sites.state, 'unavailable');
    assert.equal(runtime.current().sites.failure.code, 'SITE_DISCOVERY_INVALID');
    assert.equal(runtime.current().sites.items, undefined);
  }
});

test('an authentication failure during Site discovery purges protected memory', async () => {
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    listOrganizationSites: async () => {
      throw { problem: { status: 401, code: 'SESSION_INVALID', detail: 'session invalid', traceId: '5'.repeat(32), retryable: false } };
    },
  }), env.value);

  await runtime.bootstrap('/sites');

  assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
  assert.equal(runtime.current().principal, undefined);
  assert.deepEqual(env.navigations, ['/api/v1/auth/login?returnTo=%2Fsites']);
});

test('late Site discovery cannot reset an in-flight logout', async () => {
  const pendingSites = deferred();
  const pendingLogout = deferred();
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    listOrganizationSites: () => pendingSites.promise,
    logout: () => pendingLogout.promise,
  }), env.value);

  const bootstrap = runtime.bootstrap('/sites');
  for (let attempt = 0; attempt < 10 && runtime.current().sites?.state !== 'checking'; attempt += 1) {
    await Promise.resolve();
  }
  assert.equal(runtime.current().sites.state, 'checking');

  const logout = runtime.logout();
  assert.equal(runtime.current().logout.status, 'submitting');

  pendingSites.resolve({ data: siteCollection([registrySite()]) });
  await bootstrap;
  assert.equal(runtime.current().sites.state, 'available');
  assert.equal(runtime.current().logout.status, 'submitting');

  pendingLogout.resolve({ data: undefined });
  await logout;
  assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
});
