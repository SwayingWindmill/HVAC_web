import { readFileSync } from 'node:fs';
import type { Page, Request, Route } from '@playwright/test';
import { AssistantOwner } from './assistant-owner';

// A Platform Gateway stand-in for browser tests. Responses are recorded from the local
// stack (see fixtures/); writes are applied to in-memory alarms and work orders the way
// the owners apply them, so a test can follow an operator flow end to end.

const fixture = <T>(name: string): T => JSON.parse(readFileSync(new URL(`./fixtures/${name}`, import.meta.url), 'utf8')) as T;

type Json = Record<string, any>;

export interface RecordedRequest {
  readonly method: string;
  readonly path: string;
  readonly body: Json | null;
  readonly csrfToken: string | undefined;
}

export const SITE_ID = '018f3e00-1000-7000-8000-000000000001';
export const principal = fixture<Json>('principal.json');
export const MY_ID: string = principal.principalId;

// The recording is one Site-local day of hourly energy (UTC+8). An hourly query gets those hours
// on every Site-local day it spans, so today, yesterday and a week read the same recorded day.
const RECORDED_DAY = Date.parse('2026-10-09T16:00:00Z');
const DAY_MS = 86_400_000;

// A device breakdown's sub-meter series move with the total.
function replayEnergy(series: Json, query: Json, recordedDay = RECORDED_DAY): Json {
  if (query.granularity !== 'hour') return series;
  const from = Date.parse(query.from);
  const to = Date.parse(query.to);
  const replay = (recorded: Json[]) => {
    const points: Json[] = [];
    for (let day = from; day < to; day += DAY_MS) {
      for (const point of recorded) {
        const shift = day - recordedDay;
        points.push({ ...point, periodStart: new Date(Date.parse(point.periodStart) + shift).toISOString(), periodEnd: new Date(Date.parse(point.periodEnd) + shift).toISOString() });
      }
    }
    return points;
  };
  const devices = series.devices?.map((device: Json) => ({ ...device, points: replay(device.points) }));
  return { ...series, points: replay(series.points), ...(devices ? { devices } : {}) };
}

// The recorded day's energy, and its cost for electricity, once for each day of the window.
function replayEnergyDays(series: Json, query: Json): Json {
  const total = series.points.reduce((sum: number, point: Json) => sum + point.energyKWh, 0);
  const priced = series.points.filter((point: Json) => point.cost);
  const periods = new Map<string, { energyKWh: number; amountCNY: number }>();
  for (const period of priced.flatMap((point: Json) => point.cost.periods)) {
    const entry = periods.get(period.periodCode) ?? { energyKWh: 0, amountCNY: 0 };
    periods.set(period.periodCode, { energyKWh: entry.energyKWh + period.energyKWh, amountCNY: entry.amountCNY + period.amountCNY });
  }
  const cost = priced.length === 0 ? undefined : {
    amountCNY: priced.reduce((sum: number, point: Json) => sum + point.cost.amountCNY, 0),
    unpricedKWh: priced.reduce((sum: number, point: Json) => sum + point.cost.unpricedKWh, 0),
    periods: Array.from(periods.entries()).map(([periodCode, entry]) => ({ periodCode, ...entry })),
  };
  const points: Json[] = [];
  for (let day = Date.parse(query.from); day < Date.parse(query.to); day += DAY_MS) {
    points.push({ periodStart: new Date(day).toISOString(), periodEnd: new Date(day + DAY_MS).toISOString(), energyKWh: total, ...(cost ? { cost } : {}) });
  }
  return { ...series, points };
}

// Recorded quarter-hours answer each window the page asks for; tests pin the page clock to the recording.
function replayDemand(recorded: Json, query: Json): Json {
  const { recordedAt: _recordedAt, ...response } = recorded;
  const from = Date.parse(query.from);
  const to = Date.parse(query.to);
  const buckets = recorded.buckets.filter((bucket: Json) => Date.parse(bucket.periodStart) >= from && Date.parse(bucket.periodStart) < to);
  return { ...response, buckets, metadata: { ...recorded.metadata, requestedFrom: query.from, requestedTo: query.to, returnedBuckets: buckets.length } };
}

// The weather station's recorded days of outdoor temperature, moved to start on the requested day.
function replayOutdoorDays(recorded: Json, query: Json): Json {
  const { recordedFrom, ...response } = recorded;
  const shift = Date.parse(query.from) - Date.parse(recordedFrom);
  const moved = (instant: string) => new Date(Date.parse(instant) + shift).toISOString();
  const buckets = response.buckets
    .map((bucket: Json) => ({ ...bucket, periodStart: moved(bucket.periodStart), periodEnd: moved(bucket.periodEnd) }))
    .filter((bucket: Json) => Date.parse(bucket.periodStart) < Date.parse(query.to));
  return { ...response, buckets, metadata: { ...response.metadata, requestedFrom: query.from, requestedTo: query.to, returnedBuckets: buckets.length } };
}

// A machine's recorded hours of power, moved onto the requested day.
function replayMachineHours(recorded: Json, query: Json): Json {
  const response = recorded.byDevice[query.deviceId];
  const shift = Date.parse(query.from) - Date.parse(recorded.recordedFrom);
  const moved = (instant: string) => new Date(Date.parse(instant) + shift).toISOString();
  return { ...response, buckets: response.buckets.map((bucket: Json) => ({ ...bucket, periodStart: moved(bucket.periodStart), periodEnd: moved(bucket.periodEnd) })) };
}

// Recorded device history moved onto the requested window so recent-24h charts match any test clock.
function replayDeviceHistory(recorded: Json, query: Json): Json {
  const shift = Date.parse(query.from) - Date.parse(recorded.metadata.requestedFrom);
  const moved = (instant: string) => new Date(Date.parse(instant) + shift).toISOString();
  const buckets = recorded.buckets
    .map((bucket: Json) => ({ ...bucket, periodStart: moved(bucket.periodStart), periodEnd: moved(bucket.periodEnd) }))
    .filter((bucket: Json) => Date.parse(bucket.periodStart) >= Date.parse(query.from) && Date.parse(bucket.periodStart) < Date.parse(query.to));
  return {
    ...recorded,
    deviceId: query.deviceId,
    buckets,
    metadata: { ...recorded.metadata, requestedFrom: query.from, requestedTo: query.to, returnedBuckets: buckets.length },
  };
}

const TREND_GRANULARITY_MS: Readonly<Record<string, number>> = {
  MINUTE: 60_000,
  FIVE_MINUTE: 5 * 60_000,
  QUARTER_HOUR: 15 * 60_000,
  HOUR: 3_600_000,
  DAY: 86_400_000,
};

// Synthesizes contract-valid aggregate buckets from asset-model + snapshots for multi-point trend queries.
function synthesizeTrendAggregate(assetModel: Json, snapshots: Json, query: Json): Json {
  const stepMs = TREND_GRANULARITY_MS[query.granularity] ?? 300_000;
  const fromMs = Date.parse(query.from);
  const toMs = Date.parse(query.to);
  const startFloor = Math.floor(fromMs / stepMs) * stepMs;
  const deviceSnapshot = snapshots.items.find((item: Json) => item.deviceId === query.deviceId)?.snapshot;
  const buckets: Json[] = [];

  for (const key of query.keys as string[]) {
    const point = assetModel.telemetryPoints.find(
      (candidate: Json) => candidate.reportingDeviceId === query.deviceId && candidate.pointCode === key,
    );
    if (!point) continue;
    const snapState = deviceSnapshot?.values?.find((val: Json) => val.key === key);
    const baseNumeric = typeof snapState?.value === 'number' ? snapState.value : point.unit === 'kW' ? 210 : point.unit === 'm3/h' ? 85 : 7.2;
    const baseString = typeof snapState?.value === 'string' ? snapState.value : 'RUNNING';

    let index = 0;
    for (let cursor = startFloor; cursor < toMs; cursor += stepMs) {
      const end = cursor + stepMs;
      if (end <= fromMs) continue;
      const periodStart = new Date(cursor).toISOString();
      const periodEnd = new Date(end).toISOString();
      const isGauge = (point.pointType === 'TELEMETRY' || point.pointType === 'SETTING') && point.valueType === 'NUMBER';
      const isState =
        point.pointType === 'STATE' ||
        ((point.pointType === 'TELEMETRY' || point.pointType === 'SETTING') &&
          (point.valueType === 'STRING' || point.valueType === 'BOOLEAN'));

      if (isGauge) {
        const wave = point.pointType === 'SETTING' ? 0 : Math.sin(index / 4) * (baseNumeric * 0.04 + 0.2);
        const avg = Number((baseNumeric + wave).toFixed(2));
        const min = point.pointType === 'SETTING' ? avg : Number((avg - Math.abs(baseNumeric * 0.02 + 0.1)).toFixed(2));
        const max = point.pointType === 'SETTING' ? avg : Number((avg + Math.abs(baseNumeric * 0.02 + 0.1)).toFixed(2));
        buckets.push({
          telemetryKey: key,
          pointId: point.id,
          pointRevision: point.revision ?? 1,
          pointType: point.pointType,
          unit: point.unit,
          periodStart,
          periodEnd,
          quality: { good: 2, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 },
          completeness: 1,
          gauge: { average: avg, minimum: min, maximum: max, first: avg, last: avg, sampleCount: 2 },
        });
      } else if (isState) {
        const stateVal = index >= 18 && index < 26 ? 'STOPPED' : baseString;
        buckets.push({
          telemetryKey: key,
          pointId: point.id,
          pointRevision: point.revision ?? 1,
          pointType: point.pointType === 'STATE' ? 'STATE' : point.pointType,
          unit: point.unit,
          periodStart,
          periodEnd,
          quality: { good: 2, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 },
          completeness: 1,
          state: {
            valueType: point.valueType,
            lastValue: stateVal,
            sampleCount: 2,
            changeCount: stateVal === baseString ? 0 : 1,
            stateCounts: [{ value: stateVal, count: 2 }],
          },
        });
      } else if (point.pointType === 'COUNTER') {
        const delta = Number((180 + Math.sin(index / 3) * 24).toFixed(2));
        buckets.push({
          telemetryKey: key,
          pointId: point.id,
          pointRevision: point.revision ?? 1,
          pointType: 'COUNTER',
          unit: point.unit,
          periodStart,
          periodEnd,
          quality: { good: 12, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 },
          completeness: 1,
          counter: {
            deltaSum: delta,
            deltaCount: 12,
            resetCount: 0,
            rolloverCount: 0,
            excludedTransitionCount: 0,
          },
        });
      }
      index += 1;
    }
  }

  return {
    schemaVersion: 1,
    tenantId: assetModel.tenantId,
    siteId: SITE_ID,
    deviceId: query.deviceId,
    buckets,
    metadata: {
      requestedFrom: query.from,
      requestedTo: query.to,
      granularity: query.granularity,
      timezone: query.timezone,
      qualityPolicy: query.qualityPolicy,
      projectionWatermark: query.to,
      returnedBuckets: buckets.length,
    },
  };
}

// Recorded findings are replayed as if the last diagnosis had just run.
function replayFindings(findings: Json[], now: string): Json[] {
  const shift = Date.parse(now) - Math.max(...findings.map((finding) => Date.parse(finding.evaluationTo)));
  const moved = (instant: string) => new Date(Date.parse(instant) + shift).toISOString();
  return findings.map((finding) => ({ ...finding, evaluationFrom: moved(finding.evaluationFrom), evaluationTo: moved(finding.evaluationTo), createdAt: moved(finding.createdAt) }));
}

/** Starting or stopping plant equipment is high risk at its base, as Command Governance assesses it. */
const HIGH_RISK: readonly string[] = ['START', 'STOP', 'START_UNIT_SEQUENCE', 'STOP_UNIT_SEQUENCE'];

function advance(command: Json, status: string, at: string) {
  command.transitions.push({
    ...(command.transitions.length ? { fromStatus: command.status } : {}),
    toStatus: status, reason: status, actorType: 'PRINCIPAL', occurredAt: at, version: command.transitions.length + 1,
  });
  command.status = status;
  command.updatedAt = at;
}

// The recorded forecast is replayed as if it had just been published.
function replayForecast(forecast: Json, now: string): Json {
  const shift = Date.parse(now) - Date.parse(forecast.snapshot.forecastOrigin);
  const moved = (instant: string) => new Date(Date.parse(instant) + shift).toISOString();
  return {
    snapshot: { ...forecast.snapshot, forecastOrigin: now },
    points: forecast.points.map((point: Json) => ({ ...point, forecast_for: moved(point.forecast_for), forecast_origin: now })),
  };
}

export class Gateway {
  principalOverride: Json | null = null;
  assetModel: Json = fixture<Json>('asset-model.json');
  readonly alarms = fixture<Json[]>('alarms.json');
  readonly alarmEvaluations: Record<string, Json[]> = {};
  // The Site's rules in effect, as the Alarm owner lists them; publishing the rules file changes them, not the page.
  readonly alarmRules = fixture<Json[]>('alarm-rules.json');
  readonly workOrders = fixture<Json[]>('work-orders.json');
  readonly snapshots = fixture<Json>('snapshots.json');
  readonly commands: Json[] = [];
  readonly priorityLockedDeviceIds = new Set<string>();
  /** The Site's control mode and asset locks, as the Registry owner keeps them. */
  controlState: Json = {
    tenantId: principal.context.tenantId, siteId: SITE_ID, controlMode: 'REMOTE_AUTO', reason: '', updatedBy: null, updatedByName: null, updatedAt: null, revision: 1, assetLocks: [],
  };
  /** The latest stage-down and idle-auxiliary evaluation; null until the service evaluated the Site. */
  staging: Json | null = null;
  /** The session signed in with two steps in the last 10 minutes, so high-risk approvals go through. */
  stepUpFresh = false;
  /** Optional synthetic step-up refusal reason for testing TOTP failure handling. */
  stepUpFailureReason: 'CODE_INVALID' | 'CODE_REPLAYED' | 'MFA_NOT_CONFIGURED' | 'MFA_LOCKED' | null = null;
  /** When the forecast is replayed as published; tests that pin the page clock set it to that instant. */
  forecastOrigin: string | null = null;
  /** Optional custom handler for `device-series:aggregate` so tests can simulate outages or specific series. */
  aggregateHandler: ((query: Json) => { status?: number; body: unknown } | undefined) | null = null;
  /** Optional custom handler for `analytics/energy-series` so tests can simulate fully priced daily series. */
  energySeriesHandler: ((query: Json) => { status?: number; body: unknown } | undefined) | null = null;
  readonly findings = replayFindings(fixture<Json>('fdd-findings.json').items, new Date().toISOString());
  /** The Site's latest published recommendation; null until a run publishes one. */
  optimization: Json | null = null;
  /** How the Site's Optimization schedule last fired; null defaults to NOT_SCHEDULED with CALIBRATED + CONFIRMED. */
  optimizationPreparation: Json | null = null;
  /** The Site's automatic optimization control lease state (MANUAL_CONFIRM by default). */
  autoControlLeaseState: Json = {
    tenantId: principal.context.tenantId,
    siteId: SITE_ID,
    confirmationMode: 'MANUAL_CONFIRM',
    activeLease: null,
    latestLease: null,
  };
  /** Recent 30-minute automated optimization execution monitors for the Site. */
  optimizationExecutionMonitors: Json[] = [];
  /** The Chiller COP Model's latest read-only shadow evaluation; null when no SHADOW row exists. */
  optimizationShadow: Json | null = null;
  shadowReads = 0;
  /** IAM refuses Energy Analytics for the Site, as it does for a Site outside the principal's analytics grant. */
  analyticsDenied = false;
  /** IAM refuses Alarm listing for the Site. */
  alarmsDenied = false;
  /** IAM refuses Command listing for the Site. */
  commandsDenied = false;
  /** Recorded Tenant audit records and an IAM refusal override for `/api/v1/audit/search`. */
  readonly auditRecords = fixture<Json[]>('audit-records.json');
  auditDenied = false;
  /** Daily energy series repeat the recorded day's total each day, so days line up with other daily series. */
  energyByDay = false;
  /** Telemetry fails the weather station's daily temperature. */
  outdoorFailing = false;
  /** Machines whose sub-meter the recorded device breakdown leaves out. */
  unmeteredDeviceIds: readonly string[] = [];
  /** Critical reading-gap items returned by `GET /api/v1/sites/{siteId}/data-gaps` (#799). */
  dataGaps: Json[] = [];
  /** report-service's kept snapshots of closed months; a month without one answers 404. */
  reportSnapshots: Json[] = [];
  /** The Site's import Tariff in effect; null when none is assigned. */
  tariff: Json | null = fixture<Json>('tariff.json');
  /** What the next requested run publishes; null while the owner declines it for missing inputs. */
  nextRecommendation: Json | null = null;
  /** The low-delta-T rule revision FDD holds for the Tenant, as the seed publishes it; null while it cannot be read. */
  fddRule: { revisionId: string; revision: number; minimumDeltaTC: number; windowMinutes: number; operatingGates: Json; effectiveFrom: string } | null = {
    revisionId: '01910000-0000-7000-8000-860000000001', revision: 1, minimumDeltaTC: 3.5, windowMinutes: 15,
    operatingGates: {
      minimumOperatingShare: 0.7, minimumLowShare: 0.7, temperatureHoldMinutes: 2, gateHoldMinutes: 10,
      chillerRunning: { minimumRatedPowerFraction: 0.1 }, loadRatio: { minimum: 0.15 }, chilledWaterPumpRunning: {},
    },
    effectiveFrom: '2026-01-01T00:00:00Z',
  };
  /** Minutes of the rule's window the plant operated; under the rule's share FDD does not judge it. */
  fddOperatingMinutes: number | null = null;
  /** The Site's energy conservation measures as the M&V owner keeps them, newest first. */
  readonly measures: Json[] = [];
  /** The Site's weekly Operating Schedule and Operating Exceptions as the Registry owner keeps them. */
  operatingSchedule: Json = {
    id: null,
    tenantId: principal.context.tenantId,
    siteId: SITE_ID,
    timezone: 'Asia/Shanghai',
    name: '标准供冷周计划',
    preCoolLeadMinutes: 45,
    coastDownLagMinutes: 30,
    revision: 0,
    updatedBy: null,
    updatedAt: null,
    slots: [],
  };
  readonly operatingExceptions: Json[] = [];
  /** The Site's Monthly Electricity Budgets and Utility Bills as the Registry owner keeps them. */
  readonly electricityBudgets: Json[] = [];
  readonly utilityBills: Json[] = [];
  /** Members of the Tenant as IAM and the identity directory keep them (系统管理 → 用户与角色). */
  readonly members: Json[] = [
    {
      principalId: MY_ID, username: 'duty.engineer', displayName: principal.principal.displayName, email: principal.principal.email,
      enabled: true, locked: false, mfaEnabled: true, lastLoginAt: '2026-10-14T01:20:00Z', self: true,
      assignments: [{ bindingId: '018f3e00-2200-7000-8000-000000000001', roleKey: 'admin' }],
    },
    {
      principalId: '018f3e00-2000-7000-8000-000000000002', username: 'zhou.min', displayName: '周敏', email: 'zhou.min@example.invalid',
      enabled: true, locked: false, mfaEnabled: false, self: false,
      assignments: [{ bindingId: '018f3e00-2200-7000-8000-000000000002', roleKey: 'operator', siteId: SITE_ID }],
    },
  ];
  /** The Operations Agent's Agent Sessions for the Site. */
  readonly assistant = new AssistantOwner();
  sites: Json[] = fixture<Json>('sites.json').items;
  readonly offlineGateways = new Set<string>();
  readonly configShadows = new Map<string, Json>();
  readonly extraCrossSiteAssets: Json[] = [];
  readonly extraCrossSiteDevices: Json[] = [];
  private p1Seq = 100;
  readonly templateRevisions: Json[] = [
    {
      id: '01910000-7100-7000-8000-000000000001',
      tenantId: principal.context.tenantId,
      templateId: '01910000-7000-7000-8000-000000000001',
      templateKey: 'chiller-standard',
      templateKind: 'DEVICE',
      revisionNumber: 1,
      status: 'RELEASED',
      payload: {
        deviceType: 'CHILLER',
        standardPoints: [
          {
            pointCode: 'chw_supply_temp',
            sourceKey: 'chw_supply_temp',
            displayName: '冷冻水供水温度',
            pointType: 'TELEMETRY',
            valueType: 'NUMBER',
            unit: 'degC',
            writable: false,
            sampleIntervalMs: 5000,
            publishIntervalMs: 5000,
            staleAfterMs: 15000,
          },
        ],
      },
      releaseReferences: { registrySchema: 'v1' },
      releasedAt: '2026-10-01T00:00:00.000Z',
    },
  ];
  readonly templateAssignments: Json[] = [];
  readonly assignedTemplateKeyByTargetId = new Map<string, { templateKey: string; templateRevisionId: string }>();
  readonly concentratorStates = new Map<string, Json>();
  readonly gatewayCredentialsState = new Map<string, { revoked: boolean; enrollmentSeq: number; certSeq: number; records: Json[] }>();
  readonly gatewayQuarantineState = new Map<string, Json[]>();
  readonly gatewayPacketCaptureState = new Map<string, Json>();
  readonly writes: RecordedRequest[] = [];
  readonly unexpected: string[] = [];

