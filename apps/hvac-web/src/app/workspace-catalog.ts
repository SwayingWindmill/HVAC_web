import type { SurfaceId, SurfaceScope } from './surface-catalog';

export type WorkspaceId =
  | 'overview'
  | 'operations'
  | 'devices'
  | 'issues'
  | 'work'
  | 'performance'
  | 'improvements'
  | 'automation'
  | 'reports'
  | 'settings';

export type SurfacePlacementKind =
  | 'workspace-view'
  | 'workspace-core'
  | 'secondary-route'
  | 'durable-detail'
  | 'contextual-capability'
  | 'settings-child'
  | 'capability-view';

export type WorkspaceInspectorMode = 'none' | 'split-inspector' | 'quick-preview';

export interface WorkspaceRoutePlan {
  readonly portfolio?: string;
  readonly site?: string;
  readonly platform?: string;
  readonly secondary?: readonly string[];
}

export interface WorkspaceViewDefinition {
  readonly id: string;
  readonly label: string;
  readonly surfaceIds: readonly SurfaceId[];
  readonly capability?: string;
}

export interface WorkspaceDefinition {
  readonly id: WorkspaceId;
  readonly label: string;
  readonly scopes: readonly SurfaceScope[];
  readonly routePlan: WorkspaceRoutePlan;
  readonly views: readonly WorkspaceViewDefinition[];
  /**
   * PROMOTED / reviewed 36-Surface pages that form the visual and interaction
   * design baseline for this consolidated workspace. These are not merely
   * capability references: the workspace must inherit their mature page
   * anatomy unless a new review explicitly adjudicates a change.
   */
  readonly designSourceSurfaceIds: readonly SurfaceId[];
  readonly inspector: WorkspaceInspectorMode;
  readonly description: string;
}

export interface SurfaceWorkspacePlacement {
  readonly surfaceId: SurfaceId;
  readonly kind: SurfacePlacementKind;
  readonly workspaceIds: readonly WorkspaceId[];
  readonly viewId?: string;
  readonly note?: string;
}

/**
 * Product-level workspace catalog.
 *
 * This is the target information architecture for the page-consolidation work.
 * It deliberately does not drive the current runtime navigation until the
 * corresponding workspace routes exist. The legacy Surface Catalog remains the
 * route matcher during migration; this catalog is the target navigation model.
 */
