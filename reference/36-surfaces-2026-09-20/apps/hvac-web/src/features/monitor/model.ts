import type { Device } from '@/api/generated/platformGateway.gen';
import type { Alarm } from '@/api/alarms';
import type { AssetsDeviceOperationalProjection, AssetsPointView } from '@/features/assets/operational-projection';

export type MonitorOverviewView = 'topology' | 'anomaly' | 'energy';
export type MonitorPage = 'overview' | 'plant' | 'terminal' | 'analysis' | 'modes';
export type MonitorEnergyView = 'cooling' | 'power' | 'hydraulic';
export type MonitorDeviceKind = 'chiller' | 'chilled-pump' | 'cooling-pump' | 'tower' | 'terminal' | 'other';

export interface MonitorSearchState {
  readonly page?: MonitorPage;
  readonly view?: MonitorOverviewView;
  readonly flow?: MonitorEnergyView;
  readonly device?: string;
  readonly analysisDevice?: string;
  readonly alarm?: string;
  readonly building?: string;
  readonly floor?: string;
  readonly zone?: string;
  readonly opportunity?: string;
}

export interface MonitorDevice {
  readonly device: Device;
  readonly kind: MonitorDeviceKind;
  readonly state: AssetsDeviceOperationalProjection;
  readonly activeAlarms: readonly Alarm[];
}

export const alarmSeverityLabel: Readonly<Record<Alarm['currentSeverity'], string>> = Object.freeze({
  CRITICAL: '紧急',
  MAJOR: '重要',
  MINOR: '一般',
  WARNING: '警告',
  INFO: '提示',
});

export function classifyMonitorDevice(device: Device): MonitorDeviceKind {
  const text = `${device.code} ${device.displayName} ${device.deviceType}`.toUpperCase();
  if (/(AHU|FCU|VAV|AIR[_ -]?HANDLING|FAN[_ -]?COIL|末端|空调箱)/.test(text)) return 'terminal';
  if (/(CHWP|CHILLED[_ -]?WATER[_ -]?PUMP|冷冻水泵|冷冻泵)/.test(text)) return 'chilled-pump';
  if (/(CWP|CONDENSER[_ -]?WATER[_ -]?PUMP|COOLING[_ -]?WATER[_ -]?PUMP|冷却水泵|冷却泵)/.test(text)) return 'cooling-pump';
  if (/(COOLING[_ -]?TOWER|\bCT[-_ ]?\d|冷却塔)/.test(text)) return 'tower';
  if (/(CHILLER|\bCH[-_ ]?\d|冷水机组|冷机)/.test(text)) return 'chiller';
  return 'other';
}

export function monitorDeviceKindLabel(kind: MonitorDeviceKind): string {
  return ({
    chiller: '冷水机组',
    'chilled-pump': '冷冻水泵',
    'cooling-pump': '冷却水泵',
    tower: '冷却塔',
    terminal: '空调末端',
    other: '其他设备',
  } as const)[kind];
}

export function deviceStateLabel(state: AssetsDeviceOperationalProjection): string {
  switch (state.connection.state) {
    case 'ONLINE': return '在线';
    case 'OFFLINE': return '离线';
    case 'UNKNOWN': return '状态待确认';
    case 'NOT_APPLICABLE': return '无需连接判断';
    case 'UNAVAILABLE': return '状态暂不可用';
  }
}

export function deviceStateBadgeClass(state: AssetsDeviceOperationalProjection): string {
  if (state.connection.state === 'ONLINE') return 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300';
  if (state.connection.state === 'OFFLINE') return 'border-destructive/30 bg-destructive/10 text-destructive';
  return 'border-border bg-muted/60 text-muted-foreground';
}

export const alarmSeverityBadgeClass: Readonly<Record<Alarm['currentSeverity'], string>> = Object.freeze({
  CRITICAL: 'border-destructive/30 bg-destructive/10 text-destructive',
  MAJOR: 'border-red-300 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300',
  MINOR: 'border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300',
  WARNING: 'border-orange-300 bg-orange-50 text-orange-800 dark:border-orange-900 dark:bg-orange-950/40 dark:text-orange-300',
  INFO: 'border-blue-300 bg-blue-50 text-blue-700 dark:border-blue-900 dark:bg-blue-950/40 dark:text-blue-300',
});