  async install(page: Page) {
    await page.route('**/api/v1/**', (route) => this.handle(route));
  }

  private async handle(route: Route) {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const method = request.method();
    const csrfToken = request.headers()['x-csrf-token'];
    if (method !== 'GET') this.writes.push({ method, path, body: bodyOf(request), csrfToken });
    const agent = this.assistant.respond(SITE_ID, method, path, bodyOf(request));
    if (agent?.stream !== undefined) return route.fulfill({ status: 200, contentType: 'text/event-stream; charset=utf-8', body: agent.stream });
    const reply = agent ?? this.respond(method, path, url.searchParams, bodyOf(request), csrfToken);
    if (!reply) {
      this.unexpected.push(`${method} ${path}`);
      return route.fulfill({ status: 404, contentType: 'application/problem+json', body: JSON.stringify({ status: 404, code: 'ROUTE_NOT_FOUND', title: 'Not found' }) });
    }
    if (reply.status === 204) return route.fulfill({ status: 204 });
    return route.fulfill({ status: reply.status ?? 200, contentType: 'application/json', body: JSON.stringify(reply.body) });
  }

  /** Withdraws the commands not yet dispatched on these devices, as leaving auto control or locking does. */
  private withdraw(deviceIds: readonly string[] | null, now: string): string[] {
    return this.commands
      .filter((command) => ['AWAITING_APPROVAL', 'QUEUED'].includes(command.status) && (deviceIds === null || deviceIds.includes(command.deviceId)))
      .map((command) => { advance(command, 'CANCELLED', now); return command.commandId; });
  }

  private updateControlMode(input: Json, now: string): { status?: number; body: unknown } {
    if (!input.reason?.trim()) return { status: 400, body: { status: 400, code: 'CONTROL_EXIT_REQUEST_INVALID', detail: 'The control state mutation is invalid.' } };
    if (input.expectedRevision !== undefined && input.expectedRevision !== this.controlState.revision) {
      return { status: 409, body: { status: 409, code: 'REGISTRY_REVISION_CONFLICT', detail: 'The expected control state revision is stale.' } };
    }
    if (input.controlMode === 'SAFE_SHUTDOWN_EXIT' && this.commands.some((command) => ['DISPATCHING', 'OUTCOME_UNKNOWN'].includes(command.status))) {
      return { status: 409, body: { status: 409, code: 'SAFE_SHUTDOWN_PRECONDITION_FAILED', detail: 'Commands are still dispatching or awaiting reconciliation.' } };
    }
    const cancelled = this.controlState.controlMode === 'REMOTE_AUTO' && input.controlMode !== 'REMOTE_AUTO' ? this.withdraw(null, now) : [];
    this.controlState = { ...this.controlState, controlMode: input.controlMode, reason: input.reason, updatedBy: MY_ID, updatedByName: principal.principal.displayName, updatedAt: now, revision: this.controlState.revision + 1 };
    return { body: { ...this.controlState, cancelledCommandIds: cancelled } };
  }

  private updateControlLock(assetId: string, input: Json, now: string): { status?: number; body: unknown } {
    if (!input.reason?.trim()) return { status: 400, body: { status: 400, code: 'CONTROL_EXIT_REQUEST_INVALID', detail: 'The control state mutation is invalid.' } };
    const current = this.controlState.assetLocks.find((lock: Json) => lock.assetId === assetId);
    if (current && input.expectedRevision !== undefined && input.expectedRevision !== current.revision) {
      return { status: 409, body: { status: 409, code: 'REGISTRY_REVISION_CONFLICT', detail: 'The expected control lock revision is stale.' } };
    }
    const boundDeviceIds: string[] = Array.from(new Set<string>(this.assetModel.relationships
      .filter((relationship: Json) => relationship.role === 'CONTROLS' && relationship.toId === assetId)
      .map((relationship: Json) => this.assetModel.telemetryPoints.find((point: Json) => point.id === relationship.fromId)?.reportingDeviceId)
      .filter(Boolean)));
    const lock = { siteId: SITE_ID, assetId, lockMode: input.lockMode, reason: input.reason, updatedBy: MY_ID, updatedByName: principal.principal.displayName, updatedAt: now, revision: (current?.revision ?? 0) + 1, boundDeviceIds };
    this.controlState = { ...this.controlState, assetLocks: [...this.controlState.assetLocks.filter((item: Json) => item.assetId !== assetId), lock] };
    const cancelled = input.lockMode === 'UNLOCKED' ? [] : this.withdraw(boundDeviceIds, now);
    return { body: { ...lock, cancelledCommandIds: cancelled } };
  }

  // Site shutdown as the gateway runs it: every mode needs a reason and the authenticator code;
  // the plant and emergency modes switch the Site to safe exit and withdraw what is not dispatched.
  private shutdownSite(input: Json, now: string): { status?: number; body: unknown } {
    const refuse = (status: number, code: string, detail: string, reason?: string) => ({
      status,
      body: reason ? { status, code, detail, reason } : { status, code, detail },
    });
    if (!input.reason?.trim()) return refuse(400, 'CONTROL_EXIT_REQUEST_INVALID', 'reason is required.');
    if (this.stepUpFailureReason) {
      return refuse(428, 'STEP_UP_REQUIRED', 'Site shutdown failed step-up verification.', this.stepUpFailureReason);
    }
    if (!/^\d{6}$/.test(input.verificationCode ?? '')) return refuse(428, 'STEP_UP_REQUIRED', 'Site shutdown requires a 6-digit verificationCode.');
    if (input.expectedRevision !== undefined && input.expectedRevision !== this.controlState.revision) {
      return refuse(409, 'REGISTRY_REVISION_CONFLICT', 'The expected control state revision is stale.');
    }
    const points: Json[] = this.assetModel.telemetryPoints;
    const assets: Json[] = this.assetModel.assets;
    const chillers = assets.filter((asset) => asset.assetType === 'CHILLER');
    const submit = (count: number) => Array.from({ length: count }, (_, index) => `0190c000-0000-7000-8000-${String(this.writes.length * 10 + index).padStart(12, '0')}`);
    if (input.mode === 'ABORT_UNIT_SEQUENCE') {
      const chiller = chillers.find((asset) => asset.code.toLowerCase() === String(input.unitCode ?? '').toLowerCase());
      if (!chiller) return refuse(400, 'CONTROL_EXIT_REQUEST_INVALID', 'unitCode did not match an active CHILLER asset on this Site.');
      const deviceIds = this.assetModel.relationships
        .filter((relationship: Json) => relationship.role === 'CONTROLS' && relationship.toId === chiller.id)
        .map((relationship: Json) => points.find((point) => point.id === relationship.fromId)?.reportingDeviceId)
        .filter(Boolean);
      const cancelled = this.withdraw(deviceIds, now);
      return { body: { siteControlState: this.controlState, mode: input.mode, cancelledCommandIds: cancelled, submittedCommandIds: submit(1), deferredHoldingAssetIds: [] } };
    }
    if (input.mode === 'EMERGENCY_DO_RELEASE') {
      if (input.confirmEmergencyDoRelease !== true) return refuse(400, 'CONTROL_EXIT_REQUEST_INVALID', 'confirmEmergencyDoRelease must be explicitly true.');
      if (!points.some((point) => point.sourceMetadata?.actuationMode === 'EMERGENCY_DO_RELEASE')) {
        return refuse(409, 'EMERGENCY_DO_RELEASE_UNAVAILABLE', 'This Site has no active EMERGENCY_DO_RELEASE command point.');
      }
    } else if (input.mode !== 'PLANT_SAFE_SHUTDOWN') {
      return refuse(400, 'CONTROL_EXIT_REQUEST_INVALID', 'mode is invalid.');
    }
    const cancelled = this.withdraw(null, now);
    this.controlState = { ...this.controlState, controlMode: 'SAFE_SHUTDOWN_EXIT', reason: input.reason, updatedBy: MY_ID, updatedByName: principal.principal.displayName, updatedAt: now, revision: this.controlState.revision + 1 };
    const auxiliaries = assets.filter((asset) => ['CHILLED_WATER_PUMP', 'COOLING_WATER_PUMP', 'COOLING_TOWER'].includes(asset.assetType));
    const plant = input.mode === 'PLANT_SAFE_SHUTDOWN';
    return {
      body: {
        siteControlState: this.controlState,
        mode: input.mode,
        cancelledCommandIds: cancelled,
        submittedCommandIds: submit(plant ? chillers.length : 1),
        // The recorded chiller is still running, so its pumps and tower wait for it to stop.
        deferredHoldingAssetIds: plant ? auxiliaries.map((asset) => asset.id) : [],
      },
    };
  }

  private submitCommand(input: Json, now: string): Json {
    const point = this.assetModel.telemetryPoints.find((candidate: Json) => candidate.id === input.commandPointId);
    const capability: string = point.sourceMetadata.capability;
    const risk = HIGH_RISK.includes(capability) ? 'HIGH' : 'LOW';
    const command: Json = {
      schemaVersion: 1, commandId: `ec61c2dc-2d2c-4933-8fb7-${String(this.commands.length + 1).padStart(12, '0')}`, siteId: SITE_ID, deviceId: point.reportingDeviceId, pointId: point.id,
      capability, status: 'SUBMITTED', risk, priorityTier: input.priorityTier ?? 'MANUAL_OVERRIDE', commandSource: input.commandSource ?? 'OPERATOR',
      approvalPolicy: risk === 'LOW' ? 'NONE' : 'SINGLE_APPROVER',
      approvalCount: 0, requiredApprovalCount: risk === 'LOW' ? 0 : 1, parameters: input.parameters, transitions: [],
      createdAt: now, updatedAt: now,
    };
    advance(command, 'SUBMITTED', now);
    advance(command, risk === 'LOW' ? 'SUCCEEDED' : 'AWAITING_APPROVAL', now);
    this.commands.unshift(command);
    return command;
  }

  // The recorded loop still runs below the rule's threshold. As the FDD owner does, the evaluation
  // reads the window of the rule revision in effect now, judges it only when the plant operated for
  // the rule's share of it, and a finding for the same asset and revision from the last 24 hours
  // makes the condition ongoing rather than new.
  private evaluateLowDeltaT(input: Json, now: string): Json {
    const rule = this.fddRule!;
    const recorded = fixture<Json>('fdd-findings.json').items[0];
    const operatingMinutes = this.fddOperatingMinutes ?? rule.windowMinutes;
    const operatingShare = operatingMinutes / rule.windowMinutes;
    if (operatingShare < rule.operatingGates.minimumOperatingShare) {
      const window = { minutes: rule.windowMinutes, operatingMinutes, lowMinutes: 0, operatingShare, lowShare: 0 };
      return { status: 'NOT_OPERATING', deltaTC: recorded.observedValue, rule, window };
    }
    const low = recorded.observedValue < rule.minimumDeltaTC;
    const window = { minutes: rule.windowMinutes, operatingMinutes, lowMinutes: low ? operatingMinutes : 0, operatingShare, lowShare: low ? 1 : 0 };
    const evaluationFrom = new Date(Date.parse(now) - rule.windowMinutes * 60_000).toISOString();
    const finding = {
      ...recorded, id: `finding-${this.findings.length + 1}`, assetId: input.assetId, evaluationFrom, evaluationTo: now,
      ruleRevisionId: rule.revisionId, thresholdValue: rule.minimumDeltaTC, createdAt: now,
    };
    if (!low) return { status: 'CLEAR', deltaTC: finding.observedValue, rule, window };
    const open = this.findings.find((candidate) => candidate.assetId === input.assetId && candidate.ruleRevisionId === rule.revisionId
      && Date.parse(candidate.evaluationTo) >= Date.parse(now) - 86_400_000);
    if (open) return { status: 'ONGOING', deltaTC: finding.observedValue, rule, window, finding: open };
    this.findings.unshift(finding);
    return { status: 'FINDING', deltaTC: finding.observedValue, rule, window, finding };
  }

