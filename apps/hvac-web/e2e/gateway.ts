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

function replayEnergy(series: Json, query: Json): Json {
  if (query.granularity !== 'hour') return series;
  const from = Date.parse(query.from);
  const to = Date.parse(query.to);
  const points: Json[] = [];
  for (let day = from; day < to; day += DAY_MS) {
    for (const point of series.points) {
      const shift = day - RECORDED_DAY;
      points.push({
        ...point,
        periodStart: new Date(Date.parse(point.periodStart) + shift).toISOString(),
        periodEnd: new Date(Date.parse(point.periodEnd) + shift).toISOString(),
      });
    }
  }
  return { ...series, points };
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
  readonly findings = fixture<Json>('fdd-findings.json').items as Json[];
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
      schemaVersion: 1, commandId: `command-${this.commands.length + 1}`, siteId: SITE_ID, deviceId: point.reportingDeviceId, pointId: point.id,
      capability, status: 'SUBMITTED', risk, approvalPolicy: risk === 'LOW' ? 'NONE' : 'SINGLE_APPROVER',
      approvalCount: 0, requiredApprovalCount: risk === 'LOW' ? 0 : 1, parameters: input.parameters, transitions: [],
      createdAt: now, updatedAt: now,
    };
    advance(command, 'SUBMITTED', now);
    advance(command, risk === 'LOW' ? 'SUCCEEDED' : 'AWAITING_APPROVAL', now);
    this.commands.unshift(command);
    return command;
  }

  // The recorded loop still runs below the rule's threshold, so each evaluation records a finding.
  private evaluateLowDeltaT(input: Json, now: string): Json {
    const recorded = fixture<Json>('fdd-findings.json').items[0];
    const finding = {
      ...recorded, id: `finding-${this.findings.length + 1}`, assetId: input.assetId, evaluationFrom: input.evaluationFrom, evaluationTo: input.evaluationTo,
      ruleRevisionId: input.ruleRevisionId, thresholdValue: input.minimumDeltaTC, createdAt: now,
    };
    if (finding.observedValue >= input.minimumDeltaTC) return { status: 'CLEAR', deltaTC: finding.observedValue };
    this.findings.unshift(finding);
    return { status: 'FINDING', deltaTC: finding.observedValue, finding };
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
    if (method === 'POST' && path === '/api/v1/telemetry/observation-snapshots:batchGet') return { body: this.snapshotBatch(body!) };
    if (method === 'POST' && path === '/api/v1/telemetry/device-series:aggregate') return { body: fixture('history.json') };
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/fdd/findings`) return { body: { items: this.findings } };
    if (method === 'POST' && path === `/api/v1/sites/${SITE_ID}/fdd/evaluate/low-delta-t`) return { status: 201, body: this.evaluateLowDeltaT(body!, now) };
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/forecast/load`) return { body: replayForecast(fixture<Json>('forecast-load.json'), now) };
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

function bodyOf(request: Request): Json | null {
  const data = request.postData();
  return data ? JSON.parse(data) as Json : null;
}
