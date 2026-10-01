import { z } from 'zod';

import {
  createFrontendReviewIssueInvestigation,
  createFrontendReviewIssuePerformance,
  createFrontendReviewIssueQueue,
} from '@/app/frontend-review-issues-data';

const uuidSchema = z.string().uuid();

export const issueImpactSchema = z.object({
  avoidableEnergyKwh: z.number().nonnegative().nullable(),
  avoidableCost: z.number().nonnegative().nullable(),
  currency: z.string().min(3).max(3).nullable(),
  comfortImpactHours: z.number().nonnegative().nullable(),
  reliabilityRisk: z.enum(['LOW', 'MEDIUM', 'HIGH', 'CRITICAL']).nullable(),
  estimatedFrom: z.string().datetime().nullable(),
  estimatedTo: z.string().datetime().nullable(),
}).strict();

export const issueGroupingSchema = z.object({
  method: z.enum(['RULE', 'TOPOLOGY', 'TEMPORAL', 'CORRELATION', 'MANUAL', 'MODEL']),
  reason: z.string().min(1),
  confidence: z.number().min(0).max(1).nullable(),
}).strict();

export const issueQueueItemSchema = z.object({
  issueId: uuidSchema,
  title: z.string().min(1),
  state: z.enum(['OPEN', 'INVESTIGATING', 'ACTION_PENDING', 'VERIFYING', 'RESOLVED']),
  highestSeverity: z.enum(['CRITICAL', 'MAJOR', 'MINOR', 'WARNING', 'INFO']),
  object: z.object({
    type: z.enum(['SITE', 'SYSTEM', 'ASSET', 'DEVICE']),
    id: uuidSchema.nullable(),
    label: z.string().min(1),
    locationLabel: z.string().min(1).nullable(),
  }).strict(),
  alarmIds: z.array(uuidSchema).min(1),
  signals: z.array(z.object({
    alarmId: uuidSchema,
    title: z.string().min(1),
    severity: z.enum(['CRITICAL', 'MAJOR', 'MINOR', 'WARNING', 'INFO']),
    condition: z.enum(['ACTIVE', 'CLEARED']),
    acknowledged: z.boolean(),
    occurredAt: z.string().datetime(),
    updatedAt: z.string().datetime(),
  }).strict()).min(1),
  activeAlarmCount: z.number().int().nonnegative(),
  unacknowledgedAlarmCount: z.number().int().nonnegative(),
  diagnosisState: z.enum(['PENDING', 'PUBLISHED', 'EVIDENCE_LIMITED', 'ROOT_CAUSE_CONFIRMED']),
  assignee: z.string().min(1).nullable(),
  firstDetectedAt: z.string().datetime(),
  updatedAt: z.string().datetime(),
  impact: issueImpactSchema,
  grouping: issueGroupingSchema,
  nextAction: z.string().min(1).nullable(),
}).strict();

export const issueQueueSchema = z.object({
  schemaVersion: z.literal(1),
  siteId: uuidSchema,
  items: z.array(issueQueueItemSchema),
  total: z.number().int().nonnegative(),
  active: z.number().int().nonnegative(),
  unassigned: z.number().int().nonnegative(),
  highImpact: z.number().int().nonnegative(),
  groupedAlarmCount: z.number().int().nonnegative(),
  rawAlarmCount: z.number().int().nonnegative(),
}).strict();

export type IssueQueue = z.infer<typeof issueQueueSchema>;
export type IssueQueueItem = z.infer<typeof issueQueueItemSchema>;
export type IssueQueueScope = 'active' | 'all' | 'suppressed';
export type IssueImpactFilter = 'quantified' | 'high-risk';

export interface IssueQueueQuery {
  readonly scope: IssueQueueScope;
  readonly q?: string;
  readonly severity?: IssueQueueItem['highestSeverity'];
  readonly state?: IssueQueueItem['state'];
  readonly diagnosis?: IssueQueueItem['diagnosisState'];
  readonly owner?: 'assigned' | 'unassigned';
  readonly impact?: IssueImpactFilter;
}

export const issueHypothesisSchema = z.object({
  id: z.string().min(1),
  title: z.string().min(1),
  status: z.enum(['CANDIDATE', 'SUPPORTED', 'WEAKENED', 'REJECTED', 'CONFIRMED']),
  rationale: z.string().min(1),
  supportingEvidenceCount: z.number().int().nonnegative(),
  contradictingEvidenceCount: z.number().int().nonnegative(),
  nextVerification: z.string().min(1).nullable(),
}).strict();