  // The Registry owner's weekly Operating Schedule and full-day Operating Exceptions.
  private schedules(method: string, path: string, query: URLSearchParams, body: Json | null, now: string): { status?: number; body: unknown } | undefined {
    const schedulePath = `/api/v1/sites/${SITE_ID}/operating-schedule`;
    const exceptionsPath = `/api/v1/sites/${SITE_ID}/operating-exceptions`;
    if (path !== schedulePath && !path.startsWith(exceptionsPath)) return undefined;

    const refuse = (status: number, code: string, detail: string) => ({
      status,
      body: { type: 'about:blank', title: 'Operating schedule refused', status, detail, instance: path, code, traceId: '0'.repeat(32), retryable: false },
    });
    const activePrincipal = this.principalOverride ?? principal;
    const actor: string = activePrincipal.principalId;
    const actorDisplay: string = activePrincipal.principal.displayName;
    const capabilities: string[] = activePrincipal.authorization.capabilities ?? [];

    if (method === 'GET' && path === schedulePath) {
      return { body: this.operatingSchedule };
    }

    if (method === 'PUT' && path === schedulePath) {
      if (!capabilities.includes('schedule.manage')) {
        return refuse(403, 'CAPABILITY_DENIED', 'schedule.manage is required.');
      }
      if (body?.meta?.expectedRevision !== this.operatingSchedule.revision) {
        return refuse(409, 'REGISTRY_REVISION_CONFLICT', 'operating schedule revision conflict');
      }
      const rawSlots: Json[] = body?.slots ?? [];
      for (let day = 1; day <= 7; day += 1) {
        const daySlots = rawSlots.filter((s) => s.dayOfWeek === day).sort((a, b) => a.startMinute - b.startMinute);
        for (let i = 1; i < daySlots.length; i += 1) {
          if (daySlots[i].startMinute < daySlots[i - 1].endMinute) {
            return refuse(409, 'REGISTRY_BINDING_CONFLICT', 'overlapping slots within same dayOfWeek');
          }
        }
      }
      const nextRev = (this.operatingSchedule.revision as number) + 1;
      this.operatingSchedule = {
        ...this.operatingSchedule,
        id: this.operatingSchedule.id ?? '01950000-0000-7000-8000-000000000001',
        name: body?.name,
        preCoolLeadMinutes: body?.preCoolLeadMinutes,
        coastDownLagMinutes: body?.coastDownLagMinutes,
        revision: nextRev,
        updatedBy: actor,
        updatedAt: now,
        slots: rawSlots.map((slot, index) => ({
          id: `01950000-0001-7000-8000-${String(nextRev * 100 + index + 1).padStart(12, '0')}`,
          dayOfWeek: slot.dayOfWeek,
          startMinute: slot.startMinute,
          endMinute: slot.endMinute,
          ...(slot.targetSupplyTempC !== undefined ? { targetSupplyTempC: slot.targetSupplyTempC } : {}),
          ...(slot.spanGroupId ? { spanGroupId: slot.spanGroupId } : {}),
        })),
      };
      return { body: this.operatingSchedule };
    }

    if (method === 'GET' && path === exceptionsPath) {
      const from = query.get('from');
      const to = query.get('to');
      const status = query.get('status');
      const items = this.operatingExceptions.filter((item) =>
        (!status || item.status === status) &&
        (!from || item.endDate >= from) &&
        (!to || item.startDate <= to));
      return { body: { items } };
    }

    if (method === 'POST' && path === exceptionsPath) {
      const autoApprove = Boolean(body?.autoApprove);
      if (autoApprove && !capabilities.includes('schedule.manage')) {
        return refuse(403, 'OPERATING_EXCEPTION_FORBIDDEN', 'autoApprove requires schedule.manage.');
      }
      if (!capabilities.includes('schedule.request') && !capabilities.includes('schedule.manage')) {
        return refuse(403, 'CAPABILITY_DENIED', 'schedule.request is required.');
      }
      if (autoApprove) {
        const overlaps = this.operatingExceptions.some(
          (other) => other.status === 'APPROVED' && other.startDate <= body!.endDate && other.endDate >= body!.startDate,
        );
        if (overlaps) {
          return refuse(409, 'REGISTRY_BINDING_CONFLICT', 'date range overlaps an existing approved operating exception');
        }
      }
      const created: Json = {
        id: `01950000-0002-7000-8000-${String(this.operatingExceptions.length + 1).padStart(12, '0')}`,
        tenantId: principal.context.tenantId,
        siteId: SITE_ID,
        title: body?.title,
        kind: body?.kind,
        startDate: body?.startDate,
        endDate: body?.endDate,
        replacementSlots: body?.replacementSlots ?? [],
        reason: body?.meta?.reason ?? '',
        status: autoApprove ? 'APPROVED' : 'PENDING_APPROVAL',
        requestedBy: actor,
        requestedByDisplay: actorDisplay,
        decidedBy: autoApprove ? actor : null,
        decidedByDisplay: autoApprove ? actorDisplay : null,
        decidedAt: autoApprove ? now : null,
        decisionNote: '',
        revision: 1,
        createdAt: now,
        updatedAt: now,
      };
      this.operatingExceptions.unshift(created);
      return { status: 201, body: created };
    }

    const decideMatch = path.match(new RegExp(`^${exceptionsPath}/([^/]+)/decide$`));
    if (method === 'POST' && decideMatch) {
      const target = this.operatingExceptions.find((item) => item.id === decideMatch[1]);
      if (!target) return refuse(404, 'RESOURCE_NOT_FOUND', 'operating exception not found');
      if (body?.meta?.expectedRevision !== target.revision) {
        return refuse(409, 'REGISTRY_REVISION_CONFLICT', 'operating exception revision conflict');
      }
      const decision: string = body?.decision;
      if (decision === 'APPROVE' || decision === 'REJECT') {
        if (!capabilities.includes('schedule.manage')) {
          return refuse(403, 'OPERATING_EXCEPTION_FORBIDDEN', 'schedule.manage is required to approve or reject');
        }
        if (target.status !== 'PENDING_APPROVAL') {
          return refuse(409, 'OPERATING_EXCEPTION_STATE_CONFLICT', 'only PENDING_APPROVAL exceptions can be approved or rejected');
        }
        if (decision === 'APPROVE') {
          const overlaps = this.operatingExceptions.some(
            (other) => other.id !== target.id && other.status === 'APPROVED'
              && other.startDate <= target.endDate && other.endDate >= target.startDate,
          );
          if (overlaps) {
            return refuse(409, 'REGISTRY_BINDING_CONFLICT', 'date range overlaps an existing approved operating exception');
          }
          target.status = 'APPROVED';
        } else {
          target.status = 'REJECTED';
        }
      } else if (decision === 'CANCEL') {
        if (target.status === 'PENDING_APPROVAL') {
          if (!capabilities.includes('schedule.manage') && target.requestedBy !== actor) {
            return refuse(403, 'OPERATING_EXCEPTION_FORBIDDEN', 'only requester or manager can cancel pending exception');
          }
        } else if (target.status === 'APPROVED') {
          if (!capabilities.includes('schedule.manage')) {
            return refuse(403, 'OPERATING_EXCEPTION_FORBIDDEN', 'schedule.manage is required to cancel approved exception');
          }
        } else {
          return refuse(409, 'OPERATING_EXCEPTION_STATE_CONFLICT', 'exception cannot be cancelled in current state');
        }
        target.status = 'CANCELLED';
      }
      target.decidedBy = actor;
      target.decidedByDisplay = actorDisplay;
      target.decidedAt = now;
      target.decisionNote = body?.decisionNote ?? '';
      target.revision += 1;
      target.updatedAt = now;
      return { body: target };
    }

    return undefined;
  }

  // The Registry owner's Monthly Electricity Budgets and Utility Bills.
  private electricityBilling(method: string, path: string, query: URLSearchParams, body: Json | null, now: string): { status?: number; body: unknown } | undefined {
    const budgetsPath = `/api/v1/sites/${SITE_ID}/electricity-budgets`;
    const billsPath = `/api/v1/sites/${SITE_ID}/utility-bills`;
    if (path !== budgetsPath && !path.startsWith(billsPath)) return undefined;

    const refuse = (status: number, code: string, detail: string) => ({
      status,
      body: { type: 'about:blank', title: 'Electricity billing refused', status, detail, instance: path, code, traceId: '0'.repeat(32), retryable: false },
    });
    const activePrincipal = this.principalOverride ?? principal;
    const actor: string = activePrincipal.principalId;
    const actorDisplay: string = activePrincipal.principal.displayName;
    const capabilities: string[] = activePrincipal.authorization.capabilities ?? [];

    if (method === 'GET' && path === budgetsPath) {
      const billingMonth = query.get('billingMonth');
      const fromMonth = query.get('fromMonth');
      const toMonth = query.get('toMonth');
      const items = this.electricityBudgets
        .filter((item) =>
          (!billingMonth || item.billingMonth === billingMonth) &&
          (!fromMonth || item.billingMonth >= fromMonth) &&
          (!toMonth || item.billingMonth <= toMonth))
        .sort((a, b) => b.billingMonth.localeCompare(a.billingMonth));
      return { body: { items } };
    }

    if (method === 'PUT' && path === budgetsPath) {
      if (!capabilities.includes('billing.manage')) {
        return refuse(403, 'BILLING_MANAGE_FORBIDDEN', 'billing.manage is required.');
      }
      const existing = this.electricityBudgets.find((item) => item.billingMonth === body?.billingMonth);
      const currentRevision = existing ? (existing.revision as number) : 0;
      if (body?.meta?.expectedRevision !== currentRevision) {
        return refuse(409, 'REGISTRY_REVISION_CONFLICT', 'monthly electricity budget revision conflict');
      }
      if (existing) {
        existing.budgetAmountCny = body?.budgetAmountCny;
        existing.note = body?.note ?? '';
        existing.revision = currentRevision + 1;
        existing.updatedBy = actor;
        existing.updatedByDisplay = actorDisplay;
        existing.updatedAt = now;
        return { body: existing };
      }
      const created: Json = {
        id: `01990000-8000-7000-8000-${String(this.electricityBudgets.length + 1).padStart(12, '0')}`,
        tenantId: principal.context.tenantId,
        siteId: SITE_ID,
        billingMonth: body?.billingMonth,
        budgetAmountCny: body?.budgetAmountCny,
        note: body?.note ?? '',
        revision: 1,
        updatedBy: actor,
        updatedByDisplay: actorDisplay,
        createdAt: now,
        updatedAt: now,
      };
      this.electricityBudgets.unshift(created);
      return { body: created };
    }

    if (method === 'GET' && path === billsPath) {
      const billingMonth = query.get('billingMonth');
      const meterScope = query.get('meterScope');
      const status = query.get('status');
      const items = this.utilityBills
        .filter((item) =>
          (!billingMonth || item.billingMonth === billingMonth) &&
          (!meterScope || item.meterScope === meterScope) &&
          (!status || item.status === status))
        .sort((a, b) => b.billingMonth.localeCompare(a.billingMonth) || b.updatedAt.localeCompare(a.updatedAt));
      return { body: { items } };
    }

    if (method === 'POST' && path === billsPath) {
      if (!capabilities.includes('billing.manage')) {
        return refuse(403, 'BILLING_MANAGE_FORBIDDEN', 'billing.manage is required.');
      }
      if (body?.meta?.expectedRevision !== 0) {
        return refuse(409, 'REGISTRY_REVISION_CONFLICT', 'expectedRevision must be 0 when creating utility bill');
      }
      const created: Json = {
        id: `01990000-9000-7000-8000-${String(this.utilityBills.length + 1).padStart(12, '0')}`,
        tenantId: principal.context.tenantId,
        siteId: SITE_ID,
        billingMonth: body?.billingMonth,
        readingStartDate: body?.readingStartDate,
        readingEndDate: body?.readingEndDate,
        meterScope: body?.meterScope,
        billedEnergyKwh: body?.billedEnergyKwh,
        ...(body?.touBreakdown ? { touBreakdown: body.touBreakdown } : {}),
        energyChargeCny: body?.energyChargeCny,
        basicChargeMode: body?.basicChargeMode,
        basicChargeQuantity: body?.basicChargeMode === 'NONE' ? null : (body?.basicChargeQuantity ?? null),
        basicChargeCny: body?.basicChargeMode === 'NONE' ? 0 : body?.basicChargeCny,
        standardPowerFactor: body?.standardPowerFactor ?? null,
        actualPowerFactor: body?.actualPowerFactor ?? null,
        powerFactorAdjustmentCny: body?.powerFactorAdjustmentCny,
        otherChargesCny: body?.otherChargesCny,
        totalPayableCny: body?.totalPayableCny,
        chillerShareOverrideRatio: body?.meterScope === 'WHOLE_BUILDING' ? (body?.chillerShareOverrideRatio ?? null) : null,
        utilityAccountNo: body?.utilityAccountNo ?? '',
        invoiceNo: body?.invoiceNo ?? '',
        note: body?.note ?? '',
        status: 'DRAFT',
        confirmedBy: null,
        confirmedByDisplay: null,
        confirmedAt: null,
        updatedBy: actor,
        updatedByDisplay: actorDisplay,
        revision: 1,
        createdAt: now,
        updatedAt: now,
      };
      this.utilityBills.unshift(created);
      return { status: 201, body: created };
    }

    const updateMatch = path.match(new RegExp(`^${billsPath}/([^/]+)$`));
    if (method === 'PUT' && updateMatch) {
      if (!capabilities.includes('billing.manage')) {
        return refuse(403, 'BILLING_MANAGE_FORBIDDEN', 'billing.manage is required.');
      }
      const target = this.utilityBills.find((item) => item.id === updateMatch[1]);
      if (!target) return refuse(404, 'RESOURCE_NOT_FOUND', 'utility bill not found');
      if (target.status !== 'DRAFT') {
        return refuse(409, 'UTILITY_BILL_STATE_CONFLICT', 'confirmed utility bill cannot be modified');
      }
      if (body?.meta?.expectedRevision !== target.revision) {
        return refuse(409, 'REGISTRY_REVISION_CONFLICT', 'utility bill revision conflict');
      }
      delete target.touBreakdown;
      Object.assign(target, {
        billingMonth: body?.billingMonth,
        readingStartDate: body?.readingStartDate,
        readingEndDate: body?.readingEndDate,
        meterScope: body?.meterScope,
        billedEnergyKwh: body?.billedEnergyKwh,
        ...(body?.touBreakdown ? { touBreakdown: body.touBreakdown } : {}),
        energyChargeCny: body?.energyChargeCny,
        basicChargeMode: body?.basicChargeMode,
        basicChargeQuantity: body?.basicChargeMode === 'NONE' ? null : (body?.basicChargeQuantity ?? null),
        basicChargeCny: body?.basicChargeMode === 'NONE' ? 0 : body?.basicChargeCny,
        standardPowerFactor: body?.standardPowerFactor ?? null,
        actualPowerFactor: body?.actualPowerFactor ?? null,
        powerFactorAdjustmentCny: body?.powerFactorAdjustmentCny,
        otherChargesCny: body?.otherChargesCny,
        totalPayableCny: body?.totalPayableCny,
        chillerShareOverrideRatio: body?.meterScope === 'WHOLE_BUILDING' ? (body?.chillerShareOverrideRatio ?? null) : null,
        utilityAccountNo: body?.utilityAccountNo ?? '',
        invoiceNo: body?.invoiceNo ?? '',
        note: body?.note ?? '',
        updatedBy: actor,
        updatedByDisplay: actorDisplay,
        revision: (target.revision as number) + 1,
        updatedAt: now,
      });
      return { body: target };
    }

    const confirmMatch = path.match(new RegExp(`^${billsPath}/([^/]+)/confirm$`));
    if (method === 'POST' && confirmMatch) {
      if (!capabilities.includes('billing.manage')) {
        return refuse(403, 'BILLING_MANAGE_FORBIDDEN', 'billing.manage is required.');
      }
      const target = this.utilityBills.find((item) => item.id === confirmMatch[1]);
      if (!target) return refuse(404, 'RESOURCE_NOT_FOUND', 'utility bill not found');
      if (target.status !== 'DRAFT') {
        return refuse(409, 'UTILITY_BILL_STATE_CONFLICT', 'utility bill is already confirmed');
      }
      if (body?.meta?.expectedRevision !== target.revision) {
        return refuse(409, 'REGISTRY_REVISION_CONFLICT', 'utility bill revision conflict');
      }
      const duplicateConfirmed = this.utilityBills.some(
        (other) =>
          other.id !== target.id &&
          other.status === 'CONFIRMED' &&
          other.billingMonth === target.billingMonth &&
          other.meterScope === target.meterScope,
      );
      if (duplicateConfirmed) {
        return refuse(409, 'REGISTRY_BINDING_CONFLICT', 'duplicate confirmed utility bill for same month and meter scope');
      }
      target.status = 'CONFIRMED';
      target.confirmedBy = actor;
      target.confirmedByDisplay = actorDisplay;
      target.confirmedAt = now;
      if (body?.note) target.note = body.note;
      target.updatedBy = actor;
      target.updatedByDisplay = actorDisplay;
      target.revision = (target.revision as number) + 1;
      target.updatedAt = now;
      return { body: target };
    }

    return undefined;
  }

  // The M&V owner's ledger: implementing fits the recorded baseline over the fourteen days before the
  // implementation time; measures report at once only on different machines' sub-meters.
  private savings(method: string, path: string, query: URLSearchParams, body: Json | null, now: string): { status?: number; body: unknown } | undefined {
    const base = `/api/v1/sites/${SITE_ID}/savings`;
    const refuse = (status: number, code: string) => ({ status, body: { type: 'about:blank', title: 'Savings request refused', status, detail: code, instance: path, code, traceId: '0'.repeat(32), retryable: false } });
    if (method === 'GET' && path === `${base}/measures`) return { body: { items: this.measures.map(({ days: _days, ...measure }) => measure) } };
    if (method === 'GET' && path === `${base}/summary`) {
      const from = query.get('from')!;
      const to = query.get('to')!;
      const days = this.measures.flatMap((measure) => measure.days).filter((day: Json) => day.day >= from && day.day <= to);
      return { body: { from, to, totals: totalsOf(days) } };
    }
    if (method === 'POST' && path === `${base}/measures`) {
      if (body?.evidenceCommandId !== undefined && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(body.evidenceCommandId)) {
        return refuse(400, 'SAVINGS_REQUEST_INVALID');
      }
      const measure = {
        id: `01a12ef8-0000-7000-8000-${String(this.measures.length + 1).padStart(12, '0')}`, siteId: SITE_ID, name: body?.name, kind: body?.kind,
        description: body?.description ?? '', evidence: body?.evidence ?? '', evidenceCommandId: body?.evidenceCommandId ?? null,
        boundaryDeviceId: body?.boundaryDeviceId ?? null, status: 'PLANNED', plannedAt: body?.plannedAt, implementedAt: null, endedAt: null,
        investmentCny: body?.investmentCny ?? null, expectedAnnualKWh: body?.expectedAnnualKWh ?? null, baseline: null, totals: totalsOf([]), days: [],
        createdAt: now, updatedAt: now,
      };
      this.measures.unshift(measure);
      return { status: 201, body: measure };
    }
    const match = path.match(new RegExp(`^${base}/measures/([^/]+)(?:/(implement|refit|end))?$`));
    const measure = match ? this.measures.find((candidate) => candidate.id === match[1]) : undefined;
    if (!match) return undefined;
    if (!measure) return refuse(404, 'RESOURCE_NOT_FOUND');
    if (method === 'GET' && !match[2]) return { body: measure };
    if (method !== 'POST') return undefined;
    if (match[2] === 'implement') {
      if (measure.status !== 'PLANNED') return refuse(409, 'SAVINGS_MEASURE_STATE_CONFLICT');
      const sharesBoundary = (other: Json) => other.boundaryDeviceId === null || measure.boundaryDeviceId === null || other.boundaryDeviceId === measure.boundaryDeviceId;
      if (this.measures.some((other) => other.status === 'IMPLEMENTED' && sharesBoundary(other))) return refuse(409, 'SAVINGS_REPORTING_OVERLAP');
      const recorded = fixture<Json>('savings-measure.json').baseline;
      Object.assign(measure, {
        status: 'IMPLEMENTED', implementedAt: body?.implementedAt, updatedAt: now,
        baseline: { ...recorded, from: new Date(Date.parse(body?.implementedAt) - 14 * DAY_MS).toISOString(), to: body?.implementedAt },
      });
    } else if (match[2] === 'end') {
      if (measure.status !== 'IMPLEMENTED') return refuse(409, 'SAVINGS_MEASURE_STATE_CONFLICT');
      Object.assign(measure, { status: 'ENDED', endedAt: body?.endedAt, updatedAt: now });
    } else {
      return measure.days.length > 0 ? refuse(409, 'SAVINGS_BASELINE_LOCKED') : undefined;
    }
    return { body: measure };
  }

