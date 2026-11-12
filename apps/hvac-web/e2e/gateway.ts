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
  // The Site's rules in effect, as the Alarm owner lists them; publishing the rules file changes them, not the page.
  readonly alarmRules = fixture<Json[]>('alarm-rules.json');
  readonly workOrders = fixture<Json[]>('work-orders.json');
  readonly snapshots = fixture<Json>('snapshots.json');
  readonly commands: Json[] = [];
  /** When the forecast is replayed as published; tests that pin the page clock set it to that instant. */
  forecastOrigin: string | null = null;
  /** Optional custom handler for `device-series:aggregate` so tests can simulate outages or specific series. */
  aggregateHandler: ((query: Json) => { status?: number; body: unknown } | undefined) | null = null;
  /** Optional custom handler for `analytics/energy-series` so tests can simulate fully priced daily series. */
  energySeriesHandler: ((query: Json) => { status?: number; body: unknown } | undefined) | null = null;
  readonly findings = replayFindings(fixture<Json>('fdd-findings.json').items, new Date().toISOString());
  /** The Site's latest published recommendation; null until a run publishes one. */
  optimization: Json | null = null;
  /** How the Site's Optimization schedule last fired; null while the Site has no schedule that fired. */
  optimizationPreparation: Json | null = null;
  /** The Chiller COP Model's latest read-only shadow evaluation; null when no SHADOW row exists. */
  optimizationShadow: Json | null = null;
  shadowReads = 0;
  /** IAM refuses Energy Analytics for the Site, as it does for a Site outside the principal's analytics grant. */
  analyticsDenied = false;
  /** IAM refuses Alarm listing for the Site. */
  alarmsDenied = false;
  /** IAM refuses Command listing for the Site. */
  commandsDenied = false;
  /** Daily energy series repeat the recorded day's total each day, so days line up with other daily series. */
  energyByDay = false;
  /** Telemetry fails the weather station's daily temperature. */
  outdoorFailing = false;
  /** Machines whose sub-meter the recorded device breakdown leaves out. */
  unmeteredDeviceIds: readonly string[] = [];
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
      enabled: true, locked: false, lastLoginAt: '2026-10-14T01:20:00Z', self: true,
      assignments: [{ bindingId: '018f3e00-2200-7000-8000-000000000001', roleKey: 'admin' }],
    },
    {
      principalId: '018f3e00-2000-7000-8000-000000000002', username: 'zhou.min', displayName: '周敏', email: 'zhou.min@example.invalid',
      enabled: true, locked: false, self: false,
      assignments: [{ bindingId: '018f3e00-2200-7000-8000-000000000002', roleKey: 'operator', siteId: SITE_ID }],
    },
  ];
  /** The Operations Agent's Agent Sessions for the Site. */
  readonly assistant = new AssistantOwner();
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
    if (method !== 'GET') this.writes.push({ method, path, body: bodyOf(request), csrfToken: request.headers()['x-csrf-token'] });
    const agent = this.assistant.respond(SITE_ID, method, path, bodyOf(request));
    if (agent?.stream !== undefined) return route.fulfill({ status: 200, contentType: 'text/event-stream; charset=utf-8', body: agent.stream });
    const reply = agent ?? this.respond(method, path, url.searchParams, bodyOf(request));
    if (!reply) {
      this.unexpected.push(`${method} ${path}`);
      return route.fulfill({ status: 404, contentType: 'application/problem+json', body: JSON.stringify({ status: 404, code: 'ROUTE_NOT_FOUND', title: 'Not found' }) });
    }
    if (reply.status === 204) return route.fulfill({ status: 204 });
    return route.fulfill({ status: reply.status ?? 200, contentType: 'application/json', body: JSON.stringify(reply.body) });
  }

  private submitCommand(input: Json, now: string): Json {
    const point = fixture<Json>('asset-model.json').telemetryPoints.find((candidate: Json) => candidate.id === input.commandPointId);
    const capability: string = point.sourceMetadata.capability;
    const risk = capability === 'START' || capability === 'STOP' ? 'MEDIUM' : 'LOW';
    const command: Json = {
      schemaVersion: 1, commandId: `ec61c2dc-2d2c-4933-8fb7-${String(this.commands.length + 1).padStart(12, '0')}`, siteId: SITE_ID, deviceId: point.reportingDeviceId, pointId: point.id,
      capability, status: 'SUBMITTED', risk, approvalPolicy: risk === 'LOW' ? 'NONE' : 'SINGLE_APPROVER',
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
        principalId, username: body!.username, displayName: body!.displayName, email: body!.email, enabled: true, locked: false, self: false,
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

  // The page names each request by its position in the batch, so recorded items answer by Device.
  private snapshotBatch(body: Json): Json {
    const items = body.requests.map((request: Json) => {
      const recorded = this.snapshots.items.find((item: Json) => item.deviceId === request.deviceId);
      if (!recorded) throw new Error(`no recorded Snapshot for Device ${request.deviceId}`);
      return { ...recorded, requestId: request.requestId };
    });
    return { ...this.snapshots, items };
  }

  private respond(method: string, path: string, query: URLSearchParams, body: Json | null): { status?: number; body: unknown } | undefined {
    const now = new Date().toISOString();
    if (method === 'GET' && path === '/api/v1/principal') return { body: this.principalOverride ?? principal };
    if (method === 'GET' && path === '/api/v1/platform/status') return { body: fixture('platform-status.json') };
    const userAdmin = this.userAdmin(method, path, body);
    if (userAdmin) return userAdmin;
    if (method === 'GET' && path === '/api/v1/sites') return { body: fixture('sites.json') };
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}`) return { body: fixture<Json>('sites.json').items[0] };
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/devices`) {
      return { body: { items: this.assetModel.devices, hasMore: false, nextCursor: null } };
    }
    const gatewayCredMatch = path.match(/^\/api\/v1\/gateways\/([^/]+)\/credential$/);
    if (method === 'GET' && gatewayCredMatch) {
      return { body: { status: 'ACTIVE', expiresAt: new Date(Date.now() + 90 * 86_400_000).toISOString() } };
    }
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/asset-model`) return { body: this.assetModel };
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
        return { body: synthesizeTrendAggregate(fixture<Json>('asset-model.json'), this.snapshots, body) };
      }
      if (body?.granularity === 'QUARTER_HOUR' && body.keys.length === 1 && body.keys[0] === 'active_power') {
        return { body: replayDemand(fixture<Json>('demand-quarter-hours.json'), body) };
      }
      if (body?.granularity === 'QUARTER_HOUR') {
        const demand = replayDemand(fixture<Json>('demand-quarter-hours.json'), body);
        return demand.buckets.length > 0
          ? { body: demand }
          : { body: synthesizeTrendAggregate(fixture<Json>('asset-model.json'), this.snapshots, body) };
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
      return { body: synthesizeTrendAggregate(fixture<Json>('asset-model.json'), this.snapshots, body!) };
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
      return this.optimizationPreparation ? { body: this.optimizationPreparation } : { status: 404, body: { status: 404, code: 'RESOURCE_NOT_FOUND', title: 'Resource not found' } };
    }
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/optimization/shadow-evaluations/latest`) {
      this.shadowReads += 1;
      return this.optimizationShadow ? { body: this.optimizationShadow } : { status: 404, body: { status: 404, code: 'RESOURCE_NOT_FOUND', title: 'Resource not found' } };
    }
    if (method === 'POST' && path === '/api/v1/optimization/runs') {
      if (!this.nextRecommendation) return { status: 502, body: fixture('optimization-run-declined.json') };
      this.optimization = this.nextRecommendation;
      return { status: 202, body: { optimizationRunId: this.optimization.runId, inputSnapshotId: this.optimization.recommendation.inputSnapshotId, status: 'READY' } };
    }
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/forecast/load`) return { body: replayForecast(fixture<Json>('forecast-load.json'), this.forecastOrigin ?? now) };
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
        && (!query.get('severity') || alarm.currentSeverity === query.get('severity'))
        && (!activeTo || Date.parse(alarm.firstOccurredAt) <= Date.parse(activeTo))
        && (!activeFrom || alarm.condition === 'ACTIVE' || !alarm.clearedAt || Date.parse(alarm.clearedAt) >= Date.parse(activeFrom)));
      return { body: { data: items, meta: { nextCursor: null, hasMore: false, limit: 100, requestId: 'e2e' } } };
    }
    if (method === 'GET' && path === '/api/v1/alarm-rules') {
      return { body: { data: query.get('siteId') === SITE_ID ? this.alarmRules : [], meta: { requestId: 'e2e' } } };
    }
    const alarmMatch = path.match(/^\/api\/v1\/alarms\/([^/]+)(?:\/(ack|assign))?$/);
    if (alarmMatch) {
      const alarm = this.alarms.find((candidate) => candidate.alarmId === alarmMatch[1]);
      if (!alarm) return { status: 404, body: { status: 404, code: 'RESOURCE_NOT_FOUND' } };
      if (method === 'POST' && alarmMatch[2] === 'ack') {
        alarm.acknowledgement = { acknowledgedAt: now, acknowledgedBy: MY_ID, ...(body?.comment ? { comment: body.comment } : {}) };
        this.appendAlarmEntry(alarm, 'ACKNOWLEDGE', body?.comment ?? '', now);
      } else if (method === 'POST' && alarmMatch[2] === 'assign') {
        alarm.assigneeId = body?.assigneeId;
        this.appendAlarmEntry(alarm, 'ASSIGN', body?.reason ?? '', now);
      } else if (method !== 'GET') {
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
    if (method === 'POST' && path === '/api/v1/commands') return { status: 202, body: this.submitCommand(body!, now) };
    const approval = path.match(/^\/api\/v1\/commands\/([^/]+)\/approve$/);
    if (method === 'POST' && approval) {
      const command = this.commands.find((candidate) => candidate.commandId === approval[1]);
      if (!command || command.status !== 'AWAITING_APPROVAL') return { status: 409, body: { status: 409, code: 'COMMAND_APPROVAL_INVALID', detail: '指令不在待审批状态' } };
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

  private appendAlarmEntry(alarm: Json, operation: string, reason: string, now: string) {
    alarm.version += 1;
    alarm.timeline.push({ operation, condition: alarm.condition, reason, actorType: 'PRINCIPAL', actorId: MY_ID, currentSeverity: alarm.currentSeverity, occurredAt: now, version: alarm.version });
  }

  private appendOrderEntry(order: Json, operation: string, toStatus: string, reason: string | undefined, now: string) {
    order.version += 1;
    order.updatedAt = now;
    order.timeline.push({ operation, toStatus, reason: reason ?? '', actorType: 'PRINCIPAL', actorId: MY_ID, occurredAt: now, version: order.version });
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
