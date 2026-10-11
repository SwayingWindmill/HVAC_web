import type { LucideIcon } from 'lucide-react';
import {
  Activity,
  CalendarCheck2,
  LayoutDashboard,
  LineChart,
  ServerCog,
  Settings2,
  ShieldAlert,
} from 'lucide-react';

export interface AppNavigationGroup {
  readonly id: string;
  readonly label: string;
  readonly items: readonly AppNavigationItem[];
}

export interface AppNavigationItem {
  readonly id: string;
  readonly title: string;
  readonly path: string;
  readonly icon: LucideIcon;
}

/**
 * Navigation follows the operator's loop: watch the plant, read its energy, act on what goes
 * wrong. Only surfaces backed by a real owner are listed; a page joins when its owner does.
 */
export const APP_NAVIGATION_CONFIG: readonly AppNavigationGroup[] = [
  {
    id: 'overview-group',
    label: '总览',
    items: [{ id: 'overview', title: '总览', path: '/overview', icon: LayoutDashboard }],
  },
  {
    id: 'monitoring',
    label: '监测',
    items: [
      { id: 'operations-realtime', title: '实时运行', path: '/operations/realtime', icon: Activity },
      { id: 'operations-systems-devices', title: '设备', path: '/operations/systems-devices', icon: ServerCog },
    ],
  },
  {
    id: 'energy',
    label: '能源',
    items: [{ id: 'analytics-consumption', title: '能耗分析', path: '/energy-analysis/consumption', icon: LineChart }],
  },
  {
    id: 'operations',
    label: '运维',
    items: [
      { id: 'operations-alarms', title: '告警', path: '/operations/alarms', icon: ShieldAlert },
      { id: 'operations-work-center', title: '工单', path: '/operations/work-center', icon: CalendarCheck2 },
    ],
  },
  {
    id: 'system',
    label: '系统',
    items: [{ id: 'settings', title: '设置', path: '/settings', icon: Settings2 }],
  },
];

export function isNavActive(itemPath: string, currentPath: string): boolean {
  if (itemPath === '/overview') {
    return currentPath === '/' || currentPath === '/overview' || currentPath.startsWith('/overview');
  }
  return currentPath.startsWith(itemPath);
}
