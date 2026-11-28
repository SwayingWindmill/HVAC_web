import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';
import type {
  createShellRuntime,
  ShellRuntimeEnvironment,
} from '../apps/hvac-web/src/app/shell-runtime.ts';
import type {
  Capability,
  CurrentPrincipalResponse,
  PlatformResponse,
  PlatformStatusResponse,
  Site,
  SiteCollection,
} from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';

type ShellClient = Parameters<typeof createShellRuntime>[0];

function compile(file: string): { readonly sourcePath: string; readonly code: string } {
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

function evaluateCommonJs(
  compiled: { readonly sourcePath: string; readonly code: string },
  requireFn: (specifier: string) => unknown = () => {
    throw new Error('unexpected require');
  },
): Record<string, unknown> {
  const mod: { exports: Record<string, unknown> } = { exports: {} };
  vm.runInNewContext(
    compiled.code,
    {
      module: mod,
      exports: mod.exports,
      require: requireFn,
      URL,
      AbortController,
      DOMException,
    },
    { filename: compiled.sourcePath },
  );
  return mod.exports;
}

const policy = evaluateCommonJs(compile('apps/hvac-web/src/app/shell-policy.ts'));
const routerPaths = evaluateCommonJs(compile('apps/hvac-web/src/app/router-paths.ts'));
const protectedScope = evaluateCommonJs(compile('apps/hvac-web/src/app/protected-scope.ts'));
const realtimeStatus = evaluateCommonJs(
  compile('apps/hvac-web/src/app/realtime-status.ts'),
  (specifier) => {
    if (specifier === './router-paths') return routerPaths;
    throw new Error(`unexpected require: ${specifier}`);
  },
);
const shellRuntimeHelpers = evaluateCommonJs(
  compile('apps/hvac-web/src/app/shell-runtime-helpers.ts'),
  (specifier) => {
    if (specifier === './shell-policy') return policy;
    if (specifier === './router-paths') return routerPaths;
    throw new Error(`unexpected require: ${specifier}`);
  },
);
const runtimeModule = evaluateCommonJs(
  compile('apps/hvac-web/src/app/shell-runtime.ts'),
  (specifier) => {
    if (specifier === './shell-policy') return policy;
    if (specifier === './router-paths') return routerPaths;
    if (specifier === './protected-scope') return protectedScope;
    if (specifier === './realtime-status') return realtimeStatus;
    if (specifier === './shell-runtime-helpers') return shellRuntimeHelpers;
    throw new Error(`unexpected require: ${specifier}`);
  },
) as { readonly createShellRuntime: typeof createShellRuntime };

function deferred<T = void>(): { readonly promise: Promise<T>; readonly resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

function okResponse<T>(data: T, location: string | null = null): PlatformResponse<T> {
  return {
    data,
    requestId: null,
    traceparent: null,
    auditMessageId: null,
    routePolicyRevision: null,
    location,
  };
}

function principal(
  expiresAt = '2026-07-28T06:00:00.000Z',
  capabilities: Capability[] = ['site.list', 'site.read'],
): CurrentPrincipalResponse {
  return {
    principalId: '018f3e00-2000-7000-8000-000000000001',
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
      tenantId: '01900000-0000-7000-8000-000000000001',
      audience: 'iam-service',
      policyRevision: 'gateway:1',
      delegationExpiresAt: expiresAt,
    },
    authorization: {
      capabilitySetVersion: 14,
      policyRevision: 'iam:1',
      capabilities,
    },
    session: {
      id: 'session-1',
      expiresAt,
      csrfToken: '[REDACTED_SECRET]',
      idleTimeoutMs: 30 * 60 * 1000,
      revocationObjectiveMs: 1000,
      lastAuditMessageId: 'audit-1',
    },
  };
}

function environment(): {
  readonly navigations: string[];
  readonly timers: Map<number, { readonly handler: () => void; readonly delay: number }>;
  readonly value: ShellRuntimeEnvironment;
} {
  const navigations: string[] = [];
  const timers = new Map<number, { readonly handler: () => void; readonly delay: number }>();
  let nextTimer = 0;
  return {
    navigations,
    timers,
    value: {
      origin: 'https://hvac.example',
      now: () => Date.parse('2026-07-28T05:00:00.000Z'),
      navigate: (target: string) => {
        navigations.push(target);
      },
      postNavigate: (target: string) => {
        navigations.push(target);
      },
      setTimer: (handler: () => void, delay: number) => {
        const id = ++nextTimer;
        timers.set(id, { handler, delay });
        return id;
      },
      clearTimer: (id: unknown) => {
        if (typeof id === 'number') timers.delete(id);
      },
    },
  };
}

function platformStatus(status: PlatformStatusResponse['status'] = 'ok'): PlatformStatusResponse {
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
  tenantId = '01900000-0000-7000-8000-000000000001',
): Site {
  return {
    id,
    tenantId,
    code: `SITE-${id.slice(-4)}`,
    displayName: `Site ${id.slice(-4)}`,
    timezone: 'Asia/Tokyo',
    status: 'ACTIVE',
    revision: 1,
    createdAt: '2026-07-28T00:00:00.000Z',
    updatedAt: '2026-07-28T00:00:00.000Z',
    floorAreaM2: null,
    climateZone: null,
  };
}

function siteCollection(
  items: Site[] = [],
  nextCursor: string | null = null,
  hasMore = false,
): SiteCollection {
  return { items, nextCursor, hasMore };
}

const providerLogoutURL = 'https://identity.example/session/end?client_id=hvac-web&post_logout_redirect_uri=https%3A%2F%2Fhvac.example%2F%3Flogged_out%3D1';

function client(overrides: Partial<ShellClient> = {}): ShellClient {
  return {
    getCurrentPrincipal: async () => okResponse(principal()),
    getPlatformStatus: async () => okResponse(platformStatus()),
    listSites: async () => okResponse(siteCollection()),
    loginUrl: (params) => `/api/v1/auth/login?returnTo=${encodeURIComponent(params?.returnTo ?? '/')}`,
    logout: async () => okResponse(undefined, providerLogoutURL),
    ...overrides,
  };
}

test('transient UI states converge on quiet reusable loading surfaces', () => {
  const appHostSource = fs.readFileSync('apps/hvac-web/src/app/AppRuntimeHost.tsx', 'utf8');
  const siteRouteSource = fs.readFileSync('apps/hvac-web/src/routes/_app._site.tsx', 'utf8');

  assert.equal(fs.existsSync('apps/hvac-web/src/app/RouteLoading.tsx'), true);
  const loginStateSource = appHostSource.match(/function LoginRequiredState[\s\S]*?function PrincipalUnavailableState/)?.[0];
  assert.ok(loginStateSource);
  assert.ok(appHostSource.includes('RouteLoading'));
  assert.equal(loginStateSource.includes('服务器 Session 已撤销'), false);
  assert.equal(loginStateSource.includes('RuntimeFacts'), false);

  assert.match(siteRouteSource, /return requireSite\(context\.runtime, search\.site\)/u);
  assert.match(siteRouteSource, /component: SiteWorkspaceLayout/u);
  assert.match(siteRouteSource, /<SiteRealtimeProvider>\s*<Outlet \/>\s*<\/SiteRealtimeProvider>/u);
});

test('holds the shell in BOOTSTRAPPING until Principal bootstrap completes', async () => {
  const pending = deferred<PlatformResponse<CurrentPrincipalResponse>>();
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({ getCurrentPrincipal: () => pending.promise }), env.value);

  const bootstrap = runtime.bootstrap('https://hvac.example/system?tab=overview');
  assert.equal(runtime.current().state, 'BOOTSTRAPPING');
  assert.equal(runtime.current().principal, undefined);

  pending.resolve(okResponse(principal()));
  await bootstrap;
  assert.equal(runtime.current().state, 'READY');
  assert.equal(runtime.current().principal?.principal.displayName, 'Operator One');
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
  assert.equal(runtime.current().principal?.session.id, 'session-1');
  assert.equal(runtime.current().logout?.status, 'failed');
  assert.equal(runtime.current().logout?.retryable, true);
});

