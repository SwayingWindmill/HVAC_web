import { BRAND, COP_GOOD, LOAD_COMFORT, STATUS, SEVERITY_LABEL, type Severity } from '@/theme/tokens';

// Mock data only. Swap these functions for real API calls (src/api) once hvac-backend is live.
// Shapes intentionally match the #4 contract so the swap is mechanical.

export interface Kpi {
  energyToday: number; // kWh
  powerNow: number; // kW
  cop: number;
  load: number; // %
  savingToday: number; // kWh
  trend: { energy: number; power: number; cop: number; load: number; saving: number }; // +/- % vs yesterday
}

export const mockKpi: Kpi = {
  energyToday: 4820,
  powerNow: 318,
  cop: 4.8,
  load: 63,
  savingToday: 412,
  trend: { energy: -3.2, power: 1.1, cop: 2.4, load: -1.8, saving: 6.5 },
};

export interface EnergyPoint { t: string; value: number }
export type Range = 'day' | 'week' | 'month';
// Hourly (day), daily (week), monthly (month) series for the energy-trend chart.
export const mockEnergy: Record<Range, EnergyPoint[]> = {
  day: Array.from({ length: 24 }, (_, h) => ({
    t: `${String(h).padStart(2, '0')}:00`,
    value: Math.round(120 + 180 * Math.sin((h - 6) / 24 * Math.PI * 2) + (h > 8 && h < 20 ? 90 : 0)),
  })),
  week: ['周一', '周二', '周三', '周四', '周五', '周六', '周日'].map((d, i) => ({
    t: d,
    value: Math.round(4200 + 700 * Math.sin(i / 2) + (i >= 5 ? -600 : 0)),
  })),
  month: Array.from({ length: 30 }, (_, d) => ({
    t: `${d + 1}`,
    value: Math.round(4000 + 600 * Math.sin(d / 4) + (d % 7 === 0 ? 400 : 0)),
  })),
};

export interface Alarm {
  id: string;
  device: string;
  text: string;
  severity: Severity;
  ts: string;
  priority?: boolean; // 优先处理
}
export const mockAlarms: Alarm[] = [
  { id: 'A-2093', device: '1F-冷水机组#1', text: '冷冻水出水温度偏离设定值 >2℃', severity: 'critical', ts: '2 分钟前', priority: true },
  { id: 'A-2091', device: 'B2-冷却泵#3', text: '运行电流超额定 95%', severity: 'major', ts: '11 分钟前', priority: true },
  { id: 'A-2088', device: '3F-AHU-07', text: '滤网压差偏高', severity: 'minor', ts: '34 分钟前' },
  { id: 'A-2085', device: '屋顶-冷却塔#2', text: '风机变频通信抖动', severity: 'info', ts: '1 小时前' },
  { id: 'A-2080', device: '1F-冷水机组#2', text: 'COP 低于健康阈值', severity: 'major', ts: '2 小时前' },
];

// Setpoint vs actual (for the detail card).
export const mockSetpoint: { label: string; set: number; actual: number; unit: string }[] = [
  { label: '冷冻水供水温度', set: 7, actual: 7.4, unit: '℃' },
  { label: '冷冻水回水温度', set: 12, actual: 12.6, unit: '℃' },
  { label: '冷却水供水温度', set: 32, actual: 33.1, unit: '℃' },
  { label: '末端供水压力', set: 0.45, actual: 0.43, unit: 'MPa' },
];

// Global device tree (buildings -> zones -> units), per #2 addendum C.
export interface TreeNode {
  key: string;
  title: string;
  children?: TreeNode[];
}
export const mockTree: TreeNode[] = [
  {
    key: 'b1',
    title: '总部大楼',
    children: [
      { key: 'b1-z1', title: 'B1 制冷机房', children: [
        { key: 'b1-z1-u1', title: '冷水机组#1' },
        { key: 'b1-z1-u2', title: '冷水机组#2' },
        { key: 'b1-z1-p3', title: '冷却泵#3' },
      ] },
      { key: 'b1-z2', title: '1F 空调区域', children: [
        { key: 'b1-z2-ahu1', title: 'AHU-01' },
        { key: 'b1-z2-ahu2', title: 'AHU-02' },
      ] },
      { key: 'b1-z3', title: '3F 空调区域', children: [
        { key: 'b1-z3-ahu7', title: 'AHU-07' },
      ] },
    ],
  },
  {
    key: 'b2',
    title: '研发中心',
    children: [
      { key: 'b2-z1', title: 'B2 制冷机房', children: [
        { key: 'b2-z1-u1', title: '冷水机组#1' },
      ] },
    ],
  },
];

