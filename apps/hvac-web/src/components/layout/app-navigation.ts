import type { LucideIcon } from 'lucide-react';
import {
  Activity,
  Bell,
  CalendarCheck2,
  FileText,
  Gauge,
  LayoutDashboard,
  Leaf,
  Lightbulb,
  LineChart,
  Plug,
  ServerCog,
  Settings,
  ShieldAlert,
  SlidersHorizontal,
  Stethoscope,
  TrendingUp,
  Trophy,
  Workflow,
  Wrench,
  Zap,
} from 'lucide-react';

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
    items: [{ id: 'overview', title: '总览', path: '/overview', icon: LayoutDashboard }],
  },
  {
    id: 'monitoring',
    label: '运行',
    icon: Activity,
    section: 'main',
    items: [
      { id: 'operations-realtime', title: '实时运行', path: '/operations/realtime', icon: Activity },
      { id: 'operations-systems-devices', title: '设备', path: '/operations/systems-devices', icon: ServerCog },
    ],
  },
  {
    id: 'energy',
    label: '能源',
    icon: Zap,
    section: 'main',
    items: [
      { id: 'analytics-consumption', title: '能耗分析', path: '/energy-analysis/consumption', icon: LineChart },
      { id: 'analytics-breakdown', title: '分项能耗', path: '/energy-analysis/breakdown', icon: Workflow },
      { id: 'analytics-demand', title: '需量分析', path: '/energy-analysis/demand', icon: Gauge },
      { id: 'analytics-forecast', title: '负荷预测', path: '/energy-analysis/forecast', icon: TrendingUp },
      { id: 'benchmark', title: '站点对标', path: '/benchmark', icon: Trophy },
    ],
  },
  {
    id: 'savings',
    label: '节能',
    icon: Leaf,
    section: 'main',
    items: [
      { id: 'analytics-optimization', title: '优化建议', path: '/energy-analysis/optimization', icon: Lightbulb },
      { id: 'analytics-savings', title: '节能项目', path: '/energy-analysis/savings', icon: Leaf },
    ],
  },
  {
    id: 'reports-group',
    label: '报告',
    icon: FileText,
    section: 'main',
    items: [{ id: 'reports', title: '报告', path: '/reports', icon: FileText }],
  },
  {
    id: 'operations',
    label: '运维',
    icon: Wrench,
    section: 'main',
    items: [
      { id: 'operations-alarms', title: '告警', path: '/operations/alarms', icon: ShieldAlert },
      { id: 'operations-diagnostics', title: '故障诊断', path: '/operations/diagnostics', icon: Stethoscope },
      { id: 'operations-work-center', title: '工单', path: '/operations/work-center', icon: CalendarCheck2 },
    ],
  },
  {
    id: 'control-group',
    label: '控制指令',
    icon: SlidersHorizontal,
    section: 'control',
    items: [{ id: 'operations-control', title: '控制指令', path: '/operations/control', icon: SlidersHorizontal }],
  },
  {
    id: 'platform',
    label: '平台',
    icon: Settings,
    section: 'platform',
    items: [
      { id: 'integrations', title: '集成管理', path: '/settings/integrations', icon: Plug },
      { id: 'system', title: '系统管理', path: '/system', icon: Settings },
      { id: 'notifications', title: '通知中心', path: '/notifications', icon: Bell, sidebar: false },
    ],
  },
];

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
