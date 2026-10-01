import React, { useEffect, useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from '@tanstack/react-router';
import { NuqsAdapter } from 'nuqs/adapters/tanstack-router';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { buildAppNavigation } from '@/components/layout/app-navigation';
import { AppHeader } from '@/components/layout/AppHeader';
import { AppSidebar } from '@/components/layout/AppSidebar';
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar';
import { TooltipProvider } from '@/components/ui/tooltip';
import { Overview } from '@/features/overview/Overview';
import { SystemOperations, type SystemOperationsSearchState } from '@/features/system-operations/SystemOperations';
import { TrendAnalysisDashboard } from '@/features/trends/TrendAnalysisDashboard';
import { AssetsConsole } from '@/features/assets-workspace/AssetsConsole';
import { DeviceDetailConsole } from '@/features/assets-workspace/DeviceDetailConsole';
import { ComfortDashboard } from '@/features/comfort/ComfortDashboard';
import { AlarmCommandDesk } from '@/features/alarms/AlarmCommandDesk';
import { DiagnosticsFddConsole } from '@/features/diagnostics/DiagnosticsFddConsole';
import { WorkOrders } from '@/features/work-orders/WorkOrders';
import { VerificationsWorkspace } from '@/features/verifications/VerificationsWorkspace';
import { EnergyAnalyticsDashboard } from '@/features/energy/EnergyAnalyticsDashboard';
import { DemandAnalyticsDashboard } from '@/features/demand/DemandAnalyticsDashboard';
import { EfficiencyAnalyticsDashboard } from '@/features/efficiency/EfficiencyAnalyticsDashboard';
import { BillingCostDashboard } from '@/features/billing/BillingCostDashboard';
import { CarbonEmissionsDashboard } from '@/features/carbon/CarbonEmissionsDashboard';
import { DistributedEnergyWorkspace } from '@/features/der/DistributedEnergyWorkspace';
import { OpportunitiesWorkspace } from '@/features/opportunities/OpportunitiesWorkspace';
import { OptimizationPlansConsole } from '@/features/optimization/OptimizationPlansConsole';
import { MeasurementVerificationWorkspace } from '@/features/mv/MeasurementVerificationWorkspace';
import { ControlOptimizationConsole } from '@/features/control/ControlOptimizationConsole';
import { StrategiesConsole } from '@/features/strategies/StrategiesConsole';
import { ExecutionsLedger } from '@/features/executions/ExecutionsLedger';
import { DataQualityWorkspace } from '@/features/data-quality/DataQualityWorkspace';
import { EnergyReviewWorkspace } from '@/features/energy-review/EnergyReviewWorkspace';
import { ActionPlansWorkspace } from '@/features/action-plans/ActionPlansWorkspace';
import { ReportsHub } from '@/features/reports/ReportsHub';
import { SiteBenchmarkingWorkspace } from '@/features/benchmarking/SiteBenchmarkingWorkspace';
import { RulesWorkspace } from '@/features/rules/RulesWorkspace';
import { PortfolioOverviewWorkspace } from '@/features/portfolio/PortfolioOverviewWorkspace';
import { WorkOrderDetailWorkspace } from '@/features/work-orders/WorkOrderDetailWorkspace';
import { StrategyDetailWorkspace } from '@/features/strategies/StrategyDetailWorkspace';
import { ManagementReviewWorkspace } from '@/features/management-reviews/ManagementReviewWorkspace';
import { MeteringSemanticModelWorkspace } from '@/features/model/MeteringSemanticModelWorkspace';
import { IntegrationsWorkspace } from '@/features/integrations/IntegrationsWorkspace';
import { SiteSystemSettingsWorkspace } from '@/features/system-settings/SiteSystemSettingsWorkspace';
import { AccessControlAuditWorkspace } from '@/features/access-control/AccessControlAuditWorkspace';
import { cn } from '@/lib/utils';
import '@/global.css';

const WIREFRAME_REVISION = 'shadcn-energy-visual-spec-v16';
document.documentElement.dataset.wireframeRevision = WIREFRAME_REVISION;

class SilentEventSource {
  onerror: ((event: Event) => void) | null = null;
  addEventListener() {}
  close() {}
}

Object.defineProperty(globalThis, 'EventSource', {
  configurable: true,
  value: SilentEventSource,
});

const tenantId = '01940000-0000-7000-8000-000000000001';
const site: Site = {
  id: '01940000-0001-7000-8000-000000000001',
  tenantId,
  code: 'TOKYO-CERT',
  displayName: '东京中央冷站',
  timezone: 'Asia/Tokyo',
  status: 'ACTIVE',
  revision: 20,
  createdAt: '2026-08-01T00:00:00.000Z',
  updatedAt: '2026-08-01T00:00:00.000Z',
};

const principal = {
  principal: {
    subject: 'overview-review-operator',
    issuer: 'https://identity.example.test',
    displayName: '林值班',
    email: '',
    roles: ['operator'],
  },
  context: {
    initiatingPrincipal: {
      subject: 'overview-review-operator',
      issuer: 'https://identity.example.test',
      displayName: '林值班',
      email: '',
      roles: ['operator'],
    },
    executingServicePrincipal: {
      service: 'platform-gateway',
      spiffeId: 'spiffe://hvac.local/platform-gateway',
    },
    tenantId,
    audience: 'iam-service',
    policyRevision: 'delegation:overview-review-policy',
    delegationExpiresAt: '2026-09-15T12:00:00.000Z',
  },
  authorization: {
    capabilitySetVersion: 12,
    policyRevision: 'overview-review-policy',
    capabilities: [
      'site.read',
      'alarm.list',
      'alarm.read',
      'work-order.list',
      'work-order.read',
    ],
  },
  session: {
    id: 'overview-review-session',
    expiresAt: '2026-09-15T12:00:00.000Z',
    revocationObjectiveMs: 1000,
    lastAuditMessageId: 'audit:overview-review',
  },
} as unknown as CurrentPrincipalResponse;

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false } },
});

