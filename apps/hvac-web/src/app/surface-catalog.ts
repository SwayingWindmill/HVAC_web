export type SurfaceGroup =
  | 'overview'
  | 'operations'
  | 'events'
  | 'energy'
  | 'optimization'
  | 'management'
  | 'system';

export type SurfaceScope = 'portfolio' | 'site' | 'platform';
export type SurfaceNavigation = 'default' | 'config' | 'hidden';
export type SurfacePattern = 'overview' | 'ledger' | 'analysis' | 'engineering' | 'detail' | 'control' | 'governance';

export interface SurfaceDefinition {
  readonly id: string;
  readonly title: string;
  readonly navLabel: string;
  readonly group: SurfaceGroup;
  readonly scope: SurfaceScope;
  readonly route: string;
  readonly detailRoutes?: readonly string[];
  readonly navigation: SurfaceNavigation;
  readonly pattern: SurfacePattern;
}

export const SURFACE_GROUP_LABELS: Readonly<Record<SurfaceGroup, string>> = Object.freeze({
  overview: '总览',
  operations: '运行',
  events: '事件与工作',
  energy: '能源与绩效',
  optimization: '优化与控制',
  management: '管理',
  system: '系统',
});

export const SURFACE_CATALOG = [
  { id: '01', title: '企业总览', navLabel: '企业总览', group: 'overview', scope: 'portfolio', route: '/portfolio/overview', navigation: 'default', pattern: 'overview' },
  { id: '02', title: '站点对标', navLabel: '站点对标', group: 'management', scope: 'site', route: '/sites/:siteId/benchmarking', navigation: 'default', pattern: 'analysis' },
  { id: '03', title: '站点总览', navLabel: '站点总览', group: 'overview', scope: 'site', route: '/sites/:siteId/overview', navigation: 'default', pattern: 'overview' },
  { id: '04', title: '系统运行', navLabel: '系统运行', group: 'operations', scope: 'site', route: '/sites/:siteId/operations', navigation: 'default', pattern: 'engineering' },
  { id: '05', title: '趋势分析', navLabel: '趋势', group: 'operations', scope: 'site', route: '/sites/:siteId/operations/trends', navigation: 'hidden', pattern: 'analysis' },
  { id: '06', title: '设备', navLabel: '设备', group: 'operations', scope: 'site', route: '/sites/:siteId/devices', navigation: 'default', pattern: 'ledger' },
  { id: '07', title: '设备详情', navLabel: '设备详情', group: 'operations', scope: 'site', route: '/sites/:siteId/devices/:deviceId', navigation: 'hidden', pattern: 'detail' },
  { id: '08', title: '舒适与室内环境', navLabel: '舒适与室内环境', group: 'operations', scope: 'site', route: '/sites/:siteId/operations', navigation: 'hidden', pattern: 'analysis' },
  { id: '09', title: '告警', navLabel: '告警', group: 'events', scope: 'site', route: '/sites/:siteId/issues', navigation: 'default', pattern: 'ledger' },
  { id: '10', title: '诊断', navLabel: '诊断', group: 'events', scope: 'site', route: '/sites/:siteId/issues', navigation: 'hidden', pattern: 'ledger' },
  { id: '11', title: '工单', navLabel: '工单', group: 'events', scope: 'site', route: '/sites/:siteId/work-orders', navigation: 'default', pattern: 'ledger' },
  { id: '12', title: '工单详情', navLabel: '工单详情', group: 'events', scope: 'site', route: '/sites/:siteId/work-orders/:workOrderId', navigation: 'hidden', pattern: 'detail' },
  { id: '13', title: '功能验证 / 持续调试', navLabel: '功能验证', group: 'events', scope: 'site', route: '/sites/:siteId/verifications', detailRoutes: ['/sites/:siteId/verifications/:verificationId'], navigation: 'default', pattern: 'ledger' },
  { id: '14', title: '能源分析', navLabel: '能源', group: 'energy', scope: 'site', route: '/sites/:siteId/energy', navigation: 'default', pattern: 'analysis' },
  { id: '15', title: '需量与负荷', navLabel: '需量与负荷', group: 'energy', scope: 'site', route: '/sites/:siteId/demand', navigation: 'default', pattern: 'analysis' },
  { id: '16', title: '能效', navLabel: '能效', group: 'energy', scope: 'site', route: '/sites/:siteId/efficiency', navigation: 'default', pattern: 'analysis' },
  { id: '17', title: '能源评审', navLabel: '能源评审', group: 'energy', scope: 'site', route: '/sites/:siteId/energy-review', navigation: 'default', pattern: 'governance' },
  { id: '18', title: '账单、成本与电价', navLabel: '账单与成本', group: 'energy', scope: 'site', route: '/sites/:siteId/billing', navigation: 'default', pattern: 'analysis' },
  { id: '19', title: '碳排放', navLabel: '碳排放', group: 'energy', scope: 'site', route: '/sites/:siteId/carbon', navigation: 'default', pattern: 'analysis' },
  { id: '20', title: '分布式能源与柔性', navLabel: '分布式能源', group: 'energy', scope: 'site', route: '/sites/:siteId/der', navigation: 'default', pattern: 'engineering' },
  { id: '21', title: '节能机会', navLabel: '节能机会', group: 'optimization', scope: 'site', route: '/sites/:siteId/opportunities', navigation: 'default', pattern: 'ledger' },
  { id: '22', title: '优化方案', navLabel: '优化方案', group: 'optimization', scope: 'site', route: '/sites/:siteId/optimization-plans', detailRoutes: ['/sites/:siteId/optimization-plans/:planId'], navigation: 'default', pattern: 'detail' },
  { id: '23', title: '目标与行动计划', navLabel: '目标与行动计划', group: 'management', scope: 'site', route: '/sites/:siteId/action-plans', detailRoutes: ['/sites/:siteId/action-plans/:planId'], navigation: 'default', pattern: 'governance' },
  { id: '24', title: '节能量验证（M&V）', navLabel: '节能量验证', group: 'management', scope: 'site', route: '/sites/:siteId/mv', detailRoutes: ['/sites/:siteId/mv/:mvProjectId'], navigation: 'default', pattern: 'analysis' },
  { id: '25', title: '即时控制', navLabel: '即时控制', group: 'operations', scope: 'site', route: '/sites/:siteId/operations/control', navigation: 'hidden', pattern: 'control' },
  { id: '26', title: '策略', navLabel: '策略', group: 'optimization', scope: 'site', route: '/sites/:siteId/strategies', navigation: 'default', pattern: 'ledger' },
  { id: '27', title: '策略详情 / 仿真 / 审批', navLabel: '策略详情', group: 'optimization', scope: 'site', route: '/sites/:siteId/strategies/:strategyId', detailRoutes: ['/sites/:siteId/strategies/:strategyId/versions/:version'], navigation: 'hidden', pattern: 'detail' },
  { id: '28', title: '执行记录', navLabel: '执行记录', group: 'optimization', scope: 'site', route: '/sites/:siteId/executions', detailRoutes: ['/sites/:siteId/executions/:executionId'], navigation: 'default', pattern: 'ledger' },
  { id: '29', title: '报告', navLabel: '报告', group: 'management', scope: 'site', route: '/sites/:siteId/reports', detailRoutes: ['/sites/:siteId/reports/:reportId', '/sites/:siteId/report-definitions/:definitionId'], navigation: 'default', pattern: 'ledger' },
  { id: '30', title: '管理评审', navLabel: '管理评审', group: 'management', scope: 'site', route: '/sites/:siteId/management-reviews', detailRoutes: ['/sites/:siteId/management-reviews/:reviewId'], navigation: 'default', pattern: 'governance' },
  { id: '31', title: '数据质量', navLabel: '数据质量', group: 'system', scope: 'site', route: '/sites/:siteId/data-quality', detailRoutes: ['/sites/:siteId/data-quality/issues/:issueId'], navigation: 'default', pattern: 'ledger' },
  { id: '32', title: '计量与语义模型', navLabel: '计量与语义模型', group: 'system', scope: 'site', route: '/sites/:siteId/model', detailRoutes: ['/sites/:siteId/model/:entityId'], navigation: 'default', pattern: 'governance' },
  { id: '33', title: '规则与通知', navLabel: '规则与通知', group: 'system', scope: 'site', route: '/sites/:siteId/rules', detailRoutes: ['/sites/:siteId/rules/:ruleId'], navigation: 'default', pattern: 'governance' },
  { id: '34', title: '集成管理', navLabel: '集成管理', group: 'system', scope: 'platform', route: '/settings/integrations', detailRoutes: ['/settings/integrations/:integrationId'], navigation: 'default', pattern: 'governance' },
  { id: '35', title: '站点与系统配置', navLabel: '系统配置', group: 'system', scope: 'platform', route: '/settings/sites', detailRoutes: ['/settings/sites/:siteId'], navigation: 'default', pattern: 'governance' },
  { id: '36', title: '用户、权限与审计', navLabel: '用户与权限', group: 'system', scope: 'platform', route: '/settings/access', detailRoutes: ['/settings/access/principals/:principalId', '/settings/access/roles/:roleId', '/settings/access/audit/:auditEventId'], navigation: 'default', pattern: 'governance' },
] as const satisfies readonly SurfaceDefinition[];

