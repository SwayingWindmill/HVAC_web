import { queryOptions } from '@tanstack/react-query';
import { alarmPaths } from '@/api/generated/platformGateway.gen';

// Alarm owner API through the Platform Gateway. Condition (active/cleared) and
// acknowledgement are independent, as in ThingsBoard's alarm model: an alarm clears
// only when its condition returns to normal; acknowledging records that someone is on it.

export type AlarmSeverity = 'CRITICAL' | 'MAJOR' | 'MINOR' | 'WARNING' | 'INFO';
export type AlarmCondition = 'ACTIVE' | 'CLEARED';
export type AlarmOperation = 'PUBLISH' | 'ACKNOWLEDGE' | 'ASSIGN' | 'UNASSIGN' | 'SUPPRESS' | 'UNSUPPRESS' | 'CLEAR';

export interface AlarmTimelineEntry {
  operation: AlarmOperation;
  condition: AlarmCondition;
  reason: string;
  actorType: string;
  actorId?: string;
  assigneeId?: string;
  currentSeverity: AlarmSeverity;
  occurredAt: string;
  version: number;
}

export interface Alarm {
  alarmId: string;
  siteId: string;
  deviceId?: string;
  alarmType: string;
  title: string;
  summary: string;
  condition: AlarmCondition;
  currentSeverity: AlarmSeverity;
  peakSeverity: AlarmSeverity;
  acknowledgement?: { acknowledgedAt: string; acknowledgedBy: string; comment?: string };
  assigneeId?: string;
  occurrenceCount: number;
  firstOccurredAt: string;
  lastOccurredAt: string;
  clearedAt?: string;
  timeline: AlarmTimelineEntry[];
  version: number;
}

export interface AlarmPage {
  items: Alarm[];
  nextCursor: string | null;
  hasMore: boolean;
}

export interface AlarmListFilter {
  condition?: AlarmCondition;
  acknowledged?: boolean;
}

export class AlarmRequestError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

async function send<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...init,
    credentials: 'same-origin',
    headers: { Accept: 'application/json, application/problem+json', ...init.headers },
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    const message = payload?.detail ?? payload?.error?.message ?? payload?.title ?? '告警服务暂时不可用';
    throw new AlarmRequestError(response.status, message);
  }
  return payload as T;
}

function mutationHeaders(csrfToken: string): HeadersInit {
  return {
    'Content-Type': 'application/json',
    'X-CSRF-Token': csrfToken,
    'Idempotency-Key': `alarm-${crypto.randomUUID()}`,
  };
}

export async function listAlarms(siteId: string, filter: AlarmListFilter, cursor?: string, signal?: AbortSignal): Promise<AlarmPage> {
  const query = new URLSearchParams({ siteId, limit: '100' });
  if (filter.condition) query.set('condition', filter.condition);
  if (filter.acknowledged !== undefined) query.set('acknowledged', String(filter.acknowledged));
  if (cursor) query.set('cursor', cursor);
  const body = await send<{ data: Alarm[]; meta: { nextCursor: string | null; hasMore: boolean } }>(
    `${alarmPaths.list}?${query.toString()}`,
    { signal },
  );
  return { items: body.data, nextCursor: body.meta.nextCursor, hasMore: body.meta.hasMore };
}

export async function getAlarm(alarmId: string, signal?: AbortSignal): Promise<Alarm> {
  const body = await send<{ data: Alarm }>(alarmPaths.detail.replace('{alarmId}', alarmId), { signal });
  return body.data;
}

export async function acknowledgeAlarm(alarmId: string, comment: string, csrfToken: string): Promise<Alarm> {
  const body = await send<{ data: Alarm }>(alarmPaths.acknowledge.replace('{alarmId}', alarmId), {
    method: 'POST',
    headers: mutationHeaders(csrfToken),
    body: JSON.stringify(comment.trim() ? { comment: comment.trim() } : {}),
  });
  return body.data;
}

export async function assignAlarm(alarm: Alarm, assigneeId: string, csrfToken: string): Promise<Alarm> {
  const body = await send<{ data: Alarm }>(alarmPaths.assign.replace('{alarmId}', alarm.alarmId), {
    method: 'POST',
    headers: mutationHeaders(csrfToken),
    body: JSON.stringify({ expectedVersion: alarm.version, assigneeId, reason: '认领处理' }),
  });
  return body.data;
}

export const alarmKeys = {
  all: (siteId: string) => ['alarms', siteId] as const,
  list: (siteId: string, filter: AlarmListFilter) => ['alarms', siteId, 'list', filter] as const,
  activeSummary: (siteId: string) => ['alarms', siteId, 'active-summary'] as const,
  detail: (siteId: string, alarmId: string) => ['alarms', siteId, 'detail', alarmId] as const,
};

const PAGE_LIMIT = 10;

// The Alarm owner has no stream yet; views follow it on a short interval. Every active alarm is read,
// up to PAGE_LIMIT pages; `complete` says whether that covered them all.
export function activeAlarmsQuery(siteId: string) {
  return queryOptions({
    queryKey: alarmKeys.activeSummary(siteId),
    queryFn: async ({ signal }) => {
      const items: Alarm[] = [];
      let cursor: string | undefined;
      for (let page = 0; page < PAGE_LIMIT; page += 1) {
        const result = await listAlarms(siteId, { condition: 'ACTIVE' }, cursor, signal);
        items.push(...result.items);
        if (!result.hasMore) return { items, complete: true };
        cursor = result.nextCursor ?? undefined;
      }
      return { items, complete: false };
    },
    refetchInterval: 15_000,
  });
}

export function alarmDetailQuery(siteId: string, alarmId: string) {
  return queryOptions({
    queryKey: alarmKeys.detail(siteId, alarmId),
    queryFn: ({ signal }) => getAlarm(alarmId, signal),
  });
}

const HISTORY_DAYS = 14;

/** Alarms raised since a day before the 14-day window (the view trims to Site-local days), newest first; `complete` is false when the page cap cut it short. */
export function alarmHistoryQuery(siteId: string) {
  return queryOptions({
    queryKey: [...alarmKeys.all(siteId), 'history', HISTORY_DAYS] as const,
    queryFn: async ({ signal }) => {
      const since = Date.now() - (HISTORY_DAYS + 1) * 86_400_000;
      const alarms: Alarm[] = [];
      let cursor: string | undefined;
      for (let page = 0; page < PAGE_LIMIT; page += 1) {
        const result = await listAlarms(siteId, {}, cursor, signal);
        alarms.push(...result.items.filter((alarm) => Date.parse(alarm.firstOccurredAt) >= since));
        const reachedStart = result.items.some((alarm) => Date.parse(alarm.firstOccurredAt) < since);
        if (!result.hasMore || reachedStart) return { alarms, complete: true, days: HISTORY_DAYS };
        cursor = result.nextCursor ?? undefined;
      }
      return { alarms, complete: false, days: HISTORY_DAYS };
    },
    refetchInterval: 5 * 60_000,
  });
}