const SURFACES = [
  { id: 'portfolio-overview', number: '01', label: '企业总览', title: '企业总览与跨站点治理', path: '/portfolio/overview' },
  { id: 'benchmarking', number: '02', label: '站点对标', title: '站点能效对标与组合排名', path: `/sites/${site.id}/benchmarking` },
  { id: 'overview', number: '03', label: '站点总览', title: '站点总览', path: `/sites/${site.id}/overview` },
  { id: 'operations', number: '04', label: '系统运行', title: '系统运行', path: `/sites/${site.id}/operations` },
  { id: 'trends', number: '05', label: '趋势分析', title: '趋势分析', path: `/sites/${site.id}/trends` },
  { id: 'assets', number: '06', label: '设备中心', title: '设备中心', path: `/sites/${site.id}/devices` },
  { id: 'device-detail', number: '07', label: '设备详情', title: '设备详情 (CH-01)', path: `/sites/${site.id}/devices/CH-01` },
  { id: 'comfort', number: '08', label: '舒适环境', title: '舒适与室内环境', path: `/sites/${site.id}/comfort` },
  { id: 'alarms', number: '09', label: '告警中心', title: '告警中心', path: `/sites/${site.id}/alarms` },
  { id: 'diagnostics', number: '10', label: '诊断中心', title: '诊断中心', path: `/sites/${site.id}/diagnostics` },
  { id: 'work-orders', number: '11', label: '工单中心', title: '工单中心', path: `/sites/${site.id}/work-orders` },
  { id: 'work-order-detail', number: '12', label: '工单详情', title: '工单详情 (WO-202609-082)', path: `/sites/${site.id}/work-orders/WO-202609-082` },
  { id: 'verifications', number: '13', label: '功能验证', title: '功能验证与持续调试', path: `/sites/${site.id}/verifications` },
  { id: 'energy', number: '14', label: '能源分析', title: '能源分析', path: `/sites/${site.id}/energy` },
  { id: 'demand', number: '15', label: '需求负荷', title: '需求与负荷分析', path: `/sites/${site.id}/demand` },
  { id: 'efficiency', number: '16', label: '效率分析', title: '系统效率分析', path: `/sites/${site.id}/efficiency` },
  { id: 'energy-review', number: '17', label: '能源评审', title: '能源评审与基线体系', path: `/sites/${site.id}/energy-review` },
  { id: 'billing', number: '18', label: '账单成本', title: '账单与电价成本', path: `/sites/${site.id}/billing` },
  { id: 'carbon', number: '19', label: '碳排放', title: '碳排放与绿电核算', path: `/sites/${site.id}/carbon` },
  { id: 'der', number: '20', label: '分布式能源', title: '分布式能源与微电网', path: `/sites/${site.id}/der` },
  { id: 'opportunities', number: '21', label: '节能机会', title: '节能机会识别', path: `/sites/${site.id}/opportunities` },
  { id: 'optimize', number: '22', label: '优化方案', title: '优化方案与调度控制', path: `/sites/${site.id}/optimize` },
  { id: 'action-plans', number: '23', label: '目标计划', title: '目标与行动计划', path: `/sites/${site.id}/action-plans` },
  { id: 'mv', number: '24', label: '节能量验证', title: '节能量验证 (M&V)', path: `/sites/${site.id}/mv` },
  { id: 'control', number: '25', label: '控制中心', title: '控制中心', path: `/sites/${site.id}/control` },
  { id: 'strategies', number: '26', label: '策略中心', title: '策略中心', path: `/sites/${site.id}/strategies` },
  { id: 'strategy-detail', number: '27', label: '策略详情', title: '策略详情与仿真审批', path: `/sites/${site.id}/strategies/STRAT-CW-OPT-01` },
  { id: 'executions', number: '28', label: '执行记录', title: '执行记录与控制事实总账', path: `/sites/${site.id}/executions` },
  { id: 'reports', number: '29', label: '报告中心', title: '报告中心与合规分发', path: `/sites/${site.id}/reports` },
  { id: 'management-reviews', number: '30', label: '管理评审', title: '管理评审工作区', path: `/sites/${site.id}/management-reviews` },
  { id: 'data-quality', number: '31', label: '数据质量', title: '数据质量与遥测可观测性', path: `/sites/${site.id}/data-quality` },
  { id: 'model', number: '32', label: '计量语义', title: '计量与语义模型', path: `/sites/${site.id}/model` },
  { id: 'rules', number: '33', label: '规则通知', title: '规则与通知策略', path: `/sites/${site.id}/rules` },
  { id: 'integrations', number: '34', label: '集成管理', title: '集成管理与协议驱动', path: '/settings/integrations' },
  { id: 'sites-config', number: '35', label: '系统配置', title: '站点与系统配置', path: '/settings/sites' },
  { id: 'access', number: '36', label: '用户审计', title: '用户、权限与安全审计', path: '/settings/access' },
];