// Accent helpers reused by charts so they stay on-brand (single teal accent).
export const chartAccent = BRAND.teal;
export const chartSeriesColor = (good: boolean) => (good ? STATUS.ok : STATUS.warn);
export const copGood = (cop: number) => cop >= COP_GOOD;
export const loadGood = (load: number) => load >= LOAD_COMFORT[0] && load <= LOAD_COMFORT[1];
export { SEVERITY_LABEL };

// Per-zone energy breakdown for the demo big screen (narrative: which zone uses most).
export const mockZoneEnergy: Record<string, { zone: string; kwh: number }[]> = {
  b1: [
    { zone: 'B1 制冷机房', kwh: 2140 },
    { zone: '1F 空调区域', kwh: 1180 },
    { zone: '3F 空调区域', kwh: 860 },
    { zone: '办公照明插座', kwh: 640 },
  ],
  b2: [
    { zone: 'B2 制冷机房', kwh: 980 },
    { zone: '办公区', kwh: 720 },
  ],
};

// Resolve a building/zone title from the global tree by key (for the big-screen header).
export const treeTitle = (key: string): string => {
  const walk = (nodes: TreeNode[]): string | undefined => {
    for (const n of nodes) {
      if (n.key === key) return n.title;
      if (n.children) {
        const r = walk(n.children);
        if (r) return r;
      }
    }
    return undefined;
  };
  return walk(mockTree) ?? key;
};

// Mini sparklines for the big-screen hero KPI cards.
export const mockSparkline: Record<string, number[]> = {
  energy: [120, 150, 180, 210, 260, 320, 380, 360, 300, 280, 260, 240, 260, 300, 340, 380, 420, 400, 360, 320, 280, 240, 200, 160],
  power: [210, 240, 280, 300, 320, 340, 330, 310, 300, 290, 300, 310, 320, 330, 340, 360, 350, 330, 310, 300, 290, 280, 260, 240],
  cop: [4.6, 4.7, 4.8, 4.9, 4.8, 4.7, 4.8, 4.9, 5.0, 4.9, 4.8, 4.7, 4.8, 4.9, 5.0, 4.9, 4.8, 4.7, 4.8, 4.9, 4.8, 4.7, 4.8, 4.9],
  load: [40, 45, 52, 60, 68, 75, 80, 78, 70, 65, 62, 60, 64, 70, 76, 82, 85, 80, 72, 66, 60, 54, 48, 42],
};

// Energy composition (where the energy goes) per building, for the donut.
export const mockComposition: Record<string, { name: string; kWh: number }[]> = {
  b1: [
    { name: '制冷机房', kWh: 2140 },
    { name: '空调末端', kWh: 1180 },
    { name: '照明插座', kWh: 980 },
    { name: '其他', kWh: 520 },
  ],
  b2: [
    { name: '制冷机房', kWh: 980 },
    { name: '空调末端', kWh: 720 },
    { name: '照明插座', kWh: 410 },
    { name: '其他', kWh: 220 },
  ],
};

// Annual saving target (kWh) — for the big-screen achievement progress gauge.
export const ANNUAL_SAVING_TARGET = 60000;

// ---- Commercial central-AC (中央空调) plant metrics for the dense big-screen ----
export const mockChilledWater = { supply: 7.0, return: 12.0, flow: 320, deltaT: 5.0 }; // ℃, ℃, m³/h, ℃
export const mockCoolingWater = { towerOut: 33.0, wetBulb: 28.0, pumpFreq: 42 }; // ℃, ℃, Hz
export const mockOutdoor = { temp: 34, humidity: 65, wetBulb: 28 }; // ℃, %, ℃
export const mockEquipment = {
  chillers: { run: 2, total: 3 },
  towers: { run: 2, total: 2 },
  pumps: { run: 3, total: 3 },
};
// Predicted cooling load over 24h (RT / 冷吨-ish), for the outdoor panel mini chart.
export const mockCoolingLoad: number[] = [
  120, 150, 190, 230, 280, 330, 360, 350, 300, 260, 240, 250,
  290, 330, 370, 390, 360, 310, 270, 240, 210, 180, 150, 130,
];

// ---- Optimization suggestions (#7 paradigm) ----
export type SuggestionType = 'setpoint' | 'schedule';
export type SuggestionStatus = 'draft' | 'pending' | 'approved' | 'rejected' | 'dispatched';
export interface Diff {
  param: string;
  current: number;
  proposed: number;
  unit: string;
}
export interface Suggestion {
  id: string;
  type: SuggestionType;
  title: string;
  device: string;
  diff: Diff;
  saving: { kwh: number; cny: number; co2: number }; // 模拟收益 三指标
  confidence: number; // 0..1
  status: SuggestionStatus;
  createdAt: string;
}

