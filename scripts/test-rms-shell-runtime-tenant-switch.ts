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
  TenantSwitchOutcome,
} from '../apps/hvac-web/src/app/shell-runtime.ts';
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

const policy = evaluateModule(compileTsModule('apps/hvac-web/src/app/shell-policy.ts'));
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
