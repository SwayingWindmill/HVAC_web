import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { RouteLoading } from '@/app/RouteLoading';
import { DiagnosticsFddConsole } from '@/features/diagnostics/DiagnosticsFddConsole';

const diagnosticsSearchSchema = z.object({
  diagnosis: z.string().optional(),
  q: z.string().optional(),
  source: z.string().optional(),
  alarm: z.string().optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/diagnostics')({
  validateSearch: zodValidator(diagnosticsSearchSchema),
  staticData: {
    title: '诊断中心',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: { id: 'site-diagnostics', label: '诊断', group: 'management', order: 40, siteLeaf: 'diagnostics' },
  },
  component: DiagnosticsRoute,
});

function DiagnosticsRoute() {
  const { site, principal, runtime } = Route.useRouteContext();
  const searchState = Route.useSearch();
  const navigate = Route.useNavigate();

  return (
    <section className="real-route-surface" data-route-state="READY" data-site-id={site.id} data-site-route="diagnostics">
      <Suspense fallback={<RouteLoading label="正在加载诊断中心" />}>
        <DiagnosticsFddConsole
          site={site}
          principal={principal}
          runtime={runtime}
          searchState={searchState}
          onSearchChange={(patch: any) => {
            void navigate({
              search: (previous: any) => ({ ...previous, ...(patch as object) }),
              replace: true,
            });
          }}
        />
      </Suspense>
    </section>
  );
}