export type SurfaceId = (typeof SURFACE_CATALOG)[number]['id'];

export const WIREFRAME_READY_SURFACE_IDS: ReadonlySet<SurfaceId> = new Set<SurfaceId>([
  '01', '02', '03', '04', '05', '06', '07', '08', '09', '10', '11', '12', '13',
  '14', '15', '16', '17', '18', '19', '20', '21', '22', '23', '24',
  '25', '26', '27', '28', '29', '30', '31', '32', '33', '34', '35', '36'
]);

export function getSurface(surfaceId: SurfaceId): (typeof SURFACE_CATALOG)[number] {
  const surface = SURFACE_CATALOG.find((candidate) => candidate.id === surfaceId);
  if (!surface) throw new Error(`Unknown surface ${surfaceId}`);
  return surface;
}

function fillRoute(template: string, params: Readonly<Record<string, string>>): string {
  return template.replace(/:([A-Za-z][A-Za-z0-9]*)/g, (_, key: string) => {
    const value = params[key];
    if (!value) throw new Error(`Missing route parameter: ${key}`);
    return encodeURIComponent(value);
  });
}

export function surfacePath(surfaceId: SurfaceId, params: Readonly<Record<string, string>> = {}): string {
  return fillRoute(getSurface(surfaceId).route, params);
}

function routeRegex(template: string): RegExp {
  const pattern = template
    .split('/')
    .map((segment) => segment.startsWith(':') ? '[^/]+' : segment.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
    .join('/');
  return new RegExp(`^${pattern}/?$`);
}

const SURFACE_ROUTE_MATCHERS = SURFACE_CATALOG
  .flatMap((surface) => [surface.route, ...('detailRoutes' in surface ? surface.detailRoutes : [])].map((route) => ({ surface, route })))
  .sort((left, right) => right.route.length - left.route.length)
  .map(({ surface, route }) => ({ surface, regex: routeRegex(route) }));

export function matchSurface(pathname: string): (typeof SURFACE_CATALOG)[number] | undefined {
  return SURFACE_ROUTE_MATCHERS.find(({ regex }) => regex.test(pathname))?.surface;
}
