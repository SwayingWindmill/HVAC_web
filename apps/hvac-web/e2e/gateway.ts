import { readFileSync } from 'node:fs';
import type { Page, Request, Route } from '@playwright/test';

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

// Recorded quarter-hours answer each window the page asks for; tests pin the page clock to the recording.
function replayDemand(recorded: Json, query: Json): Json {
  const { recordedAt: _recordedAt, ...response } = recorded;
  const from = Date.parse(query.from);
  const to = Date.parse(query.to);
  const buckets = recorded.buckets.filter((bucket: Json) => Date.parse(bucket.periodStart) >= from && Date.parse(bucket.periodStart) < to);
  return { ...response, buckets, metadata: { ...recorded.metadata, requestedFrom: query.from, requestedTo: query.to, returnedBuckets: buckets.length } };
}

// A machine's recorded hours of power, moved onto the requested day.
function replayMachineHours(recorded: Json, query: Json): Json {
  const response = recorded.byDevice[query.deviceId];
  const shift = Date.parse(query.from) - Date.parse(recorded.recordedFrom);
  const moved = (instant: string) => new Date(Date.parse(instant) + shift).toISOString();
  return { ...response, buckets: response.buckets.map((bucket: Json) => ({ ...bucket, periodStart: moved(bucket.periodStart), periodEnd: moved(bucket.periodEnd) })) };
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
  readonly alarms = fixture<Json[]>('alarms.json');
  readonly workOrders = fixture<Json[]>('work-orders.json');
  readonly snapshots = fixture<Json>('snapshots.json');
  readonly commands: Json[] = [];
  readonly findings = replayFindings(fixture<Json>('fdd-findings.json').items, new Date().toISOString());
  /** The Site's latest published recommendation; null until a run publishes one. */
  optimization: Json | null = null;
  /** IAM refuses Energy Analytics for the Site, as it does for a Site outside the principal's analytics grant. */
  analyticsDenied = false;
  /** Machines whose sub-meter the recorded device breakdown leaves out. */
  unmeteredDeviceIds: readonly string[] = [];
  /** report-service's kept snapshots of closed months; a month without one answers 404. */
  reportSnapshots: Json[] = [];
  /** The Site's import Tariff in effect; null when none is assigned. */
  tariff: Json | null = fixture<Json>('tariff.json');
  /** What the next requested run publishes; null while the owner declines it for missing inputs. */
  nextRecommendation: Json | null = null;
  /** The Site's energy conservation measures as the M&V owner keeps them, newest first. */
  readonly measures: Json[] = [];
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
    const reply = this.respond(method, path, url.searchParams, bodyOf(request));
    if (!reply) {
      this.unexpected.push(`${method} ${path}`);
      return route.fulfill({ status: 404, contentType: 'application/problem+json', body: JSON.stringify({ status: 404, code: 'ROUTE_NOT_FOUND', title: 'Not found' }) });
    }
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

  // The recorded loop still runs below the rule's threshold; as the FDD owner does, a finding for the
  // same asset and rule from the last 24 hours makes the condition ongoing rather than new.
  private evaluateLowDeltaT(input: Json, now: string): Json {
    const recorded = fixture<Json>('fdd-findings.json').items[0];
    const finding = {
      ...recorded, id: `finding-${this.findings.length + 1}`, assetId: input.assetId, evaluationFrom: input.evaluationFrom, evaluationTo: input.evaluationTo,
      ruleRevisionId: input.ruleRevisionId, thresholdValue: input.minimumDeltaTC, createdAt: now,
    };
    if (finding.observedValue >= input.minimumDeltaTC) return { status: 'CLEAR', deltaTC: finding.observedValue };
    const open = this.findings.find((candidate) => candidate.assetId === input.assetId && candidate.ruleRevisionId === input.ruleRevisionId
      && Date.parse(candidate.evaluationTo) >= Date.parse(input.evaluationTo) - 86_400_000);
    if (open) return { status: 'ONGOING', deltaTC: finding.observedValue, finding: open };
    this.findings.unshift(finding);
    return { status: 'FINDING', deltaTC: finding.observedValue, finding };
  }

  // The M&V owner's ledger: implementing fits the recorded baseline over the fourteen days before the
  // implementation time, and only one measure on the Site reports at a time.
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
        description: body?.description ?? '', evidence: body?.evidence ?? '', evidenceCommandId: body?.evidenceCommandId ?? null, status: 'PLANNED', plannedAt: body?.plannedAt, implementedAt: null, endedAt: null,
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
      if (this.measures.some((other) => other.status === 'IMPLEMENTED')) return refuse(409, 'SAVINGS_REPORTING_OVERLAP');
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
    if (method === 'GET' && path === '/api/v1/principal') return { body: principal };
    if (method === 'GET' && path === '/api/v1/platform/status') return { body: fixture('platform-status.json') };
    if (method === 'GET' && path === '/api/v1/sites') return { body: fixture('sites.json') };
    if (method === 'GET' && path === '/api/v1/notifications/inbox') return { body: { data: [], meta: { requestId: 'e2e', count: 0 } } };
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/asset-model`) return { body: fixture('asset-model.json') };
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/tariff`) {
      return this.tariff ? { body: this.tariff } : {
        status: 404,
        body: { type: 'about:blank', title: 'Resource not found', status: 404, detail: 'The requested Registry resource was not found.', instance: path, code: 'RESOURCE_NOT_FOUND', traceId: '0'.repeat(32), retryable: false },
      };
    }
    if (method === 'POST' && path === '/api/v1/telemetry/observation-snapshots:batchGet') return { body: this.snapshotBatch(body!) };
    if (method === 'POST' && path === '/api/v1/telemetry/device-series:aggregate') {
      if (body?.granularity === 'QUARTER_HOUR') return { body: replayDemand(fixture<Json>('demand-quarter-hours.json'), body) };
      if (body?.granularity === 'HOUR' && body.keys.length === 1 && body.keys[0] === 'power') return { body: replayMachineHours(fixture<Json>('device-hours.json'), body) };
      return { body: fixture('history.json') };
    }
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/fdd/findings`) return { body: { items: this.findings } };
    if (method === 'POST' && path === `/api/v1/sites/${SITE_ID}/fdd/evaluate/low-delta-t`) return { status: 201, body: this.evaluateLowDeltaT(body!, now) };
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/optimization/recommendations/latest`) {
      return this.optimization ? { body: this.optimization } : { status: 404, body: { status: 404, code: 'RESOURCE_NOT_FOUND', title: 'Resource not found' } };
    }
    if (method === 'POST' && path === '/api/v1/optimization/runs') {
      if (!this.nextRecommendation) return { status: 502, body: fixture('optimization-run-declined.json') };
      this.optimization = this.nextRecommendation;
      return { status: 202, body: { optimizationRunId: this.optimization.runId, inputSnapshotId: this.optimization.recommendation.inputSnapshotId, status: 'READY' } };
    }
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/forecast/load`) return { body: replayForecast(fixture<Json>('forecast-load.json'), now) };
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
    if (method === 'POST' && path === '/api/v1/analytics/energy-series' && body?.breakdown === 'device') {
      const recorded = fixture<Json>('energy-breakdown.json');
      const devices = recorded.response.devices.filter((device: Json) => !this.unmeteredDeviceIds.includes(device.deviceId));
      return { body: replayEnergy({ ...recorded.response, devices }, body, Date.parse(recorded.recordedDay)) };
    }
    if (method === 'POST' && path === '/api/v1/analytics/energy-series') return { body: replayEnergy(fixture<Json>('energy-series.json')[body?.energyType], body!) };

    if (method === 'GET' && path === '/api/v1/alarms') {
      const items = this.alarms.filter((alarm) =>
        (!query.get('condition') || alarm.condition === query.get('condition'))
        && (!query.has('acknowledged') || Boolean(alarm.acknowledgement) === (query.get('acknowledged') === 'true'))
        && (!query.get('severity') || alarm.currentSeverity === query.get('severity')));
      return { body: { data: items, meta: { nextCursor: null, hasMore: false, limit: 100, requestId: 'e2e' } } };
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

    if (method === 'GET' && path === '/api/v1/commands') return { body: { data: this.commands } };
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

function totalsOf(days: Json[]): Json {
  const sum = (key: string) => days.reduce((total, day) => total + day[key], 0);
  return { days: days.length, unpricedHours: sum('unpricedHours'), adjustedBaselineKWh: sum('adjustedBaselineKWh'), actualKWh: sum('actualKWh'), avoidedKWh: sum('avoidedKWh'), avoidedCost: sum('avoidedCost') };
}

function bodyOf(request: Request): Json | null {
  const data = request.postData();
  return data ? JSON.parse(data) as Json : null;
}