export const WORKSPACE_CATALOG = [
  {
    id: 'overview',
    label: '总览',
    scopes: ['portfolio', 'site'],
    routePlan: {
      portfolio: '/portfolio/overview',
      site: '/sites/:siteId/overview',
    },
    views: [
      { id: 'portfolio', label: '组合概况', surfaceIds: ['01'] },
      { id: 'benchmarking', label: '站点对标', surfaceIds: ['02'] },
      { id: 'site', label: '站点概况', surfaceIds: ['03'] },
    ],
    designSourceSurfaceIds: ['01', '02', '03'],
    inspector: 'none',
    description: '在 portfolio 或 site 范围内决定当前最值得关注的事项。',
  },
  {
    id: 'operations',
    label: '运行',
    scopes: ['site'],
    routePlan: {
      site: '/sites/:siteId/operations',
      secondary: ['/sites/:siteId/operations/trends'],
    },
    views: [
      { id: 'systems', label: '系统', surfaceIds: ['04'] },
      { id: 'comfort', label: '空间与环境', surfaceIds: ['08'], capability: 'comfort-ieq' },
    ],
    designSourceSurfaceIds: ['04', '05', '08', '25'],
    inspector: 'split-inspector',
    description: '理解 HVAC 当前如何运行，并在对象上下文中连续调查实时、历史、告警与控制事实。',
  },
  {
    id: 'devices',
    label: '设备',
    scopes: ['site'],
    routePlan: {
      site: '/sites/:siteId/devices',
      secondary: ['/sites/:siteId/devices/:deviceId'],
    },
    views: [
      { id: 'ledger', label: '设备', surfaceIds: ['06'] },
    ],
    designSourceSurfaceIds: ['06', '07'],
    inspector: 'quick-preview',
    description: '扫描、筛选和比较设备；列表保持完整宽度，Quick Preview 只承担当前对象的快速判断，持续调查进入 durable detail。',
  },
  {
    id: 'issues',
    label: '告警与诊断',
    scopes: ['site'],
    routePlan: {
      site: '/sites/:siteId/issues',
    },
    views: [
      { id: 'alarms', label: '告警', surfaceIds: ['09'] },
      { id: 'diagnostics', label: '诊断', surfaceIds: ['10'] },
    ],
    designSourceSurfaceIds: ['09', '10'],
    inspector: 'split-inspector',
    description: '从权威告警事实进入证据化诊断，同时保持 Alarm 与 Finding / Hypothesis 语义分离。',
  },
  {
    id: 'work',
    label: '工单与验证',
    scopes: ['site'],
    routePlan: {
      site: '/sites/:siteId/work',
      secondary: ['/sites/:siteId/work-orders/:workOrderId'],
    },
    views: [
      { id: 'orders', label: '工单', surfaceIds: ['11'] },
      { id: 'verification', label: '验证', surfaceIds: ['13'] },
    ],
    designSourceSurfaceIds: ['11', '12', '13'],
    inspector: 'split-inspector',
    description: '管理执行责任、SLA 和下一动作，并在修正后验证系统是否恢复预期。',
  },
  {
    id: 'performance',
    label: '能源与绩效',
    scopes: ['site'],
    routePlan: {
      site: '/sites/:siteId/performance',
    },
    views: [
      { id: 'energy', label: '用能', surfaceIds: ['14'] },
      { id: 'demand', label: '需量与负荷', surfaceIds: ['15'] },
      { id: 'efficiency', label: '系统能效', surfaceIds: ['16'] },
      { id: 'methods', label: '绩效方法', surfaceIds: ['17'] },
      { id: 'billing', label: '成本与账单', surfaceIds: ['18'], capability: 'billing-tariff' },
      { id: 'carbon', label: '碳', surfaceIds: ['19'], capability: 'carbon' },
      { id: 'flexibility', label: '灵活性', surfaceIds: ['20'], capability: 'der-flexibility' },
    ],
    designSourceSurfaceIds: ['14', '15', '16', '17', '18', '19', '20'],
    inspector: 'none',
    description: '在统一的 scope、period、comparison、baseline 和数据质量上下文中解释能源绩效。',
  },
  {
    id: 'improvements',
    label: '改进',
    scopes: ['site'],
    routePlan: {
      site: '/sites/:siteId/improvements',
      secondary: [
        '/sites/:siteId/optimization-plans/:planId',
        '/sites/:siteId/mv/:mvProjectId',
      ],
    },
    views: [
      { id: 'opportunities', label: '机会', surfaceIds: ['21'] },
      { id: 'projects', label: '改进项目', surfaceIds: ['22'] },
      { id: 'actions', label: '行动计划', surfaceIds: ['23'] },
      { id: 'mv', label: 'M&V', surfaceIds: ['24'] },
      { id: 'reviews', label: '管理评审', surfaceIds: ['30'] },
    ],
    designSourceSurfaceIds: ['21', '22', '23', '24', '30'],
    inspector: 'split-inspector',
    description: '把证据化机会转成受控改进，并持续追踪行动、验证收益和管理决策。',
  },
  {
    id: 'automation',
    label: '自动化',
    scopes: ['site'],
    routePlan: {
      site: '/sites/:siteId/automation',
      secondary: ['/sites/:siteId/strategies/:strategyId'],
    },
    views: [
      { id: 'strategies', label: '策略', surfaceIds: ['26'] },
      { id: 'executions', label: '执行记录', surfaceIds: ['28'] },
    ],
    designSourceSurfaceIds: ['25', '26', '27', '28'],
    inspector: 'split-inspector',
    description: '管理长期自动化策略及其版本、审批和不可含糊的执行事实。',
  },
  {
    id: 'reports',
    label: '报告',
    scopes: ['site'],
    routePlan: {
      site: '/sites/:siteId/reports',
    },
    views: [
      { id: 'reports', label: '报告', surfaceIds: ['29'] },
    ],
    designSourceSurfaceIds: ['29'],
    inspector: 'split-inspector',
    description: '生成、计划、分发和追溯正式业务报告。',
  },
  {
    id: 'settings',
    label: '设置',
    scopes: ['site', 'platform'],
    routePlan: {
      site: '/sites/:siteId/settings',
      platform: '/settings',
    },
    views: [
      { id: 'data-quality', label: '数据质量', surfaceIds: ['31'] },
      { id: 'model', label: '计量与语义模型', surfaceIds: ['32'] },
      { id: 'rules', label: '规则与通知', surfaceIds: ['33'] },
      { id: 'integrations', label: '集成', surfaceIds: ['34'] },
      { id: 'sites', label: '站点与系统', surfaceIds: ['35'] },
      { id: 'access', label: '用户、权限与审计', surfaceIds: ['36'] },
    ],
    designSourceSurfaceIds: ['31', '32', '33', '34', '35', '36'],
    inspector: 'split-inspector',
    description: '集中管理系统结构、数据、规则、集成和授权，不与日常运营工作台平铺。',
  },
] as const satisfies readonly WorkspaceDefinition[];

