import { queryOptions } from '@tanstack/react-query';
import { failureStatus, gatewayPath, gatewayRequest, ROUTE_MISSING_MESSAGE, type GatewayRequest } from '@/api/gateway-client';
import {
  alarmPaths,
  alarmRuleDeadLetterListEnvelopeSchema,
  alarmRuleDeadLetterReplayEnvelopeSchema,
  siteAlarmRoutingEnvelopeSchema,
  siteAlarmRoutingReloadEnvelopeSchema,
  type AlarmRuleDeadLetter,
  type AlarmRuleDeadLetterReplay,
  type SiteAlarmRoutingReloadResult,
  type SiteAlarmRoutingSummary,
} from '@/api/generated/platformGateway.gen';

export type {
  AlarmRuleDeadLetter,
  AlarmRuleDeadLetterReplay,
  SiteAlarmRoutingReloadResult,
  SiteAlarmRoutingSummary,
};

// Alarm owner API through the Platform Gateway. Condition (active/cleared) and
// acknowledgement are independent, as in ThingsBoard's alarm model: an alarm clears
// only when its condition returns to normal; acknowledging records that someone is on it.

export type AlarmSeverity = 'CRITICAL' | 'MAJOR' | 'MINOR' | 'WARNING' | 'INFO';
export type AlarmCondition = 'ACTIVE' | 'CLEARED';
export type AlarmOperation = 'PUBLISH' | 'ACKNOWLEDGE' | 'ASSIGN' | 'UNASSIGN' | 'SUPPRESS' | 'UNSUPPRESS' | 'CLEAR';
export type AlarmEvaluationStatus = 'MATCHED' | 'NOT_MATCHED' | 'INDETERMINATE';
export type AlarmEvaluationEffect = 'NONE' | 'PUBLISH' | 'CLEAR';

export interface AlarmSuppression {
  readonly startsAt: string;
  readonly expiresAt: string;
  readonly reason: string;
  readonly actorId: string;
  readonly policyRevision: string;
}

export interface AlarmEvaluationRecord {
  readonly evaluatedAt: string;
  readonly policyRevisionId: string;
  readonly assignmentId: string;
  readonly inputRevision: string;
  readonly status: AlarmEvaluationStatus;
  readonly effect: AlarmEvaluationEffect;
  readonly qualityBlocker?: string;
  readonly snapshot: Readonly<Record<string, unknown>>;
  readonly activeIncidentCorrelationId?: string;
}

export interface AlarmEvaluationList {
  readonly schemaVersion: 1;
  readonly alarmId: string;
  readonly items: readonly AlarmEvaluationRecord[];
}

export interface AlarmTimelineEntry {
  operation: AlarmOperation;
  condition: AlarmCondition;
  reason: string;
  actorType: string;
  actorId?: string;
  assigneeId?: string;
  suppression?: AlarmSuppression;
  currentSeverity: AlarmSeverity;
  occurredAt: string;
  version: number;
}

export interface AlarmEdgeProjection {
  readonly gatewayId: string;
  readonly offline: boolean;
  readonly bundleRevision: number;
  readonly edgeBundleDigest?: string;
  readonly occurredAt?: string;
  readonly receivedAt?: string;
  readonly triggeredActions?: readonly string[];
  readonly clearOffline?: boolean;
  readonly clearOccurredAt?: string;
  readonly clearTriggeredActions?: readonly string[];
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
  suppression?: AlarmSuppression;
  occurrenceCount: number;
  firstOccurredAt: string;
  lastOccurredAt: string;
  clearedAt?: string;
  edgeProjection?: AlarmEdgeProjection;
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
  suppressed?: boolean;
  activeFrom?: string;
  activeTo?: string;
}

export class AlarmRequestError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

const ALARM_PROBLEM_MESSAGES: Readonly<Record<string, string>> = {
  ALARM_VERSION_CONFLICT: '告警状态刚刚已被其他人更新，请刷新后再试',
  ALARM_RULE_BUNDLE_CONFLICT: '规则版本已被他人更新，请重新加载最新版本后再发布',
  ALARM_RULE_GRAPH_INVALID: '联动依赖关系存在环路、缺失上游或超过最大深度限制，请检查后重试',
  ALARM_RULE_DRAFT_INVALID: '规则草稿配置存在不合法项，请修正后重试',
  ALARM_TRANSITION_INVALID: '当前告警状态不支持这个操作',
  ALARM_IDEMPOTENCY_CONFLICT: '重复请求与此前提交内容不一致，请刷新重试',
  ALARM_ACCESS_DENIED: '你没有执行这项告警操作的权限',
  FORBIDDEN: '你没有执行这项告警操作的权限',
  RESOURCE_NOT_FOUND: '未找到这条告警，或已无权限查看',
  ALARM_NOT_FOUND: '未找到这条告警，或已无权限查看',
  ORIGIN_NOT_ALLOWED: '页面来源校验未通过，请刷新页面后重试',
  CSRF_TOKEN_INVALID: '会话安全凭证失效，请刷新页面后重试',
  CSRF_REQUIRED: '缺少会话安全凭证，请刷新页面后重试',
  CSRF_INVALID: '会话安全凭证无效，请刷新页面后重试',
};