test('purges protected memory only after confirmed or already-invalid logout', async () => {
  for (const scenario of [
    { logout: async () => okResponse(undefined, providerLogoutURL), navigations: [providerLogoutURL] },
    {
      logout: async () => { throw { problem: { status: 401, code: 'SESSION_INVALID', detail: 'already gone', traceId: '2'.repeat(32), retryable: false } }; },
      navigations: [] as string[],
    },
  ]) {
    const env = environment();
    const runtime = runtimeModule.createShellRuntime(client({ logout: scenario.logout }), env.value);
    await runtime.bootstrap('/system');
    const outcome = await runtime.logout();
    assert.equal(outcome, 'completed');
    assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
    assert.equal(runtime.current().principal, undefined);
    assert.deepEqual(env.navigations, scenario.navigations);
  }
});

test('session expiration purges protected memory and starts login again', async () => {
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client(), env.value);
  await runtime.bootstrap('/system');
  assert.equal(env.timers.size, 2);

  const expirationTimer = [...env.timers.values()].sort((left, right) => right.delay - left.delay)[0]!;
  expirationTimer.handler();
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
  assert.equal(env.timers.size, 2);

  runtime.dispose();
  runtime.purge('SESSION_EXPIRED', true);

  assert.equal(env.timers.size, 0);
  assert.deepEqual(env.navigations, []);
});

