import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { RouteLoading } from '@/app/RouteLoading';
import { IntegrationsWorkspace } from '@/features/integrations/IntegrationsWorkspace';

export const integrationsSearchSchema = z.object({
  site: z.string().uuid().optional().catch(undefined),
  view: z.enum(['gateways', 'meters', 'sensors']).optional().catch(undefined),
  inspect: z.string().min(1).max(256).optional().catch(undefined),
  filter: z.enum(['ALL', 'ATTENTION', 'PROBE_OFFSET', 'CALIBRATION_DUE']).optional().catch(undefined),
});

export type IntegrationsSearch = z.infer<typeof integrationsSearchSchema>;

export const Route = createFileRoute('/_app/settings/integrations')({
  validateSearch: integrationsSearchSchema,
  staticData: {
    title: '集成管理',
    scope: 'platform',
    navigation: {
      id: 'settings-integrations',
      label: '集成管理',
      group: 'system',
      order: 85,
    },
  },
  component: IntegrationsRoute,
});

function IntegrationsRoute() {
  return (
    <Suspense fallback={<RouteLoading label="正在加载集成管理" />}>
      <IntegrationsWorkspace />
    </Suspense>
  );
}