export const rootCauseDecisionSchema = z.object({
  status: z.enum(['UNCONFIRMED', 'CONFIRMED']),
  title: z.string().min(1).nullable(),
  rationale: z.string().min(1).nullable(),
  evidenceIds: z.array(z.string().min(1)),
  confirmedAt: z.string().datetime().nullable(),
  confirmedBy: z.string().min(1).nullable(),
}).strict();

export const verificationStepSchema = z.object({
  id: z.string().min(1),
  title: z.string().min(1),
  method: z.string().min(1),
  status: z.enum(['PENDING', 'IN_PROGRESS', 'PASSED', 'FAILED']),
  owner: z.string().min(1).nullable(),
  dueAt: z.string().datetime().nullable(),
}).strict();

export const relatedIssueSchema = z.object({
  alarmId: uuidSchema,
  title: z.string().min(1),
  relationship: z.enum(['SAME_INCIDENT', 'SAME_EQUIPMENT', 'UPSTREAM', 'DOWNSTREAM', 'RECURRING_PATTERN']),
  occurredAt: z.string().datetime(),
}).strict();

export const issueInvestigationSchema = z.object({
  schemaVersion: z.literal(1),
  alarmId: uuidSchema,
  status: z.enum(['NOT_STARTED', 'INVESTIGATING', 'ROOT_CAUSE_CONFIRMED', 'VERIFICATION_PENDING', 'VERIFIED']),
  impact: issueImpactSchema,
  hypotheses: z.array(issueHypothesisSchema),
  rootCause: rootCauseDecisionSchema,
  verificationSteps: z.array(verificationStepSchema),
  relatedIssues: z.array(relatedIssueSchema),
  updatedAt: z.string().datetime(),
}).strict();

export type IssueInvestigation = z.infer<typeof issueInvestigationSchema>;

export const issuePerformanceSchema = z.object({
  schemaVersion: z.literal(1),
  siteId: uuidSchema,
  period: z.enum(['7d', '30d', '90d']),
  from: z.string().datetime(),
  to: z.string().datetime(),
  totalTriggered: z.number().int().nonnegative(),
  activeAtEnd: z.number().int().nonnegative(),
  acknowledgementRate: z.number().min(0).max(1),
  diagnosisCoverage: z.number().min(0).max(1),
  averageAcknowledgeSeconds: z.number().nonnegative().nullable(),
  averageResolutionSeconds: z.number().nonnegative().nullable(),
  repeatIssueRate: z.number().min(0).max(1),
  floodWindows: z.number().int().nonnegative(),
  avoidableEnergyKwh: z.number().nonnegative().nullable(),
  avoidableCost: z.number().nonnegative().nullable(),
  currency: z.string().min(3).max(3).nullable(),
  heatmap: z.object({
    columns: z.array(z.string().min(1)).min(1),
    rows: z.array(z.object({
      label: z.string().min(1),
      values: z.array(z.number().int().nonnegative()),
    }).strict()).min(1),
  }).strict(),
  topContributors: z.array(z.object({
    key: z.string().min(1),
    label: z.string().min(1),
    triggeredCount: z.number().int().nonnegative(),
    averageResolutionSeconds: z.number().nonnegative().nullable(),
  }).strict()),
  recurringIssues: z.array(z.object({
    key: z.string().min(1),
    label: z.string().min(1),
    occurrences: z.number().int().positive(),
    avoidableEnergyKwh: z.number().nonnegative().nullable(),
  }).strict()),
}).strict();

export type IssuePerformance = z.infer<typeof issuePerformanceSchema>;
export type IssuePerformancePeriod = IssuePerformance['period'];

async function readJSON<T>(path: string, schema: z.ZodType<T>, signal?: AbortSignal): Promise<T | null> {
  const response = await fetch(path, {
    method: 'GET',
    credentials: 'same-origin',
    headers: { Accept: 'application/json, application/problem+json' },
    signal,
  });
  if (response.status === 404 || response.status === 501) return null;
  if (!response.ok) throw new Error(`Issue analytics request failed: ${response.status}`);
  const payload: unknown = await response.json();
  return schema.parse(payload);
}

function issueMatchesImpact(item: IssueQueueItem, impact: IssueImpactFilter | undefined): boolean {
  if (!impact) return true;
  if (impact === 'quantified') {
    return item.impact.avoidableCost !== null
      || item.impact.avoidableEnergyKwh !== null
      || item.impact.comfortImpactHours !== null;
  }
  return item.impact.reliabilityRisk === 'HIGH' || item.impact.reliabilityRisk === 'CRITICAL';
}