test('publishes available and degraded platform status without interpreting roles', async () => {
  for (const [status, expected] of [['ok', 'available'], ['degraded', 'degraded']] as const) {
    const env = environment();
    const runtime = runtimeModule.createShellRuntime(client({
      getPlatformStatus: async () => okResponse(platformStatus(status)),
    }), env.value);

    await runtime.bootstrap('/system');

    assert.equal(runtime.current().state, 'READY');
    assert.equal(runtime.current().platform?.state, expected);
    assert.equal(runtime.current().platform?.status?.status, status);
    assert.deepEqual(runtime.current().principal?.principal.roles, ['descriptive-only']);
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
  assert.equal(runtime.current().principal?.session.id, 'session-1');
  assert.equal(runtime.current().platform?.state, 'unavailable');
  assert.equal(runtime.current().platform?.failure?.code, 'PLATFORM_STATUS_UNAVAILABLE');
  assert.equal(runtime.current().platform?.failure?.retryable, true);
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
  const pendingPlatform = deferred<PlatformResponse<PlatformStatusResponse>>();
  const pendingLogout = deferred<PlatformResponse<undefined>>();
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    getPlatformStatus: () => pendingPlatform.promise,
    logout: () => pendingLogout.promise,
  }), env.value);

  const bootstrap = runtime.bootstrap('/system');
  for (let attempt = 0; attempt < 5 && !runtime.current().platform; attempt += 1) {
    await Promise.resolve();
  }
  assert.equal(runtime.current().platform?.state, 'checking');

  const logout = runtime.logout();
  assert.equal(runtime.current().logout?.status, 'submitting');

  pendingPlatform.resolve(okResponse(platformStatus()));
  await bootstrap;
  assert.equal(runtime.current().platform?.state, 'available');
  assert.equal(runtime.current().logout?.status, 'submitting');

  pendingLogout.resolve(okResponse(undefined, providerLogoutURL));
  await logout;
  assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
});

test('does not request Registry Sites without the effective site.list capability', async () => {
  let listCalls = 0;
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    getCurrentPrincipal: async () => okResponse(principal(undefined, ['site.read'])),
    listSites: async () => {
      listCalls += 1;
      return okResponse(siteCollection([registrySite()]));
    },
  }), env.value);

  await runtime.bootstrap('/overview');

  assert.equal(runtime.current().state, 'READY');
  assert.equal(runtime.current().sites?.state, 'forbidden');
  assert.equal(listCalls, 0);
});

test('completed Site discovery does not orphan a pending platform request', async () => {
  const pendingPlatform = deferred<PlatformResponse<PlatformStatusResponse>>();
  let platformSignal: AbortSignal | undefined;
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    getPlatformStatus: (options) => {
      platformSignal = options?.signal ?? undefined;
      return pendingPlatform.promise;
    },
    listSites: async () => okResponse(siteCollection([registrySite()])),
  }), env.value);

  void runtime.bootstrap('/overview');
  for (let attempt = 0; attempt < 10 && runtime.current().sites?.state !== 'available'; attempt += 1) {
    await Promise.resolve();
  }

  assert.equal(runtime.current().sites?.state, 'available');
  assert.equal(platformSignal?.aborted, false);
  runtime.dispose();
  assert.equal(platformSignal?.aborted, true);
});

test('Site discovery is not blocked by a pending platform status request', async () => {
  const pendingPlatform = deferred<PlatformResponse<PlatformStatusResponse>>();
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    getPlatformStatus: () => pendingPlatform.promise,
    listSites: async () => okResponse(siteCollection([registrySite()])),
  }), env.value);

  const bootstrap = runtime.bootstrap('/overview');
  for (let attempt = 0; attempt < 10 && runtime.current().sites?.state !== 'available'; attempt += 1) {
    await Promise.resolve();
  }

  assert.equal(runtime.current().sites?.state, 'available');
  assert.equal(runtime.current().platform?.state, 'checking');

  pendingPlatform.resolve(okResponse(platformStatus()));
  await bootstrap;
  assert.equal(runtime.current().platform?.state, 'available');
  assert.equal(runtime.current().sites?.state, 'available');
});

