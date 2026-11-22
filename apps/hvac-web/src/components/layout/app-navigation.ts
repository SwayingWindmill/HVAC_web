import type { LucideIcon } from 'lucide-react';
import {
  Activity,
  Bell,
  Box,
  CalendarCheck2,
  CalendarClock,
  FileText,
  Gauge,
  LayoutDashboard,
  Leaf,
  Lightbulb,
  LineChart,
  Plug,
  Receipt,
  ServerCog,
  Settings,
  ShieldAlert,
  SlidersHorizontal,
  Stethoscope,
  TrendingUp,
  Trophy,
  Wrench,
  Zap,
} from 'lucide-react';
import type { Capability } from '../../api/generated/platformGateway.gen';

export interface AppNavigationGroup {
  readonly id: string;
  readonly label: string;
  readonly icon: LucideIcon;
  /** Where the group sits in the sidebar: the operator's loop, the governed control desk, or platform administration. */
  readonly section: 'main' | 'control' | 'platform';
  readonly items: readonly AppNavigationItem[];
}

export interface AppNavigationItem {
  readonly id: string;
  readonly title: string;
  readonly path: string;
  readonly icon: LucideIcon;
  /** Capabilities that must all be present for the entry to appear in sidebar and command palette. */
  readonly requiredCapabilities?: readonly Capability[];
  /** At least one of these capabilities must be present when non-empty. */
  readonly anyCapabilities?: readonly Capability[];
  /** Platform routes omit `?site=` so navigating to them cleanly exits Site scope; defaults to `'site'`. */
  readonly scope?: 'site' | 'platform';
  /** Reached from elsewhere in the shell (the notification bell), so only the command palette lists it. */
  readonly sidebar?: false;
}

/**
 * Navigation follows the operator's loop: watch the plant, read its energy, save, report, act on
 * what goes wrong. Control is governed and sits apart; platform administration sits at the
 * bottom. Only surfaces backed by a real owner are listed; a page joins when its owner does.
 */
export const APP_NAVIGATION_CONFIG: readonly AppNavigationGroup[] = [
  {
    id: 'overview-group',
    label: '总览',
    icon: LayoutDashboard,
    section: 'main',
    items: [{ id: 'overview', title: '总览', path: '/overview', icon: LayoutDashboard, requiredCapabilities: ['site.read'] }],
  },
  {
    id: 'monitoring',
    label: '运行',
    icon: Activity,
    section: 'main',
    items: [
      { id: 'operations-realtime', title: '实时运行', path: '/operations/realtime', icon: Activity, requiredCapabilities: ['site.read'], anyCapabilities: ['telemetry.snapshot.read', 'telemetry.batch.read'] },
      { id: 'operations-trends', title: '历史趋势', path: '/operations/trends', icon: TrendingUp, requiredCapabilities: ['site.read', 'telemetry.history.read'] },
      { id: 'operations-schedules', title: '运行计划', path: '/operations/schedules', icon: CalendarClock, requiredCapabilities: ['site.read'] },
      { id: 'operations-systems-devices', title: '设备', path: '/operations/systems-devices', icon: ServerCog, requiredCapabilities: ['site.read', 'device.list'] },
      { id: 'operations-digital-twin', title: '数字孪生', path: '/operations/digital-twin', icon: Box, requiredCapabilities: ['site.read', 'asset.read'] },
    ],
  },
  {
    id: 'energy',
    label: '能源',
    icon: Zap,
    section: 'main',
    items: [
      { id: 'analytics-consumption', title: '能耗分析', path: '/energy-analysis/consumption', icon: LineChart, requiredCapabilities: ['site.read'] },
      { id: 'analytics-load', title: '用电负荷', path: '/energy-analysis/load', icon: Gauge, requiredCapabilities: ['site.read'] },
      { id: 'analytics-billing', title: '电费账单', path: '/energy-analysis/billing', icon: Receipt, requiredCapabilities: ['site.read'] },
      { id: 'benchmark', title: '站点对标', path: '/benchmark', icon: Trophy, scope: 'platform', requiredCapabilities: ['site.list'] },
    ],
  },
  {
    id: 'savings',
    label: '节能',
    icon: Leaf,
    section: 'main',
    items: [
      { id: 'analytics-optimization', title: '优化建议', path: '/energy-analysis/optimization', icon: Lightbulb, requiredCapabilities: ['site.read'] },
      { id: 'analytics-savings', title: '节能项目', path: '/energy-analysis/savings', icon: Leaf, requiredCapabilities: ['site.read'] },
    ],
  },
  {
    id: 'reports-group',
    label: '报告',
    icon: FileText,
    section: 'main',
    items: [{ id: 'reports', title: '报告', path: '/reports', icon: FileText, requiredCapabilities: ['site.read'] }],
  },
  {
    id: 'operations',
    label: '运维',
    icon: Wrench,
    section: 'main',
    items: [
      { id: 'operations-alarms', title: '告警', path: '/operations/alarms', icon: ShieldAlert, requiredCapabilities: ['site.read', 'alarm.list'] },
      { id: 'notifications', title: '通知', path: '/notifications', icon: Bell, sidebar: false, requiredCapabilities: ['site.read', 'alarm.list'] },
      { id: 'operations-diagnostics', title: '故障诊断', path: '/operations/diagnostics', icon: Stethoscope, requiredCapabilities: ['site.read'] },
      { id: 'operations-work-center', title: '工单', path: '/operations/work-center', icon: CalendarCheck2, requiredCapabilities: ['site.read', 'work-order.list'] },
    ],
  },
  {
    id: 'control-group',
    label: '运行控制',
    icon: SlidersHorizontal,
    section: 'control',
    items: [{ id: 'operations-control', title: '运行控制', path: '/operations/control', icon: SlidersHorizontal, requiredCapabilities: ['site.read', 'device.list'] }],
  },
  {
    id: 'platform',
    label: '平台',
    icon: Settings,
    section: 'platform',
    items: [
      { id: 'integrations', title: '集成管理', path: '/settings/integrations', icon: Plug, scope: 'platform', requiredCapabilities: ['site.list'], anyCapabilities: ['device.read', 'asset.read'] },
      { id: 'system', title: '系统管理', path: '/system', icon: Settings, scope: 'platform', requiredCapabilities: ['site.list'] },
    ],
  },
];