type PageId = (typeof SURFACES)[number]['id'];

function ReviewSurface() {
  const [operationsSearch, setOperationsSearch] = useState<SystemOperationsSearchState>({});
  const [currentPage, setCurrentPage] = useState<PageId>(() => {
    const pageParam = new URLSearchParams(globalThis.location.search).get('page');
    const matched = SURFACES.find((s) => s.id === pageParam);
    if (matched) return matched.id;

    // Check pathname directly
    const path = globalThis.location.pathname;
    if (path.includes('/portfolio')) return 'portfolio-overview';
    if (path.includes('/management-reviews')) return 'management-reviews';
    if (path.includes('/strategies/') || path.includes('strategy-detail')) return 'strategy-detail';
    if (path.includes('/strategies')) return 'strategies';
    if (path.includes('/work-orders/') || path.includes('work-order-detail')) return 'work-order-detail';
    if (path.includes('/work-orders')) return 'work-orders';
    if (path.includes('/verifications')) return 'verifications';
    if (path.includes('/demand')) return 'demand';
    if (path.includes('/efficiency')) return 'efficiency';
    if (path.includes('/billing') || path.includes('/cost')) return 'billing';
    if (path.includes('/carbon')) return 'carbon';
    if (path.includes('/der')) return 'der';
    if (path.includes('/opportunities')) return 'opportunities';
    if (path.includes('/optimize') || path.includes('/optimization-plans')) return 'optimize';
    if (path.includes('/mv')) return 'mv';
    if (path.includes('/executions')) return 'executions';
    if (path.includes('/control')) return 'control';
    if (path.includes('/energy')) return 'energy';
    if (path.includes('/diagnostics')) return 'diagnostics';
    if (path.includes('/alarms')) return 'alarms';
    if (path.includes('/comfort')) return 'comfort';
    if (path.includes('/devices/')) return 'device-detail';
    if (path.includes('/devices')) return 'assets';
    if (path.includes('/trends')) return 'trends';
    if (path.includes('/operations')) return 'operations';
    if (path.includes('/benchmarking')) return 'benchmarking';
    if (path.includes('/energy-review')) return 'energy-review';
    if (path.includes('/action-plans')) return 'action-plans';
    if (path.includes('/reports')) return 'reports';
    if (path.includes('/data-quality')) return 'data-quality';
    if (path.includes('/rules')) return 'rules';
    if (path.includes('/model')) return 'model';
    if (path.includes('/integrations')) return 'integrations';
    if (path.includes('/settings/sites') || path.includes('sites-config')) return 'sites-config';
    if (path.includes('/access') || path.includes('/settings/access')) return 'access';
    return 'overview';
  });

  const capabilities = useMemo(() => new Set<string>(principal.authorization.capabilities), []);
  const navigation = useMemo(() => buildAppNavigation({ siteId: site.id, capabilities }), [capabilities]);

  const activeSurface = useMemo(() => SURFACES.find((s) => s.id === currentPage) ?? SURFACES[0], [currentPage]);

  const handleNavigate = (targetPath: string) => {
    let next: PageId = 'overview';
    if (targetPath.includes('/portfolio')) next = 'portfolio-overview';
    else if (targetPath.includes('/management-reviews')) next = 'management-reviews';
    else if (targetPath.includes('/strategies/') || targetPath.includes('strategy-detail')) next = 'strategy-detail';
    else if (targetPath.includes('/strategies')) next = 'strategies';
    else if (targetPath.includes('/work-orders/') || targetPath.includes('work-order-detail')) next = 'work-order-detail';
    else if (targetPath.includes('/work-orders')) next = 'work-orders';
    else if (targetPath.includes('/verifications')) next = 'verifications';
    else if (targetPath.includes('/demand')) next = 'demand';
    else if (targetPath.includes('/efficiency')) next = 'efficiency';
    else if (targetPath.includes('/billing') || targetPath.includes('/cost')) next = 'billing';
    else if (targetPath.includes('/carbon')) next = 'carbon';
    else if (targetPath.includes('/der')) next = 'der';
    else if (targetPath.includes('/opportunities')) next = 'opportunities';
    else if (targetPath.includes('/optimize') || targetPath.includes('/optimization-plans')) next = 'optimize';
    else if (targetPath.includes('/mv')) next = 'mv';
    else if (targetPath.includes('/executions')) next = 'executions';
    else if (targetPath.includes('/control')) next = 'control';
    else if (targetPath.includes('/operations')) next = 'operations';
    else if (targetPath.includes('/trends')) next = 'trends';
    else if (targetPath.includes('/devices/') || targetPath.includes('device-detail')) next = 'device-detail';
    else if (targetPath.includes('/devices')) next = 'assets';
    else if (targetPath.includes('/comfort')) next = 'comfort';
    else if (targetPath.includes('/alarms')) next = 'alarms';
    else if (targetPath.includes('/diagnostics')) next = 'diagnostics';
    else if (targetPath.includes('/energy')) next = 'energy';
    else if (targetPath.includes('/benchmarking')) next = 'benchmarking';
    else if (targetPath.includes('/energy-review')) next = 'energy-review';
    else if (targetPath.includes('/action-plans')) next = 'action-plans';
    else if (targetPath.includes('/reports')) next = 'reports';
    else if (targetPath.includes('/data-quality')) next = 'data-quality';
    else if (targetPath.includes('/rules')) next = 'rules';
    else if (targetPath.includes('/model')) next = 'model';
    else if (targetPath.includes('/integrations')) next = 'integrations';
    else if (targetPath.includes('/settings/sites') || targetPath.includes('sites-config')) next = 'sites-config';
    else if (targetPath.includes('/access') || targetPath.includes('/settings/access')) next = 'access';
    else if (targetPath.includes('/overview')) next = 'overview';

    setCurrentPage(next);
    const url = new URL(window.location.href);
    url.searchParams.set('page', next);
    window.history.pushState({}, '', url.toString());
  };

  useEffect(() => {
    const handlePopState = () => {
      const pageParam = new URLSearchParams(window.location.search).get('page');
      const matched = SURFACES.find((s) => s.id === pageParam);
      if (matched) setCurrentPage(matched.id);
    };
    window.addEventListener('popstate', handlePopState);
    return () => window.removeEventListener('popstate', handlePopState);
  }, []);

  return (
    <NuqsAdapter>
      <TooltipProvider>
      <SidebarProvider>
      <AppSidebar
        title="泉来禾智慧能源"
        pathname={activeSurface.path}
        groups={navigation.groups}
        siteId={site.id}
        siteLabel={site.displayName}
        siteOptions={[{ value: site.id, label: site.displayName }]}
        onSiteChange={() => undefined}
        onNavigate={handleNavigate}
      />
      <SidebarInset className="min-w-0 flex-1 overflow-x-hidden">
        <AppHeader
          pageTitle={activeSurface.title}
          scopeLabel={site.displayName}
          navigation={navigation.entries}
          principalName={principal.principal.displayName}
          principalRole="运维员"
          themeMode="light"
          submittingLogout={false}
          notificationCount={2}
          notificationLabel="通知"
          notificationDisabled={false}
          realtimeLabel="实时连接正常"
          realtimeState="live"
          onNavigate={handleNavigate}
          onNotificationOpen={() => undefined}
          onThemeToggle={() => undefined}
          onLogout={() => undefined}
        />

        {/* Main Workspace Display */}
        <main className="min-h-screen">
          {currentPage === 'portfolio-overview' && (
            <PortfolioOverviewWorkspace />
          )}

          {currentPage === 'overview' && (
            <Overview site={site} principal={principal} />
          )}

          {currentPage === 'operations' && (
            <SystemOperations
              site={site}
              principal={principal}
              searchState={operationsSearch}
              onSearchChange={(patch) => setOperationsSearch((current) => ({ ...current, ...patch }))}
            />
          )}

          {currentPage === 'trends' && (
            <TrendAnalysisDashboard site={site} principal={principal} />
          )}

          {currentPage === 'assets' && (
            <AssetsConsole
              site={site}
              principal={principal}
              onSelectDevice={(id) => handleNavigate(`/sites/${site.id}/devices/${id}`)}
            />
          )}

          {currentPage === 'device-detail' && (
            <DeviceDetailConsole
              site={site}
              principal={principal}
              onBack={() => handleNavigate(`/sites/${site.id}/devices`)}
            />
          )}

          {currentPage === 'comfort' && (
            <ComfortDashboard site={site} principal={principal} />
          )}

          {currentPage === 'alarms' && (
            <AlarmCommandDesk site={site} principal={principal} />
          )}

          {currentPage === 'diagnostics' && (
            <DiagnosticsFddConsole site={site} principal={principal} />
          )}

          {currentPage === 'work-orders' && (
            <WorkOrders site={site} principal={principal} registerProtectedResource={() => () => undefined} />
          )}

          {currentPage === 'work-order-detail' && (
            <WorkOrderDetailWorkspace siteId={site.id} workOrderId="WO-202609-082" />
          )}

          {currentPage === 'verifications' && (
            <VerificationsWorkspace siteId={site.id} />
          )}

          {currentPage === 'energy' && (
            <EnergyAnalyticsDashboard site={site} principal={principal} />
          )}

          {currentPage === 'demand' && (
            <DemandAnalyticsDashboard site={site} principal={principal} />
          )}

          {currentPage === 'efficiency' && (
            <EfficiencyAnalyticsDashboard site={site} principal={principal} />
          )}

          {currentPage === 'billing' && (
            <BillingCostDashboard site={site} principal={principal} />
          )}

          {currentPage === 'carbon' && (
            <CarbonEmissionsDashboard siteId={site.id} />
          )}

          {currentPage === 'der' && (
            <DistributedEnergyWorkspace siteId={site.id} />
          )}

          {currentPage === 'opportunities' && (
            <OpportunitiesWorkspace site={site} principal={principal} />
          )}

          {currentPage === 'optimize' && (
            <OptimizationPlansConsole site={site} principal={principal} />
          )}

          {currentPage === 'mv' && (
            <MeasurementVerificationWorkspace siteId={site.id} />
          )}

          {currentPage === 'control' && (
            <ControlOptimizationConsole site={site} principal={principal} />
          )}

          {currentPage === 'strategies' && (
            <StrategiesConsole siteId={site.id} />
          )}

          {currentPage === 'strategy-detail' && (
            <StrategyDetailWorkspace siteId={site.id} strategyId="STRAT-CW-OPT-01" />
          )}

          {currentPage === 'benchmarking' && (
            <SiteBenchmarkingWorkspace siteId={site.id} />
          )}

          {currentPage === 'energy-review' && (
            <EnergyReviewWorkspace siteId={site.id} />
          )}

          {currentPage === 'action-plans' && (
            <ActionPlansWorkspace siteId={site.id} />
          )}

          {currentPage === 'reports' && (
            <ReportsHub siteId={site.id} />
          )}

          {currentPage === 'management-reviews' && (
            <ManagementReviewWorkspace siteId={site.id} />
          )}

          {currentPage === 'data-quality' && (
            <DataQualityWorkspace siteId={site.id} />
          )}

          {currentPage === 'rules' && (
            <RulesWorkspace siteId={site.id} />
          )}

          {currentPage === 'model' && (
            <MeteringSemanticModelWorkspace siteId={site.id} />
          )}

          {currentPage === 'integrations' && (
            <IntegrationsWorkspace />
          )}

          {currentPage === 'sites-config' && (
            <SiteSystemSettingsWorkspace />
          )}

          {currentPage === 'access' && (
            <AccessControlAuditWorkspace />
          )}

          {currentPage === 'executions' && (
            <ExecutionsLedger siteId={site.id} />
          )}
        </main>

        {/* Floating Fast Page Switcher Dock */}
        <nav
          aria-label="快速切换页面"
          className="fixed bottom-5 left-1/2 -translate-x-1/2 z-50 flex items-center gap-1.5 rounded-full border border-border/80 bg-background/95 backdrop-blur-md px-3.5 py-1.5 shadow-lg max-w-[95vw] overflow-x-auto"
        >
          <span className="text-[11px] font-semibold text-muted-foreground mr-1 shrink-0">
            页面直达:
          </span>
          {SURFACES.map((s) => {
            const isCurrent = s.id === currentPage;
            return (
              <button
                key={s.id}
                type="button"
                onClick={() => handleNavigate(s.path)}
                className={cn(
                  'rounded-full px-2.5 py-1 text-[11px] font-medium transition-all shrink-0',
                  isCurrent
                    ? 'bg-primary text-primary-foreground shadow-xs font-semibold'
                    : 'text-muted-foreground hover:text-foreground hover:bg-muted/80',
                )}
              >
                {s.number} · {s.label}
              </button>
            );
          })}
        </nav>
      </SidebarInset>
      </SidebarProvider>
    </TooltipProvider>
    </NuqsAdapter>
  );
}