function formatAlarmProblemMessage(problem: { code?: string; detail?: string; title?: string }): string {
  const code = problem.code ?? '';
  if (code in ALARM_PROBLEM_MESSAGES) {
    return ALARM_PROBLEM_MESSAGES[code];
  }
  const raw = problem.detail ?? problem.title ?? '';
  if (code === 'INVALID_ARGUMENT') {
    if (raw.includes('suppressedUntil')) return '抑制截止时间必须在当前时间之后';
    if (raw.includes('reason')) return '原因说明格式或长度不符合要求（最多 256 字节）';
    return '提交参数无效，请检查后重试';
  }
  if (/[\u4e00-\u9fff]/.test(raw)) {
    return raw;
  }
  return '告警操作未能完成，请稍后重试';
}

async function send<T>(path: string, request: GatewayRequest<T> = {}): Promise<T> {
  const result = await gatewayRequest<T>(path, request);
  if (result.kind === 'ok') return result.value;
  if (result.kind === 'routeMissing') throw new AlarmRequestError(result.status, ROUTE_MISSING_MESSAGE);
  const message = result.kind === 'unavailable'
    ? '告警服务暂时不可用'
    : formatAlarmProblemMessage(result.problem);
  throw new AlarmRequestError(failureStatus(result), message);
}

export async function listAlarms(siteId: string, filter: AlarmListFilter, cursor?: string, signal?: AbortSignal): Promise<AlarmPage> {
  const query = new URLSearchParams({ siteId, limit: '100' });
  if (filter.condition) query.set('condition', filter.condition);
  if (filter.acknowledged !== undefined) query.set('acknowledged', String(filter.acknowledged));
  if (filter.suppressed !== undefined) query.set('suppressed', String(filter.suppressed));
  if (filter.activeFrom) query.set('activeFrom', filter.activeFrom);
  if (filter.activeTo) query.set('activeTo', filter.activeTo);
  if (cursor) query.set('cursor', cursor);
  const body = await send<{ data: Alarm[]; meta: { nextCursor: string | null; hasMore: boolean } }>(
    `${alarmPaths.list}?${query.toString()}`,
    { signal },
  );
  return { items: body.data, nextCursor: body.meta.nextCursor, hasMore: body.meta.hasMore };
}

export async function getAlarm(alarmId: string, signal?: AbortSignal): Promise<Alarm> {
  const body = await send<{ data: Alarm }>(gatewayPath(alarmPaths.detail, { alarmId }), { signal });
  return body.data;
}

export async function acknowledgeAlarm(alarmId: string, comment: string, csrfToken: string): Promise<Alarm> {
  const body = await send<{ data: Alarm }>(gatewayPath(alarmPaths.acknowledge, { alarmId }), {
    method: 'POST',
    csrfToken,
    idempotencyKey: `alarm-${crypto.randomUUID()}`,
    body: comment.trim() ? { comment: comment.trim() } : {},
  });
  return body.data;
}

export async function assignAlarm(alarm: Alarm, assigneeId: string, csrfToken: string): Promise<Alarm> {
  const body = await send<{ data: Alarm }>(gatewayPath(alarmPaths.assign, { alarmId: alarm.alarmId }), {
    method: 'POST',
    csrfToken,
    idempotencyKey: `alarm-${crypto.randomUUID()}`,
    body: { expectedVersion: alarm.version, assigneeId, reason: '由我负责' },
  });
  return body.data;
}

export async function unassignAlarm(alarm: Alarm, reason: string, csrfToken: string): Promise<Alarm> {
  const body = await send<{ data: Alarm }>(gatewayPath(alarmPaths.unassign, { alarmId: alarm.alarmId }), {
    method: 'POST',
    csrfToken,
    idempotencyKey: `alarm-${crypto.randomUUID()}`,
    body: { expectedVersion: alarm.version, reason: reason.trim() || '交回班组待分配' },
  });
  return body.data;
}

export async function suppressAlarm(alarm: Alarm, suppressedUntil: string, reason: string, csrfToken: string): Promise<Alarm> {
  const body = await send<{ data: Alarm }>(gatewayPath(alarmPaths.suppress, { alarmId: alarm.alarmId }), {
    method: 'POST',
    csrfToken,
    idempotencyKey: `alarm-${crypto.randomUUID()}`,
    body: { expectedVersion: alarm.version, suppressedUntil, reason: reason.trim() },
  });
  return body.data;
}