test('discovers all authorized Sites through bounded cursor pagination', async () => {
  const calls: Array<{ readonly limit?: number; readonly cursor?: string }> = [];
  const siteA = registrySite();
  const siteB = registrySite('01900000-0002-7000-8000-000000000002');
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    listSites: async (params) => {
      const normalizedParams = params ?? {};
      calls.push(normalizedParams);
      if (!normalizedParams.cursor) {
        return okResponse(siteCollection([siteA], 'cursor.page-two', true));
      }
      return okResponse(siteCollection([siteB], null, false));
    },
  }), env.value);

  await runtime.bootstrap('/overview');

  assert.equal(calls.length, 2);
  assert.equal(calls[0]?.limit, 100);
  assert.equal(calls[0]?.cursor, undefined);
  assert.equal(calls[1]?.limit, 100);
  assert.equal(calls[1]?.cursor, 'cursor.page-two');
  assert.equal(runtime.current().sites?.state, 'available');
  assert.equal(runtime.current().sites?.items?.length, 2);
  assert.equal(runtime.current().sites?.items?.[0]?.id, siteA.id);
  assert.equal(runtime.current().sites?.items?.[1]?.id, siteB.id);
});

test('an empty Registry collection becomes an explicit available zero-Site result', async () => {
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client(), env.value);

  await runtime.bootstrap('/overview');

  assert.equal(runtime.current().sites?.state, 'available');
  assert.equal(runtime.current().sites?.items?.length, 0);
});

test('rejects malformed, cross-Tenant, and duplicate Site discovery responses', async () => {
  const invalidCollections = [
    [registrySite('b1')],
    [registrySite('01900000-0001-7000-8000-000000000001', '01900000-0009-7000-8000-000000000009')],
    [registrySite(), registrySite()],
  ];

  for (const items of invalidCollections) {
    const env = environment();
    const runtime = runtimeModule.createShellRuntime(client({
      listSites: async () => okResponse(siteCollection(items)),
    }), env.value);

    await runtime.bootstrap('/overview');

    assert.equal(runtime.current().state, 'READY');
    assert.equal(runtime.current().sites?.state, 'unavailable');
    assert.equal(runtime.current().sites?.failure?.code, 'SITE_DISCOVERY_INVALID');
    assert.equal(runtime.current().sites?.items, undefined);
  }
});

test('an authentication failure during Site discovery purges protected memory', async () => {
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    listSites: async () => {
      throw { problem: { status: 401, code: 'SESSION_INVALID', detail: 'session invalid', traceId: '5'.repeat(32), retryable: false } };
    },
  }), env.value);

  await runtime.bootstrap('/overview');

  assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
  assert.equal(runtime.current().principal, undefined);
  assert.deepEqual(env.navigations, ['/api/v1/auth/login?returnTo=%2Foverview']);
});

test('late Site discovery cannot reset an in-flight logout', async () => {
  const pendingSites = deferred<PlatformResponse<SiteCollection>>();
  const pendingLogout = deferred<PlatformResponse<undefined>>();
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    listSites: () => pendingSites.promise,
    logout: () => pendingLogout.promise,
  }), env.value);

  const bootstrap = runtime.bootstrap('/overview');
  for (let attempt = 0; attempt < 10 && runtime.current().sites?.state !== 'checking'; attempt += 1) {
    await Promise.resolve();
  }
  assert.equal(runtime.current().sites?.state, 'checking');

  const logout = runtime.logout();
  assert.equal(runtime.current().logout?.status, 'submitting');

  pendingSites.resolve(okResponse(siteCollection([registrySite()])));
  await bootstrap;
  assert.equal(runtime.current().sites?.state, 'available');
  assert.equal(runtime.current().logout?.status, 'submitting');

  pendingLogout.resolve(okResponse(undefined, providerLogoutURL));
  await logout;
  assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
});

test('same-Site route navigation does not invoke a Site-change purge', async () => {
  const site = registrySite();
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    listSites: async () => okResponse(siteCollection([site])),
  }), env.value);
  await runtime.bootstrap(`/operations/systems-devices?site=${site.id}`);
  runtime.activateSiteScope(site.id);
  const request = runtime.protectedRequestToken();

  const outcome = await runtime.requestSiteNavigation(`/operations/alarms?site=${site.id}`);

  assert.equal(outcome, 'navigated');
  assert.equal(request.signal.aborted, false);
  assert.equal(runtime.current().protectedScope?.siteId, site.id);
  assert.deepEqual(env.navigations, [`/operations/alarms?site=${site.id}`]);
});

