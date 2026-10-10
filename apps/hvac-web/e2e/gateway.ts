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

export class Gateway {
  readonly alarms = fixture<Json[]>('alarms.json');
  readonly workOrders = fixture<Json[]>('work-orders.json');
  readonly snapshots = fixture<Json>('snapshots.json');
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

  private respond(method: string, path: string, query: URLSearchParams, body: Json | null): { status?: number; body: unknown } | undefined {
    const now = new Date().toISOString();
    if (method === 'GET' && path === '/api/v1/principal') return { body: principal };
    if (method === 'GET' && path === '/api/v1/platform/status') return { body: fixture('platform-status.json') };
    if (method === 'GET' && path === '/api/v1/sites') return { body: fixture('sites.json') };
    if (method === 'GET' && path === '/api/v1/notifications/inbox') return { body: { data: [], meta: { requestId: 'e2e', count: 0 } } };
    if (method === 'GET' && path === `/api/v1/sites/${SITE_ID}/asset-model`) return { body: fixture('asset-model.json') };
    if (method === 'POST' && path === '/api/v1/telemetry/observation-snapshots:batchGet') return { body: this.snapshots };
    if (method === 'POST' && path === '/api/v1/telemetry/device-series:aggregate') return { body: fixture('history.json') };
    if (method === 'POST' && path === '/api/v1/analytics/energy-series') return { body: fixture<Json>('energy-series.json')[body?.energyType] };

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
