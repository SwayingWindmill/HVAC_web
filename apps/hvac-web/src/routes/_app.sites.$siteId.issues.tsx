import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { fallback, zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { IssuesWorkspace } from '@/features/issues/IssuesWorkspace';

const issuesSearchSchema = z.object({
  view: fallback(z.enum(['alarms', 'diagnostics']), 'alarms').optional(),
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
  diagnosis: z.string().optional(),
  alarm: z.string().optional(),
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
  component: IssuesRoute,
});

function IssuesRoute() {
  const { site, principal, runtime } = Route.useRouteContext();
  const searchState = Route.useSearch();
  const navigate = Route.useNavigate();

  return (
    <section data-route-state="READY" data-site-id={site.id} data-site-route="issues" aria-label="告警与诊断">
      <Suspense fallback={<RouteLoading label="正在加载告警与诊断" />}>
        <IssuesWorkspace
          site={site}
          principal={principal}
          runtime={runtime}
          searchState={searchState}
          onSearchChange={(patch) => {
            void navigate({
              search: (previous) => ({ ...previous, ...patch }),
              replace: true,
            });
          }}
        />
      </Suspense>
    </section>
  );
}
