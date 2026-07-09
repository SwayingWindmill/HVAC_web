import { STATUS } from '@/theme/tokens';

export type DeviceType = 'chiller' | 'pump' | 'ahu';
export type DevStatus = 'running' | 'alarm' | 'maintenance';

// Mock asset registry for building b1. Mirrors the backend `assets/tree` shape
// (building -> zone -> unit); real backend will replace this with a tree fetch.
export const DEVICE_META: Record<string, { name: string; type: DeviceType; zone: string; zoneName: string }> = {
  'b1-z1-u1': { name: '冷水机组 #1', type: 'chiller', zone: 'z1', zoneName: '冷冻站' },
  'b1-z1-u2': { name: '冷水机组 #2', type: 'chiller', zone: 'z1', zoneName: '冷冻站' },
  'b1-z1-p3': { name: '冷冻泵 #3', type: 'pump', zone: 'z1', zoneName: '冷冻站' },
  'b1-z2-ahu1': { name: '空调机组 #1', type: 'ahu', zone: 'z2', zoneName: '空调区 A' },
  'b1-z2-ahu2': { name: '空调机组 #2', type: 'ahu', zone: 'z2', zoneName: '空调区 A' },
  'b1-z3-ahu7': { name: '空调机组 #7', type: 'ahu', zone: 'z3', zoneName: '空调区 B' },
};

export const ZONE_NAMES: Record<string, string> = {
  z1: '冷冻站',
  z2: '空调区 A',
  z3: '空调区 B',
};

// v1 mock status (backend devices/state would supply this; derived here for demo).
export const STATUS_MAP: Record<string, DevStatus> = {
  'b1-z1-u1': 'running',
  'b1-z1-u2': 'running',
  'b1-z1-p3': 'running',
  'b1-z2-ahu1': 'running',
  'b1-z2-ahu2': 'maintenance',
  'b1-z3-ahu7': 'alarm',
};

export const STATUS_INFO: Record<DevStatus, { label: string; color: string }> = {
  running: { label: '运行', color: STATUS.ok },
  alarm: { label: '告警', color: STATUS.err },
  maintenance: { label: '维护', color: STATUS.warn },
};

export const TYPE_LABEL: Record<DeviceType, string> = {
  chiller: '冷水机组',
  pump: '冷冻泵',
  ahu: '空调机组',
};

export const ASSET_TREE = [
  {
    key: 'b1',
    title: '示范园区 (b1)',
    children: Object.keys(ZONE_NAMES).map((z) => ({
      key: z,
      title: ZONE_NAMES[z],
      children: Object.entries(DEVICE_META)
        .filter(([, m]) => m.zone === z)
        .map(([id, m]) => ({ key: id, title: m.name, isLeaf: true })),
    })),
  },
];
