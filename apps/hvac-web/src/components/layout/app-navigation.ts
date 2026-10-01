import type { LucideIcon } from 'lucide-react';
import {
  Activity,
  Award,
  BarChart3,
  CalendarCheck2,
  FileText,
  GitFork,
  LayoutDashboard,
  Lightbulb,
  LineChart,
  ServerCog,
  Settings2,
  ShieldAlert,
  Sparkles,
  Workflow,
  Zap,
} from 'lucide-react';

export interface AppNavSubItem {
  readonly id: string;
  readonly title: string;
  readonly path: string;
}

export interface AppNavigationGroup {
  readonly id: string;
  readonly label: string;
  readonly icon?: LucideIcon;
  readonly items: readonly AppNavigationItem[];
}

export interface AppNavigationItem {
  readonly id: string;
  readonly title: string;
  readonly path: string;
  readonly icon: LucideIcon;
  readonly children?: readonly AppNavSubItem[];
}

export const APP_NAVIGATION_CONFIG: readonly AppNavigationGroup[] = [
  {
    id: 'overview-group',
    label: '总览',
    items: [
      {
        id: 'overview',
        title: '总览看板',
        path: '/overview',
        icon: LayoutDashboard,
      },
    ],
  },
  {
    id: 'energy-optimization',
    label: '节能优化',
    items: [
      {
        id: 'optimization-opportunities',
        title: '节能机会',
        path: '/optimization/opportunities',
        icon: Lightbulb,
      },
      {
        id: 'optimization-projects',
        title: '节能项目',
        path: '/optimization/projects',
        icon: Sparkles,
      },
      {
        id: 'optimization-verification',
        title: '节能验证 (M&V)',
        path: '/optimization/verification',
        icon: Award,
      },
    ],
  },
  {
    id: 'energy-analytics',
    label: '能源分析',
    items: [
      {
        id: 'analytics-consumption',
        title: '能耗与成本',
        path: '/energy-analysis/consumption',
        icon: LineChart,
      },
      {
        id: 'analytics-load-demand',
        title: '负荷与需量',
        path: '/energy-analysis/load-demand',
        icon: Zap,
      },
      {
        id: 'analytics-breakdown',
        title: '分项与能流',
        path: '/energy-analysis/breakdown',
        icon: GitFork,
      },
      {
        id: 'analytics-benchmarking',
        title: '绩效与对标',
        path: '/energy-analysis/benchmarking',
        icon: BarChart3,
      },
    ],
  },
  {
    id: 'operations-management',
    label: '运行管理',
    items: [
      {
        id: 'operations-realtime',
        title: '实时运行',
        path: '/operations/realtime',
        icon: Activity,
      },
      {
        id: 'operations-systems-devices',
        title: '系统与设备',
        path: '/operations/systems-devices',
        icon: ServerCog,
      },
      {
        id: 'operations-alarms',
        title: '异常与告警',
        path: '/operations/alarms',
        icon: ShieldAlert,
      },
      {
        id: 'operations-work-center',
        title: '工作中心',
        path: '/operations/work-center',
        icon: CalendarCheck2,
      },
      {
        id: 'operations-control',
        title: '策略与控制',
        path: '/operations/control',
        icon: Workflow,
      },
    ],
  },
  {
    id: 'reports-and-config',
    label: '报告与配置',
    items: [
      {
        id: 'reports',
        title: '报告中心',
        path: '/reports',
        icon: FileText,
      },
      {
        id: 'settings',
        title: '系统配置',
        path: '/settings',
        icon: Settings2,
      },
    ],
  },
];

export function getFlattenedNavigationItems(): readonly AppNavigationItem[] {
  return APP_NAVIGATION_CONFIG.flatMap((g) => g.items);
}

export function isNavActive(itemPath: string, currentPath: string): boolean {
  if (itemPath === '/overview') {
    return currentPath === '/' || currentPath === '/overview' || currentPath.startsWith('/overview');
  }
  return currentPath.startsWith(itemPath);
}