const reviewRootRoute = createRootRoute({ component: ReviewSurface });
const reviewTargets = [
  '/sites/$siteId/overview',
  '/sites/$siteId/operations',
  '/sites/$siteId/trends',
  '/sites/$siteId/devices',
  '/sites/$siteId/devices/$deviceId',
  '/sites/$siteId/comfort',
  '/sites/$siteId/alarms',
  '/sites/$siteId/diagnostics',
  '/sites/$siteId/work-orders',
  '/sites/$siteId/verifications',
  '/sites/$siteId/energy',
  '/sites/$siteId/demand',
  '/sites/$siteId/efficiency',
  '/sites/$siteId/billing',
  '/sites/$siteId/carbon',
  '/sites/$siteId/der',
  '/sites/$siteId/opportunities',
  '/sites/$siteId/optimize',
  '/sites/$siteId/optimization-plans',
  '/sites/$siteId/mv',
  '/sites/$siteId/control',
  '/sites/$siteId/strategies',
  '/sites/$siteId/executions',
  '/sites/$siteId/model',
  '/sites/$siteId/rules',
  '/sites/$siteId/data-quality',
  '/sites/$siteId/management-reviews',
  '/portfolio/overview',
  '/settings/integrations',
  '/settings/sites',
  '/settings/access',
].map((path) => createRoute({ getParentRoute: () => reviewRootRoute, path }));

const reviewRouter = createRouter({
  routeTree: reviewRootRoute.addChildren(reviewTargets),
  history: createMemoryHistory({ initialEntries: ['/'] }),
});

createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={reviewRouter} />
    </QueryClientProvider>
  </React.StrictMode>,
);
