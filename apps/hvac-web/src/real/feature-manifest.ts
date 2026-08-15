import type { RealFeatureDefinition } from './route-policy';

export const REAL_FEATURE_MANIFEST = [
  {
    id: 'site-entry',
    label: '站点入口',
    path: '/',
    delivery: 'implemented',
    availability: 'none',
    requiredCapabilities: ['site.list'],
  },
  {
    id: 'system',
    label: '系统管理',
    path: '/system',
    delivery: 'implemented',
    availability: 'platform',
    requiredCapabilities: ['site.list'],
  },
  {
    id: 'alarms',
    label: '告警',
    path: '/alarms',
    delivery: 'not-integrated',
    availability: 'none',
    requiredCapabilities: ['site.read'],
  },
  {
    id: 'work-orders',
    label: '工单',
    path: '/work-orders',
    delivery: 'not-integrated',
    availability: 'none',
    requiredCapabilities: ['site.read'],
  },
  {
    id: 'ai-investigation',
    label: 'AI 调查',
    path: '/ai-investigation',
    delivery: 'not-integrated',
    availability: 'none',
    requiredCapabilities: ['device.read'],
  },
  {
    id: 'optimization',
    label: '优化',
    path: '/optimization',
    delivery: 'hidden',
    availability: 'none',
    requiredCapabilities: ['site.read'],
  },
] as const satisfies readonly RealFeatureDefinition[];
