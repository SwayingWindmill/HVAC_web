import { Suspense, useCallback } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { fallback, zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import type { ProtectedScopeDraft, ProtectedScopeResource } from '@/app/protected-scope';
import { AlarmCenterWorkbench } from '@/features/alarms/AlarmCenterWorkbench';

const alarmSearchSchema = z.object({
  view: fallback(z.enum(['active', 'history', 'suppressed', 'performance']), 'active').optional(),
  q: z.string().optional(),
  severity: z.enum(['CRITICAL', 'MAJOR', 'MINOR', 'WARNING', 'INFO']).optional(),
  ack: z.enum(['unacknowledged', 'acknowledged']).optional(),
  owner: z.enum(['unassigned', 'assigned']).optional(),
  sourceType: z.enum(['DEVICE_RULE', 'SITE_RULE', 'EXTERNAL']).optional(),
  selected: z.string().optional(),
  source: z.string().optional(),
  device: z.string().optional(),
  deviceId: z.string().optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/alarms')({
  validateSearch: zodValidator(alarmSearchSchema),
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['alarm.list']);
  },
  staticData: {
    title: '告警',
    scope: 'site',
    requiredCapabilities: ['site.read', 'alarm.list'],
    navigation: { id: 'site-alarms', label: '告警', group: 'management', order: 30, siteLeaf: 'alarms' },
  },
  component: AlarmsRoute,
});

function AlarmsRoute() {
  const { site, principal, runtime } = Route.useRouteContext();
  const searchState = Route.useSearch();
  const navigate = Route.useNavigate();
  const registerUnsavedDraft = useCallback(
    (draft: ProtectedScopeDraft) => runtime.registerUnsavedDraft(draft),
    [runtime],
  );
  const registerProtectedResource = useCallback(
    (resource: ProtectedScopeResource) => runtime.registerProtectedResource(resource),
    [runtime],
  );
  return (
    <section className="real-route-surface real-route-surface--alarms" data-testid="real-site-route-alarms" data-route-state="READY" data-site-id={site.id} data-site-route="alarms">
      <Suspense fallback={<RouteLoading label="正在加载告警" />}>
        <AlarmCenterWorkbench
          site={site}
          principal={principal}
          searchState={searchState}
          onSearchChange={(patch: any) => {
            void navigate({
              search: (previous: any) => ({ ...previous, ...(patch as object) }),
              replace: true,
            });
          }}
          registerUnsavedDraft={registerUnsavedDraft}
          registerProtectedResource={registerProtectedResource}
        />
      </Suspense>
    </section>
  );
}
