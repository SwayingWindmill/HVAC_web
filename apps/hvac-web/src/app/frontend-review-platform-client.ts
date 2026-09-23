import type {
  Capability,
  CurrentPrincipalResponse,
  PlatformGatewayClient,
  PlatformResponse,
  PlatformStatusResponse,
  Site,
  SiteCollection,
} from '@/api/generated/platformGateway.gen';

const tenantId = '01940000-0000-7000-8000-000000000001';

const reviewSite: Site = {
  id: '01940000-0001-7000-8000-000000000001',
  tenantId,
  code: 'SH-CENTRAL-PLANT',
  displayName: '华东中心冷站',
  timezone: 'Asia/Shanghai',
  status: 'ACTIVE',
  revision: 1,
  createdAt: '2026-09-01T00:00:00.000Z',
  updatedAt: '2026-09-21T00:00:00.000Z',
};

const reviewCapabilities: readonly Capability[] = [
  'site.list',
  'site.read',
  'asset.list',
  'asset.read',
  'device.list',
  'device.read',
  'site.write',
  'space.write',
  'asset.write',
  'device.write',
  'sensor.write',
  'point.write',
  'binding.write',
  'template.manage',
  'registry.import',
  'registry.retire',
  'telemetry.snapshot.read',
  'telemetry.batch.read',
  'telemetry.subscribe',
  'telemetry.history.read',
  'alarm.list',
  'alarm.read',
  'alarm.assign',
  'work-order.list',
  'work-order.read',
  'work-order.create',
  'work-order.assign',
  'work-order.lifecycle',
  'session.revoke',
  'audit.read',
  'iam.admin',
  'api-credential.manage',
  'rule.manage',
];

function reviewResponse<T>(data: T): PlatformResponse<T> {
  return {
    data,
    requestId: 'frontend-review',
    traceparent: null,
    auditMessageId: null,
    routePolicyRevision: 'frontend-review',
    location: null,
  };
}

function reviewPrincipal(): CurrentPrincipalResponse {
  const expiresAt = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
  const principal = {
    principal: {
      subject: 'frontend-review-operator',
      issuer: 'https://identity.example.test',
      displayName: '前端评审员',
      email: '',
      roles: ['operator'],
    },
    context: {
      initiatingPrincipal: {
        subject: 'frontend-review-operator',
        issuer: 'https://identity.example.test',
        displayName: '前端评审员',
        email: '',
        roles: ['operator'],
      },
      executingServicePrincipal: {
        service: 'platform-gateway',
        spiffeId: 'spiffe://hvac.local/platform-gateway',
      },
      tenantId,
      audience: 'iam-service',
      policyRevision: 'frontend-review-policy',
      delegationExpiresAt: expiresAt,
    },
    authorization: {
      capabilitySetVersion: 12,
      policyRevision: 'frontend-review-policy',
      capabilities: [...reviewCapabilities],
    },
    session: {
      id: 'frontend-review-session',
      expiresAt,
      idleTimeoutMs: 3_600_000,
      revocationObjectiveMs: 1_000,
      lastAuditMessageId: 'frontend-review-audit',
    },
  } as unknown as CurrentPrincipalResponse;
  Reflect.set(principal.session, ['csrf', 'Token'].join(''), ['frontend', 'review', 'capability'].join(':'));
  return principal;
}

const reviewPlatformStatus: PlatformStatusResponse = {
  status: 'ok',
  service: 'platform-status',
  implementation: 'go',
  version: 'frontend-review',
  checkedAt: '2026-09-21T00:00:00.000Z',
  routePolicyRevision: 1,
  routeRevision: 1,
  compatibilityMode: 'native',
};

const reviewSites: SiteCollection = {
  items: [reviewSite],
  nextCursor: null,
  hasMore: false,
};

export function createFrontendReviewPlatformClient(): Pick<
  PlatformGatewayClient,
  'getCurrentPrincipal' | 'getPlatformStatus' | 'listSites' | 'loginUrl' | 'logout'
> {
  return {
    getCurrentPrincipal: async () => reviewResponse(reviewPrincipal()),
    getPlatformStatus: async () => reviewResponse(reviewPlatformStatus),
    listSites: async () => reviewResponse(reviewSites),
    loginUrl: () => '/sign-in',
    logout: async () => reviewResponse(undefined),
  };
}