test('cross-Site navigation purges before navigation', async () => {
  const siteA = registrySite();
  const siteB = registrySite('01900000-0002-7000-8000-000000000002');
  const events: string[] = [];
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    listSites: async () => okResponse(siteCollection([siteA, siteB])),
  }), env.value);
  await runtime.bootstrap(`/operations/systems-devices?site=${siteA.id}`);
  runtime.activateSiteScope(siteA.id);
  runtime.registerProtectedResource({
    id: 's2-session',
    kind: 'realtime',
    purge: (reason) => {
      events.push(`realtime:${reason}`);
    },
  });
  runtime.registerProtectedResource({
    id: 'query-cache',
    kind: 'query-cache',
    purge: (reason) => {
      events.push(`cache:${reason}`);
    },
  });
  const request = runtime.protectedRequestToken();
  const target = `/operations/systems-devices?site=${siteB.id}`;

  assert.equal(await runtime.requestSiteNavigation(target), 'navigated');
  assert.equal(request.signal.aborted, true);
  assert.deepEqual(events, ['realtime:SITE_CHANGE', 'cache:SITE_CHANGE']);
  assert.deepEqual(env.navigations, [target]);
  assert.equal(runtime.current().siteTransition, undefined);
  assert.equal(runtime.current().protectedScope?.state, 'idle');
  assert.equal(runtime.current().protectedScope?.siteId, undefined);
});

test('failed Site purge blocks navigation and exposes a fail-closed transition', async () => {
  const siteA = registrySite();
  const siteB = registrySite('01900000-0002-7000-8000-000000000002');
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    listSites: async () => okResponse(siteCollection([siteA, siteB])),
  }), env.value);
  await runtime.bootstrap(`/operations/systems-devices?site=${siteA.id}`);
  runtime.activateSiteScope(siteA.id);
  runtime.registerProtectedResource({
    id: 's2-session',
    kind: 'realtime',
    purge: () => { throw new Error('close failed'); },
  });

  const outcome = await runtime.requestSiteNavigation(`/operations/systems-devices?site=${siteB.id}`);

  assert.equal(outcome, 'failed');
  assert.equal(runtime.current().siteTransition?.status, 'failed');
  assert.equal(runtime.current().siteTransition?.failure?.code, 'PROTECTED_SCOPE_PURGE_FAILED');
  assert.equal(runtime.current().protectedScope?.siteId, undefined);
  assert.equal(env.navigations.length, 0);
});

test('Session loss and logout completion invoke the same protected purge semantics', async () => {
  for (const mode of ['session-loss', 'logout'] as const) {
    const site = registrySite();
    const events: string[] = [];
    const env = environment();
    const runtime = runtimeModule.createShellRuntime(client({
      listSites: async () => okResponse(siteCollection([site])),
    }), env.value);
    await runtime.bootstrap(`/operations/systems-devices?site=${site.id}`);
    runtime.activateSiteScope(site.id);
    const request = runtime.protectedRequestToken();
    runtime.registerProtectedResource({
      id: 'site-cache',
      kind: 'query-cache',
      purge: (reason) => {
        events.push(reason);
      },
    });

    if (mode === 'logout') await runtime.logout();
    else runtime.purge('SESSION_REVOKED', false);
    await Promise.resolve();

    assert.equal(request.signal.aborted, true, mode);
    assert.deepEqual(events, [mode === 'logout' ? 'LOGOUT' : 'SESSION_LOSS'], mode);
    assert.equal(runtime.current().state, 'LOGIN_REQUIRED', mode);
  }
});

test('material policy revision changes purge scope and rebootstrap the Principal', async () => {
  const site = registrySite();
  const events: string[] = [];
  let policyRevision = 'iam:1';
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    getCurrentPrincipal: async () => {
      const response = principal();
      response.authorization.policyRevision = policyRevision;
      return okResponse(response);
    },
    listSites: async () => okResponse(siteCollection([site])),
  }), env.value);
  await runtime.bootstrap(`/operations/systems-devices?site=${site.id}`);
  runtime.activateSiteScope(site.id);
  runtime.registerProtectedResource({
    id: 'selected-device',
    kind: 'selection',
    purge: (reason) => {
      events.push(reason);
    },
  });

  assert.equal(await runtime.handlePolicyRevision('iam:1'), 'unchanged');
  policyRevision = 'iam:2';
  assert.equal(await runtime.handlePolicyRevision('iam:2'), 'reloaded');

  assert.deepEqual(events, ['POLICY_CHANGE']);
  assert.equal(runtime.current().principal?.authorization.policyRevision, 'iam:2');
});

