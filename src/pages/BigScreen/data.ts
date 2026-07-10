import type { ComponentType } from 'react';
import {
  CheckCircleTwoTone,
  DollarOutlined,
  ExperimentOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { ACCENT } from './theme';

export type KpiItem = {
  icon: ComponentType;
  label: string;
  value: number;
  unit: string;
  sub: string;
  color: string;
  trend?: number;
  suffix?: string;
  decimals?: number;
  money?: boolean;
  deltaVal?: number;
  deltaUnit?: string;
};

export type DeviceStat = {
  name: string;
  run: number;
  total: number;
  power: number;
  cop?: number;
  load: number;
  unit: string;
  freq?: number;
};

export type DiagnosticItem = {
  severity: 'error' | 'warn' | 'info';
  title: string;
  time: string;
  action: string;
};

export type SceneKey = 'overview' | 'energy' | 'impact';

export const KPI_DATA: KpiItem[] = [
  { icon: ThunderboltOutlined, label: '今日节电量', value: 12860, unit: 'kWh', sub: '较昨日', trend: -5.7, color: ACCENT },
  { icon: ReloadOutlined, label: '本月节能率', value: 18.6, unit: '%', sub: '较上月', trend: 2.3, suffix: '个百分点', color: ACCENT },
  { icon: DollarOutlined, label: '累计节省电费', value: 1256780, unit: '', sub: '较上月', money: true, deltaVal: 98540, deltaUnit: '', color: ACCENT },
  { icon: ExperimentOutlined, label: '冷站综合COP', value: 6.28, unit: '', sub: '较昨日', decimals: 2, trend: 0.16, color: ACCENT },
  { icon: CheckCircleTwoTone, label: '舒适度达标率', value: 98.6, unit: '%', sub: '较昨日', trend: 0.8, suffix: '个百分点', color: ACCENT },
  { icon: SafetyCertificateOutlined, label: '系统在线率', value: 99.7, unit: '%', sub: '较昨日', trend: -0.2, suffix: '百分点', color: ACCENT },
];

export const DEVICE_STATS: DeviceStat[] = [
  { name: '冷机', run: 2, total: 3, power: 320, cop: 6.35, load: 45.4, unit: 'kW' },
  { name: '冷却塔', run: 2, total: 3, power: 38, load: 10.7, unit: 'kW' },
  { name: '冷冻泵', run: 2, total: 3, power: 45.4, freq: 105, load: 201, unit: 'kW' },
  { name: '冷却泵', run: 2, total: 3, power: 30, load: 85, unit: 'kW' },
  { name: '末端', run: 48, total: 52, power: 75, load: 8.2, unit: 'kW' },
];

export const HEALTH_SCORES = [
  { label: '冷机', value: 94 },
  { label: '冷冻泵', value: 90 },
  { label: '冷却塔', value: 91 },
  { label: '冷却泵', value: 88 },
  { label: '末端', value: 93 },
];

export const DIAGNOSTICS: DiagnosticItem[] = [
  { severity: 'error', title: '冷媒液 T-3 电值偏高', time: '监测时间：09-30~08:15', action: '待处理' },
  { severity: 'warn', title: '冷冻泵 P-2 频率异常', time: '监测时间：09-20~07:42', action: '已报警' },
  { severity: 'info', title: '冷却塔 CT-1 排温值偏高', time: '监测时间：07-17~23:16', action: '已处理' },
];

export const ALARM_PROGRESS = { total: 34, resolved: 29, processing: 3, overdue: 2, pct: 85 };

export const SUGGESTIONS = [
  '降低冷冻水温度设定（建议调整至 7℃）',
  '调整冷却水泵运行数（建议减少 1 台）',
  '优化冷却塔启停策略（建议提前调整）',
];

export const SCENES: { key: SceneKey; label: string; subtitle: string }[] = [
  { key: 'overview', label: '总览', subtitle: '实时运行 · 资产健康 · 风险态势' },
  { key: 'energy', label: '能耗', subtitle: '基线对比 · 系统构成 · 峰谷优化' },
  { key: 'impact', label: '成效', subtitle: '节能收益 · 碳减排 · 闭环进度' },
];

export const COMPOSITION = [
  { name: '制冷', pct: 46.3, kwh: 118760 },
  { name: '冷冻泵', pct: 20.1, kwh: 52430 },
  { name: '冷却泵', pct: 14.6, kwh: 40140 },
  { name: '冷却塔', pct: 10.7, kwh: 27860 },
  { name: '末端', pct: 8.3, kwh: 21520 },
];