  private userAdminSequence = 100;
  private nextUserAdminId(kind: string): string {
    this.userAdminSequence += 1;
    return `018f3e00-${kind}-7000-8000-${String(this.userAdminSequence).padStart(12, '0')}`;
  }

  // The Gateway's user administration as the identity directory and IAM decide it.
  private userAdmin(method: string, path: string, body: Json | null): { status?: number; body: unknown } | undefined {
    if (!path.startsWith('/api/v1/iam/members') && path !== '/api/v1/me/password') return undefined;
    const refuse = (status: number, code: string) => ({ status, body: { type: 'about:blank', title: 'User administration refused', status, detail: code, instance: path, code, traceId: '0'.repeat(32), retryable: false } });
    const password = (value: string) => (value.length < 8 ? 'PASSWORD_TOO_SHORT' : ['password1', '12345678'].includes(value.toLowerCase()) ? 'PASSWORD_TOO_COMMON' : null);
    if (method === 'GET' && path === '/api/v1/iam/members') {
      return { body: { roles: [
        { roleKey: 'viewer', tenantScoped: false },
        { roleKey: 'operator', tenantScoped: false },
        { roleKey: 'site-manager', tenantScoped: false },
        { roleKey: 'admin', tenantScoped: true },
      ], members: this.members } };
    }
    if (method === 'POST' && path === '/api/v1/me/password') {
      if (body!.currentPassword !== 'current-secret') return refuse(400, 'CURRENT_PASSWORD_INVALID');
      return password(body!.newPassword) ? refuse(400, password(body!.newPassword)!) : { status: 204, body: undefined };
    }
    if (method === 'POST' && path === '/api/v1/iam/members') {
      if (this.members.some((member) => member.username === body!.username)) return refuse(409, 'USERNAME_TAKEN');
      if (password(body!.password)) return refuse(400, password(body!.password)!);
      const principalId = this.nextUserAdminId('2000');
      this.members.push({
        principalId, username: body!.username, displayName: body!.displayName, email: body!.email, enabled: true, locked: false, mfaEnabled: false, self: false,
        assignments: body!.assignments.map((assignment: Json) => ({ bindingId: this.nextUserAdminId('2200'), ...assignment })),
      });
      return { status: 201, body: { principalId } };
    }
    const match = path.match(/^\/api\/v1\/iam\/members\/([^/]+)\/(password|status|profile|assignments)(?:\/([^/]+)\/revoke)?$/);
    const member = match ? this.members.find((candidate) => candidate.principalId === match[1]) : undefined;
    if (method !== 'POST' || !match) return undefined;
    if (!member) return refuse(404, 'RESOURCE_NOT_FOUND');
    if (match[2] === 'password') return password(body!.password) ? refuse(400, password(body!.password)!) : { status: 204, body: undefined };
    if (match[2] === 'status') {
      if (member.self && !body!.enabled) return refuse(409, 'IAM_ADMIN_SELF_LOCKOUT');
      member.enabled = body!.enabled;
      return { status: 204, body: undefined };
    }
    if (match[2] === 'profile') { Object.assign(member, { displayName: body!.displayName, email: body!.email }); return { status: 204, body: undefined }; }
    if (match[3]) {
      const assignment = member.assignments.find((candidate: Json) => candidate.bindingId === match[3]);
      if (!assignment) return refuse(404, 'RESOURCE_NOT_FOUND');
      if (member.self && assignment.roleKey === 'admin') return refuse(409, 'IAM_ADMIN_SELF_LOCKOUT');
      member.assignments = member.assignments.filter((candidate: Json) => candidate !== assignment);
      return { status: 204, body: undefined };
    }
    if (body!.roleKey === 'admin' && body!.siteId) return refuse(400, 'USER_ADMIN_REQUEST_INVALID');
    member.assignments.push({ bindingId: this.nextUserAdminId('2200'), roleKey: body!.roleKey, ...(body!.siteId ? { siteId: body!.siteId } : {}) });
    return { status: 204, body: undefined };
  }

  private nextUuidV7(segment = '9100'): string {
    this.p1Seq += 1;
    return `01910000-${segment}-7000-8000-${String(this.p1Seq).padStart(12, '0')}`;
  }

  setGatewayOffline(gatewayId: string, offline: boolean) {
    if (offline) {
      this.offlineGateways.add(gatewayId);
    } else {
      this.offlineGateways.delete(gatewayId);
    }
  }

  setGatewayShadowState(gatewayId: string, shadow: Json) {
    this.configShadows.set(gatewayId, shadow);
  }

  setDeviceTags(deviceId: string, tags: Record<string, string>) {
    this.assetModel.devices = (this.assetModel.devices as Json[]).map((device) =>
      device.id === deviceId ? { ...device, tags } : device,
    );
  }

  setAssetTags(assetId: string, tags: Record<string, string>) {
    this.assetModel.assets = (this.assetModel.assets as Json[]).map((asset) =>
      asset.id === assetId ? { ...asset, tags } : asset,
    );
  }

  addCrossSiteSample(site: Json, asset?: Json, device?: Json) {
    if (!this.sites.some((item) => item.id === site.id)) {
      this.sites = [...this.sites, site];
    }
    if (asset) this.extraCrossSiteAssets.push(asset);
    if (device) this.extraCrossSiteDevices.push(device);
  }

  private getOrInitConfigShadow(gatewayId: string): Json {
    const existing = this.configShadows.get(gatewayId);
    if (existing) return existing;
    const initial: Json = {
      tenantId: this.assetModel.tenantId,
      gatewayId,
      syncStatus: 'UNCONFIGURED',
      drift: [],
    };
    this.configShadows.set(gatewayId, initial);
    return initial;
  }

  // The page names each request by its position in the batch, so recorded items answer by Device.
  private snapshotBatch(body: Json): Json {
    const items = body.requests.map((request: Json) => {
      const recorded = this.snapshots.items.find((item: Json) => item.deviceId === request.deviceId);
      if (!recorded) {
        const snapNow = new Date().toISOString();
        const values = (Array.isArray(request.keys) ? request.keys : []).map((key: string) => ({
          key,
          state: 'MISSING',
          freshness: 'MISSING',
          missingReason: 'NEVER_OBSERVED',
          policyRevision: 1,
        }));
        return {
          requestId: request.requestId,
          deviceId: request.deviceId,
          status: 'OK',
          snapshot: {
            schemaVersion: 1,
            deviceId: request.deviceId,
            tenantId: this.assetModel.tenantId,
            siteId: SITE_ID,
            businessRevision: 1,
            evaluatedAt: snapNow,
            evaluationAvailability: 'AVAILABLE',
            availabilityReasons: [],
            presence: {
              applicability: 'APPLICABLE',
              currentState: 'UNKNOWN',
              policyRevision: 1,
            },
            telemetryReadiness: 'MISSING',
            displayState: 'UNKNOWN',
            values,
          },
        };
      }
      if (recorded.status === 'OK' && Array.isArray(request.keys) && Array.isArray(recorded.snapshot?.values)) {
        const byKey = new Map<string, Json>(
          recorded.snapshot.values.map((entry: Json) => [entry.key, entry] as const),
        );
        const values = request.keys.map((key: string) => byKey.get(key) ?? {
          key,
          state: 'MISSING',
          freshness: 'MISSING',
          missingReason: 'NEVER_OBSERVED',
          policyRevision: 1,
        });
        return { ...recorded, requestId: request.requestId, snapshot: { ...recorded.snapshot, values } };
      }
      return { ...recorded, requestId: request.requestId };
    });
    return { ...this.snapshots, items };
  }