test('Session loss interrupts a pending Site purge before target navigation', async () => {
  const siteA = registrySite();
  const siteB = registrySite('01900000-0002-7000-8000-000000000002');
  const pendingPurge = deferred();
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    listSites: async () => okResponse(siteCollection([siteA, siteB])),
  }), env.value);
  await runtime.bootstrap(`/operations/systems-devices?site=${siteA.id}`);
  runtime.activateSiteScope(siteA.id);
  runtime.registerProtectedResource({
    id: 's2-session',
    kind: 'realtime',
    purge: () => pendingPurge.promise,
  });

  const navigation = runtime.requestSiteNavigation(`/operations/systems-devices?site=${siteB.id}`);
  assert.equal(runtime.current().siteTransition?.status, 'purging');

  runtime.purge('SESSION_REVOKED', false);
  assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
  pendingPurge.resolve();

  assert.equal(await navigation, 'interrupted');
  assert.equal(env.navigations.length, 0);
});

test('realtime status is scoped to the active Site and clears before Site navigation', async () => {
  const siteA = registrySite();
  const siteB = registrySite('01900000-0002-7000-8000-000000000002');
  const pendingPurge = deferred();
  const env = environment();
  const runtime = runtimeModule.createShellRuntime(client({
    listSites: async () => okResponse(siteCollection([siteA, siteB])),
  }), env.value);
  await runtime.bootstrap(`/operations/systems-devices?site=${siteA.id}`);
  runtime.activateSiteScope(siteA.id);

  assert.equal(runtime.current().realtime?.state, 'idle');
  assert.equal(runtime.current().realtime?.siteId, siteA.id);
  for (const state of ['connecting', 'live', 'reconnecting', 'resync-required', 'unavailable'] as const) {
    runtime.publishRealtimeStatus({ source: 'plant', state, siteId: siteA.id });
    assert.equal(runtime.current().realtime?.state, state);
    assert.equal(runtime.current().realtime?.siteId, siteA.id);
  }
  assert.throws(
    () => runtime.publishRealtimeStatus({ source: 'plant', state: 'live', siteId: siteB.id }),
    /active Site/i,
  );

  runtime.registerProtectedResource({
    id: 's2-session',
    kind: 'realtime',
    purge: () => pendingPurge.promise,
  });
  const navigation = runtime.requestSiteNavigation(`/operations/systems-devices?site=${siteB.id}`);
  assert.equal(runtime.current().realtime?.state, 'idle');
  assert.equal(runtime.current().realtime?.siteId, undefined);
  pendingPurge.resolve();
  assert.equal(await navigation, 'navigated');
});

test('the header shows the most severe open subscription and returns to idle once all are released', async () => {
  const site = registrySite();
  const runtime = runtimeModule.createShellRuntime(client({
    listSites: async () => okResponse(siteCollection([site])),
  }), environment().value);
  await runtime.bootstrap(`/operations/realtime?site=${site.id}`);
  runtime.activateSiteScope(site.id);

  runtime.publishRealtimeStatus({ source: 'overview', state: 'live', siteId: site.id });
  runtime.publishRealtimeStatus({ source: 'breakdown', state: 'reconnecting', siteId: site.id });
  assert.equal(runtime.current().realtime?.state, 'reconnecting');

  runtime.publishRealtimeStatus({ source: 'breakdown', state: 'live', siteId: site.id });
  assert.equal(runtime.current().realtime?.state, 'live');

  // Leaving one view keeps the other's state; leaving the last one is not a fault.
  runtime.releaseRealtimeStatus('breakdown');
  assert.equal(runtime.current().realtime?.state, 'live');
  runtime.releaseRealtimeStatus('overview');
  assert.equal(runtime.current().realtime?.state, 'idle');
  assert.equal(runtime.current().realtime?.siteId, site.id);

  // A release after the source is gone (unmount after purge) changes nothing.
  runtime.releaseRealtimeStatus('overview');
  assert.equal(runtime.current().realtime?.state, 'idle');
});
