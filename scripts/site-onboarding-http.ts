import { existsSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { buildPointSourceMetadata } from './lib/site-onboarding-control.ts';
import {
  loadDeviceTemplateCatalog,
  type DeviceTemplateCatalogEntry,
} from './lib/site-onboarding-templates.ts';
import {
  checkSiteFile,
  expandPoints,
  pointStatus,
  type OnboardingDevice,
  type OnboardingPoint,
  type OnboardingProfile,
} from './site-onboarding.mjs';

export interface RegistryHttpClient {
  request<T = Record<string, unknown>>(method: string, path: string, body?: unknown): Promise<T>;
}

export interface CreateRegistryHttpClientOptions {
  readonly baseUrl: string;
  readonly bearerToken?: string;
  readonly cookie?: string;
  readonly csrfToken?: string;
  readonly fetchImpl?: typeof fetch;
}

export function createRegistryHttpClient({
  baseUrl,
  bearerToken,
  cookie,
  csrfToken,
  fetchImpl = fetch,
}: CreateRegistryHttpClientOptions): RegistryHttpClient {
  const origin = baseUrl.replace(/\/+$/u, '');
  return {
    async request<T>(method: string, path: string, body?: unknown): Promise<T> {
      const headers: Record<string, string> = { Accept: 'application/json, application/problem+json' };
      if (bearerToken) headers.Authorization = `Bearer ${bearerToken}`;
      if (cookie) headers.Cookie = cookie;
      if (csrfToken && method !== 'GET') headers['X-CSRF-Token'] = csrfToken;
      if (body !== undefined) headers['Content-Type'] = 'application/json';
      const response = await fetchImpl(`${origin}${path}`, {
        method,
        headers,
        body: body !== undefined ? JSON.stringify(body) : undefined,
      });
      const text = await response.text();
      const parsed = text ? (JSON.parse(text) as Record<string, unknown>) : {};
      if (!response.ok) {
        const detail = String(parsed.detail ?? parsed.title ?? text ?? response.statusText);
        throw new Error(`Registry API ${method} ${path} failed (${response.status}): ${detail}`);
      }
      return parsed as T;
    },
  };
}

interface SiteRecord {
  id: string;
  code: string;
  displayName: string;
  timezone: string;
  revision: number;
  buildingPlan?: Record<string, unknown> | null;
}

interface ImportPlanResponse {
  planId: string;
  digest: string;
  results: Array<{
    rowNumber: number;
    resourceType: string;
    externalId: string;
    targetId?: string;
    expectedRevision: number;
    status: string;
    errorCode?: string;
    message?: string;
  }>;
}

function stableEqual(left: unknown, right: unknown): boolean {
  return JSON.stringify(left ?? null) === JSON.stringify(right ?? null);
}

function hydrateDevice(
  device: OnboardingDevice,
  catalog: ReadonlyMap<string, DeviceTemplateCatalogEntry>,
): OnboardingDevice & { type: string; sampleSeconds: number } {
  const entry = device.template ? catalog.get(device.template) : undefined;
  return {
    ...device,
    type: device.type ?? entry?.payload.deviceType ?? 'OTHER',
    sampleSeconds: device.sampleSeconds ?? entry?.defaultSampleSeconds ?? 60,
  };
}

/** Publish all standard HVAC device templates via POST /api/v1/templates/revisions idempotently. */
export async function syncDeviceTemplatesViaApi(
  client: RegistryHttpClient,
  catalog: ReadonlyMap<string, DeviceTemplateCatalogEntry> = loadDeviceTemplateCatalog(),
): Promise<Map<string, { id: string; revisionNumber: number }>> {
  const released = new Map<string, { id: string; revisionNumber: number }>();
  for (const key of [...catalog.keys()].sort()) {
    const entry = catalog.get(key)!;
    const revision = await client.request<{ id: string; revisionNumber: number }>(
      'POST',
      '/api/v1/templates/revisions',
      {
        templateKey: entry.templateKey,
        templateKind: entry.templateKind,
        payload: entry.payload,
        releaseReferences: entry.releaseReferences,
        meta: {
          idempotencyKey: `catalog:${entry.templateKey}:v1`,
          reason: `sync standard HVAC device template ${entry.templateKey}`,
        },
      },
    );
    released.set(key, { id: revision.id, revisionNumber: revision.revisionNumber });
  }
  return released;
}

export interface OnboardingImportRow {
  rowNumber: number;
  resourceType: 'SPACE' | 'ASSET' | 'DEVICE' | 'POINT';
  externalId: string;
  payload: Record<string, unknown>;
}

/** Build Wave 1 import rows (Space, Assets, Gateway + Devices) ordered for two-wave Registry import. */
export function buildWave1ImportRows(
  profile: OnboardingProfile,
  catalog: ReadonlyMap<string, DeviceTemplateCatalogEntry> = loadDeviceTemplateCatalog(),
): OnboardingImportRow[] {
  const rows: OnboardingImportRow[] = [];
  let rowNumber = 1;
  if (profile.space) {
    rows.push({
      rowNumber: rowNumber++,
      resourceType: 'SPACE',
      externalId: `space:${profile.space.code}`,
      payload: {
        code: profile.space.code,
        displayName: profile.space.name,
        spaceType: profile.space.type,
        status: 'ACTIVE',
      },
    });
  }
  for (const asset of profile.assets ?? []) {
    const rawAsset = asset as unknown as { code: string; name?: string; type?: string };
    rows.push({
      rowNumber: rowNumber++,
      resourceType: 'ASSET',
      externalId: `asset:${rawAsset.code}`,
      payload: {
        code: rawAsset.code,
        displayName: rawAsset.name ?? rawAsset.code,
        assetType: rawAsset.type ?? 'OTHER',
        status: 'ACTIVE',
      },
    });
  }
  if (profile.gateway) {
    rows.push({
      rowNumber: rowNumber++,
      resourceType: 'DEVICE',
      externalId: `device:${profile.gateway.code}`,
      payload: {
        code: profile.gateway.code,
        displayName: profile.gateway.name,
        deviceType: 'GATEWAY',
        status: 'ACTIVE',
      },
    });
  }
  for (const rawDevice of profile.devices) {
    const device = hydrateDevice(rawDevice, catalog);
    rows.push({
      rowNumber: rowNumber++,
      resourceType: 'DEVICE',
      externalId: `device:${device.code}`,
      payload: {
        code: device.code,
        displayName: device.name ?? device.code,
        deviceType: device.type,
        status: 'ACTIVE',
      },
    });
  }
  return rows;
}

/** Build Wave 2 POINT import rows after Wave 1 device targetIds are committed or resolved. */
export function buildWave2PointImportRows(
  profile: OnboardingProfile,
  deviceIdByExternalId: ReadonlyMap<string, string>,
  catalog: ReadonlyMap<string, DeviceTemplateCatalogEntry> = loadDeviceTemplateCatalog(),
): OnboardingImportRow[] {
  const hydratedDevices = profile.devices.map((device) => ({
    ...hydrateDevice(device, catalog),
    points: expandPoints(device, profile.meterPoints),
  }));
  const devicesByCode = new Map(hydratedDevices.map((device) => [device.code, device]));
  const rows: OnboardingImportRow[] = [];
  let rowNumber = 1;

  for (const device of hydratedDevices) {
    const reportingDeviceId = deviceIdByExternalId.get(`device:${device.code}`);
    if (!reportingDeviceId) {
      throw new Error(`missing resolved reportingDeviceId for device:${device.code}`);
    }
    const interval = device.sampleSeconds * 1000;
    for (const rawPoint of device.points) {
      const point = rawPoint as OnboardingPoint & { name?: string };
      const pType = point.pointType ?? 'TELEMETRY';
      const vType = point.valueType ?? 'NUMBER';
      const writable = pType === 'COMMAND' ? point.writable !== false : Boolean(point.writable);
      const sourceMetadata = buildPointSourceMetadata(
        {
          code: point.code,
          pointType: pType,
          status: pointStatus(point),
          writable,
          asset: point.asset,
          address: point.address,
          protocol: point.protocol,
          sourceMetadata: point.sourceMetadata,
          device: {
            id: device.id,
            code: device.code,
            sourceKey: device.sourceKey,
            asset: device.asset,
          },
        },
        devicesByCode,
      );
      rows.push({
        rowNumber: rowNumber++,
        resourceType: 'POINT',
        externalId: `point:${device.code}/${point.code}`,
        payload: {
          reportingDeviceId,
          pointCode: point.code,
          sourceKey: point.sourceKey ?? point.code,
          displayName: point.name ?? point.code,
          pointType: pType,
          valueType: vType,
          ...(point.unit ? { unit: point.unit } : {}),
          writable,
          sampleIntervalMs: interval,
          publishIntervalMs: interval,
          staleAfterMs: interval * 3,
          ...(pType === 'COUNTER' ? { counterDecreaseMode: 'RESET_TO_ZERO' } : {}),
          sourceMetadata,
          status: pointStatus(point),
        },
      });
    }
  }
  return rows;
}

export interface SyncSiteResult {
  readonly siteId: string;
  readonly siteAction: 'created' | 'updated' | 'unchanged';
  readonly wave1Rows: number;
  readonly wave2Rows: number;
  readonly templateAssignments: number;
}

/** Onboard or update a Site via Registry HTTP endpoints (Sites + 2-wave Imports + TemplateAssignments). */
export async function syncSiteViaRegistryApi(
  client: RegistryHttpClient,
  profile: OnboardingProfile,
  options: {
    alarmRules?: Record<string, unknown> | null;
    templateRevisions?: ReadonlyMap<string, { id: string; revisionNumber: number }>;
    catalog?: ReadonlyMap<string, DeviceTemplateCatalogEntry>;
    dryRun?: boolean;
    now?: string;
  } = {},
): Promise<SyncSiteResult> {
  const problems = checkSiteFile(profile, options.alarmRules ?? null);
  if (problems.length > 0) {
    throw new Error(`site manifest validation failed:\n${problems.join('\n')}`);
  }
  const catalog = options.catalog ?? loadDeviceTemplateCatalog();
  const siteList = await client.request<{ items: SiteRecord[] }>('GET', '/api/v1/sites');
  const existing = siteList.items.find((item) => item.code === profile.site.code);
  const wantDisplayName = profile.site.name ?? profile.site.code;
  const wantTimezone = profile.site.timezone ?? 'Asia/Shanghai';
  const hasBuildingPlan = Object.hasOwn(profile.site, 'buildingPlan');

  let siteId = existing?.id ?? '';
  let siteAction: 'created' | 'updated' | 'unchanged' = 'unchanged';

  if (!existing) {
    if (options.dryRun) {
      return { siteId: profile.site.id, siteAction: 'created', wave1Rows: 0, wave2Rows: 0, templateAssignments: 0 };
    }
    const created = await client.request<SiteRecord>('POST', '/api/v1/sites', {
      code: profile.site.code,
      displayName: wantDisplayName,
      timezone: wantTimezone,
      status: 'ACTIVE',
      ...(hasBuildingPlan ? { buildingPlan: profile.site.buildingPlan ?? null } : {}),
      meta: {
        expectedRevision: 0,
        idempotencyKey: `site-create:${profile.site.code}`,
        reason: `onboard site ${profile.site.code}`,
      },
    });
    siteId = created.id;
    siteAction = 'created';
  } else {
    const planChanged = hasBuildingPlan && !stableEqual(existing.buildingPlan ?? null, profile.site.buildingPlan ?? null);
    if (existing.displayName !== wantDisplayName || existing.timezone !== wantTimezone || planChanged) {
      if (!options.dryRun) {
        await client.request<SiteRecord>('PATCH', `/api/v1/sites/${encodeURIComponent(existing.id)}`, {
          code: profile.site.code,
          displayName: wantDisplayName,
          timezone: wantTimezone,
          status: 'ACTIVE',
          ...(hasBuildingPlan ? { buildingPlan: profile.site.buildingPlan ?? null } : {}),
          meta: {
            expectedRevision: existing.revision,
            idempotencyKey: `site-patch:${profile.site.code}:rev-${existing.revision}`,
            reason: `sync site ${profile.site.code} manifest`,
          },
        });
      }
      siteAction = 'updated';
    }
  }

  const namespace = `site-onboarding:${profile.site.code}`;
  const wave1Rows = buildWave1ImportRows(profile, catalog);
  const wave1Plan = await client.request<ImportPlanResponse>(
    'POST',
    `/api/v1/sites/${encodeURIComponent(siteId)}/imports/dry-run`,
    { siteId, namespace, rows: wave1Rows },
  );
  const wave1Bad = wave1Plan.results.filter((row) => row.status !== 'READY');
  if (wave1Bad.length > 0) {
    throw new Error(`Wave 1 import dry-run rejected rows: ${JSON.stringify(wave1Bad)}`);
  }

  const deviceIds = new Map<string, string>();
  if (!options.dryRun) {
    const commit1 = await client.request<{ results: Array<{ externalId: string; targetId?: string }> }>(
      'POST',
      `/api/v1/sites/${encodeURIComponent(siteId)}/imports/commit`,
      {
        plan: wave1Plan,
        meta: {
          expectedRevision: 0,
          idempotencyKey: `import-w1:${wave1Plan.digest}`,
          reason: `commit site ${profile.site.code} entities`,
        },
      },
    );
    for (const row of commit1.results) {
      if (row.targetId) deviceIds.set(row.externalId, row.targetId);
    }
  } else {
    for (const row of wave1Plan.results) {
      if (row.targetId) deviceIds.set(row.externalId, row.targetId);
    }
  }

  const wave2Rows = buildWave2PointImportRows(profile, deviceIds, catalog);
  let templateAssignments = 0;

  if (wave2Rows.length > 0) {
    const wave2Plan = await client.request<ImportPlanResponse>(
      'POST',
      `/api/v1/sites/${encodeURIComponent(siteId)}/imports/dry-run`,
      { siteId, namespace, rows: wave2Rows },
    );
    const wave2Bad = wave2Plan.results.filter((row) => row.status !== 'READY');
    if (wave2Bad.length > 0) {
      throw new Error(`Wave 2 import dry-run rejected rows: ${JSON.stringify(wave2Bad)}`);
    }
    if (!options.dryRun) {
      await client.request(
        'POST',
        `/api/v1/sites/${encodeURIComponent(siteId)}/imports/commit`,
        {
          plan: wave2Plan,
          meta: {
            expectedRevision: 0,
            idempotencyKey: `import-w2:${wave2Plan.digest}`,
            reason: `commit site ${profile.site.code} points`,
          },
        },
      );
    }
  }

  if (!options.dryRun && options.templateRevisions) {
    const effectiveAt = options.now ?? new Date().toISOString();
    for (const device of profile.devices) {
      if (!device.template) continue;
      const rev = options.templateRevisions.get(device.template);
      const targetId = deviceIds.get(`device:${device.code}`);
      if (!rev || !targetId) continue;
      await client.request('POST', '/api/v1/templates/assignments', {
        siteId,
        targetType: 'DEVICE',
        targetId,
        templateRevisionId: rev.id,
        effectiveAt,
        meta: {
          expectedRevision: 0,
          idempotencyKey: `tpl-assign:${profile.site.code}:${device.code}:${rev.id}`,
          reason: `bind device ${device.code} to template ${device.template}`,
        },
      });
      templateAssignments += 1;
    }
  }

  return {
    siteId,
    siteAction,
    wave1Rows: wave1Rows.length,
    wave2Rows: wave2Rows.length,
    templateAssignments,
  };
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const [command, filePath, ...rest] = process.argv.slice(2);
  const dryRun = rest.includes('--dry-run');
  const baseUrl = process.env.HVAC_API_BASE_URL;
  if (!baseUrl) {
    console.error('error: HVAC_API_BASE_URL is required');
    process.exit(1);
  }
  const client = createRegistryHttpClient({
    baseUrl,
    bearerToken: process.env.HVAC_API_BEARER_TOKEN,
    cookie: process.env.HVAC_API_COOKIE,
    csrfToken: process.env.HVAC_API_CSRF_TOKEN,
  });

  if (command === 'sync-templates') {
    const released = await syncDeviceTemplatesViaApi(client);
    console.log(JSON.stringify(Object.fromEntries(released), null, 2));
  } else if (command === 'apply' && filePath) {
    const profile = JSON.parse(readFileSync(filePath, 'utf8')) as OnboardingProfile;
    const alarmPath = profile.intelligence?.alarmRules ? join(dirname(filePath), profile.intelligence.alarmRules) : null;
    const alarmRules = alarmPath && existsSync(alarmPath) ? (JSON.parse(readFileSync(alarmPath, 'utf8')) as Record<string, unknown>) : null;
    const templateRevisions = await syncDeviceTemplatesViaApi(client);
    const result = await syncSiteViaRegistryApi(client, profile, { alarmRules, templateRevisions, dryRun });
    console.log(JSON.stringify(result, null, 2));
  } else {
    console.error('usage: node --experimental-strip-types scripts/site-onboarding-http.ts sync-templates | apply <site.json> [--dry-run]');
    process.exit(1);
  }
}