function filterFrontendReviewQueue(queue: IssueQueue, query: IssueQueueQuery): IssueQueue {
  const normalizedQuery = query.q?.trim().toLocaleLowerCase('zh-CN') ?? '';
  const items = queue.items
    .filter((item) => !query.severity || item.highestSeverity === query.severity)
    .filter((item) => !query.state || item.state === query.state)
    .filter((item) => !query.diagnosis || item.diagnosisState === query.diagnosis)
    .filter((item) => !query.owner || (query.owner === 'assigned' ? Boolean(item.assignee) : !item.assignee))
    .filter((item) => issueMatchesImpact(item, query.impact))
    .filter((item) => !normalizedQuery || [
      item.title,
      item.object.label,
      item.object.locationLabel ?? '',
      item.assignee ?? '',
      item.nextAction ?? '',
      ...item.signals.map((signal) => signal.title),
    ].some((value) => value.toLocaleLowerCase('zh-CN').includes(normalizedQuery)));

  return issueQueueSchema.parse({
    ...queue,
    items,
    total: items.length,
    active: items.filter((item) => item.state !== 'RESOLVED').length,
    unassigned: items.filter((item) => item.state !== 'RESOLVED' && !item.assignee).length,
    highImpact: items.filter((item) => item.impact.reliabilityRisk === 'HIGH' || (item.impact.avoidableCost ?? 0) >= 500).length,
    groupedAlarmCount: items.reduce((total, item) => total + item.alarmIds.length, 0),
  });
}

export async function getIssueQueue(siteId: string, query: IssueQueueQuery, signal?: AbortSignal): Promise<IssueQueue | null> {
  if (typeof __HVAC_WEB_FRONTEND_REVIEW__ !== 'undefined' && __HVAC_WEB_FRONTEND_REVIEW__) {
    const queue = issueQueueSchema.parse(createFrontendReviewIssueQueue(siteId, query.scope));
    return filterFrontendReviewQueue(queue, query);
  }

  const params = new URLSearchParams({ scope: query.scope });
  if (query.q) params.set('q', query.q);
  if (query.severity) params.set('severity', query.severity);
  if (query.state) params.set('state', query.state);
  if (query.diagnosis) params.set('diagnosis', query.diagnosis);
  if (query.owner) params.set('owner', query.owner);
  if (query.impact) params.set('impact', query.impact);

  return readJSON(
    `/api/v1/sites/${encodeURIComponent(siteId)}/issues?${params.toString()}`,
    issueQueueSchema,
    signal,
  );
}

export async function getIssue(siteId: string, issueId: string, signal?: AbortSignal): Promise<IssueQueueItem | null> {
  if (typeof __HVAC_WEB_FRONTEND_REVIEW__ !== 'undefined' && __HVAC_WEB_FRONTEND_REVIEW__) {
    const queue = issueQueueSchema.parse(createFrontendReviewIssueQueue(siteId, 'all'));
    const item = queue.items.find((candidate) => candidate.issueId === issueId);
    return item ? issueQueueItemSchema.parse(item) : null;
  }
  return readJSON(
    `/api/v1/sites/${encodeURIComponent(siteId)}/issues/${encodeURIComponent(issueId)}`,
    issueQueueItemSchema,
    signal,
  );
}

export async function getIssueInvestigation(siteId: string, alarmId: string, signal?: AbortSignal): Promise<IssueInvestigation | null> {
  if (typeof __HVAC_WEB_FRONTEND_REVIEW__ !== 'undefined' && __HVAC_WEB_FRONTEND_REVIEW__) {
    return issueInvestigationSchema.parse(createFrontendReviewIssueInvestigation(siteId, alarmId));
  }
  return readJSON(
    `/api/v1/sites/${encodeURIComponent(siteId)}/issues/${encodeURIComponent(alarmId)}/investigation`,
    issueInvestigationSchema,
    signal,
  );
}

export async function getIssuePerformance(siteId: string, period: IssuePerformancePeriod, signal?: AbortSignal): Promise<IssuePerformance | null> {
  if (typeof __HVAC_WEB_FRONTEND_REVIEW__ !== 'undefined' && __HVAC_WEB_FRONTEND_REVIEW__) {
    return issuePerformanceSchema.parse(createFrontendReviewIssuePerformance(siteId, period));
  }
  return readJSON(
    `/api/v1/sites/${encodeURIComponent(siteId)}/issues/performance?period=${encodeURIComponent(period)}`,
    issuePerformanceSchema,
    signal,
  );
}
