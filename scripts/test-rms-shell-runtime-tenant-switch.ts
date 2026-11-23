import assert from 'node:assert/strict';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';
import type {
  createShellRuntime,
  ShellRuntime,
  ShellSnapshot,
  TenantSwitchOutcome,
} from '../apps/hvac-web/src/app/shell-runtime.ts';
import type { shouldInvalidateRouter } from '../apps/hvac-web/src/app/shell-policy.ts';
import type {
  APP_NAVIGATION_CONFIG,
  canAccessNavItem,
  filterNavigationGroups,
  navCountQueryFlags,
  navHref,
} from '../apps/hvac-web/src/components/layout/app-navigation.ts';
import type {
  currentPrincipalResponseSchema,
  CurrentPrincipalResponse,
  PlatformStatusResponse,
  SiteCollection,
} from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';

type ShellClient = Parameters<typeof createShellRuntime>[0];
type ShellEnv = Parameters<typeof createShellRuntime>[1];

function compileTsModule(file: string): { readonly sourcePath: string; readonly code: string } {
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

function evaluateModule(
  compiled: { readonly sourcePath: string; readonly code: string },
  requireFn: (specifier: string) => unknown = (specifier) => {
    throw new Error(`unexpected require: ${specifier}`);
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

const policy = evaluateModule(compileTsModule('apps/hvac-web/src/app/shell-policy.ts')) as {
  readonly shouldInvalidateRouter: typeof shouldInvalidateRouter;
};
const navExports = evaluateModule(
  compileTsModule('apps/hvac-web/src/components/layout/app-navigation.ts'),
  (specifier) => {
    if (specifier === 'lucide-react') {
      return new Proxy({}, { get: () => () => null });
    }
    throw new Error(`unexpected require: ${specifier}`);
  },
) as {
  readonly APP_NAVIGATION_CONFIG: typeof APP_NAVIGATION_CONFIG;
  readonly canAccessNavItem: typeof canAccessNavItem;
  readonly filterNavigationGroups: typeof filterNavigationGroups;
  readonly navHref: typeof navHref;
  readonly navCountQueryFlags: typeof navCountQueryFlags;
};
const routerPaths = evaluateModule(compileTsModule('apps/hvac-web/src/app/router-paths.ts'));
const protectedScope = evaluateModule(compileTsModule('apps/hvac-web/src/app/protected-scope.ts'));
const realtimeStatus = evaluateModule(
  compileTsModule('apps/hvac-web/src/app/realtime-status.ts'),
  (specifier) => {
    if (specifier === './router-paths') return routerPaths;
    throw new Error(`unexpected require: ${specifier}`);
  },
);
const runtimeExports = evaluateModule(
  compileTsModule('apps/hvac-web/src/app/shell-runtime.ts'),
  (specifier) => {
    if (specifier === './shell-policy') return policy;
    if (specifier === './router-paths') return routerPaths;
    if (specifier === './protected-scope') return protectedScope;
    if (specifier === './realtime-status') return realtimeStatus;
    throw new Error(`unexpected require: ${specifier}`);
  },
) as { readonly createShellRuntime: typeof createShellRuntime };

const TENANT_A = '01900000-0000-7000-8000-000000000001';
const TENANT_B = '01900000-0000-7000-8000-000000000002';
const CSRF_TOKEN = '[REDACTED_SECRET]';

function samplePrincipal(tenantId = TENANT_A): CurrentPrincipalResponse {
  const user = {
    subject: 'operator-1',
    issuer: 'https://identity.example',
    displayName: '值班工程师',
    email: 'operator@hvac.example',
    roles: ['operator'],
  };
  return {
    principalId: '018f3e00-2000-7000-8000-000000000001',
    principal: user,
    context: {
      initiatingPrincipal: user,
      executingServicePrincipal: { service: 'platform-gateway', spiffeId: 'spiffe://hvac/platform-gateway' },
      tenantId,
      audience: 'iam-service',
      policyRevision: 'registry-read:7',
      delegationExpiresAt: '2026-07-28T06:00:00.000Z',
    },
    authorization: {
      capabilitySetVersion: 14,
      policyRevision: 'registry-read:7',
      capabilities: ['site.list', 'site.read'],
    },
    session: {
      id: 'session-1',
      expiresAt: '2026-07-28T06:00:00.000Z',
      idleTimeoutMs: 1_800_000,
      csrfToken: CSRF_TOKEN,
      revocationObjectiveMs: 1000,
      lastAuditMessageId: 'audit-1',
    },
    availableTenants: [
      { tenantId: TENANT_A, code: 'alpha', displayName: '松雅轩一期' },
      { tenantId: TENANT_B, code: 'beta', displayName: '麓谷冷站' },
    ],
  };
}

function sampleSite(id: string, tenantId: string) {
  return {
    id,
    tenantId,
    code: `SITE-${id.slice(-4)}`,
    displayName: `Site ${id.slice(-4)}`,
    timezone: 'Asia/Tokyo',
    status: 'ACTIVE' as const,
    revision: 1,
    createdAt: '2026-07-28T00:00:00.000Z',
    updatedAt: '2026-07-28T00:00:00.000Z',
    floorAreaM2: null,
    climateZone: null,
  };
}

function samplePlatformStatus(): PlatformStatusResponse {
  return {
    status: 'ok',
    service: 'platform-status',
    implementation: 'go',
    version: 'rms-04-test',
    checkedAt: '2026-07-28T05:00:00.000Z',
    routePolicyRevision: 7,
    routeRevision: 11,
    compatibilityMode: 'native',
  };
}

function createEnv() {
  const navigations: string[] = [];
  const timers = new Map<unknown, () => void>();
  let nextTimer = 0;
  const value: ShellEnv = {
    origin: 'https://hvac.example',
    now: () => Date.parse('2026-07-28T05:00:00.000Z'),
    navigate: (target) => { navigations.push(target); },
    postNavigate: (target) => { navigations.push(target); },
    setTimer: (handler) => {
      const id = ++nextTimer;
      timers.set(id, handler);
      return id;
    },
    clearTimer: (id) => { timers.delete(id); },
  };
  return { navigations, timers, value };
}

function createClient(overrides: Partial<ShellClient> = {}): ShellClient {
  return {
    getCurrentPrincipal: async () => ({
      data: samplePrincipal(),
      requestId: null,
      traceparent: null,
      auditMessageId: null,
      routePolicyRevision: null,
      location: null,
    }),
    getPlatformStatus: async () => ({
      data: samplePlatformStatus(),
      requestId: null,
      traceparent: null,
      auditMessageId: null,
      routePolicyRevision: null,
      location: null,
    }),
    listSites: async () => ({
      data: { items: [], nextCursor: null, hasMore: false } satisfies SiteCollection,
      requestId: null,
      traceparent: null,
      auditMessageId: null,
      routePolicyRevision: null,
      location: null,
    }),
    loginUrl: (params) => `/api/v1/auth/login?returnTo=${encodeURIComponent(params?.returnTo ?? '/')}`,
    logout: async () => ({
      data: undefined,
      requestId: null,
      traceparent: null,
      auditMessageId: null,
      routePolicyRevision: null,
      location: 'https://identity.example/logout',
    }),
    ...overrides,
  };
}

test('switchTenant switches active organization, purges protected resources, clears site/inspect params, and refreshes sites', async () => {
  const siteA = sampleSite('01900000-0001-7000-8000-000000000001', TENANT_A);
  const siteB = sampleSite('01900000-0002-7000-8000-000000000002', TENANT_B);
  const events: string[] = [];
  const env = createEnv();
  let activeTenant = TENANT_A;
  const switchCalls: Array<{ tenantId: string; csrfToken: string }> = [];

  const runtime: ShellRuntime = runtimeExports.createShellRuntime(
    createClient({
      getCurrentPrincipal: async () => ({
        data: samplePrincipal(activeTenant),
        requestId: null,
        traceparent: null,
        auditMessageId: null,
        routePolicyRevision: null,
        location: null,
      }),
      listSites: async () => ({
        data: {
          items: activeTenant === TENANT_A ? [siteA] : [siteB],
          nextCursor: null,
          hasMore: false,
        },
        requestId: null,
        traceparent: null,
        auditMessageId: null,
        routePolicyRevision: null,
        location: null,
      }),
      switchActiveTenant: async (tenantId, csrfToken) => {
        switchCalls.push({ tenantId, csrfToken });
        activeTenant = tenantId;
        events.push(`api:${tenantId}`);
        return {
          data: samplePrincipal(tenantId),
          requestId: null,
          traceparent: null,
          auditMessageId: null,
          routePolicyRevision: null,
          location: null,
        };
      },
    }),
    env.value,
  );

  await runtime.bootstrap(`/overview?site=${siteA.id}`);
  const initialSnapshot = runtime.current();
  assert.equal(initialSnapshot.state, 'READY');
  assert.equal(initialSnapshot.sites?.state, 'available');
  assert.deepEqual([...(initialSnapshot.sites?.items ?? []).map((site) => site.id)], [siteA.id]);
  runtime.activateSiteScope(siteA.id);
  runtime.publishRealtimeStatus({ source: 'overview', state: 'live', siteId: siteA.id });
  assert.equal(runtime.current().realtime?.state, 'live');
  assert.equal(runtime.current().realtime?.siteId, siteA.id);

  runtime.registerProtectedResource({
    id: 'ws-tenant-a',
    kind: 'realtime',
    purge: () => { events.push('purge'); },
  });

  const unchanged: TenantSwitchOutcome = await runtime.switchTenant(TENANT_A);
  assert.equal(unchanged.state, 'unchanged');
  assert.equal(switchCalls.length, 0);

  const outcome = await runtime.switchTenant(TENANT_B, {
    currentHref: `https://hvac.example/overview?site=${siteA.id}&inspect=device-1&tab=cop`,
    onSwitched: () => { events.push('query-clear'); },
  });

  assert.equal(outcome.state, 'switched');
  if (outcome.state === 'switched') {
    assert.equal(outcome.target, '/overview?tab=cop');
  }
  assert.deepEqual(switchCalls, [{ tenantId: TENANT_B, csrfToken: CSRF_TOKEN }]);
  assert.deepEqual(events, [`api:${TENANT_B}`, 'purge', 'query-clear']);
  const nextSnapshot = runtime.current();
  assert.equal(nextSnapshot.principal?.context.tenantId, TENANT_B);
  assert.equal(nextSnapshot.realtime?.state, 'idle');
  assert.equal(nextSnapshot.realtime?.siteId, undefined);
  assert.equal(nextSnapshot.sites?.state, 'available');
  assert.deepEqual([...(nextSnapshot.sites?.items ?? []).map((site) => site.id)], [siteB.id]);
  assert.deepEqual(env.navigations, ['/overview?tab=cop']);

  runtime.beginLogin();
  assert.equal(env.navigations.at(-1), '/api/v1/auth/login?returnTo=%2Foverview%3Ftab%3Dcop');
  runtime.dispose();
});

test('switchTenant preserves current state when backend refuses with 403 and logs out on 401', async () => {
  const env = createEnv();
  let nextProblem = {
    type: 'about:blank',
    title: 'Forbidden',
    status: 403,
    detail: 'Not a member',
    instance: '/api/v1/auth/session/tenant',
    code: 'TENANT_SWITCH_FORBIDDEN',
    traceId: '0'.repeat(32),
    retryable: false,
  };

  const runtime = runtimeExports.createShellRuntime(
    createClient({
      switchActiveTenant: async () => {
        throw { problem: nextProblem };
      },
    }),
    env.value,
  );

  await runtime.bootstrap('/system?tab=users');
  let cleared = false;
  const rejected = await runtime.switchTenant(TENANT_B, {
    onSwitched: () => { cleared = true; },
  });
  assert.equal(rejected.state, 'rejected');
  if (rejected.state === 'rejected') {
    assert.equal(rejected.code, 'TENANT_SWITCH_FORBIDDEN');
  }
  assert.equal(cleared, false);
  assert.equal(runtime.current().state, 'READY');
  assert.equal(runtime.current().principal?.context.tenantId, TENANT_A);

  nextProblem = {
    ...nextProblem,
    status: 401,
    title: 'Unauthorized',
    code: 'SESSION_INVALID',
  };
  const expired = await runtime.switchTenant(TENANT_B);
  assert.equal(expired.state, 'rejected');
  assert.equal(runtime.current().state, 'LOGIN_REQUIRED');
  runtime.dispose();
});

test('principal response validates availableTenants metadata and rejects malformed entries', () => {
  const gatewayExports = evaluateModule(
    compileTsModule('apps/hvac-web/src/api/generated/platformGateway.gen.ts'),
    createRequire(import.meta.url),
  ) as { readonly currentPrincipalResponseSchema: typeof currentPrincipalResponseSchema };
  const schema = gatewayExports.currentPrincipalResponseSchema;
  const base = samplePrincipal();

  assert.equal(
    schema.safeParse({
      ...base,
      availableTenants: [
        {
          tenantId: TENANT_A,
          code: 'songyaxuan',
          displayName: '松雅轩',
        },
      ],
    }).success,
    true,
  );

  assert.equal(
    schema.safeParse({
      ...base,
      availableTenants: [
        {
          tenantId: 'not-a-uuid',
          code: 'songyaxuan',
          displayName: '松雅轩',
        },
      ],
    }).success,
    false,
  );

  assert.equal(
    schema.safeParse({
      ...base,
      availableTenants: [
        {
          tenantId: TENANT_A,
          code: 'songyaxuan',
        },
      ],
    }).success,
    false,
  );
});

test('shouldInvalidateRouter ignores realtime, protectedScope, logout, and transition noise once READY', async () => {
  const siteA = sampleSite('01900000-0001-7000-8000-000000000001', TENANT_A);
  const env = createEnv();
  const runtime: ShellRuntime = runtimeExports.createShellRuntime(
    createClient({
      listSites: async () => ({
        data: { items: [siteA], nextCursor: null, hasMore: false },
        requestId: null,
        traceparent: null,
        auditMessageId: null,
        routePolicyRevision: null,
        location: null,
      }),
    }),
    env.value,
  );

  let invalidations = 0;
  let previous = runtime.current();
  const unsubscribe = runtime.subscribe((next) => {
    if (policy.shouldInvalidateRouter(previous, next)) invalidations += 1;
    previous = next;
  });

  await runtime.bootstrap(`/overview?site=${siteA.id}`);
  assert.equal(invalidations, 1);

  invalidations = 0;
  runtime.activateSiteScope(siteA.id);
  const release = runtime.registerProtectedResource({ id: 'ws-1', kind: 'realtime', purge: () => {} });
  runtime.publishRealtimeStatus({ source: 'overview', state: 'connecting', siteId: siteA.id });
  runtime.publishRealtimeStatus({ source: 'overview', state: 'live', siteId: siteA.id });
  runtime.releaseRealtimeStatus('overview');
  release();
  assert.equal(invalidations, 0);

  const readySnapshot = runtime.current();
  const nextLogout: ShellSnapshot = { ...readySnapshot, logout: { status: 'submitting' } };
  assert.equal(policy.shouldInvalidateRouter(readySnapshot, nextLogout), false);
  assert.equal(
    policy.shouldInvalidateRouter(
      { ...readySnapshot, platform: { state: 'checking' } },
      { ...readySnapshot, platform: { state: 'available' } },
    ),
    true,
  );
  assert.equal(
    policy.shouldInvalidateRouter(readySnapshot, {
      ...readySnapshot,
      principal: {
        ...readySnapshot.principal!,
        authorization: {
          ...readySnapshot.principal!.authorization,
          policyRevision: 'registry-read:8',
        },
      },
    }),
    true,
  );
  assert.equal(
    policy.shouldInvalidateRouter(readySnapshot, {
      ...readySnapshot,
      principal: {
        ...readySnapshot.principal!,
        authorization: {
          ...readySnapshot.principal!.authorization,
          capabilities: ['site.list', 'site.read', 'alarm.list'],
        },
      },
    }),
    true,
  );
  assert.equal(policy.shouldInvalidateRouter(undefined, { state: 'BOOTSTRAPPING' }), false);
  assert.equal(
    policy.shouldInvalidateRouter(undefined, { state: 'READY', sites: { state: 'checking' } }),
    false,
  );

  unsubscribe();
  runtime.dispose();
});

test('navHref omits ?site= on platform routes and navCountQueryFlags gates queries by capability', () => {
  const siteId = '01900000-0001-7000-8000-000000000001';
  assert.equal(navExports.navHref({ path: '/overview', scope: 'site' }, siteId), `/overview?site=${siteId}`);
  assert.equal(navExports.navHref({ path: '/operations/alarms' }, siteId), `/operations/alarms?site=${siteId}`);
  assert.equal(navExports.navHref({ path: '/operations/control' }, undefined), '/operations/control');
  assert.equal(navExports.navHref({ path: '/system', scope: 'platform' }, siteId), '/system');
  assert.equal(navExports.navHref({ path: '/benchmark', scope: 'platform' }, siteId), '/benchmark');
  assert.equal(navExports.navHref({ path: '/settings/integrations', scope: 'platform' }, siteId), '/settings/integrations');

  assert.deepEqual({ ...navExports.navCountQueryFlags(undefined, ['alarm.list', 'work-order.list', 'site.read']) }, {
    alarms: false,
    workOrders: false,
    commands: false,
  });
  assert.deepEqual({ ...navExports.navCountQueryFlags(siteId, ['site.read']) }, {
    alarms: false,
    workOrders: false,
    commands: true,
  });
  assert.deepEqual({ ...navExports.navCountQueryFlags(siteId, ['alarm.list', 'work-order.list', 'site.read']) }, {
    alarms: true,
    workOrders: true,
    commands: true,
  });
});

test('filterNavigationGroups hides entries whose capabilities are missing, strips sidebar-only hidden items, and drops empty groups', () => {
  const fullCapabilities = [
    'site.list',
    'site.read',
    'device.list',
    'device.read',
    'asset.read',
    'telemetry.snapshot.read',
    'telemetry.history.read',
    'alarm.list',
    'work-order.list',
  ];

  const fullSidebarIds = navExports
    .filterNavigationGroups(navExports.APP_NAVIGATION_CONFIG, fullCapabilities, { sidebarOnly: true })
    .flatMap((group) => group.items.map((item) => item.id));
  assert.ok(fullSidebarIds.includes('operations-alarms'));
  assert.ok(fullSidebarIds.includes('operations-work-center'));
  assert.ok(fullSidebarIds.includes('integrations'));
  assert.equal(fullSidebarIds.includes('notifications'), false);

  const commandSearchIds = navExports
    .filterNavigationGroups(navExports.APP_NAVIGATION_CONFIG, fullCapabilities)
    .flatMap((group) => group.items.map((item) => item.id));
  assert.ok(commandSearchIds.includes('notifications'));

  // Denying alarm.list and work-order.list keeps 故障诊断 under operations and drops alarms/notifications/work-center.
  const withoutAlarmsAndOrders = navExports.filterNavigationGroups(
    navExports.APP_NAVIGATION_CONFIG,
    fullCapabilities.filter((cap) => cap !== 'alarm.list' && cap !== 'work-order.list'),
  );
  const opsGroup = withoutAlarmsAndOrders.find((group) => group.id === 'operations');
  assert.deepEqual(
    Array.from(opsGroup?.items ?? [], (item) => item.id),
    ['operations-diagnostics'],
  );

  // Denying both device.read and asset.read hides 集成管理 (OR requirement).
  const withoutIntegrations = navExports.filterNavigationGroups(
    navExports.APP_NAVIGATION_CONFIG,
    ['site.list', 'site.read'],
    { sidebarOnly: true },
  );
  const platformIds = Array.from(withoutIntegrations)
    .filter((group) => group.section === 'platform')
    .flatMap((group) => Array.from(group.items, (item) => item.id));
  assert.deepEqual(platformIds, ['system']);

  // Empty capabilities drops all navigation groups cleanly.
  assert.deepEqual(Array.from(navExports.filterNavigationGroups(navExports.APP_NAVIGATION_CONFIG, [])), []);
  assert.equal(navExports.canAccessNavItem({ requiredCapabilities: ['site.read'] }, undefined), false);
});

test('remembered lastSiteId survives leaving site scope for platform routes and clears on logout', async () => {
  const siteA = sampleSite('01900000-0001-7000-8000-000000000001', TENANT_A);
  const siteB = sampleSite('01900000-0002-7000-8000-000000000002', TENANT_A);
  const runtime = runtimeExports.createShellRuntime(
    createClient({
      listSites: async () => ({
        data: { items: [siteA, siteB], nextCursor: null, hasMore: false },
        requestId: null,
        traceparent: null,
        auditMessageId: null,
        routePolicyRevision: null,
        location: null,
      }),
    }),
    createEnv().value,
  );
  await runtime.bootstrap(`/overview?site=${siteB.id}`);
  runtime.activateSiteScope(siteB.id);
  assert.equal(runtime.current().lastSiteId, siteB.id);

  const outcome = await runtime.requestSiteNavigation('/benchmark');
  assert.equal(outcome, 'navigated');
  assert.equal(runtime.current().protectedScope?.siteId, undefined);
  assert.equal(runtime.current().lastSiteId, siteB.id);

  await runtime.logout();
  assert.equal(runtime.current().lastSiteId, undefined);
});

test('performSiteNavigation keeps purging state until an async environment.navigate resolves', async () => {
  const siteA = sampleSite('01900000-0001-7000-8000-000000000001', TENANT_A);
  const siteB = sampleSite('01900000-0002-7000-8000-000000000002', TENANT_A);
  let resolveNavigate: (() => void) | undefined;
  const pendingNavigate = new Promise<void>((resolve) => {
    resolveNavigate = resolve;
  });
  const baseEnv = createEnv();
  const runtime = runtimeExports.createShellRuntime(
    createClient({
      listSites: async () => ({
        data: { items: [siteA, siteB], nextCursor: null, hasMore: false },
        requestId: null,
        traceparent: null,
        auditMessageId: null,
        routePolicyRevision: null,
        location: null,
      }),
    }),
    {
      ...baseEnv.value,
      navigate: async (target) => {
        baseEnv.navigations.push(target);
        await pendingNavigate;
      },
    },
  );
  await runtime.bootstrap(`/overview?site=${siteA.id}`);
  runtime.activateSiteScope(siteA.id);

  const navigation = runtime.requestSiteNavigation(`/overview?site=${siteB.id}`);
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(runtime.current().siteTransition?.status, 'purging');

  resolveNavigate?.();
  assert.equal(await navigation, 'navigated');
  assert.equal(runtime.current().siteTransition, undefined);
});