export function canAccessNavItem(
  item: Pick<AppNavigationItem, 'requiredCapabilities' | 'anyCapabilities'>,
  capabilities: readonly string[] | undefined,
): boolean {
  if (!capabilities) return false;
  const granted = new Set(capabilities);
  if (item.requiredCapabilities && !item.requiredCapabilities.every((capability) => granted.has(capability))) {
    return false;
  }
  if (item.anyCapabilities && item.anyCapabilities.length > 0 && !item.anyCapabilities.some((capability) => granted.has(capability))) {
    return false;
  }
  return true;
}

export function filterNavigationGroups(
  groups: readonly AppNavigationGroup[],
  capabilities: readonly string[] | undefined,
  options: { readonly sidebarOnly?: boolean } = {},
): readonly AppNavigationGroup[] {
  const sidebarOnly = options.sidebarOnly ?? false;
  const visibleGroups: AppNavigationGroup[] = [];
  for (const group of groups) {
    const visibleItems = group.items.filter(
      (item) => (!sidebarOnly || item.sidebar !== false) && canAccessNavItem(item, capabilities),
    );
    if (visibleItems.length > 0) {
      visibleGroups.push({ ...group, items: visibleItems });
    }
  }
  return visibleGroups;
}

export function navHref(
  item: Pick<AppNavigationItem, 'path' | 'scope'>,
  siteId: string | undefined,
): string {
  if (item.scope === 'platform' || !siteId) return item.path;
  return `${item.path}?site=${siteId}`;
}

export function navCountQueryFlags(
  siteId: string | undefined,
  capabilities: readonly string[] | undefined,
): { readonly alarms: boolean; readonly workOrders: boolean; readonly commands: boolean } {
  if (!siteId || !capabilities) {
    return { alarms: false, workOrders: false, commands: false };
  }
  const allowed = new Set(capabilities);
  return {
    alarms: allowed.has('alarm.list'),
    workOrders: allowed.has('work-order.list'),
    commands: allowed.has('site.read'),
  };
}

export function isNavActive(itemPath: string, currentPath: string): boolean {
  if (itemPath === '/overview') {
    return currentPath === '/' || currentPath === '/overview' || currentPath.startsWith('/overview');
  }
  return currentPath.startsWith(itemPath);
}

/** A group with a single page shows as one sidebar entry named after the group. */
export function isSinglePageGroup(group: AppNavigationGroup): boolean {
  return group.items.length === 1;
}