  private respond(method: string, path: string, query: URLSearchParams, body: Json | null, csrfToken?: string): { status?: number; body: unknown } | undefined {
    const now = new Date().toISOString();
    if (method === 'GET' && path === '/api/v1/principal') return { body: this.principalOverride ?? principal };
    if (method === 'GET' && path === '/api/v1/platform/status') return { body: fixture('platform-status.json') };
    if (method === 'POST' && path === '/api/v1/audit/search') {
      const activePrincipal = this.principalOverride ?? principal;
      const canReadAudit = Boolean(activePrincipal.authorization?.capabilities?.includes('audit.read'));
      if (this.auditDenied || !canReadAudit) {
        return {
          status: 403,
          body: { type: 'about:blank', title: 'Forbidden', status: 403, detail: 'Audit search access is forbidden.', instance: path, code: 'FORBIDDEN', traceId: '0'.repeat(32), retryable: false },
        };
      }
      const limit = typeof body?.limit === 'number' && Number.isFinite(body.limit)
        ? Math.max(1, Math.min(100, body.limit))
        : 100;
      const items = this.auditRecords
        .filter((record) =>
          (!body?.actor || record.actor === body.actor)
          && (!body?.action || record.action === body.action)
          && (!body?.resourceType || record.resourceType === body.resourceType)
          && (!body?.resourceId || record.resourceId === body.resourceId)
          && (!body?.outcome || record.outcome === body.outcome)
          && (!body?.from || Date.parse(record.occurredAt) >= Date.parse(body.from))
          && (!body?.to || Date.parse(record.occurredAt) <= Date.parse(body.to)))
        .sort((left, right) => right.ledgerSequence - left.ledgerSequence)
        .slice(0, limit);
      return { body: { items } };
    }
    const userAdmin = this.userAdmin(method, path, body);
    if (userAdmin) return userAdmin;
    if (method === 'GET' && path === '/api/v1/sites') return { body: { items: this.sites, hasMore: false, nextCursor: null } };
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}`) return { body: this.sites.find((item) => item.id === SITE_ID) ?? this.sites[0] };
    const registryP1Reply = this.registryP1(method, path, query, body, now);
    if (registryP1Reply) return registryP1Reply;
    const shadowReply = this.gatewayConfigShadow(method, path, body, csrfToken, now);
    if (shadowReply) return shadowReply;
    const gatewayCredentialReply = this.gatewayCredentials(method, path, body, csrfToken, now);
    if (gatewayCredentialReply) return gatewayCredentialReply;
    const gatewayDiagnosticsReply = this.gatewayDiagnostics(method, path, query, body, csrfToken, now);
    if (gatewayDiagnosticsReply) return gatewayDiagnosticsReply;
    const concentratorReply = this.concentrators(method, path, body, csrfToken, now);
    if (concentratorReply) return concentratorReply;
    const assetModelMatch = method === 'GET' ? path.match(/^\/api\/v1\/sites\/([^/]+)\/asset-model$/) : null;
    if (assetModelMatch) {
      const targetSiteId = decodeURIComponent(assetModelMatch[1]!);
      if (targetSiteId === SITE_ID) return { body: this.assetModel };
      if (this.sites.some((item) => item.id === targetSiteId)) {
        const siteAssets = this.extraCrossSiteAssets.filter((item) => item.siteId === targetSiteId);
        const siteDevices = this.extraCrossSiteDevices.filter((item) => item.siteId === targetSiteId);
        return {
          body: {
            schemaVersion: 2,
            tenantId: this.assetModel.tenantId,
            siteId: targetSiteId,
            spaces: [],
            assets: siteAssets,
            devices: siteDevices,
            sensors: [],
            telemetryPoints: [],
            relationships: [],
            counts: {
              spaces: 0,
              assets: siteAssets.length,
              deviceEndpoints: siteDevices.length,
              physicalSensors: 0,
              points: 0,
            },
          },
        };
      }
      return { status: 404, body: { type: 'about:blank', title: 'Resource not found', status: 404, detail: 'Site not found.', instance: path, code: 'RESOURCE_NOT_FOUND', traceId: '0'.repeat(32), retryable: false } };
    }
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/data-gaps`) return { body: { items: this.dataGaps } };
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/tariff`) {
      return this.tariff ? { body: this.tariff } : {
        status: 404,
        body: { type: 'about:blank', title: 'Resource not found', status: 404, detail: 'The requested Registry resource was not found.', instance: path, code: 'RESOURCE_NOT_FOUND', traceId: '0'.repeat(32), retryable: false },
      };
    }
    if (method === 'POST' && path === '/api/v1/telemetry/observation-snapshots:batchGet') return { body: this.snapshotBatch(body!) };
    if (method === 'POST' && path === '/api/v1/telemetry/device-series:aggregate') {
      const custom = this.aggregateHandler?.(body!);
      if (custom) return custom;
      if (body?.granularity === 'MINUTE' || body?.granularity === 'FIVE_MINUTE') {
        return { body: synthesizeTrendAggregate(this.assetModel, this.snapshots, body) };
      }
      if (body?.granularity === 'QUARTER_HOUR' && body.keys.length === 1 && body.keys[0] === 'active_power') {
        return { body: replayDemand(fixture<Json>('demand-quarter-hours.json'), body) };
      }
      if (body?.granularity === 'QUARTER_HOUR') {
        const demand = replayDemand(fixture<Json>('demand-quarter-hours.json'), body);
        return demand.buckets.length > 0
          ? { body: demand }
          : { body: synthesizeTrendAggregate(this.assetModel, this.snapshots, body) };
      }
      if (body?.granularity === 'DAY' && body.keys[0] === 'ambient_dry_bulb_temperature') {
        if (this.outdoorFailing) return { status: 503, body: { type: 'about:blank', title: 'Telemetry history unavailable', status: 503, detail: 'Telemetry Query Service is unavailable.', instance: path, code: 'TELEMETRY_HISTORY_UNAVAILABLE', traceId: '0'.repeat(32), retryable: true } };
        return { body: replayOutdoorDays(fixture<Json>('outdoor-days.json'), body) };
      }
      if (body?.granularity === 'HOUR' && body.keys.length === 1 && body.keys[0] === 'power') return { body: replayMachineHours(fixture<Json>('device-hours.json'), body) };
      if (body?.granularity === 'HOUR') {
        const history = replayDeviceHistory(fixture<Json>('history.json'), body);
        const requestedKeys = new Set<string>(body?.keys ?? []);
        const historyCoversAllKeys =
          requestedKeys.size > 0 &&
          Array.from(requestedKeys).every((key) => history.buckets.some((b: Json) => b.telemetryKey === key));
        if (historyCoversAllKeys) return { body: history };
      }
      return { body: synthesizeTrendAggregate(this.assetModel, this.snapshots, body!) };
    }
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/fdd/findings`) return { body: { items: this.findings } };
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/fdd/rules/low-delta-t`) {
      return this.fddRule ? { body: this.fddRule } : { status: 404, body: { status: 404, code: 'RESOURCE_NOT_FOUND', title: 'Resource not found' } };
    }
    if (method === 'POST' && path === `/api/v1/sites/${SITE_ID}/fdd/evaluate/low-delta-t`) return { status: 201, body: this.evaluateLowDeltaT(body!, now) };
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/optimization/recommendations/latest`) {
      return this.optimization ? { body: this.optimization } : { status: 404, body: { status: 404, code: 'RESOURCE_NOT_FOUND', title: 'Resource not found' } };
    }
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/optimization/preparations/latest`) {
      const defaults = {
        trigger: null,
        status: 'NOT_SCHEDULED',
        reason: null,
        firedAt: null,
        completedAt: null,
        responseCoefficient: { status: 'CALIBRATED', calibratedAt: '2026-10-01' },
        tenantStrategy: { status: 'CONFIRMED' },
      };
      return {
        body: this.optimizationPreparation
          ? { ...defaults, ...this.optimizationPreparation }
          : defaults,
      };
    }
    const autoControlReply = this.autoControl(method, path, body, now);
    if (autoControlReply) return autoControlReply;
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/optimization/shadow-evaluations/latest`) {
      this.shadowReads += 1;
      return this.optimizationShadow ? { body: this.optimizationShadow } : { status: 404, body: { status: 404, code: 'RESOURCE_NOT_FOUND', title: 'Resource not found' } };
    }
    if (method === 'POST' && path === '/api/v1/optimization/runs') {
      if (!this.nextRecommendation) return { status: 502, body: fixture('optimization-run-declined.json') };
      this.optimization = this.nextRecommendation;
      return { status: 202, body: { optimizationRunId: this.optimization.runId, inputSnapshotId: this.optimization.recommendation.inputSnapshotId, status: 'READY' } };
    }
    const forecastMatch = method === 'GET' ? path.match(/^\/api\/v1\/sites\/([^/]+)\/forecast\/load$/) : null;
    if (forecastMatch) {
      return forecastMatch[1] === SITE_ID
        ? { body: replayForecast(fixture<Json>('forecast-load.json'), this.forecastOrigin ?? now) }
        : { status: 404, body: { status: 404, code: 'RESOURCE_NOT_FOUND', title: 'Resource not found' } };
    }
    const scheduleReply = this.schedules(method, path, query, body, now);
    if (scheduleReply) return scheduleReply;
    const billingReply = this.electricityBilling(method, path, query, body, now);
    if (billingReply) return billingReply;
    if (path.startsWith(`/api/v1/sites/${SITE_ID}/savings/`)) return this.savings(method, path, query, body, now);
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/reports/monthly`) {
      return { body: { items: this.reportSnapshots.filter((stored) => stored.month.startsWith(`${query.get('year')}-`)) } };
    }
    if (method === 'GET' && path.startsWith(`/api/v1/sites/${SITE_ID}/reports/monthly/`)) {
      const stored = this.reportSnapshots.find((candidate) => path.endsWith(`/${candidate.month}`));
      return stored ? { body: stored } : { status: 404, body: { status: 404, code: 'RESOURCE_NOT_FOUND', title: 'Resource not found' } };
    }
    if (method === 'POST' && path === '/api/v1/analytics/energy-series' && this.analyticsDenied) {
      return { status: 403, body: { type: 'about:blank', title: 'Analytics access denied', status: 403, detail: 'The requested Site is not authorized for Energy Analytics.', instance: path, code: 'ANALYTICS_ACCESS_DENIED', traceId: '0'.repeat(32), retryable: false } };
    }
    if (method === 'POST' && path === '/api/v1/analytics/energy-series') {
      const custom = this.energySeriesHandler?.(body!);
      if (custom) return custom;
      if (body?.breakdown === 'device') {
        const recorded = fixture<Json>('energy-breakdown.json');
        const devices = recorded.response.devices.filter((device: Json) => !this.unmeteredDeviceIds.includes(device.deviceId));
        return { body: replayEnergy({ ...recorded.response, devices }, body, Date.parse(recorded.recordedDay)) };
      }
      const recorded = fixture<Json>('energy-series.json')[body?.energyType];
      return { body: this.energyByDay && body?.granularity === 'day' ? replayEnergyDays(recorded, body) : replayEnergy(recorded, body!) };
    }

    if (method === 'GET' && path === '/api/v1/alarms') {
      if (this.alarmsDenied) {
        return {
          status: 403,
          body: {
            error: { code: 'FORBIDDEN', message: 'Alarm list access is forbidden.', details: {} },
            meta: { requestId: 'e2e' },
          },
        };
      }
      const activeFrom = query.get('activeFrom');
      const activeTo = query.get('activeTo');
      const items = this.alarms.filter((alarm) =>
        (!query.get('condition') || alarm.condition === query.get('condition'))
        && (!query.has('acknowledged') || Boolean(alarm.acknowledgement) === (query.get('acknowledged') === 'true'))
        && (!query.has('suppressed') || Boolean(alarm.suppression) === (query.get('suppressed') === 'true'))
        && (!query.get('severity') || alarm.currentSeverity === query.get('severity'))
        && (!activeTo || Date.parse(alarm.firstOccurredAt) <= Date.parse(activeTo))
        && (!activeFrom || alarm.condition === 'ACTIVE' || !alarm.clearedAt || Date.parse(alarm.clearedAt) >= Date.parse(activeFrom)));
      return { body: { data: items, meta: { nextCursor: null, hasMore: false, limit: 100, requestId: 'e2e' } } };
    }
    if (method === 'GET' && path === '/api/v1/alarm-rules') {
      return { body: { data: query.get('siteId') === SITE_ID ? this.alarmRules : [], meta: { requestId: 'e2e' } } };
    }
    if (method === 'POST' && path === '/api/v1/alarm-rules/simulate') {
      if (!csrfToken) {
        return { status: 403, body: { type: 'about:blank', title: 'Forbidden', status: 403, detail: 'CSRF token required.', instance: path, code: 'CSRF_INVALID', traceId: '0'.repeat(32), retryable: false } };
      }
      if (query.get('siteId') !== SITE_ID) {
        return { status: 404, body: { type: 'about:blank', title: 'Not Found', status: 404, detail: 'Site not found.', instance: path, code: 'SITE_NOT_FOUND', traceId: '0'.repeat(32), retryable: false } };
      }
      const rule = this.alarmRules.find((candidate) => candidate.ruleId === body?.ruleId);
      if (!rule) {
        return { status: 404, body: { type: 'about:blank', title: 'Not Found', status: 404, detail: 'Alarm rule not found.', instance: path, code: 'RESOURCE_NOT_FOUND', traceId: '0'.repeat(32), retryable: false } };
      }
      const rawSteps = Array.isArray(body?.steps) ? body.steps : [];
      if (rawSteps.length === 0 || rawSteps.length > 32) {
        return { status: 400, body: { type: 'about:blank', title: 'Bad Request', status: 400, detail: 'Steps out of range.', instance: path, code: 'INVALID_ARGUMENT', traceId: '0'.repeat(32), retryable: false } };
      }
      const baseMs = Date.parse('2026-11-17T10:00:00Z');
      let active = false;
      let firstNumeric: number | undefined;
      const stepsOut = rawSteps.map((stepIn: Json, idx: number) => {
        const readingsList = Array.isArray(stepIn?.readings) ? stepIn.readings : [];
        const byPoint = new Map<string, unknown>(readingsList.map((r: Json) => [r.pointCode, r.value]));
        const tempVal = byPoint.get('supply_water_temperature') ?? byPoint.get('return_water_temperature');
        const numTemp = typeof tempVal === 'number' ? tempVal : Number.NaN;
        if (firstNumeric === undefined && Number.isFinite(numTemp)) firstNumeric = numTemp;
        let status: 'MATCHED' | 'NOT_MATCHED' | 'INDETERMINATE' = 'NOT_MATCHED';
        let effect: 'NONE' | 'PUBLISH' | 'CLEAR' = 'NONE';
        const matchedReasons: string[] = [];
        if (rule.raise?.kind === 'SLOPE') {
          const delta = typeof rule.raise?.delta === 'number' ? rule.raise.delta : 2;
          if (!active) {
            if (Number.isFinite(numTemp) && firstNumeric !== undefined && numTemp - firstNumeric >= delta) {
              status = 'MATCHED';
              effect = 'PUBLISH';
              active = true;
              matchedReasons.push('达到触发条件', '斜率变化达标');
            } else {
              matchedReasons.push('未触发告警条件');
            }
          } else if (Number.isFinite(numTemp) && numTemp <= 8 && byPoint.get('run_state') === 'RUNNING') {
            status = 'MATCHED';
            effect = 'CLEAR';
            active = false;
            matchedReasons.push('满足恢复条件');
          } else {
            status = 'MATCHED';
            matchedReasons.push('告警保持活动');
          }
        } else {
          if (!active && Number.isFinite(numTemp) && numTemp <= 10.5) {
            status = 'MATCHED';
            effect = 'PUBLISH';
            active = true;
            matchedReasons.push('达到触发条件');
          } else if (active && Number.isFinite(numTemp) && numTemp >= 11.5) {
            status = 'MATCHED';
            effect = 'CLEAR';
            active = false;
            matchedReasons.push('满足恢复条件');
          } else {
            status = active ? 'MATCHED' : 'NOT_MATCHED';
            matchedReasons.push(active ? '告警保持活动' : '未触发告警条件');
          }
        }
        return {
          step: idx + 1,
          evaluatedAt: new Date(baseMs + idx * 60_000).toISOString(),
          status,
          effect,
          active,
          matchedReasons,
        };
      });
      return {
        body: {
          data: {
            ruleId: rule.ruleId,
            title: rule.title,
            hopDepth: typeof rule.hopDepth === 'number' ? rule.hopDepth : 0,
            ...(Array.isArray(rule.dependsOnRuleIds) && rule.dependsOnRuleIds.length > 0 ? { dependsOnRuleIds: rule.dependsOnRuleIds } : {}),
            finalActive: active,
            steps: stepsOut,
          },
          meta: { requestId: 'e2e' },
        },
      };
    }
    const alarmMatch = path.match(/^\/api\/v1\/alarms\/([^/]+)(?:\/(ack|assign|unassign|suppress|unsuppress|evaluations))?$/);
    if (alarmMatch) {
      const alarm = this.alarms.find((candidate) => candidate.alarmId === alarmMatch[1]);
      if (!alarm) return { status: 404, body: { status: 404, code: 'RESOURCE_NOT_FOUND' } };
      if (method === 'GET' && alarmMatch[2] === 'evaluations') {
        const limit = Math.min(100, Math.max(1, Number(query.get('limit') ?? 25)));
        const items = (this.alarmEvaluations[alarm.alarmId] ?? [{
          evaluatedAt: alarm.firstOccurredAt,
          policyRevisionId: alarm.ruleRevision,
          assignmentId: '01910000-b000-7000-8000-00000000000b',
          inputRevision: 'eval-rev-1',
          status: 'MATCHED',
          effect: 'PUBLISH',
          snapshot: {
            asOf: alarm.firstOccurredAt,
            siteId: alarm.siteId,
            tenantId: alarm.tenantId,
            subjectType: 'SITE',
            subjectId: alarm.siteId,
            correlationId: alarm.incidentCorrelationId,
            inputRevision: 'eval-rev-1',
            inputs: {
              '01910000-0000-7000-8000-000000000005/return_water_temperature': { key: '01910000-0000-7000-8000-000000000005/return_water_temperature', present: true, value: { type: 'NUMBER', number: 10.2 }, quality: 'GOOD' },
              '01910000-0000-7000-8000-000000000002/power_total': { key: '01910000-0000-7000-8000-000000000002/power_total', present: true, value: { type: 'NUMBER', number: 372.5 }, quality: 'GOOD' },
            },
          },
          activeIncidentCorrelationId: alarm.incidentCorrelationId,
        }]).slice(0, limit);
        return { body: { data: { schemaVersion: 1, alarmId: alarm.alarmId, items }, meta: { requestId: 'e2e' } } };
      }
      if (method === 'POST' && alarmMatch[2] === 'ack') {
        alarm.acknowledgement = { acknowledgedAt: now, acknowledgedBy: MY_ID, ...(body?.comment ? { comment: body.comment } : {}) };
        this.appendAlarmEntry(alarm, 'ACKNOWLEDGE', body?.comment ?? '', now);
      } else if (method === 'POST' && alarmMatch[2] === 'assign') {
        alarm.assigneeId = body?.assigneeId;
        this.appendAlarmEntry(alarm, 'ASSIGN', body?.reason ?? '', now);
      } else if (method === 'POST' && alarmMatch[2] === 'unassign') {
        delete alarm.assigneeId;
        this.appendAlarmEntry(alarm, 'UNASSIGN', body?.reason ?? '', now);
      } else if (method === 'POST' && alarmMatch[2] === 'suppress') {
        alarm.suppression = {
          startsAt: now,
          expiresAt: body?.suppressedUntil,
          reason: body?.reason ?? '',
          actorId: MY_ID,
          policyRevision: '1',
        };
        this.appendAlarmEntry(alarm, 'SUPPRESS', body?.reason ?? '', now);
      } else if (method === 'POST' && alarmMatch[2] === 'unsuppress') {
        delete alarm.suppression;
        this.appendAlarmEntry(alarm, 'UNSUPPRESS', body?.reason ?? '', now);
      } else if (method !== 'GET' || alarmMatch[2] !== undefined) {
        return undefined;
      }
      return { body: { data: alarm } };
    }

    if (method === 'GET' && path === '/api/v1/commands') {
      if (this.commandsDenied) {
        return {
          status: 403,
          body: { type: 'about:blank', title: 'Forbidden', status: 403, detail: 'Command access is forbidden.', code: 'FORBIDDEN' },
        };
      }
      return { body: { data: this.commands } };
    }
    if (method === 'POST' && path === '/api/v1/commands') {
      if (this.controlState.controlMode !== 'REMOTE_AUTO') return { status: 409, body: { status: 409, code: 'COMMAND_CONTROL_LOCKED', detail: 'Site control is not remote auto.' } };
      const point = this.assetModel.telemetryPoints.find((candidate: Json) => candidate.id === body?.commandPointId);
      if (this.controlState.assetLocks.some((lock: Json) => lock.assetId === body?.assetId && lock.lockMode !== 'UNLOCKED')) {
        return { status: 409, body: { status: 409, code: 'COMMAND_CONTROL_LOCKED', detail: 'Asset is locked.' } };
      }
      if (this.controlState.activeMaintenanceWindow && !['STOP', 'STOP_UNIT_SEQUENCE', 'RESET_FAULT'].includes(point?.sourceMetadata?.capability)) {
        return { status: 409, body: { status: 409, code: 'COMMAND_CONTROL_LOCKED', detail: 'Only stop and reset commands are permitted during the maintenance window.' } };
      }
      if (point && this.priorityLockedDeviceIds.has(point.reportingDeviceId)) {
        return { status: 409, body: { status: 409, code: 'DEVICE_PRIORITY_LOCKED', detail: 'Device is locked at a higher priority.' } };
      }
      if (HIGH_RISK.includes(point?.sourceMetadata?.capability)) {
        if (this.stepUpFailureReason) {
          return {
            status: 428,
            body: {
              status: 428,
              code: 'STEP_UP_REQUIRED',
              detail: 'High-risk commands failed step-up verification.',
              reason: this.stepUpFailureReason,
            },
          };
        }
        if (!/^\d{6}$/.test(body?.verificationCode ?? '')) {
          return { status: 428, body: { status: 428, code: 'STEP_UP_REQUIRED', detail: 'High-risk commands require a 6-digit verificationCode from your authenticator app.' } };
        }
      }
      return { status: 202, body: this.submitCommand(body!, now) };
    }
    const cancellation = path.match(/^\/api\/v1\/commands\/([^/]+)\/cancel$/);
    if (method === 'POST' && cancellation) {
      const command = this.commands.find((candidate) => candidate.commandId === cancellation[1]);
      if (!command || !['AWAITING_APPROVAL', 'QUEUED'].includes(command.status)) {
        return { status: 409, body: { status: 409, code: 'COMMAND_CANCEL_INVALID', detail: 'Only a pending or queued Command can be cancelled before dispatch begins.' } };
      }
      advance(command, 'CANCELLED', now);
      return { body: command };
    }
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/control-state`) return { body: this.controlState };
    if (method === 'PUT' && path === `/api/v1/sites/${SITE_ID}/control-mode`) return this.updateControlMode(body!, now);
    if (method === 'POST' && path === `/api/v1/sites/${SITE_ID}/control-shutdown`) return this.shutdownSite(body!, now);
    const lockMatch = path.match(/^\/api\/v1\/assets\/([^/]+)\/control-lock$/);
    if (method === 'PUT' && lockMatch) return this.updateControlLock(lockMatch[1], body!, now);
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/optimization/staging-recommendations/latest`) {
      return this.staging ? { body: this.staging } : { status: 404, body: { status: 404, code: 'RESOURCE_NOT_FOUND', title: 'Resource not found' } };
    }
    const approval = path.match(/^\/api\/v1\/commands\/([^/]+)\/approve$/);
    if (method === 'POST' && approval) {
      const command = this.commands.find((candidate) => candidate.commandId === approval[1]);
      if (!command || command.status !== 'AWAITING_APPROVAL') return { status: 409, body: { status: 409, code: 'COMMAND_APPROVAL_INVALID', detail: 'The Command is not awaiting another approval.' } };
      if ((command.risk === 'HIGH' || command.risk === 'CRITICAL') && !this.stepUpFresh) {
        return { status: 428, body: { status: 428, code: 'STEP_UP_REQUIRED', detail: 'This high-risk Command approval requires recent multi-factor authentication.' } };
      }
      command.approvalCount += 1;
      advance(command, 'APPROVED', now);
      advance(command, 'SUCCEEDED', now);
      return { body: command };
    }

    const sitePrefix = `/api/v1/sites/${SITE_ID}/work-orders`;
    if (method === 'GET' && path === sitePrefix) {
      const items = this.workOrders.filter((order) =>
        (!query.get('status') || order.status === query.get('status'))
        && (!query.get('assigneeId') || order.assigneeId === query.get('assigneeId'))
        && (!query.get('sourceDomain') || order.sourceReferences.some((source: Json) =>
          source.domain === query.get('sourceDomain') && source.resourceId === query.get('sourceRef'))));
      return { body: { schemaVersion: 1, items, nextCursor: null, hasMore: false } };
    }
    if (method === 'POST' && path === sitePrefix) {
      const order = {
        schemaVersion: 1, workOrderId: `01a10000-0000-7000-8000-${String(this.workOrders.length).padStart(12, '0')}`,
        tenantId: principal.context.tenantId, siteId: SITE_ID, title: body?.title, description: body?.description,
        priority: body?.priority, status: 'OPEN', sourceReferences: body?.sourceReferences ?? [],
        tasks: { total: 0, completed: 0, blocked: 0 }, noteCount: 0, attachmentCount: 0, completionEvidence: [],
        version: 1, createdAt: now, updatedAt: now,
        timeline: [{ operation: 'CREATE', toStatus: 'OPEN', reason: 'WORK_ORDER_CREATED', actorType: 'PRINCIPAL', actorId: MY_ID, occurredAt: now, version: 1 }],
      };
      this.workOrders.push(order);
      return { status: 201, body: order };
    }
    const orderMatch = path.match(new RegExp(`^${sitePrefix}/([^/:]+)(?::([a-z]+))?$`));
    if (orderMatch) {
      const order = this.workOrders.find((candidate) => candidate.workOrderId === orderMatch[1]);
      if (!order) return { status: 404, body: { status: 404, code: 'RESOURCE_NOT_FOUND' } };
      if (method === 'GET') return { body: order };
      const operation = orderMatch[2];
      if (operation === 'assign') {
        order.assigneeId = body?.assigneeId ?? undefined;
        this.appendOrderEntry(order, 'ASSIGN', order.status, body?.reason, now);
        return { body: order };
      }
      const next = ({ start: 'IN_PROGRESS', block: 'BLOCKED', resume: 'IN_PROGRESS', complete: 'COMPLETED', cancel: 'CANCELLED', reopen: 'OPEN' } as Record<string, string>)[operation ?? ''];
      if (!next) return undefined;
      if (operation === 'complete') order.completionEvidence = body?.completionEvidence ?? [];
      this.appendOrderEntry(order, operation!.toUpperCase(), next, body?.reason, now);
      order.status = next;
      return { body: order };
    }
    return undefined;
  }

  private autoControl(method: string, path: string, body: Json | null, now: string): { status?: number; body: unknown } | undefined {
    const leasePath = `/api/v1/sites/${SITE_ID}/optimization/auto-control-lease`;
    const revokePath = `${leasePath}/revoke`;
    const monitorsPath = `/api/v1/sites/${SITE_ID}/optimization/execution-monitors`;
    if (path !== leasePath && path !== revokePath && path !== monitorsPath) return undefined;

    const refuse = (status: number, code: string, detail: string, reason?: string) => ({
      status,
      body: {
        type: 'about:blank',
        title: code,
        status,
        detail,
        instance: path,
        code,
        ...(reason ? { reason } : {}),
        traceId: '0'.repeat(32),
        retryable: false,
      },
    });

    const activePrincipal = this.principalOverride ?? principal;
    const caps = new Set<string>(activePrincipal.authorization?.capabilities ?? []);

    if (method === 'GET' && path === leasePath) {
      if (!caps.has('site.read')) return refuse(403, 'FORBIDDEN', 'site.read required.');
      const active = this.autoControlLeaseState.activeLease;
      if (active && Date.parse(active.expiresAt) <= Date.parse(now)) {
        const expired = { ...active, status: 'EXPIRED', updatedAt: now };
        this.autoControlLeaseState = {
          ...this.autoControlLeaseState,
          confirmationMode: 'MANUAL_CONFIRM',
          activeLease: null,
          latestLease: expired,
        };
      }
      return { body: this.autoControlLeaseState };
    }

    if (method === 'GET' && path === monitorsPath) {
      if (!caps.has('site.read')) return refuse(403, 'FORBIDDEN', 'site.read required.');
      return {
        body: {
          tenantId: principal.context.tenantId,
          siteId: SITE_ID,
          items: this.optimizationExecutionMonitors,
        },
      };
    }

    if (method === 'PUT' && path === leasePath) {
      if (!caps.has('schedule.manage')) {
        return refuse(403, 'CONTROL_EXIT_FORBIDDEN', 'Schedule manage authorization is required to arm automatic control leases.');
      }
      if (this.stepUpFailureReason) {
        return refuse(428, 'STEP_UP_REQUIRED', 'Arming an automatic control lease failed step-up verification.', this.stepUpFailureReason);
      }
      if (!/^\d{6}$/.test(body?.verificationCode ?? '')) {
        return refuse(428, 'STEP_UP_REQUIRED', 'Arming an automatic control lease requires a 6-digit verificationCode.');
      }
      const expiresInHours = Number(body?.expiresInHours ?? 0);
      const supplyMin = Number(body?.supplyTempMinC);
      const supplyMax = Number(body?.supplyTempMaxC);
      const maxStep = Number(body?.maxSetpointStepC ?? 1.0);
      const zoneMax = Number(body?.zoneTempMaxC ?? 26.0);
      const minChillers = Number(body?.minRunningChillers ?? 1);
      const maxPerHour = Number(body?.maxCommandsPerHour ?? 2);
      const allowedCaps: string[] = Array.isArray(body?.allowedCapabilities) ? body.allowedCapabilities : [];
      const reason = String(body?.reason ?? '').trim();

      const valid =
        Number.isInteger(expiresInHours) && expiresInHours >= 1 && expiresInHours <= 24
        && allowedCaps.length >= 1
        && Number.isFinite(supplyMin) && supplyMin >= 4 && supplyMin <= 20
        && Number.isFinite(supplyMax) && supplyMax >= supplyMin && supplyMax <= 25
        && Number.isFinite(maxStep) && maxStep > 0 && maxStep <= 2
        && Number.isFinite(zoneMax) && zoneMax >= 18 && zoneMax <= 32
        && Number.isInteger(minChillers) && minChillers >= 0
        && Number.isInteger(maxPerHour) && maxPerHour >= 1 && maxPerHour <= 6
        && reason.length >= 1 && reason.length <= 1000;
      if (!valid) {
        return refuse(400, 'REGISTRY_MUTATION_INVALID', 'The auto control lease payload is invalid.');
      }

      const currentActive = this.autoControlLeaseState.activeLease;
      if (currentActive && body?.expectedRevision !== undefined && body.expectedRevision !== currentActive.revision) {
        return refuse(409, 'REGISTRY_REVISION_CONFLICT', 'The expected auto control lease revision is stale.');
      }

      const expiresAt = new Date(Date.parse(now) + expiresInHours * 3_600_000).toISOString();
      const lease: Json = {
        id: `01990000-8800-7000-8000-${String(this.writes.length + 1).padStart(12, '0')}`,
        tenantId: principal.context.tenantId,
        siteId: SITE_ID,
        status: 'ACTIVE',
        armedBy: activePrincipal.principalId,
        armedAt: now,
        expiresAt,
        allowedCapabilities: allowedCaps,
        maxSetpointStepC: maxStep,
        supplyTempMinC: supplyMin,
        supplyTempMaxC: supplyMax,
        zoneTempMaxC: zoneMax,
        minRunningChillers: minChillers,
        maxCommandsPerHour: maxPerHour,
        revision: 1,
        createdAt: now,
        updatedAt: now,
      };
      this.autoControlLeaseState = {
        tenantId: principal.context.tenantId,
        siteId: SITE_ID,
        confirmationMode: 'AUTO_WITH_LEASE',
        activeLease: lease,
        latestLease: lease,
      };
      return { body: this.autoControlLeaseState };
    }

    if (method === 'POST' && path === revokePath) {
      if (!caps.has('schedule.manage')) {
        return refuse(403, 'CONTROL_EXIT_FORBIDDEN', 'Schedule manage authorization is required to revoke automatic control leases.');
      }
      const reason = String(body?.reason ?? '').trim();
      if (!reason || reason.length > 1000) {
        return refuse(400, 'AUTO_CONTROL_LEASE_REQUEST_INVALID', 'reason is required.');
      }
      const currentActive = this.autoControlLeaseState.activeLease;
      if (!currentActive) {
        return refuse(404, 'RESOURCE_NOT_FOUND', 'No active auto control lease to revoke.');
      }
      if (body?.expectedRevision !== undefined && body.expectedRevision !== currentActive.revision) {
        return refuse(409, 'REGISTRY_REVISION_CONFLICT', 'The expected auto control lease revision is stale.');
      }
      const revoked: Json = {
        ...currentActive,
        status: 'REVOKED',
        tripReason: reason,
        revision: currentActive.revision + 1,
        updatedAt: now,
      };
      this.autoControlLeaseState = {
        tenantId: principal.context.tenantId,
        siteId: SITE_ID,
        confirmationMode: 'MANUAL_CONFIRM',
        activeLease: null,
        latestLease: revoked,
      };
      return { body: this.autoControlLeaseState };
    }

    return undefined;
  }

  private appendAlarmEntry(alarm: Json, operation: string, reason: string, now: string) {
    alarm.version += 1;
    alarm.timeline.push({ operation, condition: alarm.condition, reason, actorType: 'PRINCIPAL', actorId: MY_ID, currentSeverity: alarm.currentSeverity, occurredAt: now, version: alarm.version });
  }

  private appendOrderEntry(order: Json, operation: string, toStatus: string, reason: string | undefined, now: string) {
    order.version += 1;
    order.updatedAt = now;
    order.timeline.push({ operation, toStatus, reason: reason ?? '', actorType: 'PRINCIPAL', actorId: MY_ID, occurredAt: now, version: order.version });
  }

  seedGatewayCredentials(gatewayId: string, options: { revoked?: boolean; records: Json[] }) {
    this.gatewayCredentialsState.set(gatewayId, {
      revoked: Boolean(options.revoked),
      enrollmentSeq: 0,
      certSeq: options.records.length,
      records: options.records.map((record) => ({ ...record })),
    });
  }

  issueGatewayCertificate(
    gatewayId: string,
    options?: { serialNumber?: string; fingerprintSha256?: string; validDays?: number },
  ): Json {
    const now = new Date().toISOString();
    const state = this.getOrInitGatewayCredentialState(gatewayId, now);
    state.certSeq += 1;
    const seqHex = String(state.certSeq).padStart(12, '0');
    const validDays = options?.validDays ?? 90;
    const record: Json = {
      id: `0193f100-0000-7000-8000-${seqHex}`,
      gatewayId,
      fingerprintSha256: options?.fingerprintSha256
        ?? `${String(state.certSeq).padStart(8, '0')}a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c`,
      serialNumber: options?.serialNumber ?? `1a2b3c4d5e6f${state.certSeq.toString(16).padStart(4, '0')}`,
      status: 'ACTIVE',
      validFrom: now,
      validUntil: new Date(Date.parse(now) + validDays * DAY_MS).toISOString(),
      revokedAt: null,
      revocationReason: null,
      createdAt: now,
    };
    state.records.unshift(record);
    return record;
  }

  private getOrInitGatewayCredentialState(
    gatewayId: string,
    now: string,
  ): { revoked: boolean; enrollmentSeq: number; certSeq: number; records: Json[] } {
    const existing = this.gatewayCredentialsState.get(gatewayId);
    if (existing) return existing;
    const created: { revoked: boolean; enrollmentSeq: number; certSeq: number; records: Json[] } = {
      revoked: false,
      enrollmentSeq: 0,
      certSeq: 1,
      records: [
        {
          id: '0193f100-0000-7000-8000-000000000001',
          gatewayId,
          fingerprintSha256: '00000001a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c',
          serialNumber: '1a2b3c4d5e6f7a8b',
          status: 'ACTIVE',
          validFrom: new Date(Date.parse(now) - 7 * DAY_MS).toISOString(),
          validUntil: new Date(Date.parse(now) + 83 * DAY_MS).toISOString(),
          revokedAt: null,
          revocationReason: null,
          createdAt: new Date(Date.parse(now) - 7 * DAY_MS).toISOString(),
        },
      ],
    };
    this.gatewayCredentialsState.set(gatewayId, created);
    return created;
  }

  private gatewayCredentials(
    method: string,
    path: string,
    body: Json | null,
    csrfToken: string | undefined,
    now: string,
  ): { status?: number; body: unknown } | undefined {
    const certRevokeMatch = path.match(/^\/api\/v1\/gateways\/([^/]+)\/credentials\/([^/]+)\/revoke$/);
    const match = certRevokeMatch
      ? null
      : path.match(/^\/api\/v1\/gateways\/([^/]+)\/(credential|credentials|enrollment-code|rotate|revoke)$/);
    if (!certRevokeMatch && !match) return undefined;

    const gatewayId = decodeURIComponent((certRevokeMatch ? certRevokeMatch[1] : match![1])!);
    const refuse = (status: number, code: string, detail: string) => ({
      status,
      body: { type: 'about:blank', title: detail, status, detail, instance: path, code, traceId: '0'.repeat(32), retryable: false },
    });

    const gatewayDevice = (this.assetModel.devices as Json[]).find((d) => d.id === gatewayId && d.deviceType === 'GATEWAY');
    if (!gatewayDevice) return refuse(404, 'GATEWAY_NOT_FOUND', 'Gateway not found');

    const activePrincipal = this.principalOverride ?? principal;
    const caps = new Set<string>(activePrincipal.authorization?.capabilities ?? []);
    if (method === 'GET' && !caps.has('device.read')) {
      return refuse(403, 'CREDENTIAL_FORBIDDEN', 'device.read required');
    }
    if (method === 'POST') {
      if (!caps.has('device.write')) {
        return refuse(403, 'CREDENTIAL_FORBIDDEN', 'device.write required');
      }
      if (!csrfToken) {
        return refuse(403, 'CSRF_INVALID', 'CSRF token required');
      }
    }

    const state = this.getOrInitGatewayCredentialState(gatewayId, now);

    if (certRevokeMatch && method === 'POST') {
      const credentialId = decodeURIComponent(certRevokeMatch[2]!);
      const targetIndex = state.records.findIndex((record) => record.id === credentialId);
      if (targetIndex < 0) return refuse(404, 'GATEWAY_NOT_FOUND', 'Gateway certificate not found');
      let trimmedReason: string | null = null;
      if (body?.reason !== undefined && body.reason !== null) {
        if (typeof body.reason !== 'string') {
          return refuse(400, 'ENROLLMENT_REJECTED', 'Invalid revocation reason');
        }
        const candidate = body.reason.trim();
        if (!candidate || candidate.length > 512) {
          return refuse(400, 'ENROLLMENT_REJECTED', 'Revocation reason must be 1..512 characters');
        }
        trimmedReason = candidate;
      }
      const current = state.records[targetIndex]!;
      if (current.status === 'ACTIVE') {
        state.records[targetIndex] = {
          ...current,
          status: 'REVOKED',
          revokedAt: now,
          revocationReason: trimmedReason,
        };
      }
      return { body: state.records[targetIndex] };
    }

    const action = match?.[2];
    if (method === 'GET' && action === 'credential') {
      if (state.revoked) return { body: { status: 'REVOKED' } };
      const activeRecords = state.records.filter((record) => record.status === 'ACTIVE');
      if (activeRecords.length === 0) return { body: { status: 'NOT_ENROLLED' } };
      const maxExpiryMs = Math.max(...activeRecords.map((record) => Date.parse(record.validUntil)));
      const expiresAt = new Date(maxExpiryMs).toISOString();
      const nowMs = Date.parse(now);
      const status = maxExpiryMs <= nowMs
        ? 'EXPIRED'
        : maxExpiryMs < nowMs + 14 * DAY_MS
          ? 'EXPIRING'
          : 'ACTIVE';
      return { body: { status, expiresAt } };
    }

    if (method === 'GET' && action === 'credentials') {
      return { body: { items: state.records } };
    }

    if (method === 'POST' && action === 'enrollment-code') {
      if (state.revoked) return refuse(409, 'GATEWAY_REVOKED', 'Gateway identity has been revoked.');
      state.enrollmentSeq += 1;
      const enrollmentCode = `${String(state.enrollmentSeq).padStart(4, '0')}${'A'.repeat(39)}`;
      return { body: { enrollmentCode, expiresAt: new Date(Date.parse(now) + DAY_MS).toISOString() } };
    }

    if (method === 'POST' && action === 'rotate') {
      state.revoked = false;
      state.enrollmentSeq += 1;
      const enrollmentCode = `${String(state.enrollmentSeq).padStart(4, '0')}${'R'.repeat(39)}`;
      return { body: { enrollmentCode, expiresAt: new Date(Date.parse(now) + DAY_MS).toISOString() } };
    }

    if (method === 'POST' && action === 'revoke') {
      state.revoked = true;
      state.records = state.records.map((record) => (
        record.status === 'ACTIVE'
          ? { ...record, status: 'REVOKED', revokedAt: now }
          : record
      ));
      return { body: { status: 'REVOKED' } };
    }

    return undefined;
  }

  seedGatewayQuarantine(gatewayId: string, records: Json[]) {
    this.gatewayQuarantineState.set(gatewayId, records.map((record) => ({ ...record })));
  }

  seedGatewayPacketCapture(gatewayId: string, snapshot: Json) {
    this.gatewayPacketCaptureState.set(gatewayId, {
      ...snapshot,
      items: Array.isArray(snapshot.items) ? snapshot.items.map((item: Json) => ({ ...item })) : [],
    });
  }

  private getOrInitGatewayQuarantine(gatewayId: string, now: string): Json[] {
    const existing = this.gatewayQuarantineState.get(gatewayId);
    if (existing) return existing;
    const created: Json[] = [
      {
        id: '0193f200-0000-7000-8000-000000000001',
        gatewayId,
        topic: 'devices/eg8200-commercial-001/telemetry/chiller-01',
        reasonCode: 'MESSAGE_INVALID',
        detail: '报文载荷缺少必填字段 sampledAt',
        payloadSha256: 'a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90',
        payloadBytes: 86,
        payloadPreview: '{"deviceCode":"CHILLER-01","points":{"chw_supply_temp":7.4,"active_power":198.6}}',
        payloadEncoding: 'utf8',
        payloadTruncated: false,
        occurrenceCount: 4,
        receivedAt: new Date(Date.parse(now) - 18 * 60_000).toISOString(),
        lastSeenAt: new Date(Date.parse(now) - 2 * 60_000).toISOString(),
      },
      {
        id: '0193f200-0000-7000-8000-000000000002',
        gatewayId,
        topic: 'devices/eg8200-commercial-001/telemetry/meter-total',
        reasonCode: 'GATEWAY_CREDENTIAL_INACTIVE',
        detail: '网关客户端证书已吊销，当前上行报文已拒绝入库',
        payloadSha256: 'b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90ab',
        payloadBytes: 34,
        payloadPreview: '7b22746f706963223a2274656c656d65747279222c2276616c7565223a3431322e357d',
        payloadEncoding: 'hex',
        payloadTruncated: false,
        occurrenceCount: 12,
        receivedAt: new Date(Date.parse(now) - 45 * 60_000).toISOString(),
        lastSeenAt: new Date(Date.parse(now) - 8 * 60_000).toISOString(),
      },
    ];
    this.gatewayQuarantineState.set(gatewayId, created);
    return created;
  }

  private getOrInitGatewayPacketCapture(gatewayId: string): Json {
    const existing = this.gatewayPacketCaptureState.get(gatewayId);
    if (existing) return existing;
    const created: Json = {
      gatewayId,
      active: false,
      maxPackets: 200,
      droppedCount: 0,
      items: [],
    };
    this.gatewayPacketCaptureState.set(gatewayId, created);
    return created;
  }

  private gatewayDiagnostics(
    method: string,
    path: string,
    query: URLSearchParams,
    body: Json | null,
    csrfToken: string | undefined,
    now: string,
  ): { status?: number; body: unknown } | undefined {
    const match = path.match(/^\/api\/v1\/gateways\/([^/]+)\/(quarantine|packet-capture|packet-capture\/start|packet-capture\/stop)$/);
    if (!match) return undefined;

    const gatewayId = decodeURIComponent(match[1]!);
    const action = match[2]!;
    const refuse = (status: number, code: string, detail: string) => ({
      status,
      body: { type: 'about:blank', title: detail, status, detail, instance: path, code, traceId: '0'.repeat(32), retryable: false },
    });

    const gatewayDevice = (this.assetModel.devices as Json[]).find((d) => d.id === gatewayId && d.deviceType === 'GATEWAY');
    if (!gatewayDevice) return refuse(404, 'GATEWAY_NOT_FOUND', 'Gateway not found');

    const activePrincipal = this.principalOverride ?? principal;
    const caps = new Set<string>(activePrincipal.authorization?.capabilities ?? []);
    if (method === 'GET' && !caps.has('device.read')) {
      return refuse(403, 'CREDENTIAL_FORBIDDEN', 'device.read required');
    }
    if (method === 'POST') {
      if (!caps.has('device.write')) {
        return refuse(403, 'CREDENTIAL_FORBIDDEN', 'device.write required');
      }
      if (!csrfToken) {
        return refuse(403, 'CSRF_INVALID', 'CSRF token required');
      }
    }

    if (method === 'GET' && action === 'quarantine') {
      const requestedLimit = Number(query.get('limit') ?? '20');
      const limit = Number.isInteger(requestedLimit) && requestedLimit >= 1 && requestedLimit <= 50 ? requestedLimit : 20;
      const items = this.getOrInitGatewayQuarantine(gatewayId, now).slice(0, limit);
      return { body: { items } };
    }

    if (method === 'GET' && action === 'packet-capture') {
      return { body: this.getOrInitGatewayPacketCapture(gatewayId) };
    }

    if (method === 'POST' && action === 'packet-capture/start') {
      const durationSeconds = body?.durationSeconds === undefined ? 300 : Number(body.durationSeconds);
      if (!Number.isInteger(durationSeconds) || durationSeconds < 30 || durationSeconds > 900) {
        return refuse(400, 'PACKET_CAPTURE_REQUEST_INVALID', 'durationSeconds must be an integer between 30 and 900');
      }
      const nextState: Json = {
        gatewayId,
        active: true,
        startedAt: now,
        expiresAt: new Date(Date.parse(now) + durationSeconds * 1000).toISOString(),
        durationSeconds,
        maxPackets: 200,
        droppedCount: 0,
        items: [
          {
            sequence: 1,
            capturedAt: new Date(Date.parse(now) - 3_000).toISOString(),
            direction: 'UPLINK',
            channel: 'TELEMETRY',
            topic: 'devices/eg8200-commercial-001/telemetry/chiller-01',
            traceId: '4bf92f3577b34da6a3ce929d0e0e4736',
            payloadBytes: 128,
            payloadSha256: 'c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90abcd',
            payloadPreview: '{"schemaVersion":2,"points":{"chw_supply_temp":7.1,"active_power":184.2}}',
            payloadEncoding: 'utf8',
            payloadTruncated: false,
          },
          {
            sequence: 2,
            capturedAt: new Date(Date.parse(now) - 2_000).toISOString(),
            direction: 'DOWNLINK',
            channel: 'COMMAND',
            topic: 'devices/eg8200-commercial-001/commands/request',
            traceId: '8a1f2c3d4e5f60718293a4b5c6d7e8f9',
            payloadBytes: 112,
            payloadSha256: 'd4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90abcdef',
            payloadPreview: '{"capability":"SET_CHW_TEMP","parameters":{"targetTempC":7.5}}',
            payloadEncoding: 'utf8',
            payloadTruncated: false,
          },
          {
            sequence: 3,
            capturedAt: new Date(Date.parse(now) - 1_000).toISOString(),
            direction: 'UPLINK',
            channel: 'QUARANTINE',
            topic: 'devices/eg8200-commercial-001/telemetry/unknown-point',
            payloadBytes: 54,
            payloadSha256: 'e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90abcdef01',
            payloadPreview: '{"pointCode":"legacy_aux_pressure","value":0.42}',
            payloadEncoding: 'utf8',
            payloadTruncated: false,
          },
        ],
      };
      this.gatewayPacketCaptureState.set(gatewayId, nextState);
      return { body: nextState };
    }

    if (method === 'POST' && action === 'packet-capture/stop') {
      const current = this.getOrInitGatewayPacketCapture(gatewayId);
      const stoppedState: Json = { ...current, active: false };
      this.gatewayPacketCaptureState.set(gatewayId, stoppedState);
      return { body: stoppedState };
    }
    return undefined;
  }

  private gatewayConfigShadow(
    method: string,
    path: string,
    body: Json | null,
    csrfToken: string | undefined,
    now: string,
  ): { status?: number; body: unknown } | undefined {
    const match = path.match(/^\/api\/v1\/gateways\/([^/]+)\/config-shadow$/);
    if (!match) return undefined;
    const gatewayId = decodeURIComponent(match[1]!);
    const refuse = (status: number, code: string, detail: string) => ({
      status,
      body: { type: 'about:blank', title: detail, status, detail, instance: path, code, traceId: '0'.repeat(32), retryable: false },
    });

    const gatewayDevice = (this.assetModel.devices as Json[]).find((d) => d.id === gatewayId && d.deviceType === 'GATEWAY');
    if (!gatewayDevice) return refuse(404, 'GATEWAY_NOT_FOUND', 'Gateway not found');

    const activePrincipal = this.principalOverride ?? principal;
    const caps = new Set<string>(activePrincipal.authorization?.capabilities ?? []);
    if (method === 'GET') {
      if (!caps.has('device.read')) return refuse(403, 'CREDENTIAL_FORBIDDEN', 'device.read required');
      return { body: this.getOrInitConfigShadow(gatewayId) };
    }
    if (method === 'PUT') {
      if (!caps.has('device.write')) return refuse(403, 'CREDENTIAL_FORBIDDEN', 'device.write required');
      if (!csrfToken) return refuse(403, 'CSRF_INVALID', 'CSRF token required');
      const credState = this.gatewayCredentialsState.get(gatewayId);
      if (credState?.revoked) return refuse(409, 'GATEWAY_REVOKED', 'Gateway identity has been revoked.');

      const reason = String(body?.reason ?? '').trim();
      if (!reason) return refuse(400, 'ENROLLMENT_REJECTED', 'reason is required');

      const shadow = this.getOrInitConfigShadow(gatewayId);
      const currentRev = Number(shadow.desired?.desiredRevision ?? 0);
      if (body?.expectedDesiredRevision !== undefined && Number(body.expectedDesiredRevision) !== currentRev) {
        return refuse(409, 'CONFIG_SHADOW_REVISION_CONFLICT', 'Config shadow desiredRevision conflict.');
      }

      const nextRev = currentRev + 1;
      const desired: Json = {
        desiredRevision: nextRev,
        pointMappings: body?.pointMappings ?? shadow.desired?.pointMappings ?? [],
        samplingPolicies: body?.samplingPolicies ?? shadow.desired?.samplingPolicies ?? [],
        concentratorConfigs: body?.concentratorConfigs ?? shadow.desired?.concentratorConfigs ?? {},
        updatedBy: activePrincipal.principalId,
        reason,
        createdAt: shadow.desired?.createdAt ?? now,
        updatedAt: now,
      };
      const updatedShadow: Json = {
        ...shadow,
        desired,
        syncStatus: 'PENDING_SYNC',
        drift: [],
      };
      this.configShadows.set(gatewayId, updatedShadow);
      return { body: updatedShadow };
    }
    return undefined;
  }

  private filterRegistryItems(
    items: Json[],
    query: URLSearchParams,
    kind: 'ASSET' | 'DEVICE',
  ): Json[] {
    const status = query.get('status');
    const assetType = query.get('assetType');
    const deviceType = query.get('deviceType');
    const spaceId = query.get('spaceId');
    const templateKey = query.get('templateKey');
    const templateRevisionId = query.get('templateRevisionId');
    const rawTags = query.getAll('tag');

    let spaceScope: Set<string> | null = null;
    if (spaceId) {
      spaceScope = new Set<string>([spaceId]);
      let expanded = true;
      while (expanded) {
        expanded = false;
        for (const sp of this.assetModel.spaces as Json[]) {
          if (sp.parentSpaceId && spaceScope.has(sp.parentSpaceId) && !spaceScope.has(sp.id)) {
            spaceScope.add(sp.id);
            expanded = true;
          }
        }
      }
    }

    return items.filter((item) => {
      if (status && item.status !== status) return false;
      if (kind === 'ASSET' && assetType && item.assetType !== assetType) return false;
      if (kind === 'DEVICE' && deviceType && item.deviceType !== deviceType) return false;
      if (spaceScope) {
        const rels = this.assetModel.relationships as Json[];
        const directSpace = rels.some(
          (r) => !r.validTo && r.fromId === item.id && (r.role === 'INSTALLED_IN' || r.role === 'SERVES') && spaceScope!.has(r.toId),
        );
        const viaAsset = kind === 'DEVICE' && rels.some(
          (devAsset) =>
            !devAsset.validTo &&
            devAsset.fromId === item.id &&
            devAsset.toType === 'ASSET' &&
            rels.some(
              (assetSpace) =>
                !assetSpace.validTo &&
                assetSpace.fromId === devAsset.toId &&
                (assetSpace.role === 'INSTALLED_IN' || assetSpace.role === 'SERVES') &&
                spaceScope!.has(assetSpace.toId),
            ),
        );
        if (!directSpace && !viaAsset) return false;
      }
      if (templateKey) {
        const assigned = this.assignedTemplateKeyByTargetId.get(item.id);
        if (!assigned || assigned.templateKey !== templateKey) return false;
      }
      if (templateRevisionId) {
        const assigned = this.assignedTemplateKeyByTargetId.get(item.id);
        if (!assigned || assigned.templateRevisionId !== templateRevisionId) return false;
      }
      for (const rawTag of rawTags) {
        const itemTags: Record<string, string> = item.tags ?? {};
        const sepIdx = rawTag.indexOf(':');
        if (sepIdx >= 0) {
          const key = rawTag.slice(0, sepIdx);
          const val = rawTag.slice(sepIdx + 1);
          if (itemTags[key] !== val) return false;
        } else if (!Object.prototype.hasOwnProperty.call(itemTags, rawTag)) {
          return false;
        }
      }
      return true;
    });
  }

  private registryP1(
    method: string,
    path: string,
    query: URLSearchParams,
    body: Json | null,
    now: string,
  ): { status?: number; body: unknown } | undefined {
    const refuse = (status: number, code: string, detail: string) => ({
      status,
      body: { type: 'about:blank', title: detail, status, detail, instance: path, code, traceId: '0'.repeat(32), retryable: false },
    });

    if (method === 'GET' && path === '/api/v1/templates') {
      const kind = query.get('templateKind');
      const byTemplateId = new Map<string, Json>();
      for (const rev of this.templateRevisions) {
        if (kind && rev.templateKind !== kind) continue;
        const prev = byTemplateId.get(rev.templateId);
        if (!prev || rev.revisionNumber > prev.revisionNumber) {
          byTemplateId.set(rev.templateId, rev);
        }
      }
      const items = Array.from(byTemplateId.values()).map((rev) => ({
        id: rev.templateId,
        tenantId: rev.tenantId,
        templateKey: rev.templateKey,
        templateKind: rev.templateKind,
        latestRevisionId: rev.id,
        latestRevisionNumber: rev.revisionNumber,
        releasedAt: rev.releasedAt,
        createdAt: rev.releasedAt,
      }));
      return { body: { items, hasMore: false, nextCursor: null } };
    }

    const revMatch = path.match(/^\/api\/v1\/templates\/revisions\/([^/]+)$/);
    if (method === 'GET' && revMatch) {
      const rev = this.templateRevisions.find((item) => item.id === decodeURIComponent(revMatch[1]!));
      return rev ? { body: rev } : refuse(404, 'RESOURCE_NOT_FOUND', 'Template revision not found');
    }

    if (method === 'POST' && path === '/api/v1/templates/revisions') {
      const existing = this.templateRevisions.filter((item) => item.templateKey === body?.templateKey);
      const templateId = existing[0]?.templateId ?? this.nextUuidV7('7000');
      const revisionNumber = existing.reduce((max, item) => Math.max(max, item.revisionNumber), 0) + 1;
      const rev: Json = {
        id: this.nextUuidV7('7100'),
        tenantId: this.assetModel.tenantId,
        templateId,
        templateKey: body!.templateKey,
        templateKind: body!.templateKind,
        revisionNumber,
        status: 'RELEASED',
        payload: body!.payload ?? {},
        releaseReferences: body!.releaseReferences ?? {},
        releasedAt: now,
      };
      this.templateRevisions.unshift(rev);
      return { status: 201, body: rev };
    }

    const assignMatch = path.match(/^\/api\/v1\/sites\/([^/]+)\/template-assignments$/);
    if (method === 'GET' && assignMatch) {
      const targetSiteId = decodeURIComponent(assignMatch[1]!);
      const targetType = query.get('targetType');
      const targetId = query.get('targetId');
      const items = this.templateAssignments.filter(
        (item) =>
          item.siteId === targetSiteId &&
          (!targetType || item.targetType === targetType) &&
          (!targetId || item.targetId === targetId),
      );
      return { body: { items, hasMore: false, nextCursor: null } };
    }

    if (method === 'POST' && path === '/api/v1/templates/assignments') {
      const rev = this.templateRevisions.find((item) => item.id === body?.templateRevisionId);
      if (!rev) return refuse(404, 'RESOURCE_NOT_FOUND', 'Template revision not found');
      for (const prev of this.templateAssignments) {
        if (!prev.validTo && prev.targetType === body?.targetType && prev.targetId === body?.targetId) {
          prev.validTo = now;
        }
      }
      const created: Json = {
        id: this.nextUuidV7('7200'),
        tenantId: this.assetModel.tenantId,
        siteId: body!.siteId,
        targetType: body!.targetType,
        targetId: body!.targetId,
        templateRevisionId: body!.templateRevisionId,
        validFrom: body!.effectiveAt ?? now,
        revision: 1,
      };
      this.templateAssignments.unshift(created);
      this.assignedTemplateKeyByTargetId.set(body!.targetId, {
        templateKey: rev.templateKey,
        templateRevisionId: rev.id,
      });
      if (body!.targetType === 'DEVICE' && Array.isArray(rev.payload?.standardPoints)) {
        for (const pt of rev.payload.standardPoints as Json[]) {
          const exists = (this.assetModel.telemetryPoints as Json[]).some(
            (existing) => existing.reportingDeviceId === body!.targetId && existing.pointCode === pt.pointCode,
          );
          if (!exists) {
            (this.assetModel.telemetryPoints as Json[]).push({
              id: this.nextUuidV7('7300'),
              tenantId: this.assetModel.tenantId,
              siteId: body!.siteId,
              reportingDeviceId: body!.targetId,
              sensorId: null,
              pointCode: pt.pointCode,
              sourceKey: pt.sourceKey || pt.pointCode,
              displayName: pt.displayName,
              pointType: pt.pointType,
              valueType: pt.valueType,
              unit: pt.unit ?? null,
              writable: Boolean(pt.writable),
              sampleIntervalMs: pt.sampleIntervalMs ?? 5000,
              publishIntervalMs: pt.publishIntervalMs ?? 5000,
              staleAfterMs: pt.staleAfterMs ?? 15000,
              counterDecreaseMode: pt.counterDecreaseMode ?? null,
              counterRolloverModulus: pt.counterRolloverModulus ?? null,
              sourceMetadata: { materializedByTemplateKey: rev.templateKey },
              status: 'ACTIVE',
              revision: 1,
              createdAt: now,
              updatedAt: now,
            });
          }
        }
        this.assetModel.counts = {
          ...this.assetModel.counts,
          points: (this.assetModel.telemetryPoints as Json[]).length,
        };
      }
      return { status: 201, body: created };
    }

    if (method === 'GET' && path === '/api/v1/assets') {
      const combined = [...(this.assetModel.assets as Json[]), ...this.extraCrossSiteAssets];
      return { body: { items: this.filterRegistryItems(combined, query, 'ASSET'), hasMore: false, nextCursor: null } };
    }
    if (method === 'GET' && path === '/api/v1/devices') {
      const combined = [...(this.assetModel.devices as Json[]), ...this.extraCrossSiteDevices];
      return { body: { items: this.filterRegistryItems(combined, query, 'DEVICE'), hasMore: false, nextCursor: null } };
    }

    const siteAssetsMatch = path.match(/^\/api\/v1\/sites\/([^/]+)\/assets(?:\/([^/]+))?$/);
    if (siteAssetsMatch) {
      const siteId = decodeURIComponent(siteAssetsMatch[1]!);
      const assetId = siteAssetsMatch[2] ? decodeURIComponent(siteAssetsMatch[2]) : null;
      if (method === 'GET' && !assetId) {
        const list = siteId === SITE_ID
          ? (this.assetModel.assets as Json[])
          : this.extraCrossSiteAssets.filter((item) => item.siteId === siteId);
        return { body: { items: this.filterRegistryItems(list, query, 'ASSET'), hasMore: false, nextCursor: null } };
      }
      if (method === 'POST' && !assetId) {
        const created: Json = {
          id: this.nextUuidV7('7400'),
          tenantId: this.assetModel.tenantId,
          siteId,
          code: body!.code,
          displayName: body!.displayName,
          assetType: body!.assetType,
          status: body!.status ?? 'ACTIVE',
          revision: 1,
          createdAt: now,
          updatedAt: now,
          ...(body!.tags ? { tags: body!.tags } : {}),
        };
        if (siteId === SITE_ID) {
          this.assetModel.assets = [...(this.assetModel.assets as Json[]), created];
          this.assetModel.counts = { ...this.assetModel.counts, assets: this.assetModel.assets.length };
        } else {
          this.extraCrossSiteAssets.push(created);
        }
        return { status: 201, body: created };
      }
      if (method === 'PATCH' && assetId) {
        const updateTarget = (list: Json[]) => {
          const idx = list.findIndex((item) => item.id === assetId);
          if (idx < 0) return null;
          const prev = list[idx]!;
          if (body?.meta?.expectedRevision !== undefined && body.meta.expectedRevision !== prev.revision) {
            return 'CONFLICT' as const;
          }
          const next = {
            ...prev,
            code: body!.code ?? prev.code,
            displayName: body!.displayName ?? prev.displayName,
            assetType: body!.assetType ?? prev.assetType,
            status: body!.status ?? prev.status,
            tags: body!.tags ?? prev.tags ?? {},
            revision: prev.revision + 1,
            updatedAt: now,
          };
          list[idx] = next;
          return next;
        };
        const updated =
          updateTarget(this.assetModel.assets as Json[]) ??
          updateTarget(this.extraCrossSiteAssets);
        if (updated === 'CONFLICT') return refuse(409, 'REGISTRY_REVISION_CONFLICT', 'Asset revision conflict');
        if (!updated) return refuse(404, 'RESOURCE_NOT_FOUND', 'Asset not found');
        return { body: updated };
      }
    }

    const siteDevicesMatch = path.match(/^\/api\/v1\/sites\/([^/]+)\/devices(?:\/([^/]+))?$/);
    if (siteDevicesMatch) {
      const siteId = decodeURIComponent(siteDevicesMatch[1]!);
      const deviceId = siteDevicesMatch[2] ? decodeURIComponent(siteDevicesMatch[2]) : null;
      if (method === 'GET' && !deviceId) {
        const list = siteId === SITE_ID
          ? (this.assetModel.devices as Json[])
          : this.extraCrossSiteDevices.filter((item) => item.siteId === siteId);
        return { body: { items: this.filterRegistryItems(list, query, 'DEVICE'), hasMore: false, nextCursor: null } };
      }
      if (method === 'POST' && !deviceId) {
        const created: Json = {
          id: this.nextUuidV7('7500'),
          tenantId: this.assetModel.tenantId,
          siteId,
          code: body!.code,
          displayName: body!.displayName,
          deviceType: body!.deviceType,
          status: body!.status ?? 'ACTIVE',
          revision: 1,
          createdAt: now,
          updatedAt: now,
          ...(body!.tags ? { tags: body!.tags } : {}),
        };
        if (siteId === SITE_ID) {
          this.assetModel.devices = [...(this.assetModel.devices as Json[]), created];
          this.assetModel.counts = { ...this.assetModel.counts, deviceEndpoints: this.assetModel.devices.length };
        } else {
          this.extraCrossSiteDevices.push(created);
        }
        return { status: 201, body: created };
      }
      if (method === 'PATCH' && deviceId) {
        const updateTarget = (list: Json[]) => {
          const idx = list.findIndex((item) => item.id === deviceId);
          if (idx < 0) return null;
          const prev = list[idx]!;
          if (body?.meta?.expectedRevision !== undefined && body.meta.expectedRevision !== prev.revision) {
            return 'CONFLICT' as const;
          }
          const next = {
            ...prev,
            code: body!.code ?? prev.code,
            displayName: body!.displayName ?? prev.displayName,
            deviceType: body!.deviceType ?? prev.deviceType,
            status: body!.status ?? prev.status,
            tags: body!.tags ?? prev.tags ?? {},
            revision: prev.revision + 1,
            updatedAt: now,
          };
          list[idx] = next;
          return next;
        };
        const updated =
          updateTarget(this.assetModel.devices as Json[]) ??
          updateTarget(this.extraCrossSiteDevices);
        if (updated === 'CONFLICT') return refuse(409, 'REGISTRY_REVISION_CONFLICT', 'Device revision conflict');
        if (!updated) return refuse(404, 'RESOURCE_NOT_FOUND', 'Device not found');
        return { body: updated };
      }
    }

    const devicePointsMatch = path.match(/^\/api\/v1\/devices\/([^/]+)\/points$/);
    if (method === 'GET' && devicePointsMatch) {
      const targetDeviceId = decodeURIComponent(devicePointsMatch[1]!);
      const items = (this.assetModel.telemetryPoints as Json[]).filter(
        (pt) => pt.reportingDeviceId === targetDeviceId,
      );
      return { body: { items, hasMore: false, nextCursor: null } };
    }

    return undefined;
  }

  private getOrInitConcentratorState(gatewayId: string, unitId: number, now: string): Json {
    const key = `${gatewayId}:${unitId}`;
    const existing = this.concentratorStates.get(key);
    if (existing) return existing;

    const slots: Json[] = Array.from({ length: 200 }, (_, index) => {
      const slot = index + 1;
      const presetEids = [50, 51, 55, 13, 14, 53, 54];
      const eid = slot <= presetEids.length ? presetEids[slot - 1]! : slot <= 23 ? 100 + slot : 0;
      return { slot, eid };
    });

    const channels: Json[] = Array.from({ length: 23 }, (_, index) => {
      const channel = index + 1;
      const eid = slots[index]!.eid;
      if (channel === 1) return { channel, eid, online: true, sensorType: 1, sensorModel: 'WHT20-N', temperatureC: 24.6, voltageMv: 2980, rssiDbm: -68 };
      if (channel === 2) return { channel, eid, online: true, sensorType: 1, sensorModel: 'WHT20-N', temperatureC: 11.8, voltageMv: 2540, rssiDbm: -74 };
      if (channel === 3) return { channel, eid, online: true, sensorType: 1, sensorModel: 'WHT20-N', temperatureC: 12.1, voltageMv: 2410, rssiDbm: -79 };
      if (channel === 4) return { channel, eid, online: true, sensorType: 1, sensorModel: 'WHT20-N', temperatureC: 29.4, voltageMv: 3010, rssiDbm: -65 };
      if (channel === 5) return { channel, eid, online: true, sensorType: 1, sensorModel: 'WHT20-N', temperatureC: 30.1, voltageMv: 2990, rssiDbm: -66 };
      if (channel === 6) return { channel, eid, online: true, sensorType: 1, sensorModel: 'WHT20-N', temperatureC: 23.2, voltageMv: 2890, rssiDbm: -71 };
      if (channel === 7) return { channel, eid, online: true, sensorType: 1, sensorModel: 'WHT20-N', temperatureC: 22.7, voltageMv: 2910, rssiDbm: -70 };
      if (channel === 8) return { channel, eid, online: false, sensorType: 1, sensorModel: 'WHT20-N', temperatureC: null, voltageMv: null, rssiDbm: null };
      return {
        channel,
        eid,
        online: true,
        sensorType: 1,
        sensorModel: 'WHT20-N',
        temperatureC: Number((18.5 + (channel % 7) * 1.1).toFixed(1)),
        voltageMv: 2920 + (channel % 5) * 35,
        rssiDbm: -64 - (channel % 8) * 2,
      };
    });

    const created: Json = {
      params: {
        modbusId: unitId,
        baudRateCode: 3,
        offLineTime: 10,
        dataUplinkCycle: 10,
        localNet: 1,
        timeMark: 1,
        rfTxPower: 20,
        rfFrequency: 433,
        rfBandWidth: 0,
        rfSf: 9,
        rfLeadCode: 8,
        txTimeout: 5,
        rxTimeout: 5,
        rssiOut: 1,
      },
      slots,
      channels,
      recentAudits: [
        {
          id: '01910000-0000-7000-8000-000000000801',
          unitId,
          actorId: MY_ID,
          operation: 'WRITE_PARAMS',
          requestId: '01910000-0000-7000-8000-000000000802',
          outcome: 'APPLIED',
          reason: '松雅轩 1 号无线集中器初始化巡检参数',
          diff: { dataUplinkCycle: { before: 15, after: 10 } },
          occurredAt: new Date(Date.parse(now) - 3_600_000).toISOString(),
        },
      ],
      seq: 1,
    };
    this.concentratorStates.set(key, created);
    return created;
  }

  private concentrators(
    method: string,
    path: string,
    body: Json | null,
    csrfToken: string | undefined,
    now: string,
  ): { status?: number; body: unknown } | undefined {
    const match = path.match(/^\/api\/v1\/gateways\/([^/]+)\/concentrators\/([^/]+)\/(status|params|eids)$/);
    if (!match) return undefined;

    const gatewayId = decodeURIComponent(match[1]!);
    const unitId = Number(decodeURIComponent(match[2]!));
    const action = match[3]!;
    const refuse = (status: number, code: string, detail: string) => ({
      status,
      body: { type: 'about:blank', title: detail, status, detail, instance: path, code, traceId: '0'.repeat(32), retryable: false },
    });

    const gatewayDevice = (this.assetModel.devices as Json[]).find((d) => d.id === gatewayId && d.deviceType === 'GATEWAY');
    if (!gatewayDevice) return refuse(404, 'RESOURCE_NOT_FOUND', 'Gateway not found');
    if (!Number.isInteger(unitId) || unitId < 1 || unitId > 247) {
      return refuse(400, 'CONCENTRATOR_REQUEST_INVALID', 'Invalid concentrator unit ID');
    }
    if (new Set([2, 3, 4, 5, 10, 31]).has(unitId)) {
      return refuse(409, 'CONCENTRATOR_UID_PROTECTED', 'Protected RS485 unit ID');
    }

    const activePrincipal = this.principalOverride ?? principal;
    const caps = new Set<string>(activePrincipal.authorization?.capabilities ?? []);
    if (method === 'GET' && !caps.has('device.read')) {
      return refuse(403, 'FORBIDDEN', 'device.read required');
    }
    if (method === 'PUT' && (!caps.has('device.write') || !csrfToken)) {
      return refuse(403, 'FORBIDDEN', 'device.write and CSRF token required');
    }

    const state = this.getOrInitConcentratorState(gatewayId, unitId, now);
    state.seq += 1;
    const reqSuffix = String(state.seq).padStart(12, '0');
    const requestId = `01910000-0000-7000-8000-${reqSuffix}`;

    if (method === 'GET' && action === 'status') {
      return {
        body: {
          gatewayId,
          unitId,
          respondedAt: now,
          params: state.params,
          channels: state.channels,
          recentAudits: state.recentAudits,
        },
      };
    }

    if (method === 'GET' && action === 'eids') {
      return {
        body: {
          gatewayId,
          unitId,
          respondedAt: now,
          slots: state.slots,
          recentAudits: state.recentAudits,
        },
      };
    }

    if (method === 'PUT' && action === 'params') {
      const patch = body?.params ?? {};
      const reason = String(body?.reason ?? '').trim();
      if (!reason || Object.keys(patch).length === 0) {
        return refuse(400, 'CONCENTRATOR_REQUEST_INVALID', 'Invalid params patch or empty reason');
      }
      if (patch.modbusId !== undefined && new Set([2, 3, 4, 5, 10, 31]).has(Number(patch.modbusId))) {
        return refuse(409, 'CONCENTRATOR_UID_PROTECTED', 'Protected RS485 modbusId');
      }
      if (this.offlineGateways.has(gatewayId)) {
        if (body?.stageIfOffline !== true) {
          return refuse(503, 'CONCENTRATOR_TRANSPORT_UNAVAILABLE', 'Gateway remote transport is offline');
        }
        const shadow = this.getOrInitConfigShadow(gatewayId);
        const currentDesired = shadow.desired ?? {
          desiredRevision: 0,
          pointMappings: [],
          samplingPolicies: [],
          concentratorConfigs: {},
          updatedBy: MY_ID,
          reason: 'initial stage',
          createdAt: now,
          updatedAt: now,
        };
        const unitKey = String(unitId);
        const existingUnit = currentDesired.concentratorConfigs?.[unitKey] ?? {};
        const stagedParams = { ...state.params, ...patch };
        const nextDesiredRev = Number(currentDesired.desiredRevision) + 1;
        this.configShadows.set(gatewayId, {
          ...shadow,
          syncStatus: 'PENDING_SYNC',
          desired: {
            ...currentDesired,
            desiredRevision: nextDesiredRev,
            concentratorConfigs: {
              ...currentDesired.concentratorConfigs,
              [unitKey]: { ...existingUnit, params: stagedParams },
            },
            updatedBy: MY_ID,
            reason,
            updatedAt: now,
          },
        });
        const audit = {
          id: `01910000-1000-7000-8000-${reqSuffix}`,
          unitId,
          actorId: MY_ID,
          operation: 'WRITE_PARAMS',
          requestId,
          outcome: 'STAGED_SHADOW',
          reason,
          diff: patch,
          occurredAt: now,
        };
        state.recentAudits = [audit, ...state.recentAudits].slice(0, 20);
        return {
          body: {
            gatewayId,
            unitId,
            requestId,
            writtenCount: Object.keys(patch).length,
            params: state.params,
            audit,
            staged: true,
            desiredRevision: nextDesiredRev,
          },
        };
      }
      const writtenCount = Object.keys(patch).length;
      state.params = { ...state.params, ...patch };
      const nextUnitId = Number(state.params.modbusId ?? unitId);
      const audit = {
        id: `01910000-1000-7000-8000-${reqSuffix}`,
        unitId: nextUnitId,
        actorId: MY_ID,
        operation: 'WRITE_PARAMS',
        requestId,
        outcome: 'APPLIED',
        reason,
        diff: patch,
        occurredAt: now,
      };
      state.recentAudits = [audit, ...state.recentAudits].slice(0, 20);
      if (nextUnitId !== unitId) {
        this.concentratorStates.delete(`${gatewayId}:${unitId}`);
        this.concentratorStates.set(`${gatewayId}:${nextUnitId}`, state);
      }
      return {
        body: {
          gatewayId,
          unitId: nextUnitId,
          requestId,
          writtenCount,
          params: state.params,
          audit,
        },
      };
    }

    if (method === 'PUT' && action === 'eids') {
      const nextSlots = body?.slots;
      const reason = String(body?.reason ?? '').trim();
      if (!Array.isArray(nextSlots) || nextSlots.length !== 200 || !reason) {
        return refuse(400, 'CONCENTRATOR_REQUEST_INVALID', 'Invalid slots or empty reason');
      }
      const seen = new Set<number>();
      for (const entry of nextSlots) {
        const eid = Number(entry.eid);
        if (eid > 0) {
          if (seen.has(eid)) {
            return refuse(422, 'CONCENTRATOR_EID_DUPLICATE', `Duplicate non-zero EID ${eid}`);
          }
          seen.add(eid);
        }
      }
      if (this.offlineGateways.has(gatewayId)) {
        if (body?.stageIfOffline !== true) {
          return refuse(503, 'CONCENTRATOR_TRANSPORT_UNAVAILABLE', 'Gateway remote transport is offline');
        }
        const shadow = this.getOrInitConfigShadow(gatewayId);
        const currentDesired = shadow.desired ?? {
          desiredRevision: 0,
          pointMappings: [],
          samplingPolicies: [],
          concentratorConfigs: {},
          updatedBy: MY_ID,
          reason: 'initial stage',
          createdAt: now,
          updatedAt: now,
        };
        const unitKey = String(unitId);
        const existingUnit = currentDesired.concentratorConfigs?.[unitKey] ?? {};
        const stagedSlots = nextSlots.map((entry: Json) => ({ slot: Number(entry.slot), eid: Number(entry.eid) }));
        const nextDesiredRev = Number(currentDesired.desiredRevision) + 1;
        this.configShadows.set(gatewayId, {
          ...shadow,
          syncStatus: 'PENDING_SYNC',
          desired: {
            ...currentDesired,
            desiredRevision: nextDesiredRev,
            concentratorConfigs: {
              ...currentDesired.concentratorConfigs,
              [unitKey]: { ...existingUnit, eids: stagedSlots },
            },
            updatedBy: MY_ID,
            reason,
            updatedAt: now,
          },
        });
        const audit = {
          id: `01910000-2000-7000-8000-${reqSuffix}`,
          unitId,
          actorId: MY_ID,
          operation: 'WRITE_EIDS',
          requestId,
          outcome: 'STAGED_SHADOW',
          reason,
          diff: { staged: true },
          occurredAt: now,
        };
        state.recentAudits = [audit, ...state.recentAudits].slice(0, 20);
        return {
          body: {
            gatewayId,
            unitId,
            requestId,
            updatedSlots: 0,
            chunkCount: 0,
            slots: state.slots,
            audit,
            staged: true,
            desiredRevision: nextDesiredRev,
          },
        };
      }
      let updatedSlots = 0;
      let chunkCount = 0;
      let inChunk = false;
      let runLen = 0;
      for (let i = 0; i < 200; i += 1) {
        const prev = state.slots[i]!.eid;
        const next = Number(nextSlots[i]!.eid);
        if (prev !== next) {
          updatedSlots += 1;
          if (!inChunk || runLen >= 20) {
            chunkCount += 1;
            inChunk = true;
            runLen = 1;
          } else {
            runLen += 1;
          }
        } else {
          inChunk = false;
          runLen = 0;
        }
      }
      state.slots = nextSlots.map((entry: Json) => ({ slot: Number(entry.slot), eid: Number(entry.eid) }));
      state.channels = state.channels.map((ch: Json) => {
        const updatedSlot = state.slots.find((s: Json) => s.slot === ch.channel);
        return updatedSlot ? { ...ch, eid: updatedSlot.eid } : ch;
      });
      const audit = {
        id: `01910000-2000-7000-8000-${reqSuffix}`,
        unitId,
        actorId: MY_ID,
        operation: 'WRITE_EIDS',
        requestId,
        outcome: 'APPLIED',
        reason,
        diff: { updatedSlots, chunkCount },
        occurredAt: now,
      };
      state.recentAudits = [audit, ...state.recentAudits].slice(0, 20);
      return {
        body: {
          gatewayId,
          unitId,
          requestId,
          updatedSlots,
          chunkCount,
          slots: state.slots,
          audit,
        },
      };
    }

    return undefined;
  }
}

// As the owner totals them: a day several measures verified counts once, with the plant's electricity once.
function totalsOf(days: Json[]): Json {
  const sum = (key: string) => days.reduce((total, day) => total + day[key], 0);
  const plant = new Map<string, number>();
  for (const day of days) plant.set(day.day, Math.max(plant.get(day.day) ?? 0, day.plantKWh));
  return {
    days: plant.size, unpricedHours: sum('unpricedHours'), adjustedBaselineKWh: sum('adjustedBaselineKWh'), actualKWh: sum('actualKWh'),
    avoidedKWh: sum('avoidedKWh'), avoidedCost: sum('avoidedCost'), plantKWh: Array.from(plant.values()).reduce((total, kwh) => total + kwh, 0),
  };
}

function bodyOf(request: Request): Json | null {
  const data = request.postData();
  return data ? JSON.parse(data) as Json : null;
}
