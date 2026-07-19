import { STATUS } from '@/theme/tokens';

export type DeviceType = 'chiller' | 'pump' | 'ahu';
export type DevStatus = 'running' | 'alarm' | 'maintenance';
export type Protocol = 'BACnet/IP' | 'Modbus TCP' | 'OPC UA';

export interface DeviceAsset {
  name: string;
  type: DeviceType;
  zone: string;
  zoneName: string;
  buildingId: string;
  buildingName: string;
  floor: string;
  manufacturer: string;
  model: string;
  protocol: Protocol;
  gateway: string;
  pointCount: number;
  onlinePoints: number;
  ratedPower: number;
  ratedCooling?: number;
  maintainer: string;
  installedAt: string;
  lastSeen: string;
}

// Mock asset registry for building b1. Mirrors the backend `assets/tree` shape
// (building -> zone -> unit); real backend will replace this with a tree fetch.
export const DEVICE_META: Record<string, DeviceAsset> = {
  'b1-z1-u1': {
    name: '冷水机组 #1', type: 'chiller', zone: 'z1', zoneName: '冷冻站', buildingId: 'b1', buildingName: '总部大楼', floor: 'B1',
    manufacturer: 'YORK', model: 'YK-650RT', protocol: 'BACnet/IP', gateway: 'GW-B1-CH-01', pointCount: 86, onlinePoints: 84,
    ratedPower: 420, ratedCooling: 2286, maintainer: '张工', installedAt: '2021-05-18', lastSeen: '刚刚',
  },
  'b1-z1-u2': {
    name: '冷水机组 #2', type: 'chiller', zone: 'z1', zoneName: '冷冻站', buildingId: 'b1', buildingName: '总部大楼', floor: 'B1',
    manufacturer: 'YORK', model: 'YK-650RT', protocol: 'BACnet/IP', gateway: 'GW-B1-CH-01', pointCount: 86, onlinePoints: 82,
    ratedPower: 420, ratedCooling: 2286, maintainer: '李工', installedAt: '2021-05-18', lastSeen: '1 分钟前',
  },
  'b1-z1-p3': {
    name: '冷冻泵 #3', type: 'pump', zone: 'z1', zoneName: '冷冻站', buildingId: 'b1', buildingName: '总部大楼', floor: 'B1',
    manufacturer: 'KSB', model: 'Etaline 150-250', protocol: 'Modbus TCP', gateway: 'GW-B1-PUMP-01', pointCount: 42, onlinePoints: 42,
    ratedPower: 75, maintainer: '王工', installedAt: '2021-06-02', lastSeen: '刚刚',
  },
  'b1-z2-ahu1': {
    name: '空调机组 #1', type: 'ahu', zone: 'z2', zoneName: '空调区 A', buildingId: 'b1', buildingName: '总部大楼', floor: '1F',
    manufacturer: '同方', model: 'AHU-25000', protocol: 'BACnet/IP', gateway: 'GW-B1-AHU-01', pointCount: 58, onlinePoints: 57,
    ratedPower: 38, maintainer: '赵工', installedAt: '2022-03-10', lastSeen: '刚刚',
  },
  'b1-z2-ahu2': {
    name: '空调机组 #2', type: 'ahu', zone: 'z2', zoneName: '空调区 A', buildingId: 'b1', buildingName: '总部大楼', floor: '1F',
    manufacturer: '同方', model: 'AHU-22000', protocol: 'BACnet/IP', gateway: 'GW-B1-AHU-01', pointCount: 54, onlinePoints: 48,
    ratedPower: 32, maintainer: '赵工', installedAt: '2022-03-10', lastSeen: '18 分钟前',
  },
  'b1-z3-ahu7': {
    name: '空调机组 #7', type: 'ahu', zone: 'z3', zoneName: '空调区 B', buildingId: 'b1', buildingName: '总部大楼', floor: '3F',
    manufacturer: '麦克维尔', model: 'AHU-18000', protocol: 'Modbus TCP', gateway: 'GW-B1-AHU-02', pointCount: 51, onlinePoints: 46,
    ratedPower: 28, maintainer: '陈工', installedAt: '2020-11-22', lastSeen: '3 分钟前',
  },
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
    title: '总部大楼 (b1)',
    children: Object.keys(ZONE_NAMES).map((z) => ({
      key: z,
      title: ZONE_NAMES[z],
      children: Object.entries(DEVICE_META)
        .filter(([, m]) => m.zone === z)
        .map(([id, m]) => ({ key: id, title: m.name, isLeaf: true })),
    })),
  },
];