export const SURFACE_WORKSPACE_PLACEMENTS = [
  { surfaceId: '01', kind: 'workspace-view', workspaceIds: ['overview'], viewId: 'portfolio' },
  { surfaceId: '02', kind: 'workspace-view', workspaceIds: ['overview'], viewId: 'benchmarking', note: '从 site scope 修正为 portfolio benchmarking 语义。' },
  { surfaceId: '03', kind: 'workspace-view', workspaceIds: ['overview'], viewId: 'site' },
  { surfaceId: '04', kind: 'workspace-core', workspaceIds: ['operations'], viewId: 'systems' },
  { surfaceId: '05', kind: 'secondary-route', workspaceIds: ['operations'], note: 'Contextual trend 嵌入对象上下文；Advanced Trend Studio 保留可深链 secondary route。' },
  { surfaceId: '06', kind: 'workspace-core', workspaceIds: ['devices'], viewId: 'ledger' },
  { surfaceId: '07', kind: 'durable-detail', workspaceIds: ['devices'] },
  { surfaceId: '08', kind: 'capability-view', workspaceIds: ['operations'], viewId: 'comfort' },
  { surfaceId: '09', kind: 'workspace-view', workspaceIds: ['issues'], viewId: 'alarms' },
  { surfaceId: '10', kind: 'workspace-view', workspaceIds: ['issues'], viewId: 'diagnostics' },
  { surfaceId: '11', kind: 'workspace-view', workspaceIds: ['work'], viewId: 'orders' },
  { surfaceId: '12', kind: 'durable-detail', workspaceIds: ['work'] },
  { surfaceId: '13', kind: 'workspace-view', workspaceIds: ['work'], viewId: 'verification' },
  { surfaceId: '14', kind: 'workspace-view', workspaceIds: ['performance'], viewId: 'energy' },
  { surfaceId: '15', kind: 'workspace-view', workspaceIds: ['performance'], viewId: 'demand' },
  { surfaceId: '16', kind: 'workspace-view', workspaceIds: ['performance'], viewId: 'efficiency' },
  { surfaceId: '17', kind: 'workspace-view', workspaceIds: ['performance'], viewId: 'methods' },
  { surfaceId: '18', kind: 'capability-view', workspaceIds: ['performance'], viewId: 'billing' },
  { surfaceId: '19', kind: 'capability-view', workspaceIds: ['performance'], viewId: 'carbon' },
  { surfaceId: '20', kind: 'capability-view', workspaceIds: ['performance', 'automation'], viewId: 'flexibility', note: '默认归能源绩效；真实 dispatch / automated control 能力出现时可在自动化上下文提供执行入口。' },
  { surfaceId: '21', kind: 'workspace-view', workspaceIds: ['improvements'], viewId: 'opportunities' },
  { surfaceId: '22', kind: 'workspace-view', workspaceIds: ['improvements'], viewId: 'projects', note: '列表在 workspace；复杂方案保留 durable detail route。' },
  { surfaceId: '23', kind: 'workspace-view', workspaceIds: ['improvements'], viewId: 'actions' },
  { surfaceId: '24', kind: 'workspace-view', workspaceIds: ['improvements'], viewId: 'mv', note: '项目列表在 workspace；复杂 M&V 项目保留 durable detail route。' },
  { surfaceId: '25', kind: 'contextual-capability', workspaceIds: ['operations', 'devices'], note: '即时控制回到系统/设备对象上下文；不作为默认一级页面。' },
  { surfaceId: '26', kind: 'workspace-view', workspaceIds: ['automation'], viewId: 'strategies' },
  { surfaceId: '27', kind: 'durable-detail', workspaceIds: ['automation'] },
  { surfaceId: '28', kind: 'workspace-view', workspaceIds: ['automation'], viewId: 'executions' },
  { surfaceId: '29', kind: 'workspace-core', workspaceIds: ['reports'], viewId: 'reports' },
  { surfaceId: '30', kind: 'workspace-view', workspaceIds: ['improvements'], viewId: 'reviews' },
  { surfaceId: '31', kind: 'settings-child', workspaceIds: ['settings'], viewId: 'data-quality' },
  { surfaceId: '32', kind: 'settings-child', workspaceIds: ['settings'], viewId: 'model' },
  { surfaceId: '33', kind: 'settings-child', workspaceIds: ['settings'], viewId: 'rules' },
  { surfaceId: '34', kind: 'settings-child', workspaceIds: ['settings'], viewId: 'integrations' },
  { surfaceId: '35', kind: 'settings-child', workspaceIds: ['settings'], viewId: 'sites' },
  { surfaceId: '36', kind: 'settings-child', workspaceIds: ['settings'], viewId: 'access' },
] as const satisfies readonly SurfaceWorkspacePlacement[];

export const WORKSPACE_CATALOG_IS_COMPLETE:
  Exclude<WorkspaceId, (typeof WORKSPACE_CATALOG)[number]['id']> extends never ? true : never = true;

export const SURFACE_WORKSPACE_PLACEMENTS_ARE_COMPLETE:
  Exclude<SurfaceId, (typeof SURFACE_WORKSPACE_PLACEMENTS)[number]['surfaceId']> extends never ? true : never = true;

export function getWorkspace(workspaceId: WorkspaceId): WorkspaceDefinition {
  const workspace = WORKSPACE_CATALOG.find((candidate) => candidate.id === workspaceId);
  if (!workspace) throw new Error(`Unknown workspace ${workspaceId}`);
  return workspace;
}

export function getSurfaceWorkspacePlacement(surfaceId: SurfaceId): SurfaceWorkspacePlacement {
  const placement = SURFACE_WORKSPACE_PLACEMENTS.find((candidate) => candidate.surfaceId === surfaceId);
  if (!placement) throw new Error(`No workspace placement for surface ${surfaceId}`);
  return placement;
}
