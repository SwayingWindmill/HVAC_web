import { Outlet, createFileRoute } from '@tanstack/react-router';
import { fallback, zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';

import { requireCapabilities } from '@/app/route-access';

const issuesSearchSchema = z.object({
  alarmView: fallback(z.enum(['active', 'history', 'suppressed', 'performance']), 'active').optional(),
  q: z.string().optional(),
  severity: z.enum(['CRITICAL', 'MAJOR', 'MINOR', 'WARNING', 'INFO']).optional(),
  ack: z.enum(['unacknowledged', 'acknowledged']).optional(),
  owner: z.enum(['unassigned', 'assigned']).optional(),
  sourceType: z.enum(['DEVICE_RULE', 'SITE_RULE', 'EXTERNAL']).optional(),
  selected: z.string().optional(),
  source: z.string().optional(),
  device: z.string().optional(),
  deviceId: z.string().optional(),
  performancePeriod: z.enum(['7d', '30d', '90d']).optional(),
  queueMode: z.enum(['problems', 'alarms']).optional(),
  selectedIssue: z.string().optional(),
  issueState: z.enum(['OPEN', 'INVESTIGATING', 'ACTION_PENDING', 'VERIFYING', 'RESOLVED']).optional(),
  diagnosis: z.enum(['PENDING', 'PUBLISHED', 'EVIDENCE_LIMITED', 'ROOT_CAUSE_CONFIRMED']).optional(),
  impact: z.enum(['quantified', 'high-risk']).optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/issues')({
  validateSearch: zodValidator(issuesSearchSchema),
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '告警与诊断',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: { id: 'site-issues', label: '告警与诊断', group: 'management', order: 30, siteLeaf: 'issues' },
  },
  component: Outlet,
});