export function formatMonitorMetric(value: number | null | undefined, unit?: string | null, digits = 1): string {
  if (value == null) return '—';
  return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: digits }).format(value)}${unit ? ` ${unit}` : ''}`;
}

export function pointMetric(points: readonly AssetsPointView[], keywords: readonly string[]): string {
  const point = numericPoint(points, keywords)?.point
    ?? matchPoint(points, keywords);
  if (!point) return '—';
  return `${point.displayValue}${point.unit ? ` ${point.unit}` : ''}`;
}

function matchPoint(points: readonly AssetsPointView[], keywords: readonly string[]): AssetsPointView | null {
  const normalized = keywords.map((keyword) => keyword.toLocaleLowerCase('zh-CN'));
  return points.find((candidate) => candidate.state === 'PRESENT'
    && normalized.some((keyword) => `${candidate.label} ${candidate.key}`.toLocaleLowerCase('zh-CN').includes(keyword))) ?? null;
}

export function numericPoint(
  points: readonly AssetsPointView[],
  keywords: readonly string[],
): { point: AssetsPointView; value: number } | null {
  const point = matchPoint(points, keywords);
  if (!point) return null;
  const value = Number(point.displayValue.replace(/,/g, ''));
  return Number.isFinite(value) ? { point, value } : null;
}

export function averagePointValues(
  devices: readonly MonitorDevice[],
  keywords: readonly string[],
): { value: number | null; observed: number; unit: string | null } {
  const values = devices
    .map((item) => numericPoint(item.state.points, keywords))
    .filter((item): item is NonNullable<typeof item> => item !== null);
  if (values.length === 0) return { value: null, observed: 0, unit: null };
  return {
    value: values.reduce((total, item) => total + item.value, 0) / values.length,
    observed: values.length,
    unit: values[0].point.unit,
  };
}

export function pointDifference(
  points: readonly AssetsPointView[],
  highKeywords: readonly string[],
  lowKeywords: readonly string[],
): { value: number | null; unit: string | null } {
  const high = numericPoint(points, highKeywords);
  const low = numericPoint(points, lowKeywords);
  if (!high || !low) return { value: null, unit: high?.point.unit ?? low?.point.unit ?? null };
  return { value: high.value - low.value, unit: high.point.unit ?? low.point.unit };
}

export type MonitorRunState = 'RUNNING' | 'STOPPED' | 'FAULT';

export function monitorRunState(points: readonly AssetsPointView[]): MonitorRunState | null {
  const point = points.find((candidate) => candidate.state === 'PRESENT' && candidate.key.endsWith('.run_state'));
  if (!point) return null;
  const value = point.displayValue.trim().toUpperCase();
  return value === 'RUNNING' || value === 'STOPPED' || value === 'FAULT' ? value : null;
}

export function monitorRunStateLabel(state: MonitorRunState | null): string {
  if (state === 'RUNNING') return '运行';
  if (state === 'STOPPED') return '停止';
  if (state === 'FAULT') return '故障停机';
  return '运行状态待确认';
}

export function monitorRunStateBadgeClass(state: MonitorRunState | null): string {
  if (state === 'RUNNING') return 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300';
  if (state === 'FAULT') return 'border-destructive/30 bg-destructive/10 text-destructive';
  if (state === 'STOPPED') return 'border-border bg-muted/60 text-muted-foreground';
  return 'border-border bg-muted/40 text-muted-foreground';
}

export function runningDeviceSummary(devices: readonly MonitorDevice[]): { running: number; observed: number; total: number } {
  const states = devices.map((item) => monitorRunState(item.state.points));
  return {
    running: states.filter((state) => state === 'RUNNING').length,
    observed: states.filter((state) => state !== null).length,
    total: devices.length,
  };
}

export function sumPointValues(
  devices: readonly MonitorDevice[],
  keywords: readonly string[],
): { value: number | null; observed: number; unit: string | null } {
  const normalized = keywords.map((keyword) => keyword.toLocaleLowerCase('zh-CN'));
  const matches = devices.flatMap((item) => item.state.points).filter((point) => (
    point.state === 'PRESENT'
    && normalized.some((keyword) => `${point.label} ${point.key}`.toLocaleLowerCase('zh-CN').includes(keyword))
  ));
  const numeric = matches
    .map((point) => ({ point, value: Number(point.displayValue.replace(/,/g, '')) }))
    .filter((item) => Number.isFinite(item.value));
  if (numeric.length === 0) return { value: null, observed: 0, unit: null };
  return {
    value: numeric.reduce((total, item) => total + item.value, 0),
    observed: numeric.length,
    unit: numeric[0].point.unit,
  };
}

export function highestAlarm(alarms: readonly Alarm[]): Alarm | null {
  const rank: Readonly<Record<Alarm['currentSeverity'], number>> = { CRITICAL: 5, MAJOR: 4, MINOR: 3, WARNING: 2, INFO: 1 };
  return [...alarms].sort((left, right) => rank[right.currentSeverity] - rank[left.currentSeverity])[0] ?? null;
}

export function parseMonitorPage(search: string): MonitorPage {
  const value = new URLSearchParams(search).get('page');
  return value === 'plant' || value === 'terminal' || value === 'analysis' || value === 'modes' ? value : 'overview';
}

export function parseMonitorView(search: string): MonitorOverviewView {
  const value = new URLSearchParams(search).get('view');
  return value === 'anomaly' || value === 'energy' ? value : 'topology';
}

export function parseMonitorEnergyView(search: string): MonitorEnergyView {
  const value = new URLSearchParams(search).get('flow');
  return value === 'power' || value === 'hydraulic' ? value : 'cooling';
}