export async function unsuppressAlarm(alarm: Alarm, reason: string, csrfToken: string): Promise<Alarm> {
  const body = await send<{ data: Alarm }>(gatewayPath(alarmPaths.unsuppress, { alarmId: alarm.alarmId }), {
    method: 'POST',
    csrfToken,
    idempotencyKey: `alarm-${crypto.randomUUID()}`,
    body: { expectedVersion: alarm.version, reason: reason.trim() || '恢复告警通知' },
  });
  return body.data;
}

export async function listAlarmEvaluations(alarmId: string, limit = 10, signal?: AbortSignal): Promise<AlarmEvaluationList> {
  const query = new URLSearchParams({ limit: String(limit) });
  const body = await send<{ data: AlarmEvaluationList }>(
    `${gatewayPath(alarmPaths.evaluations, { alarmId })}?${query.toString()}`,
    { signal },
  );
  return body.data;
}

export const alarmKeys = {
  all: (siteId: string) => ['alarms', siteId] as const,
  list: (siteId: string, filter: AlarmListFilter) => ['alarms', siteId, 'list', filter] as const,
  activeSummary: (siteId: string) => ['alarms', siteId, 'active-summary'] as const,
  detail: (siteId: string, alarmId: string) => ['alarms', siteId, 'detail', alarmId] as const,
  evaluations: (siteId: string, alarmId: string, limit: number) => ['alarms', siteId, 'evaluations', alarmId, limit] as const,
  ruleDeadLetters: (siteId: string, limit = 20) => ['alarms', siteId, 'rule-dead-letters', limit] as const,
  routing: (siteId: string) => ['alarms', siteId, 'routing'] as const,
};

export function alarmRuleDeadLettersQuery(siteId: string, limit = 20) {
  return queryOptions({
    queryKey: alarmKeys.ruleDeadLetters(siteId, limit),
    queryFn: async ({ signal }) => {
      const query = new URLSearchParams({ siteId, limit: String(limit) });
      const body = await send(`${alarmPaths.ruleDeadLetters}?${query.toString()}`, {
        signal,
        parse: (payload) => alarmRuleDeadLetterListEnvelopeSchema.parse(payload),
      });
      return body.data;
    },
    refetchInterval: 30_000,
  });
}

export async function replayAlarmRuleDeadLetter(
  siteId: string,
  outboxId: string,
  csrfToken: string,
): Promise<AlarmRuleDeadLetterReplay> {
  const path = gatewayPath(alarmPaths.ruleDeadLetterReplay, { outboxId });
  const query = new URLSearchParams({ siteId });
  const body = await send(`${path}?${query.toString()}`, {
    method: 'POST',
    csrfToken,
    body: {},
    parse: (payload) => alarmRuleDeadLetterReplayEnvelopeSchema.parse(payload),
  });
  return body.data;
}

export function siteAlarmRoutingQuery(siteId: string) {
  return queryOptions({
    queryKey: alarmKeys.routing(siteId),
    queryFn: async ({ signal }) => {
      const path = gatewayPath(alarmPaths.siteRouting, { siteId });
      const body = await send(path, {
        signal,
        parse: (payload) => siteAlarmRoutingEnvelopeSchema.parse(payload),
      });
      return body.data;
    },
  });
}

export async function reloadSiteAlarmRouting(
  siteId: string,
  csrfToken: string,
): Promise<SiteAlarmRoutingReloadResult> {
  const path = gatewayPath(alarmPaths.siteRoutingReload, { siteId });
  const body = await send(path, {
    method: 'POST',
    csrfToken,
    body: {},
    parse: (payload) => siteAlarmRoutingReloadEnvelopeSchema.parse(payload),
  });
  return body.data;
}

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
    refetchInterval: 30_000,
  });
}

/** The twenty alarms cleared last, the latest first (as the mini program's 通知 lists them). */
export function recentlyClearedAlarmsQuery(siteId: string) {
  return queryOptions({
    queryKey: [...alarmKeys.all(siteId), 'recently-cleared'] as const,
    queryFn: async ({ signal }) => {
      const result = await listAlarms(siteId, { condition: 'CLEARED' }, undefined, signal);
      const clearedAt = (alarm: Alarm) => Date.parse(alarm.clearedAt ?? alarm.lastOccurredAt);
      return [...result.items].sort((left, right) => clearedAt(right) - clearedAt(left)).slice(0, 20);
    },
    refetchInterval: 30_000,
  });
}

export function alarmDetailQuery(siteId: string, alarmId: string) {
  return queryOptions({
    queryKey: alarmKeys.detail(siteId, alarmId),
    queryFn: ({ signal }) => getAlarm(alarmId, signal),
  });
}

export function alarmEvaluationsQuery(siteId: string, alarmId: string, limit = 10) {
  return queryOptions({
    queryKey: alarmKeys.evaluations(siteId, alarmId, limit),
    queryFn: ({ signal }) => listAlarmEvaluations(alarmId, limit, signal),
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