export const mockSuggestions: Suggestion[] = [
  { id: 'OPT-201', type: 'setpoint', title: '冷冻水供水温度下调 0.5℃', device: '1F-冷水机组#1',
    diff: { param: '冷冻水供水温度', current: 7.0, proposed: 6.5, unit: '℃' },
    saving: { kwh: 86, cny: 64, co2: 51 }, confidence: 0.92, status: 'draft', createdAt: '今天 09:12' },
  { id: 'OPT-202', type: 'setpoint', title: '冷却塔风机频率随湿球温度优化', device: '屋顶-冷却塔#2',
    diff: { param: '冷却塔出水温度', current: 33.0, proposed: 31.5, unit: '℃' },
    saving: { kwh: 142, cny: 105, co2: 84 }, confidence: 0.88, status: 'pending', createdAt: '今天 08:40' },
  { id: 'OPT-203', type: 'schedule', title: '非营业时段提前 1h 关停 AHU', device: '3F-AHU-07',
    diff: { param: '关停时刻', current: 22, proposed: 21, unit: ':00' },
    saving: { kwh: 64, cny: 47, co2: 38 }, confidence: 0.95, status: 'draft', createdAt: '今天 10:05' },
  { id: 'OPT-204', type: 'schedule', title: '冷水机组轮休避开尖峰电价', device: 'B2-冷水机组#1',
    diff: { param: '运行时段', current: 18, proposed: 16, unit: 'h/日' },
    saving: { kwh: 210, cny: 198, co2: 124 }, confidence: 0.83, status: 'approved', createdAt: '昨天 17:30' },
];

// ---- FDD entries (#7 paradigm) ----
export interface FddEntry {
  id: string;
  device: string;
  severity: Severity;
  phenomenon: string; // 故障现象
  rootCause: string; // 根因假设
  evidence: { name: string; value: string }[]; // 佐证指标
  recommended: string; // 推荐处置
  ts: string;
}
export const mockFdd: FddEntry[] = [
  { id: 'FDD-77', device: '1F-冷水机组#1', severity: 'critical',
    phenomenon: '冷冻水出水温度持续偏离设定值 >2℃', rootCause: '蒸发器结垢或制冷剂充注不足',
    evidence: [{ name: '出水温度', value: '9.6℃ (设定 7.0)' }, { name: '蒸发压力', value: '低于阈值 12%' }, { name: 'COP', value: '4.1 (健康线 4.5)' }],
    recommended: '安排蒸发器清洗并核查制冷剂充注量', ts: '3 分钟前' },
  { id: 'FDD-75', device: 'B2-冷却泵#3', severity: 'major',
    phenomenon: '运行电流超额定 95%', rootCause: '叶轮磨损或管路局部堵塞',
    evidence: [{ name: '运行电流', value: '95.2A (额定 100)' }, { name: '进出口压差', value: '偏高 8%' }],
    recommended: '检查叶轮磨损并清理过滤器', ts: '12 分钟前' },
  { id: 'FDD-71', device: '3F-AHU-07', severity: 'minor',
    phenomenon: '滤网压差偏高', rootCause: '滤网积尘',
    evidence: [{ name: '滤网压差', value: '180 Pa (告警 150)' }],
    recommended: '安排滤网更换', ts: '40 分钟前' },
];

// ---- Work orders (#7: /alarms 处置闭环) ----
export type TicketStatus = 'open' | 'assigned' | 'doing' | 'done';
export interface WorkOrder {
  id: string;
  source: 'fdd' | 'alarm';
  device: string;
  severity: Severity;
  title: string;
  description: string;
  status: TicketStatus;
  assignee?: string;
  createdAt: string;
}
export const mockWorkOrders: WorkOrder[] = [
  { id: 'WO-501', source: 'alarm', device: 'B2-冷却泵#3', severity: 'major',
    title: '冷却泵电流超额定', description: '运行电流 95.2A，需核查叶轮', status: 'open', createdAt: '12 分钟前' },
  { id: 'WO-499', source: 'fdd', device: '屋顶-冷却塔#2', severity: 'major',
    title: '冷却塔出水温度偏高', description: 'FDD-75 生成：风机频率待优化', status: 'assigned', assignee: '张工', createdAt: '20 分钟前' },
  { id: 'WO-496', source: 'alarm', device: '1F-冷水机组#2', severity: 'minor',
    title: 'COP 低于健康阈值', description: 'COP 4.3，建议检查', status: 'done', createdAt: '1 小时前' },
];
